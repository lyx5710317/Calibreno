// Package batch stores the import workflow alongside, but separately from, a Calibre library.
package batch

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"dalibre/internal/calibre"
	_ "modernc.org/sqlite"
)

type State string

const (
	Waiting     State = "waiting"
	Review      State = "review"
	Added       State = "added"
	Supplement  State = "supplement"
	Replaced    State = "replaced"
	Duplicate   State = "duplicate"
	Skipped     State = "skipped"
	Unsupported State = "unsupported"
	Failed      State = "failed"
)

type Action string

const (
	AddBook   Action = "add_book"
	AddFormat Action = "add_format"
	Replace   Action = "replace"
	Skip      Action = "skip"
)

type Reference struct {
	ID   int64
	UUID string
}

func Ref(b *calibre.Book) Reference { return Reference{ID: b.ID, UUID: b.UUID} }

func (r Reference) Book(lib *calibre.Library) (*calibre.Book, error) {
	b, err := lib.Book(r.ID)
	if err != nil {
		return nil, err
	}
	if b.UUID != r.UUID {
		return nil, calibre.ErrBookChanged
	}
	return b, nil
}

type Candidate struct {
	Book      Reference
	ByISBN    bool
	Title     string
	Publisher string
	Pubdate   time.Time
}

type Intent struct {
	Action Action
	Target Reference
	Before string
	// UUID identifies a new book even if the process exits immediately after the insert.
	UUID string
}

type Item struct {
	ID              string
	Path            string
	Format          string
	State           State
	Metadata        calibre.Metadata
	Translators     []string
	FilenameEdition string
	Candidates      []Candidate
	Book            Reference
	Digest          string
	Size            int64
	NeedTitle       bool
	NeedAuthor      bool
	NeedPublisher   bool
	NeedLanguage    bool
	Reviewed        bool
	Enriched        bool
	Problem         string
	Detail          string
	Intent          *Intent
	MetadataIntent  map[string]string
}

func (i Item) NeedsMetadata() bool {
	return i.State == Added && !i.Reviewed && (i.NeedTitle || i.NeedAuthor || i.NeedPublisher || i.NeedLanguage)
}

type Batch struct {
	ID      string
	Library string
	Source  string
	Created time.Time
	Items   []Item
}

type Summary struct {
	ID                                                                                             string
	Source                                                                                         string
	Created                                                                                        time.Time
	Files                                                                                          int
	Processed                                                                                      int
	Review, Metadata, Added, Formats, Replaced, Duplicates, Skipped, Failed, Unsupported, Enriched int
	Waiting                                                                                        int
}

func (b *Batch) Summary() Summary {
	s := Summary{ID: b.ID, Source: b.Source, Created: b.Created}
	for _, item := range b.Items {
		if item.State != Unsupported {
			s.Files++
		}
		switch item.State {
		case Waiting:
			s.Waiting++
		case Review:
			s.Review++
		case Added:
			s.Added++
		case Supplement:
			s.Formats++
		case Replaced:
			s.Replaced++
		case Duplicate:
			s.Duplicates++
		case Skipped:
			s.Skipped++
		case Failed:
			s.Failed++
		case Unsupported:
			s.Unsupported++
		}
		if item.NeedsMetadata() {
			s.Metadata++
		}
		if item.Enriched {
			s.Enriched++
		}
	}
	s.Processed = s.Files - s.Waiting - s.Review
	return s
}

func (s Summary) Pending() int { return s.Waiting + s.Review + s.Metadata + s.Failed }

func (b *Batch) Item(id string) *Item {
	for n := range b.Items {
		if b.Items[n].ID == id {
			return &b.Items[n]
		}
	}
	return nil
}

func LibraryKey(lib *calibre.Library) string {
	if id := lib.UUID(); id != "" {
		return "uuid:" + id
	}
	root, err := filepath.Abs(lib.Root)
	if err != nil {
		root = filepath.Clean(lib.Root)
	}
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	return "path:" + root
}

type Store struct {
	root   string
	memory bool
	once   sync.Once
	db     *sql.DB
	err    error
}

func NewStore(root string) *Store { return &Store{root: root, memory: root == ""} }

func (s *Store) init() error {
	s.once.Do(func() {
		if s.memory {
			s.root, s.err = os.MkdirTemp("", "calibreno-batches-")
		} else {
			s.err = os.MkdirAll(s.root, 0o700)
		}
		if s.err != nil {
			return
		}
		s.db, s.err = sql.Open("sqlite", filepath.Join(s.root, "batches.db"))
		if s.err != nil {
			return
		}
		s.db.SetMaxOpenConns(1)
		_, s.err = s.db.Exec("CREATE TABLE IF NOT EXISTS batches (id TEXT PRIMARY KEY, library TEXT NOT NULL, created INTEGER NOT NULL, summary BLOB NOT NULL, payload BLOB NOT NULL)")
	})
	return s.err
}

func (s *Store) Close() {
	if s.db != nil {
		_ = s.db.Close()
	}
	if s.memory && s.root != "" {
		_ = os.RemoveAll(s.root)
	}
}

func (s *Store) Save(b *Batch) error {
	if err := s.init(); err != nil {
		return err
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	summary, err := json.Marshal(b.Summary())
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO batches VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET summary = excluded.summary, payload = excluded.payload", b.ID, b.Library, b.Created.UnixNano(), summary, payload)
	if err != nil {
		return errors.Join(ErrStorage, err)
	}
	return err
}

var ErrStorage = errors.New("could not save the import batch")

func (s *Store) List(library string) ([]Summary, error) {
	if err := s.init(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT summary FROM batches WHERE library = ? ORDER BY created DESC", library)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var summaries []Summary
	for rows.Next() {
		var jsonBytes []byte
		var summary Summary
		if err := rows.Scan(&jsonBytes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(jsonBytes, &summary); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func (s *Store) Load(library, id string) (*Batch, error) {
	if err := s.init(); err != nil {
		return nil, err
	}
	var payload []byte
	if err := s.db.QueryRow("SELECT payload FROM batches WHERE library = ? AND id = ?", library, id).Scan(&payload); err != nil {
		return nil, err
	}
	var b Batch
	return &b, json.Unmarshal(payload, &b)
}

func New(library, source string) *Batch {
	return &Batch{ID: rand.Text(), Library: library, Source: source, Created: time.Now()}
}

func NewItem(path string, state State) Item {
	return Item{ID: rand.Text(), Path: path, Format: strings.ToUpper(strings.TrimPrefix(filepath.Ext(path), ".")), State: state}
}

func (s *Store) directory(b *Batch, item *Item) string {
	return filepath.Join(s.root, "staging", filepath.Base(b.ID), filepath.Base(item.ID))
}

func (s *Store) Staged(b *Batch, item *Item) string {
	return filepath.Join(s.directory(b, item), "book."+strings.ToLower(filepath.Base(item.Format)))
}

func (s *Store) Backup(b *Batch, item *Item) string {
	return filepath.Join(s.directory(b, item), "original")
}

func (s *Store) Stage(b *Batch, item *Item, source string) (digest string, size int64, err error) {
	if err := s.init(); err != nil {
		return "", 0, err
	}
	dst := s.Staged(b, item)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, err
	}
	in, err := os.Open(source)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), "pending-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(out.Name())
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", 0, err
	}
	digest, size, err = calibre.FileFingerprint(out.Name())
	if err != nil {
		return "", 0, err
	}
	return digest, size, os.Rename(out.Name(), dst)
}

func (s *Store) Clean(b *Batch, item *Item) {
	_ = os.Remove(s.Staged(b, item))
	_ = os.Remove(s.Backup(b, item))
	_ = os.Remove(s.directory(b, item))
	_ = os.Remove(filepath.Dir(s.directory(b, item)))
}
