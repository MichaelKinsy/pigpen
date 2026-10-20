// Writes ../../extensions/herdr/testdata/drive-cases.json: the event sequences drive.mjs replays against the original
// (herdr-agent-state.ts, in Node, behind the stub pi of the original's own test) and the Go test replays against the port.
//
//   node gen-cases.mjs
//
// `fresh: true` on a start step is a session replacement under Pi: Pi shuts the old extension instance down, loads a fresh
// copy of the module (core/extensions/loader.ts moduleCache: false) and starts the new session on it, so the driver
// imports the module again; the Go port is one instance that session_start resets.
//
// A case: env (overrides of the herdr variables; null unsets), mode, ctx (the defaults of every event's ctx), server
// (how the fake herdr answers), steps and finalMs. A step is {op, ...}: start, agent_start, agent_settled, shutdown
// (reason), bus (data) or sleep (ms); `ctx` overrides the case's for that event; `waitMs` is the quiet time after it.
import { writeFileSync } from "node:fs";

const START = { op: "start" };
const cases = [];
const add = (name, description, spec) => cases.push({ name, description, ...spec });

// Outside herdr: nothing is registered, nothing is sent.
add("outside-herdr", "no herdr variables", { env: { HERDR_ENV: null, HERDR_SOCKET_PATH: null, HERDR_PANE_ID: null }, steps: [START, { op: "agent_start" }, { op: "shutdown", reason: "quit" }] });
add("env-not-1", "HERDR_ENV=0", { env: { HERDR_ENV: "0" }, steps: [START] });
add("env-true", "HERDR_ENV=true is not 1", { env: { HERDR_ENV: "true" }, steps: [START] });
add("no-pane-id", "HERDR_PANE_ID unset", { env: { HERDR_PANE_ID: null }, steps: [START] });
add("empty-pane-id", "HERDR_PANE_ID empty", { env: { HERDR_PANE_ID: "" }, steps: [START] });
add("no-socket-path", "HERDR_SOCKET_PATH unset", { env: { HERDR_SOCKET_PATH: null }, steps: [START] });
add("empty-socket-path", "HERDR_SOCKET_PATH empty", { env: { HERDR_SOCKET_PATH: "" }, steps: [START] });
add("bin-path-not-needed", "HERDR_BIN_PATH is not part of the original's contract", { env: { HERDR_BIN_PATH: null }, steps: [START] });
add("other-pane-id", "the pane id is sent as given", { env: { HERDR_PANE_ID: "w9:p3" }, steps: [START] });

// Modes.
for (const mode of ["rpc", "json", "print"]) {
  add(`mode-${mode}`, `a ${mode} session reports nothing, and does not release`, { mode, steps: [START, { op: "agent_start" }, { op: "agent_settled" }, { op: "bus", data: { active: true, label: "x" } }, { op: "shutdown", reason: "quit" }] });
}

// State.
add("startup-idle", "session_start reports idle with the resume command", { steps: [START] });
add("turn", "idle, working, idle", { steps: [START, { op: "agent_start" }, { op: "agent_settled" }] });
add("turn-repeated-events", "repeated events send nothing new", { steps: [START, { op: "agent_start" }, { op: "agent_start" }, { op: "agent_settled" }, { op: "agent_settled" }] });
add("start-twice", "session_start always reports", { steps: [START, START] });
add("start-mid-turn", "a reload mid-turn starts as working", { ctx: { idle: false }, steps: [START, { op: "agent_settled", ctx: { idle: true } }] });
add("settled-while-running", "agent_settled with isIdle() false is ignored", { steps: [START, { op: "agent_start" }, { op: "agent_settled", ctx: { idle: false } }] });
add("settled-idle-throws", "isIdle() throws at agent_settled", { steps: [START, { op: "agent_start" }, { op: "agent_settled", ctx: { idle: "throws" } }, { op: "agent_settled", ctx: { idle: true } }] });
add("start-idle-throws", "isIdle() throws at session_start: the handler fails, the session is still the root", { ctx: { idle: "throws" }, steps: [START, { op: "agent_start" }, { op: "agent_settled", ctx: { idle: true } }] });
add("events-before-start", "agent events before session_start are ignored", { steps: [{ op: "agent_start" }, { op: "agent_settled" }, { op: "bus", data: { active: true, label: "early" } }, { op: "shutdown", reason: "quit" }] });
add("resume-changes-mid-turn", "the same state is reported again when the resume command changed", { steps: [START, { op: "agent_start" }, { op: "agent_start", ctx: { file: "/home/user/.pig/agent/sessions/two.jsonl" } }, { op: "agent_start", ctx: { file: "/home/user/.pig/agent/sessions/two.jsonl" } }, { op: "agent_settled" }] });

// Resume command.
const resume = (name, description, ctx) => add(`resume-${name}`, description, { ctx, steps: [START] });
resume("posix-file", "an absolute posix file", { file: "/h/s.jsonl", id: "abc" });
resume("windows-file", "an absolute Windows file", { file: "C:\\Users\\u\\s.jsonl", id: "abc" });
resume("windows-forward", "a Windows file with forward slashes", { file: "D:/u/s.jsonl", id: "abc" });
resume("unc", "a UNC file", { file: "\\\\srv\\share\\s.jsonl", id: "abc" });
resume("relative", "a relative file falls back to the id", { file: "s.jsonl", id: "abc" });
resume("drive-relative", "C:s.jsonl is relative", { file: "C:s.jsonl", id: "abc" });
resume("apostrophe", "an apostrophe in the file", { file: "/h/o'b/s.jsonl", id: "abc" });
resume("control", "a control character in the file", { file: "/h/a\u0001b.jsonl", id: "abc" });
resume("tab", "a tab in the file", { file: "/h/a\tb.jsonl", id: "abc" });
resume("del", "DEL in the file", { file: "/h/a\u007fb.jsonl", id: "abc" });
resume("c1", "a C1 control is allowed", { file: "/h/a\u0085b.jsonl", id: "abc" });
resume("line-separator", "U+2028 in the file is allowed and sent raw", { file: "/h/a\u2028b.jsonl", id: "abc" });
resume("quote-and-unicode", "quotes and non-ASCII", { file: '/h/"q" é 😀.jsonl', id: "abc" });
resume("in-memory", "no file: pig --session <id>", { file: null, id: "abc" });
resume("file-throws", "getSessionFile throws", { file: "throws", id: "abc" });
resume("id-throws", "getSessionId throws, the file is fine", { file: "/h/s.jsonl", id: "throws" });
resume("both-throw", "both throw: no resume command", { file: "throws", id: "throws" });
resume("unsafe-both", "an unsafe file and an unsafe id", { file: "/h/o'b/s.jsonl", id: "it's" });
resume("none", "neither a file nor an id", { file: null, id: null });
resume("empty-id", "an empty id", { file: null, id: "" });
resume("id-with-space", "an id with a space is safe", { file: null, id: "a b" });
resume("long-file", "no length rule", { file: `/h/${"x".repeat(9000)}.jsonl`, id: "abc" });

// herdr:blocked.
add("bus-basic", "blocked with a label, then idle", { steps: [START, { op: "bus", data: { active: true, label: "approve command" } }, { op: "bus", data: { active: false } }] });
add("bus-while-working", "blocked during a turn returns to working", { steps: [START, { op: "agent_start" }, { op: "bus", data: { active: true, label: "A" } }, { op: "bus", data: { active: false } }, { op: "agent_settled" }] });
add("bus-counter", "two holders, the latest label, then released", { steps: [START, { op: "bus", data: { active: true, label: "A" } }, { op: "bus", data: { active: true, label: "B" } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: true, label: "C" } }, { op: "bus", data: { active: false } }] });
add("bus-same-label-twice", "an equal string label is not reported twice", { steps: [START, { op: "bus", data: { active: true, label: "same" } }, { op: "bus", data: { active: true, label: "same" } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: false } }] });
add("bus-object-label-twice", "label objects are never ===", { steps: [START, { op: "bus", data: { active: true, label: { k: 1 } } }, { op: "bus", data: { active: true, label: { k: 1 } } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: false } }] });
add("bus-array-label", "an array label", { steps: [START, { op: "bus", data: { active: true, label: [1, "a"] } }, { op: "bus", data: { active: false } }] });
add("bus-null-then-missing", "a null label then a missing label", { steps: [START, { op: "bus", data: { active: true, label: null } }, { op: "bus", data: { active: true } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: false } }] });
for (const [name, label] of [["empty", ""], ["number", 7], ["fraction", 1.5], ["big", 12345678901234567890], ["negative-zero", -0], ["true", true], ["specials", "a<b>&c\nd\te\u2028f\u2029g\u0001h\u007fi\"j\\k"], ["unicode", "é 😀"], ["long", "x".repeat(2000)]]) {
  add(`bus-label-${name}`, `label ${name}`, { steps: [START, { op: "bus", data: { active: true, label } }] });
}
for (const [name, active, blocks] of [["true", true], ["one", 1], ["string", "yes"], ["object", {}], ["array", []], ["zero", 0], ["empty-string", ""], ["null", null], ["false", false], ["negative", -1], ["text-false", "false"]]) {
  add(`bus-active-${name}`, `active ${JSON.stringify(active)}`, { steps: [START, { op: "bus", data: { active, label: "L" } }] });
}
add("bus-no-active", "no active member releases", { steps: [START, { op: "bus", data: { active: true, label: "A" } }, { op: "bus", data: { label: "x" } }] });
for (const [name, data] of [["string", "active"], ["number", 3], ["null", null], ["array", [true]], ["true", true]]) {
  add(`bus-payload-${name}`, `a ${name} payload`, { steps: [START, { op: "bus", data: { active: true, label: "A" } }, { op: "bus", data }] });
}
add("bus-release-with-nothing", "a release with nothing held keeps the counter at zero", { steps: [START, { op: "bus", data: { active: false } }, { op: "bus", data: { active: false } }, { op: "bus", data: { active: true, label: "A" } }, { op: "bus", data: { active: false } }] });
add("bus-in-rpc", "the signal in rpc mode", { mode: "rpc", steps: [START, { op: "bus", data: { active: true, label: "x" } }] });

// Lifecycle.
for (const reason of ["reload", "new", "resume", "fork", "other"]) {
  add(`shutdown-${reason}`, `shutdown reason ${reason} does not release`, { steps: [START, { op: "shutdown", reason }, { op: "agent_start" }] });
}
add("shutdown-no-reason", "a shutdown without a reason", { steps: [START, { op: "shutdown" }] });
add("quit", "quit releases once", { steps: [START, { op: "agent_start" }, { op: "shutdown", reason: "quit" }, { op: "shutdown", reason: "quit" }, { op: "agent_start" }, { op: "agent_settled" }, { op: "bus", data: { active: true, label: "late" } }] });
add("quit-after-resume-shutdown", "a resume shutdown and then quit, as the original's test does", { steps: [START, { op: "shutdown", reason: "resume" }, { op: "shutdown", reason: "quit" }] });
add("quit-never-started", "quit before any session_start", { steps: [{ op: "shutdown", reason: "quit" }] });
add("quit-blocked", "quit while blocked drops nothing queued after it", { steps: [START, { op: "agent_start" }, { op: "bus", data: { active: true, label: "A" } }, { op: "shutdown", reason: "quit" }] });
add("fresh-session", "session_start after a replacement shutdown", { steps: [START, { op: "bus", data: { active: true, label: "stale" } }, { op: "shutdown", reason: "new" }, { op: "start", fresh: true, ctx: { file: "/x/s2.jsonl", id: "s2" } }, { op: "agent_start" }] });

// Transport.
add("retry-after-close", "the first connection ends without an answer", { server: { behaviors: ["close"] }, steps: [START, { op: "agent_start", waitMs: 200 }], finalMs: 300 });
add("retry-after-silence", "the first attempt gets no answer in 0.5 s", { server: { behaviors: ["silent"] }, steps: [START], finalMs: 1000 });
add("both-attempts-fail", "no answer twice: two attempts and no third", { server: { behaviors: ["silent", "silent"] }, steps: [START], finalMs: 2400 });
add("both-close", "the connection ends twice", { server: { behaviors: ["close", "close"] }, steps: [START], finalMs: 300 });
add("quit-behind-hung-herdr", "release behind a hung herdr", { server: { behaviors: ["silent", "silent", "silent", "silent"] }, steps: [START, { op: "shutdown", reason: "quit", waitMs: 100 }], finalMs: 4500 });
add("coalesce-behind-slow-herdr", "only the newest state is sent behind a slow herdr", { server: { delayMs: 300 }, steps: [START, { op: "agent_start", waitMs: 0 }, { op: "bus", data: { active: true, label: "A" }, waitMs: 0 }, { op: "bus", data: { active: false }, waitMs: 0 }, { op: "agent_settled", waitMs: 0 }], finalMs: 900 });
add("queued-resume-at-send", "a queued state carries the resume command current when it is sent", { server: { delayMs: 300 }, steps: [START, { op: "agent_start", ctx: { file: "/h/two.jsonl" }, waitMs: 0 }], finalMs: 700 });
add("quit-drops-queued", "quit drops a queued report; the release does not wait for the one in flight", { server: { delayMs: 400 }, steps: [START, { op: "agent_start", waitMs: 0 }, { op: "shutdown", reason: "quit", waitMs: 0 }], finalMs: 1000 });

writeFileSync(new URL("../../extensions/herdr/testdata/drive-cases.json", import.meta.url), `${JSON.stringify(cases, null, 1)}\n`);
console.log(`${cases.length} cases`);
