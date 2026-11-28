package weread

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryRecoversAndConfirmsPendingImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weread", "history.json")
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := h.Begin("Example", "same-digest")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusSending || first.ID == "" || first.At.IsZero() {
		t.Fatalf("begin returned %+v", first)
	}
	if _, dup := h.Duplicate(first.Digest); dup {
		t.Fatal("unfinished send should not be reported as a completed attempt")
	}
	if err := h.Confirm(first.ID); err == nil {
		t.Fatal("active send should not be confirmable")
	}
	if err := h.Finish(first.ID, StatusManual); err != nil {
		t.Fatal(err)
	}
	if _, dup := h.Duplicate(first.Digest); dup {
		t.Fatal("manual fallback should not prevent the next automatic send")
	}

	interrupted, err := h.Begin(first.Title, first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if r, dup := loaded.Duplicate(first.Digest); !dup || r.ID != interrupted.ID || r.Status != StatusPending {
		t.Fatalf("recovered duplicate = %+v, %v", r, dup)
	}
	if err := loaded.Confirm(interrupted.ID); err != nil {
		t.Fatal(err)
	}
	confirmed, dup := loaded.Duplicate(first.Digest)
	if !dup || confirmed.Status != StatusImported || confirmed.ConfirmedAt.IsZero() {
		t.Fatalf("confirmed duplicate = %+v, %v", confirmed, dup)
	}
	if err := loaded.Confirm(interrupted.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err = OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	recent := loaded.Recent()
	if len(recent) != 2 || recent[0] != confirmed || recent[1].Status != StatusManual {
		t.Fatalf("reopened records = %+v", recent)
	}
	recent[0].Title = "edited snapshot"
	if loaded.Recent()[0].Title != first.Title {
		t.Fatal("caller changed saved history through snapshot")
	}
}

func TestHistoryRetainsOutcomeIfSavingFailsAfterUpload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := h.Begin("Example", "digest")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := h.Finish(r.ID, StatusImported); err == nil {
		t.Fatal("expected disk replacement to fail")
	}
	if duplicate, found := h.Duplicate(r.Digest); !found || duplicate.Status != StatusImported {
		t.Fatalf("current session lost the known imported state: %+v, %v", duplicate, found)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, found := restarted.Duplicate(r.Digest); !found || duplicate.Status != StatusPending {
		t.Fatalf("restart lost the uncertain upload: %+v, %v", duplicate, found)
	}
}

func TestHistoryLimit(t *testing.T) {
	h, err := OpenHistory("")
	if err != nil {
		t.Fatal(err)
	}
	for i := range historyLimit + 1 {
		if _, err := h.Begin("Example", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if recent := h.Recent(); len(recent) != historyLimit || recent[0].Digest != fmt.Sprint(historyLimit) || recent[len(recent)-1].Digest != "1" {
		t.Fatalf("history retained %d records", len(recent))
	}
}

func TestFileDigestMatchesByContents(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "one.epub"), filepath.Join(dir, "two.epub"), filepath.Join(dir, "different.epub")}
	contents := []string{"some EPUB bytes", "some EPUB bytes", "other EPUB bytes"}
	var digests []string
	for i, path := range paths {
		if err := os.WriteFile(path, []byte(contents[i]), 0o600); err != nil {
			t.Fatal(err)
		}
		digest, err := FileDigest(path)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, digest)
	}
	if len(digests[0]) != 64 || digests[0] != digests[1] || digests[0] == digests[2] {
		t.Fatalf("incorrect file digests %v", digests)
	}
	if _, err := FileDigest(dir); err == nil {
		t.Fatal("directory is not a transferable file")
	}
}

func TestHistoryRejectsUnsupportedVersionWithoutReplacingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	data, err := json.Marshal(historyFile{Version: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenHistory(path); err == nil {
		t.Fatal("read succeeded for unsupported file version")
	}
	if persisted, err := os.ReadFile(path); err != nil || string(persisted) != string(data) {
		t.Fatalf("unsupported history data changed: %q, %v", persisted, err)
	}
}
