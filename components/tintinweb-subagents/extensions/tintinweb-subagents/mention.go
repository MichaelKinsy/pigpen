package tintinweb_subagents

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// The `@handle` grammar for messaging a subagent from the prompt (Claude Code's, reproduced): suggestions fire on `@`
// at the start of the input or after whitespace, followed by `[\w-]*`; a send is recognized only at the START of the
// input, and only with a non-empty message after the handle. A record's own identity (a UUID and a non-unique
// description) is not typeable, so the handle is derived from the agent type, and colliding handles are numbered
// (`explore`, `explore-2`). upstream: src/mention.ts.

// maxHandleLength bounds a handle, as Claude Code's `dSS` does: an agent type or a model-supplied name can be
// arbitrary text, and an unbounded handle would wrap the suggestion popup. upstream: mention.ts MAX_HANDLE_LENGTH.
const maxHandleLength = 64

// reservedHandle addresses the main conversation rather than any subagent, so it is never allocated to one. Claude
// Code reserves exactly this name. upstream: mention.ts RESERVED_HANDLES.
const reservedHandle = "main"

// isReservedHandle reports whether `@handle` names the main conversation. upstream: mention.ts isReservedHandle.
func isReservedHandle(handle string) bool { return strings.ToLower(handle) == reservedHandle }

// isHandleChar is `[\w-]`: the characters a handle can be typed with.
func isHandleChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// jsLower is JS `toLowerCase` for what handleBase can see: Go's simple case mapping differs from JS's full one at
// U+0130 only (JS: i followed by U+0307).
func jsLower(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "\u0130", "i\u0307"))
}

// handleBase is the slug of an agent type or name, restricted to the `[\w-]` the grammar allows: lowercase, every
// other run turned into one hyphen, no edge hyphens, at most maxHandleLength characters, "agent" when nothing is left.
// upstream: mention.ts handleBase.
func handleBase(s string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range jsLower(s) {
		if r < 0x80 && isHandleChar(byte(r)) && !(r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
			hyphen = false
		} else if !hyphen {
			b.WriteByte('-')
			hyphen = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > maxHandleLength {
		slug = slug[:maxHandleLength]
	}
	// The cut can land mid-run and leave the trailing hyphen back.
	slug = strings.TrimRight(slug, "-")
	if slug == "" {
		return "agent"
	}
	return slug
}

// assignHandleIn is base, else base-2, base-3, ...: the first form that is neither taken nor reserved. One shared set
// covers type-derived handles and name aliases alike, so the two can never collide. upstream: mention.ts assignHandle.
func assignHandleIn(base string, taken map[string]bool) string {
	candidate := base
	for n := 1; taken[candidate] || candidate == reservedHandle; {
		n++
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return candidate
}

// resolveHandleToType maps a typed handle back to a registered agent type, so `@explore fix it` reaches the Explore
// agent even when no instance has ever run. A type that slugs to the reserved name is unaddressable. upstream:
// mention.ts resolveHandleToType.
func resolveHandleToType(handle string, types []string) (string, bool) {
	wanted := strings.ToLower(handle)
	if wanted == reservedHandle {
		return "", false
	}
	for _, t := range types {
		if handleBase(t) == wanted {
			return t, true
		}
	}
	return "", false
}

// stripAgentPrefix unwraps Claude Code's manual `@agent-<type>` spelling, an exact synonym that the caller tries only
// after the handle as written. It reports false when the prefix is absent or is the whole handle. upstream:
// mention.ts stripAgentPrefix.
func stripAgentPrefix(handle string) (string, bool) {
	const prefix = "agent-"
	if len(handle) <= len(prefix) || !strings.EqualFold(handle[:len(prefix)], prefix) {
		return "", false
	}
	return handle[len(prefix):], true
}

// describeMention is the short description a spawn needs: the message's first line, whitespace collapsed, clipped to
// 40 UTF-16 units. upstream: mention.ts describeMention.
func describeMention(message string) string {
	line, _, _ := strings.Cut(message, "\n")
	oneLine := jsTrim(strings.Join(strings.FieldsFunc(line, isJSSpace), " "))
	units := utf16.Encode([]rune(oneLine))
	if len(units) <= 40 {
		return oneLine
	}
	return strings.TrimRightFunc(string(utf16.Decode(units[:39])), isJSSpace) + "…"
}

// agentMentionReminder is what Claude Code sends the main model when a mention names an agent it could start, verbatim
// from its 2.1.233 bundle, trailing space before the closing newline included. upstream: mention.ts.
func agentMentionReminder(typ string) string {
	return "<system-reminder>\nThe user has expressed a desire to invoke the agent \"" + typ + "\". Please invoke the agent appropriately, passing in the required context to it. \n</system-reminder>"
}

// parseMention splits `@handle message` into its parts, or reports false when the text is not a send: a bare handle, a
// leading file path, a mention that is not at the start. upstream: mention.ts parseMention (MENTION_SEND).
func parseMention(text string) (handle, message string, ok bool) {
	if len(text) == 0 || text[0] != '@' {
		return "", "", false
	}
	i := 1
	for i < len(text) && isHandleChar(text[i]) {
		i++
	}
	rest := text[i:]
	if i == 1 || rest == "" {
		return "", "", false
	}
	if first, _ := utf8.DecodeRuneInString(rest); !isJSSpace(first) {
		return "", "", false
	}
	message = jsTrim(rest)
	if message == "" {
		return "", "", false
	}
	return text[1:i], message, true
}

// mentionTrigger finds the suggestion trigger at the end of text: an `@` at the start or after whitespace (or one of
// the CJK sentence-ending marks Claude Code accepts as a boundary), then the partial handle typed so far. It reports
// that partial handle. upstream: mention.ts MENTION_TRIGGER, /(^|[\s。、？！])@([\w-]*)$/.
func mentionTrigger(text string) (partial string, ok bool) {
	end := len(text)
	i := end
	for i > 0 && isHandleChar(text[i-1]) {
		i--
	}
	if i == 0 || text[i-1] != '@' {
		return "", false
	}
	at := i - 1
	if at > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:at])
		if !isJSSpace(r) && r != '。' && r != '、' && r != '？' && r != '！' {
			return "", false
		}
	}
	return text[i:end], true
}

// utf16Prefix is JS `line.slice(0, col)`: the first col UTF-16 code units of line. Pi counts cursor columns in them.
func utf16Prefix(line string, col int) string {
	if col <= 0 {
		return ""
	}
	n := 0
	for i, r := range line {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if n+w > col {
			return line[:i]
		}
		n += w
	}
	return line
}
