package weread

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserUploadUsesOfficialFormAndNewReceipt(t *testing.T) {
	exe := installedBrowser()
	if exe == "" {
		t.Skip("Chrome or Edge is not installed")
	}
	var uploads atomic.Int32
	var loginRequired atomic.Bool
	loginRequired.Store(true)
	var variant atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<input type="file" accept=".epub" onchange="fetch('/unexpected')">`)
		if r.URL.Path == "/unexpected" {
			variant.Store(99)
		}
	}))
	defer outside.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/web/shelf":
			fmt.Fprint(w, `<button onclick="if (event.isTrusted) document.body.textContent='使用微信扫一扫登录'">传书到手机</button>`)
		case "/web/upload":
			if loginRequired.Load() {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			switch variant.Load() {
			case 1:
				fmt.Fprintf(w, `<iframe src="%s/"></iframe>`, outside.URL)
			case 2:
				http.Redirect(w, r, outside.URL+"/", http.StatusFound)
			case 3:
				fmt.Fprint(w, `<input type="file" accept=".pdf">`)
			default:
				fmt.Fprint(w, `<input type="file" accept="application/epub+zip" onchange="send(this)"><p>导入成功</p><p id="receipt"></p>
<script>
async function send(input) {
  const file = input.files[0];
  const response = await fetch('/fixture-receipt?name=' + encodeURIComponent(file.name), {method:'POST', body:file});
  document.getElementById('receipt').textContent = await response.text();
}
</script>`)
			}
		case "/fixture-receipt":
			data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil || len(data) < 2 || string(data[:2]) != "PK" || r.Method != http.MethodPost {
				t.Error("fixture did not receive the selected EPUB bytes")
			}
			uploads.Add(1)
			switch r.URL.Query().Get("name") {
			case "成功.epub":
				fmt.Fprint(w, "文件导入成功")
			case "失败.epub":
				fmt.Fprint(w, "导入失败，请查看文件")
			}
		}
	}))
	defer server.Close()

	profile := t.TempDir()
	client := NewClient(profile)
	defer client.Close()
	client.origin, client.executable, client.headless = server.URL, exe, true
	client.waitResult = 900 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	check := func(want Status) {
		t.Helper()
		status, err := client.Check(ctx)
		if status != want || err != nil {
			t.Fatalf("check result = %s, %v; want %s", status, err, want)
		}
	}
	check(StatusLoginRequired)
	firstTab := client.upload.ctx
	loginRequired.Store(false)
	if err := chromedp.Do(firstTab, chromedp.Navigate(server.URL+"/web/upload")); err != nil {
		t.Fatal(err)
	}
	check(StatusReady)
	if client.upload.ctx != firstTab {
		t.Fatal("the login and first upload did not use the same tab")
	}
	family := "chrome"
	if strings.Contains(strings.ToLower(filepath.Base(exe)), "edge") {
		family = "edge"
	}
	if _, err := os.Stat(filepath.Join(profile, family, "Default")); err != nil {
		t.Fatalf("isolated %s profile missing: %v", family, err)
	}
	for _, tc := range []struct {
		name   string
		status Status
	}{{"成功.epub", StatusImported}, {"失败.epub", StatusFailed}} {
		file := makeEPUB(t, tc.name, "application/epub+zip")
		if status, err := client.Send(ctx, file); status != tc.status || err != nil {
			t.Fatalf("send %s = %s, %v; want %s", tc.name, status, err, tc.status)
		}
	}

	check(StatusReady)
	pendingTab := client.upload.ctx
	file := makeEPUB(t, "等待.epub", "application/epub+zip")
	if status, err := client.Send(ctx, file); status != StatusPending || err != nil {
		t.Fatalf("old success text or absent receipt produced %s, %v", status, err)
	}
	if uploads.Load() != 3 {
		t.Fatalf("send triggered %d upload requests, want 3", uploads.Load())
	}
	if err := client.OpenShelf(ctx); err != nil {
		t.Fatal(err)
	}
	if client.shelf.ctx == pendingTab {
		t.Fatal("opening shelf reused the pending upload tab")
	}
	if url, err := chromedp.Run(pendingTab, chromedp.Location()); url != server.URL+"/web/upload" || err != nil {
		t.Fatalf("pending upload tab did not stay on upload page: %q, %v", url, err)
	}

	for _, scene := range []int32{1, 2, 3} {
		variant.Store(scene)
		if client.upload.ctx != nil {
			client.upload.cancel()
			client.upload = browserTab{}
		}
		if status, err := client.Send(ctx, file); status != StatusManual || err == nil {
			t.Fatalf("unexpected upload form %d yielded %s, %v", scene, status, err)
		}
		if uploads.Load() != 3 || variant.Load() != scene {
			t.Fatalf("unexpected upload on page variant %d", scene)
		}
	}
}
