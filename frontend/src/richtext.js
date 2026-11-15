// Markdown ⇄ HTML for the rich-text editor. Markdown is what is stored and
// what agents read; HTML is only the editing view. Supported: headings,
// paragraphs, **bold**, *italic*, `code`, ``` fences, [links](url), - and
// 1. lists (one level of nesting), > quotes and line breaks. Anything else
// pasted in is reduced to this subset, so no scripts or styles survive.

const esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
const safeUrl = (u) => (/^(https?:|mailto:|#|\.{0,2}\/|[\w.-]+(\/|$))/i.test(u.trim()) && !/^\s*javascript:/i.test(u) ? u.trim() : "#");

function inline(s) {
  // protect code spans first so their content is not formatted
  const codes = [];
  s = s.replace(/`([^`]+)`/g, (_, c) => `\u0000${codes.push(c) - 1}\u0000`);
  s = esc(s)
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, t, u) => `<a href="${esc(safeUrl(u))}">${t}</a>`)
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/(^|[^*\w])\*([^*\s][^*]*)\*/g, "$1<em>$2</em>")
    .replace(/(^|[^_\w])_([^_\s][^_]*)_/g, "$1<em>$2</em>");
  // eslint-disable-next-line no-control-regex -- NUL marks placeholders that cannot occur in text
  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${esc(codes[+i])}</code>`);
}

export function mdToHtml(md = "") {
  const lines = String(md).replace(/\r\n?/g, "\n").split("\n");
  const out = [];
  let para = [];
  const flush = () => { if (para.length) { out.push(`<p>${para.map(inline).join("<br>")}</p>`); para = []; } };
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i];
    if (/^```/.test(l)) {
      flush();
      const body = [];
      for (i++; i < lines.length && !/^```/.test(lines[i]); i++) body.push(lines[i]);
      out.push(`<pre><code>${esc(body.join("\n"))}</code></pre>`);
      continue;
    }
    const h = l.match(/^(#{1,4})\s+(.*)$/);
    if (h) { flush(); out.push(`<h${h[1].length}>${inline(h[2])}</h${h[1].length}>`); continue; }
    if (/^\s*>\s?/.test(l)) {
      flush();
      const body = [];
      for (; i < lines.length && /^\s*>\s?/.test(lines[i]); i++) body.push(lines[i].replace(/^\s*>\s?/, ""));
      i--;
      out.push(`<blockquote>${mdToHtml(body.join("\n"))}</blockquote>`);
      continue;
    }
    const li = l.match(/^(\s*)([-*+]|\d+[.)])\s+(.*)$/);
    if (li) {
      flush();
      const ordered = /\d/.test(li[2]);
      const items = [];
      for (; i < lines.length; i++) {
        const m = lines[i].match(/^(\s*)([-*+]|\d+[.)])\s+(.*)$/);
        if (m && m[1].length < 2) items.push({ text: m[3], kids: [] });
        else if (m && items.length) items[items.length - 1].kids.push({ text: m[3], ordered: /\d/.test(m[2]) });
        else if (/^\s{2,}\S/.test(lines[i]) && items.length) items[items.length - 1].text += "\n" + lines[i].trim();
        else break;
      }
      i--;
      const tag = ordered ? "ol" : "ul";
      out.push(`<${tag}>${items.map((it) => {
        const kids = it.kids.length ? `<${it.kids[0].ordered ? "ol" : "ul"}>${it.kids.map((k) => `<li>${inline(k.text)}</li>`).join("")}</${it.kids[0].ordered ? "ol" : "ul"}>` : "";
        return `<li>${it.text.split("\n").map(inline).join("<br>")}${kids}</li>`;
      }).join("")}</${tag}>`);
      continue;
    }
    if (!l.trim()) { flush(); continue; }
    para.push(l);
  }
  flush();
  return out.join("");
}

// --- HTML → Markdown ---------------------------------------------------------

function inlineMd(node) {
  let s = "";
  for (const n of node.childNodes) {
    if (n.nodeType === 3) { s += n.nodeValue.replace(/\u00a0/g, " "); continue; }
    if (n.nodeType !== 1) continue;
    const t = n.tagName.toLowerCase(), inner = () => inlineMd(n);
    const wrap = (m) => { const v = inner(); return v.trim() ? m + v.trim() + m + (/\s$/.test(v) ? " " : "") : v; };
    switch (t) {
      case "b": case "strong": s += wrap("**"); break;
      case "i": case "em": s += wrap("*"); break;
      case "code": s += "`" + n.textContent + "`"; break;
      case "a": { const v = inner().trim(); const href = n.getAttribute("href") || ""; s += href && href !== v ? `[${v}](${safeUrl(href)})` : v; break; }
      case "br": s += "\n"; break;
      case "span": case "font": case "u": case "mark": case "small": case "sup": case "sub": s += inner(); break;
      default: { const w = n.ownerDocument.createElement("div"); w.appendChild(n.cloneNode(true)); const v = block(w).trim(); s += v ? "\n" + v + "\n" : ""; }
    }
  }
  return s;
}

function listMd(n, depth) {
  const ordered = n.tagName.toLowerCase() === "ol";
  let k = 0, out = [];
  for (const li of n.children) {
    if (li.tagName.toLowerCase() !== "li") continue;
    k++;
    const nested = [...li.children].filter((c) => /^(ul|ol)$/i.test(c.tagName));
    const clone = li.cloneNode(true);
    [...clone.children].forEach((c) => /^(ul|ol)$/i.test(c.tagName) && c.remove());
    const text = inlineMd(clone).trim().replace(/\n+/g, "\n" + "  ".repeat(depth + 1));
    out.push("  ".repeat(depth) + (ordered ? `${k}. ` : "- ") + text);
    for (const sub of nested) out.push(listMd(sub, depth + 1));
  }
  return out.join("\n");
}

const BLOCK = /^(ul|ol|pre|blockquote|h[1-6]|div|p|section|article|hr)$/i;

function block(node) {
  const parts = [];
  let run = "";
  const push = (b) => { if (run.trim()) parts.push(run.trim()); run = ""; if (b != null && b.trim()) parts.push(b); };
  for (const n of node.childNodes) {
    if (n.nodeType === 3) { run += n.nodeValue.replace(/\u00a0/g, " "); continue; }
    if (n.nodeType !== 1) continue;
    const t = n.tagName.toLowerCase();
    if (/^h[1-6]$/.test(t)) push("#".repeat(Math.min(4, +t[1])) + " " + inlineMd(n).trim().replace(/\n/g, " "));
    else if (t === "p" || t === "div" || t === "section" || t === "article") {
      // editors wrap lines (and sometimes whole lists) in <div>: keep block structure inside
      if ([...n.children].some((c) => BLOCK.test(c.tagName))) push(block(n));
      else push(inlineMd(n).trim() ? inlineMd(n).trim() : null);
    }
    else if (t === "ul" || t === "ol") push(listMd(n, 0));
    else if (t === "pre") push("```\n" + n.textContent.replace(/\n$/, "") + "\n```");
    else if (t === "blockquote") push(block(n).split("\n").map((l) => "> " + l).join("\n"));
    else if (t === "hr") push("---");
    else if (t === "br") run += "\n";
    else if (t === "script" || t === "style") continue;
    else { const tmp = document.createElement("span"); tmp.appendChild(n.cloneNode(true)); run += inlineMd(tmp); }
  }
  push(null);
  return parts.join("\n\n");
}

export function htmlToMd(html) {
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");
  return block(doc.body).replace(/\n{3,}/g, "\n\n").trim();
}

// Pasted HTML → the supported subset (via Markdown, so it is always safe).
export const sanitizeHtml = (html) => mdToHtml(htmlToMd(html));

// --- rules as one document -------------------------------------------------

// Each rule is a "## <id>" section; its body is the rule's Markdown.
export function rulesToMd(rules) {
  return "# Functionality rules\n\n" + rules.map((r) => `## ${r.id}\n\n${(r.text || "").trim() || "_(empty)_"}`).join("\n\n") + "\n";
}

export function mdToRules(md) {
  const lines = String(md).replace(/\r\n?/g, "\n").split("\n");
  const rules = [];
  let cur = null, fence = false;
  for (const l of lines) {
    if (/^```/.test(l)) fence = !fence;
    const h = !fence && l.match(/^##\s+(.+?)\s*$/);
    if (h) { cur = { id: h[1].trim(), body: [] }; rules.push(cur); continue; }
    if (cur) cur.body.push(l);
  }
  if (rules.length) return rules.map((r) => ({ id: r.id, text: r.body.join("\n").trim().replace(/^_\(empty\)_$/, "") }));
  // older export: "- **R-001** text"
  return lines.filter((l) => /^\s*-/.test(l)).map((l, k) => { const m = l.match(/\*\*(.+?)\*\*\s*(.*)/); return m ? { id: m[1], text: m[2] } : { id: "R-" + String(k + 1).padStart(3, "0"), text: l.replace(/^\s*-\s*/, "") }; });
}

export function rulesToHtmlDoc(rules, title = "Functionality rules") {
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>${esc(title)}</title>
<style>body{font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;max-width:760px;margin:40px auto;padding:0 16px;color:#1f2328}
h2{font-size:1.05em;margin:28px 0 6px;padding-top:12px;border-top:1px solid #d0d7de;font-family:ui-monospace,Menlo,monospace}
code,pre{font-family:ui-monospace,Menlo,monospace;background:#f6f8fa;border-radius:4px}code{padding:1px 4px}pre{padding:10px;overflow:auto}
blockquote{margin:0;padding-left:12px;border-left:3px solid #d0d7de;color:#59636e}</style></head>
<body><h1>${esc(title)}</h1>
${rules.map((r) => `<section id="${esc(r.id)}"><h2>${esc(r.id)}</h2>${mdToHtml(r.text || "")}</section>`).join("\n")}
</body></html>
`;
}

// Next free id after the highest R-<number>.
export function nextRuleId(rules) {
  const n = Math.max(0, ...rules.map((r) => +((r.id.match(/(\d+)\s*$/) || [])[1] || 0)));
  return "R-" + String(n + 1).padStart(3, "0");
}
