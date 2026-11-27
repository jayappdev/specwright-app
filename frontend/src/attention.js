// What needs you, and what is running, from every part of the app: the
// Home screen lists them, the sidebar's Home item counts them. One place, so
// the two never disagree. Each item says where to go to deal with it.
import { S, update } from "./store.js";

const go = (view, then) => () => {
  S.view = view;
  update();
  then?.();
};

// the spec workflow's step waiting at its approval gate
function gateItem() {
  const wf = S.wf;
  const k = wf?.steps.findIndex((s) => s.status === "paused") ?? -1;
  if (k < 0) return null;
  return {
    id: "gate",
    kind: "approval",
    title: `Approve step ${k + 1}: ${wf.steps[k].title}`,
    detail: wf.name + " is paused at its approval gate.",
    action: "Review",
    go: go("workflow", () => {
      S.sel = k;
      update();
    }),
  };
}

// needsYou lists what waits for a person, most urgent first.
export function needsYou() {
  const out = [];
  const g = gateItem();
  if (g) out.push(g);
  for (const r of Object.values(S.flowRuns || {})) {
    if (r?.status !== "review") continue;
    out.push({
      id: "review:" + r.id,
      kind: "review",
      title: `Review ${r.flowName || "a flow run"}`,
      detail: r.review?.title ? `Paused at ${r.review.title}.` : "Paused for your review.",
      action: "Open",
      go: () => import("./views/FlowRun.jsx").then((m) => m.showFlowRun(r.id)),
    });
  }
  for (const r of S.home?.runs || []) {
    if (r.state !== "review" || S.flowRuns?.[r.id]) continue;
    out.push({
      id: "review:" + r.id,
      kind: "review",
      title: `Review ${r.title || r.workflow}`,
      detail: "Paused for your review.",
      action: "Open",
      go: () => import("./views/FlowRun.jsx").then((m) => m.showFlowRun(r.id)),
    });
  }
  const tools = (S.ws?.extensions || []).filter(
    (t) => t.status === "needs approval" || t.status === "in review",
  );
  if (tools.length)
    out.push({
      id: "tools",
      kind: "tools",
      title:
        tools.length === 1
          ? `Approve the tool ${tools[0].name}`
          : `${tools.length} tools wait for your approval`,
      detail: "Tools run only once you have read and approved them.",
      action: "Open Tools",
      go: go("tools"),
    });
  return out;
}

// runningNow lists what is working right now.
export function runningNow() {
  const out = [];
  const wf = S.wf;
  const k = wf?.steps.findIndex((s) => s.status === "running") ?? -1;
  if (k >= 0)
    out.push({
      id: "wf",
      title: `${wf.name}: ${wf.steps[k].title}`,
      detail: "Spec workflow step running",
      go: go("workflow"),
    });
  for (const r of Object.values(S.flowRuns || {})) {
    if (r?.status !== "running") continue;
    out.push({
      id: "run:" + r.id,
      title: r.flowName || "Flow run",
      detail: r.options?.chat?.role === "setup" ? "Setting up a chat" : "Flow running",
      go: () => import("./views/FlowRun.jsx").then((m) => m.showFlowRun(r.id)),
    });
  }
  for (const k of Object.keys(S.chat?.live || {})) {
    const [flow, id] = k.split("/");
    const s = S.chat.sessions?.find((x) => x.flow === flow && x.id === id);
    out.push({
      id: "chat:" + k,
      title: s?.title || "A chat",
      detail: "Answering in " + (s?.flowName || flow),
      go: () => import("./views/Chats.jsx").then((m) => m.openChatSession(flow, id)),
    });
  }
  return out;
}
