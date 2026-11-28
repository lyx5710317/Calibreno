package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	plugsdk "dalibre/sdk"
)

// dalibre-plugin dev reaches the running app with its token and adds a
// plugin's folder.
func TestDial(t *testing.T) {
	h := &testHost{lib: testLibrary(t)}
	defer func() {
		for _, p := range h.added {
			p.Close()
		}
	}()
	s := New(h, "test", true)
	s.SetToken("secret")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "p")
	if _, err := plugsdk.Init(dir, "com.example.dial", "Dial"); err != nil {
		t.Fatal(err)
	}
	cs, err := Dial(ctx, srv.URL+Path, "secret", "dalibre-plugin", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	var out addPluginOut
	if err := CallTool(ctx, cs, "add_plugin", map[string]string{"path": dir}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Plugin.ID != "com.example.dial" || out.Plugin.State != PluginLoaded {
		t.Errorf("added %+v", out.Plugin)
	}
	if err := CallTool(ctx, cs, "add_plugin", map[string]string{"path": "relative"}, nil); err == nil {
		t.Error("a tool's failure isn't an error")
	}

	if _, err := Dial(ctx, srv.URL+Path, "wrong", "dalibre-plugin", "test"); err == nil {
		t.Error("a wrong token connected")
	}
	cs.Close()
	srv.Close()
	if _, err := Dial(ctx, srv.URL+Path, "secret", "dalibre-plugin", "test"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("nothing listening: %v", err)
	}
}

func TestDialDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer destination.Close()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+Path, http.StatusTemporaryRedirect)
	}))
	defer local.Close()
	if _, err := Dial(t.Context(), local.URL+Path, "secret", "test", "test"); err == nil {
		t.Fatal("redirected endpoint was treated as an MCP server")
	}
	if reached.Load() {
		t.Fatal("followed an MCP redirect")
	}
}
