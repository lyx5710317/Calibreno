package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/weread"
)

func TestTransferSettingsTabs(t *testing.T) {
	a := newTestApp(t)
	var opened string
	a.readingTransfers.openURL = func(url string) error {
		opened = url
		return nil
	}
	a.openSettings(sectionGeneral)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click(locales.SettingsSectionTransfer()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(locales.TransferKindleUSBNotConnected()) || !tt.HasText(locales.EmailSenderTitle()) {
		t.Fatalf("Kindle pane does not show USB status and sender settings: %q", tt.Texts())
	}
	kindle := plugIn(t, a, fakeKindle(t, false))
	tt.Frame()
	if !tt.HasText(locales.TransferKindleUSBConnected(kindle.Info().Name)) {
		t.Fatalf("Kindle pane does not show the detected USB Kindle: %q", tt.Texts())
	}
	for _, tab := range []struct {
		target transferTarget
		label  string
		body   string
	}{
		{transferWeRead, locales.TransferWeRead(), locales.TransferWeReadHelp()},
		{transferBOOX, locales.TransferBOOX(), locales.BooxAddress()},
		{transferKOReader, locales.TransferKOReader(), locales.WirelessBody()},
	} {
		if err := tt.Click(tab.label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.section != sectionTransfer || a.transferTab != tab.target || !tt.HasText(tab.body) {
			t.Fatalf("tab %q did not open: %q", tab.label, tt.Texts())
		}
		if tab.target == transferWeRead {
			if err := tt.Click(locales.TransferWeReadOpen()); err != nil {
				t.Fatal(err)
			}
			if opened != weread.UploadPage {
				t.Errorf("WeRead opened %q, want %q", opened, weread.UploadPage)
			}
		}
	}
}

func TestWirelessSettingsShortcutOpensKOReaderTab(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click(nameOPDS); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(locales.SharingSettingsTooltip(locales.WirelessName())); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sharingOpen || !a.settingsOpen || a.section != sectionTransfer || a.transferTab != transferKOReader {
		t.Fatalf("wireless shortcut did not open Transfer settings › KOReader: %q", tt.Texts())
	}
}
