// Package approval ports omp's unified tool approval engine for every tool
// (`read`, `write`, `edit`, `bash`, `eval`, `task`, `ast_edit`, `ast_grep`,
// `lsp`, `debug`, `github`, `glob`, `grep`, `find`, `ask`, `todo`, `wait`,
// `web_search`, `checkpoint`, `rewind`, `yield`, `goal`, `resolve`, `reject`,
// `propose`, `report_issue`, mounted tools, eval preludes, MCP and extension
// tools) at omp v19.1.1 (31ffee513f).
//
// upstream: packages/coding-agent/src/tools/approval.ts
// upstream: packages/coding-agent/src/extensibility/extensions/wrapper.ts
// upstream: packages/coding-agent/src/eval/preludes.ts
package approval

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// Tier is a tool's capability tier, ordered read < write < exec.
// upstream: packages/agent/src/types.ts ToolTier, tools/approval.ts:131
type Tier string

const (
	TierRead  Tier = "read"
	TierWrite Tier = "write"
	TierExec  Tier = "exec"
)

// Policy is an approval decision outcome.
// upstream: packages/coding-agent/src/tools/approval.ts:40
type Policy string

const (
	PolicyAllow  Policy = "allow"
	PolicyDeny   Policy = "deny"
	PolicyPrompt Policy = "prompt"
)

// Mode is omp's tools.approvalMode enum.
// upstream: packages/coding-agent/src/tools/approval.ts:43
type Mode string

const (
	ModeAlwaysAsk       Mode = "always-ask"
	ModeAlwaysAskCached Mode = "always-ask-cached"
	ModeWrite           Mode = "write"
	ModeYolo            Mode = "yolo"
)

// ToolDecision is the normalized output of a tool's own approval hook.
// upstream: packages/coding-agent/src/tools/approval.ts:272-291
type ToolDecision struct {
	Tier     Tier
	Policy   Policy // optional explicit tool policy ("allow", "deny", "prompt", or "")
	Override bool   // true when critical, interceptor, or pattern override forces prompt/deny
	Reason   string
}

// Resolved mirrors omp's ResolvedApproval.
// upstream: packages/coding-agent/src/tools/approval.ts:114-125
type Resolved struct {
	Policy    Policy
	Tier      Tier
	Override  bool
	Source    string // "tool" | "user" | "mode" | "restriction"
	Reason    string
	PolicyKey string
}

// Setting retrieves an omp setting value from operator layers (no project
// layer) for security-sensitive policy keys (`tools.approvalMode`,
// `tools.approval`, `tools.restrictedFolders`, `bash.patterns`,
// `bash.allowedEnvVars`).
// Assigned by the integrator (`approval.Setting = settings.OperatorAny`).
// When nil, approval FAILS CLOSED to `always-ask` with empty user policies
// (matching omp `resolveApprovalFromContext` at `tools/approval.ts:103-106`).
var Setting func(ctx sdk.Context, key string) any

// SessionSetting retrieves the full session-layer view (including project
// layers, i.e. `settings.Any`). Evaluated alongside Setting so project layers
// can tighten (stricter approvalMode, deny/prompt user policy, deny/prompt
// bash patterns) without ever loosening operator layers.
// Assigned by the integrator (`approval.SessionSetting = settings.Any`);
// when nil, only Setting is evaluated.
var SessionSetting func(ctx sdk.Context, key string) any

// OperatorConfigError, when assigned by the integrator (e.g. checking
// `settings.StoreFor(ctx).LoadError()`), reports whether the operator config
// file failed to load (e.g. malformed global `config.yml`). When non-nil,
// approval FAILS CLOSED to `always-ask` with empty user policies rather than
// silently falling back to `yolo`.
var OperatorConfigError func(ctx sdk.Context) error

// IsSubagent reports whether ctx belongs to a subagent child session.
// In omp (`task/executor.ts:1298-1301`), subagents run headless with
// `tools.approvalMode: "yolo"` (the parent `task` approval is the
// authorization boundary), while user `tools.approval.<tool>` policies,
// `tools.restrictedFolders`, and tool-owned denies/prompts still apply.
// Assigned by the integrator (`approval.IsSubagent = agents.IsSubagent`).
var IsSubagent func(ctx sdk.Context) bool

// OutPolicy evaluates whether `out://` replies are allowed for ctx, matching
// omp's `out-protocol.ts:50-65` `replyTier`. When wired (matching
// `urls.SetOutPolicyProvider`), disabled or subagent `out://` writes return a
// tool-owned deny at the approval gate.
var OutPolicy func(ctx sdk.Context) urls.OutPolicy

// BashEvaluator evaluates bash's tool-owned approval decision (bashInterceptor,
// bash.patterns, CRITICAL_BASH_PATTERNS, tool-command-line read tier).
// Registered by `internal/shell` via `SetBashEvaluator` on init so bash keeps
// its pattern/interceptor rules in `internal/shell` while sharing the single
// `internal/approval` resolver and `EventToolCall` veto.
type BashEvaluator func(ctx sdk.Context, input map[string]any) ToolDecision

// BashDetailsFormatter optionally formats bash prompt detail lines.
type BashDetailsFormatter func(input map[string]any) []string

// ToolDecisionFunc evaluates a tool's own approval decision for a call.
type ToolDecisionFunc func(ctx sdk.Context, input map[string]any) ToolDecision

var (
	hookMu              sync.RWMutex
	bashEvaluator       BashEvaluator
	bashDetailFmt       BashDetailsFormatter
	sessionGrantsMu     sync.Mutex
	sessionGrants       = map[string]map[string]bool{} // sessionID -> "<tool>\x00<tier>" -> true
	sessionTargetGrants = map[string]map[string]bool{} // sessionID -> "<tier>\x00<target|dir|tree>" -> true
	configWarned        = map[string]bool{}            // sessionID -> true (warned once on operator config failure)
	maxTierRank         = map[Tier]int{TierRead: 0, TierWrite: 1, TierExec: 2}
	modeMaxTier         = map[Mode]Tier{ModeAlwaysAsk: TierRead, ModeAlwaysAskCached: TierRead, ModeWrite: TierWrite, ModeYolo: TierExec}
	modeStrictness      = map[Mode]int{ModeYolo: 0, ModeWrite: 1, ModeAlwaysAsk: 2, ModeAlwaysAskCached: 3}
	policyStrictness    = map[Policy]int{"": 0, PolicyAllow: 1, PolicyPrompt: 2, PolicyDeny: 3}
)

// SetBashEvaluator installs the bash approval evaluator and optional prompt
// details formatter (called by internal/shell on init).
func SetBashEvaluator(eval BashEvaluator, detailFmt BashDetailsFormatter) {
	hookMu.Lock()
	bashEvaluator = eval
	bashDetailFmt = detailFmt
	hookMu.Unlock()
}

// Register installs the single `EventToolCall` veto enforcing omp's approval
// policy for EVERY tool and registers `lifecycle.OnEnd("approval", ...)` to
// release session-scoped and target-cached grants when a session ends.
// Safe to call repeatedly across fresh per-session extension instances.
func Register(e *sdk.Extension) {
	lifecycle.OnEnd("approval", ClearSession)
	e.OnEvent(sdk.EventToolCall, HandleToolCallVeto)
	if e != nil {
		e.RegisterCommand("forget-approvals", sdk.CommandOptions{
			Description: "Forget every file and folder approval Ask and remember has recorded",
			Handler:     handleForgetApprovalsCommand,
		})
	}
}

// handleForgetApprovalsCommand implements `/forget-approvals`.
// upstream: packages/coding-agent/src/slash-commands/builtin-control.ts:122-130
func handleForgetApprovalsCommand(ctx sdk.Context, _ string) error {
	if sid, err := ctx.GetSessionID(); err == nil && sid != "" {
		ClearSession(sid)
	}
	ctx.Notify("Remembered approvals forgotten; the next read or write asks again.", "info")
	return nil
}

// ClearSession drops all session-scoped and target-cached approval grants
// recorded for sessionID.
func ClearSession(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionGrantsMu.Lock()
	delete(sessionGrants, sessionID)
	delete(sessionTargetGrants, sessionID)
	delete(configWarned, sessionID)
	sessionGrantsMu.Unlock()
}

// HasSessionApproval reports whether sessionID holds an explicit session grant
// for scopeKey (`<tool>\x00<tier>`).
func HasSessionApproval(sessionID, scopeKey string) bool {
	if sessionID == "" || scopeKey == "" {
		return false
	}
	sessionGrantsMu.Lock()
	defer sessionGrantsMu.Unlock()
	m := sessionGrants[sessionID]
	return m != nil && m[scopeKey]
}

// GrantSessionApproval records an explicit session grant for scopeKey in sessionID.
func GrantSessionApproval(sessionID, scopeKey string) {
	if sessionID == "" || scopeKey == "" {
		return
	}
	sessionGrantsMu.Lock()
	defer sessionGrantsMu.Unlock()
	m := sessionGrants[sessionID]
	if m == nil {
		m = make(map[string]bool)
		sessionGrants[sessionID] = m
	}
	m[scopeKey] = true
}

// HasTargetGrant reports whether sessionID holds a session-scoped cached
// target grant covering grantKey (`<tier>\x00<subject>`).
func HasTargetGrant(sessionID, grantKey string) bool {
	if sessionID == "" || grantKey == "" {
		return false
	}
	sessionGrantsMu.Lock()
	defer sessionGrantsMu.Unlock()
	m := sessionTargetGrants[sessionID]
	return m != nil && m[grantKey]
}

// GrantTarget records a session-scoped cached target grant (`<tier>\x00<subject>`)
// for sessionID, released on `lifecycle.End(sessionID)`.
func GrantTarget(sessionID, grantKey string) {
	if sessionID == "" || grantKey == "" {
		return
	}
	sessionGrantsMu.Lock()
	defer sessionGrantsMu.Unlock()
	m := sessionTargetGrants[sessionID]
	if m == nil {
		m = make(map[string]bool)
		sessionTargetGrants[sessionID] = m
	}
	m[grantKey] = true
}

func targetsAllGranted(sessionID string, targets []string, tier Tier) bool {
	if sessionID == "" || len(targets) == 0 {
		return false
	}
	sessionGrantsMu.Lock()
	m := sessionTargetGrants[sessionID]
	if len(m) == 0 {
		sessionGrantsMu.Unlock()
		return false
	}
	for _, target := range targets {
		covered := false
		for _, k := range coveringGrantKeys(target, tier) {
			if m[k] {
				covered = true
				break
			}
		}
		if !covered {
			sessionGrantsMu.Unlock()
			return false
		}
	}
	sessionGrantsMu.Unlock()
	return true
}

// recordTargetGrant records a session-scoped cached grant for `always-ask-cached`
// (`tools/approval.ts:239-257`). Persistent `approval-cache.db` is not ported,
// so both "Approve ..." and "Always Approve ..." record for the current session.
func recordTargetGrant(sessionID string, targets []string, capability Tier, scope string) {
	if sessionID == "" || len(targets) == 0 {
		return
	}
	if scope == "folder" {
		if folderKey, _, ok := approvalFolderCandidate(targets); ok {
			GrantTarget(sessionID, ApprovalGrantKey(capability, folderKey))
			return
		}
	}
	for _, target := range targets {
		GrantTarget(sessionID, ApprovalGrantKey(capability, target))
		if folderKey, isTree := ApprovalTreeSearchFolderKey(target); isTree {
			GrantTarget(sessionID, ApprovalGrantKey(capability, folderKey))
		}
	}
}

// ResetForTests clears all session grants and target grants (tests only).
func ResetForTests() {
	sessionGrantsMu.Lock()
	sessionGrants = map[string]map[string]bool{}
	sessionTargetGrants = map[string]map[string]bool{}
	configWarned = map[string]bool{}
	sessionGrantsMu.Unlock()
}

// TierRank returns the privilege rank of tier (read=0, write=1, exec=2).
// upstream: tools/approval.ts:131-135
func TierRank(t Tier) int {
	if r, ok := maxTierRank[t]; ok {
		return r
	}
	return 2 // unknown fails closed to exec
}

// NormalizeTier converts a raw tier string into a valid Tier, defaulting to exec.
func NormalizeTier(raw string) Tier {
	switch Tier(strings.ToLower(strings.TrimSpace(raw))) {
	case TierRead:
		return TierRead
	case TierWrite:
		return TierWrite
	case TierExec:
		return TierExec
	default:
		return TierExec
	}
}

// NormalizePolicy converts a raw user policy value into "allow" | "deny" | "prompt",
// or "" if invalid/unset.
// upstream: tools/approval.ts:262-266
func NormalizePolicy(raw any) Policy {
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	switch Policy(strings.ToLower(strings.TrimSpace(s))) {
	case PolicyAllow:
		return PolicyAllow
	case PolicyDeny:
		return PolicyDeny
	case PolicyPrompt:
		return PolicyPrompt
	default:
		return ""
	}
}

// NormalizeMode converts a raw mode value into a valid Mode, or "" if invalid.
// Matches omp's exact case-sensitive `isApprovalMode` (`tools/approval.ts:71-73`).
func NormalizeMode(raw any) Mode {
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	switch Mode(s) {
	case ModeAlwaysAsk, ModeAlwaysAskCached, ModeWrite, ModeYolo:
		return Mode(s)
	default:
		return ""
	}
}

// StrictestDecision folds per-target decisions for multi-target write tools
// (`edit`, `ast_edit`): the first `policy: "deny"` wins with its reason;
// otherwise the highest tier starting from "read".
// upstream: tools/approval.ts:142-150
func StrictestDecision(decisions []ToolDecision) ToolDecision {
	tier := TierRead
	for _, d := range decisions {
		if d.Policy == PolicyDeny {
			return d
		}
		dt := d.Tier
		if dt == "" {
			dt = TierExec
		}
		if TierRank(dt) > TierRank(tier) {
			tier = dt
		}
	}
	return ToolDecision{Tier: tier}
}

// ResolvePolicy ports omp's `resolvePolicy` (packages/coding-agent/src/tools/approval.ts:382-492)
// without cache targets or restrictions.
func ResolvePolicy(toolName string, decision ToolDecision, mode Mode, userPolicy Policy) Resolved {
	return resolvePolicyFull(toolName, decision, mode, userPolicy, "", nil)
}

// ResolvePolicyWithLegacy mirrors omp's legacy-name fallback (`tools/approval.ts:388-406`):
// if the canonical user policy is unset, inherits `deny` or `prompt` (NEVER `allow`)
// written against `legacyName`, recording `PolicyKey = legacyName` so DenyError names
// the legacy config key.
func ResolvePolicyWithLegacy(toolName, legacyName string, decision ToolDecision, mode Mode, userPolicy, legacyPolicy Policy) Resolved {
	effPolicy := userPolicy
	policyKey := ""
	if effPolicy != "" {
		policyKey = toolName
	} else if legacyName != "" && (legacyPolicy == PolicyDeny || legacyPolicy == PolicyPrompt) {
		effPolicy = legacyPolicy
		policyKey = legacyName
	}
	res := resolvePolicyFull(toolName, decision, mode, effPolicy, "", nil)
	if res.Source == "user" && policyKey != "" {
		res.PolicyKey = policyKey
	}
	return res
}

// ResolveApproval ports omp's full `resolveApproval` (`tools/approval.ts:356-492`):
// resolves policy (including `always-ask-cached` target grants) and then applies
// `tools.restrictedFolders` so any non-deny result touching a restricted folder
// becomes a per-call prompt (`source: "restriction"`, `override: true`) in every
// mode and over every allow policy or grant.
func ResolveApproval(
	toolName string,
	input map[string]any,
	decision ToolDecision,
	mode Mode,
	userPolicy Policy,
	sessionID string,
	cacheTargets []string,
	restrictions *ApprovalRestrictions,
) Resolved {
	resolved := resolvePolicyFull(toolName, decision, mode, userPolicy, sessionID, cacheTargets)
	if resolved.Policy == PolicyDeny {
		return resolved
	}
	if reason := RestrictedFolderReason(toolName, input, restrictions); reason != "" {
		return Resolved{
			Policy:    PolicyPrompt,
			Tier:      resolved.Tier,
			Override:  true,
			Source:    "restriction",
			Reason:    reason,
			PolicyKey: resolved.PolicyKey,
		}
	}
	return resolved
}

func resolvePolicyFull(
	toolName string,
	decision ToolDecision,
	mode Mode,
	userPolicy Policy,
	sessionID string,
	cacheTargets []string,
) Resolved {
	tier := decision.Tier
	if tier == "" {
		tier = TierExec
	}
	if mode == "" {
		mode = ModeYolo
	}
	policyKey := ""
	if userPolicy != "" {
		policyKey = toolName
	}

	// 1. Tool-owned deny (approval.ts:409-417).
	if decision.Policy == PolicyDeny {
		return Resolved{
			Policy:   PolicyDeny,
			Tier:     tier,
			Override: decision.Override,
			Source:   "tool",
			Reason:   decision.Reason,
		}
	}
	// 2. User deny (approval.ts:418-426).
	if userPolicy == PolicyDeny {
		return Resolved{
			Policy:    PolicyDeny,
			Tier:      tier,
			Override:  decision.Override,
			Source:    "user",
			PolicyKey: policyKey,
		}
	}
	// 3. Yolo mode (approval.ts:428-445).
	if mode == ModeYolo {
		if decision.Policy != "" {
			return Resolved{
				Policy:   decision.Policy,
				Tier:     tier,
				Override: false,
				Source:   "tool",
				Reason:   decision.Reason,
			}
		}
		if userPolicy != "" {
			return Resolved{
				Policy:    userPolicy,
				Tier:      tier,
				Override:  false,
				Source:    "user",
				PolicyKey: policyKey,
			}
		}
		return Resolved{
			Policy:   PolicyAllow,
			Tier:     tier,
			Override: false,
			Source:   "mode",
		}
	}
	// 4. Non-yolo override (approval.ts:447-455).
	if decision.Override {
		pol := PolicyPrompt
		if decision.Policy == PolicyAllow {
			pol = PolicyAllow
		}
		return Resolved{
			Policy:   pol,
			Tier:     tier,
			Override: true,
			Source:   "tool",
			Reason:   decision.Reason,
		}
	}
	// 5. Tool explicit allow/prompt (approval.ts:457-465).
	if decision.Policy == PolicyAllow || decision.Policy == PolicyPrompt {
		return Resolved{
			Policy:   decision.Policy,
			Tier:     tier,
			Override: false,
			Source:   "tool",
			Reason:   decision.Reason,
		}
	}
	// 6. User policy (approval.ts:467-475).
	if userPolicy != "" {
		return Resolved{
			Policy:    userPolicy,
			Tier:      tier,
			Override:  false,
			Source:    "user",
			PolicyKey: policyKey,
		}
	}
	// 7. Mode vs tier (approval.ts:477-491).
	if mode == ModeAlwaysAskCached {
		// upstream: tools/approval.ts:192-212 (`resolveCachedModeApproval`):
		//   - No cache targets: read -> allow, write/exec -> prompt.
		//   - With cache targets: allow iff every target is covered by a
		//     session target/folder grant at `tier` or above; otherwise prompt
		//     with reason `decision.Reason ?? "target not yet approved"`.
		if len(cacheTargets) == 0 {
			if tier == TierRead {
				return Resolved{Policy: PolicyAllow, Tier: tier, Override: false, Source: "mode"}
			}
			return Resolved{Policy: PolicyPrompt, Tier: tier, Override: false, Source: "mode", Reason: decision.Reason}
		}
		if targetsAllGranted(sessionID, cacheTargets, tier) {
			return Resolved{Policy: PolicyAllow, Tier: tier, Override: false, Source: "mode"}
		}
		reason := decision.Reason
		if reason == "" {
			reason = "target not yet approved"
		}
		return Resolved{Policy: PolicyPrompt, Tier: tier, Override: false, Source: "mode", Reason: reason}
	}
	maxTier := modeMaxTier[mode]
	if TierRank(tier) <= TierRank(maxTier) {
		return Resolved{Policy: PolicyAllow, Tier: tier, Override: false, Source: "mode"}
	}
	return Resolved{
		Policy:   PolicyPrompt,
		Tier:     tier,
		Override: false,
		Source:   "mode",
		Reason:   decision.Reason,
	}
}

// DenyError formats omp's exact denial message byte-for-byte.
// upstream: packages/coding-agent/src/tools/approval.ts:594-603
func DenyError(resolved Resolved, toolName string) string {
	if resolved.Source == "tool" {
		if resolved.Reason != "" {
			return fmt.Sprintf("Tool %q is blocked by tool policy.\nReason: %s", toolName, resolved.Reason)
		}
		return fmt.Sprintf("Tool %q is blocked by tool policy.", toolName)
	}
	key := resolved.PolicyKey
	if key == "" {
		key = toolName
	}
	return fmt.Sprintf("Tool %q is blocked by user policy.\nTo allow: remove \"tools.approval.%s: deny\" from config.", key, key)
}

// HeadlessPromptError formats omp's exact fail-closed refusal when a call
// resolves to `prompt` with no interactive UI available.
// upstream: packages/coding-agent/src/extensibility/extensions/wrapper.ts:484-490
func HeadlessPromptError(toolName string) string {
	return fmt.Sprintf(
		"Tool %q requires approval but no interactive UI available.\n"+
			"Options:\n"+
			"  1. Set tools.approvalMode: yolo in /settings\n"+
			"  2. Add tools.approval.%s: allow to config\n"+
			"  3. Use an interactive UI to approve the tool call",
		toolName, toolName,
	)
}

// SessionApprovalScope returns omp's session grant key (`<tool>\x00<tier>`)
// when a resolved prompt may be approved for the rest of the session: ONLY
// mode-sourced prompts. Critical overrides, restricted folders, and explicit
// tool/user `prompt` policies return "".
// upstream: packages/coding-agent/src/tools/approval.ts:583-589
func SessionApprovalScope(resolved Resolved, toolName string) string {
	if resolved.Policy != PolicyPrompt || resolved.Source != "mode" {
		return ""
	}
	return toolName + "\x00" + string(resolved.Tier)
}

// ── Two-view settings resolution (operator authority + project tightening) ───

// operatorConfigLoadError checks whether the operator settings source is
// unavailable or failed to load (e.g. malformed/unreadable global config.yml).
//
//   - When Setting == nil, returns (nil, true) -> fail closed to always-ask.
//   - When OperatorConfigError != nil, consults it first.
//   - When OperatorConfigError == nil and ctx is backed by a real host session,
//     checks settings.StoreFor(ctx).LoadError() directly so malformed operator
//     config ALWAYS fails closed even if OperatorConfigError is not separately wired.
func operatorConfigLoadError(ctx sdk.Context) (err error, failed bool) {
	if Setting == nil {
		return nil, true
	}
	if OperatorConfigError != nil {
		if loadErr := OperatorConfigError(ctx); loadErr != nil {
			return loadErr, true
		}
		return nil, false
	}
	var zero sdk.Context
	if ctx != zero {
		if loadErr := settings.StoreFor(ctx).LoadError(); loadErr != nil {
			return loadErr, true
		}
	}
	return nil, false
}

func operatorConfigFailed(ctx sdk.Context) bool {
	_, failed := operatorConfigLoadError(ctx)
	return failed
}

func warnOperatorConfigOnce(ctx sdk.Context, sessionID string, loadErr error) {
	if loadErr == nil {
		return
	}
	if sessionID != "" {
		sessionGrantsMu.Lock()
		if configWarned[sessionID] {
			sessionGrantsMu.Unlock()
			return
		}
		configWarned[sessionID] = true
		sessionGrantsMu.Unlock()
	}
	var zero sdk.Context
	if ctx == zero {
		return
	}
	func() {
		defer func() { _ = recover() }()
		ctx.Notify(fmt.Sprintf("Operator config could not be read (%v); falling back to approvalMode: always-ask.", loadErr), "warning")
	}()
}

func readOperatorSetting(ctx sdk.Context, key string) any {
	if operatorConfigFailed(ctx) {
		return nil
	}
	return Setting(ctx, key)
}

func readSessionSetting(ctx sdk.Context, key string) any {
	if operatorConfigFailed(ctx) {
		return nil
	}
	if SessionSetting == nil {
		return Setting(ctx, key)
	}
	return SessionSetting(ctx, key)
}

func modeInView(readFn func(sdk.Context, string) any, ctx sdk.Context) Mode {
	if IsSubagent != nil && IsSubagent(ctx) {
		return ModeYolo
	}
	m := NormalizeMode(readFn(ctx, "tools.approvalMode"))
	if m == "" {
		return ModeYolo
	}
	return m
}

func restrictedFoldersInView(readFn func(sdk.Context, string) any, ctx sdk.Context) *ApprovalRestrictions {
	folders := parseStringListRaw(readFn(ctx, "tools.restrictedFolders"))
	if len(folders) == 0 {
		return nil
	}
	return &ApprovalRestrictions{
		RestrictedFolders: folders,
		Cwd:               safeCwd(ctx),
	}
}

// EffectiveApprovalMode resolves `tools.approvalMode` for ctx:
//   - When the policy source is unavailable (`Setting == nil`) or the operator
//     config failed to load, FAILS CLOSED to `always-ask` (`tools/approval.ts:103-106`).
//   - Subagents run headless with `yolo` (`task/executor.ts:1298-1301`).
//   - Otherwise returns the stricter of the operator view and session view
//     for legacy single-mode callers; the gate itself evaluates BOTH views per
//     call and takes the stricter decision (deny > prompt > allow) so no total
//     order between `always-ask` and `always-ask-cached` is assumed.
func EffectiveApprovalMode(ctx sdk.Context) Mode {
	if operatorConfigFailed(ctx) {
		return ModeAlwaysAsk
	}
	if IsSubagent != nil && IsSubagent(ctx) {
		return ModeYolo
	}
	opMode := modeInView(readOperatorSetting, ctx)
	sessMode := modeInView(readSessionSetting, ctx)
	if modeStrictness[sessMode] > modeStrictness[opMode] {
		return sessMode
	}
	return opMode
}

func lookupToolPolicyInView(readFn func(sdk.Context, string) any, ctx sdk.Context, toolName string) Policy {
	if raw := readFn(ctx, "tools.approval"); raw != nil {
		switch m := raw.(type) {
		case map[string]any:
			if p := NormalizePolicy(m[toolName]); p != "" {
				return p
			}
		case map[string]string:
			if p := NormalizePolicy(m[toolName]); p != "" {
				return p
			}
		}
	}
	return NormalizePolicy(readFn(ctx, "tools.approval."+toolName))
}

// EffectiveUserPolicy resolves `tools.approval.<tool>` with two-view security:
// when the policy source is unavailable or malformed, returns "" (no grants);
// otherwise operator layers are authoritative and a project layer may TIGHTEN
// (allow -> prompt/deny, unset -> prompt/deny, prompt -> deny) and may NEVER
// LOOSEN (deny -> allow/prompt, prompt -> allow, or project-only allow when
// operator left the tool unset).
func EffectiveUserPolicy(ctx sdk.Context, toolName string) Policy {
	if operatorConfigFailed(ctx) {
		return ""
	}
	opPol := lookupToolPolicyInView(readOperatorSetting, ctx, toolName)
	sessPol := lookupToolPolicyInView(readSessionSetting, ctx, toolName)

	// Project-only "allow" cannot grant a permission the operator did not grant.
	if sessPol == PolicyAllow && opPol != PolicyAllow {
		return opPol
	}
	if policyStrictness[sessPol] > policyStrictness[opPol] {
		return sessPol
	}
	return opPol
}

// EffectiveRestrictedFolders returns the union of operator and session-layer
// `tools.restrictedFolders` so a project layer can add restrictions but never
// remove an operator's restricted folder.
func EffectiveRestrictedFolders(ctx sdk.Context) *ApprovalRestrictions {
	if operatorConfigFailed(ctx) {
		return nil
	}
	seen := map[string]bool{}
	var folders []string
	appendFrom := func(raw any) {
		for _, s := range parseStringListRaw(raw) {
			if !seen[s] {
				seen[s] = true
				folders = append(folders, s)
			}
		}
	}
	appendFrom(readOperatorSetting(ctx, "tools.restrictedFolders"))
	appendFrom(readSessionSetting(ctx, "tools.restrictedFolders"))
	if len(folders) == 0 {
		return nil
	}
	return &ApprovalRestrictions{
		RestrictedFolders: folders,
		Cwd:               safeCwd(ctx),
	}
}

// viewResolution holds one evaluated view (operator or session) for a call.
type viewResolution struct {
	resolved     Resolved
	mode         Mode
	cacheTargets []string
}

func applySessionGrantInView(res Resolved, mode Mode, toolName, sessionID string, cacheTargets []string) Resolved {
	if res.Policy != PolicyPrompt || sessionID == "" {
		return res
	}
	if mode == ModeAlwaysAskCached && res.Source == "mode" && len(cacheTargets) > 0 {
		return res
	}
	if scopeKey := SessionApprovalScope(res, toolName); scopeKey != "" && HasSessionApproval(sessionID, scopeKey) {
		return Resolved{
			Policy:   PolicyAllow,
			Tier:     res.Tier,
			Override: false,
			Source:   "session-grant",
		}
	}
	return res
}

// resolveTwoViews implements the binding integrator two-view rule:
// evaluate the call under the OPERATOR view (operator layers only) and under
// the SESSION view (all layers the session has, i.e. omp's own view) and take
// the STRICTER DECISION for that call (deny > prompt > allow). On a tie where
// both views have equal strictness, the session view (omp's own) stands.
//
// When operator config is missing or malformed, fails closed to omp's
// no-settings default (`always-ask`, no user policies, no restricted folders)
// and notifies the user once per session.
func resolveTwoViews(
	ctx sdk.Context,
	toolName string,
	input map[string]any,
	decision ToolDecision,
	sessionID string,
	cwd string,
) viewResolution {
	if loadErr, failed := operatorConfigLoadError(ctx); failed {
		warnOperatorConfigOnce(ctx, sessionID, loadErr)
		res := ResolveApproval(toolName, input, decision, ModeAlwaysAsk, "", sessionID, nil, nil)
		res = applySessionGrantInView(res, ModeAlwaysAsk, toolName, sessionID, nil)
		return viewResolution{resolved: res, mode: ModeAlwaysAsk}
	}

	opMode := modeInView(readOperatorSetting, ctx)
	opPol := lookupToolPolicyInView(readOperatorSetting, ctx, toolName)
	opRestr := restrictedFoldersInView(readOperatorSetting, ctx)

	sessMode := modeInView(readSessionSetting, ctx)
	sessPol := lookupToolPolicyInView(readSessionSetting, ctx, toolName)
	sessRestr := restrictedFoldersInView(readSessionSetting, ctx)

	var targets []string
	if opMode == ModeAlwaysAskCached || sessMode == ModeAlwaysAskCached {
		targets = toolApprovalCacheTargets(toolName, input, cwd)
	}

	var opTargets, sessTargets []string
	if opMode == ModeAlwaysAskCached {
		opTargets = targets
	}
	if sessMode == ModeAlwaysAskCached {
		sessTargets = targets
	}

	opRes := ResolveApproval(toolName, input, decision, opMode, opPol, sessionID, opTargets, opRestr)
	opRes = applySessionGrantInView(opRes, opMode, toolName, sessionID, opTargets)

	sessRes := ResolveApproval(toolName, input, decision, sessMode, sessPol, sessionID, sessTargets, sessRestr)
	sessRes = applySessionGrantInView(sessRes, sessMode, toolName, sessionID, sessTargets)

	// Pick the strictly higher-severity outcome (deny > prompt > allow).
	opRank := policyStrictness[opRes.Policy]
	sessRank := policyStrictness[sessRes.Policy]
	if opRank > sessRank {
		return viewResolution{resolved: opRes, mode: opMode, cacheTargets: opTargets}
	}
	if sessRank > opRank {
		return viewResolution{resolved: sessRes, mode: sessMode, cacheTargets: sessTargets}
	}
	// Equal severity (both deny, both prompt, or both allow):
	// Preserve a non-grantable restriction/override/explicit prompt in the
	// operator view over a grantable mode prompt in the session view; on an
	// equal-strictness tie, the session view (omp's own) stands.
	if opRes.Policy == PolicyPrompt {
		opGrantable := opRes.Source == "mode" && !opRes.Override
		sessGrantable := sessRes.Source == "mode" && !sessRes.Override
		if !opGrantable && sessGrantable {
			return viewResolution{resolved: opRes, mode: opMode, cacheTargets: opTargets}
		}
	}
	return viewResolution{resolved: sessRes, mode: sessMode, cacheTargets: sessTargets}
}

func parseStringListRaw(raw any) []string {
	switch v := raw.(type) {
	case []string:
		var out []string
		for _, s := range v {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	default:
		return nil
	}
}

// ── Per-tool upstream approval hooks & tiers ─────────────────────────────────

// upstream: packages/coding-agent/src/lsp/servers.ts:26-36
var lspReadonlyActions = map[string]bool{
	"diagnostics":     true,
	"definition":      true,
	"type_definition": true,
	"implementation":  true,
	"references":      true,
	"hover":           true,
	"symbols":         true,
	"status":          true,
	"capabilities":    true,
}

// upstream: packages/coding-agent/src/tools/debug.ts:57-68
var debugReadonlyActions = map[string]bool{
	"output":         true,
	"threads":        true,
	"stack_trace":    true,
	"scopes":         true,
	"variables":      true,
	"disassemble":    true,
	"read_memory":    true,
	"loaded_sources": true,
	"modules":        true,
	"sessions":       true,
}

// upstream: packages/coding-agent/src/tools/gh.ts:69-78
var githubReadonlyOps = map[string]bool{
	"repo_view":      true,
	"file_read":      true,
	"search_issues":  true,
	"search_prs":     true,
	"search_code":    true,
	"search_commits": true,
	"search_repos":   true,
	"run_watch":      true,
}

// upstreamReadOnlySchemes are omp built-in read-only internal URL schemes that
// may not have a handler registered in oh-my-pig (`memory://`, `omp://`,
// `security://`, `rule://`, `skill://`, `help://`, `history://`, `issue://`,
// `pr://`, `mr://`, `attachment://`, `mcp://`).
// Note: `artifact://` is NOT a registered scheme in omp v19.1.1's
// `InternalUrlRouter` (`internal-urls/router.ts:110-130`), so omp treats
// `write artifact://1` as a plain write-tier path rather than read-only.
// Writing to any scheme below is denied with omp's `<scheme>:// URLs are read-only`
// (`internal-urls/router.ts:418`).
var upstreamReadOnlySchemes = map[string]bool{
	"memory":     true,
	"omp":        true,
	"security":   true,
	"rule":       true,
	"skill":      true,
	"help":       true,
	"history":    true,
	"issue":      true,
	"pr":         true,
	"mr":         true,
	"attachment": true,
	"mcp":        true,
}

// upstream: packages/coding-agent/src/tools/read-pdf.ts:19-20
var pdfImageMemberRE = regexp.MustCompile(`(?i)^(.+\.pdf):([^:]+)$`)

// urlReadTier mirrors omp's InternalUrlRouter.readTier
// (`internal-urls/router.ts:423-432`): max spec.ReadTier over every registered
// `scheme://` occurring anywhere in text (plus `ssh://` -> exec). Default "read".
func urlReadTier(text string) Tier {
	lower := strings.ToLower(text)
	tier := TierRead
	for _, scheme := range urls.Schemes() {
		spec, ok := urls.LookupSpec(scheme)
		if !ok {
			continue
		}
		st := TierRead
		if spec.ReadTier != "" {
			st = NormalizeTier(spec.ReadTier)
		}
		if TierRank(st) <= TierRank(tier) {
			continue
		}
		if strings.Contains(lower, scheme+"://") {
			tier = st
		}
	}
	// ssh:// is exec-tier even if no ssh handler is registered (ssh-protocol.ts:269).
	if TierRank(TierExec) > TierRank(tier) && strings.Contains(lower, "ssh://") {
		tier = TierExec
	}
	return tier
}

// urlWriteTierWithOptions mirrors omp's InternalUrlRouter.writeTier
// (`internal-urls/router.ts:414-420`).
//   - `unwrapHeader`: true for `write` (`write.ts:546`), false for `edit` (already parsed)
//     and `ast_edit` (`ast-edit.ts:184` passes raw target).
//   - `withSession`: true for `write` and `edit` (passes `this.session` so `out://`
//     checks `OutPolicy`), false for `ast_edit` (`ast-edit.ts:184` passes `undefined`
//     so `out://` returns `"read"` without session refusal).
func urlWriteTierWithOptions(ctx sdk.Context, rawTarget string, unwrapHeader, withSession bool) ToolDecision {
	target := rawTarget
	if unwrapHeader {
		target = hashline.UnwrapHeaderPath(rawTarget)
	}
	if !urls.CanHandle(target) {
		norm := urls.Normalize(target)
		scheme := strings.ToLower(urls.ExtractScheme(norm))
		if scheme == "ssh" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(norm)), "ssh://") {
			return ToolDecision{Tier: TierExec}
		}
		if scheme != "" && upstreamReadOnlySchemes[scheme] && strings.HasPrefix(strings.ToLower(strings.TrimSpace(norm)), scheme+"://") {
			return ToolDecision{
				Tier:   TierWrite,
				Policy: PolicyDeny,
				Reason: fmt.Sprintf("%s:// URLs are read-only", scheme),
			}
		}
		return ToolDecision{Tier: TierWrite}
	}
	u, err := urls.Parse(target)
	if err != nil {
		return ToolDecision{Tier: TierWrite}
	}
	scheme := strings.ToLower(u.Scheme)
	h, ok := urls.Lookup(scheme)
	if !ok {
		return ToolDecision{Tier: TierWrite}
	}
	spec := h.Spec()
	if spec.Write == nil {
		return ToolDecision{
			Tier:   TierWrite,
			Policy: PolicyDeny,
			Reason: fmt.Sprintf("%s:// URLs are read-only", scheme),
		}
	}
	switch scheme {
	case "local", "agent":
		return ToolDecision{Tier: TierRead}
	case "out":
		if withSession && OutPolicy != nil {
			pol := OutPolicy(ctx)
			if pol.Subagent {
				return ToolDecision{
					Tier:   TierRead,
					Policy: PolicyDeny,
					Reason: "Subagents cannot reply to the user through out://; report through your result instead.",
				}
			}
			if !pol.Enabled {
				return ToolDecision{
					Tier:   TierRead,
					Policy: PolicyDeny,
					Reason: "out:// is off in this session (`out.enabled`; by default on only in the interactive TUI); reply in your final message instead.",
				}
			}
		}
		return ToolDecision{Tier: TierRead}
	case "proc", "ssh":
		return ToolDecision{Tier: TierExec}
	case "cfg", "conflict", "vault":
		return ToolDecision{Tier: TierWrite}
	default:
		switch spec.Write.Scope {
		case "sandbox", "coordination", "reply":
			return ToolDecision{Tier: TierRead}
		default:
			return ToolDecision{Tier: TierWrite}
		}
	}
}

func extractStringPathList(raw any) []string {
	var entries []string
	switch v := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "[") {
			var arr []any
			if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
				for _, item := range arr {
					if s, ok := item.(string); ok {
						entries = append(entries, s)
					}
				}
				return entries
			}
		}
		if trimmed != "" {
			entries = append(entries, v)
		}
	case []string:
		entries = append(entries, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				entries = append(entries, s)
			}
		}
	}
	return entries
}

func extractPathArgList(input map[string]any) []string {
	if input == nil {
		return nil
	}
	raw, ok := input["path"]
	if !ok || raw == nil {
		raw = input["paths"]
	}
	return extractStringPathList(raw)
}

// searchPathTier mirrors omp's searchPathTier (`tools/path-utils.ts:1518-1529`).
func searchPathTier(input map[string]any) ToolDecision {
	entries := extractPathArgList(input)
	return ToolDecision{Tier: urlReadTier(strings.Join(entries, "\n"))}
}

// readToolDecision mirrors ReadTool.approval (`tools/read.ts:1254-1262`).
func readToolDecision(input map[string]any) ToolDecision {
	readPath := ""
	if input != nil {
		if p, ok := input["path"].(string); ok {
			readPath = p
		}
	}
	if t := urlReadTier(readPath); t != TierRead {
		return ToolDecision{Tier: t}
	}
	if idx := strings.Index(readPath, "?q="); idx >= 0 {
		readPath = readPath[:idx]
	}
	if m := pdfImageMemberRE.FindStringSubmatch(readPath); m != nil {
		member := m[2]
		if !isReadLineSelector(member) {
			return ToolDecision{Tier: TierExec}
		}
	}
	return ToolDecision{Tier: TierRead}
}

func isReadLineSelector(s string) bool {
	if strings.EqualFold(s, "raw") || strings.EqualFold(s, "img") {
		return true
	}
	if len(s) == 0 {
		return false
	}
	c := s[0]
	return (c >= '0' && c <= '9') || c == '-' || c == '+'
}

// writeToolDecision mirrors WriteTool.approval (`tools/write.ts:541-551`).
func writeToolDecision(ctx sdk.Context, input map[string]any) ToolDecision {
	if input == nil {
		return ToolDecision{Tier: TierWrite}
	}
	rawPath, ok := input["path"].(string)
	if !ok {
		return ToolDecision{Tier: TierWrite}
	}
	return urlWriteTierWithOptions(ctx, rawPath, true, true)
}

type editMoveOp struct {
	from string
	to   string
}

func extractEditPathsAndMoves(raw string) (paths []string, moves []string) {
	p, mv := inspectEditInput(raw)
	for _, m := range mv {
		moves = append(moves, m.to)
	}
	return p, moves
}

func inspectEditInput(raw string) (paths []string, moves []editMoveOp) {
	patch, err := hashline.ParsePatchInput(raw, hashline.SplitOptions{})
	if err == nil && patch != nil && len(patch.Sections) > 0 {
		for _, sec := range patch.Sections {
			if sec.Path != "" {
				paths = append(paths, sec.Path)
			}
			if op, opErr := sec.FileOp(); opErr == nil && op != nil && op.Kind == hashline.FileOpMove && op.Dest != "" {
				moves = append(moves, editMoveOp{from: sec.Path, to: op.Dest})
			}
		}
		if len(paths) > 0 || len(moves) > 0 {
			return paths, moves
		}
	}
	if first, ok := hashline.FirstHeaderPath(raw, hashline.SplitOptions{}); ok && first != "" {
		return []string{first}, nil
	}
	return nil, nil
}

// editToolDecision mirrors EditTool.approval (`edit/index.ts:460-467`).
// Extracts every section target path and MV destination using the same
// `hashline.ParsePatchInput` parser that `internal/files/edit.go` uses.
func editToolDecision(ctx sdk.Context, input map[string]any) ToolDecision {
	if input == nil {
		return ToolDecision{Tier: TierWrite}
	}
	raw, _ := input["input"].(string)
	if strings.TrimSpace(raw) == "" {
		return ToolDecision{Tier: TierWrite}
	}
	paths, moveDests := extractEditPathsAndMoves(raw)
	targets := append(append([]string(nil), paths...), moveDests...)
	if len(targets) == 0 {
		return ToolDecision{Tier: TierWrite}
	}
	decisions := make([]ToolDecision, 0, len(targets))
	for _, target := range targets {
		decisions = append(decisions, urlWriteTierWithOptions(ctx, target, false, true))
	}
	return StrictestDecision(decisions)
}

// astEditToolDecision mirrors AstEditTool.approval (`tools/ast-edit.ts:176-187`):
// passes the raw entry with no header unwrapping and no session (`router.writeTier(target, undefined, undefined)`).
func astEditToolDecision(ctx sdk.Context, input map[string]any) ToolDecision {
	if input == nil {
		return ToolDecision{Tier: TierWrite}
	}
	rawPaths, exists := input["paths"]
	if !exists || rawPaths == nil {
		return ToolDecision{Tier: TierWrite}
	}
	switch v := rawPaths.(type) {
	case []string:
		if len(v) == 0 {
			return ToolDecision{Tier: TierWrite}
		}
		decisions := make([]ToolDecision, 0, len(v))
		for _, p := range v {
			decisions = append(decisions, urlWriteTierWithOptions(ctx, p, false, false))
		}
		return StrictestDecision(decisions)
	case []any:
		if len(v) == 0 {
			return ToolDecision{Tier: TierWrite}
		}
		decisions := make([]ToolDecision, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				// Malformed entry fails closed to exec (ast-edit.ts:183).
				return ToolDecision{Tier: TierExec}
			}
			decisions = append(decisions, urlWriteTierWithOptions(ctx, s, false, false))
		}
		return StrictestDecision(decisions)
	default:
		return ToolDecision{Tier: TierWrite}
	}
}

// EvaluateToolDecision evaluates the tool-owned ToolDecision for any tool name
// and input map according to omp v19.1.1's tool declarations.
//   - MCP tools (`mcp__*`): `write` tier (`mcp/tool-bridge.ts:811`).
//   - Unannotated / custom extension tools: default `exec` (`tools/approval.ts:290, 345`).
func EvaluateToolDecision(ctx sdk.Context, toolName string, input map[string]any) ToolDecision {
	hookMu.RLock()
	bashEval := bashEvaluator
	hookMu.RUnlock()

	switch toolName {
	case "bash":
		if bashEval != nil {
			return bashEval(ctx, input)
		}
		return ToolDecision{Tier: TierExec}

	case "read":
		return readToolDecision(input)

	case "write":
		return writeToolDecision(ctx, input)

	case "edit":
		return editToolDecision(ctx, input)

	case "ast_edit":
		return astEditToolDecision(ctx, input)

	case "glob", "grep", "find", "ast_grep":
		return searchPathTier(input)

	case "lsp":
		// upstream: lsp/tool.ts:314-318
		action := ""
		if input != nil {
			if a, ok := input["action"].(string); ok {
				action = strings.ToLower(a)
			}
		}
		if lspReadonlyActions[action] {
			return ToolDecision{Tier: TierRead}
		}
		return ToolDecision{Tier: TierWrite}

	case "debug":
		// upstream: tools/debug.ts:545-549
		action := ""
		if input != nil {
			if a, ok := input["action"].(string); ok {
				action = strings.ToLower(a)
			}
		}
		if debugReadonlyActions[action] {
			return ToolDecision{Tier: TierRead}
		}
		return ToolDecision{Tier: TierExec}

	case "github":
		// upstream: tools/gh.ts:140-144
		op := ""
		if input != nil {
			if o, ok := input["op"].(string); ok {
				op = o
			}
		}
		if githubReadonlyOps[op] {
			return ToolDecision{Tier: TierRead}
		}
		return ToolDecision{Tier: TierExec}

	case "goal":
		// upstream: goals/tools/goal-tool.ts:65-66
		if input != nil {
			if op, ok := input["op"].(string); ok && op == "get" {
				return ToolDecision{Tier: TierRead}
			}
		}
		return ToolDecision{Tier: TierWrite}

	case "context_notes":
		// upstream: tools/context-notes.ts:82-83
		if input != nil {
			if _, hasText := input["text"]; hasText {
				return ToolDecision{Tier: TierWrite}
			}
		}
		return ToolDecision{Tier: TierRead}

	case "new_context", "manage_skill", "report_issue":
		// upstream: context-notes.ts:156, manage-skill.ts:42, report-tool-issue.ts:713
		return ToolDecision{Tier: TierWrite}

	case "ask", "todo", "wait", "web_search", "checkpoint", "rewind",
		"yield", "think", "resolve", "reject", "propose",
		"recall", "reflect", "memory_edit",
		"vibe_wait", "vibe_kill", "vibe_list":
		// upstream: ask.ts:423, todo.ts:687, wait.ts:317, web/search/index.ts:594,
		// checkpoint.ts:55,91, yield.ts:316, think.ts:53, resolve.ts:343,360,378,
		// memory-recall.ts:19, memory-reflect.ts:20, memory-edit.ts:21,
		// tools/vibe.ts:130,196,219
		return ToolDecision{Tier: TierRead}

	case "eval", "task", "security_scan", "browser", "vibe_spawn", "vibe_send":
		// upstream: eval.ts:291, task/index.ts:675, security-scan.ts:109,
		// browser/prelude-definition.ts:71, tools/vibe.ts:88,108
		return ToolDecision{Tier: TierExec}

	default:
		if strings.HasPrefix(toolName, "mcp__") {
			// upstream: packages/coding-agent/src/mcp/tool-bridge.ts:811 (`MCPTool.approval = "write"`).
			return ToolDecision{Tier: TierWrite}
		}
		// Unannotated built-in or extension tool defaults to exec.
		// upstream: tools/approval.ts:290, 345
		return ToolDecision{Tier: TierExec}
	}
}

// ── Prompt formatting & interactive UI ───────────────────────────────────────

const defaultPromptTruncateChars = 2000

// TruncateForPrompt caps prompt detail values at maxChars runes with omp's
// `[…<N>ch elided…]` suffix.
// upstream: tools/approval.ts:629-633
func TruncateForPrompt(value string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = defaultPromptTruncateChars
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	omitted := len(runes) - maxChars
	return fmt.Sprintf("%s[…%dch elided…]", string(runes[:maxChars]), omitted)
}

// formatPayloadValue ports omp's `formatPayloadValue` (`tools/approval.ts:635-678`):
//   - standard 2-space indented JSON layout
//   - multiline strings expanded into indented quoted blocks with CRLF/CR normalized
//     and tabs expanded to 3 spaces (matching @oh-my-pi/pi-tui replaceTabs)
//   - control characters JSON-escaped so terminal escapes stay visible text
func formatPayloadValue(value any, indent int) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int, int64, float64, json.Number:
		return fmt.Sprint(v)
	case string:
		norm := strings.ReplaceAll(v, "\r\n", "\n")
		norm = strings.ReplaceAll(norm, "\r", "\n")
		norm = strings.ReplaceAll(norm, "\t", "   ")
		if strings.Contains(norm, "\n") {
			pad := strings.Repeat(" ", indent)
			rawLines := strings.Split(norm, "\n")
			outLines := make([]string, len(rawLines))
			for i, line := range rawLines {
				outLines[i] = pad + "  " + jsonEscapeStringContent(line)
			}
			return "\"\n" + strings.Join(outLines, "\n") + "\n" + pad + "\""
		}
		return `"` + jsonEscapeStringContent(norm) + `"`
	case []any:
		if len(v) == 0 {
			return "[]"
		}
		pad := strings.Repeat(" ", indent)
		nextIndent := indent + 2
		nextPad := strings.Repeat(" ", nextIndent)
		items := make([]string, len(v))
		for i, item := range v {
			comma := ","
			if i == len(v)-1 {
				comma = ""
			}
			items[i] = nextPad + formatPayloadValue(item, nextIndent) + comma
		}
		return "[\n" + strings.Join(items, "\n") + "\n" + pad + "]"
	case map[string]any:
		if len(v) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pad := strings.Repeat(" ", indent)
		nextIndent := indent + 2
		nextPad := strings.Repeat(" ", nextIndent)
		lines := make([]string, 0, len(keys)+2)
		lines = append(lines, "{")
		for i, k := range keys {
			comma := ","
			if i == len(keys)-1 {
				comma = ""
			}
			keyStr := `"` + jsonEscapeStringContent(k) + `"`
			valStr := formatPayloadValue(v[k], nextIndent)
			lines = append(lines, fmt.Sprintf("%s%s: %s%s", nextPad, keyStr, valStr, comma))
		}
		lines = append(lines, pad+"}")
		return strings.Join(lines, "\n")
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		var generic any
		if err := json.Unmarshal(b, &generic); err == nil {
			return formatPayloadValue(generic, indent)
		}
		return string(b)
	}
}

func jsonEscapeStringContent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// FormatApprovalArguments ports omp's `formatApprovalArguments` (`tools/approval.ts:686-695`).
func FormatApprovalArguments(args any) []string {
	if args == nil {
		return nil
	}
	if m, ok := args.(map[string]any); ok && len(m) == 0 {
		return nil
	}
	formatted := formatPayloadValue(args, 0)
	return []string{"Arguments:\n" + TruncateForPrompt(formatted, defaultPromptTruncateChars)}
}

// FormatApprovalPrompt formats omp's approval prompt header and per-tool
// details shown in `ctx.Select`.
// Note: MCP tools (`mcp__*`) declare `approval = "write"` in omp
// (`mcp/tool-bridge.ts:811`), so omp does NOT print `Origin: MCP server tool`
// (`tools/approval.ts:701`).
// upstream: tools/approval.ts:698-726
func FormatApprovalPrompt(toolName string, input map[string]any, reason string) string {
	lines := []string{fmt.Sprintf("Allow tool: %s", toolName)}
	if reason != "" {
		lines = append(lines, fmt.Sprintf("Reason: %s", reason))
	}
	lines = append(lines, formatToolDetails(toolName, input)...)
	return strings.Join(lines, "\n")
}

func formatToolDetails(toolName string, input map[string]any) []string {
	switch toolName {
	case "bash":
		hookMu.RLock()
		fmtFn := bashDetailFmt
		hookMu.RUnlock()
		if fmtFn != nil {
			if d := fmtFn(input); len(d) > 0 {
				return d
			}
		}
		cmd := "(missing)"
		if input != nil {
			if s, ok := input["command"].(string); ok {
				cmd = s
			}
		}
		return []string{"Command: " + TruncateForPrompt(cmd, defaultPromptTruncateChars)}

	case "write":
		targetPath := "(missing)"
		content := ""
		if input != nil {
			if p, ok := input["path"].(string); ok {
				targetPath = p
			}
			if c, ok := input["content"].(string); ok {
				content = c
			}
		}
		return []string{
			"Path: " + TruncateForPrompt(targetPath, defaultPromptTruncateChars),
			"Content:\n" + TruncateForPrompt(content, defaultPromptTruncateChars),
		}

	case "edit":
		// upstream: edit/index.ts:469-478 (`File: <path>` per section + `Move: <from> → <to>`).
		raw, _ := input["input"].(string)
		paths, moves := inspectEditInput(raw)
		var out []string
		for _, p := range paths {
			out = append(out, "File: "+TruncateForPrompt(p, defaultPromptTruncateChars))
		}
		for _, mv := range moves {
			out = append(out, fmt.Sprintf("Move: %s → %s",
				TruncateForPrompt(mv.from, defaultPromptTruncateChars),
				TruncateForPrompt(mv.to, defaultPromptTruncateChars)))
		}
		if len(out) == 0 {
			return []string{"File: (unknown)"}
		}
		return out

	case "eval":
		lang := "javascript (default)"
		code := ""
		if input != nil {
			if l, ok := input["language"].(string); ok && l != "" {
				switch l {
				case "py", "python":
					lang = "python"
				case "js", "javascript":
					lang = "javascript"
				default:
					lang = l
				}
			}
			if c, ok := input["code"].(string); ok {
				code = c
			}
		}
		return []string{
			"Language: " + lang,
			"Code:\n" + TruncateForPrompt(code, defaultPromptTruncateChars),
		}

	case "ast_edit":
		// upstream: tools/ast-edit.ts:188-204
		var out []string
		if input != nil {
			if ops, ok := input["ops"].([]any); ok && len(ops) > 0 {
				if first, ok := ops[0].(map[string]any); ok {
					pat, _ := first["pat"].(string)
					rep, _ := first["out"].(string)
					out = append(out, "Pattern: "+TruncateForPrompt(pat, defaultPromptTruncateChars))
					out = append(out, "Replacement: "+TruncateForPrompt(rep, defaultPromptTruncateChars))
					if len(ops) > 1 {
						plural := "s"
						if len(ops) == 2 {
							plural = ""
						}
						out = append(out, fmt.Sprintf("+%d more op%s", len(ops)-1, plural))
					}
				}
			}
			if paths, ok := input["paths"].([]any); ok && len(paths) > 0 {
				strs := make([]string, 0, len(paths))
				for _, p := range paths {
					strs = append(strs, fmt.Sprint(p))
				}
				out = append(out, "Paths: "+TruncateForPrompt(strings.Join(strs, ", "), defaultPromptTruncateChars))
			} else if paths, ok := input["paths"].([]string); ok && len(paths) > 0 {
				out = append(out, "Paths: "+TruncateForPrompt(strings.Join(paths, ", "), defaultPromptTruncateChars))
			}
		}
		return out

	case "lsp":
		// upstream: lsp/tool.ts:319-326 — `typeof params.action === "string" ? params.action : "(missing)"`
		action := "(missing)"
		var out []string
		if input != nil {
			if a, ok := input["action"].(string); ok {
				action = a
			}
		}
		out = append(out, "Action: "+action)
		if input != nil {
			if f, ok := input["file"].(string); ok && f != "" {
				out = append(out, "File: "+TruncateForPrompt(f, defaultPromptTruncateChars))
			}
		}
		return out

	case "debug":
		// upstream: tools/debug.ts:550-557 — `typeof params.action === "string" ? params.action : "(missing)"`
		action := "(missing)"
		var out []string
		if input != nil {
			if a, ok := input["action"].(string); ok {
				action = a
			}
		}
		out = append(out, "Action: "+action)
		if input != nil {
			if p, ok := input["program"].(string); ok && p != "" {
				out = append(out, "Program: "+TruncateForPrompt(p, defaultPromptTruncateChars))
			}
		}
		return out

	case "task":
		// upstream: task/index.ts:676-734
		var out []string
		if input == nil {
			return nil
		}
		if a, ok := input["agent"].(string); ok {
			out = append(out, "Agent: "+TruncateForPrompt(a, defaultPromptTruncateChars))
		}
		if n, ok := input["name"].(string); ok && strings.TrimSpace(n) != "" {
			out = append(out, "Name: "+TruncateForPrompt(n, defaultPromptTruncateChars))
		}
		if m, ok := input["model"].(string); ok {
			out = append(out, "Model: "+TruncateForPrompt(m, defaultPromptTruncateChars))
		}
		if t, ok := input["task"].(string); ok {
			out = append(out, "Task:\n"+TruncateForPrompt(t, defaultPromptTruncateChars))
		}
		if c, ok := input["context"].(string); ok && strings.TrimSpace(c) != "" {
			out = append(out, "Context:\n"+TruncateForPrompt(c, defaultPromptTruncateChars))
		}
		if tasks, ok := input["tasks"].([]any); ok && len(tasks) > 0 {
			const defaultAgent = "task"
			effectiveAgent := func(item any) string {
				if m, ok := item.(map[string]any); ok {
					if a, ok := m["agent"].(string); ok && strings.TrimSpace(a) != "" {
						return strings.TrimSpace(a)
					}
				}
				return defaultAgent
			}
			var order []string
			counts := map[string]int{}
			for _, item := range tasks {
				ag := effectiveAgent(item)
				if counts[ag] == 0 {
					order = append(order, ag)
				}
				counts[ag]++
			}
			summaryParts := make([]string, 0, len(order))
			for _, ag := range order {
				summaryParts = append(summaryParts, fmt.Sprintf("%s ×%d", ag, counts[ag]))
			}
			out = append(out, "Batch agents: "+TruncateForPrompt(strings.Join(summaryParts, ", "), defaultPromptTruncateChars))
			if first, ok := tasks[0].(map[string]any); ok {
				if n, ok := first["name"].(string); ok && strings.TrimSpace(n) != "" {
					out = append(out, "Name: "+TruncateForPrompt(n, defaultPromptTruncateChars))
				}
				out = append(out, "Agent: "+TruncateForPrompt(effectiveAgent(first), defaultPromptTruncateChars))
				if t, ok := first["task"].(string); ok {
					out = append(out, "Task:\n"+TruncateForPrompt(t, defaultPromptTruncateChars))
				}
			}
			for idx, item := range tasks {
				if m, ok := item.(map[string]any); ok {
					if mod, ok := m["model"].(string); ok {
						out = append(out, fmt.Sprintf("Task %d model: %s", idx+1, TruncateForPrompt(mod, defaultPromptTruncateChars)))
					}
				}
			}
			if len(tasks) > 1 {
				plural := "s"
				if len(tasks) == 2 {
					plural = ""
				}
				out = append(out, fmt.Sprintf("+%d more task%s", len(tasks)-1, plural))
			}
		}
		return out

	default:
		if strings.HasPrefix(toolName, "mcp__") {
			return FormatApprovalArguments(input)
		}
		return nil
	}
}

const (
	choiceOnce      = "Approve"
	choiceOnceGrant = "Approve Once"
	choiceDeny      = "Deny"
)

func choiceSessionLabel(toolName string) string {
	return fmt.Sprintf("Approve %s for this session", toolName)
}

func strictSessionID(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var id string
	func() {
		defer func() { _ = recover() }()
		if v, err := ctx.GetSessionID(); err == nil {
			id = strings.TrimSpace(v)
		}
	}()
	return id
}

func safeHasUI(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	var has bool
	func() {
		defer func() { _ = recover() }()
		has = ctx.HasUI()
	}()
	return has
}

func safeCwd(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var cwd string
	func() {
		defer func() { _ = recover() }()
		cwd = ctx.Cwd()
	}()
	return cwd
}

// HandleToolCallVeto is the single `sdk.EventToolCall` veto handler installed
// by Register. It enforces omp's approval engine for EVERY tool (built-in,
// mounted tool commands invoked via `ctx.ExecuteTool`, MCP, and extension tools).
//
// Plan-mode mutations remain governed by `internal/gate` inside each tool's
// Execute after target resolution; HandleToolCallVeto does not duplicate
// `internal/gate`.
//
// upstream: packages/coding-agent/src/extensibility/extensions/wrapper.ts:280-532
func HandleToolCallVeto(ctx sdk.Context, event map[string]any) (any, error) {
	if event == nil {
		return nil, nil
	}
	toolName, _ := event["toolName"].(string)
	if toolName == "" {
		return nil, nil
	}
	input, _ := event["input"].(map[string]any)

	decision := EvaluateToolDecision(ctx, toolName, input)
	sessionID := strictSessionID(ctx)
	cwd := safeCwd(ctx)
	vr := resolveTwoViews(ctx, toolName, input, decision, sessionID, cwd)
	resolved := vr.resolved
	mode := vr.mode
	cacheTargets := vr.cacheTargets

	switch resolved.Policy {
	case PolicyAllow:
		return nil, nil

	case PolicyDeny:
		return map[string]any{
			"block":  true,
			"reason": DenyError(resolved, toolName),
		}, nil

	case PolicyPrompt:
		grantable := mode == ModeAlwaysAskCached && resolved.Source == "mode" && len(cacheTargets) > 0
		scopeKey := ""
		if !grantable {
			scopeKey = SessionApprovalScope(resolved, toolName)
		}
		if sessionID != "" && scopeKey != "" && HasSessionApproval(sessionID, scopeKey) {
			return nil, nil
		}
		offerSession := sessionID != "" && scopeKey != ""
		offerTargetGrants := sessionID != "" && grantable
		answer, grantScope, blockRes := promptOrDenyHeadless(ctx, toolName, input, resolved, cacheTargets, offerSession, offerTargetGrants)
		if blockRes != nil {
			return blockRes, nil
		}
		if answer == "session" && offerSession {
			GrantSessionApproval(sessionID, scopeKey)
		}
		if answer == "grant" && offerTargetGrants {
			recordTargetGrant(sessionID, cacheTargets, resolved.Tier, grantScope)
		}
		return nil, nil

	default:
		return nil, nil
	}
}

// CheckPrelude gates an eval prelude host invocation (`browser`, etc.) through
// the exact same approval resolver and settings views, matching omp's
// `approvePreludeInvocation` (`packages/coding-agent/src/eval/preludes.ts:149-187`).
// Returns nil when allowed, or an error with omp's exact denial / headless /
// user-denied text.
func CheckPrelude(ctx sdk.Context, name, tierStr string, parameters json.RawMessage) error {
	if name == "" {
		return nil
	}
	tier := NormalizeTier(tierStr)
	var args map[string]any
	if len(parameters) > 0 {
		_ = json.Unmarshal(parameters, &args)
	}
	decision := ToolDecision{Tier: tier}
	// A prelude names no path of its own: restricted folders match by session cwd
	// (`eval/preludes.ts:164-172`).
	vr := resolveTwoViews(ctx, name, args, decision, strictSessionID(ctx), safeCwd(ctx))
	resolved := vr.resolved
	if resolved.Policy == PolicyDeny {
		return fmt.Errorf("%s", DenyError(resolved, name))
	}
	if resolved.Policy != PolicyPrompt {
		return nil
	}
	if !safeHasUI(ctx) {
		if resolved.Source == "restriction" {
			return fmt.Errorf(
				"Eval prelude %q requires approval but no interactive UI is available.\nReason: %s\nA restricted folder (tools.restrictedFolders) asks in every approval mode.",
				name, resolved.Reason,
			)
		}
		return fmt.Errorf(
			"Eval prelude %q requires approval but no interactive UI is available.\nSet tools.approval.%s: allow or use an interactive UI to approve the call.",
			name, name,
		)
	}
	title := FormatApprovalPrompt(name, args, resolved.Reason)
	selected, ok, err := ctx.Select(title, []string{choiceOnce, choiceDeny})
	if err != nil || !ok {
		return fmt.Errorf("Eval prelude call denied by user: %s", name)
	}
	if choice, _ := selected.(string); choice == choiceOnce {
		return nil
	}
	return fmt.Errorf("Eval prelude call denied by user: %s", name)
}

func promptOrDenyHeadless(
	ctx sdk.Context,
	toolName string,
	input map[string]any,
	resolved Resolved,
	cacheTargets []string,
	offerSession bool,
	offerTargetGrants bool,
) (answer string, grantScope string, blockRes any) {
	if safeHasUI(ctx) {
		var grants []approvalGrantChoice
		var folderBlock string
		if offerTargetGrants && len(cacheTargets) > 0 {
			folderKey, folderLabel, hasFolder := approvalFolderCandidate(cacheTargets)
			grants = approvalGrantChoices(cacheTargets, folderKey, folderLabel, hasFolder)
			if hasFolder {
				listing := readApprovalFolderListing(folderKey, approvalFolderFullLimit)
				folderBlock = formatApprovalFolderBlock(folderLabel, listing, approvalFolderFullLimit)
			}
		}

		title := FormatApprovalPrompt(toolName, input, resolved.Reason)
		if folderBlock != "" {
			title = title + "\n" + folderBlock
		}

		// Choice order matches omp's `approvalChoiceLabels` (`tools/approval.ts:914-923`):
		// session grants, always grants, Approve Once/Approve, session tool answer, Deny.
		var choices []string
		for _, g := range grants {
			choices = append(choices, g.label)
		}
		for _, g := range grants {
			choices = append(choices, g.alwaysLabel)
		}
		onceLabel := choiceOnce
		if len(grants) > 0 {
			onceLabel = choiceOnceGrant
		}
		choices = append(choices, onceLabel)
		sessionLabel := choiceSessionLabel(toolName)
		if offerSession {
			choices = append(choices, sessionLabel)
		}
		choices = append(choices, choiceDeny)

		selected, ok, err := ctx.Select(title, choices)
		if err != nil || !ok {
			return "deny", "", map[string]any{
				"block":  true,
				"reason": fmt.Sprintf("Tool call denied by user: %s", toolName),
			}
		}
		choice, _ := selected.(string)
		if choice == onceLabel {
			return "once", "", nil
		}
		if offerSession && choice == sessionLabel {
			return "session", "", nil
		}
		for _, g := range grants {
			if choice == g.label || choice == g.alwaysLabel {
				return "grant", g.scope, nil
			}
		}
		return "deny", "", map[string]any{
			"block":  true,
			"reason": fmt.Sprintf("Tool call denied by user: %s", toolName),
		}
	}

	// Headless / non-interactive: match omp's fail-closed refusals (`wrapper.ts:477-490`).
	if resolved.Source == "restriction" {
		return "deny", "", map[string]any{
			"block":  true,
			"reason": HeadlessRestrictionError(toolName, resolved.Reason),
		}
	}
	return "deny", "", map[string]any{
		"block":  true,
		"reason": HeadlessPromptError(toolName),
	}
}
