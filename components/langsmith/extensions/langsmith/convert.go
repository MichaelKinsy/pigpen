package langsmith

// Port of the conversion half of src/index.ts: usage metadata (:58-105), content normalisation (:118-181) and the
// provider payload converter (:183-315). Every object is built as an *omap in the original's key order; objects the
// host delivered are map[string]any and are read in JavaScript property order (sorted: port/PORT.md finding F1).

import (
	"errors"
	"fmt"
)

func isNullish(v any) bool { return v == nil || isUndef(v) }

// safeAdd is index.ts:58-66: undefined when every value is null or undefined, else their sum.
func safeAdd(values ...any) any {
	var result any = undef
	for _, v := range values {
		if isNullish(v) {
			continue
		}
		f := numOrZero(v)
		if isUndef(result) {
			result = float64(0)
		}
		result = result.(float64) + f
	}
	return result
}

// numOrZero is `(v ?? 0)` for the numeric values Pi sends (a non-number counts as 0: lane-ext.md).
func numOrZero(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// extractUsageMetadata is index.ts:68-105. It returns undef for a message that is not an assistant message, and the
// TypeError of `message.role` for a null or undefined one.
func extractUsageMetadata(message any) (any, error) {
	if isNullish(message) {
		return nil, fmt.Errorf("Cannot read properties of %s (reading 'role')", jsToString(message))
	}
	if recGet(message, "role") != "assistant" {
		return undef, nil
	}
	usage := recGet(message, "usage")
	totalTokens := recGet(usage, "totalTokens")
	cost := recGet(usage, "cost")
	totalCost := recGet(cost, "total")
	// Tokens but zero cost means pi has no pricing; omit cost so LangSmith prices it (index.ts:76-77).
	costUnknown := numOrZero(totalTokens) > 0 && numOrZero(totalCost) == 0

	metadata := obj(
		"input_tokens", safeAdd(recGet(usage, "input"), recGet(usage, "cacheRead"), recGet(usage, "cacheWrite")),
		"output_tokens", recGet(usage, "output"),
		"total_tokens", totalTokens,
		"input_token_details", obj("cache_read", recGet(usage, "cacheRead"), "cache_creation", recGet(usage, "cacheWrite")),
	)
	if !costUnknown {
		metadata.set("input_cost", safeAdd(recGet(cost, "input"), recGet(cost, "cacheRead"), recGet(cost, "cacheWrite")))
		metadata.set("output_cost", recGet(cost, "output"))
		metadata.set("total_cost", totalCost)
		metadata.set("input_cost_details", obj("cache_read", recGet(cost, "cacheRead"), "cache_creation", recGet(cost, "cacheWrite")))
	}
	return metadata, nil
}

// restOf is the object rest `{...rest}` of a destructuring that took the listed keys out of rec.
func restOf(rec *omap, taken ...string) *omap {
	out := &omap{keys: make([]string, 0, len(rec.keys)), vals: make(map[string]any, len(rec.keys))}
outer:
	for _, k := range rec.ownKeys() {
		for _, t := range taken {
			if k == t {
				continue outer
			}
		}
		out.keys = append(out.keys, k)
		out.vals[k] = rec.vals[k]
	}
	return out
}

// normalizeContentPart is index.ts:128-135: Pi's { type, mimeType, data } image becomes the LangChain v1 block
// { type, mime_type, base64 }; anything else is returned as it came.
func normalizeContentPart(part any) any {
	rec, ok := toOmap(part)
	if !ok {
		return part
	}
	if t, isStr := rec.val("type").(string); !isStr || t != "image" { // MULTIMODAL_PART_TYPES
		return part
	}
	mime, ok1 := rec.val("mimeType").(string)
	data, ok2 := rec.val("data").(string)
	if !ok1 || !ok2 {
		return part
	}
	out := restOf(rec, "mimeType", "data")
	out.set("mime_type", mime)
	out.set("base64", data)
	return out
}

// normalizeContent is index.ts:137-140.
func normalizeContent(content any) any {
	arr, ok := content.([]any)
	if !ok {
		return content
	}
	out := make([]any, len(arr))
	for i, p := range arr {
		out[i] = convertContentPart(p)
	}
	return out
}

// convertContentPart is index.ts:146-153: tool calls become `tool_call` parts, multimodal parts are normalised.
func convertContentPart(part any) any {
	if rec, ok := toOmap(part); ok && rec.val("type") == "toolCall" {
		out := obj("type", "tool_call", "args", rec.val("arguments"))
		return out.spread(restOf(rec, "arguments", "type"))
	}
	return normalizeContentPart(part)
}

var errNotRecord = errors.New("convertMessages: message is not an object")

// convertMessages is index.ts:155-167. A message that is not an object makes the original's destructuring fail.
func convertMessages(messages []any) ([]any, error) {
	out := make([]any, len(messages))
	for i, m := range messages {
		rec, ok := toOmap(m)
		if !ok {
			return nil, errNotRecord
		}
		var role any = rec.val("role")
		if role == "toolResult" {
			role = "tool"
		}
		o := obj("role", role, "content", normalizeContent(rec.val("content")))
		out[i] = o.spread(restOf(rec, "role", "content"))
	}
	return out, nil
}

// convertToolOutputs is index.ts:169-181.
func convertToolOutputs(result any) *omap {
	if rec, ok := toOmap(result); ok {
		if content := rec.val("content"); !isNullish(content) {
			o := obj("role", "tool").spread(rec)
			o.set("content", normalizeContent(content))
			return obj("output", o)
		}
	}
	return obj("output", result)
}

// appendFlat is the flatMap step: an array result is spliced in (one level), anything else appended.
func appendFlat(dst []any, v any) []any {
	if arr, ok := v.([]any); ok {
		return append(dst, arr...)
	}
	return append(dst, v)
}

// geminiRole is `{ user: "user", model: "assistant" }[role] ?? role` (index.ts:225). A role naming an Object.prototype
// member finds that property, so `??` keeps it: a function is omitted by JSON.stringify, and `__proto__` is
// Object.prototype, which stringifies as {}.
func geminiRole(role string) any {
	switch role {
	case "user":
		return "user"
	case "model":
		return "assistant"
	case "__proto__":
		return newOmap()
	case "constructor", "__defineGetter__", "__defineSetter__", "hasOwnProperty", "__lookupGetter__", "__lookupSetter__",
		"isPrototypeOf", "propertyIsEnumerable", "toString", "valueOf", "toLocaleString":
		return undef
	}
	return role
}

var errNullRole = errors.New("Cannot read properties of null (reading 'role')")

// convertProviderPayload is index.ts:183-315. It fails where the original throws: a null entry of `messages` when the
// system prompt is checked (index.ts:295).
func convertProviderPayload(payload any) (any, error) {
	p, ok := toOmap(payload)
	if !ok {
		return obj("payload", payload), nil
	}

	// OpenAI Responses input (index.ts:186-214).
	if input, isArr := p.val("input").([]any); isArr && isNullish(p.val("messages")) {
		msgs := make([]any, 0, len(input))
		for _, maybe := range input {
			rec, isRec := toOmap(maybe)
			if !isRec {
				msgs = appendFlat(msgs, maybe)
				continue
			}
			if content, isContent := rec.val("content").([]any); isContent && rec.val("role") == "user" {
				parts := make([]any, len(content))
				for i, part := range content {
					parts[i] = part
					if pr, isPart := toOmap(part); isPart && pr.val("type") == "input_text" {
						parts[i] = obj("type", "text").spread(restOf(pr, "type"))
					}
				}
				msgs = append(msgs, obj("role", rec.val("role"), "content", parts).spread(restOf(rec, "role", "content")))
				continue
			}
			msgs = append(msgs, maybe)
		}
		return obj("messages", msgs).spread(restOf(p, "input")), nil
	}

	// Gemini (index.ts:216-264).
	if contents, isArr := p.val("contents").([]any); isArr && isNullish(p.val("messages")) {
		msgs := make([]any, len(contents))
		for i, maybe := range contents {
			msgs[i] = maybe
			rec, isRec := toOmap(maybe)
			if !isRec {
				continue
			}
			role, isStr := rec.val("role").(string)
			parts, isParts := rec.val("parts").([]any)
			if !isStr || !isParts {
				continue
			}
			content := make([]any, len(parts))
			for j, part := range parts {
				content[j] = geminiPart(part)
			}
			msgs[i] = obj("role", geminiRole(role), "content", content).spread(restOf(rec, "role", "parts"))
		}
		return obj("messages", msgs).spread(restOf(p, "contents")), nil
	}

	// Chat-completions style messages, Anthropic included (index.ts:266-312).
	if messages, isArr := p.val("messages").([]any); isArr {
		msgs := make([]any, 0, len(messages))
		for _, maybe := range messages {
			rec, isRec := toOmap(maybe)
			if !isRec {
				msgs = appendFlat(msgs, maybe)
				continue
			}
			// Anthropic: mislabeled tool messages as user messages (index.ts:274-286).
			if content, isContent := rec.val("content").([]any); isContent && rec.val("role") == "user" && everyToolResult(content) {
				rest := restOf(rec, "role")
				for _, part := range content {
					pr, _ := toOmap(part) // every part is a record here
					msgs = append(msgs, obj("role", "tool").spread(rest).spread(restOf(pr, "type")))
				}
				continue
			}
			msgs = append(msgs, maybe)
		}

		// Anthropic: handle a complex system message (index.ts:293-309).
		if system := p.val("system"); !isNullish(system) {
			found := false
			for _, msg := range msgs {
				if msg == nil {
					return nil, errNullRole
				}
				if r := recGet(msg, "role"); r == "system" || r == "developer" {
					found = true
					break
				}
			}
			if !found {
				content := system
				if arr, isSys := system.([]any); isSys && len(arr) == 1 {
					if rec, isRec := toOmap(arr[0]); isRec && rec.val("type") == "text" {
						if text, isText := rec.val("text").(string); isText {
							content = text
						}
					}
				}
				msgs = append([]any{obj("role", "system", "content", content, "_raw", system)}, msgs...)
			}
		}
		return obj("messages", msgs).spread(restOf(p, "messages", "system")), nil
	}

	return payload, nil
}

// everyToolResult is `content.every(part => isRecord(part) && part.type === "tool_result")` (true for no parts).
func everyToolResult(content []any) bool {
	for _, part := range content {
		if !isRecord(part) || recGet(part, "type") != "tool_result" {
			return false
		}
	}
	return true
}

// geminiPart is the part mapper of index.ts:226-258.
func geminiPart(part any) any {
	rec, ok := toOmap(part)
	if !ok {
		return part
	}
	if text, isStr := rec.val("text").(string); isStr {
		return obj("type", "text", "text", text).spread(restOf(rec, "text"))
	}
	if fc := rec.val("functionCall"); isRecord(fc) {
		id := recGet(fc, "id")
		if isNullish(id) {
			id = nil
		}
		call := obj("id", id, "name", recGet(fc, "name"), "arguments", recGet(fc, "args"))
		return obj("type", "function_call", "function_call", call).spread(restOf(rec, "functionCall"))
	}
	if fr := rec.val("functionResponse"); isRecord(fr) {
		return obj("type", "function_response", "function_response", fr).spread(restOf(rec, "functionResponse"))
	}
	if iu := rec.val("imageUrl"); isRecord(iu) {
		return obj("type", "image_url", "image_url", iu).spread(restOf(rec, "imageUrl"))
	}
	return part
}
