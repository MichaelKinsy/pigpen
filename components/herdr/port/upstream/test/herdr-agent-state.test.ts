import { afterEach, expect, test } from "bun:test";
import { rm } from "node:fs/promises";
import { createServer, type Server } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

const MODULE = "../extensions/herdr-agent-state.ts";
const ENV_KEYS = ["HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"] as const;
const savedEnv = Object.fromEntries(ENV_KEYS.map((key) => [key, process.env[key]]));

let server: Server | undefined;
let socketFile: string | undefined;
let importCounter = 0;

afterEach(async () => {
  await new Promise<void>((resolve) => (server ? server.close(() => resolve()) : resolve()));
  server = undefined;
  if (socketFile) {
    await rm(socketFile, { force: true });
    socketFile = undefined;
  }
  for (const key of ENV_KEYS) {
    if (savedEnv[key] === undefined) delete process.env[key];
    else process.env[key] = savedEnv[key];
  }
});

// The extension reads its environment at import time, so each test imports a fresh copy.
function importFresh() {
  importCounter += 1;
  return import(`${MODULE}?test=${importCounter}`);
}

async function startHerdr(): Promise<any[]> {
  const requests: any[] = [];
  // On Windows Herdr's socket is a named pipe, and HERDR_SOCKET_PATH holds its name.
  const name = `herdr-pig-test-${process.pid}-${Date.now()}-${importCounter}.sock`;
  const windows = process.platform === "win32";
  const endpoint = windows ? `\\\\.\\pipe\\${name}` : join(tmpdir(), name);
  socketFile = windows ? undefined : endpoint;
  server = createServer((socket) => {
    let buffer = "";
    socket.on("data", (chunk) => {
      buffer += chunk.toString();
      let newline = buffer.indexOf("\n");
      while (newline >= 0) {
        requests.push(JSON.parse(buffer.slice(0, newline)));
        buffer = buffer.slice(newline + 1);
        socket.write('{"result":{}}\n');
        newline = buffer.indexOf("\n");
      }
    });
  });
  await new Promise<void>((resolve) => server!.listen(endpoint, () => resolve()));
  process.env.HERDR_ENV = "1";
  process.env.HERDR_SOCKET_PATH = windows ? name : endpoint;
  process.env.HERDR_PANE_ID = "w1:p1";
  return requests;
}

function harness() {
  const handlers = new Map<string, (event?: any, ctx?: any) => any>();
  const busHandlers = new Map<string, (data?: any) => void>();
  const pi = {
    on: (name: string, handler: any) => handlers.set(name, handler),
    events: { on: (name: string, handler: any) => busHandlers.set(name, handler) },
  };
  return { handlers, busHandlers, pi };
}

function ctx(overrides: Record<string, unknown> = {}) {
  return {
    mode: "tui",
    isIdle: () => true,
    sessionManager: {
      getSessionFile: () => "/home/user/.pig/agent/sessions/one.jsonl",
      getSessionId: () => "session-one",
    },
    ...overrides,
  };
}

async function waitFor(requests: any[], count: number) {
  for (let i = 0; i < 200 && requests.length < count; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  expect(requests.length).toBe(count);
}

test("does nothing outside Herdr", async () => {
  delete process.env.HERDR_ENV;
  delete process.env.HERDR_SOCKET_PATH;
  delete process.env.HERDR_PANE_ID;
  const { handlers, pi } = harness();

  const { default: install } = await importFresh();
  install(pi);

  expect(handlers.size).toBe(0);
});

test("reports idle with a resume command when a TUI session starts", async () => {
  const requests = await startHerdr();
  const { handlers, pi } = harness();
  const { default: install } = await importFresh();
  install(pi);

  await handlers.get("session_start")!({ reason: "startup" }, ctx());
  await waitFor(requests, 1);

  expect(requests[0].method).toBe("pane.report_agent");
  expect(requests[0].params).toMatchObject({
    pane_id: "w1:p1",
    source: "pig",
    agent: "pig",
    state: "idle",
    resume_argv: ["pig", "--session", "/home/user/.pig/agent/sessions/one.jsonl"],
  });
});

test("reports working then idle across a turn with increasing seq", async () => {
  const requests = await startHerdr();
  const { handlers, pi } = harness();
  const { default: install } = await importFresh();
  install(pi);

  await handlers.get("session_start")!({}, ctx());
  await waitFor(requests, 1);
  handlers.get("agent_start")!({}, ctx());
  await waitFor(requests, 2);
  handlers.get("agent_settled")!({}, ctx());
  await waitFor(requests, 3);

  expect(requests.map((request) => request.params.state)).toEqual(["idle", "working", "idle"]);
  const seqs = requests.map((request) => request.params.seq);
  expect(seqs[1]).toBeGreaterThan(seqs[0]);
  expect(seqs[2]).toBeGreaterThan(seqs[1]);
});

test("stays silent in non-TUI modes", async () => {
  const requests = await startHerdr();
  const { handlers, pi } = harness();
  const { default: install } = await importFresh();
  install(pi);

  await handlers.get("session_start")!({}, ctx({ mode: "rpc" }));
  handlers.get("agent_start")!({}, ctx({ mode: "rpc" }));
  await new Promise((resolve) => setTimeout(resolve, 50));

  expect(requests.length).toBe(0);
});

test("reports blocked while another extension holds the herdr:blocked signal", async () => {
  const requests = await startHerdr();
  const { handlers, busHandlers, pi } = harness();
  const { default: install } = await importFresh();
  install(pi);

  await handlers.get("session_start")!({}, ctx());
  await waitFor(requests, 1);
  busHandlers.get("herdr:blocked")!({ active: true, label: "approve command" });
  await waitFor(requests, 2);
  busHandlers.get("herdr:blocked")!({ active: false });
  await waitFor(requests, 3);

  expect(requests[1].params).toMatchObject({ state: "blocked", message: "approve command" });
  expect(requests[2].params.state).toBe("idle");
});

test("releases the pane only when PiG quits", async () => {
  const requests = await startHerdr();
  const { handlers, pi } = harness();
  const { default: install } = await importFresh();
  install(pi);

  await handlers.get("session_start")!({}, ctx());
  await waitFor(requests, 1);
  await handlers.get("session_shutdown")!({ reason: "resume" });
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(requests.length).toBe(1);

  await handlers.get("session_shutdown")!({ reason: "quit" });
  await waitFor(requests, 2);

  expect(requests[1].method).toBe("pane.release_agent");
  expect(requests[1].params).toMatchObject({ pane_id: "w1:p1", source: "pig", agent: "pig" });
  expect(requests[1].params.seq).toBeGreaterThan(requests[0].params.seq);
});

test("builds a resume command Herdr will accept", async () => {
  const { resumeArgvFor, isSafeResumeArg } = await importFresh();
  const withSession = (file: unknown, id: unknown) => ({
    sessionManager: { getSessionFile: () => file, getSessionId: () => id },
  });

  expect(isSafeResumeArg("it's")).toBe(false);
  expect(isSafeResumeArg("line\nbreak")).toBe(false);
  expect(isSafeResumeArg("")).toBe(false);

  // An apostrophe in the path would make Herdr reject the whole report.
  expect(resumeArgvFor(withSession("/home/o'brien/s.jsonl", "abc"))).toEqual([
    "pig",
    "--session",
    "abc",
  ]);
  expect(resumeArgvFor(withSession("relative.jsonl", "abc"))).toEqual(["pig", "--session", "abc"]);
  expect(resumeArgvFor(withSession(undefined, undefined))).toBeUndefined();
  expect(
    resumeArgvFor({
      sessionManager: {
        getSessionFile: () => {
          throw new Error("no session");
        },
        getSessionId: () => "abc",
      },
    }),
  ).toEqual(["pig", "--session", "abc"]);
});
