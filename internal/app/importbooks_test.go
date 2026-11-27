package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/batch"
)

// TestDropImport drops a new book, a new format of a book the library
// has, a copy of one and a picture: the new book goes in at once, the
// new format waits for review, the exact match is recorded, and the picture
// is listed as unsupported.
func TestDropImport(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	newBook := write("Klara and the Sun - Kazuo Ishiguro.epub")
	dunePDF := write("Dune - Frank Herbert.pdf")
	piranesi := write("Piranesi - Susanna Clarke.epub") // the sample's file holds "x" too
	pic := write("cover.jpg")
	before := len(a.books)

	if !tt.DropFiles(700, 400, newBook, dunePDF, piranesi, pic) {
		t.Fatal("drop not taken")
	}
	tt.Frame()
	if len(a.books) != before+1 {
		t.Fatalf("books = %d, want %d", len(a.books), before+1)
	}
	s := a.batch.active.Summary()
	if s.Review != 1 || s.Duplicates != 1 || s.Unsupported != 1 {
		t.Fatalf("summary %+v", s)
	}
	clickBatch(t, tt, "Start duplicate review")
	shot(t, tt, "import-review")
	clickBatch(t, tt, "Add format")
	if a.batch.active.Summary().Review != 0 {
		for _, item := range a.batch.active.Items {
			t.Log(item.State, item.Format, item.Problem, item.Detail)
		}
		t.Fatalf("review still pending: %v", tt.Texts())
	}
	if len(a.books) != before+1 {
		t.Errorf("the same file was added again: %d books", len(a.books))
	}
	for _, b := range a.books {
		if b.Title == "Dune" && !slices.Contains(b.Formats, "PDF") {
			t.Errorf("Dune formats = %v", b.Formats)
		}
	}
}

// TestDropNothingAddable drops only a picture, which stays in the batch history.
func TestDropNothingAddable(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	pic := filepath.Join(t.TempDir(), "cover.jpg")
	os.WriteFile(pic, []byte("x"), 0o644)
	before := len(a.books)
	tt.DropFiles(700, 400, pic)
	tt.Frame()
	if len(a.books) != before || a.batch.active.Items[0].State != batch.Unsupported {
		t.Fatalf("books %d batch %+v", len(a.books), a.batch.active.Summary())
	}
	clickBatch(t, tt, "Batch results")
	if !tt.HasText("Unsupported file") {
		t.Errorf("result does not show unsupported file: %q", tt.Texts())
	}
}

func clickBatch(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %q: %v. visible: %v", label, err, tt.Texts())
	}
	tt.Frame()
}
