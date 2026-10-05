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
}
