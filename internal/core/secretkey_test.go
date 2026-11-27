package core

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Without a keychain the secrets key is a file; a passphrase can lock it,
// and secrets stay readable only once it is unlocked (item 8).
func TestSecretKeyPassphrase(t *testing.T) {
	old := keychainUsable
	keychainUsable = func() bool { return false }
	defer func() { keychainUsable = old }()
	h, _ := newHub(t)
	root := h.Root()
	if st := h.SecretKeyStatus(); st.Store != "file" {
		t.Fatalf("no keychain: %+v", st)
	}
	if err := h.SetProjectSecret("API_TOKEN", "s3cret-value", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ProtectSecretKey("short"); err == nil {
		t.Fatal("a short passphrase was taken")
	}
	st, err := h.ProtectSecretKey("correct horse battery")
	if err != nil || st.Store != "passphrase" || st.Locked {
		t.Fatalf("protect: %+v %v", st, err)
	}
	if _, err := os.Stat(h.plainKeyPath()); !os.IsNotExist(err) {
		t.Fatal("the plain key file is still there")
	}
	if b, _ := os.ReadFile(h.lockedKeyPath()); strings.Contains(string(b), "s3cret") || len(b) == 0 {
		t.Fatal("locked key file")
	}
	if v, err := h.projectSecret(root, "API_TOKEN"); err != nil || v != "s3cret-value" {
		t.Fatalf("unlocked read: %q %v", v, err)
	}
	// a new session: locked until the passphrase is typed
	h.LockSecretKey()
	if _, err := h.projectSecret(root, "API_TOKEN"); !errors.Is(err, ErrSecretsLocked) && (err == nil || !strings.Contains(err.Error(), "locked")) {
		t.Fatalf("locked read: %v", err)
	}
	if err := h.SetProjectSecret("OTHER", "x-value", ""); err == nil {
		t.Fatal("saved a secret while locked (with a new key?)")
	}
	if _, err := os.Stat(h.plainKeyPath()); !os.IsNotExist(err) {
		t.Fatal("a new plain key was made while locked")
	}
	if _, err := h.UnlockSecretKey("wrong passphrase!"); err == nil {
		t.Fatal("wrong passphrase unlocked")
	}
	if st, err := h.UnlockSecretKey("correct horse battery"); err != nil || st.Locked {
		t.Fatalf("unlock: %+v %v", st, err)
	}
	if v, err := h.projectSecret(root, "API_TOKEN"); err != nil || v != "s3cret-value" {
		t.Fatalf("after unlock: %q %v", v, err)
	}
	if _, err := h.ProtectSecretKey("another passphrase"); err == nil {
		t.Fatal("locked twice")
	}
}
