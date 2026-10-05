/**
 * B10 — Checkpoint growth gate (issue #30).
 *
 * Measures persisted checkpoint content growth across unchanged continuation
 * turns, provider-visible checkpoint history after context compaction, the
 * legacy-session recovery reduction, and full-goal-block duplication in the
 * composed request. Agent-free (B8): operates on extension functions and the
 * recovery transform only.
 *
 * Rows (ops = measured chars / counts):
 *   B10.checkpoint.persisted.{1,10,100,1000}   total persisted checkpoint chars after N turns
 *   B10.checkpoint.provider-context.1000       provider-visible checkpoints after compaction (must be <=1)
 *   B10.checkpoint.recovery.850                post-recovery checkpoint chars for a 850-entry legacy session
 *   B10.checkpoint.composed-request.50t        full goal blocks in the composed request (must be 1)
 */

import { transformSessionLines } from "../../scripts/recover-session-checkpoints.mjs";
import { compactGoalCheckpointContext } from "../../extensions/goal-events.ts";
import { checkpointTriggerPrompt, goalPrompt, CHECKPOINT_TRIGGER_MAX_CHARS } from "../../extensions/prompts/goal-prompts.ts";
import { makeGoalRecord } from "./bench-common.mjs";

const LEGACY_CHECKPOINT_CHARS = 6400; // plan §17 fixture: ~6.4K chars per legacy full prompt

function legacyCheckpointLine(goalId, index) {
	const entry = {
		type: "custom_message",
		id: `cp-${index}`,
		parentId: index === 0 ? null : `cp-${index - 1}`,
		timestamp: "2026-08-23T00:00:00.000Z",
		customType: "pi-goal-event",
		display: false,
		content: `[GOAL CHECKPOINT goalId=${goalId}]\nContinue working toward the active pi goal.\n${"x".repeat(LEGACY_CHECKPOINT_CHARS)}`,
		details: { kind: "checkpoint", goalId, objective: `Objective ${goalId}: ${"y".repeat(2000)}` },
	};
	return JSON.stringify(entry);
}

function persistedCheckpointChars(turns) {
	let total = 0;
	for (let i = 0; i < turns; i += 1) {
		total += checkpointTriggerPrompt("bench-goal").length;
	}
	return total;
}

function taskTree(count) {
	const tasks = [];
	for (let i = 0; i < count; i += 1) {
		tasks.push({
			id: `t${i}`,
			title: `Task ${i}: implement the sub-feature with a reasonably descriptive title`,
			status: i < count / 2 ? "complete" : "pending",
			...(i >= count / 2 ? { verificationContract: "run the suite and confirm green" } : {}),
		});
	}
	return tasks;
}

export function run(baseline) {
	// ── persisted growth ─────────────────────────────────────────────────
	const v2MarkerLen = checkpointTriggerPrompt("bench-goal").length;
	if (v2MarkerLen > CHECKPOINT_TRIGGER_MAX_CHARS) {
		throw new Error(`v2 marker ${v2MarkerLen} chars exceeds bound ${CHECKPOINT_TRIGGER_MAX_CHARS}`);
	}
	for (const turns of [1, 10, 100, 1000]) {
		const chars = persistedCheckpointChars(turns);
		baseline.add({
			id: `B10.checkpoint.persisted.${turns}`,
			label: `persisted checkpoint chars (${turns} turns)`,
			modules: "prompts/goal-prompts",
			fixture: `${turns} unchanged continuation turns`,
			p50: "-", p95: "-", max: "-",
			ops: chars,
			n: 1,
			notes: `v2 marker is ${v2MarkerLen} chars; objective occurrences in persisted checkpoints must be 0`,
		});
	}

	// Objective leakage check over the largest fixture.
	const leaked = persistedCheckpointChars(1000);
	if (leaked > 160_000) throw new Error(`B10 gate: persisted chars at 1000 turns = ${leaked} > 160000`);

	// ── provider-visible history after context compaction ────────────────
	const goal = makeGoalRecord({ objective: "Implement the full feature set." });
	goal.taskList = { tasks: taskTree(50), blockCompletion: true, proposedAt: new Date().toISOString() };
	goal.verificationContract = "Run the test suite.";
	const messages = [];
	messages.push({ role: "user", content: [{ type: "text", text: "start" }] });
	for (let i = 0; i < 1000; i += 1) {
		messages.push({
			role: "custom",
			customType: "pi-goal-event",
			display: false,
			content: legacyCheckpointLine("bench-goal", i),
			details: { kind: "checkpoint", goalId: "bench-goal", objective: "big" },
		});
		messages.push({ role: "assistant", content: [{ type: "text", text: "work" }], stopReason: "end_turn" });
	}
	messages.push({
		role: "custom",
		customType: "pi-goal-event",
		display: false,
		content: checkpointTriggerPrompt("bench-goal"),
		details: { version: 2, kind: "checkpoint", goalId: "bench-goal", status: "active", revision: 0, checkpointSeq: 1001, timestamp: Date.now() },
	});
	const compacted = compactGoalCheckpointContext(messages, goal);
	const checkpoints = compacted.filter(m => m && m.customType === "pi-goal-event");
	// Since #68, tiny triggers stay in their original positions for cache continuity.
	// Full goal snapshots must still never survive normalization.
	const visibleCheckpoints = checkpoints.filter(m => String(m.content).length > CHECKPOINT_TRIGGER_MAX_CHARS || String(m.content).includes("[PI GOAL ACTIVE")).length;
	if (visibleCheckpoints !== 0) throw new Error(`B10 gate: ${visibleCheckpoints} unbounded/full-state checkpoints`);
	if (compacted.length !== messages.length || checkpoints.length !== 1001) throw new Error("B10 gate: normalization changed history positions");
	for (let i = 0; i < messages.length; i++) if (messages[i].role === "assistant" && compacted[i] !== messages[i]) throw new Error("B10 gate: assistant history changed");
	baseline.add({
		id: "B10.checkpoint.provider-context.1000",
		label: "unbounded provider-visible checkpoints (1000-turn fixture)",
		modules: "extensions/goal-events",
		fixture: "1001 checkpoints (1000 legacy + 1 v2), 50-task tree",
		p50: "-", p95: "-", max: "-",
		ops: visibleCheckpoints,
		n: 1,
		notes: "must be 0; all 1001 bounded triggers retain their history positions",
	});

	// ── legacy session recovery (850-entry issue #30 fixture) ────────────
	const lines = ["session-header"];
	for (let i = 0; i < 850; i += 1) lines.push(legacyCheckpointLine("g30", i));
	const originalText = lines.join("\n");
	const result = transformSessionLines(originalText);
	const recoveredCheckpointChars = result.outputLines.reduce((sum, line) => {
		try {
			const parsed = JSON.parse(line);
			if (parsed?.customType === "pi-goal-event") return sum + parsed.content.length;
		} catch {}
		return sum;
	}, 0);
	if (result.changed !== 850) throw new Error(`B10 gate: expected 850 rewritten checkpoints, got ${result.changed}`);
	if (recoveredCheckpointChars > LEGACY_CHECKPOINT_CHARS * 850 * 0.05) {
		throw new Error(`B10 gate: recovered chars ${recoveredCheckpointChars} exceed 5% of legacy`);
	}
	baseline.add({
		id: "B10.checkpoint.recovery.850",
		label: "checkpoint chars after offline recovery (850-entry legacy fixture)",
		modules: "scripts/recover-session-checkpoints.mjs",
		fixture: `850 legacy checkpoints (~${LEGACY_CHECKPOINT_CHARS} chars each)`,
		p50: "-", p95: "-", max: "-",
		ops: recoveredCheckpointChars,
		n: 1,
		notes: `was ~${LEGACY_CHECKPOINT_CHARS * 850} chars; saved ${result.savedChars}; must be <= 5% of legacy`,
	});

	// ── composed request duplication ──────────────────────────────────────
	// The composed request ≈ system prompt (with one injected goal block) plus
	// the provider-visible messages (at most one checkpoint marker). Counting
	// full-goal-block markers and objective occurrences guards PR A's
	// single-source invariant.
	const systemPrompt = `${"base system ".repeat(10)}\n\n${goalPrompt(goal)}`;
	const composed = systemPrompt + "\n" + compacted.map(m => typeof m.content === "string" ? m.content : JSON.stringify(m.content)).join("\n");
	const fullGoalBlocks = (composed.match(/\[PI GOAL ACTIVE goalId=/g) ?? []).length;
	const objectiveOccurrences = (composed.match(/Implement the full feature set\./g) ?? []).length;
	if (fullGoalBlocks !== 1) throw new Error(`B10 gate: composed full goal blocks = ${fullGoalBlocks} != 1`);
	if (objectiveOccurrences !== 1) throw new Error(`B10 gate: composed objective occurrences = ${objectiveOccurrences} != 1`);
	baseline.add({
		id: "B10.checkpoint.composed-request.50t",
		label: "full goal blocks in composed request (50-task tree)",
		modules: "prompts/goal-prompts + extensions/goal-events",
		fixture: "system prompt with injected goal block + latest checkpoint marker",
		p50: "-", p95: "-", max: "-",
		ops: fullGoalBlocks,
		n: 1,
		notes: `objective occurs ${objectiveOccurrences}x; both counts must be exactly 1`,
	});

	return baseline;
}

if (import.meta.url === `file://${process.argv[1]}`) {
	import("./bench-common.mjs").then(async ({ Baseline }) => {
		const baseline = new Baseline();
		run(baseline);
		process.stdout.write(baseline.markdown());
	});
}
