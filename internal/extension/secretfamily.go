package extension

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// Secret families. A tool can declare a secret named with a trailing *
// (DB_*): it stands for every secret whose name starts that way. A call
// that writes ${DB_SALES} in its arguments gets DB_SALES as if the tool
// declared it: from the session, the environment or the keychain, or asked
// for. A tool gets only names in its own families, never another tool's
// secret (${GITHUB_TOKEN} in a DB_* tool's arguments stays unresolved).

// IsSecretFamily reports whether s stands for a family of secrets.
func IsSecretFamily(s Secret) bool { return strings.HasSuffix(s.Name, "*") }

var argSecretRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// WithArgSecrets returns e with the secrets its arguments name through one
// of its families added (each needed, as a declared secret is).
func WithArgSecrets(e Extension, args map[string]any) Extension {
	var fams []Secret
	for _, s := range e.Secrets {
		if IsSecretFamily(s) && len(strings.TrimSuffix(s.Name, "*")) > 0 {
			fams = append(fams, s)
		}
	}
	if len(fams) == 0 || len(args) == 0 {
		return e
	}
	b, err := json.Marshal(args)
	if err != nil {
		return e
	}
	declared := map[string]bool{}
	for _, s := range e.Secrets {
		declared[s.Name] = true
	}
	out := slices.Clone(e.Secrets)
	for _, m := range argSecretRE.FindAllStringSubmatch(string(b), -1) {
		name := m[1]
		if declared[name] {
			continue
		}
		for _, f := range fams {
			prefix := strings.TrimSuffix(f.Name, "*")
			if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
				desc := name
				if f.Desc != "" {
					desc = f.Desc + " — " + name
				}
				out = append(out, Secret{Name: name, Desc: desc, Source: f.Source})
				declared[name] = true
				break
			}
		}
	}
	e.Secrets = out
	return e
}
