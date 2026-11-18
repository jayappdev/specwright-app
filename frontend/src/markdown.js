// Markdown → HTML for showing results (run outputs, previews, files): the
// GitHub flavour AI agents and tools write — headings, paragraphs with line
// breaks, nested and task lists, tables with alignment, fenced and indented
// code, quotes, rules, links, images, bold, italic, strikethrough, inline
// code. Raw HTML is shown as text (only <br> is kept, which tables use), so
// nothing in a result can run. The rich-text editor keeps its own, smaller
// converter (richtext.js) because it must turn its HTML back into Markdown.

const esc = (s) =>
  String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
const safeUrl = (u) => {
  u = String(u || "").trim();
  return /^\s*(javascript|vbscript|data):/i.test(u) && !/^data:image\//i.test(u) ? "#" : u;
};

// --- inline ------------------------------------------------------------------------

export function mdInline(src) {
  const keep = []; // finished HTML, kept away from later rules
  const put = (html) => `\u0000${keep.push(html) - 1}\u0000`;
  let s = String(src);
  // code spans (any number of backticks)
  s = s.replace(/(`+)([\s\S]*?[^`])\1(?!`)/g, (_, _t, c) =>
    put(`<code>${esc(c.replace(/^ (.*) $/, "$1"))}</code>`),
  );
  // backslash escapes
  s = s.replace(/\\([\\`*_{}[\]()#+\-.!|~<>])/g, (_, c) => put(esc(c)));
  // <br> (tables use it), autolinks <https://…>
  s = s.replace(/<br\s*\/?>/gi, () => put("<br>"));
  s = s.replace(/<((?:https?|mailto):[^>\s]+)>/g, (_, u) =>
    put(`<a href="${esc(safeUrl(u))}">${esc(u)}</a>`),
  );
  // images and links: [text](url "title")
  s = s.replace(/!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"([^"]*)")?\s*\)/g, (_, a, u, t) =>
    put(`<img src="${esc(safeUrl(u))}" alt="${esc(a)}"${t ? ` title="${esc(t)}"` : ""}>`),
  );
  s = s.replace(/\[([^\]]+)\]\(\s*<?([^)\s>]+)>?(?:\s+"([^"]*)")?\s*\)/g, (_, txt, u, t) =>
    put(`<a href="${esc(safeUrl(u))}"${t ? ` title="${esc(t)}"` : ""}>${mdInline(txt)}</a>`),
  );
  // bare URLs
  s = s.replace(
    /(^|[\s(])((?:https?:\/\/|www\.)[^\s<]*[^\s<.,:;"')\]])/g,
    (_, p, u) =>
      p + put(`<a href="${esc(safeUrl(u.startsWith("www.") ? "https://" + u : u))}">${esc(u)}</a>`),
  );
  s = esc(s);
  // emphasis: ***, **, __, *, _, ~~
  s = s
    .replace(/\*\*\*(?=\S)([\s\S]*?\S)\*\*\*/g, "<strong><em>$1</em></strong>")
    .replace(/\*\*(?=\S)([\s\S]*?\S)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^\w])__(?=\S)([\s\S]*?\S)__(?!\w)/g, "$1<strong>$2</strong>")
    .replace(/(^|[^*\w])\*(?=[^\s*])([\s\S]*?[^\s*])\*(?!\*)/g, "$1<em>$2</em>")
    .replace(/(^|[^\w])_(?=[^\s_])([\s\S]*?[^\s_])_(?!\w)/g, "$1<em>$2</em>")
    .replace(/~~(?=\S)([\s\S]*?\S)~~/g, "<del>$1</del>");
  // eslint-disable-next-line no-control-regex -- NUL marks placeholders that cannot occur in text
  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => keep[+i]);
}

// a paragraph's lines: soft breaks become spaces, hard breaks (two spaces or
// a backslash at the end) become <br>
const paraHtml = (lines) =>
  lines
    .map((l, k) => {
      const last = k === lines.length - 1,
        hard = !last && (/ {2,}$/.test(l) || /\\$/.test(l));
      return mdInline(l.replace(/\\$/, "").trim()) + (last ? "" : hard ? "<br>" : "\n");
    })
    .join("");

// --- blocks ------------------------------------------------------------------------

const FENCE = /^ {0,3}(`{3,}|~{3,})\s*([^`\s]*)[^`]*$/;
const ATX = /^ {0,3}(#{1,6})(?:\s+(.*?))?(?:\s+#+)?\s*$/;
const HR = /^ {0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^ {0,3}>\s?/;
const LI = /^( {0,3})([-*+]|\d{1,9}[.)])(\s+|$)(.*)$/;
const DELIM = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;

function splitRow(line) {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells = [];
  let cur = "",
    code = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
      continue;
    }
    if (c === "`") code = !code;
    if (c === "|" && !code) {
      cells.push(cur.trim());
      cur = "";
      continue;
    }
    cur += c;
  }
  cells.push(cur.trim());
  return cells;
}

const isTableStart = (lines, i) =>
  lines[i].includes("|") &&
  i + 1 < lines.length &&
  DELIM.test(lines[i + 1]) &&
  lines[i + 1].includes("-") &&
  (lines[i + 1].includes("|") || splitRow(lines[i]).length === 1) &&
  splitRow(lines[i + 1]).length === splitRow(lines[i]).length;

// does a line start a block (and so end a paragraph)?
const startsBlock = (lines, i) => {
  const l = lines[i];
  if (FENCE.test(l) || ATX.test(l) || HR.test(l) || QUOTE.test(l) || isTableStart(lines, i)) return true;
  const m = l.match(LI);
  return !!m && m[4].trim() !== "" && (!/\d/.test(m[2]) || /^1[.)]$/.test(m[2]));
};

const indentOf = (l) => {
  let n = 0;
  for (const c of l) {
    if (c === " ") n++;
    else if (c === "\t") n += 4 - (n % 4);
    else break;
  }
  return n;
};
const dedent = (l, n) => {
  let i = 0,
    w = 0;
  while (i < l.length && w < n && (l[i] === " " || l[i] === "\t")) {
    w += l[i] === "\t" ? 4 - (w % 4) : 1;
    i++;
  }
  return l.slice(i);
};

function blocks(lines) {
  const out = [];
  for (let i = 0; i < lines.length; ) {
    const l = lines[i];
    if (!l.trim()) {
      i++;
      continue;
    }
    // fenced code
    let m = l.match(FENCE);
    if (m) {
      const fence = m[1],
        lang = m[2],
        body = [],
        ind = indentOf(l);
      for (
        i++;
        i < lines.length &&
        !new RegExp(`^ {0,3}${fence[0] === "`" ? "`" : "~"}{${fence.length},}\\s*$`).test(lines[i]);
        i++
      )
        body.push(dedent(lines[i], ind));
      i++;
      out.push(
        `<pre><code${lang ? ` class="language-${esc(lang)}"` : ""}>${esc(body.join("\n"))}</code></pre>`,
      );
      continue;
    }
    // headings
    m = l.match(ATX);
    if (m) {
      out.push(`<h${m[1].length}>${mdInline(m[2] || "")}</h${m[1].length}>`);
      i++;
      continue;
    }
    // rule (before lists: "- - -" and "* * *")
    if (HR.test(l)) {
      out.push("<hr>");
      i++;
      continue;
    }
    // table
    if (isTableStart(lines, i)) {
      const head = splitRow(l),
        align = splitRow(lines[i + 1]).map((c) =>
          /^:-+:$/.test(c) ? "center" : /-+:$/.test(c) ? "right" : /^:-+/.test(c) ? "left" : "",
        );
      const cell = (tag, c, k) =>
        `<${tag}${align[k] ? ` style="text-align:${align[k]}"` : ""}>${mdInline(c)}</${tag}>`;
      const rows = [];
      for (i += 2; i < lines.length; i++) {
        // a blank line between rows (a table joined from parts): the table goes on
        if (
          !lines[i].trim() &&
          i + 1 < lines.length &&
          /^\s*\|/.test(lines[i + 1]) &&
          !DELIM.test(lines[i + 1]) &&
          splitRow(lines[i + 1]).length === head.length
        )
          continue;
        if (!lines[i].trim() || !lines[i].includes("|") || startsBlock(lines, i)) break;
        rows.push(splitRow(lines[i]));
      }
      out.push(
        `<div class="md-table"><table><thead><tr>${head.map((c, k) => cell("th", c, k)).join("")}</tr></thead><tbody>${rows.map((r) => `<tr>${head.map((_, k) => cell("td", r[k] ?? "", k)).join("")}</tr>`).join("")}</tbody></table></div>`,
      );
      continue;
    }
    // quote (nested quotes and lazy lines)
    if (QUOTE.test(l)) {
      const body = [];
      for (; i < lines.length && lines[i].trim(); i++) body.push(lines[i].replace(QUOTE, ""));
      out.push(`<blockquote>${blocks(body)}</blockquote>`);
      continue;
    }
    // list
    m = l.match(LI);
    if (m) {
      i = list(lines, i, out);
      continue;
    }
    // indented code
    if (indentOf(l) >= 4) {
      const body = [];
      for (; i < lines.length && (indentOf(lines[i]) >= 4 || !lines[i].trim()); i++)
        body.push(dedent(lines[i], 4));
      while (body.length && !body[body.length - 1].trim()) body.pop();
      out.push(`<pre><code>${esc(body.join("\n"))}</code></pre>`);
      continue;
    }
    // paragraph (a "---" or "===" under it makes it a heading)
    const para = [l];
    for (i++; i < lines.length && lines[i].trim() && !startsBlock(lines, i); i++) {
      if (/^ {0,3}=+\s*$/.test(lines[i])) break;
      para.push(lines[i]);
    }
    if (i < lines.length && /^ {0,3}(=+|-+)\s*$/.test(lines[i]) && lines[i].trim()) {
      const lv = lines[i].trim()[0] === "=" ? 1 : 2;
      out.push(`<h${lv}>${mdInline(para.join(" ").trim())}</h${lv}>`);
      i++;
      continue;
    }
    out.push(`<p>${paraHtml(para)}</p>`);
  }
  return out.join("\n");
}

function list(lines, i, out) {
  const first = lines[i].match(LI),
    ordered = /\d/.test(first[2]),
    kind = ordered ? first[2].slice(-1) : first[2];
  const items = [];
  let loose = false,
    sawBlank = false;
  for (; i < lines.length; ) {
    const l = lines[i],
      m = l.match(LI);
    const sameList = m && (ordered ? /\d/.test(m[2]) && m[2].slice(-1) === kind : m[2] === kind);
    const cur = items[items.length - 1];
    if (sameList && (!cur || indentOf(l) < cur.indent)) {
      if (sawBlank && cur) loose = true;
      const pad = m[3].length > 4 || !m[4] ? 1 : m[3].length;
      items.push({
        indent: m[1].length + m[2].length + pad,
        start: ordered ? parseInt(m[2], 10) : 1,
        lines: [m[4]],
      });
      sawBlank = false;
      i++;
      continue;
    }
    if (!cur) break;
    if (!l.trim()) {
      cur.lines.push("");
      sawBlank = true;
      i++;
      continue;
    }
    if (indentOf(l) >= cur.indent) {
      if (sawBlank && !/^\s*$/.test(cur.lines.join(""))) cur.loose = true;
      cur.lines.push(dedent(l, cur.indent));
      sawBlank = false;
      i++;
      continue;
    }
    if (!sawBlank && !startsBlock(lines, i) && !m) {
      cur.lines.push(l.trim());
      i++;
      continue;
    } // a lazy continuation line
    break;
  }
  while (
    items.length &&
    items[items.length - 1].lines.length &&
    !items[items.length - 1].lines[items[items.length - 1].lines.length - 1].trim()
  )
    items[items.length - 1].lines.pop();
  const tag = ordered ? "ol" : "ul",
    start = ordered && items[0].start !== 1 ? ` start="${items[0].start}"` : "";
  const tasks = items.some((x) => /^\[[ xX]\]\s/.test(x.lines[0]));
  const html = items
    .map((it) => {
      let task = "";
      const t = it.lines[0].match(/^\[([ xX])\]\s+(.*)$/);
      if (t) {
        task = `<input type="checkbox" disabled${t[1] !== " " ? " checked" : ""}> `;
        it.lines[0] = t[2];
      }
      let body = blocks(it.lines);
      if (!loose && !it.loose) body = body.replace(/^<p>([\s\S]*?)<\/p>/, "$1"); // a tight list: no paragraph spacing
      return `<li${task ? ' class="task"' : ""}>${task}${body}</li>`;
    })
    .join("");
  out.push(`<${tag}${start}${tasks ? ' class="tasks"' : ""}>${html}</${tag}>`);
  return i;
}

// mdRender: Markdown → safe HTML for display.
export function mdRender(md = "") {
  return blocks(
    String(md)
      .replace(/\r\n?/g, "\n")
      .replace(/^\ufeff/, "")
      .split("\n"),
  );
}
