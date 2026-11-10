package core

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The system keychain stores secret values the user chose to keep between
// sessions: the macOS Keychain (security) or the Secret Service on Linux
// (secret-tool, from libsecret). Entries use the service "SpecWright" and
// the secret's name as the account. Windows has no command-line reader, so
// the keychain is unavailable there.

const keychainService = "SpecWright"

// keychainLookup is replaceable in tests.
var keychainLookup = func(name string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "security", "find-generic-password", "-s", keychainService, "-a", name, "-w")
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return "", false
		}
		cmd = exec.CommandContext(ctx, "secret-tool", "lookup", "service", keychainService, "account", name)
	default:
		return "", false
	}
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	v := strings.TrimRight(string(out), "\r\n")
	return v, v != ""
}

// keychainStore is replaceable in tests.
var keychainStore = func(name, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		// -U updates an existing entry
		return run(exec.CommandContext(ctx, "security", "add-generic-password", "-U", "-s", keychainService, "-a", name, "-l", "SpecWright: "+name, "-w", value))
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return errors.New("secret-tool (libsecret) is not installed")
		}
		cmd := exec.CommandContext(ctx, "secret-tool", "store", "--label", "SpecWright: "+name, "service", keychainService, "account", name)
		cmd.Stdin = strings.NewReader(value)
		return run(cmd)
	}
	return errors.New("the system keychain is not supported on " + runtime.GOOS)
}

var keychainRemove = func(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		return run(exec.CommandContext(ctx, "security", "delete-generic-password", "-s", keychainService, "-a", name))
	case "linux":
		return run(exec.CommandContext(ctx, "secret-tool", "clear", "service", keychainService, "account", name))
	}
	return errors.New("the system keychain is not supported on " + runtime.GOOS)
}

func run(cmd *exec.Cmd) error {
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// KeychainAvailable reports whether this computer has a usable keychain.
func KeychainAvailable() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := exec.LookPath("security")
		return err == nil
	case "linux":
		_, err := exec.LookPath("secret-tool")
		return err == nil
	}
	return false
}

// kcGet is keychainLookup with a short cache: the UI asks for secret
// status often and each lookup starts a process.
var kc = struct {
	sync.Mutex
	m map[string]kcEntry
}{m: map[string]kcEntry{}}

type kcEntry struct {
	v  string
	ok bool
	at time.Time
}

func kcGet(name string) (string, bool) {
	kc.Lock()
	e, hit := kc.m[name]
	kc.Unlock()
	if hit && time.Since(e.at) < 20*time.Second {
		return e.v, e.ok
	}
	v, ok := keychainLookup(name)
	kc.Lock()
	kc.m[name] = kcEntry{v, ok, time.Now()}
	kc.Unlock()
	return v, ok
}

func kcForget(name string) { kc.Lock(); delete(kc.m, name); kc.Unlock() }

// SaveSecretToKeychain keeps a secret value in the system keychain so later
// sessions (and flow runs) find it without asking.
func (h *Hub) SaveSecretToKeychain(name, value string) error {
	if strings.TrimSpace(name) == "" || value == "" {
		return errors.New("a name and a value are needed")
	}
	h.Store.Redactor().Add(value)
	defer kcForget(name)
	return keychainStore(name, value)
}

// RemoveSecretFromKeychain deletes a stored value.
func (h *Hub) RemoveSecretFromKeychain(name string) error {
	defer kcForget(name)
	return keychainRemove(name)
}
