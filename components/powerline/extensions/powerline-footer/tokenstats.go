package powerline_footer

// Token and cost totals of a session branch. upstream: token-stats.ts.

type tokenStats struct {
	Input, Output, CacheRead, CacheWrite, Cost, SubagentCost float64
	LastAssistant                                            map[string]any
	ThinkingLevelFromSession                                 *string
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func fnum(m map[string]any, k string) (float64, bool) {
	f, ok := m[k].(float64)
	return f, ok
}

// sessionAssistantUsage reports whether message is an assistant message carrying usage and cost totals.
func sessionAssistantUsage(message map[string]any) (usage map[string]any, ok bool) {
	if message == nil || message["role"] != "assistant" {
		return nil, false
	}
	if sr, has := message["stopReason"]; has && sr != nil {
		if _, isStr := sr.(string); !isStr {
			return nil, false
		}
	}
	u := asMap(message["usage"])
	for _, k := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		if _, ok := fnum(u, k); !ok {
			return nil, false
		}
	}
	cost := asMap(u["cost"])
	if _, ok := fnum(cost, "total"); !ok {
		return nil, false
	}
	return u, true
}

func usageTokenTotal(u map[string]any) float64 {
	if t, ok := fnum(u, "totalTokens"); ok && t != 0 {
		return t
	}
	in, _ := fnum(u, "input")
	out, _ := fnum(u, "output")
	cr, _ := fnum(u, "cacheRead")
	cw, _ := fnum(u, "cacheWrite")
	return in + out + cr + cw
}

// subagentResults returns the child results of a subagent tool result or slash-result entry.
func subagentResults(e map[string]any) ([]any, bool) {
	if e["type"] == "custom_message" && e["customType"] == "subagent-slash-result" {
		inner := asMap(asMap(asMap(e["details"])["result"])["details"])
		r, ok := inner["results"].([]any)
		return r, ok
	}
	if e["type"] == "message" {
		if msg := asMap(e["message"]); msg != nil && msg["role"] == "toolResult" && msg["toolName"] == "subagent" {
			r, ok := asMap(msg["details"])["results"].([]any)
			return r, ok
		}
	}
	return nil, false
}

func extractSubagentResultCost(e map[string]any) float64 {
	results, ok := subagentResults(e)
	if !ok {
		return 0
	}
	total := 0.0
	for _, r := range results {
		if c, ok := fnum(asMap(asMap(r)["usage"]), "cost"); ok {
			total += c
		}
	}
	return total
}

func computeSessionTokenStats(events []map[string]any) tokenStats {
	var st tokenStats
	for _, e := range events {
		if e == nil {
			continue
		}
		if e["type"] == "thinking_level_change" {
			if lv, ok := e["thinkingLevel"].(string); ok {
				st.ThinkingLevelFromSession = &lv
			}
		}
		st.SubagentCost += extractSubagentResultCost(e)
		if e["type"] != "message" {
			continue
		}
		message := asMap(e["message"])
		u, ok := sessionAssistantUsage(message)
		if !ok {
			continue
		}
		if sr := message["stopReason"]; sr == "error" || sr == "aborted" {
			continue
		}
		in, _ := fnum(u, "input")
		out, _ := fnum(u, "output")
		cr, _ := fnum(u, "cacheRead")
		cw, _ := fnum(u, "cacheWrite")
		total, _ := fnum(asMap(u["cost"]), "total")
		st.Input += in
		st.Output += out
		st.CacheRead += cr
		st.CacheWrite += cw
		st.Cost += total
		if usageTokenTotal(u) > 0 {
			st.LastAssistant = message
		}
	}
	return st
}

// branchProvider is the part of the host's session manager the branch cache reads.
type branchProvider interface {
	GetLeafID() (*string, error)
	GetBranch(fromID *string) ([]map[string]any, error)
}

// sessionBranchCache keeps the branch of one leaf, so repeated renders do not ask the host again.
type sessionBranchCache struct {
	provider branchProvider
	leaf     *string
	branch   []map[string]any
	loaded   bool
}

func (c *sessionBranchCache) get(p branchProvider) []map[string]any {
	if p == nil {
		return []map[string]any{}
	}
	leaf, err := p.GetLeafID()
	if err != nil {
		return []map[string]any{}
	}
	if !c.loaded || c.provider != p || !sameString(c.leaf, leaf) {
		branch, err := p.GetBranch(nil)
		if err != nil {
			return []map[string]any{}
		}
		c.provider, c.leaf, c.branch, c.loaded = p, copyString(leaf), branch, true
	}
	return c.branch
}

func (c *sessionBranchCache) reset() { *c = sessionBranchCache{} }

func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// copyString detaches a pointer from the string it points at, so a later change of that string is seen as a change.
func copyString(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
