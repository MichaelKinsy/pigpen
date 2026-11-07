package posthorse

import (
	"fmt"
	"strings"
	"testing"
)

// rcBigBranch is a 5,000-entry active branch: owner inputs, coordination
// messages, tool calls with results (some async) and notes activity.
func rcBigBranch(n int) []rec {
	branch := []rec{
		{"type": "context_window", "id": "window", "handoff": "Original approved goal"},
	}
	for i := 0; len(branch) < n; i++ {
		id := fmt.Sprintf("00000000-0000-7000-8000-%012d", i)
		switch i % 5 {
		case 0:
			branch = append(branch, rcTimed("owner-"+id, "2026-11-01T00:00:00Z", rec{"role": "user", "content": "owner input " + strings.Repeat("x", 200)}))
		case 1:
			branch = append(branch, rec{"type": "custom_message", "id": "coord-" + id, "timestamp": "2026-11-01T00:00:01Z", "customType": "intercom", "content": "coordination " + strings.Repeat("y", 200), "display": true})
		case 2:
			branch = append(branch, rcMsg("call-"+id, rec{"role": "assistant", "stopReason": "toolUse", "content": []any{
				rcCall("c-"+id, "bash", rec{"command": "go test ./..."}),
			}}))
		case 3:
			branch = append(branch, rcMsg("res-"+id, rec{"role": "toolResult", "toolCallId": "c-" + fmt.Sprintf("00000000-0000-7000-8000-%012d", i-1), "toolName": "bash", "content": strings.Repeat("r", 400)}))
		default:
			branch = append(branch, rcMsg("note-"+id, rec{"role": "assistant", "content": []any{
				rcNotesCall("n-"+id, "write", fmt.Sprintf("note-%d.md", i%7), "state"),
			}}))
		}
	}
	return branch
}

func BenchmarkBuildAutoHandoff5000(b *testing.B) {
	branch := rcBigBranch(5_000)
	projected := rcProjection(branch)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := buildAutoHandoff(branch, projected, maxHandoffChars, autoHandoffOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildAutoHandoff5000Retained(b *testing.B) {
	branch := rcBigBranch(5_000)
	projected := rcProjection(branch)
	retained := make(map[string]struct{})
	for _, entry := range branch[len(branch)-40:] {
		id, _ := entry["id"].(string)
		retained[id] = struct{}{}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := buildAutoHandoff(branch, projected, maxHandoffChars, autoHandoffOptions{retainedEntryIds: retained}); err != nil {
			b.Fatal(err)
		}
	}
}
