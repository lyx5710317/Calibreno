package batch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"dalibre/internal/calibre"
)

func batchTestLibrary(t *testing.T) *calibre.Library {
	t.Helper()
	l, err := calibre.Create(filepath.Join(t.TempDir(), "Library"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func batchTestFile(t *testing.T, path, contents string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func batchTestPrepare(t *testing.T, s *Store, b *Batch, l *calibre.Library, source string) *Item {
	t.Helper()
	b.Items = append(b.Items, NewItem(source, Waiting))
	item := &b.Items[len(b.Items)-1]
	index, err := l.Duplicates()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(b, item, calibre.Prepare(source, calibre.Metadata{}, nil), index); err != nil {
		t.Fatal(err)
	}
	return item
}

func TestRecoverCommittedAdd(t *testing.T) {
	l := batchTestLibrary(t)
	root := t.TempDir()
	store := NewStore(root)
	b := New(LibraryKey(l), "Batch")
	item := batchTestPrepare(t, store, b, l, batchTestFile(t, filepath.Join(t.TempDir(), "Book - Author.txt"), "original"))
	item.Intent = &Intent{Action: AddBook, UUID: "1411d631-0a7a-4b8e-a20a-e8016a8f0078"}
	if err := store.Save(b); err != nil {
		t.Fatal(err)
	}
	id, err := l.AddBook(calibre.NewBook{UUID: item.Intent.UUID, Metadata: item.Metadata, File: store.Staged(b, item)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(item.Path); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store = NewStore(root)
	t.Cleanup(store.Close)
	b, err = store.Load(b.Library, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Recover(b, l); err != nil {
		t.Fatal(err)
	}
	item = &b.Items[0]
	if item.State != Added || item.Intent != nil || item.Book.ID != id {
		t.Fatalf("after recovery: %+v", item)
	}
	if _, err := store.Apply(b, item, l, AddBook, nil, ""); err != nil {
		t.Fatal(err)
	}
	if books, _ := l.Books(); len(books) != 1 {
		t.Fatalf("books after replay: %d", len(books))
	}
	if _, err := os.Stat(store.Staged(b, item)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging was retained: %v", err)
	}
	other := batchTestLibrary(t)
	list, err := store.List(LibraryKey(other))
	if err != nil || len(list) != 0 {
		t.Fatalf("other library has batches: %v, %v", list, err)
	}
}

func TestRecoverMovedFormat(t *testing.T) {
	l := batchTestLibrary(t)
	dir := t.TempDir()
	id, err := l.Import(batchTestFile(t, filepath.Join(dir, "Book - Author.txt"), "original"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := l.Book(id)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(t.TempDir())
	t.Cleanup(store.Close)
	b := New(LibraryKey(l), "Batch")
	item := batchTestPrepare(t, store, b, l, batchTestFile(t, filepath.Join(dir, "Book - Author.pdf"), "addition"))
	item.Intent = &Intent{Action: AddFormat, Target: Ref(target)}
	if err := store.Save(b); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(l.Dir(target), calibre.BookFileName("Book", "Author", false)+".pdf")
	batchTestFile(t, expected, "addition")
	if err := store.Recover(b, l); err != nil {
		t.Fatal(err)
	}
	if item.State != Supplement {
		t.Fatalf("after recovery: %+v", item)
	}
	d, err := l.Details(target)
	if err != nil || len(d.Files) != 2 {
		t.Fatalf("formats after recovery: %+v, %v", d, err)
	}
}

func TestBatchReplaceRechecksVersion(t *testing.T) {
	l := batchTestLibrary(t)
	initial := batchTestFile(t, filepath.Join(t.TempDir(), "Book - Author.pdf"), "original")
	id, err := l.Import(initial)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(t.TempDir())
	t.Cleanup(store.Close)
	b := New(LibraryKey(l), "Batch")
	source := batchTestFile(t, filepath.Join(t.TempDir(), "Book - Author.pdf"), "replacement")
	item := batchTestPrepare(t, store, b, l, source)
	if item.State != Review || len(item.Candidates) != 1 {
		t.Fatalf("review entry: %+v", item)
	}
	target, err := l.Book(id)
	if err != nil {
		t.Fatal(err)
	}
	d, err := l.Details(target)
	if err != nil {
		t.Fatal(err)
	}
	inLibrary := d.Files[0].Path
	stale, _, _ := calibre.FileFingerprint(inLibrary)
	batchTestFile(t, inLibrary, "externally updated")
	if _, err := store.Apply(b, item, l, Replace, target, stale); !errors.Is(err, calibre.ErrBookChanged) {
		t.Fatalf("stale replacement: %v", err)
	}
	if got, _ := os.ReadFile(inLibrary); string(got) != "externally updated" || item.State != Review {
		t.Fatalf("review not retained: %q, %+v", got, item)
	}
	if err := store.Recheck(b, item, l); err != nil {
		t.Fatal(err)
	}
	now, _, _ := calibre.FileFingerprint(inLibrary)
	if _, err := store.Apply(b, item, l, Replace, target, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(inLibrary); string(got) != "replacement" || item.State != Replaced {
		t.Fatalf("replacement: %q, %+v", got, item)
	}
	if got, _ := os.ReadFile(source); string(got) != "replacement" {
		t.Fatal("source file was changed")
	}
}
