package plugin

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageAssets(t *testing.T) {
	dir := write(t, testManifest, `export function search() {
	    const plain = new Uint8Array(dalibre.assets.read("plain.bin"));
	    const decoded = new Uint8Array(dalibre.assets.read("dict/value.gz", "gzip"));
	    for (const path of ["../plugin.json", "/plain.bin", "dict/../plain.bin", "dict\\value.gz"]) {
	        let rejected = false;
	        try { dalibre.assets.read(path); } catch { rejected = true; }
	        if (!rejected) throw new Error("allowed invalid path " + path);
	    }
	    return { items: [{ id: "asset", title: [...plain, ...decoded].join(","), formats: [{ format: "EPUB", url: "https://example.com/asset.epub" }] }] };
	}`)
	folder := filepath.Join(dir, "assets", "dict")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "plain.bin"), []byte{0, 255}, 0o644); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write([]byte{1, 127, 128}); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "value.gz"), compressed.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	page, err := p.Search(t.Context(), Query{Text: "asset"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "0,255,1,127,128" {
		t.Fatalf("unexpected asset data: %+v", page.Items)
	}
}
