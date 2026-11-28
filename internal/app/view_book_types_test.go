package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
)

func TestBookTypeEditorAndHomeFilter(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := newTestApp(t)
	if len(a.bookTypes) != 18 || a.counts.Uncategorized != len(samples) {
		t.Fatalf("initial book types: %d definitions, %d uncategorized", len(a.bookTypes), a.counts.Uncategorized)
	}
	view := ui.NewTester(a.View, 1440, 900)
	view.SetLocale("zh-Hans")
	view.Frame()
	click := func(label string) {
		t.Helper()
		if err := view.Click(label); err != nil {
			t.Fatalf("click %q: %v", label, err)
		}
		view.Frame()
	}

	click("Dune")
	id := a.current().ID
	a.openEditor(a.current())
	view.Frame()
	click("小说")
	click("科幻")
	click(locales.BookTypesAddCustom())
	view.Type("小说")
	view.Frame()
	click(locales.BookTypesCreate())
	if !view.HasText(locales.BookTypesDuplicate()) {
		t.Fatal("duplicate book type name was accepted")
	}
	click(locales.BookTypesName())
	view.Key(ui.Ctrl, ui.KeyA)
	view.Type("地方志")
	view.Frame()
	click(locales.BookTypesCreate())
	shot(t, view, "book-types-editor")
	if len(a.edit.types.created) != 1 || len(a.bookTypes) != 18 {
		t.Fatal("custom type was not staged in the editor")
	}
	customID := a.edit.types.created[0].ID
	click(locales.CommonSave())
	book := a.byID[id]
	if a.editOpen || !slices.Equal(book.BookTypeIDs, []string{customID, "fiction", "scienceFiction"}) || !slices.Equal(book.Tags, samples[2].tags) {
		t.Fatalf("saved types %v, tags %v, editor open %t", book.BookTypeIDs, book.Tags, a.editOpen)
	}
	if !view.HasText("小说 · 科幻 · 地方志") || !view.HasText(locales.CommonLanguage()) {
		t.Fatal("detail types or language filter missing")
	}

	click(locales.BookTypesTitle())
	shot(t, view, "book-types-filter")
	click(locales.FiltersPickerRowLabel("科幻", 1))
	if a.query.Filter != (catalog.Filter{Kind: catalog.ByBookType, Value: "scienceFiction"}) || len(a.visible()) != 1 || !view.HasText("Dune") {
		t.Fatalf("science fiction filter %+v, %d books", a.query.Filter, len(a.visible()))
	}
	click(locales.MainSearchLabel())
	view.Type("Liu")
	view.Frame()
	if len(a.visible()) != 0 {
		t.Fatal("book type did not combine with keyword search")
	}
	click(locales.FiltersClearAll())
	click(locales.BookTypesTitle())
	view.Type("地方志")
	view.Frame()
	click(locales.FiltersPickerRowLabel("地方志", 1))
	if a.query.Filter.Value != customID || len(a.visible()) != 1 {
		t.Fatalf("custom type filter %+v, %d books", a.query.Filter, len(a.visible()))
	}

	click(locales.FiltersClearAll())
	click(locales.BookTypesTitle())
	click(locales.FiltersPickerRowLabel("未分类", len(samples)-1))
	visible := a.visible()
	if a.query.Filter != (catalog.Filter{Kind: catalog.ByBookType}) || len(visible) != len(samples)-1 || slices.ContainsFunc(visible, func(b *calibre.Book) bool { return b.ID == id }) {
		t.Fatalf("uncategorized filter %+v, %d books", a.query.Filter, len(visible))
	}

	click(locales.FiltersClearAll())
	click("Dune")
	a.openEditor(a.current())
	view.Frame()
	click("未分类")
	click(locales.CommonSave())
	if len(a.byID[id].BookTypeIDs) != 0 || len(a.bookTypes) != 19 || a.counts.Uncategorized != len(samples) {
		t.Fatal("clearing a book's types should keep its custom type available in the library")
	}
}
