package weread

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
)

const historyLimit = 500

// Record tracks a transfer to the locally signed-in WeRead account.
type Record struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Digest      string    `json:"digest"`
	Status      Status    `json:"status"`
	At          time.Time `json:"at"`
	ConfirmedAt time.Time `json:"confirmed_at,omitzero"`
}

type historyFile struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// History is a bounded, newest-first set of transfer records.
type History struct {
	mu      sync.Mutex
	path    string
	records []Record
}

// OpenHistory restores activity from the last launch. An interrupted send may
// have reached WeRead, so it is marked pending when reopening the history.
// An empty path keeps history in memory only.
func OpenHistory(path string) (*History, error) {
	h := &History{path: path}
	if path == "" {
		return h, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	var saved historyFile
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, fmt.Errorf("read WeRead history: %w", err)
	}
	if saved.Version != 1 {
		return nil, fmt.Errorf("unknown WeRead history version %d", saved.Version)
	}
	h.records = saved.Records[:min(len(saved.Records), historyLimit)]
	recovered := false
	for i := range h.records {
		if h.records[i].Status == StatusSending {
			h.records[i].Status = StatusPending
			recovered = true
		}
	}
	if recovered || len(h.records) != len(saved.Records) {
		if err := h.save(h.records); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Recent returns a snapshot, from most recent to oldest.
func (h *History) Recent() []Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.records)
}

// Duplicate finds a previous transfer of the same bytes that reached WeRead
// or could have reached it without a confirmation.
func (h *History) Duplicate(digest string) (Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Digest == digest && (r.Status == StatusImported || r.Status == StatusPending) {
			return r, true
		}
	}
	return Record{}, false
}

// Begin persists an attempt before it can attach a file to the website.
func (h *History) Begin(title, digest string) (Record, error) {
	if digest == "" {
		return Record{}, errors.New("WeRead transfer requires a file digest")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	record := Record{ID: uuid.NewString(), Title: title, Digest: digest, Status: StatusSending, At: time.Now().UTC()}
	records := append([]Record{record}, h.records[:min(len(h.records), historyLimit-1)]...)
	if err := h.save(records); err != nil {
		return Record{}, err
	}
	return record, nil
}

// Finish stores the outcome of a running attempt.
func (h *History) Finish(id string, status Status) error {
	switch status {
	case StatusImported, StatusPending, StatusFailed, StatusManual, StatusLoginRequired:
	default:
		return fmt.Errorf("invalid WeRead transfer outcome %q", status)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	i := slices.IndexFunc(h.records, func(r Record) bool { return r.ID == id })
	if i == -1 {
		return errors.New("WeRead transfer record not found")
	}
	if h.records[i].Status != StatusSending {
		return errors.New("WeRead transfer is no longer sending")
	}
	records := slices.Clone(h.records)
	records[i].Status = status
	err := h.save(records)
	if err != nil {
		// The upload may have happened despite the failed disk write. Use the
		// observed outcome in this session. On restart, sending becomes pending.
		h.records = records
	}
	return err
}

// Confirm records that the user verified a pending import on their own shelf.
func (h *History) Confirm(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := slices.IndexFunc(h.records, func(r Record) bool { return r.ID == id })
	if i == -1 {
		return errors.New("WeRead transfer record not found")
	}
	record := h.records[i]
	if record.Status == StatusImported && !record.ConfirmedAt.IsZero() {
		return nil
	}
	if record.Status != StatusPending {
		return errors.New("only a pending WeRead import can be confirmed")
	}
	records := slices.Clone(h.records)
	records[i].Status = StatusImported
	records[i].ConfirmedAt = time.Now().UTC()
	return h.save(records)
}

// save replaces the on-disk snapshot before exposing the new records in memory.
// Callers writing to an existing History hold its mutex.
func (h *History) save(records []Record) error {
	if h.path != "" {
		dir := filepath.Dir(h.path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".weread-history-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		err = json.NewEncoder(f).Encode(historyFile{Version: 1, Records: records})
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		if err := os.Rename(f.Name(), h.path); err != nil {
			return err
		}
	}
	h.records = records
	return nil
}

// FileDigest identifies a file by its contents, independent of its book ID or name.
func FileDigest(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
