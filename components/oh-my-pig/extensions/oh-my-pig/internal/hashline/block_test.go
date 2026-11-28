package hashline

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Block-op resolution tests covering the complete reviewer matrix (OH-MY-PIG-REVIEW-OmpFiles2.md §1)
// plus upstream block tests from crates/edit/tests/hashline_apply.rs and hashline_parity.rs.
// Rule: every case EITHER resolves to omp's exact span OR refuses explicitly — NEVER a wrong span.

func TestReviewBlockOpMatrix(t *testing.T) {
	type blockCase struct {
		name      string
		path      string
		source    string
		anchor    uint32
		wantStart uint32 // 0 = expect refusal (nil or single-line)
		wantEnd   uint32
		expectErr string // substring expected when refused via ResolveBlockEdits
	}

	cases := []blockCase{
		// 1. JS `const a = 1;` two lines above a function, line 1 -> refuses: single-line block
		{
			name:      "js single-line const above function",
			path:      "a.js",
			source:    "const a = 1;\n\nfunction foo() {\n  return a;\n}\n",
			anchor:    1,
			expectErr: "resolved a single-line block",
		},
		// 2. Go closing `}` of previous func -> refuses: closer
		{
			name:      "go closing brace of previous func",
			path:      "a.go",
			source:    "func a() {\n\tx()\n}\nfunc b() {\n\ty()\n}\n",
			anchor:    3,
			expectErr: "could not resolve a syntactic block",
		},
		// 3. Go blank line -> "Line N is blank... Retry `PUT M*:`"
		{
			name:      "go blank line above func",
			path:      "a.go",
			source:    "\nfunc b() {\n\ty()\n}\n",
			anchor:    1,
			expectErr: "Line 1 is blank; no syntactic block can begin there. The next multi-line block begins at line 2 and ends at line 4. Retry `PUT 2*:`.",
		},
		// 4. Go `// comment` above a func -> refuses
		{
			name:      "go standalone comment above func",
			path:      "a.go",
			source:    "// note\nfunc b() {\n\ty()\n}\n",
			anchor:    1,
			expectErr: "could not resolve a syntactic block",
		},
		// 5. Python `def foo(a,\n b):` -> whole function
		{
			name:      "python multiline signature",
			path:      "a.py",
			source:    "def foo(a,\n        b):\n    return a + b\n\nx = 1\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   3,
		},
		// 6. Python `if/else` and `try/except/finally` -> whole statement
		{
			name:      "python if/elif/else",
			path:      "a.py",
			source:    "if a:\n    x = 1\nelif b:\n    x = 2\nelse:\n    x = 3\nafter()\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   6,
		},
		{
			name:      "python try/except/finally",
			path:      "a.py",
			source:    "try:\n    work()\nexcept ValueError:\n    handle()\nfinally:\n    cleanup()\ndone()\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   6,
		},
		// 7. Python `def f():  # note`, docstring at column 0, dedented comment -> whole function
		{
			name:      "python def with trailing comment, col0 docstring, dedented comment",
			path:      "a.py",
			source:    "def f():  # note\n    \"\"\"doc\ncol0 line\n\"\"\"\n# dedented comment\n    return 1\n\nafter()\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   6,
		},
		// 8. Python `x = 1` then `foo(2)` -> refuses (single-line)
		{
			name:      "python single statement above call",
			path:      "a.py",
			source:    "x = 1\nfoo(2)\n",
			anchor:    1,
			expectErr: "resolved a single-line block",
		},
		// 9. Python second decorator -> refuses; first decorator resolves whole item
		{
			name:      "python first decorator resolves whole item",
			path:      "a.py",
			source:    "@first\n@second\ndef f():\n    return 1\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   4,
		},
		{
			name:      "python second decorator refused",
			path:      "a.py",
			source:    "@first\n@second\ndef f():\n    return 1\n",
			anchor:    2,
			expectErr: "resolved a single-line block",
		},
		// 10. Rust second attribute -> refuses; first attribute resolves whole item
		{
			name:      "rust first attribute resolves whole item",
			path:      "a.rs",
			source:    "/// Docs.\n#[derive(Debug)]\nstruct S {\n    x: u32,\n}\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   5,
		},
		{
			name:      "rust second attribute refused",
			path:      "a.rs",
			source:    "/// Docs.\n#[derive(Debug)]\nstruct S {\n    x: u32,\n}\n",
			anchor:    2,
			expectErr: "resolved a single-line block",
		},
		// 11. Rust `fn foo<'a>(x: &'a str) ... {` -> whole fn (lifetimes not read as quotes)
		{
			name:      "rust fn with lifetimes",
			path:      "a.rs",
			source:    "fn foo<'a>(x: &'a str) -> &'a str {\n    let c = '{';\n    x\n}\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   4,
		},
		// 12. YAML `key: x` with `tags: [a, b]` two lines down -> refuses (single-line)
		{
			name:      "yaml scalar key above flow list",
			path:      "a.yaml",
			source:    "key: x\nother: y\ntags: [a, b]\n",
			anchor:    1,
			expectErr: "resolved a single-line block",
		},
		// 13. YAML `steps:` with same-indent list, list item, `key: |` -> resolves
		{
			name:      "yaml same-indent list under mapping key",
			path:      "a.yaml",
			source:    "steps:\n- run: build\n- run: test\nnext: true\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   3,
		},
		{
			name:      "yaml list item block",
			path:      "a.yaml",
			source:    "steps:\n  - name: build\n    run: make\n  - name: test\n",
			anchor:    2,
			wantStart: 2,
			wantEnd:   3,
		},
		{
			name:      "yaml block scalar pipe",
			path:      "a.yaml",
			source:    "script: |\n  echo one\n  echo two\nafter: 1\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   3,
		},
		// 14. Markdown heading block -> resolves through deeper headings & trailing blank before sibling (verified vs real omp)
		{
			name:      "markdown heading block includes deeper headings",
			path:      "doc.md",
			source:    "## Section\n\nIntro\n\n### Sub\n\nDetails\n\n## Next\n",
			anchor:    1,
			wantStart: 1,
			wantEnd:   8,
		},
		{
			name:      "markdown paragraph refused conservatively",
			path:      "doc.md",
			source:    "First line of para\nwith [a link](https://example.com)\n\nSecond para\n",
			anchor:    1,
			expectErr: "could not resolve a syntactic block",
		},
		// 16. Markdown list, bash if..fi, JSX <ul>...</ul>, JSX <div className={x}> -> refuse conservatively
		{
			name:      "markdown list refused conservatively",
			path:      "doc.md",
			source:    "- one\n- two\n",
			anchor:    1,
			expectErr: "could not resolve a syntactic block",
		},
		{
			name:      "bash if..fi refused conservatively",
			path:      "run.sh",
			source:    "if true; then\n  echo hi\nfi\n",
			anchor:    1,
			expectErr: "could not resolve a syntactic block",
		},
		{
			name:      "jsx ul refused conservatively",
			path:      "app.tsx",
			source:    "<ul>\n  <li>a</li>\n</ul>\n",
			anchor:    1,
			expectErr: "could not resolve a syntactic block",
		},
		{
			name:      "jsx div with expression prop refused conservatively",
			path:      "app.tsx",
			source:    "function C() {\n  return (\n    <div className={x}>\n      <span>hi</span>\n    </div>\n  );\n}\n",
			anchor:    3,
			expectErr: "could not resolve a syntactic block",
		},
		// 17. JS `} else {` -> refuses: closer
		{
			name:      "js closing brace with else refused",
			path:      "a.js",
			source:    "if (a) {\n  x();\n} else {\n  y();\n}\n",
			anchor:    3,
			expectErr: "could not resolve a syntactic block",
		},
		// 18. `.txt` (no grammar) -> refuses with no grammar message
		{
			name:      "txt unknown grammar refused",
			path:      "notes.txt",
			source:    "alpha {\n  beta\n}\n",
			anchor:    1,
			expectErr: "block locators need a tree-sitter grammar, and this file type has none",
		},
		// 19. JS line with template or regex literal containing brackets -> refuses: single-line
		{
			name:      "js template literal containing unclosed brace refused as single-line",
			path:      "a.js",
			source:    "const s = `hello { world`;\nconst next = 2;\n",
			anchor:    1,
			expectErr: "resolved a single-line block",
		},
		{
			name:      "js regex literal containing unclosed brace refused as single-line",
			path:      "a.js",
			source:    "const re = /foo{1/g;\nconst next = 2;\n",
			anchor:    1,
			expectErr: "resolved a single-line block",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edit := Edit{
				Kind:      EditBlock,
				Anchor:    Anchor{Line: tc.anchor},
				Payloads:  []string{"// replaced"},
				BlockMode: BlockModeReplace,
				LineNum:   1,
			}
			var got []BlockResolution
			_, err := ResolveBlockEdits([]Edit{edit}, tc.source, tc.path, UnresolvedThrow, func(r BlockResolution) {
				got = append(got, r)
			}, nil)
			if tc.wantStart > 0 {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				if len(got) != 1 || got[0].Start != tc.wantStart || got[0].End != tc.wantEnd {
					t.Fatalf("resolved %+v, want %d-%d", got, tc.wantStart, tc.wantEnd)
				}
			} else {
				if err == nil {
					t.Fatalf("expected refusal, but resolved %+v", got)
				}
				if tc.expectErr != "" && !strings.Contains(err.Error(), tc.expectErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tc.expectErr)
				}
			}
		})
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "native_block_resolver_uses_real_syntax"
func TestBlockResolverUsesSyntaxAndRejectsCloser(t *testing.T) {
	lines := strings.Split("fn outer() {\n\tif true {\n\t\twork();\n\t}\n}\n", "\n")
	sp := ResolveBlockAt("x.rs", lines, 2)
	if sp == nil || sp.Start != 2 || sp.End != 4 {
		t.Fatalf("got %+v, want 2-4", sp)
	}
	if sp := ResolveBlockAt("x.rs", lines, 4); sp != nil {
		t.Fatalf("expected nil on closer, got %+v", sp)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "unresolved_insert_after_block_lowers_and_warns"
func TestUnresolvedInsertAfterBlockLowersAndWarns(t *testing.T) {
	edit := Edit{
		Kind:      EditBlock,
		Anchor:    Anchor{Line: 1},
		Payloads:  []string{"next"},
		BlockMode: BlockModeInsertAfter,
		LineNum:   7,
	}
	var warnings []string
	lowered, err := ResolveBlockEdits([]Edit{edit}, "plain", "x.unknown", UnresolvedThrow, nil, func(w string) {
		warnings = append(warnings, w)
	})
	if err != nil || len(lowered) != 1 || lowered[0].Kind != EditInsert || lowered[0].Cursor.Kind != CursorAfterAnchor || len(warnings) != 1 {
		t.Fatalf("lowered=%+v warnings=%v err=%v", lowered, warnings, err)
	}
}

// upstream: crates/edit/tests/hashline_apply.rs: "unresolved_block_replacement_reports_context"
func TestUnresolvedBlockReplacementReportsContext(t *testing.T) {
	edit := Edit{
		Kind:      EditBlock,
		Anchor:    Anchor{Line: 2},
		Payloads:  []string{"x"},
		BlockMode: BlockModeReplace,
		LineNum:   9,
	}
	_, err := ResolveBlockEdits([]Edit{edit}, "fn f() {}\n\nfn g() {\n}\n", "x.rs", UnresolvedThrow, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "line 9: Line 2 is blank") {
		t.Fatalf("err=%v", err)
	}
}

// upstream: crates/edit/tests/hashline_parity.rs: "block_cut_on_leading_go_statement_keeps_following_siblings"
func TestBlockCutOnLeadingGoStatementKeepsFollowingSiblings(t *testing.T) {
	source := "package p\n\nfunc f() error {\n\tif err := step(); err != nil {\n\t\treturn err\n\t}\n\tother()\n\treturn nil\n}\n"
	w := newTestWorkspace(t)
	w.writeFile("x.go", source)
	tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "x.go"), source)
	out, _, err := w.apply(fmt.Sprintf("[x.go#%s]\nCUT 4*\n", tag))
	if err != nil {
		t.Fatalf("CUT 4* failed: %v", err)
	}
	if !strings.Contains(out, "CUT 4* → resolved lines 4-6 (3 lines)") {
		t.Fatalf("out=%s", out)
	}
	want := "package p\n\nfunc f() error {\n\tother()\n\treturn nil\n}\n"
	if got, _ := w.readFile("x.go"); got != want {
		t.Fatalf("x.go =\n%s\nwant:\n%s", got, want)
	}
}

// upstream: crates/edit/tests/hashline_parity.rs: "block_ops_refuse_a_rust_line_that_opens_no_item"
func TestBlockOpsRefuseRustLineThatOpensNoItem(t *testing.T) {
	cases := []struct {
		src    string
		anchor uint32
	}{
		{"impl S {\n    fn f() {}\n    #[allow(dead_code)]\n}\n", 3},
		{"#![allow(dead_code)]\nfn f() {\n    work();\n}\n", 1},
		{"impl S {\n    fn f() {}\n    /// Dangling docs.\n}\n", 3},
		{"//! Crate docs.\nfn f() {\n    work();\n}\n", 1},
		{"// Note.\nfn f() {\n    work();\n}\n", 1},
	}
	for _, tc := range cases {
		edit := Edit{Kind: EditBlock, Anchor: Anchor{Line: tc.anchor}, BlockMode: BlockModeCut, LineNum: 1}
		_, err := ResolveBlockEdits([]Edit{edit}, tc.src, "x.rs", UnresolvedThrow, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "resolved a single-line block") {
			t.Fatalf("src=%q err=%v", tc.src, err)
		}
	}
}

// upstream: crates/edit/tests/hashline_parity.rs: "block_op_on_a_later_multi_line_rust_attribute_is_refused"
func TestBlockOpOnLaterRustAttributesRefusedAndPointsAtFirst(t *testing.T) {
	cases := []struct {
		src      string
		wantSpan string
	}{
		{"/// Docs.\n#[derive(Debug)]\nstruct S {\n    x: u32,\n}\n", "begins at line 1 and ends at line 5"},
		{"#[derive(Debug)]\n#[cfg_attr(\n    feature = \"serde\",\n    derive(Serialize)\n)]\nstruct S {\n    x: u32,\n}\n", "begins at line 1 and ends at line 8"},
		{"#[derive(Debug)]\n/**\n * Docs.\n */\nstruct S {\n    x: u32,\n}\n", "begins at line 1 and ends at line 7"},
	}
	for _, tc := range cases {
		edit := Edit{Kind: EditBlock, Anchor: Anchor{Line: 2}, BlockMode: BlockModeCut, LineNum: 1}
		_, err := ResolveBlockEdits([]Edit{edit}, tc.src, "x.rs", UnresolvedThrow, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "resolved a single-line block") || !strings.Contains(err.Error(), tc.wantSpan) {
			t.Fatalf("err=%v, want %q", err, tc.wantSpan)
		}
	}
}

// upstream: crates/edit/src/modes/hashline/block.rs:127-165: PUT >N*: on closer lowers to PUT >N: with warning
func TestInsertAfterBlockOnCloserLowersWithWarning(t *testing.T) {
	src := "func a() {\n\tx()\n}\nfunc b() {\n\ty()\n}\n"
	edit := Edit{Kind: EditBlock, Anchor: Anchor{Line: 3}, Payloads: []string{"// between"}, BlockMode: BlockModeInsertAfter, LineNum: 1}
	var warnings []string
	lowered, err := ResolveBlockEdits([]Edit{edit}, src, "a.go", UnresolvedThrow, nil, func(w string) {
		warnings = append(warnings, w)
	})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(lowered) != 1 || lowered[0].Cursor.Kind != CursorAfterAnchor || lowered[0].Cursor.Anchor.Line != 3 {
		t.Fatalf("lowered=%+v", lowered)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "anchors on a closing delimiter") {
		t.Fatalf("warnings=%v", warnings)
	}
}
