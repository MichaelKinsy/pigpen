/**
 * Activity feed tests (plan §19.3): durable ledger events map to readable,
 * capped, deduplicated activity items with task titles preferred over ids.
 */

import assert from "node:assert/strict";
import test from "node:test";

import { deriveGoalActivity } from "../extensions/goal-activity.ts";
import type { GoalLedgerEvent } from "../extensions/goal-ledger.ts";

function ev(type: string, at: string, extra: Record<string, unknown> = {}): GoalLedgerEvent {
	return { type, goalId: "g1", at, ...extra } as unknown as GoalLedgerEvent;
}

const TITLES = new Map<string, string>([
	["t1", "Review reports page and data source"],
	["t2", "Implement filtered CSV export"],
	["t3", "Add the download button"],
]);

// ---------------------------------------------------------------------------
// Event → readable text mapping
// ---------------------------------------------------------------------------

test("lifecycle events map to readable activity text", () => {
	const events = [
		ev("goal_created", "2026-01-01T09:00:00.000Z", { objective: "x", sisyphus: false, autoContinue: true }),
		ev("goal_tweaked", "2026-01-01T09:01:00.000Z", { changeSummary: "plan" }),
		ev("goal_paused", "2026-01-01T09:02:00.000Z", { reason: "User away", suggestedAction: "Resume later" }),
		ev("goal_resumed", "2026-01-01T09:03:00.000Z", { reason: "Back" }),
		ev("goal_blocked", "2026-01-01T09:04:00.000Z", { reason: "Missing API key", source: "agent" }),
		ev("goal_budget_limited", "2026-01-01T09:05:00.000Z", { budget: 10000, tokensUsed: 10000 }),
		ev("completion_requested", "2026-01-01T09:06:00.000Z", { summary: "done" }),
		ev("audit_started", "2026-01-01T09:07:00.000Z", { provider: "anthropic", model: "x" }),
		ev("audit_result", "2026-01-01T09:08:00.000Z", { verdict: "approved", report: "ok" }),
		ev("goal_completed", "2026-01-01T09:09:00.000Z", { archivePath: ".pi/goals/archived/goal_g1.md" }),
		ev("goal_archived", "2026-01-01T09:10:00.000Z", { archivePath: ".pi/goals/archived/goal_g1.md" }),
	];
	const texts = deriveGoalActivity(events, "g1", { limit: 20 }).map((a) => a.text);
	assert.deepEqual(texts, [
		"Created and focused the goal.",
		"Updated the goal objective and task plan.",
		"Paused the goal — User away",
		"Resumed the goal.",
		"Blocked — Missing API key",
		"Reached the configured token budget.",
		"Requested completion review.",
		"Started independent completion review.",
		"Independent auditor approved completion.",
		"Completed the goal.",
		"Archived the completed goal.",
	]);
});

test("audit rejection and error verdicts map distinctly", () => {
	const rejected = deriveGoalActivity([ev("audit_result", "2026-01-01T09:00:00.000Z", { verdict: "disapproved", report: "tests missing" })], "g1");
	assert.equal(rejected[0]!.text, "Completion review requested additional work.");
	const errored = deriveGoalActivity([ev("audit_result", "2026-01-01T09:00:00.000Z", { verdict: "error", report: "boom" })], "g1");
	assert.equal(errored[0]!.text, "Completion review could not finish.");
});

test("audit-skipped distinguishes disabled from user-aborted", () => {
	const disabled = deriveGoalActivity([ev("audit_skipped", "2026-01-01T09:00:00.000Z", { reason: "disabled" })], "g1");
	assert.equal(disabled[0]!.text, "Skipped independent completion review (auditor disabled).");
	const aborted = deriveGoalActivity([ev("audit_skipped", "2026-01-01T09:00:00.000Z", { reason: "user_aborted" })], "g1");
	assert.equal(aborted[0]!.text, "Completion review was aborted by the user.");
});

test("task events prefer task titles over ids", () => {
	const events = [
		ev("task_started", "2026-01-01T09:00:00.000Z", { taskId: "t3" }),
		ev("task_complete", "2026-01-01T09:05:00.000Z", { taskId: "t2", evidence: "All tests pass" }),
		ev("task_skipped", "2026-01-01T09:06:00.000Z", { taskId: "t1", reason: "out of scope" }),
		ev("task_reopened", "2026-01-01T09:07:00.000Z", { taskId: "t1" }),
	];
	const texts = deriveGoalActivity(events, "g1", { taskTitles: TITLES, limit: 20 }).map((a) => a.text);
	assert.deepEqual(texts, [
		"Started “Add the download button”.",
		"Completed “Implement filtered CSV export”. — All tests pass",
		"Skipped “Review reports page and data source” — out of scope.",
		"Reopened “Review reports page and data source”.",
	]);
});

test("unknown task ids fall back to the id in quotes", () => {
	const items = deriveGoalActivity([ev("task_started", "2026-01-01T09:00:00.000Z", { taskId: "t9" })], "g1");
	assert.equal(items[0]!.text, "Started “t9”.");
});

test("evidence is included only when concise", () => {
	const short = deriveGoalActivity([ev("task_complete", "2026-01-01T09:00:00.000Z", { taskId: "t1", evidence: "Done" })], "g1", {
		taskTitles: TITLES,
	});
	assert.equal(short[0]!.text, "Completed “Review reports page and data source”. — Done");
	const longEvidence = "x".repeat(200);
	const long = deriveGoalActivity([ev("task_complete", "2026-01-01T09:00:00.000Z", { taskId: "t1", evidence: longEvidence })], "g1", {
		taskTitles: TITLES,
	});
	assert.equal(long[0]!.text, "Completed “Review reports page and data source”.");
	assert.ok(!long[0]!.text.includes(longEvidence));
});

test("long reasons are truncated safely", () => {
	const reason = "because ".repeat(60);
	const items = deriveGoalActivity([ev("task_skipped", "2026-01-01T09:00:00.000Z", { taskId: "t1", reason })], "g1", {
		taskTitles: TITLES,
	});
	assert.ok(items[0]!.text.length < 200);
	assert.ok(items[0]!.text.endsWith("..."));
});

// ---------------------------------------------------------------------------
// Ordering, dedupe, capping, filtering
// ---------------------------------------------------------------------------

test("items are ordered chronologically (oldest first)", () => {
	const events = [
		ev("task_complete", "2026-01-01T09:05:00.000Z", { taskId: "t2" }),
		ev("task_started", "2026-01-01T09:00:00.000Z", { taskId: "t3" }),
		ev("goal_created", "2026-01-01T08:59:00.000Z", { objective: "x", sisyphus: false, autoContinue: true }),
	];
	const ats = deriveGoalActivity(events, "g1", { limit: 20 }).map((a) => a.at);
	assert.deepEqual(ats, ["2026-01-01T08:59:00.000Z", "2026-01-01T09:00:00.000Z", "2026-01-01T09:05:00.000Z"]);
});

test("duplicate lifecycle events are merged", () => {
	const events = [
		ev("goal_paused", "2026-01-01T09:00:00.000Z", { reason: "away" }),
		ev("goal_paused", "2026-01-01T09:01:00.000Z", { reason: "away" }),
		ev("goal_resumed", "2026-01-01T09:02:00.000Z", { reason: "back" }),
		ev("goal_paused", "2026-01-01T09:03:00.000Z", { reason: "away again" }),
	];
	const texts = deriveGoalActivity(events, "g1", { limit: 20 }).map((a) => a.text);
	assert.deepEqual(texts, ["Paused the goal — away", "Resumed the goal.", "Paused the goal — away again"]);
});

test("checkpoint noise is excluded", () => {
	const events = [
		ev("goal_created", "2026-01-01T09:00:00.000Z", { objective: "x", sisyphus: false, autoContinue: true }),
		ev("goal_focused", "2026-01-01T09:00:01.000Z", { reason: "selected" }),
		ev("goal_unfocused", "2026-01-01T09:00:02.000Z", { reason: "cleared" }),
		ev("task_list_set", "2026-01-01T09:00:03.000Z", { taskCount: 5, blockCompletion: false }),
		ev("goal_stalled", "2026-01-01T09:00:04.000Z", { reason: "no work" }),
		ev("goal_budget_warning", "2026-01-01T09:00:05.000Z", { budget: 10000, tokensUsed: 9000, pct: 90 }),
	];
	const texts = deriveGoalActivity(events, "g1", { limit: 20 }).map((a) => a.text);
	assert.deepEqual(texts, ["Created and focused the goal."]);
});

test("default cap is the latest five entries; full history is available on request", () => {
	const events = [1, 2, 3, 4, 5, 6, 7].map((n) =>
		ev("task_complete", `2026-01-01T09:0${n}:00.000Z`, { taskId: `t${n}` }),
	);
	const capped = deriveGoalActivity(events, "g1");
	assert.equal(capped.length, 5);
	assert.equal(capped[0]!.at, "2026-01-01T09:03:00.000Z");
	const full = deriveGoalActivity(events, "g1", { limit: 100 });
	assert.equal(full.length, 7);
});

test("only events for the requested goal are included", () => {
	const events = [
		ev("goal_created", "2026-01-01T09:00:00.000Z", { objective: "x", sisyphus: false, autoContinue: true }),
		{ ...ev("goal_created", "2026-01-01T09:01:00.000Z", { objective: "other", sisyphus: false, autoContinue: true }), goalId: "g2" },
	];
	const items = deriveGoalActivity(events, "g1");
	assert.equal(items.length, 1);
	assert.equal(items[0]!.at, "2026-01-01T09:00:00.000Z");
});
