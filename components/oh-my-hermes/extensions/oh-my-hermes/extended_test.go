package ohmyhermes_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	ohmyhermes "github.com/MichaelKinsy/pigpen/oh-my-hermes"
)

func runCLI(args ...string) (clihook.Result, string, string) {
	var out, errBuf bytes.Buffer
	res := cli.Dispatch(clihook.Env{
		Args:   args,
		Stdout: &out,
		Stderr: &errBuf,
	})
	return res, out.String(), errBuf.String()
}

func TestExtendedCLISetupQuickstartRecommendProbe(t *testing.T) {
	omhHome := setupTempOMH(t)
	hermesHome := t.TempDir()
	t.Setenv("HERMES_HOME", hermesHome)

	// Ensure extension init/registration has run.
	_ = ohmyhermes.Extension()

	// 1. omh-setup --dry-run does not create files
	res, stdout, stderr := runCLI("omh-setup", "--dry-run", "--json", "--memory-mode", "auto-safe", "--default-executor", "codex")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("omh-setup --dry-run failed: handled=%v exit=%d stderr=%s", res.Handled, res.Exit, stderr)
	}
	var dryPayload map[string]any
	if err := json.Unmarshal([]byte(stdout), &dryPayload); err != nil {
		t.Fatalf("unmarshal dry-run json: %v (stdout=%s)", err, stdout)
	}
	if dryPayload["dry_run"] != true || dryPayload["memory_mode"] != "auto-safe" || dryPayload["default_executor"] != "codex" {
		t.Fatalf("unexpected dry-run payload: %v", dryPayload)
	}
	if _, err := os.Stat(filepath.Join(omhHome, "setup-profile.json")); !os.IsNotExist(err) {
		t.Fatalf("expected setup-profile.json NOT created on --dry-run")
	}

	// 2. omh-setup --yes creates all directories, setup-profile.json, and manifest.json
	res, stdout, stderr = runCLI("omh-setup", "--yes", "--memory-mode", "review-first", "--default-executor", "hermes")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("omh-setup --yes failed: handled=%v exit=%d stderr=%s", res.Handled, res.Exit, stderr)
	}
	if !strings.Contains(stdout, "Oh My Hermes (OMH) Setup") {
		t.Errorf("expected setup summary output, got: %s", stdout)
	}
	for _, sub := range []string{"routing", "dags", "teams", "loops", "memory", "documents", "runtime"} {
		st, err := os.Stat(filepath.Join(omhHome, sub))
		if err != nil || !st.IsDir() {
			t.Errorf("expected directory %s to exist after setup", sub)
		}
	}
	for _, f := range []string{"setup-profile.json", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(omhHome, f)); err != nil {
			t.Errorf("expected %s to exist after setup: %v", f, err)
		}
	}

	// Invalid memory-mode fails with exit 2.
	res, _, _ = runCLI("omh-setup", "--memory-mode", "invalid")
	if !res.Handled || res.Exit != 2 {
		t.Errorf("expected exit 2 for invalid --memory-mode, got handled=%v exit=%d", res.Handled, res.Exit)
	}

	// 3. quickstart text & JSON
	res, stdout, stderr = runCLI("quickstart", "--source", "terminal")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("quickstart text failed: exit=%d stderr=%s", res.Exit, stderr)
	}
	for _, section := range []string{"Summary:", "First-value packs:", "Natural-language starters:", "Operator next actions:"} {
		if !strings.Contains(stdout, section) {
			t.Errorf("quickstart output missing section %q:\n%s", section, stdout)
		}
	}
	res, stdout, stderr = runCLI("quickstart", "--json", "--source", "hermes")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("quickstart --json failed: exit=%d stderr=%s", res.Exit, stderr)
	}
	var qs map[string]any
	if err := json.Unmarshal([]byte(stdout), &qs); err != nil {
		t.Fatalf("unmarshal quickstart json: %v", err)
	}
	if qs["schema"] != "omh_quickstart_card/v1" {
		t.Errorf("unexpected quickstart schema: %v", qs["schema"])
	}

	// 4. recommend text & JSON
	res, stdout, stderr = runCLI("recommend", "profile", "memory", "benchmark", "latency", "--limit", "3", "--json")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("recommend failed: exit=%d stderr=%s", res.Exit, stderr)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(stdout), &rec); err != nil {
		t.Fatalf("unmarshal recommend json: %v", err)
	}
	recs, _ := rec["recommendations"].([]any)
	if len(recs) != 3 {
		t.Fatalf("expected 3 recommendations, got %d", len(recs))
	}
	first, _ := recs[0].(map[string]any)
	if first["id"] != "ulw-perf" {
		t.Errorf("expected top recommendation ulw-perf for perf query, got %v", first["id"])
	}

	// 5. probe --parity --roadmap --json
	res, stdout, stderr = runCLI("probe", "--parity", "--roadmap", "--json")
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("probe failed: exit=%d stderr=%s", res.Exit, stderr)
	}
	var pr map[string]any
	if err := json.Unmarshal([]byte(stdout), &pr); err != nil {
		t.Fatalf("unmarshal probe json: %v", err)
	}
	if pr["parity"] == nil || pr["roadmap"] == nil {
		t.Errorf("expected parity and roadmap in probe output: %v", pr)
	}
}

func TestOnboardFlagPreprocessorAndSessionStartCard(t *testing.T) {
	setupTempOMH(t)
	t.Setenv(ohmyhermes.EnvOMHTUIOnboard, "")
	_ = ohmyhermes.Extension()

	// Unknown onboard name rejected with exit 2.
	res, _, stderr := runCLI("--onboard", "unknown-flow")
	if !res.Handled || res.Exit != 2 || !strings.Contains(stderr, "unknown --onboard target") {
		t.Fatalf("expected exit 2 for unknown --onboard target, got handled=%v exit=%d stderr=%s", res.Handled, res.Exit, stderr)
	}

	// Valid --onboard model-setup sets OMH_TUI_ONBOARD and strips flag.
	res, _, stderr = runCLI("--onboard", "model-setup", "other-arg")
	if res.Handled || stderr != "" {
		t.Fatalf("expected --onboard model-setup to pass remaining args through, got handled=%v stderr=%s", res.Handled, stderr)
	}
	if got := os.Getenv(ohmyhermes.EnvOMHTUIOnboard); got != "model-setup" {
		t.Fatalf("expected %s=model-setup, got %q", ohmyhermes.EnvOMHTUIOnboard, got)
	}
	if len(res.Args) != 1 || res.Args[0] != "other-arg" {
		t.Fatalf("expected remaining args [other-arg], got %v", res.Args)
	}

	// OnSessionStart emits guided model-setup onboarding card when OMH_TUI_ONBOARD=model-setup.
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode:  "print",
		HasUI: sdk.Bool(false),
		Cwd:   t.TempDir(),
	})
	h.FireAll(sdk.EventSessionStart, map[string]any{})
	msgs := h.CallsTo("sendMessage")
	foundCard := false
	for _, m := range msgs {
		txt, _ := m.Args["content"].(string)
		if txt == "" {
			if msgObj, ok := m.Args["message"].(map[string]any); ok {
				txt, _ = msgObj["content"].(string)
			}
		}
		if strings.Contains(txt, "OMH Guided Model Setup Onboarding") {
			foundCard = true
			break
		}
	}
	if !foundCard {
		t.Errorf("expected OnSessionStart to send guided model-setup onboarding card; sendMessage calls=%d", len(msgs))
	}
}

func TestExtendedToolsStatusHudContextRecommendProbeCapabilitiesResume(t *testing.T) {
	setupTempOMH(t)
	t.Setenv("HERMES_HOME", t.TempDir())
	t.Setenv("OMH_LSP_TOOLS", "1")
	cwd := t.TempDir()

	h := StartHost(t, ohmyhermes.Extension(), HostOptions{
		Mode: "print",
		Cwd:  cwd,
	})

	// Verify all 25 tools (21 omh_* + 4 lsp_*) are registered.
	expected25 := []string{
		"omh_delegate_route", "omh_dag", "omh_team", "omh_loop",
		"omh_todo", "omh_completion", "omh_run_summary", "omh_remember",
		"omh_roles", "omh_maestro",
		"omh_status", "omh_hud", "omh_context", "omh_recommend",
		"omh_probe", "omh_capabilities", "omh_memory", "omh_resume",
		"omh_gather_evidence", "omh_document_plan", "omh_source_trust",
		"lsp_definition", "lsp_references", "lsp_rename", "lsp_diagnostics",
	}
	for _, name := range expected25 {
		if !h.tools[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	// omh_status
	raw, errMsg := h.Tool("omh_status", map[string]any{"limit": 3})
	if errMsg != "" {
		t.Fatalf("omh_status transport error: %s", errMsg)
	}
	p, isErr := parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["schema"] != "omh_status/v1" {
		t.Fatalf("unexpected omh_status result: isErr=%v payload=%v", isErr, p)
	}

	// omh_hud across presets
	for _, preset := range []string{"minimal", "focused", "full"} {
		raw, errMsg = h.Tool("omh_hud", map[string]any{"preset": preset, "tokens_remaining": 42000})
		if errMsg != "" {
			t.Fatalf("omh_hud(%s) transport error: %s", preset, errMsg)
		}
		p, isErr = parseToolJSON(t, raw)
		if isErr || p["ok"] != true || p["preset"] != preset {
			t.Fatalf("unexpected omh_hud(%s) result: %v", preset, p)
		}
	}

	// omh_context
	raw, errMsg = h.Tool("omh_context", map[string]any{
		"message":                "Decompose this service migration into a dependency DAG",
		"source":                 "hermes",
		"limit":                  2,
		"include_prompt_context": true,
	})
	if errMsg != "" {
		t.Fatalf("omh_context transport error: %s", errMsg)
	}
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["message_sha256"] == "" || p["prompt_context"] == "" {
		t.Fatalf("unexpected omh_context result: %v", p)
	}

	// omh_recommend
	raw, errMsg = h.Tool("omh_recommend", map[string]any{"message": "audit diffs and run qa regression checks", "limit": 4})
	if errMsg != "" {
		t.Fatalf("omh_recommend transport error: %s", errMsg)
	}
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["suggested_route"] != "review" {
		t.Fatalf("expected suggested_route=review for qa query, got: %v", p)
	}

	// omh_probe
	raw, errMsg = h.Tool("omh_probe", map[string]any{"include_parity": true, "include_roadmap": true})
	if errMsg != "" {
		t.Fatalf("omh_probe transport error: %s", errMsg)
	}
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["parity"] == nil || p["roadmap"] == nil {
		t.Fatalf("unexpected omh_probe result: %v", p)
	}

	// omh_capabilities: summary, list, inspect, export
	for _, req := range []map[string]any{
		{"action": "summary"},
		{"action": "list", "section": "roles"},
		{"action": "inspect", "id": "verifier"},
		{"action": "export", "section": "evidence_boundaries"},
	} {
		raw, errMsg = h.Tool("omh_capabilities", req)
		if errMsg != "" {
			t.Fatalf("omh_capabilities(%v) transport error: %s", req, errMsg)
		}
		p, isErr = parseToolJSON(t, raw)
		if isErr || p["ok"] != true {
			t.Fatalf("omh_capabilities(%v) failed: %v", req, p)
		}
	}

	// omh_resume
	raw, errMsg = h.Tool("omh_resume", map[string]any{"limit": 5})
	if errMsg != "" {
		t.Fatalf("omh_resume transport error: %s", errMsg)
	}
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["schema"] != "omh_resume/v1" {
		t.Fatalf("unexpected omh_resume result: %v", p)
	}
}

func TestOMHMemoryFullCRUDLifecycle(t *testing.T) {
	setupTempOMH(t)
	t.Setenv("HERMES_HOME", t.TempDir())
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{Mode: "print", Cwd: t.TempDir()})

	// Capture
	raw, errMsg := h.Tool("omh_memory", map[string]any{
		"action":          "capture",
		"summary":         "Always run go test -race on concurrency changes",
		"record_type":     "lesson",
		"subject":         "go-testing",
		"tags":            []string{"go", "race"},
		"scope":           "project",
		"retention_class": "durable",
	})
	if errMsg != "" {
		t.Fatalf("capture transport error: %s", errMsg)
	}
	p, isErr := parseToolJSON(t, raw)
	if isErr || p["ok"] != true {
		t.Fatalf("capture failed: %v", p)
	}
	rec := p["record"].(map[string]any)
	recID := rec["record_id"].(string)
	if rec["status"] != "pending" {
		t.Fatalf("expected status=pending, got %v", rec["status"])
	}

	// Confirm
	raw, _ = h.Tool("omh_memory", map[string]any{"action": "confirm", "record_id": recID})
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["record"].(map[string]any)["status"] != "confirmed" {
		t.Fatalf("confirm failed: %v", p)
	}

	// Correct
	raw, _ = h.Tool("omh_memory", map[string]any{
		"action":    "correct",
		"record_id": recID,
		"summary":   "Always run go test -race -count=1 on concurrency changes",
	})
	p, isErr = parseToolJSON(t, raw)
	if isErr || !strings.Contains(p["record"].(map[string]any)["summary"].(string), "-count=1") {
		t.Fatalf("correct failed: %v", p)
	}

	// Recall
	raw, _ = h.Tool("omh_memory", map[string]any{"action": "recall", "query": "concurrency race", "limit": 5})
	p, isErr = parseToolJSON(t, raw)
	if isErr || int(p["count"].(float64)) != 1 {
		t.Fatalf("recall failed: %v", p)
	}

	// Blocks + Read
	raw, _ = h.Tool("omh_memory", map[string]any{"action": "blocks"})
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true {
		t.Fatalf("blocks failed: %v", p)
	}
	raw, _ = h.Tool("omh_memory", map[string]any{"action": "read", "label": "omh_records"})
	p, isErr = parseToolJSON(t, raw)
	if isErr || int(p["count"].(float64)) != 1 {
		t.Fatalf("read omh_records failed: %v", p)
	}

	// Retire
	raw, _ = h.Tool("omh_memory", map[string]any{"action": "retire", "record_id": recID, "reason": "superseded"})
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["record"].(map[string]any)["status"] != "retired" {
		t.Fatalf("retire failed: %v", p)
	}

	// Status reflects retired record
	raw, _ = h.Tool("omh_memory", map[string]any{"action": "status"})
	p, isErr = parseToolJSON(t, raw)
	if isErr || int(p["records_retired"].(float64)) != 1 {
		t.Fatalf("expected records_retired=1, got: %v", p)
	}
}

func TestOMHGatherEvidenceAllowlistMetacharactersAndPathEscape(t *testing.T) {
	setupTempOMH(t)
	root := t.TempDir()
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{Mode: "print", Cwd: root})

	// 1. Reject disallowed command
	raw, _ := h.Tool("omh_gather_evidence", map[string]any{
		"project_root": root,
		"commands":     []string{"rm -rf /"},
	})
	p, isErr := parseToolJSON(t, raw)
	if !isErr || !strings.Contains(p["error"].(string), "allowlist") {
		t.Fatalf("expected allowlist rejection, got isErr=%v payload=%v", isErr, p)
	}

	// 2. Reject shell metacharacters even on allowlisted prefix
	for _, bad := range []string{
		"git status; echo pwned",
		"git status | cat",
		"git status && ls",
		"go test $(whoami)",
		"go vet `id`",
		"git diff > /tmp/out",
		"git diff < /etc/passwd",
	} {
		raw, _ = h.Tool("omh_gather_evidence", map[string]any{
			"project_root": root,
			"commands":     []string{bad},
		})
		p, isErr = parseToolJSON(t, raw)
		if !isErr || !strings.Contains(p["error"].(string), "metacharacter") {
			t.Fatalf("expected metacharacter rejection for %q, got isErr=%v payload=%v", bad, isErr, p)
		}
	}

	// 3. Reject workdir escaping project_root
	raw, _ = h.Tool("omh_gather_evidence", map[string]any{
		"project_root": root,
		"workdir":      filepath.Join(root, ".."),
		"commands":     []string{"git status"},
	})
	p, isErr = parseToolJSON(t, raw)
	if !isErr || !strings.Contains(p["error"].(string), "escapes project_root") {
		t.Fatalf("expected path escape rejection, got isErr=%v payload=%v", isErr, p)
	}

	// 3b. Reject project_root escaping session cwd
	outsideRoot := t.TempDir()
	raw, _ = h.Tool("omh_gather_evidence", map[string]any{
		"project_root": outsideRoot,
		"commands":     []string{"git status"},
	})
	p, isErr = parseToolJSON(t, raw)
	if !isErr || !strings.Contains(p["error"].(string), "escapes session cwd") {
		t.Fatalf("expected project_root escape rejection, got isErr=%v payload=%v", isErr, p)
	}

	// 3c. Reject dangerous per-prefix flags and escaping operands
	for _, bad := range []string{
		"git diff --output /tmp/pwn",
		"git diff --output=/tmp/pwn",
		"git diff --ext-diff",
		"git diff --textconv",
		"git diff --no-index a b",
		"go test -exec /bin/sh ./...",
		"go test -exec=/bin/sh ./...",
		"go test -toolexec=/bin/sh ./...",
		"go vet -vettool=/bin/sh ./...",
		"go test -C .. ./...",
		"go test -o out.bin ./...",
		"go test -cpuprofile=cpu.out ./...",
		"npm test --prefix /tmp",
		"npm test -C ..",
		"pytest --rootdir=/tmp",
		"pytest -c /tmp/pytest.ini",
		"pytest -p evil",
		"pytest --basetemp=/tmp/bt",
		"git diff ../secret",
		"git diff /etc/passwd",
	} {
		raw, _ = h.Tool("omh_gather_evidence", map[string]any{
			"project_root": root,
			"commands":     []string{bad},
		})
		p, isErr = parseToolJSON(t, raw)
		if !isErr || !strings.Contains(p["error"].(string), "disallowed") {
			t.Fatalf("expected dangerous flag/arg rejection for %q, got isErr=%v payload=%v", bad, isErr, p)
		}
	}
	// Isolate child `go vet` / `go test` from any outer GOWORK in the test environment.
	t.Setenv("GOWORK", "off")
	// 4. Execute allowlisted command inside a tiny Go package in project_root
	goMod := "module example.com/evtest\n\ngo 1.22\n"
	goSrc := "package evtest\n\nfunc Add(a, b int) int { return a + b }\n"
	goTst := "package evtest\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(2, 3) != 5 { t.Fatal(\"bad\") } }\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "add.go"), []byte(goSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "add_test.go"), []byte(goTst), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, errMsg := h.Tool("omh_gather_evidence", map[string]any{
		"project_root": root,
		"commands":     []string{"go vet ./...", "go test ./..."},
		"timeout":      30,
		"truncate":     2000,
	})
	if errMsg != "" {
		t.Fatalf("omh_gather_evidence transport error: %s", errMsg)
	}
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["all_passed"] != true {
		t.Fatalf("expected allowlisted go vet + go test to pass, got isErr=%v payload=%v", isErr, p)
	}
}

func TestOMHDocumentPlanPersistenceShowAndMark(t *testing.T) {
	omhHome := setupTempOMH(t)
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{Mode: "print", Cwd: t.TempDir()})

	// Plan
	raw, errMsg := h.Tool("omh_document_plan", map[string]any{
		"action":       "plan",
		"plan_id":      "spec-doc-1",
		"source":       "docs/architecture.md",
		"lines":        250,
		"budget_chars": 8000, // 100 lines/chunk -> 3 chunks
	})
	if errMsg != "" {
		t.Fatalf("plan transport error: %s", errMsg)
	}
	p, isErr := parseToolJSON(t, raw)
	if isErr || p["ok"] != true || int(p["chunks_count"].(float64)) != 3 {
		t.Fatalf("expected 3 chunks, got isErr=%v payload=%v", isErr, p)
	}

	// Verify file persisted under <omh_home>/documents/<plan_id>/plan.json
	planFile := filepath.Join(omhHome, "documents", "spec-doc-1", "plan.json")
	if _, err := os.Stat(planFile); err != nil {
		t.Fatalf("expected persisted plan at %s: %v", planFile, err)
	}

	// Mark chunk 1 covered
	raw, _ = h.Tool("omh_document_plan", map[string]any{
		"action":  "mark",
		"plan_id": "spec-doc-1",
		"chunk":   1,
		"state":   "covered",
		"note":    "Reviewed overview",
	})
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["chunk"].(map[string]any)["state"] != "covered" {
		t.Fatalf("mark failed: %v", p)
	}

	// Show with pagination
	raw, _ = h.Tool("omh_document_plan", map[string]any{
		"action":  "show",
		"plan_id": "spec-doc-1",
		"from":    1,
		"limit":   2,
	})
	p, isErr = parseToolJSON(t, raw)
	if isErr || len(p["chunks"].([]any)) != 2 {
		t.Fatalf("show failed: %v", p)
	}
	firstChunk := p["chunks"].([]any)[0].(map[string]any)
	if firstChunk["state"] != "covered" || firstChunk["note"] != "Reviewed overview" {
		t.Errorf("expected persisted mark on chunk 1, got %v", firstChunk)
	}
}

func TestOMHSourceTrustFailClosedCeiling(t *testing.T) {
	setupTempOMH(t)
	h := StartHost(t, ohmyhermes.Extension(), HostOptions{Mode: "print", Cwd: t.TempDir()})

	// 1. Empty claims -> fail-closed (all_allowed=false)
	raw, _ := h.Tool("omh_source_trust", map[string]any{
		"topic":  "empty",
		"claims": []any{},
	})
	p, isErr := parseToolJSON(t, raw)
	if isErr || p["all_allowed"] != false || p["overall_state"] != "blocked_empty_claims" {
		t.Fatalf("expected fail-closed empty claims, got %v", p)
	}

	// 2. Full tier x claim_kind matrix
	raw, _ = h.Tool("omh_source_trust", map[string]any{
		"topic": "matrix",
		"claims": []map[string]any{
			{"tier": "upstream_official", "claim_kind": "approach", "claim": "use context.WithTimeout"},
			{"tier": "upstream_official", "claim_kind": "finding", "claim": "Go 1.22 fixed loop var"},
			{"tier": "upstream_official", "claim_kind": "completion", "claim": "docs say tests pass"},
			{"tier": "practitioner_heuristic", "claim_kind": "approach", "claim": "prefer table-driven tests"},
			{"tier": "practitioner_heuristic", "claim_kind": "finding", "claim": "blog claims zero allocs"},
			{"tier": "unattributed", "claim_kind": "approach", "claim": "heard this works"},
		},
	})
	p, isErr = parseToolJSON(t, raw)
	if isErr || p["ok"] != true || p["all_allowed"] != false {
		t.Fatalf("expected overall blocked matrix, got isErr=%v payload=%v", isErr, p)
	}
	evals := p["evaluations"].([]any)
	wantAllowed := []bool{true, true, false, true, false, false}
	if len(evals) != len(wantAllowed) {
		t.Fatalf("expected %d evals, got %d", len(wantAllowed), len(evals))
	}
	for i, want := range wantAllowed {
		got := evals[i].(map[string]any)["allowed"].(bool)
		if got != want {
			t.Errorf("claim[%d] allowed=%v, want %v (eval=%v)", i, got, want, evals[i])
		}
	}
}
