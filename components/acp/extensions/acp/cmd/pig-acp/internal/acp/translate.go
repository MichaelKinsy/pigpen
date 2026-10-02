package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// This file ports src/acp/translate/{prompt,pi-tools,pi-messages,bash}.ts. Values that come
// from pi are decoded JSON (map[string]any, []any, string, float64), read the way the original
// reads them: a wrong type reads as absent, never as an error.

// jsTrim is String.prototype.trim: Unicode white space plus the byte order mark.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
}

func asObject(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func strOf(m map[string]any, key string) (string, bool) {
	s, ok := m[key].(string)
	return s, ok
}

// base64ByteLength is Buffer.byteLength(s, 'base64').
func base64ByteLength(s string) int {
	n := len(s)
	if n == 0 {
		return 0
	}
	pad := 0
	if strings.HasSuffix(s, "==") {
		pad = 2
	} else if strings.HasSuffix(s, "=") {
		pad = 1
	}
	return (n*3)>>2 - pad
}

// PromptToPiMessage turns ACP prompt blocks into pi message text and images.
func PromptToPiMessage(blocks []ContentBlock) (string, []Image) {
	var msg strings.Builder
	images := []Image{}
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			if s, ok := b["text"].(string); ok {
				msg.WriteString(s)
			}
		case "resource_link":
			fmt.Fprintf(&msg, "\n[Context] %s", jsString(b["uri"]))
		case "image":
			// pi expects base64 image bytes in data without a data-url prefix.
			images = append(images, Image{"type": "image", "mimeType": b["mimeType"], "data": b["data"]})
		case "resource":
			r := asObject(b["resource"])
			uri := "(unknown)"
			if s, ok := strOf(r, "uri"); ok {
				uri = s
			}
			if text, ok := strOf(r, "text"); ok {
				mime := "text/plain"
				if s, ok := strOf(r, "mimeType"); ok {
					mime = s
				}
				fmt.Fprintf(&msg, "\n[Embedded Context] %s (%s)\n%s", uri, mime, text)
			} else if blob, ok := strOf(r, "blob"); ok {
				mime := "application/octet-stream"
				if s, ok := strOf(r, "mimeType"); ok {
					mime = s
				}
				fmt.Fprintf(&msg, "\n[Embedded Context] %s (%s, %d bytes)", uri, mime, base64ByteLength(blob))
			} else {
				fmt.Fprintf(&msg, "\n[Embedded Context] %s", uri)
			}
		case "audio":
			data, _ := b["data"].(string)
			fmt.Fprintf(&msg, "\n[Audio] (%s, %d bytes) not supported by pig-acp", jsString(b["mimeType"]), base64ByteLength(data))
		}
	}
	return msg.String(), images
}

// jsString renders a value like a JS template literal would.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case string:
		return x
	case float64:
		return fmt.Sprint(x)
	default:
		return fmt.Sprint(x)
	}
}

func textBlocks(content any) string {
	arr, ok := content.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, c := range arr {
		m := asObject(c)
		if m["type"] == "text" {
			if s, ok := m["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// NormalizePiMessageText joins the text of a user message.
func NormalizePiMessageText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	return textBlocks(content)
}

// NormalizePiAssistantText joins the text blocks of an assistant message.
func NormalizePiAssistantText(content any) string { return textBlocks(content) }

func firstString(pairs ...[2]any) (string, bool) {
	for _, p := range pairs {
		if m, ok := p[0].(map[string]any); ok {
			if s, ok := m[p[1].(string)].(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

// asNumber reads a JSON number (float64 after decoding; Go integers when built by hand).
func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	}
	return 0, false
}

func firstNumber(pairs ...[2]any) (float64, bool) {
	for _, p := range pairs {
		if m, ok := p[0].(map[string]any); ok {
			if n, ok := asNumber(m[p[1].(string)]); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// jsonStringify is JSON.stringify(v, null, 2) apart from key order (Go sorts map keys).
func jsonStringify(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// truthy is JS truthiness for the values a tool result can hold.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	}
	return true
}

// ToolResultToText renders a pi tool result as text.
func ToolResultToText(result any) string {
	if !truthy(result) {
		return ""
	}
	res := asObject(result)
	details := asObject(res["details"])

	// pi's edit tool returns a terse success message in content and the full unified diff in details.diff.
	if diff, ok := details["diff"].(string); ok && jsTrim(diff) != "" {
		return diff
	}
	if content, ok := res["content"].([]any); ok {
		var texts []string
		for _, c := range content {
			m := asObject(c)
			if s, ok := m["text"].(string); ok && m["type"] == "text" && s != "" {
				texts = append(texts, s)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "")
		}
	}

	stdout, hasOut := firstString([2]any{details, "stdout"}, [2]any{res, "stdout"}, [2]any{details, "output"}, [2]any{res, "output"})
	stderr, hasErr := firstString([2]any{details, "stderr"}, [2]any{res, "stderr"})
	exit, hasExit := firstNumber([2]any{details, "exitCode"}, [2]any{res, "exitCode"}, [2]any{details, "code"}, [2]any{res, "code"})

	if (hasOut && jsTrim(stdout) != "") || (hasErr && jsTrim(stderr) != "") {
		var parts []string
		if hasOut && jsTrim(stdout) != "" {
			parts = append(parts, stdout)
		}
		if hasErr && jsTrim(stderr) != "" {
			parts = append(parts, "stderr:\n"+stderr)
		}
		if hasExit {
			parts = append(parts, fmt.Sprintf("exit code: %s", jsNumber(exit)))
		}
		return strings.TrimRightFunc(strings.Join(parts, "\n\n"), func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
	}
	return jsonStringify(result)
}

func jsNumber(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprint(int64(f))
	}
	return fmt.Sprint(f)
}

// IsBashTool reports whether a tool name is bash (case-insensitive).
func IsBashTool(name string) bool { return strings.ToLower(name) == "bash" }

// BashCommand finds the command in tool arguments or a result.
func BashCommand(v any) (string, bool) {
	rec := asObject(v)
	get := func(m map[string]any) (any, bool) {
		if m == nil {
			return nil, false
		}
		for _, k := range []string{"command", "cmd"} {
			if x, ok := m[k]; ok && x != nil {
				return x, true
			}
		}
		return nil, false
	}
	// ?? picks the first non-nullish value even when it is not a string.
	for _, m := range []map[string]any{rec, asObject(rec["args"]), asObject(rec["input"]), asObject(rec["rawInput"]), asObject(rec["toolInput"]), asObject(rec["details"])} {
		if x, ok := get(m); ok {
			if s, ok := x.(string); ok && jsTrim(s) != "" {
				return s, true
			}
			return "", false
		}
	}
	return "", false
}

// BashResultText is the output text of a bash result.
func BashResultText(result any) string {
	rec := asObject(result)
	if content, ok := rec["content"].([]any); ok {
		var texts []string
		for _, c := range content {
			m := asObject(c)
			if s, ok := m["text"].(string); ok && m["type"] == "text" && s != "" {
				texts = append(texts, s)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "")
		}
	}
	details := asObject(rec["details"])
	stdout, _ := firstString([2]any{details, "stdout"}, [2]any{rec, "stdout"}, [2]any{details, "output"}, [2]any{rec, "output"})
	stderr, _ := firstString([2]any{details, "stderr"}, [2]any{rec, "stderr"})
	var parts []string
	for _, p := range []string{stdout, stderr} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n")
}

// BashExitCode is the exit code of a bash result (1 for an error without one).
func BashExitCode(result any, isError bool) int {
	rec := asObject(result)
	details := asObject(rec["details"])
	// details?.exitCode ?? record?.exitCode ?? details?.code ?? record?.code: first non-nullish.
	for _, p := range [][2]any{{details, "exitCode"}, {rec, "exitCode"}, {details, "code"}, {rec, "code"}} {
		if m, _ := p[0].(map[string]any); m != nil {
			if v, ok := m[p[1].(string)]; ok && v != nil {
				if n, ok := asNumber(v); ok {
					return int(n)
				}
				break
			}
		}
	}
	if isError {
		return 1
	}
	return 0
}

// BashOutputDelta is the appended part of the output.
func BashOutputDelta(previous, next string) string {
	if strings.HasPrefix(next, previous) {
		return next[len(previous):]
	}
	return next
}

// BashTerminalContent is the terminal tool-call content.
func BashTerminalContent(toolCallID string) []any {
	return []any{map[string]any{"type": "terminal", "terminalId": toolCallID}}
}

// BashTerminalInfoMeta is the terminal_info _meta.
func BashTerminalInfoMeta(toolCallID, cwd string) map[string]any {
	return map[string]any{"terminal_info": map[string]any{"terminal_id": toolCallID, "cwd": cwd}}
}

// BashTerminalOutputMeta is the terminal_output _meta.
func BashTerminalOutputMeta(toolCallID, data string) map[string]any {
	return map[string]any{"terminal_output": map[string]any{"terminal_id": toolCallID, "data": data}}
}

// BashTerminalExitMeta is the terminal_exit _meta.
func BashTerminalExitMeta(toolCallID string, exitCode int) map[string]any {
	return map[string]any{"terminal_exit": map[string]any{"terminal_id": toolCallID, "exit_code": exitCode, "signal": nil}}
}
