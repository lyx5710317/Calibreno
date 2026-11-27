package app

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/batch"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

type batchLookup struct {
	book       *calibre.Book
	meta       calibre.Metadata
	candidates []plugin.Result
	source     *plugin.Plugin
	chosen     int
	coverStamp string
	err        error
}

type batchMetadataEdit struct {
	item       string
	title      string
	source     *plugin.Plugin
	result     plugin.Result
	coverStamp string
	appendTags string
	fields     []batchField
}

type batchField struct {
	key, before, after string
	selected           bool
}

var batchFields = []string{"title", "authors", "publisher", "language", "tags", "series", "pubdate", "isbn", "description", "cover"}

func batchFieldName(key string) string {
	switch key {
	case "title":
		return locales.CommonTitle()
	case "authors":
		return locales.CommonAuthors()
	case "publisher":
		return locales.CommonPublisher()
	case "language":
		return locales.CommonLanguage()
	case "tags":
		return locales.CommonTags()
	case "series":
		return locales.CommonSeries()
	case "pubdate":
		return locales.CommonPublished()
	case "isbn":
		return "ISBN"
	case "description":
		return locales.CommonDescription()
	default:
		return locales.FetchMetaCover()
	}
}

func batchFieldValue(b *calibre.Book, item *batch.Item, m calibre.Metadata, key string) string {
	switch key {
	case "title":
		if item.NeedTitle && m.Title == "Unknown" {
			return ""
		}
		return m.Title
	case "authors":
		if item.NeedAuthor && slices.Equal(m.Authors, []string{"Unknown"}) {
			return ""
		}
		return strings.Join(m.Authors, "\x00")
	case "publisher":
		return m.Publisher
	case "language":
		return strings.Join(m.Languages, "\x00")
	case "tags":
		return strings.Join(m.Tags, "\x00")
	case "series":
		if m.Series != "" {
			return m.Series + "\x00" + strconv.FormatFloat(m.SeriesIndex, 'f', -1, 64)
		}
	case "pubdate":
		if !m.Pubdate.IsZero() {
			return m.Pubdate.Format("2006-01-02")
		}
	case "isbn":
		return m.Identifiers["isbn"]
	case "description":
		return m.Comments
	case "cover":
		if b.HasCover {
			return "cover"
		}
	}
	return ""
}

func setBatchField(m *calibre.Metadata, key, value string) {
	switch key {
	case "title":
		m.Title = value
	case "authors":
		m.Authors, m.AuthorSort = strings.Split(value, "\x00"), ""
	case "publisher":
		m.Publisher = value
	case "language":
		m.Languages = strings.Split(value, "\x00")
	case "tags":
		m.Tags = strings.Split(value, "\x00")
	case "series":
		name, index, _ := strings.Cut(value, "\x00")
		m.Series, m.SeriesIndex = name, 1
		if v, err := strconv.ParseFloat(index, 64); err == nil && v > 0 {
			m.SeriesIndex = v
		}
	case "pubdate":
		m.Pubdate = plugin.ParseDate(value)
	case "isbn":
		if m.Identifiers == nil {
			m.Identifiers = map[string]string{}
		}
		m.Identifiers["isbn"] = value
	case "description":
		m.Comments = value
	}
}

func batchCandidateValues(r plugin.Result) map[string]string {
	values := map[string]string{
		"title": r.Title, "authors": strings.Join(r.Authors, "\x00"), "publisher": r.Publisher,
		"language": calibre.LanguageCode(r.Language), "tags": strings.Join(r.Tags, "\x00"),
		"isbn": r.Identifiers["isbn"], "description": htmlToText(r.Description), "cover": r.Cover,
	}
	if r.Series != "" {
		values["series"] = r.Series + "\x00" + strconv.FormatFloat(max(1, r.SeriesIndex), 'f', -1, 64)
	}
	if d := plugin.ParseDate(r.Pubdate); !d.IsZero() {
		values["pubdate"] = d.Format("2006-01-02")
	}
	return values
}

func makeBatchEdit(item *batch.Item, b *calibre.Book, m calibre.Metadata, values map[string]string, appendTags bool) batchMetadataEdit {
	e := batchMetadataEdit{item: item.ID, title: b.Title}
	if appendTags {
		e.appendTags = strings.TrimSpace(values["tags"])
	}
	for _, key := range batchFields {
		next := strings.TrimSpace(values[key])
		if next == "" {
			continue
		}
		before := batchFieldValue(b, item, m, key)
		if key == "tags" && e.appendTags != "" {
			next = batchAppendedTags(before, e.appendTags)
		}
		if next != before {
			e.fields = append(e.fields, batchField{key, before, next, before == "" || key == "tags" && appendTags})
		}
	}
	return e
}

func batchAppendedTags(before, added string) string {
	if before == "" {
		return added
	}
	return strings.Join(appendUniqueTags(strings.Split(before, "\x00"), strings.Split(added, "\x00")), "\x00")
}

func appendUniqueTags(existing, values []string) []string {
	out := slices.Clone(existing)
	for _, tag := range values {
		tag = strings.TrimSpace(tag)
		if tag != "" && !slices.ContainsFunc(out, func(old string) bool { return strings.EqualFold(old, tag) }) {
			out = append(out, tag)
		}
	}
	return out
}

func (a *App) openBatchMetadata() {
	if a.batch.active == nil || a.batch.work || a.batch.loading {
		return
	}
	if a.batch.selected == nil {
		a.batch.selected = map[string]bool{}
		for _, item := range a.batch.active.Items {
			if item.NeedsMetadata() {
				a.batch.selected[item.ID] = true
			}
		}
	}
	a.batch.mode = batchMetadata
}

func (a *App) batchSelection() []string {
	var ids []string
	if a.batch.active != nil {
		for _, i := range a.batch.active.Items {
			if i.State == batch.Added && a.batch.selected[i.ID] {
				ids = append(ids, i.ID)
			}
		}
	}
	return ids
}

func readBatchBook(lib *calibre.Library, item *batch.Item) (*calibre.Book, calibre.Metadata, error) {
	b, err := item.Book.Book(lib)
	if err != nil {
		return nil, calibre.Metadata{}, err
	}
	d, err := lib.Details(b)
	if err != nil {
		return nil, calibre.Metadata{}, err
	}
	return b, b.Metadata(d), nil
}

func (a *App) queryBatchMetadata(ids []string, source *plugin.Plugin, retry bool) {
	original, lib := a.batch.active, a.lib
	if original == nil || source == nil || len(ids) == 0 || !a.beginBatchWork(locales.CommonSearching()) {
		return
	}
	a.batch.source = source.Manifest.ID
	a.busyTotal = len(ids)
	a.run(func() {
		results := make(map[string]*batchLookup, len(ids))
		for idx, id := range ids {
			item := original.Item(id)
			if item == nil {
				continue
			}
			r := &batchLookup{source: source, chosen: -1}
			results[id] = r
			b, m, err := readBatchBook(lib, item)
			r.book, r.meta, r.err = b, m, err
			if err == nil {
				if b.HasCover {
					r.coverStamp, _, _ = calibre.FileFingerprint(lib.CoverPath(b))
				}
				q := plugin.Book{}
				if isbn := isbnOf(m.Identifiers["isbn"]); isbn != "" {
					q.Identifiers = map[string]string{"isbn": isbn}
				} else {
					q.Title = m.Title
					if batchFieldValue(b, item, m, "authors") != "" {
						q.Authors = slices.Clone(m.Authors)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
				r.candidates, r.err = source.Identify(ctx, q)
				cancel()
				if r.err == nil && len(r.candidates) == 1 {
					c := r.candidates[0]
					isbnMatch := q.Identifiers["isbn"] != "" && isbnOf(c.Identifiers["isbn"]) == q.Identifiers["isbn"]
					titleMatch := q.Title != "" && strings.EqualFold(strings.TrimSpace(c.Title), strings.TrimSpace(q.Title))
					authorMatch := len(q.Authors) == 0 || slices.ContainsFunc(c.Authors, func(author string) bool {
						return slices.ContainsFunc(q.Authors, func(old string) bool { return strings.EqualFold(old, author) })
					})
					if isbnMatch || titleMatch && authorMatch {
						r.chosen = 0
					}
				}
			}
			done := idx + 1
			a.post(func() { a.busyDone = done })
		}
		a.post(func() {
			a.endBatchWork()
			if a.lib == lib && a.batch.active == original {
				if !retry || a.batch.lookup == nil {
					a.batch.lookup = results
					a.batch.preview = nil
				} else {
					maps.Copy(a.batch.lookup, results)
				}
				if a.page == pageBatch && (a.batch.mode == batchMetadata || retry && a.batch.mode == batchPreview) {
					a.makeBatchPreview(ids...)
				}
			}
			a.drainImports()
		})
	})
}

func (a *App) makeBatchPreview(changed ...string) {
	// A new candidate only invalidates edits for that book. Other books may
	// have explicit approvals or fields the user deliberately unchecked.
	previous := make(map[string]batchMetadataEdit, len(a.batch.preview))
	for _, edit := range a.batch.preview {
		previous[edit.item] = edit
	}
	for _, id := range changed {
		delete(previous, id)
	}
	a.batch.preview = nil
	for _, id := range a.batchSelection() {
		r := a.batch.lookup[id]
		if r == nil || r.chosen < 0 || r.chosen >= len(r.candidates) {
			continue
		}
		if edit, ok := previous[id]; ok {
			a.batch.preview = append(a.batch.preview, edit)
			continue
		}
		c := r.candidates[r.chosen]
		e := makeBatchEdit(a.batch.active.Item(id), r.book, r.meta, batchCandidateValues(c), false)
		e.source, e.result = r.source, c
		e.coverStamp = r.coverStamp
		a.batch.preview = append(a.batch.preview, e)
	}
	a.batch.mode = batchPreview
}

func (a *App) previewBatchCommon() {
	b, lib, ids := a.batch.active, a.lib, a.batchSelection()
	if b == nil || len(ids) == 0 || !a.beginBatchWork(locales.CommonLoading()) {
		return
	}
	tags := appendUniqueTags(nil, strings.FieldsFunc(a.batch.tags, func(r rune) bool { return r == ',' || r == '，' || r == '、' }))
	values := map[string]string{"language": calibre.LanguageCode(a.batch.language), "publisher": strings.TrimSpace(a.batch.publisher), "tags": strings.Join(tags, "\x00")}
	a.run(func() {
		var preview []batchMetadataEdit
		var failed int
		for _, id := range ids {
			item := b.Item(id)
			book, m, err := readBatchBook(lib, item)
			if err != nil {
				failed++
				continue
			}
			preview = append(preview, makeBatchEdit(item, book, m, values, true))
		}
		a.post(func() {
			a.endBatchWork()
			if a.lib == lib && a.batch.active == b && a.page == pageBatch && a.batch.mode == batchCommon {
				a.batch.lookup = nil
				a.batch.mode, a.batch.preview = batchPreview, preview
				if failed > 0 {
					a.toast("%s", locales.BatchMetadataFailed(failed))
				}
			}
			a.drainImports()
		})
	})
}

func (a *App) applyBatchMetadata() {
	original, lib := a.batch.active, a.lib
	if original == nil || !a.beginBatchWork(locales.BatchApplying()) {
		return
	}
	edits := slices.Clone(a.batch.preview)
	a.run(func() {
		b, err := a.batchStore.Load(original.Library, original.ID)
		var failed, conflicts int
		var remaining []batchMetadataEdit
		if err == nil {
			for _, edit := range edits {
				item := b.Item(edit.item)
				if item == nil {
					continue
				}
				if !slices.ContainsFunc(edit.fields, func(field batchField) bool { return field.selected }) {
					if item.NeedsMetadata() && len(edit.fields) > 0 {
						remaining = append(remaining, edit)
					}
					continue
				}
				item.MetadataIntent = map[string]string{}
				for _, field := range edit.fields {
					if field.selected && field.key != "cover" {
						item.MetadataIntent[field.key] = field.after
					}
				}
				if len(item.MetadataIntent) > 0 {
					if err = a.batchStore.Save(b); err != nil {
						break
					}
				}
				wrote, changed, itemErr := applyBatchMetadataEdit(lib, item, edit)
				if itemErr != nil {
					failed++
				}
				if changed {
					conflicts++
				}
				if changed || itemErr != nil {
					if refreshed := refreshBatchEdit(lib, item, edit); len(refreshed.fields) > 0 {
						remaining = append(remaining, refreshed)
					}
				}
				if wrote || len(item.MetadataIntent) > 0 {
					item.MetadataIntent = nil
					if saveErr := a.batchStore.Save(b); saveErr != nil {
						err = saveErr
						break
					}
				}
			}
		}
		a.post(func() {
			a.showBatchWork(lib, b, err)
			if a.lib == lib && a.batch.active == b && a.page == pageBatch && a.batch.mode == batchPreview {
				if err == nil {
					a.batch.preview = remaining
					if !a.keepPendingBatchLookups(remaining) && len(remaining) == 0 {
						a.batch.mode, a.batch.selected, a.batch.lookup = batchResult, nil, nil
					}
				}
				if conflicts > 0 {
					a.toast("%s", locales.BatchConflicts(conflicts))
				}
				if failed > 0 {
					a.toast("%s", locales.BatchMetadataFailed(failed))
				}
			}
			a.drainImports()
		})
	})
}

func (a *App) keepPendingBatchLookups(remaining []batchMetadataEdit) bool {
	pending := make(map[string]bool, len(remaining))
	for _, edit := range remaining {
		pending[edit.item] = true
	}
	for id, result := range a.batch.lookup {
		if !pending[id] && (!a.batch.selected[id] || result.chosen >= 0) {
			delete(a.batch.lookup, id)
			delete(a.batch.selected, id)
		}
	}
	return len(a.batch.lookup) > 0
}

func clearBatchMetadataNeed(item *batch.Item, key string) {
	switch key {
	case "title":
		item.NeedTitle = false
	case "authors":
		item.NeedAuthor = false
	case "publisher":
		item.NeedPublisher = false
	case "language":
		item.NeedLanguage = false
	}
}

func (a *App) recoverBatchMetadata(b *batch.Batch, lib *calibre.Library) error {
	changed := false
	for i := range b.Items {
		item := &b.Items[i]
		if len(item.MetadataIntent) == 0 {
			continue
		}
		book, metadata, err := readBatchBook(lib, item)
		if err != nil {
			// It may be temporarily inaccessible. Keep the intent for later.
			continue
		}
		for key, value := range item.MetadataIntent {
			if batchFieldValue(book, item, metadata, key) == value {
				clearBatchMetadataNeed(item, key)
				item.Enriched = true
			}
		}
		item.MetadataIntent = nil
		changed = true
	}
	if changed {
		return a.batchStore.Save(b)
	}
	return nil
}

func refreshBatchEdit(lib *calibre.Library, item *batch.Item, edit batchMetadataEdit) batchMetadataEdit {
	book, m, err := readBatchBook(lib, item)
	if err != nil {
		return edit
	}
	updated := edit
	updated.fields = nil
	updated.title = book.Title
	updated.coverStamp = ""
	if book.HasCover {
		updated.coverStamp, _, _ = calibre.FileFingerprint(lib.CoverPath(book))
	}
	for _, field := range edit.fields {
		before := batchFieldValue(book, item, m, field.key)
		if field.key == "tags" && edit.appendTags != "" {
			field.after = batchAppendedTags(before, edit.appendTags)
		}
		if field.key != "cover" && before == field.after {
			continue
		}
		field.selected = field.selected && field.before == before
		if field.key == "cover" && edit.coverStamp != updated.coverStamp {
			field.selected = false
		}
		field.before = before
		updated.fields = append(updated.fields, field)
	}
	return updated
}

func applyBatchMetadataEdit(lib *calibre.Library, item *batch.Item, edit batchMetadataEdit) (wrote, conflict bool, err error) {
	b, m, err := readBatchBook(lib, item)
	if err != nil {
		return false, false, err
	}
	var cover bool
	var changed []string
	for _, f := range edit.fields {
		if !f.selected {
			continue
		}
		if batchFieldValue(b, item, m, f.key) != f.before {
			conflict = true
			continue
		}
		if f.key == "cover" {
			cover = true
		} else {
			setBatchField(&m, f.key, f.after)
			changed = append(changed, f.key)
		}
	}
	if len(changed) > 0 {
		if err := lib.UpdateMetadataChecked(b, m); err != nil {
			return false, conflict || errors.Is(err, calibre.ErrBookChanged), err
		}
		wrote = true
		for _, key := range changed {
			clearBatchMetadataNeed(item, key)
		}
	}
	if cover && edit.source != nil {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		data, coverErr := edit.source.Cover(ctx, edit.result)
		cancel()
		if coverErr == nil {
			fresh, lookupErr := item.Book.Book(lib)
			coverErr = lookupErr
			if lookupErr == nil {
				var latest string
				if fresh.HasCover {
					latest, _, _ = calibre.FileFingerprint(lib.CoverPath(fresh))
				}
				if fresh.HasCover != b.HasCover || (fresh.HasCover && latest == "") || latest != edit.coverStamp {
					conflict = true
				} else {
					coverErr = lib.SetCover(fresh.ID, data)
					wrote = wrote || coverErr == nil
				}
			}
		}
		err = coverErr
	}
	item.Enriched = item.Enriched || wrote
	return wrote, conflict, err
}

func (a *App) markBatchReviewed() {
	b, lib, ids := a.batch.active, a.lib, a.batchSelection()
	if b == nil || len(ids) == 0 || !a.beginBatchWork(locales.BatchApplying()) {
		return
	}
	a.run(func() {
		work, err := a.batchStore.Load(b.Library, b.ID)
		if err == nil {
			for _, id := range ids {
				if item := work.Item(id); item != nil {
					item.Reviewed = true
				}
			}
			err = a.batchStore.Save(work)
		}
		a.post(func() {
			a.showBatchWork(lib, work, err)
			if a.lib == lib && a.batch.active == work {
				a.batch.selected, a.batch.mode = nil, batchResult
			}
			a.drainImports()
		})
	})
}
