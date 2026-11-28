package mcp

import (
	"errors"

	"golang.org/x/sys/windows"
)

// IsAddressInUse reports whether a listen error came from an occupied address.
func IsAddressInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}

func isConnectionRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
