package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/settings"
	"dalibre/internal/weread"
)

func TestTransferMenuOffersAllDestinationsBeforeSetup(t *testing.T) {
	a := newTestApp(t)
	const title = "Transfer sample"
	file := sampleEPUB(t, t.TempDir(), importSample{title: title, author: "Sample Author"})
	if _, err := a.lib.AddBook(calibre.NewBook{File: file, Metadata: calibre.Metadata{Title: title}}); err != nil {
		t.Fatal(err)
	}
	a.reload(nil)
	var opened, revealed string
	a.readingTransfers.openURL = func(location string) error {
		opened = location
		return nil
	}
	a.readingTransfers.reveal = func(path string) { revealed = path }
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()

	for _, tc := range []struct {
		label string
		route []string
		tab   transferTarget
	}{
		{"Kindle USB", []string{locales.TransferKindle(), locales.TransferKindleUSB()}, transferKindle},
		{"Kindle email", []string{locales.TransferKindle(), locales.TransferKindleEmail()}, transferKindle},
		{"WeRead", []string{locales.TransferWeRead(), locales.WeReadSettings()}, transferWeRead},
		{"BOOX", []string{locales.TransferBOOX()}, transferBOOX},
		{"KOReader", []string{locales.TransferKOReader()}, transferKOReader},
	} {
		t.Run(tc.label, func(t *testing.T) {
			if err := tt.RightClick(title); err != nil {
				t.Fatal(err)
			}
			if err := tt.ChooseMenuItem(append([]string{locales.TransferMenu()}, tc.route...)...); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if !a.settingsOpen || a.section != sectionTransfer || a.transferTab != tc.tab {
				t.Fatalf("setup opened = %v, section = %v, destination = %v", a.settingsOpen, a.section, a.transferTab)
			}
			a.settingsOpen = false
			tt.Frame()
		})
	}

	if err := tt.RightClick(title); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferWeRead(), locales.WeReadManualAction()); err != nil {
		t.Fatal(err)
	}
	if opened != weread.UploadPage || !strings.HasSuffix(revealed, ".epub") {
		t.Fatalf("wanted WeRead handoff, opened %q and revealed %q", opened, revealed)
	}
}

func TestTransferMenuSendsToKindleUSB(t *testing.T) {
	a := newTestApp(t)
	root := fakeKindle(t, true)
	device := plugIn(t, a, root)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Contains(menu, locales.TransferMenu()) || slices.Contains(menu, locales.WirelessSendTo("Kindle")) {
		t.Fatalf("unexpected top-level menu %q", menu)
	}
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferKindle(), locales.TransferKindleUSBTo("Kindle")); err != nil {
		t.Fatal(err)
	}
	if got := len(device.Books()); got != 1 {
		t.Fatalf("Kindle has %d books after transfer", got)
	}
	if _, err := os.Stat(filepath.Join(root, "documents", "Herbert, Frank", "Dune - Frank Herbert.epub")); err != nil {
		t.Fatal(err)
	}

	tt.Frame()
	if err := tt.Click("Piranesi"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(locales.DetailMore()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferKindle(), locales.TransferKindleUSBTo("Kindle")); err != nil {
		t.Fatal(err)
	}
	if got := len(device.Books()); got != 2 {
		t.Fatalf("Kindle has %d books after transfer from book details", got)
	}
}

func TestTransferMenuSendsToConnectedKOReader(t *testing.T) {
	a := newTestApp(t)
	pump := queuePosts(a)
	a.settings.Wireless = settings.Wireless{On: true, Port: freePort(t)}
	a.applyDevice()
	pump()
	t.Cleanup(func() {
		a.settings.Wireless.On = false
		a.applyDevice()
		pump()
	})
	if a.device == nil || a.device.srv == nil {
		t.Fatalf("wireless service failed to start: %+v", a.device)
	}
	reader := &koreader{}
	connectKOReader(t, a, pump, reader)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); slices.Contains(menu, locales.WirelessSendTo("KOReader (Kobo)")) {
		t.Fatalf("KOReader shouldn't have a duplicate top-level action: %q", menu)
	}
	if err := tt.ChooseMenuItem(locales.TransferMenu(), locales.TransferKOReader()); err != nil {
		t.Fatal(err)
	}
	pump()
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if len(reader.got) != 1 || !strings.HasSuffix(reader.got[0], "Dune - Frank Herbert.epub") {
		t.Fatalf("KOReader received %q", reader.got)
	}
}
