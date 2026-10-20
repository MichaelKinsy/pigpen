package pi_subagents

// Fast mode (src/runs/shared/fast-mode-extension.ts): a child started with fast mode asks the provider for priority service.

// rewriteFastModeProviderRequest adds service_tier: priority to an object payload and leaves any other payload as it is.
func rewriteFastModeProviderRequest(payload any) any {
	switch p := payload.(type) {
	case map[string]any:
		out := make(map[string]any, len(p)+1)
		for k, v := range p {
			out[k] = v
		}
		out["service_tier"] = "priority"
		return out
	case *jsObject:
		out := p.clone()
		out.set("service_tier", "priority")
		return out
	}
	return payload
}
