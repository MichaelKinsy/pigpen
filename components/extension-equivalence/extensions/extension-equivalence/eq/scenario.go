// Package eq is the extension equivalence harness: it drives the SAME scripted
// scenario against a Pi TypeScript extension and its Go port, records every
// observable effect as a trace, and requires the traces to be identical.
//
// A scenario is data (JSON). A lane is one (host, extension) pair. The host is a
// real `pi` or `pig` process in RPC mode, so the extension runs in the real
// runtime, not against a mock. The model is a scripted OpenAI-compatible
// server, child processes are recorded by PATH shims, and dialogs are answered
// from the scenario.
package eq

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Scenario is one scripted event sequence. It is the shared input of every lane.
type Scenario struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Files are written into the workspace before setup runs (relative paths).
	Files map[string]string `json:"files,omitempty"`
	// Setup commands run in the workspace with the real tools and a hermetic
	// git identity, before the host starts. Each entry is an argv.
	Setup [][]string `json:"setup,omitempty"`
	// Commands describes each child process the extension may start. Any command
	// not listed runs unshimmed and unrecorded.
	Commands map[string]CommandSpec `json:"commands,omitempty"`
	// LLM is the script of assistant turns, consumed one per model request.
	LLM []Turn `json:"llm,omitempty"`
	// Args are extra host command-line arguments (for example flags the
	// extension registers). They are identical for every lane.
	Args []string `json:"args,omitempty"`
	// Steps are executed in order after the host is ready.
	Steps []Step `json:"steps"`
	// SettleMs is the quiet period after each step, for effects the extension
	// starts without awaiting. Default 150.
	SettleMs int `json:"settleMs,omitempty"`
	// TailMs is a final quiet period after the last step, before the host is
	// shut down, so late effects are recorded rather than lost. Default 1000;
	// a negative value disables it.
	TailMs int `json:"tailMs,omitempty"`
	// AgentFiles are written under the lane's agent directory before the host starts (relative
	// paths; {{server:NAME}} is expanded). An extension's user-level configuration lives there
	// (Pi's <agent dir>/settings.json, an extension's own <agent dir>/<name>.json).
	AgentFiles map[string]string `json:"agentFiles,omitempty"`
	// Servers are fake HTTP upstreams (APIs the extension calls). Their base URLs are available
	// as {{server:NAME}} in Env, Files and Args, and every request they receive enters the trace.
	Servers map[string]ServerSpec `json:"servers,omitempty"`
	// Env is extra environment for the host process (an API base URL, a fake credential). The
	// harness's own variables cannot be overridden: PATH, HOME, TMPDIR, LANG, TERM, NO_COLOR, the Go
	// toolchain variables, GOWORK, GOENV and anything starting with EQ_, PIG_, PI_ or GIT_.
	Env map[string]string `json:"env,omitempty"`
	// Capture selects which host events enter the trace. Default DefaultCapture.
	Capture []string `json:"capture,omitempty"`
}

// CommandSpec controls one shimmed child process.
type CommandSpec struct {
	// Mode is "real" (default: run the real tool and record the call), "missing"
	// (the command is not on PATH, so spawning fails) or "canned".
	Mode   string `json:"mode,omitempty"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Exit   int    `json:"exit,omitempty"`
}

// Turn is one scripted assistant response: text, or tool calls, or both.
type Turn struct {
	Text      string     `json:"text,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
}

// ToolCall is a scripted model tool call.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Step sends one RPC command and answers the dialogs it causes.
type Step struct {
	Name string `json:"name,omitempty"`
	// RPC is the command object (`id` is assigned by the harness).
	RPC map[string]any `json:"rpc"`
	// UI answers dialog requests (select, confirm, input, editor) in the order
	// they arrive during this step.
	UI []UIAnswer `json:"ui,omitempty"`
	// Wait is "response" (the command's response), "agent_end" (also wait for
	// the agent run to end) or "ui" (also wait until every scripted dialog
	// answer was requested). Default: "agent_end" for a prompt that is not a
	// slash command; "ui" for a slash command with scripted answers; else
	// "response".
	Wait string `json:"wait,omitempty"`
	// SettleMs overrides the scenario's quiet period after this step.
	SettleMs int `json:"settleMs,omitempty"`
}

// UIAnswer is the reply to one dialog. Exactly one field is set.
type UIAnswer struct {
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Cancelled bool    `json:"cancelled,omitempty"`
}

// DefaultCapture is the host-event allow-list of a trace. Everything else is host
// behavior the extension cannot influence, so it stays out of the comparison.
var DefaultCapture = []string{
	"response", "extension_ui_request", "extension_error",
	"agent_start", "agent_end", "turn_start", "turn_end",
	"tool_execution_start", "tool_execution_end", "message_end",
}

// LoadScenario reads and validates a scenario file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc Scenario
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := sc.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &sc, nil
}

// LoadScenarios loads a scenario file, or every *.json file in a directory.
func LoadScenarios(path string) ([]*Scenario, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		sc, err := LoadScenario(path)
		if err != nil {
			return nil, err
		}
		return []*Scenario{sc}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []*Scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		sc, err := LoadScenario(filepath.Join(path, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no scenarios", path)
	}
	return out, nil
}

// Validate rejects scenarios that cannot run identically in every lane.
func (s *Scenario) Validate() error {
	if !validName(s.Name) {
		return fmt.Errorf("scenario name %q must be lowercase words joined by hyphens", s.Name)
	}
	if len(s.Steps) == 0 {
		return errors.New("scenario needs at least one step")
	}
	for path := range s.Files {
		if filepath.IsAbs(path) || strings.Contains(filepath.ToSlash(filepath.Clean(path)), "../") || filepath.Clean(path) == ".." {
			return fmt.Errorf("file %q must stay inside the workspace", path)
		}
	}
	for name, spec := range s.Commands {
		if name == "" || strings.ContainsAny(name, "/\\") {
			return fmt.Errorf("command %q must be a bare name", name)
		}
		if !slices.Contains([]string{"", "real", "missing", "canned"}, spec.Mode) {
			return fmt.Errorf("command %q: unknown mode %q", name, spec.Mode)
		}
	}
	if err := s.validateServers(); err != nil {
		return err
	}
	for i, step := range s.Steps {
		typ, _ := step.RPC["type"].(string)
		if typ == "" {
			return fmt.Errorf("step %d: rpc.type is required", i)
		}
		if _, ok := step.RPC["id"]; ok {
			return fmt.Errorf("step %d: rpc.id is assigned by the harness", i)
		}
		if !slices.Contains([]string{"", "response", "agent_end", "ui"}, step.Wait) {
			return fmt.Errorf("step %d: unknown wait %q", i, step.Wait)
		}
		for j, a := range step.UI {
			n := 0
			if a.Value != nil {
				n++
			}
			if a.Confirmed != nil {
				n++
			}
			if a.Cancelled {
				n++
			}
			if n != 1 {
				return fmt.Errorf("step %d ui answer %d: set exactly one of value, confirmed, cancelled", i, j)
			}
		}
	}
	return nil
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return !strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-")
}

func (s *Scenario) settle() int {
	if s.SettleMs > 0 {
		return s.SettleMs
	}
	return 150
}

func (s *Scenario) stepSettle(st Step) int {
	if st.SettleMs > 0 {
		return st.SettleMs
	}
	return s.settle()
}

func (s *Scenario) tail() int {
	switch {
	case s.TailMs < 0:
		return 0
	case s.TailMs == 0:
		return 1000
	}
	return s.TailMs
}

func (s *Scenario) capture() []string {
	if len(s.Capture) > 0 {
		return s.Capture
	}
	return DefaultCapture
}

func (st Step) label(i int) string {
	if st.Name != "" {
		return fmt.Sprintf("%02d-%s", i+1, st.Name)
	}
	return fmt.Sprintf("%02d-%s", i+1, st.RPC["type"])
}

func (st Step) wait() string {
	if st.Wait != "" {
		return st.Wait
	}
	if st.RPC["type"] == "prompt" {
		if msg, _ := st.RPC["message"].(string); strings.HasPrefix(msg, "/") {
			if len(st.UI) > 0 {
				return "ui"
			}
			return "response"
		}
		return "agent_end"
	}
	return "response"
}

// usesDriver reports whether a step invokes one of the harness driver commands.
func (s *Scenario) usesDriver() bool {
	for _, st := range s.Steps {
		if msg, _ := st.RPC["message"].(string); strings.HasPrefix(msg, "/eq-") {
			return true
		}
	}
	return false
}
