package ohmyhermes

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	RoleContractVersion     = "omh_role_surface/v1"
	IdentityContractVersion = "omh_identity_profiles/v1"
	MaxIdentityPromptBytes  = 4000
	MaxIdentityChainEntries = 8
)

var validIdentityNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// RoleDefinition describes one built-in OMH workflow responsibility role.
type RoleDefinition struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	Purpose          string   `json:"purpose"`
	DefaultCategory  string   `json:"default_category"`
	Owns             []string `json:"owns"`
	PrimarySkills    []string `json:"primary_skills"`
	EvidenceBoundary string   `json:"evidence_boundary"`
	Aliases          []string `json:"aliases,omitempty"`
}

// IdentityModelEntry is one model preference in a custom identity profile's chain.
type IdentityModelEntry struct {
	ModelID         string `json:"model_id"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// IdentityProfile represents a user-defined OMH identity profile stored in
// $OMH_HOME/routing/identities.json.
type IdentityProfile struct {
	Name        string               `json:"name"`
	Role        string               `json:"role,omitempty"`
	Category    string               `json:"category,omitempty"`
	Prompt      string               `json:"prompt,omitempty"`
	ModelChain  []IdentityModelEntry `json:"model_chain,omitempty"`
	UpdatedAt   string               `json:"updated_at"`
}

// IdentityStoreFile is the on-disk JSON envelope for routing/identities.json.
type IdentityStoreFile struct {
	Schema   string                     `json:"schema"`
	Profiles map[string]IdentityProfile `json:"profiles"`
}

var builtinRoles = []RoleDefinition{
	{
		ID:              "planner",
		Title:           "Planner",
		Purpose:         "Own clarification, non-goals, acceptance criteria, tradeoffs, loopability, and verification strategy.",
		DefaultCategory: "ultrabrain",
		Owns: []string{
			"Clarification and scope boundaries",
			"Plan artifacts with goals, non-goals, risks, and verification",
			"Decision gates before execution or handoff",
		},
		PrimarySkills:    []string{"ulw-plan", "ulw-interview", "ulw-context"},
		EvidenceBoundary: "A planner role makes work reviewable; it is not proof that work was accepted or executed.",
		Aliases:          []string{"planning-lead"},
	},
	{
		ID:              "architect",
		Title:           "Architect",
		Purpose:         "Own system structure, module boundaries, invariants, and cross-cutting design tradeoffs.",
		DefaultCategory: "architect",
		Owns: []string{
			"Component interfaces and data contracts",
			"Dependency direction and failure-mode analysis",
			"Migration and compatibility strategy",
		},
		PrimarySkills:    []string{"ulw-plan", "dag-ulw"},
		EvidenceBoundary: "An architect role defines contracts; it is not runtime or test evidence.",
		Aliases:          []string{"guide", "system-architect"},
	},
	{
		ID:              "implementer",
		Title:           "Implementer",
		Purpose:         "Own bounded code changes, refactoring, and targeted tests within assigned write scopes.",
		DefaultCategory: "deep",
		Owns: []string{
			"Scoped code edits and feature implementation",
			"Unit and integration tests for modified behavior",
			"Clean handoff with changed files and commands run",
		},
		PrimarySkills:    []string{"ulw-work", "ulw-maestro", "dag-ulw"},
		EvidenceBoundary: "An implementer role produces code changes; completion still requires observed verification.",
		Aliases:          []string{"builder", "coder"},
	},
	{
		ID:              "reviewer",
		Title:           "Reviewer",
		Purpose:         "Own independent diff inspection, regression hunting, security checks, and contract verification.",
		DefaultCategory: "review",
		Owns: []string{
			"Correctness and edge-case audit of diffs",
			"Evidence-boundary verification (prepared vs observed)",
			"Actionable defect reports with file and line references",
		},
		PrimarySkills:    []string{"omh-code-review", "ulw-qa"},
		EvidenceBoundary: "A reviewer role audits changes; it does not substitute for passing test runs.",
		Aliases:          []string{"auditor"},
	},
	{
		ID:              "researcher",
		Title:           "Researcher",
		Purpose:         "Own source-backed discovery and keep observed evidence, inference, freshness, and unknowns separate.",
		DefaultCategory: "research",
		Owns: []string{
			"Research question framing and source boundaries",
			"Observed evidence versus inferred conclusions",
			"Structured briefs feeding planning or implementation",
		},
		PrimarySkills:    []string{"ulw-research", "omh-web-research"},
		EvidenceBoundary: "A researcher role summarizes sources; it is not implementation, review, or CI evidence.",
		Aliases:          []string{"research-lead"},
	},
	{
		ID:              "debugger",
		Title:           "Debugger",
		Purpose:         "Own root-cause isolation, minimal reproductions, trace analysis, and targeted defect fixes.",
		DefaultCategory: "forensics",
		Owns: []string{
			"Minimal reproducer and hypothesis elimination",
			"Trace, log, and state-diff forensics",
			"Verified regression test proving the root cause is fixed",
		},
		PrimarySkills:    []string{"ulw-qa", "ulw-perf"},
		EvidenceBoundary: "A debugger role isolates defects; a fix is complete only when the repro passes.",
		Aliases:          []string{"forensics", "operator"},
	},
	{
		ID:              "verifier",
		Title:           "Verifier",
		Purpose:         "Own end-to-end verification gates, command execution receipts, and release-readiness checks.",
		DefaultCategory: "review",
		Owns: []string{
			"Observed build, lint, race, and test execution",
			"Completion dossier validation against acceptance criteria",
			"Blocking unproven completion claims",
		},
		PrimarySkills:    []string{"ulw-qa", "omh-verification-gate"},
		EvidenceBoundary: "A verifier role records observed command and exit-code proof; unprepared claims are rejected.",
		Aliases:          []string{"tracker", "qa"},
	},
	{
		ID:              "writer",
		Title:           "Writer",
		Purpose:         "Own concise technical documentation, handoff guides, changelogs, and user-facing prose.",
		DefaultCategory: "writing",
		Owns: []string{
			"Clear technical guides and API documentation",
			"Handoff summaries and release notes",
			"Terminology consistency across docs and code",
		},
		PrimarySkills:    []string{"ulw-context", "omh-wiki"},
		EvidenceBoundary: "A writer role produces documentation; factual claims must cite observed code or command output.",
		Aliases:          []string{"handoff-guide", "docs"},
	},
	{
		ID:              "explorer",
		Title:           "Explorer",
		Purpose:         "Own fast, read-only codebase reconnaissance, symbol mapping, and dependency discovery.",
		DefaultCategory: "quick",
		Owns: []string{
			"Rapid file, symbol, and architecture mapping",
			"Compressed context handoffs for downstream lanes",
			"Read-only discovery without mutating the workspace",
		},
		PrimarySkills:    []string{"ulw-context", "omh-workspace-audit"},
		EvidenceBoundary: "An explorer role maps existing state; it never modifies files or certifies tests.",
		Aliases:          []string{"memory-keeper", "scout"},
	},
}

// BuiltinRoles returns a copy of the 9 built-in OMH roles.
func BuiltinRoles() []RoleDefinition {
	out := make([]RoleDefinition, len(builtinRoles))
	copy(out, builtinRoles)
	return out
}

// LookupBuiltinRole finds a built-in role by canonical ID or alias (case-insensitive).
func LookupBuiltinRole(name string) (RoleDefinition, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	needle = strings.TrimPrefix(needle, "builtin:")
	if needle == "" {
		return RoleDefinition{}, false
	}
	for _, r := range builtinRoles {
		if r.ID == needle {
			return r, true
		}
		for _, alias := range r.Aliases {
			if alias == needle {
				return r, true
			}
		}
	}
	return RoleDefinition{}, false
}

// IdentityStore manages persistent custom identity profiles under $OMH_HOME/routing/identities.json.
type IdentityStore struct {
	mu    sync.RWMutex
	paths func() OMHPaths
}

// NewIdentityStore creates an IdentityStore backed by the resolved OMHPaths.
func NewIdentityStore(pathsFn func() OMHPaths) *IdentityStore {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	return &IdentityStore{paths: pathsFn}
}

func (s *IdentityStore) loadLocked() (IdentityStoreFile, error) {
	p := s.paths().IdentitiesFile
	doc := IdentityStoreFile{
		Schema:   IdentityContractVersion,
		Profiles: make(map[string]IdentityProfile),
	}
	exists, err := readJSON(p, &doc)
	if err != nil {
		return doc, err
	}
	if !exists || doc.Profiles == nil {
		doc.Schema = IdentityContractVersion
		doc.Profiles = make(map[string]IdentityProfile)
	}
	return doc, nil
}

// List returns all saved custom identity profiles sorted by name.
func (s *IdentityStore) List() ([]IdentityProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doc.Profiles))
	for k := range doc.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]IdentityProfile, 0, len(names))
	for _, k := range names {
		out = append(out, doc.Profiles[k])
	}
	return out, nil
}

// Get retrieves a custom identity profile by name (accepts optional "custom:" prefix).
func (s *IdentityStore) Get(name string) (IdentityProfile, bool, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.TrimPrefix(key, "custom:")
	if key == "" {
		return IdentityProfile{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, err := s.loadLocked()
	if err != nil {
		return IdentityProfile{}, false, err
	}
	prof, ok := doc.Profiles[key]
	return prof, ok, nil
}

// Set validates and persists a custom identity profile atomically.
func (s *IdentityStore) Set(prof IdentityProfile) (IdentityProfile, error) {
	prof.Name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(prof.Name, "custom:")))
	if !validIdentityNameRe.MatchString(prof.Name) {
		return IdentityProfile{}, fmt.Errorf("invalid identity name %q: must match %s", prof.Name, validIdentityNameRe.String())
	}
	if len(prof.Prompt) > MaxIdentityPromptBytes {
		return IdentityProfile{}, fmt.Errorf("identity prompt exceeds %d bytes (got %d)", MaxIdentityPromptBytes, len(prof.Prompt))
	}
	if len(prof.ModelChain) > MaxIdentityChainEntries {
		return IdentityProfile{}, fmt.Errorf("identity model_chain exceeds %d entries (got %d)", MaxIdentityChainEntries, len(prof.ModelChain))
	}
	for i, entry := range prof.ModelChain {
		entry.ModelID = strings.TrimSpace(entry.ModelID)
		entry.ReasoningEffort = strings.TrimSpace(entry.ReasoningEffort)
		if entry.ModelID == "" || len(entry.ModelID) > 120 {
			return IdentityProfile{}, fmt.Errorf("model_chain[%d].model_id must be 1..120 chars", i)
		}
		if len(entry.ReasoningEffort) > 32 {
			return IdentityProfile{}, fmt.Errorf("model_chain[%d].reasoning_effort exceeds 32 chars", i)
		}
		prof.ModelChain[i] = entry
	}
	prof.Role = strings.ToLower(strings.TrimSpace(prof.Role))
	prof.Category = strings.ToLower(strings.TrimSpace(prof.Category))
	prof.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return IdentityProfile{}, err
	}
	doc.Schema = IdentityContractVersion
	doc.Profiles[prof.Name] = prof
	if err := atomicWriteJSON(s.paths().IdentitiesFile, doc); err != nil {
		return IdentityProfile{}, err
	}
	return prof, nil
}
