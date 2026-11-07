package posthorse

// Layer-1 tests for the tool definitions of lane A as the host sees them on the register frame, and for the
// live compaction-policy path (index.ts:46) through the fake host's session-graph answers.

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// covRegisteredTools returns the tools of the register frame the extension sends (all members as JSON).
func covRegisteredTools(t *testing.T, ext *sdk.Extension) []map[string]any {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()
	defer func() {
		_ = hostSide.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("the registration probe did not stop")
		}
	}()
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
			t.Fatalf("read registration: %v", err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostSide, data); err != nil {
			t.Fatalf("read registration: %v", err)
		}
		var frame struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Register *struct {
				Tools []map[string]any `json:"tools"`
			} `json:"register"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("decode registration frame: %v", err)
		}
		switch frame.Type {
		case "call":
			reply, _ := json.Marshal(map[string]any{"type": "call_result", "id": frame.ID, "call_result": map[string]any{"result": map[string]any{}}})
			var out [4]byte
			binary.BigEndian.PutUint32(out[:], uint32(len(reply)))
			_, _ = hostSide.Write(append(out[:], reply...))
		case "register":
			return frame.Register.Tools
		}
	}
}

// context-tools.ts:55-62,92-96: label, description and promptSnippet of the two tools, as the original registers them.
func TestContextToolDefinitionsOnTheRegisterFrame(t *testing.T) {
	want := map[string][3]string{
		"new_context": {
			"New Context",
			"Optionally request a deliberate clean context reset once the current foreground tools succeed; retains no prior messages, unlike automatic rollover. Not required after a notes reminder. Background work continues. Earlier conversation stays recoverable through history. Put continuation state in handoff, or save fuller state in notes first.",
			"optionally request a clean context reset with an optional handoff",
		},
		"get_context_remaining": {
			"Context Remaining",
			"Estimate tokens left before Pi's automatic rollover and the configured context limit. Check before reporting remaining space or changing your work plan because of context limits. A notes-only reminder may arrive before rollover on a working turn; continue afterward and let rollover happen automatically. No routine checks are needed.",
			"verify remaining context before reporting a token count or changing plans because of context limits",
		},
	}
	seen := 0
	for _, tool := range covRegisteredTools(t, newPosthorse(nil)) {
		name, _ := tool["name"].(string)
		w, ok := want[name]
		if !ok {
			continue
		}
		seen++
		if tool["label"] != w[0] || tool["description"] != w[1] || tool["prompt_snippet"] != w[2] {
			t.Errorf("%s: label %q, prompt_snippet %q, description %q", name, tool["label"], tool["prompt_snippet"], tool["description"])
		}
	}
	if seen != 2 {
		t.Errorf("both context tools register, saw %d", seen)
	}
}

// index.ts:46: SettingsManager.inMemory(pi.getSettings()).getCompactionSettings(ctx.model). The fake host answers the
// session-graph calls and records what the extension asked.
func TestLiveCompactionPolicyAsksTheSettingsManager(t *testing.T) {
	var created, resolved []map[string]any
	settings := rec{"compaction": rec{"reserveTokens": float64(9000), "modelOverrides": rec{"fixture/fixture-model": rec{"reserveTokens": float64(1234)}}}}
	f := newFixture(t, withLivePolicy(), withSettings(settings), withUsage(100_000, 1_000),
		withHostAnswer("sessionGraph.new", func(args map[string]any) (any, string) {
			created = append(created, args)
			return rec{"handle": "settings-1"}, ""
		}),
		withHostAnswer("sessionGraph.call", func(args map[string]any) (any, string) {
			resolved = append(resolved, args)
			return rec{"enabled": true, "reserveTokens": float64(1234), "keepRecentTokens": float64(20000)}, ""
		}))
	text := f.Run("get_context_remaining", rec{}).Text()
	coreMatch(t, text, `line at 98,767`) // 100,000 - the model override 1,234 + 1
	if len(created) == 0 || created[0]["kind"] != "SettingsManager" || created[0]["op"] != "inMemory" {
		t.Fatalf("SettingsManager.inMemory was not requested: %v", created)
	}
	forwarded, _ := created[0]["args"].([]any)
	if len(forwarded) == 0 || forwarded[0] == nil {
		t.Errorf("the host's effective settings are passed to inMemory: %v", created[0])
	} else if compaction, _ := forwarded[0].(map[string]any)["compaction"].(map[string]any); compaction["reserveTokens"] != float64(9000) {
		t.Errorf("settings forwarded to inMemory = %v", forwarded[0])
	}
	if len(resolved) == 0 || resolved[0]["method"] != "getCompactionSettings" || resolved[0]["handle"] != "settings-1" {
		t.Fatalf("getCompactionSettings was not requested on the object: %v", resolved)
	}
	args, _ := resolved[0]["args"].([]any)
	// No model is selected in the fake host's state: `ctx.model` is undefined.
	if len(args) != 0 && args[0] != nil {
		t.Errorf("without a selected model no model reaches getCompactionSettings: %v", args)
	}
}

func TestCompactionModelArgKeysOverridesByProviderAndID(t *testing.T) {
	if got := compactionModelArg("claude", "anthropic"); len(got) != 2 || got["provider"] != "anthropic" || got["id"] != "claude" {
		t.Errorf("compactionModelArg = %v", got)
	}
	if compactionModelArg("", "anthropic") != nil {
		t.Error("no model id means no model")
	}
}
