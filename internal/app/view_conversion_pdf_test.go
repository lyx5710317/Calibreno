package app

import (
	"image"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/bookconvert"
)

func TestPDFReviewNavigationAndRerun(t *testing.T) {
	state := pdfReviewState{
		open: true, title: "Scanned book", page: 1,
		summary: bookconvert.PDFWorkspaceSummary{Total: 4, Saved: []int{1, 2}, ReviewPages: []int{2, 3, 4}},
		record:  bookconvert.PDFPage{Number: 1, Available: true, Text: "Recognized sample"},
		preview: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 778, 1100))),
	}
	var rerun []int
	var busy bool
	var back, stop int
	progress := "Recognizing page 2/4"
	tt := ui.NewTester(func(c *ui.Context) {
		pdfReviewDialog(c, &state, busy, progress, func(page int) {
			state.page = page
			state.record = bookconvert.PDFPage{Number: page}
		}, func(pages []int) { rerun = pages }, func() { back++ }, func() { stop++ })
	}, 1050, 650)
	tt.Frame()
	if !tt.HasText("Recognized sample") || !tt.HasText(locales.ConversionPDFPagePosition(1, 4)) {
		t.Fatalf("page or text is missing: %v", tt.Texts())
	}
	if err := tt.Click(locales.ConversionPDFFit()); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("150%"); err != nil || state.zoom != 1 {
		t.Fatalf("zoom = %d, err = %v", state.zoom, err)
	}
	if err := tt.Click(locales.ConversionPDFNextReview()); err != nil {
		t.Fatal(err)
	}
	if state.page != 2 {
		t.Fatalf("jumped to page %d, want 2", state.page)
	}
	if err := tt.Click(locales.ConversionPDFRerun()); err != nil || !slices.Equal(rerun, []int{2}) {
		t.Fatalf("rerun pages = %v, err = %v", rerun, err)
	}
	state.rerunPages = "2、4"
	tt.Frame()
	if err := tt.Click(locales.ConversionPDFRerun()); err != nil || !slices.Equal(rerun, []int{2, 4}) {
		t.Fatalf("rerun pages = %v, err = %v", rerun, err)
	}
	busy = true
	tt.Frame()
	if !tt.HasText(progress) {
		t.Fatalf("progress is missing: %v", tt.Texts())
	}
	if err := tt.Click(locales.ActionStop(locales.ConversionPDFRerun())); err != nil || stop != 1 {
		t.Fatalf("stop calls = %d, err = %v", stop, err)
	}
	busy = false
	tt.Frame()
	if err := tt.Click(locales.ConversionPDFBack()); err != nil || back != 1 || state.open {
		t.Fatalf("back calls = %d, open = %v, err = %v", back, state.open, err)
	}
}
