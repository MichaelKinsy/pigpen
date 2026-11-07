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
	// HomeFiles are written under the lane's host home before the host starts (relative paths;
	// {{server:NAME}} is expanded): <PIG_HOME> on a pig lane, <HOME>/.pi on a pi lane. A port that
	// keeps its own configuration under PIG_HOME (not the agent directory) and the original that
	// reads the same file from Pi's home are compared with one entry.
	HomeFiles map[string]string `json:"homeFiles,omitempty"`
	// Servers are fake HTTP upstreams (APIs the extension calls). Their base URLs are available
	// as {{server:NAME}} in Env, Files, AgentFiles, HomeFiles, Args and the LLM script (turn text and the string
	// values of tool-call arguments), and every request they receive enters the trace.
	Servers map[string]ServerSpec `json:"servers,omitempty"`
	// Env is extra environment for the host process (an API base URL, a fake credential). The
	// harness's own variables cannot be overridden: PATH, HOME, TMPDIR, LANG, TERM, NO_COLOR, the Go
	// toolchain variables, GOWORK, GOENV and anything starting with EQ_, PIG_, PI_ or GIT_.
	Env map[string]string `json:"env,omitempty"`
	// Capture selects which host events enter the trace. Default DefaultCapture.
	Capture []string `json:"capture,omitempty"`
	// Normalize lists extra normalization rules (N7), applied in order to every string of every
	// lane's trace after the built-in rules. For values the extension makes up on every run
	// (a response id), not for text a port is expected to produce.
	Normalize []NormalizeRule `json:"normalize,omitempty"`

	// Model describes the scripted model the host is given (context window, output limit, reasoning,
	// input kinds). Default: eq-1, 100000 tokens, 4096 output, text only. Needs an llm script.
	Model *ModelSpec `json:"model,omitempty"`
	// Models are further models of the same scripted provider (set_model, per-model settings). Needs Model.
	Models []ModelSpec `json:"models,omitempty"`
	// Settings is the lane's Pi settings.json (the agent directory's), written before the host starts:
	// {"compaction": {"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 20000}, "retry": {"enabled": false}}.
	Settings map[string]any `json:"settings,omitempty"`
	// Session switches the host from --no-session to a real session directory (<run>/sessions, --session-dir).
	Session *SessionSpec `json:"session,omitempty"`
	// Extensions are helper extensions written into the run and loaded with -e next to the extension
	// under test (a tool that returns a huge result, a turn_end hook a test installs). Every lane loads the same source.
	Extensions []HelperExtension `json:"extensions,omitempty"`
	// CaptureFiles records the final state of files (after the host has exited) as the files channel.
	CaptureFiles []FileCapture `json:"captureFiles,omitempty"`
	// Project widens the projection of host events (message keys, image detail, events recorded whole).
	Project *Projection `json:"project,omitempty"`
	// SystemDiff selects how the extension's part of the system prompt is reported: "span" (default: the text between
	// the longest common prefix and suffix) or "hunks" (every changed run of lines on its own, for an extension that
	// changes the prompt in several places, so the host's wording between them is not compared).
	SystemDiff string `json:"systemDiff,omitempty"`
	// CollapseRuns (at least 64) records every run of that many identical characters in a trace string as
	// <run C xN>: for a scenario that feeds the host megabytes of filler. See suite_ext.go.
	CollapseRuns int `json:"collapseRuns,omitempty"`
}

// ModelSpec is one model of the scripted provider.
type ModelSpec struct {
	ID            string   `json:"id,omitempty"` // default eq-1
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	Input         []string `json:"input,omitempty"` // "text", "image"; default text
}

// SessionSpec turns on a real session directory.
type SessionSpec struct {
	// Files are written under the session directory before the host starts (relative paths): seed sessions
	// that switch_session (with {{sessions}} in its sessionPath) opens.
	Files map[string]string `json:"files,omitempty"`
}

// HelperExtension is a helper extension's source.
type HelperExtension struct {
	Name     string `json:"name"` // a bare file name ending in .ts, .mts, .js or .mjs
	Source   string `json:"source"`
	Position string `json:"position,omitempty"` // "before" or "after" (default) the extension under test
}

// Projection widens what the trace keeps of host events. Every field is opt-in: a scenario without
// them records exactly what it recorded before they existed.
type Projection struct {
	// MessageFields are further keys of a message_end message that are kept as they are (errorMessage,
	// summary, tokensBefore, usage, provider, ...).
	MessageFields []string `json:"messageFields,omitempty"`
	// Images records an image content block's mimeType, byte count and a short digest instead of only its type.
	Images bool `json:"images,omitempty"`
	// Full lists host event types recorded whole (every key but the clocks and host identifiers), in
	// addition to the types DefaultFullEvents that are always recorded whole when captured.
	Full []string `json:"full,omitempty"`
	// Drop lists key paths removed from every event recorded whole (same syntax as FileCapture.Drop: dotted key
	// names from the event's root, * for one key, a leading ** for any depth). For a value the two hosts compute
	// differently by design (a token estimate over each host's own system prompt wording).
	Drop []string `json:"drop,omitempty"`
	// SkipSystem leaves out the message_start/message_end events of system messages: the host decides when it
	// records its own prompt snapshot (Pi appends one when a tree navigation lands on a branch that has none, PiG
	// does not), and what the extension adds to the prompt is compared in each request's system delta instead.
	SkipSystem bool `json:"skipSystem,omitempty"`
}

// NormalizeRule is one scenario-level normalization rule (N7): every match of the Go regular
// expression Pattern becomes Replacement, in which $1 and ${name} insert a capture group and $$ is a
// literal dollar sign. A pattern that matches nothing is not an error.
type NormalizeRule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
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
	// Thinking is streamed as reasoning_content before the text (a reasoning model).
	Thinking string `json:"thinking,omitempty"`
	// Usage is what the usage chunk reports to the host. Absent: 1 prompt token and 1 completion token.
	Usage *Usage `json:"usage,omitempty"`
	// FinishReason replaces the finish_reason of the last chunk ("length" truncates the answer).
	FinishReason string `json:"finishReason,omitempty"`
	// Error answers the request with an HTTP error instead of a stream (a context-overflow rejection).
	Error *TurnError `json:"error,omitempty"`
	// DelayMs waits before the response starts.
	DelayMs int `json:"delayMs,omitempty"`
	// Hold keeps the request unanswered until a trigger releases the name (or the host cancels the request:
	// abort). The model request is recorded when it arrives, before the hold.
	Hold string `json:"hold,omitempty"`
}

// Usage is the token usage of one scripted response, in Pi's terms. The OpenAI-compatible chunk
// carries prompt_tokens = input + cacheRead + cacheWrite, completion_tokens = output,
// prompt_tokens_details.cached_tokens = cacheRead and .cache_write_tokens = cacheWrite, and the host
// derives totalTokens as the sum (pi-ai openai-completions parseChunkUsage), so TotalTokens, when
// given, must equal that sum.
type Usage struct {
	Input       int  `json:"input,omitempty"`
	Output      int  `json:"output,omitempty"`
	CacheRead   int  `json:"cacheRead,omitempty"`
	CacheWrite  int  `json:"cacheWrite,omitempty"`
	TotalTokens *int `json:"totalTokens,omitempty"`
}

// TurnError is an HTTP error response. Message becomes {"error":{"message":...,"type":"invalid_request_error"}};
// Body, when set, is sent as it is instead (a provider's own error shape).
type TurnError struct {
	Status  int               `json:"status,omitempty"` // default 400
	Message string            `json:"message,omitempty"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
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
	// Save stores values of the command's response for later steps: name -> path into the response
	// record (data.sessionFile, data.entries.-1.id, data.entries.[type=compaction].id). Later steps (and the
	// triggers of this one) use {{var:NAME}} in any string of their rpc command.
	Save map[string]string `json:"save,omitempty"`
	// Quiet keeps the command's response out of the trace (a helper command whose response carries host wording or
	// random ids, such as get_entries, used only to Save values). A failed response still fails the step.
	Quiet bool `json:"quiet,omitempty"`
	// Triggers act while this step runs: when the host emits an event, or the model server receives a
	// request, send another command (steer, follow_up, abort) and/or release a held model turn.
	Triggers []Trigger `json:"triggers,omitempty"`
	// CaptureFiles records the named files as the files channel after this step has settled.
	CaptureFiles []FileCapture `json:"captureFiles,omitempty"`
}

// Trigger runs once while its step is in flight.
type Trigger struct {
	When TriggerWhen `json:"when"`
	// RPC is sent when the condition holds (its response is awaited before Release).
	RPC map[string]any `json:"rpc,omitempty"`
	// Release lets held model turns (Turn.Hold) answer.
	Release []string `json:"release,omitempty"`
	// Save stores values of RPC's response like Step.Save (get_entries during a run: the entry id a held model
	// turn then quotes as {{var:NAME}}); they are set before Release. Quiet keeps that response out of the trace.
	Save  map[string]string `json:"save,omitempty"`
	Quiet bool              `json:"quiet,omitempty"`
}

// TriggerWhen is the condition of a trigger: Event (with Match and Nth) or LLMRequest.
type TriggerWhen struct {
	// Event is a host event type (tool_execution_start, message_start, compaction_start, ...).
	Event string `json:"event,omitempty"`
	// Match requires keys of the event, by dotted path, to equal these values ("toolName": "dump", "message.role": "toolResult").
	Match map[string]any `json:"match,omitempty"`
	// Nth fires on the nth matching event (default 1).
	Nth int `json:"nth,omitempty"`
	// LLMRequest fires when the nth request of the run reaches the model server (counted from 1).
	LLMRequest int `json:"llmRequest,omitempty"`
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
	data, err = expandRepeats(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
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
	if _, err := compileRules(s.Normalize); err != nil {
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
	if err := s.validateSuiteExt(); err != nil {
		return err
	}
	return s.validateFeatures()
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
