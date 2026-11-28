package app

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/weread"
)

type weReadSettingsTestClient struct {
	checks  int
	reviews int
}

func (client *weReadSettingsTestClient) Check(context.Context) (weread.Status, error) {
	client.checks++
	switch client.checks {
	case 1:
		return weread.StatusLoginRequired, nil
	case 2:
		return weread.StatusReady, nil
	}
	return weread.StatusManual, nil
}

func (*weReadSettingsTestClient) Send(context.Context, string) (weread.Status, error) {
	return weread.StatusPending, nil
}

func (client *weReadSettingsTestClient) OpenShelf(context.Context) error {
	client.reviews++
	return nil
}

func (*weReadSettingsTestClient) Close() {}

func TestWeReadSettingsShowsSignInAndPendingTransfers(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := newTestApp(t)
	client := &weReadSettingsTestClient{}
	a.weRead.client = client
	a.openTransferSettings(transferWeRead)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.SetLocale("zh-Hans")
	assertText := func(labels ...string) {
		t.Helper()
		for _, label := range labels {
			if !tt.HasText(label) {
				t.Fatalf("WeRead settings missing %q: %q", label, tt.Texts())
			}
		}
	}
	tt.Frame()
	assertText(locales.WeReadSignIn(), locales.TransferWeReadOpen(), locales.WeReadHistoryEmpty())
	if tt.HasText(locales.WeReadStatusReady()) {
		t.Fatal("fresh settings show a connection before checking")
	}
	if err := tt.Click(locales.WeReadSignIn()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	assertText(locales.WeReadStatusLoginRequired(), locales.WeReadLoginHelp(), locales.WeReadRetryAfterQR())
	if err := tt.Click(locales.WeReadRetryAfterQR()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	assertText(locales.WeReadStatusReady(), locales.WeReadCheckConnection())
	if client.checks != 2 || tt.HasText(locales.WeReadLoginHelp()) {
		t.Fatalf("expected two checks and no remaining sign-in instructions, got %d", client.checks)
	}
	if err := tt.Click(locales.WeReadCheckConnection()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	assertText(locales.WeReadStatusManual(), locales.SettingsCheckAgain())
	if tt.HasText(locales.WeReadRetryAfterQR()) {
		t.Fatal("connection retry asks the user to rescan a QR code")
	}

	now := time.Now()
	pending := weread.Record{ID: "pending", Title: "海岸笔记", Status: weread.StatusPending, At: now}
	a.weRead.records = []weread.Record{
		pending,
		{ID: "imported", Title: "城市散步", Status: weread.StatusImported, At: now},
		{ID: "verified", Title: "山间来信", Status: weread.StatusImported, At: now, ConfirmedAt: now},
		{ID: "failed", Title: "写作练习", Status: weread.StatusFailed, At: now},
	}
	a.weRead.duplicate = &pending
	tt.Frame()
	assertText(locales.WeReadDuplicate(), locales.WeReadResend(), pending.Title, locales.WeReadStatusPending(),
		locales.WeReadReviewAction(), locales.WeReadConfirm(), "城市散步", locales.WeReadStatusImported(),
		"山间来信", locales.WeReadStatusConfirmed(), "写作练习", locales.WeReadStatusFailed())
	shot(t, tt, "weread-settings")
	if err := tt.Click(locales.WeReadReviewAction()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if client.reviews != 1 {
		t.Fatalf("review opened %d dedicated shelves", client.reviews)
	}
}

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
