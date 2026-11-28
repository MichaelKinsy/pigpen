// Package github ports omp's `github` tool and `issue://`, `pr://`, `mr://`
// internal URL handlers.
//
// upstream:
//   - packages/coding-agent/src/tools/gh*.ts
//   - packages/coding-agent/src/tools/glab-mr.ts
//   - packages/coding-agent/src/tools/github-cache.ts
//   - packages/coding-agent/src/utils/{forge-cli,github,gitlab}.ts
//   - packages/coding-agent/src/internal-urls/{issue-pr-protocol,mr-protocol,forge-diff}.ts
//   - packages/tui/src/tools/{github,gh-format}.ts
//   - crates/vcs/src/git/cli.rs
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultCommandTimeout is the deadline for gh/glab and local git subprocesses.
	// upstream: utils/github.ts:14 (GH_COMMAND_TIMEOUT_MS), crates/vcs/src/git/cli.rs:40
	DefaultCommandTimeout = 5 * time.Minute

	// GitNetworkTimeout is the deadline for git fetch and git push.
	// upstream: crates/vcs/src/git/cli.rs:43, tools/gh-pr-checkout.ts:61
	GitNetworkTimeout = 30 * time.Minute

	// outputLimitBytes matches omp's 8 MiB capture cap for forge CLIs and git.
	// upstream: utils/forge-cli.ts:76, crates/vcs/src/git/cli.rs:48
	outputLimitBytes = 8 * 1024 * 1024

	// drainGrace bounds cmd.Wait after process exit when grandchildren hold pipes.
	// upstream: crates/vcs/src/git/cli.rs:52
	drainGrace = 1 * time.Second
)

type cliSpec struct {
	bin            string
	displayName    string
	installMessage string
	extraEnv       map[string]string
	timeout        time.Duration
}

var ghSpec = cliSpec{
	bin:            "gh",
	displayName:    "GitHub CLI",
	installMessage: "GitHub CLI (gh) is not installed. Install it from https://cli.github.com/.",
	extraEnv: map[string]string{
		"GH_PROMPT_DISABLED": "1",
	},
	timeout: DefaultCommandTimeout,
}

var glabSpec = cliSpec{
	bin:            "glab",
	displayName:    "GitLab CLI",
	installMessage: "GitLab CLI (glab) is not installed. Install it from https://gitlab.com/gitlab-org/cli.",
	extraEnv: map[string]string{
		"GLAB_NO_PROMPT":    "1",
		"GLAB_CHECK_UPDATE": "false",
		"NO_COLOR":          "1",
	},
	timeout: DefaultCommandTimeout,
}

type cliResult struct {
	stdout          string
	stderr          string
	exitCode        int
	stdoutTruncated bool
}

type ghCommandOptions struct {
	repoProvided bool
	trimOutput   bool
}

func defaultGhOptions() ghCommandOptions {
	return ghCommandOptions{trimOutput: true}
}

func ghAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

func glabAvailable() bool {
	_, err := exec.LookPath("glab")
	return err == nil
}

// rejectPromptCommand returns the path to `false` (or "false"), matching
// exec/non-interactive-env.ts:4 (REJECT_PROMPT_COMMAND).
func rejectPromptCommand() string {
	if p, err := exec.LookPath("false"); err == nil && p != "" {
		return p
	}
	return "false"
}

// isUTF8Locale matches crates/vcs/src/git/cli.rs:224-233.
func isUTF8Locale(val string) bool {
	lower := strings.ToLower(val)
	if strings.HasSuffix(lower, "utf-8") || strings.HasSuffix(lower, "utf8") ||
		strings.Contains(lower, ".utf-8") || strings.Contains(lower, ".utf8") {
		return true
	}
	for _, part := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == '@'
	}) {
		if part == "utf-8" || part == "utf8" {
			return true
		}
	}
	return false
}

// buildEnv constructs a subprocess environment from os.Environ(), removing unset
// keys and applying overrides. When LC_ALL is unset and had a UTF-8 value while
// LC_CTYPE is unset, LC_CTYPE is preserved so UTF-8 text handling is not
// downgraded to ASCII.
func buildEnv(unset []string, set map[string]string) []string {
	unsetSet := make(map[string]bool, len(unset))
	for _, k := range unset {
		unsetSet[k] = true
	}

	var lcAllVal string
	hasLCCType := false
	entries := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == "LC_ALL" {
			lcAllVal = v
		}
		if k == "LC_CTYPE" && v != "" {
			hasLCCType = true
		}
		if unsetSet[k] {
			continue
		}
		entries[k] = v
	}
	if unsetSet["LC_ALL"] && !hasLCCType && lcAllVal != "" && isUTF8Locale(lcAllVal) {
		entries["LC_CTYPE"] = lcAllVal
	}
	for k, v := range set {
		entries[k] = v
	}

	out := make([]string, 0, len(entries))
	for k, v := range entries {
		out = append(out, k+"="+v)
	}
	return out
}

// forgeEnv mirrors utils/forge-cli.ts:66-74 (FORGE_NON_INTERACTIVE_ENV) plus
// spec-specific variables.
func forgeEnv(extra map[string]string) []string {
	set := map[string]string{
		"GIT_ASKPASS":         "true",
		"GIT_EDITOR":          "true",
		"GIT_TERMINAL_PROMPT": "0",
		"LC_MESSAGES":         "C",
		"SSH_ASKPASS":         rejectPromptCommand(),
	}
	for k, v := range extra {
		set[k] = v
	}
	return buildEnv([]string{"LC_ALL"}, set)
}

var gitStrippedEnvVars = []string{
	"GIT_DIR",
	"GIT_COMMON_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_LITERAL_PATHSPECS",
	"GIT_GLOB_PATHSPECS",
	"GIT_NOGLOB_PATHSPECS",
	"GIT_ICASE_PATHSPECS",
	"LC_ALL",
}

// gitEnv mirrors crates/vcs/src/git/cli.rs:193-221 (apply_env).
func gitEnv() []string {
	return buildEnv(gitStrippedEnvVars, map[string]string{
		"LC_MESSAGES":         "C",
		"GIT_OPTIONAL_LOCKS":  "0",
		"GIT_ASKPASS":         "true",
		"GIT_EDITOR":          "true",
		"GIT_TERMINAL_PROMPT": "0",
		"SSH_ASKPASS":         "false",
	})
}

// ── Active process tracking (per-session) for session shutdown reaping ───────

type sessionCtxKey struct{}

func withSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

func sessionIDFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(sessionCtxKey{}).(string); ok {
		return id
	}
	return ""
}

type trackedProc struct {
	sessionID string
	cancel    context.CancelFunc
}

type procTracker struct {
	mu    sync.Mutex
	procs map[*exec.Cmd]trackedProc
}

var liveProcs = &procTracker{procs: make(map[*exec.Cmd]trackedProc)}

// errSessionEnded distinguishes a session-end cancellation from a command timeout.
var errSessionEnded = errors.New("session ended")

func (p *procTracker) add(sessionID string, cmd *exec.Cmd, cancel context.CancelFunc) {
	p.mu.Lock()
	p.procs[cmd] = trackedProc{sessionID: sessionID, cancel: cancel}
	p.mu.Unlock()
}

func (p *procTracker) remove(cmd *exec.Cmd) {
	p.mu.Lock()
	delete(p.procs, cmd)
	p.mu.Unlock()
}

// killSession cancels only child processes belonging to sessionID via their
// session-scoped context (so exec.CommandContext triggers cmd.Cancel before
// Wait reaps the PID, never signalling a recycled process group after Wait).
func (p *procTracker) killSession(sessionID string) {
	if sessionID == "" {
		return
	}
	p.mu.Lock()
	var cancels []context.CancelFunc
	for _, tp := range p.procs {
		if tp.sessionID == sessionID {
			cancels = append(cancels, tp.cancel)
		}
	}
	p.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// limitedBuffer caps captured bytes at limit. total tracks all observed bytes
// so an exact-limit capture is NOT marked truncated (matching forge-cli.test.ts).
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
	total int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	rem := b.limit - b.buf.Len()
	if rem > 0 {
		if len(p) > rem {
			b.buf.Write(p[:rem])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) truncated() bool {
	return b.total > b.limit
}

func (b *limitedBuffer) text(bin string) (string, bool) {
	t := b.buf.String()
	if b.truncated() {
		return fmt.Sprintf("%s\n[%s subprocess output truncated after 8 MiB]\n", t, bin), true
	}
	return t, false
}

func runForgeCLI(ctx context.Context, spec cliSpec, cwd string, args []string, trimOutput bool) (cliResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return cliResult{}, err
	}
	binPath, err := exec.LookPath(spec.bin)
	if err != nil {
		return cliResult{}, errors.New(spec.installMessage)
	}

	sessCtx, sessCancel := context.WithCancelCause(ctx)
	defer sessCancel(nil)
	runCtx, timeoutCancel := context.WithTimeout(sessCtx, spec.timeout)
	defer timeoutCancel()

	cmd := exec.CommandContext(runCtx, binPath, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = nil
	cmd.Env = forgeEnv(spec.extraEnv)
	cmd.WaitDelay = drainGrace
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}

	stdoutBuf := &limitedBuffer{limit: outputLimitBytes}
	stderrBuf := &limitedBuffer{limit: outputLimitBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return cliResult{}, fmt.Errorf("%s failed to start: %w", spec.displayName, err)
	}
	liveProcs.add(sessionIDFromCtx(ctx), cmd, func() { sessCancel(errSessionEnded) })

	waitErr := cmd.Wait()
	liveProcs.remove(cmd)

	if ctx.Err() != nil {
		return cliResult{}, ctx.Err()
	}
	if errors.Is(context.Cause(sessCtx), errSessionEnded) {
		return cliResult{}, context.Canceled
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return cliResult{}, fmt.Errorf("%s command timed out: %s %s", spec.displayName, spec.bin, strings.Join(args, " "))
	}
	if runCtx.Err() != nil {
		return cliResult{}, runCtx.Err()
	}

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if !errors.Is(waitErr, exec.ErrWaitDelay) {
			return cliResult{}, fmt.Errorf("%s command failed: %w", spec.displayName, waitErr)
		}
	}

	stdout, stdoutTrunc := stdoutBuf.text(spec.bin)
	stderr, _ := stderrBuf.text(spec.bin)
	if trimOutput {
		stdout = strings.TrimSpace(stdout)
		stderr = strings.TrimSpace(stderr)
	}
	return cliResult{
		stdout:          stdout,
		stderr:          stderr,
		exitCode:        exitCode,
		stdoutTruncated: stdoutTrunc,
	}, nil
}

// hardenedGitArgs mirrors crates/vcs/src/git/cli.rs:137-151: read-only
// invocations pass --no-optional-locks; mutating invocations pin
// core.fsmonitor=false and core.untrackedCache=false so transient subprocesses
// never mutate index fsmonitor/untracked-cache state.
func hardenedGitArgs(args []string, readOnly bool) []string {
	if readOnly {
		out := make([]string, 0, len(args)+1)
		hasFlag := false
		for _, a := range args {
			if a == "--no-optional-locks" {
				hasFlag = true
				break
			}
		}
		if !hasFlag {
			out = append(out, "--no-optional-locks")
		}
		return append(out, args...)
	}
	out := make([]string, 0, len(args)+4)
	out = append(out, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false")
	return append(out, args...)
}

// runGitWithTimeout runs git with omp's hardened environment, hardened argv
// prefix, detached terminal session, bounded capture, WaitDelay, process-group
// reaping, and stderr in errors.
// upstream: crates/vcs/src/git/cli.rs:137-275
func runGitWithTimeout(ctx context.Context, cwd string, timeout time.Duration, readOnly bool, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	sessCtx, sessCancel := context.WithCancelCause(ctx)
	defer sessCancel(nil)
	runCtx, timeoutCancel := context.WithTimeout(sessCtx, timeout)
	defer timeoutCancel()

	argv := hardenedGitArgs(args, readOnly)
	cmd := exec.CommandContext(runCtx, "git", argv...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = nil
	cmd.Env = gitEnv()
	cmd.WaitDelay = drainGrace
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}

	stdoutBuf := &limitedBuffer{limit: outputLimitBytes}
	stderrBuf := &limitedBuffer{limit: outputLimitBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("git failed to start: %w", err)
	}
	liveProcs.add(sessionIDFromCtx(ctx), cmd, func() { sessCancel(errSessionEnded) })

	waitErr := cmd.Wait()
	liveProcs.remove(cmd)

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(context.Cause(sessCtx), errSessionEnded) {
		return "", context.Canceled
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), timeout)
	}
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}

	stdout := strings.TrimSpace(stdoutBuf.buf.String())
	stderr := strings.TrimSpace(stderrBuf.buf.String())
	if waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay) {
		detail := stderr
		if detail == "" {
			detail = stdout
		}
		if detail != "" {
			return "", fmt.Errorf("%s", detail)
		}
		return "", waitErr
	}
	return stdout, nil
}

func runGit(ctx context.Context, cwd string, args ...string) (string, error) {
	return runGitWithTimeout(ctx, cwd, DefaultCommandTimeout, true, args...)
}

func runGitWrite(ctx context.Context, cwd string, args ...string) (string, error) {
	return runGitWithTimeout(ctx, cwd, DefaultCommandTimeout, false, args...)
}

func runGitNetwork(ctx context.Context, cwd string, args ...string) (string, error) {
	return runGitWithTimeout(ctx, cwd, GitNetworkTimeout, false, args...)
}

func formatGhFailure(args []string, stdout, stderr string, opts ghCommandOptions) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	if strings.Contains(msg, "gh auth login") || strings.Contains(msg, "not logged into any GitHub hosts") {
		return "GitHub CLI is not authenticated. Run `gh auth login`."
	}
	if !opts.repoProvided &&
		(strings.Contains(msg, "not a git repository") ||
			strings.Contains(msg, "no git remotes found") ||
			strings.Contains(msg, "unable to determine current repository")) {
		return "GitHub repository context is unavailable. Pass `repo` explicitly or run the tool inside a GitHub checkout."
	}
	if msg != "" {
		return msg
	}
	return fmt.Sprintf("GitHub CLI command failed: gh %s", strings.Join(args, " "))
}

func describeGitHubAPIError(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case map[string]any:
		if m, ok := val["message"].(string); ok && strings.TrimSpace(m) != "" {
			return strings.TrimSpace(m)
		}
		res, _ := val["resource"].(string)
		field, _ := val["field"].(string)
		code, _ := val["code"].(string)
		res = strings.TrimSpace(res)
		field = strings.TrimSpace(field)
		code = strings.TrimSpace(code)
		var targetParts []string
		if res != "" {
			targetParts = append(targetParts, res)
		}
		if field != "" {
			targetParts = append(targetParts, field)
		}
		target := strings.Join(targetParts, ".")
		if target != "" && code != "" {
			return target + ": " + code
		}
		if target != "" {
			return target
		}
		return code
	default:
		return ""
	}
}

func parseGitHubAPIErrorMessages(stdout string) []string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil || payload == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	add(describeGitHubAPIError(payload["message"]))
	if errs, ok := payload["errors"].([]any); ok {
		for _, item := range errs {
			add(describeGitHubAPIError(item))
		}
	}
	return out
}

func formatGhJSONFailure(args []string, stdout, stderr string, opts ghCommandOptions) string {
	rawMsg := strings.TrimSpace(stderr)
	if rawMsg == "" {
		rawMsg = strings.TrimSpace(stdout)
	}
	fallback := formatGhFailure(args, stdout, stderr, opts)
	if fallback != rawMsg {
		return fallback
	}
	var details []string
	for _, m := range parseGitHubAPIErrorMessages(stdout) {
		if !strings.Contains(fallback, m) {
			details = append(details, m)
		}
	}
	if len(details) == 0 {
		return fallback
	}
	var b strings.Builder
	b.WriteString(fallback)
	b.WriteString("\nGitHub details:")
	for _, d := range details {
		b.WriteString("\n- ")
		b.WriteString(d)
	}
	return b.String()
}

func ghRun(ctx context.Context, cwd string, args []string, opts ghCommandOptions) (cliResult, error) {
	if host := extractGhTargetHost(args); host != "" {
		if err := assertGhHostTrusted(sessionIDFromCtx(ctx), host); err != nil {
			return cliResult{}, err
		}
	}
	return runForgeCLI(ctx, ghSpec, cwd, args, opts.trimOutput)
}

func ghText(ctx context.Context, cwd string, args []string, opts ghCommandOptions) (string, error) {
	res, err := ghRun(ctx, cwd, args, opts)
	if err != nil {
		return "", err
	}
	if res.exitCode != 0 {
		return "", errors.New(formatGhFailure(args, res.stdout, res.stderr, opts))
	}
	return res.stdout, nil
}

func ghJSON(ctx context.Context, cwd string, args []string, dst any, opts ghCommandOptions) error {
	res, err := ghRun(ctx, cwd, args, opts)
	if err != nil {
		return err
	}
	if res.exitCode != 0 {
		return errors.New(formatGhJSONFailure(args, res.stdout, res.stderr, opts))
	}
	if strings.TrimSpace(res.stdout) == "" {
		return errors.New("GitHub CLI returned empty output.")
	}
	if err := json.Unmarshal([]byte(res.stdout), dst); err != nil {
		return errors.New("GitHub CLI returned invalid JSON output.")
	}
	return nil
}

func formatGlabFailure(args []string, stdout, stderr string) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = strings.TrimSpace(stdout)
	}
	if strings.Contains(msg, "glab auth login") ||
		strings.Contains(msg, "401 Unauthorized") ||
		strings.Contains(msg, "has not been authenticated") {
		return "GitLab CLI is not authenticated. Run `glab auth login`."
	}
	if msg != "" {
		return msg
	}
	return fmt.Sprintf("GitLab CLI command failed: glab %s", strings.Join(args, " "))
}

func glabText(ctx context.Context, cwd string, args []string, trimOutput bool) (string, error) {
	res, err := runForgeCLI(ctx, glabSpec, cwd, args, trimOutput)
	if err != nil {
		return "", err
	}
	if res.exitCode != 0 {
		return "", errors.New(formatGlabFailure(args, res.stdout, res.stderr))
	}
	return res.stdout, nil
}

func glabJSON(ctx context.Context, cwd string, args []string, dst any) error {
	res, err := runForgeCLI(ctx, glabSpec, cwd, args, true)
	if err != nil {
		return err
	}
	if res.exitCode != 0 {
		return errors.New(formatGlabFailure(args, res.stdout, res.stderr))
	}
	if strings.TrimSpace(res.stdout) == "" {
		return errors.New("GitLab CLI returned empty output.")
	}
	if err := json.Unmarshal([]byte(res.stdout), dst); err != nil {
		return errors.New("GitLab CLI returned invalid JSON output.")
	}
	return nil
}

// cwdLookupMemo caches a cwd-keyed lookup in memory for the process lifetime,
// sharing in-flight lookups detached from any individual caller's cancellation
// so aborting one waiter does not abort sibling callers on the same cwd.
// upstream: utils/forge-cli.ts:17-44 (CwdLookupMemo)
type inflightLookup struct {
	done chan struct{}
	val  string
	err  error
}

type cwdLookupMemo struct {
	mu       sync.Mutex
	values   map[string]string
	inflight map[string]*inflightLookup
	lookup   func(ctx context.Context, cwd string) (string, error)
}

func newCwdLookupMemo(lookup func(ctx context.Context, cwd string) (string, error)) *cwdLookupMemo {
	return &cwdLookupMemo{
		values:   make(map[string]string),
		inflight: make(map[string]*inflightLookup),
		lookup:   lookup,
	}
}

func (m *cwdLookupMemo) get(ctx context.Context, cwd string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	m.mu.Lock()
	if v, ok := m.values[abs]; ok {
		m.mu.Unlock()
		return v, nil
	}
	req, exists := m.inflight[abs]
	if !exists {
		req = &inflightLookup{done: make(chan struct{})}
		m.inflight[abs] = req
		go func() {
			val, lerr := m.lookup(context.Background(), abs)
			m.mu.Lock()
			if lerr == nil {
				m.values[abs] = val
			}
			delete(m.inflight, abs)
			req.val = val
			req.err = lerr
			close(req.done)
			m.mu.Unlock()
		}()
	}
	m.mu.Unlock()

	if ctx == nil {
		<-req.done
		return req.val, req.err
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-req.done:
		return req.val, req.err
	}
}

func (m *cwdLookupMemo) clear() {
	m.mu.Lock()
	clear(m.values)
	clear(m.inflight)
	m.mu.Unlock()
}
