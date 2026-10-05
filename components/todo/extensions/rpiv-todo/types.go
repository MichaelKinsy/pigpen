package rpiv_todo

// Tool and command identity: verbatim string boundaries of the original.
// upstream: tool/types.ts:11-22. The tool name "todo" is the key session replay filters toolResult
// entries by, so it is never renamed.
const (
	toolName    = "todo"
	toolLabel   = "Todo"
	commandName = "todos"
	widgetKey   = "rpiv-todos" // upstream: todo-overlay.ts:22

	errRequiresInteractive = "/todos requires interactive mode"
	msgNoTodos             = "No todos yet. Ask the agent to add some!"
)

// Task statuses (upstream: tool/types.ts:26).
const (
	statusPending    = "pending"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
	statusDeleted    = "deleted"
)

// task is one list entry. Optional text fields are pointers: the original sets them only when given
// (an empty string on update is kept and serialised), and omits them otherwise. BlockedBy ids are
// float64 because the tool's id parameters are JSON numbers and a non-integral id must be reported as
// "not found" the way JavaScript does. Metadata nil means the field is absent; an empty map is kept
// (the original keeps `{}` on create).
// upstream: tool/types.ts:28-37.
type task struct {
	ID          int
	Subject     string
	Description *string
	ActiveForm  *string
	Status      string
	BlockedBy   []float64
	Owner       *string
	Metadata    map[string]any
}

// taskState is the canonical state: the tasks and the next id. upstream: state/state.ts:12-15.
type taskState struct {
	Tasks  []task
	NextID int
}

// params is the open-shape input bag of a tool call (the decoded JSON arguments).
type params = map[string]any

// op is the closed reducer outcome. Kind is one of create, update, delete, list, get, clear, error.
// upstream: state/state-reducer.ts:20-27.
type op struct {
	Kind           string
	TaskID         int    // create
	ID             int    // update, delete
	FromStatus     string // update
	ToStatus       string // update
	Changed        bool   // update
	Subject        string // delete
	StatusFilter   string // list ("" means no filter)
	IncludeDeleted bool   // list
	Task           task   // get
	Count          int    // clear
	Message        string // error
}

type applyResult struct {
	State taskState
	Op    op
}
