// Package weread prepares local EPUBs for manual import through WeRead.
package weread

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UploadPage is the official WeRead shelf with the "传书到手机" entry.
const UploadPage = "https://weread.qq.com/web/shelf"

// ErrInvalidEPUB means the selected file cannot be identified as an EPUB.
var ErrInvalidEPUB = errors.New("select a readable EPUB file")

// Handoff opens the official WeRead shelf and reveals the existing EPUB
// in the system file manager. The user chooses the file on the website.
func Handoff(path string, open func(string) error, reveal func(string)) error {
	if !strings.EqualFold(filepath.Ext(path), ".epub") {
		return ErrInvalidEPUB
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve EPUB path: %w", err)
	}
	if err := checkEPUB(path); err != nil {
		return err
	}
	if open == nil || reveal == nil {
		return errors.New("WeRead handoff is unavailable")
	}
	if err := open(UploadPage); err != nil {
		return fmt.Errorf("open WeRead: %w", err)
	}
	reveal(path)
	return nil
}

func checkEPUB(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read EPUB: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return ErrInvalidEPUB
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read EPUB: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("read EPUB: %w", err)
	}
	if !os.SameFile(info, opened) || info.Size() != opened.Size() || !info.ModTime().Equal(opened.ModTime()) {
		return ErrInvalidEPUB
	}

	archive, err := zip.NewReader(file, opened.Size())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidEPUB, err)
	}
	var hasContainer, hasMimetype bool
	for _, entry := range archive.File {
		switch entry.Name {
		case "META-INF/container.xml":
			if hasContainer || !entry.Mode().IsRegular() || entry.UncompressedSize64 == 0 {
				return ErrInvalidEPUB
			}
			hasContainer = true
		case "mimetype":
			if hasMimetype || !isEPUBMimetype(entry) {
				return ErrInvalidEPUB
			}
			hasMimetype = true
		}
	}
	if !hasContainer || !hasMimetype {
		return ErrInvalidEPUB
	}
	return nil
}

func isEPUBMimetype(entry *zip.File) bool {
	const mimetype = "application/epub+zip"
	if !entry.Mode().IsRegular() || entry.UncompressedSize64 != uint64(len(mimetype)) {
		return false
	}
	reader, err := entry.Open()
	if err != nil {
		return false
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(len(mimetype)+1)))
	return err == nil && string(data) == mimetype
}
