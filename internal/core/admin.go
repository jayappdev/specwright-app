package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"codegen/internal/extension"
)

// Admin mode
//
// An admin holds an Ed25519 signing key, encrypted on disk with a
// passphrase (PBKDF2-SHA256 + AES-256-GCM) in <store>/admin-key.json.
// Unlocking it for the session lets them publish managed tools: signed
// entries in the app-wide catalog <store>/managed-tools.json. They are
// global to the app (every workspace sees them), everyone can run them and
// nobody else can change them. IT can also deploy a read-only machine
// catalog next to the policy file (managed-tools.json in the same folder);
// a tool there takes precedence over one of the same name in the app catalog.
//
// Which admin keys are trusted:
//   - the machine policy file, which regular users cannot write (deployed by
//     IT/MDM):  macOS /Library/Application Support/SpecWright/policy.json,
//     Linux /etc/specwright/policy.json, Windows %ProgramData%\SpecWright\policy.json
//     {"admins":[{"name":"…","publicKey":"<base64>"}],"allowLocalTrust":false}
//   - unless the policy sets allowLocalTrust to false, also the user's own
//     list in <store>/trusted-admins.json (for single-user machines).

const adminIterations = 600_000

// MachineCatalogPath is the read-only, IT-deployed managed tool catalog.
func MachineCatalogPath() string {
	return filepath.Join(filepath.Dir(PolicyPath), "managed-tools.json")
}

// PolicyPath is where the machine policy is read from (overridable in tests).
var PolicyPath = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/SpecWright/policy.json"
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "SpecWright", "policy.json")
	}
	return "/etc/specwright/policy.json"
}()

type adminKeyFile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PublicKey  string `json:"publicKey"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Iterations int    `json:"iterations"`
	Created    string `json:"created"`
}

// TrustedAdmin is one admin public key this machine trusts.
type TrustedAdmin struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
	Source    string `json:"source"` // policy | local
}

type policyFile struct {
	Admins []struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	} `json:"admins"`
	AllowLocalTrust *bool `json:"allowLocalTrust,omitempty"`
	// DisabledBuiltinTools turns individual built-in tools off on this machine.
	DisabledBuiltinTools []string `json:"disabledBuiltinTools,omitempty"`
}

type localTrust struct {
	Admins []TrustedAdmin `json:"admins"`
}

// AdminStatus is what the Admin screen shows.
type AdminStatus struct {
	HasKey         bool           `json:"hasKey"`
	KeyID          string         `json:"keyId,omitempty"`
	Name           string         `json:"name,omitempty"`
	PublicKey      string         `json:"publicKey,omitempty"`
	Unlocked       bool           `json:"unlocked"`
	KeyTrusted     bool           `json:"keyTrusted"`
	Trusted        []TrustedAdmin `json:"trusted"`
	PolicyPath     string         `json:"policyPath"`
	PolicyFound    bool           `json:"policyFound"`
	PolicyError    string         `json:"policyError,omitempty"`
	AllowLocal     bool           `json:"allowLocal"`
	ManagedFile    string         `json:"managedFile"`    // app-wide catalog the admin publishes to
	MachineCatalog string         `json:"machineCatalog"` // optional, IT-deployed, read-only
}

func (h *Hub) adminKeyPath() string   { return filepath.Join(h.Store.Dir(), "admin-key.json") }
func (h *Hub) localTrustPath() string { return filepath.Join(h.Store.Dir(), "trusted-admins.json") }

func parsePub(b64 string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("not an Ed25519 public key (expected base64 of 32 bytes)")
	}
	return ed25519.PublicKey(b), nil
}

// trustedAdmins returns the trusted keys, whether local trust is allowed,
// and a policy problem if the file exists but cannot be used.
func (h *Hub) trustedAdmins() (list []TrustedAdmin, allowLocal, policyFound bool, policyErr string) {
	allowLocal = true
	if b, err := os.ReadFile(PolicyPath); err == nil {
		policyFound = true
		var pf policyFile
		if err := json.Unmarshal(b, &pf); err != nil {
			policyErr = "cannot read policy: " + err.Error()
		} else {
			if pf.AllowLocalTrust != nil {
				allowLocal = *pf.AllowLocalTrust
			}
			for _, a := range pf.Admins {
				if pub, err := parsePub(a.PublicKey); err == nil {
					list = append(list, TrustedAdmin{ID: extension.KeyID(pub), Name: a.Name, PublicKey: a.PublicKey, Source: "policy"})
				}
			}
		}
	}
	if allowLocal {
		var lt localTrust
		if b, err := os.ReadFile(h.localTrustPath()); err == nil && json.Unmarshal(b, &lt) == nil {
			for _, a := range lt.Admins {
				if pub, err := parsePub(a.PublicKey); err == nil {
					a.ID, a.Source = extension.KeyID(pub), "local"
					list = append(list, a)
				}
			}
		}
	}
	return
}

func (h *Hub) trustedKeys() map[string]ed25519.PublicKey {
	list, _, _, _ := h.trustedAdmins()
	m := map[string]ed25519.PublicKey{}
	for _, a := range list {
		if pub, err := parsePub(a.PublicKey); err == nil {
			m[a.ID] = pub
		}
	}
	return m
}

func (h *Hub) readAdminKey() (*adminKeyFile, error) {
	b, err := os.ReadFile(h.adminKeyPath())
	if err != nil {
		return nil, err
	}
	var k adminKeyFile
	return &k, json.Unmarshal(b, &k)
}

// AdminStatus reports the admin key, trust and lock state.
func (h *Hub) AdminStatus() AdminStatus {
	st := AdminStatus{PolicyPath: PolicyPath}
	st.Trusted, st.AllowLocal, st.PolicyFound, st.PolicyError = h.trustedAdmins()
	if st.Trusted == nil {
		st.Trusted = []TrustedAdmin{}
	}
	if k, err := h.readAdminKey(); err == nil {
		st.HasKey, st.KeyID, st.Name, st.PublicKey = true, k.ID, k.Name, k.PublicKey
		for _, t := range st.Trusted {
			st.KeyTrusted = st.KeyTrusted || t.ID == k.ID
		}
	}
	h.mu.RLock()
	st.Unlocked = h.adminPriv != nil
	h.mu.RUnlock()
	st.ManagedFile = h.managed.Path()
	st.MachineCatalog = MachineCatalogPath()
	return st
}

func deriveKey(pass string, salt []byte, iter int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, pass, salt, iter, 32)
}

// CreateAdminKey makes this user an admin: a new signing key, encrypted
// with passphrase, trusted locally, and unlocked for the session.
func (h *Hub) CreateAdminKey(name, passphrase string) (AdminStatus, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AdminStatus{}, errors.New("enter the admin's name (shown on every tool they publish)")
	}
	if len(passphrase) < 10 {
		return AdminStatus{}, errors.New("use a passphrase of at least 10 characters")
	}
	if _, err := os.Stat(h.adminKeyPath()); err == nil {
		return AdminStatus{}, errors.New("an admin key already exists on this computer; unlock it instead")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return AdminStatus{}, err
	}
	salt, nonce := make([]byte, 16), make([]byte, 12)
	_, _ = rand.Read(salt)
	_, _ = rand.Read(nonce)
	key, err := deriveKey(passphrase, salt, adminIterations)
	if err != nil {
		return AdminStatus{}, err
	}
	blk, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(blk)
	ct := gcm.Seal(nil, nonce, priv.Seed(), []byte("specwright-admin-key"))
	kf := adminKeyFile{ID: extension.KeyID(pub), Name: name, PublicKey: base64.StdEncoding.EncodeToString(pub),
		Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct), Iterations: adminIterations, Created: time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.MarshalIndent(kf, "", "  ")
	if err := os.WriteFile(h.adminKeyPath(), b, 0o600); err != nil {
		return AdminStatus{}, err
	}
	if err := h.TrustAdminKey(name, kf.PublicKey); err != nil && !strings.Contains(err.Error(), "policy") {
		return AdminStatus{}, err
	}
	h.mu.Lock()
	h.adminPriv, h.adminName = priv, name
	h.mu.Unlock()
	return h.AdminStatus(), nil
}

// UnlockAdmin decrypts the admin key for this session.
func (h *Hub) UnlockAdmin(passphrase string) (AdminStatus, error) {
	k, err := h.readAdminKey()
	if err != nil {
		return AdminStatus{}, errors.New("there is no admin key on this computer; create one first")
	}
	salt, _ := base64.StdEncoding.DecodeString(k.Salt)
	nonce, _ := base64.StdEncoding.DecodeString(k.Nonce)
	ct, _ := base64.StdEncoding.DecodeString(k.Ciphertext)
	key, err := deriveKey(passphrase, salt, k.Iterations)
	if err != nil {
		return AdminStatus{}, err
	}
	blk, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(blk)
	seed, err := gcm.Open(nil, nonce, ct, []byte("specwright-admin-key"))
	if err != nil || len(seed) != ed25519.SeedSize {
		time.Sleep(400 * time.Millisecond) // slow down guessing from the UI
		return AdminStatus{}, errors.New("wrong passphrase")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if extension.KeyID(priv.Public().(ed25519.PublicKey)) != k.ID {
		return AdminStatus{}, errors.New("the admin key file is damaged")
	}
	h.mu.Lock()
	h.adminPriv, h.adminName = priv, k.Name
	h.mu.Unlock()
	return h.AdminStatus(), nil
}

// LockAdmin forgets the decrypted key.
func (h *Hub) LockAdmin() {
	h.mu.Lock()
	h.adminPriv, h.adminName = nil, ""
	h.mu.Unlock()
}

// AdminPolicySnippet is the policy.json IT deploys to trust this admin.
func (h *Hub) AdminPolicySnippet() (string, error) {
	k, err := h.readAdminKey()
	if err != nil {
		return "", errors.New("create an admin key first")
	}
	p := map[string]any{"admins": []map[string]string{{"name": k.Name, "publicKey": k.PublicKey}}, "allowLocalTrust": false}
	b, _ := json.MarshalIndent(p, "", "  ")
	return string(b) + "\n", nil
}

// TrustAdminKey adds a public key to this user's local trust list.
func (h *Hub) TrustAdminKey(name, publicKey string) error {
	pub, err := parsePub(publicKey)
	if err != nil {
		return err
	}
	_, allowLocal, _, _ := h.trustedAdmins()
	if !allowLocal {
		return errors.New("the machine policy does not allow locally trusted admins; ask IT to add the key to " + PolicyPath)
	}
	var lt localTrust
	if b, err := os.ReadFile(h.localTrustPath()); err == nil {
		_ = json.Unmarshal(b, &lt)
	}
	id := extension.KeyID(pub)
	for _, a := range lt.Admins {
		if p, err := parsePub(a.PublicKey); err == nil && extension.KeyID(p) == id {
			return nil
		}
	}
	lt.Admins = append(lt.Admins, TrustedAdmin{ID: id, Name: strings.TrimSpace(name), PublicKey: base64.StdEncoding.EncodeToString(pub), Source: "local"})
	b, _ := json.MarshalIndent(lt, "", "  ")
	return os.WriteFile(h.localTrustPath(), b, 0o600)
}

// UntrustAdminKey removes a key from the local trust list (policy keys stay).
func (h *Hub) UntrustAdminKey(id string) error {
	var lt localTrust
	b, err := os.ReadFile(h.localTrustPath())
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(b, &lt)
	out := lt.Admins[:0]
	for _, a := range lt.Admins {
		if p, err := parsePub(a.PublicKey); err != nil || extension.KeyID(p) != id {
			out = append(out, a)
		}
	}
	lt.Admins = out
	nb, _ := json.MarshalIndent(lt, "", "  ")
	return os.WriteFile(h.localTrustPath(), nb, 0o600)
}

// --- managed tools ---------------------------------------------------------

// managedTools returns every managed tool with its check result, as the
// UI shows them (read-only, scope "managed").
func (h *Hub) managedTools() []extension.Extension {
	list := h.managedEntries()
	trusted := h.trustedKeys()
	out := make([]extension.Extension, 0, len(list))
	for _, m := range list {
		e := m.Tool
		e.Scope, e.Kind = "managed", "script"
		e.Managed = &extension.ManagedInfo{Version: m.Version, SignedBy: m.SignedBy, Signer: m.Signer, SignedAt: m.SignedAt, Status: m.Verify(trusted)}
		e.Approved = e.Managed.Status == extension.ManagedVerified
		e.Status = map[bool]string{true: "managed", false: "blocked"}[e.Approved]
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// managedRunnable returns the verified managed tools pointed at private,
// signed copies of their scripts (what the bridge actually runs).
func (h *Hub) managedRunnable() []extension.Extension {
	list := h.managedEntries()
	trusted := h.trustedKeys()
	base := filepath.Join(h.Store.Dir(), "managed")
	var out []extension.Extension
	for _, m := range list {
		if m.Verify(trusted) != extension.ManagedVerified {
			continue
		}
		e, err := extension.Materialize(m, base)
		if err != nil {
			continue
		}
		e.Scope, e.Status, e.Approved = "managed", "managed", true
		e.Managed = &extension.ManagedInfo{Version: m.Version, SignedBy: m.SignedBy, Signer: m.Signer, SignedAt: m.SignedAt, Status: extension.ManagedVerified}
		e.Hash = e.Fingerprint()
		out = append(out, e)
	}
	return out
}

// managedEntries merges the machine catalog (wins on a name clash) and the
// app catalog. Both are global: the same tools in every workspace.
func (h *Hub) managedEntries() []extension.ManagedTool {
	var out []extension.ManagedTool
	seen := map[string]bool{}
	for _, ms := range []*extension.ManagedStore{extension.NewManagedStore(MachineCatalogPath()), h.managed} {
		list, err := ms.List()
		if err != nil {
			continue
		}
		for _, m := range list {
			if !seen[m.Tool.Name] {
				seen[m.Tool.Name] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func (h *Hub) isManagedName(name string) bool {
	for _, e := range h.managedTools() {
		if e.Name == name {
			return true
		}
	}
	return false
}

func (h *Hub) adminKey() (ed25519.PrivateKey, string, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.adminPriv == nil {
		return nil, "", errors.New("unlock admin mode first (Admin screen)")
	}
	return h.adminPriv, h.adminName, nil
}

// SaveManagedTool signs and publishes a tool for everyone (admin only).
func (h *Hub) SaveManagedTool(e extension.Extension) (extension.Extension, error) {
	priv, name, err := h.adminKey()
	if err != nil {
		return e, err
	}
	if _, ok := h.trustedKeys()[extension.KeyID(priv.Public().(ed25519.PublicKey))]; !ok {
		return e, errors.New("your admin key is not trusted on this computer, so nobody here could run the tool; trust it on the Admin screen or deploy the policy file")
	}
	if list, _ := extension.NewManagedStore(MachineCatalogPath()).List(); len(list) > 0 {
		for _, t := range list {
			if t.Tool.Name == e.Name {
				return e, fmt.Errorf("%s comes from the machine catalog (%s); change it there", e.Name, MachineCatalogPath())
			}
		}
	}
	m, err := h.managed.Put(e, priv, name)
	if err != nil {
		return e, err
	}
	for _, t := range h.managedTools() {
		if t.Name == m.Tool.Name {
			return t, nil
		}
	}
	return m.Tool, nil
}

// DeleteManagedTool retires a managed tool (admin only).
func (h *Hub) DeleteManagedTool(name string) error {
	if _, _, err := h.adminKey(); err != nil {
		return err
	}
	return h.managed.Remove(name)
}

func managedRefusal(name string) error {
	return fmt.Errorf("%s is a managed tool published by an admin; it cannot be changed here", name)
}
