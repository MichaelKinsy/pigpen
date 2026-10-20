package pi_permission_system

// Port of src/presentation/review-log-renderer.ts: the payload facts the permission review log persists (ADR 0011 section 6). A
// fact the ask does not carry is omitted rather than written as null; evidence and annotations are deliberately absent.

// RenderReviewLogFacts renders the request facts the log's own structured columns do not already carry.
func RenderReviewLogFacts(payload PromptPayload) *jsObject {
	request := payload.Request
	out := newObject()
	out.set("surface", request.Surface)
	present(out, "matchedPattern", request.MatchedPattern)
	present(out, "executedUnit", request.ExecutedUnit)
	present(out, "commandContext", request.CommandContext)
	present(out, "invokedToolName", request.InvokedToolName)
	// A local ask states nothing; a forwarded one names the session that raised it.
	if request.Requester.Forwarded {
		out.set("forwarded", true)
		present(out, "requesterSessionId", request.Requester.SessionID)
	}
	return out
}

func present(o *jsObject, key string, value *string) {
	if value != nil {
		o.set(key, *value)
	}
}
