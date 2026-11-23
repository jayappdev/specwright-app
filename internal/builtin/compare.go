package builtin

// file_compare: what is different between two files or two folders of the
// workspace. Text files: a unified diff. JSON files: the values that were
// added, removed or changed, by their path ($.a.b[2]). Folders: the files
// only in one of them and the ones that differ (with their diffs, for text,
// up to a limit). format "json" gives the result as data for later steps.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

func fileCompare(ctx context.Context, b *Box, a args) (string, error) {
	pa, err := a.need("a")
	if err != nil {
		return "", err
	}
	pb, err := a.need("b")
	if err != nil {
		return "", err
	}
	absA, relA, err := b.resolve(pa)
	if err != nil {
		return "", err
	}
	absB, relB, err := b.resolve(pb)
	if err != nil {
		return "", err
	}
	fa, err := os.Stat(absA)
	if err != nil {
		return "", fmt.Errorf("%s: %v", relA, err)
	}
	fb, err := os.Stat(absB)
	if err != nil {
		return "", fmt.Errorf("%s: %v", relB, err)
	}
	asJSON := strings.EqualFold(a.str("format"), "json")
	limit := a.num("max_bytes", 40000, 1000, 400000)
	switch {
	case fa.IsDir() && fb.IsDir():
		return compareDirs(ctx, absA, absB, relA, relB, asJSON, limit)
	case fa.IsDir() || fb.IsDir():
		return "", errors.New("compare two files or two folders, not a file with a folder")
	}
	ba, _ := os.ReadFile(absA)
	bb, _ := os.ReadFile(absB)
	if bytes.Equal(ba, bb) {
		if asJSON {
			return toJSON(map[string]any{"a": relA, "b": relB, "same": true}), nil
		}
		return fmt.Sprintf("%s and %s are the same.", relA, relB), nil
	}
	if isBinary(ba) || isBinary(bb) {
		msg := fmt.Sprintf("%s (%s) and %s (%s) differ; binary files, no diff.", relA, human(int64(len(ba))), relB, human(int64(len(bb))))
		if asJSON {
			return toJSON(map[string]any{"a": relA, "b": relB, "same": false, "binary": true}), nil
		}
		return msg, nil
	}
	mode := strings.ToLower(a.str("mode"))
	var ja, jb any
	isJSON := mode == "json" || (mode == "" && strings.HasSuffix(strings.ToLower(relA), ".json") && strings.HasSuffix(strings.ToLower(relB), ".json"))
	if isJSON && json.Unmarshal(ba, &ja) == nil && json.Unmarshal(bb, &jb) == nil {
		changes := jsonDiff("$", ja, jb, nil)
		if asJSON {
			return toJSON(map[string]any{"a": relA, "b": relB, "same": len(changes) == 0, "changes": changes}), nil
		}
		if len(changes) == 0 {
			return fmt.Sprintf("%s and %s hold the same JSON (they differ only in layout).", relA, relB), nil
		}
		var s strings.Builder
		fmt.Fprintf(&s, "%s → %s: %s\n", relA, relB, plural(len(changes), "change", "changes"))
		for i, c := range changes {
			if i == 300 {
				fmt.Fprintf(&s, "… and %d more\n", len(changes)-300)
				break
			}
			switch c.Change {
			case "added":
				fmt.Fprintf(&s, "+ %s = %s\n", c.Path, short(c.To))
			case "removed":
				fmt.Fprintf(&s, "- %s (was %s)\n", c.Path, short(c.From))
			default:
				fmt.Fprintf(&s, "~ %s: %s → %s\n", c.Path, short(c.From), short(c.To))
			}
		}
		return s.String(), nil
	}
	d := textDiff(ctx, b.Root(), absA, absB, relA, relB, string(ba), string(bb))
	if len(d) > limit {
		d = d[:limit] + fmt.Sprintf("\n… diff cut at %s; raise max_bytes\n", human(int64(limit)))
	}
	if asJSON {
		return toJSON(map[string]any{"a": relA, "b": relB, "same": false, "diff": d}), nil
	}
	return d, nil
}

// textDiff is a unified diff: git's when git is there (it does not need a
// repository), else a simple one.
func textDiff(ctx context.Context, root, absA, absB, relA, relB, ta, tb string) string {
	if _, err := exec.LookPath("git"); err == nil {
		c := exec.CommandContext(ctx, "git", "--no-pager", "diff", "--no-index", "--no-color", "--unified=3", "--", absA, absB)
		c.Dir = root
		var out bytes.Buffer
		c.Stdout = &out
		_ = c.Run() // exit code 1 means "they differ"
		if s := out.String(); s != "" {
			// workspace-relative names instead of absolute paths
			s = strings.ReplaceAll(s, "a"+filepath.ToSlash(absA), "a/"+relA)
			s = strings.ReplaceAll(s, "b"+filepath.ToSlash(absB), "b/"+relB)
			s = strings.ReplaceAll(s, filepath.ToSlash(absA), relA)
			s = strings.ReplaceAll(s, filepath.ToSlash(absB), relB)
			return s
		}
	}
	return "--- " + relA + "\n+++ " + relB + "\n" + diffSummary(ta, tb)
}

// JSONChange is one difference between two JSON documents.
type JSONChange struct {
	Path   string `json:"path"`
	Change string `json:"change"` // added | removed | changed
	From   any    `json:"from,omitempty"`
	To     any    `json:"to,omitempty"`
}

func jsonDiff(path string, a, b any, out []JSONChange) []JSONChange {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return append(out, JSONChange{Path: path, Change: "changed", From: a, To: b})
		}
		keys := map[string]bool{}
		for k := range av {
			keys[k] = true
		}
		for k := range bv {
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			p := path + "." + k
			x, inA := av[k]
			y, inB := bv[k]
			switch {
			case !inB:
				out = append(out, JSONChange{Path: p, Change: "removed", From: x})
			case !inA:
				out = append(out, JSONChange{Path: p, Change: "added", To: y})
			default:
				out = jsonDiff(p, x, y, out)
			}
		}
		return out
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return append(out, JSONChange{Path: path, Change: "changed", From: a, To: b})
		}
		for i := 0; i < max(len(av), len(bv)); i++ {
			p := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case i >= len(bv):
				out = append(out, JSONChange{Path: p, Change: "removed", From: av[i]})
			case i >= len(av):
				out = append(out, JSONChange{Path: p, Change: "added", To: bv[i]})
			default:
				out = jsonDiff(p, av[i], bv[i], out)
			}
		}
		return out
	}
	if !reflect.DeepEqual(a, b) {
		out = append(out, JSONChange{Path: path, Change: "changed", From: a, To: b})
	}
	return out
}

func short(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// hashFiles hashes a folder's files (skipping skipDirs: dependencies, build output, git).
func hashFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if n++; n > 20000 {
			return errors.New("more than 20,000 files: compare smaller folders")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		h := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%x", h[:8])
		return nil
	})
	return out, err
}

func compareDirs(ctx context.Context, absA, absB, relA, relB string, asJSON bool, limit int) (string, error) {
	ha, err := hashFiles(absA)
	if err != nil {
		return "", err
	}
	hb, err := hashFiles(absB)
	if err != nil {
		return "", err
	}
	var onlyA, onlyB, differ []string
	for p, h := range ha {
		switch hb2, ok := hb[p]; {
		case !ok:
			onlyA = append(onlyA, p)
		case hb2 != h:
			differ = append(differ, p)
		}
	}
	for p := range hb {
		if _, ok := ha[p]; !ok {
			onlyB = append(onlyB, p)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	sort.Strings(differ)
	diffs := map[string]string{}
	used := 0
	for _, p := range differ {
		if used > limit {
			break
		}
		x, _ := os.ReadFile(filepath.Join(absA, filepath.FromSlash(p)))
		y, _ := os.ReadFile(filepath.Join(absB, filepath.FromSlash(p)))
		if isBinary(x) || isBinary(y) {
			continue
		}
		d := textDiff(ctx, absA, filepath.Join(absA, filepath.FromSlash(p)), filepath.Join(absB, filepath.FromSlash(p)), relA+"/"+p, relB+"/"+p, string(x), string(y))
		if used+len(d) > limit {
			d = d[:max(0, limit-used)] + "\n… cut\n"
		}
		diffs[p], used = d, used+len(d)
	}
	if asJSON {
		return toJSON(map[string]any{"a": relA, "b": relB, "same": len(onlyA)+len(onlyB)+len(differ) == 0,
			"onlyInA": nz(onlyA), "onlyInB": nz(onlyB), "different": nz(differ), "diffs": diffs}), nil
	}
	if len(onlyA)+len(onlyB)+len(differ) == 0 {
		return fmt.Sprintf("%s and %s hold the same files (%s).", relA, relB, plural(len(ha), "file", "files")), nil
	}
	var s strings.Builder
	list := func(title string, ps []string) {
		if len(ps) == 0 {
			return
		}
		fmt.Fprintf(&s, "%s (%d):\n", title, len(ps))
		for i, p := range ps {
			if i == 200 {
				fmt.Fprintf(&s, "  … and %d more\n", len(ps)-200)
				break
			}
			s.WriteString("  " + p + "\n")
		}
	}
	list("Only in "+relA, onlyA)
	list("Only in "+relB, onlyB)
	list("Different", differ)
	for _, p := range differ {
		if d, ok := diffs[p]; ok {
			s.WriteString("\n" + d)
		}
	}
	if used > limit {
		s.WriteString(fmt.Sprintf("\n… diffs cut at %s; compare one file, or raise max_bytes\n", human(int64(limit))))
	}
	return s.String(), nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
