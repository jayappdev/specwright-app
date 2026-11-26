package core

// Project secrets: values a project's flows read as
// {{project_secret.NAME}}, kept encrypted in the project
// (.codegen/secrets/secrets.json, AES-GCM). The key stays on this computer
// (the system keychain, else a 0600 file in the app's folder: the one
// trigger secrets use), so the file is useless anywhere else; it is kept out
// of git too. Values never go back to the UI: names, descriptions and when
// they were set only.
//
// {{keychain.NAME}} reads the system keychain directly (SpecWright's
// entries), and {{env.NAME}} the flow's variables, then the environment.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codegen/internal/flow"
)

// ProjectSecretsDir holds the encrypted file, relative to the project.
const ProjectSecretsDir = ".codegen/secrets"

// ProjectSecret is what the UI sees of a project secret.
type ProjectSecret struct {
	Name    string    `json:"name"`
	Desc    string    `json:"desc,omitempty"`
	Updated time.Time `json:"updated"`
	// Readable: this computer can decrypt it (another computer's key: set it again)
	Readable bool `json:"readable"`
}

type projectSecretFile struct {
	Version int                         `json:"version"`
	Secrets map[string]projectSecretRec `json:"secrets"`
}

type projectSecretRec struct {
	Sealed  string    `json:"sealed"` // base64 nonce+ciphertext
	Desc    string    `json:"desc,omitempty"`
	Updated time.Time `json:"updated"`
}

var projectSecretsMu sync.Mutex

func projectSecretsPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(ProjectSecretsDir), "secrets.json")
}

func readProjectSecrets(root string) (projectSecretFile, error) {
	pf := projectSecretFile{Version: 1, Secrets: map[string]projectSecretRec{}}
	b, err := os.ReadFile(projectSecretsPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return pf, nil
	}
	if err != nil {
		return pf, err
	}
	if err := json.Unmarshal(b, &pf); err != nil {
		return pf, fmt.Errorf("the project's secrets file is damaged: %v", err)
	}
	if pf.Secrets == nil {
		pf.Secrets = map[string]projectSecretRec{}
	}
	return pf, nil
}

func writeProjectSecrets(root string, pf projectSecretFile) error {
	dir := filepath.Dir(projectSecretsPath(root))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	ign := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(ign); err != nil {
		_ = os.WriteFile(ign, []byte("# project secrets (SpecWright): encrypted with this computer's key; not shared\n*\n"), 0o644)
	}
	b, _ := json.MarshalIndent(pf, "", "  ")
	tmp := projectSecretsPath(root) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, projectSecretsPath(root))
}

// ProjectSecrets lists the project's secrets (never their values).
func (h *Hub) ProjectSecrets() ([]ProjectSecret, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	projectSecretsMu.Lock()
	pf, err := readProjectSecrets(root)
	projectSecretsMu.Unlock()
	if err != nil {
		return nil, err
	}
	key, _, kerr := h.triggerKey()
	out := []ProjectSecret{}
	for name, r := range pf.Secrets {
		ps := ProjectSecret{Name: name, Desc: r.Desc, Updated: r.Updated}
		if kerr == nil {
			_, err := unseal(key, r.Sealed)
			ps.Readable = err == nil
		}
		out = append(out, ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetProjectSecret saves (or replaces) a project secret; an empty value
// keeps the value and changes only the description.
func (h *Hub) SetProjectSecret(name, value, desc string) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if !flow.RefNameRE.MatchString(name) {
		return fmt.Errorf("name %q: start with a letter or _, then letters, digits, _ . or -", name)
	}
	projectSecretsMu.Lock()
	defer projectSecretsMu.Unlock()
	pf, err := readProjectSecrets(root)
	if err != nil {
		return err
	}
	r, had := pf.Secrets[name]
	if value == "" && !had {
		return fmt.Errorf("give %s a value", name)
	}
	if value != "" {
		key, _, err := h.triggerKey()
		if err != nil {
			return fmt.Errorf("could not get the encryption key: %v", err)
		}
		if r.Sealed, err = seal(key, value); err != nil {
			return err
		}
		h.Store.Redactor().Add(value)
	}
	r.Desc, r.Updated = strings.TrimSpace(desc), time.Now()
	pf.Secrets[name] = r
	return writeProjectSecrets(root, pf)
}

// DeleteProjectSecret removes a project secret.
func (h *Hub) DeleteProjectSecret(name string) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	projectSecretsMu.Lock()
	defer projectSecretsMu.Unlock()
	pf, err := readProjectSecrets(root)
	if err != nil {
		return err
	}
	if _, ok := pf.Secrets[name]; !ok {
		return fmt.Errorf("there is no project secret %q", name)
	}
	delete(pf.Secrets, name)
	return writeProjectSecrets(root, pf)
}

// projectSecret reads one project secret's value.
func (h *Hub) projectSecret(root, name string) (string, error) {
	projectSecretsMu.Lock()
	pf, err := readProjectSecrets(root)
	projectSecretsMu.Unlock()
	if err != nil {
		return "", err
	}
	r, ok := pf.Secrets[name]
	if !ok {
		return "", fmt.Errorf("there is no project secret %s: add it in Settings › Secrets", name)
	}
	key, _, err := h.triggerKey()
	if err != nil {
		return "", fmt.Errorf("could not get the encryption key: %v", err)
	}
	v, err := unseal(key, r.Sealed)
	if err != nil {
		return "", fmt.Errorf("project secret %s: %v", name, err)
	}
	return v, nil
}

// refSecrets looks up the secrets f's templates read
// ({{project_secret.X}}, {{keychain.X}}), hiding each value from outputs,
// events and saved files. A missing one stops the run before it starts.
func (h *Hub) refSecrets(root string, f *flow.Flow) (map[string]string, error) {
	b, _ := json.Marshal(f)
	_, refs := flow.TemplateRefs(string(b))
	out := map[string]string{}
	var errs []string
	for _, r := range refs {
		var v string
		var err error
		switch r.Store {
		case flow.RefProjectSecret:
			v, err = h.projectSecret(root, r.Name)
		case flow.RefKeychain:
			if !KeychainAvailable() {
				err = fmt.Errorf("{{keychain.%s}}: this computer has no keychain to read", r.Name)
			} else if x, ok := kcGet(r.Name); ok {
				v = x
			} else {
				err = fmt.Errorf("{{keychain.%s}}: nothing in %s; add it in Settings › Secrets or with %s", r.Name, describeRef(ownRef(r.Name)), keychainApp())
			}
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		h.Store.Redactor().Add(v)
		out[r.Key()] = v
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return out, nil
}

// flowEnv is {{env.NAME}}'s values for a run: the flow's variables (the
// environment's value of the same name wins), and a Chatflow session's
// environment (its variables and secrets). Names the flow does not declare
// are read from the environment when used (flowEnvLookup).
func flowEnv(f *flow.Flow, o FlowRunOptions) map[string]string {
	env := map[string]string{}
	b, _ := json.Marshal(f)
	used, _ := flow.TemplateRefs(string(b))
	for _, name := range used {
		if v, ok := lookupEnv(name); ok {
			env[name] = v
		}
	}
	for _, v := range f.Vars {
		if x, ok := lookupEnv(v.Name); ok {
			env[v.Name] = x
		} else {
			env[v.Name] = v.Value
		}
	}
	for k, v := range o.Env {
		env[k] = v
	}
	return env
}

// SecretRefUse is a secret the project's flows read, where it comes from,
// and whether it has a value on this computer.
type SecretRefUse struct {
	Store string   `json:"store"` // project_secret | keychain
	Name  string   `json:"name"`
	Flows []string `json:"flows"` // the flows that read it (names)
	Found bool     `json:"found"`
}

// SecretRefUses lists the {{project_secret.X}} and {{keychain.X}} the
// project's flows use, so the Secrets settings can show what is missing.
func (h *Hub) SecretRefUses() ([]SecretRefUse, error) {
	root, err := h.need()
	if err != nil {
		return nil, err
	}
	sums, err := flow.List(root)
	if err != nil {
		return nil, err
	}
	pf, _ := readProjectSecrets(root)
	by := map[string]*SecretRefUse{}
	var keys []string
	for _, s := range sums {
		f, err := flow.Load(root, s.ID)
		if err != nil {
			continue
		}
		b, _ := json.Marshal(f)
		_, refs := flow.TemplateRefs(string(b))
		for _, r := range refs {
			u := by[r.Key()]
			if u == nil {
				u = &SecretRefUse{Store: r.Store, Name: r.Name, Flows: []string{}}
				switch r.Store {
				case flow.RefProjectSecret:
					_, u.Found = pf.Secrets[r.Name]
				case flow.RefKeychain:
					if KeychainAvailable() {
						_, u.Found = kcGet(r.Name)
					}
				}
				by[r.Key()] = u
				keys = append(keys, r.Key())
			}
			u.Flows = append(u.Flows, f.Name)
		}
	}
	sort.Strings(keys)
	out := []SecretRefUse{}
	for _, k := range keys {
		out = append(out, *by[k])
	}
	return out, nil
}
