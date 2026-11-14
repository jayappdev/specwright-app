//go:build !windows

package core

import (
	"errors"

	"codegen/internal/flow"
)

// Windows Credential Manager exists only on Windows.

func wincredRead(flow.KeychainRef) (string, bool) { return "", false }

func wincredWrite(flow.KeychainRef, string) error {
	return errors.New("Windows Credential Manager is only on Windows")
}

func wincredDelete(flow.KeychainRef) error {
	return errors.New("Windows Credential Manager is only on Windows")
}
