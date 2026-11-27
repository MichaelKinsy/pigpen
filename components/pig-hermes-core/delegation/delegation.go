// Package delegation provides shared delegation types and the pluggable RouteResolver
// hook so Phase 2 (oh-my-hermes-pig) can inject category/identity routing into delegate_task.
package delegation

import "sync"

// SpawnRequest describes a delegated child task.
type SpawnRequest struct {
	TaskID        string   `json:"task_id,omitempty"`
	Goal          string   `json:"goal"`
	Context       string   `json:"context,omitempty"`
	Category      string   `json:"category,omitempty"`
	Identity      string   `json:"identity,omitempty"`
	Model         string   `json:"model,omitempty"`
	ThinkingLevel string   `json:"thinking_level,omitempty"`
	Toolsets      []string `json:"toolsets,omitempty"`
	MaxIterations int      `json:"max_iterations,omitempty"`
	Depth         int      `json:"depth,omitempty"`
}

// SpawnResult captures the settled result of a delegated child task.
type SpawnResult struct {
	TaskID         string  `json:"task_id"`
	ChildSessionID string  `json:"child_session_id,omitempty"`
	Status         string  `json:"status"` // "completed", "error", "cancelled"
	Summary        string  `json:"summary"`
	DurationMs     int64   `json:"duration_ms"`
	InputTokens    int     `json:"input_tokens,omitempty"`
	OutputTokens   int     `json:"output_tokens,omitempty"`
	Cost           float64 `json:"cost,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// RouteDecision is returned by a RouteResolver to override model/thinking selection.
type RouteDecision struct {
	Model         string `json:"model,omitempty"`
	ThinkingLevel string `json:"thinking_level,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// RouteResolver resolves a model and thinking level for a delegation request.
type RouteResolver interface {
	Resolve(req SpawnRequest) (RouteDecision, bool, error)
}

// RouteResolverFunc adapts a function to RouteResolver.
type RouteResolverFunc func(req SpawnRequest) (RouteDecision, bool, error)

func (f RouteResolverFunc) Resolve(req SpawnRequest) (RouteDecision, bool, error) {
	return f(req)
}

var (
	resolverMu sync.RWMutex
	resolvers  []RouteResolver
)

// RegisterRouteResolver adds a resolver consulted by delegate_task (LIFO precedence).
func RegisterRouteResolver(r RouteResolver) {
	if r == nil {
		return
	}
	resolverMu.Lock()
	defer resolverMu.Unlock()
	resolvers = append(resolvers, r)
}

// ResolveRoute queries registered resolvers in reverse registration order.
func ResolveRoute(req SpawnRequest) (RouteDecision, bool, error) {
	resolverMu.RLock()
	list := make([]RouteResolver, len(resolvers))
	copy(list, resolvers)
	resolverMu.RUnlock()

	for i := len(list) - 1; i >= 0; i-- {
		dec, ok, err := list[i].Resolve(req)
		if err != nil {
			return RouteDecision{}, false, err
		}
		if ok {
			return dec, true, nil
		}
	}
	return RouteDecision{}, false, nil
}
