package pi_goal_x

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const gsbF = "goal-surface-baseline"

// extensionRegistration is what an extension declares to the host in its register frame, in registration order.
type extensionRegistration struct {
	Events, Tools, Commands, Renderers []string
}

// registrationOf runs ext against a bare in-memory host and reads its register frame (the original's recording mock Pi: registerTool,
// registerCommand, on and registerMessageRenderer record names in call order).
func registrationOf(t *testing.T, ext *sdk.Extension) extensionRegistration {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()
	defer func() {
		write := func(v any) {
			data, _ := json.Marshal(v)
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
			_, _ = hostSide.Write(hdr[:])
			_, _ = hostSide.Write(data)
		}
		write(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		_ = hostSide.Close()
	}()
	for {
		var hdr [4]byte
		_ = hostSide.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
			t.Fatalf("read register frame: %v", err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostSide, data); err != nil {
			t.Fatalf("read register frame: %v", err)
		}
		var f struct {
			Type     string `json:"type"`
			Register *struct {
				Handlers []struct {
					Event string `json:"event"`
				} `json:"handlers"`
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
				Commands []struct {
					Name string `json:"name"`
				} `json:"commands"`
				Renderers []struct {
					CustomType string `json:"custom_type"`
				} `json:"renderers"`
			} `json:"register"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		if f.Type != "register" || f.Register == nil {
			continue
		}
		var out extensionRegistration
		for _, h := range f.Register.Handlers {
			out.Events = append(out.Events, h.Event)
		}
		for _, tl := range f.Register.Tools {
			out.Tools = append(out.Tools, tl.Name)
		}
		for _, c := range f.Register.Commands {
			out.Commands = append(out.Commands, c.Name)
		}
		for _, r := range f.Register.Renderers {
			out.Renderers = append(out.Renderers, r.CustomType)
		}
		return out
	}
}

// The pinned surface (registration order, which is also the order Pi exposes the tools in).
var (
	gsbExpectedTools    = []string{"get_goal", "create_goal", "update_goal", "set_goal_tasks", "update_goal_task", "goal_question", "goal_questionnaire", "propose_goal_draft"}
	gsbExpectedCommands = []string{"goal", "sisyphus", "goal-cancel", "goal-direct", "sisyphus-direct", "goal-list", "goal-status", "goal-refresh", "goal-recovery", "goal-focus", "goal-unfocus", "goal-settings", "goal-tweak", "goal-clear", "goal-pause", "goal-resume"}
)

func TestGoalSurfaceBaselineTwins(t *testing.T) {
	tw(t, gsbF, "baseline: execution and drafting tools are registered in pinned order", func(t *testing.T) {
		ext, _ := buildGoalExtension(goalDeps{})
		eq(t, registrationOf(t, ext).Tools, gsbExpectedTools, "registered tools")
	})
	tw(t, gsbF, "baseline: exactly 16 slash commands are registered, in pinned order", func(t *testing.T) {
		ext, _ := buildGoalExtension(goalDeps{})
		eq(t, registrationOf(t, ext).Commands, gsbExpectedCommands, "registered commands")
	})
	tw(t, gsbF, "baseline: no duplicate tool or command registrations", func(t *testing.T) {
		ext, _ := buildGoalExtension(goalDeps{})
		r := registrationOf(t, ext)
		for what, names := range map[string][]string{"tools": r.Tools, "commands": r.Commands} {
			seen := map[string]bool{}
			for _, n := range names {
				if seen[n] {
					t.Errorf("duplicate %s registration %s", what, n)
				}
				seen[n] = true
			}
		}
	})
	tw(t, gsbF, "baseline: execution profiles remain three/five tools and drafting is separate", func(t *testing.T) {
		eq(t, fiveGoalTools, []string{"create_goal", "get_goal", "update_goal", "set_goal_tasks", "update_goal_task"}, "five")
		eq(t, coreGoalTools, []string{"create_goal", "get_goal", "update_goal"}, "core")
		eq(t, draftingGoalTools, []string{"goal_question", "goal_questionnaire", "propose_goal_draft"}, "drafting")
	})
	tw(t, gsbF, "baseline: every registered tool name is referenced by goal-tool-names constants", func(t *testing.T) {
		ext, _ := buildGoalExtension(goalDeps{})
		known := map[string]bool{}
		for _, n := range allRegisteredGoalTools {
			known[n] = true
		}
		for _, tool := range registrationOf(t, ext).Tools {
			if !known[tool] {
				t.Errorf("registered tool %s is not named in goal-tool-names", tool)
			}
		}
	})
}
