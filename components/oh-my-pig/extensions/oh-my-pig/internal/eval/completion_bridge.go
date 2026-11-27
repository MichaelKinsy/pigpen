package eval

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/eval/budget-bridge.ts:1-48
// upstream: packages/coding-agent/src/eval/completion-bridge.ts:1-631
// upstream: packages/coding-agent/src/eval/judgment-bridge.ts:1-289
// upstream: packages/coding-agent/src/eval/judgment-batch-bridge.ts:1-583
// upstream: packages/coding-agent/src/eval/handle-bridge.ts:1-312

// EvalHandleConcurrency is the process-wide concurrency ceiling shared by
// completion() handles, judge(), and judge_batch() items.
// upstream: packages/coding-agent/src/eval/completion-bridge.ts:132
const EvalHandleConcurrency = 32

const (
	completionHandleRetention = 30 * time.Minute
	judgeBatchDefaultRetries  = 1
	judgeBatchDefaultIntent   = "Judging"
	judgeBatchDrainCoalesce   = 100 * time.Millisecond
)

// TurnBudgetHook returns the active token budget snapshot for ctx's session.
// Wired by wiring.go -> modes.TurnBudgetFor + session usage.
// When nil, __budget__ returns {total: nil, spent: 0, hard: false}.
// upstream: packages/coding-agent/src/eval/budget-bridge.ts:33-48
var TurnBudgetHook func(ctx sdk.Context) (total *int, spent int, hard bool)

// ResolveModelRole resolves a role or selector (e.g. "smol", "slow", "judge",
// "@judge", "default") to (provider, id, thinking, ok).
// Wired by wiring.go -> providerwire.ResolveRoleWithThinking.
var ResolveModelRole func(ctx sdk.Context, selector string) (provider, id, thinking string, ok bool)

// CompleteFn executes a one-shot completion request. Overridable in tests.
// Defaults to running sdkCtx.Complete in a goroutine with goCtx.Done() select.
var CompleteFn = defaultEvalComplete

func defaultEvalComplete(goCtx context.Context, sdkCtx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error) {
	type result struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		raw, err := sdkCtx.Complete(modelObj, req, auth)
		ch <- result{raw: raw, err: err}
	}()
	select {
	case <-goCtx.Done():
		return nil, goCtx.Err()
	case r := <-ch:
		return r.raw, r.err
	}
}

// evalRequestSlots enforces EvalHandleConcurrency across completion(), judge(),
// and judge_batch() items.
var evalRequestSlots = make(chan struct{}, EvalHandleConcurrency)

func acquireEvalSlot(ctx context.Context) error {
	select {
	case evalRequestSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseEvalSlot() {
	select {
	case <-evalRequestSlots:
	default:
	}
}

var (
	evalBridgeSeq atomic.Uint64

	completionMu      sync.Mutex
	completionHandles = make(map[string]*completionHandleEntry)

	judgeBatchMu sync.Mutex
	judgeBatches = make(map[string]*judgmentBatch)
)

func init() {
	lifecycle.OnEnd("eval-completions", func(sessionID string) {
		releaseCompletionHandles(sessionID)
		releaseJudgmentBatches(sessionID)
	})
}

// ── __budget__ ───────────────────────────────────────────────────────────────

func dispatchBudgetCall(run *bridgeRun) (any, error) {
	if fn := TurnBudgetHook; fn != nil {
		total, spent, hard := fn(run.sdkCtx)
		var totalVal any
		if total != nil {
			totalVal = *total
		}
		return map[string]any{
			"total": totalVal,
			"spent": spent,
			"hard":  hard,
		}, nil
	}
	return map[string]any{"total": nil, "spent": 0, "hard": false}, nil
}

// ── Model & response helpers ─────────────────────────────────────────────────

type resolvedModel struct {
	provider string
	id       string
	thinking string
	modelObj map[string]any
	auth     map[string]any
}

func (m resolvedModel) label() string {
	if m.provider != "" {
		return m.provider + "/" + m.id
	}
	return m.id
}

func isKnownRoleName(s string) bool {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "@")) {
	case "smol", "slow", "judge", "tiny", "default", "fast", "plan", "vision", "commit", "review", "subagent":
		return true
	default:
		return false
	}
}

func resolveEvalModel(ctx sdk.Context, selector string) (resolvedModel, error) {
	sel := strings.TrimSpace(selector)
	var provider, id, thinking string

	if sel == "" || sel == "default" {
		if info, err := ctx.GetModelInfo(); err == nil && info != nil && strings.TrimSpace(info.ID) != "" {
			provider = strings.TrimSpace(info.Provider)
			id = strings.TrimSpace(info.ID)
		} else if ResolveModelRole != nil {
			if p, m, th, ok := ResolveModelRole(ctx, "default"); ok && m != "" {
				provider, id, thinking = p, m, th
			}
		}
	} else if strings.HasPrefix(sel, "@") || isKnownRoleName(sel) {
		roleName := strings.TrimPrefix(sel, "@")
		if ResolveModelRole != nil {
			if p, m, th, ok := ResolveModelRole(ctx, roleName); ok && m != "" {
				provider, id, thinking = p, m, th
			}
		}
		if id == "" && strings.Contains(roleName, "/") {
			parts := strings.SplitN(roleName, "/", 2)
			provider, id = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		}
	} else if strings.Contains(sel, "/") {
		parts := strings.SplitN(sel, "/", 2)
		provider, id = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	} else if ResolveModelRole != nil {
		if p, m, th, ok := ResolveModelRole(ctx, sel); ok && m != "" {
			provider, id, thinking = p, m, th
		}
	}

	if id == "" {
		if sel == "" {
			sel = "default"
		}
		return resolvedModel{}, fmt.Errorf("could not resolve model %q", sel)
	}

	modelObj := map[string]any{
		"id":       id,
		"provider": provider,
	}
	var zero sdk.Context
	if ctx != zero {
		if regModel, err := ctx.ModelRegistry().Find(provider, id); err == nil && regModel != nil {
			modelObj = regModel
		}
	}

	var auth map[string]any
	if ctx != zero {
		if rawAuth, err := ctx.GetModelAuth(provider, id); err == nil && len(rawAuth) > 0 {
			_ = json.Unmarshal(rawAuth, &auth)
		}
	}

	return resolvedModel{
		provider: provider,
		id:       id,
		thinking: thinking,
		modelObj: modelObj,
		auth:     auth,
	}, nil
}

func resolveJudgeModel(ctx sdk.Context) (resolvedModel, error) {
	if ResolveModelRole != nil {
		for _, role := range []string{"judge", "smol"} {
			if p, id, th, ok := ResolveModelRole(ctx, role); ok && strings.TrimSpace(id) != "" {
				m, err := buildResolvedModel(ctx, p, id, th)
				if err == nil {
					return m, nil
				}
			}
		}
	}
	return resolveEvalModel(ctx, "default")
}

func buildResolvedModel(ctx sdk.Context, provider, id, thinking string) (resolvedModel, error) {
	provider = strings.TrimSpace(provider)
	id = strings.TrimSpace(id)
	if id == "" {
		return resolvedModel{}, errors.New("empty model id")
	}
	modelObj := map[string]any{
		"id":       id,
		"provider": provider,
	}
	var zero sdk.Context
	if ctx != zero {
		if regModel, err := ctx.ModelRegistry().Find(provider, id); err == nil && regModel != nil {
			modelObj = regModel
		}
	}
	var auth map[string]any
	if ctx != zero {
		if rawAuth, err := ctx.GetModelAuth(provider, id); err == nil && len(rawAuth) > 0 {
			_ = json.Unmarshal(rawAuth, &auth)
		}
	}
	return resolvedModel{
		provider: provider,
		id:       id,
		thinking: thinking,
		modelObj: modelObj,
		auth:     auth,
	}, nil
}

// extractEvalCompleteText parses an assistant message returned by CompleteFn.
func extractEvalCompleteText(raw json.RawMessage) (text, errMsg string) {
	if len(raw) == 0 {
		return "", "empty response from provider"
	}
	var resp struct {
		StopReason   string `json:"stopReason"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
		Content      any    `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err.Error()
	}
	if resp.Error != "" {
		return "", resp.Error
	}
	if resp.ErrorMessage != "" {
		return "", resp.ErrorMessage
	}
	if resp.StopReason == "aborted" {
		return "", "request aborted"
	}
	if resp.StopReason == "error" && resp.Content == nil {
		return "", "provider returned error"
	}
	switch c := resp.Content.(type) {
	case string:
		return strings.TrimSpace(c), ""
	case []any:
		var parts []string
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "text" {
				if t, ok := block["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "")), ""
	}
	return "", ""
}

// extractJSONPayload strips markdown code fences and extracts a JSON object/array/value.
func extractJSONPayload(text string) (any, string, error) {
	s := strings.TrimSpace(text)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	var val any
	if err := json.Unmarshal([]byte(s), &val); err == nil {
		canonical, _ := json.Marshal(val)
		return val, string(canonical), nil
	}
	// Fallback: locate first '{' ... last '}' or '[' ... last ']'.
	for _, pair := range [][2]byte{{'{', '}'}, {'[', ']'}} {
		start := strings.IndexByte(s, pair[0])
		end := strings.LastIndexByte(s, pair[1])
		if start >= 0 && end > start {
			sub := s[start : end+1]
			if err := json.Unmarshal([]byte(sub), &val); err == nil {
				canonical, _ := json.Marshal(val)
				return val, string(canonical), nil
			}
		}
	}
	return nil, "", errors.New("response did not contain valid JSON")
}

// ── __completion__ + handle-bridge integration ───────────────────────────────

type completionAttempt struct {
	Model   string  `json:"model"`
	Effort  *string `json:"effort"`
	Outcome string  `json:"outcome"`
	Error   string  `json:"error,omitempty"`
}

type completionHandleEntry struct {
	id        string
	sessionID string
	cancel    context.CancelFunc
	done      chan struct{}

	mu        sync.Mutex
	settled   bool
	cancelled bool
	text      string
	hasData   bool
	data      any
	errMsg    string
	attempts  []completionAttempt
	evict     *time.Timer
}

func (e *completionHandleEntry) snapshot(includeAttempts bool) map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]any{
		"kind": "completion",
		"id":   e.id,
	}
	if !e.settled {
		out["status"] = "running"
	} else if e.errMsg != "" {
		if e.cancelled {
			out["status"] = "cancelled"
		} else {
			out["status"] = "failed"
		}
		out["error"] = e.errMsg
	} else {
		out["status"] = "completed"
		out["text"] = e.text
		if e.hasData {
			out["data"] = e.data
		}
	}
	if includeAttempts {
		cp := make([]completionAttempt, len(e.attempts))
		copy(cp, e.attempts)
		out["attempts"] = cp
	}
	return out
}

func (e *completionHandleEntry) cancelHandle() bool {
	e.mu.Lock()
	if e.settled {
		e.mu.Unlock()
		return false
	}
	e.cancelled = true
	e.mu.Unlock()
	e.cancel()
	return true
}

func getCompletionHandle(sessionID, id string) (*completionHandleEntry, bool) {
	completionMu.Lock()
	defer completionMu.Unlock()
	e, ok := completionHandles[id]
	if !ok || e.sessionID != sessionID {
		return nil, false
	}
	return e, true
}

func releaseCompletionHandles(sessionID string) {
	completionMu.Lock()
	var toCancel []*completionHandleEntry
	for id, e := range completionHandles {
		if e.sessionID != sessionID {
			continue
		}
		delete(completionHandles, id)
		toCancel = append(toCancel, e)
	}
	completionMu.Unlock()

	for _, e := range toCancel {
		e.mu.Lock()
		if e.evict != nil {
			e.evict.Stop()
		}
		e.cancelled = true
		e.mu.Unlock()
		e.cancel()
	}
}

func dispatchCompletionCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var args struct {
		Prompt string         `json:"prompt"`
		Model  string         `json:"model"`
		System string         `json:"system"`
		Schema map[string]any `json:"schema"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, fmt.Errorf("completion() received invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return nil, errors.New("completion() received invalid arguments: prompt must be a non-empty string")
	}

	resolved, err := resolveEvalModel(run.sdkCtx, args.Model)
	if err != nil {
		return nil, fmt.Errorf("completion() %w", err)
	}

	id := fmt.Sprintf("cmp-%d-%s", evalBridgeSeq.Add(1), newRandomID())
	hCtx, cancel := context.WithCancel(context.Background())
	if sdkDone := run.sdkCtx.Done(); sdkDone != nil {
		go func() {
			select {
			case <-sdkDone:
				cancel()
			case <-hCtx.Done():
			}
		}()
	}
	entry := &completionHandleEntry{
		id:        id,
		sessionID: run.sessionID,
		cancel:    cancel,
		done:      make(chan struct{}),
	}
	completionMu.Lock()
	completionHandles[id] = entry
	completionMu.Unlock()

	completeFn := CompleteFn
	go runCompletionEntry(hCtx, run.sdkCtx, entry, resolved, args.Prompt, args.System, args.Schema, completeFn)
	return map[string]any{"id": id}, nil
}

func runCompletionEntry(
	ctx context.Context,
	sdkCtx sdk.Context,
	entry *completionHandleEntry,
	model resolvedModel,
	prompt, system string,
	schema map[string]any,
	completeFn func(goCtx context.Context, sdkCtx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error),
) {
	defer func() {
		entry.mu.Lock()
		entry.settled = true
		entry.evict = time.AfterFunc(completionHandleRetention, func() {
			completionMu.Lock()
			if completionHandles[entry.id] == entry {
				delete(completionHandles, entry.id)
			}
			completionMu.Unlock()
		})
		entry.mu.Unlock()
		close(entry.done)
	}()

	var effortPtr *string
	if model.thinking != "" {
		th := model.thinking
		effortPtr = &th
	}

	entry.mu.Lock()
	entry.attempts = append(entry.attempts, completionAttempt{
		Model:   model.label(),
		Effort:  effortPtr,
		Outcome: "running",
	})
	attemptIdx := len(entry.attempts) - 1
	entry.mu.Unlock()

	setAttempt := func(outcome, errMsg string) {
		entry.mu.Lock()
		entry.attempts[attemptIdx].Outcome = outcome
		entry.attempts[attemptIdx].Error = errMsg
		entry.mu.Unlock()
	}

	if err := acquireEvalSlot(ctx); err != nil {
		entry.mu.Lock()
		entry.cancelled = true
		entry.errMsg = "completion() request cancelled."
		entry.mu.Unlock()
		setAttempt("cancelled", "completion() request cancelled.")
		return
	}
	defer releaseEvalSlot()

	sysPrompt := strings.TrimSpace(system)
	userPrompt := prompt
	if len(schema) > 0 {
		schemaJSON, _ := json.Marshal(schema)
		instruction := "Respond ONLY with valid JSON matching this JSON Schema (no markdown fences, no commentary):\n" + string(schemaJSON)
		if sysPrompt != "" {
			sysPrompt += "\n\n" + instruction
		} else {
			sysPrompt = instruction
		}
	}

	req := map[string]any{
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": []map[string]any{{"type": "text", "text": userPrompt}},
			},
		},
	}
	if sysPrompt != "" {
		req["systemPrompt"] = sysPrompt
	}

	raw, err := completeFn(ctx, sdkCtx, model.modelObj, req, model.auth)
	if err != nil {
		msg := err.Error()
		outcome := "failed"
		if ctx.Err() != nil {
			outcome = "cancelled"
			msg = "completion() request cancelled."
			entry.mu.Lock()
			entry.cancelled = true
			entry.mu.Unlock()
		}
		entry.mu.Lock()
		entry.errMsg = msg
		entry.mu.Unlock()
		setAttempt(outcome, msg)
		return
	}

	text, errMsg := extractEvalCompleteText(raw)
	if errMsg != "" {
		entry.mu.Lock()
		entry.errMsg = errMsg
		entry.mu.Unlock()
		setAttempt("failed", errMsg)
		return
	}
	if text == "" {
		msg := "completion() returned no text output."
		if len(schema) > 0 {
			msg = "completion() returned no structured response."
		}
		entry.mu.Lock()
		entry.errMsg = msg
		entry.mu.Unlock()
		setAttempt("failed", msg)
		return
	}

	if len(schema) > 0 {
		parsed, canonical, err := extractJSONPayload(text)
		if err != nil {
			msg := "completion() did not return a structured response matching the schema."
			entry.mu.Lock()
			entry.errMsg = msg
			entry.mu.Unlock()
			setAttempt("failed", msg)
			return
		}
		entry.mu.Lock()
		entry.text = canonical
		entry.data = parsed
		entry.hasData = true
		entry.mu.Unlock()
		setAttempt("succeeded", "")
		return
	}

	entry.mu.Lock()
	entry.text = text
	entry.mu.Unlock()
	setAttempt("succeeded", "")
}

// ── __judge__ ────────────────────────────────────────────────────────────────

type judgeQuestion struct {
	Type         string // "choice", "bool", "score"
	Instructions string
	ChoiceLabels []string          // sorted keys for deterministic prompts
	ChoiceRubric map[string]string // label -> rubric ("" when nil)
	BoolTrue     string
	BoolFalse    string
	ScoreLevels  []string
}

type judgeImage struct {
	Data     string
	MimeType string
	Detail   string
}

func parseJudgeState(raw any) (any, error) {
	if raw == nil {
		return nil, errors.New("judge() received invalid arguments: state must be a string, a JSON object, or a JSON array")
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil, errors.New("judge() received invalid arguments: state must not be empty")
		}
		return v, nil
	case map[string]any, []any:
		return v, nil
	default:
		return nil, errors.New("judge() received invalid arguments: state must be a string, a JSON object, or a JSON array")
	}
}

func parseJudgeQuestions(raw any) (map[string]judgeQuestion, error) {
	qmap, ok := raw.(map[string]any)
	if !ok || len(qmap) == 0 {
		return nil, errors.New("judge() received invalid arguments: questions must be a non-empty object keyed by question id")
	}
	out := make(map[string]judgeQuestion, len(qmap))
	for id, val := range qmap {
		obj, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("judge() received invalid arguments: question %q must be an object", id)
		}
		qType, _ := obj["type"].(string)
		instr, _ := obj["instructions"].(string)
		if instr == "" {
			return nil, fmt.Errorf("judge() received invalid arguments: question %q needs non-empty string instructions", id)
		}
		switch qType {
		case "choice":
			crit, ok := obj["criteria"].(map[string]any)
			if !ok || len(crit) < 2 {
				return nil, fmt.Errorf("judge() received invalid arguments: choice question %q needs criteria with at least two options", id)
			}
			labels := make([]string, 0, len(crit))
			rubrics := make(map[string]string, len(crit))
			for label, rv := range crit {
				if rv != nil {
					s, ok := rv.(string)
					if !ok {
						return nil, fmt.Errorf("judge() received invalid arguments: choice question %q criteria %q must be a string or null", id, label)
					}
					rubrics[label] = s
				}
				labels = append(labels, label)
			}
			sort.Strings(labels)
			out[id] = judgeQuestion{
				Type:         "choice",
				Instructions: instr,
				ChoiceLabels: labels,
				ChoiceRubric: rubrics,
			}
		case "bool":
			q := judgeQuestion{
				Type:         "bool",
				Instructions: instr,
			}
			if rawCrit, exists := obj["criteria"]; exists && rawCrit != nil {
				crit, ok := rawCrit.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("judge() received invalid arguments: bool question %q criteria must be { true?: string, false?: string }", id)
				}
				if tv, ok := crit["true"]; ok && tv != nil {
					s, ok := tv.(string)
					if !ok {
						return nil, fmt.Errorf("judge() received invalid arguments: bool question %q criteria.true must be a string", id)
					}
					q.BoolTrue = s
				}
				if fv, ok := crit["false"]; ok && fv != nil {
					s, ok := fv.(string)
					if !ok {
						return nil, fmt.Errorf("judge() received invalid arguments: bool question %q criteria.false must be a string", id)
					}
					q.BoolFalse = s
				}
			}
			out[id] = q
		case "score":
			rawLevels, ok := obj["criteria"].([]any)
			if !ok || len(rawLevels) < 2 {
				return nil, fmt.Errorf("judge() received invalid arguments: score question %q needs criteria with at least two levels", id)
			}
			levels := make([]string, len(rawLevels))
			for i, lv := range rawLevels {
				s, ok := lv.(string)
				if !ok {
					return nil, fmt.Errorf("judge() received invalid arguments: score question %q criteria levels must be strings", id)
				}
				levels[i] = s
			}
			out[id] = judgeQuestion{
				Type:         "score",
				Instructions: instr,
				ScoreLevels:  levels,
			}
		default:
			return nil, fmt.Errorf("judge() received invalid arguments: question %q type must be \"choice\", \"bool\", or \"score\"", id)
		}
	}
	return out, nil
}

func loadJudgeImages(raw any, cwd string) ([]judgeImage, error) {
	if raw == nil {
		return nil, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, errors.New("invalid judgment image source: images must be an array of paths or image attachments")
	}
	var out []judgeImage
	for idx, src := range arr {
		switch v := src.(type) {
		case string:
			if v == "" {
				return nil, fmt.Errorf("invalid judgment image source: images[%d] path must not be empty", idx)
			}
			p := v
			if !filepath.IsAbs(p) && cwd != "" {
				p = filepath.Join(cwd, p)
			}
			const maxJudgeImageBytes = 20 * 1024 * 1024 // 20 MiB cap
			st, err := os.Stat(p)
			if err != nil {
				return nil, fmt.Errorf("invalid judgment image source: images[%d] path %q could not be loaded: %w", idx, v, err)
			}
			if st.IsDir() || st.Size() > maxJudgeImageBytes {
				return nil, fmt.Errorf("invalid judgment image source: images[%d] path %q is not a valid image file", idx, v)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("invalid judgment image source: images[%d] path %q could not be loaded: %w", idx, v, err)
			}
			mt := detectJudgeImageMIME(p, data)
			if mt == "" {
				return nil, fmt.Errorf("invalid judgment image source: images[%d] path %q is not a supported image format", idx, v)
			}
			out = append(out, judgeImage{
				Data:     base64.StdEncoding.EncodeToString(data),
				MimeType: mt,
			})
		case map[string]any:
			typ, _ := v["type"].(string)
			data, _ := v["data"].(string)
			mt, _ := v["mimeType"].(string)
			detail, _ := v["detail"].(string)
			if typ != "image" || data == "" || !strings.HasPrefix(mt, "image/") {
				return nil, fmt.Errorf("invalid judgment image source: images[%d] must be a local path or { type: \"image\", data, mimeType }", idx)
			}
			out = append(out, judgeImage{
				Data:     data,
				MimeType: mt,
				Detail:   detail,
			})
		default:
			return nil, fmt.Errorf("invalid judgment image source: images[%d] must be a local path or image attachment", idx)
		}
	}
	return out, nil
}

func detectJudgeImageMIME(path string, data []byte) string {
	// Check image magic bytes first (PNG, JPEG, GIF, WebP).
	switch {
	case len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G':
		return "image/png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if strings.HasPrefix(mt, "image/") {
		if semi := strings.IndexByte(mt, ';'); semi > 0 {
			mt = strings.TrimSpace(mt[:semi])
		}
		return mt
	}
	return ""
}

func buildJudgePrompt(state any, questions map[string]judgeQuestion) (systemPrompt, userPrompt string) {
	systemPrompt = "You are a precise structured evaluator. Evaluate the provided state against each question independently and respond ONLY with a single JSON object mapping each question id to its answer. Do not include markdown fences or extra text.\n" +
		"Answer formats by question type:\n" +
		"- choice: the exact chosen option label string (must match one of the listed options).\n" +
		"- bool: a boolean (true or false) or probability number between 0 and 1.\n" +
		"- score: a number between 0 and (levels.length - 1) representing the level index (0 = lowest)."

	var b strings.Builder
	b.WriteString("## State\n")
	if s, ok := state.(string); ok {
		b.WriteString(s)
	} else {
		enc, _ := json.MarshalIndent(state, "", "  ")
		b.Write(enc)
	}
	b.WriteString("\n\n## Questions\n")

	ids := make([]string, 0, len(questions))
	for id := range questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		q := questions[id]
		fmt.Fprintf(&b, "- %q (type=%s): %s\n", id, q.Type, q.Instructions)
		switch q.Type {
		case "choice":
			b.WriteString("  Options:\n")
			for _, label := range q.ChoiceLabels {
				if rubric := q.ChoiceRubric[label]; rubric != "" {
					fmt.Fprintf(&b, "    - %q: %s\n", label, rubric)
				} else {
					fmt.Fprintf(&b, "    - %q\n", label)
				}
			}
		case "bool":
			if q.BoolTrue != "" {
				fmt.Fprintf(&b, "  true: %s\n", q.BoolTrue)
			}
			if q.BoolFalse != "" {
				fmt.Fprintf(&b, "  false: %s\n", q.BoolFalse)
			}
		case "score":
			b.WriteString("  Levels (0 = lowest):\n")
			for i, lvl := range q.ScoreLevels {
				fmt.Fprintf(&b, "    - %d: %s\n", i, lvl)
			}
		}
	}
	b.WriteString("\nRespond with JSON object mapping each question id to its answer.")
	return systemPrompt, b.String()
}

func parseJudgeAnswers(rawText string, questions map[string]judgeQuestion) (map[string]any, error) {
	val, _, err := extractJSONPayload(rawText)
	if err != nil {
		return nil, fmt.Errorf("judge() failed to parse JSON response: %w", err)
	}
	obj, ok := val.(map[string]any)
	if !ok {
		return nil, errors.New("judge() expected a JSON object of answers")
	}
	// Unwrap {"answers": {...}} if the model nested them.
	if inner, ok := obj["answers"].(map[string]any); ok {
		allPresent := true
		for id := range questions {
			if _, exists := obj[id]; exists {
				allPresent = false
				break
			}
		}
		if allPresent {
			obj = inner
		}
	}

	answers := make(map[string]any, len(questions))
	for id, q := range questions {
		rawAns, exists := obj[id]
		if !exists {
			return nil, fmt.Errorf("judge() response missing answer for question %q", id)
		}
		ans, err := coerceCellAnswer(id, q, rawAns)
		if err != nil {
			return nil, err
		}
		answers[id] = ans
	}
	return answers, nil
}

func coerceCellAnswer(id string, q judgeQuestion, raw any) (map[string]any, error) {
	switch q.Type {
	case "choice":
		var chosen string
		switch v := raw.(type) {
		case string:
			chosen = v
		case map[string]any:
			if s, ok := v["choice"].(string); ok {
				chosen = s
			}
		}
		if chosen == "" {
			return nil, fmt.Errorf("judge() choice answer for %q must be a string option", id)
		}
		// Match exact or case-insensitive label.
		matched := ""
		for _, lbl := range q.ChoiceLabels {
			if lbl == chosen {
				matched = lbl
				break
			}
		}
		if matched == "" {
			for _, lbl := range q.ChoiceLabels {
				if strings.EqualFold(lbl, strings.TrimSpace(chosen)) {
					matched = lbl
					break
				}
			}
		}
		if matched == "" {
			return nil, fmt.Errorf("judge() choice answer %q for %q is not in %v", chosen, id, q.ChoiceLabels)
		}
		probs := make(map[string]float64, len(q.ChoiceLabels))
		for _, lbl := range q.ChoiceLabels {
			if lbl == matched {
				probs[lbl] = 1
			} else {
				probs[lbl] = 0
			}
		}
		return map[string]any{
			"type":          "choice",
			"choice":        matched,
			"probabilities": probs,
			"confidence":    1.0,
		}, nil

	case "bool":
		var prob float64
		switch v := raw.(type) {
		case bool:
			if v {
				prob = 1
			} else {
				prob = 0
			}
		case float64:
			prob = clamp01(v)
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "yes", "1":
				prob = 1
			case "false", "no", "0":
				prob = 0
			default:
				f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil {
					return nil, fmt.Errorf("judge() bool answer for %q must be bool or 0..1 number", id)
				}
				prob = clamp01(f)
			}
		case map[string]any:
			if b, ok := v["bool"].(bool); ok {
				if b {
					prob = 1
				}
			} else if f, ok := v["bool"].(float64); ok {
				prob = clamp01(f)
			} else if f, ok := v["noul"].(float64); ok {
				prob = clamp01(f)
			} else {
				return nil, fmt.Errorf("judge() bool answer for %q missing bool value", id)
			}
		default:
			return nil, fmt.Errorf("judge() bool answer for %q must be bool or 0..1 number", id)
		}
		return map[string]any{
			"type": "bool",
			"bool": prob,
		}, nil

	case "score":
		var score float64
		switch v := raw.(type) {
		case float64:
			score = v
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("judge() score answer for %q must be a number", id)
			}
			score = f
		case map[string]any:
			if f, ok := v["score"].(float64); ok {
				score = f
			} else {
				return nil, fmt.Errorf("judge() score answer for %q missing score number", id)
			}
		default:
			return nil, fmt.Errorf("judge() score answer for %q must be a number", id)
		}
		maxIdx := float64(len(q.ScoreLevels) - 1)
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return nil, fmt.Errorf("judge() score answer for %q must be finite", id)
		}
		if score < 0 {
			score = 0
		} else if score > maxIdx {
			score = maxIdx
		}
		rounded := int(math.Round(score))
		probs := make(map[string]float64, len(q.ScoreLevels))
		for i := range q.ScoreLevels {
			if i == rounded {
				probs[strconv.Itoa(i)] = 1
			} else {
				probs[strconv.Itoa(i)] = 0
			}
		}
		return map[string]any{
			"type":          "score",
			"score":         score,
			"probabilities": probs,
			"confidence":    1.0,
		}, nil
	}
	return nil, fmt.Errorf("unsupported question type %q", q.Type)
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func executeJudgeOnce(
	ctx context.Context,
	sdkCtx sdk.Context,
	model resolvedModel,
	state any,
	questions map[string]judgeQuestion,
	images []judgeImage,
	completeFn func(goCtx context.Context, sdkCtx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error),
) (map[string]any, string, error) {
	sysPrompt, userPrompt := buildJudgePrompt(state, questions)
	content := make([]map[string]any, 0, 1+len(images))
	content = append(content, map[string]any{"type": "text", "text": userPrompt})
	for _, img := range images {
		block := map[string]any{
			"type":     "image",
			"data":     img.Data,
			"mimeType": img.MimeType,
		}
		if img.Detail != "" {
			block["detail"] = img.Detail
		}
		content = append(content, block)
	}
	req := map[string]any{
		"systemPrompt": sysPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": content},
		},
	}

	raw, err := completeFn(ctx, sdkCtx, model.modelObj, req, model.auth)
	if err != nil {
		return nil, "", err
	}
	text, errMsg := extractEvalCompleteText(raw)
	if errMsg != "" {
		return nil, "", errors.New(errMsg)
	}
	answers, err := parseJudgeAnswers(text, questions)
	if err != nil {
		return nil, "", err
	}
	return answers, model.label(), nil
}

func dispatchJudgeCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var rawMap map[string]any
	if err := json.Unmarshal(rawArgs, &rawMap); err != nil || rawMap == nil {
		return nil, errors.New("judge() received invalid arguments: expected { state, questions }")
	}
	state, err := parseJudgeState(rawMap["state"])
	if err != nil {
		return nil, err
	}
	questions, err := parseJudgeQuestions(rawMap["questions"])
	if err != nil {
		return nil, err
	}
	images, err := loadJudgeImages(rawMap["images"], run.cwd)
	if err != nil {
		return nil, err
	}
	model, err := resolveJudgeModel(run.sdkCtx)
	if err != nil {
		return nil, fmt.Errorf("judge() %w", err)
	}

	completeFn := CompleteFn
	return withBridgeTimeoutPause(run.emitStatus, func() (any, error) {
		if err := acquireEvalSlot(run.ctx); err != nil {
			return nil, errors.New("judge() cancelled")
		}
		defer releaseEvalSlot()

		answers, modelLabel, err := executeJudgeOnce(run.ctx, run.sdkCtx, model, state, questions, images, completeFn)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"answers": answers,
			"model":   modelLabel,
		}, nil
	})
}

// ── __judge_batch__ ──────────────────────────────────────────────────────────

type judgmentBatchInput struct {
	Key              any
	State            any
	ImageSources     any
	ImageSourceError string
}

type judgmentBatchItem struct {
	Key     any            `json:"key"`
	Answers map[string]any `json:"answers,omitempty"`
	Error   string         `json:"error,omitempty"`
	Model   string         `json:"model,omitempty"`
}

type judgmentBatch struct {
	id         string
	sessionID  string
	intent     string
	total      int
	inputs     []judgmentBatchInput
	questions  map[string]judgeQuestion
	cwd        string
	sdkCtx     sdk.Context
	completeFn func(goCtx context.Context, sdkCtx sdk.Context, modelObj, req, auth map[string]any) (json.RawMessage, error)

	concurrency int
	retries     int
	minOk       int

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	startedAt time.Time
	settled   []judgmentBatchItem
	cursor    int
	failed    int
	model     string
	running   bool
	errMsg    string
	notify    chan struct{} // closed & replaced whenever a new item settles
}

func newJudgmentBatch(
	sessionID string,
	sdkCtx sdk.Context,
	cwd string,
	inputs []judgmentBatchInput,
	questions map[string]judgeQuestion,
	concurrency, retries, minOk int,
	intent string,
) *judgmentBatch {
	ctx, cancel := context.WithCancel(context.Background())
	return &judgmentBatch{
		id:          fmt.Sprintf("jdgb-%d-%s", evalBridgeSeq.Add(1), newRandomID()),
		sessionID:   sessionID,
		intent:      intent,
		total:       len(inputs),
		inputs:      inputs,
		questions:   questions,
		cwd:         cwd,
		sdkCtx:      sdkCtx,
		completeFn:  CompleteFn,
		concurrency: concurrency,
		retries:     retries,
		minOk:       minOk,
		ctx:         ctx,
		cancel:      cancel,
		done:        make(chan struct{}),
		startedAt:   time.Now(),
		running:     true,
		notify:      make(chan struct{}),
	}
}

func (b *judgmentBatch) start() {
	go b.run()
}

func (b *judgmentBatch) settleLocked(item judgmentBatchItem) {
	b.settled = append(b.settled, item)
	if item.Error != "" {
		b.failed++
	}
	if item.Model != "" {
		b.model = item.Model
	}
	close(b.notify)
	b.notify = make(chan struct{})
}

func (b *judgmentBatch) run() {
	defer func() {
		b.mu.Lock()
		b.running = false
		close(b.notify)
		b.notify = make(chan struct{})
		b.mu.Unlock()
		close(b.done)
	}()

	model, err := resolveJudgeModel(b.sdkCtx)
	if err != nil {
		msg := err.Error()
		b.mu.Lock()
		for _, inp := range b.inputs {
			b.settleLocked(judgmentBatchItem{Key: inp.Key, Error: msg})
		}
		b.errMsg = msg
		b.mu.Unlock()
		return
	}

	var nextIdx atomic.Int64
	workers := min(b.concurrency, b.total)
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				idx := int(nextIdx.Add(1) - 1)
				if idx >= len(b.inputs) {
					return
				}
				inp := b.inputs[idx]
				item := b.judgeItem(model, inp)
				b.mu.Lock()
				b.settleLocked(item)
				b.mu.Unlock()
			}
		}()
	}
	wg.Wait()

	b.mu.Lock()
	okCount := len(b.settled) - b.failed
	if b.ctx.Err() != nil {
		b.errMsg = "judge_batch cancelled"
	} else if okCount < b.minOk {
		lastErr := "unknown"
		for i := len(b.settled) - 1; i >= 0; i-- {
			if e := b.settled[i].Error; e != "" && e != "cancelled" {
				lastErr = e
				break
			}
		}
		b.errMsg = fmt.Sprintf("judge_batch: only %d/%d item(s) judged (min_ok=%d); last error: %s", okCount, b.total, b.minOk, lastErr)
	}
	b.mu.Unlock()
}

func (b *judgmentBatch) judgeItem(model resolvedModel, inp judgmentBatchInput) judgmentBatchItem {
	if b.ctx.Err() != nil {
		return judgmentBatchItem{Key: inp.Key, Error: "cancelled"}
	}
	if inp.ImageSourceError != "" {
		return judgmentBatchItem{Key: inp.Key, Error: inp.ImageSourceError}
	}
	if err := acquireEvalSlot(b.ctx); err != nil {
		return judgmentBatchItem{Key: inp.Key, Error: "cancelled"}
	}
	defer releaseEvalSlot()

	if b.ctx.Err() != nil {
		return judgmentBatchItem{Key: inp.Key, Error: "cancelled"}
	}
	images, err := loadJudgeImages(inp.ImageSources, b.cwd)
	if err != nil {
		return judgmentBatchItem{Key: inp.Key, Error: err.Error()}
	}

	var lastErr error
	for attempt := 0; attempt <= b.retries; attempt++ {
		if b.ctx.Err() != nil {
			return judgmentBatchItem{Key: inp.Key, Error: "cancelled"}
		}
		answers, modelLabel, err := executeJudgeOnce(b.ctx, b.sdkCtx, model, inp.State, b.questions, images, b.completeFn)
		if err == nil {
			return judgmentBatchItem{Key: inp.Key, Answers: answers, Model: modelLabel}
		}
		lastErr = err
	}
	if b.ctx.Err() != nil {
		return judgmentBatchItem{Key: inp.Key, Error: "cancelled"}
	}
	msg := "judgment failed"
	if lastErr != nil {
		msg = lastErr.Error()
	}
	return judgmentBatchItem{Key: inp.Key, Error: msg}
}

func (b *judgmentBatch) status() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	elapsedS := math.Round(time.Since(b.startedAt).Seconds()*10) / 10
	out := map[string]any{
		"id":       b.id,
		"intent":   b.intent,
		"total":    b.total,
		"done":     len(b.settled),
		"failed":   b.failed,
		"cost":     0.0,
		"running":  b.running,
		"elapsedS": elapsedS,
	}
	if b.model != "" {
		out["model"] = b.model
	}
	if b.errMsg != "" {
		out["error"] = b.errMsg
	}
	return out
}

func (b *judgmentBatch) drain(ctx context.Context, timeoutMs *float64) ([]judgmentBatchItem, error) {
	var deadline time.Time
	hasDeadline := timeoutMs != nil
	if hasDeadline {
		deadline = time.Now().Add(time.Duration(*timeoutMs * float64(time.Millisecond)))
	}

	b.mu.Lock()
	if b.cursor == len(b.settled) && b.running {
		notifyCh := b.notify
		b.mu.Unlock()

		var timer *time.Timer
		var timerCh <-chan time.Time
		if hasDeadline {
			rem := time.Until(deadline)
			if rem <= 0 {
				return []judgmentBatchItem{}, nil
			}
			timer = time.NewTimer(rem)
			timerCh = timer.C
		}

		select {
		case <-notifyCh:
		case <-b.done:
		case <-timerCh:
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil, errors.New("judge_batch drain aborted")
		}
		if timer != nil {
			timer.Stop()
		}

		// Brief coalesce window matching upstream DRAIN_COALESCE_MS.
		b.mu.Lock()
		stillRunning := b.running && b.cursor < len(b.settled)
		b.mu.Unlock()
		if stillRunning {
			linger := judgeBatchDrainCoalesce
			if hasDeadline {
				if rem := time.Until(deadline); rem < linger {
					linger = rem
				}
			}
			if linger > 0 {
				t := time.NewTimer(linger)
				select {
				case <-b.done:
				case <-t.C:
				case <-ctx.Done():
					t.Stop()
					return nil, errors.New("judge_batch drain aborted")
				}
				t.Stop()
			}
		}
		b.mu.Lock()
	}

	if b.cursor < len(b.settled) {
		items := append([]judgmentBatchItem(nil), b.settled[b.cursor:]...)
		b.cursor = len(b.settled)
		b.mu.Unlock()
		return items, nil
	}
	errMsg := b.errMsg
	running := b.running
	b.mu.Unlock()

	if !running && errMsg != "" {
		return nil, errors.New(errMsg)
	}
	return []judgmentBatchItem{}, nil
}

func (b *judgmentBatch) results() map[string]map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]map[string]any)
	for _, item := range b.settled {
		if item.Answers != nil {
			out[batchKeyString(item.Key)] = item.Answers
		}
	}
	return out
}

func (b *judgmentBatch) failedMap() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]string)
	for _, item := range b.settled {
		if item.Error != "" {
			out[batchKeyString(item.Key)] = item.Error
		}
	}
	return out
}

func (b *judgmentBatch) cancelBatch() bool {
	b.mu.Lock()
	running := b.running
	b.mu.Unlock()
	if !running || b.ctx.Err() != nil {
		return false
	}
	b.cancel()
	return true
}

func batchKeyString(k any) string {
	switch v := k.(type) {
	case string:
		return v
	case float64:
		if v == math.Trunc(v) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	default:
		return fmt.Sprint(v)
	}
}

func getJudgmentBatch(sessionID, id string) (*judgmentBatch, bool) {
	judgeBatchMu.Lock()
	defer judgeBatchMu.Unlock()
	b, ok := judgeBatches[id]
	if !ok || b.sessionID != sessionID {
		return nil, false
	}
	return b, true
}

func releaseJudgmentBatches(sessionID string) {
	judgeBatchMu.Lock()
	var toCancel []*judgmentBatch
	for id, b := range judgeBatches {
		if b.sessionID != sessionID {
			continue
		}
		delete(judgeBatches, id)
		toCancel = append(toCancel, b)
	}
	judgeBatchMu.Unlock()

	for _, b := range toCancel {
		b.cancelBatch()
	}
}

func parseBatchItems(raw any) ([]judgmentBatchInput, error) {
	arr, ok := raw.([]any)
	if !ok || len(arr) == 0 {
		return nil, errors.New("judge_batch() received invalid arguments: items must be a non-empty array of { key, state }")
	}
	seen := make(map[string]struct{}, len(arr))
	out := make([]judgmentBatchInput, 0, len(arr))
	for i, entry := range arr {
		obj, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("judge_batch() received invalid arguments: item %d must be an object", i)
		}
		key, exists := obj["key"]
		if !exists || key == nil {
			return nil, fmt.Errorf("judge_batch() received invalid arguments: item %d needs a string or number key", i)
		}
		switch k := key.(type) {
		case string:
			if k == "" {
				return nil, fmt.Errorf("judge_batch() received invalid arguments: item %d key must not be empty", i)
			}
		case float64:
			if math.IsNaN(k) || math.IsInf(k, 0) {
				return nil, fmt.Errorf("judge_batch() received invalid arguments: item %d key must be finite", i)
			}
		default:
			return nil, fmt.Errorf("judge_batch() received invalid arguments: item %d needs a string or number key", i)
		}
		ks := batchKeyString(key)
		if _, dup := seen[ks]; dup {
			return nil, fmt.Errorf("judge_batch() received invalid arguments: duplicate item key %q", ks)
		}
		seen[ks] = struct{}{}

		st, err := parseJudgeState(obj["state"])
		if err != nil {
			return nil, err
		}
		inp := judgmentBatchInput{Key: key, State: st}
		if imgs, hasImgs := obj["images"]; hasImgs && imgs != nil {
			if _, ok := imgs.([]any); ok {
				inp.ImageSources = imgs
			} else {
				inp.ImageSourceError = fmt.Sprintf("images for input %q must be an array of paths or image attachments", ks)
			}
		}
		out = append(out, inp)
	}
	return out, nil
}

func parseNonNegInt(raw any, name string, fallback, maxVal int) (int, error) {
	if raw == nil {
		return fallback, nil
	}
	f, ok := raw.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 {
		return 0, fmt.Errorf("judge_batch() received invalid arguments: %s must be a non-negative integer", name)
	}
	n := int(f)
	if maxVal > 0 && n > maxVal {
		n = maxVal
	}
	return n, nil
}

func dispatchJudgeBatchCall(run *bridgeRun, rawArgs json.RawMessage) (any, error) {
	var args map[string]any
	if err := json.Unmarshal(rawArgs, &args); err != nil || args == nil {
		return nil, errors.New("judge_batch() received invalid arguments: expected { op, ... }")
	}
	op, _ := args["op"].(string)
	if op == "" {
		return nil, errors.New("judge_batch() received invalid arguments: expected { op, ... }")
	}

	requireBatch := func() (*judgmentBatch, error) {
		id, _ := args["id"].(string)
		if id == "" {
			return nil, errors.New("judge_batch() received invalid arguments: op requires a batch id")
		}
		b, ok := getJudgmentBatch(run.sessionID, id)
		if !ok {
			return nil, fmt.Errorf("unknown judge_batch %q", id)
		}
		return b, nil
	}

	switch op {
	case "create":
		if _, hasTopImages := args["images"]; hasTopImages {
			return nil, errors.New("judge_batch() received invalid arguments: images must be provided on each item")
		}
		items, err := parseBatchItems(args["items"])
		if err != nil {
			return nil, err
		}
		questions, err := parseJudgeQuestions(args["questions"])
		if err != nil {
			return nil, err
		}
		concurrency, err := parseNonNegInt(args["concurrency"], "concurrency", EvalHandleConcurrency, EvalHandleConcurrency)
		if err != nil {
			return nil, err
		}
		if concurrency <= 0 {
			concurrency = EvalHandleConcurrency
		}
		retries, err := parseNonNegInt(args["retries"], "retries", judgeBatchDefaultRetries, 0)
		if err != nil {
			return nil, err
		}
		minOk, err := parseNonNegInt(args["minOk"], "minOk", 1, len(items))
		if err != nil {
			return nil, err
		}
		intent := judgeBatchDefaultIntent
		if rawIntent, exists := args["intent"]; exists && rawIntent != nil {
			s, ok := rawIntent.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, errors.New("judge_batch() received invalid arguments: intent must be a non-empty string")
			}
			intent = s
		}

		batch := newJudgmentBatch(run.sessionID, run.sdkCtx, run.cwd, items, questions, concurrency, retries, minOk, intent)
		judgeBatchMu.Lock()
		judgeBatches[batch.id] = batch
		judgeBatchMu.Unlock()
		batch.start()
		return batch.status(), nil

	case "attach", "status":
		b, err := requireBatch()
		if err != nil {
			return nil, err
		}
		return b.status(), nil

	case "drain":
		b, err := requireBatch()
		if err != nil {
			return nil, err
		}
		var timeoutMs *float64
		if rawT, exists := args["timeoutMs"]; exists && rawT != nil {
			f, ok := rawT.(float64)
			if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
				return nil, errors.New("judge_batch() received invalid arguments: timeoutMs must be a non-negative finite number")
			}
			timeoutMs = &f
		}
		return withBridgeTimeoutPause(run.emitStatus, func() (any, error) {
			items, err := b.drain(run.ctx, timeoutMs)
			if err != nil {
				return nil, err
			}
			return map[string]any{"items": items}, nil
		})

	case "results":
		b, err := requireBatch()
		if err != nil {
			return nil, err
		}
		return map[string]any{"results": b.results()}, nil

	case "failed":
		b, err := requireBatch()
		if err != nil {
			return nil, err
		}
		return map[string]any{"failed": b.failedMap()}, nil

	case "cancel":
		b, err := requireBatch()
		if err != nil {
			return nil, err
		}
		return map[string]any{"cancelled": b.cancelBatch()}, nil

	case "close":
		b, err := requireBatch()
		if err != nil {
			return nil, err
		}
		b.cancelBatch()
		judgeBatchMu.Lock()
		delete(judgeBatches, b.id)
		judgeBatchMu.Unlock()
		return map[string]any{"closed": true}, nil

	default:
		return nil, fmt.Errorf("judge_batch() received invalid arguments: unknown op %q", op)
	}
}
