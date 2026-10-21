package pi_goal_x

// ported from goal-prompt-cache.ts
//
// Pi marks the final user block for explicit caching. Our request-only state never enters history, so that block cannot be
// reused on the next request. Move that existing breakpoint to the preceding cacheable history block. Implicit caches and
// cacheRetention=none have no marker and are untouched. Every live block (stable goal state plus volatile counters, and
// previously sent tails retained mid-history) is transient: the breakpoint skips all of them and lands on real history.

func hasKey(o *jsObject, k string) bool {
	_, ok := o.get(k)
	return ok
}

func objDelete(o *jsObject, k string) {
	if _, ok := o.vals[k]; ok {
		delete(o.vals, k)
		o.keys = removeKey(o.keys, k)
	}
}

func lastItem(list []any) any {
	if len(list) == 0 {
		return nil
	}
	return list[len(list)-1]
}

func itemAt(list []any, from int) any {
	if from < 0 || from >= len(list) {
		return nil
	}
	return list[from]
}

// cacheGoalHistory relocates the explicit cache breakpoint of a provider payload off the live tails. It returns the
// payload when it moved a marker and nil (undefined) when the payload was left untouched.
func cacheGoalHistory(payload any, liveContent []string) any {
	live := map[string]bool{}
	for _, text := range liveContent {
		if text != "" {
			live[text] = true
		}
	}
	root := asRecord(payload)
	if len(live) == 0 || root == nil {
		return nil
	}
	messages, ok := root.vals["messages"].([]any)
	if !ok {
		return nil
	}
	lastIndex := len(messages) - 1
	for ; lastIndex >= 0; lastIndex-- {
		var content any
		if m := asRecord(messages[lastIndex]); m != nil {
			content = m.vals["content"]
		}
		switch c := content.(type) {
		case string:
			if len(c) > 0 {
				goto found
			}
		case []any:
			if len(c) > 0 {
				goto found
			}
		case nil, undef:
		default:
			goto found
		}
	}
found:
	last := asRecord(itemAtIdx(messages, lastIndex))
	if last == nil {
		return nil
	}
	if role, _ := last.str("role"); role != "user" {
		return nil
	}
	lastContent, ok := last.vals["content"].([]any)
	if !ok {
		return nil
	}
	// Match on text membership, not the type tag: Bedrock payloads use typeless { text } blocks, and neither cachePoint
	// markers nor thinking blocks carry a text field, so they can never match by accident.
	isLiveText := func(block any) bool {
		record := asRecord(block)
		if record == nil {
			return false
		}
		text, ok := record.str("text")
		return ok && live[text]
	}
	// Bedrock represents its breakpoint as a separate content block. The tail may be several live blocks; skip back over
	// all of them.
	bedrockPoint := asRecord(lastItem(lastContent))
	if bedrockPoint != nil && asRecord(bedrockPoint.vals["cachePoint"]) != nil {
		// Require our live text immediately before the marker. A bare marker or another extension's suffix is not ours
		// to relocate.
		if !isLiveText(itemAt(lastContent, len(lastContent)-2)) {
			return nil
		}
		for i := lastIndex; i >= 0; i-- {
			previous := asRecord(messages[i])
			if previous == nil {
				continue
			}
			content, ok := previous.vals["content"].([]any)
			if !ok {
				continue
			}
			for j := len(content) - 1; j >= 0; j-- {
				block := asRecord(content[j])
				if block == nil || isLiveText(block) {
					continue
				}
				if !(hasKey(block, "text") || hasKey(block, "image") || hasKey(block, "document") || hasKey(block, "toolUse") || hasKey(block, "toolResult")) {
					continue
				}
				last.set("content", lastContent[:len(lastContent)-1])
				// previous may be last itself: re-read so the splice sees the popped content.
				content, _ = previous.vals["content"].([]any)
				if next := asRecord(itemAt(content, j+1)); next == nil || asRecord(next.vals["cachePoint"]) == nil {
					spliced := make([]any, 0, len(content)+1)
					spliced = append(spliced, content[:j+1]...)
					spliced = append(spliced, bedrockPoint.clone())
					spliced = append(spliced, content[j+1:]...)
					previous.set("content", spliced)
				}
				return payload
			}
		}
		return nil
	}
	tail := asRecord(lastItem(lastContent))
	if tail == nil {
		return nil
	}
	tailText, isText := tail.str("text")
	if typ, _ := tail.str("type"); typ != "text" || !isText || !live[tailText] || asRecord(tail.vals["cache_control"]) == nil {
		return nil
	}
	control := tail.vals["cache_control"]
	for i := lastIndex; i >= 0; i-- {
		message := asRecord(messages[i])
		if message == nil {
			continue
		}
		role, _ := message.str("role")
		if role != "user" && role != "assistant" && role != "tool" {
			continue
		}
		if text, ok := message.vals["content"].(string); ok && len(text) > 0 && !live[text] {
			block := newObject()
			block.set("type", "text")
			block.set("text", text)
			block.set("cache_control", control)
			message.set("content", []any{block})
			objDelete(tail, "cache_control")
			return payload
		}
		content, ok := message.vals["content"].([]any)
		if !ok {
			continue
		}
		for j := len(content) - 1; j >= 0; j-- {
			block := asRecord(content[j])
			// Thinking blocks cannot carry cache_control. Never split/reorder tool pairs. Live tails are transient by
			// construction: never anchor the breakpoint on one.
			if block == nil || isLiveText(block) {
				continue
			}
			typ, _ := block.str("type")
			if typ != "text" && typ != "image" && typ != "tool_use" && typ != "tool_result" {
				continue
			}
			if v, ok := block.vals["cache_control"]; !ok || v == nil {
				block.set("cache_control", control)
			}
			objDelete(tail, "cache_control")
			return payload
		}
	}
	return nil
}

func itemAtIdx(list []any, i int) any {
	if i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
}
