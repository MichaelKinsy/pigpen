// Builds the single-extension Piglet Binaries the performance section measures: one with the goal port of a checkout (it needs
// pitui, which that checkout carries) and one with the previous release (0.1.1, a checkout of origin/goal-honest-refusal). The control,
// a Piglet whose only extension registers nothing, is built with --control from the checkout's seed-check fixture
// (piglets/pig-with-batteries/extensions/seed-check; scripts/perf/build-singles.mjs's control too). The shipped Piglet fuses the
// extension into the Binary (`extensionRealization: fused` in piglets/pig-popular), so this is the vehicle whose size and start-up
// the extension changes.
//
//   PIG_BIN=/path/to/pig PIG_SOURCE_ROOT=/path/to/pig-src node port/perf/build-piglets.mjs <checkout> <name> <out dir> [--control]
//
// <checkout> holds components/goal (and components/pitui for the port); <name> is the output file name under <out dir>/bin.
import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const control = process.argv.includes("--control");
const [checkout, name, out] = process.argv.slice(2).filter((a) => a !== "--control").map((p, i) => (i === 1 ? p : resolve(p)));
if (!checkout || !name || !out) throw new Error("usage: build-piglets.mjs <checkout> <name> <out dir>");
const pig = process.env.PIG_BIN;
if (!pig) throw new Error("PIG_BIN is not set");
const withPitui = existsSync(join(checkout, "components", "pitui"));
const packages = control ? [] : withPitui ? ["pitui", "goal"] : ["goal"];
const tools = ["get_goal", "create_goal", "update_goal", "set_goal_tasks", "update_goal_task", "goal_question", "goal_questionnaire", "propose_goal_draft"];

const dir = join(out, "piglets", `perf-${name}`);
rmSync(dir, { recursive: true, force: true });
mkdirSync(join(dir, "packages"), { recursive: true });
const skip = (p) => /node_modules|\/\.git(\/|$)|port\/oracle|\/testdata\//.test(p);
if (control) cpSync(join(checkout, "piglets", "pig-with-batteries", "extensions", "seed-check"), join(dir, "extensions", "seed-check"), { recursive: true });
for (const pkg of packages) cpSync(join(checkout, "components", pkg), join(dir, "packages", pkg), { recursive: true, filter: (p) => !skip(p) });
writeFileSync(join(dir, "piglet.yaml"), [
  `name: perf-${name}`, 'description: "one extension, for perf measurement"', "release:", "  version: 0.1.0", "build:", "  targets: [linux/amd64]",
  ...(control ? [] : ["packages:", ...packages.map((p) => `  ${p}: local:./packages/${p}`)]),
  "extensions:", control ? "  - name: seed-check" : "  - name: pi-goal-x", control ? "    origins: [local:./extensions/seed-check]" : "    origins: [package:goal]", `    tools: [${control ? "" : tools.join(", ")}]`,
  "skills: []", "discovery:", "  extensions: []", "  skills: []", "",
].join("\n"));
const pigHome = mkdtempSync(join(tmpdir(), "goalperf-pighome-"));
mkdirSync(join(out, "bin"), { recursive: true });
const target = join(out, "bin", name);
rmSync(target, { force: true });
const r = spawnSync(pig, ["piglet", "build", join(dir, "piglet.yaml"), "--format", "binary", "--out", target, "--targets", "linux/amd64"], {
  cwd: checkout, env: { ...process.env, PIG_HOME: pigHome, PIG_CODING_AGENT_DIR: join(pigHome, "agent") }, stdio: ["ignore", "inherit", "inherit"],
});
rmSync(pigHome, { recursive: true, force: true });
console.log(`${name}: ${r.status === 0 ? "built" : `FAILED (${r.status})`}`);
process.exit(r.status ?? 1);
