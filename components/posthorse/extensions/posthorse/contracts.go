package posthorse

// Port of src/contracts.ts and src/records.ts: the shared types and constants every module uses.

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// rec is a decoded JSON object exactly as the SDK delivers it (numbers are float64).
type rec = map[string]any

// messageLike and entryLike are the TS MessageLike / EntryLike (contracts.ts:36-69): session
// messages and entries stay dynamic records; read them with the helpers of dyn.go.
type (
	messageLike = rec
	entryLike   = rec
)

// compactionPolicy is the TS CompactionPolicy (contracts.ts:5-9); keepRecentTokens nil is undefined.
type compactionPolicy struct {
	enabled          bool
	reserveTokens    int
	keepRecentTokens *int
}

// contextUsage is the TS ContextUsage (contracts.ts:10-14); nil tokens and percent are JS null.
type contextUsage struct {
	tokens        *int
	contextWindow int
	percent       *float64
}

// policyContext is the TS PolicyContext (contracts.ts:15-19). Each getter is a host round trip in Go.
type policyContext interface {
	getCompactionSettings() (compactionPolicy, error)
	getContextUsage() (*contextUsage, error) // nil is undefined
	getSystemPrompt() (string, error)
}

// policyReader is the TS PolicyReader (contracts.ts:20).
type policyReader func(ctx sdk.Context) (compactionPolicy, error)

// snapshotReader is the TS SnapshotReader (contracts.ts:21-24); livePolicy nil is undefined.
type snapshotReader func(ctx sdk.Context, livePolicy *compactionPolicy) policyContext

// toolTokensFunc is the TS `toolTokens: () => number` closure (index.ts:48): the host reads
// the active tools, so it needs the handler's context.
type toolTokensFunc = func(ctx sdk.Context) (int, error)

// imageLike is the TS ImageLike (contracts.ts:25-29) without its constant type "image".
type imageLike struct {
	data     string
	mimeType string
}

// toolCallLike is the TS ToolCallLike (contracts.ts:30-38). A nil string pointer is undefined;
// hasArguments and hasExecutionArguments tell a present key (possibly JSON null) from undefined.
type toolCallLike struct {
	id                    *string
	name                  *string
	namespace             *string
	arguments             any
	hasArguments          bool
	executionArguments    any
	hasExecutionArguments bool
	async                 bool
}

// windowedEntry is the TS WindowedEntry (contracts.ts:70-75).
type windowedEntry struct {
	entry    rec
	windowId string
	text     string
	images   []imageLike
}

// projectedEntry is the TS ProjectedEntry (contracts.ts:76-79).
type projectedEntry struct {
	sourceEntry rec
	messages    []rec
}

// historyHit is the TS HistoryHit (contracts.ts:80-85); priority is 0 or 1.
type historyHit struct {
	id           string
	text         string
	headerLength int
	priority     int
}

// reminderFingerprint is the TS ReminderFingerprint (contracts.ts:86-90); nil is undefined.
type reminderFingerprint struct {
	windowId      string
	contextWindow *int
	reserveTokens *int
}

// toolResult is the TS TextResult (contracts.ts:91-96): text first, then images, then details.
type toolResult = sdk.ToolResult

const (
	maxHandoffChars     = 20_000 // contracts.ts:98
	maxPageChars        = 40_000 // contracts.ts:99
	minPageChars        = 1_000  // contracts.ts:100
	pageMarginTokens    = 1_000  // contracts.ts:101
	estimatedImageChars = 4_800  // contracts.ts:102
	reminderType        = "posthorse-reminder"
	autoHandoffPrefix   = "Automatic context rollover recovery record."
	emptyHandoff        = "Fresh context. Restore relevant notes and history before continuing."
	maxSafeInteger      = 9007199254740991 // Number.MAX_SAFE_INTEGER
)

// isRecord is `isRecord` (records.ts:1-3): a non-null, non-array object.
func isRecord(v any) bool {
	_, ok := v.(rec)
	return ok
}

// isReminderType is `isReminderType` (contracts.ts:119-121).
func isReminderType(customType any) bool {
	s, ok := customType.(string)
	return ok && s == reminderType
}

// requireValue is `requireValue` (contracts.ts:111-118): present is false for undefined.
func requireValue(value string, present bool, name, op string) (string, error) {
	if !present || value == "" {
		return "", fmt.Errorf("\"%s\" is required for op \"%s\".", name, op)
	}
	return value, nil
}

// textResult is `textResult` (contracts.ts:122-130). A nil display leaves details undefined.
func textResult(text string, images []imageLike, display *posthorseDisplay) toolResult {
	result := sdk.ToolResult{Content: text}
	if len(images) > 0 {
		result.Images = make([]sdk.ImageContent, len(images))
		for i, image := range images {
			result.Images[i] = sdk.ImageContent{Data: image.data, MimeType: image.mimeType}
		}
	}
	if display != nil {
		result.Details = display
	}
	return result
}
