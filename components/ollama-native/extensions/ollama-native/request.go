package ollamanative

import (
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// chatRequest turns PiG's transcript into an /api/chat body.
//
// The transcript is {"messages": [...]}. System messages also carry the tool
// set: "toolsAdded" and "toolsRemoved" are changes, applied in order, so the
// tools on offer are what is left after the last message.
func chatRequest(model, transcript map[string]any, opts sdk.ProviderStreamOptions) map[string]any {
	req := map[string]any{"model": model["id"], "stream": true}
	var messages []map[string]any
	var instructions []string
	sections := map[string]string{}
	var toolNames []string
	tools := map[string]map[string]any{}

	raw, _ := transcript["messages"].([]any)
	for _, item := range raw {
		m, _ := item.(map[string]any)
		switch m["role"] {
		case "system":
			for _, t := range list(m["toolsRemoved"]) {
				name, _ := t["name"].(string)
				delete(tools, name)
				toolNames = remove(toolNames, name)
			}
			for _, t := range list(m["toolsAdded"]) {
				name, _ := t["name"].(string)
				if _, seen := tools[name]; !seen {
					toolNames = append(toolNames, name)
				}
				tools[name] = t
			}
			if text, _ := textAndImages(m["content"]); text != "" {
				instructions = append(instructions, text)
			}
			if named, ok := m["sections"].(map[string]any); ok {
				for name, value := range named {
					if text, ok := value.(string); ok {
						sections[name] = text
					} else {
						delete(sections, name) // null removes the section
					}
				}
			}
		case "user":
			text, images := textAndImages(m["content"])
			msg := map[string]any{"role": "user", "content": text}
			if len(images) > 0 {
				msg["images"] = images
			}
			messages = append(messages, msg)
		case "assistant":
			// Thinking is not sent back: Ollama's models are not trained on it as input.
			var text []string
			var calls []map[string]any
			for _, b := range list(m["content"]) {
				switch b["type"] {
				case "text":
					if s, _ := b["text"].(string); s != "" {
						text = append(text, s)
					}
				case "toolCall":
					args, _ := b["arguments"].(map[string]any)
					if args == nil {
						args = map[string]any{}
					}
					calls = append(calls, map[string]any{"function": map[string]any{"name": b["name"], "arguments": args}})
				}
			}
			if len(text) == 0 && len(calls) == 0 {
				continue // an aborted or failed turn that said nothing
			}
			msg := map[string]any{"role": "assistant", "content": strings.Join(text, "\n")}
			if len(calls) > 0 {
				msg["tool_calls"] = calls
			}
			messages = append(messages, msg)
		case "toolResult":
			text, images := textAndImages(m["content"])
			msg := map[string]any{"role": "tool", "tool_name": m["toolName"], "content": text}
			if len(images) > 0 {
				msg["images"] = images
			}
			messages = append(messages, msg)
		}
	}
	names := make([]string, 0, len(sections))
	for name, text := range sections {
		if text != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		instructions = append(instructions, sections[name])
	}
	if len(instructions) > 0 {
		messages = append([]map[string]any{{"role": "system", "content": strings.Join(instructions, "\n\n")}}, messages...)
	}
	req["messages"] = messages

	if len(toolNames) > 0 {
		offered := make([]map[string]any, 0, len(toolNames))
		for _, name := range toolNames {
			t := tools[name]
			params, _ := t["parameters"].(map[string]any)
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			offered = append(offered, map[string]any{"type": "function", "function": map[string]any{
				"name": name, "description": t["description"], "parameters": params}})
		}
		req["tools"] = offered
	}

	options := map[string]any{}
	if v, ok := opts.Values["temperature"].(float64); ok {
		options["temperature"] = v
	}
	if v, ok := opts.Values["maxTokens"].(float64); ok && v > 0 {
		options["num_predict"] = int(v)
	}
	if len(options) > 0 {
		req["options"] = options
	}
	// Ollama refuses `think` for a model without the capability, so it is sent
	// only to models the catalog marked as reasoning, and only when asked for.
	if reasoning, _ := model["reasoning"].(bool); reasoning {
		if on, ok := opts.Values["thinkingEnabled"].(bool); ok {
			req["think"] = on
		}
	}
	return req
}

func list(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func remove(names []string, name string) []string {
	out := names[:0]
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

// textAndImages reads message content, which is a string or a list of blocks.
// Text blocks are joined with newlines; image blocks give their base64 data.
func textAndImages(content any) (string, []string) {
	if s, ok := content.(string); ok {
		return s, nil
	}
	var text, images []string
	for _, b := range list(content) {
		switch b["type"] {
		case "text":
			if s, _ := b["text"].(string); s != "" {
				text = append(text, s)
			}
		case "image":
			if s, _ := b["data"].(string); s != "" {
				images = append(images, s)
			}
		}
	}
	return strings.Join(text, "\n"), images
}
