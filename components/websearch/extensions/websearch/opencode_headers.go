package websearch

import "strings"

// Port of opencode-session-headers.ts. Pi adds OpenCode attribution headers inside the main agent
// loop; extension side calls (page queries, summaries) dispatch directly, so they add the same
// session attribution themselves.

// OpenCodeSessionHeaders returns the attribution headers for a side call made with model, or nil
// when the model is not an OpenCode one (provider "opencode" or "opencode-go", or a baseUrl whose
// hostname is opencode.ai) or the session has no id (opencode-session-headers.ts:10-23). model is
// the registry's model object; sessionID is the session manager's id.
func OpenCodeSessionHeaders(model map[string]any, sessionID string) map[string]string {
	provider, _ := model["provider"].(string)
	isOpenCode := provider == "opencode" || provider == "opencode-go"
	if !isOpenCode {
		// String(model.baseUrl ?? ""): a missing or non-string value never parses as a URL.
		if baseURL, ok := model["baseUrl"].(string); ok {
			if u, err := ParseURL(baseURL, nil); err == nil && JSHostname(u) == "opencode.ai" {
				isOpenCode = true
			}
		}
	}
	if !isOpenCode || sessionID == "" {
		return nil
	}
	return map[string]string{"x-opencode-session": sessionID, "x-opencode-client": "pi"}
}

// isOpenCodeHost is the destination check of openai-search.ts:251-262: https and the exact
// hostname opencode.ai.
func isOpenCodeHost(rawURL string) bool {
	u, err := ParseURL(rawURL, nil)
	return err == nil && u.Scheme == "https" && strings.EqualFold(JSHostname(u), "opencode.ai")
}
