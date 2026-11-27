package builtin

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNetGuardAddresses(t *testing.T) {
	for _, c := range []struct {
		ip              string
		forbidden, home bool
	}{
		{"169.254.169.254", true, true},
		{"fe80::1", true, true},
		{"fd00:ec2::254", true, true},
		{"100.100.100.200", true, false},
		{"127.0.0.1", false, true},
		{"::1", false, true},
		{"10.1.2.3", false, true},
		{"192.168.0.10", false, true},
		{"172.16.5.5", false, true},
		{"0.0.0.0", false, true},
		{"8.8.8.8", false, false},
		{"2606:4700::1111", false, false},
	} {
		ip := net.ParseIP(c.ip)
		if forbiddenIP(ip) != c.forbidden {
			t.Errorf("forbiddenIP(%s) = %v", c.ip, !c.forbidden)
		}
		if in, _ := PrivateHost(context.Background(), c.ip); in != (c.home || c.forbidden) && c.ip != "100.100.100.200" {
			t.Errorf("PrivateHost(%s) = %v", c.ip, in)
		}
	}
	for _, h := range []string{"localhost", "app.localhost", "[::1]"} {
		if in, _ := PrivateHost(context.Background(), h); !in {
			t.Errorf("PrivateHost(%s) = false", h)
		}
	}
}

// The guarded transport refuses a metadata address before connecting.
func TestNetGuardDialRefusesMetadata(t *testing.T) {
	tr := guardTransport(http.DefaultTransport.(*http.Transport).Clone())
	tr.Proxy = nil
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	_, err := c.Get("http://169.254.169.254/latest/meta-data/")
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("metadata address: %v", err)
	}
}

// A public page may not redirect inward; an inside page may redirect inside.
func TestNetGuardRedirects(t *testing.T) {
	req := func(u string) *http.Request {
		p, _ := url.Parse(u)
		return (&http.Request{URL: p}).WithContext(context.Background())
	}
	if err := guardRedirect(req("http://127.0.0.1:8080/admin"), []*http.Request{req("https://93.184.216.34/")}); err == nil {
		t.Fatal("a public page redirected to this computer")
	}
	if err := guardRedirect(req("http://10.0.0.5/wiki"), []*http.Request{req("https://93.184.216.34/")}); err == nil {
		t.Fatal("a public page redirected to a private network")
	}
	if err := guardRedirect(req("http://127.0.0.1:8080/b"), []*http.Request{req("http://localhost:8080/a")}); err != nil {
		t.Fatalf("a local redirect inside: %v", err)
	}
	if err := guardRedirect(req("https://1.1.1.1/"), []*http.Request{req("https://93.184.216.34/")}); err != nil {
		t.Fatalf("a public redirect: %v", err)
	}
}
