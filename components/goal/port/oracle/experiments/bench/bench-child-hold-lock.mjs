/**
 * B5 helper: holds the goal lock for holdMs then releases. The parent bench
 * process (b5) spawns this via spawnContention inside allowChildProcess.
 *
 * Usage: node bench-child-hold-lock.mjs <cwd> <goalId> <holdMs>
 */

import { acquireGoalLock } from "../../extensions/storage/goal-lock.ts";

const [cwd, goalId, holdMsRaw] = process.argv.slice(2);
const holdMs = Number(holdMsRaw ?? 3000);
const lock = acquireGoalLock({ cwd }, goalId);
setTimeout(() => {
	lock.release();
	process.exit(0);
}, holdMs);
