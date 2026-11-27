package calibre

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestScanLibraryDuplicates(t *testing.T) {
	lib := newTestLibrary(t)
	epub := writeTemp(t, "format.epub", "shared epub")
	pdf := writeTemp(t, "format.pdf", "shared pdf")
	a := addLibraryScanBook(t, lib, "Original book", "First author", epub)
	b := addLibraryScanBook(t, lib, "Different title and author", "Second author", epub)
	c := addLibraryScanBook(t, lib, "PDF copy", "Third author", pdf)
	d := addLibraryScanBook(t, lib, "Original book", "First author", writeTemp(t, "different.epub", "other words"))
	if err := lib.AddFormat(a, pdf); err != nil {
		t.Fatal(err)
	}
	// Identical bytes with a different format do not count.
	if err := lib.AddFormat(d, writeTemp(t, "different.pdf", "shared epub")); err != nil {
		t.Fatal(err)
	}
	// Simulate an older format row with a stale size, and a book without a file.
	if _, err := lib.db.Exec("UPDATE data SET uncompressed_size = 999 WHERE book = ?", b); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.db.Exec("INSERT INTO books (title) VALUES ('No formats')"); err != nil {
		t.Fatal(err)
	}

	database := filepath.Join(lib.Root, "metadata.db")
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(lib.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var progress [][2]int
	got, err := reader.ScanDuplicates(t.Context(), func(done, total int) {
		progress = append(progress, [2]int{done, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	want := LibraryDuplicateReport{
		Groups: []LibraryDuplicateGroup{
			{BookIDs: []int64{a, b}, Format: "EPUB"},
			{BookIDs: []int64{a, c}, Format: "PDF"},
		},
		BooksScanned: 5,
		FilesScanned: 6,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report = %+v, want %+v", got, want)
	}
	if want := [][2]int{{0, 6}, {1, 6}, {2, 6}, {3, 6}, {4, 6}, {5, 6}, {6, 6}}; !slices.Equal(progress, want) {
		t.Fatalf("progress = %v, want %v", progress, want)
	}
	after, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("scanning changed metadata.db")
	}
}

func TestScanLibraryDuplicatesSkipsInvalidFiles(t *testing.T) {
	lib := newTestLibrary(t)
	shared := writeTemp(t, "format.epub", "shared epub")
	a := addLibraryScanBook(t, lib, "First", "Author", shared)
	b := addLibraryScanBook(t, lib, "Second", "Author", shared)
	c := addLibraryScanBook(t, lib, "Missing", "Author", shared)
	d := addLibraryScanBook(t, lib, "Unsafe", "Author", shared)

	book, err := lib.Book(c)
	if err != nil {
		t.Fatal(err)
	}
	details, err := lib.Details(book)
	if err != nil {
		t.Fatal(err)
	}
	missing := details.Files[0].Path
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(filepath.Dir(lib.Root), "outside")
	if err := os.Mkdir(external, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "Unsafe - Author.epub"), []byte("shared epub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.db.Exec("UPDATE books SET path = '../outside' WHERE id = ?", d); err != nil {
		t.Fatal(err)
	}

	report, err := lib.ScanDuplicates(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(lib.Root, missing)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Groups, []LibraryDuplicateGroup{{BookIDs: []int64{a, b}, Format: "EPUB"}}) ||
		!slices.Equal(report.Unreadable, []string{relative, fmt.Sprintf("#%d", d)}) {
		t.Fatalf("report = %+v", report)
	}
}

func TestScanLibraryDuplicatesCancels(t *testing.T) {
	lib := newTestLibrary(t)
	file := writeTemp(t, "format.epub", "identical")
	addLibraryScanBook(t, lib, "First", "Author", file)
	addLibraryScanBook(t, lib, "Second", "Author", file)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	last := -1
	report, err := lib.ScanDuplicates(ctx, func(done, _ int) {
		last = done
		if done == 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || last != 1 || len(report.Groups) != 0 {
		t.Fatalf("report = %+v, progress = %d, error = %v", report, last, err)
	}
}

func addLibraryScanBook(t *testing.T, lib *Library, title, author, file string) int64 {
	t.Helper()
	id, err := lib.AddBook(NewBook{File: file, Metadata: Metadata{Title: title, Authors: []string{author}}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}
