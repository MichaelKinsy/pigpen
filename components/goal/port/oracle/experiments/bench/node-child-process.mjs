/**
 * Shadow of node:child_process (B8 enforcement).
 * Any spawn/exec/fork throws unless the harness explicitly allows it
 * (b5 contention child via allowChildProcess + spawnContention).
 */

import { createRequire } from "node:module";
import { state, recordViolation } from "./guard-state.mjs";

const real = createRequire(import.meta.url)("child_process");

function forbid(name) {
	return function (..._args) {
		recordViolation("child_process", `${name} called in a benchmark`);
	};
}

/** The only spawn path a benchmark may use; requires allowChildProcess(). */
export function spawnContention(...args) {
	if (!state.childProcessAllowed) {
		recordViolation("child_process", "spawnContention without allowChildProcess");
	}
	return real.spawn(...args);
}

export const spawn = forbid("spawn");
export const exec = forbid("exec");
export const execFile = forbid("execFile");
export const fork = forbid("fork");
export const spawnSync = forbid("spawnSync");
export const execSync = forbid("execSync");
export const execFileSync = forbid("execFileSync");

export const execArgv = real.execArgv;
export const execPath = real.execPath;
export const execFileAsync = real.execFile;
export const execAsync = real.exec;

export default real;
