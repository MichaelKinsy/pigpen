package ohmypig

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/agents"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ansi"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/approval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ask"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ast"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autolearn"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/browser"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/checkpoint"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/commit"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/debug"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/files"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/github"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lsp"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/modes"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/prediction"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/prompt"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ratchet"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/search"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/shell"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/snapcompact"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/tern"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/todo"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/vibe"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

var (
	wireOnce  sync.Once
	wireCount atomic.Int64
)

// wire connects cross-area hooks once per process before any area registers.
func wire() {
	wireOnce.Do(func() {
		wireCount.Add(1)
		wireSettings()
		wireToolCommands()
		wireLineageAndIntent()
		wireSpillAndURLs()
		wirePlanAndDiagnostics()
	})
}

func wireSettings() {
	// Per-area Setting hooks -> internal/settings.
	ask.Setting = settings.Any
	todo.Setting = settings.Any
	search.SetSetting(settings.Any)
	search.SetArchiveReader(&search.ArchiveMemberReader{
		ParseCandidates: func(filePath string) []search.ArchivePathCandidate {
			raw := files.ParseArchivePathCandidates(filePath)
			if len(raw) == 0 {
				return nil
			}
			out := make([]search.ArchivePathCandidate, len(raw))
			for i, c := range raw {
				out[i] = search.ArchivePathCandidate{
					ArchivePath: c.ArchivePath,
					SubPath:     c.SubPath,
				}
			}
			return out
		},
		ReadMember: files.ReadArchiveMember,
	})
	tern.Setting = settings.Any
	browser.Setting = settings.Any
	browser.SetSetting = func(ctx sdk.Context, key string, value any) error {
		return settings.Set(ctx, key, value, true)
	}
	modes.Setting = settings.Any
	modes.SetSetting = settings.Set
	snapcompact.Setting = settings.Any
	snapcompact.HandoffPrompt = modes.RenderHandoffPrompt
	prompt.Skillful = func(ctx sdk.Context) bool {
		return settings.Enabled(ctx, "skillful", true)
	}
	web.Setting = settings.Any
	eval.Setting = settings.Any
	commit.Setting = settings.Any
	prediction.Setting = settings.Any
	prediction.IsSubagent = agents.IsSubagent
	ratchet.Setting = settings.Any
	ratchet.SetSessionOverride = func(ctx sdk.Context, key string, value any) error {
		return settings.Set(ctx, key, value, false)
	}
	// ansi reads the drawing settings (statusLine.compactThinkingLevel,
	// composer.shape, read.toolResultPreview, theme) and prints the release
	// version on the welcome banner.
	ansi.Setting = settings.Any
	ansi.PigletVersion = surface.Version

	// shell.Setting serves BOTH security-sensitive keys (`bash.patterns` and
	// `bash.allowedEnvVars`) from operator layers only (`settings.OperatorAny`)
	// so a project layer can never loosen operator pattern rules or env
	// allowlists (upstream: tools/bash.ts:846-921, Settings.grantAuthority).
	// All other keys come from `settings.Any`.
	shell.Setting = func(ctx sdk.Context, key string) any {
		switch key {
		case "bash.patterns", "bash.allowedEnvVars":
			return settings.OperatorAny(ctx, key)
		case "tools.approval.bash":
			if m, ok := settings.Any(ctx, "tools.approval").(map[string]any); ok {
				return m["bash"]
			}
			return nil
		default:
			return settings.Any(ctx, key)
		}
	}
	// shell.SessionSetting supplies the full session view (including project
	// layers) so shell evaluates project deny/prompt tightening alongside the
	// operator authority view (`tools/bash.ts:846-921`).
	shell.SessionSetting = settings.Any
	shell.KillAgentHook = agents.KillAgent
	// approval is the one tool_call veto for every tool. Policy keys come from
	// operator layers; the session view (project layers included) may tighten
	// and never loosen (omp: tools/approval.ts:382-492).
	approval.Setting = settings.OperatorAny
	approval.SessionSetting = settings.Any
	approval.IsSubagent = agents.IsSubagent
	// Per-area boolean Enabled hooks -> internal/settings.Enabled.
	ast.Enabled = settings.Enabled
	autolearn.Enabled = settings.Enabled
	autolearn.IsSubagent = agents.IsSubagent
	autolearn.ExplicitTools = agents.ExplicitTools
	checkpoint.Enabled = settings.Enabled
	debug.Enabled = settings.Enabled
	github.Enabled = settings.Enabled
	toolcmd.Enabled = settings.Enabled

	toolcmd.DocsModeSetting = func(ctx sdk.Context) toolcmd.DocsMode {
		switch m := toolcmd.DocsMode(strings.TrimSpace(settings.String(ctx, "tools.commandDocs"))); m {
		case toolcmd.DocsModeInline, toolcmd.DocsModeBuiltins, toolcmd.DocsModeCatalog:
			return m
		default:
			return toolcmd.DefaultDocsMode
		}
	}
	toolcmd.InlinePatternsSetting = func(ctx sdk.Context) []string {
		return settings.Strings(ctx, "tools.commandInline")
	}

	github.CacheSettingsFn = func(ctx sdk.Context) github.CacheTTLSettings {
		// upstream: tools/github-cache.ts:771-772 — 0 is valid (soft 0 = always
		// revalidate, hard 0 = keep nothing); only negatives clamp, and fractions
		// are preserved. Schema defaults supply 300 / 604800 when unset.
		softSec := math.Max(0, settings.Float(ctx, github.SettingCacheSoftTTL))
		hardSec := math.Max(0, settings.Float(ctx, github.SettingCacheHardTTL))
		return github.CacheTTLSettings{
			Enabled: settings.Enabled(ctx, github.SettingCacheEnabled, true),
			SoftTTL: time.Duration(softSec * float64(time.Second)),
			HardTTL: time.Duration(hardSec * float64(time.Second)),
		}
	}

	lsp.SettingsReader = func(ctx sdk.Context) (map[string]any, error) {
		store := settings.StoreFor(ctx)
		keys := []string{
			"lsp.enabled",
			"lsp.lazy",
			"lsp.shared",
			"lsp.formatOnWrite",
			"lsp.diagnosticsOnWrite",
			"lsp.diagnosticsOnEdit",
			"lsp.diagnosticsDeduplicate",
			"lsp.requestTimeout",
			"tools.maxTimeout",
		}
		out := make(map[string]any, len(keys))
		for _, k := range keys {
			if v, ok := store.Get(k); ok {
				out[k] = v
			}
		}
		return out, nil
	}
	// symbolPreset (unicode, nerd, ascii) selects the symbol-kind icons lsp prints.
	lsp.Setting = settings.Any
}

func wireToolCommands() {
	shell.CommandPreamble = toolcmd.PreambleWithRelease
	shell.CommandNames = toolcmd.CommandNames
	shell.InvokedCommand = toolcmd.InvokedCommand
	shell.DirectCommand = func(ctx sdk.Context, command, cwd string, timeout time.Duration) (string, int, bool, bool, bool) {
		r := toolcmd.TryDirectWithOptions(ctx, command, toolcmd.DirectOptions{
			Cwd:     cwd,
			Timeout: timeout,
		})
		return r.Output, r.ExitCode, r.TimedOut, r.Cancelled, r.Handled
	}
	shell.ToolCommandDispatches = toolcmd.DrainDispatches
	shell.PrepareLoadout = toolcmd.PrepareLoadout
	// A subagent created with an explicit tool list keeps the caller's order
	// (omp: tools/index.ts:862-869); nil for every other session.
	toolcmd.ExplicitToolOrder = agents.ExplicitTools
	prompt.ToolCommands = toolcmd.PromptDocs
}

func wireLineageAndIntent() {
	modes.ParentSession = agents.ParentSession
	modes.VibeActive = vibe.Active
	settings.ParentSession = agents.ParentSession
	intent.ParentSession = agents.ParentSession
	prompt.SubagentSession = agents.IsSubagent

	intent.Configured = func(ctx sdk.Context) (bool, bool) {
		store := settings.StoreFor(ctx)
		if store.Provenance("tools.intentTracing") == settings.ProvenanceDefault {
			return false, false
		}
		return store.Bool("tools.intentTracing"), true
	}

	// upstream: sdk.ts:4655 — prompt follows intent.PromptEnabled (does not
	// check PI_NO_INTENT; PI_NO_INTENT only suppresses wire schema injection).
	prompt.Intent = func(ctx sdk.Context) (bool, string, string) {
		return intent.PromptEnabled(ctx), intent.Field, ""
	}

	// Notify intent when `tools.intentTracing` changes mid-session so the next
	// `before_agent_start` re-registers schemas in lockstep with the prompt
	// (upstream: sdk.ts:5660-5666).
	settings.OnChange(intent.NotifySettingChange)

	// upstream: sdk.ts:4673, main.ts:2445 — cfg:// write approval is available
	// only in top-level interactive TUI sessions (not print/RPC/subagents).
	prompt.SettingsApproval = func(ctx sdk.Context) bool {
		return !agents.IsSubagent(ctx) && isInteractiveTUI(ctx)
	}

	// upstream: sdk.ts:4664, irc/messaging.ts:16-20 — IRC peer messaging is
	// enabled for subagents (taskDepth > 0) and for top-level sessions able to
	// spawn peers (`canSpawnAtDepth(task.maxRecursionDepth, 0)`).
	prompt.TaskIRC = func(ctx sdk.Context) bool {
		if agents.IsSubagent(ctx) {
			return true
		}
		maxDepth := settings.Int(ctx, "task.maxRecursionDepth")
		return agents.CanSpawnAtDepth(maxDepth, 0)
	}
}

func wireSpillAndURLs() {
	// Shared per-session exclusive 0-based `<N>.log` allocator rooted at the
	// session's `local://` directory (upstream: session/artifacts.ts:86-163).
	shell.LocalSpillRoot = urls.LocalRoot
	jobs.SpillFunc = shell.WriteSessionSpill

	urls.SetLocalRootProvider(func(ctx sdk.Context) (string, bool) {
		root, ok := agents.ParentLocalRoot(ctx)
		if !ok || root == "" {
			return "", false
		}
		if abs, err := filepath.Abs(root); err == nil {
			return abs, true
		}
		return root, true
	})

	// Single policy function shared by `urls.SetOutPolicyProvider` (write gate)
	// and `prompt.OutReplies` (prompt advertisement; upstream: sdk.ts:4674).
	outPolicy := func(ctx sdk.Context) urls.OutPolicy {
		if agents.IsSubagent(ctx) {
			return urls.OutPolicy{Enabled: false, Subagent: true}
		}
		store := settings.StoreFor(ctx)
		if store.Provenance("out.enabled") != settings.ProvenanceDefault {
			return urls.OutPolicy{Enabled: store.Bool("out.enabled")}
		}
		return urls.OutPolicy{Enabled: isInteractiveTUI(ctx)}
	}
	urls.SetOutPolicyProvider(func(c urls.Context) urls.OutPolicy {
		return outPolicy(c.SDK)
	})
	prompt.OutReplies = func(ctx sdk.Context) bool {
		return outPolicy(ctx).Enabled
	}
	approval.OutPolicy = outPolicy
	prompt.RuleOptions = func(ctx sdk.Context) prompt.RuleDiscoveryOptions {
		store := settings.StoreFor(ctx)
		builtin := store.Bool("ttsr.builtinRules")
		return prompt.RuleDiscoveryOptions{
			BuiltinRules:  &builtin,
			DisabledRules: store.Strings("ttsr.disabledRules"),
		}
	}
	prompt.IncludeWorkspaceTree = func(ctx sdk.Context) bool {
		return settings.Enabled(ctx, "includeWorkspaceTree", false)
	}
	urls.SetRulesProvider(func(c urls.Context) []urls.RuleEntry {
		return prompt.DiscoverRulesForContext(c.SDK, c.Cwd)
	})

	urls.SetConflictWriteHooks(urls.ConflictWriteHooks{
		OnFileWritten: func(c urls.Context, absPath, before, after string) string {
			commit.RecordFileChange(c.SDK, "edit", absPath, &before, &after)
			store := hashline.ForSession(c.SDK)
			_, bomStripped := hashline.StripBOM(after)
			normalized := hashline.NormalizeToLF(bomStripped)
			tag := store.RecordSnapshot(absPath, normalized)
			display := formatDisplayPath(absPath, c.Cwd)
			return hashline.FormatHeader(display, tag)
		},
	})
	// Selective-commit journal wiring: resolve session artifacts dir, agent identity,
	// and running status from internal/agents, and journal writes from write/edit/ast_edit/conflict://.
	commit.ArtifactsDirResolver = agents.SessionTreeArtifactsDir
	commit.AgentIdentity = func(ctx sdk.Context) commit.JournalAgent {
		id, parent := agents.AgentIdentityFor(ctx)
		return commit.JournalAgent{Agent: id, Parent: parent}
	}
	commit.IsAgentRunning = agents.IsAgentRunningInSession
	files.SetRecordFileChangeHook(commit.RecordFileChange)
	files.SetRecordFileChangeActive(commit.JournalActive)
	ast.RecordFileChange = commit.RecordFileChange
}

func wirePlanAndDiagnostics() {
	ast.PlanProposalPending = modes.PlanActive
	// Both hooks run strictly after the file's mutation-queue turn is released (internal/files/write.go, edit.go):
	// lsp's format-on-write re-enters that queue for the same path. write follows lsp.diagnosticsOnWrite and edit
	// follows lsp.diagnosticsOnEdit, as omp's two paths do.
	files.SetDiagnosticsHook(lsp.DiagnosticsAfterWrite)
	files.SetEditDiagnosticsHook(lsp.DiagnosticsAfterEdit)
}

func isInteractiveTUI(ctx sdk.Context) (ok bool) {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return ctx.HasUI() && ctx.Mode() == "tui"
}

func formatDisplayPath(absPath, cwd string) string {
	if cwd != "" {
		if absCwd, err := filepath.Abs(cwd); err == nil {
			stdCwd := dirs.StandardizeMacOSPath(absCwd)
			stdAbs := dirs.StandardizeMacOSPath(absPath)
			if rel, err := filepath.Rel(stdCwd, stdAbs); err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(absPath)
}
