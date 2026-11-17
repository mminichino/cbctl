//go:build windows

package capellaapi

import (
	"errors"
	"syscall"
)

// Winsock reports refused and reset connections with its own codes, which do not match the
// POSIX-style syscall.ECONNREFUSED / ECONNRESET values on Windows.
const (
	wsaeConnReset   syscall.Errno = 10054 // WSAECONNRESET
	wsaeConnRefused syscall.Errno = 10061 // WSAECONNREFUSED
)

func isConnRefused(err error) bool {
	return errors.Is(err, wsaeConnRefused) || errors.Is(err, syscall.ECONNREFUSED)
}

func isConnReset(err error) bool {
	return errors.Is(err, wsaeConnReset) || errors.Is(err, syscall.ECONNRESET)
}
