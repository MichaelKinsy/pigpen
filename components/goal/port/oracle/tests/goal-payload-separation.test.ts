/**
 * PR F — UI/model payload separation acceptance tests:
 *   - audit-start events are display-only (no turn trigger);
 *   - no report_auditor_progress tool exists anywhere;
 *   - auditor prompt carries objective + task tree exactly once;
 *   - post-compaction injection is a delta, not a duplicate summary.
 */

import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import { buildGoalAuditorPrompt } from "../extensions/goal-auditor.ts";
import { buildPostCompactionGoalDelta } from "../extensions/goal-compaction.ts";
import { createGoal } from "../extensions/goal-record.ts";
import type { GoalRecord } from "../extensions/goal-record.ts";

const completionSource = readFileSync(new URL("../extensions/goal-completion.ts", import.meta.url), "utf8");
const auditorSource = readFileSync(new URL("../extensions/goal-auditor.ts", import.meta.url), "utf8");

function goal(overrides: Partial<GoalRecord> = {}): GoalRecord {
	const base = createGoal({ objective: "Deliver the audited thing.", autoContinue: true, sisyphus: false }, Date.UTC(2026, 7, 23, 9, 0, 0));
	base.taskList = {
		tasks: [
			{ id: "t1", title: "First task", status: "complete" },
			{ id: "t2", title: "Second task", status: "pending" },
		],
		blockCompletion: true,
		proposedAt: "2026-08-23T09:01:00.000Z",
	};
	base.verificationContract = "All suites green.";
	return { ...base, ...overrides };
}

describe("PR F §59/§60: audit event separation and progress tool removal", () => {

	it("the report_auditor_progress tool no longer exists in any shipped surface", () => {
		assert.doesNotMatch(auditorSource, /name:\s*"report_auditor_progress"/);
		assert.doesNotMatch(completionSource, /report_auditor_progress/);
		const goalCoreTools = readFileSync(new URL("../extensions/goal-core-tools.ts", import.meta.url), "utf8");
		assert.doesNotMatch(goalCoreTools, /report_auditor_progress/);
	});
});

describe("PR F §61: single-source auditor prompt", () => {
	it("objective appears exactly once; task tree exactly once", () => {
		const g = goal();
		const prompt = buildGoalAuditorPrompt({
			goal: g,
			detailedSummary: `Goal: ${g.objective}\nStatus: running\nTasks: 1/2 complete`, // legacy verbose summary input
			completionSummary: "I claim it is done.",
		});
		assert.equal(prompt.split("Deliver the audited thing.").length - 1, 1, "objective occurs once");
		assert.equal(prompt.split("[ ] t2").length - 1, 1, "task tree rendered once");
		assert.ok(!prompt.includes("report_auditor_progress"), "no progress protocol in the prompt");
	});
});

describe("PR F §62: post-compaction delta", () => {
	it("carries focus/events/findings without repeating objective or policy", () => {
		const g = goal({ currentTaskId: "t2" });
		g.taskList!.tasks[1]!.verificationContract = "Prove t2.";
		const ledger = [
			{ type: "goal_resumed", goalId: g.id, reason: "user", at: "2026-08-23T10:00:00.000Z" },
			{ type: "audit_result", goalId: g.id, verdict: "disapproved", report: "Missing evidence for t2.", at: "2026-08-23T11:00:00.000Z" },
		];
		const delta = buildPostCompactionGoalDelta({ goal: g, ledgerEvents: ledger as never, otherOpenCount: 3 });
		assert.match(delta, /\[POST-COMPACTION RESYNC goalId=/);
		assert.match(delta, /Current task: t2 — Second task \(contract: Prove t2\.\)/);
		assert.match(delta, /goal_resumed/);
		assert.match(delta, /Latest unresolved auditor finding: Missing evidence for t2\./);
		assert.match(delta, /Other open goals: 3/);
		// The active system block already has these — the delta must not repeat them.
		assert.ok(!delta.includes(g.objective), "delta must not repeat the objective");
		assert.ok(!delta.includes("update_goal"), "delta must not restate lifecycle policy");
	});
});
