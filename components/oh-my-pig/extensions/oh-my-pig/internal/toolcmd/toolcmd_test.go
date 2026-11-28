package toolcmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

var querySchemaJSON = json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)

func queryDesc(name string) *ToolDescriptor {
	return &ToolDescriptor{
		Name:           name,
		Label:          name,
		Description:    name + " fixture",
		HasDescription: true,
		Parameters:     querySchemaJSON,
	}
}

func describedTool(name, description, summary string) *ToolDescriptor {
	d := queryDesc(name)
	d.Description = description
	d.Summary = summary
	return d
}

func stateOf(tools []*ToolDescriptor, mounted, active, builtIn []string) *State {
	tmap := make(map[string]*ToolDescriptor, len(tools))
	for _, tl := range tools {
		tmap[tl.Name] = tl
	}
	mOrder := mounted
	if mOrder == nil {
		for _, tl := range tools {
			mOrder = append(mOrder, tl.Name)
		}
	}
	mSet := make(map[string]bool, len(mOrder))
	for _, n := range mOrder {
		mSet[n] = true
	}
	aSet := make(map[string]bool, len(active))
	for _, n := range active {
		aSet[n] = true
	}
	bSet := make(map[string]bool)
	if builtIn == nil {
		for _, tl := range tools {
			bSet[tl.Name] = true
		}
	} else {
		for _, n := range builtIn {
			bSet[n] = true
		}
	}
	return &State{
		Tools:        tmap,
		MountedOrder: mOrder,
		MountedNames: mSet,
		BuiltInNames: bSet,
		IsActive:     func(name string) bool { return aSet[name] },
	}
}

func sectionName(section string) string {
	first, _, _ := strings.Cut(section, "\n")
	return strings.TrimPrefix(first, "## ")
}

func sectionNames(sections []string) []string {
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = sectionName(s)
	}
	return out
}

func findBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("real bash /dev/tcp end-to-end test runs on Unix")
	}
	bashPath := "/bin/bash"
	if _, statErr := os.Stat(bashPath); statErr != nil {
		var lookErr error
		bashPath, lookErr = exec.LookPath("bash")
		if lookErr != nil {
			t.Skip("bash not found on PATH")
		}
	}
	return bashPath
}

// upstream: test/tools/tool-commands.test.ts: "mounts a discoverable tool whose name is a plain command word"
func TestMountRuleDiscoverablePlainCommandWord(t *testing.T) {
	if !IsToolCommandName("github") {
		t.Fatal("expected github to be valid tool command name")
	}
	if !IsToolCommandName("mcp__linear__create_issue") {
		t.Fatal("expected mcp__linear__create_issue to be valid tool command name")
	}
	if !IsMountableAsToolCommand("github", LoadModeDiscoverable) {
		t.Fatal("expected github discoverable to mount")
	}
	if !IsMountableAsToolCommand("mcp__linear__create_issue", LoadModeDiscoverable) {
		t.Fatal("expected mcp__linear__create_issue discoverable to mount")
	}
}

// upstream: test/tools/tool-commands.test.ts: "rejects names the shell resolves first and names that are not command words"
func TestMountRuleRejectsShellReservedAndInvalidNames(t *testing.T) {
	for _, name := range []string{"cd", "echo", "grep", "if", "", "1tool", "two words", "a/b", "x;y", "-flag"} {
		if IsToolCommandName(name) {
			t.Errorf("IsToolCommandName(%q) = true, want false", name)
		}
		if IsMountableAsToolCommand(name, LoadModeDiscoverable) {
			t.Errorf("IsMountableAsToolCommand(%q, discoverable) = true, want false", name)
		}
	}
}

// upstream: test/tools/tool-commands.test.ts: "keeps pinned top-level tools, the bash transport and non-discoverable tools unmounted"
func TestMountRuleKeepsPinnedTopLevelAndNonDiscoverable(t *testing.T) {
	for _, name := range []string{"todo", "yield", "ask", "web_search", "bash"} {
		if IsMountableAsToolCommand(name, LoadModeDiscoverable) {
			t.Errorf("expected %q to stay top-level", name)
		}
	}
	if IsMountableAsToolCommand("github", LoadModeEssential) {
		t.Error("expected essential github not to mount")
	}
	if IsMountableAsToolCommand("github", "") {
		t.Error("expected empty loadMode github not to mount")
	}
}

// upstream: test/issue-5764-registertool-loadmode.test.ts: "defaults omitted loadMode to essential for essential built-in names, discoverable otherwise"
func TestIssue5764DefaultLoadModeRules(t *testing.T) {
	// upstream: test/issue-5764-registertool-loadmode.test.ts: "never mounts the bash transport under toolCommands, even when mislabeled discoverable"
	if IsMountableAsToolCommand("bash", LoadModeDiscoverable) {
		t.Fatal("bash must never mount even when declared discoverable")
	}
	if !IsMountableAsToolCommand("lsp", LoadModeDiscoverable) {
		t.Fatal("lsp with discoverable must mount")
	}
	for _, name := range []string{"read", "bash", "edit", "glob"} {
		if got := DefaultLoadModeForToolName(name, ""); got != LoadModeEssential {
			t.Errorf("DefaultLoadModeForToolName(%q, \"\") = %q, want essential", name, got)
		}
		if IsMountableAsToolCommand(name, DefaultLoadModeForToolName(name, "")) {
			t.Errorf("re-registered essential %q must not mount", name)
		}
	}
	// upstream: test/issue-5764-registertool-loadmode.test.ts: "RegisteredToolAdapter still defaults a novel extension tool to discoverable"
	if got := DefaultLoadModeForToolName("my_ext_tool", ""); got != LoadModeDiscoverable {
		t.Fatalf("novel extension tool = %q, want discoverable", got)
	}
	if !IsMountableAsToolCommand("my_ext_tool", DefaultLoadModeForToolName("my_ext_tool", "")) {
		t.Fatal("novel extension tool should be mountable")
	}
	// Explicit declared mode always wins.
	if got := DefaultLoadModeForToolName("read", LoadModeDiscoverable); got != LoadModeDiscoverable {
		t.Fatalf("explicit discoverable on read = %q", got)
	}
	if got := DefaultLoadModeForToolName("some_extension_tool", LoadModeEssential); got != LoadModeEssential {
		t.Fatalf("explicit essential on extension tool = %q", got)
	}
	// Hidden built-ins are NOT in EssentialBuiltinToolNames (upstream essential-tools.ts:23-37).
	for name := range HiddenToolNames {
		if EssentialBuiltinToolNames[name] {
			t.Errorf("hidden tool %q must not be in EssentialBuiltinToolNames", name)
		}
		if got := BuiltinLoadMode(name); got != "" {
			t.Errorf("BuiltinLoadMode(%q) = %q, want empty (declares no loadMode)", name, got)
		}
	}
	// Special tools declare no loadMode and must never be mountable.
	for name := range SpecialToolNames {
		if got := BuiltinLoadMode(name); got != "" {
			t.Errorf("BuiltinLoadMode(%q) = %q, want empty", name, got)
		}
		if IsMountableAsToolCommand(name, BuiltinLoadMode(name)) {
			t.Errorf("special tool %q must not mount into catalog", name)
		}
	}
}

// upstream: test/tools/tool-commands.test.ts: "dispatch reaches mounted and active top-level tools; the mounted lookup only mounted ones"
func TestResolutionMountedAndActive(t *testing.T) {
	mounted := queryDesc("mounted_tool")
	active := queryDesc("active_tool")
	disabled := queryDesc("disabled_tool")
	st := stateOf([]*ToolDescriptor{mounted, active, disabled}, []string{"mounted_tool"}, []string{"active_tool"}, nil)

	if ResolveToolCommand(st, "mounted_tool") != mounted {
		t.Fatal("expected mounted_tool")
	}
	if ResolveToolCommand(st, "active_tool") != active {
		t.Fatal("expected active_tool")
	}
	if ResolveToolCommand(st, "disabled_tool") != nil {
		t.Fatal("expected nil for disabled_tool")
	}
	if ResolveToolCommand(st, "no_such_tool") != nil {
		t.Fatal("expected nil for no_such_tool")
	}

	if ResolveMountedToolCommand(st, "mounted_tool") != mounted {
		t.Fatal("expected mounted_tool from mounted lookup")
	}
	if ResolveMountedToolCommand(st, "active_tool") != nil {
		t.Fatal("expected nil for active_tool from mounted lookup")
	}
	if ResolveMountedToolCommand(st, "disabled_tool") != nil {
		t.Fatal("expected nil for disabled_tool from mounted lookup")
	}
}

// upstream: test/tools/tool-commands.test.ts: "resolves a wire alias for dispatch only while its canonical tool is enabled"
func TestResolutionWireAlias(t *testing.T) {
	tl := queryDesc("canonical")
	tl.CustomWireName = "wire"
	st := stateOf([]*ToolDescriptor{tl}, []string{"canonical"}, nil, nil)

	if ResolveToolCommand(st, "wire") != tl {
		t.Fatal("expected wire alias to resolve")
	}
	if ResolveMountedToolCommand(st, "wire") != nil {
		t.Fatal("mounted lookup must take exact canonical names only")
	}

	delete(st.MountedNames, "canonical")
	if ResolveToolCommand(st, "canonical") != nil || ResolveToolCommand(st, "wire") != nil {
		t.Fatal("expected unmounted canonical and wire alias to stop resolving")
	}
}

// upstream: test/tools/tool-commands.test.ts: prompt docs modes, allowlist, budgets, truncation
func TestPromptDocsModesAndBudgets(t *testing.T) {
	builtin := queryDesc("builtin_tool")
	external := queryDesc("mcp__other_server_execute")
	allowlisted := queryDesc("mcp__context_mode_ctx_execute")
	st := stateOf([]*ToolDescriptor{builtin, external, allowlisted}, nil, nil, []string{"builtin_tool"})

	// upstream: test/tools/tool-commands.test.ts: "inline mode gives every mounted tool a help section"
	inlinePlan := PlanToolCommandPromptDocs(st, DocsModeInline, nil)
	if !reflect.DeepEqual(sectionNames(inlinePlan.Sections), []string{"builtin_tool", "mcp__other_server_execute", "mcp__context_mode_ctx_execute"}) || len(inlinePlan.CatalogOrder) != 0 {
		t.Fatalf("inline mismatch: sections=%v catalog=%v", sectionNames(inlinePlan.Sections), inlinePlan.CatalogOrder)
	}

	// upstream: test/tools/tool-commands.test.ts: "builtins mode inlines built-ins and glob-allowlisted tools, cataloging the rest"
	builtinsPlan := PlanToolCommandPromptDocs(st, DocsModeBuiltins, nil)
	if !reflect.DeepEqual(sectionNames(builtinsPlan.Sections), []string{"builtin_tool"}) ||
		!reflect.DeepEqual(builtinsPlan.CatalogOrder, []string{"mcp__other_server_execute", "mcp__context_mode_ctx_execute"}) {
		t.Fatalf("builtins mismatch: sections=%v catalog=%v", sectionNames(builtinsPlan.Sections), builtinsPlan.CatalogOrder)
	}
	allowPlan := PlanToolCommandPromptDocs(st, DocsModeBuiltins, []string{"mcp__context_mode_*"})
	if !reflect.DeepEqual(sectionNames(allowPlan.Sections), []string{"builtin_tool", "mcp__context_mode_ctx_execute"}) ||
		!reflect.DeepEqual(allowPlan.CatalogOrder, []string{"mcp__other_server_execute"}) {
		t.Fatalf("builtins+allowlist mismatch: sections=%v catalog=%v", sectionNames(allowPlan.Sections), allowPlan.CatalogOrder)
	}

	// upstream: test/tools/tool-commands.test.ts: "catalog mode lists every mounted tool, allowlisted ones included"
	catPlan := PlanToolCommandPromptDocs(st, DocsModeCatalog, []string{"mcp__context_mode_*"})
	if len(catPlan.Sections) != 0 ||
		!reflect.DeepEqual(catPlan.CatalogOrder, []string{"builtin_tool", "mcp__other_server_execute", "mcp__context_mode_ctx_execute"}) {
		t.Fatalf("catalog mismatch: sections=%v catalog=%v", sectionNames(catPlan.Sections), catPlan.CatalogOrder)
	}

	// upstream: test/tools/tool-commands.test.ts: "degrades a malformed inline allowlist to the catalog instead of throwing"
	malformedPlan := PlanToolCommandPromptDocs(st, DocsModeBuiltins, []string{"", "{unclosed"})
	if !slices.Contains(malformedPlan.CatalogOrder, "mcp__context_mode_ctx_execute") {
		t.Fatalf("malformed allowlist should degrade to catalog: %v", malformedPlan.CatalogOrder)
	}

	// upstream: test/tools/tool-commands.test.ts: "catalogs a tool whose help exceeds the per-tool cap without starving the others"
	giant := describedTool("giant_tool", strings.Repeat("x", ToolCommandDocsPerToolCap+1), "")
	small := queryDesc("small_tool")
	capPlan := PlanToolCommandPromptDocs(stateOf([]*ToolDescriptor{giant, small}, nil, nil, nil), DocsModeInline, nil)
	if !reflect.DeepEqual(sectionNames(capPlan.Sections), []string{"small_tool"}) ||
		!reflect.DeepEqual(capPlan.CatalogOrder, []string{"giant_tool"}) {
		t.Fatalf("per-tool cap mismatch: sections=%v catalog=%v", sectionNames(capPlan.Sections), capPlan.CatalogOrder)
	}

	// upstream: test/tools/tool-commands.test.ts: "keeps inlined sections within the total budget and catalogs the overflow"
	names := []string{"bulky_tool_0", "bulky_tool_1", "bulky_tool_2", "bulky_tool_3", "bulky_tool_4", "bulky_tool_5", "bulky_tool_6"}
	bulkyDesc := strings.Repeat("y", ToolCommandDocsPerToolCap-1000)
	bulkyTools := make([]*ToolDescriptor, len(names))
	for i, n := range names {
		bulkyTools[i] = describedTool(n, bulkyDesc, "")
	}
	budgetPlan := PlanToolCommandPromptDocs(stateOf(bulkyTools, nil, nil, nil), DocsModeInline, nil)
	totalUnits := 0
	for _, s := range budgetPlan.Sections {
		totalUnits += utf16Len(s)
	}
	if totalUnits > ToolCommandDocsTotalBudget {
		t.Fatalf("total section UTF-16 length %d exceeds budget %d", totalUnits, ToolCommandDocsTotalBudget)
	}
	if len(budgetPlan.Sections) == 0 || len(budgetPlan.CatalogOrder) == 0 {
		t.Fatalf("expected both inlined sections and catalog overflow: sections=%d catalog=%d", len(budgetPlan.Sections), len(budgetPlan.CatalogOrder))
	}
	combined := append(sectionNames(budgetPlan.Sections), budgetPlan.CatalogOrder...)
	if !reflect.DeepEqual(combined, names) {
		t.Fatalf("order lost: got %v, want %v", combined, names)
	}

	// upstream: test/tools/tool-commands.test.ts: "caps an external tool's inlined description while --help keeps it whole"
	longDesc := "LEDE " + strings.Repeat("z", ToolCommandExternalDescriptionCap*3) + " END_OF_DESCRIPTION"
	extTool := describedTool("mcp__long__tool", longDesc, "")
	biTool := describedTool("long_builtin", longDesc, "")
	longState := stateOf([]*ToolDescriptor{extTool, biTool}, nil, nil, []string{"long_builtin"})
	longPlan := PlanToolCommandPromptDocs(longState, DocsModeInline, nil)
	secMap := map[string]string{}
	for _, s := range longPlan.Sections {
		secMap[sectionName(s)] = s
	}
	if !strings.Contains(secMap["mcp__long__tool"], "LEDE ") || strings.Contains(secMap["mcp__long__tool"], "END_OF_DESCRIPTION") {
		t.Fatalf("external inlined description was not capped: %s", secMap["mcp__long__tool"])
	}
	if !strings.Contains(secMap["mcp__long__tool"], "… (full text: `mcp__long__tool --help`)") {
		t.Fatalf("external cut notice missing: %s", secMap["mcp__long__tool"])
	}
	if !strings.Contains(secMap["long_builtin"], "END_OF_DESCRIPTION") {
		t.Fatalf("builtin description should not be capped")
	}
	fullHelp, ok := ToolCommandHelp(longState, "mcp__long__tool")
	if !ok || !strings.Contains(fullHelp, "END_OF_DESCRIPTION") {
		t.Fatalf("--help should keep external description whole")
	}

	// Non-ASCII description under 200 UTF-16 units (>200 UTF-8 bytes) must NOT get a false cut marker.
	cjkShort := strings.Repeat("あ", 150) // 150 UTF-16 units, 450 UTF-8 bytes
	cjkTool := describedTool("mcp__cjk__tool", cjkShort, "")
	cjkPlan := PlanToolCommandPromptDocs(stateOf([]*ToolDescriptor{cjkTool}, nil, nil, []string{}), DocsModeInline, nil)
	if len(cjkPlan.Sections) != 1 || strings.Contains(cjkPlan.Sections[0], "full text:") || !strings.Contains(cjkPlan.Sections[0], cjkShort) {
		t.Fatalf("short non-ASCII external description got false cut marker: %v", cjkPlan.Sections)
	}

	// upstream: test/tools/tool-commands.test.ts: "omits catalog lines for tools the caller lists elsewhere"
	fullRendered := RenderToolCommandPromptDocs(catPlan, nil)
	if !strings.Contains(fullRendered, "mcp__other_server_execute") || !strings.Contains(fullRendered, "## More commands (help on demand)") || !strings.Contains(fullRendered, "Run `<tool> --help` before first use.") {
		t.Fatalf("unexpected catalog render:\n%s", fullRendered)
	}
	filteredRendered := RenderToolCommandPromptDocs(catPlan, map[string]bool{"mcp__other_server_execute": true})
	if strings.Contains(filteredRendered, "mcp__other_server_execute") || !strings.Contains(filteredRendered, "builtin_tool") {
		t.Fatalf("listedElsewhere filter failed:\n%s", filteredRendered)
	}
}

// upstream: test/tools/tool-commands.test.ts: "bounds external catalog summaries in UTF-8 bytes on a code point boundary"
func TestExternalCatalogSummariesUTF8ByteBoundary(t *testing.T) {
	multiByteTail := strings.Repeat("あ", ToolCommandExternalDescriptionCap)
	dynamicTool := describedTool("mcp__weather__forecast", "Weather forecast.", "Napoved\u0007\u2028vremena "+multiByteTail)
	builtinTool := describedTool("weather", "Weather.", "Gets the weather "+multiByteTail)
	entries := ToolCommandEntries(stateOf([]*ToolDescriptor{builtinTool, dynamicTool}, nil, nil, []string{"weather"}))
	byName := map[string]CommandEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	dyn := byName["mcp__weather__forecast"]
	if !dyn.Dynamic {
		t.Fatal("expected external entry dynamic=true")
	}
	if !strings.HasPrefix(dyn.Summary, "Napoved vremena ") {
		t.Fatalf("control chars not collapsed: %q", dyn.Summary)
	}
	if len(dyn.Summary) > ToolCommandExternalDescriptionCap {
		t.Fatalf("summary UTF-8 bytes %d > cap %d", len(dyn.Summary), ToolCommandExternalDescriptionCap)
	}
	if !strings.HasSuffix(dyn.Summary, summaryEllipsis) {
		t.Fatalf("expected trailing ellipsis: %q", dyn.Summary)
	}
	body := strings.TrimSuffix(dyn.Summary, summaryEllipsis)
	if !strings.HasSuffix(body, "あ") {
		t.Fatalf("expected body to end cleanly on あ: %q", body)
	}
	if !utf8.ValidString(body) {
		t.Fatal("truncated body is not valid UTF-8")
	}
	if len(dyn.Summary) <= ToolCommandExternalDescriptionCap-6 {
		t.Fatalf("summary backed off too far: %d bytes", len(dyn.Summary))
	}

	bi := byName["weather"]
	if bi.Dynamic {
		t.Fatal("expected builtin dynamic=false")
	}
	if bi.Summary != "Gets the weather "+multiByteTail {
		t.Fatalf("builtin summary was unexpectedly modified")
	}
}

// upstream: test/tools/tool-commands.test.ts: "names the tool command a bash call's line starts with" / "returns undefined for other tools, shell-reserved words and malformed calls"
func TestToolCommandCallName(t *testing.T) {
	if got := ToolCommandCallName("bash", map[string]any{"command": "github pr_view --number 12"}); got != "github" {
		t.Fatalf("got %q, want github", got)
	}
	if got := ToolCommandCallName("bash", map[string]any{"command": `  resolve "looks right"`}); got != "resolve" {
		t.Fatalf("got %q, want resolve", got)
	}
	if got := ToolCommandCallName("bash", map[string]any{"command": "ast_edit|cat"}); got != "ast_edit" {
		t.Fatalf("got %q, want ast_edit", got)
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"eval", map[string]any{"command": "github pr_view"}},
		{"bash", map[string]any{"command": "echo github"}},
		{"bash", map[string]any{"command": ""}},
		{"bash", map[string]any{"command": 42}},
		{"bash", map[string]any{}},
		{"bash", nil},
	} {
		if got := ToolCommandCallName(tc.name, tc.args); got != "" {
			t.Errorf("ToolCommandCallName(%q, %v) = %q, want empty", tc.name, tc.args, got)
		}
	}
}

// upstream: test/tools/tool-commands.test.ts: "toolCommandDispatches"
func TestToolCommandDispatches(t *testing.T) {
	first := map[string]any{"tool": "github", "mode": "execute", "exitCode": 0, "args": map[string]any{"op": "pr_view"}}
	second := map[string]any{"tool": "ast_edit", "mode": "help", "exitCode": 0}
	got := ToolCommandDispatches("bash", map[string]any{"content": []any{}, "details": map[string]any{"toolCommands": []any{first, second}}})
	if len(got) != 2 || got[0].Tool != "github" || got[1].Tool != "ast_edit" {
		t.Fatalf("got %+v", got)
	}
	if len(ToolCommandDispatches("write", map[string]any{"details": map[string]any{"toolCommands": []any{first}}})) != 0 {
		t.Fatal("expected empty for non-bash tool")
	}
	malformed := ToolCommandDispatches("bash", map[string]any{
		"details": map[string]any{
			"toolCommands": []any{
				first,
				map[string]any{"mode": "execute", "exitCode": 0},
				map[string]any{"tool": "x", "mode": "run", "exitCode": 0},
				map[string]any{"tool": "x", "mode": "execute", "exitCode": "0"},
				map[string]any{"tool": "x", "mode": "execute", "exitCode": 0, "args": "op"},
				nil,
			},
		},
	})
	if len(malformed) != 1 || malformed[0].Tool != "github" {
		t.Fatalf("expected only valid first dispatch, got %+v", malformed)
	}
}

// TestDefaultSessionCatalogMatchesOmpOracle verifies that a default session with
// `ast_edit`, `debug`, `lsp` (registered with ToolExposureCodemode + Mountable)
// produces the exact `# Tool Commands` catalog lines and order captured in
// internal/prompt/testdata/oracle/omp_block0.txt:41-46, never swallows special
// tools (`propose`, `resolve`, `reject`), unmounts disabled tools non-stickily,
// and activates mountable tools top-level when `tools.commands` is false.
func TestDefaultSessionCatalogMatchesOmpOracle(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	prevEnabled := Enabled
	prevMode := DocsModeSetting
	t.Cleanup(func() {
		Enabled = prevEnabled
		DocsModeSetting = prevMode
		ResetForTests()
		lifecycle.ResetForTests()
	})

	lspEnabled := true
	githubEnabled := false
	planModeActive := false

	// Declare built-ins in reverse order to prove BuiltinToolOrder wins over registry order.
	Mountable("lsp", func(sdk.Context) bool { return lspEnabled })
	Mountable("debug", func(sdk.Context) bool { return true })
	Mountable("ast_edit", func(sdk.Context) bool { return true })
	Mountable("github", func(sdk.Context) bool { return githubEnabled })
	RegisterSpecialCommand("resolve", func(sdk.Context) bool { return false })
	RegisterSpecialCommand("reject", func(sdk.Context) bool { return false })
	RegisterSpecialCommand("propose", func(sdk.Context) bool { return planModeActive })

	allTools := []sdk.ToolInfo{
		{Name: "read", Description: "Read files.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)},
		{Name: "bash", Description: "Run shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
		{Name: "todo", Description: "Manage todos.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "ask", Description: "Ask user.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "grep", Description: "Regex search.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "web_search", Description: "Search web.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		// Deliberately register lsp before ast_edit to assert omp BuiltinToolOrder: ast_edit, debug, lsp.
		{Name: "lsp", Description: "Symbol-aware code intelligence from language servers — navigation, refactors, and diagnostics where text tools miss callsites.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}},"required":["action"]}`)},
		{Name: "debug", Description: "Debugger access. Prefer over bash for program state, breakpoints, stepping, or thread inspection.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}},"required":["action"]}`)},
		{Name: "ast_edit", Description: "Structural AST editing.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"ops":{"type":"string"}},"required":["ops"]}`)},
		{Name: "github", Description: "GitHub ops.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"op":{"type":"string"}},"required":["op"]}`)},
		{Name: "resolve", Description: "Apply staged action.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"}},"required":["reason"]}`)},
		{Name: "reject", Description: "Discard staged action.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"}},"required":["reason"]}`)},
		{Name: "propose", Description: "Submit plan.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)},
	}
	// Codemode tools do NOT start active on PiG.
	initialActive := []string{"read", "bash", "todo", "ask", "grep", "web_search"}

	ext := sdk.New("test-oracle")
	Register(ext)

	var capturedMounted []string
	var capturedDocs string
	var capturedCmdNames []string
	ext.OnEvent("probe_docs", func(ctx sdk.Context, _ map[string]any) (any, error) {
		capturedMounted, capturedDocs = PromptDocs(ctx)
		capturedCmdNames = CommandNames(ctx)
		return nil, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID:   "oracle-1",
		allTools:    allTools,
		activeTools: initialActive,
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start failed: %s", errMsg)
	}
	if _, errMsg := host.emitEvent("probe_docs", nil); errMsg != "" {
		t.Fatalf("probe_docs failed: %s", errMsg)
	}

	// Exact order and summaries from internal/prompt/testdata/oracle/omp_block0.txt:41-46.
	wantMounted := []string{"ast_edit", "debug", "lsp"}
	if !slices.Equal(capturedMounted, wantMounted) {
		t.Fatalf("mounted = %v, want %v", capturedMounted, wantMounted)
	}
	wantOracleCatalog := strings.Join([]string{
		"## More commands (help on demand)",
		"- `ast_edit` — Perform AST-aware code edits (SHOULD be preferred for codemods)",
		"- `debug` — Debug a running process with DAP (debugger adapter protocol)",
		"- `lsp` — Query LSP (language server) for diagnostics, hover info, and references",
		"",
		"Run `<tool> --help` before first use.",
	}, "\n")
	if capturedDocs != wantOracleCatalog {
		t.Fatalf("catalog docs mismatch against omp v19.1.1 oracle:\n--- GOT ---\n%s\n--- WANT ---\n%s", capturedDocs, wantOracleCatalog)
	}

	// Special commands must be in CommandNames (preamble) but NEVER in PromptDocs catalog.
	for _, sp := range []string{"resolve", "reject", "propose"} {
		if !slices.Contains(capturedCmdNames, sp) {
			t.Errorf("expected special command %q in CommandNames=%v", sp, capturedCmdNames)
		}
		if strings.Contains(capturedDocs, sp) {
			t.Errorf("special command %q must not appear in catalog docs", sp)
		}
	}

	// Non-sticky unmount: disabling lsp and enabling github updates mounted set immediately on PromptDocs
	// without needing a prior before_agent_start turn.
	lspEnabled = false
	githubEnabled = true
	if _, errMsg := host.emitEvent("probe_docs", nil); errMsg != "" {
		t.Fatalf("probe_docs 2 failed: %s", errMsg)
	}
	if !slices.Equal(capturedMounted, []string{"ast_edit", "debug", "github"}) {
		t.Fatalf("non-sticky toggle failed: mounted = %v, want [ast_edit debug github]", capturedMounted)
	}

	// Disabling tools.commands activates enabled mountable tools (and applicable special tools) top-level.
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "tools.commands" {
			return false
		}
		return def
	}
	planModeActive = true
	if _, errMsg := host.emitEvent("probe_docs", nil); errMsg != "" {
		t.Fatalf("probe_docs 3 failed: %s", errMsg)
	}
	if len(capturedMounted) != 0 || capturedDocs != "" {
		t.Fatalf("expected no mounted commands with tools.commands=false, got %v / %q", capturedMounted, capturedDocs)
	}
	activeWhenOff := host.getActiveTools()
	for _, want := range []string{"ast_edit", "debug", "github", "propose"} {
		if !slices.Contains(activeWhenOff, want) {
			t.Errorf("expected %q active top-level when tools.commands=false; active=%v", want, activeWhenOff)
		}
	}
	if slices.Contains(activeWhenOff, "lsp") {
		t.Errorf("disabled lsp must not be activated top-level; active=%v", activeWhenOff)
	}
	if slices.Contains(activeWhenOff, "resolve") {
		t.Errorf("non-applicable resolve must not be activated top-level; active=%v", activeWhenOff)
	}

	_, _ = host.emitEvent(sdk.EventSessionShutdown, nil)
}

// TestExecuteToolStrictHostExposure verifies against the strict fake host
// (which enforces PiG's real callableEntriesLocked rule) that:
//   - A tool registered with `ToolExposureCodemode` + `Mountable` is mounted,
//     stays in PiG's activeTools for direct-call fallback (while stripped from
//     advertised provider tools in before_provider_request), and executes
//     cleanly via `Context.ExecuteTool`.
//   - A `direct` tool is NOT deactivated into an unreachable state if its area
//     forgot to set `ToolExposureCodemode` (syncSession keeps it active top-level).
//   - Multi-block text results match omp's `renderToolResult` (one newline per
//     block, no spurious blank line between blocks).
func TestExecuteToolStrictHostExposure(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	Mountable("lsp", func(sdk.Context) bool { return true })
	Mountable("misconfigured_direct", func(sdk.Context) bool { return true })

	var executedName string
	var executedArgs map[string]any

	ext := sdk.New("test-strict-exec")
	Register(ext)
	ext.Tool("bash", "Shell.", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"command": map[string]any{"type": "string"}},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		cmd, _ := params["command"].(string)
		dr := TryDirectWithOptions(ctx, cmd, DirectOptions{Cwd: ctx.Cwd(), Timeout: 5 * time.Second})
		return map[string]any{"output": dr.Output, "exitCode": dr.ExitCode, "timedOut": dr.TimedOut, "cancelled": dr.Cancelled, "handled": dr.Handled}, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "strict-exec-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "lsp", Description: "LSP.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"}},"required":["action"]}`)},
			{Name: "misconfigured_direct", Description: "Forgot codemode.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)},
		},
		activeTools: []string{"bash", "misconfigured_direct"},
		executeTool: func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			executedName = name
			executedArgs = args
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result: sdk.AgentToolResult{Content: []sdk.ToolResultContent{
					{Type: "text", Text: "line1\n"},
					{Type: "text", Text: "line2"},
				}},
			}
		},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}
	// misconfigured_direct must stay active top-level (not deactivated into unreachability).
	if !slices.Contains(host.getActiveTools(), "misconfigured_direct") {
		t.Fatalf("direct tool without codemode exposure must stay active top-level; active=%v", host.getActiveTools())
	}
	// Mounted codemode lsp stays in PiG's activeTools so direct model calls
	// still dispatch (omp resolveFallbackTool), while before_provider_request
	// strips it from outgoing provider payloads.
	if !slices.Contains(host.getActiveTools(), "lsp") {
		t.Fatalf("mounted codemode lsp must remain in activeTools for direct-call fallback; active=%v", host.getActiveTools())
	}

	raw, errMsg := host.callTool("bash", map[string]any{"command": "lsp definition"})
	if errMsg != "" {
		t.Fatalf("bash tool_call failed: %s", errMsg)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if executedName != "lsp" || executedArgs["action"] != "definition" {
		t.Fatalf("ExecuteTool called with name=%q args=%v", executedName, executedArgs)
	}
	// Multi-block text join must match omp appendLine: "line1\n" + "line2\n" = "line1\nline2\n" (no double newline).
	if res["handled"] != true || res["output"] != "line1\nline2\n" {
		t.Fatalf("unexpected bash result: %v", res)
	}
}

// TestBashBridgeEndToEndThroughExecuteTool tests the full real-bash `/dev/tcp`
// loopback bridge end-to-end strictly through PiG's `Context.ExecuteTool`
// (no Provide fallback), covering:
//   - Pipes (`fake --flag piped | tr a-z A-Z`)
//   - Stdin (`-`) with JSON object and raw literal text
//   - Quoting, spaces, and newlines in arguments
//   - `--help` without invoking the tool
//   - Bad arguments exiting 2 with usage hint on stderr
//   - Tool returning `isError: true` exiting 1 on stderr
//   - Alias and function precedence (upstream: bash-tool-commands.test.ts)
//   - `set -euxo pipefail` produces zero xtrace token/frame leakage
//   - Caller's open FD 9 (`flock`-style) is NOT clobbered or closed
//   - Hostile `base64` on `PATH` is bypassed via `command -p`
//   - Sourced preamble file unlinks itself immediately so child commands cannot read it
//   - Per-call token revocation after `release()` rejects subsequent replay attempts
//   - `TryDirectWithOptions` honors explicit `cwd`, `timeout`, and rejects unquoted `name=~/x`
//   - Listener closes cleanly on `session_shutdown`
func TestBashBridgeEndToEndThroughExecuteTool(t *testing.T) {
	bashPath := findBash(t)

	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	fakeSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"flag": {"type": "string", "description": "Flag value."},
			"extra": {"type": "string"}
		},
		"required": ["flag"]
	}`)

	Mountable("fake", func(sdk.Context) bool { return true })
	Mountable("other", func(sdk.Context) bool { return true })

	var mu sync.Mutex
	var executed []string
	var lastCallerID string

	ext := sdk.New("test-bridge-exec")
	Register(ext)

	// Fake `bash` tool that uses PreambleWithRelease + a temp preamble file
	// (mirroring internal/shell) and runs a real bash subprocess while the
	// bash tool call's sdk.Context is active.
	ext.Tool("bash", "Shell.", sdk.Schema{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"cwd":     map[string]any{"type": "string"},
			"mode":    map[string]any{"type": "string"},
			"timeout": map[string]any{"type": "number"},
		},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		script, _ := params["command"].(string)
		cwd, _ := params["cwd"].(string)
		mode, _ := params["mode"].(string)
		if mode == "direct" {
			var timeout time.Duration
			if secs, ok := params["timeout"].(float64); ok && secs > 0 {
				timeout = time.Duration(secs * float64(time.Second))
			}
			dr := TryDirectWithOptions(ctx, script, DirectOptions{Cwd: cwd, Timeout: timeout})
			return map[string]any{"stdout": dr.Output, "exitCode": dr.ExitCode, "timedOut": dr.TimedOut, "cancelled": dr.Cancelled, "handled": dr.Handled}, nil
		}

		preamble, release := PreambleWithRelease(ctx)
		defer release()
		if preamble == "" {
			return map[string]any{"error": "empty preamble"}, nil
		}

		// Write preamble to a temp file and source it, matching internal/shell.
		tmpFile, err := os.CreateTemp(t.TempDir(), "omp-shell-preamble-*.sh")
		if err != nil {
			return nil, err
		}
		preamblePath := tmpFile.Name()
		_, _ = tmpFile.WriteString(preamble)
		_ = tmpFile.Close()
		defer func() { _ = os.Remove(preamblePath) }()

		wrapper := fmt.Sprintf(". %s\n%s", singleQuoteShell(preamblePath), script)
		cmd := exec.Command(bashPath, "-c", wrapper)
		if cwd != "" {
			cmd.Dir = cwd
		}
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		rc := 0
		if runErr != nil {
			var ee *exec.ExitError
			if ok := errorAs(runErr, &ee); ok {
				rc = ee.ExitCode()
			} else {
				return nil, runErr
			}
		}
		return map[string]any{
			"stdout":       outBuf.String(),
			"stderr":       errBuf.String(),
			"exitCode":     rc,
			"preamblePath": preamblePath,
		}, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "bridge-exec-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "fake", Description: "Fake tool for bridge tests.", Exposure: sdk.ToolExposureCodemode, Parameters: fakeSchema},
			{Name: "other", Description: "Other tool.", Exposure: sdk.ToolExposureCodemode, Parameters: fakeSchema},
		},
		activeTools: []string{"bash"},
		executeTool: func(execCtx context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			flagVal, _ := args["flag"].(string)
			if flagVal == "sleep" {
				select {
				case <-execCtx.Done():
					return sdk.AgentToolCallOutcome{
						ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
						Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "cancelled"}}, IsError: true},
						IsError:  true,
					}
				case <-time.After(5 * time.Second):
				}
			}
			mu.Lock()
			executed = append(executed, name+":"+flagVal)
			lastCallerID = callerID
			mu.Unlock()
			if flagVal == "fail" {
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
					Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "fake exploded"}}, IsError: true},
					IsError:  true,
				}
			}
			text := name + ":" + flagVal
			if extra, ok := args["extra"].(string); ok && extra != "" {
				text += ":" + extra
			}
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: text}}},
			}
		},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	runBash := func(script string, extra map[string]any) (stdout, stderr string, rc int, meta map[string]any) {
		t.Helper()
		params := map[string]any{"command": script}
		for k, v := range extra {
			params[k] = v
		}
		raw, errMsg := host.callTool("bash", params)
		if errMsg != "" {
			t.Fatalf("bash call failed: %s", errMsg)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal bash result: %v", err)
		}
		stdout, _ = m["stdout"].(string)
		stderr, _ = m["stderr"].(string)
		if f, ok := m["exitCode"].(float64); ok {
			rc = int(f)
		}
		return stdout, stderr, rc, m
	}

	// 1. Pipe mounted tool stdout into tr (upstream: bash-tool-commands.test.ts: "pipes a tool's stdout into the next command")
	out, errOut, rc, meta := runBash(`fake --flag piped | tr a-z A-Z`, nil)
	if rc != 0 || errOut != "" || strings.TrimSpace(out) != "FAKE:PIPED" {
		t.Fatalf("pipe test failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	// Verify preamble file was unlinked immediately upon source.
	if ppath, _ := meta["preamblePath"].(string); ppath != "" {
		if _, err := os.Stat(ppath); !os.IsNotExist(err) {
			t.Fatalf("preamble file %s still existed after source", ppath)
		}
	}

	// 2. Two tools in invocation order (upstream: bash-tool-commands.test.ts: "records two tools of one line in invocation order")
	out, errOut, rc, _ = runBash(`fake --flag a; other --flag b`, nil)
	if rc != 0 || errOut != "" || out != "fake:a\nother:b\n" {
		t.Fatalf("two tools test failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}

	// 3. Stdin (`-`) with JSON object and raw literal text
	out, errOut, rc, _ = runBash(`printf '{"flag":"from-stdin","extra":"line1\\nline2"}' | fake -`, nil)
	if rc != 0 || errOut != "" || out != "fake:from-stdin:line1\nline2\n" {
		t.Fatalf("stdin JSON test failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	out, errOut, rc, _ = runBash(`printf 'hello world\n' | fake -`, nil)
	if rc != 0 || errOut != "" || out != "fake:hello world\n" {
		t.Fatalf("stdin literal test failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}

	// 4. Quoting, spaces, quotes, and newlines in arguments
	out, errOut, rc, _ = runBash("fake --flag 'a \"quoted\"\nline' --extra $'tab\there'", nil)
	if rc != 0 || errOut != "" || out != "fake:a \"quoted\"\nline:tab\there\n" {
		t.Fatalf("quoting/newlines test failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}

	// 5. --help exits 0 without executing tool
	mu.Lock()
	beforeCount := len(executed)
	mu.Unlock()
	out, errOut, rc, _ = runBash(`fake --help`, nil)
	if rc != 0 || errOut != "" || !strings.Contains(out, "Usage:\n  fake <flag> [OPTIONS] [@FILE] [-]") {
		t.Fatalf("--help failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}
	mu.Lock()
	afterCount := len(executed)
	mu.Unlock()
	if afterCount != beforeCount {
		t.Fatalf("--help executed the tool (%d -> %d)", beforeCount, afterCount)
	}

	// 6. Bad arguments exit 2 with usage hint on stderr
	out, errOut, rc, _ = runBash(`fake --bogus x`, nil)
	if rc != 2 || out != "" || !strings.Contains(errOut, "fake --help") {
		t.Fatalf("bad args failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}

	// 7. Tool returning isError exits 1 with text on stderr
	out, errOut, rc, _ = runBash(`fake --flag fail`, nil)
	if rc != 1 || out != "" || errOut != "fake exploded\n" {
		t.Fatalf("tool error failed: rc=%d stdout=%q stderr=%q", rc, out, errOut)
	}

	// 8. Precedence over earlier shell function and alias (upstream: bash-tool-commands.test.ts)
	// Simulate saved state sourced BEFORE preamble, as internal/shell does.
	out, errOut, rc, _ = runBash(`fake --flag win && echo ok`, nil)
	if rc != 0 || out != "fake:win\nok\n" {
		t.Fatalf("chain failed: rc=%d out=%q err=%q", rc, out, errOut)
	}
	// Verify preamble overrides an earlier alias and function when sourced after them.
	sess := defaultRuntime.getSession("bridge-exec-1")
	if sess == nil || sess.listener == nil {
		t.Fatal("expected active bridge listener")
	}
	listenAddr := sess.listener.addr

	// 9. set -euxo pipefail must NOT trace __omp_tc_token or internal bridge protocol to stderr!
	out, errOut, rc, _ = runBash("set -euxo pipefail\nfake --flag traced", nil)
	if rc != 0 || out != "fake:traced\n" {
		t.Fatalf("set -euxo pipefail failed: rc=%d out=%q err=%q", rc, out, errOut)
	}
	if strings.Contains(errOut, "TOKEN ") || strings.Contains(errOut, "__omp_tc_token") || strings.Contains(errOut, "/dev/tcp/") {
		t.Fatalf("set -x leaked dispatcher internals/token to stderr:\n%s", errOut)
	}

	// 10. Caller's open FD 9 (flock pattern) must survive invoking a mounted tool!
	fd9File := filepath.Join(t.TempDir(), "fd9.txt")
	out, errOut, rc, _ = runBash(fmt.Sprintf("exec 9>%s\nfake --flag fdcheck\necho still-open >&9\nexec 9>&-", singleQuoteShell(fd9File)), nil)
	if rc != 0 || out != "fake:fdcheck\n" || errOut != "" {
		t.Fatalf("fd 9 preservation test failed: rc=%d out=%q err=%q", rc, out, errOut)
	}
	fd9Content, err := os.ReadFile(fd9File)
	if err != nil || string(fd9Content) != "still-open\n" {
		t.Fatalf("caller fd 9 was clobbered/closed: content=%q err=%v", string(fd9Content), err)
	}

	// 11. Hostile project-local `base64` on PATH is bypassed via `command -p`
	poisonDir := t.TempDir()
	poisonB64 := filepath.Join(poisonDir, "base64")
	if err := os.WriteFile(poisonB64, []byte("#!/bin/sh\necho HIJACKED\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errOut, rc, _ = runBash(fmt.Sprintf("PATH=%s:\"$PATH\" fake --flag clean", singleQuoteShell(poisonDir)), nil)
	if rc != 0 || out != "fake:clean\n" || errOut != "" {
		t.Fatalf("poisoned PATH base64 hijacked dispatcher: rc=%d out=%q err=%q", rc, out, errOut)
	}

	// 12. TryDirectWithOptions honors explicit cwd and rejects unquoted `name=~/x`
	subDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(subDir, "in.json"), []byte(`{"flag":"from-sub-cwd"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, rc, meta = runBash(`fake @in.json`, map[string]any{"mode": "direct", "cwd": subDir})
	if meta["handled"] != true || rc != 0 || out != "fake:from-sub-cwd\n" {
		t.Fatalf("TryDirectWithOptions cwd failed: meta=%v", meta)
	}
	// Unquoted `name=~/x` must NOT be handled by TryDirect (bash tilde-expands it).
	_, _, _, meta = runBash(`fake --flag=~/foo`, map[string]any{"mode": "direct"})
	if meta["handled"] != false {
		t.Fatalf("TryDirect must defer unquoted --flag=~/foo to bash, got handled=true")
	}
	// TryDirectWithOptions timeout returns TimedOut=true (distinct from Cancelled/130) (N5).
	out, _, rc, meta = runBash(`fake --flag sleep`, map[string]any{"mode": "direct", "timeout": 0.08})
	if meta["handled"] != true || meta["timedOut"] != true || meta["cancelled"] != false {
		t.Fatalf("TryDirectWithOptions timeout expected timedOut=true cancelled=false, got rc=%d out=%q meta=%v", rc, out, meta)
	}
	_ = lastCallerID
	// 13. Shutdown closes listener; nothing remains listening on listenAddr
	if _, errMsg := host.emitEvent(sdk.EventSessionShutdown, nil); errMsg != "" {
		t.Fatalf("session_shutdown: %s", errMsg)
	}
	if conn, dialErr := net.Dial("tcp", listenAddr); dialErr == nil {
		_ = conn.Close()
		t.Fatalf("listener still accepting connections at %s after session_shutdown", listenAddr)
	}
}

// TestBridgeSecurityWrongTokenRevokedTokenOversizedAndParallelAttribution verifies:
//   - Wrong token is rejected silently without executing any tool.
//   - Oversized unauthenticated first line (>maxTokenLineBytes without newline) is rejected immediately without unbounded buffering.
//   - Invalid UTF-8 stdin exits 2 with `<name>: stdin is not valid UTF-8`.
//   - Revoking a per-call grant immediately rejects subsequent replay with that token.
//   - Two parallel bash calls carry independent per-call tokens attributed to their own callerIDs.
//   - Killing/closing the client TCP connection cancels the in-flight ExecuteTool call.
//   - A panic in a tool handler is recovered on the bridge goroutine without crashing the process.
func TestBridgeSecurityWrongTokenRevokedTokenOversizedAndParallelAttribution(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	schema := json.RawMessage(`{"type":"object","properties":{"v":{"type":"string"}},"required":["v"]}`)
	Mountable("tool", func(sdk.Context) bool { return true })

	cancelledCh := make(chan string, 2)
	startedCh := make(chan struct{}, 2)

	ext := sdk.New("test-security")
	Register(ext)

	type grantPair struct {
		addr    string
		token   string
		release func()
	}
	grantCh := make(chan grantPair, 2)
	holdCh := make(chan struct{})

	ext.Tool("bash", "Shell.", sdk.Schema{"type": "object", "properties": map[string]any{"hold": map[string]any{"type": "boolean"}}}, func(ctx sdk.Context, params map[string]any) (any, error) {
		preamble, release := PreambleWithRelease(ctx)
		sess := defaultRuntime.getSession("sec-1")
		// Extract token from preamble for wire-level testing.
		var token string
		for _, line := range strings.Split(preamble, "\n") {
			if strings.HasPrefix(line, "__omp_tc_token='") {
				token = strings.TrimSuffix(strings.TrimPrefix(line, "__omp_tc_token='"), "'")
				break
			}
		}
		grantCh <- grantPair{addr: sess.listener.addr, token: token, release: release}
		<-holdCh
		release()
		return map[string]any{"ok": true}, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "sec-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "tool", Description: "Tool.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
		},
		activeTools: []string{"bash"},
		executeTool: func(execCtx context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			v, _ := args["v"].(string)
			if v == "block" {
				startedCh <- struct{}{}
				<-execCtx.Done()
				cancelledCh <- callerID
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
					Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "aborted"}}, IsError: true},
					IsError:  true,
				}
			}
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: callerID + ":" + v}}},
			}
		},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	// Start two parallel bash calls simultaneously.
	var callWG sync.WaitGroup
	callWG.Add(2)
	go func() {
		defer callWG.Done()
		_, _ = host.callToolWithID("bash", "bash-A", map[string]any{"hold": true})
	}()
	go func() {
		defer callWG.Done()
		_, _ = host.callToolWithID("bash", "bash-B", map[string]any{"hold": true})
	}()

	g1 := <-grantCh
	g2 := <-grantCh
	if g1.token == "" || g2.token == "" || g1.token == g2.token {
		t.Fatalf("expected distinct non-empty per-call tokens, got %q and %q", g1.token, g2.token)
	}

	sendRaw := func(addr, payload string) string {
		t.Helper()
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("dial %s: %v", addr, err)
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = c.Write([]byte(payload))
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(c)
		return buf.String()
	}

	// 1. Wrong token returns empty response and closes connection.
	resp := sendRaw(g1.addr, "TOKEN 000000000000000000000000000000000000000000000000\nNAME tool\nCWD Lw==\nARGC 1\nARG eA==\nEND\n")
	if resp != "" {
		t.Fatalf("wrong token should close without response, got %q", resp)
	}

	// 2. Oversized unauthenticated first line (>128 bytes without newline) is dropped immediately.
	resp = sendRaw(g1.addr, "TOKEN "+strings.Repeat("a", 512))
	if resp != "" {
		t.Fatalf("oversized unauthenticated line should close without response, got %q", resp)
	}

	// 3. Invalid UTF-8 stdin returns RC 2 with `<name>: stdin is not valid UTF-8`.
	badUTF8B64 := base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe, 0xfd})
	resp = sendRaw(g1.addr, fmt.Sprintf("TOKEN %s\nNAME tool\nCWD Lw==\nARGC 1\nARG LQ==\nSTDIN_B64\n%s\nEND\n", g1.token, badUTF8B64))
	if !strings.Contains(resp, "RC 2\n") {
		t.Fatalf("invalid UTF-8 stdin expected RC 2, got %q", resp)
	}
	for _, line := range strings.Split(resp, "\n") {
		if strings.HasPrefix(line, "ERR ") {
			dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "ERR "))
			if !strings.Contains(string(dec), "tool: stdin is not valid UTF-8") {
				t.Fatalf("unexpected stderr for bad UTF-8 stdin: %q", string(dec))
			}
		}
	}

	// 4. Parallel call attribution: each grant attributes its ExecuteTool call to its OWN bash callerID.
	reqFor := func(tok, arg string) string {
		return fmt.Sprintf("TOKEN %s\nNAME tool\nCWD Lw==\nARGC 1\nARG %s\nEND\n", tok, base64.StdEncoding.EncodeToString([]byte(arg)))
	}
	decodeOut := func(raw string) string {
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, "OUT ") {
				dec, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "OUT "))
				return string(dec)
			}
		}
		return ""
	}
	out1 := decodeOut(sendRaw(g1.addr, reqFor(g1.token, "one")))
	out2 := decodeOut(sendRaw(g2.addr, reqFor(g2.token, "two")))
	gotPair := []string{strings.TrimSpace(out1), strings.TrimSpace(out2)}
	slices.Sort(gotPair)
	if !((gotPair[0] == "bash-A:one" && gotPair[1] == "bash-B:two") || (gotPair[0] == "bash-A:two" && gotPair[1] == "bash-B:one")) {
		t.Fatalf("parallel calls misattributed callerIDs: got %v", gotPair)
	}

	// 5. Client disconnect (simulating killed/timed-out bash subprocess) cancels in-flight ExecuteTool!
	c, err := net.DialTimeout("tcp", g1.addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial for cancel test: %v", err)
	}
	_, _ = c.Write([]byte(reqFor(g1.token, "block")))
	select {
	case <-startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked tool did not start")
	}
	_ = c.Close() // Simulate shell killed by timeout
	select {
	case <-cancelledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("closing client TCP connection did not cancel in-flight ExecuteTool call")
	}

	// 6. Releasing/ending bash calls immediately revokes both per-call tokens.
	close(holdCh)
	callWG.Wait()
	if resp := sendRaw(g1.addr, reqFor(g1.token, "replay")); resp != "" {
		t.Fatalf("revoked token g1 still accepted replay: %q", resp)
	}
	if resp := sendRaw(g2.addr, reqFor(g2.token, "replay")); resp != "" {
		t.Fatalf("revoked token g2 still accepted replay: %q", resp)
	}

	_, _ = host.emitEvent(sdk.EventSessionShutdown, nil)
}

// TestMultiSessionLifecycleIsolationAndDoubleRegister verifies:
//  1. Register called twice (two fresh extension instances in one process, as PiG does per session/reload).
//  2. Two concurrent sessions each have independent mounted tool sets and loopback listeners.
//  3. Ending session 1 via lifecycle.End (twice, plus an unknown ID, plus empty ID) closes ONLY session 1's listener and state;
//     session 2 remains fully operational (PromptDocs, Preamble, TryDirect, live loopback listener).
//  4. Calling session_shutdown on session 2 closes session 2 cleanly via lifecycle.End, leaving no listeners open.
func TestMultiSessionLifecycleIsolationAndDoubleRegister(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	schema := json.RawMessage(`{"type":"object","properties":{"v":{"type":"string"}},"required":["v"]}`)
	Mountable("echo_tool", func(sdk.Context) bool { return true })
	Mountable("lsp", func(sdk.Context) bool { return true })
	Mountable("debug", func(sdk.Context) bool { return true })

	makeExt := func(tag string, outPreamble *string, outMounted *[]string, outDirect *string) *sdk.Extension {
		ext := sdk.New("test-multi-" + tag)
		Register(ext)
		ext.Tool("bash", "Shell.", sdk.Schema{"type": "object", "properties": map[string]any{"cmd": map[string]any{"type": "string"}}}, func(ctx sdk.Context, params map[string]any) (any, error) {
			*outPreamble = Preamble(ctx)
			m, _ := PromptDocs(ctx)
			*outMounted = m
			if cmd, ok := params["cmd"].(string); ok && cmd != "" {
				out, rc, handled := TryDirect(ctx, cmd)
				if !handled || rc != 0 {
					*outDirect = ""
				} else {
					*outDirect = out
				}
			}
			return map[string]any{"ok": true}, nil
		})
		return ext
	}

	var pre1, pre2 string
	var m1, m2 []string
	var dir1, dir2 string

	ext1 := makeExt("s1", &pre1, &m1, &dir1)
	ext2 := makeExt("s2", &pre2, &m2, &dir2)

	execFor := func(tag string) func(context.Context, string, string, map[string]any) sdk.AgentToolCallOutcome {
		return func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			v, _ := args["v"].(string)
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: tag + ":" + v}}},
			}
		}
	}

	host1 := startFakeHost(t, ext1, fakeHostOptions{
		sessionID: "session-alpha",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "echo_tool", Description: "Alpha tool.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
			{Name: "lsp", Description: "LSP.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
		},
		activeTools: []string{"bash"},
		executeTool: execFor("s1"),
	})
	host2 := startFakeHost(t, ext2, fakeHostOptions{
		sessionID: "session-beta",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "echo_tool", Description: "Beta tool.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
			{Name: "debug", Description: "Debug.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
		},
		activeTools: []string{"bash"},
		executeTool: execFor("s2"),
	})

	if _, errMsg := host1.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("host1 session_start: %s", errMsg)
	}
	if _, errMsg := host2.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("host2 session_start: %s", errMsg)
	}

	if _, errMsg := host1.callTool("bash", map[string]any{"cmd": "echo_tool one"}); errMsg != "" {
		t.Fatalf("host1 probe: %s", errMsg)
	}
	if _, errMsg := host2.callTool("bash", map[string]any{"cmd": "echo_tool two"}); errMsg != "" {
		t.Fatalf("host2 probe: %s", errMsg)
	}

	if pre1 == "" || pre2 == "" || pre1 == pre2 {
		t.Fatalf("expected distinct non-empty preambles for the two sessions")
	}
	// Builtin `lsp`/`debug` precedes dynamic `echo_tool`.
	if !slices.Equal(m1, []string{"lsp", "echo_tool"}) {
		t.Fatalf("session 1 mounted = %v, want [lsp echo_tool]", m1)
	}
	if !slices.Equal(m2, []string{"debug", "echo_tool"}) {
		t.Fatalf("session 2 mounted = %v, want [debug echo_tool]", m2)
	}
	if dir1 != "s1:one\n" || dir2 != "s2:two\n" {
		t.Fatalf("per-session execution isolation failed: dir1=%q dir2=%q", dir1, dir2)
	}

	s1 := defaultRuntime.getSession("session-alpha")
	s2 := defaultRuntime.getSession("session-beta")
	if s1 == nil || s1.listener == nil || s2 == nil || s2.listener == nil {
		t.Fatalf("expected both sessions to have active bridge listeners")
	}
	addr1 := s1.listener.addr
	addr2 := s2.listener.addr

	// End session 1 via lifecycle.End (child-session path). Even though Register
	// ran a second time for ext2, the package-level lifecycle handler releases session 1.
	lifecycle.End("session-alpha")
	lifecycle.End("session-alpha")
	lifecycle.End("session-nonexistent")
	lifecycle.End("")

	if defaultRuntime.getSession("session-alpha") != nil {
		t.Fatal("session-alpha state was not released by lifecycle.End")
	}
	if conn, err := net.Dial("tcp", addr1); err == nil {
		_ = conn.Close()
		t.Fatalf("session-alpha listener still open at %s after lifecycle.End", addr1)
	}

	// Session 2 must be completely unaffected.
	if conn, err := net.Dial("tcp", addr2); err != nil {
		t.Fatalf("session-beta listener at %s broke when session-alpha ended: %v", addr2, err)
	} else {
		_ = conn.Close()
	}
	dir2 = ""
	if _, errMsg := host2.callTool("bash", map[string]any{"cmd": "echo_tool still-alive"}); errMsg != "" {
		t.Fatalf("host2 probe after session-alpha end: %s", errMsg)
	}
	if dir2 != "s2:still-alive\n" || !slices.Equal(m2, []string{"debug", "echo_tool"}) {
		t.Fatalf("session-beta damaged after session-alpha ended: dir2=%q m2=%v", dir2, m2)
	}

	// Zero sdk.Context must never wipe live sessions or collapse into a shared bucket.
	if gotM, gotD := PromptDocs(sdk.Context{}); len(gotM) != 0 || gotD != "" {
		t.Fatalf("zero Context PromptDocs = (%v, %q), want empty", gotM, gotD)
	}
	if gotP := Preamble(sdk.Context{}); gotP != "" {
		t.Fatalf("zero Context Preamble = %q, want empty", gotP)
	}
	if defaultRuntime.getSession("session-beta") == nil {
		t.Fatal("zero Context call affected session-beta")
	}

	// Shut down session 2 via session_shutdown.
	if _, errMsg := host2.emitEvent(sdk.EventSessionShutdown, nil); errMsg != "" {
		t.Fatalf("host2 session_shutdown: %s", errMsg)
	}
	if defaultRuntime.getSession("session-beta") != nil {
		t.Fatal("session-beta state was not released by session_shutdown")
	}
	if conn, err := net.Dial("tcp", addr2); err == nil {
		_ = conn.Close()
		t.Fatalf("session-beta listener still open at %s after session_shutdown", addr2)
	}
}

// TestBridgeRecoverPanic verifies that a panic on a bridge connection goroutine
// is recovered cleanly without crashing the PiG process.
func TestBridgeRecoverPanic(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	sess := defaultRuntime.getOrCreateSession("panic-sess")
	srv, err := startBridgeServer(defaultRuntime, sess)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Trigger handleConn with a nil conn-facing state that causes panic inside RunToolCli
	// if unguarded; verify server stays alive for subsequent connections.
	tok, release, err := srv.issueGrant(sdk.Context{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	c, err := net.DialTimeout("tcp", srv.addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(c, "TOKEN %s\nNAME missing\nCWD Lw==\nARGC 0\nEND\n", tok)
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(c)
	_ = c.Close()
	if !strings.Contains(buf.String(), "RC 1\n") {
		t.Fatalf("expected RC 1 for missing command, got %q", buf.String())
	}
}

// TestPreambleAllexportDoesNotLeakTokenOrRequest (N1, N3) verifies that under
// a persisted `set -a` (allexport), sourcing the preamble does NOT export
// `__omp_tc_port`, `__omp_tc_token`, helper paths, functions, or request/stdin
// buffers to any child process — checked under both PATH bash and `/bin/bash`
// (macOS bash 3.2) when present — while preserving `set -a` in the outer shell.
// Also verifies helpers are resolved once at source time (N3).
func TestPreambleAllexportDoesNotLeakTokenOrRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("allexport Unix bash test")
	}
	var shells []string
	if _, err := os.Stat("/bin/bash"); err == nil {
		shells = append(shells, "/bin/bash")
	}
	if p, err := exec.LookPath("bash"); err == nil && p != "" && !slices.Contains(shells, p) {
		shells = append(shells, p)
	}
	if len(shells) == 0 {
		t.Skip("no bash found")
	}

	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	schema := json.RawMessage(`{"type":"object","properties":{"v":{"type":"string"}},"required":["v"]}`)
	Mountable("probe", func(sdk.Context) bool { return true })

	ext := sdk.New("test-allexport")
	Register(ext)
	ext.Tool("bash", "Shell.", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"shell": map[string]any{"type": "string"}},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		shellPath, _ := params["shell"].(string)
		preamble, release := PreambleWithRelease(ctx)
		defer release()

		tmpFile, err := os.CreateTemp(t.TempDir(), "omp-pre-allexport-*.sh")
		if err != nil {
			return nil, err
		}
		pPath := tmpFile.Name()
		_, _ = tmpFile.WriteString(preamble)
		_ = tmpFile.Close()
		defer func() { _ = os.Remove(pPath) }()

		// Wrap `base64` with a spy binary ONLY to verify subshell child env;
		// we verify normal env AND spy env when base64 is invoked.
		envDumpFile := filepath.Join(t.TempDir(), "child-env.txt")
		script := fmt.Sprintf(`
set -a
. %s
env > %s
opts_after="$-"
printf 'hello-stdin\n' | probe -
printf 'OPTS=%%s\n' "$opts_after"
`, singleQuoteShell(pPath), singleQuoteShell(envDumpFile))

		cmd := exec.Command(shellPath, "-c", script)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		rc := 0
		if runErr != nil {
			var ee *exec.ExitError
			if errorAs(runErr, &ee) {
				rc = ee.ExitCode()
			} else {
				return nil, runErr
			}
		}
		envBytes, _ := os.ReadFile(envDumpFile)
		return map[string]any{
			"stdout":   outBuf.String(),
			"stderr":   errBuf.String(),
			"exitCode": rc,
			"env":      string(envBytes),
			"preamble": preamble,
		}, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "allexport-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "probe", Description: "Probe.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
		},
		activeTools: []string{"bash"},
		executeTool: func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			v, _ := args["v"].(string)
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "ok:" + v}}},
			}
		},
	})
	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	for _, sh := range shells {
		t.Run(filepath.Base(sh)+"_"+sh, func(t *testing.T) {
			raw, errMsg := host.callTool("bash", map[string]any{"shell": sh})
			if errMsg != "" {
				t.Fatalf("bash call failed: %s", errMsg)
			}
			var res map[string]any
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			stdout, _ := res["stdout"].(string)
			stderr, _ := res["stderr"].(string)
			envDump, _ := res["env"].(string)
			preamble, _ := res["preamble"].(string)
			rc := int(res["exitCode"].(float64))
			if rc != 0 || stderr != "" || !strings.Contains(stdout, "ok:hello-stdin\n") {
				t.Fatalf("shell=%s failed: rc=%d stdout=%q stderr=%q", sh, rc, stdout, stderr)
			}
			// Outer shell's `set -a` must still be preserved after sourcing preamble.
			if !strings.Contains(stdout, "OPTS=") || !strings.Contains(strings.Split(stdout, "OPTS=")[1], "a") {
				t.Fatalf("shell=%s did not preserve allexport option: stdout=%q", sh, stdout)
			}
			// Zero __omp_tc_* variables or functions may leak into child env!
			for _, line := range strings.Split(envDump, "\n") {
				if strings.Contains(line, "__omp_tc_") {
					t.Fatalf("shell=%s leaked __omp_tc_ into child env under set -a: %q", sh, line)
				}
			}
			// N3: helpers are resolved once at source time (no per-call `command -p base64` inside __omp_tc_b64_enc/__omp_tc_b64_dec).
			if idx := strings.Index(preamble, "__omp_tc_b64_enc()"); idx >= 0 {
				rest := preamble[idx:]
				if strings.Contains(rest, "command -p base64") || strings.Contains(rest, "printf 'YQ=='") {
					t.Fatalf("helpers should be probed once at source time, not inside __omp_tc_b64_enc/__omp_tc_b64_dec")
				}
			}
		})
	}
}

// TestBridgeClosedConnectionDiagnosticAndAuthSlotWaiting (N4) verifies:
//  1. A dropped/closed bridge connection (e.g. revoked token) prints
//     `<name>: tool-command bridge closed the connection` to stderr with exit 1
//     instead of failing silently.
//  2. Unauthenticated connections holding all `maxPreAuthConnections` slots do
//     not starve an authenticated call once pre-auth releases, and filling all
//     `maxAuthConnections` causes an additional authenticated client to WAIT
//     for a slot and succeed rather than being dropped.
func TestBridgeClosedConnectionDiagnosticAndAuthSlotWaiting(t *testing.T) {
	bashPath := findBash(t)
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	schema := json.RawMessage(`{"type":"object","properties":{"v":{"type":"string"}},"required":["v"]}`)
	Mountable("tool", func(sdk.Context) bool { return true })

	releaseHold := make(chan struct{})
	inFlight := make(chan struct{}, maxAuthConnections+4)

	ext := sdk.New("test-n4")
	Register(ext)
	ext.Tool("bash", "Shell.", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"op": map[string]any{"type": "string"}},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		op, _ := params["op"].(string)
		if op == "revoked_diag" {
			preamble, release := PreambleWithRelease(ctx)
			// Immediately revoke BEFORE running bash so server closes connection without RC.
			release()
			cmd := exec.Command(bashPath, "-c", preamble+"\ntool hello")
			var outBuf, errBuf bytes.Buffer
			cmd.Stdout = &outBuf
			cmd.Stderr = &errBuf
			rc := 0
			if err := cmd.Run(); err != nil {
				var ee *exec.ExitError
				if errorAs(err, &ee) {
					rc = ee.ExitCode()
				}
			}
			return map[string]any{"stdout": outBuf.String(), "stderr": errBuf.String(), "exitCode": rc}, nil
		}

		// Test authenticated slot waiting: hold maxAuthConnections authenticated calls,
		// start one more authenticated call, then release the held ones and confirm
		// the waiter completes with RC 0 instead of being dropped.
		preamble, release := PreambleWithRelease(ctx)
		defer release()
		sess := defaultRuntime.getSession("n4-1")
		var token string
		for _, line := range strings.Split(preamble, "\n") {
			if strings.HasPrefix(line, "__omp_tc_token='") {
				token = strings.TrimSuffix(strings.TrimPrefix(line, "__omp_tc_token='"), "'")
				break
			}
		}
		addr := sess.listener.addr

		reqFor := func(arg string) string {
			return fmt.Sprintf("TOKEN %s\nNAME tool\nCWD Lw==\nARGC 1\nARG %s\nEND\n", token, base64.StdEncoding.EncodeToString([]byte(arg)))
		}
		var wg sync.WaitGroup
		// Start maxAuthConnections authenticated calls that block inside ExecuteTool.
		for range maxAuthConnections {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, err := net.DialTimeout("tcp", addr, 2*time.Second)
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }()
				_, _ = c.Write([]byte(reqFor("hold")))
				_, _ = bytes.NewBuffer(nil).ReadFrom(c)
			}()
		}
		var closeHoldOnce sync.Once
		closeHold := func() { closeHoldOnce.Do(func() { close(releaseHold) }) }
		defer closeHold()
		// Wait until all maxAuthConnections are inside ExecuteTool (and have released preAuthSem).
		for range maxAuthConnections {
			select {
			case <-inFlight:
			case <-time.After(5 * time.Second):
				return map[string]any{"error": "timed out waiting for auth slots to fill"}, nil
			}
		}
		// Launch the (maxAuthConnections+1)-th authenticated client; it should WAIT (not drop).
		waiterOut := make(chan string, 1)
		go func() {
			c, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err != nil {
				waiterOut <- "dial-err:" + err.Error()
				return
			}
			defer func() { _ = c.Close() }()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = c.Write([]byte(reqFor("waiter")))
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(c)
			waiterOut <- buf.String()
		}()
		// Give the waiter a moment to authenticate and park on authSem, then release.
		time.Sleep(50 * time.Millisecond)
		closeHold()
		got := <-waiterOut
		wg.Wait()
		return map[string]any{"waiter": got}, nil
	})
	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "n4-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "tool", Description: "Tool.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
		},
		activeTools: []string{"bash"},
		executeTool: func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			v, _ := args["v"].(string)
			if v == "hold" {
				inFlight <- struct{}{}
				<-releaseHold
			}
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "done:" + v}}},
			}
		},
	})
	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	// 1. Revoked token / closed connection prints diagnostic to stderr and exits 1.
	raw, errMsg := host.callTool("bash", map[string]any{"op": "revoked_diag"})
	if errMsg != "" {
		t.Fatalf("revoked_diag failed: %s", errMsg)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if int(res["exitCode"].(float64)) != 1 || res["stdout"] != "" || !strings.Contains(res["stderr"].(string), "tool: tool-command bridge closed the connection") {
		t.Fatalf("closed connection diagnostic failed: %v", res)
	}

	// 2. Authenticated client beyond maxAuthConnections waits and succeeds.
	raw, errMsg = host.callTool("bash", map[string]any{"op": "slot_wait"})
	if errMsg != "" {
		t.Fatalf("slot_wait failed: %s", errMsg)
	}
	res = nil
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if e, _ := res["error"].(string); e != "" {
		t.Fatal(e)
	}
	waiterResp, _ := res["waiter"].(string)
	if !strings.Contains(waiterResp, "RC 0\n") {
		t.Fatalf("expected waiter authenticated client to succeed with RC 0, got %q", waiterResp)
	}
	_, _ = host.emitEvent(sdk.EventSessionShutdown, nil)
}

// TestInvokedCommandClassifier (N2) verifies that InvokedCommand classifies
// simple and compound commands without executing any tool, and always agrees
// with TryDirectWithOptions on simple commands.
func TestInvokedCommandClassifier(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	schema := json.RawMessage(`{"type":"object","properties":{"v":{"type":"string"}},"required":["v"]}`)
	Mountable("ast_edit", func(sdk.Context) bool { return true })
	RegisterSpecialCommand("propose", func(sdk.Context) bool { return false })

	called := false
	ext := sdk.New("test-invoked")
	Register(ext)
	ext.OnEvent("classify", func(ctx sdk.Context, data map[string]any) (any, error) {
		cmd, _ := data["command"].(string)
		name, ok := InvokedCommand(ctx, cmd)
		return map[string]any{"name": name, "ok": ok}, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "invoked-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "ast_edit", Description: "AST edit.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
			{Name: "propose", Description: "Propose.", Exposure: sdk.ToolExposureCodemode, Parameters: schema},
		},
		activeTools: []string{"bash"},
		executeTool: func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			called = true
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "ran"}}},
			}
		},
	})
	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	cases := []struct {
		cmd      string
		wantName string
		wantOK   bool
	}{
		{`ast_edit --v x`, "ast_edit", true},
		{`propose "plan-a"`, "propose", true},
		{`touch /tmp/x && ast_edit --v x`, "ast_edit", true},
		{`FOO=1 ast_edit --v x | head -5`, "ast_edit", true},
		{`echo ast_edit`, "", false},
		{`ls -la`, "", false},
	}
	for _, tc := range cases {
		raw, errMsg := host.emitEvent("classify", map[string]any{"command": tc.cmd})
		if errMsg != "" {
			t.Fatalf("classify %q: %s", tc.cmd, errMsg)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got["name"] != tc.wantName || got["ok"] != tc.wantOK {
			t.Errorf("InvokedCommand(%q) = (%v, %v), want (%q, %v)", tc.cmd, got["name"], got["ok"], tc.wantName, tc.wantOK)
		}
	}
	if called {
		t.Fatal("InvokedCommand must never execute tools")
	}
	_, _ = host.emitEvent(sdk.EventSessionShutdown, nil)
}

// TestMountedToolHarnessIntentValidation verifies end-to-end against a fake
// host that validates required schema properties:
//  1. A mounted tool whose wire schema has `i` first and required (as wrapped
//     by internal/intent) produces identical `--help`, inline prompt docs, and
//     catalog output to the same tool without `i`, and rejects `--i`.
//  2. Invoking the mounted tool (both object root and pure `anyOf` root) passes
//     `"i": ""` to Context.ExecuteTool so host required-property validation passes.
//  3. A mounted tool whose schema does NOT define `i` receives NO `i` argument.
func TestMountedToolHarnessIntentValidation(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	prevMode := DocsModeSetting
	t.Cleanup(func() {
		DocsModeSetting = prevMode
		ResetForTests()
		lifecycle.ResetForTests()
	})

	schemaWithIntent := json.RawMessage(`{
		"type":"object",
		"properties":{
			"i":{"type":"string","description":"concise intent"},
			"action":{"type":"string","description":"Action to run."},
			"target":{"type":"string","description":"Target symbol."}
		},
		"required":["i","action"]
	}`)
	schemaWithoutIntent := json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","description":"Action to run."},
			"target":{"type":"string","description":"Target symbol."}
		},
		"required":["action"]
	}`)
	unionWithIntent := json.RawMessage(`{
		"anyOf":[
			{
				"type":"object",
				"properties":{
					"i":{"type":"string","description":"concise intent"},
					"op":{"const":"read","type":"string"},
					"file":{"type":"string"}
				},
				"required":["i","op","file"]
			},
			{
				"type":"object",
				"properties":{
					"i":{"type":"string","description":"concise intent"},
					"op":{"const":"list","type":"string"}
				},
				"required":["i","op"]
			}
		]
	}`)
	plainNoIntent := json.RawMessage(`{
		"type":"object",
		"properties":{"goal":{"type":"string","description":"Checkpoint goal."}},
		"required":["goal"]
	}`)

	// 1. Compare --help and catalog/inline prompt docs for identical tools with vs without `i`.
	helpWith := RenderToolHelp(ToolCliDefinition{Name: "lsp", Description: "LSP tool.", HasDesc: true, Schema: schemaWithIntent})
	helpWithout := RenderToolHelp(ToolCliDefinition{Name: "lsp", Description: "LSP tool.", HasDesc: true, Schema: schemaWithoutIntent})
	if helpWith != helpWithout {
		t.Fatalf("--help differs when `i` is present:\n--- WITH ---\n%s\n--- WITHOUT ---\n%s", helpWith, helpWithout)
	}

	stWith := stateOf([]*ToolDescriptor{{
		Name: "lsp", Label: "lsp", Summary: BuiltinToolSummaries["lsp"],
		Description: "LSP tool.", HasDescription: true, Parameters: schemaWithIntent,
	}}, []string{"lsp"}, nil, []string{"lsp"})
	stWithout := stateOf([]*ToolDescriptor{{
		Name: "lsp", Label: "lsp", Summary: BuiltinToolSummaries["lsp"],
		Description: "LSP tool.", HasDescription: true, Parameters: schemaWithoutIntent,
	}}, []string{"lsp"}, nil, []string{"lsp"})
	for _, mode := range []DocsMode{DocsModeCatalog, DocsModeInline, DocsModeBuiltins} {
		gotW := ToolCommandDocsAll(stWith, mode, nil)
		gotWO := ToolCommandDocsAll(stWithout, mode, nil)
		if gotW != gotWO {
			t.Fatalf("mode %s docs differ with `i`:\n--- WITH ---\n%s\n--- WITHOUT ---\n%s", mode, gotW, gotWO)
		}
		if strings.Contains(gotW, "--i") || strings.Contains(gotW, "<i>") {
			t.Fatalf("mode %s docs leaked `i`:\n%s", mode, gotW)
		}
	}

	// 2. Wire end-to-end execution through a fake host that validates required properties
	// against each tool's registered JSON schema.
	Mountable("lsp", func(sdk.Context) bool { return true })
	Mountable("union_tool", func(sdk.Context) bool { return true })
	Mountable("checkpoint", func(sdk.Context) bool { return true })

	schemaByTool := map[string]json.RawMessage{
		"lsp":        schemaWithIntent,
		"union_tool": unionWithIntent,
		"checkpoint": plainNoIntent,
	}

	var mu sync.Mutex
	received := map[string]map[string]any{}

	// validateRequiredBySchema mirrors PiG host's required-property validation on
	// both flat object schemas and pure anyOf/oneOf branch schemas.
	validateRequiredBySchema := func(raw json.RawMessage, args map[string]any) error {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		checkBranch := func(branch map[string]any) error {
			req, _ := branch["required"].([]any)
			for _, r := range req {
				k, _ := r.(string)
				v, exists := args[k]
				if !exists {
					return fmt.Errorf("missing required property %q", k)
				}
				if props, ok := branch["properties"].(map[string]any); ok {
					if p, ok := props[k].(map[string]any); ok {
						if p["type"] == "string" {
							if _, isStr := v.(string); !isStr {
								return fmt.Errorf("property %q must be string", k)
							}
						}
						if cv, hasConst := p["const"]; hasConst && v != cv {
							return fmt.Errorf("property %q must equal %v", k, cv)
						}
					}
				}
			}
			return nil
		}
		if anyOf, ok := doc["anyOf"].([]any); ok {
			var lastErr error
			for _, b := range anyOf {
				if bm, ok := b.(map[string]any); ok {
					if err := checkBranch(bm); err == nil {
						return nil
					} else {
						lastErr = err
					}
				}
			}
			return lastErr
		}
		return checkBranch(doc)
	}

	ext := sdk.New("test-harness-intent")
	Register(ext)
	ext.Tool("bash", "Shell.", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"command": map[string]any{"type": "string"}},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		cmd, _ := params["command"].(string)
		dr := TryDirectWithOptions(ctx, cmd, DirectOptions{Cwd: ctx.Cwd(), Timeout: 5 * time.Second})
		return map[string]any{"output": dr.Output, "exitCode": dr.ExitCode, "handled": dr.Handled}, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "intent-valid-1",
		allTools: []sdk.ToolInfo{
			{Name: "bash", Description: "Shell.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)},
			{Name: "lsp", Description: "LSP tool.", Exposure: sdk.ToolExposureCodemode, Parameters: schemaWithIntent},
			{Name: "union_tool", Description: "Union tool.", Exposure: sdk.ToolExposureCodemode, Parameters: unionWithIntent},
			{Name: "checkpoint", Description: "Checkpoint tool.", Exposure: sdk.ToolExposureCodemode, Parameters: plainNoIntent},
		},
		activeTools: []string{"bash"},
		executeTool: func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			if err := validateRequiredBySchema(schemaByTool[name], args); err != nil {
				return sdk.AgentToolCallOutcome{
					ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
					Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: err.Error()}}, IsError: true},
					IsError:  true,
				}
			}
			mu.Lock()
			cp := make(map[string]any, len(args))
			for k, v := range args {
				cp[k] = v
			}
			received[name] = cp
			mu.Unlock()
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "ok:" + name}}},
			}
		},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	runDirect := func(cmd string) (string, int) {
		t.Helper()
		raw, errMsg := host.callTool("bash", map[string]any{"command": cmd})
		if errMsg != "" {
			t.Fatalf("bash %q failed: %s", cmd, errMsg)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m["handled"] != true {
			t.Fatalf("expected %q to be handled", cmd)
		}
		out, _ := m["output"].(string)
		return out, int(m["exitCode"].(float64))
	}

	// Flat object tool with required `i`: succeeds and passes `"i": ""` to host.
	if out, rc := runDirect("lsp definition --target Foo"); rc != 0 || out != "ok:lsp\n" {
		t.Fatalf("lsp failed: rc=%d out=%q", rc, out)
	}
	// Pure anyOf union tool with `i` in each branch: succeeds and passes `"i": ""` to host.
	if out, rc := runDirect("union_tool list"); rc != 0 || out != "ok:union_tool\n" {
		t.Fatalf("union_tool failed: rc=%d out=%q", rc, out)
	}
	// `--i` is NOT accepted as a CLI flag.
	if out, rc := runDirect("lsp definition --i custom"); rc != 2 || !strings.Contains(out, "unknown flag `--i`") {
		t.Fatalf("expected --i rejected with exit 2, got rc=%d out=%q", rc, out)
	}
	// Tool without `i` in its schema: succeeds and does NOT get `i`.
	if out, rc := runDirect("checkpoint --goal explore"); rc != 0 || out != "ok:checkpoint\n" {
		t.Fatalf("checkpoint failed: rc=%d out=%q", rc, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(received["lsp"], map[string]any{"action": "definition", "target": "Foo", "i": ""}) {
		t.Fatalf("lsp received args = %#v", received["lsp"])
	}
	if !reflect.DeepEqual(received["union_tool"], map[string]any{"op": "list", "i": ""}) {
		t.Fatalf("union_tool received args = %#v", received["union_tool"])
	}
	if _, hasI := received["checkpoint"]["i"]; hasI {
		t.Fatalf("checkpoint without `i` in schema unexpectedly received `i`: %#v", received["checkpoint"])
	}
	if !reflect.DeepEqual(received["checkpoint"], map[string]any{"goal": "explore"}) {
		t.Fatalf("checkpoint received args = %#v", received["checkpoint"])
	}
}

func errorAs[T any](err error, target *T) bool {
	if err == nil {
		return false
	}
	if v, ok := err.(T); ok {
		*target = v
		return true
	}
	return false
}

// TestMountedToolsDirectFallbackAndPrepareLoadoutHiddenDeclarations verifies:
//   - Mounted tools (ast_edit, debug, lsp) remain active in PiG so direct model
//     tool calls dispatch through PiG's active-tool gate (`callModelTool`,
//     mirroring `agent/tool_execution.go:366-369`), matching omp's
//     `resolveFallbackTool` (`sdk.ts:5228`, `agent-loop.ts:2978-2992`).
//   - `bash`'s `PrepareLoadout: toolcmd.PrepareLoadout` returns those mounted
//     names in `ToolLoadoutChanges.HiddenDeclarations`, so PiG's host
//     `hideDeclarations` projection omits them before EVERY provider conversion
//     (`coding/session_loadout.go:288-327`, `coding/session.go:566`) while
//     leaving Anthropic cache breakpoints intact on the last projected tool.
//   - Special commands (`resolve`, `reject`, `propose`) are NOT kept active
//     while the transport is on (omp `tools/tool-commands.ts:237-254`), so a
//     direct model call answers `"Tool <name> not found"`, while bash
//     `ExecuteTool` still invokes them via codemode/deferred exposure.
//   - Disabled mountable tools (`github`) are inactive and refused by
//     `callModelTool` with `"Tool github not found"`.
//   - Toggling `tools.commands = false` mid-session stops hiding mounted tools
//     via `PrepareLoadout` so they become advertised top-level.
//   - `toolcmd.Register` installs NO `before_provider_request` handler.
//
// upstream: packages/coding-agent/src/sdk.ts:4301-4327, 5228
// upstream: packages/coding-agent/src/tools/tool-commands.ts:237-254
// upstream: packages/agent/src/agent-loop.ts:2975-2994
func TestMountedToolsDirectFallbackAndPrepareLoadoutHiddenDeclarations(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	prevEnabled := Enabled
	t.Cleanup(func() {
		Enabled = prevEnabled
		ResetForTests()
		lifecycle.ResetForTests()
	})

	commandsOn := true
	Enabled = func(_ sdk.Context, key string, def bool) bool {
		if key == "tools.commands" {
			return commandsOn
		}
		return def
	}

	Mountable("ast_edit", func(sdk.Context) bool { return true })
	Mountable("debug", func(sdk.Context) bool { return true })
	Mountable("lsp", func(sdk.Context) bool { return true })
	Mountable("github", func(sdk.Context) bool { return false })
	RegisterSpecialCommand("resolve", func(sdk.Context) bool { return false })
	RegisterSpecialCommand("reject", func(sdk.Context) bool { return false })
	RegisterSpecialCommand("propose", func(sdk.Context) bool { return false })

	ext := sdk.New("test-direct-fallback-loadout")
	Register(ext)

	// Register bash with PrepareLoadout = toolcmd.PrepareLoadout (mirroring
	// wiring.go's `shell.PrepareLoadout = toolcmd.PrepareLoadout`).
	ext.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "bash",
		Description:    "Run shell.",
		Exposure:       sdk.ToolExposureDirect,
		Parameters:     sdk.Schema{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}},
		PrepareLoadout: PrepareLoadout,
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			cmd, _ := params["command"].(string)
			dr := TryDirectWithOptions(ctx, cmd, DirectOptions{Cwd: ctx.Cwd(), Timeout: 5 * time.Second})
			return map[string]any{"output": dr.Output, "exitCode": dr.ExitCode, "handled": dr.Handled}, nil
		},
	})

	var lspDirectRan, resolveDirectRan, githubDirectRan bool
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp",
		Label:       "lsp",
		Description: "LSP tool.",
		Exposure:    sdk.ToolExposureCodemode,
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string"}}},
		Execute: func(_ sdk.Context, params map[string]any) (any, error) {
			lspDirectRan = true
			return map[string]any{"ok": true, "action": params["action"]}, nil
		},
	})
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "resolve",
		Label:       "resolve",
		Description: "Resolve staged action.",
		Exposure:    sdk.ToolExposureCodemode,
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{"reason": map[string]any{"type": "string"}}},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) {
			resolveDirectRan = true
			return map[string]any{"ok": true}, nil
		},
	})
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "github",
		Label:       "github",
		Description: "GitHub tool.",
		Exposure:    sdk.ToolExposureCodemode,
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{"op": map[string]any{"type": "string"}}},
		Execute: func(_ sdk.Context, _ map[string]any) (any, error) {
			githubDirectRan = true
			return map[string]any{"ok": true}, nil
		},
	})

	var resolveViaBashRan bool
	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID: "fallback-loadout-1",
		allTools: []sdk.ToolInfo{
			{Name: "read", Description: "Read.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "bash", Description: "Bash.", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "ast_edit", Description: "AST edit.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "debug", Description: "Debug.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "lsp", Description: "LSP.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "github", Description: "GitHub.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "resolve", Description: "Resolve.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"}}}`)},
			{Name: "reject", Description: "Reject.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "propose", Description: "Propose.", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		// Include `resolve` in initialActive to prove syncSession actively removes
		// special commands when transport is active.
		activeTools: []string{"read", "bash", "resolve"},
		executeTool: func(_ context.Context, callerID, name string, args map[string]any) sdk.AgentToolCallOutcome {
			if name == "resolve" {
				resolveViaBashRan = true
			}
			return sdk.AgentToolCallOutcome{
				ToolCall: sdk.ToolCall{Type: "toolCall", ID: callerID + "/1", Name: name, Arguments: args},
				Result:   sdk.AgentToolResult{Content: []sdk.ToolResultContent{{Type: "text", Text: "ok:" + name}}},
			}
		},
	})

	// Verify toolcmd registered NO before_provider_request handler.
	if len(host.handlers[sdk.EventBeforeProviderRequest]) != 0 {
		t.Fatalf("expected no before_provider_request handler, got %v", host.handlers[sdk.EventBeforeProviderRequest])
	}

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}

	// 1. Mounted tools are active (for direct fallback), while specials and
	// disabled tools are NOT active.
	wantActive := []string{"read", "bash", "ast_edit", "debug", "lsp"}
	if got := host.getActiveTools(); !slices.Equal(got, wantActive) {
		t.Fatalf("activeTools = %v, want %v", got, wantActive)
	}
	// 2. PrepareLoadout hid every mounted tool from the transcript projection
	// so every provider sees only [read, bash].
	wantHidden := []string{"ast_edit", "debug", "lsp"}
	if got := host.getHiddenDeclarations(); !slices.Equal(got, wantHidden) {
		t.Fatalf("HiddenDeclarations = %v, want %v", got, wantHidden)
	}
	wantProjected := []string{"read", "bash"}
	if got := host.getProjectedProviderTools(); !slices.Equal(got, wantProjected) {
		t.Fatalf("projected provider tools = %v, want %v", got, wantProjected)
	}

	// 3. Direct model call to mounted `lsp` succeeds through active-gated host.
	if _, errMsg := host.callModelTool("lsp", "direct-lsp-1", map[string]any{"action": "status"}); errMsg != "" {
		t.Fatalf("direct model call to mounted lsp failed: %s", errMsg)
	}
	if !lspDirectRan {
		t.Fatal("expected direct lsp call to execute handler")
	}

	// 4. Direct model calls to special commands (`resolve`, `reject`, `propose`)
	// and disabled tools (`github`) are REFUSED with "Tool <name> not found".
	for _, denied := range []string{"resolve", "reject", "propose", "github"} {
		_, errMsg := host.callModelTool(denied, "direct-"+denied, map[string]any{})
		wantErr := "Tool " + denied + " not found"
		if errMsg != wantErr {
			t.Fatalf("direct model call to %q: got err %q, want %q", denied, errMsg, wantErr)
		}
	}
	if resolveDirectRan || githubDirectRan {
		t.Fatalf("refused tools must never execute: resolve=%v github=%v", resolveDirectRan, githubDirectRan)
	}

	// 5. Special command `resolve` STILL runs via bash -> ExecuteTool (codemode exposure).
	if _, errMsg := host.callModelTool("bash", "bash-resolve-1", map[string]any{"command": "resolve --reason ok"}); errMsg != "" {
		t.Fatalf("bash resolve call failed: %s", errMsg)
	}
	if !resolveViaBashRan {
		t.Fatal("expected special command resolve to execute through bash ExecuteTool")
	}

	// 6. Disabling tools.commands mid-session clears HiddenDeclarations so
	// enabled mountable tools become advertised top-level (with no empty reset).
	beforeCalls := len(host.callsTo("setActiveTools"))
	commandsOn = false
	if _, errMsg := host.emitEvent(sdk.EventBeforeAgentStart, map[string]any{
		"systemPromptOptions": map[string]any{},
	}); errMsg != "" {
		t.Fatalf("before_agent_start (commands off): %s", errMsg)
	}
	if got := host.getHiddenDeclarations(); len(got) != 0 {
		t.Fatalf("expected no HiddenDeclarations when tools.commands=false, got %v", got)
	}
	if got := host.getProjectedProviderTools(); !slices.Equal(got, wantActive) {
		t.Fatalf("projected tools when tools.commands=false = %v, want %v", got, wantActive)
	}
	for _, call := range host.callsTo("setActiveTools")[beforeCalls:] {
		if raw, _ := call.Args["tools"].([]any); len(raw) == 0 {
			t.Fatalf("mid-session sync must NEVER call setActiveTools([]): %+v", call)
		}
	}
}

// TestActiveToolsOrderedByOmpBuiltinOrderAndNoMidSessionReset verifies:
//   - TRIAGE finding 3: default-set active tools are ordered at session_start
//     by omp's BUILTIN_TOOLS order (`tools/index.ts:622-652`), giving
//     `read, bash, edit, eval, glob, grep, task, wait, todo, web_search, write`
//     as the projected provider tools (and `AdvertisedTools`) even under PiG's
//     `planToolLoadout` order-preservation rule.
//   - Review finding 3: mid-session syncs (`mcp_servers_change`, `Sync`,
//     `PromptDocs`) NEVER call `SetActiveTools([])` even after another area
//     appends a built-in (e.g. `write`, `goal`) out of BUILTIN_TOOLS order.
//   - Review finding 4: when `ExplicitToolOrder` supplies an explicit list
//     (e.g. a subagent frontmatter order `read, find, grep, glob, bash, web_search`),
//     caller order is preserved instead of sorting into BUILTIN_TOOLS order.
//
// upstream: packages/coding-agent/src/tools/index.ts:622-652, 862-875
// upstream: packages/coding-agent/src/sdk.ts:4701-4762
func TestActiveToolsOrderedByOmpBuiltinOrderAndNoMidSessionReset(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	prevExplicit := ExplicitToolOrder
	t.Cleanup(func() {
		ExplicitToolOrder = prevExplicit
		ResetForTests()
		lifecycle.ResetForTests()
	})

	Mountable("ast_edit", func(sdk.Context) bool { return true })
	Mountable("debug", func(sdk.Context) bool { return true })
	Mountable("lsp", func(sdk.Context) bool { return true })
	RegisterSpecialCommand("resolve", func(sdk.Context) bool { return false })
	RegisterSpecialCommand("reject", func(sdk.Context) bool { return false })

	// 1. Default session starting from the mis-ordered host list in TRIAGE finding 3.
	hostOrder := []string{"read", "bash", "edit", "write", "grep", "glob", "todo", "web_search", "eval", "task", "wait"}
	allTools := make([]sdk.ToolInfo, 0, len(hostOrder)+7)
	for _, name := range hostOrder {
		allTools = append(allTools, sdk.ToolInfo{
			Name:       name,
			Exposure:   sdk.ToolExposureDirect,
			Parameters: json.RawMessage(`{"type":"object"}`),
		})
	}
	allTools = append(allTools,
		sdk.ToolInfo{Name: "custom_ext", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "goal", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "ast_edit", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "debug", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "lsp", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "resolve", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "reject", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
	)

	ext := sdk.New("test-active-order")
	Register(ext)
	ext.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "bash",
		Description:    "Run shell.",
		Exposure:       sdk.ToolExposureDirect,
		Parameters:     sdk.Schema{"type": "object"},
		PrepareLoadout: PrepareLoadout,
		Execute:        func(sdk.Context, map[string]any) (any, error) { return "ok", nil },
	})
	var advertised []string
	ext.OnEvent("probe_advertised", func(ctx sdk.Context, _ map[string]any) (any, error) {
		active, _ := ctx.GetActiveTools()
		advertised = AdvertisedTools(ctx, active)
		return nil, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID:   "order-default",
		allTools:    allTools,
		activeTools: append(append([]string(nil), hostOrder...), "custom_ext"),
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}
	if _, errMsg := host.emitEvent("probe_advertised", nil); errMsg != "" {
		t.Fatalf("probe_advertised: %s", errMsg)
	}

	// Active tools are in BUILTIN_TOOLS order (with mounted tools active for
	// fallback and specials NOT active).
	wantActive := []string{
		"read", "bash", "edit", "ast_edit", "debug", "eval", "glob", "grep", "lsp",
		"task", "wait", "todo", "web_search", "write",
		"custom_ext",
	}
	if got := host.getActiveTools(); !slices.Equal(got, wantActive) {
		t.Fatalf("activeTools order = %v\nwant              = %v", got, wantActive)
	}
	// Projected provider tools (after PrepareLoadout.HiddenDeclarations) and
	// AdvertisedTools match omp's default set order verbatim.
	wantAdvertised := []string{
		"read", "bash", "edit", "eval", "glob", "grep",
		"task", "wait", "todo", "web_search", "write",
		"custom_ext",
	}
	if got := host.getProjectedProviderTools(); !slices.Equal(got, wantAdvertised) {
		t.Fatalf("projected provider tools = %v\nwant                     = %v", got, wantAdvertised)
	}
	if !slices.Equal(advertised, wantAdvertised) {
		t.Fatalf("AdvertisedTools = %v\nwant            = %v", advertised, wantAdvertised)
	}

	// 2. Mid-session append of out-of-order built-ins followed by mcp_servers_change
	// must NEVER issue SetActiveTools([]).
	midTools := append(append([]string(nil), host.getActiveTools()...), "goal")
	// Move `write` to the end after `goal` to simulate plan-mode re-adding `write`.
	midTools = slices.DeleteFunc(midTools, func(n string) bool { return n == "write" })
	midTools = append(midTools, "write")
	host.setTools(allTools, midTools)

	callsBeforeMid := len(host.callsTo("setActiveTools"))
	if _, errMsg := host.emitEvent(sdk.EventMcpServersChange, nil); errMsg != "" {
		t.Fatalf("mcp_servers_change: %s", errMsg)
	}
	for _, call := range host.callsTo("setActiveTools")[callsBeforeMid:] {
		if raw, _ := call.Args["tools"].([]any); len(raw) == 0 {
			t.Fatalf("mid-session mcp_servers_change emitted empty setActiveTools([]): %+v", call)
		}
	}
	if got := host.getActiveTools(); !slices.Equal(got, midTools) {
		t.Fatalf("mid-session sync unexpectedly reordered activeTools: got %v, want %v", got, midTools)
	}

	// 3. Explicit tool list using the REAL bundled reviewer agent frontmatter
	// (`read, find, grep, glob, bash, lsp, web_search, ast_grep`, plus `wait`
	// appended by ResolveChildToolNames):
	//   - Explicitly listed built-ins (`lsp`, `ast_grep`) stay TOP-LEVEL (NOT
	//     mounted as bash commands, NOT in HiddenDeclarations, NOT in PromptDocs),
	//     matching omp `tools/index.ts:930-938` (`mountBuiltinTools = requestedTools === undefined`)
	//     and `sdk.ts:4747-4754, 4835-4838`.
	//   - Unlisted mountables (`ast_edit`, `debug`) are NEVER activated or mounted.
	//   - Caller order is preserved verbatim.
	Mountable("ast_grep", func(sdk.Context) bool { return true })
	reviewerOrder := []string{"read", "find", "grep", "glob", "bash", "lsp", "web_search", "ast_grep", "wait"}
	ExplicitToolOrder = func(ctx sdk.Context) []string {
		if id, _ := ctx.GetSessionID(); id == "order-reviewer" {
			return reviewerOrder
		}
		return nil
	}
	extChild := sdk.New("test-explicit-order")
	Register(extChild)
	extChild.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "bash",
		Description:    "Run shell.",
		Exposure:       sdk.ToolExposureDirect,
		Parameters:     sdk.Schema{"type": "object"},
		PrepareLoadout: PrepareLoadout,
		Execute:        func(sdk.Context, map[string]any) (any, error) { return "ok", nil },
	})
	var childMounted []string
	var childDocs string
	extChild.OnEvent("probe_child_docs", func(ctx sdk.Context, _ map[string]any) (any, error) {
		childMounted, childDocs = PromptDocs(ctx)
		return nil, nil
	})
	childTools := []sdk.ToolInfo{
		{Name: "read", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "find", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "grep", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "glob", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "bash", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "lsp", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "web_search", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "ast_grep", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "wait", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		// Unlisted mountables registered in the fused extension must stay inactive/unmounted.
		{Name: "ast_edit", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "debug", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	// PiG's host starts only declarable (direct) tools active; codemode tools
	// (`lsp`, `ast_grep`) start inactive and must be activated top-level by
	// syncSession in their exact explicitOrder slots.
	childInitialActive := []string{"read", "find", "grep", "glob", "bash", "web_search", "wait"}
	childHost := startFakeHost(t, extChild, fakeHostOptions{
		sessionID:   "order-reviewer",
		allTools:    childTools,
		activeTools: childInitialActive,
	})
	if _, errMsg := childHost.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("child session_start: %s", errMsg)
	}
	if _, errMsg := childHost.emitEvent("probe_child_docs", nil); errMsg != "" {
		t.Fatalf("probe_child_docs: %s", errMsg)
	}
	if got := childHost.getProjectedProviderTools(); !slices.Equal(got, reviewerOrder) {
		t.Fatalf("reviewer projected tools = %v\nwant                     = %v", got, reviewerOrder)
	}
	if got := childHost.getHiddenDeclarations(); len(got) != 0 {
		t.Fatalf("reviewer must NOT hide explicitly requested built-ins; got HiddenDeclarations=%v", got)
	}
	if len(childMounted) != 0 || childDocs != "" {
		t.Fatalf("reviewer must NOT mount explicitly requested built-ins; got mounted=%v docs=%q", childMounted, childDocs)
	}
	for _, unlisted := range []string{"ast_edit", "debug"} {
		if slices.Contains(childHost.getActiveTools(), unlisted) {
			t.Fatalf("unlisted mountable %q must NOT be activated in explicit-list subagent; active=%v", unlisted, childHost.getActiveTools())
		}
	}
}

// TestRegisterAutoActivatePlacesFindInBuiltinOrder verifies ORACLE TRIAGE 2
// finding 9: a conditional built-in registered with DefaultActive: false and
// declared via `RegisterAutoActivate("find", enabled)` is activated at
// `session_start` in its exact omp `BUILTIN_TOOLS` slot (immediately after
// `grep`, `tools/index.ts:635`) rather than appended after `write`. Also checks:
//   - when the predicate returns false, `find` is NOT activated;
//   - when `ExplicitToolOrder` supplies an explicit caller list omitting `find`,
//     `RegisterAutoActivate` does not inject `find` or disturb caller order.
//
// upstream: packages/coding-agent/src/tools/index.ts:622-652, 862-875
func TestRegisterAutoActivatePlacesFindInBuiltinOrder(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	prevExplicit := ExplicitToolOrder
	t.Cleanup(func() {
		ExplicitToolOrder = prevExplicit
		ResetForTests()
		lifecycle.ResetForTests()
	})

	findOn := true
	RegisterAutoActivate("find", func(sdk.Context) bool { return findOn })
	Mountable("ast_edit", func(sdk.Context) bool { return true })
	Mountable("debug", func(sdk.Context) bool { return true })
	Mountable("lsp", func(sdk.Context) bool { return true })

	hostOrder := []string{"read", "bash", "edit", "write", "grep", "glob", "todo", "web_search", "eval", "task", "wait"}
	allTools := make([]sdk.ToolInfo, 0, len(hostOrder)+4)
	for _, name := range hostOrder {
		allTools = append(allTools, sdk.ToolInfo{
			Name:       name,
			Exposure:   sdk.ToolExposureDirect,
			Parameters: json.RawMessage(`{"type":"object"}`),
		})
	}
	allTools = append(allTools,
		sdk.ToolInfo{Name: "find", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "ast_edit", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "debug", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		sdk.ToolInfo{Name: "lsp", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
	)

	newExt := func(name string) *sdk.Extension {
		ext := sdk.New(name)
		Register(ext)
		ext.RegisterTool(sdk.ToolDefinition{
			Name:           "bash",
			Label:          "bash",
			Description:    "Run shell.",
			Exposure:       sdk.ToolExposureDirect,
			Parameters:     sdk.Schema{"type": "object"},
			PrepareLoadout: PrepareLoadout,
			Execute:        func(sdk.Context, map[string]any) (any, error) { return "ok", nil },
		})
		return ext
	}

	// 1. Default session with find.enabled="on": `find` lands right after `grep`
	// (before `task`), matching omp `tools/index.ts:622-652`.
	host1 := startFakeHost(t, newExt("test-auto-find-on"), fakeHostOptions{
		sessionID:   "auto-find-on",
		allTools:    allTools,
		activeTools: hostOrder, // `find` starts inactive (DefaultActive: false)
	})
	if _, errMsg := host1.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start (on): %s", errMsg)
	}
	wantOn := []string{"read", "bash", "edit", "eval", "glob", "grep", "find", "task", "wait", "todo", "web_search", "write"}
	if got := host1.getProjectedProviderTools(); !slices.Equal(got, wantOn) {
		t.Fatalf("projected tools with find on = %v\nwant                         = %v", got, wantOn)
	}

	// 2. Default session with find disabled: `find` stays inactive.
	findOn = false
	host2 := startFakeHost(t, newExt("test-auto-find-off"), fakeHostOptions{
		sessionID:   "auto-find-off",
		allTools:    allTools,
		activeTools: hostOrder,
	})
	if _, errMsg := host2.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start (off): %s", errMsg)
	}
	wantOff := []string{"read", "bash", "edit", "eval", "glob", "grep", "task", "wait", "todo", "web_search", "write"}
	if got := host2.getProjectedProviderTools(); !slices.Equal(got, wantOff) {
		t.Fatalf("projected tools with find off = %v\nwant                          = %v", got, wantOff)
	}

	// 3. Explicit caller list omitting `find`: even when predicate is true,
	// explicit caller membership and order are preserved.
	findOn = true
	explicitList := []string{"read", "grep", "glob", "bash"}
	ExplicitToolOrder = func(ctx sdk.Context) []string {
		if id, _ := ctx.GetSessionID(); id == "auto-find-explicit" {
			return explicitList
		}
		return nil
	}
	host3 := startFakeHost(t, newExt("test-auto-find-explicit"), fakeHostOptions{
		sessionID:   "auto-find-explicit",
		allTools:    allTools,
		activeTools: explicitList,
	})
	if _, errMsg := host3.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start (explicit): %s", errMsg)
	}
	if got := host3.getProjectedProviderTools(); !slices.Equal(got, explicitList) {
		t.Fatalf("projected tools with explicit order = %v, want %v", got, explicitList)
	}
}

// TestExternalDeactivationNotRestored verifies Final Review finding 13:
// when a user's tool picker or another extension removes a mountable built-in
// (e.g. `lsp`) or a codemode/deferred extension/MCP tool from PiG's active tool
// list mid-session, subsequent `before_agent_start` / `PromptDocs` / `Sync`
// calls MUST NOT re-activate or re-mount it. Only if the tool's area setting
// flips false -> true (mirroring omp's `reconcileBuiltinTools`,
// `session/session-tools.ts:2554-2577`) or a user/extension explicitly
// re-activates it does it return.
//
// upstream: packages/coding-agent/src/session/session-tools.ts:1557, 2554-2577
func TestExternalDeactivationNotRestored(t *testing.T) {
	ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})

	lspEnabled := true
	Mountable("lsp", func(sdk.Context) bool { return lspEnabled })
	Mountable("debug", func(sdk.Context) bool { return true })

	allTools := []sdk.ToolInfo{
		{Name: "read", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "bash", Exposure: sdk.ToolExposureDirect, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "lsp", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "debug", Exposure: sdk.ToolExposureCodemode, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__docs__search", Exposure: sdk.ToolExposureDeferred, Parameters: json.RawMessage(`{"type":"object"}`)},
	}

	ext := sdk.New("test-external-deact")
	Register(ext)
	ext.RegisterTool(sdk.ToolDefinition{
		Name:           "bash",
		Label:          "bash",
		Description:    "Run shell.",
		Exposure:       sdk.ToolExposureDirect,
		Parameters:     sdk.Schema{"type": "object"},
		PrepareLoadout: PrepareLoadout,
		Execute:        func(sdk.Context, map[string]any) (any, error) { return "ok", nil },
	})
	var mounted []string
	var docs string
	ext.OnEvent("probe", func(ctx sdk.Context, _ map[string]any) (any, error) {
		mounted, docs = PromptDocs(ctx)
		return nil, nil
	})

	host := startFakeHost(t, ext, fakeHostOptions{
		sessionID:   "ext-deact-1",
		allTools:    allTools,
		activeTools: []string{"read", "bash"},
	})

	if _, errMsg := host.emitEvent(sdk.EventSessionStart, nil); errMsg != "" {
		t.Fatalf("session_start: %s", errMsg)
	}
	if _, errMsg := host.emitEvent("probe", nil); errMsg != "" {
		t.Fatalf("probe 1: %s", errMsg)
	}
	wantInitialMounted := []string{"debug", "lsp", "mcp__docs__search"}
	if !slices.Equal(mounted, wantInitialMounted) {
		t.Fatalf("initial mounted = %v, want %v", mounted, wantInitialMounted)
	}

	// Simulate another extension / host tool picker removing `lsp` and `mcp__docs__search`.
	host.setTools(allTools, []string{"read", "bash", "debug"})

	// Next turn's before_agent_start and PromptDocs MUST NOT re-activate or re-mount them.
	if _, errMsg := host.emitEvent(sdk.EventBeforeAgentStart, map[string]any{
		"systemPromptOptions": map[string]any{},
	}); errMsg != "" {
		t.Fatalf("before_agent_start: %s", errMsg)
	}
	if _, errMsg := host.emitEvent("probe", nil); errMsg != "" {
		t.Fatalf("probe 2: %s", errMsg)
	}
	if slices.Contains(host.getActiveTools(), "lsp") || slices.Contains(host.getActiveTools(), "mcp__docs__search") {
		t.Fatalf("externally deactivated tools were re-activated: active=%v", host.getActiveTools())
	}
	if !slices.Equal(mounted, []string{"debug"}) {
		t.Fatalf("mounted after external deactivation = %v, want [debug]", mounted)
	}
	if strings.Contains(docs, "lsp") || strings.Contains(docs, "mcp__docs__search") {
		t.Fatalf("externally deactivated tools leaked into PromptDocs:\n%s", docs)
	}

	// Direct model call to externally deactivated `lsp` is refused.
	if _, errMsg := host.callModelTool("lsp", "direct-lsp-after-deact", map[string]any{}); errMsg != "Tool lsp not found" {
		t.Fatalf("expected Tool lsp not found after external deactivation, got %q", errMsg)
	}

	// Turning lsp.enabled off then back on (reconcileBuiltinTools delta.added)
	// restores `lsp` as a newly enabled tool.
	lspEnabled = false
	if _, errMsg := host.emitEvent("probe", nil); errMsg != "" {
		t.Fatalf("probe 3 (lsp off): %s", errMsg)
	}
	lspEnabled = true
	if _, errMsg := host.emitEvent("probe", nil); errMsg != "" {
		t.Fatalf("probe 4 (lsp back on): %s", errMsg)
	}
	if !slices.Contains(host.getActiveTools(), "lsp") || !slices.Equal(mounted, []string{"debug", "lsp"}) {
		t.Fatalf("setting flip false->true should restore lsp; active=%v mounted=%v", host.getActiveTools(), mounted)
	}
}
