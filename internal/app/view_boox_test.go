package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
)

func TestBOOXSettingsConnectionAndManualUpload(t *testing.T) {
	const address = "http://192.168.1.20:8085"
	s := booxSettingsState{address: " " + address + " "}
	var tested, opened string
	removed := false
	actions := booxSettingsActions{
		test: func(url string) {
			tested, s.testing = url, true
		},
		save: func(url string) {
			s.address, s.savedAddress = url, url
		},
		openUpload: func(url string) { opened = url },
		remove: func() {
			removed, s.address, s.savedAddress = true, "", ""
		},
	}
	tt := ui.NewTester(func(c *ui.Context) { booxPane(c, &s, actions) }, 740, 600)
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	click(locales.BooxTestConnection())
	if tested != address || !tt.HasText(locales.BooxTesting()) {
		t.Fatalf("probe address %q, visible text %v", tested, tt.Texts())
	}
	s.testing, s.failed, s.status = false, true, "Incompatible firmware"
	tt.Frame()
	if !tt.HasText(s.status) {
		t.Fatalf("missing probe status in %v", tt.Texts())
	}
	click(locales.BooxOpenUpload())
	if opened != address {
		t.Fatalf("manual upload address %q", opened)
	}
	click(locales.SettingsApply())
	if s.savedAddress != address {
		t.Fatalf("saved address %q", s.savedAddress)
	}
	click(locales.CommonRemove())
	if !removed || s.savedAddress != "" {
		t.Fatalf("remove called = %v, saved address %q", removed, s.savedAddress)
	}
}
