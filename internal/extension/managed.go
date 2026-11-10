package extension

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Managed tools are tools an admin publishes for the whole app: they are
// available in every workspace, not tied to one project. They live in an
// app-wide catalog (managed-tools.json in the app's data folder, plus an
// optional machine-wide catalog deployed by IT), each signed with the
// admin's Ed25519 key over its full contents. Anyone can read the file, but
// changing a tool (or adding one) without the admin key breaks the
// signature, and the app then refuses to run it. Regular users cannot edit,
// replace or shadow them from the app.

// ManagedTool is one signed entry.
type ManagedTool struct {
	Tool      Extension `json:"tool"`
	Version   int       `json:"version"`
	SignedBy  string    `json:"signedBy"` // key id (see KeyID)
	Signer    string    `json:"signer"`   // display name of the admin
	SignedAt  time.Time `json:"signedAt"`
	Signature string    `json:"signature"` // base64 Ed25519 over Payload()
}

type managedFile struct {
	Version int           `json:"version"`
	Tools   []ManagedTool `json:"tools"`
}

// KeyID is a short, stable id for a public key.
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "sw1-" + hex.EncodeToString(h[:8])
}

// signedView is exactly what a signature covers: everything that decides
// what runs and what the agent is told, plus who signed it and the version.
type signedView struct {
	Name     string   `json:"name"`
	Desc     string   `json:"desc"`
	Kind     string   `json:"kind"`
	Lang     string   `json:"lang"`
	Params   []Param  `json:"params"`
	Secrets  []Secret `json:"secrets"`
	Perms    []string `json:"perms"`
	Command  string   `json:"command"`
	Script   string   `json:"script"`
	Deps     []string `json:"deps"`
	Version  int      `json:"version"`
	SignedBy string   `json:"signedBy"`
	Signer   string   `json:"signer"`
	SignedAt string   `json:"signedAt"`
}

// Payload is the canonical bytes that are signed.
func (m ManagedTool) Payload() []byte {
	t := m.Tool
	v := signedView{Name: t.Name, Desc: t.Desc, Kind: t.Kind, Lang: t.Lang, Params: t.Params, Secrets: t.Secrets, Perms: t.Perms,
		Command: t.Command, Script: t.Script, Deps: t.Deps, Version: m.Version, SignedBy: m.SignedBy, Signer: m.Signer,
		SignedAt: m.SignedAt.UTC().Format(time.RFC3339)}
	if v.Params == nil {
		v.Params = []Param{}
	}
	if v.Secrets == nil {
		v.Secrets = []Secret{}
	}
	if v.Perms == nil {
		v.Perms = []string{}
	}
	if v.Deps == nil {
		v.Deps = []string{}
	}
	b, _ := json.Marshal(v)
	return append([]byte("specwright-managed-tool-v1\n"), b...)
}

// Sign fills SignedBy, SignedAt and Signature with priv.
func (m *ManagedTool) Sign(priv ed25519.PrivateKey, signer string) {
	m.SignedBy = KeyID(priv.Public().(ed25519.PublicKey))
	m.Signer = signer
	m.SignedAt = time.Now().UTC().Truncate(time.Second)
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.Payload()))
}

// Verdict of checking one managed tool.
const (
	ManagedVerified  = "verified"
	ManagedTampered  = "signature invalid"
	ManagedUntrusted = "signed by an untrusted key"
)

// Verify checks m against the trusted admin keys (key id → public key).
func (m ManagedTool) Verify(trusted map[string]ed25519.PublicKey) string {
	pub, ok := trusted[m.SignedBy]
	if !ok {
		return ManagedUntrusted
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || !ed25519.Verify(pub, m.Payload(), sig) {
		return ManagedTampered
	}
	return ManagedVerified
}

// ManagedStore is one managed-tools.json catalog file.
type ManagedStore struct {
	mu   sync.Mutex
	path string
}

// NewManagedStore opens the catalog at path (it need not exist yet).
func NewManagedStore(path string) *ManagedStore { return &ManagedStore{path: path} }

// Path of the catalog file.
func (s *ManagedStore) Path() string { return s.path }

// List reads every entry (verified or not).
func (s *ManagedStore) List() ([]ManagedTool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

func (s *ManagedStore) read() ([]ManagedTool, error) {
	b, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f managedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path(), err)
	}
	return f.Tools, nil
}

func (s *ManagedStore) write(ts []ManagedTool) error {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Tool.Name < ts[j].Tool.Name })
	b, err := json.MarshalIndent(managedFile{Version: 1, Tools: ts}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		return err
	}
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path())
}

// Put signs and stores e (a new version if it exists).
func (s *ManagedStore) Put(e Extension, priv ed25519.PrivateKey, signer string) (ManagedTool, error) {
	if !NameRE.MatchString(e.Name) {
		return ManagedTool{}, errors.New("name must start with a letter and use lowercase letters, digits, - and _ (max 40)")
	}
	if e.Kind == "npm" {
		return ManagedTool{}, errors.New("npm ext:* scripts live in package.json and cannot be published as managed tools")
	}
	e.Kind = "script"
	e.Scope = "managed"
	// what is signed is the content; per-user state does not travel
	e.Approved, e.Hash, e.ApprovedAt, e.Dir, e.ScriptPath = false, "", time.Time{}, "", ""
	e.Status = "managed"
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, err := s.read()
	if err != nil {
		return ManagedTool{}, err
	}
	m := ManagedTool{Tool: e, Version: 1}
	idx := -1
	for i, t := range ts {
		if t.Tool.Name == e.Name {
			idx, m.Version = i, t.Version+1
		}
	}
	m.Sign(priv, signer)
	if idx >= 0 {
		ts[idx] = m
	} else {
		ts = append(ts, m)
	}
	return m, s.write(ts)
}

// Remove deletes a managed tool.
func (s *ManagedStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, err := s.read()
	if err != nil {
		return err
	}
	out := ts[:0]
	for _, t := range ts {
		if t.Tool.Name != name {
			out = append(out, t)
		}
	}
	return s.write(out)
}

// Materialize writes a verified managed tool's script into dir (a private
// folder outside the workspace, keyed by the signature) and returns the
// tool pointed at that copy. Agents therefore always run the signed bytes,
// even if someone edits files in the workspace.
func Materialize(m ManagedTool, base string) (Extension, error) {
	h := sha256.Sum256([]byte(m.Signature))
	dir := filepath.Join(base, m.Tool.Name+"-v"+fmt.Sprint(m.Version)+"-"+hex.EncodeToString(h[:6]))
	e := m.Tool
	if HasDeps(e) {
		return WriteTool(dir, filepath.ToSlash(dir), e)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return e, err
	}
	p := filepath.Join(dir, e.Name+ScriptExt(e.Lang))
	if b, err := os.ReadFile(p); err != nil || string(b) != e.Script {
		if err := os.WriteFile(p, []byte(e.Script), 0o700); err != nil {
			return e, err
		}
	}
	e.Command = RepointScript(e.Command, e.Name, filepath.ToSlash(p))
	e.ScriptPath = filepath.ToSlash(p)
	return e, nil
}

// IsManagedScope reports whether scope marks a managed tool.
func IsManagedScope(scope string) bool { return strings.EqualFold(scope, "managed") }
