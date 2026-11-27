package bookconvert

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParsePages(t *testing.T) {
	got, err := ParsePages("６、１，３—５, 4")
	if err != nil || !slices.Equal(got, []int{1, 3, 4, 5, 6}) {
		t.Fatalf("pages = %v, %v", got, err)
	}
	for _, invalid := range []string{"0", "3-1", "3001", "1-21", "1, ,4"} {
		if _, err := ParsePages(invalid); err == nil {
			t.Errorf("accepted invalid page range %q", invalid)
		}
	}
}

func TestCopyNewPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "source.epub"), filepath.Join(dir, "output.epub")
	if err := os.WriteFile(from, []byte("converted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyNew(t.Context(), from, to); !os.IsExist(err) {
		t.Fatalf("expected existing output error, got %v", err)
	}
	if data, err := os.ReadFile(to); err != nil || string(data) != "existing" {
		t.Fatalf("output changed: %q, %v", data, err)
	}
}

func TestPublishOutputNeverReplacesConcurrentWinner(t *testing.T) {
	dir := t.TempDir()
	inputs := []string{filepath.Join(dir, "one"), filepath.Join(dir, "two")}
	target := filepath.Join(dir, "output.epub")
	results := make(chan error, len(inputs))
	for i, path := range inputs {
		if err := os.WriteFile(path, []byte{byte(i + 1)}, 0o600); err != nil {
			t.Fatal(err)
		}
		go func() { results <- publishOutput(t.Context(), path, target) }()
	}
	var successes int
	for range inputs {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatalf("unexpected publication error: %v", err)
		}
	}
	data, err := os.ReadFile(target)
	if err != nil || successes != 1 || len(data) != 1 {
		t.Fatalf("successes = %d, output %v, err %v", successes, data, err)
	}
}

func TestRequestUsesLibraryFormatForRenamedBackingFile(t *testing.T) {
	req := Request{Input: "content.bin", Output: "out.epub", sourceFormat: "TXT"}
	if err := req.validate(); err != nil {
		t.Fatal(err)
	}
	req.sourceFormat = ""
	if err := req.validate(); err == nil {
		t.Fatal("unexpected acceptance of standalone BIN input")
	}
}
