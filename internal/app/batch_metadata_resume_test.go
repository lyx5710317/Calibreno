package app

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/batch"
	"dalibre/internal/plugin"
)

func TestBatchMetadataKeepsDraftsAndRetriesPendingBooks(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := newTestApp(t)
	work := batch.New(a.batchKey, "Retry fixture")
	titles := []string{"Dune", "Piranesi", "Invisible Cities"}
	for _, title := range titles {
		book := a.byID[bookID(t, a, title)]
		item := batch.NewItem(title+".epub", batch.Added)
		item.Book, item.NeedPublisher = batch.Ref(book), true
		work.Items = append(work.Items, item)
	}
	if err := a.batchStore.Save(work); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	source, err := plugin.LoadBuiltin(fstest.MapFS{
		"plugin.json": {Data: []byte(`{"id":"test.batch-meta","name":"Fixture","version":"1.0.0","api":1,
			"capabilities":{"metadata":{"fields":["title","authors","publisher","description","tags"]}},
			"permissions":{"hosts":["metadata.example"],"library":"read"}}`)},
		"main.js": {Data: []byte(`export async function identify(book) {
			const response = await fetch("https://metadata.example/books?title=" + encodeURIComponent(book.title));
			if (!response.ok) throw dalibre.error("unavailable", "Catalog unavailable");
			return response.json();
		}`)},
	}, plugin.Options{Transport: fakeWeb{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("title")
		calls[title]++
		switch title {
		case "Dune":
			fmt.Fprint(w, `[{"id":"d","title":"Dune","authors":["Frank Herbert"],"publisher":"Publisher A","description":"Unused catalog description","tags":["Chosen catalog tag"]}]`)
		case "Piranesi":
			fmt.Fprint(w, `[{"id":"p","title":"Piranesi","authors":["Susanna Clarke"],"publisher":"Publisher B"},{"id":"p2","title":"Piranesi","authors":["Susanna Clarke"],"publisher":"Other edition"}]`)
		case "Invisible Cities":
			if calls[title] == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `[{"id":"i","title":"Invisible Cities","authors":["Italo Calvino"],"publisher":"Publisher C"}]`)
		default:
			fmt.Fprint(w, "[]")
		}
	})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	a.plugins = append(a.plugins, source)

	a.openBatches()
	a.openBatchMetadata()
	view := ui.NewTester(a.View, 1440, 900)
	view.SetLocale("zh-Hans")
	view.Frame()
	clickBatch(t, view, locales.BatchLookup())
	itemA, itemB, itemC := &work.Items[0], &work.Items[1], &work.Items[2]
	getDraft := func(id, key string) *batchField {
		t.Helper()
		for i := range a.batch.preview {
			edit := &a.batch.preview[i]
			if edit.item == id {
				for j := range edit.fields {
					if edit.fields[j].key == key {
						return &edit.fields[j]
					}
				}
			}
		}
		t.Fatalf("missing preview field %s", key)
		return nil
	}
	getDraft(itemA.ID, "description").selected = false
	getDraft(itemA.ID, "tags").selected = true
	view.Frame()
	assertDraft := func() {
		t.Helper()
		if getDraft(itemA.ID, "description").selected || !getDraft(itemA.ID, "tags").selected {
			t.Fatal("the other book's approvals changed")
		}
	}

	// An unselected result with available candidates may be retried in place.
	clickBatch(t, view, locales.CommonRetry())
	if calls["Piranesi"] != 2 || calls["Dune"] != 1 || calls["Invisible Cities"] != 1 {
		t.Fatalf("unexpected requests: %v", calls)
	}
	assertDraft()
	r := a.batch.lookup[itemB.ID]
	clickBatch(t, view, locales.BatchCandidateFor("Piranesi"))
	clickBatch(t, view, batchCandidateLabel(r.candidates[0]))
	if r.chosen != 0 {
		t.Fatalf("choice = %d", r.chosen)
	}
	assertDraft()
	getDraft(itemB.ID, "publisher").selected = false
	view.Frame()

	clickBatch(t, view, locales.BatchApplyMetadata())
	if a.batch.mode != batchPreview || a.batch.active.Summary().Metadata != 2 || len(a.batch.lookup) != 2 ||
		a.batch.lookup[itemB.ID] == nil || a.batch.lookup[itemC.ID] == nil {
		t.Fatalf("unresolved lookup was not retained: mode=%v summary=%+v lookups=%v", a.batch.mode, a.batch.active.Summary(), a.batch.lookup)
	}
	if getDraft(itemB.ID, "publisher").selected {
		t.Fatal("the untouched book's field was reselected after applying another book")
	}
	_, m, err := readBatchBook(a.lib, itemA)
	if err != nil || m.Publisher != "Publisher A" || m.Comments != "" || !slices.Equal(m.Tags, []string{"Chosen catalog tag"}) {
		t.Fatalf("applied draft = %+v, %v", m, err)
	}

	clickBatch(t, view, locales.CommonRetry())
	if calls["Invisible Cities"] != 2 || len(a.batch.preview) != 2 || !getDraft(itemC.ID, "publisher").selected {
		t.Fatalf("retry did not keep both remaining books: requests=%v preview=%+v", calls, a.batch.preview)
	}
	if getDraft(itemB.ID, "publisher").selected {
		t.Fatal("retry reselected the untouched book's field")
	}
	getDraft(itemB.ID, "publisher").selected = true
	view.Frame()
	clickBatch(t, view, locales.BatchApplyMetadata())
	if a.batch.mode != batchResult || a.batch.active.Summary().Enriched != 3 || a.batch.active.Summary().Metadata != 0 {
		t.Fatalf("batch did not finish: mode=%v summary=%+v", a.batch.mode, a.batch.active.Summary())
	}
}

func TestBatchMetadataRefreshMergesOnlyRequestedTags(t *testing.T) {
	a := newTestApp(t)
	item := batch.Item{Book: batch.Ref(a.byID[bookID(t, a, "Dune")]), State: batch.Added}
	book, current, err := readBatchBook(a.lib, &item)
	if err != nil {
		t.Fatal(err)
	}
	edit := makeBatchEdit(&item, book, current, map[string]string{"tags": "Imported"}, true)
	current.Tags = []string{"Added by Calibre"}
	if err := a.lib.UpdateMetadata(book.ID, current); err != nil {
		t.Fatal(err)
	}
	wrote, conflict, err := applyBatchMetadataEdit(a.lib, &item, edit)
	if wrote || !conflict || err != nil {
		t.Fatalf("stale apply = %v, %v, %v", wrote, conflict, err)
	}
	next := refreshBatchEdit(a.lib, &item, edit)
	if len(next.fields) != 1 || next.fields[0].before != "Added by Calibre" ||
		next.fields[0].after != "Added by Calibre\x00Imported" || next.fields[0].selected {
		t.Fatalf("refreshed tag edit = %+v", next.fields)
	}
	next.fields[0].selected = true
	wrote, conflict, err = applyBatchMetadataEdit(a.lib, &item, next)
	if !wrote || conflict || err != nil {
		t.Fatalf("second apply = %v, %v, %v", wrote, conflict, err)
	}
	_, current, err = readBatchBook(a.lib, &item)
	if err != nil || !slices.Equal(current.Tags, []string{"Added by Calibre", "Imported"}) {
		t.Fatalf("tag values = %v, %v", current.Tags, err)
	}
}

func TestBatchMetadataRefreshClearsRemovedCoverFingerprint(t *testing.T) {
	a := newTestApp(t)
	item := batch.Item{Book: batch.Ref(a.byID[bookID(t, a, "Dune")]), State: batch.Added}
	stale := batchMetadataEdit{
		coverStamp: "old-fingerprint",
		fields:     []batchField{{key: "cover", before: "cover", after: "https://metadata.example/cover", selected: true}},
	}
	refreshed := refreshBatchEdit(a.lib, &item, stale)
	if refreshed.coverStamp != "" || len(refreshed.fields) != 1 || refreshed.fields[0].before != "" || refreshed.fields[0].selected {
		t.Fatalf("refreshed cover edit = %+v", refreshed)
	}
}
