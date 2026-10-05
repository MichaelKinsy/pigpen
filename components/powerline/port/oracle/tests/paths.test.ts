import test from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { getAgentDir, getAgentPath, getAgentSessionDirs, normalizeAgentDirPath } from "../paths.ts";

function withTemporaryPathEnv(run: (home: string, agentDir: string) => void): void {
  const root = mkdtempSync(join(tmpdir(), "powerline-paths-"));
  const home = join(root, "home");
  const agentDir = join(root, "custom-agent");
  const originalHome = process.env.HOME;
  const originalUserProfile = process.env.USERPROFILE;
  const originalAgentDir = process.env.PI_CODING_AGENT_DIR;

  try {
    mkdirSync(home, { recursive: true });
    process.env.HOME = home;
    delete process.env.USERPROFILE;
    run(home, agentDir);
  } finally {
    if (originalHome === undefined) delete process.env.HOME;
    else process.env.HOME = originalHome;
    if (originalUserProfile === undefined) delete process.env.USERPROFILE;
    else process.env.USERPROFILE = originalUserProfile;
    if (originalAgentDir === undefined) delete process.env.PI_CODING_AGENT_DIR;
    else process.env.PI_CODING_AGENT_DIR = originalAgentDir;
    rmSync(root, { recursive: true, force: true });
  }
}

test("agent dir uses non-empty PI_CODING_AGENT_DIR", () => {
  withTemporaryPathEnv((_home, agentDir) => {
    process.env.PI_CODING_AGENT_DIR = agentDir;

    assert.equal(getAgentDir(), agentDir);
    assert.equal(getAgentPath("settings.json"), join(agentDir, "settings.json"));
  });
});

test("agent dir falls back to HOME .pi/agent for empty env values", () => {
  withTemporaryPathEnv((home) => {
    process.env.PI_CODING_AGENT_DIR = "   ";

    assert.equal(getAgentDir(), join(home, ".pi", "agent"));
    assert.equal(getAgentPath("sessions"), join(home, ".pi", "agent", "sessions"));
  });
});

test("agent dir normalizes tilde and file URL env values like Pi", () => {
  withTemporaryPathEnv((home, agentDir) => {
    process.env.PI_CODING_AGENT_DIR = "~/custom-agent";
    assert.equal(getAgentDir(), join(home, "custom-agent"));
    assert.equal(normalizeAgentDirPath("~"), home);

    process.env.PI_CODING_AGENT_DIR = pathToFileURL(agentDir).href;
    assert.equal(getAgentDir(), agentDir);
  });
});

test("agent sessions include legacy ~/.pi/sessions only when it exists", () => {
  withTemporaryPathEnv((home, agentDir) => {
    process.env.PI_CODING_AGENT_DIR = agentDir;
    assert.deepEqual(getAgentSessionDirs(), [join(agentDir, "sessions")]);

    mkdirSync(join(home, ".pi", "sessions"), { recursive: true });
    assert.deepEqual(getAgentSessionDirs(), [join(agentDir, "sessions"), join(home, ".pi", "sessions")]);
  });
});
