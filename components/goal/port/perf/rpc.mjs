// End-to-end measurements through a real pig (PiG's RPC mode) for the performance section of port/PORT.md: cold start to
// registration, and the round trip of each ported tool and command with the extension running as PiG hosts it (a Go
// subprocess for the port, Node under PiG's TypeScript runtime for the original).
//
//   node port/perf/rpc.mjs --pig /work/bin/pig --cpus 64-95 --runs 12 --home-root /scratch --no-flow old \
//        control=none new=/w/components/goal/extensions/pi-goal-x old=/old/components/goal/extensions/pi-goal-x \
//        orig=/w/components/goal/port/oracle/extensions/goal.ts new-piglet=bin:/out/bin/new   > results.json
//
// Each subject is `name=path` (the `-e` argument; `none` is plain pig, the control; a name that starts with `control` only gets the
// cold-start measure). One run is one pig process with a fresh
// agent directory, project directory and mock model; the subjects of a round run one after the other, rounds are repeated
// (interleaved), and the first round is a warm-up (it also fills each subject's extension build cache, kept in its own PIG_HOME
// under --home-root so a Go extension's build is not part of any later run).
//
// Cold start: from spawning pig to the RPC response of `get_commands` that lists the extension's commands. pig starts reading RPC
// commands after it has loaded its extensions (checked against the control), and a command sent at spawn waits in the pipe, so the
// span is pig's own start plus the extension's registration; the control's span is pig alone. PIG_STARTUP_TRACE marks are kept
// too (extension.<name>.spawn-start and handshake-done are pig's own measure of the extension).
//
// Tools: a scripted OpenAI-compatible model (the same format as pigeq's) answers each request with the next queued turn. The tool's
// span is tool_execution_start to tool_execution_end as read on pig's stdout (model, host and extension are the same in every
// subject; only the extension differs). create_goal and set_goal_tasks end the turn, so each runs in a prompt of its own. Commands: `prompt` "/name args"; the span is the send to the first notification the
// command raises. Settings: maxAutonomousRuns 0; PI_GOAL_AUTO_CONFIRM=1 answers the task confirmation.
import { spawn } from "node:child_process";
import http from "node:http";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

const argv = process.argv.slice(2);
const o = { pig: "pig", cpus: "0-1", runs: 12, homeRoot: path.join(tmpdir(), "goalperf-home"), noFlow: new Set() };
const subjects = [];
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  if (a === "--pig") o.pig = argv[++i];
  else if (a === "--cpus") o.cpus = argv[++i];
  else if (a === "--runs") o.runs = Number(argv[++i]);
  else if (a === "--home-root") o.homeRoot = argv[++i];
  else if (a === "--no-flow") o.noFlow = new Set(argv[++i].split(","));
  else if (a.includes("=")) {
    // name=none (plain pig), name=<path> (pig -e <path>) or name=bin:<path> (a Piglet Binary that carries the extension fused in).
    const name = a.slice(0, a.indexOf("=")), spec = a.slice(a.indexOf("=") + 1);
    subjects.push(spec.startsWith("bin:") ? { name, ext: "fused", bin: spec.slice(4) } : { name, ext: spec });
  }
  else throw new Error(`unknown argument ${a}`);
}
const now = () => Number(process.hrtime.bigint()) / 1e6;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function mockModel(queue) {
  const server = http.createServer((req, res) => {
    let body = "";
    req.on("data", (d) => { body += d; });
    req.on("end", () => {
      const turn = queue.shift() ?? { text: "done" };
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      const send = (delta, finish) => res.write(`data: ${JSON.stringify({ id: "m", object: "chat.completion.chunk", created: 1, model: "eq-1", choices: [{ index: 0, delta, finish_reason: finish }] })}\n\n`);
      send({ role: "assistant", content: "" }, null);
      if (turn.text) send({ content: turn.text }, null);
      let finish = "stop";
      (turn.calls ?? []).forEach((c, j) => { send({ tool_calls: [{ index: j, id: `call_${Math.random().toString(36).slice(2)}`, type: "function", function: { name: c.name, arguments: JSON.stringify(c.args) } }] }, null); finish = "tool_calls"; });
      send({}, finish);
      res.write(`data: ${JSON.stringify({ id: "m", object: "chat.completion.chunk", created: 1, model: "eq-1", choices: [], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } })}\n\ndata: [DONE]\n\n`);
      res.end();
    });
  });
  return new Promise((r) => server.listen(0, "127.0.0.1", () => r(server)));
}

const steps = [];
for (let g = 1; g <= 3; g++) for (let s = 1; s <= 3; s++) steps.push(`group-${g}-step-${s}`);
function taskList() {
  const out = [];
  for (let g = 1; g <= 3; g++) {
    out.push({ id: `group-${g}`, title: `Milestone ${g} of the quarterly report`, verification_contract: "The section is written and reviewed." });
    for (let s = 1; s <= 3; s++) out.push({ id: `group-${g}-step-${s}`, parent_id: `group-${g}`, title: `Step ${s} of milestone ${g}` });
  }
  return out;
}
const call = (name, args) => ({ calls: [{ name, args }] });

async function oneRun(subject, round) {
  const root = mkdtempSync(path.join(tmpdir(), "goalperf-run-"));
  const agent = path.join(root, "agent"), cwd = path.join(root, "work"), home = path.join(root, "home");
  for (const d of [agent, cwd, home, path.join(cwd, ".pi")]) mkdirSync(d, { recursive: true });
  writeFileSync(path.join(cwd, ".pi", "pi-goal-x-settings.json"), '{"maxAutonomousRuns": 0}');
  const queue = [];
  const model = await mockModel(queue);
  writeFileSync(path.join(agent, "models.json"), JSON.stringify({ providers: { "eq-llm": { baseUrl: `http://127.0.0.1:${model.address().port}/v1`, api: "openai-completions", apiKey: "eq-key", models: [{ id: "eq-1", name: "eq-1", reasoning: false, input: ["text"], contextWindow: 100000, maxTokens: 4096, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } } }));
  const pigHome = path.join(o.homeRoot, subject.name);
  mkdirSync(pigHome, { recursive: true });
  const env = {
    PATH: process.env.PATH, HOME: home, LANG: "C.UTF-8", TERM: "dumb", NO_COLOR: "1", TMPDIR: root,
    PIG_HOME: pigHome, PIG_CODING_AGENT_DIR: agent, PI_CODING_AGENT_DIR: agent, PI_SKIP_VERSION_CHECK: "1", PI_OFFLINE: "1", PI_TELEMETRY: "0",
    PIG_STARTUP_TRACE: "1", PI_GOAL_AUTO_CONFIRM: "1", GOCACHE: process.env.GOCACHE ?? "", GOMODCACHE: process.env.GOMODCACHE ?? "", GOFLAGS: process.env.GOFLAGS ?? "", GOTOOLCHAIN: "local", GOPROXY: "off",
    PIG_SDK_DIR: process.env.PIG_SDK_DIR ?? "", PIG_SOURCE_ROOT: process.env.PIG_SOURCE_ROOT ?? "",
  };
  const args = ["--mode", "rpc", "--no-session", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--offline", "--provider", "eq-llm", "--model", "eq-1"];
  if (subject.ext !== "none" && subject.ext !== "fused") args.push("-e", subject.ext);

  const events = [];
  const waiters = [];
  let stderr = "";
  const t0 = now();
  const child = spawn("taskset", ["-c", o.cpus, subject.bin ?? o.pig, ...args], { cwd, env, stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.on("data", (d) => { stderr += d; });
  let buf = "";
  child.stdout.on("data", (d) => {
    buf += d;
    for (let i; (i = buf.indexOf("\n")) >= 0;) {
      const line = buf.slice(0, i); buf = buf.slice(i + 1);
      let obj; try { obj = JSON.parse(line); } catch { continue; }
      const ev = { t: now(), obj };
      events.push(ev);
      for (const w of [...waiters]) if (w.pred(ev)) { waiters.splice(waiters.indexOf(w), 1); w.resolve(ev); }
    }
  });
  const exited = new Promise((r) => child.on("close", r));
  const send = (cmd) => child.stdin.write(`${JSON.stringify(cmd)}\n`);
  const waitFor = (pred, ms = 60000) => new Promise((resolve, reject) => {
    const hit = events.find(pred);
    if (hit) return resolve(hit);
    const w = { pred, resolve: (e) => { clearTimeout(timer); resolve(e); } };
    const timer = setTimeout(() => { waiters.splice(waiters.indexOf(w), 1); reject(new Error(`${subject.name}: timeout waiting; stderr: ${stderr.slice(-500)}`)); }, ms);
    waiters.push(w);
  });

  const result = { subject: subject.name, round, tools: {}, cmds: {} };
  try {
    send({ id: "gc", type: "get_commands" });
    const resp = await waitFor((e) => e.obj.type === "response" && e.obj.id === "gc");
    result.coldMs = resp.t - t0;
    const names = (resp.obj.data?.commands ?? []).map((c) => c.name);
    result.goalCommands = names.filter((n) => n.startsWith("goal") || n.startsWith("sisyphus")).length;
    if (!subject.name.startsWith("control") && result.goalCommands === 0) throw new Error(`${subject.name}: the extension registered no goal command: ${JSON.stringify(names)}`);
    result.marks = {};
    // Marks are written as the run goes; read them at the end.

    if (!subject.name.startsWith("control") && !o.noFlow.has(subject.name)) {
      let seq = 0;
      const prompt = async (message, turns) => {
        queue.push(...turns);
        const id = `p${++seq}`;
        const from = events.length;
        send({ id, type: "prompt", message });
        await waitFor((e) => e.obj.type === "agent_end" && events.indexOf(e) >= from, 120000);
        const starts = new Map();
        if (process.env.GOALPERF_DEBUG) process.stderr.write(`${message}: ${JSON.stringify(events.slice(from).map((e) => e.obj.type === "tool_execution_end" ? `end:${e.obj.toolName}:${JSON.stringify(e.obj.result).slice(0, 120)}` : e.obj.type))}\n`);
        for (const e of events.slice(from)) {
          if (e.obj.type === "tool_execution_start") starts.set(e.obj.toolCallId, { t: e.t, key: e.obj.toolName === "get_goal" && e.obj.args?.section ? `get_goal ${e.obj.args.section}` : e.obj.toolName });
          if (e.obj.type === "tool_execution_end" && starts.has(e.obj.toolCallId)) { const st = starts.get(e.obj.toolCallId); (result.tools[st.key] ??= []).push(e.t - st.t); }
        }
        await sleep(30);
      };
      // create_goal and set_goal_tasks end the turn (their results terminate it): one prompt each, one scripted turn each.
      for (let i = 1; i <= 5; i++) await prompt("create a goal", [call("create_goal", { objective: `Write the quarterly report ${i}\nSuccess criteria: three sections, each reviewed, with the figures checked against the ledger.` })]);
      for (let i = 0; i < 6; i++) await prompt("set the tasks", [call("set_goal_tasks", { tasks: taskList(), block_completion: true })]);
      await prompt("read the goal", [...Array(10).fill(call("get_goal", {})), ...Array(10).fill(call("get_goal", { section: "tasks" }))]);
      await prompt("work the tasks", [...steps.flatMap((s) => [call("update_goal_task", { task_id: s, status: "start" }), call("update_goal_task", { task_id: s, status: "complete", evidence: "The step is written." })])]);
      const command = async (name, a) => {
        const from = events.length;
        const t = now();
        send({ id: `c${++seq}`, type: "prompt", message: `/${name}${a ? ` ${a}` : ""}` });
        const hit = await waitFor((e) => events.indexOf(e) >= from && e.obj.type === "extension_ui_request" && e.obj.method === "notify");
        (result.cmds[a ? `${name} ${a}` : name] ??= []).push(hit.t - t);
        await sleep(30);
      };
      for (let i = 0; i < 12; i++) {
        await command("goal-list", "");
        await command("goal-status", "");
        await command("goal-status", "verbose");
        await command("goal-refresh", "");
      }
      await prompt("pause", [call("update_goal", { status: "paused", reason: "Waiting for the review of the second milestone.", suggested_action: "Ask the reviewer." })]);
    }
  } finally {
    child.stdin.end();
    const killer = setTimeout(() => child.kill("SIGKILL"), 15000);
    await exited;
    clearTimeout(killer);
    model.close();
    rmSync(root, { recursive: true, force: true });
  }
  for (const m of stderr.matchAll(/\[startup\] (\S+)\s+(\d+)ms/g)) result.marks[m[1]] = Number(m[2]);
  return result;
}

const all = [], warm = [];
for (let round = 0; round <= o.runs; round++) {
  for (const s of subjects) {
    const r = await oneRun(s, round);
    (round > 0 ? all : warm).push(r); // round 0 is the warm-up: the first start of each subject, with its build or transpile cache empty
    process.stderr.write(`round ${round} ${s.name}: cold ${r.coldMs.toFixed(1)} ms\n`);
  }
}
console.log(JSON.stringify({ cpus: o.cpus, runs: o.runs, subjects: subjects.map((s) => s.name), results: all, firstStart: warm.map((r) => ({ subject: r.subject, coldMs: r.coldMs })) }));
