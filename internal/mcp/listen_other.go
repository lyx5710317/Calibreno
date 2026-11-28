//go:build !windows

package mcp

import (
	"errors"
	"syscall"
)

// IsAddressInUse reports whether a listen error came from an occupied address.
func IsAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
