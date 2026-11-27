package calibre

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"
)

// Duplicate is a book in the library that a file being added matches.
type Duplicate struct {
	Book      *Book
	HasFormat bool // the book has a file in the new file's format
	SameFile  bool // and that file is byte for byte the new one
	ByISBN    bool
}

// Duplicates finds the books a library has already, by title and author
// or by ISBN, for files about to be added.
type Duplicates struct {
	l      *Library
	byName map[string][]int64 // dupKey(title, author) → books
	byISBN map[string][]int64
}

// Duplicates indexes the library's books for Find.
func (l *Library) Duplicates() (*Duplicates, error) {
	books, err := l.Books()
	if err != nil {
		return nil, err
	}
	rows, err := l.db.Query("SELECT val, book FROM identifiers WHERE lower(type) = 'isbn' ORDER BY book")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d := &Duplicates{l: l, byName: map[string][]int64{}, byISBN: map[string][]int64{}}
	for _, b := range books {
		d.addNames(b.ID, b.Title, b.Authors)
	}
	for rows.Next() {
		var value string
		var id int64
		if err := rows.Scan(&value, &id); err != nil {
			return nil, err
		}
		if isbn := digits(value); isbn != "" {
			d.byISBN[isbn] = append(d.byISBN[isbn], id)
		}
	}
	return d, rows.Err()
}

// Added records a book added since the index was made, so that a second
// copy in the same import is found too.
func (d *Duplicates) Added(id int64, m Metadata) {
	m.normalize()
	d.addNames(id, m.Title, m.Authors)
	if isbn := digits(m.Identifiers["isbn"]); isbn != "" {
		d.byISBN[isbn] = append(d.byISBN[isbn], id)
	}
}

func (d *Duplicates) addNames(id int64, title string, authors []string) {
	if len(authors) == 0 {
		authors = []string{"Unknown"}
	}
	for _, a := range authors {
		key := dupKey(title, a)
		d.byName[key] = append(d.byName[key], id)
	}
}

// Find returns the book p duplicates, or nil.
func (d *Duplicates) Find(p Prepared) (*Duplicate, error) {
	all, err := d.FindAll(p)
	if len(all) == 0 || err != nil {
		return nil, err
	}
	return &all[0], nil
}

// FindAll keeps alternate editions visible even when an ISBN matched.
func (d *Duplicates) FindAll(p Prepared) ([]Duplicate, error) {
	m := p.Metadata
	m.normalize()
	isbnMatches := d.byISBN[digits(m.Identifiers["isbn"])]
	ids := slices.Clone(isbnMatches)
	for _, a := range m.Authors {
		ids = append(ids, d.byName[dupKey(m.Title, a)]...)
	}
	seen := make(map[int64]bool, len(ids))
	var matches []Duplicate
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		b, err := d.l.Book(id)
		if err != nil {
			return nil, err
		}
		det, err := d.l.Details(b)
		if err != nil {
			return nil, err
		}
		dup := Duplicate{Book: b, ByISBN: slices.Contains(isbnMatches, id)}
		for _, f := range det.Files {
			if f.Format == p.Format {
				dup.HasFormat = true
				dup.SameFile = sameFile(f.Path, p.File)
				break
			}
		}
		matches = append(matches, dup)
	}
	return matches, nil
}

// dupKey is a title and an author with case, punctuation and spacing
// left out, so "The Three-Body Problem" by "Liu  Cixin" matches
// "the three body problem" by "liu cixin".
func dupKey(title, author string) string {
	return fold(title) + "\x00" + fold(author)
}

func fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' || r == 'x' || r == 'X' {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

// sameFile reports whether two files hold the same bytes.
func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || sa.Size() != sb.Size() {
		return false
	}
	ha, hb := fileHash(a), fileHash(b)
	return ha != nil && bytes.Equal(ha, hb)
}

func fileHash(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil
	}
	return h.Sum(nil)
}
