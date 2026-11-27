package weread

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type epubEntry struct{ name, body string }

func makeEPUB(t *testing.T, name, mimetype string, extra ...epubEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entries := []epubEntry{
		{"mimetype", mimetype},
		{"META-INF/container.xml", "<container><rootfiles><rootfile full-path=\"Book.opf\"/></rootfiles></container>"},
		{"Book.opf", "<package version=\"3.0\"><manifest/><spine/></package>"},
	}
	for _, entry := range append(extra, entries...) {
		writer, err := archive.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(writer, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHandoffOpensOfficialPageAndRevealsOriginal(t *testing.T) {
	epub := makeEPUB(t, "一本书.EPUB", "application/epub+zip")
	var events []string
	err := Handoff(epub, func(url string) error {
		events = append(events, url)
		return nil
	}, func(path string) {
		events = append(events, path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://weread.qq.com/web/shelf", epub}; !slices.Equal(events, want) {
		t.Errorf("handoff events %q, want %q", events, want)
	}
}

func TestHandoffRejectsInvalidFileBeforeOpening(t *testing.T) {
	notZIP := filepath.Join(t.TempDir(), "renamed.epub")
	if err := os.WriteFile(notZIP, []byte("not an EPUB"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "folder.epub")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		notZIP,
		dir,
		makeEPUB(t, "book.epub", "application/some+zip"),
		makeEPUB(t, "book.zip", "application/epub+zip"),
		makeEPUB(t, "duplicate.epub", "application/epub+zip", epubEntry{"mimetype", "application/epub+zip"}),
	} {
		t.Run(filepath.Base(file), func(t *testing.T) {
			err := Handoff(file, func(string) error {
				t.Fatal("opened a browser for an invalid EPUB")
				return nil
			}, func(string) {
				t.Fatal("revealed an invalid EPUB")
			})
			if !errors.Is(err, ErrInvalidEPUB) {
				t.Errorf("got %v, want ErrInvalidEPUB", err)
			}
		})
	}
}

func TestHandoffReturnsBrowserFailure(t *testing.T) {
	epub := makeEPUB(t, "book.epub", "application/epub+zip")
	openErr := errors.New("no browser")
	err := Handoff(epub, func(string) error { return openErr }, func(string) {
		t.Fatal("revealed the file after browser failure")
	})
	if !errors.Is(err, openErr) {
		t.Errorf("got %v, want browser error", err)
	}
}
