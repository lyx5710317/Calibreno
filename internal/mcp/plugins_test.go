package mcp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An agent starts a plugin from the template, writes it, adds it, tries
// it, and reloads it after an edit.
func TestPluginTools(t *testing.T) {
	h := &testHost{lib: testLibrary(t)}
	cs := connect(t, New(h, "test", true))
	dir := filepath.Join(t.TempDir(), "my-plugin")

	if msg := call(t, cs, "create_plugin", map[string]any{"path": "relative", "id": "com.example.mine"}, nil); !strings.Contains(msg, "absolute") {
		t.Errorf("relative path: %q", msg)
	}
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "Not An Id"}, nil); msg == "" {
		t.Error("a bad id made a plugin")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a bad id left a folder")
	}
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "dalibre.mine"}, nil); !strings.Contains(msg, "kept for plugins") {
		t.Errorf("reserved id: %q", msg)
	}
	var co createOut
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "com.example.mine", "name": "Mine"}, &co); msg != "" {
		t.Fatal(msg)
	}
	if co.Folder != dir || co.Docs != PluginDocs || !slices.Contains(co.Files, "plugin.json") || !slices.Contains(co.Files, "dalibre.d.ts") {
		t.Fatalf("created %+v", co)
	}
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "com.example.mine"}, nil); !strings.Contains(msg, "isn’t empty") {
		t.Errorf("a folder with files: %q", msg)
	}

	script := func(title string) {
		os.WriteFile(filepath.Join(dir, "main.js"), []byte(`
			export function identify(book) {
				console.log("looking up", book.title, book.language);
				if (book.title === "boom") throw dalibre.error("unavailable", "down");
				if (book.title === "bug") null.x;
				return [{ id: "1", title: "`+title+`" }, { id: "2", title: "" }];
			}
		`), 0o644)
	}
	script("First")
	var ao addPluginOut
	if msg := call(t, cs, "add_plugin", map[string]any{"path": dir}, &ao); msg != "" {
		t.Fatal(msg)
	}
	if ao.Plugin.ID != "com.example.mine" || ao.Plugin.State != PluginLoaded || ao.Plugin.Folder != dir {
		t.Fatalf("added %+v", ao)
	}

	identify := func(title string) map[string]any {
		return map[string]any{"id": "com.example.mine", "call": "identify", "book": map[string]any{"title": title, "language": "fr"}}
	}
	var to struct {
		OK     bool             `json:"ok"`
		Result []map[string]any `json:"result"`
		Error  *TestError       `json:"error"`
		Logs   []string         `json:"logs"`
	}
	if msg := call(t, cs, "test_plugin", identify("dune"), &to); msg != "" {
		t.Fatal(msg)
	}
	if !to.OK || len(to.Result) != 1 || to.Result[0]["title"] != "First" {
		t.Fatalf("identify: %+v", to)
	}
	// What the script logged, and what Dalibre dropped of its answer.
	logs := strings.Join(to.Logs, "\n")
	if !strings.Contains(logs, "looking up dune fr") || !strings.Contains(logs, "warning") {
		t.Errorf("logs: %q", to.Logs)
	}

	// The script's errors come back with their kind and stack.
	to.Error, to.OK = nil, false
	call(t, cs, "test_plugin", identify("boom"), &to)
	if to.OK || to.Error == nil || to.Error.Kind != "unavailable" || !strings.Contains(to.Error.Message, "down") {
		t.Errorf("thrown: %+v", to.Error)
	}
	to.Error = nil
	call(t, cs, "test_plugin", identify("bug"), &to)
	if to.Error == nil || to.Error.Kind != "script" || !strings.Contains(to.Error.Stack, "main.js") {
		t.Errorf("a bug: %+v", to.Error)
	}

	// Mistakes in the call are the tool's errors.
	for args, want := range map[*map[string]any]string{
		{"id": "com.example.mine", "call": "search"}:    "not a book source",
		{"id": "com.example.mine", "call": "identify"}:  "needs book",
		{"id": "com.example.mine", "call": "fly"}:       "no call",
		{"id": "com.example.other", "call": "identify"}: "add_plugin",
	} {
		if msg := call(t, cs, "test_plugin", *args, nil); !strings.Contains(msg, want) {
			t.Errorf("%v: %q, want %q", *args, msg, want)
		}
	}

	// An edit shows after reloading.
	script("Second")
	var lo PluginList
	if msg := call(t, cs, "reload_plugins", map[string]any{}, &lo); msg != "" || len(lo.Plugins) != 1 {
		t.Fatalf("reload: %q %+v", msg, lo)
	}
	call(t, cs, "test_plugin", identify("dune"), &to)
	if len(to.Result) != 1 || to.Result[0]["title"] != "Second" {
		t.Errorf("after the edit: %+v", to.Result)
	}

	var po pluginOut
	call(t, cs, "enable_plugin", map[string]any{"id": "com.example.mine", "enabled": false}, &po)
	if po.Plugins[0].State != PluginDisabled {
		t.Errorf("turned off: %+v", po)
	}
	if msg := call(t, cs, "test_plugin", identify("dune"), nil); !strings.Contains(msg, "it is disabled") {
		t.Errorf("testing one turned off: %q", msg)
	}
	call(t, cs, "enable_plugin", map[string]any{"id": "com.example.mine"}, &po)
	if po.Plugins[0].State != PluginLoaded {
		t.Errorf("turned on: %+v", po)
	}
	call(t, cs, "remove_plugin", map[string]any{"id": "com.example.mine"}, &po)
	call(t, cs, "list_plugins", map[string]any{}, &lo)
	if len(po.Plugins) != 0 || len(lo.Plugins) != 0 {
		t.Errorf("removed: %+v %+v", po, lo)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.js")); err != nil {
		t.Error("removing deleted the folder")
	}
}

// Without changes allowed, and without Dalibre's window, there are no
// plugin tools.
func TestPluginToolsNeedChanges(t *testing.T) {
	s := New(&testHost{lib: testLibrary(t)}, "test", false)
	cs := connect(t, s)
	if got := toolNames(t, cs); slices.Contains(got, "add_plugin") {
		t.Errorf("read only: %v", got)
	}
	s.SetAllowChanges(true)
	if got := toolNames(t, cs); !slices.Contains(got, "add_plugin") {
		t.Errorf("changes allowed: %v", got)
	}
	s.SetAllowChanges(false)
	if got := toolNames(t, cs); slices.Contains(got, "test_plugin") {
		t.Errorf("read only again: %v", got)
	}
}
