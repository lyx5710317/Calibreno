package calibre

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// LibraryDuplicateGroup contains books whose files in Format match.
type LibraryDuplicateGroup struct {
	BookIDs []int64
	Format  string
}

type LibraryDuplicateReport struct {
	Groups       []LibraryDuplicateGroup
	BooksScanned int
	FilesScanned int
	Unreadable   []string // library-relative paths or book references for invalid paths
}

type libraryScanFile struct {
	bookID int64
	format string
	path   string
	info   os.FileInfo
}

type libraryScanSize struct {
	format string
	size   int64
}

// ScanDuplicates reads the current format records. It groups equal digests
// within a single format without changing the library. Progress counts
// format records, including any that cannot be inspected.
func (l *Library) ScanDuplicates(ctx context.Context, progress func(done, total int)) (LibraryDuplicateReport, error) {
	if err := ctx.Err(); err != nil {
		return LibraryDuplicateReport{}, err
	}
	files, books, err := l.libraryScanFiles(ctx)
	if err != nil {
		return LibraryDuplicateReport{}, err
	}
	root, err := os.OpenRoot(l.Root)
	if err != nil {
		return LibraryDuplicateReport{}, err
	}
	defer root.Close()

	if progress != nil {
		progress(0, len(files))
	}
	// Calibre may retain an outdated size in metadata.db after a file edit.
	// Stat the actual files before deciding which files need to be hashed.
	sizeCounts := make(map[libraryScanSize]int)
	for i := range files {
		if err := ctx.Err(); err != nil {
			return LibraryDuplicateReport{}, err
		}
		file := &files[i]
		if file.path == "" {
			continue
		}
		info, err := root.Stat(file.path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file.info = info
		sizeCounts[libraryScanSize{file.format, info.Size()}]++
	}
	type matchKey struct {
		libraryScanSize
		sum [sha256.Size]byte
	}
	matches := make(map[matchKey][]int64)
	report := LibraryDuplicateReport{BooksScanned: books, FilesScanned: len(files)}
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return LibraryDuplicateReport{}, err
		}
		if file.info == nil {
			ref := file.path
			if ref == "" {
				ref = fmt.Sprintf("#%d", file.bookID)
			}
			report.Unreadable = append(report.Unreadable, ref)
		} else {
			size := libraryScanSize{file.format, file.info.Size()}
			hash := sizeCounts[size] > 1
			sum, err := scanLibraryFile(ctx, root, file, hash)
			if ctx.Err() != nil {
				return LibraryDuplicateReport{}, ctx.Err()
			}
			if err != nil {
				report.Unreadable = append(report.Unreadable, file.path)
			} else if hash {
				key := matchKey{size, sum}
				matches[key] = append(matches[key], file.bookID)
			}
		}
		if progress != nil {
			progress(i+1, len(files))
		}
	}
	for key, ids := range matches {
		// The snapshot is ordered by book ID. A group represents distinct books.
		ids = slices.Compact(ids)
		if len(ids) > 1 {
			report.Groups = append(report.Groups, LibraryDuplicateGroup{BookIDs: ids, Format: key.format})
		}
	}
	slices.SortFunc(report.Groups, func(a, b LibraryDuplicateGroup) int {
		if order := cmp.Compare(a.Format, b.Format); order != 0 {
			return order
		}
		return slices.Compare(a.BookIDs, b.BookIDs)
	})
	if err := ctx.Err(); err != nil {
		return LibraryDuplicateReport{}, err
	}
	return report, nil
}

// Release the database rows before checking any files or notifying callers.
func (l *Library) libraryScanFiles(ctx context.Context) ([]libraryScanFile, int, error) {
	rows, err := l.db.QueryContext(ctx, "SELECT b.id, b.path, d.id, d.format, d.name "+
		"FROM books b LEFT JOIN data d ON d.book = b.id ORDER BY b.id, d.format, d.id")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var files []libraryScanFile
	var previous int64
	books := 0
	for rows.Next() {
		var id int64
		var dir, format, name sql.NullString
		var dataID sql.NullInt64
		if err := rows.Scan(&id, &dir, &dataID, &format, &name); err != nil {
			return nil, 0, err
		}
		if books == 0 || id != previous {
			previous, books = id, books+1
		}
		if dataID.Valid {
			path := libraryScanPath(dir.String, name.String, format.String)
			files = append(files, libraryScanFile{bookID: id, format: strings.ToUpper(format.String), path: path})
		}
	}
	return files, books, rows.Err()
}

func libraryScanPath(dir, name, format string) string {
	dir = filepath.FromSlash(dir)
	if !filepath.IsLocal(dir) || filepath.Clean(dir) == "." || !scanFileComponent(name) || !scanFileComponent(format) {
		return ""
	}
	return filepath.Join(dir, name+"."+strings.ToLower(format))
}

func scanFileComponent(name string) bool {
	return filepath.IsLocal(name) && name != "." && !strings.ContainsAny(name, "/\\:\x00")
}

func scanLibraryFile(ctx context.Context, root *os.Root, file libraryScanFile, hash bool) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := root.Open(file.path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return sum, err
	}
	if !info.Mode().IsRegular() || !sameLibraryScanVersion(file.info, info) {
		return sum, ErrBookChanged
	}
	if !hash {
		return sum, nil
	}
	digest := sha256.New()
	n, err := io.Copy(digest, libraryScanReader{ctx: ctx, Reader: f})
	if err != nil {
		return sum, err
	}
	infoAfter, err := root.Stat(file.path)
	if err != nil {
		return sum, err
	}
	if n != info.Size() || !sameLibraryScanVersion(info, infoAfter) {
		return sum, ErrBookChanged
	}
	digest.Sum(sum[:0])
	return sum, nil
}

func sameLibraryScanVersion(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b)
}

type libraryScanReader struct {
	ctx context.Context
	io.Reader
}

func (r libraryScanReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
