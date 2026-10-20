package pi_subagents


// The turn delta the reviewer reads (watchdog/turn-delta.ts). Messages and events are decoded JSON in the ordered form of
// jsjson.go (*jsObject, []any, string, float64, bool, nil).

type watchdogTurnDeltaInput struct {
	UserPrompt         string
	IncludeUserPrompt  bool
	Messages           []any
	Events             []any
	FinalAssistantStop bool
	// StructuredTerminal: a validated structured output is the authoritative terminal response.
	StructuredTerminal bool
}

// wdProp is `value.key` for an object value; present is false for a missing key and for undefined.
func wdProp(v any, key string) (any, bool) { panic("not implemented") }

func wdPropV(v any, key string) any { panic("not implemented") }

// wdNullish is the first of values that is neither null nor undefined (`a ?? b ?? c`); nil when all are.
func wdNullish(values ...any) any { panic("not implemented") }

func wdPropStr(v any, key string) string { panic("not implemented") }

func wdTextFromContent(content any) string { panic("not implemented") }

func wdFormatValue(value any, indent string) string { panic("not implemented") }

func wdRedactEditWriteInput(input any) any { panic("not implemented") }

func wdFormatToolCall(name, input any) string { panic("not implemented") }

func wdFormatToolResult(name string, content, details, errValue any, isError any) string {
	panic("not implemented")
}

func wdIsObject(v any) bool { panic("not implemented") }

func wdMsg(kv ...any) *jsObject { panic("not implemented") }

func wdMessagesFromEvent(event any) []any { panic("not implemented") }

// formatWatchdogReviewMessage formats one message for the reviewer; ok is false for a message the reviewer does not see.
func formatWatchdogReviewMessage(message any, structuredTerminal bool) (string, bool) {
	panic("not implemented")
}

func formatWatchdogTurnDelta(in watchdogTurnDeltaInput) string { panic("not implemented") }

// formatWatchdogOrchestrationActivity: only completed, exactly paired native orchestration calls are activity evidence.
func formatWatchdogOrchestrationActivity(event any) string { panic("not implemented") }
