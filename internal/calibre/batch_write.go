package calibre

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrBookChanged means a reviewed book or file needs a fresh comparison.
var ErrBookChanged = errors.New("the book or its format changed")

func checkBookVersion(tx *sql.Tx, book *Book) error {
	var uuid, modified, path string
	err := tx.QueryRow("SELECT uuid, CAST(last_modified AS TEXT), path FROM books WHERE id = ?", book.ID).Scan(&uuid, &modified, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBookChanged
	}
	if err != nil {
		return err
	}
	if book.UUID != uuid || book.Path != path || !book.LastModified.Equal(parseTime(modified)) {
		return ErrBookChanged
	}
	return nil
}

// FileFingerprint returns the SHA-256 and the number of bytes read.
func FileFingerprint(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func copyBeside(source, target string) (path string, size int64, err error) {
	temp, err := os.CreateTemp(filepath.Dir(target), ".calibreno-*")
	if err != nil {
		return "", 0, err
	}
	path = temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	size, err = copyFile(source, path)
	if err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	return path, size, nil
}

func (l *Library) batchFormatPath(book *Book, name, extension string) (string, error) {
	dir := l.Dir(book)
	root, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, absolute)
	if err != nil || rel == "." || !filepath.IsLocal(rel) || filepath.Base(name) != name {
		return "", ErrBookChanged
	}
	return filepath.Join(absolute, name+extension), nil
}

// RecoverBatchFormat registers a file moved into place before an interrupted
// transaction. The exact content must match the persisted write intent.
func (l *Library) RecoverBatchFormat(book *Book, format, digest string, size int64) (bool, error) {
	tx, err := l.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := checkBookVersion(tx, book); err != nil {
		return false, err
	}
	name := BookFileName(book.Title, firstOr(book.Authors, "Unknown"), l.keepNames.Load())
	dst, err := l.batchFormatPath(book, name, "."+strings.ToLower(format))
	if err != nil {
		return false, err
	}
	hash, bytes, err := FileFingerprint(dst)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash != digest || bytes != size {
		return false, ErrBookChanged
	}
	if _, err := tx.Exec("INSERT INTO data(book, format, uncompressed_size, name) VALUES(?, ?, ?, ?)", book.ID, format, size, name); err != nil {
		return false, err
	}
	if _, err := tx.Exec("UPDATE books SET last_modified = ? WHERE id = ?", formatTime(time.Now()), book.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// PutBatchFormat adds an absent format, or replaces the reviewed version.
// For replacement, backup stays available until the batch confirms completion.
func (l *Library) PutBatchFormat(book *Book, source, before, backup string) (err error) {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkBookVersion(tx, book); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(source))
	format := strings.ToUpper(strings.TrimPrefix(ext, "."))
	var name string
	err = tx.QueryRow("SELECT name FROM data WHERE book = ? AND format = ?", book.ID, format).Scan(&name)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if exists != (before != "") {
		return ErrBookChanged
	}
	if !exists {
		name = BookFileName(book.Title, firstOr(book.Authors, "Unknown"), l.keepNames.Load())
	}
	dst, err := l.batchFormatPath(book, name, ext)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if exists {
		digest, _, err := FileFingerprint(dst)
		if err != nil {
			return err
		}
		if digest != before || backup == "" {
			return ErrBookChanged
		}
		saved, _, err := FileFingerprint(backup)
		switch {
		case errors.Is(err, os.ErrNotExist):
			tmp, _, err := copyBeside(dst, backup)
			if err != nil {
				return err
			}
			if err := os.Rename(tmp, backup); err != nil {
				_ = os.Remove(tmp)
				return err
			}
		case err != nil:
			return err
		case saved != before:
			return ErrBookChanged
		}
	} else if _, err := os.Lstat(dst); err == nil {
		return ErrBookChanged
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, size, err := copyBeside(source, dst)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := tx.Exec("INSERT INTO data(book, format, uncompressed_size, name) VALUES(?, ?, ?, ?) ON CONFLICT(book, format) DO UPDATE SET uncompressed_size = excluded.uncompressed_size, name = excluded.name", book.ID, format, size, name); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE books SET last_modified = ? WHERE id = ?", formatTime(time.Now()), book.ID); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		if !exists {
			return errors.Join(err, os.Remove(dst))
		}
		restore, _, copyErr := copyBeside(backup, dst)
		if copyErr != nil {
			return errors.Join(err, copyErr)
		}
		defer os.Remove(restore)
		return errors.Join(err, os.Rename(restore, dst))
	}
	return nil
}
