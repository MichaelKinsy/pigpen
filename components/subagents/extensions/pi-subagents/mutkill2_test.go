package pi_subagents

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A failed wait is a failed tool call (the host drops the details of a failed result); a wait that ended is a result.
func TestWaitToolResultOfAFailedWaitIsAToolError(t *testing.T) {
	res, err := waitToolResult(WaitResult{Text: "provider 'patty' disappeared", IsError: true})
	if err == nil || err.Error() != "provider 'patty' disappeared" || res != nil {
		t.Errorf("failed wait: %v, %v", res, err)
	}
	res, err = waitToolResult(WaitResult{Text: "done."})
	if err != nil || res == nil {
		t.Errorf("wait: %v, %v", res, err)
	}
}

// details.spawnBudget is the original's snapshot: null for an absent limit, the grant history with its previous limit.
func TestSpawnBudgetDetails(t *testing.T) {
	configured, limit, remaining, grantRemaining := 4, 5, 3, 3
	got := marshalJSON(spawnBudgetDetails(spawnBudgetSnapshot{Used: 2, ConfiguredLimit: &configured, Granted: 1, Limit: &limit, Remaining: &remaining, GrantRemaining: &grantRemaining,
		GrantHistory: []grantRecord{{SessionID: "s", Amount: 1, GrantedAt: 7, PreviousLimit: 4, Limit: 5}}}), "")
	eq(t, got, `{"used":2,"configuredLimit":4,"granted":1,"limit":5,"remaining":3,"grantRemaining":3,"grantHistory":[{"sessionId":"s","amount":1,"grantedAt":7,"previousLimit":4,"limit":5}]}`, "limited")
	got = marshalJSON(spawnBudgetDetails(spawnBudgetSnapshot{Used: 2, Granted: 0}), "")
	eq(t, got, `{"used":2,"configuredLimit":null,"granted":0,"limit":null,"remaining":null,"grantRemaining":null,"grantHistory":[]}`, "unlimited")
}

func TestCeilingListLimits(t *testing.T) {
	parse := func(names []string) error {
		_, err := normalizeCeilingList(toDyn(anySlice(names)), "allowedAgents")
		return err
	}
	long := func(n int) string { return strings.Repeat("a", n) }
	eq(t, parse([]string{long(128)}) == nil, true, "128 bytes")
	err := parse([]string{long(129)})
	if err == nil || !strings.Contains(err.Error(), "max 128 UTF-8 bytes") {
		t.Errorf("129 bytes: %v", err)
	}
	many := make([]string, 0, 257)
	for i := 0; i < 256; i++ {
		many = append(many, fmt.Sprintf("n%d", i))
	}
	eq(t, parse(many) == nil, true, "256 names")
	err = parse(append(many, "extra"))
	if err == nil || !strings.Contains(err.Error(), "at most 256 names") {
		t.Errorf("257 names: %v", err)
	}
}

func anySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func TestPromptDescriptionBudget(t *testing.T) {
	exact := strings.Repeat("a", maxDescriptionBytes)
	eq(t, promptDescription(exact), exact, "exactly the budget stays whole")
	eq(t, promptDescription(exact+"a"), strings.Repeat("a", maxDescriptionBytes-3)+"…", "one over is cut with an ellipsis")
}

func TestAdvertisedCatalogLeavesOutWhatDoesNotApply(t *testing.T) {
	yes := true
	mk := func(name string, source AgentSource, disabled bool) AgentConfig {
		a := AgentConfig{Name: name, Description: "d", Source: source, Advertise: &yes}
		if disabled {
			a.Disabled = &yes
		}
		return a
	}
	body := buildAdvertisedAgentCatalog([]AgentConfig{mk("shown", SourceProject, false), mk("off", SourceProject, true), mk("live", SourceRuntime, false)}, nil)
	if !strings.Contains(body, "<name>shown</name>") || strings.Contains(body, "<name>off</name>") || strings.Contains(body, "<name>live</name>") {
		t.Errorf("catalog: %s", body)
	}
	huge := mk(strings.Repeat("h", maxCatalogBytes+1), SourceProject, false)
	eq(t, strings.Contains(buildAdvertisedAgentCatalog([]AgentConfig{huge, mk("shown", SourceProject, false)}, nil), "<omitted count=\"1\" />"), true, "a name that cannot fit is counted as omitted")
}

func TestAdvertisementState(t *testing.T) {
	e := newMgmtEnv(t)
	a := newAdvertisement()
	a.hasCtx, a.cwd = true, e.dir
	writeFile(t, filepath.Join(e.dir, ".pi", "agents", "a.md"), "---\nname: a\ndescription: A\nadvertise: false\n---\n\nA.\n")
	a.refresh()
	eq(t, len(a.snapshot()), 0, "not advertised")
	writeFile(t, filepath.Join(e.dir, ".pi", "agents", "a.md"), "---\nname: a\ndescription: A\nadvertise: true\n---\n\nA.\n")
	a.refresh()
	eq(t, len(a.snapshot()), 1, "a refresh rediscovers from the files, not from the cache")

	a = newAdvertisement()
	a.err = errors.New("discovery failed")
	eq(t, a.wait(nil), a.err, "wait returns the discovery error of the catalog")

	// an older session's lookup that ends after a newer session started does not overwrite the newer root
	a = newAdvertisement()
	block := make(chan struct{})
	a.lookup = func(context.Context) string { <-block; return "first" }
	a.begin(e.dir, "")
	first := a.ready
	a.lookup = func(context.Context) string { return "second" }
	a.begin(e.dir, "")
	_ = a.wait(nil)
	close(block)
	<-first
	a.mu.Lock()
	root := *a.globalRoot
	a.mu.Unlock()
	eq(t, root, "second", "the newer generation's root")
}

func TestAcceptanceEvidenceKindsAndBlankReason(t *testing.T) {
	eq(t, isEvidenceKind("commands-run"), true, "a kind")
	eq(t, isEvidenceKind("bogus"), false, "not a kind")
	eq(t, isEvidenceKind(5), false, "not a string")
	got := validateAcceptanceInput(map[string]any{"level": "none", "reason": "   "}, "acceptance")
	eq(t, got, []string{"acceptance.reason is required when level is none."}, "a blank reason")
	eq(t, validateAcceptanceInput(map[string]any{"level": "none", "reason": "not needed"}, "acceptance"), []string{}, "a reason")
}

func TestPermissionRulesDropAllowRules(t *testing.T) {
	got := resolvePermissionRules(&permissionConfig{Rules: permissionRules{"read": "allow", "bash": "deny"}}, permissionRules{"write": "allow", "edit": "ask"})
	eq(t, got, permissionRules{"bash": "deny", "edit": "ask"}, "no allow rule survives the merge")
}
