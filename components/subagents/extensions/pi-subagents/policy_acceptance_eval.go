package pi_subagents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Port of src/runs/shared/acceptance.ts: evaluating an acceptance policy against a finished child (the runtime checks, the
// host's verify commands, the review gate).

// runtimeCheck is the original's AcceptanceRuntimeCheck.
type runtimeCheck struct{ ID, Status, Message string }

type workspaceState struct {
	Kind, RepoRoot, CwdRelative, Head, DiffHash string
}

// verifyResult is the original's AcceptanceVerifyResult.
type verifyResult struct {
	ID, Command, Cwd      string
	ExitCode              *int
	Status                string // passed, failed, timed-out, allowed-failure
	Stdout, Stderr        string
	HasStdout, HasStderr  bool
	DurationMs            int64
	ArtifactPath          string
	CacheKey              string
	Memoized              *bool
	EnvKeys               []string
	EnvHash               string
	WorkspaceState        *workspaceState
	ArtifactError         string
	StructuredOutput      any
	HasStructuredOutput   bool
	StructuredOutputError string
}

type reviewFinding struct{ Severity, File, Issue, Rationale string }

// reviewResult is the original's AcceptanceReviewResult.
type reviewResult struct {
	Status   string // review-required, reviewed, blockers
	Findings []reviewFinding
}

type acceptanceRecovery struct{ Status, Reason, ReportPath, ReportHash string }

// acceptanceLedger is the original's AcceptanceLedger.
type acceptanceLedger struct {
	Status, EvidenceStatus string
	Explicit               bool
	Effective              *resolvedAcceptance
	InferredReason         []string
	Criteria               []acceptanceGate
	ChildReport            *jsObject
	ChildReportParseError  string
	Recovery               *acceptanceRecovery
	RuntimeChecks          []runtimeCheck
	VerifyRuns             []verifyResult
	ReviewResult           *reviewResult
}

func (l *acceptanceLedger) anyFailedCheck() bool {
	for _, c := range l.RuntimeChecks {
		if c.Status == "failed" {
			return true
		}
	}
	return false
}

// watchdogWarning is the part of a child's watchdog warning the acceptance gate reads.
type watchdogWarning struct {
	Severity             string
	Addressed, Stalemate bool
}

func unresolvedChildWatchdogBlockers(w []watchdogWarning) int {
	n := 0
	for _, x := range w {
		if x.Severity == "blocker" && (!x.Addressed || x.Stalemate) {
			n++
		}
	}
	return n
}

func criteriaReportMap(report *jsObject) map[string]*jsObject {
	m := map[string]*jsObject{}
	for _, item := range anyList(report.vals["criteriaSatisfied"]) {
		if o, ok := asObject(item); ok {
			if id, _ := o.vals["id"].(string); id != "" {
				m[normalizedToken(id)] = o
			}
		}
	}
	return m
}

func checkCriteriaSatisfied(criteria []acceptanceGate, report *jsObject) []runtimeCheck {
	reports := criteriaReportMap(report)
	var out []runtimeCheck
	for _, c := range criteria {
		if c.Severity == "recommended" {
			continue
		}
		item := reports[normalizedToken(c.ID)]
		switch {
		case item == nil:
			out = append(out, runtimeCheck{"criterion:" + c.ID, "failed", fmt.Sprintf("Required criterion '%s' was not reported.", c.ID)})
		case item.vals["status"] != "satisfied":
			out = append(out, runtimeCheck{"criterion:" + c.ID, "failed", fmt.Sprintf("Required criterion '%s' was reported as %s.", c.ID, jsToString(item.vals["status"]))})
		default:
			out = append(out, runtimeCheck{"criterion:" + c.ID, "passed", fmt.Sprintf("Required criterion '%s' satisfied.", c.ID)})
		}
	}
	return out
}

func reportEvidenceStatus(report *jsObject, kind string) string {
	list := func(k string) (any, bool) { v := report.vals[k]; return v, isStringArray(v) }
	switch kind {
	case "changed-files", "tests-added":
		key := "changedFiles"
		if kind == "tests-added" {
			key = "testsAddedOrUpdated"
		}
		v, ok := list(key)
		if !ok {
			return "failed"
		}
		if len(anyList(v)) == 0 {
			return "not-applicable"
		}
		return "passed"
	case "commands-run":
		if a, ok := asArray(report.vals["commandsRun"]); ok && len(a) > 0 {
			return "passed"
		}
	case "validation-output":
		if v, ok := list("validationOutput"); ok && len(anyList(v)) > 0 {
			return "passed"
		}
	case "residual-risks":
		if _, ok := list("residualRisks"); ok {
			return "passed"
		}
	case "no-staged-files":
		if report.vals["noStagedFiles"] == true {
			return "passed"
		}
	case "diff-summary":
		if s, ok := report.vals["diffSummary"].(string); ok && jsTrim(s) != "" {
			return "passed"
		}
	case "review-findings":
		if _, ok := list("reviewFindings"); ok {
			return "passed"
		}
	case "manual-notes":
		for _, k := range []string{"manualNotes", "notes"} {
			if s, ok := report.vals[k].(string); ok {
				return map[bool]string{true: "passed", false: "failed"}[jsTrim(s) != ""]
			}
		}
	}
	return "failed"
}

// plainGit runs git (no fsmonitor override: the acceptance checks use the repository as it is configured).
func plainGit(cwd string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	hideWindow(cmd)
	err = cmd.Run()
	return out.String(), errb.String(), err
}

func checkNoStagedFiles(cwd string) runtimeCheck {
	out, _, err := plainGit(cwd, "status", "--short")
	if err != nil {
		return runtimeCheck{"no-staged-files", "not-applicable", "git status unavailable; no staged-files check skipped"}
	}
	var staged []string
	for _, line := range regexp.MustCompile(`\r?\n`).Split(out, -1) {
		if len(line) >= 2 && line[0] != ' ' && line[0] != '?' {
			staged = append(staged, line)
		}
	}
	if len(staged) == 0 {
		return runtimeCheck{"no-staged-files", "passed", "No staged files detected."}
	}
	return runtimeCheck{"no-staged-files", "failed", "Staged files present: " + strings.Join(staged, ", ")}
}

// captureStagedIndexBaseline is the repository-wide index tree; an error when git cannot give trustworthy evidence.
func captureStagedIndexBaseline(cwd string) (string, error) {
	out, stderr, err := plainGit(cwd, "write-tree")
	oid := jsTrim(out)
	if err != nil || oid == "" {
		detail := jsTrim(stderr)
		if detail != "" {
			detail = ": " + detail
		} else {
			detail = "."
		}
		return "", errors.New("Unable to capture staged index baseline" + detail)
	}
	return oid, nil
}

func checkStagedIndexUnchanged(cwd, baseline string) runtimeCheck {
	terminal, err := captureStagedIndexBaseline(cwd)
	switch {
	case err != nil:
		return runtimeCheck{"staged-index-unchanged", "failed", err.Error()}
	case terminal == baseline:
		return runtimeCheck{"staged-index-unchanged", "passed", "Staged index matches the launch baseline."}
	}
	return runtimeCheck{"staged-index-unchanged", "failed", "Staged index changed after launch."}
}

func runStructuralChecks(a *resolvedAcceptance, report *jsObject, cwd string) []runtimeCheck {
	var checks []runtimeCheck
	for _, kind := range a.Evidence {
		if kind == "no-staged-files" && a.PreserveStagedIndex {
			continue
		}
		if kind == "no-staged-files" && !(report.hasOwn("noStagedFiles") && !isUndefined(report.vals["noStagedFiles"])) {
			continue
		}
		status := reportEvidenceStatus(report, kind)
		msg := kind + " evidence missing from child report."
		switch status {
		case "passed":
			msg = kind + " evidence present."
		case "not-applicable":
			msg = kind + " evidence explicitly reported as not applicable."
		}
		checks = append(checks, runtimeCheck{"evidence:" + kind, status, msg})
	}
	if !a.PreserveStagedIndex && contains(a.Evidence, "no-staged-files") {
		checks = append(checks, checkNoStagedFiles(cwd))
	}
	return checks
}

func trimOutput(value string) (string, bool) {
	t := jsTrim(value)
	if t == "" {
		return "", false
	}
	if u := utf16.Encode([]rune(t)); len(u) > 12000 {
		return string(utf16.Decode(u[:12000])) + "\n...[truncated]", true
	}
	return t, true
}

var sensitiveEnvKey = regexp.MustCompile(`(?i)(?:^|_)(?:TOKEN|SECRET|PASSWORD|PASS|AUTH|CREDENTIAL|COOKIE|SESSION|PRIVATE|API_KEY|ACCESS_KEY)(?:_|$)`)

func effectiveVerifyEnv(env map[string]string) map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			out[k] = v
		}
	}
	for k, v := range env {
		out[k] = v
	}
	return out
}

func redactVerifyEnv(value string, env map[string]string) string {
	var secrets []string
	seen := map[string]bool{}
	for k, v := range effectiveVerifyEnv(env) {
		if len(utf16.Encode([]rune(v))) >= 4 && sensitiveEnvKey.MatchString(k) && !seen[v] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, s := range secrets {
		value = strings.ReplaceAll(value, s, "[REDACTED]")
	}
	return value
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

const defaultVerifyTimeoutMs = 120000

func readVerifyWorkspaceState(cwd string) *workspaceState {
	top, _, err := plainGit(cwd, "rev-parse", "--show-toplevel")
	if err != nil || jsTrim(top) == "" {
		return nil
	}
	repoRoot, err := filepath.EvalSymlinks(jsTrim(top))
	if err != nil {
		return nil
	}
	head, _, herr := plainGit(repoRoot, "rev-parse", "HEAD")
	diff, _, derr := plainGit(repoRoot, "diff", "--binary", "--full-index", "HEAD", "--")
	if herr != nil || derr != nil || jsTrim(head) == "" {
		return nil
	}
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil
	}
	rel, err := filepath.Rel(repoRoot, real)
	if err != nil || rel == "" {
		rel = "."
	}
	return &workspaceState{Kind: "git-tracked", RepoRoot: repoRoot, CwdRelative: rel, Head: jsTrim(head), DiffHash: sha256Hex(diff)}
}

// verifyOptions are what a verify run reads of its caller.
type verifyOptions struct {
	Done         <-chan struct{}
	AbortMessage string
	ArtifactsDir string
	RunID        string
}

// limitedBuffer keeps the first max bytes of a stream.
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		b.buf.Write(p[:room])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }

func runVerifyCommand(c verifyCommand, defaultCwd string, o verifyOptions) verifyResult {
	started := time.Now()
	cwd := defaultCwd
	if c.Cwd != "" {
		cwd = nodeResolve(defaultCwd, c.Cwd)
	}
	res := verifyResult{ID: c.ID, Command: c.Command, Cwd: cwd}
	finish := func(r verifyResult) verifyResult {
		r.ID, r.Command, r.Cwd, r.DurationMs = c.ID, c.Command, cwd, time.Since(started).Milliseconds()
		return r
	}
	cmd := shellCommand(quoteExecutableForShell(c.Command, hostPlatform()))
	cmd.Dir = cwd
	env := effectiveVerifyEnv(c.Env)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdout, stderr := &limitedBuffer{max: 1 << 20}, &limitedBuffer{max: 1 << 20}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		status := "failed"
		if c.AllowFailure {
			status = "allowed-failure"
		}
		one := 1
		return finish(verifyResult{ExitCode: &one, Status: status, Stderr: redactVerifyEnv(err.Error(), c.Env), HasStderr: true})
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	timeoutMs := c.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = defaultVerifyTimeoutMs
	}
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	timedOut, hardKilled := false, false
	var werr error
	select {
	case werr = <-waited:
	case <-timer.C:
		timedOut = true
	case <-o.Done:
		timedOut = true
	}
	if timedOut {
		terminate(cmd.Process)
		select {
		case werr = <-waited:
		case <-time.After(time.Second):
			_ = cmd.Process.Kill()
			hardKilled = true
			werr = <-waited
		}
	}
	outText, hasOut := trimOutput(redactVerifyEnv(stdout.String(), c.Env))
	errRaw := stderr.String()
	if errRaw == "" && timedOut {
		errRaw = o.AbortMessage
		if errRaw == "" && hardKilled {
			errRaw = "Acceptance verification timed out."
		}
	}
	errText, hasErr := trimOutput(redactVerifyEnv(errRaw, c.Env))
	res = verifyResult{Stdout: outText, HasStdout: hasOut, Stderr: errText, HasStderr: hasErr}
	var ee *exec.ExitError
	var code *int
	if errors.As(werr, &ee) {
		n := ee.ExitCode()
		if n >= 0 {
			code = &n
		}
	} else if werr == nil {
		zero := 0
		code = &zero
	}
	if timedOut {
		res.ExitCode, res.Status = nil, "timed-out"
	} else {
		res.ExitCode = code
		switch {
		case code != nil && *code == 0:
			res.Status = "passed"
		case c.AllowFailure:
			res.Status = "allowed-failure"
		default:
			res.Status = "failed"
		}
	}
	return finish(res)
}

const typedVerifyOutputMaxChars = 12000

// applyTypedVerifyOutput: a passing `output: "json"` command must print one JSON document; its value becomes the run's
// structured output, anything else turns the pass into a failure so the verdict is not silently dropped.
func applyTypedVerifyOutput(c verifyCommand, run *verifyResult) {
	if run.Status != "passed" {
		return
	}
	fail := func(m string) { run.Status, run.StructuredOutputError = "failed", m }
	switch {
	case jsTrim(run.Stdout) == "":
		fail(`output: "json" command printed nothing on stdout.`)
		return
	case strings.HasSuffix(run.Stdout, "...[truncated]"):
		fail(fmt.Sprintf(`output: "json" stdout exceeded %d characters and was truncated.`, typedVerifyOutputMaxChars))
		return
	}
	value, msg := jsJSONParse(run.Stdout)
	if msg != "" {
		fail(`output: "json" stdout is not valid JSON: ` + msg)
		return
	}
	if c.Schema != nil {
		if ok, m := validateStructuredOutputValue(c.Schema, value); !ok {
			fail(`output: "json" stdout does not match gate.schema: ` + m)
			return
		}
	}
	run.StructuredOutput, run.HasStructuredOutput = value, true
}

// typedVerifyOutput is the first typed verify result of a ledger.
func typedVerifyOutput(l *acceptanceLedger) (any, bool) {
	if l != nil {
		for _, r := range l.VerifyRuns {
			if r.HasStructuredOutput {
				return r.StructuredOutput, true
			}
		}
	}
	return nil, false
}

func (r verifyResult) object() *jsObject {
	o := newObject()
	o.set("id", r.ID)
	o.set("command", r.Command)
	o.set("cwd", r.Cwd)
	if r.ExitCode != nil {
		o.set("exitCode", float64(*r.ExitCode))
	} else {
		o.set("exitCode", nil)
	}
	o.set("status", r.Status)
	if r.HasStdout {
		o.set("stdout", r.Stdout)
	}
	if r.HasStderr {
		o.set("stderr", r.Stderr)
	}
	o.set("durationMs", float64(r.DurationMs))
	return o
}

func cachedVerifyResult(v any) (verifyResult, bool) {
	o, ok := asObject(v)
	if !ok {
		return verifyResult{}, false
	}
	id, idOK := o.vals["id"].(string)
	cmd, cmdOK := o.vals["command"].(string)
	status, _ := o.vals["status"].(string)
	dur, durOK := asNumber(o.vals["durationMs"])
	var code *int
	if f, isNum := asNumber(o.vals["exitCode"]); isNum {
		n := int(f)
		code = &n
	} else if o.vals["exitCode"] != nil {
		return verifyResult{}, false
	}
	if !idOK || !cmdOK || !durOK || !contains([]string{"passed", "failed", "timed-out", "allowed-failure"}, status) {
		return verifyResult{}, false
	}
	r := verifyResult{ID: id, Command: cmd, ExitCode: code, Status: status, DurationMs: int64(dur)}
	r.Stdout, r.HasStdout = o.vals["stdout"].(string)
	r.Stderr, r.HasStderr = o.vals["stderr"].(string)
	return r, true
}

// runMemoizedVerifyCommand reuses the verdict of an identical command on an identical tracked Git state (failures included);
// typed commands read inputs that change without the tree changing, so they are never reused.
func runMemoizedVerifyCommand(c verifyCommand, defaultCwd string, o verifyOptions) verifyResult {
	cwd := defaultCwd
	if c.Cwd != "" {
		cwd = nodeResolve(defaultCwd, c.Cwd)
	}
	state := readVerifyWorkspaceState(cwd)
	if state == nil || o.ArtifactsDir == "" || o.RunID == "" || c.Output == "json" {
		return runVerifyCommand(c, defaultCwd, o)
	}
	envKeysSet := map[string]bool{}
	for k := range c.Env {
		envKeysSet[k] = true
	}
	envKeys := sortedKeys(envKeysSet)
	full := effectiveVerifyEnv(c.Env)
	keys := make([]string, 0, len(full))
	for k := range full {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	envObj := newObject()
	for _, k := range keys {
		envObj.set(k, full[k])
	}
	envHash := sha256Hex(marshalJSON(envObj, ""))
	timeoutMs := c.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = defaultVerifyTimeoutMs
	}
	keyObj := newObject()
	keyObj.set("version", 1.0)
	keyObj.set("command", c.Command)
	keyObj.set("cwdRelative", state.CwdRelative)
	keyObj.set("envKeys", anyStrings(envKeys))
	keyObj.set("envHash", envHash)
	keyObj.set("timeoutMs", float64(timeoutMs))
	keyObj.set("allowFailure", c.AllowFailure)
	keyObj.set("head", state.Head)
	keyObj.set("diffHash", state.DiffHash)
	cacheKey := sha256Hex(marshalJSON(keyObj, ""))
	artifactPath := filepath.Join(o.ArtifactsDir, "acceptance", "verify", o.RunID, cacheKey+".json")
	if data, err := os.ReadFile(artifactPath); err == nil {
		if v, msg := jsJSONParse(string(data)); msg == "" {
			if co, ok := asObject(v); ok && co.vals["cacheKey"] == cacheKey {
				if r, ok := cachedVerifyResult(co.vals["result"]); ok {
					yes := true
					r.ID, r.Command, r.Cwd, r.ArtifactPath, r.CacheKey, r.Memoized, r.EnvKeys, r.EnvHash, r.WorkspaceState = c.ID, c.Command, cwd, artifactPath, cacheKey, &yes, envKeys, envHash, state
					return r
				}
			}
		}
	}
	r := runVerifyCommand(c, defaultCwd, o)
	no := false
	r.ArtifactPath, r.CacheKey, r.Memoized, r.EnvKeys, r.EnvHash, r.WorkspaceState = artifactPath, cacheKey, &no, envKeys, envHash, state
	rec := newObject()
	rec.set("version", 1.0)
	rec.set("cacheKey", cacheKey)
	rec.set("command", c.Command)
	rec.set("cwdRelative", state.CwdRelative)
	rec.set("envKeys", anyStrings(envKeys))
	rec.set("envHash", envHash)
	rec.set("timeoutMs", float64(timeoutMs))
	rec.set("allowFailure", c.AllowFailure)
	ws := newObject()
	ws.set("kind", state.Kind)
	ws.set("repoRoot", state.RepoRoot)
	ws.set("cwdRelative", state.CwdRelative)
	ws.set("head", state.Head)
	ws.set("diffHash", state.DiffHash)
	rec.set("workspaceState", ws)
	rec.set("result", r.object())
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o777); err == nil {
		err = os.WriteFile(artifactPath, []byte(marshalJSON(rec, "  ")), 0o666)
		if err != nil {
			r.ArtifactError, r.ArtifactPath = err.Error(), ""
		}
	} else {
		r.ArtifactError, r.ArtifactPath = err.Error(), ""
	}
	return r
}

// acceptanceEval is the input of evaluateAcceptance.
type acceptanceEval struct {
	Acceptance          *resolvedAcceptance
	Output, Cwd         string
	StagedIndexBaseline string
	FileOutput          *acceptanceFileOutput
	Report              any // a structured report, when HasReport (otherwise it is parsed from the output)
	HasReport           bool
	ReportError         string
	ReviewResult        *reviewResult
	Done                <-chan struct{}
	AbortMessage        string
	ReportOptional      bool
	ArtifactsDir, RunID string
	Watchdog            []watchdogWarning
	HasWatchdog         bool
}

func newLedger(a *resolvedAcceptance, status string) *acceptanceLedger {
	return &acceptanceLedger{Status: status, EvidenceStatus: status, Explicit: a.Explicit, Effective: a, InferredReason: a.InferredReason, Criteria: a.Criteria, RuntimeChecks: []runtimeCheck{}, VerifyRuns: []verifyResult{}}
}

func isDone(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// evaluateAcceptance judges a finished child against its acceptance policy.
func evaluateAcceptance(in acceptanceEval) *acceptanceLedger {
	a := in.Acceptance
	initial := "claimed"
	if a.Level == "none" {
		initial = "not-required"
	}
	ledger := newLedger(a, initial)
	if a.Level == "none" {
		return ledger
	}
	if in.HasWatchdog {
		if unresolvedChildWatchdogBlockers(in.Watchdog) > 0 {
			ledger.RuntimeChecks = append(ledger.RuntimeChecks, runtimeCheck{"watchdog-blocker", "failed", "Unresolved watchdog blocker (details are available in child watchdog status)."})
		} else {
			ledger.RuntimeChecks = append(ledger.RuntimeChecks, runtimeCheck{"watchdog-blocker", "passed", "No unresolved watchdog blockers."})
		}
	}
	var parsed acceptanceReportParse
	switch {
	case in.ReportError != "":
		parsed = acceptanceReportParse{Error: in.ReportError}
	case in.HasReport:
		if r, errs := validateAcceptanceReport(in.Report, ""); r != nil {
			parsed = acceptanceReportParse{Report: r}
		} else {
			parsed = acceptanceReportParse{Error: "Failed to parse acceptance-report: Invalid acceptance-report: " + strings.Join(errs, "; "), Malformed: true}
		}
	default:
		parsed = parseAcceptanceReportSources(in.Output, in.FileOutput)
	}
	if parsed.Malformed && parsed.SourcePath != "" && in.FileOutput != nil && in.FileOutput.Authoritative && in.FileOutput.Durable {
		ledger.Recovery = &acceptanceRecovery{Status: "available-for-review", Reason: "acceptance-metadata-rejected", ReportPath: parsed.SourcePath, ReportHash: sha256Hex(in.FileOutput.Content)}
	}
	needsReport := acceptanceRequiresChildReport(a)
	switch {
	case parsed.Report != nil:
		ledger.ChildReport = parsed.Report
		ledger.Status, ledger.EvidenceStatus = "attested", "attested"
	case !in.ReportOptional || needsReport || parsed.Error != acceptanceReportNotFound:
		ledger.ChildReportParseError = parsed.Error
		msg := parsed.Error
		if msg == "" {
			msg = "Structured acceptance report missing."
		}
		ledger.RuntimeChecks = append(ledger.RuntimeChecks, runtimeCheck{"attestation", "failed", msg})
		if ledger.Recovery != nil {
			ledger.Status, ledger.EvidenceStatus = "rejected", "rejected"
		}
		if !in.ReportOptional {
			ledger.Status, ledger.EvidenceStatus = "rejected", "rejected"
			return ledger
		}
	default:
		ledger.ChildReportParseError = parsed.Error
	}
	if parsed.Report != nil && acceptanceLevelRank[a.Level] >= acceptanceLevelRank["checked"] {
		ledger.RuntimeChecks = append(ledger.RuntimeChecks, checkCriteriaSatisfied(a.Criteria, parsed.Report)...)
		ledger.RuntimeChecks = append(ledger.RuntimeChecks, runStructuralChecks(a, parsed.Report, in.Cwd)...)
		if !ledger.anyFailedCheck() {
			ledger.Status, ledger.EvidenceStatus = "checked", "checked"
		}
	}
	if acceptanceLevelRank[a.Level] >= acceptanceLevelRank["verified"] && (a.Level == "verified" || len(a.Verify) > 0) {
		if a.Level == "verified" && len(a.Verify) == 0 {
			ledger.RuntimeChecks = append(ledger.RuntimeChecks, runtimeCheck{"verification-config", "failed", "verified acceptance requires runtime verify commands."})
			ledger.Status, ledger.EvidenceStatus = "rejected", "rejected"
		} else {
			ledger.VerifyRuns = []verifyResult{}
			for _, c := range a.Verify {
				run := runMemoizedVerifyCommand(c, in.Cwd, verifyOptions{Done: in.Done, AbortMessage: in.AbortMessage, ArtifactsDir: in.ArtifactsDir, RunID: in.RunID})
				if c.Output == "json" {
					applyTypedVerifyOutput(c, &run)
				}
				ledger.VerifyRuns = append(ledger.VerifyRuns, run)
				if isDone(in.Done) {
					break
				}
			}
			failed := false
			for _, r := range ledger.VerifyRuns {
				failed = failed || r.Status == "failed" || r.Status == "timed-out"
			}
			switch {
			case failed:
				ledger.Status, ledger.EvidenceStatus = "rejected", "rejected"
			case !ledger.anyFailedCheck():
				ledger.Status, ledger.EvidenceStatus = "verified", "verified"
			}
		}
	}
	if a.PreserveStagedIndex {
		if in.StagedIndexBaseline != "" {
			ledger.RuntimeChecks = append(ledger.RuntimeChecks, checkStagedIndexUnchanged(in.Cwd, in.StagedIndexBaseline))
		} else {
			ledger.RuntimeChecks = append(ledger.RuntimeChecks, runtimeCheck{"staged-index-unchanged", "failed", "Staged index launch baseline is unavailable."})
		}
	}
	if ledger.Status == "rejected" || ledger.anyFailedCheck() {
		ledger.Status, ledger.EvidenceStatus = "rejected", "rejected"
		return ledger
	}
	if ledger.Status == "claimed" {
		ledger.Status = a.Level
		ledger.EvidenceStatus = ledger.Status
	}
	if a.Review != nil {
		switch {
		case in.ReviewResult != nil && in.ReviewResult.Status == "reviewed":
			ledger.ReviewResult = in.ReviewResult
			ledger.Status = "reviewed"
		case in.ReviewResult != nil && in.ReviewResult.Status == "blockers":
			ledger.ReviewResult = in.ReviewResult
			ledger.Status = "rejected"
		case a.Review.Required == nil || *a.Review.Required:
			ledger.ReviewResult = in.ReviewResult
			if ledger.ReviewResult == nil {
				ledger.ReviewResult = &reviewResult{Status: "review-required", Findings: []reviewFinding{{Severity: "non-blocking", Issue: "Independent review has not been supplied.", Rationale: "The run cannot be marked reviewed from child evidence alone."}}}
			}
			ledger.Status = "review-required"
		}
	}
	return ledger
}

// buildSkippedAcceptanceLedger is the ledger of a run whose acceptance was not evaluated (a failed check unless none is required).
func buildSkippedAcceptanceLedger(a *resolvedAcceptance, id, message string) *acceptanceLedger {
	status := "rejected"
	if a.Level == "none" {
		status = "not-required"
	}
	l := newLedger(a, status)
	if a.Level != "none" {
		l.RuntimeChecks = []runtimeCheck{{id, "failed", message}}
	}
	return l
}

// acceptanceFailureMessage is why a rejected ledger was rejected ("" when it was not).
func acceptanceFailureMessage(l *acceptanceLedger) string {
	if l.Status != "rejected" {
		return ""
	}
	for _, c := range l.RuntimeChecks {
		if c.Status == "failed" {
			return "Acceptance rejected: " + c.Message
		}
	}
	for _, r := range l.VerifyRuns {
		if r.Status == "failed" || r.Status == "timed-out" {
			if r.StructuredOutputError != "" {
				return fmt.Sprintf("Acceptance verification '%s' failed: %s", r.ID, r.StructuredOutputError)
			}
			return fmt.Sprintf("Acceptance verification '%s' %s.", r.ID, r.Status)
		}
	}
	if l.ReviewResult != nil && l.ReviewResult.Status == "blockers" {
		return "Acceptance review found blockers."
	}
	return "Acceptance rejected."
}
