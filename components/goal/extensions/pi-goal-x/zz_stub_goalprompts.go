package pi_goal_x

// Temporary stubs for symbols owned by other wave-1 slices. Each body panics; twins that reach one stay red until integration
// replaces the stub with the owner's implementation. Delete this file at integration.

// ── GoalTasksPolicy: goal-task-index.ts, goal-contract.ts, goal-tool-names.ts, goal-detail.ts ──

type goalTaskRow struct {
	task     *jsObject
	depth    int
	parentId string
}

type goalTaskIndex struct {
	tasks    []any
	byId     map[string]*jsObject
	ordered  []goalTaskRow
	pending  []*jsObject
	complete int
	skipped  int
}

func taskIndex(tasks []any) *goalTaskIndex { panic("stub: owned by GoalTasksPolicy") }

func promptSafeObjective(objective string) string { panic("stub: owned by GoalTasksPolicy") }

// postStopAllowedTools is POST_STOP_ALLOWED_TOOLS.
var postStopAllowedTools = []string{"get_goal"}

type goalDetailQuery struct {
	section string
	taskId  *string
	cursor  string
}

type goalDetailResult struct {
	ok         bool
	text       string
	content    string
	nextCursor string
	hasNext    bool
	totalChars int
}

func goalDetailPage(goal *jsObject, q goalDetailQuery, events []*jsObject) goalDetailResult {
	panic("stub: owned by GoalTasksPolicy")
}

// ── GoalService: goal-scheduler-state.ts ──

func schedulerSummaryParts(s *jsObject, limit *float64, showRuns bool) (runs, instructions string) {
	panic("stub: owned by GoalService")
}

// ── GoalLedger: goal-ledger.ts, goal-activity.ts ──

type orderedMap[V any] struct {
	ids []string
	m   map[string]V
}

func (o *orderedMap[V]) get(id string) (V, bool) { panic("stub: owned by GoalLedger") }
func (o *orderedMap[V]) has(id string) bool      { panic("stub: owned by GoalLedger") }
func (o *orderedMap[V]) size() int               { panic("stub: owned by GoalLedger") }
func (o *orderedMap[V]) keys() []string          { panic("stub: owned by GoalLedger") }

type ledgerStateMap = orderedMap[*reconstructedGoalState]

type reconstructedGoalState struct {
	goalID                     string
	latestStatus               string
	latestFocus                bool
	latestPauseReason          *string
	latestPauseSuggestedAction *string
	latestAuditorResult        *jsObject
	latestOracleResult         *jsObject
	createdAt                  *string
	completedAt                *string
	abortedAt                  *string
	tweakedAt                  *string
	resumedAt                  *string
}

type goalLedgerState struct {
	focusedGoalID *string
	goals         *ledgerStateMap
	terminalGoals *ledgerStateMap
}

type ledgerStateReadResult struct {
	state              *goalLedgerState
	recentEventsByGoal map[string][]*jsObject
	malformed          int
	coveredBytes       int64
	coveredEvents      int
	source             string
}

type ledgerRevision struct{ _ byte }

type goalLedgerReadResult struct {
	events    []*jsObject
	malformed int
	revision  *ledgerRevision
}

type goalAppendResult struct {
	ok  bool
	err error
}

const ledgerCheckpointFile = ".goal-ledger-checkpoint.json"

type goalActivityItem struct {
	at, kind, text string
	marker         string
}

type deriveGoalActivityOptions struct {
	taskTitles map[string]string
	limit      *int
}

func latestEventsForGoal(events []*jsObject, goalID string, limit int) []*jsObject {
	panic("stub: owned by GoalLedger")
}

func latestAuditorResultForGoal(events []*jsObject, goalID string) *jsObject {
	panic("stub: owned by GoalLedger")
}

func reconstructGoalLedger(events []*jsObject) *goalLedgerState { panic("stub: owned by GoalLedger") }

func appendGoalEvent(ctx storage, event *jsObject) goalAppendResult {
	panic("stub: owned by GoalLedger")
}

func appendGoalEvents(ctx storage, events []*jsObject) goalAppendResult {
	panic("stub: owned by GoalLedger")
}

func readGoalLedger(ctx storage) goalLedgerReadResult { panic("stub: owned by GoalLedger") }

func invalidateGoalLedgerCache() { panic("stub: owned by GoalLedger") }

func goalActivityEvents(ctx storage, goalID string) []*jsObject {
	panic("stub: owned by GoalLedger")
}

func goalOracleState(ctx storage, goalID, fingerprint string) *jsObject {
	panic("stub: owned by GoalLedger")
}

func loadLedgerState(ctx storage) ledgerStateReadResult { panic("stub: owned by GoalLedger") }

func deriveGoalActivity(events []*jsObject, goalID string, o deriveGoalActivityOptions) []goalActivityItem {
	panic("stub: owned by GoalLedger")
}

// ── GoalDashModel: widgets/goal-dashboard-model.ts ──

type dashboardUsage struct {
	activeSeconds float64
	tokens        float64
	footerBits    string
}

type dashboardTaskNode struct {
	id    string
	title string
}

type goalDashboardModel struct {
	usage    dashboardUsage
	taskTree []dashboardTaskNode
}

type goalDashboardModelOptions struct {
	focused        bool
	otherOpenGoals int
}

func deriveGoalDashboardModel(goal *jsObject, options goalDashboardModelOptions) *goalDashboardModel {
	panic("stub: owned by GoalDashModel")
}

// ── GoalSettings: goal-settings.ts ──

type settingsScope string

const (
	settingsScopeGlobal  settingsScope = "global"
	settingsScopeProject settingsScope = "project"
)

type settingsDiagnostic struct {
	scope       settingsScope
	path        string
	settingPath string
	code        string
	message     string
}

type resolvedSetting struct {
	value  any
	source string
	envVar string
}

type settingsSnapshot struct {
	value       goalSettings
	provenance  map[string]*resolvedSetting
	diagnostics []settingsDiagnostic
}

type settingsMutation struct {
	op    string
	path  []string
	value any
}

type settingsMutateInput struct {
	scope    settingsScope
	cwd      string
	env      map[string]string
	mutation settingsMutation
}

func parseSettingsLayer(raw any, scope settingsScope, filePath string) (*jsObject, []settingsDiagnostic) {
	panic("stub: owned by GoalSettings")
}

func loadSettingsSnapshot(cwd string, env ...map[string]string) *settingsSnapshot {
	panic("stub: owned by GoalSettings")
}

func mutateSettingsLayer(input settingsMutateInput) (*settingsSnapshot, error) {
	panic("stub: owned by GoalSettings")
}

func invalidateGoalSettingsCache() { panic("stub: owned by GoalSettings") }
