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

func TestWeReadManualImportHandsOffAnExistingEPUB(t *testing.T) {
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
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferWeRead(), locales.WeReadManualAction()); err != nil {
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
	a.manualWeRead(a.byID[id])
	if len(a.toasts) != 1 || a.toasts[0].text != locales.WeReadOpened() {
		t.Fatalf("unexpected WeRead status: %+v", a.toasts)
	}
	a.toasts[0].onAction()
	if copied != want || !strings.HasSuffix(copied, ".epub") {
		t.Fatalf("copied %q, want %q", copied, want)
	}
	a.readingTransfers.copyPath = func(string) error { return errors.New("clipboard is busy") }
	a.toasts = nil
	a.manualWeRead(a.byID[id])
	if len(a.toasts) != 1 || a.toasts[0].onAction == nil {
		t.Fatalf("missing clipboard action: %+v", a.toasts)
	}
	a.toasts[0].onAction()
	if got := a.toasts[len(a.toasts)-1]; !got.error || got.text == locales.WeReadCopiedPath() {
		t.Fatalf("failed clipboard write reported %+v", got)
	}
}

type weReadSendTestClient struct {
	send    func(string) (weread.Status, error)
	reviews int
}

func (*weReadSendTestClient) Check(context.Context) (weread.Status, error) {
	return weread.StatusReady, nil
}

func (c *weReadSendTestClient) Send(_ context.Context, file string) (weread.Status, error) {
	return c.send(file)
}

func (c *weReadSendTestClient) OpenShelf(context.Context) error {
	c.reviews++
	return nil
}

func (*weReadSendTestClient) Close() {}

func TestWeReadUploadsSelectedBookAndDoesNotRetryPending(t *testing.T) {
	a := newTestApp(t)
	const title = "WeRead upload sample"
	file := sampleEPUB(t, t.TempDir(), importSample{title: title, author: "Sample Author"})
	id, err := a.lib.AddBook(calibre.NewBook{File: file, Metadata: calibre.Metadata{Title: title}})
	if err != nil {
		t.Fatal(err)
	}
	a.reload(nil)
	book := a.byID[id]
	_, want, err := libraryFormatPaths(a.lib, book, "EPUB")
	if err != nil {
		t.Fatal(err)
	}
	stages := []weread.Status{weread.StatusLoginRequired, weread.StatusPending, weread.StatusImported, weread.StatusFailed}
	calls := 0
	client := &weReadSendTestClient{send: func(file string) (weread.Status, error) {
		if file != want || calls >= len(stages) {
			t.Fatalf("unexpected upload of %q, call %d, want %q", file, calls+1, want)
		}
		stage := stages[calls]
		calls++
		return stage, nil
	}}
	a.weRead.client = client
	view := ui.NewTester(a.View, 1440, 900)
	view.Frame()
	if err := view.RightClick(title); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem(locales.TransferMenu(), locales.TransferWeRead(), locales.WeReadSendAction()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || a.weRead.status != weread.StatusLoginRequired {
		t.Fatalf("expected sign-in, calls=%d status=%q", calls, a.weRead.status)
	}
	a.prepareWeRead(book)
	if calls != 2 || a.weRead.status != weread.StatusPending || len(a.weRead.records) != 2 {
		t.Fatalf("expected pending, calls=%d state=%+v", calls, a.weRead)
	}
	review := a.toasts[len(a.toasts)-1]
	if review.action != locales.WeReadReviewAction() || review.onAction == nil {
		t.Fatalf("pending transfer has no review action: %+v", review)
	}
	review.onAction()
	a.prepareWeRead(book)
	if client.reviews != 1 || calls != 2 || a.weRead.duplicate == nil {
		t.Fatalf("expected review and duplicate prompt without reupload: reviews=%d calls=%d", client.reviews, calls)
	}
	pendingID := a.weRead.duplicate.ID
	a.confirmWeRead(pendingID)
	if calls != 2 || a.weRead.records[0].Status != weread.StatusImported || a.weRead.records[0].ConfirmedAt.IsZero() {
		t.Fatalf("manual confirmation did not update the pending record: %+v", a.weRead.records)
	}
	a.resendWeRead()
	if calls != 3 || a.weRead.duplicate != nil || a.weRead.records[0].Status != weread.StatusImported {
		t.Fatalf("explicit resend did not record import: calls=%d state=%+v", calls, a.weRead)
	}
	a.prepareWeRead(book)
	a.resendWeRead()
	if calls != 4 || a.weRead.status != weread.StatusFailed || !a.toasts[len(a.toasts)-1].error {
		t.Fatal("expected a clear failure after explicit website rejection")
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
	const title = "Convert to send"
	source := filepath.Join(t.TempDir(), "source.mobi")
	if err := os.WriteFile(source, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := a.lib.AddBook(calibre.NewBook{File: source, Metadata: calibre.Metadata{Title: title}})
	if err != nil {
		t.Fatal(err)
	}
	a.reload(nil)
	var sent string
	a.weRead.client = &weReadSendTestClient{send: func(path string) (weread.Status, error) {
		sent = path
		return weread.StatusImported, nil
	}}
	a.prepareWeRead(a.byID[id])
	s := a.conversion
	if s == nil || !s.form.open || s.form.selectedSource() != "MOBI" ||
		s.form.selectedTarget(bookconvert.OutputFormats) != "EPUB" || sent != "" {
		t.Fatal("expected MOBI to EPUB conversion")
	}
	file := sampleEPUB(t, t.TempDir(), importSample{title: title, author: "Sample Author"})
	if err := a.lib.AddFormat(id, file); err != nil {
		t.Fatal(err)
	}
	a.completeConversion(s, "EPUB", "", false)
	toast := a.toasts[len(a.toasts)-1]
	if toast.action != locales.WeReadSendAction() || toast.onAction == nil || sent != "" {
		t.Fatalf("conversion must ask before sending: %+v", toast)
	}
	toast.onAction()
	_, want, err := libraryFormatPaths(a.lib, a.byID[id], "EPUB")
	if err != nil || sent != want || sent == "" {
		t.Fatalf("send converted EPUB: path=%q, want %q, error=%v", sent, want, err)
	}
}
