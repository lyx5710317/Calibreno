package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/bookconvert"
	"dalibre/internal/calibre"
)

func TestDesktopConversionBookMenu(t *testing.T) {
	if !bookconvert.Supported() {
		t.Skip("desktop conversion uses the Windows x64 runtime")
	}
	a := newTestApp(t)
	a.conversionHome = t.TempDir()
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.ConversionTitle()); err != nil {
		t.Fatalf("open conversion: %v (menu %q)", err, tt.Menu())
	}
	tt.Frame()
	if s := a.conversion; s == nil || !s.form.open || s.form.title != "Dune" || s.form.selectedSource() != "EPUB" {
		t.Fatalf("unexpected conversion state: %+v", s)
	}
	if !tt.HasText(locales.ConversionLocal()) || !tt.HasText(locales.ConversionTargetFormat()) {
		t.Fatal("missing conversion entry or output control")
	}
	shot(t, tt, "conversion-book")
}

func conversionTestEngine(t *testing.T) bookconvert.Engine {
	t.Helper()
	root := os.Getenv("CALIBRENO_TEST_CONVERSION_RUNTIME")
	if root == "" || !bookconvert.Supported() {
		t.Skip("requires CALIBRENO_TEST_CONVERSION_RUNTIME on Windows x64")
	}
	return bookconvert.Engine{Directory: root, KindleCache: t.TempDir()}
}

func TestDesktopConversionAddsLibraryFormat(t *testing.T) {
	engine := conversionTestEngine(t)
	a := newTestApp(t)
	a.conversionHome, a.conversionEngine = t.TempDir(), engine
	file := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(file, []byte("天地玄黄。吾輩は猫である。"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := a.lib.AddBook(calibre.NewBook{File: file, Metadata: calibre.Metadata{Title: "Conversion source"}})
	if err != nil {
		t.Fatal(err)
	}
	a.reload(nil)
	a.openBookConversion(a.byID[id], "")
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click(locales.ConversionSubmit()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	b, err := a.lib.Book(id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(b.Formats, "EPUB") || !slices.Contains(b.Formats, "TXT") || a.busy != "" {
		t.Fatalf("converted book: %v, busy %q", b.Formats, a.busy)
	}
}

func TestDesktopPDFConversionReviewAndResume(t *testing.T) {
	engine := conversionTestEngine(t)
	a := newTestApp(t)
	a.conversionHome, a.conversionEngine = t.TempDir(), engine
	source := samplePDF(t, t.TempDir(), "test", "First page conversion text")
	a.openLocalConversion(source)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	shot(t, tt, "conversion-pdf-options")
	if err := tt.Click(locales.ConversionPreviewPages()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s := a.conversion
	if !s.pdf.open || s.pdf.preview == nil || !strings.Contains(s.pdf.record.Text, "First page conversion text") {
		t.Fatalf("unexpected page review: %+v", s.pdf)
	}
	shot(t, tt, "conversion-pdf-review")
	if err := tt.Click(locales.ConversionPDFBack()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.form.open || !s.form.reviewAvailable {
		t.Fatal("could not return to the conversion form")
	}
	output := filepath.Join(t.TempDir(), "result.epub")
	a.startConversion(s, "EPUB", output, nil)
	if st, err := os.Stat(output); err != nil || st.Size() == 0 {
		t.Fatalf("could not resume the PDF workspace: %v, form error %q", err, s.form.err)
	}
	reopened := New("")
	reopened.run = func(fn func()) { fn() }
	reopened.conversionHome = a.conversionHome
	reopened.openLocalConversion(source)
	if !reopened.conversion.form.reviewAvailable {
		t.Fatal("the saved PDF workspace was not restored on reopening the file")
	}
}
