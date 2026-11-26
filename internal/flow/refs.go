package flow

// Values from outside the flow, in templates:
//
//	{{env.NAME}}             a variable: the flow's own (Flow.Vars, a Chatflow's
//	                         environment), else the environment the app runs in
//	{{project_secret.NAME}}  a secret kept encrypted for this project
//	                         (Settings › Secrets)
//	{{keychain.NAME}}        a secret in the system keychain (SpecWright's
//	                         entries: service "SpecWright", account NAME)
//
// The secrets are looked up when the run starts (a missing one stops it
// before any step runs) and hidden from every output, event and saved file.

import (
	"regexp"
	"sort"
	"strings"
)

// Secret stores a template can read from.
const (
	RefEnv           = "env"
	RefProjectSecret = "project_secret"
	RefKeychain      = "keychain"
)

// RefNameRE is what a variable or secret name may be.
var RefNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

// refHead says whether a template variable's head is one of the stores
// (case does not matter: {{Keychain.X}} is {{keychain.X}}).
func refHead(head string) (string, bool) {
	switch h := strings.ToLower(head); h {
	case RefEnv, RefProjectSecret, RefKeychain:
		return h, true
	}
	return "", false
}

// SecretRef is a secret a flow's templates read.
type SecretRef struct {
	Store string `json:"store"` // project_secret | keychain
	Name  string `json:"name"`
}

// Key is how Data.Secrets holds its value: "<store>.<name>".
func (r SecretRef) Key() string { return r.Store + "." + r.Name }

var refRE = regexp.MustCompile(`(?i)\{\{\s*(env|project_secret|keychain)\.([^{}\s]+?)\s*\}\}`)

// TemplateRefs lists the env variables and secrets a text (a flow file,
// or one field) refers to.
func TemplateRefs(s string) (env []string, secrets []SecretRef) {
	seenE, seenS := map[string]bool{}, map[string]bool{}
	for _, m := range refRE.FindAllStringSubmatch(s, -1) {
		store, name := strings.ToLower(m[1]), m[2]
		if !RefNameRE.MatchString(name) {
			continue
		}
		if store == RefEnv {
			if !seenE[name] {
				seenE[name] = true
				env = append(env, name)
			}
			continue
		}
		r := SecretRef{Store: store, Name: name}
		if !seenS[r.Key()] {
			seenS[r.Key()] = true
			secrets = append(secrets, r)
		}
	}
	sort.Strings(env)
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Key() < secrets[j].Key() })
	return env, secrets
}

// lookupRef reads {{env.X}}, {{project_secret.X}} and {{keychain.X}}.
func lookupRef(store, name string, d Data) (string, bool) {
	if store == RefEnv {
		v, ok := d.Env[name]
		return v, ok
	}
	v, ok := d.Secrets[store+"."+name]
	return v, ok
}

// hasVar reports whether the flow declares a variable (or a Chatflow
// secret) of that name.
func (f *Flow) hasVar(name string) bool {
	for _, v := range f.Vars {
		if v.Name == name {
			return true
		}
	}
	for _, s := range f.Secrets {
		if s.Name == name {
			return true
		}
	}
	return false
}
