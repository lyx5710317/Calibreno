package bookconvert

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dalibre/internal/calibre"
)

// ConvertBook reads the selected source format and either exports a copy or
// adds an absent target format to the same library book.
func (e Engine) ConvertBook(ctx context.Context, lib *calibre.Library, id int64, from, to string, opts Request, progress io.Writer) (Result, error) {
	book, err := lib.Book(id)
	if err != nil {
		return Result{}, err
	}
	details, err := lib.Details(book)
	if err != nil {
		return Result{}, err
	}
	from, to = strings.ToUpper(from), strings.ToUpper(to)
	index := slices.IndexFunc(details.Files, func(file calibre.FormatFile) bool { return file.Format == from })
	if index < 0 {
		return Result{}, fmt.Errorf("book %d has no %s source format", id, from)
	}
	opts.Input, err = lib.ResolveLibraryFile(details.Files[index].Path)
	if err != nil {
		return Result{}, err
	}
	opts.sourceFormat = from
	if opts.Title == "" {
		opts.Title = book.Title
	}
	if opts.Author == "" {
		opts.Author = strings.Join(book.Authors, ", ")
	}
	if opts.Language == "" && len(book.Languages) > 0 {
		opts.Language = book.Languages[0]
	}
	if opts.Output != "" || len(opts.Pages) > 0 {
		return e.Convert(ctx, opts, progress)
	}
	if to == "" {
		return Result{}, errors.New("choose an output format")
	}
	switch to {
	case "EPUB", "AZW3", "PDF", "TXT":
	default:
		return Result{}, fmt.Errorf("unsupported output format: %s", to)
	}
	if slices.Contains(book.Formats, to) {
		return Result{}, fmt.Errorf("book already has %s; choose an output file to save a copy", to)
	}
	source, err := os.Stat(opts.Input)
	if err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp("", "calibreno-book-format-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	opts.Output = filepath.Join(work, "result."+strings.ToLower(to))
	result, err := e.Convert(ctx, opts, progress)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	current, err := os.Stat(opts.Input)
	if err != nil || !os.SameFile(source, current) || source.Size() != current.Size() || !source.ModTime().Equal(current.ModTime()) {
		return result, calibre.ErrBookChanged
	}
	if _, err := lib.ResolveLibraryDirectory(lib.Dir(book)); err != nil {
		return result, err
	}
	// This checks the original book version and refuses a format that appeared
	// while the converter was running.
	return result, lib.PutBatchFormat(book, opts.Output, "", "")
}
