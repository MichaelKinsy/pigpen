package pi_permission_system

// Port of src/access-intent/access-intent.ts. The TypeScript unions are Go interfaces sealed by an unexported marker method;
// AgentName nil is the original's undefined.

// QueryIntent is what an external policy query can feed the resolver: a tool, access-path or path-values intent.
type QueryIntent interface{ queryIntent() }

// AccessIntent is what a gate emits: a raw tool input, an AccessPath, or a bash command unit.
type AccessIntent interface{ accessIntent() }

// ResolvedAccessIntent is what the manager consumes: the access-path variant already unwrapped to path-values.
type ResolvedAccessIntent interface{ resolvedAccessIntent() }

// ToolAccessIntent is a raw tool input the manager must normalize. Surface is the tool name fed to NormalizeInput.
type ToolAccessIntent struct {
	Surface   string
	Input     any
	AgentName *string
}

// PathValuesAccessIntent is precomputed equivalent policy values for a surface, evaluated as-is (a path-shaped surface or mcp).
type PathValuesAccessIntent struct {
	Surface   string
	Values    []string
	AgentName *string
}

// AccessPathAccessIntent is an AccessPath value object for a path-shaped surface.
type AccessPathAccessIntent struct {
	Surface   string
	Path      *AccessPath
	AgentName *string
}

// BashCommandAccessIntent is one bash command unit with the other spellings the shell runs identically. Surface is always "bash".
type BashCommandAccessIntent struct {
	Surface   string
	Command   string
	Spellings []string
	AgentName *string
}

func (ToolAccessIntent) queryIntent()       {}
func (PathValuesAccessIntent) queryIntent() {}
func (AccessPathAccessIntent) queryIntent() {}

func (ToolAccessIntent) accessIntent()        {}
func (AccessPathAccessIntent) accessIntent()  {}
func (BashCommandAccessIntent) accessIntent() {}

func (ToolAccessIntent) resolvedAccessIntent()        {}
func (PathValuesAccessIntent) resolvedAccessIntent()  {}
func (BashCommandAccessIntent) resolvedAccessIntent() {}
