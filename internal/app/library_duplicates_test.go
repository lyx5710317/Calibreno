package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
)

func TestLibraryDuplicateScanView(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	root := t.TempDir()
	library := filepath.Join(root, "Library")
	lib, err := calibre.Create(library)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	write := func(path, contents string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	add := func(title, author, format, contents string) int64 {
		t.Helper()
		id, err := lib.AddBook(calibre.NewBook{
			File:     write(filepath.Join(root, "sample."+format), contents),
			Metadata: calibre.Metadata{Title: title, Authors: []string{author}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	const alpha, bridge, gamma, unrelated = "Alpha story", "Bridge story", "Gamma story", "Unrelated story"
	alphaID := add(alpha, "Author A", "epub", "same")
	bridgeID := add(bridge, "Author B", "epub", "same")
	gammaID := add(gamma, "Author C", "pdf", "copy")
	add(unrelated, "Author D", "epub", "else")
	if err := lib.AddFormat(bridgeID, write(filepath.Join(root, "sample.pdf"), "copy")); err != nil {
		t.Fatal(err)
	}
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}

	a := New("")
	a.run = func(fn func()) { fn() }
	a.covers.thumbs = nil
	a.openLibrary(library)
	if a.lib == nil {
		t.Fatalf("library not open: %s", msgText(a.loadErr))
	}
	t.Cleanup(func() {
		a.batchStore.Close()
		a.lib.Close()
	})
	tt := ui.NewTester(a.View, 1440, 900)
	tt.SetLocale("zh-Hans")
	tt.Frame()
	clickBatch(t, tt, locales.LibraryDuplicateScan())
	result := a.act.results
	if result == nil || result.duplicates == nil || len(result.duplicates.report.Groups) != 2 || result.groupCount() != 2 {
		t.Fatalf("expected two native groups, got %+v; texts %q", result, tt.Texts())
	}
	var memberships [][]string
	for _, row := range result.rows {
		if row.book == nil {
			memberships = append(memberships, []string{})
			continue
		}
		if len(memberships) == 0 {
			t.Fatal("a duplicate book row appears before its group header")
		}
		memberships[len(memberships)-1] = append(memberships[len(memberships)-1], row.book.Title)
	}
	for _, group := range memberships {
		slices.Sort(group)
	}
	want := [][]string{{alpha, bridge}, {bridge, gamma}}
	for _, group := range want {
		if !slices.ContainsFunc(memberships, func(got []string) bool { return slices.Equal(got, group) }) {
			t.Errorf("missing rendered group %q, got %q", group, memberships)
		}
	}
	for _, title := range []string{alpha, bridge, gamma} {
		if !tt.HasText(title) {
			t.Errorf("result doesn't display %q", title)
		}
	}
	if tt.HasText(unrelated) {
		t.Error("a same-length file with different content appears in results")
	}
	shot(t, tt, "library-duplicates")

	for _, change := range []struct {
		id            int64
		before, after string
	}{{alphaID, "same", "diff"}, {gammaID, "copy", "gone"}} {
		book, err := a.lib.Book(change.id)
		if err != nil {
			t.Fatal(err)
		}
		details, err := a.lib.Details(book)
		if err != nil {
			t.Fatal(err)
		}
		if len(details.Files) != 1 {
			t.Fatalf("expected the scanned file to remain present for %q: %+v", book.Title, details.Files)
		}
		path := details.Files[0].Path
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != change.before {
			t.Fatalf("scan changed %q: contents %q, error %v", book.Title, contents, err)
		}
		write(path, change.after)
	}
	clickBatch(t, tt, locales.ActionRunAgain())
	empty := a.act.results
	if empty == nil || empty.duplicates == nil || len(empty.duplicates.report.Groups) != 0 || !tt.HasText(locales.LibraryDuplicateNone()) {
		t.Fatalf("rerun should display a native empty result, got %+v; texts %q", empty, tt.Texts())
	}
}

func TestLibraryDuplicateScanDiscardsResultsAfterLibrarySwitch(t *testing.T) {
	a := newTestApp(t)
	otherRoot := filepath.Join(t.TempDir(), "Other Library")
	other, err := calibre.Create(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	// Hold the completed scan's main-thread callbacks until the library
	// changes, as can happen when a scan and a library switch finish together.
	var callbacks []func()
	a.post = func(fn func()) { callbacks = append(callbacks, fn) }
	a.runLibraryDuplicateScan()
	if a.duplicateScan == nil || len(callbacks) == 0 {
		t.Fatal("scan did not schedule a result")
	}
	a.post = func(fn func()) { fn() }
	a.openLibrary(otherRoot)
	if a.lib == nil || a.lib.Root != otherRoot {
		t.Fatalf("did not switch libraries: %+v", a.lib)
	}
	for _, callback := range callbacks {
		callback()
	}
	if a.act.results != nil || a.duplicateScan != nil || a.busy != "" {
		t.Fatalf("previous library left results or work behind: results=%+v, scan=%v, busy=%q", a.act.results, a.duplicateScan, a.busy)
	}
}
