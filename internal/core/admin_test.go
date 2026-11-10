package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codegen/internal/extension"
)

func usePolicy(t *testing.T, content string) {
	old := PolicyPath
	PolicyPath = filepath.Join(t.TempDir(), "policy.json")
	if content != "" {
		_ = os.WriteFile(PolicyPath, []byte(content), 0o644)
	}
	t.Cleanup(func() { PolicyPath = old })
}

func adminTool() extension.Extension {
	return extension.Extension{Name: "corp-lint", Desc: "Run the corporate linter.", Lang: "Bash",
		Command: "bash .codegen/tools/corp-lint.sh", Script: "#!/usr/bin/env bash\necho corp-lint-ok\n"}
}

func TestAdminPublishesGlobalReadOnlyTool(t *testing.T) {
	usePolicy(t, "")
	h, _ := newHub(t)
	if _, err := h.SaveManagedTool(adminTool()); err == nil {
		t.Fatal("publishing must need admin mode")
	}
	if _, err := h.CreateAdminKey("Ada Admin", "short"); err == nil {
		t.Fatal("short passphrase must be refused")
	}
	st, err := h.CreateAdminKey("Ada Admin", "correct horse battery")
	if err != nil || !st.Unlocked || !st.KeyTrusted {
		t.Fatalf("%+v %v", st, err)
	}
	h.LockAdmin()
	if _, err := h.UnlockAdmin("wrong passphrase!"); err == nil {
		t.Fatal("wrong passphrase must fail")
	}
	if _, err := h.UnlockAdmin("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	pub, err := h.SaveManagedTool(adminTool())
	if err != nil || pub.Managed == nil || pub.Managed.Status != extension.ManagedVerified || pub.Managed.Signer != "Ada Admin" {
		t.Fatalf("%+v %v", pub, err)
	}
	h.LockAdmin()

	// read-only for a regular user
	if _, err := h.SaveExtension(adminTool()); err == nil || !strings.Contains(err.Error(), "managed") {
		t.Fatalf("user must not overwrite a managed tool: %v", err)
	}
	if err := h.DeleteExtension("corp-lint"); err == nil {
		t.Fatal("user must not delete a managed tool")
	}
	if err := h.DeleteManagedTool("corp-lint"); err == nil {
		t.Fatal("retiring needs admin mode")
	}

	// global: a different workspace sees and can run it
	other := filepath.Join(t.TempDir(), "another project")
	_ = os.MkdirAll(other, 0o755)
	if _, err := h.OpenWorkspace(other); err != nil {
		t.Fatal(err)
	}
	var found *extension.Extension
	for _, e := range h.Callable() {
		if e.Name == "corp-lint" {
			found = &e
		}
	}
	if found == nil {
		t.Fatal("managed tool must be callable in every workspace")
	}
	sec, _ := h.ResolveSecrets(context.Background(), *found)
	if r := extension.Exec(context.Background(), other, *found, nil, sec, nil, 0); !r.OK || r.Output != "corp-lint-ok" {
		t.Fatalf("%+v", r)
	}
	if w := extension.SplitWords(found.Command); len(w) < 2 || !strings.HasPrefix(w[1], filepath.ToSlash(filepath.Join(h.Store.Dir(), "managed"))) {
		t.Fatalf("must run the private signed copy, got %s", found.Command)
	}

	// tampering with the catalog by hand blocks the tool
	b, _ := os.ReadFile(h.managed.Path())
	_ = os.WriteFile(h.managed.Path(), []byte(strings.Replace(string(b), "corp-lint-ok", "rm -rf ~", 1)), 0o644)
	for _, e := range h.ManagedTools() {
		if e.Name == "corp-lint" && (e.Managed.Status != extension.ManagedTampered || e.Approved) {
			t.Fatalf("tampered tool must be blocked: %+v", e.Managed)
		}
	}
	for _, e := range h.Callable() {
		if e.Name == "corp-lint" {
			t.Fatal("tampered tool must not be callable")
		}
	}
}

func TestUntrustedAdminAndPolicy(t *testing.T) {
	usePolicy(t, "")
	h, _ := newHub(t)
	st, _ := h.CreateAdminKey("Ada Admin", "correct horse battery")
	if _, err := h.SaveManagedTool(adminTool()); err != nil {
		t.Fatal(err)
	}
	// a machine policy that forbids local trust and names another key
	usePolicy(t, `{"admins":[{"name":"IT","publicKey":"`+strings.Repeat("A", 43)+`="}],"allowLocalTrust":false}`)
	st = h.AdminStatus()
	if !st.PolicyFound || st.AllowLocal || st.KeyTrusted {
		t.Fatalf("%+v", st)
	}
	for _, e := range h.ManagedTools() {
		if e.Managed.Status != extension.ManagedUntrusted {
			t.Fatalf("key not in policy must be untrusted: %+v", e.Managed)
		}
	}
	if err := h.TrustAdminKey("me", st.PublicKey); err == nil {
		t.Fatal("policy forbids local trust")
	}
	if _, err := h.SaveManagedTool(adminTool()); err == nil {
		t.Fatal("an untrusted admin key must not publish")
	}
	if snip, err := h.AdminPolicySnippet(); err != nil || !strings.Contains(snip, st.PublicKey) {
		t.Fatalf("%s %v", snip, err)
	}
}
