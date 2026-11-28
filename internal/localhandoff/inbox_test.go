package localhandoff

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testID = "9fb7dbcf-1cb1-4340-8e9f-235b0f914253"

func newPacket(t *testing.T, inbox string, m Manifest, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	m.Version, m.Bytes, m.SHA256 = 1, int64(len(data)), hex.EncodeToString(sum[:])
	directory := filepath.Join(inbox, m.ID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, manifestName), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	filename := m.Filename
	if !safeBookName(filename) {
		filename = "book.epub"
	}
	if err := os.WriteFile(filepath.Join(directory, filename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestSelectedInboxAndOriginalFilename(t *testing.T) {
	t.Setenv("ZLIB_CALIBRENO_HANDOFF_DIR", "")
	preferences := filepath.Join(t.TempDir(), "preferences.json")
	t.Setenv("ZLIB_CALIBRENO_HANDOFF_PREFERENCES_PATH", preferences)
	inbox := filepath.Join(t.TempDir(), "豆瓣 待整理")
	if err := os.Mkdir(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	conf, err := json.Marshal(map[string]any{"version": 1, "directory": inbox})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preferences, conf, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := DefaultRoot(); err != nil || got != inbox {
		t.Fatalf("selected root = %q, %v", got, err)
	}
	if RecognizesInbox(inbox) {
		t.Fatal("an ordinary empty directory is not an inbox")
	}
	if err := os.WriteFile(filepath.Join(inbox, markerName), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !RecognizesInbox(inbox) || !IsPacketDirectory(filepath.Join(inbox, ".partial-new")) {
		t.Fatal("the marker must recognize selected folders and pending packets")
	}
	name := "手島 さくら #1.EPUB"
	data := []byte("unchanged book bytes")
	packet := newPacket(t, inbox, Manifest{ID: testID, Filename: name, SourceName: name}, data)
	messages, err := Scan(inbox)
	if err != nil || len(messages) != 1 || messages[0].Filename != name || messages[0].SourceName != name {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	snapshot, clean, err := Snapshot(inbox, messages[0])
	if err != nil {
		t.Fatal(err)
	}
	defer clean()
	if filepath.Base(snapshot) != name {
		t.Fatalf("snapshot filename = %q, want %q", filepath.Base(snapshot), name)
	}
	if got, err := os.ReadFile(filepath.Join(packet, name)); err != nil || string(got) != string(data) {
		t.Fatalf("inbox file changed: %q, %v", got, err)
	}
	if err := os.Remove(filepath.Join(inbox, markerName)); err != nil {
		t.Fatal(err)
	}
	if !RecognizesInbox(inbox) || !IsPacketDirectory(packet) {
		t.Fatal("a valid unmarked legacy or moved inbox must be recognized")
	}
}

func TestPublishedInboxRoundTripAndReceiptReplacement(t *testing.T) {
	inbox := t.TempDir()
	book := []byte("offline fixture · 繁體")
	directory := newPacket(t, inbox, Manifest{ID: testID, Filename: "book.epub", SourceName: "測試 書.epub"}, book)
	if err := os.Mkdir(filepath.Join(inbox, ".partial-"+testID), 0o700); err != nil {
		t.Fatal(err)
	}
	messages, err := Scan(inbox)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	m := messages[0]
	p, clean, err := Snapshot(inbox, m)
	if err != nil {
		t.Fatal(err)
	}
	defer clean()
	data, err := os.ReadFile(p)
	if err != nil || string(data) != string(book) || filepath.Base(p) != m.SourceName || filepath.Dir(p) == directory {
		t.Fatalf("snapshot = %q, %q, %v", p, data, err)
	}
	receipt := Receipt{LibraryID: "3378779f-3618-4113-b6ce-ddbb3d7dd9d7", Status: "review"}
	owner := Receiver{StoreID: rand.Text(), LibraryKey: strings.Repeat("a", 64)}
	packet, err := LockPacket(inbox, m)
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	if err := packet.Reserve(owner, receipt.LibraryID, false); err != nil {
		t.Fatal(err)
	}
	if err := packet.WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Status, receipt.BookID = "added", 42
	if err := packet.WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(directory, receiptName))
	if err != nil {
		t.Fatal(err)
	}
	var got Receipt
	receipt.Version, receipt.ID, receipt.SHA256 = 1, testID, m.SHA256
	if err := json.Unmarshal(data, &got); err != nil || got != receipt {
		t.Fatalf("receipt = %+v, %v", got, err)
	}
}

func TestDeliveryReservationSurvivesLockReleaseBeforeTheBatchExists(t *testing.T) {
	inbox := t.TempDir()
	newPacket(t, inbox, Manifest{ID: testID, Filename: "book.epub"}, []byte("waiting fixture"))
	messages, err := Scan(inbox)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	m := messages[0]
	owner := Receiver{StoreID: rand.Text(), LibraryKey: strings.Repeat("a", 64)}
	otherStore := Receiver{StoreID: rand.Text(), LibraryKey: owner.LibraryKey}
	libraryID := "3378779f-3618-4113-b6ce-ddbb3d7dd9d7"
	packet, err := LockPacket(inbox, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := packet.Reserve(owner, libraryID, false); err != nil {
		packet.Close()
		t.Fatal(err)
	}
	if second, err := LockPacket(inbox, m); !errors.Is(err, ErrBusy) {
		if second != nil {
			second.Close()
		}
		packet.Close()
		t.Fatalf("concurrent receiver got %v, expected a busy packet", err)
	}
	packet.Close()

	reopened, err := LockPacket(inbox, m)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Reserve(otherStore, libraryID, false); !errors.Is(err, ErrClaimed) {
		t.Fatalf("same library and different batch store: %v", err)
	}
	if err := reopened.Reserve(owner, libraryID, false); err != nil {
		t.Fatalf("owner could not resume before creating its batch: %v", err)
	}
	if err := reopened.WriteReceipt(Receipt{Status: "added", BookID: 42}); err != nil {
		t.Fatal(err)
	}
}

func TestInboxRejectsEscapingNamesAndChangedBytes(t *testing.T) {
	inbox := t.TempDir()
	directory := newPacket(t, inbox, Manifest{ID: testID, Filename: "book.epub", SourceName: "LPT¹.epub"}, []byte("safe"))
	newPacket(t, inbox, Manifest{ID: "e8f5aa99-483c-4f31-9484-ae88d1837f39", Filename: "../book.epub"}, []byte("wrong"))
	messages, err := Scan(inbox)
	if err == nil || len(messages) != 1 || messages[0].SourceName != "" {
		t.Fatalf("messages = %+v, %v", messages, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "book.epub"), []byte("swap"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, clean, err := Snapshot(inbox, messages[0]); err == nil {
		clean()
		t.Fatal("changed handoff accepted")
	}
}
