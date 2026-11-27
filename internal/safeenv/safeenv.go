// Package safeenv keeps secrets out of the commands the app starts
// (docs/security/2026-11-26-security-review.md, item 5).
//
// A command step, run_command, a project tool, a build or a dependency
// install runs code the app did not write (and an AI may have steered).
// It used to inherit every variable the app was started with: cloud keys,
// API keys, tokens. Clean drops the variables whose names say they hold a
// secret; everything else (PATH, HOME, proxies, JAVA_HOME, registries, CA
// files, SSH_AUTH_SOCK, …) passes, so builds and tools keep working.
//
// A secret a command needs goes in explicitly: a tool's declared secrets,
// the project's secrets, or a name an admin lists in the machine policy
// ("keepEnvVars": ["NPM_TOKEN"]).
//
// The AI CLIs themselves (claude, copilot) are not cleaned: they need the
// login they were started with.
package safeenv

import (
	"strings"
	"sync"
)

// secretWords: a variable whose name contains one of these holds a secret.
var secretWords = []string{
	"TOKEN", "SECRET", "PASSWORD", "PASSWD", "PASSPHRASE", "CREDENTIAL",
	"API_KEY", "APIKEY", "ACCESS_KEY", "PRIVATE_KEY", "SESSION_KEY", "CLIENT_KEY",
	"CONNECTION_STRING", "SAS_KEY", "WEBHOOK",
}

// secretNames: whole names that hold a secret without saying so.
var secretNames = map[string]bool{
	"ANTHROPIC_AUTH": true, "GOOGLE_APPLICATION_CREDENTIALS": true,
	"AZURE_STORAGE_KEY": true, "DATABASE_URL": true,
}

var (
	mu   sync.RWMutex
	keep func() []string
)

// SetKeep sets where the names that pass anyway come from (the machine
// policy's keepEnvVars).
func SetKeep(f func() []string) { mu.Lock(); keep = f; mu.Unlock() }

// Secret reports whether a variable's name says it holds a secret.
func Secret(name string) bool {
	n := strings.ToUpper(name)
	if secretNames[n] {
		return true
	}
	for _, w := range secretWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// Clean returns env without the secret variables (see Secret), except
// those the policy keeps.
func Clean(env []string) []string {
	mu.RLock()
	f := keep
	mu.RUnlock()
	kept := map[string]bool{}
	if f != nil {
		for _, n := range f() {
			kept[strings.ToUpper(strings.TrimSpace(n))] = true
		}
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if Secret(k) && !kept[strings.ToUpper(k)] {
			continue
		}
		out = append(out, kv)
	}
	return out
}
