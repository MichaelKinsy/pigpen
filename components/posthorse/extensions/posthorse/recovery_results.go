package posthorse

// Port of src/recovery-results.ts.

// recoveryEntry is RecoveryEntry (recovery-results.ts:12): the projected
// message with the identity the history tool can read back.
type recoveryEntry struct {
	id       string
	hasID    bool
	message  rec
	retained bool
}

func (e *recoveryEntry) idSlice(fallback string) string {
	if e.hasID {
		return u16slice(e.id, 0, 120)
	}
	return fallback
}

type linkedCall struct {
	entry int // index into the entries slice: identity of the owning entry
	block toolCallLike
}

// recoveryBlock is RecoveryBlock (recovery-results.ts:15).
type recoveryBlock struct {
	header string
	text   string
}

// recoveryBatch is RecoveryBatch (recovery-results.ts:16). hasCallID is false
// when callId is undefined.
type recoveryBatch struct {
	callID    string
	hasCallID bool
	blocks    []recoveryBlock
}

func messageRole(message rec) string {
	role, _ := str(message, "role")
	return role
}

// callsByID: recovery-results.ts:20.
func callsByID(entries []recoveryEntry) map[string]linkedCall {
	calls := make(map[string]linkedCall)
	for i := range entries {
		message := entries[i].message
		if messageRole(message) != "assistant" {
			continue
		}
		for _, block := range toolCalls(message["content"]) {
			if block.id != nil {
				calls[*block.id] = linkedCall{entry: i, block: block}
			}
		}
	}
	return calls
}

// trailingStart: recovery-results.ts:34.
func trailingStart(entries []recoveryEntry) int {
	hasTrailingResult := false
	for i := len(entries) - 1; i >= 0; i-- {
		message := entries[i].message
		switch messageRole(message) {
		case "toolResult":
			hasTrailingResult = true
		case "assistant":
			stop, _ := str(message, "stopReason")
			invalid := stop == "error" || stop == "aborted" || stop == "length"
			if !invalid || hasTrailingResult {
				return i
			}
		}
	}
	return -1
}

// resultOutput: recovery-results.ts:49.
func resultOutput(result *recoveryEntry) string {
	message := result.message
	resultID := result.idSlice("unknown")
	images := imageSummary(imagesOf(message["content"]))
	output := jsTrim(textOf(message))
	if images != "" {
		if output != "" {
			output += "\n"
		}
		output += images + " — recover with history read id " + resultID
	}
	if output == "" {
		return "(empty result)"
	}
	return output
}

// callDetails: recovery-results.ts:61.
func callDetails(entries []recoveryEntry, matching *linkedCall, allLinked bool) string {
	if matching == nil {
		return "\nNo matching projected call"
	}
	entry := ""
	if !allLinked {
		entry = "\nCall entry: " + entries[matching.entry].idSlice("unknown")
	}
	arguments := matching.block.arguments
	if arguments == nil {
		arguments = rec{}
	}
	return entry + "\nCall arguments: " + safeJsonStringify(arguments) + executionArgumentsText(matching.block)
}

// resultBlock: recovery-results.ts:70.
func resultBlock(entries []recoveryEntry, result *recoveryEntry, matching *linkedCall, allLinked bool) recoveryBlock {
	message := result.message
	var name, namespace *string
	if v, ok := str(message, "toolName"); ok {
		name = &v
	} else if matching != nil {
		name = matching.block.name
	}
	if v, ok := str(message, "namespace"); ok {
		namespace = &v
	} else if matching != nil {
		namespace = matching.block.namespace
	}
	resultID := result.idSlice("unknown")
	kind := "result"
	if message["isError"] == true {
		kind = "error"
	}
	callID := "unknown"
	if v, ok := str(message, "toolCallId"); ok {
		callID = v
	}
	return recoveryBlock{
		header: "[" + kind + " entry " + resultID + "]",
		text: resultOutput(result) + "\n\nTool: " + toolIdentity(name, namespace) +
			"\nCall ID: " + callID + callDetails(entries, matching, allLinked),
	}
}

// recoverableToolResults: recovery-results.ts:87. Async receipts may arrive
// after a response; completion does not establish receipt delivery.
func recoverableToolResults(entries []recoveryEntry) *recoveryBatch {
	calls := callsByID(entries)
	start := trailingStart(entries)
	var results []int
	for index := range entries {
		entry := &entries[index]
		callID, hasCallID := str(entry.message, "toolCallId")
		if messageRole(entry.message) != "toolResult" || !hasCallID || entry.retained {
			continue
		}
		call, linked := calls[callID]
		// Only projected provenance is eligible. Orphans never resurrect a raw call.
		if index > start || !linked || call.block.async {
			results = append(results, index)
		}
	}
	if len(results) == 0 {
		return nil
	}
	linkedOf := func(index int) *linkedCall {
		callID, _ := str(entries[index].message, "toolCallId")
		if call, ok := calls[callID]; ok {
			return &call
		}
		return nil
	}
	first := linkedOf(results[0])
	allLinked := first != nil
	if allLinked {
		for _, index := range results {
			if call := linkedOf(index); call == nil || call.entry != first.entry {
				allLinked = false
				break
			}
		}
	}
	batch := &recoveryBatch{blocks: make([]recoveryBlock, 0, len(results))}
	if allLinked {
		batch.callID = entries[first.entry].idSlice("unknown")
		batch.hasCallID = true
	}
	for _, index := range results {
		batch.blocks = append(batch.blocks, resultBlock(entries, &entries[index], linkedOf(index), allLinked))
	}
	return batch
}
