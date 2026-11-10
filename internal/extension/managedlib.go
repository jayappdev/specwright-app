package extension

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Managed libraries are the corporate (global) skills an admin publishes for
// the whole app, like managed tools: one app-wide catalog
// (managed-libraries.json in the app's data folder, plus an optional
// machine-wide catalog deployed by IT next to the policy file), each entry
// signed with the admin's Ed25519 key. Only verified entries reach a
// project: they are copied into <workspace>/.codegen/skills/global/ and
// overwritten there whenever they change, so editing the copy does nothing.

// LibraryNameRE is a valid library name (it becomes a folder name).
var LibraryNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,59}$`)

// ManagedLibrary is one signed corporate skill.
type ManagedLibrary struct {
	Name      string    `json:"name"`
	Desc      string    `json:"desc"`
	Content   string    `json:"content"` // SKILL.md (Markdown)
	Version   int       `json:"version"`
	SignedBy  string    `json:"signedBy"`
	Signer    string    `json:"signer"`
	SignedAt  time.Time `json:"signedAt"`
	Signature string    `json:"signature"`
	Status    string    `json:"status,omitempty"` // set when listed: verified | signature invalid | …
	Source    string    `json:"source,omitempty"` // app | machine
}

// Payload is the canonical bytes that are signed.
func (m ManagedLibrary) Payload() []byte {
	b, _ := json.Marshal(struct {
		Name, Desc, Content string
		Version             int
		SignedBy, Signer    string
		SignedAt            string
	}{m.Name, m.Desc, m.Content, m.Version, m.SignedBy, m.Signer, m.SignedAt.UTC().Format(time.RFC3339)})
	return append([]byte("specwright-managed-library-v1\n"), b...)
}

// Sign fills SignedBy, Signer, SignedAt and Signature.
func (m *ManagedLibrary) Sign(priv ed25519.PrivateKey, signer string) {
	m.SignedBy = KeyID(priv.Public().(ed25519.PublicKey))
	m.Signer = signer
	m.SignedAt = time.Now().UTC().Truncate(time.Second)
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m.Payload()))
}

// Verify checks m against the trusted admin keys.
func (m ManagedLibrary) Verify(trusted map[string]ed25519.PublicKey) string {
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

// LibraryStore is one managed-libraries.json catalog.
type LibraryStore struct {
	mu   sync.Mutex
	path string
}

// NewLibraryStore opens the catalog at path (it need not exist yet).
func NewLibraryStore(path string) *LibraryStore { return &LibraryStore{path: path} }

// Path of the catalog file.
func (s *LibraryStore) Path() string { return s.path }

type libraryFile struct {
	Version   int              `json:"version"`
	Libraries []ManagedLibrary `json:"libraries"`
}

// List reads every entry (verified or not).
func (s *LibraryStore) List() ([]ManagedLibrary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

func (s *LibraryStore) read() ([]ManagedLibrary, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f libraryFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return f.Libraries, nil
}

func (s *LibraryStore) write(ls []ManagedLibrary) error {
	sort.Slice(ls, func(i, j int) bool { return ls[i].Name < ls[j].Name })
	for i := range ls {
		ls[i].Status, ls[i].Source = "", ""
	}
	b, err := json.MarshalIndent(libraryFile{Version: 1, Libraries: ls}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Put signs and stores a library (a new version if the name exists).
func (s *LibraryStore) Put(name, desc, content string, priv ed25519.PrivateKey, signer string) (ManagedLibrary, error) {
	name = strings.TrimSpace(name)
	if !LibraryNameRE.MatchString(name) {
		return ManagedLibrary{}, errors.New("name must start with a lowercase letter or digit and use lowercase letters, digits, '.', '-' and '_' (max 60)")
	}
	if strings.TrimSpace(content) == "" {
		return ManagedLibrary{}, errors.New("the skill is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ls, err := s.read()
	if err != nil {
		return ManagedLibrary{}, err
	}
	m := ManagedLibrary{Name: name, Desc: strings.TrimSpace(desc), Content: content, Version: 1}
	idx := -1
	for i, l := range ls {
		if l.Name == name {
			idx, m.Version = i, l.Version+1
		}
	}
	m.Sign(priv, signer)
	if idx >= 0 {
		ls[idx] = m
	} else {
		ls = append(ls, m)
	}
	return m, s.write(ls)
}

// Remove deletes a library from the catalog.
func (s *LibraryStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls, err := s.read()
	if err != nil {
		return err
	}
	out := ls[:0]
	for _, l := range ls {
		if l.Name != name {
			out = append(out, l)
		}
	}
	return s.write(out)
}
