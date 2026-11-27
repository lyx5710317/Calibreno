package app

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
)

func TestConversionDialogPreviewAndStop(t *testing.T) {
	state := conversionDialogState{
		open: true, title: "Scanned book", sources: []string{"PDF"}, pages: "3–1",
		pdfLayout: 1, pdfLanguage: 2, ocr: true,
	}
	var previewPages []int
	var stops int
	callbacks := conversionDialogCallbacks{
		targets: func(string) []string { return []string{"EPUB"} },
		preview: func() {
			var err error
			previewPages, err = state.selectedPages()
			if err != nil {
				t.Errorf("preview used an invalid range: %v", err)
			}
		},
		cancelWork: func() { stops++ },
	}
	tt := ui.NewTester(func(c *ui.Context) { conversionDialog(c, &state, callbacks) }, 1050, 700)
	tt.Frame()
	if !tt.HasText(locales.ConversionInvalidPageRange()) {
		t.Fatalf("invalid range was not shown in the dialog: %q", tt.Texts())
	}

	state.pages = "1、3–4"
	tt.Frame()
	if err := tt.Click(locales.ConversionPreviewPages()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(previewPages, []int{1, 3, 4}) {
		t.Fatalf("preview pages = %v", previewPages)
	}
	opts := state.requestOptions("EPUB")
	if opts.SourceLayout != "vertical-rtl" || opts.PDFLanguage != "ja" || !opts.OCR {
		t.Fatalf("incorrect PDF options: %+v", opts)
	}

	callbacks.busy, callbacks.progress = true, "Recognizing pages 2/4"
	tt.Frame()
	if !tt.HasText(callbacks.progress) {
		t.Fatalf("progress is absent: %q", tt.Texts())
	}
	if err := tt.Click(locales.ToolsStop()); err != nil {
		t.Fatal(err)
	}
	if stops != 1 || !state.open {
		t.Fatalf("stop calls = %d, dialog open = %v", stops, state.open)
	}
}

func TestConversionDialogExistingFormatGetsCopy(t *testing.T) {
	state := conversionDialogState{
		open: true, title: "Book with a Kindle edition", isBook: true,
		sources: []string{"EPUB"}, formats: []string{"EPUB", "AZW3"},
	}
	callbacks := conversionDialogCallbacks{targets: func(string) []string { return []string{"AZW3"} }}
	tt := ui.NewTester(func(c *ui.Context) { conversionDialog(c, &state, callbacks) }, 1050, 700)
	tt.Frame()
	if !tt.HasText(locales.ConversionExistingFormat()) || state.writesToLibrary("AZW3") {
		t.Fatalf("existing format is not marked for export: %q", tt.Texts())
	}
}
