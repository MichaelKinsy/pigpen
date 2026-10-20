package pi_goal_x

// Temporary stubs for symbols owned by other slices (GoalAuditor). Delete each when its owner's file lands.

// goal-settings.ts (GoalSettings)
func parseGoalSettings(raw any) (*jsObject, error) { panic("stub: owned by GoalSettings") }
func loadGoalSettingsFileConfig(cwd string, env ...map[string]string) *jsObject {
	panic("stub: owned by GoalSettings")
}
func saveGoalSettingsFileConfig(cwd string, settings *jsObject) (*jsObject, error) {
	panic("stub: owned by GoalSettings")
}
func goalSettingsPath(cwd string, env ...map[string]string) string {
	panic("stub: owned by GoalSettings")
}

// goal-ledger.ts (GoalLedger)
type ledgerRevision struct{}
type goalLedgerReadResult struct {
	events    []*jsObject
	malformed int
	revision  *ledgerRevision
}
type reconstructedGoalState struct {
	goalID       string
	latestStatus string
	completedAt  *string
	abortedAt    *string
}
type ledgerStateMap struct{}

func (m *ledgerStateMap) get(id string) (*reconstructedGoalState, bool) {
	panic("stub: owned by GoalLedger")
}
func (m *ledgerStateMap) size() int { panic("stub: owned by GoalLedger") }

type goalLedgerState struct {
	focusedGoalID *string
	goals         *ledgerStateMap
	terminalGoals *ledgerStateMap
}

func readGoalLedger(ctx storage) goalLedgerReadResult { panic("stub: owned by GoalLedger") }
func reconstructGoalLedger(events []*jsObject) *goalLedgerState {
	panic("stub: owned by GoalLedger")
}

// goal-compaction.ts (GoalPrompts)
type compactionSummaryArgs struct {
	goalsById                      *goalPool
	focusedGoalId                  *string
	ledgerEvents                   []*jsObject
	capOpenGoals, capEventsPerGoal *int
}
type postCompactionDeltaArgs struct {
	goal           *jsObject
	ledgerEvents   []*jsObject
	otherOpenCount int
}

func buildCompactionSummary(args compactionSummaryArgs) string { panic("stub: owned by GoalPrompts") }
func buildPostCompactionGoalDelta(args postCompactionDeltaArgs) string {
	panic("stub: owned by GoalPrompts")
}
