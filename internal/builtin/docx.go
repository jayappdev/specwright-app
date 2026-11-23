package builtin

// docx.go writes blocks (export.go) as a Word document: the smallest set of
// parts Word, Pages, LibreOffice and Google Docs open — content types, the
// document, its styles (Title, Heading 1–6, Code, Quote) and the
// relationships its links need.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

func xmlEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\t' || r == '\n' || r >= 0x20 && r != 0xFFFE && r != 0xFFFF:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func docxFile(title string, blocks []block) ([]byte, error) {
	var links []string
	var body strings.Builder
	runs := func(ss []span, base string) string {
		var b strings.Builder
		for _, s := range ss {
			if s.text == "" {
				continue
			}
			props := base
			if s.bold {
				props += "<w:b/>"
			}
			if s.italic {
				props += "<w:i/>"
			}
			if s.strike {
				props += "<w:strike/>"
			}
			if s.mono {
				props += `<w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`
			}
			var r strings.Builder
			for i, part := range strings.Split(s.text, "\n") {
				if i > 0 {
					r.WriteString("<w:br/>")
				}
				r.WriteString(`<w:t xml:space="preserve">` + xmlEsc(part) + `</w:t>`)
			}
			run := ""
			if s.link != "" {
				props += `<w:rStyle w:val="Hyperlink"/>`
				run = "<w:r><w:rPr>" + props + "</w:rPr>" + r.String() + "</w:r>"
				links = append(links, s.link)
				fmt.Fprintf(&b, `<w:hyperlink r:id="rLink%d" w:history="1">%s</w:hyperlink>`, len(links), run)
				continue
			}
			if props != "" {
				run = "<w:r><w:rPr>" + props + "</w:rPr>" + r.String() + "</w:r>"
			} else {
				run = "<w:r>" + r.String() + "</w:r>"
			}
			b.WriteString(run)
		}
		return b.String()
	}
	para := func(style, ppr, content string) {
		body.WriteString("<w:p><w:pPr>")
		if style != "" {
			body.WriteString(`<w:pStyle w:val="` + style + `"/>`)
		}
		body.WriteString(ppr + "</w:pPr>" + content + "</w:p>")
	}
	for _, bl := range blocks {
		switch bl.kind {
		case "h":
			para(fmt.Sprintf("Heading%d", min(max(bl.level, 1), 6)), "", runs(bl.spans, ""))
		case "p":
			para("", indentPr(bl.level), runs(bl.spans, ""))
		case "quote":
			para("Quote", indentPr(bl.level), runs(bl.spans, ""))
		case "li":
			left := 360 * (bl.level + 1)
			ppr := fmt.Sprintf(`<w:spacing w:after="60"/><w:ind w:left="%d" w:hanging="360"/>`, left+360)
			marker := ""
			if bl.marker != "" {
				marker = `<w:r><w:t xml:space="preserve">` + xmlEsc(bl.marker) + "\t</w:t></w:r>"
			} else {
				marker = `<w:r><w:tab/></w:r>`
			}
			para("", ppr, marker+runs(bl.spans, ""))
		case "code":
			for _, l := range bl.lines {
				para("Code", indentPr(bl.level), `<w:r><w:t xml:space="preserve">`+xmlEsc(l)+`</w:t></w:r>`)
			}
			para("", `<w:spacing w:after="0"/>`, "")
		case "hr":
			para("", `<w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="BFBFBF"/></w:pBdr>`, "")
		case "table":
			body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/><w:tblBorders>`)
			for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
				body.WriteString(`<w:` + side + ` w:val="single" w:sz="4" w:space="0" w:color="BFBFBF"/>`)
			}
			body.WriteString(`</w:tblBorders><w:tblCellMar><w:left w:w="100" w:type="dxa"/><w:right w:w="100" w:type="dxa"/></w:tblCellMar></w:tblPr>`)
			for i, row := range bl.rows {
				body.WriteString("<w:tr>")
				for _, cell := range row {
					shade := ""
					rpr := ""
					if i == 0 {
						shade = `<w:shd w:val="clear" w:color="auto" w:fill="F2F2F2"/>`
						rpr = "<w:rPr><w:b/></w:rPr>"
					}
					body.WriteString(`<w:tc><w:tcPr>` + shade + `</w:tcPr><w:p><w:pPr><w:spacing w:after="0"/></w:pPr><w:r>` + rpr + `<w:t xml:space="preserve">` + xmlEsc(cell) + `</w:t></w:r></w:p></w:tc>`)
				}
				body.WriteString("</w:tr>")
			}
			body.WriteString("</w:tbl>")
			para("", `<w:spacing w:after="0"/>`, "")
		}
	}
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body>` +
		body.String() + `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`
	var rels strings.Builder
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`)
	for i, l := range links {
		fmt.Fprintf(&rels, `<Relationship Id="rLink%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="%s" TargetMode="External"/>`, i+1, xmlEsc(l))
	}
	rels.WriteString(`</Relationships>`)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	parts := []struct{ name, data string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rDoc" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rCore" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
		{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>` + xmlEsc(title) + `</dc:title><dc:creator>SpecWright</dc:creator><dcterms:created xsi:type="dcterms:W3CDTF">` + now + `</dcterms:created></cp:coreProperties>`},
		{"word/document.xml", document},
		{"word/_rels/document.xml.rels", rels.String()},
		{"word/styles.xml", docxStyles},
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := z.Create(p.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(p.data)); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func indentPr(depth int) string {
	if depth <= 0 {
		return ""
	}
	return fmt.Sprintf(`<w:ind w:left="%d"/>`, 360*(depth+1))
}

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:cs="Calibri" w:eastAsia="Calibri"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="140" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="360" w:after="120"/><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="36"/><w:color w:val="1F2328"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="300" w:after="100"/><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/><w:sz w:val="30"/><w:color w:val="1F2328"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/><w:outlineLvl w:val="2"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:outlineLvl w:val="3"/></w:pPr><w:rPr><w:b/><w:sz w:val="24"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading5"><w:name w:val="heading 5"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:outlineLvl w:val="4"/></w:pPr><w:rPr><w:b/><w:sz w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading6"><w:name w:val="heading 6"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/><w:pPr><w:keepNext/><w:outlineLvl w:val="5"/></w:pPr><w:rPr><w:b/><w:i/><w:sz w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/><w:shd w:val="clear" w:color="auto" w:fill="F6F8FA"/></w:pPr><w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:sz w:val="19"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="567"/><w:pBdr><w:left w:val="single" w:sz="18" w:space="8" w:color="D0D7DE"/></w:pBdr></w:pPr><w:rPr><w:color w:val="59636E"/></w:rPr></w:style>
<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:rPr><w:color w:val="0969DA"/><w:u w:val="single"/></w:rPr></w:style>
<w:style w:type="table" w:styleId="TableGrid"><w:name w:val="Table Grid"/><w:tblPr><w:tblBorders><w:top w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:left w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:bottom w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:right w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideH w:val="single" w:sz="4" w:space="0" w:color="auto"/><w:insideV w:val="single" w:sz="4" w:space="0" w:color="auto"/></w:tblBorders></w:tblPr></w:style>
</w:styles>`
