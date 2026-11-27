package builtin

// git: read-only source control information for flows and agents —
// status, a diff between two points, the log, branches, tags, one commit,
// and who last changed each line. With format "json" the results are data
// for later steps (release notes from the log, a review of the diff's
// files); otherwise text for the AI. Nothing here changes the repository.

import (
	"bytes"
	"codegen/internal/safeenv"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// refRE: a revision, a range (a..b, a...b) or a date ("2026-11-01"), never
// an option
var refRE = regexp.MustCompile(`^[A-Za-z0-9_./@{}~^:+-]{1,200}$`)

func checkRef(name, v string) error {
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "-") || !refRE.MatchString(v) {
		return fmt.Errorf("%s %q is not a git revision (a branch, tag, commit or HEAD~3)", name, v)
	}
	return nil
}

func (b *Box) git(ctx context.Context, gitArgs ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false", "--no-pager"}, gitArgs...)...)
	c.Dir = b.Root()
	c.Env = append(safeenv.Clean(c.Environ()), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return out.String(), errors.New("git " + gitArgs[0] + ": " + msg)
	}
	return out.String(), nil
}

func gitTool(ctx context.Context, b *Box, a args) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", errors.New("git is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if out, err := b.git(ctx, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return "", errors.New("the workspace is not a git repository")
	}
	action := strings.ToLower(strings.TrimSpace(a.str("action")))
	if action == "" {
		action = "status"
	}
	asJSON := strings.EqualFold(a.str("format"), "json")
	ref, base := strings.TrimSpace(a.str("ref")), strings.TrimSpace(a.str("base"))
	if err := checkRef("ref", ref); err != nil {
		return "", err
	}
	if err := checkRef("base", base); err != nil {
		return "", err
	}
	var pathArgs []string
	if p := a.str("path"); p != "" {
		_, rel, err := b.resolve(p)
		if err != nil {
			return "", err
		}
		pathArgs = []string{"--", rel}
	}
	limit := a.num("max_bytes", 40000, 1000, 400000)
	cut := func(s string) string {
		if len(s) > limit {
			return s[:limit] + fmt.Sprintf("\n… cut at %s; pass path to see one file, or raise max_bytes\n", human(int64(limit)))
		}
		return s
	}
	switch action {
	case "status":
		out, err := b.git(ctx, append([]string{"status", "--porcelain=v2", "--branch", "--untracked-files=all"}, pathArgs...)...)
		if err != nil {
			return "", err
		}
		st := parseStatus(out)
		if asJSON {
			return toJSON(st), nil
		}
		var s strings.Builder
		fmt.Fprintf(&s, "On %s", firstStr(st.Branch, "(detached)"))
		if st.Upstream != "" {
			fmt.Fprintf(&s, " (tracking %s: %d ahead, %d behind)", st.Upstream, st.Ahead, st.Behind)
		}
		if len(st.Files) == 0 {
			s.WriteString("\nNothing changed.")
		}
		for _, f := range st.Files {
			fmt.Fprintf(&s, "\n%-10s %s", f.Status, f.Path)
		}
		return s.String(), nil
	case "diff":
		// base..ref; ref alone: ref against the working tree; neither: uncommitted changes
		rng := []string{}
		switch {
		case base != "" && ref != "":
			rng = []string{base + ".." + ref}
		case base != "":
			rng = []string{base}
		case ref != "":
			rng = []string{ref}
		default:
			rng = []string{"HEAD"}
		}
		if asJSON {
			num, err := b.git(ctx, append(append([]string{"diff", "--numstat"}, rng...), pathArgs...)...)
			if err != nil {
				return "", err
			}
			patch, _ := b.git(ctx, append(append([]string{"diff", "--no-color", "--unified=3"}, rng...), pathArgs...)...)
			return toJSON(map[string]any{"range": strings.Join(rng, " "), "files": parseNumstat(num), "patch": cut(patch)}), nil
		}
		out, err := b.git(ctx, append(append([]string{"diff", "--stat", "--patch", "--no-color", "--unified=3"}, rng...), pathArgs...)...)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(out) == "" {
			return "No differences for " + strings.Join(rng, " ") + ".", nil
		}
		return cut(out), nil
	case "log":
		n := a.num("max_count", 30, 1, 500)
		lg := []string{"log", fmt.Sprintf("--max-count=%d", n), "--date=iso-strict", "--format=%H%x1f%h%x1f%an%x1f%ae%x1f%ad%x1f%s%x1f%b%x1e"}
		if s := strings.TrimSpace(a.str("since")); s != "" {
			if err := checkRef("since", s); err != nil {
				return "", err
			}
			if isDate(s) {
				lg = append(lg, "--since="+s)
			} else { // a tag or commit: what came after it
				lg = append(lg, s+".."+firstStr(ref, "HEAD"))
				ref = ""
			}
		}
		if ref != "" {
			lg = append(lg, ref)
		}
		out, err := b.git(ctx, append(lg, pathArgs...)...)
		if err != nil {
			return "", err
		}
		cs := parseLog(out)
		if asJSON {
			return toJSON(cs), nil
		}
		if len(cs) == 0 {
			return "No commits.", nil
		}
		var s strings.Builder
		for _, c := range cs {
			fmt.Fprintf(&s, "%s %s %s · %s\n", c.Short, c.Date[:min(10, len(c.Date))], c.Subject, c.Author)
		}
		return s.String(), nil
	case "branches", "tags":
		var out string
		var err error
		if action == "branches" {
			out, err = b.git(ctx, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)%1f%(objectname:short)%1f%(committerdate:iso-strict)%1f%(HEAD)%1f%(upstream:short)", "refs/heads", "refs/remotes")
		} else {
			out, err = b.git(ctx, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)%1f%(objectname:short)%1f%(creatordate:iso-strict)%1f%1f%(subject)", "refs/tags")
		}
		if err != nil {
			return "", err
		}
		var list []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Split(l, "\x1f")
			if len(f) < 5 || f[0] == "" {
				continue
			}
			m := map[string]any{"name": f[0], "commit": f[1], "date": f[2]}
			if action == "branches" {
				m["current"] = f[3] == "*"
				if f[4] != "" {
					m["upstream"] = f[4]
				}
			} else if f[4] != "" {
				m["message"] = f[4]
			}
			list = append(list, m)
		}
		if asJSON {
			return toJSON(list), nil
		}
		var s strings.Builder
		for _, m := range list {
			mark := "  "
			if m["current"] == true {
				mark = "* "
			}
			fmt.Fprintf(&s, "%s%s (%s, %s)\n", mark, m["name"], m["commit"], fmt.Sprint(m["date"])[:min(10, len(fmt.Sprint(m["date"])))])
		}
		if len(list) == 0 {
			return "None.", nil
		}
		return s.String(), nil
	case "show":
		out, err := b.git(ctx, append([]string{"show", "--stat", "--patch", "--no-color", "--format=commit %H%nAuthor: %an <%ae>%nDate:   %ad%n%n%B", firstStr(ref, "HEAD")}, pathArgs...)...)
		if err != nil {
			return "", err
		}
		return cut(out), nil
	case "blame":
		if len(pathArgs) == 0 {
			return "", errors.New("blame needs path (a file)")
		}
		bl := []string{"blame", "--date=short"}
		if s, e := a.num("start_line", 0, 0, 0), a.num("end_line", 0, 0, 0); s > 0 {
			if e < s {
				e = s + 40
			}
			bl = append(bl, "-L", strconv.Itoa(s)+","+strconv.Itoa(e))
		}
		if ref != "" {
			bl = append(bl, ref)
		}
		out, err := b.git(ctx, append(bl, pathArgs...)...)
		if err != nil {
			return "", err
		}
		return cut(out), nil
	}
	return "", fmt.Errorf("action must be status, diff, log, branches, tags, show or blame, not %q", action)
}

type gitStatus struct {
	Branch   string    `json:"branch"`
	Upstream string    `json:"upstream,omitempty"`
	Ahead    int       `json:"ahead"`
	Behind   int       `json:"behind"`
	Files    []gitFile `json:"files"`
}

type gitFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // modified | added | deleted | renamed | untracked | conflict
	Staged bool   `json:"staged,omitempty"`
}

func parseStatus(out string) gitStatus {
	st := gitStatus{Files: []gitFile{}}
	word := map[byte]string{'M': "modified", 'A': "added", 'D': "deleted", 'R': "renamed", 'C': "copied", 'T': "modified"}
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "# branch.head "):
			st.Branch = strings.TrimPrefix(l, "# branch.head ")
			if st.Branch == "(detached)" {
				st.Branch = ""
			}
		case strings.HasPrefix(l, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(l, "# branch.upstream ")
		case strings.HasPrefix(l, "# branch.ab "):
			_, _ = fmt.Sscanf(strings.TrimPrefix(l, "# branch.ab "), "+%d -%d", &st.Ahead, &st.Behind)
		case strings.HasPrefix(l, "1 ") || strings.HasPrefix(l, "2 "):
			f := strings.Fields(l)
			if len(f) < 9 {
				continue
			}
			xy := f[1]
			path := strings.Join(f[8:], " ")
			if l[0] == '2' && len(f) >= 10 { // renamed: "<path>\t<orig>"
				path = strings.SplitN(strings.Join(f[9:], " "), "\t", 2)[0]
			}
			c := xy[1]
			staged := false
			if c == '.' {
				c, staged = xy[0], true
			}
			st.Files = append(st.Files, gitFile{Path: path, Status: firstStr(word[c], "modified"), Staged: staged})
		case strings.HasPrefix(l, "u "):
			f := strings.Fields(l)
			st.Files = append(st.Files, gitFile{Path: f[len(f)-1], Status: "conflict"})
		case strings.HasPrefix(l, "? "):
			st.Files = append(st.Files, gitFile{Path: strings.TrimPrefix(l, "? "), Status: "untracked"})
		}
	}
	return st
}

func parseNumstat(out string) []map[string]any {
	files := []map[string]any{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) < 3 {
			continue
		}
		m := map[string]any{"path": f[2]}
		if f[0] == "-" {
			m["binary"] = true
		} else {
			m["added"], _ = strconv.Atoi(f[0])
			m["removed"], _ = strconv.Atoi(f[1])
		}
		files = append(files, m)
	}
	return files
}

type gitCommit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Email   string `json:"email"`
	Date    string `json:"date"`
	Subject string `json:"subject"`
	Body    string `json:"body,omitempty"`
}

func parseLog(out string) []gitCommit {
	cs := []gitCommit{}
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimLeft(rec, "\n"), "\x1f")
		if len(f) < 7 || f[0] == "" {
			continue
		}
		cs = append(cs, gitCommit{Hash: f[0], Short: f[1], Author: f[2], Email: f[3], Date: f[4], Subject: f[5], Body: strings.TrimSpace(f[6])})
	}
	return cs
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)

func isDate(s string) bool { return dateRE.MatchString(s) }

func toJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func firstStr(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
