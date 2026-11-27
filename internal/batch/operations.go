package batch

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"dalibre/internal/calibre"
)

// Prepare records the comparison input before the import hook's temporary file is released.
func (s *Store) Prepare(b *Batch, item *Item, prepared calibre.Prepared, index *calibre.Duplicates) error {
	title := prepared.Metadata.Title
	item.FilenameEdition = ""
	if hint := filenameEdition.FindStringSubmatch(title); hint != nil && strings.Contains(filepath.Base(item.Path), hint[2]) {
		prepared.Metadata.Title = strings.TrimSpace(hint[1])
		item.FilenameEdition = strings.TrimSpace(hint[2])
	}
	item.Format, item.Metadata = prepared.Format, prepared.Metadata
	item.Translators = prepared.Translators
	item.NeedTitle = strings.TrimSpace(prepared.Metadata.Title) == ""
	item.NeedAuthor = len(prepared.Metadata.Authors) == 0
	// A source with publishing information often lacks just one of these
	// fields. Books without either can still be enriched as an optional task.
	hasPublisher := strings.TrimSpace(prepared.Metadata.Publisher) != ""
	hasLanguage := len(prepared.Metadata.Languages) > 0
	item.NeedPublisher = !hasPublisher && hasLanguage
	item.NeedLanguage = !hasLanguage && hasPublisher
	item.Candidates, item.Problem, item.Detail = nil, "", ""
	digest, size, err := s.Stage(b, item, prepared.File)
	if err != nil {
		return s.RecordError(b, item, Failed, "read", err)
	}
	item.Digest, item.Size = digest, size
	prepared.File = s.Staged(b, item)
	return s.match(b, item, prepared, index)
}

var filenameEdition = regexp.MustCompile("^(.+?)[（(]([^（）()]+译)[）)]$")

func (s *Store) match(b *Batch, item *Item, prepared calibre.Prepared, index *calibre.Duplicates) error {
	matches, err := index.FindAll(prepared)
	if err != nil {
		state := Failed
		if item.State == Review {
			state = Review
		}
		return s.RecordError(b, item, state, "compare", err)
	}
	item.Candidates, item.Problem, item.Detail = nil, "", ""
	for _, match := range matches {
		if match.SameFile {
			item.Book, item.State, item.Intent = Ref(match.Book), Duplicate, nil
			if err := s.Save(b); err != nil {
				return err
			}
			s.Clean(b, item)
			return nil
		}
		item.Candidates = append(item.Candidates, Candidate{Book: Ref(match.Book), ByISBN: match.ByISBN,
			Title: match.Book.Title, Publisher: match.Book.Publisher, Pubdate: match.Book.Pubdate})
	}
	if len(item.Candidates) > 0 {
		item.State = Review
	}
	return s.Save(b)
}

func (s *Store) Recheck(b *Batch, item *Item, lib *calibre.Library) error {
	if item == nil || item.State != Review || LibraryKey(lib) != b.Library {
		return calibre.ErrBookChanged
	}
	if recovered, err := s.recoverItem(b, item, lib); recovered || err != nil {
		return err
	}
	if item.Intent != nil {
		return calibre.ErrBookChanged
	}
	index, err := lib.Duplicates()
	if err != nil {
		return err
	}
	return s.match(b, item, calibre.Prepared{File: s.Staged(b, item), Format: item.Format, Metadata: item.Metadata}, index)
}

func (s *Store) RecordError(b *Batch, item *Item, state State, problem string, err error) error {
	item.State, item.Problem, item.Detail = state, problem, err.Error()
	return s.Save(b)
}

// Apply writes a confirmed choice and returns the resulting book when applicable.
func (s *Store) Apply(b *Batch, item *Item, lib *calibre.Library, act Action, target *calibre.Book, before string) (*calibre.Book, error) {
	state := item.State
	if state != Waiting && state != Review && state != Failed {
		return nil, nil
	}
	var err error
	if lib == nil || LibraryKey(lib) != b.Library {
		return nil, calibre.ErrBookChanged
	}
	if item.Intent != nil {
		recovered, err := s.recoverItem(b, item, lib)
		if err != nil {
			return nil, err
		}
		if recovered {
			return item.Book.Book(lib)
		}
		if item.Intent != nil {
			return nil, calibre.ErrBookChanged
		}
	}
	if act == Skip {
		if target != nil {
			item.Book = Ref(target)
		}
		item.State, item.Problem, item.Detail = Skipped, "", ""
		if err := s.Save(b); err != nil {
			return nil, err
		}
		s.Clean(b, item)
		return nil, nil
	}
	source := s.Staged(b, item)
	hash, _, err := calibre.FileFingerprint(source)
	if err != nil {
		return nil, errors.Join(err, s.RecordError(b, item, state, "read", err))
	}
	if hash != item.Digest {
		err := calibre.ErrBookChanged
		return nil, errors.Join(err, s.RecordError(b, item, state, "changed", err))
	}
	op := &Intent{Action: act, Before: before}
	switch act {
	case AddBook:
		op.UUID = uuid.NewString()
	case AddFormat, Replace:
		if target == nil || (act == Replace) != (before != "") {
			return nil, calibre.ErrBookChanged
		}
		op.Target = Ref(target)
	default:
		return nil, calibre.ErrBookChanged
	}
	item.Intent = op
	if err := s.Save(b); err != nil {
		return nil, err
	}
	switch act {
	case AddBook:
		p := calibre.Prepare(source, item.Metadata, nil)
		var id int64
		id, err = lib.AddBook(calibre.NewBook{UUID: op.UUID, Metadata: item.Metadata, File: source, Cover: p.Cover})
		if err == nil {
			target, err = lib.Book(id)
		}
	case AddFormat, Replace:
		err = lib.PutBatchFormat(target, source, before, s.Backup(b, item))
	}
	if err != nil {
		problem := "write"
		if errors.Is(err, calibre.ErrBookChanged) {
			problem = "changed"
		}
		if state == Waiting {
			state = Failed
		}
		return nil, errors.Join(err, s.RecordError(b, item, state, problem, err))
	}
	if err := s.finish(b, item, target, act); err != nil {
		return nil, err
	}
	return target, nil
}

func (s *Store) finish(b *Batch, item *Item, book *calibre.Book, act Action) error {
	item.Book = Ref(book)
	switch act {
	case AddBook:
		item.State = Added
	case AddFormat:
		item.State = Supplement
	case Replace:
		item.State = Replaced
	}
	item.Intent, item.Problem, item.Detail = nil, "", ""
	if err := s.Save(b); err != nil {
		return err
	}
	s.Clean(b, item)
	return nil
}

// Recover records writes that reached Calibre before the batch checkpoint.
func (s *Store) Recover(b *Batch, lib *calibre.Library) error {
	for n := range b.Items {
		if _, err := s.recoverItem(b, &b.Items[n], lib); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recoverItem(b *Batch, item *Item, lib *calibre.Library) (bool, error) {
	op := item.Intent
	if op == nil {
		return false, nil
	}
	var book *calibre.Book
	var err error
	if op.Action == AddBook {
		book, err = lib.BookByUUID(op.UUID)
		if errors.Is(err, sql.ErrNoRows) {
			item.Intent = nil
			return false, s.Save(b)
		}
	} else {
		book, err = op.Target.Book(lib)
	}
	if err != nil {
		if errors.Is(err, calibre.ErrBookChanged) || errors.Is(err, sql.ErrNoRows) {
			if _, statErr := os.Stat(s.Backup(b, item)); errors.Is(statErr, os.ErrNotExist) {
				item.Intent = nil
			}
			return false, s.RecordError(b, item, Review, "changed", err)
		}
		return false, err
	}
	details, err := lib.Details(book)
	if err != nil {
		return false, err
	}
	foundFormat := false
	for _, file := range details.Files {
		if file.Format != item.Format {
			continue
		}
		foundFormat = true
		hash, size, err := calibre.FileFingerprint(file.Path)
		if op.Action == Replace && err == nil && hash == op.Before {
			item.Intent = nil
			_ = os.Remove(s.Backup(b, item))
			return false, s.Save(b)
		}
		if err != nil || hash != item.Digest || size != item.Size {
			continue
		}
		if file.Size != size {
			if err := lib.FormatRewritten(book.ID, item.Format, size); err != nil {
				return false, err
			}
		}
		return true, s.finish(b, item, book, op.Action)
	}
	if op.Action == AddFormat && !foundFormat {
		if recovered, err := lib.RecoverBatchFormat(book, item.Format, item.Digest, item.Size); err != nil {
			if !errors.Is(err, calibre.ErrBookChanged) {
				return false, err
			}
			item.Intent = nil
			return false, s.RecordError(b, item, Review, "changed", err)
		} else if recovered {
			return true, s.finish(b, item, book, op.Action)
		}
	}
	if op.Action == AddBook {
		return false, s.RecordError(b, item, Review, "changed", calibre.ErrBookChanged)
	}
	if op.Action == Replace {
		// Keep the original until the user can reconcile a changed file.
		if _, err := os.Stat(s.Backup(b, item)); err == nil {
			return false, s.RecordError(b, item, Review, "changed", calibre.ErrBookChanged)
		}
	}
	item.Intent = nil
	return false, s.Save(b)
}
