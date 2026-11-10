package core

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"codegen/internal/extension"
)

var (
	detectOnce sync.Once
	detected   extension.PackageSettings
)

// detectedPackages is what the machine uses without overrides (cached; the
// npm/pip/go lookups spawn processes).
func detectedPackages() extension.PackageSettings {
	detectOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		detected = extension.DetectPackageSettings(ctx)
	})
	return detected
}

// packageSettings are the workspace's saved overrides (with in-memory
// proxy credentials); zero when no workspace is open.
func (h *Hub) packageSettings() extension.PackageSettings {
	h.mu.RLock()
	ps := h.pkgs
	h.mu.RUnlock()
	if ps == nil {
		return extension.PackageSettings{}
	}
	s, _ := ps.Load()
	return s
}

// PackageView is what the "Packages & proxy" card shows.
type PackageView struct {
	Saved    extension.PackageSettings `json:"saved"`    // overrides (passwords masked)
	Detected extension.PackageSettings `json:"detected"` // inherited from this machine
	Caches   string                    `json:"caches"`   // workspace-local package repositories
	Tools    map[string]bool           `json:"tools"`    // npm, python3, go, mvn, java on PATH
}

// PackageSettings returns the overrides and what they override.
func (h *Hub) PackageSettings() (PackageView, error) {
	root, err := h.need()
	if err != nil {
		return PackageView{}, err
	}
	v := PackageView{Saved: h.packageSettings().Public(), Detected: detectedPackages(), Caches: ".codegen/cache/{npm,pip,go-mod,m2}", Tools: map[string]bool{}}
	for _, t := range []string{"npm", "python3", "go", "mvn"} {
		v.Tools[t] = installedBin(t)
	}
	v.Tools["java"] = runtimeInstalled("java")
	_ = root
	return v, nil
}

// SavePackageSettings stores overrides in .codegen/packages.json (proxy
// credentials stay in memory) and refreshes the Maven settings file.
func (h *Hub) SavePackageSettings(s extension.PackageSettings) error {
	root, err := h.need()
	if err != nil {
		return err
	}
	h.mu.RLock()
	ps := h.pkgs
	h.mu.RUnlock()
	if err := ps.Save(s); err != nil {
		return err
	}
	cur := h.packageSettings()
	for _, p := range []string{cur.Proxy.HTTP, cur.Proxy.HTTPS} {
		h.Store.Redactor().Add(proxyPassword(p))
	}
	extension.EnsureGitignore(root)
	_, err = cur.MavenSettings(root)
	return err
}

// CheckRegistries tries each registry with the effective settings.
func (h *Hub) CheckRegistries() ([]extension.RegistryCheck, error) {
	if _, err := h.need(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(h.ctx, 20*time.Second)
	defer cancel()
	return extension.CheckRegistries(ctx, h.packageSettings().Effective(detectedPackages())), nil
}

// agentPackageEnv is added to agent runs and project builds when the
// workspace asks for it: the same proxy and registries tools use, plus
// MAVEN_ARGS pointing Maven at a settings file with the mirror and proxy.
func (h *Hub) agentPackageEnv(root string) []string {
	s := h.packageSettings()
	if !s.ApplyToAgents || root == "" {
		return nil
	}
	env := s.Env("") // keep the project's own caches for its builds
	if p, err := projectMavenSettings(s, root); err == nil {
		env = append(env, "MAVEN_ARGS=-s "+p)
	}
	return env
}

// projectMavenSettings is MavenSettings without the tools' local
// repository, so project builds keep using ~/.m2.
func projectMavenSettings(s extension.PackageSettings, root string) (string, error) {
	p, err := s.MavenSettings(root)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	var keep []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.Contains(l, "<localRepository>") {
			keep = append(keep, l)
		}
	}
	out := strings.TrimSuffix(p, ".xml") + "-project.xml"
	return out, os.WriteFile(out, []byte(strings.Join(keep, "\n")), 0o600)
}

func proxyPassword(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

func installedBin(name string) bool { _, err := exec.LookPath(name); return err == nil }
