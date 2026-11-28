// Package localhandoff reads ZlibraryNo's local, file-based import queue.
package localhandoff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	manifestName = "manifest.json"
	receiptName  = "receipt.json"
	markerName   = ".zlibraryno-handoff.json"
	maxManifest  = 16 * 1024
	maxBook      = 512 * 1024 * 1024
)

var (
	messageID     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	sha           = regexp.MustCompile(`^[0-9a-f]{64}$`)
	bookExtension = regexp.MustCompile(`^\.(epub|pdf|mobi|azw3|azw|fb2|cbz|djvu|djv|docx|rtf|prc|txt)$`)
)

type Manifest struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	Filename        string `json:"filename"`
	Bytes           int64  `json:"bytes"`
	SHA256          string `json:"sha256"`
	SourceName      string `json:"sourceName,omitempty"`
	TargetLibraryID string `json:"targetLibraryId,omitempty"`
}

type Receipt struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	Status    string `json:"status"`
	LibraryID string `json:"libraryId,omitempty"`
	BookID    int64  `json:"bookId,omitempty"`
	Message   string `json:"message,omitempty"`
}

func DefaultRoot() (string, error) {
	if override := os.Getenv("ZLIB_CALIBRENO_HANDOFF_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("ZlibraryNo inbox override must be an absolute path")
		}
		return filepath.Clean(override), nil
	}
	preferences, err := preferencesPath()
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(filepath.Dir(preferences))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil {
		defer root.Close()
		var pref struct {
			Version   int    `json:"version"`
			Directory string `json:"directory"`
		}
		err = readInboxJSON(root, filepath.Base(preferences), &pref)
		if err == nil {
			if pref.Version != 1 || !filepath.IsAbs(pref.Directory) || strings.ContainsRune(pref.Directory, 0) {
				return "", errors.New("invalid ZlibraryNo inbox preferences")
			}
			return filepath.Clean(pref.Directory), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return LegacyRoot()
}

func preferencesPath() (string, error) {
	if override := os.Getenv("ZLIB_CALIBRENO_HANDOFF_PREFERENCES_PATH"); override != "" {
		if !filepath.IsAbs(override) {
			return "", errors.New("ZlibraryNo preferences override must be an absolute path")
		}
		return filepath.Clean(override), nil
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "ZlibraryNo-CalibreNo", "preferences.json"), nil
}

// LegacyRoot is the fixed folder used by the first handoff version.
func LegacyRoot() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "ZlibraryNo-CalibreNo", "inbox"), nil
}

func hasInboxMarker(root *os.Root) bool {
	var marker struct {
		Version int `json:"version"`
	}
	return readInboxJSON(root, markerName, &marker) == nil && marker.Version == 1
}

// RecognizesInbox detects previously selected folders and older unmarked inboxes.
func RecognizesInbox(directory string) bool {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return false
	}
	defer root.Close()
	if hasInboxMarker(root) {
		return true
	}
	children, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return false
	}
	for _, child := range children {
		if child.IsDir() && messageID.MatchString(child.Name()) {
			if _, err := read(root, child.Name()); err == nil {
				return true
			}
		}
	}
	return false
}

// IsPacketDirectory prevents ordinary folder scans from ingesting handoff
// copies ahead of the manifest verification step.
func IsPacketDirectory(directory string) bool {
	name := filepath.Base(directory)
	packet := messageID.MatchString(name)
	if !packet && !strings.HasPrefix(name, ".partial-") {
		return false
	}
	root, err := os.OpenRoot(filepath.Dir(directory))
	if err != nil {
		return false
	}
	defer root.Close()
	if hasInboxMarker(root) {
		return true
	}
	if packet {
		_, err := read(root, name)
		return err == nil
	}
	return false
}

// Scan reads only published UUID directories, never the sender's temporary entries.
// It returns the valid messages even when an unrelated one is malformed.
func Scan(directory string) ([]Manifest, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	children, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	var messages []Manifest
	var invalid int
	for _, child := range children {
		if !messageID.MatchString(child.Name()) {
			continue
		}
		m, err := read(root, child.Name())
		if err != nil {
			invalid++
			continue
		}
		messages = append(messages, m)
	}
	if invalid > 0 {
		return messages, fmt.Errorf("ZlibraryNo inbox contains %d unreadable handoff(s)", invalid)
	}
	return messages, nil
}

func openMessage(root *os.Root, id string) (*os.Root, error) {
	if !messageID.MatchString(id) {
		return nil, errors.New("invalid ZlibraryNo handoff ID")
	}
	info, err := root.Lstat(id)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("ZlibraryNo handoff is not a regular directory")
	}
	return root.OpenRoot(id)
}

func openRegular(root *os.Root, name string, max int64) (*os.File, error) {
	return openRegularWithFlags(root, name, max, os.O_RDONLY)
}

func openRegularWithFlags(root *os.Root, name string, max int64, flags int) (*os.File, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, errors.New("ZlibraryNo handoff file type or size is invalid")
	}
	file, err := root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) || !actual.Mode().IsRegular() || actual.Size() != info.Size() {
		file.Close()
		return nil, errors.New("ZlibraryNo handoff changed while it was being opened")
	}
	return file, nil
}

func read(root *os.Root, id string) (Manifest, error) {
	message, err := openMessage(root, id)
	if err != nil {
		return Manifest{}, err
	}
	defer message.Close()
	return readManifest(message, id)
}

func readManifest(message *os.Root, id string) (Manifest, error) {
	var m Manifest
	if err := readInboxJSON(message, manifestName, &m); err != nil {
		return Manifest{}, err
	}
	ext := path.Ext(m.Filename)
	if m.Version != 1 || m.ID != id ||
		!bookExtension.MatchString(strings.ToLower(ext)) || !safeBookName(m.Filename) ||
		!sha.MatchString(m.SHA256) || m.Bytes <= 0 || m.Bytes > maxBook ||
		(m.TargetLibraryID != "" && !messageID.MatchString(strings.ToLower(m.TargetLibraryID))) {
		return Manifest{}, errors.New("invalid ZlibraryNo handoff manifest")
	}
	if m.Filename == "book"+strings.ToLower(ext) {
		if !safeBookName(m.SourceName) || !strings.EqualFold(path.Ext(m.SourceName), ext) {
			m.SourceName = ""
		}
	} else if m.SourceName != "" && m.SourceName != m.Filename {
		return Manifest{}, errors.New("invalid original ZlibraryNo filename")
	}
	return m, nil
}

func readInboxJSON(root *os.Root, name string, value any) error {
	file, err := openRegular(root, name, maxManifest)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifest+1))
	if err != nil {
		return err
	}
	if len(data) > maxManifest {
		return errors.New("ZlibraryNo handoff metadata is too large")
	}
	return json.Unmarshal(data, value)
}

func safeBookName(name string) bool {
	if name == "" || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || strings.ContainsAny(name, "/\\:<>|\"?*\x00") {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	// Depending on the platform, a device name can open a named system resource.
	start, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(strings.TrimRight(start, " ")) {
	case "AUX", "CON", "NUL", "PRN", "CONIN$", "CONOUT$", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
		return false
	}
	return true
}

// Snapshot makes a private, verified copy before the importer or its hooks can open a path.
func Snapshot(directory string, expected Manifest) (filename string, clean func(), err error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	message, err := openMessage(root, expected.ID)
	if err != nil {
		return "", nil, err
	}
	defer message.Close()
	actual, err := readManifest(message, expected.ID)
	if err != nil || actual != expected {
		return "", nil, errors.New("ZlibraryNo handoff manifest changed")
	}
	source, err := openRegular(message, actual.Filename, actual.Bytes)
	if err != nil {
		return "", nil, err
	}
	defer source.Close()
	tmp, err := os.MkdirTemp("", "calibreno-inbox-")
	if err != nil {
		return "", nil, err
	}
	remove := func() { _ = os.RemoveAll(tmp) }
	name := actual.SourceName
	if name == "" {
		name = actual.Filename
	}
	dest, err := os.OpenFile(filepath.Join(tmp, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		remove()
		return "", nil, err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(dest, hash), io.LimitReader(source, actual.Bytes+1))
	err = errors.Join(copyErr, dest.Close())
	if err == nil && (n != actual.Bytes || hex.EncodeToString(hash.Sum(nil)) != actual.SHA256) {
		err = errors.New("ZlibraryNo handoff size or checksum does not match")
	}
	if err != nil {
		remove()
		return "", nil, err
	}
	return dest.Name(), remove, nil
}
