package pi_subagents_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// toolText is the text of a tool result: a string content or the text blocks of a content list.
func toolText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var r struct {
		Content any `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	switch c := r.Content.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, b := range c {
			if m, ok := b.(map[string]any); ok {
				sb.WriteString(m["text"].(string))
			}
		}
		return sb.String()
	}
	return string(raw)
}
