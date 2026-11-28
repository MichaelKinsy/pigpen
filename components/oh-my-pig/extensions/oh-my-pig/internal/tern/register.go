package tern

import (
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// Setting is an optional settings lookup hook assigned by the integrator
// (matching `ask.Setting`, `todo.Setting`, `search.Setting`, `shell.Setting`).
// When nil, omp's defaults apply.
//
// Keys read at use:
//   - `tui.titleState` (bool, default true)
//   - `tui.titleSpinner` (string: "braille"|"pulse"|"dots"|"line", default "braille")
//   - `tui.spinnerInterval` (number in ms, default 80; 0 pins static `:`)
//   - `completion.notify` (string: "on"|"off", default "on")
//   - `error.notify` (string: "on"|"off", default "off")
//   - `ask.notify` (string: "on"|"off", default "on")
var Setting func(ctx sdk.Context, key string) any

// terminalOwnerEnv is omp's `TERMINAL_OWNER_ENV`, claimed by the interactive
// TUI at startup so child processes (bash, eval kernels, services) know the
// Tern pane belongs to this session.
// upstream: packages/coding-agent/src/tools/browser/terminal-owner.ts:10
// upstream: packages/coding-agent/src/main.ts:1985
const terminalOwnerEnv = "PI_TERMINAL_OWNER_PID"

// Register registers omp's Tern connections on e:
//   - Interactive terminal owner claim (`PI_TERMINAL_OWNER_PID` on TUI `session_start`).
//   - Session title lifecycle (`session_start`, `session_info_changed`,
//     `agent_start`, `agent_end`, `ui_prompt_start`, `ui_prompt_end`,
//     `session_shutdown`): plain native title in Tern (`oh-my-pig` before named),
//     `π > / ⠋ / !` title with spinner in other terminals (TUI mode only,
//     with dialog holds keeping `!` across `agent_end`).
//   - Completion notifications (`agent_end`) and per-call `ask` notifications
//     (`tool_execution_start` for `ask`) via `ctx.Bell()`, in TUI mode only,
//     honouring `PI_NOTIFICATIONS` and omp's defaults (`completion.notify=on`,
//     `error.notify=off`, `ask.notify=on`).
//   - `tool_result` hook for `read`: attaches `details.mediaPath` for whole-file
//     reads of regular files whose extension Tern's media viewer shows
//     (`ShowsMedia`), skipping directories (`details.isDirectory`) and
//     non-regular/missing paths.
//
// Register performs no I/O and never panics.
func Register(e *sdk.Extension) {
	registerWithOptions(e, registerOptions{
		lookup:    osLookupEnv,
		notifyCfg: DefaultNotifyConfig(),
		claimPID:  os.Getpid(),
		setEnv:    os.Setenv,
	})
}

type registerOptions struct {
	lookup    EnvLookup
	notifyCfg NotifyConfig
	claimPID  int
	setEnv    func(key, value string) error
	titles    *TitleController
	settingFn func(ctx sdk.Context, key string) any
}

func registerWithOptions(e *sdk.Extension, opts registerOptions) {
	lookup := opts.lookup
	if lookup == nil {
		lookup = osLookupEnv
	}
	titles := opts.titles
	if titles == nil {
		titles = NewTitleController(lookup)
	}
	readSetting := func(ctx sdk.Context, key string) any {
		fn := opts.settingFn
		if fn == nil {
			fn = Setting
		}
		if fn == nil {
			return nil
		}
		return fn(ctx, key)
	}
	applyTitleSettings := func(ctx sdk.Context) {
		var enabled *bool
		var style *string
		var interval *time.Duration
		if v, ok := settingBool(readSetting(ctx, "tui.titleState")); ok {
			enabled = &v
		}
		if v, ok := settingString(readSetting(ctx, "tui.titleSpinner")); ok {
			style = &v
		}
		if v, ok := settingDurationMs(readSetting(ctx, "tui.spinnerInterval")); ok {
			interval = &v
		}
		if enabled != nil || style != nil || interval != nil {
			titles.ApplySettings(enabled, style, interval)
		}
	}
	resolveNotifyConfig := func(ctx sdk.Context) NotifyConfig {
		cfg := opts.notifyCfg
		if v, ok := settingOnOff(readSetting(ctx, "completion.notify")); ok {
			cfg.CompletionOn = v
		}
		if v, ok := settingOnOff(readSetting(ctx, "error.notify")); ok {
			cfg.ErrorOn = v
		}
		if v, ok := settingOnOff(readSetting(ctx, "ask.notify")); ok {
			cfg.AskOn = v
		}
		return cfg
	}

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if ctx.Mode() == "tui" && opts.setEnv != nil && opts.claimPID > 0 {
			_ = opts.setEnv(terminalOwnerEnv, strconv.Itoa(opts.claimPID))
		}
		titles.Init(ctx)
		applyTitleSettings(ctx)
		titles.SetSession(ctx, ctx.SessionName(), ctx.Cwd())
		return nil, nil
	})

	e.OnEvent(sdk.EventSessionInfoChanged, func(ctx sdk.Context, _ map[string]any) (any, error) {
		applyTitleSettings(ctx)
		titles.SetSession(ctx, ctx.SessionName(), ctx.Cwd())
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		applyTitleSettings(ctx)
		titles.SetState(ctx, TitleWorking)
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentEnd, func(ctx sdk.Context, data map[string]any) (any, error) {
		applyTitleSettings(ctx)
		titles.SetState(ctx, TitleIdle)
		NotifyAgentEnd(ctx, lookup, resolveNotifyConfig(ctx), data)
		return nil, nil
	})

	// Ring once per `ask` tool call, matching omp's `AskTool.#sendAskNotification`
	// (`tools/ask.ts:448-459`), rather than on every individual `ui_prompt_start`.
	e.OnEvent(sdk.EventToolExecutionStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		if name, _ := data["toolName"].(string); name == "ask" {
			NotifyAsk(ctx, lookup, resolveNotifyConfig(ctx))
		}
		return nil, nil
	})

	e.OnEvent(sdk.EventUIPromptStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		titles.HoldBlocked(ctx)
		return nil, nil
	})

	e.OnEvent(sdk.EventUIPromptEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		titles.ReleaseBlocked(ctx)
		return nil, nil
	})

	e.OnSessionShutdown(func(_ sdk.Context, _ map[string]any) (any, error) {
		titles.Dispose()
		return nil, nil
	})

	// Attach `details.mediaPath` on whole-file `read` results whose extension
	// Tern's media viewer displays, matching omp's `read.ts:2320,2611`.
	e.OnToolResult(func(ctx sdk.Context, data map[string]any) (any, error) {
		toolName, _ := data["toolName"].(string)
		if toolName != "read" {
			return nil, nil
		}
		if isErr, _ := data["isError"].(bool); isErr {
			return nil, nil
		}
		existing, _ := data["details"].(map[string]any)
		if existing != nil {
			if isDir, _ := existing["isDirectory"].(bool); isDir {
				return nil, nil
			}
			if cur, _ := existing["mediaPath"].(string); strings.TrimSpace(cur) != "" {
				return nil, nil
			}
		}
		input, _ := data["input"].(map[string]any)
		rawPath, _ := input["path"].(string)
		absPath, selector, ok := resolveReadMediaTarget(rawPath, ctx.Cwd(), existing)
		if !ok {
			return nil, nil
		}
		// Check extension and whole-file selector first so non-media reads cost no os.Stat.
		mediaPath := MediaPathForRead(absPath, selector)
		if mediaPath == "" {
			return nil, nil
		}
		info, err := os.Stat(mediaPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil
		}
		outPath := mediaPath
		if existing == nil || strings.TrimSpace(fmtResolvedPath(existing)) == "" {
			outPath = dirs.StandardizeMacOSPath(mediaPath)
		}
		updated := AttachMediaPath(existing, outPath)
		return map[string]any{"details": updated}, nil
	})
}

func fmtResolvedPath(m map[string]any) string {
	s, _ := m["resolvedPath"].(string)
	return s
}

func settingBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func settingString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != ""
}

func settingOnOff(v any) (bool, bool) {
	switch x := v.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "on", "true", "1":
			return true, true
		case "off", "false", "0":
			return false, true
		}
	case bool:
		return x, true
	}
	return false, false
}

func settingDurationMs(v any) (time.Duration, bool) {
	switch n := v.(type) {
	case int:
		if n < 0 {
			n = 0
		}
		return time.Duration(n) * time.Millisecond, true
	case int64:
		if n < 0 {
			n = 0
		}
		return time.Duration(n) * time.Millisecond, true
	case float64:
		if n < 0 {
			n = 0
		}
		return time.Duration(n) * time.Millisecond, true
	}
	return 0, false
}
