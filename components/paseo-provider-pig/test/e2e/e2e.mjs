// End-to-end check of the PiG provider plugin against a real Paseo daemon, with no account and no real model.
//
//   PASEO_BIN=/path/to/paseo PIG_BIN=/path/to/pig PIG_ACP_BIN=/path/to/pig-acp node test/e2e/e2e.mjs
//
// It starts a scratch Paseo daemon (its own home, a private port, relay off), installs this directory as a plugin,
// points pig at a scripted local model, and drives three agents through the plugin's "pig" provider: a plain turn, a
// read tool call, and a bash tool call held by an approval that the run first allows and then denies. Everything lives
// in one temporary directory that is removed at the end, and only the processes this script started are stopped.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { startFakeModel } from "./fake-model.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const pluginDir = resolve(here, "..", "..");
const required = ["PASEO_BIN", "PIG_BIN", "PIG_ACP_BIN"];
const missing = required.filter((name) => !process.env[name]);
if (missing.length > 0) {
  console.error(`set ${missing.join(", ")}`);
  process.exit(2);
}

const tmp = mkdtempSync(join(tmpdir(), "pzp-"));
const home = join(tmp, "home");
const paseoHome = join(home, ".paseo");
const agentDir = join(tmp, "agent");
const project = join(tmp, "proj");
const bin = join(tmp, "bin");
for (const dir of [paseoHome, agentDir, project, bin]) mkdirSync(dir, { recursive: true });
symlinkSync(resolve(process.env.PIG_BIN), join(bin, "pig"));
symlinkSync(resolve(process.env.PIG_ACP_BIN), join(bin, "pig-acp"));
writeFileSync(join(project, "README.md"), "# Demo project\nSecond line.\n");

function freePort() {
  return new Promise((done, fail) => {
    const probe = createServer();
    probe.once("error", fail);
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => done(port));
    });
  });
}

const model = await startFakeModel();
const port = await freePort();
writeFileSync(join(agentDir, "models.json"), JSON.stringify({ providers: { fake: { baseUrl: `http://127.0.0.1:${model.port}/v1`, api: "openai-completions", apiKey: "fake", models: [{ id: "fake-model", name: "fake-model", contextWindow: 200000, maxTokens: 8192 }] } } }));
writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ lastChangelogVersion: "9999.0.0", defaultProvider: "fake", defaultModel: "fake-model", defaultProjectTrust: "always", extensions: [join(here, "approve.mjs")] }));
writeFileSync(join(agentDir, "auth.json"), "{}");
writeFileSync(join(paseoHome, "config.json"), JSON.stringify({ version: 1, pluginsEnabled: true, daemon: { listen: `127.0.0.1:${port}`, relay: { enabled: false } } }));

const env = {
  ...process.env,
  HOME: home,
  PATH: `${bin}:${process.env.PATH}`,
  PIG_HOME: agentDir,
  PIG_CODING_AGENT_DIR: agentDir,
  PI_CODING_AGENT_DIR: agentDir,
  PIG_OFFLINE: "1",
  TMPDIR: tmp,
};
// The scripted model runs in this process, so the CLI must run without blocking the event loop.
const execFileAsync = promisify(execFile);
const paseo = async (...args) => (await execFileAsync(process.env.PASEO_BIN, [...args, "--home", paseoHome], { env, cwd: project, encoding: "utf8", timeout: 120000 })).stdout;
const json = async (...args) => JSON.parse(await paseo(...args, "--json"));
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
const step = (message) => console.log(`ok  ${message}`);

async function until(describe, probe, timeoutMs = 60000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const value = await probe();
    if (value) return value;
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${describe}`);
    await sleep(500);
  }
}

let failed = false;
try {
  await paseo("daemon", "start", "--timeout", "120");
  step(`scratch daemon listening on 127.0.0.1:${port}`);

  await paseo("plugin", "install", pluginDir);
  const plugin = (await until("the plugin to run", async () => (await json("plugin", "ls")).find((entry) => entry.id === "pig-provider" && entry.status === "running"))) ;
  step(`plugin ${plugin.id} is running`);

  const diagnostic = await paseo("provider", "diagnostic", "pig");
  assert.match(diagnostic, /Status: Ready/);
  const versions = /pig-acp \d+\.\d+\.\d+, (?:PiG \d+\.\d+\.\d+, )?Pi \d+\.\d+\.\d+/.exec(diagnostic)?.[0];
  assert.ok(versions, `the diagnostic names pig-acp and Pi versions:\n${diagnostic}`);
  step(`version check: ${versions}`);

  const providers = await json("provider", "ls");
  const pig = providers.find((entry) => entry.provider === "pig" || entry.id === "pig");
  assert.ok(pig, "the provider list holds pig");
  assert.equal(pig.label, "PiG");
  step("provider pig is listed with the label PiG");

  const run = (title, prompt, wait = true) => json("run", "--provider", "pig/fake/fake-model", "--title", title, ...(wait ? ["--wait-timeout", "90s"] : ["--no-wait"]), prompt);
  const logs = (agent) => paseo("logs", agent.agentId);

  const turn = await run("turn", "Say hello");
  assert.equal(turn.status, "completed");
  assert.match(await logs(turn), /Hello! This reply comes from a local fake model\./);
  step("full turn: the model's reply streamed back");

  const read = await run("read", "Please read the README and tell me the first line");
  assert.equal(read.status, "completed");
  const readLog = await logs(read);
  assert.match(readLog, /\[Read\]/);
  assert.match(readLog, /The tool returned: # Demo project/);
  step("tool call: the read tool ran and its result reached the model");

  const pending = async (agent) => (await json("permit", "ls")).find((request) => request.agentId === agent.agentId);

  const allowed = await run("approve", "RUNBASH please", false);
  const request = await until("the approval request", () => pending(allowed));
  assert.match(request.name, /Allow bash\?/);
  await paseo("permit", "allow", allowed.agentId, request.id);
  await until("the allowed bash call", async () => /The tool returned: paseo-ok/.test(await logs(allowed)));
  step("approval: allowed, the bash command ran and printed paseo-ok");

  const denied = await run("deny", "RUNBASH please", false);
  const second = await until("the second approval request", () => pending(denied));
  await paseo("permit", "deny", denied.agentId, second.id);
  await until("the denied bash call", async () => /Blocked by the user/.test(await logs(denied)));
  step("approval: denied, the bash command did not run");

  const edit = await run("edit", "RUNEDIT please");
  assert.equal(edit.status, "completed");
  assert.match(readFileSync(join(project, "README.md"), "utf8"), /# Demo project \(edited\)/);
  step("edit tool: the file changed");
} catch (error) {
  failed = true;
  console.error(error);
  try {
    console.error(`--- daemon log tail\n${readFileSync(join(paseoHome, "daemon.log"), "utf8").split("\n").slice(-25).join("\n")}`);
  } catch {
    // No log to show.
  }
} finally {
  try {
    await paseo("daemon", "stop");
  } catch {
    // The daemon may never have started.
  }
  await model.close();
  rmSync(tmp, { recursive: true, force: true });
}
process.exit(failed ? 1 : 0);
