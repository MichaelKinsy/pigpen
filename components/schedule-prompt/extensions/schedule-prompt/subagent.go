package schedule_prompt

import (
	"context"
	"github.com/MichaelKinsy/pigpen/schedule-prompt/internal/croner"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The per-task `model` mode (upstream: subagent.ts): a fresh in-process agent session with the chosen model runs the prompt to
// completion and its text comes back. The session is PiG's child agent session (sdk.Context.CreateAgentSession, PiG's counterpart of
// Pi's createAgentSession); nothing is persisted.

var defaultToolNames = []string{"bash", "read", "edit", "write", "grep", "find", "ls"}

// modelRegistry is the part of ctx.modelRegistry the runner uses.
type modelRegistry interface {
	Find(provider, id string) map[string]any
	GetAvailable() ([]map[string]any, error)
}

// childSession is the part of a child agent session the runner uses (*sdk.AgentSession).
type childSession interface {
	BindExtensions() error
	Abort() error
	Subscribe(listener sdk.AgentSessionEventListener) func()
	Prompt(ctx context.Context, text string, options ...sdk.PromptOptions) error
	Messages() ([]map[string]any, error)
	Dispose()
}

// subagentHost is the extension context the runner reads: the working directory, the model registry and the session factory.
type subagentHost interface {
	Cwd() string
	ModelRegistry() modelRegistry
	CreateAgentSession(sdk.CreateAgentSessionOptions) (childSession, error)
}

type sdkSubagentHost struct{ ctx sdk.Context }

func (h sdkSubagentHost) Cwd() string { return h.ctx.Cwd() }
func (h sdkSubagentHost) ModelRegistry() modelRegistry {
	return sdkRegistry{h.ctx.ModelRegistry()}
}
func (h sdkSubagentHost) CreateAgentSession(o sdk.CreateAgentSessionOptions) (childSession, error) {
	s, err := h.ctx.CreateAgentSession(o)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type sdkRegistry struct{ r sdk.ModelRegistry }

func (s sdkRegistry) Find(provider, id string) map[string]any { return s.r.Find(provider, id) }
func (s sdkRegistry) GetAvailable() ([]map[string]any, error) { return s.r.GetAvailable() }

func modelField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// resolveModel finds the model: provider/id exactly, else the id (the part after the slash for the slash form) against the models with
// configured auth, exact first, then as a substring, then the name as a substring. A nil model is `undefined`.
func resolveModel(registry modelRegistry, modelStr string) (map[string]any, error) {
	fuzzyNeedle := modelStr
	if slash := strings.Index(modelStr, "/"); slash != -1 {
		provider, id := modelStr[:slash], modelStr[slash+1:]
		if found := registry.Find(provider, id); found != nil {
			return found, nil
		}
		// The slash form did not match exactly: fuzzy against the id part only, since model ids do not carry the provider prefix.
		fuzzyNeedle = id
	}
	needle := strings.ToLower(fuzzyNeedle)
	candidates, err := registry.GetAvailable()
	if err != nil {
		return nil, err
	}
	for _, m := range candidates {
		if strings.ToLower(modelField(m, "id")) == needle {
			return m, nil
		}
	}
	for _, m := range candidates {
		if strings.Contains(strings.ToLower(modelField(m, "id")), needle) {
			return m, nil
		}
	}
	for _, m := range candidates {
		if strings.Contains(strings.ToLower(modelField(m, "name")), needle) {
			return m, nil
		}
	}
	return nil, nil
}

// getLastAssistantText is the text of the latest assistant message that has any: a string content as it is, an array content as its text
// parts joined, trimmed.
func getLastAssistantText(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg["role"] != "assistant" {
			continue
		}
		var parts strings.Builder
		switch c := msg["content"].(type) {
		case string:
			parts.WriteString(c)
		case []any:
			for _, e := range c {
				if m, ok := e.(map[string]any); ok && m["type"] == "text" {
					if text, _ := m["text"].(string); text != "" {
						parts.WriteString(text)
					}
				}
			}
		}
		if text := croner.JSTrim(parts.String()); text != "" {
			return text
		}
	}
	return ""
}

func describeAvailableModels(registry modelRegistry) (string, error) {
	available, err := registry.GetAvailable()
	if err != nil {
		return "", err
	}
	if len(available) == 0 {
		return "No models with configured auth.", nil
	}
	var sample []string
	for _, m := range available[:min(5, len(available))] {
		sample = append(sample, modelField(m, "provider")+"/"+modelField(m, "id"))
	}
	more := ""
	if len(available) > 5 {
		more = ", … (" + strconv.Itoa(len(available)-5) + " more)"
	}
	return "Available: " + strings.Join(sample, ", ") + more, nil
}

// isEnabled is `v === true || (Array.isArray(v) && v.length > 0)`: an empty list means none, as unset.
func isEnabled(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	l, ok := v.([]any)
	return ok && len(l) > 0
}

// nameList is the non-empty list of names, nil otherwise.
func nameList(v any) []string {
	l, ok := v.([]any)
	if !ok || len(l) == 0 {
		return nil
	}
	out := make([]string, len(l))
	for i, e := range l {
		out[i] = jsStr(e)
	}
	return out
}

// runSubagentOnce runs the prompt in a fresh agent session with the model and returns the assistant's text. Cancelling abort aborts the
// run.
func runSubagentOnce(host subagentHost, prompt, modelStr string, abort context.Context, opts subagentOptions) (result subagentResult) {
	fail := func(err error) subagentResult { return subagentResult{Error: err.Error()} }
	registry := host.ModelRegistry()
	model, err := resolveModel(registry, modelStr)
	if err != nil {
		return fail(err)
	}
	if model == nil {
		available, err := describeAvailableModels(registry)
		if err != nil {
			return fail(err)
		}
		return subagentResult{Error: "Unknown model '" + modelStr + "'. " + available}
	}

	extEnabled, skillsEnabled := isEnabled(opts.Extensions), isEnabled(opts.Skills)
	resources := sdk.ResourceOptions{
		// Prevent recursive loading of this extension into the subagent; context files are loaded by default.
		NoExtensions:      !extEnabled,
		NoSkills:          !skillsEnabled,
		NoPromptTemplates: true,
		NoThemes:          true,
		ExtensionNames:    nameList(opts.Extensions),
		SkillNames:        nameList(opts.Skills),
	}
	options := sdk.CreateAgentSessionOptions{Cwd: host.Cwd(), Model: model, InMemory: true, Resources: resources}
	if !extEnabled {
		options.Tools = defaultToolNames
	}
	session, err := host.CreateAgentSession(options)
	if err != nil {
		return fail(err)
	}
	defer session.Dispose()

	if extEnabled {
		if err := session.BindExtensions(); err != nil {
			return fail(err)
		}
	}
	// A signal already aborted aborts an idle session, which does nothing: the run still happens, as in the original.
	runCtx := abort
	if abort != nil && abort.Err() != nil {
		_ = session.Abort()
		runCtx = context.Background()
	}
	if runCtx == nil {
		runCtx = context.Background()
	}

	var buffered strings.Builder
	unsubscribe := session.Subscribe(func(event map[string]any) {
		switch event["type"] {
		case "message_start":
			buffered.Reset()
		case "message_update":
			if ame, ok := event["assistantMessageEvent"].(map[string]any); ok && ame["type"] == "text_delta" {
				delta, _ := ame["delta"].(string)
				buffered.WriteString(delta)
			}
		}
	})
	err = session.Prompt(runCtx, prompt)
	unsubscribe()
	if err != nil {
		return fail(err)
	}
	text := croner.JSTrim(buffered.String())
	if text == "" {
		messages, err := session.Messages()
		if err != nil {
			return fail(err)
		}
		text = getLastAssistantText(messages)
	}
	return subagentResult{OK: true, Text: text}
}

// newSubagentRunner is the scheduler's runner on a retained host context.
func newSubagentRunner(ctx sdk.Context) subagentRunner {
	host := sdkSubagentHost{ctx}
	return func(prompt, model string, abort context.Context, opts subagentOptions) subagentResult {
		return runSubagentOnce(host, prompt, model, abort, opts)
	}
}
