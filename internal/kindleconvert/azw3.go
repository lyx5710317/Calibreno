// Package kindleconvert compiles EPUBs for Kindle USB transfers.
package kindleconvert

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"dalibre/internal/plugin"
	"dalibre/internal/proc"
)

const maxBookSize = 256 << 20

var windowsBuild = plugin.Build{
	URL:    "https://github.com/cbz-tools/Epub3-Kindle/releases/download/v0.5.2/epub3-kindle-v0.5.2-windows-x64.zip",
	SHA256: "dcd1dfbe8cec93932ec982b8266c54b9da61f586bd15fd6b1c7c8ccf32b31d86",
	Size:   1020301,
	Bin:    "epub3-kindle.exe",
}

// Converter stages the source and writes the validated result in an empty,
// caller-owned temporary directory. It reports whether the writer warned
// about changes to the book.
type Converter func(ctx context.Context, source, work string) (file string, warnings bool, err error)

// New returns the installed platform's converter. On first use it downloads
// the pinned Epub3-Kindle build into cacheDir.
func New(cacheDir string) Converter {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" || cacheDir == "" {
		return nil
	}
	cacheDir, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil
	}
	store := &plugin.ToolStore{Dir: cacheDir}
	return func(ctx context.Context, source, work string) (string, bool, error) {
		return convert(ctx, store, windowsBuild, source, work)
	}
}

func convert(ctx context.Context, store *plugin.ToolStore, build plugin.Build, source, work string) (string, bool, error) {
	if err := stageEPUB(ctx, source, filepath.Join(work, "book.epub")); err != nil {
		return "", false, err
	}
	download, cancel := context.WithTimeout(ctx, 90*time.Second)
	exe, err := store.Fetch(download, build, nil)
	cancel()
	if err != nil {
		return "", false, fmt.Errorf("could not get Epub3-Kindle: %w", err)
	}
	ctx, cancel = context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "book.epub", "-o", "book.azw3")
	proc.Group(cmd)
	cmd.Dir = work
	cmd.WaitDelay = 2 * time.Second
	var log commandLog
	cmd.Stdout, cmd.Stderr = &log, &log
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	// The pinned writer uses exit code 1 for a successful result with warnings.
	var exit *exec.ExitError
	warnings := errors.As(err, &exit) && exit.ExitCode() == 1
	if err != nil && !warnings {
		return "", false, fmt.Errorf("Epub3-Kindle: %w\n%s", err, strings.TrimSpace(string(log)))
	}
	file := filepath.Join(work, "book.azw3")
	if err := checkAZW3(file); err != nil {
		return "", false, fmt.Errorf("Epub3-Kindle produced no valid AZW3: %w\n%s", err, strings.TrimSpace(string(log)))
	}
	return file, warnings, nil
}

// Keep the last lines of the writer's diagnostics, including its final error.
type commandLog []byte

func (log *commandLog) Write(p []byte) (int, error) {
	const limit = 8192
	n := len(p)
	if n >= limit {
		*log = append((*log)[:0], p[n-limit:]...)
		return n, nil
	}
	if drop := len(*log) + n - limit; drop > 0 {
		*log = (*log)[:copy(*log, (*log)[drop:])]
	}
	*log = append(*log, p...)
	return n, nil
}

var invalidAZW3 = errors.New("invalid standalone KF8 header or record table")

func checkAZW3(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBookSize {
		return invalidAZW3
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var pdb [78]byte
	if _, err := io.ReadFull(f, pdb[:]); err != nil || string(pdb[60:68]) != "BOOKMOBI" {
		return invalidAZW3
	}
	be := binary.BigEndian
	records := int(be.Uint16(pdb[76:78]))
	if records < 3 {
		return invalidAZW3
	}
	index := make([]byte, 8*records)
	if _, err := io.ReadFull(f, index); err != nil {
		return invalidAZW3
	}
	previous := int64(len(pdb) + len(index) - 1)
	for i := 0; i < len(index); i += 8 {
		offset := int64(be.Uint32(index[i:]))
		if offset <= previous || offset >= info.Size() {
			return invalidAZW3
		}
		previous = offset
	}
	first := int64(be.Uint32(index))
	var mobi [40]byte
	if int64(be.Uint32(index[8:]))-first < int64(len(mobi)) {
		return invalidAZW3
	}
	if _, err := f.ReadAt(mobi[:], first); err != nil {
		return invalidAZW3
	}
	compression := be.Uint16(mobi[:2])
	textRecords := int(be.Uint16(mobi[8:10]))
	if string(mobi[16:20]) != "MOBI" || be.Uint16(mobi[12:14]) != 0 ||
		be.Uint32(mobi[36:40]) != 8 || be.Uint32(mobi[28:32]) != 65001 ||
		(compression != 1 && compression != 2) || textRecords == 0 || textRecords >= records {
		return invalidAZW3
	}
	return nil
}
