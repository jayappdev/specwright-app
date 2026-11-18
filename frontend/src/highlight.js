// Tiny syntax highlighter (Java/Kotlin/TS/Go-ish, Markdown, XML) that
// returns tokens; colors come from the theme's --syn-* variables.
const KW =
  /^(package|import|public|private|protected|final|class|interface|enum|record|static|void|new|return|if|else|throw|throws|this|for|while|try|catch|extends|implements|boolean|int|long|double|true|false|null|func|var|let|const|type|struct|def|from|export|async|await|fun|val|namespace|using)$/;
const RE =
  /(\/\*[\s\S]*?\*\/|\/\/[^\n]*|#[^\n]*)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')|(<\/?[\w.-]+|\/?>)|(@\w+)|\b(\d+)\b|\b([A-Z]\w*)\b|\b([a-z_]\w*)(?=\()|\b(\w+)\b/g;

// Returns an array of lines; each line is an array of [className|null, text].
export function highlight(src, path) {
  const lines = [];
  if (/\.md$/i.test(path)) {
    for (const l of src.split("\n"))
      lines.push([[/^#/.test(l) ? "syn-keyword" : /^\s*[-\d]/.test(l) ? "syn-string" : null, l]]);
    return lines;
  }
  const hashComments = /\.(py|sh|ya?ml|toml|rb)$/i.test(path);
  let cur = [];
  const push = (cls, text) => {
    const parts = text.split("\n");
    parts.forEach((p, i) => {
      if (i > 0) {
        lines.push(cur);
        cur = [];
      }
      if (p) cur.push([cls, p]);
    });
  };
  let last = 0,
    m;
  RE.lastIndex = 0;
  while ((m = RE.exec(src))) {
    if (m.index > last) push(null, src.slice(last, m.index));
    last = RE.lastIndex;
    const [t, com, str, tag, ann, num, typ, fn, word] = m;
    if (com) push(com.startsWith("#") && !hashComments ? null : "syn-comment", t);
    else if (str) push("syn-string", t);
    else if (tag) push("syn-keyword", t);
    else if (ann) push("syn-function", t);
    else if (num) push("syn-number", t);
    else if (typ) push("syn-type", t);
    else if (fn) push(KW.test(fn) ? "syn-keyword" : "syn-function", t);
    else push(KW.test(word) ? "syn-keyword" : null, t);
  }
  if (last < src.length) push(null, src.slice(last));
  lines.push(cur);
  return lines;
}
