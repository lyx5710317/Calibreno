package localhandoff

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
)

const (
	claimName     = "claim.json"
	claimLockName = ".receive.lock"
)

var (
	ErrClaimed = errors.New("ZlibraryNo delivery already belongs to another receiver")
	ErrBusy    = errors.New("ZlibraryNo delivery is being processed")
	receiverID = regexp.MustCompile(`^[A-Z2-7]{26}$`)
)

type Receiver struct {
	StoreID    string `json:"storeId"`
	LibraryKey string `json:"libraryKey"`
}

type claim struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	SHA256  string `json:"sha256"`
	Receiver
}

// Packet holds the delivery lock across the reservation, book mutation and receipt.
type Packet struct {
	directory *os.Root
	unlock    func()
	expected  Manifest
	libraryID string
	claimed   bool
}

func LockPacket(directory string, expected Manifest) (*Packet, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	message, err := openMessage(root, expected.ID)
	if err != nil {
		return nil, err
	}
	lock, err := message.OpenFile(claimLockName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		lock, err = openRegularWithFlags(message, claimLockName, 0, os.O_RDWR)
	}
	if err != nil {
		message.Close()
		return nil, err
	}
	unlock, err := tryLockPacket(lock)
	if err != nil {
		lock.Close()
		message.Close()
		return nil, err
	}
	p := &Packet{directory: message, unlock: func() { unlock(); lock.Close() }, expected: expected}
	if err := p.verifyManifest(); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func (p *Packet) Close() {
	p.unlock()
	p.directory.Close()
}

func (p *Packet) verifyManifest() error {
	current, err := readManifest(p.directory, p.expected.ID)
	if err != nil || current != p.expected {
		return errors.New("ZlibraryNo handoff manifest changed")
	}
	return nil
}

// Reserve persists the first receiver before a batch can change its library. A
// legacy receipt can only be adopted by the matching library with an existing batch.
func (p *Packet) Reserve(owner Receiver, libraryID string, existingBatch bool) error {
	if !receiverID.MatchString(owner.StoreID) || !sha.MatchString(owner.LibraryKey) {
		return errors.New("invalid ZlibraryNo receiver identity")
	}
	if p.expected.TargetLibraryID != "" && !strings.EqualFold(p.expected.TargetLibraryID, libraryID) {
		return ErrClaimed
	}
	wanted := claim{Version: 1, ID: p.expected.ID, SHA256: p.expected.SHA256, Receiver: owner}
	var previous claim
	err := readInboxJSON(p.directory, claimName, &previous)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	claimed := err == nil
	if claimed && previous != wanted {
		return ErrClaimed
	}
	if !claimed {
		receipt, valid, err := p.receipt()
		if err != nil {
			return err
		}
		if valid && (!existingBatch || !strings.EqualFold(receipt.LibraryID, libraryID)) {
			return ErrClaimed
		}
		if err := p.writeJSON(claimName, wanted); err != nil {
			return err
		}
	}
	p.libraryID, p.claimed = libraryID, true
	return nil
}

func (p *Packet) receipt() (Receipt, bool, error) {
	var r Receipt
	if err := readInboxJSON(p.directory, receiptName, &r); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Receipt{}, false, nil
		}
		var syntax *json.SyntaxError
		var field *json.UnmarshalTypeError
		if errors.As(err, &syntax) || errors.As(err, &field) {
			return Receipt{}, false, nil
		}
		return Receipt{}, false, err
	}
	if r.Version != 1 || r.ID != p.expected.ID || r.SHA256 != p.expected.SHA256 ||
		(r.LibraryID != "" && !messageID.MatchString(strings.ToLower(r.LibraryID))) ||
		(p.expected.TargetLibraryID != "" && !strings.EqualFold(p.expected.TargetLibraryID, r.LibraryID)) || r.BookID < 0 {
		return Receipt{}, false, nil
	}
	switch r.Status {
	case "added", "duplicate", "review", "failed":
		return r, true, nil
	default:
		return Receipt{}, false, nil
	}
}

func (p *Packet) WriteReceipt(receipt Receipt) error {
	if !p.claimed {
		return ErrClaimed
	}
	if err := p.verifyManifest(); err != nil {
		return err
	}
	receipt.Version, receipt.ID, receipt.SHA256, receipt.LibraryID = 1, p.expected.ID, p.expected.SHA256, p.libraryID
	if previous, valid, _ := p.receipt(); valid && previous == receipt {
		return nil
	}
	return p.writeJSON(receiptName, receipt)
}

// Both the claim and receipt are published under the same interprocess lock.
// A separate synced file keeps interrupted writes from exposing partial JSON.
func (p *Packet) writeJSON(name string, value any) error {
	temporary := ".receive-" + rand.Text()
	file, err := p.directory.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer p.directory.Remove(temporary)
	err = json.NewEncoder(file).Encode(value)
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return p.directory.Rename(temporary, name)
}
