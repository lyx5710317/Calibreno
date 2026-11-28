package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"dalibre/internal/batch"
	"dalibre/internal/calibre"
	"dalibre/internal/localhandoff"
	"github.com/google/uuid"
)

func TestZlibraryInboxRoutesChosenAndPreviousFolders(t *testing.T) {
	root := t.TempDir()
	oldInbox, current := filepath.Join(root, "旧收件箱"), filepath.Join(root, "待整理")
	for _, dir := range []string{oldInbox, current} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := publishTestHandoff(t, oldInbox, "旧文件.txt", "legacy book", "")
	oldPacket := filepath.Join(oldInbox, old.ID)
	t.Setenv("ZLIB_CALIBRENO_HANDOFF_DIR", current)
	ordinary, inboxes := splitHandoffFolders([]string{oldInbox, current, oldPacket, root})
	if !slices.Equal(ordinary, []string{root}) || !slices.Equal(inboxes, []string{oldInbox, current}) {
		t.Fatalf("ordinary = %v, inboxes = %v", ordinary, inboxes)
	}
	regular := filepath.Join(root, "普通文件.txt")
	if err := os.WriteFile(regular, []byte("ordinary file"), 0o600); err != nil {
		t.Fatal(err)
	}
	items := scanImport([]string{root})
	if len(items) != 1 || items[0].Path != regular {
		t.Fatalf("ordinary folder import must not collect unverified handoffs: %+v", items)
	}
}

func publishTestHandoff(t *testing.T, inbox, name, content, target string) localhandoff.Manifest {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	message := localhandoff.Manifest{
		Version: 1, ID: uuid.NewString(), Filename: "book" + filepath.Ext(name), SourceName: name,
		Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), TargetLibraryID: target,
	}
	subdir := filepath.Join(inbox, message.ID)
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, message.Filename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return message
}

func readTestReceipt(t *testing.T, inbox string, message localhandoff.Manifest) localhandoff.Receipt {
	t.Helper()
	var receipt localhandoff.Receipt
	data, err := os.ReadFile(filepath.Join(inbox, message.ID, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Version != 1 || receipt.ID != message.ID || receipt.SHA256 != message.SHA256 {
		t.Fatalf("invalid receipt %+v, %v", receipt, err)
	}
	return receipt
}

func TestZlibraryInboxImportsRecoversAndStaysBoundToTheFirstLibrary(t *testing.T) {
	a := newTestApp(t)
	inbox := t.TempDir()
	original := a.lib
	origKey := a.batchKey
	book := publishTestHandoff(t, inbox, "湖畔の朝 - 王明.txt", "a distinctive book", "")
	a.receiveZlibraryInbox(inbox)
	added := readTestReceipt(t, inbox, book)
	if added.Status != "added" || added.BookID <= 0 || added.LibraryID != original.UUID() {
		t.Fatalf("added receipt: %+v", added)
	}
	finalBook, err := original.Book(added.BookID)
	if err != nil || finalBook.Title != "湖畔の朝" {
		t.Fatalf("book from original basename: %+v, %v", finalBook, err)
	}

	duplicate := publishTestHandoff(t, inbox, "Different metadata - Different author.txt", "a distinctive book", "")
	a.receiveZlibraryInbox(inbox)
	ignored := readTestReceipt(t, inbox, duplicate)
	if ignored.Status != "duplicate" || ignored.BookID != added.BookID || len(a.books) != len(samples)+1 {
		t.Fatalf("duplicate receipt: %+v, books = %d", ignored, len(a.books))
	}
	if a.batch.active == nil || a.batch.active.ID != "zlibrary-"+duplicate.ID {
		t.Fatal("new handoff should take priority over a previously completed batch")
	}

	file := filepath.Join(inbox, book.ID, "receipt.json")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(t.TempDir(), "Another library")
	other, err := calibre.Create(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	a.openLibrary(otherPath)
	if len(a.books) != 0 {
		t.Fatal("new library was not initially empty")
	}
	a.receiveZlibraryInbox(inbox)
	if len(a.books) != 0 {
		t.Fatal("same handoffs were imported into a different library")
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("another library wrote a receipt: %v", err)
	}
	a.openLibrary(original.Root)
	if a.batchKey != origKey {
		t.Fatal("could not restore original library")
	}
	a.receiveZlibraryInbox(inbox)
	if recovered := readTestReceipt(t, inbox, book); recovered != added || len(a.books) != len(samples)+1 {
		t.Fatalf("recovered receipt: %+v, books = %d", recovered, len(a.books))
	}
}

func TestZlibraryInboxUpdatesReviewAndRejectsCorruption(t *testing.T) {
	a := newTestApp(t)
	inbox := t.TempDir()
	target := a.lib.UUID()
	review := publishTestHandoff(t, inbox, "Dune - Frank Herbert.pdf", "a different format", target)
	corrupt := publishTestHandoff(t, inbox, "Corrupt file - Author.txt", "file", target)
	if err := os.WriteFile(filepath.Join(inbox, corrupt.ID, corrupt.Filename), []byte("swap"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := publishTestHandoff(t, inbox, "Another library - Author.txt", "unrelated", uuid.NewString())
	a.receiveZlibraryInbox(inbox)
	if missing := readTestReceipt(t, inbox, corrupt); missing.Status != "failed" || missing.Message != "source_invalid" || missing.BookID != 0 {
		t.Fatalf("corrupt receipt: %+v", missing)
	}
	if _, err := os.Stat(filepath.Join(inbox, other.ID, "receipt.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong target produced a receipt: %v", err)
	}
	if pending := readTestReceipt(t, inbox, review); pending.Status != "review" || pending.BookID != 0 {
		t.Fatalf("review receipt: %+v", pending)
	}
	if a.batch.active == nil || a.batch.active.ID != ("zlibrary-"+review.ID) {
		t.Fatal("inbox did not open its pending review batch")
	}
	a.openBatchCompare(a.batch.active.Items[0].ID)
	a.applyBatchDecision(batch.AddFormat)
	if resolved := readTestReceipt(t, inbox, review); resolved.Status != "added" || resolved.BookID <= 0 || resolved.LibraryID != target {
		t.Fatalf("resolved receipt: %+v", resolved)
	}
}

func TestZlibraryInboxIndependentReceiversDoNotDuplicateOrReplaceReceipt(t *testing.T) {
	first, second := newTestApp(t), newTestApp(t)
	destination := map[*App]string{first: t.TempDir(), second: t.TempDir()}
	for app, path := range destination {
		app.batchStore.Close()
		app.batchStore = batch.NewStore(path)
	}
	inbox := t.TempDir()
	message := publishTestHandoff(t, inbox, "湖畔の朝 - 王明.txt", "independent receivers must not both import this", "")

	start := make(chan struct{})
	results := make(chan error, 2)
	for app := range destination {
		go func(app *App) {
			<-start
			_, err := app.runZlibraryInbox(importRequest{key: app.batchKey, inbox: inbox}, app.lib, nil, nil)
			results <- err
		}(app)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	original := readTestReceipt(t, inbox, message)
	winner, loser := first, second
	if original.LibraryID == second.lib.UUID() {
		winner, loser = second, first
	}
	if original.Status != "added" || original.BookID <= 0 || original.LibraryID != winner.lib.UUID() {
		t.Fatalf("receipt = %+v", original)
	}
	staysUnimported := func() {
		t.Helper()
		loser.reload(nil)
		if _, err := loser.batchStore.LoadHandoff(message.ID); !errors.Is(err, sql.ErrNoRows) || len(loser.books) != len(samples) {
			t.Fatalf("another profile collected the delivery: %v, %d books", err, len(loser.books))
		}
	}
	staysUnimported()
	loser.receiveZlibraryInbox(inbox)
	staysUnimported()
	if got := readTestReceipt(t, inbox, message); got != original {
		t.Fatalf("another profile replaced the receipt: %+v", got)
	}

	receiptFile := filepath.Join(inbox, message.ID, "receipt.json")
	if err := os.Remove(receiptFile); err != nil {
		t.Fatal(err)
	}
	loser.receiveZlibraryInbox(inbox)
	staysUnimported()
	if _, err := os.Stat(receiptFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("another profile recreated the missing receipt: %v", err)
	}
	winner.batchStore.Close()
	winner.batchStore = batch.NewStore(destination[winner])
	winner.receiveZlibraryInbox(inbox)
	if got := readTestReceipt(t, inbox, message); got != original || len(winner.books) != len(samples)+1 {
		t.Fatalf("restart recovery: %+v, %d books", got, len(winner.books))
	}

	// A version without reservation support could still publish a stale foreign
	// receipt. The owner can restore the result without importing twice.
	legacy := original
	legacy.LibraryID, legacy.BookID = loser.lib.UUID(), 156
	writeTestReceipt(t, receiptFile, legacy)
	loser.receiveZlibraryInbox(inbox)
	staysUnimported()
	winner.receiveZlibraryInbox(inbox)
	if got := readTestReceipt(t, inbox, message); got != original || len(winner.books) != len(samples)+1 {
		t.Fatalf("foreign receipt repair: %+v, %d books", got, len(winner.books))
	}
}

func TestZlibraryInboxExistingLegacyReceiptPreventsNewImport(t *testing.T) {
	a := newTestApp(t)
	inbox := t.TempDir()
	firstLibrary := uuid.NewString()
	message := publishTestHandoff(t, inbox, "旧文件.txt", "valid legacy handoff receipt", "")
	previous := localhandoff.Receipt{
		Version: 1, ID: message.ID, SHA256: message.SHA256, Status: "added", LibraryID: firstLibrary, BookID: 5,
	}
	receiptFile := filepath.Join(inbox, message.ID, "receipt.json")
	writeTestReceipt(t, receiptFile, previous)
	a.receiveZlibraryInbox(inbox)
	if got := readTestReceipt(t, inbox, message); got != previous || len(a.books) != len(samples) {
		t.Fatalf("legacy receipt was not preserved: %+v, %d books", got, len(a.books))
	}
	if _, err := a.batchStore.LoadHandoff(message.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy handoff now has a local batch: %v", err)
	}
}

func writeTestReceipt(t *testing.T, path string, receipt localhandoff.Receipt) {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
