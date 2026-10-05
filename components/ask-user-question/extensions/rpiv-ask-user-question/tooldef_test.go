package ask_user_question_test

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"time"

	ask "github.com/MichaelKinsy/pigpen/ask-user-question"
)

// ToolDef is what the extension registered for a tool. The template fake host records only tool names; the
// guidance, schema and label tests read the rest. It is read from the extension's register frame on a pipe of
// its own, so fakehost_test.go stays the Skill's template (scripts/skill-templates.test.mjs).
type ToolDef struct {
	Label            string
	Description      string
	PromptSnippet    string
	PromptGuidelines []string
	Parameters       json.RawMessage
}

// ToolDef returns the registration of tool name, made by a fresh extension under the test's environment (its
// HOME decides the config the registration reads), as the rig's own extension made it.
func (r *rig) ToolDef(name string) ToolDef {
	t := r.Host.t
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ask.Extension().RunWithConn(extSide) }()
	var hdr [4]byte
	if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
		t.Fatalf("read register header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(hostSide, data); err != nil {
		t.Fatalf("read register frame: %v", err)
	}
	_ = hostSide.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("extension did not stop after its host closed")
	}
	var f struct {
		Register struct {
			Tools []struct {
				Name             string          `json:"name"`
				Label            string          `json:"label"`
				Description      string          `json:"description"`
				PromptSnippet    string          `json:"prompt_snippet"`
				PromptGuidelines []string        `json:"prompt_guidelines"`
				Parameters       json.RawMessage `json:"parameters"`
			} `json:"tools"`
		} `json:"register"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("decode register frame %s: %v", data, err)
	}
	for _, tl := range f.Register.Tools {
		if tl.Name == name {
			return ToolDef{Label: tl.Label, Description: tl.Description, PromptSnippet: tl.PromptSnippet, PromptGuidelines: tl.PromptGuidelines, Parameters: tl.Parameters}
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return ToolDef{}
}
