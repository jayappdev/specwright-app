package store

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
)

// A secret is masked as it is and in its common encodings (item 6).
func TestRedactEncodedSecrets(t *testing.T) {
	const secret = "Tr0ub4dor&3-horse/battery+staple"
	r := NewRedactor()
	r.Add(secret)
	b64 := base64.StdEncoding.EncodeToString([]byte(secret))
	cases := map[string]string{
		"plain":        "password is " + secret + ".",
		"base64":       "data: " + b64,
		"base64 raw":   "data: " + strings.TrimRight(b64, "="),
		"base64 url":   "data: " + base64.RawURLEncoding.EncodeToString([]byte(secret)),
		"basic auth":   "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("ada:"+secret)),
		"basic auth2":  "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("bob:"+secret+"!")),
		"url":          "https://x/?p=" + url.QueryEscape(secret),
		"path":         "https://x/" + url.PathEscape(secret) + "/y",
		"hex":          "key=" + hex.EncodeToString([]byte(secret)),
		"HEX":          "key=" + strings.ToUpper(hex.EncodeToString([]byte(secret))),
		"json":         `{"p":"` + strings.ReplaceAll(secret, "&", `&`) + `"}`,
		"wrapped":      "pw: " + secret[:10] + "\n" + secret[10:],
		"wrapped crlf": "pw: " + secret[:5] + "\r\n    " + secret[5:20] + "\n> " + secret[20:],
		"b64 wrapped":  "data:\n" + b64[:16] + "\n" + b64[16:],
	}
	for name, in := range cases {
		got := r.String(in)
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("%s: not masked: %q", name, got)
		}
		for _, part := range []string{"Tr0ub4dor", "battery", b64[:12]} {
			if strings.Contains(got, part) {
				t.Errorf("%s: %q left in %q", name, part, got)
			}
		}
	}
	// ordinary text stays as it is
	for _, s := range []string{"hello world\nline two", "data: aGVsbG8gd29ybGQ=", "Tr0ub4 dor"} {
		if got := r.String(s); got != s {
			t.Errorf("changed %q to %q", s, got)
		}
	}
}
