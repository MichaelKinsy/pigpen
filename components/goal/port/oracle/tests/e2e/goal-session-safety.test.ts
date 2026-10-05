import test from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const run = promisify(execFile);
const worker = fileURLToPath(new URL("../session-safety-worker.mjs", import.meta.url));
for (const mode of ["approve", "reject", "skip", "abort", "error", "history-completions", "history-responses", "child-fresh", "child-fork", "child-resume", "child-nested"]) {
	test(`real SDK: goal session safety (${mode})`, { timeout: 30_000 }, async () => {
		const child = mode.startsWith("child-");
		const { stdout } = await run(process.execPath, ["--experimental-strip-types", worker, mode], {
			timeout: 25_000,
			env: { ...process.env, PI_SUBAGENT_CHILD: child && mode !== "child-nested" ? "1" : "", PI_SUBAGENT_DEPTH: mode === "child-nested" ? "3" : "", PI_GOAL_AUTO_CONFIRM: "" },
		});
		assert.equal(JSON.parse(stdout.trim().split("\n").at(-1)!).passed, true);
	});
}
