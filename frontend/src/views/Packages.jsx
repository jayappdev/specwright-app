// "Packages & proxy": where tool dependencies are downloaded from. Every
// field overrides what this computer already uses (shown as the placeholder).
import { useEffect } from "react";
import { backend } from "../backend.js";
import { S, update, toast, errMsg } from "../store.js";
import { Card, Icon } from "../ui.jsx";

let loading = false;
export async function loadPackages() {
  if (loading) return;
  loading = true;
  try { S.pkg = { ...(await backend.PackageSettings()), checks: S.pkg?.checks || null, dirty: false }; update(); }
  catch (e) { toast(errMsg(e), "error"); }
  finally { loading = false; }
}

// registry the given tool language downloads from (override or inherited)
export function registryFor(lang) {
  const s = S.pkg?.saved, d = S.pkg?.detected;
  if (!s || !d) return "";
  return { "Node.js": s.npm.registry || d.npm.registry || "https://registry.npmjs.org/", Python: s.pip.indexUrl || d.pip.indexUrl, Go: s.go.proxy || d.go.proxy, Java: s.maven.mirrorUrl || d.maven.mirrorUrl }[lang] || "";
}

const FIELDS = [
  ["Proxy", [
    ["proxy.https", "HTTPS proxy", "http://user:password@proxy.corp:8080"],
    ["proxy.http", "HTTP proxy", "http://proxy.corp:8080"],
    ["proxy.noProxy", "No proxy for", "localhost,127.0.0.1,.corp.example"],
    ["caFile", "Corporate CA bundle (PEM)", "/etc/ssl/corp-root-ca.pem"],
  ]],
  ["npm (Node.js)", [["npm.registry", "Registry URL", "https://nexus.corp/repository/npm-group/"]]],
  ["pip (Python)", [["pip.indexUrl", "Index URL", "https://nexus.corp/repository/pypi/simple"], ["pip.extraIndexUrl", "Extra index URL", ""], ["pip.trustedHost", "Trusted host", "nexus.corp"]]],
  ["Go modules", [["go.proxy", "GOPROXY", "https://athens.corp,direct"], ["go.private", "GOPRIVATE", "git.corp/*"], ["go.noSumDb", "GONOSUMDB", "git.corp/*"], ["go.sumDb", "GOSUMDB", "off"], ["go.toolchain", "GOTOOLCHAIN", "local"]]],
  ["Maven (Java)", [["maven.mirrorUrl", "Mirror URL", "https://nexus.corp/repository/maven-public/"], ["maven.mirrorOf", "Mirror of", "*"]]],
];
const get = (o, path) => path.split(".").reduce((x, k) => x?.[k], o) ?? "";
const put = (o, path, v) => { const ks = path.split("."); let x = o; while (ks.length > 1) { const k = ks.shift(); x = x[k] ||= {}; } x[ks[0]] = v; };

export function PackagesCard({ index }) {
  useEffect(() => { if (S.ws && !S.pkg) loadPackages(); }, [S.ws?.root]);
  const p = S.pkg;
  if (!p) return <Card id="w-packages" index={index} title="Packages & proxy"><p className="muted">Reading this computer's package settings…</p></Card>;
  const s = p.saved, overrides = FIELDS.flatMap(([, f]) => f).filter(([k]) => get(s, k)).length;
  const save = async () => {
    try {
      await backend.SavePackageSettings(s); await loadPackages();
      const sk = await backend.SpecKitInfo().catch(() => null);
      toast("Package settings saved to .codegen/packages.json." + (sk?.downloading ? " Spec Kit is now being downloaded through this proxy." : ""));
    } catch (e) { toast(errMsg(e), "error"); }
  };
  const check = async () => { S.pkg.checks = "running"; update(); try { if (p.dirty) { await backend.SavePackageSettings(s); p.dirty = false; } S.pkg.checks = await backend.CheckRegistries(); } catch (e) { S.pkg.checks = null; toast(errMsg(e), "error"); } update(); };
  const missing = Object.entries({ npm: "Node.js", python3: "Python", go: "Go", mvn: "Java (Maven)" }).filter(([b]) => !p.tools?.[b]).map(([, l]) => l);
  return (
    <Card id="w-packages" index={index} title="Packages & proxy" badge={<span className="badge">{overrides ? `${overrides} override${overrides === 1 ? "" : "s"}` : "inherited"}</span>}>
      <p className="wf-desc">Tools that need packages get their own folder under <code className="inline-code">.codegen/tools/</code>, filled from these registries into local package repositories in <code className="inline-code">{p.caches}</code> (git-ignored). Leave a field empty to use what this computer already uses, shown in grey. Proxy passwords stay in memory and are never written to disk.</p>
      {missing.length > 0 && <p className="muted" style={{ color: "var(--warn)" }}><Icon.warn /> Not installed here: {missing.join(", ")}. Tools in those languages cannot install packages until it is.</p>}
      {FIELDS.map(([group, fields]) => (
        <div key={group}><h4>{group}</h4>
          <div className="two-col">{fields.map(([k, label, eg]) => (
            <label key={k} className="field inline"><span>{label}</span>
              <span style={{ display: "flex", gap: 6 }}>
                <input style={{ flex: 1, fontFamily: "var(--font-mono)" }} value={get(s, k)} placeholder={get(p.detected, k) || (eg ? "e.g. " + eg : "")} spellCheck="false" autoComplete="off"
                  onChange={(e) => { put(s, k, e.target.value.trim()); p.dirty = true; update(); }} />
                {k === "caFile" && backend.kind === "desktop" && <button type="button" className="btn secondary" onClick={async () => { const f = await backend.PickCAFile().catch(() => ""); if (f) { s.caFile = f; p.dirty = true; update(); } }}>Browse…</button>}
              </span></label>
          ))}</div>
        </div>
      ))}
      <label className="check" style={{ marginTop: 10 }}><input type="checkbox" checked={!!s.applyToAgents} onChange={(e) => { s.applyToAgents = e.target.checked; p.dirty = true; update(); }} /> Also use the proxy and registries for spec workflow runs and "Compile project" (Maven gets them through <code className="inline-code">MAVEN_ARGS</code>)</label>
      <div className="row-actions"><button className="btn primary" disabled={!p.dirty} onClick={save}>Save</button><button className="btn secondary" disabled={p.checks === "running"} onClick={check}>{p.checks === "running" ? "Checking…" : "Check connections"}</button></div>
      {Array.isArray(p.checks) && <table className="table" style={{ marginTop: 10 }}><thead><tr><th>Registry</th><th>Result</th><th>Via</th><th>Time</th></tr></thead><tbody>
        {p.checks.map((c) => <tr key={c.ecosystem}><td><b>{c.ecosystem}</b><small className="muted" style={{ display: "block", wordBreak: "break-all" }}>{c.url}</small></td><td style={{ color: `var(${c.ok ? "--ok" : "--error"})` }}>{c.ok ? <Icon.check /> : <Icon.x />} {c.status}</td><td className="muted">{c.via}</td><td className="muted">{c.ms} ms</td></tr>)}
      </tbody></table>}
    </Card>
  );
}
