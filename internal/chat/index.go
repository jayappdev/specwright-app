package chat

// The session index (docs/design/2026-11-24-chatflows.md, "Data catalog
// and search"): a session's data in one SQLite file, index.db, next to it.
// Tabular data (CSV, TSV, JSON rows, Markdown tables) becomes tables the
// chat queries with SQL; text (documents, pages) is split into passages in
// an FTS5 full-text index the chat searches. The AI then gets a catalog of
// the large data (columns, row counts, a few rows) instead of all of it.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the "sqlite" database/sql driver (with FTS5)
)

// IndexFile is the index's name in a session's folder.
const IndexFile = "index.db"

// Column is a table's column and its SQLite type.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Entry is what the index holds for one data item.
type Entry struct {
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Kind     string   `json:"kind"` // "table" or "text"
	Table    string   `json:"table,omitempty"`
	Columns  []Column `json:"columns,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Passages int      `json:"passages,omitempty"`
	Bytes    int      `json:"bytes"`
	stamp    string
}

// Passage is a piece of a document that a search found.
type Passage struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Heading string `json:"heading,omitempty"`
	Text    string `json:"text"`
}

// Index is an open session index.
type Index struct{ db *sql.DB }

// OpenIndex opens (or creates) a session's index.
func (s *Store) OpenIndex(flow, id string) (*Index, error) {
	d, err := s.dir(flow, id)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("name the session")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(d, IndexFile)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS sw_catalog (name TEXT PRIMARY KEY, title TEXT, kind TEXT, tbl TEXT, columns TEXT, rows INTEGER, passages INTEGER, bytes INTEGER, stamp TEXT)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS sw_passages USING fts5(name UNINDEXED, title UNINDEXED, heading, body, tokenize='porter unicode61')`,
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("the session index: %v", err)
		}
	}
	return &Index{db: db}, nil
}

// Close closes the index.
func (x *Index) Close() error { return x.db.Close() }

// Indexed reports whether a data item belongs in the index: not the
// chat's own query and search results (they come from it).
func Indexed(d Data) bool { return !strings.HasPrefix(d.Step, "data.") }

func stampOf(d Data) string { return d.At.UTC().Format(time.RFC3339Nano) + "/" + strconv.Itoa(d.Bytes) }

// Sync brings the index up to date with a session's data: new and changed
// items are added, removed ones dropped. content reads an item (as text:
// a page already turned from HTML into text).
func (x *Index) Sync(items []Data, content func(Data) (string, error)) error {
	have, err := x.Catalog()
	if err != nil {
		return err
	}
	old := map[string]Entry{}
	for _, e := range have {
		old[e.Name] = e
	}
	want := map[string]bool{}
	var errs []string
	for _, d := range items {
		if !Indexed(d) {
			continue
		}
		want[d.Name] = true
		if e, ok := old[d.Name]; ok && e.stamp == stampOf(d) {
			continue
		}
		c, err := content(d)
		if err == nil {
			_, err = x.Add(d, c)
		}
		if err != nil {
			errs = append(errs, d.Name+": "+err.Error())
		}
	}
	for name := range old {
		if !want[name] {
			if err := x.remove(name); err != nil {
				errs = append(errs, name+": "+err.Error())
			}
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (x *Index) remove(name string) error {
	var tbl string
	_ = x.db.QueryRow(`SELECT tbl FROM sw_catalog WHERE name = ?`, name).Scan(&tbl)
	if tbl != "" {
		if _, err := x.db.Exec(`DROP TABLE IF EXISTS ` + quoteIdent(tbl)); err != nil {
			return err
		}
	}
	if _, err := x.db.Exec(`DELETE FROM sw_passages WHERE name = ?`, name); err != nil {
		return err
	}
	_, err := x.db.Exec(`DELETE FROM sw_catalog WHERE name = ?`, name)
	return err
}

// Add indexes one data item (replacing what the index had for it).
func (x *Index) Add(d Data, content string) (Entry, error) {
	if err := x.remove(d.Name); err != nil {
		return Entry{}, err
	}
	e := Entry{Name: d.Name, Title: firstNonEmpty(d.Title, d.Name), Bytes: len(content), stamp: stampOf(d)}
	cols, rows, isTable := ParseTable(content)
	if isTable { // (named before the transaction: the index has one connection)
		e.Table = x.tableName(d.Name)
	}
	tx, err := x.db.Begin()
	if err != nil {
		return e, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if isTable {
		e.Kind, e.Rows = "table", len(rows)
		e.Columns = columnTypes(cols, rows)
		defs := make([]string, len(e.Columns))
		for i, c := range e.Columns {
			defs[i] = quoteIdent(c.Name) + " " + c.Type
		}
		if _, err := tx.Exec(`CREATE TABLE ` + quoteIdent(e.Table) + ` (` + strings.Join(defs, ", ") + `)`); err != nil {
			return e, err
		}
		ins, err := tx.Prepare(`INSERT INTO ` + quoteIdent(e.Table) + ` VALUES (` + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + `)`)
		if err != nil {
			return e, err
		}
		vals := make([]any, len(cols))
		for _, r := range rows {
			for i, c := range e.Columns {
				vals[i] = typed(r[i], c.Type)
			}
			if _, err := ins.Exec(vals...); err != nil {
				ins.Close()
				return e, err
			}
		}
		ins.Close()
	} else {
		e.Kind = "text"
		for _, p := range SplitPassages(content) {
			if _, err := tx.Exec(`INSERT INTO sw_passages (name, title, heading, body) VALUES (?, ?, ?, ?)`, d.Name, e.Title, p.Heading, p.Text); err != nil {
				return e, err
			}
			e.Passages++
		}
	}
	cj, _ := json.Marshal(e.Columns)
	if _, err := tx.Exec(`INSERT INTO sw_catalog VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.Name, e.Title, e.Kind, e.Table, string(cj), e.Rows, e.Passages, e.Bytes, e.stamp); err != nil {
		return e, err
	}
	return e, tx.Commit()
}

// Catalog lists what the index holds.
func (x *Index) Catalog() ([]Entry, error) {
	rows, err := x.db.Query(`SELECT name, title, kind, tbl, columns, rows, passages, bytes, stamp FROM sw_catalog ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var cj string
		if err := rows.Scan(&e.Name, &e.Title, &e.Kind, &e.Table, &cj, &e.Rows, &e.Passages, &e.Bytes, &e.stamp); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(cj), &e.Columns)
		out = append(out, e)
	}
	return out, rows.Err()
}

// tableName is a data item's table: its name as an SQL name, not one
// another item has.
func (x *Index) tableName(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base = strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(base), "_"), "_")
	if base == "" || (base[0] >= '0' && base[0] <= '9') || strings.HasPrefix(base, "sw_") || strings.HasPrefix(base, "sqlite") {
		base = "t_" + base
	}
	t := base
	for i := 2; ; i++ {
		var n int
		_ = x.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE lower(name) = ?`, t).Scan(&n)
		if n == 0 {
			return t
		}
		t = fmt.Sprintf("%s_%d", base, i)
	}
}

var selectRE = regexp.MustCompile(`(?is)^\s*(select|with)\b`)

// Query runs one read-only SQL query (SELECT or WITH) and gives up to max
// rows, as text; more reports that there were more.
func (x *Index) Query(ctx context.Context, q string, max int) (cols []string, out [][]string, more bool, err error) {
	q = strings.TrimSpace(q)
	for strings.HasSuffix(q, ";") {
		q = strings.TrimSpace(strings.TrimSuffix(q, ";"))
	}
	if !selectRE.MatchString(q) {
		return nil, nil, false, errors.New("only a SELECT (or WITH … SELECT) query can run")
	}
	if strings.Contains(stripSQLStrings(q), ";") {
		return nil, nil, false, errors.New("run one query at a time")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := x.db.Conn(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		return nil, nil, false, err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA query_only = OFF`) //nolint:errcheck // best effort on a closing conn
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	if cols, err = rows.Columns(); err != nil {
		return nil, nil, false, err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if len(out) >= max {
			more = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		r := make([]string, len(cols))
		for i, v := range vals {
			r[i] = cellText(v)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return cols, out, more, nil
}

func cellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case time.Time:
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}

var sqlStringRE = regexp.MustCompile(`'(?:[^']|'')*'|"(?:[^"]|"")*"`)

func stripSQLStrings(q string) string { return sqlStringRE.ReplaceAllString(q, "''") }

var wordRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

var stopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an and are as at be by can do does for from has have how i in is it its me my of on or our show tell than that the their them there these this to us was we what when where which who why will with you your") {
		stopWords[w] = true
	}
}

// Search finds the k passages that best match the words of q (BM25; a
// match in a heading counts double; words are stemmed: "sales" finds
// "sale").
func (x *Index) Search(q string, k int) ([]Passage, error) {
	seen := map[string]bool{}
	var terms []string
	for _, w := range wordRE.FindAllString(strings.ToLower(q), -1) {
		if stopWords[w] || seen[w] || (len(w) < 2 && !strings.ContainsAny(w, "0123456789")) {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"`)
		if len(terms) == 16 {
			break
		}
	}
	if len(terms) == 0 {
		return nil, nil
	}
	rows, err := x.db.Query(`SELECT name, title, heading, body FROM sw_passages WHERE sw_passages MATCH ? ORDER BY bm25(sw_passages, 0, 0, 2.0, 1.0) LIMIT ?`, strings.Join(terms, " OR "), k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passage
	for rows.Next() {
		var p Passage
		if err := rows.Scan(&p.Name, &p.Title, &p.Heading, &p.Text); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- reading tables ----

// ParseTable reads content as a table: a JSON array of objects (or an
// object holding one), JSON lines, a Markdown table, or CSV/TSV (or ; or |
// separated). It gives the columns and the rows (every row as long as the
// columns).
func ParseTable(content string) (cols []string, rows [][]string, ok bool) {
	t := strings.TrimSpace(content)
	if t == "" {
		return nil, nil, false
	}
	if t[0] == '[' || t[0] == '{' {
		if cols, rows, ok = jsonTable(t); ok {
			return cols, rows, true
		}
	}
	if t[0] == '|' {
		if cols, rows, ok = markdownTable(t); ok {
			return cols, rows, true
		}
	}
	return delimitedTable(t)
}

func jsonTable(t string) ([]string, [][]string, bool) {
	var objs []json.RawMessage
	if json.Valid([]byte(t)) {
		var v json.RawMessage = []byte(t)
		objs = objectList(v, 0)
	} else { // JSON lines
		for _, l := range strings.Split(t, "\n") {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if !strings.HasPrefix(l, "{") || !json.Valid([]byte(l)) {
				return nil, nil, false
			}
			objs = append(objs, json.RawMessage(l))
		}
	}
	if len(objs) == 0 {
		return nil, nil, false
	}
	var cols []string
	idx := map[string]int{}
	var recs []map[string]any
	for _, o := range objs {
		keys, vals, ok := orderedObject(o)
		if !ok {
			return nil, nil, false
		}
		for _, k := range keys {
			if _, ok := idx[k]; !ok {
				idx[k] = len(cols)
				cols = append(cols, k)
			}
		}
		recs = append(recs, vals)
	}
	if len(cols) == 0 {
		return nil, nil, false
	}
	rows := make([][]string, len(recs))
	for i, r := range recs {
		row := make([]string, len(cols))
		for k, v := range r {
			row[idx[k]] = jsonCell(v)
		}
		rows[i] = row
	}
	return cols, rows, true
}

// objectList: the array of objects v is, or the largest one it holds (one
// level down: {"rows": [...]}).
func objectList(v json.RawMessage, depth int) []json.RawMessage {
	var arr []json.RawMessage
	if json.Unmarshal(v, &arr) == nil {
		for _, x := range arr {
			if t := bytes.TrimSpace(x); len(t) == 0 || t[0] != '{' {
				return nil
			}
		}
		return arr
	}
	var obj map[string]json.RawMessage
	if depth > 0 || json.Unmarshal(v, &obj) != nil {
		return nil
	}
	var best []json.RawMessage
	for _, x := range obj {
		if l := objectList(x, 1); len(l) > len(best) {
			best = l
		}
	}
	return best
}

// orderedObject decodes an object keeping its keys' order.
func orderedObject(raw json.RawMessage) ([]string, map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, false
	}
	var keys []string
	vals := map[string]any{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, false
		}
		k, _ := tok.(string)
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, nil, false
		}
		if _, dup := vals[k]; !dup {
			keys = append(keys, k)
		}
		vals[k] = v
	}
	return keys, vals, true
}

func jsonCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

var mdSepRE = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$`)

func markdownTable(t string) ([]string, [][]string, bool) {
	lines := strings.Split(t, "\n")
	if len(lines) < 3 || !mdSepRE.MatchString(strings.TrimSpace(lines[1])) {
		return nil, nil, false
	}
	cells := func(l string) []string {
		l = strings.TrimSpace(l)
		l = strings.TrimSuffix(strings.TrimPrefix(l, "|"), "|")
		l = strings.ReplaceAll(l, `\|`, "\x00")
		parts := strings.Split(l, "|")
		for i, p := range parts {
			p = strings.ReplaceAll(strings.TrimSpace(p), "\x00", "|")
			parts[i] = strings.ReplaceAll(p, "<br>", "\n")
		}
		return parts
	}
	cols := cells(lines[0])
	var rows [][]string
	for _, l := range lines[2:] {
		if !strings.HasPrefix(strings.TrimSpace(l), "|") {
			break // the table ended (text after it)
		}
		rows = append(rows, fit(cells(l), len(cols)))
	}
	if len(rows) == 0 {
		return nil, nil, false
	}
	return uniqueCols(cols), rows, true
}

// delimitedTable reads CSV-like text: the separator that gives the same
// number of fields (2 or more) on nearly every line, with a header of
// short names.
func delimitedTable(t string) ([]string, [][]string, bool) {
	var bestCols []string
	var bestRows [][]string
	for _, sep := range []rune{',', '\t', ';', '|'} {
		first, _, _ := strings.Cut(t, "\n")
		if !strings.ContainsRune(first, sep) {
			continue
		}
		r := csv.NewReader(strings.NewReader(t))
		r.Comma, r.LazyQuotes, r.FieldsPerRecord = sep, true, -1
		recs, err := r.ReadAll()
		if err != nil || len(recs) < 2 || len(recs[0]) < 2 {
			continue
		}
		head := recs[0]
		okHead := true
		for _, h := range head {
			if len(h) > 80 || strings.Count(strings.TrimSpace(h), " ") > 5 {
				okHead = false
			}
		}
		same := 0
		for _, rec := range recs[1:] {
			if len(rec) == len(head) {
				same++
			}
		}
		if !okHead || float64(same) < 0.9*float64(len(recs)-1) || len(head) <= len(bestCols) {
			continue
		}
		rows := make([][]string, 0, len(recs)-1)
		for _, rec := range recs[1:] {
			if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
				continue
			}
			rows = append(rows, fit(rec, len(head)))
		}
		bestCols, bestRows = head, rows
	}
	if bestCols == nil {
		return nil, nil, false
	}
	return uniqueCols(bestCols), bestRows, true
}

func fit(r []string, n int) []string {
	if len(r) >= n {
		return r[:n]
	}
	return append(r, make([]string, n-len(r))...)
}

// uniqueCols names empty columns and tells repeated names apart.
func uniqueCols(cols []string) []string {
	out := make([]string, len(cols))
	seen := map[string]int{}
	for i, c := range cols {
		c = strings.TrimSpace(strings.TrimPrefix(c, "\ufeff"))
		if c == "" {
			c = fmt.Sprintf("col%d", i+1)
		}
		base := c
		for seen[strings.ToLower(c)] > 0 {
			seen[strings.ToLower(base)]++
			c = fmt.Sprintf("%s_%d", base, seen[strings.ToLower(base)])
		}
		seen[strings.ToLower(c)]++
		out[i] = c
	}
	return out
}

// columnTypes: INTEGER or REAL when every value in a column is that
// number (empty values aside), else TEXT.
func columnTypes(cols []string, rows [][]string) []Column {
	out := make([]Column, len(cols))
	for i, c := range cols {
		ints, reals, some := true, true, false
		for _, r := range rows {
			v := strings.TrimSpace(r[i])
			if v == "" {
				continue
			}
			some = true
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				ints = false
				if _, err := strconv.ParseFloat(v, 64); err != nil {
					reals = false
					break
				}
			}
		}
		typ := "TEXT"
		switch {
		case some && ints:
			typ = "INTEGER"
		case some && reals:
			typ = "REAL"
		}
		out[i] = Column{Name: c, Type: typ}
	}
	return out
}

func typed(v, typ string) any {
	t := strings.TrimSpace(v)
	switch {
	case t == "" && typ != "TEXT":
		return nil
	case typ == "INTEGER":
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case typ == "REAL":
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return v
}

// quoteIdent quotes an SQL name.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// QuoteIdent quotes a column or table name when SQL needs it.
func QuoteIdent(s string) string {
	if regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(s) {
		return s
	}
	return quoteIdent(s)
}

// ---- splitting text ----

// Section is a passage of a document, under its heading.
type Section struct {
	Heading string
	Text    string
}

const passageSize = 1200

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// SplitPassages cuts text into passages of about passageSize characters,
// along paragraphs, each with the Markdown heading it is under.
func SplitPassages(text string) []Section {
	var out []Section
	type head struct {
		level int
		text  string
	}
	var heads []head
	heading := func() string {
		parts := make([]string, len(heads))
		for i, h := range heads {
			parts[i] = h.text
		}
		return strings.Join(parts, " › ")
	}
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, Section{Heading: heading(), Text: t})
		}
		cur.Reset()
	}
	add := func(p string) {
		for len(p) > passageSize { // a long paragraph: cut at a sentence end or a space
			cut := strings.LastIndexAny(p[:passageSize], ".!?\n")
			if cut < passageSize/2 {
				cut = strings.LastIndex(p[:passageSize], " ")
			}
			if cut < passageSize/2 {
				cut = passageSize - 1
			}
			flush()
			cur.WriteString(p[:cut+1])
			flush()
			p = strings.TrimSpace(p[cut+1:])
		}
		if cur.Len() > 0 && cur.Len()+len(p) > passageSize {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	for _, para := range regexp.MustCompile(`\n\s*\n`).Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		// a heading starts a new passage (and may open the paragraph)
		first, rest, _ := strings.Cut(para, "\n")
		if m := headingRE.FindStringSubmatch(first); m != nil {
			flush()
			level := len(m[1])
			for len(heads) > 0 && heads[len(heads)-1].level >= level {
				heads = heads[:len(heads)-1]
			}
			heads = append(heads, head{level, m[2]})
			if para = strings.TrimSpace(rest); para == "" {
				continue
			}
		}
		add(para)
	}
	flush()
	return out
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}
