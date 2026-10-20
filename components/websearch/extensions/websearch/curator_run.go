package websearch

import (
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of curator-run.ts: which workflow a web_search call runs, and the per-prompt approval of the
// remaining searches.

// ResolveWebSearchWorkflow is resolveWebSearchWorkflow: auto-summary works without a UI, the other
// workflows need one; anything unknown is "none".
func ResolveWebSearchWorkflow(input any, hasUI bool) string {
	s, _ := input.(string)
	normalized := strings.ToLower(jsTrim(s))
	switch {
	case normalized == "auto-summary":
		return "auto-summary"
	case !hasUI:
		return "none"
	case normalized == "none":
		return "none"
	case normalized == "summary-review":
		return "summary-review"
	}
	return "none"
}

// CuratorRunState holds the approval "approve + auto-summary the remaining searches for this
// prompt": until the run ends, a web_search call that inherits summary-review from the
// configuration runs auto-summary instead.
type CuratorRunState struct {
	mu                   sync.Mutex
	autoApproveRemaining bool
}

// ApproveRemainingSearches is approveRemainingSearches.
func (s *CuratorRunState) ApproveRemainingSearches() {
	s.mu.Lock()
	s.autoApproveRemaining = true
	s.mu.Unlock()
}

// Reset is reset.
func (s *CuratorRunState) Reset() {
	s.mu.Lock()
	s.autoApproveRemaining = false
	s.mu.Unlock()
}

// Resolve is resolve(requestedWorkflow, configuredWorkflow, hasUI). hasRequested is false when the
// call did not name a workflow (the original's `undefined`): the configured one is inherited, and
// only an inherited summary-review is changed by the approval.
func (s *CuratorRunState) Resolve(requested any, hasRequested bool, configured any, hasUI bool) string {
	inherited := !hasRequested
	input := requested
	if inherited {
		input = configured
	}
	workflow := ResolveWebSearchWorkflow(input, hasUI)
	s.mu.Lock()
	approved := s.autoApproveRemaining
	s.mu.Unlock()
	if inherited && approved && workflow == "summary-review" {
		return "auto-summary"
	}
	return workflow
}

// curatorRunResetEvents are the events that end an approval (registerCuratorRunLifecycle).
var curatorRunResetEvents = []string{
	sdk.EventBeforeAgentStart, sdk.EventAgentSettled, sdk.EventSessionStart, sdk.EventSessionTree, sdk.EventSessionShutdown,
}
