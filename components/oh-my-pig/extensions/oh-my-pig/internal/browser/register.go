package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/tools/browser/prelude-definition.ts,
//           packages/coding-agent/src/tools/browser/prelude.js,
//           packages/coding-agent/src/tools/browser/prelude.py,
//           packages/coding-agent/src/prompts/tools/browser.md,
//           packages/coding-agent/src/prompts/tools/browser-core.md,
//           packages/coding-agent/src/prompts/system/browser-user-session.md,
//           packages/coding-agent/src/prompts/system/browser-relay.md,
//           packages/coding-agent/src/prompts/system/browser-x11.md

//go:embed assets/prelude.js
var browserPreludeJS string

//go:embed assets/prelude.py
var browserPreludePY string

//go:embed assets/browser.md
var browserDocTemplate string

//go:embed assets/browser-core.md
var browserCoreDoc string

//go:embed assets/browser-user-session.md
var browserUserSessionTemplate string

//go:embed assets/browser-relay.md
var browserRelayTemplate string

//go:embed assets/browser-x11.md
var browserX11Guidance string

//go:embed assets/aria-snapshot.bundle.txt
var ariaSnapshotBundle string

// Setting is a nil-safe hook wired by the integrator to read omp's extension
// settings (`browser.*`, `tools.maxTimeout`) from internal/settings for a call's
// sdk.Context. When nil, defaults (or Manager.SetSettingsProvider) are used.
var Setting func(ctx sdk.Context, key string) any

var (
	defaultManager  = NewManager()
	defaultChromium *ChromiumBackend
	defaultRelay    *RelayBackend
	registerOnce    sync.Once
)

// DefaultManager returns the process-wide browser Manager shared by every
// session instance in this process (keyed internally by session ID).
func DefaultManager() *Manager {
	return defaultManager
}

// Register installs the `browser` eval prelude, registers the Chromium/CDP
// backends (`headless`, `connected`, `spawned`) once, registers the
// package-level `lifecycle.OnEnd("browser", ...)` cleanup, and hooks per-instance
// session lifecycle events.
//
// PiG builds a fresh extension instance per session, reload, /new, /resume, and
// /fork; calling Register repeatedly is idempotent and never resets shared state
// or kills another session's tabs.
// Must not panic or do I/O at registration time.
func Register(e *sdk.Extension) {
	// Create the shared ChromiumBackend once so a second Register never replaces
	// a backend holding live refcounted Chromium processes.
	registerOnce.Do(func() {
		defaultChromium = NewChromiumBackend()
		defaultRelay = NewRelayBackend(defaultChromium)
	})
	if _, ok := LookupBackend(string(KindHeadless)); !ok {
		RegisterBackend(string(KindHeadless), defaultChromium)
	}
	if _, ok := LookupBackend(string(KindConnected)); !ok {
		RegisterBackend(string(KindConnected), defaultChromium)
	}
	if _, ok := LookupBackend(string(KindSpawned)); !ok {
		RegisterBackend(string(KindSpawned), defaultChromium)
	}
	if _, ok := LookupBackend(string(KindRelay)); !ok {
		RegisterBackend(string(KindRelay), defaultRelay)
	}
	// Register package-level cleanup keyed by sessionID (covers both top-level
	// sessions and child/subagent sessions that emit no session_shutdown).
	lifecycle.OnEnd("browser", func(sessionID string) {
		if sessionID == "" {
			return
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		defaultManager.ReleaseTabsForOwner(c, sessionID, true)
	})

	eval.RegisterPrelude(PreludeDefinition(defaultManager))

	// Clear any ended-session tombstone FIRST THING on session_start (after /reload
	// or /resume, PiG reuses the same session ID with a fresh extension instance),
	// then reap stale temp Chromium profile directories of dead pids.
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sessionID := sessionIDFromSDK(ctx); sessionID != "" {
			defaultManager.StartSession(sessionID)
		}
		go cleanupStaleProfiles()
		return nil, nil
	})
	// Session shutdown: end ONLY the ending session via lifecycle.End(sessionID).
	// If the session ID cannot be resolved, release nothing (never "all sessions").
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		sessionID := sessionIDFromSDK(ctx)
		if sessionID == "" {
			return nil, nil
		}
		lifecycle.End(sessionID)
		return nil, nil
	})

	// Turn end: freeze idle headless Chromium tabs and reap any past idleCloseSec
	// ONLY for this session.
	e.OnEvent("turn_end", func(ctx sdk.Context, _ map[string]any) (any, error) {
		sessionID := sessionIDFromSDK(ctx)
		if sessionID == "" {
			return nil, nil
		}
		settings := effectiveSettingsForSDK(ctx, defaultManager)
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		defaultManager.FreezeIdleTabsForOwner(c, sessionID, settings)
		if settings.IdleCloseSec > 0 {
			defaultManager.ReleaseIdleTabsForOwner(c, sessionID, time.Duration(settings.IdleCloseSec)*time.Second)
		}
		return nil, nil
	})
}

func settingsFromHook(ctx sdk.Context) (Settings, bool) {
	fn := Setting
	if fn == nil {
		return Settings{}, false
	}
	s := DefaultSettings()
	if v, ok := fn(ctx, "browser.enabled").(bool); ok {
		s.Enabled = v
	}
	if v, ok := fn(ctx, "browser.cdpUrl").(string); ok {
		s.CdpURL = v
	}
	if v, ok := fn(ctx, "browser.relay").(bool); ok {
		s.Relay = v
	}
	if v, ok := fn(ctx, "browser.relayUrl").(string); ok {
		s.RelayURL = v
	}
	if v, ok := fn(ctx, "browser.headless").(bool); ok {
		s.Headless = v
	}
	if v, ok := fn(ctx, "browser.cmux").(bool); ok {
		s.Cmux = v
	}
	if v, ok := fn(ctx, "browser.tern").(bool); ok {
		s.Tern = v
	}
	if v, ok := fn(ctx, "browser.webkit").(bool); ok {
		s.WebKit = v
	}
	if v, ok := fn(ctx, "browser.webkitPath").(string); ok {
		s.WebKitPath = v
	}
	if v, ok := fn(ctx, "browser.freezeOnTurnEnd").(bool); ok {
		s.FreezeOnTurnEnd = v
	}
	switch v := fn(ctx, "browser.idleCloseSec").(type) {
	case int:
		s.IdleCloseSec = v
	case int64:
		s.IdleCloseSec = int(v)
	case float64:
		s.IdleCloseSec = int(v)
	}
	if v, ok := fn(ctx, "browser.screenshotDir").(string); ok {
		s.ScreenshotDir = v
	}
	switch v := fn(ctx, "tools.maxTimeout").(type) {
	case int:
		s.MaxTimeoutSec = v
	case int64:
		s.MaxTimeoutSec = int(v)
	case float64:
		s.MaxTimeoutSec = int(v)
	}
	return s, true
}

func effectiveSettingsForSDK(ctx sdk.Context, m *Manager) Settings {
	if s, ok := settingsFromHook(ctx); ok {
		return s
	}
	return m.Settings()
}

// sessionIDFromSDK returns the session's unique ID from ctx.GetSessionID().
// Never falls back to Cwd or a shared "default" key so an unresolved session ID
// cannot collide with or release another session's tabs.
func sessionIDFromSDK(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

// Documentation renders omp's `browser.md` template for settings and platform.
// upstream: browserDocumentation in prelude-definition.ts:22
func Documentation(settings Settings, platform string) string {
	x11 := x11GuidanceText(settings, platform)
	out := strings.ReplaceAll(browserDocTemplate, "{{{x11Guidance}}}", x11)
	return strings.TrimSpace(out)
}

// DocumentationForContext renders omp's `browser.md` template using the
// session's live settings via browser.Setting(ctx, ...).
func DocumentationForContext(ctx sdk.Context, m *Manager) string {
	if m == nil {
		m = defaultManager
	}
	return Documentation(effectiveSettingsForSDK(ctx, m), m.platform)
}

// Guidance renders omp's `browser-user-session.md` system prompt block
// (including optional relay and X11 sub-blocks).
// upstream: createBrowserPreludeDefinition.guidance in prelude-definition.ts:54
func Guidance(settings Settings, platform string, lookup func(string) (string, bool)) string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if platform == "" {
		platform = runtime.GOOS
	}
	var relayBlock string
	if IsRelayOnly(lookup) {
		relayBlock = renderRelayGuidance(true, nil)
	} else {
		sess := ResolveSession{Settings: settings, Platform: platform, PID: os.Getpid()}
		if k, err := ResolveBrowserKind(ResolveParams{}, sess, lookup); err == nil && k.Kind == KindRelay {
			fb, _ := ResolveBrowserKind(ResolveParams{App: "auto", HasApp: true}, sess, lookup)
			relayBlock = renderRelayGuidance(false, &fb)
		}
	}
	x11 := x11GuidanceText(settings, platform)
	return renderUserSessionGuidance(relayBlock, x11)
}

// GuidanceForContext renders omp's `browser-user-session.md` system prompt
// block using the session's live settings via browser.Setting(ctx, ...).
func GuidanceForContext(ctx sdk.Context, m *Manager) string {
	if m == nil {
		m = defaultManager
	}
	return Guidance(effectiveSettingsForSDK(ctx, m), m.platform, m.env)
}

func x11GuidanceText(settings Settings, platform string) string {
	if platform == "" {
		platform = runtime.GOOS
	}
	remote := false
	for _, u := range []string{settings.CdpURL, settings.RelayURL} {
		u = strings.TrimSpace(u)
		if u != "" && !IsLoopbackURL(u) {
			remote = true
			break
		}
	}
	if platform == "linux" || remote {
		return "\n" + strings.TrimSpace(browserX11Guidance)
	}
	return ""
}

func renderRelayGuidance(only bool, fallback *ResolvedKind) string {
	var body string
	if only {
		body = `This session was started from omp's extension in the user's Chrome: ` +
			"`browser.open`" + ` drives that Chrome, signed in as the user, and tabs it opens appear in the user's window (omp tab group). There is no other browser: ` +
			"`app`" + ` accepts only ` + "`\"relay\"`" + `.`
	} else {
		var alt string
		switch {
		case fallback != nil && fallback.Kind == KindConnected:
			alt = fmt.Sprintf("Pass `app: \"auto\"` to open in the browser at `browser.cdpUrl` (%s) instead.", RedactURLForLog(fallback.CdpURL))
		case fallback != nil && fallback.Kind == KindTern:
			alt = "Pass `app: \"auto\"` for omp's own Tern picture-in-picture instead."
		case fallback != nil && fallback.Kind == KindCmux:
			alt = "Pass `app: \"auto\"` for omp's own cmux browser surface instead."
		default:
			alt = "Pass `app: \"auto\"` for a throwaway managed browser."
		}
		body = "The browser relay is on: `browser.open` without `app` drives the user's own Chrome, signed in as them. " + alt
	}
	return "# Browser Relay\n\n" + body
}

func renderUserSessionGuidance(relayBlock, x11Block string) string {
	base := browserUserSessionTemplate
	if idx := strings.Index(base, "{{#if relay}}"); idx >= 0 {
		base = base[:idx]
	}
	parts := []string{strings.TrimSpace(base)}
	if strings.TrimSpace(relayBlock) != "" {
		parts = append(parts, strings.TrimSpace(relayBlock))
	}
	if strings.TrimSpace(x11Block) != "" {
		parts = append(parts, strings.TrimSpace(x11Block))
	}
	return strings.Join(parts, "\n\n")
}

// PreludeDefinition constructs the eval.Prelude definition backed by m.
// Reads live browser.* settings from the Setting hook in Enabled and Invoke.
// upstream: createBrowserPrelude in browser.ts:328, createBrowserPreludeDefinition in prelude-definition.ts:40
func PreludeDefinition(m *Manager) eval.Prelude {
	return eval.Prelude{
		Name: "browser",
		Documentation: func() string {
			return Documentation(m.Settings(), m.platform)
		},
		DocumentationFor: func(c sdk.Context) string {
			return Documentation(effectiveSettingsForSDK(c, m), m.platform)
		},
		FirstCallDocumentation: strings.TrimSpace(browserCoreDoc),
		JavaScript:             browserPreludeJS,
		Python:                 browserPreludePY,
		Exports:                []string{"browser"},
		Guidance: func() string {
			return Guidance(m.Settings(), m.platform, m.env)
		},
		GuidanceFor: func(c sdk.Context) string {
			return Guidance(effectiveSettingsForSDK(c, m), m.platform, m.env)
		},
		Approval: "exec",
		Enabled: func(c sdk.Context) bool {
			return effectiveSettingsForSDK(c, m).Enabled
		},
		Invoke: func(call eval.PreludeCall, parameters json.RawMessage) (*eval.PreludeResult, error) {
			ctx := context.Background()
			var cancel context.CancelFunc
			if done := call.SDK.Done(); done != nil {
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				go func() {
					select {
					case <-done:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			sessionID := sessionIDFromSDK(call.SDK)
			if sessionID == "" {
				return nil, fmt.Errorf("browser prelude requires an active session id")
			}
			settings := effectiveSettingsForSDK(call.SDK, m)
			req := InvokeRequest{
				Cwd:            call.SDK.Cwd(),
				OwnerSessionID: sessionID,
				CellTimeoutMs:  call.CellTimeoutMs,
				HasCellTimeout: call.CellTimeoutMs >= 0,
				Settings:       &settings,
			}
			res, err := m.Invoke(ctx, req, parameters)
			if err != nil {
				return nil, err
			}
			out := &eval.PreludeResult{
				Text:    res.Text,
				Details: res.Details,
			}
			for _, img := range res.Images {
				out.Images = append(out.Images, eval.PreludeImage{
					MimeType: img.MimeType,
					Data:     img.Data,
				})
			}
			return out, nil
		},
		Status: describePreludeStatus,
	}
}

func describePreludeStatus(parameters json.RawMessage, result *eval.PreludeResult) string {
	var params BrowserParams
	if err := json.Unmarshal(parameters, &params); err != nil {
		return ""
	}
	name := params.Name
	if name == "" {
		name = DefaultTabName
	}
	switch params.Action {
	case "open":
		var u string
		if result != nil {
			switch d := result.Details.(type) {
			case InvokeDetails:
				u = d.URL
			case *InvokeDetails:
				if d != nil {
					u = d.URL
				}
			}
		}
		if u != "" {
			return fmt.Sprintf("open %s %s", name, u)
		}
		return "open " + name
	case "close":
		if params.All {
			return "close all"
		}
		return "close " + name
	case "run":
		if params.Fn != "" {
			return name + ".run(fn)"
		}
		first := strings.TrimSpace(params.Code)
		if idx := strings.IndexByte(first, '\n'); idx >= 0 {
			first = first[:idx]
		}
		return fmt.Sprintf("%s.run(%s)", name, first)
	case "call":
		return FormatStatusCallChain(name, params.Chain)
	case "tabs":
		return "tabs"
	case "targets":
		return "targets"
	default:
		return ""
	}
}
