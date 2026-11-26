package builtin

// dashboard: one HTML page from several panels — the outputs of earlier
// flow steps (charts, tables, Markdown, JSON rows, text) — in a grid, with
// a title, an intro and the time it was made. Saved under out/dashboards.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DashboardMarker is on every dashboard page.
const DashboardMarker = `data-specwright-dashboard="1"`

const maxPanels = 8

// a dashboard panel, ready to place
type panel struct {
	title string
	body  string // HTML
	kind  string // chart | table | html | markdown | rows | text
	wide  bool
}

var (
	svgTitleRE   = regexp.MustCompile(`(?s)<title>(.*?)</title>`)
	tableBlockRE = regexp.MustCompile(`(?s)<div class="sw-table-wrap">.*</table>(?:<div class="more">.*?</div>)?</div>`)
	captionRE    = regexp.MustCompile(`(?s)<caption>(.*?)(?:<small>|</caption>)`)
	bodyRE       = regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	scriptRE     = regexp.MustCompile(`(?is)<script\b.*?</script>|\son[a-z]+\s*=\s*("[^"]*"|'[^']*')`)
	styleRE      = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	chartSVGRE   = regexp.MustCompile(`(?s)<svg\b[^>]*` + regexp.QuoteMeta(ChartMarker) + `.*?</svg>`)
)

// makePanel turns one step's output into a panel.
func makePanel(content string) panel {
	t := strings.TrimSpace(content)
	switch {
	case strings.Contains(t, ChartMarker) && chartSVGRE.MatchString(t):
		svg := chartSVGRE.FindString(t)
		title := ""
		if m := svgTitleRE.FindStringSubmatch(svg); m != nil && html.UnescapeString(m[1]) != "Chart" {
			title = html.UnescapeString(m[1])
		}
		// a panel, not a chart of its own (no Export PNG for the page)
		svg = strings.Replace(svg, ChartMarker, `data-specwright-panel="chart"`, 1)
		svg = regexp.MustCompile(`<svg\b([^>]*?) width="\d+" height="\d+"`).ReplaceAllString(svg, `<svg$1 width="100%"`)
		return panel{title: title, body: svg, kind: "chart"}
	case strings.Contains(t, TableMarker) && tableBlockRE.MatchString(t):
		block := tableBlockRE.FindString(t)
		title := ""
		if m := captionRE.FindStringSubmatch(block); m != nil {
			title = html.UnescapeString(strings.TrimSpace(m[1]))
		}
		return panel{title: title, body: block, kind: "table"}
	case strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{"):
		var v any
		if json.Unmarshal([]byte(t), &v) == nil {
			if rows, err := rowsArg(v); err == nil && len(rows) > 0 {
				s, err := parseTable(args{"rows": v, "max_rows": 200})
				if err == nil {
					return panel{body: TableHTML(s), kind: "rows"}
				}
			}
			b, _ := json.MarshalIndent(v, "", "  ")
			return panel{body: "<pre>" + html.EscapeString(string(b)) + "</pre>", kind: "text"}
		}
	case strings.HasPrefix(strings.ToLower(t), "<!doctype") || strings.HasPrefix(strings.ToLower(t), "<html") || strings.HasPrefix(t, "<"):
		body := t
		if m := bodyRE.FindStringSubmatch(t); m != nil {
			body = m[1]
		}
		body = scriptRE.ReplaceAllString(styleRE.ReplaceAllString(body, ""), "")
		return panel{body: `<div class="html">` + body + `</div>`, kind: "html"}
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(t), &buf); err != nil {
		return panel{body: "<pre>" + html.EscapeString(t) + "</pre>", kind: "text"}
	}
	return panel{body: `<div class="md">` + buf.String() + `</div>`, kind: "markdown"}
}

// DashboardSpec is what the dashboard tool lays out.
type DashboardSpec struct {
	Title   string
	Intro   string
	Columns int
	Panels  []panel
}

func parseDashboard(a args) (DashboardSpec, error) {
	d := DashboardSpec{Title: firstStr(a.str("title"), "Dashboard"), Intro: a.str("intro"), Columns: a.num("columns", 2, 1, 4)}
	titles := strList(a["titles"])
	if t, ok := a["titles"].(string); ok && !strings.HasPrefix(strings.TrimSpace(t), "[") {
		titles = nil
		for _, x := range strings.Split(t, "|") {
			titles = append(titles, strings.TrimSpace(x))
		}
	} else if ok {
		_ = json.Unmarshal([]byte(t), &titles)
	}
	wide := map[int]bool{}
	for _, n := range floatList(a["wide"]) {
		wide[int(n)] = true
	}
	for i := 1; i <= maxPanels; i++ {
		c := a.str("panel_" + strconv.Itoa(i))
		if strings.TrimSpace(c) == "" {
			continue
		}
		p := makePanel(c)
		if i-1 < len(titles) && titles[i-1] != "" {
			p.title = titles[i-1]
		}
		p.wide = wide[i] || p.kind == "table" && d.Columns > 1 && strings.Count(p.body, "<th") > 6
		d.Panels = append(d.Panels, p)
	}
	if len(d.Panels) == 0 {
		return d, fmt.Errorf("give at least one panel: panel_1 … panel_%d, each an earlier step's output (e.g. {{chart.buffer_output}})", maxPanels)
	}
	return d, nil
}

const dashboardCSS = `
*{box-sizing:border-box}
body{margin:0;background:#f6f8fa;color:#1f2328;font:14px/1.5 -apple-system,"Segoe UI",Helvetica,Arial,sans-serif}
header{padding:20px 24px 4px}
header h1{margin:0;font-size:22px}
header .meta{color:#57606a;font-size:12px;margin-top:2px}
header .intro{margin-top:8px;max-width:900px}
.grid{display:grid;grid-template-columns:repeat(var(--cols),minmax(0,1fr));gap:16px;padding:16px 24px 24px}
.panel{background:#fff;border:1px solid #d0d7de;border-radius:8px;padding:14px 16px;min-width:0;overflow:auto}
.panel.wide{grid-column:1/-1}
.panel h2{font-size:14px;margin:0 0 10px;color:#3a3f45}
.panel svg{display:block;height:auto;max-width:100%}
.panel pre{white-space:pre-wrap;font:12px/1.45 ui-monospace,SFMono-Regular,Menlo,monospace;margin:0}
.panel .md table{border-collapse:collapse}.panel .md td,.panel .md th{border:1px solid #d0d7de;padding:4px 8px}
@media (max-width:760px){.grid{grid-template-columns:1fr}}
`

// DashboardPage lays the panels out as one HTML page.
func DashboardPage(d DashboardSpec, made time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!doctype html>\n<html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title><style>%s%s</style></head>\n<body %s>\n",
		html.EscapeString(d.Title), dashboardCSS, TableCSS, DashboardMarker)
	fmt.Fprintf(&b, "<header><h1>%s</h1><div class=\"meta\">Made %s</div>", html.EscapeString(d.Title), made.Format("2006-01-02 15:04"))
	if strings.TrimSpace(d.Intro) != "" {
		var buf bytes.Buffer
		if md.Convert([]byte(d.Intro), &buf) == nil {
			b.WriteString(`<div class="intro">` + buf.String() + `</div>`)
		}
	}
	fmt.Fprintf(&b, "</header>\n<main class=\"grid\" style=\"--cols:%d\">\n", d.Columns)
	for _, p := range d.Panels {
		cls := "panel " + p.kind
		if p.wide {
			cls += " wide"
		}
		b.WriteString(`<section class="` + cls + `">`)
		// a chart or table shows its own title
		if p.title != "" && p.kind != "chart" && !(p.kind == "table" && strings.Contains(p.body, "<caption>")) {
			b.WriteString("<h2>" + html.EscapeString(p.title) + "</h2>")
		}
		b.WriteString(p.body + "</section>\n")
	}
	b.WriteString("</main>\n" + tableSortJS + "\n</body></html>\n")
	return b.String()
}

func dashboardTool(_ context.Context, b *Box, a args) (string, error) {
	d, err := parseDashboard(a)
	if err != nil {
		return "", err
	}
	page := DashboardPage(d, time.Now())
	p := strings.TrimSpace(a.str("path"))
	if p == "" {
		p = "out/dashboards/" + slugOf(d.Title, "dashboard") + "-" + time.Now().Format("20060102-150405") + ".html"
	}
	if !strings.HasSuffix(strings.ToLower(p), ".html") {
		p += ".html"
	}
	abs, _, err := b.resolve(p)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(page), 0o644); err != nil {
		return "", err
	}
	return page, nil
}

// PreviewDashboard lays the dashboard out without saving it.
func PreviewDashboard(raw map[string]any) (string, error) {
	d, err := parseDashboard(args(raw))
	if err != nil {
		return "", err
	}
	return DashboardPage(d, time.Now()), nil
}
