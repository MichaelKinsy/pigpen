package sessioningest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Session v2/v3 entries form a tree (id, parentId). Pi's active branch is the
// path from the last entry in the file back to the root; read_session reads that path.

func writeLines(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const treeHeader = `{"type":"session","version":3,"id":"tree-session-0001","cwd":"/w","timestamp":"2026-06-01T10:00:00.000Z"}`

func userEntry(id, parent, text string) string {
	p := "null"
	if parent != "" {
		p = `"` + parent + `"`
	}
	return `{"type":"message","id":"` + id + `","parentId":` + p + `,"timestamp":"2026-06-01T10:00:01.000Z","message":{"role":"user","content":"` + text + `"}}`
}

func assistantEntry(id, parent, text string, cost float64) string {
	usage := ""
	if cost > 0 {
		usage = `,"usage":{"input":10,"output":5,"totalTokens":15,"cost":{"total":` + formatCost(cost) + `}}`
	}
	return `{"type":"message","id":"` + id + `","parentId":"` + parent + `","timestamp":"2026-06-01T10:00:02.000Z","message":{"role":"assistant","provider":"acme","model":"m-1","content":[{"type":"text","text":"` + text + `"}]` + usage + `}}`
}

func formatCost(c float64) string { return strconv.FormatFloat(c, 'f', -1, 64) }

// forkedFixture: a1 -> a2 -> a3 -> a4 is an abandoned branch, a1 -> a2 -> a5 -> a6 the active one.
func forkedFixture(t *testing.T) string {
	return writeLines(t,
		treeHeader,
		userEntry("a1", "", "root question"),
		assistantEntry("a2", "a1", "first answer", 0),
		userEntry("a3", "a2", "abandoned question"),
		`{"type":"model_change","id":"m1","parentId":"a3","timestamp":"2026-06-01T10:00:03.000Z","provider":"old","modelId":"abandoned-model"}`,
		assistantEntry("a4", "m1", "abandoned answer", 0),
		userEntry("a5", "a2", "active question"),
		assistantEntry("a6", "a5", "active answer", 0),
	)
}

func TestForkedSessionShowsOnlyTheActiveBranch(t *testing.T) {
	path := forkedFixture(t)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.TotalTurns != 2 || toc.TotalMessages != 4 {
		t.Fatalf("turns/messages = %d/%d, want 2/4 (the abandoned branch is not part of the transcript)", toc.TotalTurns, toc.TotalMessages)
	}
	if toc.Items[1].User != "active question" {
		t.Fatalf("turn 2 = %q, want the active branch's question", toc.Items[1].User)
	}
	if len(toc.Events) != 0 {
		t.Fatalf("events = %+v, want none (the model change is on the abandoned branch)", toc.Events)
	}
	slice := run(t, map[string]any{"path": path, "mode": "slice", "format": "json"}).(sliceReport)
	var texts []string
	for _, m := range slice.Messages {
		texts = append(texts, strings.Join(m.Text, ""))
	}
	if got := strings.Join(texts, "|"); got != "root question|first answer|active question|active answer" {
		t.Fatalf("slice = %q", got)
	}
	for i, m := range slice.Messages {
		if m.MessageIndex != i+1 {
			t.Fatalf("message %d has index %d, want consecutive indexes on the active branch", i, m.MessageIndex)
		}
	}
}

func TestForkedSessionSaysHowMuchWasLeftOut(t *testing.T) {
	path := forkedFixture(t)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.Session.OffBranchEntries != 3 {
		t.Fatalf("OffBranchEntries = %d, want 3 (user, model change, assistant)", toc.Session.OffBranchEntries)
	}
	for _, mode := range []string{"toc", "slice", "stats", "query", "turn", "tools"} {
		params := map[string]any{"path": path, "mode": mode, "query": "question", "turn": 1}
		res := run(t, params).(sdk.ToolResult)
		if !strings.Contains(res.Content, "off_branch_entries: 3") {
			t.Errorf("mode=%s does not say that 3 entries were left out:\n%s", mode, res.Content)
		}
	}
	linear := run(t, map[string]any{"path": writeRichFixture(t), "mode": "toc"}).(sdk.ToolResult)
	if strings.Contains(linear.Content, "off_branch_entries") {
		t.Errorf("a linear session must not mention other branches:\n%s", linear.Content)
	}
}

func TestQueryDoesNotFindTextOnAbandonedBranches(t *testing.T) {
	q := run(t, map[string]any{"path": forkedFixture(t), "mode": "query", "query": "abandoned", "format": "json"}).(queryReport)
	if q.TotalHits != 0 {
		t.Fatalf("hits = %d, want 0", q.TotalHits)
	}
}

func TestLastEntryInTheFileIsTheLeaf(t *testing.T) {
	// The same tree with the abandoned branch appended last: that branch is now the active one.
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "root question"),
		assistantEntry("a2", "a1", "first answer", 0),
		userEntry("a5", "a2", "was active"),
		userEntry("a3", "a2", "now active"),
	)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.TotalTurns != 2 || toc.Items[1].User != "now active" || toc.Session.OffBranchEntries != 1 {
		t.Fatalf("toc = %+v", toc)
	}
}

func TestEntriesWithoutParentIdAreLinear(t *testing.T) {
	// Sessions written before the tree (v1) and entries that omit parentId follow file order.
	path := writeLines(t,
		`{"type":"session","id":"old"}`,
		`{"type":"message","message":{"role":"user","content":"one"}}`,
		`{"type":"message","id":"x","message":{"role":"assistant","content":"two"}}`,
		`{"type":"message","id":"x","message":{"role":"user","content":"three"}}`,
	)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.TotalMessages != 3 || toc.TotalTurns != 2 || toc.Session.OffBranchEntries != 0 {
		t.Fatalf("toc = %+v", toc)
	}
}

func TestUnknownParentStartsANewRoot(t *testing.T) {
	// As Pi's getBranch: the walk from the leaf stops where a parent is not in the file.
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "lost"),
		userEntry("a2", "ghost", "kept"),
	)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.TotalTurns != 1 || toc.Items[0].User != "kept" || toc.Session.OffBranchEntries != 1 {
		t.Fatalf("toc = %+v", toc)
	}
}

func compactedFixture(t *testing.T) string {
	return writeLines(t,
		treeHeader,
		userEntry("a1", "", "old question"),
		assistantEntry("a2", "a1", "old answer", 0.1),
		userEntry("a3", "a2", "kept question"),
		`{"type":"compaction","id":"c1","parentId":"a3","timestamp":"2026-06-01T10:00:05.000Z","summary":"Summary of the old work","firstKeptEntryId":"a3","tokensBefore":12345,"usage":{"input":100,"output":50,"totalTokens":150,"cost":{"total":0.05}}}`,
		userEntry("a5", "c1", "after compaction"),
		assistantEntry("a6", "a5", "ok", 0),
	)
}

func TestCompactionStaysInTheTranscript(t *testing.T) {
	path := compactedFixture(t)
	slice := run(t, map[string]any{"path": path, "mode": "slice", "format": "json", "roles": []any{"compactionSummary"}}).(sliceReport)
	if len(slice.Messages) != 1 {
		t.Fatalf("compaction messages = %d, want 1", len(slice.Messages))
	}
	c := slice.Messages[0]
	if strings.Join(c.Text, "") != "Summary of the old work" || c.TokensBefore != 12345 || c.TurnIndex != 2 || c.MessageIndex != 4 {
		t.Fatalf("compaction = %+v", c)
	}
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.TotalTurns != 3 {
		t.Fatalf("turns = %d, want 3 (messages before the compaction stay readable)", toc.TotalTurns)
	}
	if toc.Items[1].Compactions != 1 || toc.Items[0].Compactions != 0 {
		t.Fatalf("compactions per turn = %d, %d, want 0, 1", toc.Items[0].Compactions, toc.Items[1].Compactions)
	}
	md := run(t, map[string]any{"path": path, "mode": "toc"}).(sdk.ToolResult).Content
	if !strings.Contains(md, "compaction:1") {
		t.Fatalf("toc markdown does not show the compaction:\n%s", md)
	}
}

func TestCompactionSummaryIsSearchable(t *testing.T) {
	q := run(t, map[string]any{"path": compactedFixture(t), "mode": "query", "query": "old work", "format": "json"}).(queryReport)
	if q.TotalHits != 1 || q.Hits[0].Role != "compactionSummary" || q.Hits[0].TurnIndex != 2 {
		t.Fatalf("query = %+v", q)
	}
}

func TestCompactionIsPartOfItsTurnAndCostsMoney(t *testing.T) {
	path := compactedFixture(t)
	turn := run(t, map[string]any{"path": path, "mode": "turn", "turn": 2, "format": "json"}).(turnReport)
	var roles []string
	for _, m := range turn.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,compactionSummary" {
		t.Fatalf("turn 2 roles = %v", roles)
	}
	stats := run(t, map[string]any{"path": path, "mode": "stats", "format": "json"}).(statsReport)
	if stats.Roles["compactionSummary"] != 1 || stats.Usage.Cost < 0.1499 || stats.Usage.Cost > 0.1501 {
		t.Fatalf("stats roles=%v cost=%v, want the compaction's own cost (0.05) added to 0.10", stats.Roles, stats.Usage.Cost)
	}
}

func TestBranchSummaryAndCustomMessageAreTranscriptItems(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "start"),
		`{"type":"branch_summary","id":"b1","parentId":"a1","timestamp":"2026-06-01T10:00:02.000Z","fromId":"zz","summary":"Tried the other way"}`,
		`{"type":"custom_message","id":"k1","parentId":"b1","timestamp":"2026-06-01T10:00:03.000Z","customType":"note","content":[{"type":"text","text":"injected note"}],"display":true}`,
		`{"type":"custom","id":"x1","parentId":"k1","timestamp":"2026-06-01T10:00:04.000Z","customType":"private-state","data":{"secret":"zzz"}}`,
		`{"type":"label","id":"l1","parentId":"x1","timestamp":"2026-06-01T10:00:05.000Z","targetId":"a1","label":"mark"}`,
		userEntry("a2", "l1", "next"),
	)
	slice := run(t, map[string]any{"path": path, "mode": "slice", "format": "json"}).(sliceReport)
	var got []string
	for _, m := range slice.Messages {
		got = append(got, m.Role+":"+strings.Join(m.Text, ""))
	}
	want := "user:start|branchSummary:Tried the other way|custom:injected note|user:next"
	if strings.Join(got, "|") != want {
		t.Fatalf("slice = %q, want %q (entries that never reach the model, custom and label, stay out)", strings.Join(got, "|"), want)
	}
	if slice.Messages[2].CustomType != "note" {
		t.Fatalf("custom type = %q", slice.Messages[2].CustomType)
	}
}

func TestBashExecutionMessageShowsItsCommandAndOutput(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "go"),
		`{"type":"message","id":"b1","parentId":"a1","timestamp":"2026-06-01T10:00:02.000Z","message":{"role":"bashExecution","command":"ls -la","output":"total 0","exitCode":0}}`,
	)
	slice := run(t, map[string]any{"path": path, "mode": "slice", "format": "json", "roles": []any{"bashExecution"}}).(sliceReport)
	if len(slice.Messages) != 1 || strings.Join(slice.Messages[0].Text, "|") != "$ ls -la|total 0" {
		t.Fatalf("slice = %+v", slice.Messages)
	}
}

func TestSessionNameAndSideUsageAreReported(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		`{"type":"session_info","id":"i1","parentId":null,"timestamp":"2026-06-01T10:00:00.500Z","name":"first name"}`,
		userEntry("a1", "i1", "hello"),
		`{"type":"session_info","id":"i2","parentId":"a1","timestamp":"2026-06-01T10:00:01.500Z","name":"final name"}`,
		`{"type":"usage","id":"u1","parentId":"i2","timestamp":"2026-06-01T10:00:02.000Z","kind":"cache_warm","provider":"acme","model":"m-1","usage":{"input":7,"output":3,"totalTokens":10,"cost":{"total":0.02}}}`,
	)
	stats := run(t, map[string]any{"path": path, "mode": "stats", "format": "json"}).(statsReport)
	if stats.Session.Name != "final name" {
		t.Fatalf("name = %q, want the latest session_info on the branch", stats.Session.Name)
	}
	if stats.OtherUsage.Total != 10 || stats.Usage.Total != 10 || stats.Usage.Cost != 0.02 {
		t.Fatalf("usage = %+v other = %+v, want the usage entry counted once in the total", stats.Usage, stats.OtherUsage)
	}
	md := run(t, map[string]any{"path": path, "mode": "stats"}).(sdk.ToolResult).Content
	if !strings.Contains(md, "- name: final name") {
		t.Fatalf("stats markdown does not show the name:\n%s", md)
	}
}

func TestEntryWithoutAMessageBodyKeepsItsChildrenAttached(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "first"),
		`{"type":"message","id":"broken","parentId":"a1","timestamp":"2026-06-01T10:00:02.000Z"}`,
		userEntry("a2", "broken", "second"),
	)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if toc.TotalTurns != 2 || toc.Session.OffBranchEntries != 0 {
		t.Fatalf("toc = %+v, want both turns (the entry between them has no message but is still on the branch)", toc)
	}
}

func TestEventsOnTheActiveBranchAreListedOnce(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "go"),
		`{"type":"model_change","id":"m1","parentId":"a1","timestamp":"2026-06-01T10:00:02.000Z","provider":"acme","modelId":"m-9"}`,
		`{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-06-01T10:00:03.000Z","thinkingLevel":"high"}`,
	)
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json"}).(tocReport)
	if len(toc.Events) != 2 || toc.Events[0].ModelID != "m-9" || toc.Events[1].ThinkingLevel != "high" {
		t.Fatalf("events = %+v", toc.Events)
	}
}

func TestStatsMarkdownShowsSpendOutsideMessages(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "hello"),
		`{"type":"usage","id":"u1","parentId":"a1","timestamp":"2026-06-01T10:00:02.000Z","kind":"cache_warm","provider":"acme","model":"m-1","usage":{"input":7,"output":3,"totalTokens":10,"cost":{"total":0.02}}}`,
	)
	md := run(t, map[string]any{"path": path, "mode": "stats"}).(sdk.ToolResult).Content
	if !strings.Contains(md, "usage_outside_messages (included above): total 10 tokens, $0.0200") {
		t.Fatalf("stats markdown:\n%s", md)
	}
}

func TestTurnMarkdownShowsCompactionAndCustomDetails(t *testing.T) {
	path := writeLines(t,
		treeHeader,
		userEntry("a1", "", "go"),
		`{"type":"compaction","id":"c1","parentId":"a1","timestamp":"2026-06-01T10:00:02.000Z","summary":"Gist","firstKeptEntryId":"a1","tokensBefore":777}`,
		`{"type":"custom_message","id":"k1","parentId":"c1","timestamp":"2026-06-01T10:00:03.000Z","customType":"note","content":"hi","display":true}`,
	)
	turn := run(t, map[string]any{"path": path, "mode": "turn", "turn": 1, "format": "json"}).(turnReport)
	if turn.Messages[1].TokensBefore != 777 || turn.Messages[2].CustomType != "note" {
		t.Fatalf("turn messages = %+v", turn.Messages)
	}
	md := run(t, map[string]any{"path": path, "mode": "turn", "turn": 1}).(sdk.ToolResult).Content
	for _, want := range []string{"- tokens_before_compaction: 777", "- custom_type: note"} {
		if !strings.Contains(md, want) {
			t.Errorf("turn markdown missing %q:\n%s", want, md)
		}
	}
}

// The optional leaf parameter reads the branch that ends at an entry id, instead of the active branch.

func texts(slice sliceReport) string {
	var out []string
	for _, m := range slice.Messages {
		out = append(out, strings.Join(m.Text, ""))
	}
	return strings.Join(out, "|")
}

func TestLeafReadsTheAbandonedBranchThatEndsThere(t *testing.T) {
	path := forkedFixture(t)
	slice := run(t, map[string]any{"path": path, "mode": "slice", "format": "json", "leaf": "a4"}).(sliceReport)
	if got := texts(slice); got != "root question|first answer|abandoned question|abandoned answer" {
		t.Fatalf("slice = %q", got)
	}
	if slice.Session.Leaf != "a4" || slice.Session.OffBranchEntries != 2 {
		t.Fatalf("session = %+v, want leaf a4 and the 2 entries of the active branch left out", slice.Session)
	}
	toc := run(t, map[string]any{"path": path, "mode": "toc", "format": "json", "leaf": "a4"}).(tocReport)
	if len(toc.Events) != 1 || toc.Events[0].ModelID != "abandoned-model" {
		t.Fatalf("events = %+v, want the model change on that branch", toc.Events)
	}
	q := run(t, map[string]any{"path": path, "mode": "query", "query": "abandoned", "format": "json", "leaf": "a4"}).(queryReport)
	if q.TotalHits != 2 {
		t.Fatalf("hits = %d, want the two abandoned messages", q.TotalHits)
	}
}

func TestLeafInTheMiddleOfABranchCutsItThere(t *testing.T) {
	slice := run(t, map[string]any{"path": forkedFixture(t), "mode": "slice", "format": "json", "leaf": "a2"}).(sliceReport)
	if got := texts(slice); got != "root question|first answer" || slice.Session.OffBranchEntries != 5 {
		t.Fatalf("slice = %q, off branch %d", got, slice.Session.OffBranchEntries)
	}
}

func TestLeafMayBeAnyEntryNotOnlyAMessage(t *testing.T) {
	path := compactedFixture(t)
	slice := run(t, map[string]any{"path": path, "mode": "slice", "format": "json", "leaf": "c1"}).(sliceReport)
	if len(slice.Messages) != 4 || slice.Messages[3].Role != "compactionSummary" || slice.Session.OffBranchEntries != 2 {
		t.Fatalf("slice = %+v off %d", slice.Messages, slice.Session.OffBranchEntries)
	}
}

func TestNoLeafOrAnEmptyOneIsTheActiveBranch(t *testing.T) {
	path := forkedFixture(t)
	want := texts(run(t, map[string]any{"path": path, "mode": "slice", "format": "json"}).(sliceReport))
	for _, leaf := range []any{"", nil, 7} {
		got := run(t, map[string]any{"path": path, "mode": "slice", "format": "json", "leaf": leaf}).(sliceReport)
		if texts(got) != want || got.Session.Leaf != "" {
			t.Errorf("leaf %v: %q leaf %q", leaf, texts(got), got.Session.Leaf)
		}
	}
	if md := run(t, map[string]any{"path": path, "mode": "toc"}).(sdk.ToolResult).Content; strings.Contains(md, "leaf:") {
		t.Errorf("the default branch must not print a leaf line:\n%s", md)
	}
}

func TestAnUnknownLeafIsAnErrorThatNamesItInEveryMode(t *testing.T) {
	path := forkedFixture(t)
	for _, mode := range []string{"toc", "query", "slice", "turn", "tools", "stats"} {
		_, err := handleReadSession(sdk.Context{}, map[string]any{"path": path, "mode": mode, "query": "x", "turn": 1, "leaf": "no-such-entry"})
		if err == nil || !strings.Contains(err.Error(), `"no-such-entry"`) || !strings.Contains(err.Error(), "leaf") {
			t.Errorf("mode=%s: err = %v", mode, err)
		}
	}
	linear := writeLines(t, `{"type":"session","id":"old"}`, `{"type":"message","message":{"role":"user","content":"one"}}`)
	if _, err := handleReadSession(sdk.Context{}, map[string]any{"path": linear, "leaf": "x"}); err == nil {
		t.Error("a session whose entries have no ids has no leaf to select")
	}
}

func TestTheChosenLeafIsShownAndKeptByTheNextPageHint(t *testing.T) {
	path := forkedFixture(t)
	md := run(t, map[string]any{"path": path, "mode": "slice", "limit": 1, "leaf": "a4"}).(sdk.ToolResult).Content
	for _, want := range []string{"- leaf: a4", `"leaf":"a4"`} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q:\n%s", want, md)
		}
	}
	q := run(t, map[string]any{"path": path, "mode": "query", "query": "abandoned", "format": "json", "leaf": "a4"}).(queryReport)
	if !strings.Contains(q.Hits[0].RetrievalHint, "leaf=a4") {
		t.Errorf("a retrieval hint must name the leaf: %q", q.Hits[0].RetrievalHint)
	}
}
