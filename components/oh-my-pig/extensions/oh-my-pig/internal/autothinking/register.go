package autothinking

import (
	"context"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// per-session auto-thinking state. PiG's SDK ThinkingLevel enum is concrete
// (off|minimal|low|medium|high|xhigh|max), so we track whether a top-level
// session is in `auto` mode (enabled via `SetAutoMode` / `defaultThinkingLevel: "auto"`,
// cleared when the user explicitly selects a concrete thinking level).
type sessionState struct {
	autoMode     bool
	explicitMode bool // true once user explicitly toggled auto or picked a concrete level
	lastPrompt   string
	settingLevel bool // true while we are applying a classified level via SetThinkingLevel
}

var (
	stateMu   sync.Mutex
	states    = make(map[string]*sessionState)
	cleanupMu sync.Once
)

func sessionIDFor(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	if sid, err := ctx.GetSessionID(); err == nil && strings.TrimSpace(sid) != "" {
		return strings.TrimSpace(sid)
	}
	return ""
}

func getStateLocked(sid string) *sessionState {
	st, ok := states[sid]
	if !ok {
		st = &sessionState{}
		states[sid] = st
	}
	return st
}

// SetAutoMode explicitly enables or disables per-turn auto-thinking for the
// session associated with ctx (e.g. from `/effort auto` or `/thinking auto`).
func SetAutoMode(ctx sdk.Context, enabled bool) {
	sid := sessionIDFor(ctx)
	if sid == "" {
		return
	}
	stateMu.Lock()
	st := getStateLocked(sid)
	st.autoMode = enabled
	st.explicitMode = true
	stateMu.Unlock()
}

// IsAutoMode reports whether the session associated with ctx is currently in
// per-turn auto-thinking mode.
func IsAutoMode(ctx sdk.Context) bool {
	sid := sessionIDFor(ctx)
	if sid == "" {
		return false
	}
	stateMu.Lock()
	st, ok := states[sid]
	if ok && st.explicitMode {
		auto := st.autoMode
		stateMu.Unlock()
		return auto
	}
	stateMu.Unlock()

	// Check if host itself reports "auto".
	var zero sdk.Context
	if ctx != zero {
		if lvl, err := ctx.GetThinkingLevel(); err == nil && strings.EqualFold(strings.TrimSpace(lvl), "auto") {
			return true
		}
	}
	// Fall back to defaultThinkingLevel setting when user hasn't picked a concrete level.
	return strings.EqualFold(settingString(ctx, "defaultThinkingLevel", ""), "auto")
}

// ResetSessionsForTests clears per-session auto-thinking state between tests.
func ResetSessionsForTests() {
	stateMu.Lock()
	defer stateMu.Unlock()
	clear(states)
}

// Register wires per-turn auto-thinking classification for top-level sessions.
// Subagent sessions are skipped (their thinking level is classified at spawn
// time in internal/agents using `solutionSpace`). Sessions configured with a
// concrete thinking level never invoke the LLM judge (zero extra HTTP calls).
func Register(e *sdk.Extension) {
	cleanupMu.Do(func() {
		lifecycle.OnEnd("autothinking", func(sessionID string) {
			if sessionID == "" {
				return
			}
			stateMu.Lock()
			delete(states, sessionID)
			stateMu.Unlock()
		})
	})

	// Capture latest user input text so EventBeforeAgentStart has the prompt even
	// if the host omits data["prompt"].
	e.OnEvent(sdk.EventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		if IsSubagent != nil && IsSubagent(ctx) {
			return nil, nil
		}
		text, _ := data["text"].(string)
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		sid := sessionIDFor(ctx)
		if sid == "" {
			return nil, nil
		}
		stateMu.Lock()
		st := getStateLocked(sid)
		st.lastPrompt = text
		stateMu.Unlock()
		return nil, nil
	})

	// When the user explicitly selects a concrete thinking level via UI/command,
	// leave auto mode (unless we are the ones applying the classified level).
	e.OnEvent(sdk.EventThinkingLevelSelect, func(ctx sdk.Context, data map[string]any) (any, error) {
		if IsSubagent != nil && IsSubagent(ctx) {
			return nil, nil
		}
		sid := sessionIDFor(ctx)
		if sid == "" {
			return nil, nil
		}
		level, _ := data["level"].(string)
		if level == "" {
			level, _ = data["thinkingLevel"].(string)
		}
		stateMu.Lock()
		st := getStateLocked(sid)
		if st.settingLevel {
			stateMu.Unlock()
			return nil, nil
		}
		if strings.EqualFold(strings.TrimSpace(level), "auto") {
			st.autoMode = true
			st.explicitMode = true
		} else if strings.TrimSpace(level) != "" {
			st.autoMode = false
			st.explicitMode = true
		}
		stateMu.Unlock()
		return nil, nil
	})

	// On before_agent_start (top-level sessions only): classify the user's prompt
	// when the session is in auto mode, and apply the resolved level.
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		if IsSubagent != nil && IsSubagent(ctx) {
			return nil, nil
		}
		if !IsAutoMode(ctx) {
			return nil, nil
		}

		promptText, _ := data["prompt"].(string)
		if strings.TrimSpace(promptText) == "" {
			promptText, _ = data["text"].(string)
		}
		sid := sessionIDFor(ctx)
		if strings.TrimSpace(promptText) == "" && sid != "" {
			stateMu.Lock()
			if st, ok := states[sid]; ok {
				promptText = st.lastPrompt
			}
			stateMu.Unlock()
		}
		if strings.TrimSpace(promptText) == "" {
			return nil, nil
		}

		var targetModel map[string]any
		if info, err := ctx.GetModelInfo(); err == nil && info != nil && strings.TrimSpace(info.ID) != "" {
			if regModel, findErr := ctx.ModelRegistry().Find(info.Provider, info.ID); findErr == nil && regModel != nil {
				targetModel = regModel
			}
		}

		classified, err := ClassifyDifficulty(context.Background(), ctx, Input{
			Request:     promptText,
			TargetModel: targetModel,
		})
		if err != nil || classified == "" {
			return nil, nil
		}

		if sid != "" {
			stateMu.Lock()
			st := getStateLocked(sid)
			st.settingLevel = true
			stateMu.Unlock()
			defer func() {
				stateMu.Lock()
				if st, ok := states[sid]; ok {
					st.settingLevel = false
				}
				stateMu.Unlock()
			}()
		}
		ctx.SetThinkingLevel(classified)
		return nil, nil
	})
}
