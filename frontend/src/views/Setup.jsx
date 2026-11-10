import { useEffect, useState } from "react";
import { backend } from "../backend.js";
import { S, update, errMsg } from "../store.js";
import { Icon, Spinner } from "../ui.jsx";
import { Logo } from "../Logo.jsx";

export const PROVIDERS = {
  copilot: { title: "GitHub Copilot", blurb: "Runs the Copilot CLI.", envName: "COPILOT_GITHUB_TOKEN", placeholder: "github_pat_…",
    help: "Use a fine-grained personal access token (github_pat_…) with the “Copilot Requests” permission. Classic tokens (ghp_…) are rejected by the Copilot CLI." },
  claude: { title: "Claude", blurb: "Runs the Claude Code CLI.", envName: "ANTHROPIC_API_KEY", placeholder: "sk-ant-…",
    help: "Paste an API key (sk-ant-api…) or a token from `claude setup-token` (sk-ant-oat…)." },
  mock: { title: "Demo agent", blurb: "Scripted mock, no CLI or account needed.", envName: "", placeholder: "", help: "" },
};

export function agentLabel(p = S.setup.provider) {
  return { claude: "Claude", copilot: "Copilot", mock: "Demo agent", skipped: "no agent" }[p] || "no agent";
}

export function Setup() {
  const [provider, setProvider] = useState(S.setup.provider && S.setup.provider !== "skipped" ? S.setup.provider : "claude");
  const [rows, setRows] = useState(null);
  const [show, setShow] = useState(false);
  const [tok, setTok] = useState("");
  const [err, setErr] = useState("");
  const scan = () => { setRows(null); backend.DetectCredentials().then(setRows, () => setRows([])); };
  useEffect(scan, []);
  const p = PROVIDERS[provider];
  const cli = S.info?.agents?.find((a) => a.name === provider);
  const mine = (rows || []).filter((r) => r.provider === provider);
  const firstFound = mine.find((r) => r.found);

  const finish = async (source, token = "") => {
    try {
      await backend.SetAgent(provider, token, S.setup.model || "");
      S.setup = { ...S.setup, done: true, provider, source };
      update();
    } catch (e) { setErr(errMsg(e)); }
  };
  const submit = (e) => {
    e.preventDefault();
    if (provider === "mock") return finish("scripted");
    const t = tok.trim();
    if (!t) return setErr("Enter a token, or use credentials found on this computer.");
    if (/\s/.test(t)) return setErr("A token cannot contain whitespace.");
    finish("pasted token (this session)", t);
  };

  return (
    <section className="view setup">
      <form className="card setup-card" autoComplete="off" onSubmit={submit}>
        <div className="setup-hero"><span className="hero-glow" aria-hidden="true" /><Logo size={132} className="hero-logo" /></div>
        <h1 style={{ textAlign: "center" }}>Welcome to <span className="brand">SpecWright</span></h1>
        <p className="wf-desc">SpecWright turns a written spec into working code, step by step, using an AI coding assistant. Pick the agent, then use the credentials its CLI already has on this computer, or paste a token for this session. You can change this later under Appearance › Account.</p>
        <div className="setup-choices" role="radiogroup" aria-label="Agent" style={{ gridTemplateColumns: "repeat(3, 1fr)" }}>
          {Object.entries(PROVIDERS).map(([id, c]) => (
            <button key={id} type="button" role="radio" aria-checked={provider === id} className={"setup-choice" + (provider === id ? " active" : "")} onClick={() => { setProvider(id); setErr(""); }}>
              <strong>{c.title}</strong><span className="muted">{c.blurb}</span>
            </button>
          ))}
        </div>
        {cli && provider !== "mock" && (
          <div className={"banner" + (cli.available ? "" : " error")}>
            {cli.available ? <>CLI found: <code className="inline-code">{cli.path}</code>{cli.version ? ` · ${cli.version}` : ""}</> : cli.problem}
          </div>
        )}
        {provider !== "mock" && <>
          <div className="card-head" style={{ margin: "4px 0 0" }}><h3>Credentials found on this computer</h3>{rows ? <button type="button" className="link" onClick={scan}>Scan again</button> : <Spinner label="Scanning" />}</div>
          <div className="setup-detect" aria-live="polite">
            {(rows ? mine : [1, 2]).map((r, k) => !rows ? (
              <div className="detect-row" key={k}><span className="ic"><Spinner /></span><div className="grow"><b>Scanning…</b></div></div>
            ) : (
              <div className="detect-row" key={r.id}>
                <span className={"ic " + (r.found ? "ok-ic" : "no-ic")}>{r.found ? <Icon.check /> : <Icon.x />}</span>
                <div className="grow"><b>{r.label}</b><small>{r.where}{r.found ? " · " + r.who : " · not set"}</small>{r.found && r.warn && <small style={{ color: "var(--warn)" }}>{r.warn}</small>}</div>
                {r.found && <button type="button" className={"btn " + (r === firstFound ? "primary" : "secondary")} onClick={() => finish(r.label)}>Use this</button>}
              </div>
            ))}
          </div>
          <div className="or-line">or paste a token</div>
          <label className="setup-field"><span>Token <code className="inline-code">{p.envName}</code></span>
            <div className="setup-input"><input type={show ? "text" : "password"} placeholder={p.placeholder} spellCheck="false" value={tok} onChange={(e) => setTok(e.target.value)} /><button type="button" className="btn secondary" onClick={() => setShow(!show)}>{show ? "Hide" : "Show"}</button></div>
          </label>
          <p className="muted">{p.help} The token is kept in memory for this session only and passed to the {p.title} CLI as <code className="inline-code">{p.envName}</code>. It is never written to disk or to run history.</p>
        </>}
        {err && <div className="banner error">{err}</div>}
        <div className="row-actions">
          <button className="btn primary">{provider === "mock" ? "Use the demo agent" : "Save and continue"}</button>
          <button type="button" className="btn secondary" onClick={() => { S.setup = { ...S.setup, done: true, provider: "skipped", source: "" }; update(); }}>Skip for now</button>
        </div>
      </form>
    </section>
  );
}
