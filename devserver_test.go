package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDevServerCalls(t *testing.T) {
	app, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.shutdown(context.Background())

	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	ws := t.TempDir()
	res, err := devCall(app, "OpenWorkspace", []json.RawMessage{raw(ws)})
	if err != nil || !strings.Contains(string(raw(res)), `"root"`) {
		t.Fatalf("OpenWorkspace: %v %s", err, raw(res))
	}
	if _, err := devCall(app, "PickFolder", nil); err == nil || !strings.Contains(err.Error(), "desktop app") {
		t.Fatalf("PickFolder: want refused, got %v", err)
	}
	if _, err := devCall(app, "NoSuchMethod", nil); err == nil {
		t.Fatal("unknown method: want an error")
	}
	if _, err := devCall(app, "startup", nil); err == nil {
		t.Fatal("unexported method: want an error")
	}
	if _, err := devCall(app, "OpenWorkspace", []json.RawMessage{raw(1), raw(2)}); err == nil {
		t.Fatal("too many arguments: want an error")
	}
}

func TestDevServerAccess(t *testing.T) {
	check := func(origin, token string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/call", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if token != "" {
			r.Header.Set("X-Dev-Token", token)
		}
		w := httptest.NewRecorder()
		if devAllowed(w, r, "secret") {
			return 0
		}
		return w.Code
	}
	if c := check(previewOrigin, "secret"); c != 0 {
		t.Fatalf("preview with token: %d", c)
	}
	if c := check("", "wrong"); c != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", c)
	}
	if c := check("http://evil.example", "secret"); c != http.StatusForbidden {
		t.Fatalf("other origin: %d", c)
	}
}
