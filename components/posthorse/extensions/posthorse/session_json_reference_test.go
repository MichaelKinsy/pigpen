package posthorse

// The SDK-decoder implementation of parseSessionLine that session_json.go replaced, kept as the
// reference the single-pass reader is tested against.

import "github.com/MichaelKinsy/PiG/extensions/sdk/json"

// parseSessionLine is `parseSessionLine` (session-files.ts:211-241): one JSONL line as a
// normalized entry record, false for a malformed line (Pi skips those too). Keys the original
// does not carry are dropped and ill-typed string fields are absent, so the record has the shape
// of the TS EntryLike; `display` is always a boolean and a null `parentId` or `replacement` stays
// null. The decoded object is normalized in place.
func parseSessionLineReference(line string) (rec, bool) {
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return nil, false
	}
	entry, ok := value.(rec)
	if !ok {
		return nil, false
	}
	for key, v := range entry {
		switch key {
		case "type", "id", "timestamp", "summary", "customType", "cwd", "parentSession", "handoff", "firstKeptEntryId", "targetId":
			if _, isString := v.(string); !isString {
				delete(entry, key)
			}
		case "parentId":
			if _, isString := v.(string); !isString && v != nil {
				delete(entry, key)
			}
		case "content", "details", "data":
		case "display":
			shown, _ := v.(bool)
			entry[key] = shown
		case "message":
			if message, ok := v.(rec); ok {
				normalizeMessageReference(message)
			} else {
				delete(entry, key)
			}
		case "replacement":
			switch r := v.(type) {
			case nil:
			case rec:
				replacement := rec{}
				if content, present := r["content"]; present {
					replacement["content"] = content
				}
				entry[key] = replacement
			default:
				delete(entry, key)
			}
		default:
			delete(entry, key)
		}
	}
	if _, present := entry["display"]; !present {
		entry["display"] = false
	}
	return entry, true
}

// normalizeMessage is `messageFrom` (session-files.ts:187-210) applied in place.
func normalizeMessageReference(message rec) {
	for key, v := range message {
		switch key {
		case "role", "stopReason", "errorMessage", "summary", "toolName", "namespace", "toolCallId", "command", "output":
			if _, isString := v.(string); !isString {
				delete(message, key)
			}
		case "content", "details", "nestedCalls":
		case "isError", "excludeFromContext":
			flag, _ := v.(bool)
			message[key] = flag
		default:
			delete(message, key)
		}
	}
	for _, key := range [2]string{"isError", "excludeFromContext"} {
		if _, present := message[key]; !present {
			message[key] = false
		}
	}
}
