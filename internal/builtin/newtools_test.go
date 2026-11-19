package builtin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run1(t *testing.T, b *Box, name string, a map[string]any) string {
	t.Helper()
	out, err := b.Call(context.Background(), name, a)
	if err != nil {
		t.Fatalf("%s %v: %v", name, a, err)
	}
	return out
}

func write(t *testing.T, root, rel, s string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitTool(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	b, root := box(t)
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = root
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com", "GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-q", "-m", "First: the orders service")
	run("tag", "v1.0.0")
	write(t, root, "README.md", "# Orders\n\nNow with docs.\n")
	run("commit", "-q", "-am", "Docs for the orders service", "-m", "A longer body.")
	write(t, root, "new.txt", "untracked\n")

	var st gitStatus
	_ = json.Unmarshal([]byte(call(t, b, "git", map[string]any{"format": "json"})), &st)
	if st.Branch != "main" || len(st.Files) != 1 || st.Files[0].Status != "untracked" {
		t.Fatalf("%+v", st)
	}
	var log []gitCommit
	_ = json.Unmarshal([]byte(call(t, b, "git", map[string]any{"action": "log", "format": "json", "since": "v1.0.0"})), &log)
	if len(log) != 1 || log[0].Subject != "Docs for the orders service" || log[0].Body != "A longer body." || log[0].Author != "Ada" {
		t.Fatalf("commits since the tag: %+v", log)
	}
	var d map[string]any
	_ = json.Unmarshal([]byte(call(t, b, "git", map[string]any{"action": "diff", "base": "v1.0.0", "ref": "HEAD", "format": "json"})), &d)
	files := d["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "README.md" || !strings.Contains(d["patch"].(string), "+Now with docs.") {
		t.Fatalf("%+v", d)
	}
	if out := call(t, b, "git", map[string]any{"action": "tags"}); !strings.Contains(out, "v1.0.0") {
		t.Fatal(out)
	}
	if out := call(t, b, "git", map[string]any{"action": "blame", "path": "README.md"}); !strings.Contains(out, "Ada") {
		t.Fatal(out)
	}
	if _, err := b.Call(context.Background(), "git", map[string]any{"action": "diff", "ref": "--output=/tmp/x"}); err == nil {
		t.Fatal("an option is not a revision")
	}
}

func TestFileCompare(t *testing.T) {
	b, root := box(t)
	write(t, root, "a/notes.txt", "one\ntwo\nthree\n")
	write(t, root, "b/notes.txt", "one\n2\nthree\n")
	write(t, root, "a/conf.json", `{"name": "orders", "port": 8080, "tags": ["x"]}`)
	write(t, root, "b/conf.json", "{\n  \"name\": \"orders\",\n  \"port\": 9090,\n  \"tags\": [\"x\", \"y\"],\n  \"debug\": true\n}")
	write(t, root, "a/only-a.txt", "a")
	write(t, root, "b/only-b.txt", "b")
	write(t, root, "a/same.txt", "same")
	write(t, root, "b/same.txt", "same")

	if out := call(t, b, "file_compare", map[string]any{"a": "a/notes.txt", "b": "b/notes.txt"}); !strings.Contains(out, "-two") || !strings.Contains(out, "+2") || strings.Contains(out, root) {
		t.Fatalf("%s", out)
	}
	var j struct {
		Changes []JSONChange `json:"changes"`
	}
	_ = json.Unmarshal([]byte(call(t, b, "file_compare", map[string]any{"a": "a/conf.json", "b": "b/conf.json", "format": "json"})), &j)
	got := map[string]string{}
	for _, c := range j.Changes {
		got[c.Path] = c.Change
	}
	if len(got) != 3 || got["$.port"] != "changed" || got["$.tags[1]"] != "added" || got["$.debug"] != "added" {
		t.Fatalf("%+v", j.Changes)
	}
	out := call(t, b, "file_compare", map[string]any{"a": "a", "b": "b"})
	for _, want := range []string{"Only in a", "only-a.txt", "Only in b", "only-b.txt", "Different", "notes.txt", "conf.json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "same.txt") {
		t.Fatal("the same file is not listed")
	}
	if _, err := b.Call(context.Background(), "file_compare", map[string]any{"a": "a", "b": "/etc/hosts"}); err == nil {
		t.Fatal("outside the workspace is refused")
	}
}

func TestTestRunnerGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	b, root := box(t)
	write(t, root, "svc/go.mod", "module example.com/svc\n\ngo 1.22\n")
	write(t, root, "svc/calc.go", "package svc\n\nfunc Add(a, b int) int { return a + b }\n")
	write(t, root, "svc/calc_test.go", `package svc

import "testing"

func TestAdd(t *testing.T) { if Add(1, 2) != 3 { t.Fatal("no") } }
func TestAddWrong(t *testing.T) { if Add(2, 2) != 5 { t.Errorf("Add(2, 2) = %d, want 5", Add(2, 2)) } }
func TestLater(t *testing.T) { t.Skip("later") }
`)
	var r TestResult
	_ = json.Unmarshal([]byte(call(t, b, "test_runner", map[string]any{"path": "svc", "format": "json"})), &r)
	if r.Tool != "go" || r.OK || r.Passed != 1 || r.Failed != 1 || r.Skipped != 1 || len(r.Failures) != 1 ||
		r.Failures[0].Name != "TestAddWrong" || !strings.Contains(r.Failures[0].Message, "Add(2, 2) = 4, want 5") {
		t.Fatalf("%+v", r)
	}
	_ = json.Unmarshal([]byte(call(t, b, "test_runner", map[string]any{"path": "svc", "filter": "TestAdd$", "format": "json"})), &r)
	if !r.OK || r.Passed != 1 || r.Failed != 0 {
		t.Fatalf("filtered: %+v", r)
	}
	// a build error: not "0 failed, ok"
	write(t, root, "svc/broken.go", "package svc\n\nfunc Broken() int { return \"x\" }\n")
	_ = json.Unmarshal([]byte(call(t, b, "test_runner", map[string]any{"path": "svc", "format": "json"})), &r)
	if r.OK || !strings.Contains(r.OutputTail, "broken.go") {
		t.Fatalf("build error: %+v", r)
	}
	if _, err := b.Call(context.Background(), "test_runner", map[string]any{"path": "svc", "filter": "x; rm -rf /"}); err == nil {
		t.Fatal("a filter cannot be a command")
	}
}

func TestJUnitAndSummaries(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.xml")
	_ = os.WriteFile(p, []byte(`<testsuites><testsuite name="s"><testcase name="ok" classname="A"/><testcase name="bad" classname="A"><failure message="expected 1">at A.java:12</failure></testcase><testcase name="later" classname="A"><skipped/></testcase></testsuite></testsuites>`), 0o644)
	var r TestResult
	readJUnitFile(&r, p)
	if r.Passed != 1 || r.Failed != 1 || r.Skipped != 1 || r.Failures[0].Suite != "A" || !strings.Contains(r.Failures[0].Message, "A.java:12") {
		t.Fatalf("%+v", r)
	}
	for out, want := range map[string][3]int{
		"Tests:       1 failed, 5 passed, 6 total":                {5, 1, 0},
		" Tests  2 failed | 10 passed (12)":                       {10, 2, 0},
		"test result: ok. 7 passed; 0 failed; 1 ignored; 0 m":     {7, 0, 1},
		"Failed!  - Failed:     1, Passed:     9, Skipped:     0": {9, 1, 0},
	} {
		var r TestResult
		parseSummary(out, &r)
		if [3]int{r.Passed, r.Failed, r.Skipped} != want {
			t.Errorf("%q: %d %d %d, want %v", out, r.Passed, r.Failed, r.Skipped, want)
		}
	}
}

const sampleMD = "# Release 1.2\n\nThe **orders** service, with `code` and a [link](https://example.com).\n\n## Changes\n\n- Faster *search*\n- Fixed the € price\n  1. nested one\n\n```go\nfunc main() {}\n```\n\n> A quote\n\n| Area | Change |\n|---|---|\n| API | New endpoint |\n\n---\n\nThe end.\n"

func TestExportDoc(t *testing.T) {
	b, root := box(t)
	write(t, root, "reports/release.md", sampleMD)
	if out := call(t, b, "export_doc", map[string]any{"path": "reports/release.md"}); !strings.Contains(out, "reports/release.html") {
		t.Fatal(out)
	}
	h, _ := os.ReadFile(filepath.Join(root, "reports", "release.html"))
	for _, want := range []string{"<title>Release 1.2</title>", "<table>", "<strong>orders</strong>", `<a href="https://example.com">`, "<blockquote>"} {
		if !strings.Contains(string(h), want) {
			t.Fatalf("html: missing %s", want)
		}
	}
	if _, err := b.Call(context.Background(), "export_doc", map[string]any{"path": "reports/release.md"}); err == nil {
		t.Fatal("an existing file is not replaced without overwrite")
	}
	// Word: a zip with the document, headings, the table, the link
	call(t, b, "export_doc", map[string]any{"markdown": sampleMD, "to": "docx", "out": "out/r.docx"})
	zb, _ := os.ReadFile(filepath.Join(root, "out", "r.docx"))
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	var doc, rels string
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		switch f.Name {
		case "word/document.xml":
			doc = string(data)
		case "word/_rels/document.xml.rels":
			rels = string(data)
		}
	}
	for _, want := range []string{`w:val="Heading1"`, "Release 1.2", "<w:tbl>", "New endpoint", `w:val="Code"`, "func main() {}", "<w:b/>", "Fixed the € price"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("docx: missing %s", want)
		}
	}
	if !strings.Contains(rels, "https://example.com") {
		t.Fatal("docx: the link's target")
	}
	// PDF: a valid file, the text in it, the euro sign in the fonts' encoding
	call(t, b, "export_doc", map[string]any{"markdown": sampleMD + strings.Repeat("\nMore text to fill pages. ", 400), "to": "pdf", "out": "out/r.pdf"})
	pb, _ := os.ReadFile(filepath.Join(root, "out", "r.pdf"))
	if !bytes.HasPrefix(pb, []byte("%PDF-1.4")) || !bytes.Contains(pb, []byte("%%EOF")) || !bytes.Contains(pb, []byte("/Count ")) || bytes.Contains(pb, []byte("/Count 1 ")) {
		t.Fatalf("pdf: %d bytes, several pages expected", len(pb))
	}
	if got := encodeAnsi("€ “x” — ü ☃"); string(got) != "\x80 \x93x\x94 \x97 \xfc ?" {
		t.Fatalf("%q", got)
	}
}

func TestHTTPRequestAndNotify(t *testing.T) {
	var got struct {
		method, auth, ctype, body, path string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bs, _ := io.ReadAll(r.Body)
		got.method, got.auth, got.ctype, got.body, got.path = r.Method, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(bs), r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
		}
		_, _ = w.Write([]byte(`{"id": 7, "ok": true}`))
	}))
	defer srv.Close()
	b, _ := box(t)
	b.secret = func(_ context.Context, name string) (string, error) {
		return map[string]string{"API_TOKEN": "tok-123", "SLACK_WEBHOOK_URL": srv.URL + "/hook"}[name], nil
	}
	var shown []string
	b.notify = func(title, msg, link string) error { shown = append(shown, title+"|"+msg); return nil }

	var j map[string]any
	_ = json.Unmarshal([]byte(call(t, b, "http_request", map[string]any{"url": srv.URL + "/items", "query": map[string]any{"q": "a b"}, "format": "json"})), &j)
	if j["status"].(float64) != 200 || j["json"].(map[string]any)["id"].(float64) != 7 || got.path != "/items?q=a+b" {
		t.Fatalf("%v %s", j, got.path)
	}
	call(t, b, "http_request", map[string]any{"method": "POST", "url": srv.URL + "/items", "json": map[string]any{"name": "x"}, "auth_secret": "API_TOKEN"})
	if got.method != "POST" || got.auth != "Bearer tok-123" || got.ctype != "application/json" || got.body != `{"name":"x"}` {
		t.Fatalf("%+v", got)
	}
	call(t, b, "http_request", map[string]any{"url": srv.URL + "/x", "auth_secret": "API_TOKEN", "auth_scheme": "header:X-Api-Key"})
	if _, err := b.Call(context.Background(), "http_request", map[string]any{"url": srv.URL + "/missing"}); err == nil {
		t.Fatal("an error status is an error in text format")
	}
	if _, err := b.Call(context.Background(), "http_request", map[string]any{"url": "http://example.com/x", "auth_secret": "API_TOKEN"}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("a secret only over https: %v", err)
	}
	if _, err := b.Call(context.Background(), "http_request", map[string]any{"url": srv.URL, "headers": map[string]any{"Authorization": "x"}}); err == nil {
		t.Fatal("no Authorization header by hand")
	}

	// notify: the desktop, and a webhook (https only: the test server's address is not)
	call(t, b, "notify", map[string]any{"title": "Nightly tests", "message": "2 failed"})
	if len(shown) != 1 || shown[0] != "Nightly tests|2 failed" {
		t.Fatal(shown)
	}
	if _, err := b.Call(context.Background(), "notify", map[string]any{"title": "x", "channel": "slack"}); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("an http webhook is refused: %v", err)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bs, _ := io.ReadAll(r.Body)
		got.body, got.path = string(bs), r.URL.Path
	}))
	defer tls.Close()
	testTransport = tls.Client().Transport
	defer func() { testTransport = nil }()
	b.secret = func(_ context.Context, name string) (string, error) { return tls.URL + "/" + name, nil }
	call(t, b, "notify", map[string]any{"title": "Done", "message": "All good", "link": "https://x.example/run", "channel": "slack"})
	var slack map[string]string
	_ = json.Unmarshal([]byte(got.body), &slack)
	if got.path != "/SLACK_WEBHOOK_URL" || slack["text"] != "*Done*\nAll good\n<https://x.example/run>" {
		t.Fatalf("%s %s", got.path, got.body)
	}
	call(t, b, "notify", map[string]any{"title": "Done", "channel": "teams"})
	if got.path != "/TEAMS_WEBHOOK_URL" || !strings.Contains(got.body, "AdaptiveCard") {
		t.Fatal(got.body)
	}

	// which calls send data out
	for _, c := range []struct {
		name string
		a    map[string]any
		out  bool
	}{
		{"http_request", map[string]any{"url": "https://x"}, false},
		{"http_request", map[string]any{"method": "head", "url": "https://x"}, false},
		{"http_request", map[string]any{"method": "post", "url": "https://x"}, true},
		{"notify", map[string]any{"title": "x"}, false},
		{"notify", map[string]any{"channel": "teams"}, true},
		{"read_file", map[string]any{"path": "x"}, false},
	} {
		if out, _ := SendsOut(c.name, c.a); out != c.out {
			t.Errorf("SendsOut(%s, %v) = %v", c.name, c.a, out)
		}
	}
}
