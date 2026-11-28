package calibre

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	bookTypePreference = "namespaced:calibreno:book_types"
	bookTypeData       = "calibreno.book_types"
	customTypePrefix   = "custom:"
)

// BookType is a library category distinct from Calibre's tags.
type BookType struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Custom bool   `json:"custom"`
}

var presetBookTypes = [...]BookType{
	{ID: "fiction", Name: "小说"},
	{ID: "scienceFiction", Name: "科幻"},
	{ID: "mystery", Name: "推理"},
	{ID: "romance", Name: "言情"},
	{ID: "literature", Name: "文学"},
	{ID: "women", Name: "女性"},
	{ID: "poetry", Name: "诗"},
	{ID: "drama", Name: "戏剧"},
	{ID: "socialSciencesHumanities", Name: "社科人文"},
	{ID: "nonfiction", Name: "非虚构"},
	{ID: "psychology", Name: "心理学"},
	{ID: "history", Name: "历史"},
	{ID: "philosophy", Name: "哲学"},
	{ID: "biography", Name: "传记"},
	{ID: "religionSpirituality", Name: "宗教与灵性"},
	{ID: "scienceTechnology", Name: "科学与技术"},
	{ID: "personalGrowth", Name: "个人成长"},
	{ID: "comics", Name: "漫画"},
}

var (
	ErrInvalidBookTypeName  = errors.New("invalid book type name")
	ErrDuplicateBookType    = errors.New("book type already exists")
	ErrUnknownBookType      = errors.New("unknown book type")
	ErrBookTypesChanged     = errors.New("the book types changed")
	ErrBookTypesUnavailable = errors.New("this library does not support saving book types")
)

// NewCustomBookType makes a draft definition. Saving a BookTypeEdit persists it.
func NewCustomBookType(name string) (BookType, error) {
	name, err := validateBookTypeName(name)
	if err != nil {
		return BookType{}, err
	}
	return BookType{ID: customTypePrefix + newUUID(), Name: name, Custom: true}, nil
}

// BookTypeNameKey compares custom names with the type names shown in the editor.
func BookTypeNameKey(name string) string {
	return cases.Fold().String(norm.NFKC.String(strings.TrimSpace(name)))
}

func validateBookTypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch BookTypeNameKey(name) {
	case "", "未分类", "uncategorized":
		return "", ErrInvalidBookTypeName
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidBookTypeName
	}
	return name, nil
}

type queryRower interface {
	QueryRow(string, ...any) *sql.Row
}

func readCustomBookTypes(db queryRower) ([]BookType, error) {
	var raw string
	err := db.QueryRow("SELECT val FROM preferences WHERE key = ?", bookTypePreference).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var types []BookType
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil, fmt.Errorf("read custom book types: %w", err)
	}
	return types, nil
}

// BookTypes lists the preset types followed by this library's custom types.
func (l *Library) BookTypes() ([]BookType, error) {
	types := slices.Clone(presetBookTypes[:])
	exists, err := hasTable(l.db, "preferences")
	if err != nil || !exists {
		return types, err
	}
	custom, err := readCustomBookTypes(l.db)
	if err != nil {
		return nil, err
	}
	return append(types, custom...), nil
}

type storedBookTypes struct {
	UUID string   `json:"uuid"`
	IDs  []string `json:"ids"`
}

func decodeBookTypes(raw, bookUUID string) ([]string, error) {
	var stored storedBookTypes
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("read book types: %w", err)
	}
	if stored.UUID != bookUUID {
		return nil, nil
	}
	return sortedBookTypeIDs(stored.IDs), nil
}

func sortedBookTypeIDs(ids []string) []string {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	return slices.Compact(ids)
}

func (l *Library) loadBookTypes(byID map[int64]*Book, id int64) error {
	if len(byID) == 0 {
		return nil
	}
	exists, err := hasTable(l.db, "books_plugin_data")
	if err != nil || !exists {
		return err
	}
	query := "SELECT book, val FROM books_plugin_data WHERE name = ?"
	args := []any{bookTypeData}
	if id != 0 {
		query += " AND book = ?"
		args = append(args, id)
	}
	rows, err := l.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var bookID int64
		var val sql.NullString
		if err := rows.Scan(&bookID, &val); err != nil {
			return err
		}
		if b := byID[bookID]; b != nil && val.Valid {
			// A damaged classification should keep the book browsable.
			b.BookTypeIDs, _ = decodeBookTypes(val.String, b.UUID)
		}
	}
	return rows.Err()
}

// BookTypeEdit replaces Before with IDs when the assignments still match.
// Create contains any new custom definitions this edit needs to save.
type BookTypeEdit struct {
	Before []string
	IDs    []string
	Create []BookType
}

// UpdateBookTypes atomically saves type assignments and new definitions.
func (l *Library) UpdateBookTypes(old *Book, edit BookTypeEdit) error {
	return l.writeBookTypes(old, bookTypeWrite{edit: &edit})
}

// UpdateMetadataAndBookTypes saves a checked metadata edit and its type changes together.
func (l *Library) UpdateMetadataAndBookTypes(old *Book, m Metadata, edit BookTypeEdit) error {
	return l.updateMetadata(old, m, true, &bookTypeWrite{edit: &edit})
}

// UpdateMetadataAddingBookTypes merges IDs into the current assignments and optionally saves metadata.
func (l *Library) UpdateMetadataAddingBookTypes(old *Book, m *Metadata, ids []string) error {
	if len(ids) == 0 {
		if m == nil {
			return nil
		}
		return l.UpdateMetadataChecked(old, *m)
	}
	change := bookTypeWrite{adding: ids}
	if m == nil {
		return l.writeBookTypes(old, change)
	}
	return l.updateMetadata(old, *m, true, &change)
}

func (l *Library) writeBookTypes(old *Book, change bookTypeWrite) error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bookUUID string
	err = tx.QueryRow("SELECT COALESCE(uuid, '') FROM books WHERE id = ?", old.ID).Scan(&bookUUID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBookChanged
	}
	if err != nil {
		return err
	}
	if old.UUID != bookUUID {
		return ErrBookChanged
	}
	if err := change.apply(tx, old); err != nil {
		return err
	}
	return tx.Commit()
}

type bookTypeWrite struct {
	edit   *BookTypeEdit
	adding []string
}

func (w bookTypeWrite) apply(tx *sql.Tx, old *Book) error {
	for _, table := range []string{"preferences", "books_plugin_data"} {
		exists, err := hasTable(tx, table)
		if err != nil {
			return err
		}
		if !exists {
			return ErrBookTypesUnavailable
		}
	}
	current, err := readBookTypeIDs(tx, old)
	if err != nil {
		return err
	}
	definitions, err := readCustomBookTypes(tx)
	if err != nil {
		return err
	}
	if w.edit != nil {
		if !slices.Equal(current, sortedBookTypeIDs(w.edit.Before)) {
			return ErrBookTypesChanged
		}
		definitions, err = saveCreatedBookTypes(tx, definitions, w.edit.Create)
		if err != nil {
			return err
		}
	}
	ids := w.adding
	if w.edit != nil {
		ids = w.edit.IDs
	}
	for _, id := range ids {
		if !hasBookType(presetBookTypes[:], id) && !hasBookType(definitions, id) {
			return fmt.Errorf("%w: %s", ErrUnknownBookType, id)
		}
	}
	if w.edit == nil {
		ids = append(current, ids...)
	}
	ids = sortedBookTypeIDs(ids)
	if slices.Equal(current, ids) {
		return nil
	}
	if len(ids) == 0 {
		_, err = tx.Exec("DELETE FROM books_plugin_data WHERE book = ? AND name = ?", old.ID, bookTypeData)
		return err
	}
	raw, err := json.Marshal(storedBookTypes{UUID: old.UUID, IDs: ids})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO books_plugin_data (book, name, val) VALUES (?, ?, ?) ON CONFLICT (book, name) DO UPDATE SET val = excluded.val", old.ID, bookTypeData, string(raw))
	return err
}

func readBookTypeIDs(db queryRower, book *Book) ([]string, error) {
	var val string
	err := db.QueryRow("SELECT val FROM books_plugin_data WHERE book = ? AND name = ?", book.ID, bookTypeData).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeBookTypes(val, book.UUID)
}

func hasBookType(types []BookType, id string) bool {
	return slices.ContainsFunc(types, func(t BookType) bool { return t.ID == id })
}

func saveCreatedBookTypes(tx *sql.Tx, definitions, create []BookType) ([]BookType, error) {
	if len(create) == 0 {
		return definitions, nil
	}
	names := make(map[string]bool, len(presetBookTypes)+len(definitions)+len(create))
	for _, t := range presetBookTypes {
		names[BookTypeNameKey(t.Name)] = true
	}
	for _, t := range definitions {
		names[BookTypeNameKey(t.Name)] = true
	}
	for _, t := range create {
		name, err := validateBookTypeName(t.Name)
		if err != nil {
			return nil, err
		}
		rawID, ok := strings.CutPrefix(t.ID, customTypePrefix)
		parsed, err := uuid.Parse(rawID)
		if !t.Custom || !ok || err != nil || parsed.String() != rawID {
			return nil, fmt.Errorf("%w: %s", ErrUnknownBookType, t.ID)
		}
		key := BookTypeNameKey(name)
		if names[key] || hasBookType(definitions, t.ID) {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateBookType, name)
		}
		names[key] = true
		t.Name = name
		definitions = append(definitions, t)
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec("INSERT INTO preferences (key, val) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET val = excluded.val", bookTypePreference, string(raw))
	return definitions, err
}
