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

// SourceRuntime marks an agent registered by another extension at runtime (the original's "runtime" source).
const SourceRuntime AgentSource = "runtime"

// AgentMemoryConfig is the `memory` frontmatter block (agents.ts AgentMemoryConfig).
type AgentMemoryConfig struct{ Scope, Path string }

// OverrideInfo records the settings override that shaped an agent (agents.ts BuiltinAgentOverrideInfo).
type OverrideInfo struct {
	Scope       string // "user" or "project"
	Path        string
	Base        OverrideBase
	Fields      []string
	FieldScopes map[string][]string
}

// ModelSource says the model came from `subagents.defaultModel` (agents.ts AgentModelSourceInfo).
type ModelSource struct {
	Type            string
	Scope           string
	Path            string
	Model           string
	DefaultProvider string
}

// KV is one extra frontmatter field; the order of the file is kept.
type KV struct{ Key, Value string }

// AgentConfig is a discovered agent definition (agents.ts AgentConfig). Unset is the zero value: "" for a string, nil for a
// slice, pointer or interface; an explicit empty list is a non-nil empty slice.
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
	hasDiscoveryPriority  bool

	PackageSourceName      string
	PackageSourceVersion   string
	PackageSourceRoot      string
	Advertise              *bool
	ExcludeTools           []string
	AllowNestedSubagents   *bool
	AllowedAgents          []string
	ModelProvider          string
	Fast                   *bool
	DefaultTimeoutMs       *int
	DefaultToolTimeoutMs   *int
	DefaultAcceptance      any
	SkillPath              []string
	Extensions             []string
	ExtensionsFromDefault  bool
	SubagentOnlyExtensions []string
	MutationTools          []string
	Output                 string
	OutputMode             string
	OutputSchema           any
	Interactive            bool
	MaxSubagentDepth       *int
	ToolBudget             any
	Permissions            any
	Memory                 *AgentMemoryConfig
	Machine                string
	Disabled               *bool
	ExtraFields            []KV
	Override               *OverrideInfo
	ModelSource            *ModelSource
	MaxThinking            string
	DefinitionDigest       string
	// FrontmatterFields are the keys of the definition's frontmatter (the original's agentFrontmatterFields WeakMap).
	FrontmatterFields map[string]bool
}

// Runner is the `runner:` frontmatter block (agents.ts parseAgentRunnerFrontmatter).
type Runner struct {
	Type, Command  string
	Adapter        string
	Args           []string
	PromptDelivery string
	Capabilities   *jsObject
	Provider       string
	Options        *jsObject
}

// Diagnostic reports a definition file that could not be loaded; the other definitions stay usable.
type Diagnostic struct {
	Source   AgentSource
	Name     string
	FilePath string
	Error    string

	RuntimeName          string
	PackageSpecified     bool
	DiscoveryPriority    int
	HasDiscoveryPriority bool
}

// Discovery is the effective set of agents for a scope (agents.ts AgentDiscoveryResult).
type Discovery struct {
	Agents           []AgentConfig
	Diagnostics      []Diagnostic
	ProjectAgentsDir string // "" for none
	UserDir          string
	Cwd              string
	Scope            AgentScope
	Directories      []DirectoryReport
	ModelScope       *ModelScopeConfig
	MaxThinking      string
	// Err is the malformed settings file or package manifest that made discovery fail (the original throws).
	Err error
}

// DiscoveryAll is every source separately, plus the chains (agents.ts AgentDiscoveryAllResult).
type DiscoveryAll struct {
	Builtin, Package, User, Project []AgentConfig
	Cwd                             string
	Diagnostics                     []Diagnostic
	Chains                          []ChainConfig
	ChainDiagnostics                []Diagnostic
	ProjectDir                      string
	UserDir                         string
	ProjectChainDir                 string
	UserChainDir                    string
	UserSettingsPath                string
	ProjectSettingsPath             string
	MaxThinking                     string
	Err                             error
}
