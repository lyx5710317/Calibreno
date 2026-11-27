package bookconvert

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func commitOutput(staged, target string) error {
	source, err := outputPath(staged)
	if err != nil {
		return err
	}
	destination, err := outputPath(target)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(source, destination, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "publish conversion", Old: staged, New: target, Err: err}
	}
	return nil
}

func outputPath(path string) (*uint16, error) {
	full, err := windows.FullPath(path)
	if err != nil {
		return nil, err
	}
	if len(full) >= 248 && !strings.HasPrefix(full, `\\?\`) && !strings.HasPrefix(full, `\\.\`) {
		if strings.HasPrefix(full, `\\`) {
			full = `\\?\UNC\` + full[2:]
		} else {
			full = `\\?\` + full
		}
	}
	return windows.UTF16PtrFromString(full)
}
