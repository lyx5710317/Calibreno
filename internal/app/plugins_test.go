package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/mcp"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// fakeWeb answers the plugin's requests without a network.
type fakeWeb struct{ h http.Handler }

func (f fakeWeb) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Host == "" {
		r.Host = r.URL.Host
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

func TestBookDownloadPluginsUnsupported(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	const id = "com.example.books"
	folder := t.TempDir()
	manifest := filepath.Join(folder, "plugin.json")
	for _, capabilities := range []string{`["source"]`, `{"bookSource":{},"metadata":{}}`} {
		if err := os.WriteFile(manifest, []byte(`{"id":"`+id+`","name":"Example Books","version":"1.0.0","api":1,
			"capabilities":`+capabilities+`,"permissions":{"library":"read","hosts":["a.example"]}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "main.js"), []byte(`exports.search = () => ({items: []}); exports.identify = () => []`), 0o644); err != nil {
			t.Fatal(err)
		}
		archive, err := plugin.Pack(folder, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		a.installPlugin(archive)
		a.finishInstall(archive)
		a.linkPlugin(folder)
		if _, err := (mcpHost{a}).AddPlugin(t.Context(), folder); err == nil || err.Error() != locales.PluginsStatusDownloadUnsupported() {
			t.Fatalf("MCP added unsupported plugin: %v", err)
		}
		if _, err := os.Stat(filepath.Join(a.pluginDir, id)); !os.IsNotExist(err) || a.consent != nil || a.settings.Plugins[id] != nil {
			t.Fatalf("unsupported plugin installed, configured or waiting for consent: %v", err)
		}
		// Simulate a previous install and its already granted permissions.
		m, err := plugin.Install(archive, a.pluginDir)
		if err != nil {
			t.Fatal(err)
		}
		st := a.settings.Plugin(id)
		st.Granted = grantFor(m)
		st.Values = map[string]string{"key": "saved"}
		for _, path := range []string{"", folder} {
			st.Path = path
			a.loadPlugins()
			if len(a.sources()) != 0 || len(a.plugins) != 0 || len(a.pluginsPending) != 0 || len(a.pluginErrs) != 0 {
				t.Fatal("existing unsupported plugin was offered, run, or queued for approval")
			}
			known, err := a.knownPlugin(id)
			if err != nil {
				t.Fatal(err)
			}
			if status, _, loaded := a.pluginStatus(known); status != locales.PluginsStatusDownloadUnsupported() || loaded {
				t.Fatalf("plugin status: %q, loaded %v", status, loaded)
			}
			list := a.pluginList()
			if len(list.Plugins) != 1 || list.Plugins[0].State != mcp.PluginUnsupported {
				t.Fatalf("MCP plugin state: %+v", list.Plugins)
			}
			if err := (mcpHost{a}).SetPluginEnabled(t.Context(), id, true); err == nil || err.Error() != locales.PluginsStatusDownloadUnsupported() {
				t.Fatalf("MCP enabled unsupported plugin: %v", err)
			}
		}
		if _, err := os.Stat(filepath.Join(a.pluginDir, id, "plugin.json")); err != nil || st.Values["key"] != "saved" {
			t.Fatalf("legacy files or settings were removed: %v", err)
		}
		if err := plugin.Uninstall(a.pluginDir, id); err != nil {
			t.Fatal(err)
		}
		delete(a.settings.Plugins, id)
	}
}

// A plugin loads only once the user grants its permissions, and an
// update asking for more waits for the user again.
func TestPluginConsent(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	pack := func(version, hosts string) string {
		src := filepath.Join(t.TempDir(), "src")
		os.MkdirAll(src, 0o755)
		os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.books","name":"Example","version":"`+version+
			`","api":1,"capabilities":["metadata"],"permissions":{"library":"read","hosts":[`+hosts+`]}}`), 0o644)
		os.WriteFile(filepath.Join(src, "main.js"), []byte(`exports.identify = () => []`), 0o644)
		pkg, err := plugin.Pack(src, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}

	a.installPlugin(pack("1.0.0", `"a.example"`))
	if a.consent == nil || len(a.plugins) != 0 {
		t.Fatal("installed without asking")
	}
	a.grantConsent()
	if len(a.plugins) != 1 || a.plugins[0].Manifest.Version != "1.0.0" {
		t.Fatalf("plugins %d after granting", len(a.plugins))
	}

	// An update within what was granted installs at once.
	a.installPlugin(pack("1.1.0", `"a.example"`))
	if a.consent != nil || a.plugins[0].Manifest.Version != "1.1.0" {
		t.Fatal("an update needing nothing new asked")
	}

	// One asking for a new host is asked about; installed by hand, it
	// waits unloaded.
	if _, err := plugin.Install(pack("2.0.0", `"a.example","b.example"`), a.pluginDir); err != nil {
		t.Fatal(err)
	}
	a.loadPlugins()
	if len(a.plugins) != 0 || len(a.pluginsPending) != 1 {
		t.Fatalf("plugins %d, pending %d", len(a.plugins), len(a.pluginsPending))
	}
	a.askConsent("com.example.books")
	a.grantConsent()
	if len(a.plugins) != 1 || len(a.pluginsPending) != 0 {
		t.Fatal("not loaded after granting")
	}

	a.setPluginEnabled("com.example.books", false)
	if len(a.plugins) != 0 {
		t.Fatal("a disabled plugin loaded")
	}
	a.uninstallPlugin("com.example.books")
	if _, ok := a.settings.Plugins["com.example.books"]; ok {
		t.Fatal("uninstalling kept the plugin's state")
	}
}

// A plugin added from a folder loads from it, as it is, after the user
// grants it; removing it leaves the folder alone.
func TestLinkedPlugin(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	src := filepath.Join(t.TempDir(), "my-source")
	os.MkdirAll(src, 0o755)
	manifest := func(version string) {
		os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.dev","name":"Dev","version":"`+version+
			`","api":1,"capabilities":["metadata"],"permissions":{"library":"read","hosts":["a.example"]}}`), 0o644)
	}
	manifest("0.1.0")
	os.WriteFile(filepath.Join(src, "main.js"), []byte(`exports.identify = () => []`), 0o644)

	a.installPlugin(src)
	if a.consent == nil {
		t.Fatal("linked without asking")
	}
	a.grantConsent()
	if len(a.plugins) != 1 || a.settings.Plugins["com.example.dev"].Path != src {
		t.Fatalf("plugins %d, state %+v", len(a.plugins), a.settings.Plugins["com.example.dev"])
	}
	if len(a.pluginsKnown) != 1 || a.pluginsKnown[0].linked != src {
		t.Fatalf("known %+v", a.pluginsKnown)
	}
	// Only its data folder (.data) is there, as for every plugin.
	if entries, _ := os.ReadDir(a.pluginDir); len(entries) != 1 || entries[0].Name() != ".data" {
		t.Fatalf("copied into the plugins folder: %v", entries)
	}

	// An edit shows on the next load, with no install.
	manifest("0.2.0")
	a.loadPlugins()
	if a.plugins[0].Manifest.Version != "0.2.0" {
		t.Fatalf("version %s after editing", a.plugins[0].Manifest.Version)
	}

	a.uninstallPlugin("com.example.dev")
	if len(a.plugins) != 0 || a.settings.Plugins["com.example.dev"] != nil {
		t.Fatal("still loaded after removing")
	}
	if _, err := os.Stat(filepath.Join(src, "main.js")); err != nil {
		t.Fatal("removing deleted the folder")
	}
}

// A plugin's secret settings live in the credential store, never in
// settings.json; one saved there before is moved.
func TestPluginSecrets(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	store := memorySecrets{}
	a.secrets = store
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.secret","name":"Secret","version":"1.0.0","api":1,
		"capabilities":["metadata"],"permissions":{"library":"read","hosts":["a.example"]},
		"settings":[{"key":"email","title":"Email","required":true},
			{"key":"password","title":"Password","type":"secret","required":true}]}`), 0o644)
	os.WriteFile(filepath.Join(src, "main.js"), []byte(`exports.identify = () => []`), 0o644)
	const id = "com.example.secret"
	key := plugin.SecretKey(id, "password")

	// Linked, granted, with a password saved in settings.json as before.
	st := a.settings.Plugin(id)
	st.Path = src
	st.Granted = &settings.Grant{Library: plugin.LibraryRead, Hosts: []string{"a.example"}}
	st.Values = map[string]string{"email": "me@example.com", "password": "old"}
	a.loadPlugins()
	if len(a.plugins) != 1 || a.plugins[0].MissingSettings() != nil {
		t.Fatalf("plugins %d, missing %v", len(a.plugins), a.plugins[0].MissingSettings())
	}
	if _, ok := a.settings.Plugins[id].Values["password"]; ok || store[key] != "old" {
		t.Fatalf("not moved: values %v, store %v", a.settings.Plugins[id].Values, store)
	}
	if data, _ := json.Marshal(a.settings); strings.Contains(string(data), `"old"`) {
		t.Fatal("the password is still in the settings")
	}

	// Saving puts the password in the store; the email stays in settings.
	a.setPluginValues(id, map[string]string{"email": "me@example.com", "password": "new"})
	if store[key] != "new" || a.settings.Plugins[id].Values["email"] != "me@example.com" || a.settings.Plugins[id].Values["password"] != "" {
		t.Fatalf("saved: values %v, store %v", a.settings.Plugins[id].Values, store)
	}
	if a.pluginSecrets[id]["password"] != "new" || len(a.plugins) != 1 || a.plugins[0].MissingSettings() != nil {
		t.Fatal("not loaded with the new password")
	}

	// Clearing it removes it; removing the plugin forgets it.
	a.setPluginValues(id, map[string]string{"email": "me@example.com"})
	if _, ok := store[key]; ok || a.plugins[0].MissingSettings() == nil {
		t.Fatalf("cleared: store %v", store)
	}
	a.setPluginValues(id, map[string]string{"email": "me@example.com", "password": "again"})
	a.uninstallPlugin(id)
	if _, ok := store[key]; ok {
		t.Fatal("removing kept the password")
	}
}

func TestFolderWatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.js"), []byte("a"), 0o644)
	w := newFolderWatch([]string{dir})
	if ch, _ := w.changed(); ch {
		t.Fatal("changed with nothing changed")
	}
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644)
	if ch, _ := w.changed(); ch {
		t.Fatal("a hidden file counted")
	}
	os.WriteFile(filepath.Join(dir, "main.js"), []byte("abc"), 0o644)
	ch, settled := w.changed()
	if !ch {
		t.Fatal("an edit not seen")
	}
	os.WriteFile(filepath.Join(dir, "lib.js"), []byte("x"), 0o644)
	if settled() {
		t.Fatal("settled while still changing")
	}
	if !settled() {
		t.Fatal("not settled once quiet")
	}
	if ch, _ := w.changed(); ch {
		t.Fatal("the same change seen twice")
	}
}

// The built-in plugins load with what they ask for, and can be turned off
// but stay listed.
func TestBuiltinPlugins(t *testing.T) {
	a := newTestApp(t)
	a.builtins = true
	a.pluginTransport = fakeWeb{http.NotFoundHandler()}
	a.loadPlugins()
	if a.pluginByID("dalibre.gutenberg") != nil || a.pluginByID("dalibre.neodb") == nil || a.pluginByID("dalibre.googlebooks") == nil || len(a.pluginsKnown) != 2 || !a.pluginsKnown[0].builtin {
		t.Fatalf("plugins %v, known %v, errors %v", a.plugins, a.pluginsKnown, a.pluginErrs)
	}
	if len(a.sources()) != 0 {
		t.Fatalf("default book sources: %v", a.sources())
	}
	a.setPluginEnabled("dalibre.neodb", false)
	if len(a.plugins) != 1 || len(a.pluginsKnown) != 2 {
		t.Fatalf("plugins %d, known %d after turning off", len(a.plugins), len(a.pluginsKnown))
	}
	a.setPluginEnabled("dalibre.neodb", true)
	if a.pluginByID("dalibre.neodb") == nil {
		t.Fatal("not back on")
	}
}

func TestPluginGrantOpenAndCovers(t *testing.T) {
	m := &plugin.Manifest{
		ID:      "com.example.open",
		Version: "1.0.0",
		Permissions: plugin.Permissions{
			CoverHosts: plugin.CoversAny,
			OpenHosts:  plugin.OpenAny,
		},
	}
	g := grantFor(m)
	if g.Covers != plugin.CoversAny {
		t.Fatalf("grantFor Covers = %q, want %q", g.Covers, plugin.CoversAny)
	}
	if g.Open != plugin.OpenAny {
		t.Fatalf("grantFor Open = %q, want %q", g.Open, plugin.OpenAny)
	}
	if !granted(&settings.PluginState{Granted: g}, m) {
		t.Fatal("granted returned false for matching Open and Covers grants")
	}
	if granted(&settings.PluginState{Granted: &settings.Grant{Covers: plugin.CoversAny}}, m) {
		t.Fatal("granted returned true when Open grant is missing")
	}
	if granted(&settings.PluginState{Granted: &settings.Grant{Open: plugin.OpenAny}}, m) {
		t.Fatal("granted returned true when Covers grant is missing")
	}
}
