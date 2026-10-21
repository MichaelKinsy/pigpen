package pi_goal_x

import "regexp"

// ported from goal-format.ts: isErrorAssistantMessage, isNetworkErrorAssistantMessage, hasNetworkErrorAssistantMessage.
// goal-format.ts has no wave-1 owner; these three are here because network-error-backoff.test.ts exercises them.

// transientProviderErrorRE matches transient provider failures that are safe for goal-level recovery backoff: Pi-declared
// network errors plus HTTP 429/5xx-style outages. Auth and malformed-request failures are deliberately excluded.
var transientProviderErrorRE = regexp.MustCompile(`(?i)` + joinAlt(
	`\bnetwork(?:_|`+jsS+`|-)?error\b`,
	`\bprotocol(?:_|`+jsS+`|-)?error\b`,
	`\bserver(?:_|`+jsS+`)?error\b`,
	`\bfinish(?:_|`+jsS+`|-)?reason:`+jsS+`*error\b`,
	`\b(?:429|502|503|504|529)\b`,
	`\brate(?:_|`+jsS+`|-)?limited\b`,
	`\brate(?:_|`+jsS+`|-)?limit\b`,
	`\btoo many requests\b`,
	`\bservice unavailable\b`,
	`\bbad gateway\b`,
	`\bgateway timeout\b`,
	`\bupstream request failed\b`,
	`\bendpoint is unavailable\b`,
	`\boverloaded(?:_|`+jsS+`)?error\b`,
))

// nonTransientProviderErrorRE: quota/billing exhaustion is deterministic; retrying cannot fix it.
var nonTransientProviderErrorRE = regexp.MustCompile(`(?i)` + joinAlt(
	`\binsufficient(?:_|`+jsS+`)?quota\b`,
	`\bout of budget\b`,
	`\bquota exceeded\b`,
	`\bbilling\b`,
	`GoUsageLimitError`,
	`FreeUsageLimitError`,
))

func joinAlt(parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "|"
		}
		out += p
	}
	return out
}

func isErrorAssistantMessage(message any) bool {
	raw := asRecord(message)
	if raw == nil {
		return false
	}
	role, _ := raw.str("role")
	stop, _ := raw.str("stopReason")
	return role == "assistant" && stop == "error"
}

// isNetworkErrorAssistantMessage: a transient provider failure is safe for the bounded goal backoff.
func isNetworkErrorAssistantMessage(message any) bool {
	if !isErrorAssistantMessage(message) {
		return false
	}
	raw := asRecord(message)
	details := ""
	first := true
	for _, k := range []string{"rawStopReason", "errorMessage"} {
		if s, ok := raw.str(k); ok {
			if !first {
				details += " "
			}
			details += s
			first = false
		}
	}
	details = jsFoldSafe(details)
	// Quota/billing exhaustion is deterministic and must never be retried.
	if nonTransientProviderErrorRE.MatchString(details) {
		return false
	}
	return transientProviderErrorRE.MatchString(details)
}

func hasNetworkErrorAssistantMessage(messages []any) bool {
	for _, m := range messages {
		if isNetworkErrorAssistantMessage(m) {
			return true
		}
	}
	return false
}
