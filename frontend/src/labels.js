// Status and kind labels, in one place. The words differ on purpose by
// context: a workflow step is "Approved" or "Rejected" (a person decided);
// a flow run "Completed" or "Failed"; a prompt "Completed" or "Stopped".

// workflow steps (and the default for Pill)
export const STEP_STATUS_LABEL = {
  pending: "Pending",
  running: "Running",
  paused: "Waiting",
  done: "Approved",
  failed: "Rejected",
  idle: "Idle",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
};

// flow runs and their steps
export const FLOW_STATUS_LABEL = {
  running: "Running",
  review: "Waiting for you",
  scheduled: "Scheduled",
  done: "Completed",
  failed: "Failed",
  stopped: "Stopped",
  interrupted: "Interrupted",
  pending: "Pending",
  skipped: "Skipped",
};

// playground prompts
export const PROMPT_STATUS_LABEL = {
  done: "Completed",
  failed: "Failed",
  cancelled: "Stopped",
  running: "Running",
};

// kinds of run: short (History list) and long (Playground thread)
export const RUN_KIND = { playground: "Ask AI", workflow: "Workflow", flow: "Flow" };

export const RUN_KIND_LONG = { playground: "Ask AI question", workflow: "Workflow step", flow: "Flow run" };

// the kind of a stored run
export const runKind = (r) =>
  r.kind || (r.step === "flow" ? "flow" : r.workflow === "playground" ? "playground" : "workflow");
