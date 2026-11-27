package frontend

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func TestOmpCompactionNodeWithSnapcompactMeta(t *testing.T) {
	h := readyOmpRoles(t)
	h.session.lookupCompaction = func(tokensBefore int) (CompactionMeta, bool) {
		if tokensBefore != 66557 {
			return CompactionMeta{}, false
		}
		return CompactionMeta{
			Method:       "snapcompact",
			TokensBefore: 66557,
			TokensAfter:  4200,
			FrameCount:   3,
			Summary:      "## Summary\n\nKept key edits and test state.",
			Warning:      "Context approached hard limit",
		}, true
	}

	// Collapsed PiG CompactionSummaryMessageComponent lines.
	collapsedLines := []string{
		"\x1b[48;2;40;40;60m                                                            \x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[1m[compaction]\x1b[22m                                               \x1b[0m",
		"\x1b[48;2;40;40;60m                                                            \x1b[0m",
		"\x1b[48;2;40;40;60m Compacted from 66,557 tokens (ctrl+o to expand)            \x1b[0m",
		"\x1b[48;2;40;40;60m                                                            \x1b[0m",
	}
	ops := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "c1",
		Index:  0,
		Node:   fe.Lines{Lines: collapsedLines},
	})

	sec := node(ops, "n.c1")
	if sec == nil || sec["k"] != "section" {
		t.Fatalf("expected section node, got %#v", sec)
	}
	p := props(sec)
	if p["role"] != "omp.compaction" || p["collapsible"] != true || p["collapsed"] != true {
		t.Fatalf("section props = %#v", p)
	}
	wantHead := []any{
		map[string]any{"t": "Snap-compacted", "s": "muted"},
		map[string]any{"t": " · 67K", "s": "muted mono"},
		map[string]any{"t": " → ", "s": "dim"},
		map[string]any{"t": "4.2K tokens", "s": "muted mono"},
		map[string]any{"t": " ⚠", "s": "warning"},
	}
	if !reflect.DeepEqual(p["head"], wantHead) {
		t.Fatalf("head = %#v\nwant   %#v", p["head"], wantHead)
	}

	capNode := node(ops, "n.c1.caption")
	if capNode == nil || capNode["k"] != "text" || props(capNode)["role"] != "omp.compaction.caption" {
		t.Fatalf("caption node = %#v", capNode)
	}
	wantCaption := []any{map[string]any{"t": "Summary · 3 snapcompact frames attached", "s": "dim"}}
	if !reflect.DeepEqual(props(capNode)["spans"], wantCaption) {
		t.Fatalf("caption spans = %#v, want %#v", props(capNode)["spans"], wantCaption)
	}

	warnNode := node(ops, "n.c1.warning")
	if warnNode == nil || warnNode["k"] != "row" || props(warnNode)["role"] != "omp.notice.warning" {
		t.Fatalf("warning notice node = %#v", warnNode)
	}

	sumNode := node(ops, "n.c1.summary")
	if sumNode == nil || sumNode["k"] != "md" || props(sumNode)["text"] != "## Summary\n\nKept key edits and test state." {
		t.Fatalf("summary md node = %#v", sumNode)
	}

	// Expanded PiG CompactionSummaryMessageComponent lines (after Ctrl+O).
	expandedLines := []string{
		"\x1b[48;2;40;40;60m \x1b[1m[compaction]\x1b[22m\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[1mCompacted from 66,557 tokens\x1b[22m\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m Fallback expanded body\x1b[0m",
	}
	ops2 := h.applyOps(fe.Op{
		Kind:   fe.Update,
		Region: fe.RegionMain,
		ID:     "c1",
		Index:  0,
		Node:   fe.Lines{Lines: expandedLines},
	})
	doc := shownBy(ops2)
	if doc["n.c1"]["collapsed"] != false {
		t.Fatalf("expanded compaction should set collapsed=false, got ops=%#v", ops2)
	}
}

func TestOmpCompactionNodeWithoutMeta(t *testing.T) {
	h := readyOmpRoles(t)

	// Collapsed without metadata.
	collapsed := []string{
		" [compaction]",
		"",
		" Compacted from 66,557 tokens (ctrl+o to expand)",
	}
	ops := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "c2",
		Index:  0,
		Node:   fe.Lines{Lines: collapsed},
	})
	sec := node(ops, "n.c2")
	if sec == nil || sec["k"] != "section" || props(sec)["role"] != "omp.compaction" {
		t.Fatalf("section = %#v", sec)
	}
	wantHead := []any{
		map[string]any{"t": "Compacted", "s": "muted"},
		map[string]any{"t": " · 67K tokens", "s": "muted mono"},
	}
	if !reflect.DeepEqual(props(sec)["head"], wantHead) {
		t.Fatalf("head = %#v\nwant   %#v", props(sec)["head"], wantHead)
	}
	capNode := node(ops, "n.c2.caption")
	wantCaption := []any{map[string]any{"t": "Summary of the earlier conversation", "s": "dim"}}
	if !reflect.DeepEqual(props(capNode)["spans"], wantCaption) {
		t.Fatalf("caption spans = %#v, want %#v", props(capNode)["spans"], wantCaption)
	}
	if warn := node(ops, "n.c2.warning"); warn != nil {
		t.Fatalf("unexpected warning node when meta.Warning is empty: %#v", warn)
	}
	sumNode := node(ops, "n.c2.summary")
	if sumNode == nil || props(sumNode)["text"] != "Compacted from 66,557 tokens" {
		t.Fatalf("collapsed fallback summary = %#v", sumNode)
	}

	// Expanded without metadata should parse summary body from PiG lines.
	expanded := []string{
		" [compaction]",
		"",
		" Compacted from 66,557 tokens",
		"",
		" Built parser and verified tests.",
		" Next: wire frontend.",
	}
	ops2 := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "c3",
		Index:  1,
		Node:   fe.Lines{Lines: expanded},
	})
	sec3 := node(ops2, "n.c3")
	if sec3 == nil || props(sec3)["collapsed"] != false {
		t.Fatalf("expanded section = %#v", sec3)
	}
	sum3 := node(ops2, "n.c3.summary")
	if sum3 == nil || props(sum3)["text"] != "Built parser and verified tests.\nNext: wire frontend." {
		t.Fatalf("expanded parsed summary = %#v", sum3)
	}
}

func TestOmpBranchNodeCollapsedAndExpanded(t *testing.T) {
	h := readyOmpRoles(t)

	collapsed := []string{
		"\x1b[48;2;40;40;60m \x1b[1m[branch]\x1b[22m\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m Branch summary (ctrl+o to expand)\x1b[0m",
	}
	ops := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "b1",
		Index:  0,
		Node:   fe.Lines{Lines: collapsed},
	})
	sec := node(ops, "n.b1")
	if sec == nil || sec["k"] != "section" {
		t.Fatalf("branch section = %#v", sec)
	}
	p := props(sec)
	if p["role"] != "omp.branch" || p["collapsible"] != true || p["collapsed"] != true {
		t.Fatalf("branch props = %#v", p)
	}
	wantHead := []any{map[string]any{"t": "Branch summarized", "s": "muted"}}
	if !reflect.DeepEqual(p["head"], wantHead) {
		t.Fatalf("branch head = %#v, want %#v", p["head"], wantHead)
	}

	expanded := []string{
		"\x1b[48;2;40;40;60m \x1b[1m[branch]\x1b[22m\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m Branch Summary\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m Abandoned spike on old layout.\x1b[0m",
	}
	ops2 := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "b2",
		Index:  1,
		Node:   fe.Lines{Lines: expanded},
	})
	sec2 := node(ops2, "n.b2")
	if sec2 == nil || props(sec2)["collapsed"] != false {
		t.Fatalf("expanded branch section = %#v", sec2)
	}
	sum2 := node(ops2, "n.b2.summary")
	if sum2 == nil || sum2["k"] != "md" || props(sum2)["text"] != "Abandoned spike on old layout." {
		t.Fatalf("expanded branch summary = %#v", sum2)
	}
}

func TestOmpAsyncResultNodeSingleMultiAndError(t *testing.T) {
	h := readyOmpRoles(t)

	// 1. Single agent job with <task-result> and agent:// URL.
	singleAgent := []string{
		"\x1b[48;2;40;40;60m \x1b[1m[async-result]\x1b[22m\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m <system-notice>\x1b[0m",
		"\x1b[48;2;40;40;60m Background job StencilGapsResearcher has completed.\x1b[0m",
		"\x1b[48;2;40;40;60m Resume your work using the result below.\x1b[0m",
		"\x1b[48;2;40;40;60m <task-result id=\"StencilGapsResearcher\" agent=\"scout\" status=\"ok\" duration=\"12.4s\">\x1b[0m",
		"\x1b[48;2;40;40;60m StencilGapsResearcher is now idle.\x1b[0m",
		"\x1b[48;2;40;40;60m </task-result>\x1b[0m",
		"\x1b[48;2;40;40;60m Agent: agent://StencilGapsResearcher, history://StencilGapsResearcher\x1b[0m",
		"\x1b[48;2;40;40;60m </system-notice>\x1b[0m",
	}
	ops := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "a1",
		Index:  0,
		Node:   fe.Lines{Lines: singleAgent},
	})
	blk := node(ops, "n.a1")
	if blk == nil || blk["k"] != "col" || props(blk)["role"] != "omp.status-block" {
		t.Fatalf("async-result block = %#v", blk)
	}
	row0 := node(ops, "n.a1.0")
	if row0 == nil || row0["k"] != "row" || props(row0)["role"] != "omp.notice.success" || props(row0)["tone"] != "success" {
		t.Fatalf("row0 = %#v", row0)
	}
	icon0 := node(ops, "n.a1.0.icon")
	if icon0 == nil || icon0["k"] != "icon" || props(icon0)["name"] != "success" || props(icon0)["tone"] != "success" {
		t.Fatalf("icon0 = %#v", icon0)
	}
	txt0 := node(ops, "n.a1.0.text")
	wantSpans0 := []any{
		map[string]any{"t": "Background job completed"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "agent", "s": "dim"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "StencilGapsResearcher", "s": "dim"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "12.4s", "s": "dim"},
	}
	if !reflect.DeepEqual(props(txt0)["spans"], wantSpans0) {
		t.Fatalf("row0 spans = %#v\nwant        %#v", props(txt0)["spans"], wantSpans0)
	}
	// Ensure no raw <system-notice> leaked into any emitted op.
	for _, op := range ops {
		if strings.Contains(opJSON(t, op), "system-notice") {
			t.Fatalf("raw <system-notice> leaked into TSP op: %#v", op)
		}
	}

	// 2. Multi-job batch with one bash success and one agent error.
	multiBatch := []string{
		"\x1b[48;2;40;40;60m \x1b[1m[async-result]\x1b[22m\x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[0m",
		"\x1b[48;2;40;40;60m <system-notice>\x1b[0m",
		"\x1b[48;2;40;40;60m 2 background jobs have completed. Resume your work using the results below.\x1b[0m",
		"\x1b[48;2;40;40;60m ── Job bg_1 (go test) ──\x1b[0m",
		"\x1b[48;2;40;40;60m ok  pkg/foo 0.4s\x1b[0m",
		"\x1b[48;2;40;40;60m ── Job ReviewerA ──\x1b[0m",
		"\x1b[48;2;40;40;60m <task-result id=\"ReviewerA\" agent=\"reviewer\" status=\"error\" duration=\"5.0s\">\x1b[0m",
		"\x1b[48;2;40;40;60m timed out\x1b[0m",
		"\x1b[48;2;40;40;60m </task-result>\x1b[0m",
		"\x1b[48;2;40;40;60m </system-notice>\x1b[0m",
	}
	ops2 := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "a2",
		Index:  1,
		Node:   fe.Lines{Lines: multiBatch},
	})
	r0 := node(ops2, "n.a2.0")
	r1 := node(ops2, "n.a2.1")
	if r0 == nil || props(r0)["role"] != "omp.notice.success" {
		t.Fatalf("multi job 0 = %#v", r0)
	}
	wantMulti0 := []any{
		map[string]any{"t": "Background job completed"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "bash", "s": "dim"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "bg_1", "s": "dim"},
	}
	if !reflect.DeepEqual(props(node(ops2, "n.a2.0.text"))["spans"], wantMulti0) {
		t.Fatalf("multi job 0 spans = %#v", props(node(ops2, "n.a2.0.text"))["spans"])
	}
	if r1 == nil || props(r1)["role"] != "omp.notice.error" || props(r1)["tone"] != "error" {
		t.Fatalf("multi job 1 = %#v", r1)
	}
	icon1 := node(ops2, "n.a2.1.icon")
	if icon1 == nil || props(icon1)["name"] != "error" || props(icon1)["tone"] != "error" || props(icon1)["aria"] != "Error" {
		t.Fatalf("multi job 1 icon = %#v", icon1)
	}
	wantMulti1 := []any{
		map[string]any{"t": "Background job completed"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "agent", "s": "dim"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "ReviewerA", "s": "dim"},
		map[string]any{"t": " · ", "s": "dim"},
		map[string]any{"t": "5.0s", "s": "dim"},
	}
	if !reflect.DeepEqual(props(node(ops2, "n.a2.1.text"))["spans"], wantMulti1) {
		t.Fatalf("multi job 1 spans = %#v", props(node(ops2, "n.a2.1.text"))["spans"])
	}
}

func TestOmpCustomMessageCardAndPlainTextGuard(t *testing.T) {
	h := readyOmpRoles(t)

	// Purple-bg custom message box -> card[omp.custom] with stripped <system-notice>.
	ircLines := []string{
		"\x1b[48;2;40;40;60m                                        \x1b[0m",
		"\x1b[48;2;40;40;60m \x1b[1m[irc:incoming]\x1b[22m                         \x1b[0m",
		"\x1b[48;2;40;40;60m                                        \x1b[0m",
		"\x1b[48;2;40;40;60m <system-notice>                        \x1b[0m",
		"\x1b[48;2;40;40;60m From ScoutA: found the root cause.     \x1b[0m",
		"\x1b[48;2;40;40;60m </system-notice>                       \x1b[0m",
		"\x1b[48;2;40;40;60m                                        \x1b[0m",
	}
	ops := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "m1",
		Index:  0,
		Node:   fe.Lines{Lines: ircLines},
	})
	card := node(ops, "n.m1")
	if card == nil || card["k"] != "card" || props(card)["role"] != "omp.custom" {
		t.Fatalf("custom card = %#v", card)
	}
	wantHead := []any{map[string]any{"t": "irc:incoming", "s": "customMessageLabel strong"}}
	if !reflect.DeepEqual(props(card)["head"], wantHead) {
		t.Fatalf("custom card head = %#v, want %#v", props(card)["head"], wantHead)
	}
	md := node(ops, "n.m1.md")
	if md == nil || md["k"] != "md" || props(md)["text"] != "From ScoutA: found the root cause." {
		t.Fatalf("custom card md = %#v", md)
	}

	// Non-bg plain lines starting with [foo] MUST NOT false-positive as omp.custom.
	plainLines := []string{
		"[foo]",
		"some plain output line",
	}
	ops2 := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "m2",
		Index:  1,
		Node:   fe.Lines{Lines: plainLines},
	})
	plainNode := node(ops2, "n.m2")
	if plainNode == nil || plainNode["k"] != "ansi" {
		t.Fatalf("plain [foo] without bg SGR must stay ansi, got %#v", plainNode)
	}

	// Discovery sections like [Extensions] must keep omp.notice.discovery even with bg SGR.
	discoveryLines := []string{
		"\x1b[48;2;40;40;60m [Extensions]\x1b[0m",
		"\x1b[48;2;40;40;60m   oh-my-pig\x1b[0m",
	}
	ops3 := h.applyOps(fe.Op{
		Kind:   fe.Insert,
		Region: fe.RegionMain,
		ID:     "m3",
		Index:  2,
		Node:   fe.Lines{Lines: discoveryLines},
	})
	discNode := node(ops3, "n.m3")
	if discNode == nil || discNode["k"] != "section" || props(discNode)["role"] != "omp.notice.discovery" {
		t.Fatalf("discovery section = %#v", discNode)
	}
}

func opJSON(t *testing.T, op any) string {
	t.Helper()
	b, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}
