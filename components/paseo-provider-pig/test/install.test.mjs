import assert from "node:assert/strict";
import { test } from "node:test";
import {
  checkPigInstall,
  compareVersions,
  MIN_PIG_ACP_VERSION,
  parsePigAcpVersion,
  parsePigVersion,
  pigCommand,
} from "../server/install.ts";

const launch = (over = {}) => ({ command: "/bin/pig-acp", args: [], env: {}, ...over });
const enoent = Object.assign(new Error("spawn x ENOENT"), { code: "ENOENT" });

// exec answers by command name; an Error value is thrown.
const fakeExec = (answers) => async (command, args) => {
  const key = `${command} ${args.join(" ")}`;
  const answer = answers[key];
  if (answer === undefined) throw enoent;
  if (answer instanceof Error) throw answer;
  return { stdout: answer };
};

test("pig-acp --version parses", () => {
  assert.deepEqual(parsePigAcpVersion("pig-acp 0.2.2 (ACP protocol version 1)\n"), { version: "0.2.2", protocol: "1" });
  assert.deepEqual(parsePigAcpVersion("pig-acp 0.3.0-rc.1\n"), { version: "0.3.0-rc.1", protocol: undefined });
  assert.equal(parsePigAcpVersion("pi-acp 0.0.28"), undefined);
});

test("pig --version parses Pi's bare version and the earlier composite", () => {
  assert.deepEqual(parsePigVersion("1.5.0\n"), { pig: undefined, pi: "1.5.0" });
  assert.deepEqual(parsePigVersion("0.7.0+1.5.0\n"), { pig: "0.7.0", pi: "1.5.0" });
  assert.equal(parsePigVersion("pig: 0.7.0"), undefined);
});

test("version order", () => {
  assert.equal(compareVersions("0.2.1", "0.2.1"), 0);
  assert.equal(compareVersions("0.2.2", "0.2.10"), -1);
  assert.equal(compareVersions("0.10.0", "0.2.9"), 1);
  assert.equal(compareVersions("1.0.0-rc.1", "0.9.9"), 1);
});

test("the pig command comes from --pig, then PIG_ACP_PIG_COMMAND, then PATH", () => {
  assert.equal(pigCommand(launch({ args: ["--pig", "/opt/pig"] })), "/opt/pig");
  assert.equal(pigCommand(launch({ env: { PIG_ACP_PIG_COMMAND: "/env/pig" } })), "/env/pig");
  assert.equal(pigCommand(launch({ args: ["--pig", "/opt/pig"], env: { PIG_ACP_PIG_COMMAND: "/env/pig" } })), "/opt/pig");
  assert.equal(pigCommand(launch()), "pig");
});

test("a working install is available and names both versions", async () => {
  const exec = fakeExec({ "/bin/pig-acp --version": "pig-acp 0.2.2 (ACP protocol version 1)\n", "pig --version": "1.5.0\n" });
  assert.deepEqual(await checkPigInstall(exec, launch()), { available: true, diagnostic: "pig-acp 0.2.2, Pi 1.5.0" });
});

test("PiG 0.7.0's composite version is accepted", async () => {
  const exec = fakeExec({ "/bin/pig-acp --version": "pig-acp 0.2.1 (ACP protocol version 1)\n", "pig --version": "0.7.0+1.5.0\n" });
  assert.deepEqual(await checkPigInstall(exec, launch()), { available: true, diagnostic: "pig-acp 0.2.1, PiG 0.7.0, Pi 1.5.0" });
});

test("the override's arguments reach both probes", async () => {
  const exec = fakeExec({ "/bin/pig-acp --pig /opt/pig --version": "pig-acp 0.2.2 (ACP protocol version 1)\n", "/opt/pig --version": "1.5.0\n" });
  const status = await checkPigInstall(exec, launch({ args: ["--pig", "/opt/pig"] }));
  assert.equal(status.available, true);
});

test("a missing pig-acp is unavailable with the fix named", async () => {
  const status = await checkPigInstall(fakeExec({}), launch());
  assert.equal(status.available, false);
  assert.match(status.diagnostic, /pig-acp \(\/bin\/pig-acp\) did not run: not found/);
});

test("an old pig-acp is unavailable", async () => {
  const exec = fakeExec({ "/bin/pig-acp --version": "pig-acp 0.1.9 (ACP protocol version 1)\n" });
  const status = await checkPigInstall(exec, launch());
  assert.deepEqual(status, { available: false, diagnostic: `pig-acp 0.1.9 is older than ${MIN_PIG_ACP_VERSION}. Update pig-acp.` });
});

test("a missing pig is unavailable even when pig-acp runs", async () => {
  const exec = fakeExec({ "/bin/pig-acp --version": "pig-acp 0.2.2 (ACP protocol version 1)\n" });
  const status = await checkPigInstall(exec, launch());
  assert.equal(status.available, false);
  assert.match(status.diagnostic, /pig \(pig\) did not run: not found/);
});

test("output that is not a version is unavailable", async () => {
  const exec = fakeExec({ "/bin/pig-acp --version": "hello\n" });
  assert.equal((await checkPigInstall(exec, launch())).available, false);
  const exec2 = fakeExec({ "/bin/pig-acp --version": "pig-acp 0.2.2 (ACP protocol version 1)\n", "pig --version": "pig is great\n" });
  assert.equal((await checkPigInstall(exec2, launch())).available, false);
});
