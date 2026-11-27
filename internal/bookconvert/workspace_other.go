//go:build !windows

package bookconvert

import "errors"

func lockWorkspace(string) (func(), error) {
	return nil, errors.New("this PDF conversion engine requires Windows x64")
}
