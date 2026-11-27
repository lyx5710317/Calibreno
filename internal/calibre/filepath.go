package calibre

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrLibraryFileOutside means a path does not resolve inside the library.
var ErrLibraryFileOutside = errors.New("the file is outside the library")

// ResolveLibraryFile returns the real path to a regular library file.
// The resolved location stays under the library even when it uses links.
func (l *Library) ResolveLibraryFile(path string) (string, error) {
	file, err := l.resolveLibraryPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("the library path is not a regular file")
	}
	return file, nil
}

// ResolveLibraryDirectory returns the real path to an existing directory
// underneath the library root.
func (l *Library) ResolveLibraryDirectory(path string) (string, error) {
	dir, err := l.resolveLibraryPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("the library path is not a directory")
	}
	return dir, nil
}

func (l *Library) resolveLibraryPath(path string) (string, error) {
	root, err := filepath.Abs(l.Root)
	if err != nil {
		return "", err
	}
	file, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !withinLibrary(root, file) {
		return "", ErrLibraryFileOutside
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	if !withinLibrary(root, file) {
		return "", ErrLibraryFileOutside
	}
	return file, nil
}

func withinLibrary(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && filepath.IsLocal(relative)
}
