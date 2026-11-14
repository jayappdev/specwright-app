package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"codegen/internal/flow"
)

// The system keychain stores secret values the user chose to keep between
// sessions: the macOS Keychain (security) or the Secret Service on Linux
// (secret-tool, from libsecret). SpecWright's own entries use the service
// "SpecWright" and the secret's name as the account. A flow's secret input
// can also read any other entry by service (or, for internet passwords, the
// server) and account; macOS asks the first time before the security
// command reads an entry another app made. Windows has no command-line reader, so
// the keychain is unavailable there.
//
// Values never appear on a command line (where other processes could list
// them): reads print them to SpecWright only, and saves go through
// security's interactive mode (macOS) or secret-tool's standard input.

const keychainService = flow.DefaultKeychainService

// keychainRead is replaceable in tests.
var keychainRead = func(ref flow.KeychainRef) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // macOS may ask the user first
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		what := "find-generic-password"
		if ref.Kind == "internet" {
			what = "find-internet-password"
		}
		cmd = exec.CommandContext(ctx, "security", what, "-s", ref.Service, "-a", ref.Account, "-w")
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return "", false
		}
		cmd = exec.CommandContext(ctx, "secret-tool", "lookup", "service", ref.Service, "account", ref.Account)
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

// keychainWrite is replaceable in tests.
var keychainWrite = func(ref flow.KeychainRef, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("a value saved in the keychain must be on one line")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	label := "SpecWright: " + ref.Account
	switch runtime.GOOS {
	case "darwin":
		if ref.Kind == "internet" {
			return errors.New("SpecWright saves passwords as keychain passwords (generic), not internet passwords")
		}
		// interactive mode reads the command from standard input, so the
		// value is not in the process list; -U updates an existing entry
		cmd := exec.CommandContext(ctx, "security", "-i")
		cmd.Stdin = strings.NewReader(securityLine("add-generic-password", "-U", "-s", ref.Service, "-a", ref.Account, "-l", label, "-w", value) + "\n")
		out, err := cmd.CombinedOutput()
		if msg := strings.TrimSpace(string(out)); err != nil || strings.Contains(msg, "returned -") || strings.Contains(msg, "SecKeychain") {
			if msg == "" && err != nil {
				msg = err.Error()
			}
			return errors.New("the keychain refused it: " + msg)
		}
		return nil
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err != nil {
			return errors.New("secret-tool (libsecret) is not installed")
		}
		cmd := exec.CommandContext(ctx, "secret-tool", "store", "--label", label, "service", ref.Service, "account", ref.Account)
		cmd.Stdin = strings.NewReader(value)
		return run(cmd)
	}
	return errors.New("the system keychain is not supported on " + runtime.GOOS)
}

// securityLine quotes a command for `security -i`: each argument in double
// quotes, with backslashes and double quotes escaped.
func securityLine(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a) + `"`
	}
	return strings.Join(q, " ")
}

var keychainRemove = func(ref flow.KeychainRef) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		return run(exec.CommandContext(ctx, "security", "delete-generic-password", "-s", ref.Service, "-a", ref.Account))
	case "linux":
		return run(exec.CommandContext(ctx, "secret-tool", "clear", "service", ref.Service, "account", ref.Account))
	}
	return errors.New("the system keychain is not supported on " + runtime.GOOS)
}

// SpecWright's own entry for a tool secret.
func ownRef(name string) flow.KeychainRef {
	return flow.KeychainRef{Service: keychainService, Account: name}
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

// kcGet is keychainRead with a short cache: the UI asks for secret status
// often and each lookup starts a process.
var kc = struct {
	sync.Mutex
	m map[string]kcEntry
}{m: map[string]kcEntry{}}

type kcEntry struct {
	v  string
	ok bool
	at time.Time
}

func kcKey(ref flow.KeychainRef) string {
	return ref.Kind + "\x00" + ref.Service + "\x00" + ref.Account
}

func kcGetRef(ref flow.KeychainRef) (string, bool) {
	k := kcKey(ref)
	kc.Lock()
	e, hit := kc.m[k]
	kc.Unlock()
	if hit && time.Since(e.at) < 20*time.Second {
		return e.v, e.ok
	}
	v, ok := keychainRead(ref)
	kc.Lock()
	kc.m[k] = kcEntry{v, ok, time.Now()}
	kc.Unlock()
	return v, ok
}

func kcGet(name string) (string, bool) { return kcGetRef(ownRef(name)) }

func kcForgetRef(ref flow.KeychainRef) { kc.Lock(); delete(kc.m, kcKey(ref)); kc.Unlock() }

func kcForget(name string) { kcForgetRef(ownRef(name)) }

// SaveSecretToKeychain keeps a secret value in the system keychain so later
// sessions (and flow runs) find it without asking.
func (h *Hub) SaveSecretToKeychain(name, value string) error {
	if strings.TrimSpace(name) == "" || value == "" {
		return errors.New("a name and a value are needed")
	}
	h.Store.Redactor().Add(value)
	defer kcForget(name)
	return keychainWrite(ownRef(name), value)
}

// RemoveSecretFromKeychain deletes a stored value.
func (h *Hub) RemoveSecretFromKeychain(name string) error {
	defer kcForget(name)
	return keychainRemove(ownRef(name))
}

// KeychainStatus says whether a keychain entry exists, without returning
// its value (the value never goes to the screen).
type KeychainStatus struct {
	Available bool   `json:"available"`
	Found     bool   `json:"found"`
	Where     string `json:"where"` // e.g. "Keychain · SpecWright / github-token"
}

// CheckKeychain reports whether ref has a value.
func (h *Hub) CheckKeychain(ref flow.KeychainRef) KeychainStatus {
	ref = ref.Normalized("")
	st := KeychainStatus{Available: KeychainAvailable(), Where: ref.Describe()}
	if st.Available && ref.Account != "" {
		_, st.Found = kcGetRef(ref)
	}
	return st
}

// SaveToKeychain stores value at ref (SpecWright's entries or any generic
// password the user names).
func (h *Hub) SaveToKeychain(ref flow.KeychainRef, value string) error {
	ref = ref.Normalized("")
	if ref.Account == "" || value == "" {
		return errors.New("an account and a value are needed")
	}
	h.Store.Redactor().Add(value)
	defer kcForgetRef(ref)
	if err := keychainWrite(ref, value); err != nil {
		return fmt.Errorf("could not save to %s: %v", ref.Describe(), err)
	}
	return nil
}

// SecretInputState tells the run form where a secret input's value will
// come from, without the value.
type SecretInputState struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Found  bool   `json:"found"` // keychain / env / auto: a value is there
	Where  string `json:"where"` // e.g. "Keychain · SpecWright / token", "$API_TOKEN"
}

// FlowSecretStatus reports, for each secret input of flow id, whether its
// value will be found without typing it.
func (h *Hub) FlowSecretStatus(id string) ([]SecretInputState, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	f, err := flow.Load(root, id)
	if err != nil {
		return nil, err
	}
	out := []SecretInputState{}
	for _, in := range f.Inputs {
		if in.Type != "secret" {
			continue
		}
		st := SecretInputState{Name: in.Name, Source: in.SecretSource()}
		kcs := func() bool { k := h.CheckKeychain(in.KeychainFor()); st.Where = k.Where; return k.Found }
		env := func() bool {
			if in.Env == "" {
				return false
			}
			st.Where = "$" + in.Env
			v, ok := lookupEnv(in.Env)
			return ok && v != ""
		}
		switch st.Source {
		case "keychain":
			st.Found = kcs()
		case "env":
			st.Found = env()
		case "auto":
			if st.Found = kcs(); !st.Found {
				where := st.Where
				if st.Found = env(); !st.Found && in.Env != "" {
					st.Where = where + " or $" + in.Env
				} else if !st.Found {
					st.Where = where
				}
			}
		}
		out = append(out, st)
	}
	return out, nil
}
