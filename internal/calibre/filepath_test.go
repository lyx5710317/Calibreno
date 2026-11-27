package calibre

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLibraryFile(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Library")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Root: root}
	inside := filepath.Join(root, "inside.epub")
	outside := filepath.Join(parent, "outside.epub")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("sample"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := lib.ResolveLibraryFile(inside); err != nil || !withinLibrary(root, got) {
		t.Fatalf("in-library file returned %q, %v", got, err)
	}
	if _, err := lib.ResolveLibraryFile(outside); !errors.Is(err, ErrLibraryFileOutside) {
		t.Fatalf("outside file returned %v", err)
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(root, "outside.epub")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		if _, err := lib.ResolveLibraryFile(link); !errors.Is(err, ErrLibraryFileOutside) {
			t.Fatalf("symlink returned %v", err)
		}
	})
}
