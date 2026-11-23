package builtin

// http_request and notify: SpecWright talking to other services.
//
// http_request calls an API: any method, headers, a query, a body or JSON,
// and a secret as the authentication (by name: its value comes from the
// session, the environment or the keychain and is never shown or kept).
// notify tells a person a result: a desktop notification, or a message to
// Slack, Teams or any webhook (the webhook's address is a secret too).
//
// Both can send data out of the computer. Reading (GET, HEAD) and desktop
// notifications need nothing; anything else is approved by a person: per
// project for a flow step, per call for an agent (core/outward.go). SendsOut
// says which calls those are.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"codegen/internal/flow"
)

// SendsOut says whether a call of a built-in tool sends data out of the
// computer, and describes it for a person to approve ("POST https://…").
func SendsOut(name string, a map[string]any) (bool, string) {
	x := args(a)
	switch name {
	case "http_request":
		m := strings.ToUpper(firstStr(strings.TrimSpace(x.str("method")), "GET"))
		if m == "GET" || m == "HEAD" {
			return false, ""
		}
		return true, m + " " + x.str("url")
	case "notify":
		ch := strings.ToLower(firstStr(strings.TrimSpace(x.str("channel")), "desktop"))
		if ch == "desktop" {
			return false, ""
		}
		return true, fmt.Sprintf("send a %s message through the webhook in %s", ch, webhookSecret(ch, x.str("webhook_secret")))
	}
	return false, ""
}

func (b *Box) secretValue(ctx context.Context, name string) (string, error) {
	if b.secret == nil {
		return "", errors.New("secrets are not available here")
	}
	if !secretNameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a secret name (letters, digits, _)", name)
	}
	v, err := b.secret(ctx, name)
	if err != nil {
		return "", fmt.Errorf("the secret %s: %v", name, err)
	}
	if strings.TrimSpace(v) == "" {
		return "", fmt.Errorf("the secret %s is empty", name)
	}
	return v, nil
}

// testTransport replaces the HTTP transport in tests (a test server's).
var testTransport http.RoundTripper

func (b *Box) client(timeout time.Duration) *http.Client {
	if testTransport != nil {
		return &http.Client{Transport: testTransport, Timeout: timeout}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = proxyFrom(b)
	return &http.Client{Transport: tr, Timeout: timeout}
}

func localHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

func httpRequest(ctx context.Context, b *Box, a args) (string, error) {
	raw, err := a.need("url")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("url must be an absolute http(s) address")
	}
	method := strings.ToUpper(firstStr(strings.TrimSpace(a.str("method")), "GET"))
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return "", fmt.Errorf("method must be GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS, not %q", method)
	}
	if q, ok := a["query"].(map[string]any); ok {
		vals := u.Query()
		for _, k := range sortedKeys(q) {
			vals.Set(k, fmt.Sprint(q[k]))
		}
		u.RawQuery = vals.Encode()
	}
	var body io.Reader
	contentType := ""
	switch {
	case a["json"] != nil:
		j := a["json"]
		if s, ok := j.(string); ok { // JSON given as text
			if !json.Valid([]byte(s)) {
				return "", errors.New("json is not valid JSON")
			}
			body = strings.NewReader(s)
		} else {
			bs, _ := json.Marshal(j)
			body = bytes.NewReader(bs)
		}
		contentType = "application/json"
	case a.str("body") != "":
		body = strings.NewReader(a.str("body"))
	}
	if body != nil && (method == "GET" || method == "HEAD") {
		return "", errors.New("a GET or HEAD request has no body; use POST, PUT or PATCH")
	}
	timeout := time.Duration(a.num("timeout_seconds", 30, 1, 300)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "SpecWright/1 (http_request)")
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if h, ok := a["headers"].(map[string]any); ok {
		for _, k := range sortedKeys(h) {
			if strings.EqualFold(k, "authorization") {
				return "", errors.New("give authentication with auth_secret (the name of a secret), not as a header")
			}
			req.Header.Set(k, fmt.Sprint(h[k]))
		}
	}
	if name := strings.TrimSpace(a.str("auth_secret")); name != "" {
		if u.Scheme != "https" && !localHost(u.Hostname()) {
			return "", errors.New("a secret is only sent over https (or to this computer)")
		}
		v, err := b.secretValue(ctx, name)
		if err != nil {
			return "", err
		}
		scheme := firstStr(strings.TrimSpace(a.str("auth_scheme")), "Bearer")
		switch {
		case strings.EqualFold(scheme, "basic"): // the secret is user:token
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(v)))
		case strings.HasPrefix(strings.ToLower(scheme), "header:"): // header:X-Api-Key
			req.Header.Set(strings.TrimSpace(scheme[len("header:"):]), v)
		default: // Bearer, Token, …
			req.Header.Set("Authorization", scheme+" "+v)
		}
	}
	resp, err := b.client(timeout).Do(req)
	if err != nil {
		return "", fmt.Errorf("the request failed: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, fetchDataLimit+1))
	format := strings.ToLower(strings.TrimSpace(a.str("format")))
	switch format {
	case "raw":
		return fetchRaw(resp, data, a.num("max_chars", fetchDataLimit, 500, fetchDataLimit))
	case "json":
		out := map[string]any{"status": resp.StatusCode, "ok": resp.StatusCode < 400, "url": resp.Request.URL.String(), "headers": someHeaders(resp.Header)}
		var j any
		if json.Unmarshal(data, &j) == nil {
			out["json"] = j
		} else {
			out["body"] = string(data[:min(len(data), a.num("max_chars", 200000, 500, fetchDataLimit))])
		}
		return toJSON(out), nil
	}
	limit := a.num("max_chars", 20000, 500, 200000)
	text := string(data)
	if isBinary(data) {
		text = fmt.Sprintf("(%s of binary content)", human(int64(len(data))))
	} else if len(text) > limit {
		text = text[:limit] + fmt.Sprintf("\n… %s more; raise max_chars, or use format raw", human(int64(len(data)-limit)))
	}
	head := fmt.Sprintf("%s %s → %s", method, resp.Request.URL, resp.Status)
	if resp.StatusCode >= 400 {
		return head + "\n\n" + text, fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}
	return head + "\n\n" + text, nil
}

func someHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"Content-Type", "Location", "Link", "Etag", "Last-Modified", "Retry-After", "X-Ratelimit-Remaining", "X-Total-Count"} {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	return out
}

// secretNameRE: what a secret's name may be (as in Tools › Secrets)
var secretNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,99}$`)

func webhookSecret(channel, name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	switch channel {
	case "slack":
		return "SLACK_WEBHOOK_URL"
	case "teams":
		return "TEAMS_WEBHOOK_URL"
	}
	return "NOTIFY_WEBHOOK_URL"
}

func notifyTool(ctx context.Context, b *Box, a args) (string, error) {
	title := strings.TrimSpace(a.str("title"))
	msg := strings.TrimSpace(a.str("message"))
	if title == "" && msg == "" {
		return "", errors.New("give a title or a message")
	}
	title = firstStr(title, "SpecWright")
	link := strings.TrimSpace(a.str("link"))
	channel := strings.ToLower(firstStr(strings.TrimSpace(a.str("channel")), "desktop"))
	switch channel {
	case "desktop":
		if b.notify == nil {
			return "", errors.New("desktop notifications are not available here")
		}
		if err := b.notify(title, clipStr(msg, 2000), link); err != nil {
			return "", err
		}
		return "Shown on this computer: " + title, nil
	case "slack", "teams", "webhook":
	default:
		return "", fmt.Errorf("channel must be desktop, slack, teams or webhook, not %q", channel)
	}
	name := webhookSecret(channel, a.str("webhook_secret"))
	hook, err := b.secretValue(ctx, name)
	if err != nil {
		return "", err
	}
	hu, err := url.Parse(strings.TrimSpace(hook))
	if err != nil || hu.Scheme != "https" || hu.Host == "" {
		return "", fmt.Errorf("the secret %s is not an https webhook address", name)
	}
	var payload any
	switch channel {
	case "slack":
		t := "*" + title + "*"
		if msg != "" {
			t += "\n" + msg
		}
		if link != "" {
			t += "\n<" + link + ">"
		}
		payload = map[string]any{"text": t}
	case "teams": // an Adaptive Card: Teams workflows and the older connectors both take it
		items := []any{map[string]any{"type": "TextBlock", "text": title, "weight": "Bolder", "size": "Medium", "wrap": true}}
		if msg != "" {
			items = append(items, map[string]any{"type": "TextBlock", "text": msg, "wrap": true})
		}
		card := map[string]any{"type": "AdaptiveCard", "$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "version": "1.4", "body": items}
		if link != "" {
			card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": "Open", "url": link}}
		}
		payload = map[string]any{"type": "message", "attachments": []any{map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "content": card}}}
	default:
		payload = map[string]any{"title": title, "message": msg, "link": link, "source": "SpecWright"}
	}
	bs, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", hu.String(), bytes.NewReader(bs))
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client(20 * time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("sending failed: %v", err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("the webhook answered %s: %s", resp.Status, strings.TrimSpace(string(answer)))
	}
	return fmt.Sprintf("Sent to %s: %s", map[string]string{"slack": "Slack", "teams": "Teams", "webhook": "the webhook"}[channel], title), nil
}

// HTTPJSONShape is the shape of http_request's output with format=json.
var HTTPJSONShape = &flow.Schema{Type: "object", Required: []string{"status", "ok", "url", "headers"}, Properties: flow.Props{
	{Name: "status", Schema: &flow.Schema{Type: "integer", Description: "HTTP status code, e.g. 200"}},
	{Name: "ok", Schema: &flow.Schema{Type: "boolean", Description: "true below 400"}},
	{Name: "url", Schema: &flow.Schema{Type: "string", Description: "the final URL, after redirects"}},
	{Name: "headers", Schema: &flow.Schema{Type: "object", Description: "a few useful response headers (Content-Type, Location, Link, Etag, rate limits)"}},
	{Name: "json", Schema: &flow.Schema{Description: "the body parsed, when it is JSON"}},
	{Name: "body", Schema: &flow.Schema{Type: "string", Description: "the body as text, when it is not JSON"}},
}}
