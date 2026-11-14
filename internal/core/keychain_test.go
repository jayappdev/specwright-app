package core

import (
	"testing"

	"codegen/internal/flow"
)

// stubKeychain replaces the system keychain with a map keyed by
// "service/account" (SpecWright's own entries: "SpecWright/<name>").
func stubKeychain(t *testing.T) map[string]string {
	t.Helper()
	store := map[string]string{}
	oldR, oldW := keychainRead, keychainWrite
	keychainRead = func(r flow.KeychainRef) (string, bool) {
		v, ok := store[r.Service+"/"+r.Account]
		return v, ok && v != ""
	}
	keychainWrite = func(r flow.KeychainRef, v string) error { store[r.Service+"/"+r.Account] = v; return nil }
	kc.Lock()
	kc.m = map[string]kcEntry{}
	kc.Unlock()
	t.Cleanup(func() {
		keychainRead, keychainWrite = oldR, oldW
		kc.Lock()
		kc.m = map[string]kcEntry{}
		kc.Unlock()
	})
	return store
}

func TestSecurityLineQuotes(t *testing.T) {
	got := securityLine("add-generic-password", "-w", `pa ss"wo\rd`)
	want := `"add-generic-password" "-w" "pa ss\"wo\\rd"`
	if got != want {
		t.Fatalf("%s", got)
	}
}
