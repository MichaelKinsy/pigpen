package posthorse

// Port of src/message-text.ts: text extraction from session messages, tool-call rendering,
// excerpts. Lengths are UTF-16 code units (js.go).

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
)

// isImage is `isImage` (message-text.ts:12-19).
func isImage(part any) bool {
	m, ok := recOf(part)
	if !ok {
		return false
	}
	if t, ok := str(m, "type"); !ok || t != "image" {
		return false
	}
	_, hasData := str(m, "data")
	_, hasMime := str(m, "mimeType")
	return hasData && hasMime
}

// imagesOf is `imagesOf` (message-text.ts:20-22).
func imagesOf(content any) []imageLike {
	parts, ok := arrOf(content)
	if !ok {
		return nil
	}
	var images []imageLike
	for _, part := range parts {
		if isImage(part) {
			m := part.(rec)
			data, _ := str(m, "data")
			mime, _ := str(m, "mimeType")
			images = append(images, imageLike{data: data, mimeType: mime})
		}
	}
	return images
}

// imageSummary is `imageSummary` (message-text.ts:23-33): distinct MIME types in first-seen order.
func imageSummary(images []imageLike) string {
	if len(images) == 0 {
		return ""
	}
	var types []string
	for _, image := range images {
		mime := image.mimeType
		if mime == "" {
			mime = "unknown type"
		}
		seen := false
		for _, t := range types {
			if t == mime {
				seen = true
				break
			}
		}
		if !seen {
			types = append(types, mime)
		}
	}
	plural := "s"
	if len(images) == 1 {
		plural = ""
	}
	return "[" + strconv.Itoa(len(images)) + " image" + plural + ": " + strings.Join(types, ", ") + "]"
}

// safeJsonStringify is `safeJsonStringify` (message-text.ts:34-41). Go nil is JSON null; pass
// jsUndef for JavaScript undefined.
func safeJsonStringify(value any) string {
	if _, undefined := value.(jsUndefined); undefined {
		return "undefined"
	}
	encoded, err := jsStringify(value)
	if err != nil {
		return "[unserializable]"
	}
	return encoded
}

// toolIdentity is `toolIdentity` (message-text.ts:42-44); nil is undefined.
func toolIdentity(name, namespace *string) string {
	identity := "tool"
	if name != nil {
		identity = *name
	}
	if namespace == nil {
		return identity
	}
	return identity + " (namespace: " + safeJsonStringify(*namespace) + ")"
}

// executionArgumentsText is `executionArgumentsText` (message-text.ts:45-49).
func executionArgumentsText(call toolCallLike) string {
	if !call.hasExecutionArguments {
		return ""
	}
	return "\nExecution arguments: " + safeJsonStringify(call.executionArguments)
}

func optStr(m rec, k string) *string {
	if s, ok := str(m, k); ok {
		return &s
	}
	return nil
}

// toolCallOf is `toolCallOf` (message-text.ts:50-63).
func toolCallOf(part any) (toolCallLike, bool) {
	m, ok := recOf(part)
	if !ok {
		return toolCallLike{}, false
	}
	if t, ok := str(m, "type"); !ok || t != "toolCall" {
		return toolCallLike{}, false
	}
	call := toolCallLike{id: optStr(m, "id"), name: optStr(m, "name"), namespace: optStr(m, "namespace")}
	call.arguments, call.hasArguments = m["arguments"]
	call.executionArguments, call.hasExecutionArguments = m["executionArguments"]
	call.async, _ = boolean(m, "async")
	return call, true
}

// toolCalls is `toolCalls` (message-text.ts:64-69).
func toolCalls(content any) []toolCallLike {
	parts, _ := arrOf(content)
	var calls []toolCallLike
	for _, part := range parts {
		if call, ok := toolCallOf(part); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

// blockText is `blockText` (message-text.ts:70-86).
func blockText(part any) string {
	m, ok := recOf(part)
	if !ok {
		return ""
	}
	switch t, _ := str(m, "type"); t {
	case "text":
		s, _ := optionalString(m["text"])
		return s
	case "thinking":
		s, _ := optionalString(m["thinking"])
		return s
	}
	call, ok := toolCallOf(part)
	if !ok {
		return ""
	}
	var arguments any = rec{}
	if call.arguments != nil {
		arguments = call.arguments
	}
	text := toolIdentity(call.name, call.namespace) + " " + safeJsonStringify(arguments)
	if call.id != nil {
		text += "\nCall ID: " + *call.id
	}
	return text + executionArgumentsText(call)
}

// textOf is `textOf` (message-text.ts:87-95).
func textOf(message messageLike) string {
	if s, ok := message["content"].(string); ok {
		return s
	}
	parts, _ := arrOf(message["content"])
	if len(parts) == 1 {
		return blockText(parts[0]) // the common single block: no copy
	}
	var b strings.Builder
	first := true
	for _, part := range parts {
		text := blockText(part)
		if text == "" {
			continue
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(text)
	}
	return b.String()
}

// assistantFailure is `assistantFailure` (message-text.ts:96-105).
func assistantFailure(message messageLike) string {
	if role, _ := str(message, "role"); role != "assistant" {
		return ""
	}
	errorMessage, _ := str(message, "errorMessage")
	stopReason, hasStop := str(message, "stopReason")
	if stopReason != "error" && stopReason != "aborted" && errorMessage == "" {
		return ""
	}
	if !hasStop {
		stopReason = "error"
	}
	if errorMessage == "" {
		return "[" + stopReason + "]"
	}
	return "[" + stopReason + "] " + errorMessage
}

// isRecoveryTool is `isRecoveryTool` (message-text.ts:106-108).
func isRecoveryTool(name any) bool {
	s, ok := name.(string)
	return ok && (s == "history" || s == "notes" || s == "new_context")
}

// isRecoveryCall is `isRecoveryCall` (message-text.ts:109-112).
func isRecoveryCall(part any) bool {
	call, ok := toolCallOf(part)
	if !ok || call.name == nil {
		return false
	}
	return isRecoveryTool(*call.name)
}

// shortKey is `shortKey` (message-text.ts:113-116): ten base64url characters of SHA-256 over the
// UTF-8 text (a lone surrogate hashes as U+FFFD, as Node encodes it).
func shortKey(text string) string {
	sum := sha256.Sum256(jsUTF8Bytes(text))
	var encoded [43]byte
	base64.RawURLEncoding.Encode(encoded[:], sum[:])
	return string(encoded[:10])
}

// sourceTag is `sourceTag` (message-text.ts:117-120): nested session files are subagent runs.
func sourceTag(source string) string {
	if strings.ContainsRune(source, os.PathSeparator) {
		return " [subagent]"
	}
	return ""
}

// excerptAround is `excerptAround` (message-text.ts:121-125).
func excerptAround(text string, index, before, length int) string {
	total := u16len(text)
	start := max(0, index-before)
	end := min(total, start+length)
	var b strings.Builder
	if start > 0 {
		b.WriteString("…")
	}
	b.WriteString(u16slice(text, start, end))
	if end < total {
		b.WriteString("…")
	}
	return b.String()
}

const (
	excerptMarker       = "\n… middle omitted …\n"
	excerptMarkerLength = 20 // excerptMarker.length in UTF-16 units
)

// excerpt is `excerpt` (message-text.ts:126-134).
func excerpt(text string, limit int) string {
	total := u16len(text)
	if total <= limit {
		return text
	}
	if limit <= excerptMarkerLength {
		return u16slice(text, 0, limit)
	}
	head := (limit - excerptMarkerLength) / 2
	tail := limit - excerptMarkerLength - head
	return u16slice(text, 0, head) + excerptMarker + u16slice(text, total-tail, u16end)
}

// boundedBlock is `boundedBlock` (message-text.ts:135-138).
func boundedBlock(header, text string, limit int) string {
	textLimit := max(0, limit-u16len(header)-1)
	if textLimit > 0 {
		return header + "\n" + excerpt(text, textLimit)
	}
	return header
}
