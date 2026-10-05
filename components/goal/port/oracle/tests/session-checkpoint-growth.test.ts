/**
 * Issue #30 reproducer: repeated full checkpoint prompts.
 *
 * Pins the bounded-checkpoint contract for continuation scheduling BEFORE the
 * v2 marker fix lands (fails against the legacy full-prompt builder):
 *
 *   1. A scheduled continuation must persist a tiny structured marker
 *      (≤160 chars), never the objective/task/contract/policy text.
 *   2. The persisted details must omit objective text entirely.
 */

import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { GoalRuntime } from "../extensions/goal-runtime.ts";
import { CHECKPOINT_TRIGGER_MAX_CHARS } from "../extensions/prompts/goal-prompts.ts";
import { createGoal } from "../extensions/goal-record.ts";
import type { GoalRecord } from "../extensions/goal-record.ts";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";

const LONG_OBJECTIVE = "Reproduce the repeated full checkpoint prompt growth defect. ".repeat(20);

function activeGoal(): GoalRecord {
	return createGoal({ objective: LONG_OBJECTIVE, autoContinue: true, sisyphus: false }, Date.UTC(2026, 7, 23, 9, 0, 0));
}

function idleCtx(): ExtensionContext {
	return {
		cwd: "/tmp",
		isIdle: () => true,
		hasPendingMessages: () => false,
	} as unknown as ExtensionContext;
}

interface SentMessage {
	content: string;
	details: Record<string, unknown>;
}

async function fireContinuation(goal: GoalRecord): Promise<SentMessage> {
	const sent: SentMessage[] = [];
	const runtime = new GoalRuntime({
		authorize: () => ({ generation: "test-generation", dispatchId: "test-dispatch" }),
		sendFollowUp: (content, details) => {
			sent.push({ content, details });
		},
		getGoal: () => goal,
		isActionable: () => true,
	});
	runtime.queueContinuation(idleCtx(), goal, true);
	// Idle ctx schedules with 0ms delay; wait past the timer.
	await new Promise((resolve) => setTimeout(resolve, 30));
	assert.equal(sent.length, 1, "exactly one follow-up must be delivered");
	return sent[0]!;
}

describe("issue #30: bounded continuation checkpoints", () => {
	it("persisted checkpoint content stays within the trigger bound", async () => {
		const sent = await fireContinuation(activeGoal());
		assert.ok(
			sent.content.length <= CHECKPOINT_TRIGGER_MAX_CHARS,
			`checkpoint content was ${sent.content.length} chars; bound is ${CHECKPOINT_TRIGGER_MAX_CHARS}`,
		);
	});

	it("persisted checkpoint omits objective, task, contract, and policy text", async () => {
		const sent = await fireContinuation(activeGoal());
		for (const forbidden of [LONG_OBJECTIVE.slice(0, 40), "TASK LIST", "VERIFICATION CONTRACT", "[OUTCOMES]", "lifecycle"]) {
			assert.ok(!sent.content.includes(forbidden), `checkpoint content leaked ${JSON.stringify(forbidden)}`);
			assert.ok(!JSON.stringify(sent.details).includes(forbidden), `checkpoint details leaked ${JSON.stringify(forbidden)}`);
		}
	});

	it("persisted details carry generation-tagged v3 fields", async () => {
		const goal = activeGoal();
		const sent = await fireContinuation(goal);
		assert.equal(sent.details.version, 3);
		assert.equal(sent.details.generation, "test-generation");
		assert.equal(sent.details.dispatchId, "test-dispatch");
		assert.equal(sent.details.kind, "checkpoint");
		assert.equal(sent.details.goalId, goal.id);
		assert.equal(sent.details.status, "active");
		assert.equal(typeof sent.details.checkpointSeq, "number");
	});

	it("sequential checkpoints increment checkpointSeq", async () => {
		const goal = activeGoal();
		const seqs: unknown[] = [];
		const runtime = new GoalRuntime({
		authorize: () => ({ generation: "test-generation", dispatchId: "test-dispatch" }),
			sendFollowUp: (_content, details) => {
				seqs.push(details.checkpointSeq);
			},
			getGoal: () => goal,
			isActionable: () => true,
		});
		const ctx = idleCtx();
		runtime.queueContinuation(ctx, goal, true);
		await new Promise((resolve) => setTimeout(resolve, 30));
		runtime.clearContinuationState();
		runtime.queueContinuation(ctx, goal, true);
		await new Promise((resolve) => setTimeout(resolve, 30));
		assert.deepEqual(seqs, [1, 2]);
	});
});
