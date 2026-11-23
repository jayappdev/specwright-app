package builtin

// export_doc: turns Markdown (a report a flow wrote, a spec) into a file to
// share: HTML (one self-contained page), Word (.docx) or PDF. Markdown is
// read once (goldmark, with GitHub tables, task lists and strike-through)
// into blocks — headings, paragraphs, lists, code, quotes, tables, rules —
// that the Word and PDF writers lay out (docx.go, pdf.go). Raw HTML in the
// Markdown is left out; images are named, not embedded.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// span is a piece of text with one style.
type span struct {
	text               string
	bold, italic, mono bool
	strike             bool
	link               string
}

// block is one laid-out unit of a document.
type block struct {
	kind   string // h | p | li | code | quote | hr | table
	level  int    // heading level, or list depth (0-based)
	marker string // a list item's bullet or number
	spans  []span
	lines  []string   // code
	rows   [][]string // table cells (plain text); the first row is the header
}

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

func exportDoc(_ context.Context, b *Box, a args) (string, error) {
	src := a.str("markdown")
	from := ""
	if src == "" {
		p := a.str("path")
		if p == "" {
			return "", errors.New("give markdown (the text) or path (a Markdown file in the workspace)")
		}
		abs, rel, err := b.resolve(p)
		if err != nil {
			return "", err
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("%s: %v", rel, err)
		}
		src, from = string(raw), rel
	}
	to := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(a.str("to")), "."))
	if to == "" {
		to = "html"
	}
	if to == "word" {
		to = "docx"
	}
	if to != "html" && to != "docx" && to != "pdf" {
		return "", fmt.Errorf("to must be html, docx or pdf, not %q", to)
	}
	srcb := []byte(src)
	doc := md.Parser().Parse(text.NewReader(srcb))
	blocks := mdBlocks(doc, srcb)
	title := strings.TrimSpace(a.str("title"))
	if title == "" {
		for _, bl := range blocks {
			if bl.kind == "h" {
				title = plainSpans(bl.spans)
				break
			}
		}
	}
	if title == "" {
		title = "Document"
	}
	out := strings.TrimSpace(a.str("out"))
	if out == "" {
		base := slugName(title)
		if from != "" {
			out = strings.TrimSuffix(from, filepath.Ext(from)) + "." + to
		} else {
			out = "exports/" + base + "." + to
		}
	}
	if !strings.HasSuffix(strings.ToLower(out), "."+to) {
		out += "." + to
	}
	abs, rel, err := b.resolve(out)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err == nil && !a.flag("overwrite") {
		return "", fmt.Errorf("%s exists; pass overwrite: true to replace it, or another out", rel)
	}
	var data []byte
	switch to {
	case "html":
		var body bytes.Buffer
		if err := md.Renderer().Render(&body, srcb, doc); err != nil {
			return "", err
		}
		data = []byte(htmlPage(title, body.String()))
	case "docx":
		data, err = docxFile(title, blocks)
	case "pdf":
		data, err = pdfFile(title, blocks)
	}
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := writeAtomic(abs, data, 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %s (%s, %s).", rel, strings.ToUpper(to), human(int64(len(data)))), nil
}

var slugRE = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slugName(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return firstStr(s, "document")
}

func plainSpans(ss []span) string {
	var b strings.Builder
	for _, s := range ss {
		b.WriteString(s.text)
	}
	return strings.TrimSpace(b.String())
}

// mdBlocks lays the Markdown tree out as blocks.
func mdBlocks(doc ast.Node, src []byte) []block {
	var out []block
	var visit func(c ast.Node, depth int, quote bool)
	children := func(n ast.Node, depth int, quote bool) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c, depth, quote)
		}
	}
	visit = func(c ast.Node, depth int, quote bool) {
		switch v := c.(type) {
		case *ast.Heading:
			out = append(out, block{kind: "h", level: v.Level, spans: inlineSpans(v, src)})
		case *ast.Paragraph, *ast.TextBlock:
			k := "p"
			if quote {
				k = "quote"
			}
			out = append(out, block{kind: k, level: depth, spans: inlineSpans(c, src)})
		case *ast.List:
			num := v.Start
			for it := v.FirstChild(); it != nil; it = it.NextSibling() {
				marker := "•"
				if v.IsOrdered() {
					marker = fmt.Sprintf("%d.", num)
					num++
				}
				first := true
				for ic := it.FirstChild(); ic != nil; ic = ic.NextSibling() {
					switch ic.(type) {
					case *ast.Paragraph, *ast.TextBlock:
						bl := block{kind: "li", level: depth, spans: inlineSpans(ic, src)}
						if first {
							bl.marker = marker
						}
						out = append(out, bl)
					default: // a nested list, code, a quote: one level in
						visit(ic, depth+1, quote)
					}
					first = false
				}
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			var lines []string
			for i := 0; i < c.Lines().Len(); i++ {
				seg := c.Lines().At(i)
				lines = append(lines, strings.TrimRight(string(seg.Value(src)), "\r\n"))
			}
			out = append(out, block{kind: "code", level: depth, lines: lines})
		case *ast.Blockquote:
			children(c, depth, true)
		case *ast.ThematicBreak:
			out = append(out, block{kind: "hr"})
		case *east.Table:
			var rows [][]string
			for r := c.FirstChild(); r != nil; r = r.NextSibling() {
				var row []string
				for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
					row = append(row, plainSpans(inlineSpans(cell, src)))
				}
				rows = append(rows, row)
			}
			out = append(out, block{kind: "table", rows: rows})
		case *ast.HTMLBlock:
			// left out: raw HTML has no place in a Word or PDF file
		default:
			children(c, depth, quote)
		}
	}
	children(doc, 0, false)
	return out
}

// inlineSpans is a block's text with its styles.
func inlineSpans(n ast.Node, src []byte) []span {
	var out []span
	var walk func(n ast.Node, st span)
	walk = func(n ast.Node, st span) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch v := c.(type) {
			case *ast.Text:
				s := st
				s.text = string(v.Segment.Value(src))
				out = append(out, s)
				if v.HardLineBreak() {
					out = append(out, span{text: "\n"})
				} else if v.SoftLineBreak() {
					out = append(out, span{text: " "})
				}
			case *ast.String:
				s := st
				s.text = string(v.Value)
				out = append(out, s)
			case *ast.Emphasis:
				s := st
				if v.Level >= 2 {
					s.bold = true
				} else {
					s.italic = true
				}
				walk(c, s)
			case *ast.CodeSpan:
				s := st
				s.mono = true
				var b strings.Builder
				for t := c.FirstChild(); t != nil; t = t.NextSibling() {
					if tt, ok := t.(*ast.Text); ok {
						b.Write(tt.Segment.Value(src))
					}
				}
				s.text = b.String()
				out = append(out, s)
			case *ast.Link:
				s := st
				s.link = string(v.Destination)
				walk(c, s)
			case *ast.AutoLink:
				s := st
				s.link = string(v.URL(src))
				s.text = string(v.Label(src))
				out = append(out, s)
			case *ast.Image:
				alt := plainSpans(inlineSpans(c, src))
				out = append(out, span{text: "[image: " + firstStr(alt, string(v.Destination)) + "]", italic: true})
			case *east.Strikethrough:
				s := st
				s.strike = true
				walk(c, s)
			case *east.TaskCheckBox:
				box := "[ ] "
				if v.IsChecked {
					box = "[x] "
				}
				out = append(out, span{text: box, mono: true})
			case *ast.RawHTML:
				// left out
			default:
				walk(c, st)
			}
		}
	}
	walk(n, span{})
	// neighbours with the same style are one piece (the parser splits text)
	var merged []span
	for _, s := range out {
		if k := len(merged) - 1; k >= 0 {
			p := merged[k]
			if p.bold == s.bold && p.italic == s.italic && p.mono == s.mono && p.strike == s.strike && p.link == s.link {
				merged[k].text += s.text
				continue
			}
		}
		merged = append(merged, s)
	}
	return merged
}

// htmlPage wraps the rendered Markdown in a self-contained page.
func htmlPage(title, body string) string {
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(title) + `</title>
<style>
:root { color-scheme: light dark; --fg: #1f2328; --muted: #59636e; --bg: #fff; --line: #d1d9e0; --code: #f6f8fa; --link: #0969da; }
@media (prefers-color-scheme: dark) { :root { --fg: #e6edf3; --muted: #9198a1; --bg: #0d1117; --line: #3d444d; --code: #151b23; --link: #4493f8; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.6 -apple-system, "Segoe UI", Helvetica, Arial, sans-serif; }
main { max-width: 860px; margin: 0 auto; padding: 32px 20px 64px; }
h1, h2 { border-bottom: 1px solid var(--line); padding-bottom: .3em; }
h1, h2, h3, h4 { line-height: 1.25; margin: 1.5em 0 .6em; }
a { color: var(--link); }
code, pre { font: 13.5px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; background: var(--code); border-radius: 6px; }
code { padding: .15em .35em; }
pre { padding: 14px 16px; overflow: auto; }
pre code { padding: 0; background: none; }
blockquote { margin: 0; padding: 0 1em; color: var(--muted); border-left: 4px solid var(--line); }
table { border-collapse: collapse; display: block; overflow: auto; margin: 1em 0; }
th, td { border: 1px solid var(--line); padding: 6px 12px; text-align: left; }
th { background: var(--code); }
hr { border: 0; border-top: 1px solid var(--line); margin: 2em 0; }
img { max-width: 100%; }
@media print { body { background: #fff; color: #000; } main { max-width: none; padding: 0; } }
</style></head>
<body><main>
` + body + `</main></body></html>
`
}
