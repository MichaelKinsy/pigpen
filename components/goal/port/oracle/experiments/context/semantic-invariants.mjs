/**
 * Semantic occurrence counting over the SERIALIZED composed request (PR D).
 *
 * Every semantic field the goal system must communicate is counted exactly:
 * later optimization PRs must keep required markers present while removing
 * duplicate restatements.
 */

const MARKERS = {
	objective: null, // per-fixture: the exact objective text
	verificationContract: null, // per-fixture
	currentTask: (fixture) => fixture.currentTaskLine,
	lifecyclePolicyThirdBlocker: /SAME blocker recur(s)? on three consecutive goal turns|THIRD consecutive identical blocker/g,
	independentAuditor: /independent (completion )?auditor/g,
	neverEditObjective: /objective is immutable|never edit it yourself|never edit the objective/g,
	blockerPolicyUpdateGoal: /update_goal\(\{status: "blocked"\}\)/g,
	completionPolicyUpdateGoal: /update_goal\(\{status: "complete"\}\)/g,
	goalActiveMarker: /\[PI GOAL ACTIVE goalId=/g,
	checkpointMarker: /pi_goal_continuation/g,
	unfocusedSafety: /\[PI GOAL UNFOCUSED\]/g,
	pausedMarker: /\[PI GOAL PAUSED goalId=/g,
	budgetLimitedMarker: /\[PI GOAL BUDGET LIMITED goalId=/g,
	staleMarker: /\[GOAL STALE goalId=/g,
	postCompactionMarker: /\[POST-COMPACTION RESYNC goalId=/g,
};

function countMatches(text, pattern) {
	if (!pattern) return 0;
	const matches = text.match(pattern);
	return matches ? matches.length : 0;
}

/**
 * Count semantic fields in a captured request.
 * `fixtureContext` supplies per-fixture needles: objective, verification
 * contract, current-task line.
 */
export function countSemanticOccurrences(requestText, { objective, verificationContract, currentTaskLine } = {}) {
	return {
		objective: objective ? countMatches(requestText, new RegExp(escapeRegExp(objective), "g")) : 0,
		verificationContract: verificationContract ? countMatches(requestText, new RegExp(escapeRegExp(verificationContract), "g")) : 0,
		currentTask: currentTaskLine ? countMatches(requestText, new RegExp(escapeRegExp(currentTaskLine), "g")) : 0,
		lifecyclePolicyThirdBlocker: countMatches(requestText, MARKERS.lifecyclePolicyThirdBlocker),
		independentAuditor: countMatches(requestText, MARKERS.independentAuditor),
		neverEditObjective: countMatches(requestText, MARKERS.neverEditObjective),
		blockerPolicyUpdateGoal: countMatches(requestText, MARKERS.blockerPolicyUpdateGoal),
		completionPolicyUpdateGoal: countMatches(requestText, MARKERS.completionPolicyUpdateGoal),
		goalActiveMarker: countMatches(requestText, MARKERS.goalActiveMarker),
		checkpointMarker: countMatches(requestText, MARKERS.checkpointMarker),
		unfocusedSafety: countMatches(requestText, MARKERS.unfocusedSafety),
		pausedMarker: countMatches(requestText, MARKERS.pausedMarker),
		budgetLimitedMarker: countMatches(requestText, MARKERS.budgetLimitedMarker),
		staleMarker: countMatches(requestText, MARKERS.staleMarker),
		postCompactionMarker: countMatches(requestText, MARKERS.postCompactionMarker),
	};
}

function escapeRegExp(text) {
	return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Current-task needle as rendered by taskListBlock ("Current: <id> · <title>"). */
export function currentTaskNeedle(goal) {
	if (!goal?.currentTaskId || !goal.taskList) return undefined;
	const find = (tasks) => {
		for (const t of tasks) {
			if (t.id === goal.currentTaskId) return t;
			const nested = t.subtasks && find(t.subtasks);
			if (nested) return nested;
		}
		return undefined;
	};
	const task = find(goal.taskList.tasks);
	return task ? `Current: ${task.id} · ${task.title}` : undefined;
}
