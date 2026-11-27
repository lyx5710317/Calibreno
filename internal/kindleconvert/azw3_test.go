package kindleconvert

import (
	"archive/zip"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dalibre/internal/epub"
	"dalibre/internal/plugin"
)

func writeTestEPUB(t *testing.T, extra map[string]string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "中日文.epub")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	add := func(name, value string) {
		t.Helper()
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	add("book.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:ab0fbac6-6a59-473e-aa6f-d94fc64c6c97</dc:identifier><dc:title>中日文</dc:title><dc:creator>Sample Author</dc:creator><dc:language>zh</dc:language><meta property="dcterms:modified">2026-01-01T00:00:00Z</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="text" href="body.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="text"/></spine></package>`)
	add("nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>目录</title></head><body><nav epub:type="toc"><ol><li><a href="body.xhtml">第一章</a></li></ol></nav></body></html>`)
	add("body.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>第一章</title></head><body><h1>第一章</h1><p>天地玄黄。吾輩は猫である。</p></body></html>`)
	for name, value := range extra {
		add(name, value)
	}
	if err := errors.Join(z.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestCheckEPUBProtection(t *testing.T) {
	const enc = `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:e="http://www.w3.org/2001/04/xmlenc#"><e:EncryptedData><e:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/><e:CipherData><e:CipherReference URI="%s"/></e:CipherData></e:EncryptedData></encryption>`
	fonts := writeTestEPUB(t, map[string]string{
		"META-INF/encryption.xml": strings.Replace(enc, "%s", "my%20font.otf", 1),
		"my font.otf":             "obfuscated font",
	})
	if err := checkEPUB(t.Context(), fonts); err != nil {
		t.Fatalf("font obfuscation: %v", err)
	}
	protected := writeTestEPUB(t, map[string]string{
		"META-INF/encryption.xml": strings.Replace(enc, "%s", "body.xhtml", 1),
	})
	if err := checkEPUB(t.Context(), protected); !errors.Is(err, epub.ErrDRM) {
		t.Fatalf("expected protected content error, got %v", err)
	}
}

// Set CALIBRENO_TEST_KINDLE_ZIP to the official pinned release archive to
// exercise download verification, conversion and offline cache reuse.
func TestConvertWithPinnedWriter(t *testing.T) {
	archive := os.Getenv("CALIBRENO_TEST_KINDLE_ZIP")
	if archive == "" || runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("requires the Windows x64 Epub3-Kindle release archive")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, archive)
	}))
	defer server.Close()
	build := windowsBuild
	build.URL = server.URL + "/epub3-kindle.zip"
	store := &plugin.ToolStore{Dir: t.TempDir(), Client: server.Client()}
	input := writeTestEPUB(t, nil)
	for range 2 {
		file, _, err := convert(t.Context(), store, build, input, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		book, err := epub.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, chapter := range book.Spine() {
			content, err := book.Read(chapter.Name)
			if err != nil {
				t.Fatal(err)
			}
			found = found || strings.Contains(string(content), "天地玄黄。吾輩は猫である。")
		}
		if !found {
			t.Fatal("missing Chinese/Japanese text in converted AZW3")
		}
		server.Close()
	}
}
