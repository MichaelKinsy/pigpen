package vibe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
)

// upstream: packages/coding-agent/src/tools/vibe.ts:1-251

// ToolNames lists the five ephemeral Vibe director tools in upstream declaration order.
// upstream: packages/coding-agent/src/tools/vibe.ts:29
var ToolNames = []string{
	"vibe_spawn",
	"vibe_send",
	"vibe_wait",
	"vibe_kill",
	"vibe_list",
}

const (
	// killedIDsListed caps the trailing killed-session id list in vibe_list.
	// upstream: packages/coding-agent/src/tools/vibe.ts:56
	killedIDsListed = 8
	// waitProgressInterval is the interim TV-wall tick interval during vibe_wait.
	// upstream: packages/coding-agent/src/tools/vibe.ts:126
	waitProgressInterval = 500 * time.Millisecond
)

// vibeSpawnSchema matches upstream `vibeSpawnSchema` (`tools/vibe.ts:31-37`).
func vibeSpawnSchema() sdk.Schema {
	props := sdk.NewOrderedObject(
		"cli", sdk.NewOrderedObject(
			"enum", []string{"fast", "good"},
			"description", "worker flavor: fast = low-latency model for mechanical work; good = strong model for hard work",
		),
		"name", sdk.NewOrderedObject(
			"type", "string",
			"maxLength", 48,
			"description", "optional session name; generated when omitted",
		),
		"prompt", sdk.NewOrderedObject(
			"type", "string",
			"minLength", 1,
			"description", "first instruction; the worker starts with no other context",
		),
	)
	return sdk.Schema{
		"~order":               []string{"type", "properties", "required", "additionalProperties"},
		"type":                 "object",
		"properties":           props,
		"required":             []string{"cli", "prompt"},
		"additionalProperties": false,
	}
}

// vibeSendSchema matches upstream `vibeSendSchema` (`tools/vibe.ts:39-42`).
func vibeSendSchema() sdk.Schema {
	props := sdk.NewOrderedObject(
		"session", sdk.NewOrderedObject(
			"type", "string",
			"minLength", 1,
			"description", "session id from vibe_spawn / vibe_list",
		),
		"message", sdk.NewOrderedObject(
			"type", "string",
			"minLength", 1,
			"description", "message for the session; steers mid-turn, else runs as its next turn",
		),
	)
	return sdk.Schema{
		"~order":               []string{"type", "properties", "required", "additionalProperties"},
		"type":                 "object",
		"properties":           props,
		"required":             []string{"session", "message"},
		"additionalProperties": false,
	}
}

// vibeWaitSchema matches upstream `vibeWaitSchema` (`tools/vibe.ts:44-47`).
func vibeWaitSchema() sdk.Schema {
	props := sdk.NewOrderedObject(
		"sessions", sdk.NewOrderedObject(
			"type", "array",
			"items", map[string]any{"type": "string"},
			"description", "session ids to watch; omit to watch every session with a turn in flight",
		),
		"timeout", sdk.NewOrderedObject(
			"type", "number",
			"exclusiveMinimum", 0,
			"description", "max seconds to wait (default 30)",
		),
	)
	return sdk.Schema{
		"~order":               []string{"type", "properties", "additionalProperties"},
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
}

// vibeKillSchema matches upstream `vibeKillSchema` (`tools/vibe.ts:49-51`).
func vibeKillSchema() sdk.Schema {
	props := sdk.NewOrderedObject(
		"session", sdk.NewOrderedObject(
			"type", "string",
			"minLength", 1,
			"description", "session id to terminate",
		),
	)
	return sdk.Schema{
		"~order":               []string{"type", "properties", "required", "additionalProperties"},
		"type":                 "object",
		"properties":           props,
		"required":             []string{"session"},
		"additionalProperties": false,
	}
}

// vibeListSchema matches upstream `vibeListSchema` (`tools/vibe.ts:53`).
func vibeListSchema() sdk.Schema {
	return sdk.Schema{
		"~order":               []string{"type", "properties", "additionalProperties"},
		"type":                 "object",
		"properties":           sdk.NewOrderedObject(),
		"additionalProperties": false,
	}
}

// wallOf computes the TV-wall screen snapshot matching upstream `wallOf`
// (`packages/coding-agent/src/tools/vibe.ts:65-79`).
func wallOf(reg *SessionRegistry, parent *ParentSession, ids []string, keep map[string]bool) (screens []ScreenSnapshot, hiddenKilled []string) {
	all, err := reg.Screens(parent, ids)
	if err != nil {
		return []ScreenSnapshot{}, nil
	}
	if len(ids) > 0 {
		if all == nil {
			all = []ScreenSnapshot{}
		}
		return all, nil
	}
	screens = make([]ScreenSnapshot, 0, len(all))
	for _, sc := range all {
		if sc.Killed && !keep[sc.ID] {
			hiddenKilled = append(hiddenKilled, sc.ID)
		} else {
			screens = append(screens, sc)
		}
	}
	return screens, hiddenKilled
}

// ExecuteSpawn implements `vibe_spawn`.
// upstream: packages/coding-agent/src/tools/vibe.ts:86-103
func ExecuteSpawn(reg *SessionRegistry, parent *ParentSession, params map[string]any) (sdk.ToolResult, error) {
	rawCLI, _ := params["cli"].(string)
	name, _ := params["name"].(string)
	prompt, _ := params["prompt"].(string)

	outcome, err := reg.Spawn(parent, SpawnParams{
		CLI:    CLI(rawCLI),
		Name:   name,
		Prompt: prompt,
	})
	if err != nil {
		return sdk.ToolResult{}, err
	}
	screens, hiddenKilled := wallOf(reg, parent, nil, nil)
	text := fmt.Sprintf(
		"Spawned %s session `%s` (turn job `%s`). The turn result will be delivered when it finishes — keep directing other sessions meanwhile. Continue this one with vibe_send `%s`.",
		rawCLI, outcome.ID, outcome.JobID, outcome.ID,
	)
	return sdk.ToolResult{
		Content: text,
		Details: ToolDetails{
			Op:           OpSpawn,
			Screens:      screens,
			HiddenKilled: hiddenKilled,
			Spawned: &SpawnedDetail{
				ID:    outcome.ID,
				CLI:   CLI(rawCLI),
				JobID: outcome.JobID,
			},
		},
	}, nil
}

// ExecuteSend implements `vibe_send`.
// upstream: packages/coding-agent/src/tools/vibe.ts:106-124
func ExecuteSend(reg *SessionRegistry, parent *ParentSession, params map[string]any) (sdk.ToolResult, error) {
	sessionID, _ := params["session"].(string)
	message, _ := params["message"].(string)

	outcome, err := reg.Send(parent, SendParams{
		Session: sessionID,
		Message: message,
	})
	if err != nil {
		return sdk.ToolResult{}, err
	}
	var ack string
	switch outcome.Mode {
	case SendModeTurn:
		ack = fmt.Sprintf("Started a new turn on `%s` (job `%s`). Its result will be delivered when the turn finishes.", outcome.ID, outcome.JobID)
	case SendModeSteered:
		ack = fmt.Sprintf("Steered `%s` mid-turn — the running turn sees your message at its next step.", outcome.ID)
	default:
		ack = fmt.Sprintf("`%s` is mid-turn; your message is queued and runs automatically as the next turn.", outcome.ID)
	}
	screens, hiddenKilled := wallOf(reg, parent, nil, nil)
	oc := outcome
	return sdk.ToolResult{
		Content: ack,
		Details: ToolDetails{
			Op:           OpSend,
			Screens:      screens,
			HiddenKilled: hiddenKilled,
			Send:         &oc,
		},
	}, nil
}

// ExecuteWait implements `vibe_wait` with live TV-wall progress ticks.
// upstream: packages/coding-agent/src/tools/vibe.ts:128-192
func ExecuteWait(
	ctx context.Context,
	reg *SessionRegistry,
	parent *ParentSession,
	params map[string]any,
	onUpdate func(sdk.ToolResult),
) (sdk.ToolResult, error) {
	sessions := extractStringSliceParam(params["sessions"])
	var timeout time.Duration
	hasTimeout := false
	if rawTimeout, ok := params["timeout"]; ok && rawTimeout != nil {
		if secs, ok := numberParam(rawTimeout); ok && secs > 0 {
			timeout = time.Duration(secs * float64(time.Second))
			hasTimeout = true
		}
	}

	initialScreens, _ := wallOf(reg, parent, sessions, nil)
	shown := make(map[string]bool, len(initialScreens))
	for _, sc := range initialScreens {
		shown[sc.ID] = true
	}

	emitProgress := func() {
		if onUpdate == nil {
			return
		}
		screens, hiddenKilled := wallOf(reg, parent, sessions, shown)
		onUpdate(sdk.ToolResult{
			Content: "",
			Details: ToolDetails{
				Op:           OpWait,
				Screens:      screens,
				HiddenKilled: hiddenKilled,
				Wait: &WaitDetails{
					Settled:      []WaitSettledDetail{},
					StillRunning: []string{},
					TimedOut:     false,
					Waiting:      true,
				},
			},
		})
	}

	var stopTicker chan struct{}
	var tickerDone chan struct{}
	if onUpdate != nil {
		emitProgress()
		stopTicker = make(chan struct{})
		tickerDone = make(chan struct{})
		go func() {
			defer close(tickerDone)
			tk := time.NewTicker(waitProgressInterval)
			defer tk.Stop()
			for {
				select {
				case <-tk.C:
					emitProgress()
				case <-stopTicker:
					return
				}
			}
		}()
	}

	outcome, err := reg.Wait(ctx, parent, WaitParams{
		Sessions:  sessions,
		Timeout:   timeout,
		HasCustom: hasTimeout,
	})
	if stopTicker != nil {
		close(stopTicker)
		<-tickerDone
	}
	if err != nil {
		return sdk.ToolResult{}, err
	}

	for _, entry := range outcome.Settled {
		shown[entry.ID] = true
	}
	settledDetails := make([]WaitSettledDetail, len(outcome.Settled))
	for i, entry := range outcome.Settled {
		settledDetails[i] = WaitSettledDetail{
			ID:     entry.ID,
			JobID:  entry.JobID,
			Status: entry.Status,
		}
	}
	screens, hiddenKilled := wallOf(reg, parent, sessions, shown)
	stillRunning := outcome.StillRunning
	if stillRunning == nil {
		stillRunning = []string{}
	}
	details := ToolDetails{
		Op:           OpWait,
		Screens:      screens,
		HiddenKilled: hiddenKilled,
		Wait: &WaitDetails{
			Settled:      settledDetails,
			StillRunning: stillRunning,
			TimedOut:     outcome.TimedOut,
		},
	}

	if len(outcome.Settled) == 0 && len(outcome.StillRunning) == 0 {
		return sdk.ToolResult{
			Content: "No turns in flight to wait for.",
			Details: details,
		}, nil
	}

	var lines []string
	for _, entry := range outcome.Settled {
		lines = append(lines, fmt.Sprintf("## `%s` — %s", entry.ID, entry.Status), entry.ResultText, "")
	}
	if len(outcome.StillRunning) > 0 {
		quoted := make([]string, len(outcome.StillRunning))
		for i, id := range outcome.StillRunning {
			quoted[i] = "`" + id + "`"
		}
		lines = append(lines, "Still running: "+strings.Join(quoted, ", ")+".")
	}
	if outcome.TimedOut {
		lines = append(lines, "Wait window elapsed before any turn settled — re-issue vibe_wait to keep waiting.")
	}
	text := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	return sdk.ToolResult{
		Content: text,
		Details: details,
	}, nil
}

// ExecuteKill implements `vibe_kill`.
// upstream: packages/coding-agent/src/tools/vibe.ts:194-215
func ExecuteKill(reg *SessionRegistry, parent *ParentSession, params map[string]any) (sdk.ToolResult, error) {
	sessionID, _ := params["session"].(string)
	outcome, err := reg.Kill(parent, sessionID)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	cancelNote := ""
	if outcome.CancelledTurn {
		cancelNote = " Its in-flight turn was cancelled."
	}
	screens, hiddenKilled := wallOf(reg, parent, nil, nil)
	oc := outcome
	return sdk.ToolResult{
		Content: fmt.Sprintf("Killed session `%s`.%s Transcript remains at history://%s.", outcome.ID, cancelNote, outcome.ID),
		Details: ToolDetails{
			Op:           OpKill,
			Screens:      screens,
			HiddenKilled: hiddenKilled,
			Killed:       &oc,
		},
	}, nil
}

// ExecuteList implements `vibe_list`.
// upstream: packages/coding-agent/src/tools/vibe.ts:217-247
func ExecuteList(reg *SessionRegistry, parent *ParentSession) (sdk.ToolResult, error) {
	screens, hiddenKilled := wallOf(reg, parent, nil, nil)
	details := ToolDetails{
		Op:           OpList,
		Screens:      screens,
		HiddenKilled: hiddenKilled,
	}
	lines := make([]string, 0, len(screens)+2)
	for _, sc := range screens {
		turnWord := "turns"
		if sc.Turns == 1 {
			turnWord = "turn"
		}
		parts := []string{
			fmt.Sprintf("- `%s` [%s] %s", sc.ID, sc.CLI, sc.State),
			fmt.Sprintf("%d %s", sc.Turns, turnWord),
		}
		if sc.Queued > 0 {
			parts = append(parts, fmt.Sprintf("%d queued", sc.Queued))
		}
		if sc.Model != "" {
			parts = append(parts, sc.Model)
		}
		if sc.LastActivity != "" {
			parts = append(parts, "last: "+sc.LastActivity)
		}
		lines = append(lines, strings.Join(parts, " · "))
	}
	if len(lines) == 0 {
		if len(hiddenKilled) > 0 {
			lines = append(lines, "No live vibe sessions.")
		} else {
			lines = append(lines, "No vibe sessions. Spawn one with vibe_spawn.")
		}
	}
	if len(hiddenKilled) > 0 {
		start := 0
		if len(hiddenKilled) > killedIDsListed {
			start = len(hiddenKilled) - killedIDsListed
		}
		recent := make([]string, 0, len(hiddenKilled)-start+1)
		for _, id := range hiddenKilled[start:] {
			recent = append(recent, "`"+id+"`")
		}
		if older := start; older > 0 {
			recent = append(recent, fmt.Sprintf("+%d more", older))
		}
		lines = append(lines, fmt.Sprintf("Killed (%d, transcripts at history://<id>): %s", len(hiddenKilled), strings.Join(recent, ", ")))
	}
	return sdk.ToolResult{
		Content: strings.Join(lines, "\n"),
		Details: details,
	}, nil
}

// buildToolDefinitions creates the five Vibe tool definitions wrapped with
// `schemaorder.Apply` and `intent.ToolForContext` (matching every other area).
func (c *controller) buildToolDefinitions(ctx sdk.Context) []sdk.ToolDefinition {
	defaultOff := false
	defs := []sdk.ToolDefinition{
		{
			Name:          "vibe_spawn",
			Label:         "Vibe Spawn",
			Description:   strings.TrimSpace(vibeSpawnPrompt),
			Parameters:    vibeSpawnSchema(),
			DefaultActive: &defaultOff,
			Execute: func(tctx sdk.Context, params map[string]any) (any, error) {
				if err := c.requireActive(tctx); err != nil {
					return nil, err
				}
				return ExecuteSpawn(c.reg, c.parentSession(tctx), params)
			},
		},
		{
			Name:          "vibe_send",
			Label:         "Vibe Send",
			Description:   strings.TrimSpace(vibeSendPrompt),
			Parameters:    vibeSendSchema(),
			DefaultActive: &defaultOff,
			Execute: func(tctx sdk.Context, params map[string]any) (any, error) {
				if err := c.requireActive(tctx); err != nil {
					return nil, err
				}
				return ExecuteSend(c.reg, c.parentSession(tctx), params)
			},
		},
		{
			Name:          "vibe_wait",
			Label:         "Vibe Wait",
			Description:   strings.TrimSpace(vibeWaitPrompt),
			Parameters:    vibeWaitSchema(),
			DefaultActive: &defaultOff,
			Execute: func(tctx sdk.Context, params map[string]any) (any, error) {
				if err := c.requireActive(tctx); err != nil {
					return nil, err
				}
				waitCtx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if done := tctx.Done(); done != nil {
					go func() {
						select {
						case <-done:
							cancel()
						case <-waitCtx.Done():
						}
					}()
				}
				return ExecuteWait(waitCtx, c.reg, c.parentSession(tctx), params, func(partial sdk.ToolResult) {
					_ = tctx.OnUpdate(partial)
				})
			},
		},
		{
			Name:          "vibe_kill",
			Label:         "Vibe Kill",
			Description:   strings.TrimSpace(vibeKillPrompt),
			Parameters:    vibeKillSchema(),
			DefaultActive: &defaultOff,
			Execute: func(tctx sdk.Context, params map[string]any) (any, error) {
				if err := c.requireActive(tctx); err != nil {
					return nil, err
				}
				return ExecuteKill(c.reg, c.parentSession(tctx), params)
			},
		},
		{
			Name:          "vibe_list",
			Label:         "Vibe List",
			Description:   strings.TrimSpace(vibeListPrompt),
			Parameters:    vibeListSchema(),
			DefaultActive: &defaultOff,
			Execute: func(tctx sdk.Context, _ map[string]any) (any, error) {
				if err := c.requireActive(tctx); err != nil {
					return nil, err
				}
				return ExecuteList(c.reg, c.parentSession(tctx))
			},
		},
	}
	out := make([]sdk.ToolDefinition, len(defs))
	for i, d := range defs {
		ordered := schemaorder.Apply(d)
		var zero sdk.Context
		if ctx != zero {
			out[i] = intent.ToolForContext(ctx, ordered)
		} else {
			out[i] = intent.Tool(ordered)
		}
	}
	return out
}

func extractStringSliceParam(v any) []string {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func numberParam(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, false
		}
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
