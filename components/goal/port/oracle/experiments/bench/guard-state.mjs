/**
 * B8 agent-exclusion guard state (shared by the wrapper modules).
 *
 * The wrapper modules (node-fs.mjs, node-net.mjs, node-http.mjs,
 * node-https.mjs, node-child-process.mjs) are installed via
 * bench-adapter-hooks.mjs registerHooks and shadow the real Node builtins
 * for every module in the process (extension + harness). They consult this
 * module for:
 *   - sync fs op counting (B2 per-turn I/O accounting),
 *   - injected read latency (B1 slow-storage emulation),
 *   - the no-model/no-network/no-spawn enforcement (B8): any net/http/https
 *     use throws; child_process use throws unless explicitly allowed by the
 *     contention harness (b5).
 */

export const state = {
	fsOpCount: 0,
	latencyMs: 0,
	childProcessAllowed: false,
	violations: [],
	trace: null,
};

export function beginFsCount() {
	state.fsOpCount = 0;
	state.trace = process.env.PI_GOAL_BENCH_TRACE ? [] : null;
}

export function endFsCount() {
	const trace = state.trace;
	state.trace = null;
	return state.fsOpCount;
}

export function setLatency(ms) {
	state.latencyMs = ms;
}

/** Run fn with an injected per-sync-op latency of ms (slow-storage emulation). */
export function withLatency(ms, fn) {
	const previous = state.latencyMs;
	state.latencyMs = ms;
	try {
		return fn();
	} finally {
		state.latencyMs = previous;
	}
}

export function allowChildProcess(fn) {
	const previous = state.childProcessAllowed;
	state.childProcessAllowed = true;
	try {
		return fn();
	} finally {
		state.childProcessAllowed = previous;
	}
}

export function recordViolation(kind, detail) {
	state.violations.push({ kind, detail });
	throw new Error(`[B8] agent-exclusion violation: ${kind} — ${detail}`);
}

export function assertNoViolations() {
	if (state.violations.length > 0) {
		throw new Error(`[B8] ${state.violations.length} violation(s) recorded: ${JSON.stringify(state.violations, null, 2)}`);
	}
}

/** Synchronous bounded sleep (Atomics.wait is legal on the main thread). */
export function sleepMs(ms) {
	const buffer = new Int32Array(new SharedArrayBuffer(4));
	Atomics.wait(buffer, 0, 0, ms);
}
