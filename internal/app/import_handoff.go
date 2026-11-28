package app

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"dalibre/internal/batch"
	"dalibre/internal/calibre"
	"dalibre/internal/localhandoff"
	"dalibre/internal/plugin"
)

func (a *App) receiveZlibraryInbox(root string) {
	if a.lib == nil || a.loading || !filepath.IsAbs(root) {
		return
	}
	a.imports = append(a.imports, importRequest{key: a.batchKey, inbox: filepath.Clean(root)})
	a.drainImports()
}

func (a *App) runZlibraryInbox(request importRequest, lib *calibre.Library, hooks, after []*plugin.Plugin) (*batch.Batch, error) {
	messages, totalErr := localhandoff.Scan(request.inbox)
	var selected *batch.Batch
	selectedPriority := -1
	libraryID := lib.UUID()
	for _, message := range messages {
		if message.TargetLibraryID != "" && !strings.EqualFold(message.TargetLibraryID, libraryID) {
			continue
		}
		b, started, err := a.runZlibraryPacket(request, message, libraryID, lib, hooks, after)
		if errors.Is(err, localhandoff.ErrClaimed) || errors.Is(err, localhandoff.ErrBusy) {
			continue
		}
		if err != nil {
			totalErr = errors.Join(totalErr, err)
		}
		if b == nil {
			continue
		}
		priority := 0
		if started {
			priority++
		}
		if b.Items[0].State == batch.Review {
			priority += 2
		}
		if priority > selectedPriority || (priority == selectedPriority && b.Created.After(selected.Created)) {
			selected = b
			selectedPriority = priority
		}
	}
	return selected, totalErr
}

func (a *App) runZlibraryPacket(request importRequest, message localhandoff.Manifest, libraryID string, lib *calibre.Library, hooks, after []*plugin.Plugin) (*batch.Batch, bool, error) {
	packet, err := localhandoff.LockPacket(request.inbox, message)
	if err != nil {
		return nil, false, err
	}
	defer packet.Close()
	b, err := a.batchStore.LoadHandoff(message.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if b != nil {
		if b.Library != request.key {
			return nil, false, localhandoff.ErrClaimed
		}
		if !matchesHandoff(b, request.inbox, message) {
			return nil, false, errors.New("ZlibraryNo handoff no longer matches the original delivery")
		}
	}
	if err := a.reserveHandoff(packet, request.key, libraryID, b != nil); err != nil {
		return nil, false, err
	}
	if b == nil {
		name := message.SourceName
		if name == "" {
			name = message.Filename
		}
		b = batch.New(request.key, "ZlibraryNo · "+name)
		b.ID = "zlibrary-" + message.ID
		b.Handoff = &batch.Handoff{Inbox: request.inbox, Manifest: message}
		b.Items = []batch.Item{batch.NewItem(filepath.Join(request.inbox, message.ID, name), batch.Waiting)}
		if err := a.batchStore.Save(b); err != nil {
			return nil, false, err
		}
	}
	started := b.Items[0].State == batch.Waiting || b.Items[0].Intent != nil
	var importErr error
	if started {
		importErr = a.runImport(b, lib, hooks, after, "")
	}
	return b, started, errors.Join(importErr, a.writeHandoffReceipt(packet, b))
}

func matchesHandoff(b *batch.Batch, inbox string, manifest localhandoff.Manifest) bool {
	return b.Handoff != nil && b.Handoff.Inbox == inbox && b.Handoff.Manifest == manifest && len(b.Items) == 1
}

func (a *App) reserveHandoff(packet *localhandoff.Packet, libraryKey, libraryID string, existingBatch bool) error {
	id, err := a.batchStore.ReceiverID()
	if err != nil {
		return err
	}
	owner := localhandoff.Receiver{StoreID: id, LibraryKey: fmt.Sprintf("%x", sha256.Sum256([]byte(libraryKey)))}
	return packet.Reserve(owner, libraryID, existingBatch)
}

// withHandoff keeps review, retries and recovery under the same packet lock as
// the first intake. Ordinary batches retain their existing path.
func (a *App) withHandoff(b *batch.Batch, lib *calibre.Library, action func() error) error {
	if b.Handoff == nil {
		return action()
	}
	inbox, manifest := b.Handoff.Inbox, b.Handoff.Manifest
	packet, err := localhandoff.LockPacket(inbox, manifest)
	if err != nil {
		return err
	}
	defer packet.Close()
	latest, err := a.batchStore.Load(b.Library, b.ID)
	if err != nil {
		return err
	}
	if !matchesHandoff(latest, inbox, manifest) {
		return errors.New("ZlibraryNo handoff no longer matches the original delivery")
	}
	if err := a.reserveHandoff(packet, b.Library, lib.UUID(), true); err != nil {
		return err
	}
	*b = *latest
	return errors.Join(action(), a.writeHandoffReceipt(packet, b))
}

// Read the committed batch before publishing a result. A receipt can be recreated
// after a restart without running the import again.
func (a *App) writeHandoffReceipt(packet *localhandoff.Packet, b *batch.Batch) error {
	committed, err := a.batchStore.Load(b.Library, b.ID)
	if err != nil {
		return err
	}
	item := committed.Items[0]
	if item.Intent != nil {
		return nil
	}
	var receipt localhandoff.Receipt
	switch item.State {
	case batch.Added, batch.Supplement, batch.Replaced:
		receipt.Status, receipt.BookID = "added", item.Book.ID
	case batch.Duplicate:
		receipt.Status, receipt.BookID = "duplicate", item.Book.ID
	case batch.Review:
		receipt.Status = "review"
	case batch.Failed, batch.Unsupported:
		receipt.Status, receipt.Message = "failed", "import_failed"
		if item.Problem == "handoff" {
			receipt.Message = "source_invalid"
		}
	case batch.Skipped:
		receipt.Status, receipt.Message = "failed", "skipped"
	default:
		return nil
	}
	return packet.WriteReceipt(receipt)
}
