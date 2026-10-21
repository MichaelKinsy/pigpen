// herdr-pig: reports PiG's state and resume command to Herdr.
//
// Installed by the herdr-pig plugin; reinstalling or updating overwrites this file.
// HERDR_PIG_EXTENSION_VERSION=1
//
// Licensed under the Apache License, Version 2.0:
// https://www.apache.org/licenses/LICENSE-2.0
// Copyright 2026 Hai-Fai Solutions. Based on the Pi integration in Herdr
// (https://github.com/herdrdev/herdr), copyright the Herdr authors, and
// modified to report PiG's state.

import net from "node:net";
import path from "node:path";

// Herdr reserves the `herdr:` source prefix for its own integrations, so this
// reports under a plain source and lets Herdr treat PiG as a self-reporting agent.
const SOURCE = "pig";
const AGENT = "pig";

const socketPath = process.env.HERDR_SOCKET_PATH;
const socketEndpoint =
  process.platform === "win32" && socketPath ? `\\\\.\\pipe\\${socketPath}` : socketPath;
const paneId = process.env.HERDR_PANE_ID;

function enabled(): boolean {
  return process.env.HERDR_ENV === "1" && !!socketPath && !!paneId;
}

function sendRequestAttempt(request: unknown, timeoutMs: number): Promise<boolean> {
  if (!enabled()) {
    return Promise.resolve(true);
  }

  return new Promise((resolve) => {
    let done = false;
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const finish = (delivered: boolean) => {
      if (done) return;
      done = true;
      if (timeout) {
        clearTimeout(timeout);
      }
      socket.destroy();
      resolve(delivered);
    };

    const socket = net.createConnection(socketEndpoint!);
    socket.on("error", () => finish(false));
    socket.on("connect", () => socket.write(`${JSON.stringify(request)}\n`));
    socket.on("data", () => finish(true));
    socket.on("end", () => finish(false));
    timeout = setTimeout(() => finish(false), timeoutMs);
    timeout.unref?.();
  });
}

async function sendRequest(request: unknown): Promise<void> {
  if (await sendRequestAttempt(request, 500)) {
    return;
  }
  await sendRequestAttempt(request, 1500);
}

type AgentState = "working" | "blocked" | "idle";

type QueuedState = {
  state: AgentState;
  message?: string;
  seq: number;
};

// Herdr drops reports whose seq is not higher than the last one it accepted,
// so the counter starts from the clock to stay increasing across restarts.
let reportSeq = Date.now() * 1000;
let resumeArgv: string[] | undefined;

function nextReportSeq(): number {
  reportSeq += 1;
  return reportSeq;
}

function requestId(kind: string): string {
  return `${SOURCE}:${kind}:${Date.now()}:${Math.random().toString(36).slice(2)}`;
}

// Herdr rejects a resume command whose arguments contain an apostrophe or a
// control character, and rejecting it drops the whole state report.
export function isSafeResumeArg(value: unknown): value is string {
  // biome-ignore lint/suspicious/noControlCharactersInRegex: the control characters are the point of this check
  return typeof value === "string" && value.length > 0 && !/['\u0000-\u001f\u007f]/.test(value);
}

export function resumeArgvFor(ctx: any): string[] | undefined {
  let file: unknown;
  let id: unknown;
  try {
    file = ctx?.sessionManager?.getSessionFile?.();
  } catch {
    file = undefined;
  }
  try {
    id = ctx?.sessionManager?.getSessionId?.();
  } catch {
    id = undefined;
  }

  const absoluteFile =
    typeof file === "string" && (path.posix.isAbsolute(file) || path.win32.isAbsolute(file));
  // The session file is unambiguous even with a custom session directory;
  // the id is the fallback for in-memory or oddly named paths.
  if (absoluteFile && isSafeResumeArg(file)) {
    return ["pig", "--session", file];
  }
  if (isSafeResumeArg(id)) {
    return ["pig", "--session", id];
  }
  return undefined;
}

function sendState(state: AgentState, message?: string, seq = nextReportSeq()): Promise<void> {
  const params: Record<string, unknown> = {
    pane_id: paneId,
    source: SOURCE,
    agent: AGENT,
    state,
    message,
    seq,
  };
  if (resumeArgv) {
    // Sent with the state so the same request both holds the pane and
    // attaches the command; older Herdr versions ignore the field.
    params.resume_argv = resumeArgv;
  }
  return sendRequest({ id: requestId("state"), method: "pane.report_agent", params });
}

function sendRelease(): Promise<void> {
  return sendRequest({
    id: requestId("release"),
    method: "pane.release_agent",
    params: { pane_id: paneId, source: SOURCE, agent: AGENT, seq: nextReportSeq() },
  });
}

let sendInFlight = false;
let queuedState: QueuedState | undefined;

// Only the latest state matters: a newer one replaces anything still queued.
function queueState(state: AgentState, message?: string): void {
  queuedState = { state, message, seq: nextReportSeq() };
  if (!sendInFlight) {
    void drainStateQueue();
  }
}

async function drainStateQueue(): Promise<void> {
  if (sendInFlight) {
    return;
  }

  sendInFlight = true;
  try {
    while (queuedState) {
      const next = queuedState;
      queuedState = undefined;
      await sendState(next.state, next.message, next.seq);
    }
  } finally {
    sendInFlight = false;
    if (queuedState) {
      void drainStateQueue();
    }
  }
}

// PiG passes its extension API here. It is untyped because PiG's types are not a dependency.
export default function (pi: any) {
  if (!enabled()) {
    return;
  }

  let agentActive = false;
  let blockedCount = 0;
  let blockedMessage: string | undefined;
  let lastState: AgentState | undefined;
  let lastMessage: string | undefined;
  let lastResume: string | undefined;
  let rootSession = false;

  function desiredState() {
    if (blockedCount > 0) {
      return { state: "blocked" as const, message: blockedMessage };
    }
    if (agentActive) {
      return { state: "working" as const, message: undefined };
    }
    return { state: "idle" as const, message: undefined };
  }

  function publishState(force = false) {
    const next = desiredState();
    const resume = resumeArgv?.join("\u0000");
    if (
      !force &&
      next.state === lastState &&
      next.message === lastMessage &&
      resume === lastResume
    ) {
      return;
    }
    lastState = next.state;
    lastMessage = next.message;
    lastResume = resume;
    queueState(next.state, next.message);
  }

  // Other extensions can mark the agent as waiting on the user by emitting
  // `herdr:blocked` with `{ active, label }`, the same convention Pi uses.
  pi.events.on("herdr:blocked", (data: any) => {
    if (!rootSession) {
      return;
    }
    if (!data?.active) {
      blockedCount = Math.max(0, blockedCount - 1);
      if (blockedCount === 0) {
        blockedMessage = undefined;
      }
      publishState();
      return;
    }

    blockedCount += 1;
    blockedMessage = data.label;
    publishState();
  });

  pi.on("session_start", (_event: any, ctx: any) => {
    // TUI only: print, JSON and RPC modes have no pane for Herdr to show.
    if (ctx?.mode !== "tui") {
      return;
    }
    rootSession = true;
    resumeArgv = resumeArgvFor(ctx);
    // A reload can replace this extension mid-run without another agent_start.
    agentActive = ctx?.isIdle?.() === false;
    publishState(true);
  });

  pi.on("agent_start", (_event: any, ctx: any) => {
    if (!rootSession) {
      return;
    }
    resumeArgv = resumeArgvFor(ctx);
    agentActive = true;
    publishState();
  });

  pi.on("agent_settled", (_event: any, ctx: any) => {
    if (!rootSession || ctx?.isIdle?.() !== true) {
      return;
    }
    agentActive = false;
    publishState();
  });

  pi.on("session_shutdown", async (event: any) => {
    // Switching or reloading a session keeps PiG in the pane, so only a real
    // quit releases it. Herdr clears a pane on its own if this never runs.
    if (!rootSession || event?.reason !== "quit") {
      return;
    }
    rootSession = false;
    queuedState = undefined;
    await sendRelease();
  });
}
