package pi_permission_system

// Port of src/presentation/forwarded-ask-payload.ts.

// BuildForwardedAskPayload builds the payload for an ask forwarded up from a subagent. It is a projection, not a synthesizer: the
// child ships its own complete payload, so the serving node renders the child's facts under the parent's budget. A request
// carrying no payload renders from whatever it does hold: fail-closed applies to presentation as it does to policy, so a
// version-skewed ask still reaches the human rather than resolving without one.
func BuildForwardedAskPayload(request ForwardedPermissionRequest) PromptPayload {
	// The child built its payload with a local requester. The serving node is the only party that knows the ask arrived over the
	// wire, and the request's own provenance is authoritative; everything else is the child's fact and passes through untouched.
	agentName, sessionID := request.RequesterAgentName, request.RequesterSessionID
	requester := PromptRequester{AgentName: &agentName, Forwarded: true, SessionID: &sessionID}

	if request.Payload != nil {
		payload := *request.Payload
		payload.Request.Requester = requester
		return payload
	}
	return degradedForwardedPayload(request, requester)
}

// degradedForwardedPayload is the render for an ask that arrived without a payload. Kind "forwarded" narrows to meaning exactly
// this: not "an ask from a subagent", which every branch above is too.
func degradedForwardedPayload(request ForwardedPermissionRequest, requester PromptRequester) PromptPayload {
	display := func(d DisplayString) string {
		if d.Set && d.Value != nil {
			return *d.Value
		}
		return ""
	}
	return PromptPayload{
		Kind: "forwarded",
		Request: PromptRequestFacts{
			Requester: requester,
			// The child's display projection: what the ask was about, as the child's own gate named it.
			Surface: display(request.Surface),
			Value:   display(request.Value),
		},
		// Nothing to carry: the wire no longer relays a sentence, and inventing evidence the child never sent is exactly the
		// fiction the bounded renderers would then have to trust.
		Evidence:    []PromptEvidence{},
		Annotations: []PromptAnnotation{},
	}
}
