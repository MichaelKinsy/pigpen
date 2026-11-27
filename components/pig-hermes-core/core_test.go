package pighermes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/cli"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/delegation"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/memory"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/todo"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/usage"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
)

func TestPathsAndConfigLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HERMES_HOME", tmp)
	t.Setenv("HERMES_PROFILE", "")

	p := paths.ResolveProfilePaths("")
	if p.Home != tmp || p.ProfileName != "default" {
		t.Fatalf("unexpected paths: %+v", p)
	}
	if !paths.ValidateProfileName("work-1") || paths.ValidateProfileName("../bad") {
		t.Fatalf("profile validation mismatch")
	}

	yamlCfg := "model: gpt-5\ndisplay:\n  skin: ares\nmemory:\n  memory_char_limit: 2500\nmcp_servers:\n  demo:\n    command: npx\n    args:\n      - -y\n      - demo-mcp\n"
	if err := os.WriteFile(p.ConfigFile, []byte(yamlCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p.ConfigFile, p.EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "gpt-5" || cfg.Skin != "ares" || cfg.MemoryLimit != 2500 {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	srv, ok := cfg.MCPServers["demo"]
	if !ok || srv.Command != "npx" || len(srv.Args) != 2 {
		t.Fatalf("unexpected mcp server: %+v", srv)
	}
}

func TestMemoryStoreAtomicBatchAndReplaceRejection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memories")
	s := memory.NewStore(dir, 200, 100)
	ctx := context.Background()

	res, err := s.Apply(ctx, memory.TargetMemory, []memory.Operation{
		{Action: "add", Content: "prefers terse diffs"},
		{Action: "add", Content: "uses Go 1.26"},
	})
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if len(res.Entries) != 2 || res.CharsUsed == 0 {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Upstream rejects replace in favor of patch
	if _, err := s.Apply(ctx, memory.TargetMemory, []memory.Operation{
		{Action: "replace", OldText: "Go 1.26", Content: "Go 1.27"},
	}); err == nil || !strings.Contains(err.Error(), "patch") {
		t.Fatalf("expected replace rejection pointing to patch, got %v", err)
	}

	// Patch succeeds
	res, err = s.Apply(ctx, memory.TargetMemory, []memory.Operation{
		{Action: "patch", OldText: "Go 1.26", Content: "uses Go 1.27"},
	})
	if err != nil || !strings.Contains(strings.Join(res.Entries, ","), "Go 1.27") {
		t.Fatalf("patch failed: %v %+v", err, res)
	}

	// Frozen snapshot stays stable across later writes
	snap := s.LoadSnapshot()
	if _, err := s.Apply(ctx, memory.TargetMemory, []memory.Operation{
		{Action: "add", Content: "after snapshot"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(s.FrozenSnapshot().MemoryEntries) != len(snap.MemoryEntries) {
		t.Fatalf("frozen snapshot mutated unexpectedly")
	}
}

func TestDelegationRouteResolverAndTodoAndUsageAndCLI(t *testing.T) {
	delegation.RegisterRouteResolver(delegation.RouteResolverFunc(func(req delegation.SpawnRequest) (delegation.RouteDecision, bool, error) {
		if req.Category == "deep" {
			return delegation.RouteDecision{Model: "claude-opus-4-6", ThinkingLevel: "high", Reason: "test"}, true, nil
		}
		return delegation.RouteDecision{}, false, nil
	}))
	dec, ok, err := delegation.ResolveRoute(delegation.SpawnRequest{Category: "deep"})
	if err != nil || !ok || dec.Model != "claude-opus-4-6" {
		t.Fatalf("unexpected route decision: %+v %v %v", dec, ok, err)
	}

	ts := todo.NewStore()
	items, err := ts.Replace("s1", []todo.Item{{ID: "1", Content: "ship pig-hermes", Status: "in_progress"}})
	if err != nil || len(items) != 1 || !strings.Contains(todo.FormatSummary(items), "[~]") {
		t.Fatalf("unexpected todo result: %+v %v", items, err)
	}

	ul := usage.NewLedger()
	ul.Add("s1", "", "gpt-5", 100, 50, 0.01, 1, 2)
	inTok, outTok, cost, turns, calls := ul.Totals()
	if inTok != 100 || outTok != 50 || turns != 1 || calls != 2 || cost <= 0 {
		t.Fatalf("unexpected usage totals")
	}

	cli.RegisterSubcommand("ping-test", "test subcommand", func(env clihook.Env, args []string) int {
		if len(args) == 1 && args[0] == "ok" {
			return 0
		}
		return 2
	})
	r := cli.Dispatch(clihook.Env{Args: []string{"ping-test", "ok"}})
	if !r.Handled || r.Exit != 0 {
		t.Fatalf("unexpected cli dispatch: %+v", r)
	}
	_ = json.Valid
}
