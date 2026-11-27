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
	oldR, oldW, oldU := keychainRead, keychainWrite, keychainUsable
	keychainUsable = func() bool { return true } // CI's Linux has no secret-tool
	keychainRead = func(r flow.KeychainRef) (string, bool) {
		v, ok := store[r.Service+"/"+r.Account]
		return v, ok && v != ""
	}
	keychainWrite = func(r flow.KeychainRef, v string) error { store[r.Service+"/"+r.Account] = v; return nil }
	kc.Lock()
	kc.m = map[string]kcEntry{}
	kc.Unlock()
	t.Cleanup(func() {
		keychainRead, keychainWrite, keychainUsable = oldR, oldW, oldU
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

// Windows Credential Manager blobs: SpecWright writes UTF-16LE (as Windows
// tools do) and reads that or UTF-8 (as some cross-platform tools write).
func TestWincredBlobs(t *testing.T) {
	for _, v := range []string{"hunter2", "pä$$wörd ✓", "x"} {
		if got := wincredValue(wincredBlob(v)); got != v {
			t.Fatalf("round trip %q → %q", v, got)
		}
	}
	if got := wincredValue([]byte("utf8-pässword")); got != "utf8-pässword" {
		t.Fatalf("utf-8: %q", got)
	}
	if b := wincredBlob("ab"); string(b) != "a\x00b\x00" {
		t.Fatalf("not UTF-16LE: %q", b)
	}
	if got := wincredValue(nil); got != "" {
		t.Fatal(got)
	}
	own := wincredTargets(flow.KeychainRef{Service: "SpecWright", Account: "token"})
	other := wincredTargets(flow.KeychainRef{Service: "git:https://github.com", Account: "ann"})
	if len(own) != 1 || own[0] != "SpecWright/token" || len(other) != 2 || other[1] != "git:https://github.com" {
		t.Fatalf("%v %v", own, other)
	}
}
