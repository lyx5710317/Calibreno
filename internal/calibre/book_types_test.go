package calibre

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func bookForTypeTest(t *testing.T, l *Library) *Book {
	t.Helper()
	id, err := l.AddBook(NewBook{
		File: writeTemp(t, "book.epub", "epub"),
		Metadata: Metadata{
			Title: "Original", Authors: []string{"Author"}, Tags: []string{"科幻"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return loadTypeTestBook(t, l, id)
}

func loadTypeTestBook(t *testing.T, l *Library, id int64) *Book {
	t.Helper()
	book, err := l.Book(id)
	if err != nil {
		t.Fatal(err)
	}
	return book
}

func TestBookTypesPersistWithMetadata(t *testing.T) {
	lib := newTestLibrary(t)
	old := bookForTypeTest(t, lib)
	if len(old.BookTypeIDs) != 0 {
		t.Fatalf("an ordinary tag assigned a book type: %v", old.BookTypeIDs)
	}
	presets, err := lib.BookTypes()
	if err != nil {
		t.Fatal(err)
	}
	wantPresets := []BookType{
		{ID: "fiction", Name: "小说"}, {ID: "scienceFiction", Name: "科幻"}, {ID: "mystery", Name: "推理"},
		{ID: "romance", Name: "言情"}, {ID: "literature", Name: "文学"}, {ID: "women", Name: "女性"},
		{ID: "poetry", Name: "诗"}, {ID: "drama", Name: "戏剧"},
		{ID: "socialSciencesHumanities", Name: "社科人文"}, {ID: "nonfiction", Name: "非虚构"},
		{ID: "psychology", Name: "心理学"}, {ID: "history", Name: "历史"}, {ID: "philosophy", Name: "哲学"},
		{ID: "biography", Name: "传记"}, {ID: "religionSpirituality", Name: "宗教与灵性"},
		{ID: "scienceTechnology", Name: "科学与技术"}, {ID: "personalGrowth", Name: "个人成长"},
		{ID: "comics", Name: "漫画"},
	}
	if !slices.Equal(presets, wantPresets) {
		t.Fatalf("presets = %+v", presets)
	}
	custom, err := NewCustomBookType("  创作资料  ")
	if err != nil || custom.Name != "创作资料" || !custom.Custom {
		t.Fatalf("custom = %+v, %v", custom, err)
	}
	m := old.Metadata(nil)
	m.Title = "Renamed"
	err = lib.UpdateMetadataAndBookTypes(old, m, BookTypeEdit{
		IDs: []string{"scienceFiction", custom.ID, "scienceFiction"}, Create: []BookType{custom},
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReadOnly(lib.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	types, err := reopened.BookTypes()
	if err != nil || !slices.Equal(types, append(wantPresets, custom)) {
		t.Fatalf("reopened types = %+v, %v", types, err)
	}
	books, err := reopened.Books()
	if err != nil || len(books) != 1 {
		t.Fatalf("reopened books = %v, %v", books, err)
	}
	book := books[0]
	if book.Title != m.Title || !slices.Equal(book.Tags, old.Tags) || !slices.Equal(book.BookTypeIDs, []string{custom.ID, "scienceFiction"}) {
		t.Fatalf("saved book = %+v", book)
	}
	details, err := reopened.Details(book)
	if err != nil || len(details.Files) != 1 {
		t.Fatalf("file details = %+v, %v", details, err)
	}
	if _, err := os.Stat(details.Files[0].Path); err != nil {
		t.Fatal(err)
	}
	other := newTestLibrary(t)
	if types, err := other.BookTypes(); err != nil || !slices.Equal(types, presets) {
		t.Fatalf("types in another library = %+v, %v", types, err)
	}
}

func TestBookTypeUpdatesPreserveConcurrentAdditions(t *testing.T) {
	lib := newTestLibrary(t)
	preview := bookForTypeTest(t, lib)
	other, err := Open(lib.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := lib.UpdateMetadataAddingBookTypes(preview, nil, []string{"fiction"}); err != nil {
		t.Fatal(err)
	}
	if err := other.UpdateMetadataAddingBookTypes(preview, nil, []string{"history", "fiction"}); err != nil {
		t.Fatal(err)
	}
	b := loadTypeTestBook(t, lib, preview.ID)
	if !slices.Equal(b.BookTypeIDs, []string{"fiction", "history"}) || !b.LastModified.Equal(preview.LastModified) {
		t.Fatalf("type-only merges changed the expected state: %+v", b)
	}
	if err := lib.UpdateBookTypes(preview, BookTypeEdit{IDs: []string{"mystery"}}); !errors.Is(err, ErrBookTypesChanged) {
		t.Fatalf("stale replacement = %v", err)
	}
	m := preview.Metadata(nil)
	m.Title = "Fresh title"
	if err := lib.UpdateMetadataChecked(preview, m); err != nil {
		t.Fatal(err)
	}
	b = loadTypeTestBook(t, lib, preview.ID)
	if b.Title != m.Title || !slices.Equal(b.BookTypeIDs, []string{"fiction", "history"}) {
		t.Fatalf("ordinary metadata save changed assignments: %+v", b)
	}
	if err := lib.UpdateBookTypes(b, BookTypeEdit{Before: []string{"history", "fiction"}}); err != nil {
		t.Fatal(err)
	}
	if got := loadTypeTestBook(t, lib, preview.ID); len(got.BookTypeIDs) != 0 {
		t.Fatalf("clear left categories behind: %v", got.BookTypeIDs)
	}
	if err := other.UpdateMetadataAddingBookTypes(preview, nil, []string{"mystery"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveBooks([]int64{b.ID}, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := lib.db.QueryRow("SELECT count(*) FROM books_plugin_data WHERE book = ?", b.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("book deletion left %d assignment rows, %v", n, err)
	}
	if err := other.UpdateMetadataAddingBookTypes(preview, nil, []string{"history"}); !errors.Is(err, ErrBookChanged) {
		t.Fatalf("deleted-book assignment = %v", err)
	}
}

func TestBookTypeChangesRollBackOnSaveFailure(t *testing.T) {
	lib := newTestLibrary(t)
	old := bookForTypeTest(t, lib)
	custom, err := NewCustomBookType("资料")
	if err != nil {
		t.Fatal(err)
	}
	m := old.Metadata(nil)
	m.Title = "Rejected"
	if err := lib.UpdateMetadataAndBookTypes(old, m, BookTypeEdit{
		IDs: []string{custom.ID, "unknown"}, Create: []BookType{custom},
	}); !errors.Is(err, ErrUnknownBookType) {
		t.Fatalf("unknown type = %v", err)
	}
	if _, err := lib.db.Exec("CREATE TRIGGER reject_title BEFORE UPDATE OF title ON books BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := lib.UpdateMetadataAndBookTypes(old, m, BookTypeEdit{
		IDs: []string{custom.ID}, Create: []BookType{custom},
	}); err == nil {
		t.Fatal("metadata save succeeded despite the rejecting trigger")
	}
	got := loadTypeTestBook(t, lib, old.ID)
	if got.Title != old.Title || len(got.BookTypeIDs) != 0 {
		t.Fatalf("failed save changed the book: %+v", got)
	}
	if types, err := lib.BookTypes(); err != nil || !slices.Equal(types, presetBookTypes[:]) {
		t.Fatalf("failed save persisted custom types: %+v, %v", types, err)
	}
}

func TestCustomBookTypeNames(t *testing.T) {
	lib := newTestLibrary(t)
	old := bookForTypeTest(t, lib)
	for _, name := range []string{"   ", "未分类", "UNCATEGORIZED", "control\ncharacter"} {
		if _, err := NewCustomBookType(name); !errors.Is(err, ErrInvalidBookTypeName) {
			t.Errorf("new name %q = %v", name, err)
		}
	}
	for _, names := range [][]string{{"小说"}, {"Ｒｅｆｅｒｅｎｃｅ", "reference"}} {
		edit := BookTypeEdit{}
		for _, name := range names {
			typ, err := NewCustomBookType(name)
			if err != nil {
				t.Fatal(err)
			}
			edit.Create = append(edit.Create, typ)
			edit.IDs = append(edit.IDs, typ.ID)
		}
		if err := lib.UpdateBookTypes(old, edit); !errors.Is(err, ErrDuplicateBookType) {
			t.Errorf("duplicate names %v = %v", names, err)
		}
	}
}
