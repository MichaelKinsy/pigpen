// Renders testdata/welcome-states.json with the ORIGINAL welcome.ts (WelcomeComponent, WelcomeHeader, getRecentSessions) and writes
// the result as JSON. Run: node --experimental-strip-types drive/welcome-drive.mjs <states.json> <out.json>
// The original's dev dependencies (@earendil-works/pi-tui) must be installed in port/oracle (npm ci --ignore-scripts).
import { mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { WelcomeComponent, WelcomeHeader, getRecentSessions } from "../oracle/welcome.ts";

const [statesPath, outPath] = process.argv.slice(2);
const states = JSON.parse(readFileSync(statesPath, "utf8"));
const result = {};

function writeFiles(root, files, now) {
  for (const f of files) {
    const dir = join(root, f.dir);
    mkdirSync(dir, { recursive: true });
    const file = join(dir, f.name);
    writeFileSync(file, f.header + "\n" + "z".repeat(f.body));
    utimesSync(file, (now - f.age) / 1000, (now - f.age) / 1000);
  }
}

for (const s of states) {
  if (s.kind === "sessions") {
    const home = mkdtempSync(join(tmpdir(), "welcome-drive-"));
    const saved = { HOME: process.env.HOME, agent: process.env.PI_CODING_AGENT_DIR };
    try {
      process.env.HOME = home;
      delete process.env.PI_CODING_AGENT_DIR;
      let agentSessions = join(home, ".pi", "agent", "sessions");
      if (s.customAgentDir) {
        process.env.PI_CODING_AGENT_DIR = join(home, "agent-dir");
        agentSessions = join(home, "agent-dir", "sessions");
      }
      mkdirSync(agentSessions, { recursive: true });
      const now = Date.now();
      writeFiles(agentSessions, s.files, now);
      writeFiles(join(home, ".pi", "sessions"), s.legacy, now);
      result[s.name] = await getRecentSessions(s.maxCount);
    } finally {
      if (saved.HOME === undefined) delete process.env.HOME; else process.env.HOME = saved.HOME;
      if (saved.agent === undefined) delete process.env.PI_CODING_AGENT_DIR; else process.env.PI_CODING_AGENT_DIR = saved.agent;
      rmSync(home, { recursive: true, force: true });
    }
    continue;
  }
  const tokens = s.tokens === "NaN" ? Number.NaN : s.tokens;
  const view = s.kind === "overlay"
    ? new WelcomeComponent(s.model, s.provider, s.sessions, s.counts, tokens)
    : new WelcomeHeader(s.model, s.provider, s.sessions, s.counts, tokens);
  if (s.kind === "overlay") view.setCountdown(s.countdown);
  const byWidth = {};
  for (const w of s.widths) byWidth[w] = view.render(w);
  result[s.name] = byWidth;
}
writeFileSync(outPath, JSON.stringify(result, null, 1) + "\n");
