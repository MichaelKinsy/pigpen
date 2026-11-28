package memory

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autolearn"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/memory-retain.ts:1-146
// upstream: packages/coding-agent/src/tools/memory-recall.ts:1-108
// upstream: packages/coding-agent/src/tools/memory-reflect.ts:1-94
// upstream: packages/coding-agent/src/tools/memory-edit.ts:1-66
// upstream: packages/coding-agent/src/tools/learn.ts:1-192

//go:embed prompts/retain.md
var retainPromptTemplate string

//go:embed prompts/recall.md
var recallPrompt string

//go:embed prompts/reflect.md
var reflectPrompt string

//go:embed prompts/memory-edit.md
var memoryEditPrompt string

//go:embed prompts/learn.md
var learnPromptTemplate string

//go:embed prompts/mnemopi-instructions.md
var mnemopiInstructionsPrompt string

//go:embed prompts/hindsight-instructions.md
var hindsightInstructionsPrompt string

// Track active Hindsight session configs so session shutdown can best-effort flush
// pending outbox items and release per-session state cleanly.
type sessionHindsightRef struct {
	agentDir string
	cfg      HindsightConfig
	scope    HindsightBankScope
}

var (
	sessionStateMu sync.Mutex
	sessionRefs    = map[string]sessionHindsightRef{}
)

func recordSessionHindsight(sid, agentDir string, cfg HindsightConfig, scope HindsightBankScope) {
	if sid == "" {
		return
	}
	sessionStateMu.Lock()
	sessionRefs[sid] = sessionHindsightRef{agentDir: agentDir, cfg: cfg, scope: scope}
	sessionStateMu.Unlock()
}

func cleanupSession(sid string) {
	if sid == "" {
		return
	}
	sessionStateMu.Lock()
	ref, ok := sessionRefs[sid]
	delete(sessionRefs, sid)
	sessionStateMu.Unlock()
	if ok && ref.cfg.APIURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, _ = FlushHindsightOutbox(ctx, ref.agentDir, ref.cfg, ref.scope)
	}
}

func renderGlobalScopeTemplate(tmpl string, globalScope bool) string {
	const openTag = "{{#if globalScope}}"
	const closeTag = "{{/if}}"
	start := strings.Index(tmpl, openTag)
	if start < 0 {
		return strings.TrimSpace(tmpl)
	}
	end := strings.Index(tmpl[start+len(openTag):], closeTag)
	if end < 0 {
		return strings.TrimSpace(tmpl)
	}
	endIdx := start + len(openTag) + end
	var rendered string
	if globalScope {
		rendered = tmpl[:start] + tmpl[start+len(openTag):endIdx] + tmpl[endIdx+len(closeTag):]
	} else {
		rendered = tmpl[:start] + tmpl[endIdx+len(closeTag):]
	}
	return strings.TrimSpace(rendered)
}

// ── Schemas ──────────────────────────────────────────────────────────────────

func retainSchema(globalScope bool) sdk.Schema {
	itemProps := map[string]any{
		"content": map[string]any{
			"type":        "string",
			"description": "information to remember",
		},
		"context": map[string]any{
			"type":        "string",
			"description": "source context",
		},
	}
	if globalScope {
		itemProps["scope"] = map[string]any{
			"type":        "string",
			"enum":        []any{"project", "global"},
			"description": "storage scope; defaults to project, global is for durable cross-project knowledge",
		}
	}
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "memories to retain",
				"items": map[string]any{
					"type":                 "object",
					"properties":           itemProps,
					"required":             []any{"content"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"items"},
		"additionalProperties": false,
	}
}

func recallSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "natural language search query",
			},
		},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
}

func reflectSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "question to answer",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "optional context",
			},
		},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
}

func memoryEditSchema() sdk.Schema {
	return sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"enum":        []any{"update", "forget", "invalidate"},
				"description": "memory edit operation",
			},
			"id": map[string]any{
				"type":        "string",
				"description": "memory id from recall output",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "replacement content for update",
			},
			"importance": map[string]any{
				"type":        "number",
				"description": "replacement importance for update (0–1)",
			},
			"replacement_id": map[string]any{
				"type":        "string",
				"description": "replacement memory id for invalidate",
			},
		},
		"required":             []any{"op", "id"},
		"additionalProperties": false,
	}
}

func learnSchema(globalScope bool) sdk.Schema {
	props := map[string]any{
		"memory": map[string]any{
			"type":        "string",
			"description": "the durable, self-contained lesson to remember (what, when, why)",
		},
		"context": map[string]any{
			"type":        "string",
			"description": "optional source context for the lesson",
		},
		"skill": map[string]any{
			"type":        "object",
			"description": "also create or enhance a managed skill in the same call",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []any{"create", "update"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "kebab-case skill name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "one-line description of when to use the skill",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "the SKILL.md body in markdown (no frontmatter)",
				},
			},
			"required":             []any{"action", "name", "description", "body"},
			"additionalProperties": false,
		},
	}
	if globalScope {
		props["scope"] = map[string]any{
			"type":        "string",
			"enum":        []any{"project", "global"},
			"description": "storage scope; defaults to project, global is for durable cross-project knowledge",
		}
	}
	return sdk.Schema{
		"type":                 "object",
		"properties":           props,
		"required":             []any{"memory"},
		"additionalProperties": false,
	}
}

// ── Tool Parameter Structs ───────────────────────────────────────────────────

// RetainItem is one item passed to `retain`.
type RetainItem struct {
	Content string `json:"content"`
	Context string `json:"context,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

// RetainParams holds arguments for the `retain` tool.
type RetainParams struct {
	Items []RetainItem `json:"items"`
}

// RecallParams holds arguments for the `recall` tool.
type RecallParams struct {
	Query string `json:"query"`
}

// ReflectParams holds arguments for the `reflect` tool.
type ReflectParams struct {
	Query   string `json:"query"`
	Context string `json:"context,omitempty"`
}

// MemoryEditParams holds arguments for the `memory_edit` tool.
type MemoryEditParams struct {
	Op            string   `json:"op"`
	ID            string   `json:"id"`
	Content       *string  `json:"content,omitempty"`
	Importance    *float64 `json:"importance,omitempty"`
	ReplacementID string   `json:"replacement_id,omitempty"`
}

// LearnSkillPayload is the optional `skill` payload on `learn`.
type LearnSkillPayload struct {
	Action      string `json:"action"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// LearnParams holds arguments for the `learn` tool.
type LearnParams struct {
	Memory  string             `json:"memory"`
	Context string             `json:"context,omitempty"`
	Scope   string             `json:"scope,omitempty"`
	Skill   *LearnSkillPayload `json:"skill,omitempty"`
}

// ── Registration & Lifecycle ─────────────────────────────────────────────────

func retainToolDef(globalScope bool) sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "retain",
		Label:         "Retain",
		Description:   renderGlobalScopeTemplate(retainPromptTemplate, globalScope),
		Parameters:    retainSchema(globalScope),
		DefaultActive: &defaultOff,
		Execute:       executeRetainRaw,
	})
}

func recallToolDef() sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "recall",
		Label:         "Recall",
		Description:   strings.TrimSpace(recallPrompt),
		Parameters:    recallSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeRecallRaw,
	})
}

func reflectToolDef() sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "reflect",
		Label:         "Reflect",
		Description:   strings.TrimSpace(reflectPrompt),
		Parameters:    reflectSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeReflectRaw,
	})
}

func memoryEditToolDef() sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "memory_edit",
		Label:         "Memory Edit",
		Description:   strings.TrimSpace(memoryEditPrompt),
		Parameters:    memoryEditSchema(),
		DefaultActive: &defaultOff,
		Execute:       executeMemoryEditRaw,
	})
}

func learnToolDef(globalScope bool) sdk.ToolDefinition {
	defaultOff := false
	return schemaorder.Apply(sdk.ToolDefinition{
		Name:          "learn",
		Label:         "Learn",
		Description:   renderGlobalScopeTemplate(learnPromptTemplate, globalScope),
		Parameters:    learnSchema(globalScope),
		DefaultActive: &defaultOff,
		Execute:       executeLearnRaw,
	})
}

// Register installs `retain`, `recall`, `reflect`, `memory_edit`, and `learn`
// (all with DefaultActive=false and exact upstream AutoActivate gates), registers
// the `memory://` internal URL handler, and wires per-session cleanup.
func Register(e *sdk.Extension) {
	_ = urls.Register(MemoryHandler{})

	retainDef := retainToolDef(false)
	recallDef := recallToolDef()
	reflectDef := reflectToolDef()
	editDef := memoryEditToolDef()
	learnDef := learnToolDef(false)

	e.RegisterTool(intent.Tool(retainDef))
	e.RegisterTool(intent.Tool(recallDef))
	e.RegisterTool(intent.Tool(reflectDef))
	e.RegisterTool(intent.Tool(editDef))
	e.RegisterTool(intent.Tool(learnDef))

	intent.RegisterSync(e, retainDef)
	intent.RegisterSync(e, recallDef)
	intent.RegisterSync(e, reflectDef)
	intent.RegisterSync(e, editDef)
	intent.RegisterSync(e, learnDef)

	toolcmd.RegisterAutoActivate("retain", RetainRecallReflectActive)
	toolcmd.RegisterAutoActivate("recall", RetainRecallReflectActive)
	toolcmd.RegisterAutoActivate("reflect", RetainRecallReflectActive)
	toolcmd.RegisterAutoActivate("memory_edit", MemoryEditActive)
	toolcmd.RegisterAutoActivate("learn", LearnActive)

	// When a session enables global memory scope on retain/learn, upgrade the
	// session's tool schema to include the conditional `scope` property.
	// Default sessions (`memory.backend == "off"`) perform zero host calls.
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if !IsGlobalMemoryScopeAvailable(ctx) {
			return nil, nil
		}
		if RetainRecallReflectActive(ctx) {
			ctx.RegisterTool(intent.ToolForContext(ctx, retainToolDef(true)))
		}
		if LearnActive(ctx) {
			ctx.RegisterTool(intent.ToolForContext(ctx, learnToolDef(true)))
		}
		return nil, nil
	})

	registerMemoryCommand(e)

	lifecycle.OnEnd("memory", cleanupSession)
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid := sessionIDForContext(ctx); sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}

// BuildMemoryPromptInstructions returns system-prompt memory instructions for
// ctx's configured backend, or "" when `memory.backend == "off"` (zero default
// system prompt drift).
// upstream: packages/coding-agent/src/{memories,mnemopi,hindsight}/backend.ts
func BuildMemoryPromptInstructions(ctx sdk.Context) string {
	switch BackendFor(ctx) {
	case BackendLocal:
		return buildLocalInstructions(ctx)
	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		return truncateApproxTokens(strings.TrimSpace(mnemopiInstructionsPrompt), cfg.InjectionLimit)
	case BackendHindsight:
		if !isHindsightURLConfigured(ctx) {
			return ""
		}
		return strings.TrimSpace(hindsightInstructionsPrompt)
	default:
		return ""
	}
}

func signalContext(ctx sdk.Context) context.Context {
	var zero sdk.Context
	if ctx != zero {
		var sig context.Context
		func() {
			defer func() { _ = recover() }()
			sig = ctx.Signal()
		}()
		if sig != nil {
			return sig
		}
	}
	return context.Background()
}

// ── Execute Implementations ──────────────────────────────────────────────────

// ExecuteRetain runs the `retain` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/memory-retain.ts:64-145
func ExecuteRetain(ctx sdk.Context, params RetainParams) (*sdk.ToolResult, error) {
	if len(params.Items) == 0 {
		return nil, errors.New("retain requires at least one item")
	}
	for i, item := range params.Items {
		if item.Scope != "" && item.Scope != "project" && item.Scope != "global" {
			return nil, fmt.Errorf("invalid scope %q on item %d; expected \"project\" or \"global\"", item.Scope, i+1)
		}
	}

	backend := BackendFor(ctx)
	switch backend {
	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		hasGlobal := false
		for _, item := range params.Items {
			if item.Scope == "global" {
				hasGlobal = true
				break
			}
		}
		if hasGlobal {
			if _, err := cfg.GlobalRetainBank(); err != nil {
				return nil, err
			}
		}

		sid := sessionIDForContext(ctx)
		cwd := ResolveCwd(ctx)
		goCtx := signalContext(ctx)
		storedIDs := make([]string, 0, len(params.Items))
		for i, item := range params.Items {
			var ctxVal any
			if item.Context != "" {
				ctxVal = item.Context
			}
			id, err := MnemopiRemember(goCtx, cfg, MnemopiRememberInput{
				Content:    item.Content,
				Source:     "coding-agent-retain",
				Importance: 0.75,
				MemoryType: "fact",
				Store:      StoreWorking,
				Veracity:   "tool",
				SessionID:  sid,
				Scope:      item.Scope,
				Metadata: map[string]any{
					"session_id": sid,
					"cwd":        cwd,
					"context":    ctxVal,
					"tool":       "retain",
				},
			})
			if err != nil {
				kept := "Nothing was stored."
				if len(storedIDs) > 0 {
					parts := make([]string, len(storedIDs))
					for idx, storedID := range storedIDs {
						parts[idx] = fmt.Sprintf("item %d (id %s)", idx+1, storedID)
					}
					kept = "Stored before the failure and kept: " + strings.Join(parts, ", ") + "."
				}
				untried := ""
				if i+1 < len(params.Items) {
					untried = " Later items were not attempted."
				}
				return nil, fmt.Errorf("Mnemopi did not store item %d of %d: %v. %s%s", i+1, len(params.Items), err, kept, untried)
			}
			storedIDs = append(storedIDs, id)
		}

		count := len(params.Items)
		noun := "memories"
		if count == 1 {
			noun = "memory"
		}
		return &sdk.ToolResult{
			Content: fmt.Sprintf("%d %s stored.", count, noun),
			Details: map[string]any{"count": count},
		}, nil

	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		if cfg.APIURL == "" {
			return nil, errors.New("Hindsight backend is not initialised for this session.")
		}
		cwd := ResolveCwd(ctx)
		agentDir := ResolveAgentDir(ctx)
		scope := ComputeHindsightBankScope(cfg, cwd)
		sid := sessionIDForContext(ctx)
		callID := toolCallIDForContext(ctx)

		inputs := make([]HindsightRetainInput, len(params.Items))
		for i, item := range params.Items {
			inputs[i] = HindsightRetainInput(item)
		}
		if _, err := EnqueueHindsightRetain(agentDir, sid, callID, cfg, scope, inputs); err != nil {
			return nil, err
		}
		recordSessionHindsight(sid, agentDir, cfg, scope)
		// Best-effort immediate flush; failure leaves events safely durable in the JSONL outbox.
		_, _, _ = FlushHindsightOutbox(signalContext(ctx), agentDir, cfg, scope)

		count := len(params.Items)
		noun := "memories"
		if count == 1 {
			noun = "memory"
		}
		return &sdk.ToolResult{
			Content: fmt.Sprintf("%d %s queued.", count, noun),
			Details: map[string]any{"count": count},
		}, nil

	default:
		return nil, fmt.Errorf("retain is not available with memory.backend=%q", backend)
	}
}

func executeRetainRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid retain arguments: %w", err)
	}
	var params RetainParams
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid retain arguments: %w", err)
	}
	return ExecuteRetain(ctx, params)
}

// ExecuteRecall runs the `recall` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/memory-recall.ts:38-107
func ExecuteRecall(ctx sdk.Context, params RecallParams) (*sdk.ToolResult, error) {
	backend := BackendFor(ctx)
	goCtx := signalContext(ctx)

	switch backend {
	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		results, err := MnemopiRecall(goCtx, cfg, params.Query)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return &sdk.ToolResult{
				Content: "No relevant memories found.",
				Details: map[string]any{},
			}, nil
		}
		noun := "memories"
		if len(results) == 1 {
			noun = "memory"
		}
		formatted := FormatMnemopiRecallWithIDs(results)
		return &sdk.ToolResult{
			Content: fmt.Sprintf("Found %d relevant %s (as of %s UTC):\n\n%s", len(results), noun, FormatCurrentTime(time.Now()), formatted),
			Details: map[string]any{},
		}, nil

	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		if cfg.APIURL == "" {
			return nil, errors.New("Hindsight backend is not initialised for this session.")
		}
		client, err := NewHindsightClient(cfg)
		if err != nil {
			return nil, err
		}
		scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
		resp, err := client.Recall(goCtx, scope.BankID, params.Query, cfg, scope)
		if err != nil {
			return nil, err
		}
		if len(resp.Results) == 0 {
			return &sdk.ToolResult{
				Content: "No relevant memories found.",
				Details: map[string]any{},
			}, nil
		}
		noun := "memories"
		if len(resp.Results) == 1 {
			noun = "memory"
		}
		formatted := FormatHindsightMemories(resp.Results)
		return &sdk.ToolResult{
			Content: fmt.Sprintf("Found %d relevant %s (as of %s UTC):\n\n%s", len(resp.Results), noun, FormatCurrentTime(time.Now()), formatted),
			Details: map[string]any{},
		}, nil

	default:
		return nil, fmt.Errorf("recall is not available with memory.backend=%q", backend)
	}
}

func executeRecallRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid recall arguments: %w", err)
	}
	var params RecallParams
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid recall arguments: %w", err)
	}
	return ExecuteRecall(ctx, params)
}

// ExecuteReflect runs the `reflect` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/memory-reflect.ts:39-93
func ExecuteReflect(ctx sdk.Context, params ReflectParams) (*sdk.ToolResult, error) {
	backend := BackendFor(ctx)
	goCtx := signalContext(ctx)

	switch backend {
	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		query := params.Query
		if extra := strings.TrimSpace(params.Context); extra != "" {
			query = strings.TrimSpace(params.Query) + "\n\nAdditional context:\n" + extra
		}
		results, err := MnemopiRecall(goCtx, cfg, query)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return &sdk.ToolResult{
				Content: "No relevant information found to reflect on.",
				Details: map[string]any{},
			}, nil
		}
		summary := FormatMnemopiReflectContext(results)
		return &sdk.ToolResult{
			Content: fmt.Sprintf("Based on recalled memories:\n\n%s", summary),
			Details: map[string]any{},
		}, nil

	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		if cfg.APIURL == "" {
			return nil, errors.New("Hindsight backend is not initialised for this session.")
		}
		client, err := NewHindsightClient(cfg)
		if err != nil {
			return nil, err
		}
		scope := ComputeHindsightBankScope(cfg, ResolveCwd(ctx))
		ensureHindsightBank(goCtx, client, scope.BankID, cfg)
		resp, err := client.Reflect(goCtx, scope.BankID, params.Query, params.Context, cfg, scope)
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(resp.Text)
		if text == "" {
			text = "No relevant information found to reflect on."
		}
		return &sdk.ToolResult{
			Content: text,
			Details: map[string]any{},
		}, nil

	default:
		return nil, fmt.Errorf("reflect is not available with memory.backend=%q", backend)
	}
}

func executeReflectRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid reflect arguments: %w", err)
	}
	var params ReflectParams
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid reflect arguments: %w", err)
	}
	return ExecuteReflect(ctx, params)
}

// ExecuteMemoryEdit runs the `memory_edit` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/memory-edit.ts:39-65
func ExecuteMemoryEdit(ctx sdk.Context, params MemoryEditParams) (*sdk.ToolResult, error) {
	if BackendFor(ctx) != BackendMnemopi {
		return nil, errors.New("Mnemopi backend is not initialised for this session.")
	}
	switch params.Op {
	case "update", "forget", "invalidate":
	default:
		return nil, fmt.Errorf("invalid memory_edit operation %q; expected \"update\", \"forget\", or \"invalidate\"", params.Op)
	}
	if params.Op == "update" && params.Content == nil && params.Importance == nil {
		return nil, errors.New("memory_edit update requires content or importance.")
	}
	var clampedImp *float64
	if params.Importance != nil {
		v := math.Max(0, math.Min(1, *params.Importance))
		clampedImp = &v
	}

	cfg := LoadMnemopiConfig(ctx)
	res, err := MnemopiEditScopedMemory(cfg, params.Op, params.ID, MnemopiEditOptions{
		Content:       params.Content,
		Importance:    clampedImp,
		ReplacementID: params.ReplacementID,
	})
	if err != nil {
		return nil, err
	}

	location := ""
	if res.Bank != "" {
		location = " in bank " + res.Bank
		if res.Store != "" {
			location += " (" + res.Store + ")"
		}
	}
	var text string
	switch res.Status {
	case "not_found":
		text = fmt.Sprintf("Memory %s was not found%s.", params.ID, location)
	case "not_editable":
		text = fmt.Sprintf("Memory %s is a read-only fact%s; it cannot be edited. Read it with memory://%s.", params.ID, location, params.ID)
	default:
		text = fmt.Sprintf("Memory %s %s%s.", params.ID, res.Status, location)
	}

	details := map[string]any{"status": res.Status}
	if res.Bank != "" {
		details["bank"] = res.Bank
	}
	if res.Store != "" {
		details["store"] = res.Store
	}
	return &sdk.ToolResult{
		Content: text,
		Details: details,
	}, nil
}

func executeMemoryEditRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid memory_edit arguments: %w", err)
	}
	var params MemoryEditParams
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid memory_edit arguments: %w", err)
	}
	return ExecuteMemoryEdit(ctx, params)
}

// ExecuteLearn runs the `learn` tool directly (exported for tests).
// upstream: packages/coding-agent/src/tools/learn.ts:79-191
func ExecuteLearn(ctx sdk.Context, params LearnParams) (*sdk.ToolResult, error) {
	backend := BackendFor(ctx)
	if params.Scope != "" && params.Scope != "project" && params.Scope != "global" {
		return nil, fmt.Errorf("invalid scope %q; expected \"project\" or \"global\"", params.Scope)
	}
	if params.Scope == "global" && backend != BackendMnemopi && backend != BackendHindsight {
		return nil, fmt.Errorf("Global memory scope is not available with the %s memory backend.", backend)
	}

	agentDir := ResolveAgentDir(ctx)
	cwd := ResolveCwd(ctx)
	sid := sessionIDForContext(ctx)
	goCtx := signalContext(ctx)

	memoryMessage := "Lesson stored"
	switch backend {
	case BackendMnemopi:
		cfg := LoadMnemopiConfig(ctx)
		if params.Scope == "global" {
			if _, err := cfg.GlobalRetainBank(); err != nil {
				return nil, err
			}
		}
		var ctxVal any
		if params.Context != "" {
			ctxVal = params.Context
		}
		if _, err := MnemopiRemember(goCtx, cfg, MnemopiRememberInput{
			Content:    params.Memory,
			Source:     "coding-agent-learn",
			Importance: 0.8,
			MemoryType: "fact",
			Store:      StoreWorking,
			Veracity:   "tool",
			SessionID:  sid,
			Scope:      params.Scope,
			Metadata: map[string]any{
				"session_id": sid,
				"cwd":        cwd,
				"context":    ctxVal,
				"tool":       "learn",
			},
		}); err != nil {
			return nil, fmt.Errorf("Mnemopi did not store the lesson: %w", err)
		}

	case BackendLocal:
		res, err := SaveLearnedLesson(agentDir, cwd, LocalSaveInput{
			Content:    params.Memory,
			Context:    params.Context,
			Source:     "coding-agent-learn",
			Importance: 0.8,
		})
		if err != nil {
			return nil, err
		}
		if res.Stored == 0 {
			return nil, errors.New("Lesson was empty after sanitization; nothing stored.")
		}

	case BackendHindsight:
		cfg := LoadHindsightConfig(ctx)
		if cfg.APIURL == "" {
			return nil, errors.New("Hindsight backend is not initialised for this session.")
		}
		scope := ComputeHindsightBankScope(cfg, cwd)
		callID := toolCallIDForContext(ctx)
		if _, err := EnqueueHindsightRetain(agentDir, sid, callID, cfg, scope, []HindsightRetainInput{
			{Content: params.Memory, Context: params.Context, Scope: params.Scope},
		}); err != nil {
			return nil, err
		}
		recordSessionHindsight(sid, agentDir, cfg, scope)
		_, _, _ = FlushHindsightOutbox(goCtx, agentDir, cfg, scope)
		memoryMessage = "Lesson queued for retention"

	default:
		return nil, fmt.Errorf("learn is not available with memory.backend=%q", backend)
	}

	if params.Skill != nil {
		safeName, _ := autolearn.SanitizeSkillName(params.Skill.Name)
		if params.Skill.Action == "create" && safeName != "" && autolearn.IsAuthoredSkillCollision(ctx, safeName, agentDir) {
			return &sdk.ToolResult{
				Content: fmt.Sprintf(
					"%s. Did not create managed skill %q: an authored skill of that name already exists, and managed skills cannot override authored ones. Choose a different name.",
					memoryMessage,
					params.Skill.Name,
				),
				IsError: true,
				Details: map[string]any{"skill": nil, "shadowed": true},
			}, nil
		}
		if _, err := autolearn.WriteManagedSkill(autolearn.WriteManagedSkillInput{
			Action:      params.Skill.Action,
			Name:        params.Skill.Name,
			Description: params.Skill.Description,
			Body:        params.Skill.Body,
			AgentDir:    agentDir,
		}); err != nil {
			return nil, fmt.Errorf("%s, but the managed skill could not be written: %w", memoryMessage, err)
		}
		verb := "Updated"
		if params.Skill.Action == "create" {
			verb = "Created"
		}
		refresh := RefreshSkills
		if refresh == nil {
			refresh = autolearn.RefreshSkills
		}
		if refresh != nil {
			if err := refresh(ctx); err != nil {
				return nil, fmt.Errorf("%s. %s managed skill %q, but refreshing session skills failed: %w", memoryMessage, verb, params.Skill.Name, err)
			}
		}
		return &sdk.ToolResult{
			Content: fmt.Sprintf("%s. %s managed skill %q.", memoryMessage, verb, params.Skill.Name),
			Details: map[string]any{"skill": params.Skill.Name},
		}, nil
	}

	return &sdk.ToolResult{
		Content: memoryMessage + ".",
		Details: map[string]any{"skill": nil},
	}, nil
}

func executeLearnRaw(ctx sdk.Context, raw map[string]any) (any, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid learn arguments: %w", err)
	}
	var params LearnParams
	if err := json.Unmarshal(buf, &params); err != nil {
		return nil, fmt.Errorf("invalid learn arguments: %w", err)
	}
	return ExecuteLearn(ctx, params)
}
