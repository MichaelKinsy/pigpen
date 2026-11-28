package hashline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// JSON-driven upstream fixture suites loaded directly from testdata/fixtures/
// (copied verbatim from omp tag v19.1.1, commit 31ffee513f,
// crates/edit/tests/fixtures/hashline/).

type patcherFixtureRoot struct {
	Cases []patcherFixtureCase `json:"cases"`
}

type patcherFixtureCase struct {
	Name      string                `json:"name"`
	Files     map[string]string     `json:"files"`
	Snapshots []patcherFixtureSnap  `json:"snapshots"`
	Policy    *patcherFixturePolicy `json:"policy"`
	Args      struct {
		Input string `json:"input"`
	} `json:"args"`
	Expect patcherFixtureExpect `json:"expect"`
}

type patcherFixtureSnap struct {
	Path      string   `json:"path"`
	Text      *string  `json:"text"`
	SeenLines []uint32 `json:"seenLines"`
}

type patcherFixturePolicy struct {
	EnforceSeenLines *bool `json:"enforceSeenLines"`
}

type patcherFixtureExpect struct {
	Files        map[string]string `json:"files"`
	Deleted      []string          `json:"deleted"`
	Writes       *int              `json:"writes"`
	Error        string            `json:"error"`
	TextContains []string          `json:"textContains"`
}

// upstream: crates/edit/tests/fixtures/hashline/patcher.json (all 26 cases loaded via encoding/json)
func TestPatcherJSONFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", "patcher.json"))
	if err != nil {
		t.Fatalf("read patcher.json: %v", err)
	}
	var root patcherFixtureRoot
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("unmarshal patcher.json: %v", err)
	}
	if len(root.Cases) != 26 {
		t.Fatalf("expected 26 cases in patcher.json, got %d", len(root.Cases))
	}

	for _, tc := range root.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			w := newTestWorkspace(t)
			if tc.Policy != nil && tc.Policy.EnforceSeenLines != nil {
				w.opts.EnforceSeenLines = *tc.Policy.EnforceSeenLines
			}
			for rel, content := range tc.Files {
				w.writeFile(rel, content)
			}
			tags := make(map[string]string)
			for _, snap := range tc.Snapshots {
				var text string
				if snap.Text != nil {
					text = *snap.Text
				} else {
					rawContent, ok := tc.Files[snap.Path]
					if !ok {
						t.Fatalf("snapshot path %q missing from files", snap.Path)
					}
					_, body := StripBOM(rawContent)
					text = NormalizeToLF(body)
				}
				var tag string
				if snap.SeenLines != nil {
					tag = w.store.Record(filepath.Join(w.dir, snap.Path), text, snap.SeenLines, true)
				} else {
					tag = w.store.Record(filepath.Join(w.dir, snap.Path), text, nil, false)
				}
				tags[snap.Path] = tag
			}
			input := tc.Args.Input
			for rel, tag := range tags {
				input = strings.ReplaceAll(input, fmt.Sprintf("{{tag:%s}}", rel), tag)
			}

			out, staged, applyErr := w.apply(input)
			writes := 0
			for i := range staged {
				if staged[i].Op != EngineFileOpNoop {
					writes++
				}
			}

			if tc.Expect.Writes != nil && writes != *tc.Expect.Writes {
				t.Fatalf("writes = %d, want %d (err=%v, out=%q)", writes, *tc.Expect.Writes, applyErr, out)
			}

			if tc.Expect.Error != "" {
				if applyErr == nil {
					t.Fatalf("expected error containing %q, got success:\n%s", tc.Expect.Error, out)
				}
				if !strings.Contains(applyErr.Error(), tc.Expect.Error) {
					t.Fatalf("error = %q, want substring %q", applyErr.Error(), tc.Expect.Error)
				}
			} else {
				if applyErr != nil {
					t.Fatalf("unexpected error: %v", applyErr)
				}
			}

			for _, substr := range tc.Expect.TextContains {
				if !strings.Contains(out, substr) {
					t.Fatalf("output missing %q:\n%s", substr, out)
				}
			}
			for rel, want := range tc.Expect.Files {
				got, ok := w.readFile(rel)
				if !ok {
					t.Fatalf("expected file %q to exist", rel)
				}
				if got != want {
					t.Fatalf("file %q = %q, want %q", rel, got, want)
				}
			}
			for _, rel := range tc.Expect.Deleted {
				if _, ok := w.readFile(rel); ok {
					t.Fatalf("expected file %q to be deleted", rel)
				}
			}
		})
	}
}

type parityFixtureRoot struct {
	Source string              `json:"source"`
	Cases  []parityFixtureCase `json:"cases"`
}

type parityFixtureCase struct {
	Name  string            `json:"name"`
	Parse []parityParseCall `json:"parse"`
	Apply []parityApplyCall `json:"apply"`
}

type parityParseCall struct {
	Input  string `json:"input"`
	Error  string `json:"error"`
	Expect *struct {
		EditCount int             `json:"editCount"`
		FileOp    json.RawMessage `json:"fileOp"`
		Warnings  []string        `json:"warnings"`
	} `json:"expect"`
}

type parityApplyCall struct {
	Text         string           `json:"text"`
	Input        string           `json:"input"`
	Path         string           `json:"path"`
	OnEmptyPaste string           `json:"onEmptyPaste"`
	Error        string           `json:"error"`
	Clipboard    *parityClipboard `json:"clipboard"`
	Expect       *struct {
		Text             string   `json:"text"`
		FirstChangedLine *uint32  `json:"firstChangedLine"`
		Warnings         []string `json:"warnings"`
	} `json:"expect"`
}

type parityClipboard struct {
	Lines           []string            `json:"lines"`
	Named           map[string][]string `json:"named"`
	PendingAnonCuts []string            `json:"pendingAnonCuts"`
}

func legacyParityError(msg string) string {
	if s, ok := strings.CutPrefix(msg, "InvalidAbsoluteRangeError: "); ok {
		return s
	}
	if s, ok := strings.CutPrefix(msg, "Error: "); ok {
		return s
	}
	return msg
}

func fileOpJSON(op *FileOp) string {
	if op == nil {
		return "null"
	}
	switch op.Kind {
	case FileOpDelete:
		return `{"kind":"rem"}`
	case FileOpMove:
		b, _ := json.Marshal(map[string]string{"kind": "move", "dest": op.Dest})
		return string(b)
	default:
		return "null"
	}
}

func normalizeCompactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// upstream: crates/edit/tests/hashline_parity.rs: "legacy_parser_and_applier_cases_match_byte_for_byte"
// Loads the 12 parse/apply parity_*.json fixtures from testdata/fixtures/ (10 run every
// parse+apply call byte-for-byte; 2 with tree-sitter parse-probe apply cases run every
// parse call byte-for-byte). The 6 remaining parity_*.json fixtures in testdata/fixtures/ are:
//   - parity_streaming_matcher_paths.json: loaded and asserted in TestParityStreamingMatcherPathsFixture.
//   - parity_seen_line_guard.json, parity_hashline_loop_guard.json, parity_coding_agent_hashline.json,
//     parity_block_replace.json, parity_edit_streaming_preview.json: title-only inventory files
//     (no input/expect payloads in JSON — upstream drives them via Rust code in hashline_parity.rs);
//     verified by TestParityInventoryFixturesCovered and the dedicated Go tests in patcher_test.go,
//     parity_test.go, block_test.go, and review_fixes_test.go.
func TestParityJSONFixtures(t *testing.T) {
	// These 10 fixtures have zero tree-sitter parse-probe dependencies: EVERY parse and apply call MUST pass byte-for-byte.
	fullParityFixtures := []string{
		"parity_block.json",
		"parity_clipboard.json",
		"parity_core_contracts.json",
		"parity_diff_preview.json",
		"parity_file_ops.json",
		"parity_format_v2.json",
		"parity_leniency.json",
		"parity_patcher.json",
		"parity_recovery_session_chain.json",
		"parity_snapshots.json",
	}
	// These 2 fixtures mix parse calls (which MUST pass byte-for-byte) with apply calls that
	// exercise tree-sitter parse-probe checks (`parsesCleanly`, `node_chain_at`, `landing_rejection`,
	// `boundary_fix_rejection`, `edit_broke_parse_warning`) documented in UPSTREAM.md as not ported.
	parseOnlyAndTrackApplyFixtures := []string{
		"parity_boundary_repair.json",
		"parity_landing_shift.json",
	}
	runParse := func(t *testing.T, source, caseName string, call parityParseCall) {
		t.Helper()
		parsed, pErr := ParsePatch(call.Input)
		if call.Error != "" {
			if pErr == nil {
				t.Errorf("%s: %s: expected parse error %q for %q, got %d edits", source, caseName, call.Error, call.Input, len(parsed.Edits))
				return
			}
			want := legacyParityError(call.Error)
			if pErr.Error() != want {
				t.Errorf("%s: %s: parse error mismatch for %q:\ngot:  %q\nwant: %q", source, caseName, call.Input, pErr.Error(), want)
			}
			return
		}
		if pErr != nil {
			t.Errorf("%s: %s: unexpected parse error for %q: %v", source, caseName, call.Input, pErr)
			return
		}
		if call.Expect == nil {
			return
		}
		if len(parsed.Edits) != call.Expect.EditCount {
			t.Errorf("%s: %s: editCount = %d, want %d for %q", source, caseName, len(parsed.Edits), call.Expect.EditCount, call.Input)
		}
		if gotOp, wantOp := fileOpJSON(parsed.FileOp), normalizeCompactJSON(call.Expect.FileOp); gotOp != wantOp {
			t.Errorf("%s: %s: fileOp = %s, want %s for %q", source, caseName, gotOp, wantOp, call.Input)
		}
		wantWarnings := call.Expect.Warnings
		if wantWarnings == nil {
			wantWarnings = []string{}
		}
		gotWarnings := parsed.Warnings
		if gotWarnings == nil {
			gotWarnings = []string{}
		}
		if !slices.Equal(gotWarnings, wantWarnings) {
			t.Errorf("%s: %s: warnings = %v, want %v for %q", source, caseName, gotWarnings, wantWarnings, call.Input)
		}
	}

	runApply := func(source, caseName string, call parityApplyCall) error {
		parsed, pErr := ParsePatch(call.Input)
		if pErr != nil {
			return fmt.Errorf("%s: %s: fixture apply input did not parse: %v", source, caseName, pErr)
		}
		var cb *Clipboard
		if call.Clipboard != nil {
			cb = &Clipboard{
				Lines:           slices.Clone(call.Clipboard.Lines),
				HasAnon:         call.Clipboard.Lines != nil,
				PendingAnonCuts: slices.Clone(call.Clipboard.PendingAnonCuts),
			}
			if call.Clipboard.Named != nil {
				cb.Named = make(map[string][]string, len(call.Clipboard.Named))
				for k, v := range call.Clipboard.Named {
					cb.Named[k] = slices.Clone(v)
				}
			}
		}
		onEmpty := EmptyPasteThrow
		if call.OnEmptyPaste == "drop" {
			onEmpty = EmptyPasteDrop
		}
		res, aErr := ApplyEdits(call.Text, parsed.Edits, ApplyOptions{
			Clipboard:    cb,
			Path:         call.Path,
			OnEmptyPaste: onEmpty,
		})
		if call.Error != "" {
			if aErr == nil {
				return fmt.Errorf("%s: %s: expected apply error %q, got text=%q", source, caseName, call.Error, res.Text)
			}
			want := legacyParityError(call.Error)
			if aErr.Error() != want {
				return fmt.Errorf("%s: %s: apply error mismatch:\ngot:  %q\nwant: %q", source, caseName, aErr.Error(), want)
			}
			return nil
		}
		if aErr != nil {
			return fmt.Errorf("%s: %s: unexpected apply error: %v", source, caseName, aErr)
		}
		if call.Expect == nil {
			return nil
		}
		var wantFirst uint32
		if call.Expect.FirstChangedLine != nil {
			wantFirst = *call.Expect.FirstChangedLine
		}
		wantWarnings := call.Expect.Warnings
		if wantWarnings == nil {
			wantWarnings = []string{}
		}
		gotWarnings := res.Warnings
		if gotWarnings == nil {
			gotWarnings = []string{}
		}
		if res.Text != call.Expect.Text || res.FirstChangedLine != wantFirst || !slices.Equal(gotWarnings, wantWarnings) {
			return fmt.Errorf("%s: %s: apply mismatch:\ngot:  text=%q first=%d warnings=%v\nwant: text=%q first=%d warnings=%v",
				source, caseName, res.Text, res.FirstChangedLine, gotWarnings, call.Expect.Text, wantFirst, wantWarnings)
		}
		return nil
	}

	totalParse := 0
	totalFullApply := 0
	for _, fname := range fullParityFixtures {
		t.Run(fname, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", fname))
			if err != nil {
				t.Fatalf("read %s: %v", fname, err)
			}
			var root parityFixtureRoot
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatalf("unmarshal %s: %v", fname, err)
			}
			for _, c := range root.Cases {
				for _, p := range c.Parse {
					totalParse++
					runParse(t, root.Source, c.Name, p)
				}
				for _, a := range c.Apply {
					totalFullApply++
					if err := runApply(root.Source, c.Name, a); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}

	for _, fname := range parseOnlyAndTrackApplyFixtures {
		t.Run(fname+"_parse", func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", fname))
			if err != nil {
				t.Fatalf("read %s: %v", fname, err)
			}
			var root parityFixtureRoot
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatalf("unmarshal %s: %v", fname, err)
			}
			for _, c := range root.Cases {
				for _, p := range c.Parse {
					totalParse++
					runParse(t, root.Source, c.Name, p)
				}
			}
		})
	}

	if totalParse == 0 || totalFullApply == 0 {
		t.Fatalf("expected non-zero fixture calls, got parse=%d apply=%d", totalParse, totalFullApply)
	}
}

// upstream: crates/edit/tests/fixtures/hashline/parity_streaming_matcher_paths.json
func TestParityStreamingMatcherPathsFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", "parity_streaming_matcher_paths.json"))
	if err != nil {
		t.Fatalf("read parity_streaming_matcher_paths.json: %v", err)
	}
	var root struct {
		Cases []struct {
			Name    string     `json:"name"`
			Input   string     `json:"input"`
			Paths   []string   `json:"paths"`
			Entries [][]string `json:"entries"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(root.Cases) == 0 {
		t.Fatal("expected cases in parity_streaming_matcher_paths.json")
	}
	for _, tc := range root.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := InspectInput(tc.Input)
			wantPaths := tc.Paths
			if wantPaths == nil {
				wantPaths = []string{}
			}
			gotPaths := got.Paths
			if gotPaths == nil {
				gotPaths = []string{}
			}
			if !slices.Equal(gotPaths, wantPaths) {
				t.Fatalf("paths = %q, want %q", gotPaths, wantPaths)
			}
			if tc.Entries != nil {
				if len(got.Entries) != len(tc.Entries) {
					t.Fatalf("entries len = %d (%+v), want %d (%v)", len(got.Entries), got.Entries, len(tc.Entries), tc.Entries)
				}
				for i, want := range tc.Entries {
					if got.Entries[i].Path != want[0] || got.Entries[i].Digest != want[1] {
						t.Fatalf("entry[%d] = %+v, want %q", i, got.Entries[i], want)
					}
				}
			}
		})
	}
}

// Verifies all 18 parity_*.json files in testdata/fixtures/ are accounted for:
// 13 of 18 are executed directly from JSON payloads (12 parse/apply suites +
// parity_streaming_matcher_paths.json), and 5 are upstream title-only inventory files
// (64 upstream titles; no input/expect payloads in JSON — upstream drives them via Rust
// code in hashline_parity.rs). Every one of the 64 titles is mapped below to the Go test
// that covers it (or explicitly marked "not covered"):
//
// 1. parity_seen_line_guard.json (15 titles, source: packages/coding-agent/test/edit/seen-line-guard.test.ts):
//   - "records the displayed range as seen and excludes far lines" -> internal/files/url_seen_test.go: TestSeenLineEnforcementAndRetry
//   - "rejects an edit on a line the partial read never displayed" -> internal/files/url_seen_test.go: TestSeenLineEnforcementAndRetry, patcher_test.go: TestSeenLineGuardCoversFullRangeAndRecoveryPath
//   - "applies an edit on a displayed line" -> internal/files/url_seen_test.go: TestSeenLineEnforcementAndRetry
//   - "records raw single-range reads as seen without emitting a hashline header" -> internal/files/files_test.go: TestReadSelectorForms, internal/files/pass2_fixes_test.go: TestRawTruncationAndClosedRangeTerminator
//   - "records every raw multi-range line and rejects anchors outside those ranges" -> internal/files/files_test.go: TestReadSelectorForms, patcher_test.go: TestSeenLineGuardCoversFullRangeAndRecoveryPath
//   - "does not mark a raw multi-range line as seen when the byte cap prevents full output" -> internal/files/pass2_fixes_test.go: TestRawTruncationAndClosedRangeTerminator
//   - "merges displayed lines from ACP bridge range reads into existing provenance" -> not covered (ACP bridge not ported; see UPSTREAM.md)
//   - "marks raw ACP bridge blank-line reads as seen without hashline output" -> not covered (ACP bridge not ported; see UPSTREAM.md)
//   - "merges displayed lines from ACP bridge multi-range reads into existing provenance" -> not covered (ACP bridge not ported; see UPSTREAM.md)
//   - "reveals the actual line content in the rejection and unblocks a same-tag retry" -> patcher_test.go: TestSeenLineGuardCoversFullRangeAndRecoveryPath, TestMultiFileRevealLetsIdenticalResendApply
//   - "keeps the re-read fallback when the anchor set exceeds the inline reveal cap" -> parity_test.go: TestShiftedStaleTagOverRevealCapNamesCurrentNumbers
//   - "marks column-clipped read lines as seen (clipped-line check removed)" -> review_fixes_test.go: TestSeenLinesAllColumnCutAndWholeFileRead
//   - "records matched lines as seen and rejects an edit on an unsearched line" -> not covered (search-minted tag integration lives in internal/search)
//   - "rejects editing an unsearched line under a search-minted tag" -> not covered (search-minted tag integration lives in internal/search)
//   - "applies an edit on an unseen line when edit.enforceSeenLines is off" -> patcher_test.go: TestMultiFileAtomicityDuplicateCanonicalAndSamePathMerge (opts.EnforceSeenLines=false)
//
// 2. parity_hashline_loop_guard.json (5 titles, source: packages/coding-agent/test/core/hashline-loop-guard.test.ts):
//   - "returns the soft hint for the first NOOP_HARD_LIMIT - 1 attempts" -> patcher_test.go: TestIdenticalNoopEscalatesOnThirdAttempt
//   - "escalates to a thrown ToolError on the Nth consecutive byte-identical no-op" -> patcher_test.go: TestIdenticalNoopEscalatesOnThirdAttempt
//   - "does not accumulate across distinct canonical paths" -> not covered (per-path key verified in store.go; no dedicated multi-path loop-guard test)
//   - "resets the counter after a successful (non-noop) commit on the same path" -> patcher_test.go: TestIdenticalNoopEscalatesOnThirdAttempt (ResetNoop in workspace apply)
//   - "isolates state per ToolSession (no cross-session leakage)" -> session_registry_test.go: TestSessionStoreLifecycleIsolation
//
// 3. parity_coding_agent_hashline.json (16 titles, source: packages/coding-agent/test/core/hashline.test.ts):
//   - "rejects file creation and directs to the write tool" -> patcher_test.go: TestEachRefusalCarriesErrorKind (file_not_found)
//   - "applies duplicate pure-insert payload literally" -> apply_test.go: TestApplyInsertsBeforeAfterHeadAndTail
//   - "preserves UTF-8 BOM bytes when hashline edits decoded text" -> hashline_test.go: TestLFNormalizationRoundTrips, internal/files/v19_guards_test.go: TestWriteLineEndingsAndBOM
//   - "edits BOM-prefixed notebooks through the virtual cell text" -> notebook_test.go: TestNotebookEndToEndHashlineEditAndHeadTailSafety
//   - "emits an actionable no-op diagnostic when the payload matches the file byte-for-byte" -> patcher_test.go: TestIdenticalNoopEscalatesOnThirdAttempt
//   - "preflights every section before writing multi-file edits" -> patcher_test.go: TestMultiFileAtomicityDuplicateCanonicalAndSamePathMerge
//   - "rejects duplicate canonical targets before writing stale section results" -> patcher_test.go: TestMultiFileAtomicityDuplicateCanonicalAndSamePathMerge
//   - "applies multiple sections targeting the same file against the original snapshot" -> patcher_test.go: TestMultiFileAtomicityDuplicateCanonicalAndSamePathMerge
//   - "recovers when the file was modified out-of-band after a read" -> internal/files/files_test.go: TestReadThenEditAndStaleTag, extra_upstream_test.go: TestUpwardShiftRangeRescue
//   - "falls back to mismatch error when the cache does not cover the failing anchor" -> patcher_test.go: TestTagFromMismatchReplyCoversOnlyDisplayedRows
//   - "captures the post-edit result so the next edit can recover from anchors against it" -> patcher_test.go: TestRowsAnEditWroteAreDisplayedForNextEdit
//   - "rejects replay when a prior in-session edit rewrote the line the model re-targets" -> extra_upstream_test.go: TestShiftedInsertCutAndRestoredRowStayRejected
//   - "redirects a bare filename to the full path of the file its tag names" -> parity_test.go: TestSandboxTagPathRecoveryRebindsWithinSandboxOnly
//   - "refuses redirects that escalate privilege or leave the working tree" -> patcher_test.go: TestMissingPathWithKnownNameAndTagRejectedNotRebound, parity_test.go: TestSandboxTagPathRecoveryRebindsWithinSandboxOnly
//   - "recovers a bare plan-file name onto the local:// sandbox in plan mode" -> parity_test.go: TestSandboxTagPathRecoveryRebindsWithinSandboxOnly
//   - "still rejects an existing working-tree edit in plan mode after the recovery reorder" -> gate_test.go: TestGateChecksAllSectionsAndBlocksBypasses, internal/files/v19_guards_test.go: TestGatePolicyAndMultiSessionRegister
//
// 4. parity_block_replace.json (14 titles, source: packages/coding-agent/test/core/block-replace.test.ts):
//   - "resolves the inner `if` block (line 2) and replaces its full span" -> block_test.go: TestBlockResolverUsesSyntaxAndRejectsCloser, extra_upstream_test.go: TestBlockReplaceAndCutEndToEnd
//   - "resolves the enclosing function block (line 1) and replaces the whole construct" -> extra_upstream_test.go: TestBlockReplaceAndCutEndToEnd
//   - "deletes the resolved `if` block (line 2) end-to-end via `CUT N*`" -> extra_upstream_test.go: TestBlockReplaceAndCutEndToEnd
//   - "inserts after an Emacs Lisp top-level macro-style form" -> not covered (.el is refused conservatively in pure Go; see UPSTREAM.md)
//   - "inserts after an extensionless .emacs top-level form" -> not covered (.emacs is refused conservatively in pure Go; see UPSTREAM.md)
//   - "reports the diff for a resolved block edit" -> extra_upstream_test.go: TestBlockReplaceAndCutEndToEnd
//   - "echoes the resolved span in the result text for PUT N*:" -> extra_upstream_test.go: TestBlockReplaceAndCutEndToEnd
//   - "echoes the resolved span in the result text for CUT N*" -> extra_upstream_test.go: TestBlockReplaceAndCutEndToEnd, block_test.go: TestBlockCutOnLeadingGoStatementKeepsFollowingSiblings
//   - "rejects a lone closing delimiter (no block begins there) and steers to `SWAP N.=M:`" -> block_test.go: TestReviewBlockOpMatrix, TestBlockResolverUsesSyntaxAndRejectsCloser
//   - "suggests the next block opener for a blank anchor without modifying the file" -> block_test.go: TestReviewBlockOpMatrix, TestUnresolvedBlockReplacementReportsContext
//   - "suggests the enclosing block and exact statement range without modifying the file" -> block_test.go: TestBlockOpOnLaterRustAttributesRefusedAndPointsAtFirst
//   - "rejects a block edit on an unrecognized language" -> block_test.go: TestReviewBlockOpMatrix (notes.txt)
//   - "CUT N* at a `## H2` deletes the entire section, including nested subsections" -> block_test.go: TestReviewBlockOpMatrix (doc.md), review_fixes_test.go: TestAdversarialBlockAnchorsPerResolvableLanguage
//   - "PUT >N*: at a `## H2` lands after the whole section, past nested subsections" -> block_test.go: TestReviewBlockOpMatrix (doc.md heading span 1-8)
//
// 5. parity_edit_streaming_preview.json (14 titles, source: packages/coding-agent/test/edit-streaming-preview.test.ts):
//   - "keeps section A's preview when section B's header just arrived" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "ignores parse errors from the trailing in-progress section" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "renders both sections once each has at least one valid op" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "renders a live diff while the sole payload line is still being typed" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "does not surface stale hash errors while streaming" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "final preview accepts a live content hash even when the snapshot store has no history" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "final preview recovers a stale tag from snapshot history" -> not covered (streaming preview recovery tested only at StageInput level)
//   - "surfaces stale hash errors once streaming is complete" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "yields no preview (not an error) before the first payload byte arrives" -> parity_test.go: TestPreviewPatchStreamingAndFinal
//   - "hashline: digests stripped `+` body rows only, never headers or op lines" -> fixture_json_test.go: TestParityStreamingMatcherPathsFixture
//   - "hashline: grammar-only payload digests to empty, missing input to undefined" -> fixture_json_test.go: TestParityStreamingMatcherPathsFixture
//   - "streaming: a bare header never rebinds onto a same-named working-tree file" -> not covered (bare-header streaming preview isolation not separately asserted)
//   - "args-complete: the final preview names the file the tag was issued for" -> not covered (final preview path recovery not separately asserted)
//   - "no unique basename+tag match: surfaces the read error instead of recovering" -> not covered (preview path-recovery miss not separately asserted)
func TestAll18ParityFixturesAccountedFor(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	inventoryOnly := map[string]bool{
		"parity_seen_line_guard.json":        true,
		"parity_hashline_loop_guard.json":    true,
		"parity_coding_agent_hashline.json":  true,
		"parity_block_replace.json":          true,
		"parity_edit_streaming_preview.json": true,
	}
	executable := map[string]bool{
		"parity_block.json":                   true,
		"parity_boundary_repair.json":         true,
		"parity_clipboard.json":               true,
		"parity_core_contracts.json":          true,
		"parity_diff_preview.json":            true,
		"parity_file_ops.json":                true,
		"parity_format_v2.json":               true,
		"parity_landing_shift.json":           true,
		"parity_leniency.json":                true,
		"parity_patcher.json":                 true,
		"parity_recovery_session_chain.json":  true,
		"parity_snapshots.json":               true,
		"parity_streaming_matcher_paths.json": true,
	}
	count := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "parity_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		count++
		if !executable[name] && !inventoryOnly[name] {
			t.Errorf("unaccounted parity fixture: %s", name)
		}
		if inventoryOnly[name] {
			// Confirm it really has no runnable parse/apply/input payload we missed.
			raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Cases []map[string]any `json:"cases"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("unmarshal %s: %v", name, err)
			}
			for _, c := range doc.Cases {
				if _, hasParse := c["parse"]; hasParse {
					t.Errorf("%s unexpectedly has runnable parse payloads", name)
				}
				if _, hasApply := c["apply"]; hasApply {
					t.Errorf("%s unexpectedly has runnable apply payloads", name)
				}
			}
		}
	}
	if count != 18 {
		t.Fatalf("expected 18 parity_*.json fixtures, got %d", count)
	}
}
