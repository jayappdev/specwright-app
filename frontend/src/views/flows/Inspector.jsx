// The Flow designer's inspector: flow settings, inputs (incl. secret sources),
// each step type's settings, decisions, filters, aggregates, connections, the
// data panel (what a step gets and gives) and variable chips.
import { LessonsEditor } from "./Lessons.jsx";
import { TagEditor, allTags, allGroups } from "../../flowLists.jsx";
import { HeadingPicker } from "../../listLayout.jsx";
import { ContextInspector } from "./ContextStep.jsx";
import { StepGuidance, aiStep } from "./StepGuidance.jsx";
import { Reliability } from "../../ToolTile.jsx";
import { TriggersEditor } from "./Triggers.jsx";
import { ModelPicker } from "../../ModelPicker.jsx";
import { stepRef, toolRef, isBuiltin, loopInfo } from "../../flowNames.js";
import { useEffect, useRef, useState } from "react";
import { backend } from "../../backend.js";
import { S, update, toast, errMsg } from "../../store.js";
import { Icon } from "../../ui.jsx";
import { AttachBar, onPasteAttach } from "../../Attachments.jsx";
import { FileLink } from "../../FileLinks.jsx";
import { openFlowGuide, GUIDE_TYPES } from "../FlowGuide.jsx";
import { paths as schemaPaths, compact, typeName } from "../../schema.js";
import {
  FILE_PARAM,
  FORMATS,
  GATHER_MODES,
  INPUT_TYPES,
  MERGE_MODES,
  SPLIT_MODES,
  STEP_TYPES,
  StepIcon,
  ToolOptions,
  ancestors,
  changed,
  copySteps,
  feedFor,
  fl,
  nodeOf,
  openFlow,
  parentsOf,
  pasteAsNewFlow,
  pasteSteps,
  refreshShapes,
  removeSelected,
  renameNode,
  selectedIds,
  setSelection,
  togglePane,
  tools,
} from "../Flows.jsx";
import { AggregateInspector, DecisionInspector, EdgeInspector, FilterInspector } from "./Conditions.jsx";
import {
  PASSWORD_PARAM,
  SecretPick,
  SecretSourceEditor,
  VarChips,
  insertText,
  insertVar,
  lastField,
  track,
} from "./Fields.jsx";

// --- inspector ----------------------------------------------------------------------------

export const Field = ({ label, hint, children }) => (
  <label className="field fl-field">
    <span className="muted">{label}</span>
    {children}
    {hint && <small className="muted">{hint}</small>}
  </label>
);

function FlowSettings() {
  const f = fl(),
    flow = f.flow;
  const ctxFiles = useRef(null),
    skillFiles = useRef(null);
  ctxFiles.current ||= (flow.context.files || []).map((p) => ({
    path: p,
    name: p.split("/").pop(),
    kind: /\.(png|jpe?g|gif|webp)$/i.test(p) ? "image" : /\.pdf$/i.test(p) ? "pdf" : "text",
    size: 0,
  }));
  // keep the attachment list and the flow's paths in sync
  useEffect(() => {
    const paths = ctxFiles.current.filter((a) => a.path && !a.saving).map((a) => a.path);
    if (JSON.stringify(paths) !== JSON.stringify(flow.context.files || [])) {
      flow.context.files = paths;
      changed();
    }
  });
  const skills = (S.ws.libraries || []).filter((l) => l.kind === "skill");
  const has = (p) => flow.skills.some((s) => s.path === p);
  const toggle = (l) => {
    flow.skills = has(l.path)
      ? flow.skills.filter((s) => s.path !== l.path)
      : [...flow.skills, { path: l.path, name: l.name }];
    changed();
  };
  skillFiles.current ||= [];
  useEffect(() => {
    for (const a of skillFiles.current)
      if (a.path && !a.saving && !has(a.path)) {
        flow.skills.push({ path: a.path, name: a.name.replace(/\.md$/, "") });
        changed();
      }
  });
  return (
    <>
      <h3>Flow</h3>
      <Field label="Name">
        <input
          value={flow.name}
          onChange={(e) => {
            flow.name = e.target.value;
            changed();
          }}
        />
      </Field>
      <Field label="Description">
        <textarea
          rows={2}
          value={flow.description || ""}
          onChange={(e) => {
            flow.description = e.target.value;
            changed();
          }}
        />
      </Field>
      <Field
        label="Group"
        hint="Where the flow is listed, e.g. Releases or Billing / Refunds. Empty: not in a group."
      >
        <input
          value={flow.group || ""}
          list="fls-group-suggestions"
          placeholder="e.g. Releases"
          onChange={(e) => {
            flow.group = e.target.value;
            changed();
          }}
        />
        <datalist id="fls-group-suggestions">
          {allGroups(fl().list).map((g) => (
            <option key={g} value={g} />
          ))}
        </datalist>
      </Field>
      {flow.id && (
        <Field
          label="Heading"
          hint="Your own arrangement of the flow lists (you can also drag a flow onto a heading). Kept in SpecWright, not in the flow."
        >
          <HeadingPicker list="flows" id={flow.id} label="" />
        </Field>
      )}
      <Field
        label="Tags"
        hint="Labels to find the flow by; the flow lists can be searched and filtered by them."
      >
        <TagEditor
          tags={flow.tags || []}
          suggestions={allTags(fl().list)}
          onChange={(t) => {
            flow.tags = t;
            changed();
          }}
        />
      </Field>
      <Field
        label="Manual work one run replaces (minutes)"
        hint="Counted as hours saved in Metrics when a run completes. Empty: estimated from the steps."
      >
        <input
          type="number"
          min="0"
          value={flow.minutesSaved || ""}
          placeholder="estimate"
          onChange={(e) => {
            flow.minutesSaved = +e.target.value || 0;
            changed();
          }}
        />
      </Field>
      {(flow.revision > 1 || flow.designed) && (
        <p className="muted fl-help">
          {flow.revision > 1 && (
            <>
              Version {flow.revision}
              {flow.parent ? (
                <>
                  {" "}
                  of{" "}
                  <button className="link" onClick={() => openFlow(flow.parent)}>
                    {(f.list || []).find((x) => x.id === flow.parent)?.name || flow.parent}
                  </button>
                </>
              ) : (
                ""
              )}
              .{" "}
            </>
          )}
          {flow.designed}
        </p>
      )}
      <FlowInterface />
      <h4>Custom context</h4>
      <p className="muted fl-help">
        Given to every AI step (and available as {"{{context}}"}): background, conventions, constraints.
      </p>
      <textarea
        rows={6}
        value={flow.context.text || ""}
        placeholder="e.g. We build Java 21 Spring services. Specs follow the team template; keep them under two pages."
        {...track}
        onPaste={onPasteAttach(ctxFiles.current)}
        onChange={(e) => {
          flow.context.text = e.target.value;
          changed();
        }}
      />
      <AttachBar
        list={ctxFiles.current}
        insertText={(t) => {
          flow.context.text = (flow.context.text ? flow.context.text + "\n" : "") + t;
          changed();
        }}
      />
      <h4>Skills</h4>
      <p className="muted fl-help">Skill files the AI steps follow ({"{{skills}}"}).</p>
      <ul className="fl-checks">
        {skills.map((l) => (
          <li key={l.id}>
            <label className="check">
              <input type="checkbox" checked={has(l.path)} onChange={() => toggle(l)} /> <b>{l.name}</b>{" "}
              <span className="muted">{l.scope === "global" ? "corporate" : "project"}</span>
            </label>
          </li>
        ))}
        {flow.skills
          .filter((s) => !skills.some((l) => l.path === s.path))
          .map((s) => (
            <li key={s.path}>
              <label className="check">
                <input
                  type="checkbox"
                  checked
                  onChange={() => {
                    flow.skills = flow.skills.filter((x) => x.path !== s.path);
                    changed();
                  }}
                />{" "}
                <FileLink path={s.path}>{s.name || s.path}</FileLink> <span className="muted">attached</span>
              </label>
            </li>
          ))}
        {!skills.length && !flow.skills.length && <li className="muted">No skills in this project yet.</li>}
      </ul>
      <AttachBar list={skillFiles.current} />
      <p className="muted fl-help">
        Attach a skill file (.md) that is not in the project's libraries; it is saved with the project and
        embedded when you export the flow.
      </p>
    </>
  );
}

function InputsEditor() {
  const flow = fl().flow;
  const files = useRef({});
  const add = () => {
    let k = flow.inputs.length + 1,
      name = "input" + k;
    while (flow.inputs.some((i) => i.name === name)) name = "input" + ++k;
    flow.inputs.push({ name, label: "Input " + k, type: "text", required: false });
    changed();
  };
  return (
    <>
      <h3>Inputs</h3>
      <p className="muted fl-help">
        What the flow asks for when it runs. Steps use them as {"{{inputs.name}}"}.
      </p>
      {flow.inputs.map((i, k) => {
        const fl_ = (files.current[i.name] ||= (i.files || []).map((p) => ({
          path: p,
          name: p.split("/").pop(),
          kind: /\.pdf$/i.test(p) ? "pdf" : /\.(png|jpe?g|gif|webp)$/i.test(p) ? "image" : "text",
          size: 0,
        })));
        const sync = () => {
          const paths = fl_.filter((a) => a.path && !a.saving).map((a) => a.path);
          if (JSON.stringify(paths) !== JSON.stringify(i.files || [])) {
            i.files = paths;
            changed();
          }
        };
        setTimeout(sync, 0);
        return (
          <div key={k} className="fl-input">
            <div className="fl-row">
              <Field label="Name">
                <input
                  value={i.name}
                  style={{ fontFamily: "var(--font-mono)" }}
                  onChange={(e) => {
                    i.name = e.target.value.replace(/[^A-Za-z0-9_]/g, "");
                    changed();
                  }}
                />
              </Field>
              <Field label="Type">
                <select
                  value={i.type}
                  onChange={(e) => {
                    i.type = e.target.value;
                    if (i.type === "secret") {
                      delete i.default;
                      i.files = [];
                      fl_.length = 0;
                    } else {
                      delete i.source;
                      delete i.keychain;
                      delete i.env;
                    }
                    changed();
                  }}
                >
                  {INPUT_TYPES.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              </Field>
              <button
                className="icon-btn fl-del"
                title="Remove input"
                onClick={() => {
                  flow.inputs.splice(k, 1);
                  changed();
                }}
              >
                <Icon.x />
              </button>
            </div>
            <Field label="Label">
              <input
                value={i.label || ""}
                onChange={(e) => {
                  i.label = e.target.value;
                  changed();
                }}
              />
            </Field>
            {["choice", "multichoice"].includes(i.type) && (
              <Field
                label="Options (one per line)"
                hint={
                  i.type === "multichoice"
                    ? "People tick any number of them; the flow gets the ticked ones, one per line (a Loop can go through them)"
                    : undefined
                }
              >
                <textarea
                  rows={3}
                  value={(i.options || []).join("\n")}
                  onChange={(e) => {
                    i.options = e.target.value.split("\n");
                    changed();
                  }}
                />
              </Field>
            )}
            {i.type === "secret" && <SecretSourceEditor i={i} />}
            {["file", "files"].includes(i.type) && (
              <Field label="Accepted files" hint="e.g. .pdf,.md,image/*">
                <input
                  value={i.accept || ""}
                  onChange={(e) => {
                    i.accept = e.target.value;
                    changed();
                  }}
                />
              </Field>
            )}
            {["date", "datetime"].includes(i.type) && (
              <Field
                label="Default value"
                hint={
                  i.type === "date"
                    ? "A date (2026-11-17), or relative: today, today-7d, today+1w. Worked out when the run starts."
                    : "A date and time (2026-11-17T09:00), or relative: now, now-2h, now+30m, today. Worked out when the run starts."
                }
              >
                <input
                  value={i.default || ""}
                  placeholder={i.type === "date" ? "e.g. today-7d" : "e.g. now-1h"}
                  onChange={(e) => {
                    i.default = e.target.value;
                    changed();
                  }}
                />
              </Field>
            )}
            {["text", "multiline", "number", "clipboard", "choice"].includes(i.type) && (
              <Field label="Default value">
                {i.type === "multiline" || i.type === "clipboard" ? (
                  <textarea
                    rows={3}
                    value={i.default || ""}
                    onChange={(e) => {
                      i.default = e.target.value;
                      changed();
                    }}
                    onPaste={onPasteAttach(fl_)}
                  />
                ) : (
                  <input
                    value={i.default || ""}
                    onChange={(e) => {
                      i.default = e.target.value;
                      changed();
                    }}
                  />
                )}
              </Field>
            )}
            {i.type === "multichoice" && (i.options || []).filter(Boolean).length > 0 && (
              <div className="field">
                <span className="muted">Ticked by default</span>
                <div className="mc-list">
                  {(i.options || []).filter(Boolean).map((o) => {
                    const on = (i.default || "").split("\n").includes(o);
                    return (
                      <label key={o} className="check">
                        <input
                          type="checkbox"
                          checked={on}
                          onChange={(e) => {
                            const cur = (i.default || "")
                              .split("\n")
                              .filter(Boolean)
                              .filter((x) => x !== o);
                            i.default = (e.target.checked ? [...cur, o] : cur)
                              .sort((a, b) => i.options.indexOf(a) - i.options.indexOf(b))
                              .join("\n");
                            changed();
                          }}
                        />{" "}
                        {o}
                      </label>
                    );
                  })}
                </div>
              </div>
            )}
            {i.type === "boolean" && (
              <label className="check">
                <input
                  type="checkbox"
                  checked={i.default === "true"}
                  onChange={(e) => {
                    i.default = e.target.checked ? "true" : "false";
                    changed();
                  }}
                />{" "}
                Yes by default
              </label>
            )}
            {["file", "files", "clipboard", "multiline"].includes(i.type) && (
              <>
                <div className="muted fl-help">
                  Default {i.type === "clipboard" ? "content" : "files"} (paste, drop or pick):
                </div>
                <AttachBar
                  list={fl_}
                  insertText={(t) => {
                    i.default = (i.default ? i.default + "\n" : "") + t;
                    changed();
                  }}
                />
              </>
            )}
            <div className="fl-row">
              <label className="check">
                <input
                  type="checkbox"
                  checked={!!i.required}
                  onChange={(e) => {
                    i.required = e.target.checked;
                    changed();
                  }}
                />{" "}
                Required
              </label>
              <Field label="">
                <input
                  placeholder="Help text shown when the flow runs"
                  value={i.description || ""}
                  onChange={(e) => {
                    i.description = e.target.value;
                    changed();
                  }}
                />
              </Field>
            </div>
          </div>
        );
      })}
      <button className="btn secondary" onClick={add}>
        <Icon.plus /> Add input
      </button>
    </>
  );
}

function ArgsEditor({ node, args, toolName, extraVars, aiFills }) {
  const t = tools().find((x) => x.name === toolName);
  if (!t) return <p className="muted">Choose a tool.</p>;
  if (!(t.params || []).length) return <p className="muted">{t.name} takes no parameters.</p>;
  const long = (p) => /sql|query|script|body|json|text|prompt|code/i.test(p.name + " " + (p.desc || ""));
  const flow = fl().flow,
    feeders = node
      ? parentsOf(flow, node.id)
          .map(nodeOf)
          .filter((x) => x && x.type !== "start")
      : [];
  return (
    <>
      {node && feeders.length > 0 && extraVars == null && (
        <div className="fl-receives">
          <Icon.workflow /> This step gets the result of{" "}
          {feeders.map((x) => <b key={x.id}>{x.title || x.id}</b>).reduce((a, b) => [a, ", ", b])}{" "}
          automatically{aiFills ? ", and the AI uses it when it fills in the fields" : ""}.
          {!aiFills && (
            <>
              {" "}
              To put that result into a field below, click <b>← {feeders[0].title || feeders[0].id}</b> next
              to the field.
            </>
          )}
          <details className="fl-how">
            <summary>How is it passed?</summary>
            <ul>
              <li>
                <b>Into a field as text:</b>{" "}
                <code
                  className="inline-code"
                  data-nofile
                >{`{{${stepRef(flow, feeders[0].id)}.buffer_output}}`}</code>{" "}
                is the result itself.
              </li>
              <li>
                <b>Into a field that wants a file</b> (like{" "}
                <code className="inline-code" data-nofile>
                  content_file
                </code>
                ):{" "}
                <code
                  className="inline-code"
                  data-nofile
                >{`{{${stepRef(flow, feeders[0].id)}.output_file}}`}</code>{" "}
                is the path of the file the result is saved in (in the run's results folder). The ← button
                picks this for fields with “file” in their name.
              </li>
              <li>
                <b>From any earlier step,</b> not only the one connected here: use its tool's name (or its
                title, or its id), e.g.{" "}
                <code className="inline-code" data-nofile>
                  {"{{html-report-generator.output_file}}"}
                </code>
                . The Insert chips below list them.
              </li>
              <li>
                <code className="inline-code" data-nofile>
                  {"{{input_file}}"}
                </code>{" "}
                still works: a file holding the results of every step connected into this one.
              </li>
              {!isBuiltin(t.name) && (
                <li>
                  <b>Without a field:</b> tools that read piped data (like{" "}
                  <code className="inline-code" data-nofile>
                    jq
                  </code>{" "}
                  or{" "}
                  <code className="inline-code" data-nofile>
                    grep
                  </code>
                  ) get it as their input, as with{" "}
                  <code className="inline-code" data-nofile>
                    a | b
                  </code>{" "}
                  in a terminal.
                </li>
              )}
              {isBuiltin(t.name) && (
                <li>
                  <b>Built-in tools</b> only read their fields: put the result into one (← button) or name a
                  file from it.
                </li>
              )}
            </ul>
          </details>
        </div>
      )}
      {t.params.map((p) => (
        <Field
          key={p.name}
          label={
            <>
              {p.name}
              {p.required && !aiFills ? " *" : ""} <span className="muted">· {p.type}</span>
              {extraVars == null &&
                feeders.map((x) => (
                  <button
                    key={x.id}
                    type="button"
                    className="fl-use"
                    title={
                      FILE_PARAM(p)
                        ? `Give ${p.name} a file holding the result of ${x.title || x.id}`
                        : `Put the result of ${x.title || x.id} into ${p.name}`
                    }
                    onClick={(e) => {
                      e.preventDefault();
                      args[p.name] = feedFor(p, x.id);
                      changed();
                    }}
                  >
                    ← {x.title || x.id}
                    {FILE_PARAM(p) ? " (as a file)" : ""}
                  </button>
                ))}
              {(PASSWORD_PARAM.test(p.name) ||
                (!/bool|int|number|float/i.test(p.type || "") &&
                  !p.enum?.length &&
                  fl().flow.inputs.some((i) => i.type === "secret"))) && <SecretPick args={args} p={p} />}
            </>
          }
          hint={p.desc}
        >
          {p.enum?.length ? (
            <select
              value={args[p.name] || ""}
              onChange={(e) => {
                args[p.name] = e.target.value;
                changed();
              }}
            >
              <option value="">{p.required ? "Choose…" : "(default)"}</option>
              {p.enum.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
              {args[p.name] && !p.enum.includes(args[p.name]) && (
                <option value={args[p.name]}>{args[p.name]}</option>
              )}
            </select>
          ) : long(p) ? (
            <textarea
              rows={3}
              value={args[p.name] || ""}
              placeholder={
                aiFills ? "AI fills this (or type a value to fix it)" : p.required ? "Required" : "Optional"
              }
              style={{ fontFamily: "var(--font-mono)" }}
              {...track}
              onChange={(e) => {
                args[p.name] = e.target.value;
                changed();
              }}
            />
          ) : (
            <input
              value={args[p.name] || ""}
              placeholder={
                aiFills ? "AI fills this (or type a value to fix it)" : p.required ? "Required" : "Optional"
              }
              {...track}
              onChange={(e) => {
                args[p.name] = e.target.value;
                changed();
              }}
            />
          )}
        </Field>
      ))}
      <VarChips node={node} extra={extraVars} />
    </>
  );
}

// Example requests for a tool, by what it does.
function requestExample(t) {
  const s = ((t?.name || "") + " " + (t?.desc || "")).toLowerCase();
  if (/sql|postgres|mysql|database|query|db\b/.test(s))
    return "e.g. The 10 customers with the most orders last month, with their email and order count";
  if (/jira|ticket|issue/.test(s))
    return "e.g. Open tickets in the ORD project assigned to me, highest priority first";
  if (/http|rest|api|fetch|url/.test(s)) return "e.g. The current status of the payments service";
  if (/test/.test(s)) return "e.g. Run the tests for the package that handles refunds";
  if (/search|find|grep|todo/.test(s)) return "e.g. Everything still marked TODO in the checkout code";
  return "Describe what this call should achieve; AI works out the arguments";
}

export function Preview({ node }) {
  const f = fl(),
    flow = f.flow;
  const parents = parentsOf(flow, node.id).filter((p) => p !== "start");
  const key = flow.id,
    sm = (f.samples[key] ||= { inputs: {}, steps: {} });
  const res = f.preview[node.id];
  const run = async () => {
    f.preview[node.id] = { busy: true };
    update();
    const steps = { ...sm.steps };
    for (const id of ancestors(flow, node.id)) if (!(id in steps)) steps[id] = "";
    const r = await backend
      .PreviewFlowStep(flow, node.id, {
        inputs: sm.inputs,
        steps,
        context: flow.context.text || "",
        skills: flow.skills.map((s) => s.path).join("\n"),
      })
      .catch((e) => ({ error: errMsg(e) }));
    f.preview[node.id] = r;
    await refreshShapes(); // sample data teaches the shapes of steps that do not declare one
    update();
  };
  const usesInputs = flow.inputs.filter((i) => JSON.stringify(node).includes("inputs." + i.name));
  return (
    <details className="fl-preview" open={!!res}>
      <summary>
        <b>Try it with sample data</b>
      </summary>
      {parents.map((p) => (
        <Field key={p} label={"Sample output of " + (nodeOf(p)?.title || p)}>
          <textarea
            rows={3}
            value={sm.steps[p] || ""}
            placeholder={'e.g. {"summary": "Place order"}'}
            onChange={(e) => {
              sm.steps[p] = e.target.value;
              update();
            }}
          />
        </Field>
      ))}
      {usesInputs.map((i) => (
        <Field key={i.name} label={"Sample " + (i.label || i.name)}>
          <input
            value={sm.inputs[i.name] || ""}
            onChange={(e) => {
              sm.inputs[i.name] = e.target.value;
              update();
            }}
          />
        </Field>
      ))}
      <button className="btn secondary" onClick={run} disabled={res?.busy}>
        {res?.busy ? "Running…" : "Preview"}
      </button>
      {res && !res.busy && (
        <>
          {res.error && <div className="banner error">{res.error}</div>}
          {res.output != null &&
            res.output !== "" &&
            (node.type === "output" && node.format === "html" ? (
              <iframe className="fl-html" sandbox="" srcDoc={res.output} title="HTML preview" />
            ) : (
              <pre className="code fl-out">{res.output}</pre>
            ))}
        </>
      )}
    </details>
  );
}

function StepInspector({ n }) {
  const f = fl(),
    flow = f.flow,
    ops = f.ops?.ops || {};
  const set = (k, v) => {
    n[k] = v;
    changed();
  };
  const parents = parentsOf(flow, n.id);
  const issues = f.issues.filter((i) => i.node === n.id);
  const opGroups = Object.entries(ops).reduce((g, [id, o]) => ((g[o.group] ||= []).push([id, o]), g), {});
  const op = ops[n.op] || {};
  return (
    <>
      <div className="fl-insp-head">
        <StepIcon n={n} tools={tools()} size={36} />
        <div>
          <h3>{n.title || n.id}</h3>
          <small className="muted">
            {n.type === "tool"
              ? isBuiltin(n.tool)
                ? "Built-in tool"
                : "Tool"
              : STEP_TYPES.find((t) => t.type === n.type)?.name || "Inputs"}
          </small>
        </div>
        <span style={{ marginLeft: "auto", display: "flex", gap: 10 }}>
          {GUIDE_TYPES.has(n.type) && (
            <button
              className="link"
              title="How this step works, with examples"
              onClick={() => openFlowGuide(n.type)}
            >
              How to use ↗
            </button>
          )}
          {n.type !== "start" && (
            <button
              className="link"
              title="Copy this step (⌘C); paste it with ⌘V here or in another flow"
              onClick={() => copySteps()}
            >
              Copy
            </button>
          )}
          {n.type !== "start" && (
            <button className="link" onClick={removeSelected}>
              Delete
            </button>
          )}
        </span>
      </div>
      {issues.map((i, k) => (
        <div key={k} className={"banner " + (i.level === "error" ? "error" : "")}>
          {i.message}
        </div>
      ))}
      {n.type === "start" ? (
        <>
          <InputsEditor />
          <TriggersEditor />
        </>
      ) : (
        <>
          <div className="fl-row">
            <Field label="Title">
              <input value={n.title || ""} onChange={(e) => set("title", e.target.value)} />
            </Field>
            <Field
              label="Step id"
              hint={`Later steps read it as {{${stepRef(fl().flow, n.id)}.buffer_output}} (text) or {{${stepRef(fl().flow, n.id)}.output_file}} (file)`}
            >
              <input
                defaultValue={n.id}
                key={n.id}
                style={{ fontFamily: "var(--font-mono)" }}
                onBlur={(e) => e.target.value !== n.id && renameNode(n, e.target.value)}
              />
            </Field>
          </div>
          {n.type === "tool" && (
            <>
              <Field label="Tool">
                <select
                  value={n.tool || ""}
                  onChange={(e) => {
                    n.tool = e.target.value;
                    n.args = {};
                    const t = tools().find((x) => x.name === n.tool);
                    (t?.params || []).forEach((p) => {
                      n.args[p.name] = "";
                    });
                    changed();
                  }}
                >
                  <option value="">Choose…</option>
                  <ToolOptions />
                </select>
              </Field>
              {n.tool && <Reliability name={n.tool} long />}
              {isBuiltin(n.tool) && (
                <p className="muted fl-builtin-desc">
                  {(tools().find((x) => x.name === n.tool)?.desc || "").replace(/^[^:]*:\s*/, "")}{" "}
                  <span>Built into SpecWright; runs in the project without the AI.</span>
                </p>
              )}
              {n.tool &&
                (() => {
                  const describe = n.instruction != null || f.describe?.[n.id];
                  const t = tools().find((x) => x.name === n.tool);
                  const setMode = (d) => {
                    (f.describe ||= {})[n.id] = d;
                    if (!d) delete n.instruction;
                    else n.instruction ??= "";
                    changed();
                  };
                  return (
                    <>
                      <Field label="How to fill in the arguments">
                        <div className="seg">
                          <button type="button" aria-pressed={!describe} onClick={() => setMode(false)}>
                            Exact values
                          </button>
                          <button type="button" aria-pressed={describe} onClick={() => setMode(true)}>
                            <Icon.spark /> Describe it, AI builds them
                          </button>
                        </div>
                      </Field>
                      {describe && (
                        <>
                          <Field
                            label="What should this step do?"
                            hint="When the flow runs, the agent investigates first (schema files, migrations, models, docs; it may also call the tool to explore, read-only), works out the arguments, then the tool runs with them. You see the chosen arguments and why."
                          >
                            <textarea
                              rows={4}
                              value={n.instruction || ""}
                              placeholder={requestExample(t)}
                              {...track}
                              onChange={(e) => {
                                n.instruction = e.target.value;
                                changed();
                              }}
                            />
                          </Field>
                          <VarChips node={n} />
                          <h4>
                            Fixed values{" "}
                            <span className="muted" style={{ fontWeight: 400, textTransform: "none" }}>
                              · optional; AI fills the rest
                            </span>
                          </h4>
                        </>
                      )}
                      {!describe && <h4>Arguments</h4>}
                      <ArgsEditor node={n} args={(n.args ||= {})} toolName={n.tool} aiFills={describe} />
                    </>
                  );
                })()}
            </>
          )}
          {n.type === "ai" && (
            <>
              <Field
                label="Prompt"
                hint="The flow's context and skills are added automatically, with this step's guidance (below)."
              >
                <textarea
                  rows={8}
                  value={n.prompt || ""}
                  placeholder="e.g. Write a specification for {{inputs.ticket}} from {{steps.jira}}. Follow the skills."
                  {...track}
                  onChange={(e) => set("prompt", e.target.value)}
                />
              </Field>
              <VarChips node={n} />
              <ExpectedReply n={n} set={set} />
              <div className="field fl-field">
                <span className="muted">Model</span>
                <ModelPicker value={n.model || ""} onChange={(v) => set("model", v)} label="Model" />
                <small className="muted">
                  Default: the model chosen when the flow runs, else the app's model.
                </small>
              </div>
              <LessonsEditor n={n} />
            </>
          )}
          {n.type === "improve" && (
            <>
              <ImproveInspector n={n} set={set} flow={flow} />
              <LessonsEditor n={n} />
            </>
          )}
          {aiStep(n) && <StepGuidance n={n} set={set} />}
          <DataPanel n={n} />
          {n.type === "decision" && <DecisionInspector n={n} set={set} />}
          {n.type === "filter" && <FilterInspector n={n} set={set} />}
          {n.type === "aggregate" && <AggregateInspector n={n} set={set} />}
          {n.type === "context" && <ContextInspector n={n} set={set} />}
          {n.type === "merge" && (
            <>
              <Field label="How to merge">
                <select value={n.mode || "concat"} onChange={(e) => set("mode", e.target.value)}>
                  {MERGE_MODES.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              </Field>
              {n.mode === "concat" && (
                <Field label="Separator" hint="\n is a new line">
                  <input value={n.separator ?? ""} onChange={(e) => set("separator", e.target.value)} />
                </Field>
              )}
              {n.mode === "template" && (
                <>
                  <Field label="Template">
                    <textarea
                      rows={6}
                      value={n.template || ""}
                      {...track}
                      onChange={(e) => set("template", e.target.value)}
                    />
                  </Field>
                  <VarChips node={n} />
                </>
              )}
              <p className="muted fl-help">
                Merges, in this order:{" "}
                {parents.map((p) => nodeOf(p)?.title || p).join(" → ") || "nothing yet; connect steps to it"}.
              </p>
              <Preview node={n} />
            </>
          )}
          {n.type === "loop" &&
            (() => {
              const g = loopInfo(fl().flow).gather[n.id];
              return (
                <>
                  <p className="muted fl-help">
                    Every step connected after this one runs once per item, up to{" "}
                    {g ? (
                      <b>{nodeOf(g)?.title || g}</b>
                    ) : (
                      <>
                        a <b>Gather</b> step (add one after the steps to repeat)
                      </>
                    )}
                    , which collects the results. In those steps use{" "}
                    <code className="inline-code" data-nofile>
                      {"{{item}}"}
                    </code>{" "}
                    (and{" "}
                    <code className="inline-code" data-nofile>
                      {"{{item.json.name}}"}
                    </code>{" "}
                    for JSON items),{" "}
                    <code className="inline-code" data-nofile>
                      {"{{item_index}}"}
                    </code>{" "}
                    and{" "}
                    <code className="inline-code" data-nofile>
                      {"{{item_count}}"}
                    </code>
                    . A tool or AI prompt connected after the loop gets the current item. With loops inside
                    loops, use the loop's step id:{" "}
                    <code className="inline-code" data-nofile>{`{{${stepRef(fl().flow, n.id)}.item}}`}</code>,{" "}
                    <code
                      className="inline-code"
                      data-nofile
                    >{`{{${stepRef(fl().flow, n.id)}.item.json.name}}`}</code>
                    ,{" "}
                    <code
                      className="inline-code"
                      data-nofile
                    >{`{{${stepRef(fl().flow, n.id)}.item_index}}`}</code>
                    .
                  </p>
                  <LoopListFrom n={n} set={set} />
                  <Field label="Split the list into items">
                    <select value={n.split || "auto"} onChange={(e) => set("split", e.target.value)}>
                      {SPLIT_MODES.map(([v, l]) => (
                        <option key={v} value={v}>
                          {l}
                        </option>
                      ))}
                    </select>
                  </Field>
                  {n.split === "regex" && (
                    <Field
                      label="Pattern"
                      hint="Each match is an item (its first group, if it has one), e.g. class (\w+)"
                    >
                      <input
                        value={n.expr || ""}
                        style={{ fontFamily: "var(--font-mono)" }}
                        {...track}
                        onChange={(e) => set("expr", e.target.value)}
                      />
                    </Field>
                  )}
                  <div className="fl-row">
                    <Field label="Keep only items matching" hint="A regular expression; empty keeps all">
                      <input
                        value={n.filter || ""}
                        style={{ fontFamily: "var(--font-mono)" }}
                        placeholder="e.g. ^[A-Z]"
                        onChange={(e) => set("filter", e.target.value)}
                      />
                    </Field>
                    <Field label="At most" hint="items (up to 1000)">
                      <input
                        type="number"
                        min={1}
                        max={1000}
                        value={n.max || ""}
                        placeholder="1000"
                        onChange={(e) => set("max", Math.max(0, Math.min(1000, +e.target.value || 0)))}
                      />
                    </Field>
                  </div>
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={!!n.unique}
                      onChange={(e) => set("unique", e.target.checked)}
                    />{" "}
                    Skip repeated items
                  </label>
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={!!n.continueOnError}
                      onChange={(e) => set("continueOnError", e.target.checked)}
                    />{" "}
                    Keep going when an item fails (its error is gathered)
                  </label>
                  <details>
                    <summary className="muted">The list (default: the output of the step before)</summary>
                    <Field
                      label="Use instead"
                      hint="A template, e.g. {{steps.symbols.json.items}} or one name per line"
                    >
                      <textarea
                        rows={3}
                        value={n.source || ""}
                        placeholder="{{steps.…}}"
                        {...track}
                        onChange={(e) => set("source", e.target.value)}
                      />
                    </Field>
                    <VarChips node={n} />
                  </details>
                  <Preview node={n} />
                </>
              );
            })()}
          {n.type === "gather" && (
            <>
              <p className="muted fl-help">
                Ends the loop{" "}
                {(() => {
                  const li = loopInfo(fl().flow),
                    l = Object.keys(li.gather).find((k) => li.gather[k] === n.id);
                  return l ? <b>{nodeOf(l)?.title || l}</b> : <i>(connect it after a loop's steps)</i>;
                })()}
                : for each item it takes the result of the steps connected into it, then gathers them all. The
                steps after it run once, with everything.
              </p>
              <Field label="Gather as">
                <select value={n.mode || "concat"} onChange={(e) => set("mode", e.target.value)}>
                  {GATHER_MODES.map(([v, l]) => (
                    <option key={v} value={v}>
                      {l}
                    </option>
                  ))}
                </select>
              </Field>
              {(n.mode || "concat") === "concat" && (
                <Field label="Separator" hint="\n is a new line (default: a blank line)">
                  <input value={n.separator ?? ""} onChange={(e) => set("separator", e.target.value)} />
                </Field>
              )}
              {n.mode === "template" && (
                <>
                  <Field
                    label="Template for each item"
                    hint="{{input}} is the item's result; joined with new lines"
                  >
                    <textarea
                      rows={4}
                      value={n.template || ""}
                      placeholder="- **{{item}}**: {{input}}"
                      {...track}
                      onChange={(e) => set("template", e.target.value)}
                    />
                  </Field>
                  <VarChips node={n} extra={["input", "item", "item_index", "item_count"]} />
                </>
              )}
              <Preview node={n} />
            </>
          )}
          {n.type === "transform" && (
            <>
              <Field label="Transformation">
                <select value={n.op || ""} onChange={(e) => set("op", e.target.value)}>
                  {Object.entries(opGroups).map(([g, list]) => (
                    <optgroup key={g} label={g}>
                      {list.map(([id, o]) => (
                        <option key={id} value={id}>
                          {o.name}
                        </option>
                      ))}
                    </optgroup>
                  ))}
                </select>
              </Field>
              {op.help && <p className="muted fl-help">{op.help}</p>}
              {op.expr && (
                <Field label={op.expr}>
                  <input
                    list={/^json-(path|template|stringify|parse)$/.test(n.op) ? "paths-" + n.id : undefined}
                    data-path={/^json-(path|template|stringify|parse)$/.test(n.op) ? "1" : undefined}
                    value={n.expr || ""}
                    style={{ fontFamily: "var(--font-mono)" }}
                    {...track}
                    onChange={(e) => set("expr", e.target.value)}
                  />
                </Field>
              )}
              {op.replace && (
                <Field label="Replace with">
                  <input
                    value={n.replace || ""}
                    style={{ fontFamily: "var(--font-mono)" }}
                    onChange={(e) => set("replace", e.target.value)}
                  />
                </Field>
              )}
              {op.template && (
                <>
                  <Field label={op.ai ? "Instruction for the AI" : "Template"}>
                    <textarea
                      value={n.template || ""}
                      placeholder={
                        op.ai
                          ? "e.g. Turn {{input}} into a bulleted list of acceptance criteria."
                          : n.op === "json-template"
                            ? '{\n  "customer": "{{this.customer.name}}",\n  "orders": { "{{#each this.orders}}": {\n    "id": "{{this.id}}",\n    "lines": { "{{#each this.lines}}": {\n      "sku": "{{this.sku}}",\n      "order": "{{parent.id}}",\n      "n": "{{this_index}}"\n    } }\n  } }\n}'
                            : "{{input}}"
                      }
                      style={n.op === "json-template" ? { fontFamily: "var(--font-mono)" } : undefined}
                      rows={n.op === "json-template" ? 12 : 5}
                      {...track}
                      onChange={(e) => set("template", e.target.value)}
                    />
                  </Field>
                  <VarChips
                    node={n}
                    extra={n.op === "json-template" ? ["this", "this_index", "parent", "root"] : ["input"]}
                  />
                </>
              )}
              <details>
                <summary className="muted">Input (default: the output of the step before)</summary>
                <Field label="Use instead">
                  <input
                    value={n.source || ""}
                    placeholder="{{steps.…}}"
                    {...track}
                    onChange={(e) => set("source", e.target.value)}
                  />
                </Field>
              </details>
              {!op.ai && <Preview node={n} />}
            </>
          )}
          {n.type === "output" && (
            <>
              <Field label="Format">
                <div className="seg">
                  {FORMATS.map(([v, l]) => (
                    <button
                      key={v}
                      type="button"
                      aria-pressed={n.format === v}
                      onClick={() => set("format", v)}
                    >
                      {l}
                    </button>
                  ))}
                </div>
              </Field>
              <Field
                label="Content"
                hint="Empty: the output of the steps before. JSON, CSV, Markdown and HTML are converted to the format."
              >
                <textarea
                  rows={4}
                  value={n.template || ""}
                  placeholder="{{steps.…}}"
                  {...track}
                  onChange={(e) => set("template", e.target.value)}
                />
              </Field>
              <VarChips node={n} />
              <h4>Actions</h4>
              {(n.actions ||= []).map((a, k) => (
                <div key={k} className="fl-action">
                  <div className="fl-row">
                    <select
                      value={a.type}
                      onChange={(e) => {
                        a.type = e.target.value;
                        changed();
                      }}
                    >
                      <option value="save">Save to a file</option>
                      <option value="tool">Call a tool</option>
                      <option value="clipboard">Copy to the clipboard</option>
                      <option value="show">Show the result</option>
                    </select>
                    <button
                      className="icon-btn fl-del"
                      title="Remove action"
                      onClick={() => {
                        n.actions.splice(k, 1);
                        changed();
                      }}
                    >
                      <Icon.x />
                    </button>
                  </div>
                  {a.type === "save" && (
                    <Field
                      label="File (in the project)"
                      hint={`e.g. out/{{inputs.ticket}}.${n.format || "md"}`}
                    >
                      <input
                        value={a.path || ""}
                        style={{ fontFamily: "var(--font-mono)" }}
                        {...track}
                        onChange={(e) => {
                          a.path = e.target.value;
                          changed();
                        }}
                      />
                    </Field>
                  )}
                  {a.type === "tool" && (
                    <>
                      <Field label="Tool">
                        <select
                          value={a.tool || ""}
                          onChange={(e) => {
                            a.tool = e.target.value;
                            a.args = {};
                            changed();
                          }}
                        >
                          <option value="">Choose…</option>
                          <ToolOptions />
                        </select>
                      </Field>
                      {a.tool && (
                        <ArgsEditor
                          node={n}
                          args={(a.args ||= {})}
                          toolName={a.tool}
                          extraVars={["output"]}
                        />
                      )}
                    </>
                  )}
                </div>
              ))}
              <div className="row-actions">
                {[
                  ["save", "Save"],
                  ["tool", "Call tool"],
                  ["clipboard", "Copy"],
                  ["show", "Show"],
                ].map(([t, l]) => (
                  <button
                    key={t}
                    className="btn secondary"
                    onClick={() => {
                      n.actions.push({
                        type: t,
                        ...(t === "save" ? { path: `out/${flow.id}.${n.format || "md"}` } : {}),
                      });
                      changed();
                    }}
                  >
                    <Icon.plus /> {l}
                  </button>
                ))}
              </div>
              <Preview node={n} />
            </>
          )}
          <label className="check fl-review">
            <input type="checkbox" checked={!!n.review} onChange={(e) => set("review", e.target.checked)} />{" "}
            Pause for review after this step{" "}
            <span className="muted">(accept, refine, run a command or stop)</span>
          </label>
          <Field label="Note">
            <textarea
              rows={2}
              value={n.note || ""}
              placeholder="Why this step is here"
              onChange={(e) => set("note", e.target.value)}
            />
          </Field>
        </>
      )}
    </>
  );
}

export function Inspector() {
  const f = fl(),
    n = f.sel ? nodeOf(f.sel) : null,
    e = !n && f.selEdge != null ? f.flow.edges[f.selEdge] : null,
    many = selectedIds();
  if (S.layout["flows:insp:c"])
    return (
      <aside className="fl-inspector is-collapsed" aria-label="Inspector">
        <button
          className="icon-btn"
          aria-label="Show the inspector"
          title="Show the inspector (⌘\)"
          onClick={() => togglePane("flows:insp:c")}
        >
          <Icon.panelR />
        </button>
      </aside>
    );
  return (
    <aside className="fl-inspector" aria-label="Inspector">
      <button
        className="icon-btn fl-collapse fl-insp-collapse"
        aria-label="Hide the inspector"
        title="Hide the inspector (⌘\)"
        onClick={() => togglePane("flows:insp:c")}
      >
        <Icon.panelR />
      </button>
      {many.length > 1 ? (
        <MultiInspector ids={many} />
      ) : n ? (
        <StepInspector key={n.id} n={n} />
      ) : e ? (
        <EdgeInspector key={f.selEdge} e={e} />
      ) : (
        <FlowSettings key={f.flow.id} />
      )}
    </aside>
  );
}

// Improve until done: pairs with the tool step before it (runs it again with
// changed arguments) or after it (the tool checks the AI's result).
function ImproveInspector({ n, set, flow }) {
  const after = n.pair === "after";
  const partner = after
    ? flow.edges.filter((e) => e.from === n.id).map((e) => e.to)
    : parentsOf(flow, n.id).filter((p) => p !== "start");
  const pn = partner.length === 1 ? flow.nodes.find((x) => x.id === partner[0]) : null;
  const ok = pn?.type === "tool";
  return (
    <>
      <Field label="Works with">
        <div className="seg">
          <button type="button" aria-pressed={!after} onClick={() => set("pair", "before")}>
            The tool before it
          </button>
          <button type="button" aria-pressed={after} onClick={() => set("pair", "after")}>
            The tool after it
          </button>
        </div>
      </Field>
      <p className="muted fl-builtin-desc">
        {after
          ? "The AI produces a result; the tool connected after this step checks it (tests, a linter, a validator). The tool's findings go back to the AI, which revises the result. Round after round, until it is done."
          : "The tool connected into this step runs; the AI judges its result and, when it is not good enough, changes the tool's arguments (or the input it gets) and runs it again. Round after round, until it is done."}{" "}
        {ok ? (
          <span>
            Paired with <b>{pn.title || pn.tool}</b>.
          </span>
        ) : (
          <span className="danger-text">
            {after ? "Connect this step to one tool step." : "Connect one tool step into this step."}
          </span>
        )}
      </p>
      <Field
        label={after ? "What should the AI produce?" : "Instructions (optional)"}
        hint="The flow's context and skills are added automatically."
      >
        <textarea
          rows={after ? 6 : 3}
          value={n.prompt || ""}
          placeholder={
            after
              ? "e.g. Write a SQL migration that adds {{inputs.column}} to the orders table"
              : "e.g. Prefer narrowing the date range over raising the limit"
          }
          {...track}
          onChange={(e) => set("prompt", e.target.value)}
        />
      </Field>
      <Field label="Done when" hint="The AI judges every round against this.">
        <textarea
          rows={3}
          value={n.goal || ""}
          placeholder={
            after
              ? "e.g. The check reports no errors and the migration can be rolled back"
              : "e.g. The query returns rows, and every row has a customer id"
          }
          {...track}
          onChange={(e) => set("goal", e.target.value)}
        />
      </Field>
      <VarChips node={n} />
      <div className="fl-row">
        <Field label="At most" hint="rounds (1–20)">
          <input
            type="number"
            min={1}
            max={20}
            value={n.rounds || 5}
            onChange={(e) => set("rounds", Math.max(1, Math.min(20, +e.target.value || 5)))}
          />
        </Field>
        <Field label="Who decides it is done">
          <select value={n.approve || "done"} onChange={(e) => set("approve", e.target.value)}>
            <option value="none">The AI alone</option>
            <option value="done">The AI, then a person confirms</option>
            <option value="each">A person, after every round</option>
          </select>
        </Field>
      </div>
      <small className="muted">
        A person can approve the result, or send feedback for another round. Runs started with reviews off let
        the AI decide.
      </small>
      <label className="check">
        <input
          type="checkbox"
          checked={!!n.continueOnError}
          onChange={(e) => set("continueOnError", e.target.checked)}
        />{" "}
        When it never gets there, go on with the last result (otherwise the step fails)
      </label>
      {after && <ExpectedReply n={n} set={set} />}
      <div className="field fl-field">
        <span className="muted">Model</span>
        <ModelPicker value={n.model || ""} onChange={(v) => set("model", v)} label="Model" />
      </div>
      <small className="muted">
        Each round the AI gets the goal, the current result and findings, and a short record of the earlier
        rounds, not their full text, so long runs stay within its context window. Long results are cut (start
        and end kept) and saved in the run's results folder, where the AI can read them whole.
      </small>
    </>
  );
}

function MultiInspector({ ids }) {
  const f = fl(),
    flow = f.flow,
    set = new Set(ids),
    inner = flow.edges.filter((e) => set.has(e.from) && set.has(e.to)).length;
  return (
    <>
      <div className="fl-insp-head">
        <div>
          <h3>{ids.length} steps selected</h3>
          <small className="muted">
            {inner} connection{inner === 1 ? "" : "s"} between them
          </small>
        </div>
        <button className="link" style={{ marginLeft: "auto" }} onClick={removeSelected}>
          Delete
        </button>
      </div>
      <ul className="fl-multi">
        {ids.map((id) => {
          const n = nodeOf(id);
          return (
            <li key={id}>
              <StepIcon n={n} tools={tools()} size={22} /> <b>{n.title || n.id}</b>{" "}
              <span className="muted">{n.type === "tool" ? toolRef(n.tool) : n.type}</span>
              <button
                className="link"
                title="Remove from the selection"
                onClick={() => setSelection(ids.filter((x) => x !== id))}
              >
                ✕
              </button>
            </li>
          );
        })}
      </ul>
      <div className="row-actions">
        <button className="btn primary" onClick={() => copySteps()}>
          Copy
        </button>
        <button
          className="btn secondary"
          onClick={() => {
            copySteps();
            pasteSteps(S.flowClip);
          }}
        >
          Duplicate
        </button>
        <button
          className="btn secondary"
          onClick={() => {
            copySteps();
            pasteAsNewFlow(S.flowClip);
          }}
        >
          Copy to a new flow
        </button>
      </div>
      <p className="muted fl-help">
        Drag one of them to move them all. ⌘C copies them with the connections between them (and the inputs
        they use); ⌘V pastes them into this flow, or open another flow and paste there. ⇧-click or ⌘-click
        adds or removes a step; drawing a box on the empty canvas selects the steps it touches; ⌘A selects
        all; Esc clears.
      </p>
    </>
  );
}

// --- data shapes: what each step gets and gives ------------------------------------

const SHAPE_SOURCE = {
  declared: ["Declared", "badge-ok", "declared by the tool, the expected reply or the flow's inputs"],
  derived: ["Worked out", "badge-info", "worked out from the steps before it"],
  sample: ["Learned", "badge-warn", "learned from sample data or the flow's last run; declare it to be sure"],
  unknown: ["Not known yet", "", ""],
};
const jsonPathVar = (base, p) => `${base}.buffer_output.json${p.startsWith("[") ? "" : "."}${p}`;

// a field list; clicking a field puts it where the cursor is
function ShapeTree({ schema, pick, max = 40 }) {
  const [all, setAll] = useState(false);
  if (!schema) return null;
  const ps = schemaPaths(schema, 400);
  if (!ps.length)
    return (
      <p className="muted fl-help">
        {typeName(schema) === "string" ? "Text (not JSON)." : "A " + typeName(schema) + " value."}
        {schema.description ? " " + schema.description : ""}
      </p>
    );
  const shown = all ? ps : ps.slice(0, max);
  return (
    <div className="fl-shape">
      {shown.map((p) => (
        <button
          key={p.path}
          type="button"
          className="fl-shape-row"
          style={{ paddingLeft: 6 + p.depth * 14 }}
          title={(p.description ? p.description + " · " : "") + p.path + " · click to use it"}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => pick(p.path)}
        >
          <code>{p.path.split(".").pop()}</code>
          <span className="fl-shape-type">{p.type}</span>
          {p.description && <small className="muted">{p.description}</small>}
        </button>
      ))}
      {ps.length > max && (
        <button className="link" onClick={() => setAll(!all)}>
          {all ? "Show fewer" : `Show all ${ps.length} fields`}
        </button>
      )}
    </div>
  );
}

function DataPanel({ n }) {
  const f = fl(),
    flow = f.flow,
    sh = f.shapes?.[n.id];
  if (!sh || n.type === "start") return null;
  const parents = parentsOf(flow, n.id),
    one = parents.length === 1 ? parents[0] : null;
  const inLoop = !!one && nodeOf(one)?.type === "loop";
  const rowsStep = n.type === "filter" || n.type === "aggregate",
    dataStep = rowsStep || n.type === "decision";
  const gets = rowsStep ? sh.rows : n.type === "loop" ? sh.item : sh.in;
  // a field of what the step gets: into a path field as it is, into a template as a variable
  const pickIn = (p) => {
    const el = lastField,
      isPath = el && document.body.contains(el) ? el.dataset?.path : "";
    if (isPath)
      return insertText(
        isPath === "list" && el.value.trim() ? el.value.replace(/,?\s*$/, ", ") + p : p,
        true,
      );
    if (dataStep || n.type === "loop") return insertVar("this." + p);
    if (inLoop) return insertVar("item.json" + (p.startsWith("[") ? "" : ".") + p);
    if (one) return insertVar(jsonPathVar(one, p));
    toast("Connect one step into this one to use its fields.");
  };
  const pickOut = (p) => {
    const v = `{{${jsonPathVar(n.id, p)}}}`;
    navigator.clipboard?.writeText(v).catch(() => {});
    toast(`Copied ${v}: paste it into a later step.`);
  };
  const [label, cls, why] = SHAPE_SOURCE[sh.source] || SHAPE_SOURCE.unknown;
  const unknownHint =
    n.type === "tool" ? (
      <>
        Declare the tool's output on the{" "}
        <button
          className="link"
          onClick={() => {
            S.view = "tools";
            update();
          }}
        >
          Tools
        </button>{" "}
        screen, or give sample output (Try it with sample data) or run the flow once to learn it.
      </>
    ) : n.type === "ai" ? (
      "Set the expected reply above, or run the flow once to learn it."
    ) : (
      "Connect a step whose output is known."
    );
  return (
    <details className="fl-data" open>
      <summary>
        <b>Data</b> <span className="muted">the fields this step gets and gives</span>
      </summary>
      <datalist id={"paths-" + n.id}>
        {(gets ? schemaPaths(gets, 300) : []).map((p) => (
          <option key={p.path} value={p.path}>
            {p.type}
            {p.description ? " · " + p.description : ""}
          </option>
        ))}
      </datalist>
      <div className="fl-data-sec">
        <div className="fl-data-head">
          {rowsStep ? "Each row it gets" : n.type === "loop" ? "Each item" : "Gets"}
          {one && <small className="muted">from {nodeOf(one)?.title || one}</small>}
        </div>
        {gets ? (
          <>
            <code className="fl-compact" title="The shape, compactly">
              {compact(gets)}
            </code>
            <ShapeTree schema={gets} pick={pickIn} />
            <small className="muted">
              Click a field to use it where you were typing
              {dataStep || n.type === "loop"
                ? " (as a path in a field box, else as {{this.<field>}})"
                : inLoop
                  ? " ({{item.json.<field>}})"
                  : one
                    ? ` ({{${one}.buffer_output.json.<field>}})`
                    : ""}
              .
            </small>
          </>
        ) : (
          <p className="muted fl-help">
            {parents.length
              ? "Not known yet: " +
                (parents.length > 1
                  ? "several steps are connected, and their outputs arrive one after another as text."
                  : "the step before has no known shape.")
              : "Nothing is connected into it."}
          </p>
        )}
      </div>
      {n.type !== "output" && (
        <div className="fl-data-sec">
          <div className="fl-data-head">
            Gives{" "}
            <span className={"badge " + cls} title={why}>
              {label}
            </span>
          </div>
          {sh.out ? (
            <>
              <code className="fl-compact">{compact(sh.out)}</code>
              <ShapeTree schema={sh.out} pick={pickOut} />
              <small className="muted">
                Later steps read a field as{" "}
                <code className="inline-code" data-nofile>{`{{${n.id}.buffer_output.json.<field>}}`}</code>;
                click one to copy it.
              </small>
            </>
          ) : (
            <p className="muted fl-help">{unknownHint}</p>
          )}
        </div>
      )}
    </details>
  );
}

// An AI step's expected reply: shown to later steps and, only when set, sent to the AI.
function ExpectedReply({ n, set }) {
  const [text, setText] = useState(n.outputSchema ? JSON.stringify(n.outputSchema, null, 2) : "");
  const apply = async () => {
    if (!text.trim()) {
      if (n.outputSchema) set("outputSchema", undefined);
      return;
    }
    try {
      const s = await backend.ParseSchema(text);
      set("outputSchema", s);
      setText(JSON.stringify(s, null, 2));
    } catch (e) {
      toast("Expected reply: " + errMsg(e));
    }
  };
  return (
    <details className="fl-expect" open={!!n.outputSchema}>
      <summary>
        <b>Expected reply</b>{" "}
        <span className="muted">
          {n.outputSchema ? compact(n.outputSchema) : "optional: JSON the next steps can rely on"}
        </span>
      </summary>
      <p className="muted fl-help">
        Paste an example of the JSON you want back, or a JSON Schema. Later steps then know its fields, and
        the AI is asked to reply with only JSON in this shape. It is the only shape ever sent to the AI, so
        leave it empty for free-form answers.
      </p>
      <textarea
        rows={6}
        value={text}
        placeholder={'{"type": "bug", "title": "Checkout crashes", "priority": 2}'}
        style={{ fontFamily: "var(--font-mono)" }}
        onChange={(e) => setText(e.target.value)}
        onBlur={apply}
      />
      {n.outputSchema && (
        <div className="row-actions">
          <button
            className="link"
            onClick={() => {
              setText("");
              set("outputSchema", undefined);
            }}
          >
            Clear
          </button>
        </div>
      )}
    </details>
  );
}

// The flow's interface: its inputs and the shapes of its outputs, for whoever integrates it.
function FlowInterface() {
  const f = fl(),
    flow = f.flow,
    sh = f.shapes || {};
  const outs = flow.nodes.filter((n) => n.type === "output");
  const iface = () => ({
    flow: flow.id,
    inputs: sh.start?.out || { type: "object" },
    outputs: Object.fromEntries(
      outs.map((n) => [
        n.title || n.id,
        { format: n.format, ...(n.format === "json" && sh[n.id]?.out ? { schema: sh[n.id].out } : {}) },
      ]),
    ),
  });
  return (
    <details className="fl-data">
      <summary>
        <b>Interface</b> <span className="muted">inputs and outputs, for integrating this flow</span>
      </summary>
      <div className="fl-data-sec">
        <div className="fl-data-head">Inputs</div>
        {flow.inputs.length ? (
          <code className="fl-compact">{compact(sh.start?.out)}</code>
        ) : (
          <p className="muted fl-help">No inputs.</p>
        )}
      </div>
      <div className="fl-data-sec">
        <div className="fl-data-head">Outputs</div>
        {outs.map((n) => (
          <div key={n.id}>
            <b>{n.title || n.id}</b> <span className="muted">{(n.format || "").toUpperCase()}</span>
            {n.format === "json" && (
              <code className="fl-compact">
                {sh[n.id]?.out ? compact(sh[n.id].out) : "shape not known yet"}
              </code>
            )}
          </div>
        ))}
        {!outs.length && <p className="muted fl-help">No outputs yet.</p>}
      </div>
      <button
        className="btn secondary small"
        onClick={() => {
          navigator.clipboard?.writeText(JSON.stringify(iface(), null, 2)).catch(() => {});
          toast("Copied the flow's interface as JSON Schema.");
        }}
      >
        Copy as JSON Schema
      </button>
    </details>
  );
}

// --- a loop's list ------------------------------------------------------------------

// A Loop connected to the Inputs step gets all the inputs as one JSON object:
// take the list from the input that holds one (several lines, picks, files).
export function loopFromInputs(a, n) {
  if (a?.type !== "start" || n?.type !== "loop" || (n.field || "").trim() || (n.source || "").trim())
    return "";
  const ins = fl().flow.inputs || [];
  if (!ins.length) return "";
  const i =
    ins.find((x) => ["multiline", "multichoice", "files"].includes(x.type)) ||
    ins.find((x) => x.type !== "secret");
  if (!i) return "";
  n.field = i.name;
  return `The loop goes through the input “${i.label || i.name}”, one item per line. Change it in “List from”.`;
}

function LoopListFrom({ n, set }) {
  const f = fl(),
    sh = f.shapes?.[n.id],
    obj = sh?.in?.type === "object" ? sh.in : null;
  const ps = obj ? schemaPaths(obj, 200) : [];
  return (
    <Field
      label="List from"
      hint={
        obj
          ? "The input is a JSON object: the field that holds the list. Empty: its only field, or its only list."
          : "For JSON input: the path of the list, e.g. items or data.rows. Empty: the whole input (or an object's only field)."
      }
    >
      <input
        list={"listpaths-" + n.id}
        data-path="1"
        {...track}
        value={n.field || ""}
        placeholder={obj ? ps.find((p) => p.depth === 0)?.path || "field" : "e.g. data.items"}
        style={{ fontFamily: "var(--font-mono)" }}
        onChange={(e) => set("field", e.target.value)}
      />
      <datalist id={"listpaths-" + n.id}>
        {ps.map((p) => (
          <option key={p.path} value={p.path}>
            {p.type}
            {p.description ? " · " + p.description : ""}
          </option>
        ))}
      </datalist>
    </Field>
  );
}
