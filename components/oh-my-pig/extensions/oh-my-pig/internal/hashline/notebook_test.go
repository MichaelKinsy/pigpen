package hashline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ported upstream tests from crates/edit/src/notebook.rs:489-623 plus end-to-end
// hashline StageInput + Persist round-tripping on .ipynb files.

// upstream: crates/edit/src/notebook.rs: "detects_notebook_paths_case_insensitively"
func TestNotebookPathDetection(t *testing.T) {
	if !IsNotebookPath("work/Book.IPYNB") {
		t.Fatal("expected .IPYNB to be detected")
	}
	if IsNotebookPath("work/Book.json") {
		t.Fatal("expected .json not to be detected")
	}
}

// upstream: crates/edit/src/notebook.rs: "splits_source_preserving_line_endings"
func TestNotebookSplitSource(t *testing.T) {
	if got := SplitNotebookSource(""); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
	got1 := SplitNotebookSource("a\nb\n")
	if len(got1) != 2 || got1[0] != "a\n" || got1[1] != "b\n" {
		t.Fatalf("got1 = %q", got1)
	}
	got2 := SplitNotebookSource("a\nb")
	if len(got2) != 2 || got2[0] != "a\n" || got2[1] != "b" {
		t.Fatalf("got2 = %q", got2)
	}
}

// upstream: crates/edit/src/notebook.rs: "serializes_floats_like_javascript"
func TestNotebookSerializesFloatsLikeJS(t *testing.T) {
	v, err := parseOrderedJSON(`[0.1, 1e21, 1.5e-7, 123456789012345680000.0, -0.0]`)
	if err != nil {
		t.Fatal(err)
	}
	want := "[\n 0.1,\n 1e+21,\n 1.5e-7,\n 123456789012345680000,\n 0\n]"
	if got := stringifyIndent1(v); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// upstream: crates/edit/src/notebook.rs: "golden_json_serialization_matches_bun" & "unchanged_editable_round_trip_matches_golden"
func TestNotebookGoldenRoundTrip(t *testing.T) {
	for _, base := range []string{"mixed", "empty"} {
		fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "notebooks", base+".ipynb"))
		if err != nil {
			t.Fatal(err)
		}
		goldenBytes, err := os.ReadFile(filepath.Join("testdata", "notebooks", base+".ipynb.golden.json"))
		if err != nil {
			t.Fatal(err)
		}
		fixture := string(fixtureBytes)
		golden := string(goldenBytes)

		parsed, err := parseOrderedJSON(fixture)
		if err != nil {
			t.Fatalf("%s parse: %v", base, err)
		}
		if got := stringifyIndent1(parsed); got != golden {
			t.Fatalf("%s golden mismatch:\ngot:\n%s\nwant:\n%s", base, got, golden)
		}

		editable, err := NotebookToEditableText(fixture, base+".ipynb")
		if err != nil {
			t.Fatalf("%s to editable: %v", base, err)
		}
		serialized, err := SerializeEditedNotebookText(fixture, true, editable, base+".ipynb")
		if err != nil {
			t.Fatalf("%s round-trip: %v", base, err)
		}
		if serialized != golden {
			t.Fatalf("%s round-trip mismatch:\ngot:\n%s\nwant:\n%s", base, serialized, golden)
		}
	}
}

// upstream: crates/edit/src/notebook.rs: "marker_like_source_lines_are_escaped_and_restored"
func TestNotebookMarkerEscaping(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "notebooks", "mixed.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := string(fixtureBytes)
	editable, err := NotebookToEditableText(fixture, "mixed.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(editable, "# %%% [markdown] cell:3") {
		t.Fatalf("expected escaped marker in:\n%s", editable)
	}
	serialized, err := SerializeEditedNotebookText(fixture, true, editable, "mixed.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(serialized), &doc); err != nil {
		t.Fatal(err)
	}
	cells := doc["cells"].([]any)
	src := cells[0].(map[string]any)["source"].([]any)
	if src[1].(string) != "# %% [markdown] cell:3\n" {
		t.Fatalf("restored source[1] = %q", src[1])
	}
}

// upstream: crates/edit/src/notebook.rs: "reuses_cells_and_preserves_outputs_metadata_and_key_order"
func TestNotebookReusesCellsAndPreservesMetadata(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "notebooks", "mixed.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	text := "# %% [markdown] cell:0\nUpdated\n# %% [code] cell:1\nprint('new')\n"
	serialized, err := SerializeEditedNotebookText(string(fixtureBytes), true, text, "mixed.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	var nb map[string]any
	if err := json.Unmarshal([]byte(serialized), &nb); err != nil {
		t.Fatal(err)
	}
	cells := nb["cells"].([]any)
	c0 := cells[0].(map[string]any)
	if c0["metadata"].(map[string]any)["nested"].(map[string]any)["label"] != "λ" {
		t.Fatalf("c0 metadata not preserved: %+v", c0["metadata"])
	}
	if _, hasEC := c0["execution_count"]; hasEC {
		t.Fatal("markdown cell should not keep execution_count")
	}
	if _, hasOut := c0["outputs"]; hasOut {
		t.Fatal("markdown cell should not keep outputs")
	}
	c1 := cells[1].(map[string]any)
	if c1["execution_count"] != nil {
		t.Fatalf("c1 execution_count = %v", c1["execution_count"])
	}
	if out, ok := c1["outputs"].([]any); !ok || len(out) != 0 {
		t.Fatalf("c1 outputs = %v", c1["outputs"])
	}
	src1 := c1["source"].([]any)
	if len(src1) != 1 || src1[0] != "print('new')\n" {
		t.Fatalf("c1 source = %v", src1)
	}
}

// upstream: crates/edit/src/notebook.rs: "duplicate_or_missing_indices_create_fresh_cells", "absent_notebook_uses_empty_nbformat_45_document", "reports_validation_errors_verbatim"
func TestNotebookDuplicateIndicesAbsentAndValidationErrors(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "notebooks", "mixed.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	text := "# %% [code] cell:0\na\n# %% [raw] cell:0\nb\n# %% [markdown]\nc"
	serialized, err := SerializeEditedNotebookText(string(fixtureBytes), true, text, "mixed.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	var nb map[string]any
	if err := json.Unmarshal([]byte(serialized), &nb); err != nil {
		t.Fatal(err)
	}
	cells := nb["cells"].([]any)
	if cells[0].(map[string]any)["execution_count"].(float64) != 7 {
		t.Fatalf("cell 0 execution_count = %v", cells[0].(map[string]any)["execution_count"])
	}
	if cells[1].(map[string]any)["cell_type"] != "raw" {
		t.Fatalf("cell 1 = %v", cells[1])
	}
	if cells[2].(map[string]any)["cell_type"] != "markdown" {
		t.Fatalf("cell 2 = %v", cells[2])
	}

	// Absent notebook creates nbformat 4.5.
	fresh, err := SerializeEditedNotebookText("", false, "# %% [code] cell:0\nanswer = 42", "new.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	var nb2 map[string]any
	if err := json.Unmarshal([]byte(fresh), &nb2); err != nil {
		t.Fatal(err)
	}
	if nb2["nbformat"].(float64) != 4 || nb2["nbformat_minor"].(float64) != 5 {
		t.Fatalf("nb2 = %v", nb2)
	}

	// Exact validation error strings.
	checks := []struct {
		fn   func() error
		want string
	}{
		{func() error { _, e := NotebookToEditableText("not json", "bad.ipynb"); return e }, "Invalid JSON in notebook: bad.ipynb"},
		{func() error { _, e := NotebookToEditableText("[]", "bad.ipynb"); return e }, "Invalid notebook structure (expected object): bad.ipynb"},
		{func() error { _, e := NotebookToEditableText("{}", "bad.ipynb"); return e }, "Invalid notebook structure (missing cells array): bad.ipynb"},
		{func() error {
			_, e := NotebookToEditableText(`{"cells":[{"cell_type":"other"}]}`, "bad.ipynb")
			return e
		}, "Invalid notebook cell 0 in bad.ipynb"},
		{func() error { _, e := ParseNotebookEditableText("preamble", "bad.ipynb"); return e }, `Invalid notebook editable representation for bad.ipynb: expected first line to be "# %% [code] cell:0", "# %% [markdown] cell:0", or "# %% [raw] cell:0".`},
	}
	for _, c := range checks {
		err := c.fn()
		if err == nil || err.Error() != c.want {
			t.Fatalf("got %v, want %q", err, c.want)
		}
	}
}

// End-to-end test: editing a `.ipynb` via StageInput + Persist edits the cell projection
// (including head/tail inserts) and re-serializes valid JSON instead of corrupting raw JSON.
func TestNotebookEndToEndHashlineEditAndHeadTailSafety(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("testdata", "notebooks", "mixed.ipynb"))
	if err != nil {
		t.Fatal(err)
	}
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("n.ipynb", string(fixtureBytes))

	editable, err := NotebookToEditableText(string(fixtureBytes), "n.ipynb")
	if err != nil {
		t.Fatal(err)
	}
	tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "n.ipynb"), NormalizeToLF(editable))

	// 1. Anchored edit on cell 0 source line 2.
	if _, _, editErr := w.apply("[n.ipynb#" + tag + "]\nPUT 2.=2:\n+print(\"updated\")\n"); editErr != nil {
		t.Fatalf("anchored notebook edit failed: %v", editErr)
	}
	after, _ := w.readFile("n.ipynb")
	var doc map[string]any
	if err := json.Unmarshal([]byte(after), &doc); err != nil {
		t.Fatalf("notebook corrupted after edit: %v\n%s", err, after)
	}
	src0 := doc["cells"].([]any)[0].(map[string]any)["source"].([]any)
	if src0[0] != "print(\"updated\")\n" {
		t.Fatalf("cell 0 source = %v", src0)
	}

	// 2. Tail insert (`PUT >$:`) appends to the last cell cleanly (valid JSON, never corrupts raw JSON).
	editable2, _ := NotebookToEditableText(after, "n.ipynb")
	tag2 := w.store.RecordReadWholeFile(filepath.Join(w.dir, "n.ipynb"), NormalizeToLF(editable2))
	if _, _, editErr := w.apply("[n.ipynb#" + tag2 + "]\nPUT >$:\n+tail\n"); editErr != nil {
		t.Fatalf("tail insert on notebook failed: %v", editErr)
	}
	after2, _ := w.readFile("n.ipynb")
	var doc2 map[string]any
	if err := json.Unmarshal([]byte(after2), &doc2); err != nil {
		t.Fatalf("notebook corrupted after PUT >$:: %v\n%s", err, after2)
	}

	// 3. Head insert (`PUT <1:`) with non-marker line is refused by ParseNotebookEditableText
	// before any write, leaving the valid JSON file untouched!
	editable3, _ := NotebookToEditableText(after2, "n.ipynb")
	tag3 := w.store.RecordReadWholeFile(filepath.Join(w.dir, "n.ipynb"), NormalizeToLF(editable3))
	if _, _, editErr := w.apply("[n.ipynb#" + tag3 + "]\nPUT <1:\n+preamble\n"); editErr == nil || !strings.Contains(editErr.Error(), "Invalid notebook editable representation") {
		t.Fatalf("expected invalid editable representation refusal for non-marker PUT <1:, got %v", editErr)
	}
	after3, _ := w.readFile("n.ipynb")
	if after3 != after2 {
		t.Fatal("failed PUT <1: should not have modified n.ipynb")
	}
}

// Finding N7: verified byte-for-byte against real omp 19.1.1 (serde_json preserve_order swap-remove)
// when changing code cells to raw/markdown drops execution_count and outputs.
func TestNotebookSwapRemoveKeyOrderMatchesRealOmp(t *testing.T) {
	orig := "{\n \"cells\": [\n  {\n   \"cell_type\": \"code\",\n   \"execution_count\": 3,\n   \"id\": \"c0\",\n   \"metadata\": {\n    \"k\": 1\n   },\n   \"outputs\": [\n    {\n     \"output_type\": \"stream\",\n     \"name\": \"stdout\",\n     \"text\": [\n      \"hi\\n\"\n     ]\n    }\n   ],\n   \"source\": [\n    \"x = 1\\n\"\n   ],\n   \"custom\": \"keep0\"\n  },\n  {\n   \"cell_type\": \"code\",\n   \"execution_count\": 7,\n   \"id\": \"c1\",\n   \"metadata\": {\n    \"m\": 2\n   },\n   \"outputs\": [],\n   \"source\": [\n    \"y = 2\\n\"\n   ],\n   \"custom\": \"keep1\"\n  }\n ],\n \"metadata\": {},\n \"nbformat\": 4,\n \"nbformat_minor\": 5\n}"
	edited := "# %% [raw] cell:0\nx = 1\n\n# %% [markdown] cell:1\ny = 2\n"
	got, err := SerializeEditedNotebookText(orig, true, edited, "nb.ipynb")
	if err != nil {
		t.Fatalf("SerializeEditedNotebookText: %v", err)
	}
	// Exact bytes produced by real omp 19.1.1 via oracle.sh:
	want := "{\n \"cells\": [\n  {\n   \"cell_type\": \"raw\",\n   \"custom\": \"keep0\",\n   \"id\": \"c0\",\n   \"metadata\": {\n    \"k\": 1\n   },\n   \"source\": [\n    \"x = 1\\n\"\n   ]\n  },\n  {\n   \"cell_type\": \"markdown\",\n   \"custom\": \"keep1\",\n   \"id\": \"c1\",\n   \"metadata\": {\n    \"m\": 2\n   },\n   \"source\": [\n    \"y = 2\\n\"\n   ]\n  }\n ],\n \"metadata\": {},\n \"nbformat\": 4,\n \"nbformat_minor\": 5\n}"
	if got != want {
		t.Fatalf("notebook key order mismatch vs real omp:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
