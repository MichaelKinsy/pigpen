package eval

import (
	"os"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/eval.ts,
//           packages/coding-agent/src/session/session-tools.ts:894-901,964-975,
//           packages/coding-agent/src/tools/index.ts:622-652

// MountedToolNames returns the names of tools mounted as bash tool commands for
// ctx's session in presentation order (e.g. `ast_edit`, `debug`, `lsp`), so the
// eval description's `Session tool names` line appends enabled mounted tools
// after advertised top-level tools without including disabled or special tools.
// Defaults to `toolcmd.MountedNames`.
var MountedToolNames func(ctx sdk.Context) []string = toolcmd.MountedNames

// HiddenCommandNames returns all bash tool command names installed for ctx's
// session (mounted commands + special commands such as `resolve`, `reject`,
// `propose`, `report_issue`) that stay in PiG's active tool list solely for
// direct-call fallback dispatch and are stripped from provider requests.
// Defaults to `toolcmd.CommandNames`.
var HiddenCommandNames func(ctx sdk.Context) []string = toolcmd.CommandNames

// ompBuiltinToolOrder mirrors omp's BUILTIN_TOOLS registration order
// (packages/coding-agent/src/tools/index.ts:622-652).
var ompBuiltinToolOrder = func() map[string]int {
	m := make(map[string]int, len(toolcmd.BuiltinToolOrder))
	for i, name := range toolcmd.BuiltinToolOrder {
		m[name] = i
	}
	return m
}()

var defaultManager = NewManager()

// DefaultManager returns the process-wide eval kernel Manager registered with the extension.
func DefaultManager() *Manager {
	return defaultManager
}

// Register installs the `eval` tool, lifecycle cleanup, session_start / session_shutdown
// hooks, and `help://eval/<prelude>` docs provider. Safe to call multiple times across
// sessions in one process; does no I/O and calls no prelude hooks at registration time.
func Register(e *sdk.Extension) {
	registerWithManager(e, defaultManager)
}

func registerWithManager(e *sdk.Extension, mgr *Manager) {
	urls.SetEvalPreludeDocsProvider(func(c sdk.Context) map[string]string {
		enabled := EnabledPreludes(c)
		docs := make(map[string]string, len(enabled))
		for _, p := range enabled {
			if isBuiltinBridgePrelude(p.Name) {
				continue
			}
			if doc := strings.TrimSpace(p.DocumentationText(c)); doc != "" {
				docs[p.Name] = doc
			}
		}
		return docs
	})

	// Keyed by area name in internal/lifecycle; always points at mgr (package-level
	// defaultManager in production) so repeated Register calls across sessions never lose
	// cleanup for earlier sessions, and child sessions disposed via lifecycle.End are reaped.
	lifecycle.OnEnd("eval", func(sessionID string) {
		mgr.ShutdownSession(sessionID)
	})

	// Per-extension-instance advertised snapshot populated on session_start using the
	// real sdk.Context (never a zero sdk.Context), matching omp's frozen advertised
	// prelude/settings snapshot (tools/eval.ts:320-360).
	var (
		snapMu         sync.RWMutex
		sessionStarted bool
		sessionOpts    DescriptionOptions
		sessionMounted []string
		sessionHidden  []string
	)

	snapshotToolCmdState := func(ctx sdk.Context) ([]string, []string) {
		var mounted, hidden []string
		if MountedToolNames != nil {
			mounted = append([]string(nil), MountedToolNames(ctx)...)
		}
		if HiddenCommandNames != nil {
			hidden = append([]string(nil), HiddenCommandNames(ctx)...)
		}
		return mounted, hidden
	}

	// Initial registration description has no session context yet (Preludes is nil,
	// so no Enabled(ctx) or DocumentationFor(ctx) callback runs here). OnSessionStart
	// re-renders with the real session sdk.Context and replaces the tool definition.
	initialOpts := staticDescriptionOptions(nil)

	buildToolDef := func(baseOpts DescriptionOptions) sdk.ToolDefinition {
		return sdk.ToolDefinition{
			Name:          "eval",
			Label:         "Eval",
			Description:   RenderDescription(baseOpts),
			Parameters:    evalToolSchema(enabledLanguagesFromOpts(baseOpts), baseOpts.MaxTimeout),
			ExecutionMode: "sequential",
			PrepareLoadout: func(loadout sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
				snapMu.RLock()
				started := sessionStarted
				opts := sessionOpts
				mounted := append([]string(nil), sessionMounted...)
				hidden := append([]string(nil), sessionHidden...)
				snapMu.RUnlock()
				toolNames := advertisedEvalToolNames(loadout.Declared, hidden, mounted)
				if !started {
					// Before session_start no real sdk.Context has arrived yet; render
					// without invoking any prelude Enabled(ctx) hook.
					opts = staticDescriptionOptions(toolNames)
				} else {
					opts.ToolNames = toolNames
				}
				return &sdk.ToolLoadoutChanges{
					Descriptions: map[string]string{
						"eval": RenderDescription(opts),
					},
				}
			},
			ReachesTools: func(_ map[string]any) sdk.ToolReach {
				return sdk.ToolReach{All: true}
			},
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				return mgr.Execute(ctx, params)
			},
		}
	}

	evalDef := schemaorder.Apply(buildToolDef(initialOpts))
	e.RegisterTool(intent.Tool(evalDef))
	intent.RegisterSync(e, evalDef)

	// Snapshot enabled preludes and session settings once the real sdk.Context is live,
	// then refresh the registered tool definition so the model sees the exact session
	// description and schema (and PrepareLoadout reuses the snapshot).
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.SessionManager().GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
			jobs.BindSession(id, ctx)
		}
		opts := descriptionOptionsForSession(ctx, nil)
		mounted, hidden := snapshotToolCmdState(ctx)
		snapMu.Lock()
		sessionOpts = opts
		sessionMounted = mounted
		sessionHidden = hidden
		sessionStarted = true
		snapMu.Unlock()
		ctx.RegisterTool(intent.ToolForContext(ctx, schemaorder.Apply(buildToolDef(opts))))
		return nil, nil
	})

	// Refresh mounted/hidden tool-command names before each agent turn so settings/MCP
	// changes (or registration order where eval.Register runs before toolcmd sync)
	// never leave PrepareLoadout with a stale snapshot.
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		mounted, hidden := snapshotToolCmdState(ctx)
		snapMu.Lock()
		sessionMounted = mounted
		sessionHidden = hidden
		snapMu.Unlock()
		return nil, nil
	})

	// Forward the ending session's ID to lifecycle.End (never shut down all sessions,
	// and never treat an unknown/empty session ID as "all sessions").
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if id, err := ctx.SessionManager().GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
			lifecycle.End(id)
		}
		return nil, nil
	})
}

// staticDescriptionOptions builds env-only defaults without calling any prelude hook.
func staticDescriptionOptions(toolNames []string) DescriptionOptions {
	opts := DefaultDescriptionOptions(nil)
	opts.ToolNames = toolNames
	if v := strings.TrimSpace(os.Getenv("PI_PY")); v == "0" || strings.EqualFold(v, "false") {
		opts.Py = false
	}
	if v := strings.TrimSpace(os.Getenv("PI_JS")); v == "0" || strings.EqualFold(v, "false") {
		opts.JS = false
	}
	return opts
}

// descriptionOptionsForSession evaluates EnabledPreludes and eval.* settings against
// a real session sdk.Context (never a zero Context).
func descriptionOptionsForSession(ctx sdk.Context, toolNames []string) DescriptionOptions {
	preludes := EnabledPreludes(ctx)
	opts := DefaultDescriptionOptions(preludes)
	opts.SessionContext = ctx
	opts.ToolNames = toolNames
	settings := readSessionSettings(ctx)
	opts.Py = settings.AllowPy
	opts.JS = settings.AllowJS
	opts.EvalTools = settings.EvalToolsEnabled
	opts.AutoBackgroundEnabled = settings.AutoBackgroundEnabled
	opts.MaxTimeout = settings.MaxTimeout
	return opts
}

func enabledLanguagesFromOpts(opts DescriptionOptions) []string {
	var langs []string
	if opts.Py {
		langs = append(langs, "py")
	}
	if opts.JS {
		langs = append(langs, "js")
	}
	return langs
}

// advertisedEvalToolNames builds omp's `Session tool names:` list from the active
// top-level tools (`loadout.Declared` minus `hidden` mounted/special commands that
// stay active in PiG only for direct-call fallback dispatch, sorted in omp's
// BUILTIN_TOOLS order first then non-built-ins in declared order) followed by any
// enabled mounted tool commands (`mounted`, in omp's BUILTIN_TOOLS order then
// mounted order), deduplicated and filtered by `isEvalBridgeToolCallable`.
//
// This matches omp's `getEnabledToolNames()` (`session-tools.ts:894-901`) and
// `getEvalBridgeToolNames()` (`session-tools.ts:964-975`), where active top-level
// tools come first in BUILTIN_TOOLS order and enabled mounted tool commands follow
// in BUILTIN_TOOLS order, while unmounted special commands (`resolve`, `reject`,
// `propose`, `report_issue`) and disabled tools are excluded.
func advertisedEvalToolNames(declared []sdk.AgentTool, hidden, mounted []string) []string {
	hiddenSet := make(map[string]bool, len(hidden))
	for _, name := range hidden {
		if name != "" {
			hiddenSet[name] = true
		}
	}

	topLevel := make([]string, 0, len(declared))
	for _, t := range declared {
		if t.Name == "" || hiddenSet[t.Name] || !isEvalBridgeToolCallable(t.Name) {
			continue
		}
		topLevel = append(topLevel, t.Name)
	}
	sortByOmpBuiltinOrder(topLevel)

	mountedNames := make([]string, 0, len(mounted))
	for _, name := range mounted {
		if name != "" && isEvalBridgeToolCallable(name) {
			mountedNames = append(mountedNames, name)
		}
	}
	sortByOmpBuiltinOrder(mountedNames)

	seen := make(map[string]bool, len(topLevel)+len(mountedNames))
	out := make([]string, 0, len(topLevel)+len(mountedNames))
	for _, name := range topLevel {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, name := range mountedNames {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func sortByOmpBuiltinOrder(names []string) {
	sort.SliceStable(names, func(i, j int) bool {
		bi, hasI := ompBuiltinToolOrder[names[i]]
		bj, hasJ := ompBuiltinToolOrder[names[j]]
		if hasI && hasJ {
			return bi < bj
		}
		if hasI != hasJ {
			return hasI
		}
		return false
	})
}
