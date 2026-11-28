package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/bookconvert"
	"dalibre/internal/boox"
	"dalibre/internal/calibre"
	"dalibre/internal/settings"
	"dalibre/internal/weread"
)

const testBOOXAddress = "http://192.168.1.20:8085"

func TestBOOXBookMenuConfiguresThenSends(t *testing.T) {
	a := newTestApp(t)
	settingsFile := filepath.Join(t.TempDir(), "settings.json")
	a.settings = settings.Load(settingsFile)
	var sent string
	a.readingTransfers.send = func(_ context.Context, address, format, filename, path string) (boox.Outcome, error) {
		if address != testBOOXAddress || format != "EPUB" || filename != "Dune - Frank Herbert.epub" {
			t.Errorf("unexpected transfer: %q, %q, %q", address, format, filename)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error(err)
		}
		sent = path
		return boox.Sent, nil
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferBOOX()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settingsOpen || a.section != sectionTransfer || a.transferTab != transferBOOX || !tt.HasText(locales.BooxAddress()) {
		t.Fatal("setup did not open the BOOX settings pane")
	}
	shot(t, tt, "boox-settings")
	a.saveBOOXAddress("  " + testBOOXAddress + "/  ")
	if got := settings.Load(settingsFile).BOOXAddress; got != testBOOXAddress {
		t.Fatalf("persisted address = %q", got)
	}
	a.settingsOpen = false
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferBOOX()); err != nil {
		t.Fatal(err)
	}
	if sent == "" || a.busy != "" {
		t.Fatalf("did not finish sending: sent path %q, busy %q", sent, a.busy)
	}
}

func TestBOOXDoesNotRetryUnconfirmedAndOffersManualFallback(t *testing.T) {
	a := newTestApp(t)
	a.settings.BOOXAddress = testBOOXAddress
	var calls []string
	a.readingTransfers.send = func(_ context.Context, _, _, filename, path string) (boox.Outcome, error) {
		calls = append(calls, filename)
		if len(calls) == 1 {
			return boox.Unconfirmed, nil
		}
		return "", boox.ErrIncompatible
	}
	var opened, revealed string
	a.readingTransfers.openURL = func(address string) error { opened = address; return nil }
	a.readingTransfers.reveal = func(path string) { revealed = path }
	first, second, third := a.books[0], a.books[1], a.books[2]
	a.toasts = nil
	a.sendBooksToBOOX([]int64{first.ID, second.ID, third.ID}, "")
	if len(calls) != 2 || opened != "" || revealed != "" {
		t.Fatalf("calls = %v, opened = %q, revealed = %q", calls, opened, revealed)
	}
	var unconfirmed, notSent bool
	for _, toast := range a.toasts {
		switch {
		case toast.text == locales.BooxUnconfirmed(first.Title):
			unconfirmed = toast.error
		case toast.text == locales.BooxNotSent(1):
			notSent = true
		case toast.action == locales.BooxOpenUpload():
			toast.onAction()
		}
	}
	if !unconfirmed || !notSent || opened != testBOOXAddress || filepath.Ext(revealed) != ".epub" {
		t.Fatalf("unexpected transfer outcome or fallback: %+v, opened = %q, revealed = %q", a.toasts, opened, revealed)
	}
}

func TestWeReadBookMenuHandsOffAnExistingEPUB(t *testing.T) {
	a := newTestApp(t)
	title := "WeRead sample"
	file := sampleEPUB(t, t.TempDir(), importSample{title: title, author: "Sample Author"})
	id, err := a.lib.AddBook(calibre.NewBook{File: file, Metadata: calibre.Metadata{Title: title}})
	if err != nil {
		t.Fatal(err)
	}
	a.reload(nil)
	var opened, revealed, copied string
	a.readingTransfers.openURL = func(url string) error { opened = url; return nil }
	a.readingTransfers.reveal = func(path string) { revealed = path }
	a.readingTransfers.copyPath = func(path string) error { copied = path; return nil }
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick(title); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferWeRead()); err != nil {
		t.Fatal(err)
	}
	want, _, err := libraryFormatPaths(a.lib, a.byID[id], "EPUB")
	if err != nil {
		t.Fatal(err)
	}
	if opened != weread.UploadPage || revealed != want || copied != "" {
		t.Fatalf("unexpected handoff: opened = %q, revealed = %q, copied = %q", opened, revealed, copied)
	}
	a.toasts = nil
	a.prepareWeRead(a.byID[id])
	if len(a.toasts) != 1 || a.toasts[0].text != locales.WeReadOpened() {
		t.Fatalf("unexpected WeRead status: %+v", a.toasts)
	}
	a.toasts[0].onAction()
	if copied != want || !strings.HasSuffix(copied, ".epub") {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	a.readingTransfers.copyPath = func(string) error { return errors.New("clipboard is busy") }
	a.toasts = nil
	a.prepareWeRead(a.byID[id])
	if len(a.toasts) != 1 || a.toasts[0].onAction == nil {
		t.Fatalf("missing clipboard action: %+v", a.toasts)
	}
	a.toasts[0].onAction()
	if got := a.toasts[len(a.toasts)-1]; !got.error || got.text == locales.WeReadCopiedPath() {
		t.Fatalf("failed clipboard write reported %+v", got)
	}
}

func TestReadingTransfersRejectFileOutsideLibrary(t *testing.T) {
	a := newTestApp(t)
	b := *a.books[0]
	original, _, err := libraryFormatPaths(a.lib, &b, "EPUB")
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	data, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, filepath.Base(original)), data, 0o600); err != nil {
		t.Fatal(err)
	}
	b.Path, err = filepath.Rel(a.lib.Root, outside)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := libraryFormatPaths(a.lib, &b, "EPUB"); !errors.Is(err, calibre.ErrLibraryFileOutside) {
		t.Fatalf("resolve path outside library: %v", err)
	}
	if _, ok := readerUpload(a.lib, &b, "EPUB"); ok {
		t.Fatal("USB upload accepted a file outside the library")
	}
}

func TestWeReadBookWithoutEPUBOpensConversion(t *testing.T) {
	if !bookconvert.Supported() {
		t.Skip("format conversion uses the Windows x64 runtime")
	}
	a := newTestApp(t)
	a.conversionHome = t.TempDir()
	b := a.books[0]
	b.Formats = []string{"MOBI"}
	a.readingTransfers.openURL = func(string) error {
		t.Fatal("the browser opened before an EPUB was available")
		return nil
	}
	a.prepareWeRead(b)
	s := a.conversion
	if s == nil || !s.form.open || s.form.selectedSource() != "MOBI" ||
		s.form.selectedTarget(bookconvert.OutputFormats) != "EPUB" {
		t.Fatal("expected MOBI to EPUB conversion")
	}
}
