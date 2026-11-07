package posthorse

import "regexp"

// Port of src/recovery-records.ts.

// recoveryRecord is RecoveryRecord (recovery-records.ts:4).
type recoveryRecord struct {
	id        string
	timestamp string
	kind      string // "owner" | "coordination"
	label     string
	text      string
}

type recordSource struct {
	kind    string
	label   string
	content any
}

// jsWhitespace is the set JS `\s` and String.prototype.trim share.
const jsWhitespaceClass = "[\\t\\n\\v\\f\\r \\x{a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}]"

var jsWhitespaceRun = regexp.MustCompile(jsWhitespaceClass + "+")

// sliceID is `value?.slice(0, limit) ?? fallback` for an optional string field.
func sliceID(m rec, key string, limit int, fallback string) string {
	if v, ok := str(m, key); ok {
		return u16slice(v, 0, limit)
	}
	return fallback
}

// ownerAnswer: recovery-records.ts:16.
func ownerAnswer(entry rec, ownerQuestion bool) bool {
	if !ownerQuestion {
		return false
	}
	if t, _ := str(entry, "type"); t != "message" {
		return false
	}
	message, ok := recOf(entry["message"])
	if !ok {
		return false
	}
	if role, _ := str(message, "role"); role != "toolResult" {
		return false
	}
	if name, _ := str(message, "toolName"); name != "ask_question" {
		return false
	}
	if _, has := message["namespace"]; has {
		return false
	}
	return message["isError"] != true
}

// recordSourceOf: recovery-records.ts:27.
func recordSourceOf(entry rec, ownerQuestion bool) (recordSource, bool) {
	typ, _ := str(entry, "type")
	message, hasMessage := recOf(entry["message"])
	if typ == "message" && hasMessage {
		if role, _ := str(message, "role"); role == "user" {
			return recordSource{"owner", "owner input", message["content"]}, true
		}
	}
	if ownerAnswer(entry, ownerQuestion) {
		return recordSource{"owner", "owner answer via ask_question", message["content"]}, true
	}
	if typ == "custom_message" && entry["display"] == true && !isReminderType(entry["customType"]) {
		custom := "custom"
		if v, ok := str(entry, "customType"); ok {
			custom = v
		}
		return recordSource{
			"coordination",
			"visible " + u16slice(custom, 0, 80) + " coordination input (not direct owner input)",
			entry["content"],
		}, true
	}
	return recordSource{}, false
}

// recoveryRecordOf is recoveryRecord (recovery-records.ts:51).
func recoveryRecordOf(entry rec, ownerQuestion bool) (recoveryRecord, bool) {
	source, ok := recordSourceOf(entry, ownerQuestion)
	if !ok {
		return recoveryRecord{}, false
	}
	id := sliceID(entry, "id", 120, "unknown")
	text := jsTrim(textOf(rec{"content": source.content}))
	summary := ""
	if source.label != "owner answer via ask_question" {
		summary = imageSummary(imagesOf(source.content))
	}
	body := text
	if summary != "" {
		extra := summary + " — recover with history read id " + id
		if body != "" {
			body += "\n"
		}
		body += extra
	}
	if body == "" {
		body = "(non-text content; recover the entry from history)"
	}
	return recoveryRecord{
		id:        id,
		timestamp: sliceID(entry, "timestamp", 80, "unknown time"),
		kind:      source.kind,
		label:     source.label,
		text:      body,
	}, true
}

// formatRecord: recovery-records.ts:76.
func formatRecord(record recoveryRecord, limit int) string {
	return boundedBlock("["+record.label+" | "+record.timestamp+" | entry "+record.id+"]", record.text, limit)
}

// indexLine: recovery-records.ts:83.
func indexLine(record recoveryRecord) string {
	header := formatRecord(record, 0)
	if record.kind != "owner" {
		return header
	}
	preview := jsTrim(jsWhitespaceRun.ReplaceAllString(record.text, " "))
	if u16len(preview) > 120 {
		return header + " " + u16slice(preview, 0, 119) + "…"
	}
	return header + " " + preview
}
