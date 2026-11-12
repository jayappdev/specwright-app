package extension

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PackageSettings say where tools download their dependencies from. Every
// field is optional: empty means "inherit what this machine already uses"
// (environment variables, npm/pip/go/maven config). A value overrides it.
// Saved, without proxy credentials, in <workspace>/.codegen/packages.json.
type PackageSettings struct {
	Proxy struct {
		HTTP    string `json:"http"`
		HTTPS   string `json:"https"`
		NoProxy string `json:"noProxy"`
	} `json:"proxy"`
	CAFile string `json:"caFile"` // corporate root CA bundle (PEM)
	Npm    struct {
		Registry string `json:"registry"`
	} `json:"npm"`
	Pip struct {
		IndexURL      string `json:"indexUrl"`
		ExtraIndexURL string `json:"extraIndexUrl"`
		TrustedHost   string `json:"trustedHost"`
	} `json:"pip"`
	Go struct {
		Proxy     string `json:"proxy"`     // GOPROXY
		Private   string `json:"private"`   // GOPRIVATE
		NoSumDB   string `json:"noSumDb"`   // GONOSUMDB
		SumDB     string `json:"sumDb"`     // GOSUMDB ("off" behind some proxies)
		Insecure  string `json:"insecure"`  // GOINSECURE
		Toolchain string `json:"toolchain"` // GOTOOLCHAIN ("local" when downloads are blocked)
	} `json:"go"`
	Maven struct {
		MirrorURL string `json:"mirrorUrl"`
		MirrorOf  string `json:"mirrorOf"` // default "*"
	} `json:"maven"`
	// ApplyToAgents also hands the proxy/registry environment to agent runs
	// and project builds, not only to tool installs and tool runs.
	ApplyToAgents bool `json:"applyToAgents"`
}

// PackageStore keeps the settings of one workspace. Proxy credentials
// (user:password@ in a proxy URL) are kept in memory only.
type PackageStore struct {
	root  string
	mu    sync.Mutex
	creds map[string]*url.Userinfo // "http" | "https" → credentials
}

// NewPackageStore opens the settings of workspace root.
func NewPackageStore(root string) *PackageStore {
	return &PackageStore{root: root, creds: map[string]*url.Userinfo{}}
}

func (p *PackageStore) path() string { return filepath.Join(p.root, ".codegen", "packages.json") }

// CacheDir is the workspace-local package repository for an ecosystem
// (npm, pip, go-mod, m2): shared by every tool, never committed.
func CacheDir(root, eco string) string { return filepath.Join(root, ".codegen", "cache", eco) }

// Load returns the saved settings with in-memory proxy credentials applied.
func (p *PackageStore) Load() (PackageSettings, error) {
	var s PackageSettings
	b, err := os.ReadFile(p.path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s); err != nil {
			return s, fmt.Errorf("%s: %w", p.path(), err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s.Proxy.HTTP = withCreds(s.Proxy.HTTP, p.creds["http"])
	s.Proxy.HTTPS = withCreds(s.Proxy.HTTPS, p.creds["https"])
	return s, nil
}

// Public returns s with proxy passwords masked, for showing in the UI.
func (s PackageSettings) Public() PackageSettings {
	s.Proxy.HTTP = maskCreds(s.Proxy.HTTP)
	s.Proxy.HTTPS = maskCreds(s.Proxy.HTTPS)
	return s
}

// Save validates and stores s; proxy credentials stay in memory.
func (p *PackageStore) Save(s PackageSettings) error {
	for _, u := range []string{s.Proxy.HTTP, s.Proxy.HTTPS, s.Npm.Registry, s.Pip.IndexURL, s.Pip.ExtraIndexURL, s.Maven.MirrorURL} {
		if u == "" {
			continue
		}
		pu, err := url.Parse(u)
		if err != nil || pu.Scheme == "" || pu.Host == "" {
			return fmt.Errorf("%q is not a URL (want e.g. https://host:port/path)", u)
		}
	}
	if s.CAFile != "" {
		if _, err := os.Stat(s.CAFile); err != nil {
			return fmt.Errorf("CA bundle: %w", err)
		}
	}
	p.mu.Lock()
	for k, v := range map[string]*string{"http": &s.Proxy.HTTP, "https": &s.Proxy.HTTPS} {
		u, err := url.Parse(*v)
		if err != nil || *v == "" {
			continue
		}
		if u.User != nil {
			if passwordOf(u.User) != passwordMask { // the masked value the UI sends back
				p.creds[k] = u.User
			}
			u.User = nil
			*v = u.String()
		} else {
			delete(p.creds, k)
		}
	}
	p.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.path()), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(p.path(), append(b, '\n'), 0o644)
}

func passwordOf(u *url.Userinfo) string { pw, _ := u.Password(); return pw }

func withCreds(raw string, ui *url.Userinfo) string {
	if raw == "" || ui == nil {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = ui
	return u.String()
}

// passwordMask is how url.URL.Redacted shows a password.
const passwordMask = "xxxxx"

func maskCreds(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Redacted()
}

// Env returns the environment for installing and running tools in root:
// proxy, CA bundle, registries and the workspace-local caches. Only
// overridden values are set; everything else is inherited.
func (s PackageSettings) Env(root string) []string {
	var env []string
	set := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	for _, k := range []string{"HTTP_PROXY", "http_proxy"} {
		set(k, s.Proxy.HTTP)
	}
	for _, k := range []string{"HTTPS_PROXY", "https_proxy"} {
		set(k, firstNonEmpty(s.Proxy.HTTPS, s.Proxy.HTTP))
	}
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		set(k, s.Proxy.NoProxy)
	}
	set("npm_config_proxy", s.Proxy.HTTP)
	set("npm_config_https_proxy", firstNonEmpty(s.Proxy.HTTPS, s.Proxy.HTTP))
	set("npm_config_noproxy", s.Proxy.NoProxy)
	set("PIP_PROXY", firstNonEmpty(s.Proxy.HTTPS, s.Proxy.HTTP))
	if s.CAFile != "" {
		for _, k := range []string{"NODE_EXTRA_CA_CERTS", "npm_config_cafile", "PIP_CERT", "REQUESTS_CA_BUNDLE", "SSL_CERT_FILE"} {
			set(k, s.CAFile)
		}
	}
	set("npm_config_registry", s.Npm.Registry)
	set("PIP_INDEX_URL", s.Pip.IndexURL)
	set("PIP_EXTRA_INDEX_URL", s.Pip.ExtraIndexURL)
	set("PIP_TRUSTED_HOST", s.Pip.TrustedHost)
	// uv (used to fetch Spec Kit's CLI) reads its own index variables
	set("UV_DEFAULT_INDEX", s.Pip.IndexURL)
	set("UV_INDEX", s.Pip.ExtraIndexURL)
	set("UV_INSECURE_HOST", s.Pip.TrustedHost)
	set("GOPROXY", s.Go.Proxy)
	set("GOPRIVATE", s.Go.Private)
	set("GONOSUMDB", s.Go.NoSumDB)
	set("GOSUMDB", s.Go.SumDB)
	set("GOINSECURE", s.Go.Insecure)
	set("GOTOOLCHAIN", s.Go.Toolchain)
	if root != "" {
		set("npm_config_cache", CacheDir(root, "npm"))
		set("PIP_CACHE_DIR", CacheDir(root, "pip"))
		set("GOMODCACHE", CacheDir(root, "go-mod"))
		set("GOFLAGS", "-mod=mod -modcacherw") // writable cache: deleting .codegen/cache just works
	}
	return env
}

// MavenSettings writes <root>/.codegen/cache/maven-settings.xml (local
// repository, mirror, proxy) and returns its path. Maven reads no proxy
// variables, so this file is how the proxy reaches it.
func (s PackageSettings) MavenSettings(root string) (string, error) {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!-- generated by Codegen from .codegen/packages.json -->\n<settings>\n")
	fmt.Fprintf(&b, "  <localRepository>%s</localRepository>\n", xmlEsc(CacheDir(root, "m2")))
	if s.Maven.MirrorURL != "" {
		of := firstNonEmpty(s.Maven.MirrorOf, "*")
		fmt.Fprintf(&b, "  <mirrors><mirror><id>codegen-mirror</id><mirrorOf>%s</mirrorOf><url>%s</url></mirror></mirrors>\n", xmlEsc(of), xmlEsc(s.Maven.MirrorURL))
	}
	var proxies []string
	for _, p := range []struct{ id, raw string }{{"http", s.Proxy.HTTP}, {"https", firstNonEmpty(s.Proxy.HTTPS, s.Proxy.HTTP)}} {
		u, err := url.Parse(p.raw)
		if p.raw == "" || err != nil || u.Hostname() == "" {
			continue
		}
		port := u.Port()
		if port == "" {
			port = map[string]string{"https": "443"}[u.Scheme]
			if port == "" {
				port = "80"
			}
		}
		x := fmt.Sprintf("    <proxy><id>codegen-%s</id><active>true</active><protocol>%s</protocol><host>%s</host><port>%s</port>", p.id, p.id, xmlEsc(u.Hostname()), port)
		if u.User != nil {
			x += fmt.Sprintf("<username>%s</username><password>%s</password>", xmlEsc(u.User.Username()), xmlEsc(passwordOf(u.User)))
		}
		if s.Proxy.NoProxy != "" {
			x += "<nonProxyHosts>" + xmlEsc(strings.ReplaceAll(s.Proxy.NoProxy, ",", "|")) + "</nonProxyHosts>"
		}
		proxies = append(proxies, x+"</proxy>")
	}
	if len(proxies) > 0 {
		b.WriteString("  <proxies>\n" + strings.Join(proxies, "\n") + "\n  </proxies>\n")
	}
	b.WriteString("</settings>\n")
	p := filepath.Join(root, ".codegen", "cache", "maven-settings.xml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	// 0600: it can hold proxy credentials
	return p, os.WriteFile(p, []byte(b.String()), 0o600)
}

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// DetectPackageSettings reports what this machine uses today, from the
// environment and the package managers' own configuration. The UI shows it
// as the inherited value under each override field.
func DetectPackageSettings(ctx context.Context) PackageSettings {
	var s PackageSettings
	env := func(ks ...string) string {
		for _, k := range ks {
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
		return ""
	}
	out := func(name string, args ...string) string {
		if _, err := exec.LookPath(name); err != nil {
			return ""
		}
		c, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		b, err := exec.CommandContext(c, name, args...).Output()
		if err != nil {
			return ""
		}
		v := strings.TrimSpace(string(b))
		if v == "undefined" || v == "null" {
			return ""
		}
		return v
	}
	s.Proxy.HTTP = maskCreds(env("HTTP_PROXY", "http_proxy"))
	s.Proxy.HTTPS = maskCreds(env("HTTPS_PROXY", "https_proxy"))
	s.Proxy.NoProxy = env("NO_PROXY", "no_proxy")
	s.CAFile = env("NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE")
	s.Npm.Registry = firstNonEmpty(env("npm_config_registry"), out("npm", "config", "get", "registry"))
	s.Pip.IndexURL = firstNonEmpty(env("PIP_INDEX_URL"), out("pip3", "config", "get", "global.index-url"), "https://pypi.org/simple")
	s.Pip.ExtraIndexURL = firstNonEmpty(env("PIP_EXTRA_INDEX_URL"), out("pip3", "config", "get", "global.extra-index-url"))
	s.Pip.TrustedHost = firstNonEmpty(env("PIP_TRUSTED_HOST"), out("pip3", "config", "get", "global.trusted-host"))
	if g := out("go", "env", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GOSUMDB", "GOINSECURE", "GOTOOLCHAIN"); g != "" {
		v := strings.Split(g, "\n")
		for len(v) < 6 {
			v = append(v, "")
		}
		s.Go.Proxy, s.Go.Private, s.Go.NoSumDB, s.Go.SumDB, s.Go.Insecure, s.Go.Toolchain = v[0], v[1], v[2], v[3], v[4], v[5]
	}
	s.Maven.MirrorURL, s.Maven.MirrorOf = "https://repo.maven.apache.org/maven2", "central"
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".m2", "settings.xml")); err == nil {
			if m := regexp.MustCompile(`(?s)<mirror>.*?<mirrorOf>([^<]*)</mirrorOf>.*?<url>([^<]+)</url>`).FindSubmatch(b); m != nil {
				s.Maven.MirrorOf, s.Maven.MirrorURL = string(m[1]), string(m[2])
			} else if m := regexp.MustCompile(`(?s)<mirror>.*?<url>([^<]+)</url>`).FindSubmatch(b); m != nil {
				s.Maven.MirrorURL, s.Maven.MirrorOf = string(m[1]), "*"
			}
		}
	}
	return s
}

// Effective fills every empty override in s from detected.
func (s PackageSettings) Effective(detected PackageSettings) PackageSettings {
	f := func(a *string, b string) {
		if *a == "" {
			*a = b
		}
	}
	f(&s.Proxy.HTTP, detected.Proxy.HTTP)
	f(&s.Proxy.HTTPS, detected.Proxy.HTTPS)
	f(&s.Proxy.NoProxy, detected.Proxy.NoProxy)
	f(&s.CAFile, detected.CAFile)
	f(&s.Npm.Registry, firstNonEmpty(detected.Npm.Registry, "https://registry.npmjs.org/"))
	f(&s.Pip.IndexURL, detected.Pip.IndexURL)
	f(&s.Go.Proxy, firstNonEmpty(detected.Go.Proxy, "https://proxy.golang.org,direct"))
	f(&s.Maven.MirrorURL, detected.Maven.MirrorURL)
	return s
}

// RegistryCheck is the result of reaching one registry.
type RegistryCheck struct {
	Ecosystem string `json:"ecosystem"`
	URL       string `json:"url"`
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	Via       string `json:"via"` // proxy used, or "direct"
	MS        int64  `json:"ms"`
}

// CheckRegistries requests a small known file from each registry through
// the configured proxy and CA bundle (eff = Effective settings).
func CheckRegistries(ctx context.Context, eff PackageSettings) []RegistryCheck {
	hc := eff.HTTPClient(12 * time.Second)
	goProxy := strings.Split(strings.Split(eff.Go.Proxy, ",")[0], "|")[0]
	return checkRegistries(ctx, eff, hc, goProxy)
}

// HTTPClient reaches the internet through the configured proxy and CA
// bundle.
func (eff PackageSettings) HTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{Proxy: func(r *http.Request) (*url.URL, error) {
		raw := eff.Proxy.HTTP
		if r.URL.Scheme == "https" {
			raw = firstNonEmpty(eff.Proxy.HTTPS, eff.Proxy.HTTP)
		}
		if raw == "" || noProxy(eff.Proxy.NoProxy, r.URL.Hostname()) {
			return nil, nil
		}
		return url.Parse(raw)
	}, TLSHandshakeTimeout: 8 * time.Second}
	if eff.CAFile != "" {
		if pem, err := os.ReadFile(eff.CAFile); err == nil {
			pool, _ := x509.SystemCertPool()
			if pool == nil {
				pool = x509.NewCertPool()
			}
			pool.AppendCertsFromPEM(pem)
			tr.TLSClientConfig = &tls.Config{RootCAs: pool}
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func checkRegistries(ctx context.Context, eff PackageSettings, hc *http.Client, goProxy string) []RegistryCheck {
	targets := []struct{ eco, url string }{
		{"npm", strings.TrimRight(eff.Npm.Registry, "/") + "/-/ping"},
		{"pip", strings.TrimRight(eff.Pip.IndexURL, "/") + "/pip/"},
		{"go", strings.TrimRight(goProxy, "/") + "/golang.org/x/text/@v/list"},
		{"maven", strings.TrimRight(eff.Maven.MirrorURL, "/") + "/org/apache/maven/plugins/maven-dependency-plugin/maven-metadata.xml"},
	}
	out := make([]RegistryCheck, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := RegistryCheck{Ecosystem: t.eco, URL: t.url, Via: "direct"}
			defer func() { out[i] = c }()
			if strings.HasPrefix(t.url, "/") || goProxy == "direct" || goProxy == "off" && t.eco == "go" {
				c.Status = "no registry set"
				return
			}
			req, err := http.NewRequestWithContext(ctx, "GET", t.url, nil)
			if err != nil {
				c.Status = err.Error()
				return
			}
			if p, _ := hc.Transport.(*http.Transport).Proxy(req); p != nil {
				c.Via = maskCreds(p.String())
			}
			start := time.Now()
			resp, err := hc.Do(req)
			c.MS = time.Since(start).Milliseconds()
			if err != nil {
				c.Status = err.Error()
				return
			}
			resp.Body.Close()
			c.Status = resp.Status
			c.OK = resp.StatusCode < 400
		}()
	}
	wg.Wait()
	return out
}

func noProxy(list, host string) bool {
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimPrefix(strings.TrimSpace(n), "*")
		if n != "" && (host == strings.TrimPrefix(n, ".") || strings.HasSuffix(host, n)) {
			return true
		}
	}
	return false
}
