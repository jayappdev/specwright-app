package core

// The key that encrypts trigger secrets and project secrets
// (docs/security/2026-11-26-security-review.md, item 8).
//
// It lives in the system keychain when there is one. Without one it is a
// file in the app's folder (0600), which keeps the secrets safe only from
// someone who copies the secrets file alone: any program running as the
// user can read both. Settings › Secrets says so, and offers to lock the
// key with a passphrase (PBKDF2-SHA256 + AES-256-GCM, as the admin key).
// A locked key is unlocked once per app start; until then flows and
// triggers that need a secret stop with a message saying where to unlock.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codegen/internal/flow"
)

const triggerKeyAccount = "SpecWright trigger secrets key"

// secretKeyAAD binds the sealed key to its purpose.
const secretKeyAAD = "specwright-secrets-key"

// ErrSecretsLocked: the key is protected by a passphrase not typed in yet.
var ErrSecretsLocked = errors.New("the secrets are locked: unlock them with the passphrase in Settings › Secrets")

// keychainUsable is replaceable in tests.
var keychainUsable = KeychainAvailable

// SecretKeyStatus is where the secrets key is kept (Settings › Secrets).
type SecretKeyStatus struct {
	Store  string `json:"store"`  // keychain | file | passphrase
	Locked bool   `json:"locked"` // passphrase: not unlocked in this session
	Path   string `json:"path,omitempty"`
}

type lockedKeyFile struct {
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Iterations int    `json:"iterations"`
	Created    string `json:"created"`
}

func (h *Hub) plainKeyPath() string  { return filepath.Join(h.Store.Dir(), "triggers", "key") }
func (h *Hub) lockedKeyPath() string { return filepath.Join(h.Store.Dir(), "triggers", "key.locked") }

func decodeKey(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != 32 {
		return nil
	}
	return b
}

// triggerKey returns the key trigger and project secrets are encrypted
// with, making one the first time: in the system keychain, else in a file.
// The second result says where it is (keychain, file, passphrase).
func (h *Hub) triggerKey() ([]byte, string, error) {
	ref := flow.KeychainRef{Service: keychainService, Account: triggerKeyAccount}
	if keychainUsable() {
		if v, ok := keychainRead(ref); ok {
			if k := decodeKey(v); k != nil {
				return k, "keychain", nil
			}
		}
	}
	if _, err := os.Stat(h.lockedKeyPath()); err == nil {
		h.mu.RLock()
		k := h.secretKey
		h.mu.RUnlock()
		if k == nil {
			return nil, "passphrase", ErrSecretsLocked
		}
		return k, "passphrase", nil
	}
	file := h.plainKeyPath()
	if b, err := os.ReadFile(file); err == nil {
		if k := decodeKey(string(b)); k != nil {
			return k, "file", nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, "", err
	}
	enc := base64.StdEncoding.EncodeToString(k)
	if keychainUsable() {
		if err := keychainWrite(ref, enc); err == nil {
			return k, "keychain", nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(file, []byte(enc), 0o600); err != nil {
		return nil, "", err
	}
	return k, "file", nil
}

// SecretKeyStatus reports where the secrets key is kept.
func (h *Hub) SecretKeyStatus() SecretKeyStatus {
	if keychainUsable() {
		if v, ok := keychainRead(flow.KeychainRef{Service: keychainService, Account: triggerKeyAccount}); ok && decodeKey(v) != nil {
			return SecretKeyStatus{Store: "keychain"}
		}
	}
	if _, err := os.Stat(h.lockedKeyPath()); err == nil {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return SecretKeyStatus{Store: "passphrase", Locked: h.secretKey == nil, Path: h.lockedKeyPath()}
	}
	if _, err := os.Stat(h.plainKeyPath()); err == nil || !keychainUsable() {
		return SecretKeyStatus{Store: "file", Path: h.plainKeyPath()}
	}
	return SecretKeyStatus{Store: "keychain"} // made there on first use
}

// ProtectSecretKey locks the file-kept key with a passphrase: the plain
// file is replaced by an encrypted one, and the key stays unlocked for
// this session.
func (h *Hub) ProtectSecretKey(passphrase string) (SecretKeyStatus, error) {
	if len(passphrase) < 10 {
		return h.SecretKeyStatus(), errors.New("use a passphrase of at least 10 characters")
	}
	if st := h.SecretKeyStatus(); st.Store != "file" {
		return st, errors.New("only a key kept in a file can be locked with a passphrase")
	}
	key, where, err := h.triggerKey() // makes one if there is none yet
	if err != nil {
		return h.SecretKeyStatus(), err
	}
	if where != "file" {
		return h.SecretKeyStatus(), nil // it went to the keychain after all
	}
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return h.SecretKeyStatus(), err
	}
	if _, err := rand.Read(nonce); err != nil {
		return h.SecretKeyStatus(), err
	}
	dk, err := deriveKey(passphrase, salt, adminIterations)
	if err != nil {
		return h.SecretKeyStatus(), err
	}
	blk, _ := aes.NewCipher(dk)
	gcm, _ := cipher.NewGCM(blk)
	lf := lockedKeyFile{
		Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, key, []byte(secretKeyAAD))),
		Iterations: adminIterations, Created: time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(lf, "", "  ")
	tmp := h.lockedKeyPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return h.SecretKeyStatus(), err
	}
	if err := os.Rename(tmp, h.lockedKeyPath()); err != nil {
		return h.SecretKeyStatus(), err
	}
	// overwrite, then remove the plain copy
	_ = os.WriteFile(h.plainKeyPath(), make([]byte, 44), 0o600)
	if err := os.Remove(h.plainKeyPath()); err != nil {
		return h.SecretKeyStatus(), err
	}
	h.mu.Lock()
	h.secretKey = key
	h.mu.Unlock()
	return h.SecretKeyStatus(), nil
}

// UnlockSecretKey decrypts the passphrase-locked key for this session.
func (h *Hub) UnlockSecretKey(passphrase string) (SecretKeyStatus, error) {
	b, err := os.ReadFile(h.lockedKeyPath())
	if err != nil {
		return h.SecretKeyStatus(), errors.New("the secrets key is not locked with a passphrase")
	}
	var lf lockedKeyFile
	if err := json.Unmarshal(b, &lf); err != nil {
		return h.SecretKeyStatus(), errors.New("the locked key file is damaged")
	}
	salt, _ := base64.StdEncoding.DecodeString(lf.Salt)
	nonce, _ := base64.StdEncoding.DecodeString(lf.Nonce)
	ct, _ := base64.StdEncoding.DecodeString(lf.Ciphertext)
	dk, err := deriveKey(passphrase, salt, lf.Iterations)
	if err != nil {
		return h.SecretKeyStatus(), err
	}
	blk, _ := aes.NewCipher(dk)
	gcm, err := cipher.NewGCM(blk)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return h.SecretKeyStatus(), errors.New("the locked key file is damaged")
	}
	key, err := gcm.Open(nil, nonce, ct, []byte(secretKeyAAD))
	if err != nil || len(key) != 32 {
		time.Sleep(400 * time.Millisecond) // slow down guessing from the UI
		return h.SecretKeyStatus(), errors.New("wrong passphrase")
	}
	h.mu.Lock()
	h.secretKey = key
	h.mu.Unlock()
	return h.SecretKeyStatus(), nil
}

// LockSecretKey forgets the unlocked key until the passphrase is typed again.
func (h *Hub) LockSecretKey() SecretKeyStatus {
	h.mu.Lock()
	h.secretKey = nil
	h.mu.Unlock()
	return h.SecretKeyStatus()
}
