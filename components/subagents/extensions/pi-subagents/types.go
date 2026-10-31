package pi_subagents

// AgentSource says where a definition came from. The precedence is builtin < package < user < project.
type AgentSource string

const (
	SourceBuiltin AgentSource = "builtin"
	SourcePackage AgentSource = "package"
	SourceUser    AgentSource = "user"
	SourceProject AgentSource = "project"
)

// AgentScope selects which definitions an execution may see.
type AgentScope string

const (
	ScopeUser    AgentScope = "user"
	ScopeProject AgentScope = "project"
	ScopeBoth    AgentScope = "both"
)

// AgentConfig is a discovered agent definition (the part of the original's AgentConfig that this port reads).
type AgentConfig struct {
	Name        string // the runtime name: "<package>.<local>" or the local name
	LocalName   string
	PackageName string
	Description string
	Model       string
	// Thinking is nil when unset, false when the frontmatter turns thinking off, or a level string.
	Thinking              any
	Tools                 []string
	McpDirectTools        []string
	DefaultContext        string // "fork", "fresh" or ""
	AcceptanceRole        string
	DefaultReads          []string
	DefaultProgress       bool
	DefaultAsync          *bool
	Runner                *Runner
	Skills                []string
	Aliases               []string
	SystemPromptMode      string // "replace" or "append"
	InheritProjectContext bool
	InheritGlobalContext  bool
	InheritSkills         bool
	SystemPrompt          string
	Source                AgentSource
	FilePath              string
	discoveryPriority     int

	// Fields the management actions read and write (the original's AgentConfig);
	// the loader fills them from frontmatter and the serializer writes them back.
	Advertise              *bool
	Fast                   *bool
	ExcludeTools           []string
	SkillPath              []string
	Extensions             []string
	SubagentOnlyExtensions []string
	MutationTools          []string
	AllowNestedSubagents   *bool
	AllowedAgents          []string
	DefaultTimeoutMs       *int64
	DefaultToolTimeoutMs   *int64
	DefaultAcceptance      string // raw JSON (or "true"/"false"), as declared
	ToolBudget             string // raw JSON, as declared
	Machine                string
	Output                 string
	OutputMode             string
	OutputSchema           string // raw JSON, as declared
	Interactive            bool
	MaxSubagentDepth       *int
	// ExtraFrontmatter holds declared keys the typed loader does not model, in
	// file order, so an update can write them back unchanged.
	ExtraFrontmatter []ExtraField
	// Disabled is the settings override's `disabled` flag (builtin/package agents).
	Disabled bool
	// OverrideScope and OverridePath name the settings override that disabled the agent.
	OverrideScope string
	OverridePath  string
}

// ExtraField is one declared frontmatter key the typed loader keeps verbatim.
type ExtraField struct {
	Key   string
	Value string
}

// Runner is the `runner:` frontmatter block; only the type and the command are read.
type Runner struct{ Type, Command string }

// Diagnostic reports a definition file that could not be loaded; the other definitions stay usable.
type Diagnostic struct {
	Source   AgentSource
	Name     string
	FilePath string
	Error    string
}

// Discovery is the result of DiscoverAgents.
type Discovery struct {
	Agents           []AgentConfig
	Diagnostics      []Diagnostic
	ProjectAgentsDir string
	UserDir          string
	// ModelScope is the winning settings model scope (the project's over the user's).
	ModelScope *ModelScopeConfig
}

// DiscoveryAll is the result of DiscoverAgentsAll: every source separately, plus the chains.
type DiscoveryAll struct {
	Builtin, Package, User, Project []AgentConfig
	Diagnostics                     []Diagnostic
	Chains                          []ChainConfig
	ChainDiagnostics                []Diagnostic
	ProjectDir                      string
	UserDir                         string
	ProjectChainDir                 string
	UserChainDir                    string
	// ModelScope is the winning settings model scope (the project's over the user's).
	ModelScope *ModelScopeConfig
}
