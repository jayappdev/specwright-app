// Finds file names and paths in text (no imports, so it is easy to test).
// Used by FileLinks.jsx.

// Extensions treated as files. Kept explicit so that version numbers,
// domains (proxy.golang.org) and "e.g." never become links.
const EXT = ("js jsx mjs cjs ts tsx mts cts json jsonc json5 md mdx markdown txt text rst adoc " +
  "java kt kts groovy gradle scala sc clj go mod sum work py pyi ipynb rb rs c h cc cpp cxx hpp hh m mm swift " +
  "cs csproj fs vb sln php pl lua r dart ex exs erl hs ml elm zig nim v sv vhd " +
  "xml xsd xsl wsdl yml yaml toml ini cfg conf config properties env plist editorconfig " +
  "sh bash zsh fish ps1 psm1 bat cmd mk makefile cmake dockerfile containerfile " +
  "sql graphql gql proto thrift avsc avdl prisma http rest " +
  "html htm xhtml css scss sass less styl vue svelte astro hbs ejs pug twig jinja j2 tpl tmpl " +
  "svg png jpg jpeg gif webp bmp ico tif tiff heic avif " +
  "pdf csv tsv log lock patch diff drawio puml plantuml mmd dot bpmn " +
  "jar war ear zip tgz gz tar whl class dll so dylib exe wasm").split(/\s+/);
const EXT_RE = EXT.join("|");
const SPECIAL = "Dockerfile|Makefile|Jenkinsfile|Procfile|Gemfile|Rakefile|Vagrantfile|LICENSE|CODEOWNERS|\\.gitignore|\\.gitattributes|\\.dockerignore|\\.npmrc|\\.env(?:\\.[\\w-]+)?|\\.editorconfig|\\.prettierrc|\\.eslintrc(?:\\.\\w+)?";

// a path: optional ~/, /, ./ or ../ prefix, folders, a name with a known
// extension (or a special name), then an optional :line[:col] or #Lline
const PATH_SRC = `(?:~\\/|\\.{1,2}\\/|\\/)?(?:[\\w.@+-]+\\/)*(?:[\\w@+-][\\w.@+-]*\\.(?:${EXT_RE})|(?:${SPECIAL}))(?::(\\d+)(?::\\d+)?|#L(\\d+))?`;
const PATH_IN_TEXT = new RegExp(`(^|[\\s(\\[{<'"\`=,;:|>])(${PATH_SRC})(?=$|[\\s)\\]}>'"\`,;:!?|]|\\.(?:\\s|$))`, "gi");
// a whole string that is one path (spaces allowed inside, e.g. "Application Support")
const WHOLE_PATH = new RegExp(`^(?:~\\/|\\.{1,2}\\/|\\/)?(?:[^\\n\`'"<>|*?]+\\/)*(?:[^\\s/\`'"<>|*?][^/\`'"<>|*?\\n]*\\.(?:${EXT_RE})|(?:${SPECIAL}))(?::(\\d+)(?::\\d+)?|#L(\\d+))?$`, "i");

// "Node.js", "Vue.js": framework names, not files (unless written as a path)
const NOT_FILE = /^[A-Z][A-Za-z0-9]*\.js$/;

const isUrlish = (s) => /^[a-z][\w+.-]*:\/\//i.test(s) || /^(www\.)/i.test(s);

// Split "src/a.java:42" into { path, line }.
export function parseFileRef(raw) {
  const m = /^(.*?)(?::(\d+)(?::\d+)?|#L(\d+))?$/.exec(raw.trim());
  return { path: m[1], line: +(m[2] || m[3] || 0) };
}

// Is a whole string (e.g. the text of a <code>) a file path?
export function wholeFileRef(text) {
  const t = (text || "").trim();
  if (!t || t.length > 400 || isUrlish(t) || NOT_FILE.test(t) || /\s{2,}|\n/.test(t)) return null;
  // a bare name with spaces is a sentence, not a file; spaces need a folder
  if (/\s/.test(t) && !t.includes("/")) return null;
  if (!WHOLE_PATH.test(t)) return null;
  return parseFileRef(t);
}

// Find file references in free text: [{ index, length, raw, path, line }].
export function findFileRefs(text) {
  const out = [];
  if (!text || typeof text !== "string") return out;
  // quoted or backticked paths may contain spaces: `My Project/a b.txt`
  const quoted = /([`'"])([^`'"\n]{1,300})\1/g;
  let m;
  while ((m = quoted.exec(text))) {
    const ref = /\s/.test(m[2]) ? wholeFileRef(m[2]) : null;
    if (ref) out.push({ index: m.index + 1, length: m[2].length, raw: m[2], ...ref });
  }
  PATH_IN_TEXT.lastIndex = 0;
  while ((m = PATH_IN_TEXT.exec(text))) {
    const raw = m[2], index = m.index + m[1].length;
    if (out.some((o) => index < o.index + o.length && index + raw.length > o.index)) continue;
    const before = text.slice(Math.max(0, index - 8), index);
    if (/:\/\/[^\s]*$/.test(before) || /[\w.-]$/.test(before) && !/[\s(\[{<'"`=,;:|>]$/.test(before)) continue; // inside a URL or word
    const ref = parseFileRef(raw);
    if (!ref.path.includes("/") && NOT_FILE.test(ref.path)) continue;
    if (/^\d+(\.\d+)+$/.test(ref.path)) continue;
    out.push({ index, length: raw.length, raw, ...ref });
  }
  return out.sort((a, b) => a.index - b.index);
}

