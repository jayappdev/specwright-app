package builtin

// test_runner: runs the project's tests with its own tool and says what
// passed and what failed — per test, with the failure messages — instead of
// a wall of output. It finds the tool from the project's files (go.mod,
// pom.xml, build.gradle, package.json, pyproject.toml, Cargo.toml, a .NET
// project) and reads the tool's own reports (go test -json, JUnit XML from
// Maven, Gradle and pytest; the summary line for npm, Cargo and dotnet).
// The commands are fixed by the tool, run without a shell; filter only
// selects tests.

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TestResult is what test_runner found.
type TestResult struct {
	Tool       string        `json:"tool"`
	Command    string        `json:"command"`
	Dir        string        `json:"dir"`
	OK         bool          `json:"ok"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	Skipped    int           `json:"skipped"`
	DurationMS int64         `json:"durationMs"`
	Failures   []TestFailure `json:"failures"`
	Note       string        `json:"note,omitempty"` // how the numbers were found, or why they are missing
	OutputTail string        `json:"outputTail,omitempty"`
}

// TestFailure is one failed test.
type TestFailure struct {
	Name    string `json:"name"`
	Suite   string `json:"suite,omitempty"`
	Message string `json:"message"`
}

var filterRE = regexp.MustCompile(`^[A-Za-z0-9_.*/:#$\[\] -]{1,200}$`)

// detectTests picks the test tool from the files in dir.
func detectTests(dir string) string {
	has := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	switch {
	case has("go.mod"):
		return "go"
	case has("pom.xml"):
		return "maven"
	case has("build.gradle") || has("build.gradle.kts"):
		return "gradle"
	case has("Cargo.toml"):
		return "cargo"
	case has("package.json"):
		return "npm"
	case has("pyproject.toml") || has("pytest.ini") || has("setup.cfg") || has("tox.ini"):
		return "pytest"
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.sln")); len(m) > 0 {
		return "dotnet"
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.csproj")); len(m) > 0 {
		return "dotnet"
	}
	return ""
}

func testRunner(ctx context.Context, b *Box, a args) (string, error) {
	abs, rel, err := b.resolve(firstStr(a.str("path"), "."))
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a folder", rel)
	}
	tool := strings.ToLower(strings.TrimSpace(a.str("tool")))
	if tool == "" || tool == "auto" {
		tool = detectTests(abs)
	}
	if tool == "" {
		return "", fmt.Errorf("no test tool found in %s (looked for go.mod, pom.xml, build.gradle, package.json, pyproject.toml, Cargo.toml, a .NET project); give tool, or path to the project folder", rel)
	}
	filter := strings.TrimSpace(a.str("filter"))
	if filter != "" && !filterRE.MatchString(filter) {
		return "", fmt.Errorf("filter %q: use test names, * and . only", filter)
	}
	timeout := time.Duration(a.num("timeout_seconds", 600, 10, 3600)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	var argv []string
	var junit string // where JUnit reports are written (pytest)
	switch tool {
	case "go":
		argv = []string{"go", "test", "-json", "-count=1"}
		if filter != "" {
			argv = append(argv, "-run", filter)
		}
		argv = append(argv, "./...")
	case "maven":
		mvn := "mvn"
		if _, err := os.Stat(filepath.Join(abs, "mvnw")); err == nil {
			mvn = "./mvnw"
		}
		argv = []string{mvn, "-B", "-q", "test"}
		if filter != "" {
			argv = append(argv, "-Dtest="+filter, "-Dsurefire.failIfNoSpecifiedTests=false")
		}
	case "gradle":
		g := "gradle"
		if _, err := os.Stat(filepath.Join(abs, "gradlew")); err == nil {
			g = "./gradlew"
		}
		argv = []string{g, "test", "--console=plain"}
		if filter != "" {
			argv = append(argv, "--tests", filter)
		}
	case "npm":
		argv = []string{"npm", "test", "--silent"}
		if filter != "" {
			argv = append(argv, "--", "-t", filter)
		}
	case "pytest":
		f, err := os.CreateTemp("", "specwright-pytest-*.xml")
		if err != nil {
			return "", err
		}
		junit = f.Name()
		f.Close()
		defer os.Remove(junit)
		py := "python3"
		if _, err := exec.LookPath(py); err != nil {
			py = "python"
		}
		argv = []string{py, "-m", "pytest", "-q", "--junitxml=" + junit}
		if filter != "" {
			argv = append(argv, "-k", filter)
		}
	case "cargo":
		argv = []string{"cargo", "test"}
		if filter != "" {
			argv = append(argv, filter)
		}
	case "dotnet":
		argv = []string{"dotnet", "test"}
		if filter != "" {
			argv = append(argv, "--filter", filter)
		}
	default:
		return "", fmt.Errorf("tool must be go, maven, gradle, npm, pytest, cargo or dotnet, not %q", tool)
	}
	if !strings.HasPrefix(argv[0], "./") {
		if _, err := exec.LookPath(argv[0]); err != nil {
			return "", fmt.Errorf("%s is not installed (needed to run the %s tests)", argv[0], tool)
		}
	}
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Dir = abs
	c.Env = os.Environ()
	if b.env != nil {
		c.Env = append(c.Env, b.env()...)
	}
	c.Env = append(c.Env, "CI=1", "NO_COLOR=1", "TERM=dumb", "FORCE_COLOR=0")
	out := &ring{n: 4 << 20}
	c.Stdout, c.Stderr = out, out
	c.WaitDelay = 2 * time.Second
	runErr := c.Run()
	res := TestResult{Tool: tool, Command: strings.Join(argv, " "), Dir: rel, DurationMS: time.Since(start).Milliseconds(), Failures: []TestFailure{}}
	text := out.String()
	switch tool {
	case "go":
		parseGoTest(text, &res)
	case "maven":
		readJUnit(&res, start, filepath.Join(abs), "surefire-reports", "failsafe-reports")
	case "gradle":
		readJUnit(&res, start, filepath.Join(abs), "test-results")
	case "pytest":
		readJUnitFile(&res, junit)
	default:
		parseSummary(text, &res)
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.OK, res.Note = false, fmt.Sprintf("stopped after %s (timeout_seconds)", timeout)
	case runErr != nil && res.Failed == 0:
		res.OK = false
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return "", fmt.Errorf("could not run %s: %v", argv[0], runErr)
		}
		res.Note = strings.TrimSpace(res.Note + " The command failed (exit code " + strconv.Itoa(ee.ExitCode()) + ") without a failed test: likely a build or setup error; see the output.")
	default:
		res.OK = runErr == nil && res.Failed == 0
	}
	if !res.OK || res.Passed+res.Failed+res.Skipped == 0 {
		tail := text
		if tool == "go" {
			tail = goPlainOutput(text)
		}
		if len(tail) > 4000 {
			tail = "…" + tail[len(tail)-4000:]
		}
		res.OutputTail = strings.TrimSpace(tail)
	}
	if strings.EqualFold(a.str("format"), "json") {
		return toJSON(res), nil
	}
	return testText(res), nil
}

func testText(r TestResult) string {
	var s strings.Builder
	mark := "PASSED"
	if !r.OK {
		mark = "FAILED"
	}
	fmt.Fprintf(&s, "%s: %d passed, %d failed, %d skipped · %s in %s (%.1f s)\n", mark, r.Passed, r.Failed, r.Skipped, r.Command, firstStr(r.Dir, "."), float64(r.DurationMS)/1000)
	if r.Note != "" {
		s.WriteString(r.Note + "\n")
	}
	for i, f := range r.Failures {
		if i == 20 {
			fmt.Fprintf(&s, "… and %d more failures\n", len(r.Failures)-20)
			break
		}
		name := f.Name
		if f.Suite != "" {
			name = f.Suite + " › " + f.Name
		}
		fmt.Fprintf(&s, "\n✗ %s\n%s\n", name, indent(clipStr(f.Message, 800)))
	}
	if r.OutputTail != "" {
		s.WriteString("\nOutput (end):\n" + r.OutputTail + "\n")
	}
	return s.String()
}

func indent(s string) string {
	return "    " + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n    ")
}

func clipStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- go test -json -----------------------------------------------------------------

func parseGoTest(out string, r *TestResult) {
	type ev struct {
		Action, Package, Test, Output string
	}
	logs := map[string]*strings.Builder{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		var e ev
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		key := e.Package + " " + e.Test
		switch e.Action {
		case "output":
			if e.Test != "" {
				if logs[key] == nil {
					logs[key] = &strings.Builder{}
				}
				if logs[key].Len() < 4000 {
					logs[key].WriteString(e.Output)
				}
			}
		case "pass":
			if e.Test != "" {
				r.Passed++
			}
		case "skip":
			if e.Test != "" {
				r.Skipped++
			}
		case "fail":
			if e.Test != "" {
				r.Failed++
				msg := ""
				if l := logs[key]; l != nil {
					msg = goFailureLines(l.String())
				}
				r.Failures = append(r.Failures, TestFailure{Name: e.Test, Suite: e.Package, Message: msg})
			}
		}
	}
	r.Note = "from go test -json"
}

// goFailureLines keeps a test's own messages, not the === RUN / --- FAIL lines.
func goFailureLines(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "=== ") || strings.HasPrefix(t, "--- ") {
			continue
		}
		keep = append(keep, t)
	}
	return strings.Join(keep, "\n")
}

// goPlainOutput is go test -json's output text (build errors, panics).
func goPlainOutput(out string) string {
	var b strings.Builder
	for _, l := range strings.Split(out, "\n") {
		var e struct{ Action, Output string }
		if json.Unmarshal([]byte(l), &e) == nil && e.Action != "" {
			if e.Action == "output" || e.Action == "build-output" { // build errors (Go 1.24+)
				b.WriteString(e.Output)
			}
			continue
		}
		b.WriteString(l + "\n") // not JSON: the build's own errors
	}
	return b.String()
}

// --- JUnit XML (Maven, Gradle, pytest) ---------------------------------------------

type junitCase struct {
	Name      string `xml:"name,attr"`
	Classname string `xml:"classname,attr"`
	Failure   *struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	} `xml:"failure"`
	Error *struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	} `xml:"error"`
	Skipped *struct{} `xml:"skipped"`
}

type junitSuite struct {
	Cases  []junitCase  `xml:"testcase"`
	Suites []junitSuite `xml:"testsuite"`
}

func addJUnit(r *TestResult, s junitSuite) {
	for _, c := range s.Cases {
		switch {
		case c.Failure != nil || c.Error != nil:
			r.Failed++
			msg := ""
			if c.Failure != nil {
				msg = strings.TrimSpace(c.Failure.Message + "\n" + c.Failure.Text)
			} else {
				msg = strings.TrimSpace(c.Error.Message + "\n" + c.Error.Text)
			}
			r.Failures = append(r.Failures, TestFailure{Name: c.Name, Suite: c.Classname, Message: msg})
		case c.Skipped != nil:
			r.Skipped++
		default:
			r.Passed++
		}
	}
	for _, x := range s.Suites {
		addJUnit(r, x)
	}
}

func readJUnitFile(r *TestResult, path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return false
	}
	var s junitSuite
	if xml.Unmarshal(b, &s) != nil {
		return false
	}
	addJUnit(r, s)
	r.Note = "from the JUnit report"
	return true
}

// readJUnit reads the TEST-*.xml reports written since start, in folders
// named like dirs (target/surefire-reports, build/test-results/test), in
// every module.
func readJUnit(r *TestResult, start time.Time, root string, dirs ...string) {
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "TEST-") || !strings.HasSuffix(name, ".xml") {
			return nil
		}
		in := false
		for _, x := range dirs {
			in = in || strings.Contains(filepath.ToSlash(p), "/"+x+"/")
		}
		if fi, err := d.Info(); err != nil || !in || fi.ModTime().Before(start.Add(-2*time.Second)) {
			return nil
		}
		if readJUnitFile(r, p) {
			n++
		}
		return nil
	})
	if n == 0 {
		r.Note = "no test reports were written (the build may have failed before the tests ran)"
	} else {
		r.Note = fmt.Sprintf("from %s", plural(n, "test report", "test reports"))
	}
}

// --- summary lines (npm/jest/vitest, cargo, dotnet) --------------------------------

var (
	jestRE   = regexp.MustCompile(`(?m)^\s*Tests:\s+(.*)$`)                         // Tests: 1 failed, 5 passed, 6 total
	vitestRE = regexp.MustCompile(`(?m)^\s*Tests\s+(.*\d+\s+(?:passed|failed).*)$`) // Tests  2 failed | 10 passed (12)
	cargoRE  = regexp.MustCompile(`test result: \w+\. (\d+) passed; (\d+) failed; (\d+) ignored`)
	dotnetRE = regexp.MustCompile(`Failed:\s*(\d+), Passed:\s*(\d+), Skipped:\s*(\d+)`)
	countRE  = regexp.MustCompile(`(\d+)\s+(passed|failed|skipped|todo|pending)`)
	failLine = regexp.MustCompile(`(?m)^\s*(?:✕|×|FAIL|●)\s+(.+)$`)
)

func parseSummary(out string, r *TestResult) {
	switch {
	case cargoRE.MatchString(out):
		for _, m := range cargoRE.FindAllStringSubmatch(out, -1) {
			p, _ := strconv.Atoi(m[1])
			f, _ := strconv.Atoi(m[2])
			s, _ := strconv.Atoi(m[3])
			r.Passed, r.Failed, r.Skipped = r.Passed+p, r.Failed+f, r.Skipped+s
		}
	case dotnetRE.MatchString(out):
		for _, m := range dotnetRE.FindAllStringSubmatch(out, -1) {
			f, _ := strconv.Atoi(m[1])
			p, _ := strconv.Atoi(m[2])
			s, _ := strconv.Atoi(m[3])
			r.Passed, r.Failed, r.Skipped = r.Passed+p, r.Failed+f, r.Skipped+s
		}
	default:
		line := ""
		if m := jestRE.FindStringSubmatch(out); m != nil {
			line = m[1]
		} else if m := vitestRE.FindStringSubmatch(out); m != nil {
			line = m[1]
		}
		for _, m := range countRE.FindAllStringSubmatch(line, -1) {
			n, _ := strconv.Atoi(m[1])
			switch m[2] {
			case "passed":
				r.Passed += n
			case "failed":
				r.Failed += n
			default:
				r.Skipped += n
			}
		}
	}
	for _, m := range failLine.FindAllStringSubmatch(out, 50) {
		r.Failures = append(r.Failures, TestFailure{Name: strings.TrimSpace(m[1])})
	}
	if r.Passed+r.Failed+r.Skipped == 0 {
		r.Note = "no summary line found in the output; the result is the command's exit code"
	} else {
		r.Note = "from the summary line"
	}
}
