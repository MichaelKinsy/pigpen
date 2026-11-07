package posthorse

import (
	"fmt"
	"strings"
	"testing"
)

// Layer-1 tests that pin the arithmetic, wording and branch decisions of the recovery record directly.

func TestRecoveryHasRetainedRolloverMarker(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    bool
	}{
		{"marker after the prefix", autoHandoffPrefix + "\nbody\nContext rolled over after entry e1. more", true},
		{"prefix without a marker", autoHandoffPrefix + "\nbody", false},
		{"marker without the prefix", "plain\nContext rolled over after entry e1.", false},
		{"marker not at a line start", autoHandoffPrefix + " Context rolled over after entry e1.", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		if got := hasRetainedRolloverMarker(c.summary); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRecoverySkippedRecordsFitsTheLimitInUTF16Units(t *testing.T) {
	if _, ok := skippedRecords(nil, 100); ok {
		t.Error("no skipped records is undefined")
	}
	const lead = "Earlier automatic records skipped (not nested; use history read): "
	cases := []struct {
		ids   []string
		limit int
		want  string
	}{
		{[]string{"a", "bb", "ccc"}, 5, lead + "a, bb; 1 earlier record(s) not listed."},
		{[]string{"a", "bb", "ccc"}, 4, lead + "a; 2 earlier record(s) not listed."},
		{[]string{"a", "bb", "ccc"}, 9, lead + "a, bb; 1 earlier record(s) not listed."},
		{[]string{"a", "bb", "ccc"}, 10, lead + "a, bb, ccc"},
		{[]string{"a", "bb", "ccc"}, 100, lead + "a, bb, ccc"},
		{[]string{"a", "bb", "ccc"}, 0, lead + "a; 2 earlier record(s) not listed."},
		{[]string{"é", "éé"}, 5, lead + "é, éé"},
		{[]string{"é", "éé"}, 4, lead + "é; 1 earlier record(s) not listed."},
		{[]string{"😀", "😀😀"}, 8, lead + "😀, 😀😀"},
		{[]string{"😀", "😀😀"}, 7, lead + "😀; 1 earlier record(s) not listed."},
	}
	for _, c := range cases {
		got, ok := skippedRecords(c.ids, c.limit)
		if !ok || got != c.want {
			t.Errorf("skippedRecords(%q, %d) = %q, want %q", c.ids, c.limit, got, c.want)
		}
	}
}

func rcBatch(callID string, hasCallID bool, headers ...string) *recoveryBatch {
	batch := &recoveryBatch{callID: callID, hasCallID: hasCallID}
	for _, h := range headers {
		batch.blocks = append(batch.blocks, recoveryBlock{header: h, text: "text of " + h})
	}
	return batch
}

func TestRecoveryBatchHeaderNamesTheCallEntryOnlyWhenKnown(t *testing.T) {
	const base = "Tool result evidence (current projected window; may already have been received or handled; not current progress)."
	if got := batchHeader(nil); got != "" {
		t.Errorf("no batch has no header, got %q", got)
	}
	cases := []struct {
		batch *recoveryBatch
		want  string
	}{
		{rcBatch("", false), base},
		{rcBatch("", true), base},
		{rcBatch("call-1", false), base},
		{rcBatch("call-1", true), base + " Tool-call entry call-1:"},
	}
	for _, c := range cases {
		if got := batchHeader(c.batch); got != c.want {
			t.Errorf("batchHeader(%+v) = %q, want %q", c.batch, got, c.want)
		}
	}
}

func TestRecoveryPlanBatchFitsHeadersFromTheNewest(t *testing.T) {
	h1, h2 := "[result entry 1]", "[result entry 2]" // 16 units: 18 with the separator
	cases := []struct {
		available int
		blocks    []string
		omitted   int
	}{
		{36, []string{h1, h2}, 0},
		{35, []string{h2}, 1},
		{18, []string{h2}, 1},
		{17, nil, 2},
		{0, nil, 2},
	}
	for _, c := range cases {
		plan := planBatch(rcBatch("", false, h1, h2), "HEADER", c.available)
		var got []string
		for _, b := range plan.blocks {
			got = append(got, b.header)
		}
		if strings.Join(got, "|") != strings.Join(c.blocks, "|") {
			t.Errorf("available %d: blocks %q, want %q", c.available, got, c.blocks)
		}
		wantOmission := ""
		if c.omitted > 0 {
			wantOmission = fmt.Sprintf("Omitted %d earlier tool result(s) whose headers could not fit. Use history search/read to recover them.", c.omitted)
		}
		if plan.omission != wantOmission {
			t.Errorf("available %d: omission %q, want %q", c.available, plan.omission, wantOmission)
		}
	}
	// Headers count UTF-16 units, not bytes.
	wide := "[é é é é é é é é é é]"
	plan := planBatch(rcBatch("", false, wide), "HEADER", u16len(wide)+2)
	if len(plan.blocks) != 1 {
		t.Errorf("a %d-unit header must fit %d units, got %d blocks", u16len(wide), u16len(wide)+2, len(plan.blocks))
	}
	// The history pointer names the call entry only when there is one.
	with := planBatch(rcBatch("call-9", true, h1, h2), "HEADER", 0)
	if !strings.Contains(with.omission, "Use history read with tool-call entry call-9, then history search/read to recover them.") {
		t.Errorf("omission %q", with.omission)
	}
	empty := planBatch(rcBatch("", true, h1, h2), "HEADER", 0)
	if !strings.Contains(empty.omission, ". Use history search/read to recover them.") {
		t.Errorf("an empty call id gives no pointer, got %q", empty.omission)
	}
	if none := planBatch(nil, "", 100); none.header != "" || none.bare != "" || len(none.blocks) != 0 {
		t.Errorf("no batch plans nothing, got %+v", none)
	}
}

func TestRecoveryRenderIndexListsTheNewestLinesThatFit(t *testing.T) {
	coordination := func(id, customType string) recoveryRecord {
		return recoveryRecord{id: id, timestamp: "t", kind: "coordination", label: "visible " + customType + " coordination input (not direct owner input)", text: "x"}
	}
	records := []recoveryRecord{coordination("e1", "alpha"), coordination("e2", "beta")}
	l1, l2 := u16len(indexLine(records[0])), u16len(indexLine(records[1]))
	const head = "Other current-window inputs, oldest first (recover with history read):"

	if _, ok := renderIndex(nil, 100); ok {
		t.Error("no records renders no index")
	}
	all, _ := renderIndex(records, l1+l2+2)
	if want := head + "\n" + indexLine(records[0]) + "\n" + indexLine(records[1]); all != want {
		t.Errorf("exact budget lists everything oldest first:\n%s\nwant\n%s", all, want)
	}
	one, _ := renderIndex(records, l1+l2+1)
	if want := head + "\n1 earlier input(s) not listed; use history search to find them.\n" + indexLine(records[1]); one != want {
		t.Errorf("one unit short drops the oldest line:\n%s\nwant\n%s", one, want)
	}
	none, _ := renderIndex(records, 0)
	if want := head + "\n2 earlier input(s) not listed; use history search to find them."; none != want {
		t.Errorf("no budget lists nothing:\n%s\nwant\n%s", none, want)
	}
	wide := []recoveryRecord{coordination("é", "ééééé")}
	exact, _ := renderIndex(wide, u16len(indexLine(wide[0]))+1)
	if !strings.HasSuffix(exact, indexLine(wide[0])) {
		t.Errorf("a line is measured in UTF-16 units, got %q", exact)
	}
	// Owner inputs keep a one-line preview of 120 units at most.
	owner := func(text string) string {
		return indexLine(recoveryRecord{id: "o", timestamp: "t", kind: "owner", label: "owner input", text: text})
	}
	head120 := "[owner input | t | entry o] "
	if got := owner(strings.Repeat("a", 120)); got != head120+strings.Repeat("a", 120) {
		t.Errorf("120 units are not cut: %q", got)
	}
	if got := owner(strings.Repeat("a", 121)); got != head120+strings.Repeat("a", 119)+"…" {
		t.Errorf("121 units are cut to 119 plus an ellipsis: %q", got)
	}
	if got := owner("a\u00a0\u3000  b\n\tc"); got != head120+"a b c" {
		t.Errorf("whitespace runs collapse (JS \\s): %q", got)
	}
}

func rcNoteBranch(names ...string) []rec {
	var branch []rec
	for i, name := range names {
		callID := fmt.Sprintf("c%d", i)
		branch = append(branch,
			rcMsg(fmt.Sprintf("call-%d", i), rec{"role": "assistant", "content": []any{rcNotesCall(callID, "write", name, "x")}}),
			rcNotesResult(fmt.Sprintf("res-%d", i), callID, "notes", "Wrote /ws/.pi/notes/"+name),
		)
	}
	return branch
}

func rcRenderNotes(t *testing.T, branch []rec, maxChars int, span *retainedSpan, retained map[string]struct{}) (string, bool) {
	t.Helper()
	out, ok, err := renderSessionNotes(branch, branch, nil, sessionNotesOptions{retained: retained, span: span, maxChars: maxChars})
	if err != nil {
		t.Fatal(err)
	}
	return out, ok
}

func TestRecoveryNotesSectionCapsAndBudget(t *testing.T) {
	// 6 written and 5 read notes: only the newest 4 written and 3 read are listed.
	var branch []rec
	for i := range 6 {
		branch = append(branch, rcNoteBranch(fmt.Sprintf("w%d.md", i))...)
	}
	for i := range 5 {
		callID := fmt.Sprintf("rc%d", i)
		branch = append(branch,
			rcMsg(fmt.Sprintf("rcall-%d", i), rec{"role": "assistant", "content": []any{rcNotesCall(callID, "read", fmt.Sprintf("r%d.md", i), "")}}),
			rcNotesResult(fmt.Sprintf("rres-%d", i), callID, "notes", fmt.Sprintf("File: /ws/.pi/notes/r%d.md\nbody", i)),
		)
	}
	out, ok := rcRenderNotes(t, branch, 20_000, nil, nil)
	if !ok {
		t.Fatal("no section")
	}
	if got := strings.Count(out, "- Active note (written)"); got != 4 {
		t.Errorf("%d written lines, want 4:\n%s", got, out)
	}
	if got := strings.Count(out, "- Referenced note (read)"); got != 3 {
		t.Errorf("%d read lines, want 3:\n%s", got, out)
	}
	if !strings.Contains(out, "w5.md") || strings.Contains(out, "w1.md") || !strings.Contains(out, "r4.md") || strings.Contains(out, "r1.md") {
		t.Errorf("the newest notes are kept:\n%s", out)
	}
	if !strings.HasSuffix(out, "Read w5.md (notes read or read on its path) before continuing to restore the saved goal, progress, decisions, and next steps, then keep that note current as work progresses.") {
		t.Errorf("the action names the newest written note:\n%s", out)
	}

	// The section is measured in UTF-16 units: exactly its length fits, one unit less does not.
	one := rcNoteBranch(strings.Repeat("é", 40) + ".md")
	full, _ := rcRenderNotes(t, one, 20_000, nil, nil)
	if got, ok := rcRenderNotes(t, one, u16len(full), nil, nil); !ok || got != full {
		t.Errorf("a section of exactly %d units fits", u16len(full))
	}
	if _, ok := rcRenderNotes(t, one, u16len(full)-1, nil, nil); ok {
		t.Error("a section one unit too long is dropped")
	}
	if _, ok := rcRenderNotes(t, nil, 20_000, nil, nil); ok {
		t.Error("no notes, no section")
	}
}

func TestRecoveryNotesSectionCapInTheHandoff(t *testing.T) {
	// Find the shapes that make the full notes section exactly 1,200 and 1,201 units long. The last note's
	// path counts three times (line label, line path, action) and the call entry id once, so every residue is reachable.
	build := func(pad, idPad int) []rec {
		names := []string{strings.Repeat("a", 150) + ".md", strings.Repeat("b", 150) + ".md", strings.Repeat("c", pad) + ".md"}
		branch := rcNoteBranch(names...)
		branch[4]["id"] = "call-2" + strings.Repeat("i", idPad)
		return branch
	}
	for _, c := range []struct {
		length     int
		wantOldest bool
	}{{1200, true}, {1201, false}} {
		found := false
		for pad := 1; pad < 400 && !found; pad++ {
			for idPad := 0; idPad < 3 && !found; idPad++ {
				branch := build(pad, idPad)
				out, _ := rcRenderNotes(t, branch, 1<<20, nil, nil)
				if u16len(out) != c.length {
					continue
				}
				found = true
				got := rcBuild(t, branch, rcProjection(branch), maxHandoffChars, autoHandoffOptions{})
				// The oldest written note is the last line before the action; it is listed only when everything fits.
				if listed := strings.Contains(got, strings.Repeat("a", 150)+".md ("); listed != c.wantOldest {
					t.Errorf("a %d-unit notes section: oldest note listed = %v, want %v", c.length, listed, c.wantOldest)
				}
			}
		}
		if !found {
			t.Fatalf("no shape gives a %d-unit section", c.length)
		}
	}
}

func TestRecoveryTrackedNotesMergeTargets(t *testing.T) {
	read := func(entry, abs, ref string) noteTouch {
		return noteTouch{notePath: "a.md", absPath: abs, referenceScope: ref, kind: "read", entryId: entry}
	}
	// The second touch has the same key ("scope1\x00a.md") through its abs path.
	got := trackedNotes([]noteTouch{read("e1", "/abs/one", "scope1"), read("e2", "scope1", "")}, "")
	if len(got) != 1 || got[0].absPath != "scope1" || got[0].referenceScope != "scope1" || got[0].entryId != "e2" || got[0].order != 1 {
		t.Errorf("a read replaces the target but keeps the reference scope: %+v", got)
	}
	// A read without an abs path keeps the earlier one.
	got = trackedNotes([]noteTouch{read("e1", "/abs/one", "scope1"), read("e2", "", "scope1")}, "")
	if len(got) != 1 || got[0].absPath != "/abs/one" {
		t.Errorf("a read without an abs path keeps the earlier one: %+v", got)
	}
	// A read after a write keeps the written note and its proof.
	written := noteTouch{notePath: "a.md", absPath: "/abs/a.md", kind: "full-write", entryId: "w1", inRetained: true}
	got = trackedNotes([]noteTouch{written, {notePath: "a.md", absPath: "/abs/a.md", kind: "read", entryId: "r1"}}, "")
	if len(got) != 1 || got[0].kind != "written" || got[0].proof.kind != "full" || got[0].entryId != "w1" {
		t.Errorf("a read after a write leaves the written note: %+v", got)
	}
	// A clear forgets the note; the default directory fills a missing abs path.
	got = trackedNotes([]noteTouch{written, {notePath: "a.md", absPath: "/abs/a.md", kind: "clear", entryId: "c1"}}, "")
	if len(got) != 0 {
		t.Errorf("a clear forgets the note: %+v", got)
	}
	got = trackedNotes([]noteTouch{{notePath: "b.md", kind: "read", entryId: "e"}}, "/dir")
	if len(got) != 1 || got[0].absPath != "/dir/b.md" {
		t.Errorf("the default directory completes the path: %+v", got)
	}
	// Newest touch first.
	got = trackedNotes([]noteTouch{read("e1", "/x/1", ""), {notePath: "z.md", absPath: "/x/z", kind: "read", entryId: "e2"}}, "")
	if len(got) != 2 || got[0].notePath != "z.md" {
		t.Errorf("newest first: %+v", got)
	}
}

func TestRecoveryNoteLabelsAndLines(t *testing.T) {
	reference := trackedNote{notePath: "a.md", referenceScope: "/x/y", kind: "read", entryId: "e1"}
	if got, want := formatNoteLabel(&reference), `a.md (read-only reference; notes read {"scope":"/x/y","path":"a.md"})`; got != want {
		t.Errorf("label %q, want %q", got, want)
	}
	plain := trackedNote{notePath: "a.md", absPath: "a.md"}
	if got := formatNoteLabel(&plain); got != "a.md" {
		t.Errorf("an abs path equal to the note path adds nothing, got %q", got)
	}
	span := &retainedSpan{firstId: "f1", lastId: "l1"}
	retainedRead := trackedNote{notePath: "a.md", kind: "read", entryId: "e1", inRetained: true}
	if got, want := formatReadNoteLine(&retainedRead, span), "- Referenced note (read): a.md [read in the retained tail above at entry e1]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := formatReadNoteLine(&retainedRead, nil), "- Referenced note (read): a.md [compacted at entry e1]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	compactedRead := trackedNote{notePath: "a.md", kind: "read", entryId: "e1"}
	if got, want := formatReadNoteLine(&compactedRead, span), "- Referenced note (read): a.md [compacted before entry f1]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	compactedWrite := trackedNote{notePath: "a.md", kind: "written", entryId: "e9", proof: contentProof{kind: "compacted"}}
	if got, want := formatWrittenNoteLine(&compactedWrite, span), "- Active note (written): a.md [compacted before entry f1 — read this note now]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := formatWrittenNoteLine(&compactedWrite, nil), "- Active note (written): a.md [compacted at entry e9 — read this note now]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	full := trackedNote{notePath: "a.md", kind: "written", proof: contentProof{kind: "full", entryId: "w1", updatedAt: "u1"}}
	if got, want := formatWrittenNoteLine(&full, span), "- Active note (written): a.md [latest full write is in the retained tail above at entry w1, updated at entry u1]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := formatNotesAction(&full, span), "Your latest full write to a.md is already visible above at entry w1; do not rewrite it unless new findings after entry l1 require it."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := formatNotesAction(nil, span); got != "Read the referenced note again if its context is still needed, and keep one current-state note for this task." {
		t.Errorf("got %q", got)
	}
}

// C-06: a bare ask_question is registered; a namespaced call or result is not the owner's answer.
func TestRecoveryNamespacedAskQuestionIsNotOwnerInput(t *testing.T) {
	build := func(callNamespace, resultNamespace string) string {
		call := rec{"type": "toolCall", "id": "q1", "name": "ask_question", "arguments": rec{}}
		if callNamespace != "" {
			call["namespace"] = callNamespace
		}
		result := rec{"role": "toolResult", "toolName": "ask_question", "toolCallId": "q1", "isError": false, "content": "SURVEY_ANSWER"}
		if resultNamespace != "" {
			result["namespace"] = resultNamespace
		}
		branch := []rec{
			rcTimed("u1", "1", rec{"role": "user", "content": "start the survey"}),
			rcMsg("qa", rec{"role": "assistant", "stopReason": "toolUse", "content": []any{call}}),
			rcTimed("qr", "2", result),
		}
		return newFixture(t, withExtraTools("ask_question")).AutomaticHandoff(branch)
	}
	bare := build("", "")
	rcMatch(t, bare, `\[owner answer via ask_question \| 2 \| entry qr\]\nSURVEY_ANSWER`)
	for name, handoff := range map[string]string{"namespaced call": build("survey", ""), "namespaced result": build("", "survey")} {
		if strings.Contains(handoff, "owner answer via ask_question") {
			t.Errorf("%s must not be an owner answer:\n%s", name, handoff)
		}
		rcMatch(t, handoff, `\[result entry qr\]\nSURVEY_ANSWER`)
		rcMatch(t, handoff, `Tool result evidence`)
	}
}

// C-19: an in-turn edit of a synchronous result before the threshold fallback; the draft points at the
// replacement, never the original.
func TestRecoveryThresholdFallbackUsesTheEditedResult(t *testing.T) {
	const window, reserve = 100_000, 16_384
	big := strings.Repeat("x", 600_000)
	call := rec{"type": "toolCall", "id": "c1", "name": "dump", "arguments": rec{}}
	assistant := rec{"role": "assistant", "content": []any{call}, "stopReason": "toolUse", "timestamp": float64(1)}
	original := rec{"role": "toolResult", "toolCallId": "c1", "toolName": "dump", "content": []any{fxText("PRIVATE_TOOL_MARKER " + big)}, "isError": false, "timestamp": float64(1)}
	edited := rec{"role": "toolResult", "toolCallId": "c1", "toolName": "dump", "content": []any{fxText("APPROVED_TOOL_MARKER " + big)}, "isError": false, "timestamp": float64(1)}
	session := fxIndexedSession(
		fxMessageEntry("u1", fxUser("Run the local dump tool.")),
		fxMessageEntry("a1", assistant),
		fxMessageEntry("r1", original),
		rec{"type": "context_edit", "id": "edit-1", "targetId": "r1", "replacement": rec{"content": []any{fxText("APPROVED_TOOL_MARKER " + big)}}},
	)
	f := newFixture(t, withUsage(window, window-reserve+1), withSession(session))
	got := f.TurnEndEvent(roFallbackEvent(roFreshBoundary(
		rec{"sourceEntry": rec{"type": "message", "id": "a1"}, "messages": []any{assistant}},
		rec{"sourceEntry": rec{"type": "message", "id": "r1"}, "messages": []any{edited}},
	), nil))
	entries, _ := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("want one threshold draft, got %v", got)
	}
	summary, _ := roRec(entries[0])["summary"].(string)
	if strings.Contains(summary, "PRIVATE_TOOL_MARKER") {
		t.Error("the original result must not reach the record")
	}
	if !strings.Contains(summary, "APPROVED_TOOL_MARKER") {
		t.Errorf("the edited result is the evidence:\n%.600s", summary)
	}
	rcMatch(t, summary, `\[result entry edit-1\]\nAPPROVED_TOOL_MARKER`)
}
