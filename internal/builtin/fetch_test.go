package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codegen/internal/flow"
)

func TestFetchWebpageFormats(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "secret=1")
			_, _ = w.Write([]byte(`{"quotes": [{"symbol": "AMZN", "price": 182.4}]}`))
		case "/big":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"t": [` + strings.Repeat(`1794580200,`, 3000) + `1]}`))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Docs</title></head><body><h1>Hello</h1><p>World</p></body></html>`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`not here`))
		}
	}))
	defer srv.Close()
	b, _ := box(t)
	// text (the default): as before
	txt := call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/api"})
	if !strings.Contains(txt, "200 OK") || !strings.Contains(txt, `"symbol": "AMZN"`) {
		t.Fatal(txt)
	}
	// json: a JSON object with the parsed body
	var got map[string]any
	out := call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/api", "format": "json"})
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(out)
	}
	if got["status"].(float64) != 200 || got["ok"] != true || got["body"] != nil || !strings.HasPrefix(out, `{
  "url"`) {
		t.Fatal(out)
	}
	q := got["json"].(map[string]any)["quotes"].([]any)[0].(map[string]any)
	if q["symbol"] != "AMZN" || q["price"].(float64) != 182.4 {
		t.Fatal(out)
	}
	if hs := got["headers"].(map[string]any); hs["content-type"] != "application/json" || hs["set-cookie"] != nil {
		t.Fatalf("headers %v: no cookies passed on", hs)
	}
	// html: title and readable text; an error status is data, not a failure
	got = nil
	_ = json.Unmarshal([]byte(call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/page", "format": "json"})), &got)
	if got["title"] != "Docs" || !strings.Contains(got["body"].(string), "Hello") || got["json"] != nil {
		t.Fatalf("%v", got)
	}
	got = nil
	_ = json.Unmarshal([]byte(call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/gone", "format": "json"})), &got)
	if got["status"].(float64) != 404 || got["ok"] != false || got["body"] != "not here" {
		t.Fatalf("%v", got)
	}
	// a long JSON body: cut, as text
	got = nil
	_ = json.Unmarshal([]byte(call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/api", "format": "json", "max_chars": 500.0})), &got)
	if got["truncated"] != false {
		t.Fatalf("%v", got)
	}
	// raw: exactly the body, nothing added; an error status fails
	if raw := call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/api", "format": "raw"}); raw != `{"quotes": [{"symbol": "AMZN", "price": 182.4}]}` {
		t.Fatalf("%q", raw)
	}
	if _, err := b.Call(context.Background(), "fetch_webpage", map[string]any{"url": srv.URL + "/gone", "format": "raw"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatal("an error status fails in raw:", err)
	}
	// a body longer than max_chars: text is cut with a note, raw fails rather than give broken JSON
	long := call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/big", "max_chars": 600.0})
	if !strings.Contains(long, "more characters") {
		t.Fatal(long)
	}
	if _, err := b.Call(context.Background(), "fetch_webpage", map[string]any{"url": srv.URL + "/big", "format": "raw", "max_chars": 600.0}); err == nil || !strings.Contains(err.Error(), "larger than 600") {
		t.Fatal(err)
	}
	if big := call(t, b, "fetch_webpage", map[string]any{"url": srv.URL + "/big", "format": "raw"}); !json.Valid([]byte(big)) || len(big) < 20000 {
		t.Fatal("raw gives the whole response by default", len(big))
	}
	if _, err := b.Call(context.Background(), "fetch_webpage", map[string]any{"url": srv.URL + "/api", "format": "xml"}); err == nil {
		t.Fatal("an unknown format is refused")
	}
	// the declared shape matches what it gives
	sh := flow.SchemaOfText(out)
	for _, f := range []string{"url", "status", "ok", "contentType", "headers", "json", "size", "truncated"} {
		if sh.Prop(f) == nil || FetchJSONShape.Prop(f) == nil {
			t.Errorf("field %s: in the output %v, declared %v", f, sh.Prop(f) != nil, FetchJSONShape.Prop(f) != nil)
		}
	}
}
