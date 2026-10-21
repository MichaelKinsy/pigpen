package pi_goal_x

import "strings"

// ported from goal-compaction.ts

// jsText renders a JSON value the way a template literal does.
func jsText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return jsNumber(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	}
	return "undefined"
}

func evText(e *jsObject, k string) string {
	v, _ := e.get(k)
	return jsText(v)
}

// buildGoalCompactSummary summarizes one goal; auditorOverride (verdict/report/at) wins over the ledger's latest result.
func buildGoalCompactSummary(goal *jsObject, events []*jsObject, auditorOverride *jsObject) string {
	var lines []string
	lines = append(lines, "Goal "+gstr(goal, "id")+" — "+statusLabel(goal))
	lines = append(lines, "  Objective: "+truncateText(gstr(goal, "objective"), 200))
	tokens, seconds := usageOf(goal)
	if tokens > 0 {
		lines = append(lines, "  Cumulative goal usage (not context occupancy): "+formatTokenValue(tokens))
	}
	if seconds > 0 {
		lines = append(lines, "  Time: "+formatDuration(seconds))
	}

	recent := latestEventsForGoal(events, gstr(goal, "id"), 5)
	if len(recent) > 0 {
		lines = append(lines, "  Recent events:")
		for _, event := range recent {
			switch gstr(event, "type") {
			case "goal_paused":
				lines = append(lines, "    - paused: "+evText(event, "reason"))
			case "goal_resumed":
				lines = append(lines, "    - resumed: "+evText(event, "reason"))
			case "goal_tweaked":
				lines = append(lines, "    - tweaked: "+evText(event, "changeSummary"))
			case "auditor_toggled":
				state := "off"
				if gflag(event, "enabled") {
					state = "on"
				}
				lines = append(lines, "    - auditor toggled "+state)
			case "completion_requested":
				suffix := ""
				if s := gstr(event, "summary"); s != "" {
					suffix = ": " + truncateText(s, 80)
				}
				lines = append(lines, "    - completion requested"+suffix)
			case "audit_result":
				suffix := ""
				if gstr(event, "verdict") == "disapproved" {
					suffix = ": " + truncateText(gstr(event, "report"), 80)
				}
				lines = append(lines, "    - auditor "+evText(event, "verdict")+suffix)
			case "goal_completed":
				lines = append(lines, "    - completed")
			case "task_list_set":
				suffix := ""
				if gflag(event, "blockCompletion") {
					suffix = " (blocking)"
				}
				lines = append(lines, "    - task list set: "+evText(event, "taskCount")+" tasks"+suffix)
			case "task_complete":
				suffix := ""
				if e := gstr(event, "evidence"); e != "" {
					suffix = " — " + truncateText(e, 60)
				}
				lines = append(lines, "    - task complete: "+evText(event, "taskId")+suffix)
			case "task_skipped":
				lines = append(lines, "    - task skipped: "+evText(event, "taskId")+" — "+truncateText(gstr(event, "reason"), 60))
			case "task_reopened":
				lines = append(lines, "    - task reopened: "+evText(event, "taskId"))
			case "goal_aborted":
				lines = append(lines, "    - aborted: "+evText(event, "reason"))
			}
		}
	}

	auditor := auditorOverride
	if auditor == nil {
		auditor = latestAuditorResultForGoal(events, gstr(goal, "id"))
	}
	if auditor != nil && gstr(auditor, "verdict") == "disapproved" {
		lines = append(lines, "  Auditor rejection (latest): "+truncateText(gstr(auditor, "report"), 120))
	}

	if r := gstr(goal, "pauseReason"); r != "" {
		lines = append(lines, "  Pause reason: "+r)
	}
	if a := gstr(goal, "pauseSuggestedAction"); a != "" {
		lines = append(lines, "  Suggested action: "+a)
	}
	return strings.Join(lines, "\n")
}

// compactionSummaryArgs mirrors the args object of buildCompactionSummary. ledgerState is the checkpoint-fed view (reconstructed
// state plus per-goal recent tails instead of a full event list); nil caps default to 20 open goals and 5 events per goal.
type compactionSummaryArgs struct {
	goalsById        *goalPool
	focusedGoalId    *string
	ledgerEvents     []*jsObject
	ledgerState      *ledgerStateReadResult
	capOpenGoals     *int
	capEventsPerGoal *int
}

func buildCompactionSummary(args compactionSummaryArgs) string {
	capOpenGoals, capEventsPerGoal := 20, 5
	if args.capOpenGoals != nil {
		capOpenGoals = *args.capOpenGoals
	}
	if args.capEventsPerGoal != nil {
		capEventsPerGoal = *args.capEventsPerGoal
	}
	goalsById := args.goalsById
	if goalsById == nil {
		goalsById = newPool()
	}

	var lines []string
	var openGoals []*jsObject
	for _, g := range goalsById.values() {
		if gstr(g, "status") != "complete" {
			openGoals = append(openGoals, g)
		}
	}
	var reconstructed *goalLedgerState
	if args.ledgerState != nil {
		reconstructed = args.ledgerState.state
	} else {
		reconstructed = reconstructGoalLedger(args.ledgerEvents)
	}
	recentEventsFor := func(goalID string, limit int) []*jsObject {
		if args.ledgerState != nil {
			recent := args.ledgerState.recentEventsByGoal[goalID]
			if len(recent) > limit {
				recent = recent[:limit]
			}
			return recent
		}
		return latestEventsForGoal(args.ledgerEvents, goalID, limit)
	}

	if args.focusedGoalId != nil && *args.focusedGoalId != "" && goalsById.has(*args.focusedGoalId) {
		id := *args.focusedGoalId
		focused := goalsById.get(id)
		var auditor *jsObject
		if args.ledgerState != nil {
			if s, ok := reconstructed.goals.get(id); ok && s.latestAuditorResult != nil {
				auditor = s.latestAuditorResult
			} else if s, ok := reconstructed.terminalGoals.get(id); ok {
				auditor = s.latestAuditorResult
			}
		}
		lines = append(lines, "[FOCUSED GOAL]")
		lines = append(lines, buildGoalCompactSummary(focused, recentEventsFor(id, capEventsPerGoal), auditor))
		lines = append(lines, "")
	}

	var otherOpen []*jsObject
	for _, g := range openGoals {
		if args.focusedGoalId == nil || gstr(g, "id") != *args.focusedGoalId {
			otherOpen = append(otherOpen, g)
		}
	}
	if len(otherOpen) > 0 {
		lines = append(lines, "[OTHER OPEN GOALS — "+jsNumber(float64(len(otherOpen)))+" total]")
		shown := otherOpen
		if len(shown) > capOpenGoals {
			shown = shown[:capOpenGoals]
		}
		for _, g := range shown {
			lines = append(lines, "- "+gstr(g, "id")+" — "+statusLabel(g)+" — "+truncateText(gstr(g, "objective"), 120))
		}
		if len(otherOpen) > capOpenGoals {
			lines = append(lines, "... and "+jsNumber(float64(len(otherOpen)-capOpenGoals))+" more")
		}
		lines = append(lines, "")
	}

	if reconstructed.terminalGoals.size() > 0 {
		lines = append(lines, "[TERMINAL GOALS — "+jsNumber(float64(reconstructed.terminalGoals.size()))+" completed or aborted]")
		for _, goalID := range reconstructed.terminalGoals.keys() {
			state, _ := reconstructed.terminalGoals.get(goalID)
			label := "aborted"
			if state.latestStatus == "complete" {
				label = "completed"
			}
			line := "- " + goalID + " — " + label
			if state.completedAt != nil && *state.completedAt != "" {
				line += " at " + *state.completedAt
			}
			if state.abortedAt != nil && *state.abortedAt != "" {
				line += " at " + *state.abortedAt
			}
			lines = append(lines, line)
		}
		lines = append(lines, "")
	}

	if len(openGoals) == 0 && reconstructed.terminalGoals.size() == 0 {
		lines = append(lines, "[NO GOALS]")
		lines = append(lines, "No open or terminal goals recorded in this session.")
	}

	lines = append(lines, "[INSTRUCTION]")
	lines = append(lines, "Continue from the focused goal above, or ask the user to run /goal-focus.")
	lines = append(lines, "Do not rely on chat memory for goal state; use the facts above.")
	return strings.Join(lines, "\n")
}

// postCompactionDeltaArgs mirrors the args object of buildPostCompactionGoalDelta.
type postCompactionDeltaArgs struct {
	goal           *jsObject
	ledgerEvents   []*jsObject
	otherOpenCount int
}

// buildPostCompactionGoalDelta (PR E §62) is the post-compaction DELTA for the focused goal: the execution focus, a bounded
// recent-events tail, the latest unresolved auditor finding, and the other-open-goals count.
func buildPostCompactionGoalDelta(args postCompactionDeltaArgs) string {
	goal := args.goal
	var lines []string
	lines = append(lines, "[POST-COMPACTION RESYNC goalId="+gstr(goal, "id")+"]")
	// Execution focus (with contract) — the one thing memory must not lose.
	if cur := gstr(goal, "currentTaskId"); cur != "" && goal.obj("taskList") != nil {
		tasks, _ := goal.obj("taskList").get("tasks")
		list, _ := tasks.([]any)
		if current, ok := taskIndex(list).byId[cur]; ok {
			contract := gstr(current, "verificationContract")
			suffix := ""
			if contract != "" {
				suffix = " (contract: " + truncateText(contract, 600) + ")"
			}
			id, title := gstr(current, "id"), gstr(current, "title")
			lines = append(lines, "Current task: "+truncateText(id, 80)+" — "+truncateText(title, 180)+suffix)
			if utf16Len(id) > 80 || utf16Len(title) > 180 || utf16Len(contract) > 600 {
				lines = append(lines, `Retrieve omitted task requirements with get_goal(section="tasks") before continuing.`)
			}
		}
	}
	// Bounded recent-event tail.
	recent := latestEventsForGoal(args.ledgerEvents, gstr(goal, "id"), 5)
	if len(recent) > 0 {
		lines = append(lines, "Recent events:")
		for _, event := range recent {
			at := compactionSlice(gstr(event, "at"), 11, 19)
			lines = append(lines, "  "+at+" "+gstr(event, "type"))
		}
	}
	// Latest unresolved auditor finding.
	if audit := latestAuditorResultForGoal(args.ledgerEvents, gstr(goal, "id")); audit != nil && gstr(audit, "verdict") == "disapproved" {
		lines = append(lines, "Latest unresolved auditor finding: "+utf16Prefix(gstr(audit, "report"), 300))
	}
	lines = append(lines, "Other open goals: "+jsNumber(float64(args.otherOpenCount)))
	lines = append(lines, "Continue from authoritative files and goal storage.")
	return strings.Join(lines, "\n")
}

// compactionSlice is s.slice(from, to) in UTF-16 units for non-negative bounds.
func compactionSlice(s string, from, to int) string {
	head := utf16Prefix(s, to)
	return strings.TrimPrefix(head, utf16Prefix(head, from))
}
