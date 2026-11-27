package builtin

// Where fetch_webpage and http_request may connect
// (docs/security/2026-11-26-security-review.md, item 4: SSRF).
//
//   - Never to a link-local address (169.254.0.0/16, fe80::/10), which is
//     where cloud metadata services answer (169.254.169.254) and where
//     nothing a flow needs lives. The check runs on the address actually
//     dialled, after DNS, so a name that resolves there is refused too.
//   - A redirect from a public address may not lead to this computer or a
//     private network: a page cannot bounce a request inward.
//   - Reaching this computer or a private network on purpose (an intranet
//     page, a local API) stays possible; the app asks a person first when
//     an AI makes the call (core: agentNetworkAllowed, using PrivateHost).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// metadataIPs are cloud metadata endpoints outside the link-local range.
var metadataIPs = map[string]bool{
	"100.100.100.200": true, // Alibaba Cloud
	"fd00:ec2::254":   true, // AWS (IPv6)
}

// forbiddenIP: never connect there.
func forbiddenIP(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || metadataIPs[ip.String()]
}

// inwardIP: this computer or a private network.
func inwardIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || forbiddenIP(ip)
}

// guardedDialer refuses forbidden addresses at connect time.
func guardedDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && forbiddenIP(ip) {
				return fmt.Errorf("connecting to %s is not allowed (a link-local or cloud metadata address)", host)
			}
			return nil
		},
	}
}

// guardTransport makes a transport use the guarded dialer.
func guardTransport(tr *http.Transport) *http.Transport {
	tr.DialContext = guardedDialer().DialContext
	return tr
}

// guardRedirect refuses a redirect from a public address to this computer
// or a private network, and more than 10 redirects.
func guardRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	from := via[0].URL.Hostname()
	if in, _ := PrivateHost(req.Context(), from); in {
		return nil // the request was already for an inside address
	}
	if in, _ := PrivateHost(req.Context(), req.URL.Hostname()); in {
		return fmt.Errorf("%s redirected to %s, an address on this computer or a private network: not followed", from, req.URL.Host)
	}
	return nil
}

// PrivateHost reports whether host is (or resolves to) this computer, a
// private network or a link-local address.
func PrivateHost(ctx context.Context, host string) (bool, error) {
	host = strings.Trim(host, "[]")
	if host == "" {
		return false, nil
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return inwardIP(ip), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false, err
	}
	for _, a := range addrs {
		if inwardIP(a.IP) {
			return true, nil
		}
	}
	return false, nil
}
