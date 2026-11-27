package modes

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts
// upstream: packages/coding-agent/src/plan-mode/plan-files.ts
// upstream: packages/coding-agent/src/plan-mode/plan-autosave.ts
// upstream: packages/coding-agent/src/tools/plan-mode-guard.ts
// upstream: packages/coding-agent/src/tools/resolve.ts (createProposeTool)
// upstream: crates/edit/src/path_policy.rs (enforce_write)
// upstream: packages/coding-agent/src/modes/interactive-mode.ts (plan mode)

const (
	defaultPlanFileURL = "local://PLAN.md"
	proposeToolName    = "propose"
	planReminderMax    = 3 // upstream: agent-session.ts:650 PLAN_MODE_REMINDER_MAX = 3

	planErrWorkingTreeReadOnly = "Plan mode: the working tree is read-only. Write your plan to a local://<slug>-plan.md file instead."
	planErrRenameDisallowed    = "Plan mode: renaming files is not allowed."
	planErrDeleteDisallowed    = "Plan mode: deleting files is not allowed."
)

// PlanApprovalDetails matches omp's PlanApprovalDetails returned by `propose`.
// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts:9-13
type PlanApprovalDetails struct {
	PlanFilePath string `json:"planFilePath"`
	Title        string `json:"title"`
	PlanExists   bool   `json:"planExists"`
}

// ResolvedApprovedPlan is the result of locating and normalizing a submitted plan.
type ResolvedApprovedPlan struct {
	PlanFilePath string
	PlanContent  string
	Title        string
}

var (
	planTitleSpacesRE    = regexp.MustCompile(`\s+`)
	planTitleInvalidRE   = regexp.MustCompile(`[^A-Za-z0-9_-]`)
	planTitleMultiDashRE = regexp.MustCompile(`-{2,}`)
	planTitleMDExtRE     = regexp.MustCompile(`(?i)\.md$`)
	planTitleSuffixRE    = regexp.MustCompile(`(?i)-plan$`)
	planFirstH1RE        = regexp.MustCompile(`(?m)^[ \t]*#[ \t]+(.+?)[ \t]*$`)
	planSchemeStripRE    = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:/+`)
	planFilenameMatchRE  = regexp.MustCompile(`(?i)plan\.md$`)
)

// NormalizePlanTitle validates and sanitizes a plan title into a safe filename stem.
// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts:21-48
func NormalizePlanTitle(raw string) (title, fileName string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("Plan title is required and must not be empty.")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\`) || strings.Contains(trimmed, "..") {
		return "", "", fmt.Errorf("Plan title must not contain path separators or '..'.")
	}
	withoutExt := planTitleMDExtRE.ReplaceAllString(trimmed, "")
	sanitized := planTitleSpacesRE.ReplaceAllString(withoutExt, "-")
	sanitized = planTitleInvalidRE.ReplaceAllString(sanitized, "")
	sanitized = planTitleMultiDashRE.ReplaceAllString(sanitized, "-")
	sanitized = strings.Trim(sanitized, "-")
	if sanitized == "" {
		return "", "", fmt.Errorf("Plan title must contain at least one letter, number, underscore, or hyphen after sanitization.")
	}
	return sanitized, sanitized + ".md", nil
}

// ResolvePlanTitle derives a valid title from suppliedTitle, H1 heading,
// filename stem, or default "plan".
// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts:59-85
func ResolvePlanTitle(suppliedTitle, planContent, planFilePath string) (title, fileName, source string) {
	type cand struct {
		val string
		src string
	}
	var cands []cand
	if s := strings.TrimSpace(suppliedTitle); s != "" {
		cands = append(cands, cand{s, "supplied"})
	}
	if m := planFirstH1RE.FindStringSubmatch(planContent); m != nil {
		if h := strings.TrimSpace(m[1]); h != "" {
			cands = append(cands, cand{h, "heading"})
		}
	}
	if stem := planFilenameStem(planFilePath); stem != "" {
		cands = append(cands, cand{stem, "filename"})
	}
	cands = append(cands, cand{"plan", "default"})

	for _, c := range cands {
		if t, fn, err := NormalizePlanTitle(c.val); err == nil {
			return t, fn, c.src
		}
	}
	return "plan", "plan.md", "default"
}

func planFilenameStem(planFilePath string) string {
	withoutScheme := planSchemeStripRE.ReplaceAllString(planFilePath, "")
	parts := strings.FieldsFunc(withoutScheme, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return ""
	}
	return planTitleMDExtRE.ReplaceAllString(parts[len(parts)-1], "")
}

// HumanizePlanTitle converts a normalized plan title to a readable session name.
// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts:105-109
func HumanizePlanTitle(title string) string {
	var b strings.Builder
	prevSep := false
	for _, r := range title {
		if r == '-' || r == '_' {
			if !prevSep && b.Len() > 0 {
				b.WriteByte(' ')
				prevSep = true
			}
			continue
		}
		prevSep = false
		b.WriteRune(r)
	}
	spaced := strings.TrimSpace(b.String())
	if spaced == "" {
		return ""
	}
	runes := []rune(spaced)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// PlanFileURLForSlug maps a plan slug to its canonical `local://` URL.
// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts:114-116
func PlanFileURLForSlug(slug string) string {
	return "local://" + slug + "-plan.md"
}

func planSlugFromSupplied(supplied string) string {
	if strings.TrimSpace(supplied) == "" {
		return ""
	}
	t, _, err := NormalizePlanTitle(supplied)
	if err != nil {
		return ""
	}
	slug := planTitleSuffixRE.ReplaceAllString(t, "")
	if slug == "" {
		return t
	}
	return slug
}

// PlanSaveFileName formats `<TOPIC>_PLAN.md` for saving approved plans.
// upstream: packages/coding-agent/src/plan-mode/plan-autosave.ts:15-28
func PlanSaveFileName(title string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(unicode.ToUpper(r))
			prevUnderscore = false
		} else if !prevUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	stem := strings.Trim(b.String(), "_")
	const maxLen = 32
	if len(stem) > maxLen {
		if cut := strings.LastIndexByte(stem[:maxLen+1], '_'); cut > 0 {
			stem = stem[:cut]
		} else {
			stem = stem[:maxLen]
		}
	}
	if stem == "" || stem == "PLAN" {
		return "PLAN.md"
	}
	if strings.HasSuffix(stem, "_PLAN") {
		return stem + ".md"
	}
	return stem + "_PLAN.md"
}

// ResolveApprovedPlan locates the plan file the agent wrote without renaming it.
// upstream: packages/coding-agent/src/plan-mode/approved-plan.ts:156-190
func ResolveApprovedPlan(
	suppliedTitle string,
	statePlanFilePath string,
	readPlan func(planURL string) (string, bool, error),
	listPlanFiles func() ([]string, error),
) (ResolvedApprovedPlan, error) {
	var ordered []string
	consider := func(u string) {
		if u == "" {
			return
		}
		for _, existing := range ordered {
			if existing == u {
				return
			}
		}
		ordered = append(ordered, u)
	}

	if slug := planSlugFromSupplied(suppliedTitle); slug != "" {
		consider(PlanFileURLForSlug(slug))
	}

	var listed []string
	if listPlanFiles != nil {
		if l, err := listPlanFiles(); err == nil {
			listed = l
		}
	}

	canonicalListed := make(map[string]bool, len(listed))
	for _, u := range listed {
		canonicalListed[normalizePlanURL(u)] = true
	}
	if statePlanFilePath != "" && !canonicalListed[normalizePlanURL(statePlanFilePath)] {
		consider(statePlanFilePath)
	}
	for _, u := range listed {
		consider(u)
	}
	consider(statePlanFilePath)

	for _, u := range ordered {
		content, exists, err := readPlan(u)
		if err != nil {
			return ResolvedApprovedPlan{}, err
		}
		if exists {
			title, _, _ := ResolvePlanTitle(suppliedTitle, content, u)
			return ResolvedApprovedPlan{
				PlanFilePath: u,
				PlanContent:  content,
				Title:        title,
			}, nil
		}
	}

	target := statePlanFilePath
	if len(ordered) > 0 {
		target = ordered[0]
	}
	return ResolvedApprovedPlan{}, fmt.Errorf("Plan file not found at %s. Write the finalized plan to %s before requesting approval.", target, target)
}

func normalizePlanURL(u string) string {
	norm := urls.Normalize(u)
	if strings.HasPrefix(strings.ToLower(norm), "local:/") && !strings.HasPrefix(strings.ToLower(norm), "local://") {
		return "local://" + norm[len("local:/"):]
	}
	return norm
}

// resolveLocalRootDir returns the session-scoped `local://` root directory.
// Uses urls.SandboxRoots when a `local://` handler is registered in internal/urls;
// otherwise derives `<session-file-base>/local` or `os.TempDir()/omp-local/<safe-session-id>`.
// Returns "" if neither a sandbox root, session file, nor session ID is available.
func resolveLocalRootDir(ctx sdk.Context) string {
	uc := urls.Context{SDK: ctx, Cwd: ctx.Cwd()}
	if roots := urls.SandboxRoots(uc); len(roots) > 0 {
		return roots[0]
	}
	if located, ok := urls.LocateSync(uc, "local://"); ok && located != "" {
		return located
	}
	if sf, err := ctx.GetSessionFile(); err == nil && sf != nil && *sf != "" {
		base := strings.TrimSuffix(*sf, filepath.Ext(*sf))
		return filepath.Join(base, "local")
	}
	sid := strictSessionID(ctx)
	if sid == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "omp-local", sanitizeSessionID(sid))
}

func sanitizeSessionID(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" || s == "." || s == ".." {
		return "session"
	}
	return s
}

// resolvePlanFilePath resolves a plan path (internal URL or cwd-relative path)
// to an absolute filesystem path.
// upstream: packages/coding-agent/src/plan-mode/plan-files.ts:11-23
func resolvePlanFilePath(ctx sdk.Context, planPath string) (string, error) {
	norm := normalizePlanURL(planPath)
	uc := urls.Context{SDK: ctx, Cwd: ctx.Cwd()}
	if urls.CanHandle(norm) {
		if p, ok, err := urls.Locate(uc, norm, urls.LocateOptions{Create: true}); err == nil && ok {
			return p, nil
		}
		if p, ok := urls.LocateSync(uc, norm); ok {
			return p, nil
		}
	}
	if strings.HasPrefix(strings.ToLower(norm), "local://") {
		rel := strings.TrimPrefix(norm[len("local://"):], "/")
		root := resolveLocalRootDir(ctx)
		if root == "" {
			return "", fmt.Errorf("cannot resolve local:// root without session identity")
		}
		if rel == "" {
			return root, nil
		}
		clean := filepath.Clean(rel)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return "", fmt.Errorf("invalid local:// plan path %q", planPath)
		}
		return filepath.Join(root, clean), nil
	}
	if filepath.IsAbs(norm) {
		return filepath.Clean(norm), nil
	}
	return filepath.Join(ctx.Cwd(), norm), nil
}

func readPlanFile(ctx sdk.Context, planPath string) (string, bool, error) {
	resolved, err := resolvePlanFilePath(ctx, planPath)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

func listPlanFiles(ctx sdk.Context) ([]string, error) {
	root := resolveLocalRootDir(ctx)
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	type item struct {
		url   string
		mtime int64
	}
	var plans []item
	for _, e := range entries {
		if e.IsDir() || !planFilenameMatchRE.MatchString(e.Name()) {
			continue
		}
		var mt int64
		if info, err := e.Info(); err == nil {
			mt = info.ModTime().UnixNano()
		}
		plans = append(plans, item{url: "local://" + e.Name(), mtime: mt})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].mtime > plans[j].mtime })
	out := make([]string, len(plans))
	for i, p := range plans {
		out[i] = p.url
	}
	return out, nil
}

// copyLocalArtifacts recursively copies session-local planning artifacts from
// oldRoot to newRoot when "Approve and execute" starts a fresh session.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7129-7132
func copyLocalArtifacts(oldRoot, newRoot string) error {
	if oldRoot == "" || newRoot == "" || filepath.Clean(oldRoot) == filepath.Clean(newRoot) {
		return nil
	}
	info, err := os.Stat(oldRoot)
	if err != nil || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(oldRoot, func(src string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(oldRoot, src)
		if err != nil || rel == "." {
			return nil
		}
		dst := filepath.Join(newRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(dst)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// canonicalWritePath resolves the deepest existing ancestor of absPath via
// filepath.EvalSymlinks and re-appends missing path components. Returns ""
// when the path traverses a dangling symlink or cannot be inspected.
// upstream: packages/coding-agent/src/tools/plan-mode-guard.ts:14-31
// upstream: crates/edit/src/path_policy.rs:213-221
func canonicalWritePath(absPath string) string {
	var tail []string
	cur := filepath.Clean(absPath)
	for {
		realPath, err := filepath.EvalSymlinks(cur)
		if err == nil {
			parts := append([]string{realPath}, tail...)
			return filepath.Join(parts...)
		}
		if !os.IsNotExist(err) {
			return ""
		}
		if _, lerr := os.Lstat(cur); lerr == nil {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

func isWithinRoot(absPath, root string) bool {
	absPath = filepath.Clean(absPath)
	root = filepath.Clean(root)
	if absPath == root {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(absPath, root)
}

func sandboxRoots(ctx sdk.Context) []string {
	uc := urls.Context{SDK: ctx, Cwd: ctx.Cwd()}
	roots := urls.SandboxRoots(uc)
	if localRoot := resolveLocalRootDir(ctx); localRoot != "" {
		found := false
		for _, r := range roots {
			if filepath.Clean(r) == filepath.Clean(localRoot) {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, localRoot)
		}
	}
	return roots
}

// pathWithinLocalSandbox reports whether absPath physically resolves inside one
// of the session's writable sandbox roots (`local://`), preventing symlink escapes.
// upstream: crates/edit/src/path_policy.rs:213-221
// upstream: packages/coding-agent/src/tools/plan-mode-guard.ts:46-69
func pathWithinLocalSandbox(ctx sdk.Context, absPath string) bool {
	if absPath == "" || !filepath.IsAbs(absPath) {
		return false
	}
	roots := sandboxRoots(ctx)
	if len(roots) == 0 {
		return false
	}
	canonTarget := canonicalWritePath(absPath)
	if canonTarget == "" {
		return false
	}
	for _, root := range roots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		canonRoot := canonicalWritePath(absRoot)
		if canonRoot != "" && isWithinRoot(canonTarget, canonRoot) {
			return true
		}
	}
	return false
}

// urlTargetsSandbox reports whether an internal URL resolves to a `sandbox`-scope
// writable scheme (e.g. `local://`).
func urlTargetsSandbox(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	norm := normalizePlanURL(rawURL)
	if info := urls.WriteTarget(norm); info != nil {
		return info.Spec.Write != nil && info.Spec.Write.Scope == "sandbox"
	}
	return false
}

// planGatePolicy is installed via `gate.SetPolicy` and evaluates resolved
// mutations from tools that call `gate.Check` (`write`, `edit`, `ast_edit`,
// `resolve`, `lsp`, `bash`, `github`, `eval`). Plan mode is enforced only where
// a tool resolves a write target (as omp's `enforce_write`), plus `gate.PlanActive`
// for subagents in `internal/agents`.
//
// Rules match omp v19.1.1:
//   - `crates/edit/src/path_policy.rs:176-208` (`enforce_write`):
//     OpMove -> "Plan mode: renaming files is not allowed."
//     OpDelete -> "Plan mode: deleting files is not allowed."
//     OpWrite / OpEdit -> allowed only when the resolved Path physically lies
//     inside a `local://` sandbox root (or a registered `sandbox`-scope URL
//     whose located Path is inside a sandbox root); otherwise
//     "Plan mode: the working tree is read-only. Write your plan to a local://<slug>-plan.md file instead."
//   - `OpExec`:
//   - `bash`: omp does NOT block `bash` in plan mode (prompt-governed only;
//     `tools/bash.ts` has no plan check). Allowed here so read-only
//     exploration commands work identically to omp.
//   - `eval`: omp allows eval cells (refusing eval-defined tools for subagents
//     in `eval/agent-bridge.ts:185`).
//   - `github` (and other mutating `OpExec` callers like `pr_create`/
//     `pr_checkout`/`pr_push`): denied while planning (deliberate tightening
//     documented in UPSTREAM.md).
//   - Host error resolving session ID while another session may be planning:
//     fails closed when `strictSessionID(ctx) == ""` and any session has plan
//     mode active (so a host error never opens the working tree while planning).
func (m *manager) planGatePolicy(ctx sdk.Context, r gate.Request) error {
	if strictSessionID(ctx) == "" {
		if m.anyPlanActive() {
			return fmt.Errorf(planErrWorkingTreeReadOnly)
		}
		return nil
	}
	if !m.planActive(ctx) {
		return nil
	}
	switch r.Op {
	case gate.OpMove:
		return fmt.Errorf(planErrRenameDisallowed)
	case gate.OpDelete:
		return fmt.Errorf(planErrDeleteDisallowed)
	case gate.OpWrite, gate.OpEdit:
		// If the tool resolved a filesystem Path, verify it physically lands
		// inside a sandbox root (prevents symlinks out of local://).
		if r.Path != "" {
			if pathWithinLocalSandbox(ctx, r.Path) {
				return nil
			}
			return fmt.Errorf(planErrWorkingTreeReadOnly)
		}
		// Handler-owned URL write with no filesystem path: allow only if the
		// scheme declares `sandbox` write scope.
		if r.URL != "" && urlTargetsSandbox(r.URL) {
			return nil
		}
		return fmt.Errorf(planErrWorkingTreeReadOnly)
	case gate.OpExec:
		switch r.Tool {
		case "bash":
			// omp v19.1.1 does not gate `bash` in plan mode; prompt-governed only.
			return nil
		case "eval":
			// omp allows eval cells (refusing eval-defined tools for subagents in eval/agent-bridge.ts:185).
			if r.Detail == "agent-custom-tools" {
				return fmt.Errorf("Eval-defined tools are unavailable in plan mode.")
			}
			return nil
		default:
			return fmt.Errorf(planErrWorkingTreeReadOnly)
		}
	default:
		return fmt.Errorf(planErrWorkingTreeReadOnly)
	}
}

// buildPlanModeActivePrompt renders plan-mode-active.md for the current session state.
// upstream: packages/coding-agent/src/session/agent-session.ts:8911-8950
func (m *manager) buildPlanModeActivePrompt(ctx sdk.Context, st *sessionState) string {
	planPath := st.planFilePath
	if planPath == "" {
		planPath = defaultPlanFileURL
	}
	_, planExists, _ := readPlanFile(ctx, planPath)
	tools, _ := ctx.GetActiveTools()
	hasTool := func(name string) bool {
		return containsString(tools, name)
	}
	return renderTemplate(planModeActiveTemplate, templateData{
		Vars: map[string]string{
			"planFilePath":  planPath,
			"askToolName":   "ask",
			"writeToolName": "write",
			"bashToolName":  "bash",
			"editToolName":  "edit",
		},
		Bools: map[string]bool{
			"planExists":         planExists,
			"proposeIsCommand":   proposeIsMountedCommand(ctx),
			"askAvailable":       hasTool("ask"),
			"taskAvailable":      hasTool("task"),
			"isHashlineEditMode": true,
			"reentry":            st.planHasEntered,
			"iterative":          false,
			"scoutAvailable":     hasTool("task"),
		},
	})
}

func renderPlanApprovedPrompt(planFilePath, planContent string, contextPreserved bool, tools []string) string {
	return renderTemplate(planModeApprovedTemplate, templateData{
		Vars: map[string]string{
			"planFilePath": planFilePath,
			"planContent":  planContent,
		},
		Bools: map[string]bool{
			"contextPreserved": contextPreserved,
		},
		Lists: map[string][]string{
			"tools": tools,
		},
	})
}

func renderPlanReferencePrompt(planFilePath, planContent string) string {
	return renderTemplate(planModeReferenceTemplate, templateData{
		Vars: map[string]string{
			"planFilePath": planFilePath,
			"planContent":  planContent,
		},
	})
}

func renderPlanCompactInstructions(planFilePath string) string {
	return renderTemplate(planModeCompactInstructionsTemplate, templateData{
		Vars: map[string]string{"planFilePath": planFilePath},
	})
}

func proposeIsMountedCommand(ctx sdk.Context) bool {
	for _, name := range toolcmd.CommandNames(ctx) {
		if name == proposeToolName {
			return true
		}
	}
	return false
}

func renderPlanDecisionReminder(ctx sdk.Context) string {
	return renderTemplate(planModeToolDecisionReminderTemplate, templateData{
		Vars: map[string]string{
			"askToolName":  "ask",
			"bashToolName": "bash",
		},
		Bools: map[string]bool{
			"proposeIsCommand": proposeIsMountedCommand(ctx),
		},
	})
}

// handlePlanCommand implements `/plan [prompt]`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7317-7382
func (m *manager) handlePlanCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyError(ctx, "Cannot toggle plan mode without an active session ID.")
		return nil
	}
	initialPrompt := strings.TrimSpace(args)
	if VibeActive != nil && VibeActive(ctx) {
		notifyWarn(ctx, "Exit vibe mode first.")
		return nil
	}

	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.goalState != nil && (st.goalState.Enabled || st.goalState.Goal.Status == GoalStatusPaused) {
		m.mu.Unlock()
		notifyWarn(ctx, "Exit goal mode first.")
		return nil
	}
	wasEnabled := st.planEnabled
	wasPaused := st.planPaused
	planFile := st.planFilePath
	if planFile == "" {
		planFile = st.planReferencePath
	}
	m.mu.Unlock()

	if wasEnabled {
		if m.hasPlanDraftContent(ctx, planFile) && ctx.HasUI() {
			confirmed, err := ctx.Confirm("Exit plan mode?", "This exits plan mode without approving a plan.")
			if err != nil {
				return err
			}
			if b, ok := confirmed.(bool); !ok || !b {
				return nil
			}
		}
		if idle, err := ctx.IsIdle(); err == nil && !idle {
			ctx.Abort()
		}
		m.exitPlanMode(ctx, sid, true, false)
		return nil
	}

	if wasPaused && initialPrompt == "" {
		m.disablePausedPlanMode(ctx, sid)
		return nil
	}

	m.enterPlanMode(ctx, sid, planFile)
	if initialPrompt != "" {
		if err := sendPromptOrSteer(ctx, initialPrompt); err != nil {
			notifyError(ctx, fmt.Sprintf("Failed to send plan prompt: %v", err))
		}
	}
	return nil
}

// handlePlanReviewCommand implements `/plan-review`.
// Because `/plan-review` runs in a PiG command context, all approval options
// including "Approve and execute" (which calls `ctx.NewSession`) work here.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7969-8015
func (m *manager) handlePlanReviewCommand(ctx sdk.Context, _ string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyWarn(ctx, "Plan mode is not active.")
		return nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	enabled := st.planEnabled
	statePath := st.planFilePath
	m.mu.Unlock()

	if !enabled {
		notifyWarn(ctx, "Plan mode is not active.")
		return nil
	}

	const noPlan = "No plan to review yet — write one to a local://<slug>-plan.md file first."
	listed, _ := listPlanFiles(ctx)
	var planPath string
	if len(listed) > 0 {
		planPath = listed[0]
	} else if statePath != "" {
		planPath = statePath
	}
	if planPath == "" {
		notifyWarn(ctx, noPlan)
		return nil
	}
	content, exists, err := readPlanFile(ctx, planPath)
	if err != nil || !exists || strings.TrimSpace(content) == "" {
		notifyWarn(ctx, noPlan)
		return nil
	}
	title, _, _ := ResolvePlanTitle("", content, planPath)
	return m.runPlanReviewDialog(ctx, sid, PlanApprovalDetails{
		PlanFilePath: planPath,
		Title:        title,
		PlanExists:   true,
	}, true)
}

func (m *manager) hasPlanDraftContent(ctx sdk.Context, planFilePath string) bool {
	seen := map[string]bool{}
	var candidates []string
	if planFilePath != "" {
		candidates = append(candidates, planFilePath)
		seen[planFilePath] = true
	}
	if listed, err := listPlanFiles(ctx); err == nil {
		for _, u := range listed {
			if !seen[u] {
				candidates = append(candidates, u)
				seen[u] = true
			}
		}
	}
	for _, c := range candidates {
		if content, ok, err := readPlanFile(ctx, c); err == nil && ok && strings.TrimSpace(content) != "" {
			return true
		}
	}
	return false
}

func (m *manager) enterPlanMode(ctx sdk.Context, sid, planFilePath string) {
	if planFilePath == "" {
		planFilePath = defaultPlanFileURL
	}
	prevTools, _ := ctx.GetActiveTools()

	nextTools := append([]string(nil), prevTools...)
	if !containsString(nextTools, "write") {
		if allTools, err := ctx.GetAllTools(); err == nil {
			for _, ti := range allTools {
				if ti.Name == "write" {
					nextTools = append(nextTools, "write")
					break
				}
			}
		}
	}
	if len(nextTools) > 0 && !containsString(prevTools, "write") && containsString(nextTools, "write") {
		ctx.SetActiveTools(nextTools)
	}

	m.mu.Lock()
	st := m.stateLocked(sid)
	st.planPaused = false
	st.planEnabled = true
	st.planFilePath = planFilePath
	st.planPrevTools = append([]string(nil), prevTools...)
	st.planProposeUsedTurn = false
	st.planAskUsedTurn = false
	st.planReminderCount = 0
	st.planAwaitingProgress = false
	st.pendingPlanReview = nil
	st.planHasEntered = true
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	// Let toolcmd reconcile `propose` (mounted in bash preamble vs top-level when bash transport is off).
	toolcmd.Sync(ctx)

	m.mu.Lock()
	st = m.stateLocked(sid)
	planPrompt := m.buildPlanModeActivePrompt(ctx, st)
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "plan",
		"data": map[string]any{"planFilePath": planFilePath},
	})
	if idle, err := ctx.IsIdle(); err == nil && !idle {
		_ = ctx.SendMessage("plan-mode-context", planPrompt, false, sdk.SendMessageOptions{DeliverAs: "steer"})
	}
	notifyInfo(ctx, "Plan mode enabled. Plan file: "+planFilePath)
}

func (m *manager) exitPlanMode(ctx sdk.Context, sid string, paused, silent bool) {
	m.mu.Lock()
	st := m.stateLocked(sid)
	if !st.planEnabled {
		m.mu.Unlock()
		return
	}
	prevTools := append([]string(nil), st.planPrevTools...)
	st.planEnabled = false
	st.planPaused = paused
	st.planFilePath = ""
	st.planPrevTools = nil
	st.pendingPlanReview = nil
	st.planProposeUsedTurn = false
	st.planAskUsedTurn = false
	st.planReminderCount = 0
	st.planAwaitingProgress = false
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	if len(prevTools) > 0 {
		if restored := removeString(prevTools, proposeToolName); len(restored) > 0 {
			ctx.SetActiveTools(restored)
		}
	}
	// Let toolcmd deactivate `propose` if it was presented top-level while bash transport was off.
	toolcmd.Sync(ctx)
	modeVal := "none"
	if paused {
		modeVal = "plan_paused"
	}
	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{"mode": modeVal})
	if !silent {
		if paused {
			notifyInfo(ctx, "Plan mode paused.")
		} else {
			notifyInfo(ctx, "Plan mode disabled.")
		}
	}
}

func (m *manager) disablePausedPlanMode(ctx sdk.Context, sid string) {
	m.mu.Lock()
	st := m.stateLocked(sid)
	st.planPaused = false
	st.planHasEntered = false
	m.syncStatus(ctx, st)
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{"mode": "none"})
	notifyInfo(ctx, "Plan mode disabled.")
}

// handleProposeTool implements omp's `propose` tool for submitting a plan.
// upstream: packages/coding-agent/src/tools/resolve.ts:368-389
// upstream: packages/coding-agent/src/session/agent-session.ts:2208-2223
func (m *manager) handleProposeTool(ctx sdk.Context, params map[string]any) (any, error) {
	sid := strictSessionID(ctx)
	if sid == "" {
		return nil, sdk.NewToolError("No plan is awaiting approval — `propose` only accepts a plan title while plan mode is active.")
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	enabled := st.planEnabled
	statePath := st.planFilePath
	if statePath == "" {
		statePath = defaultPlanFileURL
	}
	m.mu.Unlock()

	if !enabled {
		return nil, sdk.NewToolError("No plan is awaiting approval — `propose` only accepts a plan title while plan mode is active.")
	}
	rawTitle, _ := params["title"].(string)
	resolved, err := ResolveApprovedPlan(
		strings.TrimSpace(rawTitle),
		statePath,
		func(u string) (string, bool, error) { return readPlanFile(ctx, u) },
		func() ([]string, error) { return listPlanFiles(ctx) },
	)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}

	details := PlanApprovalDetails{
		PlanFilePath: resolved.PlanFilePath,
		Title:        resolved.Title,
		PlanExists:   true,
	}
	m.mu.Lock()
	st = m.stateLocked(sid)
	st.planFilePath = resolved.PlanFilePath
	st.planProposeUsedTurn = true
	st.planReminderCount = 0
	st.planAwaitingProgress = false
	st.pendingPlanReview = &details
	m.mu.Unlock()

	return sdk.ToolResult{
		Content: "Plan ready for review.",
		Details: details,
	}, nil
}

// runPlanReviewDialog presents omp's plan-review options via PiG's Select/Input
// dialogs and performs the selected approval/refine/save action.
//
// `inCommandCtx` indicates whether ctx is a PiG command context (from
// `/plan-review`). PiG restricts `ctx.NewSession` to command contexts
// (`coding/extension/host/subprocess/command_only_calls.go:8-21`), so when
// invoked from `agent_end` (after `propose`), "Approve and execute" cannot
// call `NewSession` directly: we omit it from the `agent_end` dialog and direct
// the user to `/plan-review` if they want a fresh-session execution, so plan
// mode is never left half-exited on failure.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:8109-8324
func (m *manager) runPlanReviewDialog(ctx sdk.Context, sid string, details PlanApprovalDetails, inCommandCtx bool) error {
	if !ctx.HasUI() {
		notifyInfo(ctx, fmt.Sprintf("Plan ready for review: %s (%s). Run /plan-review to approve.", details.Title, details.PlanFilePath))
		return nil
	}

	planContent, exists, err := readPlanFile(ctx, details.PlanFilePath)
	if err != nil || !exists {
		notifyError(ctx, fmt.Sprintf("Plan file not found at %s", details.PlanFilePath))
		return nil
	}

	for {
		var options []string
		if inCommandCtx {
			options = []string{
				"Approve and execute",
				"Approve and compact context",
				"Approve and keep context",
				"Refine plan",
				"Save",
			}
		} else {
			options = []string{
				"Approve and compact context",
				"Approve and keep context",
				"Approve in fresh session (run /plan-review)",
				"Refine plan",
				"Save",
			}
		}
		choiceVal, ok, err := ctx.Select(fmt.Sprintf("Plan mode - next step (%s)", details.Title), options)
		if err != nil || !ok {
			return err
		}
		choice, _ := choiceVal.(string)
		switch choice {
		case "Save":
			suggested := PlanSaveFileName(details.Title)
			pathVal, ok, err := ctx.Input("Save plan to path", suggested)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			dest, _ := pathVal.(string)
			dest = strings.TrimSpace(dest)
			if dest == "" {
				dest = suggested
			}
			if !filepath.IsAbs(dest) {
				dest = filepath.Join(ctx.Cwd(), dest)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				notifyError(ctx, fmt.Sprintf("Failed to save plan to %s: %v", dest, err))
				continue
			}
			if err := os.WriteFile(dest, []byte(planContent), 0o644); err != nil {
				notifyError(ctx, fmt.Sprintf("Failed to save plan to %s: %v", dest, err))
				continue
			}
			notifyInfo(ctx, fmt.Sprintf("Saved plan to %s", dest))
			continue

		case "Refine plan":
			fbVal, ok, err := ctx.Editor("Refine plan feedback", "")
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			feedback, _ := fbVal.(string)
			feedback = strings.TrimSpace(feedback)
			if feedback == "" {
				continue
			}
			return sendPromptOrSteer(ctx, feedback)

		case "Approve in fresh session (run /plan-review)":
			notifyInfo(ctx, "Run /plan-review and choose \"Approve and execute\" to start a fresh session with this plan.")
			return nil

		case "Approve and execute":
			return m.approvePlanFreshSession(ctx, sid, details, planContent)

		case "Approve and compact context":
			return m.approvePlanCompact(ctx, sid, details, planContent, inCommandCtx)

		case "Approve and keep context":
			return m.approvePlanKeepContext(ctx, sid, details, planContent)

		default:
			return nil
		}
	}
}

// approvePlanFreshSession handles "Approve and execute" from a command context.
// Plan mode stays active until `ctx.NewSession` succeeds; all post-replacement
// operations run inside the `withSession` callback against the replacement
// session's `ReplacedSessionContext` (never the stale pre-replacement `ctx`).
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7128-7293
func (m *manager) approvePlanFreshSession(ctx sdk.Context, sid string, details PlanApprovalDetails, planContent string) error {
	oldLocalRoot := resolveLocalRootDir(ctx)
	m.mu.Lock()
	st := m.stateLocked(sid)
	prevTools := append([]string(nil), st.planPrevTools...)
	m.mu.Unlock()

	var dispatchErr error
	res, err := ctx.NewSession(map[string]any{
		"withSession": sdk.WithSessionFunc(func(next sdk.ReplacedSessionContext) error {
			newCtx := next.Context
			newLocalRoot := resolveLocalRootDir(newCtx)
			_ = copyLocalArtifacts(oldLocalRoot, newLocalRoot)
			if newPath, err := resolvePlanFilePath(newCtx, details.PlanFilePath); err == nil {
				_ = os.MkdirAll(filepath.Dir(newPath), 0o755)
				_ = os.WriteFile(newPath, []byte(planContent), 0o644)
			}

			execTools := restoreExecutionTools(newCtx, prevTools)
			if newSID := strictSessionID(newCtx); newSID != "" {
				m.mu.Lock()
				nst := m.stateLocked(newSID)
				nst.planEnabled = false
				nst.planPaused = false
				nst.planReferencePath = details.PlanFilePath
				nst.planReferenceSent = true
				m.syncStatus(newCtx, nst)
				m.mu.Unlock()
			}
			_ = newCtx.AppendEntry(entryTypeModeChange, map[string]any{
				"mode": "none",
				"data": map[string]any{"approvedPlanFilePath": details.PlanFilePath},
			})
			if name := HumanizePlanTitle(details.Title); name != "" {
				if existing, err := newCtx.GetSessionName(); err == nil && (existing == nil || *existing == "") {
					_ = newCtx.SetSessionName(name)
				}
			}
			approvedPrompt := renderPlanApprovedPrompt(details.PlanFilePath, planContent, false, execTools)
			dispatchErr = newCtx.SendMessage("plan-mode-approved", approvedPrompt, false, sdk.SendMessageOptions{
				TriggerTurn: sdk.Bool(true),
				DeliverAs:   "followUp",
			})
			return nil
		}),
	})
	if err != nil {
		// Plan mode remains active; nothing was torn down.
		notifyError(ctx, fmt.Sprintf("Failed to start fresh session for plan execution: %v", err))
		return nil
	}
	if res.Cancelled {
		// Plan mode remains active.
		notifyWarn(ctx, "Plan execution cancelled.")
		return nil
	}
	// Release the old session's plan state only after NewSession succeeded.
	m.mu.Lock()
	if oldSt := m.sessions[sid]; oldSt != nil {
		oldSt.planEnabled = false
		oldSt.planPaused = false
		oldSt.planFilePath = ""
		oldSt.pendingPlanReview = nil
	}
	m.mu.Unlock()
	return dispatchErr
}

// isCompactionCancelledError reports whether an error returned by PiG's
// `CompactWithOptions` (`coding/session.go:3025-3028,3069`,
// `coding/session_summarization_auth.go:110`, `coding/extension/host/subprocess/ui_bridge.go:2758-2760`)
// represents an aborted/cancelled compaction rather than a best-effort failure.
func isCompactionCancelledError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "Compaction cancelled" || msg == "This operation was aborted"
}

// approvePlanCompact handles "Approve and compact context": compacts the
// plan-mode transcript before dispatching the approved plan prompt, and skips
// dispatch if compaction was cancelled.
//
// PiG's manual compaction (`coding/session.go:2947-2952`) aborts and waits for
// the active run to become idle (`WaitForIdle`). When called from an event
// handler such as `agent_end` (`inCommandCtx == false`), blocking the handler
// on compaction would deadlock the run that waits for the handler to return
// (`coding/extension/host/subprocess/command_only_calls.go:5-7`). Therefore,
// outside command contexts we return immediately after `CompactWithOptions`
// and finish name seeding + approved-prompt dispatch inside `OnComplete`/
// `OnError` using the captured `sessionCtx`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:7136-7293
func (m *manager) approvePlanCompact(ctx sdk.Context, sid string, details PlanApprovalDetails, planContent string, inCommandCtx bool) error {
	m.mu.Lock()
	st := m.stateLocked(sid)
	prevTools := append([]string(nil), st.planPrevTools...)
	sessionCtx := ctx
	if st.hasSessionCtx {
		sessionCtx = st.sessionCtx
	}
	m.mu.Unlock()

	m.exitPlanMode(ctx, sid, false, true)
	execTools := restoreExecutionTools(ctx, prevTools)

	m.mu.Lock()
	st = m.stateLocked(sid)
	st.planReferencePath = details.PlanFilePath
	st.planReferenceSent = false
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "none",
		"data": map[string]any{"approvedPlanFilePath": details.PlanFilePath},
	})

	finish := func(compactErr error) error {
		if isCompactionCancelledError(compactErr) {
			notifyWarn(sessionCtx, "Plan approved, but compaction was cancelled — execution not dispatched. Submit a turn to continue.")
			return nil
		}

		m.mu.Lock()
		if st := m.sessions[sid]; st != nil {
			st.planReferenceSent = true
		}
		m.mu.Unlock()

		if name := HumanizePlanTitle(details.Title); name != "" {
			if existing, err := sessionCtx.GetSessionName(); err == nil && (existing == nil || *existing == "") {
				_ = sessionCtx.SetSessionName(name)
			}
		}
		approvedPrompt := renderPlanApprovedPrompt(details.PlanFilePath, planContent, true, execTools)
		return sessionCtx.SendMessage("plan-mode-approved", approvedPrompt, false, sdk.SendMessageOptions{
			TriggerTurn: sdk.Bool(true),
			DeliverAs:   "followUp",
		})
	}

	if !inCommandCtx {
		ctx.CompactWithOptions(sdk.CompactOptions{
			CustomInstructions: renderPlanCompactInstructions(details.PlanFilePath),
			OnComplete: func(_ map[string]any) {
				if err := finish(nil); err != nil {
					notifyError(sessionCtx, fmt.Sprintf("Failed to dispatch approved plan: %v", err))
				}
			},
			OnError: func(err error) {
				if derr := finish(err); derr != nil {
					notifyError(sessionCtx, fmt.Sprintf("Failed to dispatch approved plan: %v", derr))
				}
			},
		})
		return nil
	}

	done := make(chan struct{})
	var compactErr error
	ctx.CompactWithOptions(sdk.CompactOptions{
		CustomInstructions: renderPlanCompactInstructions(details.PlanFilePath),
		OnComplete: func(_ map[string]any) {
			close(done)
		},
		OnError: func(err error) {
			compactErr = err
			close(done)
		},
	})
	<-done
	return finish(compactErr)
}

func (m *manager) approvePlanKeepContext(ctx sdk.Context, sid string, details PlanApprovalDetails, planContent string) error {
	m.mu.Lock()
	st := m.stateLocked(sid)
	prevTools := append([]string(nil), st.planPrevTools...)
	m.mu.Unlock()

	m.exitPlanMode(ctx, sid, false, true)
	execTools := restoreExecutionTools(ctx, prevTools)

	m.mu.Lock()
	st = m.stateLocked(sid)
	st.planReferencePath = details.PlanFilePath
	st.planReferenceSent = true
	m.mu.Unlock()

	_ = ctx.AppendEntry(entryTypeModeChange, map[string]any{
		"mode": "none",
		"data": map[string]any{"approvedPlanFilePath": details.PlanFilePath},
	})
	if name := HumanizePlanTitle(details.Title); name != "" {
		if existing, err := ctx.GetSessionName(); err == nil && (existing == nil || *existing == "") {
			_ = ctx.SetSessionName(name)
		}
	}
	approvedPrompt := renderPlanApprovedPrompt(details.PlanFilePath, planContent, true, execTools)
	return ctx.SendMessage("plan-mode-approved", approvedPrompt, false, sdk.SendMessageOptions{
		TriggerTurn: sdk.Bool(true),
		DeliverAs:   "followUp",
	})
}

func restoreExecutionTools(ctx sdk.Context, prevTools []string) []string {
	if len(prevTools) > 0 {
		execTools := removeString(prevTools, proposeToolName)
		if !containsString(execTools, "read") {
			execTools = append(execTools, "read")
		}
		ctx.SetActiveTools(execTools)
		return execTools
	}
	active, _ := ctx.GetActiveTools()
	return active
}

func proposeToolSchema() sdk.Schema {
	return sdk.Schema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"title"},
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "Plan slug matching local://<slug>-plan.md.",
			},
		},
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func removeString(list []string, target string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s != target {
			out = append(out, s)
		}
	}
	return out
}

func sendPromptOrSteer(ctx sdk.Context, text string) error {
	idle, err := ctx.IsIdle()
	if err == nil && !idle {
		return ctx.SendUserMessage(text, "steer")
	}
	return ctx.SendUserMessage(text, "")
}
