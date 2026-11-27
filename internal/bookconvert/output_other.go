//go:build !windows

package bookconvert

import "os"

func commitOutput(staged, target string) error {
	return os.Link(staged, target)
}
