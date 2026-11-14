package core

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"codegen/internal/flow"
)

// Windows Credential Manager (the Windows counterpart of the Keychain):
// SpecWright keeps generic credentials named "<service>/<account>", e.g.
// "SpecWright/github-token", with the account as the user name. To read a
// credential another program saved (git, an installer, cmdkey), give its
// "Internet or network address" as the service: an entry with exactly that
// name is used when its user name is the account (or no account is given).
// The calls are in wincred_windows.go; these helpers are shared so they can
// be tested anywhere.

// wincredTargets lists the credential names to try for ref, in order.
func wincredTargets(ref flow.KeychainRef) []string {
	out := []string{ref.Service + "/" + ref.Account}
	if ref.Service != flow.DefaultKeychainService {
		out = append(out, ref.Service)
	}
	return out
}

// wincredBlob encodes a value as Windows tools store passwords: UTF-16LE.
func wincredBlob(v string) []byte {
	u := utf16.Encode([]rune(v))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// wincredValue decodes a credential blob: UTF-16LE (Windows tools, and
// SpecWright) or UTF-8 (some cross-platform tools, e.g. git's).
func wincredValue(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) && !strings.ContainsRune(string(b), 0) {
		return string(b)
	}
	if len(b)%2 == 0 {
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return strings.TrimRight(string(utf16.Decode(u)), "\x00")
	}
	return string(b)
}

// wincredDescribe names a Windows credential for people.
func wincredDescribe(ref flow.KeychainRef) string {
	return "Credential Manager · " + ref.Service + "/" + ref.Account
}
