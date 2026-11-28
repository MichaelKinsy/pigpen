package ohmyhermes

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
)

const ModelChainsContractVersion = "mixture_chain_overrides/v1"

// ChainEntry is one model + thinking level hop in a category fallback chain.
type ChainEntry struct {
	Model         string `json:"model"`
	ThinkingLevel string `json:"thinking_level,omitempty"`
}

// CategorySpec describes a built-in OMH routing category and its default chain.
type CategorySpec struct {
	Category string       `json:"category"`
	Purpose  string       `json:"purpose"`
	Chain    []ChainEntry `json:"chain"`
}

// ModelChainsFile is persisted at $OMH_HOME/routing/model-chains.json.
type ModelChainsFile struct {
	Schema    string                  `json:"schema"`
	UpdatedAt string                  `json:"updated_at,omitempty"`
	Overrides map[string][]ChainEntry `json:"overrides"`
}

var defaultCategories = []CategorySpec{
	{
		Category: "ultrabrain",
		Purpose:  "Deepest reasoning, hard architecture, and multi-constraint planning",
		Chain: []ChainEntry{
			{Model: "claude-opus-5-5", ThinkingLevel: "high"},
			{Model: "gpt-6.2-astra", ThinkingLevel: "high"},
		},
	},
	{
		Category: "deep",
		Purpose:  "Strong default engineering tier for implementation and refactoring",
		Chain: []ChainEntry{
			{Model: "claude-sonnet-5-5", ThinkingLevel: "medium"},
			{Model: "gpt-6.1-sol", ThinkingLevel: "medium"},
		},
	},
	{
		Category: "architect",
		Purpose:  "Architecture and system design",
		Chain: []ChainEntry{
			{Model: "claude-opus-5-5", ThinkingLevel: "high"},
			{Model: "claude-sonnet-5-5", ThinkingLevel: "high"},
		},
	},
	{
		Category: "quick",
		Purpose:  "Short tasks, reconnaissance, and mechanical updates",
		Chain: []ChainEntry{
			{Model: "claude-haiku-5-5", ThinkingLevel: "low"},
			{Model: "gemini-4-argon", ThinkingLevel: "low"},
		},
	},
	{
		Category: "artistry",
		Purpose:  "Unconventional, creative, or lateral problem solving",
		Chain: []ChainEntry{
			{Model: "claude-opus-5-5", ThinkingLevel: "high"},
			{Model: "claude-fable-5-2", ThinkingLevel: "medium"},
		},
	},
	{
		Category: "visual-engineering",
		Purpose:  "Frontend, UI, and visual design engineering",
		Chain: []ChainEntry{
			{Model: "claude-sonnet-5-5", ThinkingLevel: "medium"},
			{Model: "gemini-3.1-pro", ThinkingLevel: "medium"},
		},
	},
	{
		Category: "writing",
		Purpose:  "Technical prose, documentation, and handoff guides",
		Chain: []ChainEntry{
			{Model: "claude-sonnet-5-5", ThinkingLevel: "low"},
			{Model: "claude-fable-5-2", ThinkingLevel: "low"},
		},
	},
	{
		Category: "research",
		Purpose:  "Source-backed discovery, synthesis, and literature review",
		Chain: []ChainEntry{
			{Model: "gemini-3.1-pro", ThinkingLevel: "medium"},
			{Model: "claude-sonnet-5-5", ThinkingLevel: "medium"},
		},
	},
	{
		Category: "review",
		Purpose:  "Independent code review and adversarial diff inspection",
		Chain: []ChainEntry{
			{Model: "gpt-6.1-sol", ThinkingLevel: "high"},
			{Model: "claude-opus-5-5", ThinkingLevel: "medium"},
		},
	},
	{
		Category: "forensics",
		Purpose:  "Root-causing live symptoms, race conditions, and traces",
		Chain: []ChainEntry{
			{Model: "claude-opus-5-5", ThinkingLevel: "high"},
			{Model: "gpt-6.2-sol", ThinkingLevel: "high"},
		},
	},
	{
		Category: "swarm",
		Purpose:  "Parallel worker slices across independent files",
		Chain: []ChainEntry{
			{Model: "claude-sonnet-5-5", ThinkingLevel: "low"},
			{Model: "claude-haiku-5-5", ThinkingLevel: "low"},
		},
	},
	{
		Category: "synthesize",
		Purpose:  "Merging multiple subagent outputs into one coherent answer",
		Chain: []ChainEntry{
			{Model: "claude-sonnet-5-5", ThinkingLevel: "medium"},
			{Model: "claude-opus-5-5", ThinkingLevel: "medium"},
		},
	},
	{
		Category: "default",
		Purpose:  "Fallback category when no specific category is requested",
		Chain: []ChainEntry{
			{Model: "claude-sonnet-5-5", ThinkingLevel: "medium"},
			{Model: "claude-haiku-5-5", ThinkingLevel: "low"},
		},
	},
}

// RouteRouter resolves delegation routes using custom identities, persistent
// category chain overrides ($OMH_HOME/routing/model-chains.json), and built-in defaults.
type RouteRouter struct {
	mu           sync.RWMutex
	paths        func() OMHPaths
	identities   *IdentityStore
	activeRoute  *delegation.RouteDecision
	activeCat    string
	fallbackIdx  map[string]int
}

// NewRouteRouter creates a RouteRouter backed by OMHPaths and IdentityStore.
func NewRouteRouter(pathsFn func() OMHPaths, identities *IdentityStore) *RouteRouter {
	if pathsFn == nil {
		pathsFn = func() OMHPaths { return ResolvePaths("") }
	}
	if identities == nil {
		identities = NewIdentityStore(pathsFn)
	}
	return &RouteRouter{
		paths:       pathsFn,
		identities:  identities,
		fallbackIdx: make(map[string]int),
	}
}

func (r *RouteRouter) loadOverridesLocked() (ModelChainsFile, error) {
	doc := ModelChainsFile{
		Schema:    ModelChainsContractVersion,
		Overrides: make(map[string][]ChainEntry),
	}
	exists, err := readJSON(r.paths().ModelChainsFile, &doc)
	if err != nil {
		return doc, err
	}
	if !exists || doc.Overrides == nil {
		doc.Schema = ModelChainsContractVersion
		doc.Overrides = make(map[string][]ChainEntry)
	}
	return doc, nil
}

// ChainForCategory returns the effective chain (override if present, else built-in default).
func (r *RouteRouter) ChainForCategory(category string) ([]ChainEntry, bool, error) {
	cat := normalizeCategory(category)
	r.mu.RLock()
	doc, err := r.loadOverridesLocked()
	r.mu.RUnlock()
	if err != nil {
		return nil, false, err
	}
	if chain, ok := doc.Overrides[cat]; ok && len(chain) > 0 {
		out := make([]ChainEntry, len(chain))
		copy(out, chain)
		return out, true, nil
	}
	for _, spec := range defaultCategories {
		if spec.Category == cat {
			out := make([]ChainEntry, len(spec.Chain))
			copy(out, spec.Chain)
			return out, false, nil
		}
	}
	return nil, false, nil
}

// AllChains returns all known categories (built-ins + custom override keys) with effective chains.
func (r *RouteRouter) AllChains() ([]CategorySpec, map[string]bool, error) {
	r.mu.RLock()
	doc, err := r.loadOverridesLocked()
	r.mu.RUnlock()
	if err != nil {
		return nil, nil, err
	}
	overridden := make(map[string]bool)
	seen := make(map[string]bool)
	out := make([]CategorySpec, 0, len(defaultCategories)+len(doc.Overrides))
	for _, spec := range defaultCategories {
		seen[spec.Category] = true
		eff := spec
		if ov, ok := doc.Overrides[spec.Category]; ok && len(ov) > 0 {
			eff.Chain = append([]ChainEntry(nil), ov...)
			overridden[spec.Category] = true
		} else {
			eff.Chain = append([]ChainEntry(nil), spec.Chain...)
		}
		out = append(out, eff)
	}
	var extra []string
	for cat := range doc.Overrides {
		if !seen[cat] {
			extra = append(extra, cat)
		}
	}
	sort.Strings(extra)
	for _, cat := range extra {
		overridden[cat] = true
		out = append(out, CategorySpec{
			Category: cat,
			Purpose:  "Custom category override",
			Chain:    append([]ChainEntry(nil), doc.Overrides[cat]...),
		})
	}
	return out, overridden, nil
}

// SetOverride persists a model chain override for category in routing/model-chains.json.
func (r *RouteRouter) SetOverride(category string, chain []ChainEntry) error {
	cat := normalizeCategory(category)
	if cat == "" {
		return fmt.Errorf("category is required")
	}
	if len(chain) == 0 {
		return fmt.Errorf("chain must contain at least one model entry")
	}
	clean := make([]ChainEntry, 0, len(chain))
	for i, c := range chain {
		m := strings.TrimSpace(c.Model)
		if m == "" {
			return fmt.Errorf("chain[%d].model is required", i)
		}
		clean = append(clean, ChainEntry{
			Model:         m,
			ThinkingLevel: strings.TrimSpace(c.ThinkingLevel),
		})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	doc, err := r.loadOverridesLocked()
	if err != nil {
		return err
	}
	doc.Schema = ModelChainsContractVersion
	doc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	doc.Overrides[cat] = clean
	delete(r.fallbackIdx, cat)
	return atomicWriteJSON(r.paths().ModelChainsFile, doc)
}

// ClearOverride removes a persistent override for category (or clears active pin if category == "").
func (r *RouteRouter) ClearOverride(category string) error {
	cat := normalizeCategory(category)
	r.mu.Lock()
	defer r.mu.Unlock()
	if cat == "" {
		r.activeRoute = nil
		r.activeCat = ""
		return nil
	}
	doc, err := r.loadOverridesLocked()
	if err != nil {
		return err
	}
	delete(doc.Overrides, cat)
	delete(r.fallbackIdx, cat)
	doc.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return atomicWriteJSON(r.paths().ModelChainsFile, doc)
}

// SetActivePin pins a route for the next delegation call.
func (r *RouteRouter) SetActivePin(category, model, thinking string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeCat = normalizeCategory(category)
	if model != "" {
		r.activeRoute = &delegation.RouteDecision{
			Model:         strings.TrimSpace(model),
			ThinkingLevel: strings.TrimSpace(thinking),
			Reason:        "omh:active_pin",
		}
	} else {
		r.activeRoute = nil
	}
}

// AdvanceFallback steps to the next model in category's chain and returns it.
func (r *RouteRouter) AdvanceFallback(category string) (ChainEntry, int, error) {
	cat := normalizeCategory(category)
	if cat == "" {
		cat = "default"
	}
	chain, _, err := r.ChainForCategory(cat)
	if err != nil {
		return ChainEntry{}, 0, err
	}
	if len(chain) == 0 {
		return ChainEntry{}, 0, fmt.Errorf("unknown category %q", cat)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.fallbackIdx[cat] + 1) % len(chain)
	r.fallbackIdx[cat] = idx
	return chain[idx], idx, nil
}

// Resolve implements pig-hermes-core/delegation.RouteResolver.
// Resolution precedence:
//  1. Explicit req.Model (left untouched -> returns ok=false so caller keeps explicit model)
//  2. Custom identity profile (req.Identity) with model_chain or category
//  3. Built-in role default category (req.Identity matching a built-in role)
//  4. Explicit req.Category chain (override > built-in)
//  5. Active route pin (from omh_delegate_route action="set")
//  6. "default" category when req.Category is empty and an override exists for "default"
func (r *RouteRouter) Resolve(req delegation.SpawnRequest) (delegation.RouteDecision, bool, error) {
	if strings.TrimSpace(req.Model) != "" {
		return delegation.RouteDecision{}, false, nil
	}

	// 1. Custom identity profile
	if id := strings.TrimSpace(req.Identity); id != "" {
		if prof, ok, err := r.identities.Get(id); err == nil && ok && prof.IsEnabled() {
			if len(prof.ModelChain) > 0 {
				head := prof.ModelChain[0]
				return delegation.RouteDecision{
					Model:         head.ModelID,
					ThinkingLevel: head.ReasoningEffort,
					Reason:        "omh:identity:" + prof.Name,
				}, true, nil
			}
			if prof.Category != "" {
				req.Category = prof.Category
			} else if prof.Role != "" {
				if role, ok := LookupBuiltinRole(prof.Role); ok {
					req.Category = role.DefaultCategory
				}
			}
		} else if role, ok := LookupBuiltinRole(id); ok && strings.TrimSpace(req.Category) == "" {
			req.Category = role.DefaultCategory
		}
	}

	// 2. Explicit category
	if cat := normalizeCategory(req.Category); cat != "" {
		chain, overridden, err := r.ChainForCategory(cat)
		if err != nil {
			return delegation.RouteDecision{}, false, err
		}
		if len(chain) > 0 {
			r.mu.RLock()
			idx := r.fallbackIdx[cat]
			r.mu.RUnlock()
			if idx >= len(chain) {
				idx = 0
			}
			src := "default_chain"
			if overridden {
				src = "override_chain"
			}
			return delegation.RouteDecision{
				Model:         chain[idx].Model,
				ThinkingLevel: chain[idx].ThinkingLevel,
				Reason:        fmt.Sprintf("omh:category:%s:%s[%d]", cat, src, idx),
			}, true, nil
		}
	}

	// 3. Active route pin
	r.mu.RLock()
	pinned := r.activeRoute
	pinnedCat := r.activeCat
	r.mu.RUnlock()
	if pinned != nil {
		return *pinned, true, nil
	}
	if pinnedCat != "" {
		chain, _, err := r.ChainForCategory(pinnedCat)
		if err == nil && len(chain) > 0 {
			return delegation.RouteDecision{
				Model:         chain[0].Model,
				ThinkingLevel: chain[0].ThinkingLevel,
				Reason:        "omh:pinned_category:" + pinnedCat,
			}, true, nil
		}
	}

	return delegation.RouteDecision{}, false, nil
}

func normalizeCategory(cat string) string {
	return strings.ToLower(strings.TrimSpace(cat))
}

func registerDelegateRouteTool(ext *sdk.Extension, router *RouteRouter) {
	ext.RegisterTool(sdk.ToolDefinition{
		Name:          "omh_delegate_route",
		Label:         "OMH Delegate Route",
		Description:   "Inspect, resolve, override, or advance OMH category model-chain routes used by delegate_task.",
		PromptSnippet: "Route delegated subagents by OMH category or identity with omh_delegate_route (status, resolve, set, clear, fallback).",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"status", "resolve", "set", "clear", "fallback"},
					"description": "Action to perform (default: status).",
				},
				"category": map[string]any{
					"type":        "string",
					"description": "OMH work category (e.g. deep, quick, ultrabrain, artistry, visual-engineering, writing, research, review, default).",
				},
				"identity": map[string]any{
					"type":        "string",
					"description": "Built-in role or custom identity name for resolution.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Head model ID when action=set.",
				},
				"thinking_level": map[string]any{
					"type":        "string",
					"description": "Thinking/reasoning effort level (low, medium, high) when action=set.",
				},
				"chain": map[string]any{
					"type":        "array",
					"description": "Optional full ordered model chain [{model, thinking_level}] when action=set.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"model":          map[string]any{"type": "string"},
							"thinking_level": map[string]any{"type": "string"},
						},
						"required": []string{"model"},
					},
				},
			},
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			action := strings.ToLower(paramString(params, "action"))
			if action == "" {
				action = "status"
			}
			category := normalizeCategory(paramString(params, "category"))
			identity := paramString(params, "identity")

			switch action {
			case "status":
				chains, overridden, err := router.AllChains()
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":         true,
					"schema":     ModelChainsContractVersion,
					"categories": chains,
					"overridden": overridden,
					"store_path": router.paths().ModelChainsFile,
				}, false), nil

			case "resolve":
				dec, matched, err := router.Resolve(delegation.SpawnRequest{
					Category: category,
					Identity: identity,
				})
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":       true,
					"matched":  matched,
					"category": category,
					"identity": identity,
					"decision": dec,
				}, false), nil

			case "set":
				model := paramString(params, "model")
				thinking := paramString(params, "thinking_level")
				chain := parseChainParam(params["chain"])
				if len(chain) == 0 && model != "" {
					chain = []ChainEntry{{Model: model, ThinkingLevel: thinking}}
				}
				if category != "" && len(chain) > 0 {
					if err := router.SetOverride(category, chain); err != nil {
						return errorToolResult(err.Error()), nil
					}
					return jsonToolResult(map[string]any{
						"ok":       true,
						"action":   "set",
						"category": category,
						"chain":    chain,
					}, false), nil
				}
				if category != "" || model != "" {
					router.SetActivePin(category, model, thinking)
					return jsonToolResult(map[string]any{
						"ok":             true,
						"action":         "pin",
						"category":       category,
						"model":          model,
						"thinking_level": thinking,
					}, false), nil
				}
				return errorToolResult("set requires category and model/chain"), nil

			case "clear":
				if err := router.ClearOverride(category); err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":       true,
					"action":   "clear",
					"category": category,
				}, false), nil

			case "fallback":
				entry, idx, err := router.AdvanceFallback(category)
				if err != nil {
					return errorToolResult(err.Error()), nil
				}
				return jsonToolResult(map[string]any{
					"ok":       true,
					"action":   "fallback",
					"category": category,
					"index":    idx,
					"entry":    entry,
				}, false), nil

			default:
				return errorToolResult(fmt.Sprintf("unsupported action %q", action)), nil
			}
		},
	})
}

func parseChainParam(raw any) []ChainEntry {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]ChainEntry, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		model, _ := m["model"].(string)
		thinking, _ := m["thinking_level"].(string)
		if strings.TrimSpace(model) != "" {
			out = append(out, ChainEntry{
				Model:         strings.TrimSpace(model),
				ThinkingLevel: strings.TrimSpace(thinking),
			})
		}
	}
	return out
}
