package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// fakeMarketplace serves a marketplace.json and its plugins' packages.
type fakeMarketplace struct {
	srv      *httptest.Server
	listings []map[string]any
	packages map[string][]byte
	fail     bool
}

func newFakeMarketplace(t *testing.T) *fakeMarketplace {
	f := &fakeMarketplace{packages: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/marketplace.json", func(w http.ResponseWriter, r *http.Request) {
		if f.fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"name":       "Dalibre Marketplace",
			"categories": []map[string]string{{"id": "library", "title": "Library"}, {"id": "metadata", "title": "Metadata"}},
			"plugins":    f.listings,
		})
	})
	mux.HandleFunc("/pkg/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(f.packages[strings.TrimPrefix(r.URL.Path, "/pkg/")])
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// publish lists a version of an actions plugin, replacing the one listed.
func (f *fakeMarketplace) publish(id, name, version, category string, downloads int, hosts string) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("plugin.json")
	manifest := `{"id":"` + id + `","name":"` + name + `","version":"` + version + `","api":1,"author":"Randy","license":"GPL-3.0-or-later",
		"description":"Find books you have more than once","icon":"layers",
		"capabilities":{"actions":[{"id":"find","title":"Find duplicates…","scope":"library"},{"id":"exempt","title":"Not duplicates","scope":"books","books":{"min":2},"in":"results"}]},
		"permissions":{"hosts":[` + hosts + `],"library":"read"}}`
	w.Write([]byte(manifest))
	w, _ = zw.Create("main.js")
	w.Write([]byte(`export function run() { return { message: "No duplicates" } }`))
	zw.Close()
	key := id + "-" + version
	f.packages[key] = buf.Bytes()
	var m map[string]any
	json.Unmarshal([]byte(manifest), &m)
	l := map[string]any{"id": id, "name": name, "version": version, "description": m["description"], "author": "Randy",
		"license": "GPL-3.0-or-later", "icon": "layers", "category": category, "calibre": name, "downloads": downloads,
		"repo": "djyde/dalibre", "package": f.srv.URL + "/pkg/" + key,
		"capabilities": m["capabilities"], "permissions": m["permissions"]}
	for i, old := range f.listings {
		if old["id"] == id {
			f.listings[i] = l
			return
		}
	}
	f.listings = append(f.listings, l)
}

func TestMarketplaceInstallAndUpdate(t *testing.T) {
	a := newTestApp(t)
	useTestPluginDir(t, a)
	mk := newFakeMarketplace(t)
	mk.publish("org.example.dupes", "Find Duplicates", "1.0.0", "library", 11688, "")
	mk.publish("org.example.wiki", "Wikidata", "1.0.0", "metadata", 2542, `"www.wikidata.org"`)
	mk.listings[0]["origin"] = "upstream"
	a.marketURL = mk.srv.URL + "/marketplace.json"
	tt := ui.NewTester(a.View, 1440, 900)

	a.openPlugins("")
	tt.Frame()
	if err := tt.Click("Marketplace"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	for _, s := range []string{"Find Duplicates", "Wikidata", "Find duplicates…", "Tools", "Version 1.0.0 · Randy",
		"No internet access", "2 plugins", "GPL-3.0-or-later", "Upstream port"} {
		if !tt.HasText(s) {
			t.Fatalf("marketplace misses %q: %q", s, tt.Texts())
		}
	}
	shot(t, tt, "marketplace")

	// Categories and search filter the list.
	if err := tt.Click("Metadata"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.marketListings(); len(got) != 1 || got[0].Name != "Wikidata" {
		t.Fatalf("metadata: %+v", got)
	}
	a.mk.cat, a.mk.query = "", "dupl"
	if got := a.marketListings(); len(got) != 1 || got[0].Name != "Find Duplicates" {
		t.Fatalf("search: %+v", got)
	}
	a.mk.query = ""

	// Install asks for what the plugin may reach, then installs it.
	a.mk.sel = "org.example.dupes"
	a.installListing(a.mk.index.Find("org.example.dupes"))
	tt.Frame()
	if a.consent == nil || a.consent.manifest.ID != "org.example.dupes" {
		t.Fatalf("no consent asked: %q", tt.Texts())
	}
	a.grantConsent()
	tt.Frame()
	st := a.settings.Plugins["org.example.dupes"]
	if a.pluginByID("org.example.dupes") == nil || st == nil || st.Source == nil || !st.Source.Marketplace || st.Source.Ref != "1.0.0" {
		t.Fatalf("installed: %+v", st)
	}
	if !tt.HasText("Installed") || a.listingState(a.mk.index.Find("org.example.dupes")) != listingInstalled {
		t.Fatalf("row: %q", tt.Texts())
	}

	// A newer version listed, asking for nothing new, installs by itself.
	mk.publish("org.example.dupes", "Find Duplicates", "1.1.0", "library", 11688, "")
	a.checkUpdates(true)
	tt.Frame()
	if a.pluginByID("org.example.dupes").Manifest.Version != "1.1.0" || st.Source.Ref != "1.1.0" || a.updates["org.example.dupes"] != nil {
		t.Fatalf("auto update: %s, %v", st.Source.Ref, a.updates)
	}

	// One asking for more waits, shown as Update in the Marketplace.
	mk.publish("org.example.dupes", "Find Duplicates", "2.0.0", "library", 11688, `"api.example.com"`)
	a.loadMarketplace(true)
	tt.Frame()
	if a.listingState(a.mk.index.Find("org.example.dupes")) != listingUpdate || !tt.HasText("Update to 2.0.0") {
		t.Fatalf("update: %v %q", a.updates, tt.Texts())
	}
	shot(t, tt, "marketplace-update")
	a.updatePlugin("org.example.dupes", false)
	tt.Frame()
	if a.consent == nil || !tt.HasText("Connect to api.example.com") {
		t.Fatalf("consent: %q", tt.Texts())
	}
	a.grantConsent()
	tt.Frame()
	if st.Source.Ref != "2.0.0" || a.pluginByID("org.example.dupes").Manifest.Version != "2.0.0" {
		t.Fatalf("after consent: %s", st.Source.Ref)
	}

	// One whose source says Dalibre's own releases updates from the
	// Marketplace: those releases are Dalibre's, not the plugin's.
	st.Source = &settings.PluginSource{Repo: plugin.MarketplaceRepo, Ref: "v0.6.3", Auto: true}
	mk.publish("org.example.dupes", "Find Duplicates", "2.1.0", "library", 11688, `"api.example.com"`)
	mk.listings[0]["origin"] = "upstream"
	a.checkUpdates(true)
	tt.Frame()
	if !st.Source.Marketplace || st.Source.Ref != "2.1.0" || a.pluginByID("org.example.dupes").Manifest.Version != "2.1.0" {
		t.Fatalf("from Dalibre's repository: %+v", st.Source)
	}

	// A plugin needing a newer Dalibre is shown, not offered.
	defer func(v string) { plugin.DalibreVersion = v }(plugin.DalibreVersion)
	plugin.DalibreVersion = "0.4.0"
	mk.publish("org.example.wiki", "Wikidata", "1.0.0", "metadata", 2542, `"www.wikidata.org"`)
	mk.listings[1]["dalibre"] = ">=9.0.0"
	a.loadMarketplace(true)
	a.mk.sel = "org.example.wiki"
	tt.Frame()
	if a.listingState(a.mk.index.Find("org.example.wiki")) != listingNeedsDalibre || !tt.HasText("Needs Calibreno 9.0.0") {
		t.Fatalf("needs Dalibre: %q", tt.Texts())
	}

	// Offline, the copy kept is shown, with Try again.
	mk.fail = true
	a.loadMarketplace(true)
	tt.Frame()
	if a.mk.index == nil || !tt.HasText("Couldn’t refresh the Marketplace; showing the last copy.") || !tt.HasText("Try again") {
		t.Fatalf("offline: %q", tt.Texts())
	}
}

func TestParseMarketplace(t *testing.T) {
	m, err := plugin.ParseMarketplace([]byte(`{"plugins":[
		{"id":"a.b","name":"A","version":"1.0.0","package":"https://x/a"},
		{"id":"dalibre.x","name":"Reserved","version":"1.0.0","package":"https://x/b"},
		{"id":"c.d","name":"No package","version":"1.0.0"},
		{"id":"e.f","name":"Bad version","version":"one","package":"https://x/c"}]}`))
	if err != nil || len(m.Plugins) != 1 || m.Find("a.b") == nil {
		t.Fatalf("%v %+v", err, m)
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.1.0", "1.0.0", true}, {"1.0.0", "1.0.0", false}, {"1.0.0", "1.0.0-beta", true}, {"0.9.9", "1.0.0", false}, {"1.10.0", "1.9.0", true}, {"x", "1.0.0", false}} {
		if got := plugin.NewerVersion(c.a, c.b); got != c.want {
			t.Errorf("NewerVersion(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestMarketplaceChineseRanking(t *testing.T) {
	const (
		chinese = "org.randynamic.dalibre.chinese-convert"
		isbn    = "org.randynamic.dalibre.extract-isbn"
		search  = "org.randynamic.dalibre.search-internet"
		merge   = "org.randynamic.dalibre.epub-merge"
		brasil  = "io.github.bellussi1.isbn-brasil"
	)
	locale := "en"
	i18n.SetLocaleFunc(func() string { return locale })
	t.Cleanup(func() { i18n.SetLocaleFunc(nil) })
	a := &App{mk: marketState{index: &plugin.Marketplace{Plugins: []plugin.Listing{
		{ID: brasil, Downloads: 500},
		{ID: isbn, Downloads: 50},
		{ID: chinese, Downloads: 10},
		{ID: merge, Downloads: 100},
		{ID: search, Downloads: 200},
	}}}}
	check := func(want []string) {
		t.Helper()
		var got []string
		for _, l := range a.marketListings() {
			got = append(got, l.ID)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("ranking in %s: got %v, want %v", locale, got, want)
		}
	}
	check([]string{brasil, search, merge, isbn, chinese})
	locale = "zh-Hans"
	check([]string{search, chinese, isbn, merge, brasil})
	locale = "en"
	a.mk.index.Featured = []string{search, chinese}
	check([]string{search, chinese, brasil, merge, isbn})
	locale = "zh-Hans"
	a.mk.query = "ISBN"
	check([]string{isbn, brasil})
}

func TestMarketplaceRejectsChangedRepository(t *testing.T) {
	const id = "org.example.tool"
	listing := plugin.Listing{ID: id, Repo: "example/other-owner", Version: "2.0.0", Package: "https://example.test/tool"}
	index := &plugin.Marketplace{Plugins: []plugin.Listing{listing}}
	if u := marketUpdate(index, id, "1.0.0", "example/first-owner"); u != nil {
		t.Fatalf("offered an update from another repository: %+v", u)
	}
	if u := marketUpdate(index, id, "1.0.0", "Example/Other-owner"); u == nil {
		t.Fatal("missed an update from the same repository")
	}
	index.Plugins[0].Repo = plugin.MarketplaceRepo
	index.Plugins[0].Origin = plugin.MarketOriginUpstream
	if u := marketUpdate(index, id, "1.0.0", "djyde/dalibre"); u == nil {
		t.Fatal("missed the approved transition from the upstream index")
	}
}

func TestMarketplaceRejectsWrongPackageVersion(t *testing.T) {
	const id = "org.example.tool"
	a := newTestApp(t)
	useTestPluginDir(t, a)
	mk := newFakeMarketplace(t)
	mk.publish(id, "Example tool", "1.0.0", "library", 0, "")
	mk.listings[0]["version"] = "2.0.0"
	a.marketURL = mk.srv.URL + "/marketplace.json"
	a.loadMarketplace(true)
	a.installListing(a.mk.index.Find(id))
	if a.consent != nil || a.knownManifest(id) != nil || a.mk.installing != "" {
		t.Fatal("a package for the wrong version reached installation")
	}
	if len(a.toasts) == 0 || !strings.Contains(a.toasts[len(a.toasts)-1].text, locales.MarketVersionMismatch("2.0.0", "1.0.0")) {
		t.Fatalf("the mismatch was not reported: %+v", a.toasts)
	}
}

func TestMarketplaceRejectsUndeclaredDownloadPlugin(t *testing.T) {
	const id = "org.example.tool"
	a := newTestApp(t)
	useTestPluginDir(t, a)
	mk := newFakeMarketplace(t)
	mk.publish(id, "Example tool", "1.0.0", "library", 0, "")
	// The listing doesn't declare bookSource, but the package does.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, contents := range map[string]string{
		"plugin.json": `{"id":"org.example.tool","name":"Example tool","version":"1.0.0","api":1,
			"capabilities":{"bookSource":{}},"permissions":{"hosts":["books.example"]}}`,
		"main.js": `export function search() { return {items: []} }`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	mk.packages[id+"-1.0.0"] = buf.Bytes()
	a.marketURL = mk.srv.URL + "/marketplace.json"
	a.loadMarketplace(true)
	a.installListing(a.mk.index.Find(id))
	if a.consent != nil || a.knownManifest(id) != nil || a.mk.installing != "" {
		t.Fatal("a download plugin reached installation")
	}
	if len(a.toasts) == 0 || !strings.Contains(a.toasts[len(a.toasts)-1].text, locales.PluginsStatusDownloadUnsupported()) {
		t.Fatalf("the unsupported capability was not reported: %+v", a.toasts)
	}
}
