package builtin

// pdf.go writes blocks (export.go) as a PDF: A4 pages, the standard PDF
// fonts (Helvetica, its bold and italic, Courier for code and tables; no
// fonts are embedded), text wrapped by the fonts' own widths, page numbers.
// Characters the standard fonts cannot show (outside Western European
// text) become "?"; for those, export to HTML or Word instead.

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"time"
)

const (
	pdfW, pdfH   = 595.0, 842.0
	pdfML, pdfMR = 56.0, 56.0
	pdfMT, pdfMB = 64.0, 64.0
)

// widths of characters 32–126 in 1/1000 em (the fonts' published metrics)
var helvW = []int{278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556, 1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556, 333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556, 556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584}
var helvBW = []int{278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611, 975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556, 333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611, 611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584}

// highW: widths of the signs above 126 that are not 556 (Helvetica)
var highW = map[byte]int{0x85: 1000, 0x91: 222, 0x92: 222, 0x93: 333, 0x94: 333, 0x95: 350, 0x97: 1000, 0x99: 1000, 0xA0: 278, 0xA9: 737, 0xAE: 737, 0xB0: 400, 0xB7: 278}

// winAnsi maps a rune to the fonts' encoding (WinAnsiEncoding); 0 when it has none.
func winAnsi(r rune) byte {
	switch {
	case r >= 32 && r < 127, r >= 0xA0 && r <= 0xFF:
		return byte(r)
	}
	switch r {
	case '€':
		return 0x80
	case '…':
		return 0x85
	case '‘':
		return 0x91
	case '’':
		return 0x92
	case '“':
		return 0x93
	case '”':
		return 0x94
	case '•':
		return 0x95
	case '–':
		return 0x96
	case '—':
		return 0x97
	case '™':
		return 0x99
	case '\t':
		return ' '
	}
	return 0
}

func encodeAnsi(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		c := winAnsi(r)
		if c == 0 {
			c = '?'
		}
		out = append(out, c)
	}
	return out
}

// font: 1 regular, 2 bold, 3 italic, 4 bold italic, 5 Courier
func fontOf(s span) int {
	switch {
	case s.mono:
		return 5
	case s.bold && s.italic:
		return 4
	case s.bold:
		return 2
	case s.italic:
		return 3
	}
	return 1
}

func textWidth(b []byte, font int, size float64) float64 {
	if font == 5 {
		return float64(len(b)) * 600 * size / 1000
	}
	tbl := helvW
	if font == 2 || font == 4 {
		tbl = helvBW
	}
	bold := font == 2 || font == 4
	w := 0
	for _, c := range b {
		switch {
		case c >= 32 && c <= 126:
			w += tbl[c-32]
		case highW[c] != 0:
			w += highW[c]
			if bold && (c == 0x91 || c == 0x92 || c == 0x93 || c == 0x94) {
				w += 167 // the bold quotes are wider
			}
		default:
			w += 556
		}
	}
	return float64(w) * size / 1000
}

type pdfDoc struct {
	pages []*bytes.Buffer
	cur   *bytes.Buffer
	y     float64
}

func (d *pdfDoc) newPage() {
	d.cur = &bytes.Buffer{}
	d.pages = append(d.pages, d.cur)
	d.y = pdfH - pdfMT
}

// need starts a new page when h points do not fit.
func (d *pdfDoc) need(h float64) {
	if d.cur == nil || d.y-h < pdfMB {
		d.newPage()
	}
}

func pdfStr(b []byte) string {
	var s strings.Builder
	s.WriteByte('(')
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			s.WriteByte('\\')
			s.WriteByte(c)
		default:
			s.WriteByte(c)
		}
	}
	s.WriteByte(')')
	return s.String()
}

func (d *pdfDoc) text(x, y float64, font int, size float64, gray float64, b []byte) {
	fmt.Fprintf(d.cur, "BT %.2f g /F%d %.1f Tf %.2f %.2f Td %s Tj ET\n", gray, font, size, x, y, pdfStr(b))
}

func (d *pdfDoc) line(x1, y1, x2, y2, width, gray float64) {
	fmt.Fprintf(d.cur, "%.2f G %.2f w %.2f %.2f m %.2f %.2f l S\n", gray, width, x1, y1, x2, y2)
}

type word struct {
	b    []byte
	font int
	link string
	sp   bool // a space after it
}

// words splits spans into words that keep their font.
func words(ss []span) []word {
	var out []word
	for _, s := range ss {
		f := fontOf(s)
		t := s.text
		if s.link != "" && s.link != s.text && !strings.HasPrefix(s.link, "#") {
			t += " (" + s.link + ")"
		}
		for i, part := range strings.Split(t, "\n") {
			if i > 0 {
				out = append(out, word{b: nil}) // a line break
			}
			// a space where the style changes ("**bold** text") belongs to the word before
			if strings.TrimLeft(part, " \t") != part && len(out) > 0 && out[len(out)-1].b != nil {
				out[len(out)-1].sp = true
			}
			fields := strings.Fields(part)
			for j, w := range fields {
				sp := j < len(fields)-1 || strings.HasSuffix(part, " ")
				out = append(out, word{b: encodeAnsi(w), font: f, sp: sp})
			}
			if len(fields) == 0 && strings.Contains(part, " ") && len(out) > 0 {
				out[len(out)-1].sp = true
			}
		}
	}
	return out
}

// para lays out wrapped text at x with the given width.
func (d *pdfDoc) para(ws []word, x, width, size, gray float64, lead float64) {
	lh := size * lead
	var line []word
	lw := 0.0
	flush := func() {
		d.need(lh)
		d.y -= lh
		cx := x
		for _, w := range line {
			sz := size
			if w.font == 5 {
				sz = size * 0.92
			}
			d.text(cx, d.y, w.font, sz, gray, w.b)
			cx += textWidth(w.b, w.font, sz)
			if w.sp {
				cx += textWidth([]byte(" "), w.font, sz)
			}
		}
		line, lw = nil, 0
	}
	for _, w := range ws {
		if w.b == nil { // a hard line break
			flush()
			continue
		}
		ww := textWidth(w.b, w.font, size)
		sw := textWidth([]byte(" "), w.font, size)
		if lw > 0 && lw+ww > width {
			flush()
		}
		// a word longer than the line: cut it
		for ww > width && len(w.b) > 1 {
			n := len(w.b)
			for n > 1 && textWidth(w.b[:n], w.font, size) > width {
				n--
			}
			line = append(line, word{b: w.b[:n], font: w.font})
			flush()
			w.b = w.b[n:]
			ww = textWidth(w.b, w.font, size)
		}
		line = append(line, w)
		lw += ww
		if w.sp {
			lw += sw
		}
	}
	if len(line) > 0 {
		flush()
	}
}

func pdfFile(title string, blocks []block) ([]byte, error) {
	d := &pdfDoc{}
	d.newPage()
	width := pdfW - pdfML - pdfMR
	for i, bl := range blocks {
		switch bl.kind {
		case "h":
			size := map[int]float64{1: 20, 2: 16, 3: 13.5}[bl.level]
			if size == 0 {
				size = 12
			}
			gap := size * 0.9
			if i == 0 {
				gap = 0
			}
			d.need(gap + size*3) // keep a heading with what follows
			d.y -= gap
			ws := words(bl.spans)
			for k := range ws {
				if ws[k].font == 1 {
					ws[k].font = 2
				} else if ws[k].font == 3 {
					ws[k].font = 4
				}
			}
			d.para(ws, pdfML, width, size, 0.1, 1.25)
			if bl.level <= 2 {
				d.y -= 4
				d.line(pdfML, d.y, pdfW-pdfMR, d.y, 0.6, 0.8)
			}
			d.y -= 6
		case "p":
			x := pdfML + 18*float64(bl.level)
			d.para(words(bl.spans), x, width-(x-pdfML), 11, 0.12, 1.4)
			d.y -= 6
		case "quote":
			x := pdfML + 16 + 18*float64(bl.level)
			top := d.y
			d.para(words(bl.spans), x, width-(x-pdfML), 11, 0.35, 1.4)
			if d.y < top { // the bar beside it (on this page)
				d.line(x-10, min(top, pdfH-pdfMT)-2, x-10, d.y-3, 2.5, 0.8)
			}
			d.y -= 6
		case "li":
			x := pdfML + 18*float64(bl.level+1)
			if bl.marker != "" {
				d.need(11 * 1.4)
				mk := encodeAnsi(bl.marker)
				d.text(x-4-textWidth(mk, 1, 11), d.y-11*1.4, 1, 11, 0.12, mk)
			}
			d.para(words(bl.spans), x, width-(x-pdfML), 11, 0.12, 1.4)
			d.y -= 2
		case "code":
			x := pdfML + 18*float64(bl.level)
			size := 9.0
			maxc := int((width - (x - pdfML) - 12) / (0.6 * size))
			d.y -= 4
			for _, l := range bl.lines {
				b := encodeAnsi(strings.ReplaceAll(l, "\t", "    "))
				for {
					d.need(size * 1.35)
					d.y -= size * 1.35
					n := min(len(b), maxc)
					d.text(x+6, d.y, 5, size, 0.15, b[:n])
					if n >= len(b) {
						break
					}
					b = b[n:]
				}
			}
			d.y -= 8
		case "hr":
			d.need(18)
			d.y -= 9
			d.line(pdfML, d.y, pdfW-pdfMR, d.y, 0.6, 0.75)
			d.y -= 9
		case "table":
			d.pdfTable(bl.rows, width)
		}
	}
	return d.bytes(title)
}

// pdfTable sets a table in Courier, columns as wide as their content allows.
func (d *pdfDoc) pdfTable(rows [][]string, width float64) {
	if len(rows) == 0 {
		return
	}
	size := 9.0
	total := int(width / (0.6 * size))
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	ws := make([]int, cols)
	for _, r := range rows {
		for i, c := range r {
			ws[i] = max(ws[i], len([]rune(c)))
		}
	}
	// shrink the widest columns until the row fits (3 characters between columns)
	for {
		sum := 0
		for _, w := range ws {
			sum += w
		}
		if sum+3*(cols-1) <= total {
			break
		}
		wi := 0
		for i := range ws {
			if ws[i] > ws[wi] {
				wi = i
			}
		}
		if ws[wi] <= 4 {
			break
		}
		ws[wi]--
	}
	d.y -= 4
	for ri, r := range rows {
		var b strings.Builder
		for i := 0; i < cols; i++ {
			c := ""
			if i < len(r) {
				c = r[i]
			}
			rs := []rune(c)
			if len(rs) > ws[i] {
				rs = append(rs[:max(0, ws[i]-1)], '…')
			}
			b.WriteString(string(rs) + strings.Repeat(" ", ws[i]-len(rs)))
			if i < cols-1 {
				b.WriteString(" │ ")
			}
		}
		d.need(size * 1.5)
		d.y -= size * 1.5
		font := 5
		gray := 0.15
		line := strings.ReplaceAll(b.String(), "│", "|")
		d.text(pdfML, d.y, font, size, gray, encodeAnsi(line))
		if ri == 0 {
			d.line(pdfML, d.y-3, pdfML+width, d.y-3, 0.5, 0.6)
		}
	}
	d.y -= 10
}

func (d *pdfDoc) bytes(title string) ([]byte, error) {
	var out bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(d.pages)
	// 1 catalog, 2 pages, 3–7 fonts, 8 info, then a page and its content per page
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := 0; i < n; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 9+2*i))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", n, strings.Join(kids, " ")))
	for _, f := range []string{"Helvetica", "Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique", "Courier"} {
		obj("<< /Type /Font /Subtype /Type1 /BaseFont /" + f + " /Encoding /WinAnsiEncoding >>")
	}
	obj(fmt.Sprintf("<< /Title %s /Producer (SpecWright) /CreationDate (D:%s) >>", pdfStr(encodeAnsi(title)), time.Now().UTC().Format("20060102150405Z")))
	fonts := "<< /F1 3 0 R /F2 4 0 R /F3 5 0 R /F4 6 0 R /F5 7 0 R >>"
	for i, p := range d.pages {
		num := encodeAnsi(fmt.Sprintf("%d", i+1))
		fmt.Fprintf(p, "BT 0.5 g /F1 9 Tf %.2f %.2f Td %s Tj ET\n", pdfW/2-textWidth(num, 1, 9)/2, pdfMB/2, pdfStr(num))
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		_, _ = zw.Write(p.Bytes())
		_ = zw.Close()
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font %s >> /Contents %d 0 R >>", pdfW, pdfH, fonts, 10+2*i))
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", len(offs), z.Len())
		out.Write(z.Bytes())
		out.WriteString("\nendstream\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 8 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes(), nil
}
