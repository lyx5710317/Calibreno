package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
)

func TestGetBooksAppearsAfterAddingOPDS(t *testing.T) {
	a := newTestApp(t)
	a.secrets = memorySecrets{}
	a.settings.GetBooksSource = "dalibre.gutenberg"
	srv := remoteCatalog(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if tt.HasText(locales.CommonGetBooks()) {
		t.Fatalf("Get books is visible before any OPDS catalog is configured: %q", tt.Texts())
	}
	if err := tt.Click(locales.CatalogAdd()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.catEdit.open || !tt.HasText(locales.SettingsAddCatalogTitle()) {
		t.Fatalf("catalog sheet did not open: %q", tt.Texts())
	}
	a.catEdit.url = srv.URL + "/opds"
	a.catEdit.user, a.catEdit.password = "randy", "secret"
	a.checkCatalogSheet()
	tt.Frame()
	if err := tt.Click(locales.SettingsAddCatalogSubmit()); err != nil {
		t.Fatal(err)
	}
	a.settingsOpen = false
	tt.Frame()
	if err := tt.Click(locales.CommonGetBooks()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	cats := a.settings.OPDS.Catalogs
	if len(cats) != 1 || a.gb.source != catalogPrefix+cats[0].ID {
		t.Fatalf("new catalog not active: %v, selected %q", cats, a.gb.source)
	}
	a.gb.text = "earthsea"
	a.gbSearch()
	tt.Frame()
	if !tt.HasText("A Wizard of Earthsea") {
		t.Fatalf("catalog search result missing: %q", tt.Texts())
	}
	if err := tt.Click(locales.GetBooksSourceLabel("Remote")); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(locales.GetBooksAddOPDS()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.catEdit.open {
		t.Fatal("the OPDS picker no longer opens catalog setup")
	}
}
