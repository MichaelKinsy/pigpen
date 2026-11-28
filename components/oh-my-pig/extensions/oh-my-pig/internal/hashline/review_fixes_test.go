package hashline

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Tests covering every finding from OH-MY-PIG-REVIEW-OmpEdit-rewrite.md and integrator decisions:
// (1) Block ops: findings 1-3 exact examples + >=8 adversarial anchors per resolvable language.
// (2) uint32 wrap at 4294967295 (must finish within 1s with "Line N does not exist").
// (3) Seen-line guard: empty displayed set -> sentinel line 0; RecordReadWholeFile vs RecordSnapshot.
// (4) Diff gap row removal + Python/YAML/Markdown enclosing block context rows.
// (5) PathPolicy::address / expandEditPath (URI-like refusal, leading `:`, `file://`, NBSP).
// (6) literalValue trailing commas and hex float refusal.
// (7) Private fallback store (no session ID) refuses anchored edits while reads still work.
// (8) Full replay of all 20 edit calls across all 4 oracle cases from report.md.

// Finding 1, 2, 3: exact review examples asserted on full file contents / refusal.
func TestReviewFindings123ExactExamples(t *testing.T) {
	t.Run("finding1 C multiline signature CUT 1* refuses rather than leaving orphan brace", func(t *testing.T) {
		w := newTestWorkspace(t)
		src := "static int foo(int a,\n    int b)\n{\n    return a + b;\n}\n"
		w.writeFile("a.c", src)
		tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "a.c"), src)
		_, _, err := w.apply(fmt.Sprintf("[a.c#%s]\nCUT 1*\n", tag))
		if err == nil || !strings.Contains(err.Error(), "could not resolve a syntactic block") {
			t.Fatalf("expected block_unresolved refusal, got %v", err)
		}
		if got, _ := w.readFile("a.c"); got != src {
			t.Fatalf("file was modified on refusal:\n%s", got)
		}
	})

	t.Run("finding1 Go multiline if condition CUT 4* refuses rather than leaving c {", func(t *testing.T) {
		w := newTestWorkspace(t)
		src := "package p\n\nfunc f() {\n\tif foo(a,\n\t\tb) &&\n\t\tc {\n\t\tx()\n\t}\n}\n"
		w.writeFile("a.go", src)
		tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "a.go"), src)
		_, _, err := w.apply(fmt.Sprintf("[a.go#%s]\nCUT 4*\n", tag))
		if err == nil || !strings.Contains(err.Error(), "could not resolve a syntactic block") {
			t.Fatalf("expected block_unresolved refusal, got %v", err)
		}
		if got, _ := w.readFile("a.go"); got != src {
			t.Fatalf("file was modified:\n%s", got)
		}
	})

	t.Run("finding1 TS promise.then().catch() CUT 1* refuses rather than leaving dangling .catch", func(t *testing.T) {
		w := newTestWorkspace(t)
		src := "promise.then(() => {\n  work();\n})\n  .catch((e) => {\n    fail(e);\n  });\n"
		w.writeFile("a.ts", src)
		tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "a.ts"), src)
		_, _, err := w.apply(fmt.Sprintf("[a.ts#%s]\nCUT 1*\n", tag))
		if err == nil || !strings.Contains(err.Error(), "could not resolve a syntactic block") {
			t.Fatalf("expected block_unresolved refusal, got %v", err)
		}
		if got, _ := w.readFile("a.ts"); got != src {
			t.Fatalf("file was modified:\n%s", got)
		}
	})

	t.Run("finding1 TS multiline arrow, extends/implements, return+tail, continuation lines, and dropped languages refuse", func(t *testing.T) {
		refuseCases := []struct {
			name   string
			path   string
			src    string
			anchor uint32
		}{
			{"ts multiline arrow", "a.ts", "const f = (\n  a,\n): T =>\n  g(a);\n", 1},
			{"ts class extends implements", "a.ts", "class A extends B<{\n  x: number\n}>\n  implements C {\n}\n", 1},
			{"ts return call plus tail", "a.ts", "return f(\n  x\n) +\n  \"tail\";\n", 1},
			{"ts continuation .then", "a.ts", "promise\n  .then(() => {\n    work();\n  });\n", 2},
			{"ts continuation + foo(", "a.ts", "const x = a\n  + foo(\n    b\n  );\n", 2},
			{"js backslash-continued string", "a.js", "function f() {\n  const s = \"hello \\\n  }\";\n  return s;\n}\n", 1},
			{"dropped tf # comment", "a.tf", "resource \"a\" \"b\" {\n  # }\n  x = 1\n}\n", 1},
			{"dropped sh heredoc", "a.sh", "f() {\n  cat <<EOF\n}\nEOF\n}\n", 1},
			{"dropped sh param expansion", "a.sh", "f() {\n  echo \"${x#}}\"\n}\n", 1},
			{"dropped php heredoc", "a.php", "function f() {\n  $x = <<<EOT\n}\nEOT;\n}\n", 1},
			{"dropped java text block", "a.java", "class A {\n  String s = \"\"\"\n  }\n  \"\"\";\n}\n", 1},
			{"dropped cs Allman throws", "a.cs", "void Foo(int a,\n  int b)\n{\n}\n", 1},
			{"dropped kt triple quote", "a.kt", "fun f() {\n  val s = \"\"\"\n  }\n  \"\"\"\n}\n", 1},
			{"dropped dart triple quote", "a.dart", "void f() {\n  var s = '''\n  }\n  ''';\n}\n", 1},
			{"dropped ps1 # comment", "a.ps1", "function f {\n  # }\n  1\n}\n", 1},
			{"dropped r # comment", "a.r", "f <- function() {\n  # }\n  1\n}\n", 1},
			{"dropped graphql # comment", "a.graphql", "type A {\n  # }\n  id: ID!\n}\n", 1},
			{"dropped nix", "a.nix", "{\n  # }\n  a = 1;\n}\n", 1},
			{"dropped scala", "a.scala", "object A {\n  val s = \"\"\"}\"\"\"\n}\n", 1},
			{"dropped groovy", "a.groovy", "def f() {\n  def s = '''}'''\n}\n", 1},
			{"dropped swift", "a.swift", "func f() {\n  let s = \"\"\"\n  }\n  \"\"\"\n}\n", 1},
		}
		for _, tc := range refuseCases {
			t.Run(tc.name, func(t *testing.T) {
				edit := Edit{Kind: EditBlock, Anchor: Anchor{Line: tc.anchor}, BlockMode: BlockModeCut, LineNum: 1}
				_, err := ResolveBlockEdits([]Edit{edit}, tc.src, tc.path, UnresolvedThrow, nil, nil)
				if err == nil || !strings.Contains(err.Error(), "could not resolve a syntactic block") {
					t.Fatalf("%s: expected block_unresolved refusal, got %v", tc.name, err)
				}
			})
		}
	})

	t.Run("finding2 Rust attribute/doc + where/multiline/const refuses rather than orphaning tail", func(t *testing.T) {
		cases := []struct {
			name string
			src  string
		}{
			{
				"inline fn where",
				"#[inline]\nfn foo<T>(x: T) -> T\nwhere\n    T: Clone,\n{\n    x\n}\n",
			},
			{
				"doc pub fn multiline where",
				"/// Doc.\npub fn foo(\n    a: u32,\n) -> u32\nwhere\n    u32: Copy,\n{\n    a\n}\n",
			},
			{
				"allow const multiline",
				"#[allow(dead_code)]\nconst X: &[u8] =\n    &[1, 2, 3];\n",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				w := newTestWorkspace(t)
				w.writeFile("a.rs", tc.src)
				tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "a.rs"), tc.src)
				_, _, err := w.apply(fmt.Sprintf("[a.rs#%s]\nPUT 1*:\n+fn replacement() {}\n", tag))
				if err == nil || !strings.Contains(err.Error(), "could not resolve a syntactic block") {
					t.Fatalf("%s: expected block_unresolved refusal, got %v", tc.name, err)
				}
				if got, _ := w.readFile("a.rs"); got != tc.src {
					t.Fatalf("%s: file was corrupted:\n%s", tc.name, got)
				}
			})
		}
		// Single-line complete Rust item under attribute STILL resolves 1-2!
		w := newTestWorkspace(t)
		src := "#[allow(dead_code)]\nconst X: u32 = 1;\nconst Y: u32 = 2;\n"
		w.writeFile("ok.rs", src)
		tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "ok.rs"), src)
		if _, _, err := w.apply(fmt.Sprintf("[ok.rs#%s]\nCUT 1*\n", tag)); err != nil {
			t.Fatalf("complete single-line const under attr should resolve 1-2: %v", err)
		}
		if got, _ := w.readFile("ok.rs"); got != "const Y: u32 = 2;\n" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("finding3 Markdown setext headings, HTML comments/blocks, and front-matter", func(t *testing.T) {
		// Real omp (tree-sitter-markdown) keeps setext headings inside the enclosing ATX section
		// and includes trailing blank lines up to the next ATX heading of level <= current.
		w := newTestWorkspace(t)
		src := "# A\ntext\n\nTitle\n=====\nbody\n\n# C\n"
		w.writeFile("doc.md", src)
		tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "doc.md"), src)
		out, _, err := w.apply(fmt.Sprintf("[doc.md#%s]\nCUT 1*\n", tag))
		if err != nil {
			t.Fatalf("CUT 1* failed: %v", err)
		}
		if !strings.Contains(out, "CUT 1* → resolved lines 1-7 (7 lines)") {
			t.Fatalf("expected lines 1-7 (matching real omp), got:\n%s", out)
		}
		if got, _ := w.readFile("doc.md"); got != "# C\n" {
			t.Fatalf("unexpected doc.md after CUT 1*:\n%q", got)
		}

		// HTML comment with # hidden inside refuses rather than mis-resolving 1-2.
		for _, tc := range []struct {
			name string
			src  string
		}{
			{"html comment", "# A\n<!--\n# hidden\n-->\ntext\n# C\n"},
			{"html div block", "# A\n<div>\n# B\n</div>\n"},
			{"yaml front matter", "---\ntitle: x\n---\n# A\nbody\n# B\n"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				edit := Edit{Kind: EditBlock, Anchor: Anchor{Line: 1}, BlockMode: BlockModeCut, LineNum: 1}
				if tc.name == "yaml front matter" {
					edit.Anchor.Line = 4
				}
				_, err := ResolveBlockEdits([]Edit{edit}, tc.src, "doc.md", UnresolvedThrow, nil, nil)
				if err == nil || !strings.Contains(err.Error(), "could not resolve a syntactic block") {
					t.Fatalf("%s: expected refusal, got %v", tc.name, err)
				}
			})
		}
	})
}

// Adversarial block-op matrix: >=8 adversarial anchors for EVERY language still in the resolvable set
// (JS/TS, JSX/TSX, Rust, Go, C/C++/Brace, Python, YAML, Markdown).
// Rule: every anchor either resolves to the exact tree-sitter node span omp would produce
// (derived from crates/ast/src/block.rs:94-197) OR refuses explicitly. NEVER a wrong span.
func TestAdversarialBlockAnchorsPerResolvableLanguage(t *testing.T) {
	type advCase struct {
		name      string
		path      string
		src       string
		anchor    uint32
		wantStart uint32 // 0 = expect refusal (unresolved or single-line)
		wantEnd   uint32
	}

	cases := []advCase{
		// --- 1. JS / TS (10 adversarial cases) ---
		{"js/valid_fn", "a.ts", "function foo(x: number): number {\n  return x + 1;\n}\nconst y = 2;\n", 1, 1, 3},
		{"js/chained_then_catch_refuse", "a.ts", "fetch(url).then((r) => {\n  return r.json();\n})\n  .catch((e) => {\n    log(e);\n  });\n", 1, 0, 0},
		{"js/multiline_sig_refuse", "a.ts", "function foo(\n  a: number,\n  b: number,\n) {\n  return a + b;\n}\n", 1, 0, 0},
		{"js/continuation_dot_refuse", "a.ts", "items\n  .map((x) => {\n    return x;\n  });\n", 2, 0, 0},
		{"js/template_literal_braces", "a.ts", "function f() {\n  const s = `a } b { c`;\n  return s;\n}\n", 1, 1, 4},
		{"js/regex_with_brace", "a.ts", "function f(s: string) {\n  return /foo}bar/.test(s);\n}\n", 1, 1, 3},
		{"js/allman_brace_refuse", "a.ts", "if (cond)\n{\n  work();\n}\n", 1, 0, 0},
		{"js/decorator_above_class_refuse", "a.ts", "@Injectable()\nclass Svc {\n  run() {}\n}\n", 1, 0, 0},
		{"js/allman_else_next_line_refuse", "a.ts", "if (a) {\n  x();\n}\nelse {\n  y();\n}\n", 1, 0, 0},
		{"js/trailing_member_after_close_brace_refuse", "a.ts", "const x = {\n  a: 1,\n}.a;\n", 1, 0, 0},

		// --- 2. JSX / TSX (8 adversarial cases) ---
		{"jsx/valid_component_fn", "a.tsx", "export function App() {\n  return <div>hi</div>;\n}\n", 1, 1, 3},
		{"jsx/opening_tag_refuse", "a.tsx", "function App() {\n  return (\n    <section>\n      <p>hi</p>\n    </section>\n  );\n}\n", 3, 0, 0},
		{"jsx/multiline_props_fn_refuse", "a.tsx", "function App({\n  title,\n}: Props) {\n  return <div />;\n}\n", 1, 0, 0},
		{"jsx/return_paren_refuse", "a.tsx", "function App() {\n  return (\n    <div />\n  );\n}\n", 2, 0, 0},
		{"jsx/continuation_ternary_refuse", "a.tsx", "const x = ok\n  ? (() => {\n    return 1;\n  })()\n  : 0;\n", 2, 0, 0},
		{"jsx/template_with_closer_in_fn", "a.tsx", "function C() {\n  const x = `}`;\n  return x;\n}\n", 1, 1, 4},
		{"jsx/decorator_refuse", "a.tsx", "@observer\nclass View {\n  render() { return null; }\n}\n", 1, 0, 0},
		{"jsx/inline_else_chain", "a.tsx", "if (a) {\n  x();\n} else {\n  y();\n}\n", 1, 1, 5},

		// --- 3. Rust (10 adversarial cases) ---
		{"rs/valid_fn_lifetimes_rawstr", "a.rs", "fn foo<'a>(s: &'a str) -> &'a str {\n    let x = r#\"}\"#;\n    let c = '}';\n    s\n}\n", 1, 1, 5},
		{"rs/valid_first_attr_struct", "a.rs", "#[derive(Debug)]\nstruct S {\n    x: u32,\n}\n", 1, 1, 4},
		{"rs/nested_block_comment_brace", "a.rs", "fn f() {\n    /* outer /* } */ }\n    still comment */\n    work();\n}\n", 1, 1, 5},
		{"rs/where_clause_refuse", "a.rs", "fn foo<T>(x: T) -> T\nwhere\n    T: Clone,\n{\n    x\n}\n", 1, 0, 0},
		{"rs/attr_plus_where_refuse", "a.rs", "#[inline]\nfn foo<T>(x: T) -> T\nwhere\n    T: Clone,\n{\n    x\n}\n", 1, 0, 0},
		{"rs/attr_multiline_const_refuse", "a.rs", "#[allow(dead_code)]\nconst X: &[u8] =\n    &[1, 2];\n", 1, 0, 0},
		{"rs/multiline_sig_refuse", "a.rs", "pub fn foo(\n    a: u32,\n) -> u32 {\n    a\n}\n", 1, 0, 0},
		{"rs/chained_method_after_match_refuse", "a.rs", "match x {\n    Some(v) => v,\n    None => 0,\n}\n    .to_string();\n", 1, 0, 0},
		{"rs/allman_brace_refuse", "a.rs", "fn foo()\n{\n    1\n}\n", 1, 0, 0},
		{"rs/second_attr_refuse", "a.rs", "#[derive(Debug)]\n#[allow(dead_code)]\nstruct S {\n    x: u32,\n}\n", 2, 0, 0},

		// --- 4. Go (9 adversarial cases) ---
		{"go/valid_func_raw_string", "a.go", "func f() string {\n\ts := `}`\n\treturn s\n}\n", 1, 1, 4},
		{"go/valid_if_else", "a.go", "if ok {\n\ta()\n} else {\n\tb()\n}\n", 1, 1, 5},
		{"go/multiline_if_cond_refuse", "a.go", "if foo(a,\n\tb) &&\n\tc {\n\tx()\n}\n", 1, 0, 0},
		{"go/multiline_sig_refuse", "a.go", "func foo(\n\ta int,\n\tb int,\n) int {\n\treturn a + b\n}\n", 1, 0, 0},
		{"go/chained_call_after_lit_refuse", "a.go", "Builder{\n\tx: 1,\n}\n\t.Build()\n", 1, 0, 0},
		{"go/continuation_dot_refuse", "a.go", "x\n\t.Then(func() {\n\t\ty()\n\t})\n", 2, 0, 0},
		{"go/comment_with_brace", "a.go", "func f() {\n\t// }\n\t/* } */\n\tx()\n}\n", 1, 1, 5},
		{"go/switch_case_colon_single_line_refuse", "a.go", "switch x {\ncase 1:\n\twork()\n}\n", 2, 0, 0},
		{"go/rune_literal_brace", "a.go", "func f() rune {\n\treturn '}'\n}\n", 1, 1, 3},

		// --- 5. C / C++ removed from resolvable set (pass 3 W8): every anchor refuses ---
		{"c/knr_func_refused_w8", "a.c", "int add(int a, int b) {\n    return a + b;\n}\n", 1, 0, 0},
		{"c/allman_brace_refuse", "a.c", "int add(int a, int b)\n{\n    return a + b;\n}\n", 1, 0, 0},
		{"c/multiline_sig_allman_refuse", "a.c", "static int foo(int a,\n    int b)\n{\n    return a + b;\n}\n", 1, 0, 0},
		{"c/struct_trailing_typedef_name_refuse", "a.c", "typedef struct {\n    int x;\n} Point;\n", 1, 0, 0},
		{"c/preprocessor_refuse", "a.c", "#if 1\nint x = 1;\n#endif\n", 1, 0, 0},
		{"c/backslash_string_refuse", "a.c", "void f() {\n    const char *s = \"a \\\n}\";\n}\n", 1, 0, 0},
		{"cpp/raw_string_refuse", "a.cpp", "void f() {\n    auto s = R\"(}\n)\";\n}\n", 1, 0, 0},
		{"c/comment_with_brace_refused_w8", "a.c", "void f() {\n    /* } */\n    // }\n    work();\n}\n", 1, 0, 0},
		{"c/do_while_next_line_refuse", "a.c", "do {\n    step();\n}\nwhile (cond);\n", 1, 0, 0},
		{"c/header_refused_w8", "a.h", "struct S {\n    int x;\n};\n", 1, 0, 0},
		// --- 6. Python (9 adversarial cases) ---
		{"py/valid_def_multiline_sig", "a.py", "def foo(\n    a: int,\n    b: int,\n) -> int:\n    return a + b\n", 1, 1, 5},
		{"py/valid_decorator_def", "a.py", "@cache\ndef foo():\n    return 1\n", 1, 1, 3},
		{"py/second_decorator_refuse", "a.py", "@a\n@b\ndef foo():\n    return 1\n", 2, 0, 0},
		{"py/docstring_col0_dedented_comment", "a.py", "def f():\n    \"\"\"line1\ncol0\n\"\"\"\n# note\n    return 1\n", 1, 1, 6},
		{"py/if_elif_else", "a.py", "if a:\n    x()\nelif b:\n    y()\nelse:\n    z()\n", 1, 1, 6},
		{"py/backslash_header_refuse", "a.py", "if a and \\\n   b:\n    x()\n", 1, 0, 0},
		{"py/multiline_call_chain_refuse", "a.py", "res = foo(\n    a,\n).bar()\n", 1, 0, 0},
		{"py/inside_triple_quote_refuse", "a.py", "s = \"\"\"\ndef fake():\n    pass\n\"\"\"\n", 2, 0, 0},
		{"py/single_line_suite_refuse", "a.py", "def f(): pass\n", 1, 0, 0},

		// --- 7. YAML (8 adversarial cases) ---
		{"yaml/nested_mapping", "a.yaml", "server:\n  host: localhost\n  port: 8080\nclient:\n  x: 1\n", 1, 1, 3},
		{"yaml/same_indent_sequence", "a.yaml", "steps:\n- run: a\n- run: b\nnext: 1\n", 1, 1, 3},
		{"yaml/block_scalar_pipe", "a.yaml", "script: |\n  echo 1\n  echo 2\nnext: 1\n", 1, 1, 3},
		{"yaml/multiline_plain_scalar_refuse", "a.yaml", "desc: first line\n  second line\nnext: 1\n", 1, 0, 0},
		{"yaml/multiline_flow_seq_refuse", "a.yaml", "items: [\n  # ]\n  1,\n]\n", 1, 0, 0},
		{"yaml/single_line_scalar_refuse", "a.yaml", "a: 1\nb: 2\n", 1, 0, 0},
		{"yaml/doc_separator_refuse", "a.yaml", "---\na: 1\n", 1, 0, 0},
		{"yaml/sequence_item_block", "a.yaml", "jobs:\n  - name: ci\n    runs: linux\n  - name: deploy\n", 2, 2, 3},

		// --- 8. Markdown (8 adversarial cases, verified against real omp) ---
		{"md/atx_with_subheadings", "a.md", "## Top\n\nIntro\n\n### Child\n\nBody\n\n## Sibling\n", 1, 1, 8},
		{"md/fenced_heading_ignored", "a.md", "# Real\n```md\n# NotAHeading\n```\nAfter\n# Next\n", 1, 1, 5},
		{"md/inside_fence_refuse", "a.md", "```md\n# Inside\ntext\n```\n", 2, 0, 0},
		{"md/setext_h1_stays_inside_section", "a.md", "# A\ntext\n\nTitle\n=====\nbody\n", 1, 1, 6},
		{"md/setext_h2_under_h1_included", "a.md", "# A\ntext\n\nSub\n---\nbody\n# B\n", 1, 1, 6},
		{"md/html_comment_refuse", "a.md", "# A\n<!--\n# hidden\n-->\ntext\n# B\n", 1, 0, 0},
		{"md/html_tag_block_refuse", "a.md", "# A\n<div>\n# B\n</div>\n", 1, 0, 0},
		{"md/front_matter_refuse", "a.md", "---\ntitle: hi\n---\n# A\nbody\n", 4, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edit := Edit{Kind: EditBlock, Anchor: Anchor{Line: tc.anchor}, BlockMode: BlockModeCut, LineNum: 1}
			var got []BlockResolution
			_, err := ResolveBlockEdits([]Edit{edit}, tc.src, tc.path, UnresolvedThrow, func(r BlockResolution) {
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
					t.Fatalf("expected refusal, but resolved %+v (would corrupt code!)", got)
				}
			}
		})
	}
}

// Pass 3 review findings W1-W8 and the three cheap wins (leading `while`, block-comment
// skipping in next-line check, CSS selector characters not treated as operators).
func TestPass3BlockSpanFindingsAndCheapWins(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		src       string
		anchor    uint32
		wantStart uint32 // 0 = expect refusal
		wantEnd   uint32
	}{
		// W1: JS/TS statement ending in `}` without `;` followed by `;`, `(`, `[`, or backtick.
		{"W1/js_asi_leading_semi", "a.ts", "const v = {\n  a: 1,\n}\n;[1].forEach(f)\n", 1, 0, 0},
		{"W1/js_asi_leading_paren", "a.ts", "const w = {\n  a: 1,\n}\n(function () {})()\n", 1, 0, 0},
		{"W1/js_asi_leading_backtick", "a.ts", "const t2 = {\n  a: 1,\n}\n`tpl`\n", 1, 0, 0},
		{"W1/js_asi_leading_bracket", "a.ts", "const z2 = {\n  a: 1,\n}\n[1, 2].map(f)\n", 1, 0, 0},
		// W1 non-regression: with semicolon on closing brace, next-line `[` is NOT a continuation.
		{"W1/js_with_semi_resolves", "a.ts", "const v = {\n  a: 1,\n};\n[1].forEach(f)\n", 1, 1, 3},
		// W2: Bare `{` anchor must NOT swallow `} else { ... }`.
		{"W2/rs_bare_brace_else_refused", "a.rs", "fn f() {\n    let w = if cond\n        && ok\n    {\n        a()\n    } else {\n        b()\n    };\n}\n", 4, 0, 0},
		// W3: Rust items inside macro invocation or macro_rules! body refused.
		{"W3/rs_macro_rules_fn_refused", "a.rs", "macro_rules! m {\n    () => {\n        fn f() -> u16 {\n            1\n        }\n    };\n}\n", 3, 0, 0},
		{"W3/rs_macro_call_enum_refused", "a.rs", "code_enum! {\n    /// doc\n    pub enum Ok_ {\n        A,\n    }\n}\n", 3, 0, 0},
		{"W3/rs_macro_call_doc_refused", "a.rs", "code_enum! {\n    /// doc\n    pub enum Ok_ {\n        A,\n    }\n}\n", 2, 0, 0},
		// W4: Python later decorator after multi-line decorator refused (single-line).
		{"W4/py_later_decorator_after_multiline", "a.py", "@a.b(c=[\n    1,\n])\n@d\nclass K(B,\n        metaclass=M):\n    pass\n", 4, 0, 0},
		{"W4/py_first_multiline_decorator_resolves", "a.py", "@a.b(c=[\n    1,\n])\n@d\nclass K(B,\n        metaclass=M):\n    pass\n", 1, 1, 7},
		// W5: Go closed block then `; for {` on anchor line refused.
		{"W5/go_closed_block_then_for_refused", "a.go", "package p\n\nfunc f() {\n\tx := 1\n\tfor i := 0; i < 3; i++ { b() }; for {\n\t\tbreak\n\t}\n\t_ = x\n}\n", 5, 0, 0},
		// W6: YAML flow-mapping sequence item does NOT absorb trailing comments.
		{"W6/yaml_flow_mapping_item_no_trailing_comments", "a.yml", "include:\n  - {\n      a: 1,\n    }\n  # next item comment\n  - {\n      b: 2,\n    }\n", 2, 2, 4},
		// W7: CSS anchor starting with `,` or combinator refused.
		{"W7/css_leading_comma_refused", "a.css", "g\n,h {\n  i: j;\n}\n\nbody {\n  margin: 0;\n}\n", 2, 0, 0},
		{"W7/css_leading_combinator_refused", "a.css", ".a\n> .b {\n  color: red;\n}\n", 2, 0, 0},
		// W8: C/C++ removed; templated YAML and CSS refused; CSS custom property block refused.
		{"W8/c_refused", "a.c", "int f(void) {\n    return 1;\n}\n", 1, 0, 0},
		{"W8/cpp_refused", "a.cpp", "int f() {\n    return 1;\n}\n", 1, 0, 0},
		{"W8/yaml_go_template_refused", "job.yaml", "metadata:\n  name: {{ .Name }}\n  labels:\n    a: b\n", 1, 0, 0},
		{"W8/yaml_jinja_template_refused", "job.yaml", "items:\n{% for x in xs %}\n  - a: 1\n{% endfor %}\n", 1, 0, 0},
		{"W8/css_template_refused", "t.css", ":root {\n  {{THEME_VARS}}\n}\n", 1, 0, 0},
		{"W8/css_custom_prop_block_refused", "t.css", ":root {\n  --a: { b };\n}\n", 1, 0, 0},
		// Cheap win 1: leading `while` is not a continuation token.
		{"win1/rs_while_loop_resolves", "a.rs", "fn f(mut n: i32) {\n    while n > 0 {\n        n -= 1;\n    }\n}\n", 2, 2, 4},
		{"win1/ts_while_loop_resolves", "a.ts", "function f(n: number) {\n  while (n > 0) {\n    n--;\n  }\n}\n", 2, 2, 4},
		// Cheap win 2: skip whole `/** ... */` block comment in next-line check.
		{"win2/ts_block_followed_by_jsdoc_resolves", "a.ts", "function a() {\n  return 1;\n}\n\n/**\n * Doc for b.\n */\nfunction b() {\n  return 2;\n}\n", 1, 1, 3},
		// Pass 4 C1: CSS/SCSS/SASS/LESS removed from resolvable set -> all anchors refuse.
		{"C1/css_plain_rule_refused", "a.css", "body {\n  margin: 0;\n}\n", 1, 0, 0},
		{"C1/css_content_open_brace_refused", "a.css", ".q::before {\n  content: \"{\";\n}\n.after {\n  color: red;\n}\n", 4, 0, 0},
		{"C1/css_container_refused", "a.css", "@container sidebar (min-width: 400px) {\n  .card { padding: 8px; }\n}\n", 1, 0, 0},
		{"C1/scss_mixin_refused", "a.scss", "@mixin box($p) {\n  padding: $p;\n}\n", 1, 0, 0},
		{"C1/sass_refused", "a.sass", ".btn\n  color: red\n", 1, 0, 0},
		{"C1/less_refused", "a.less", ".btn {\n  color: red;\n}\n", 1, 0, 0},
		// Pass 4 Y1: YAML block reaching EOF absorbs trailing blank lines (not phantom row);
		// blocks followed by `---` or `...` document markers refuse conservatively.
		{"Y1/yaml_eof_blank_line_lf", "lint.yml", "name: lint\n\njobs:\n  linter:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - name: run-linter\n        uses: example/lint-action@v1\n\n", 3, 3, 10},
		{"Y1/yaml_eof_blank_line_nested_seq_item", "lint.yml", "name: lint\n\njobs:\n  linter:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n      - name: run-linter\n        uses: example/lint-action@v1\n\n", 8, 8, 10},
		{"Y1/yaml_eof_blank_line_crlf", "svc.yaml", "type: example.api.Service\r\n\r\nhttp:\r\n  rules:\r\n    - selector: example.Echo\r\n      post: /v1/echo\r\n    - selector: example.EchoDelete\r\n      delete: /v1/echo\r\n\r\n", 3, 3, 9},
		{"Y1/yaml_eof_blank_line_block_scalar", "a.yaml", "a: 1\nscript: |\n  echo hi\n  echo bye\n\n", 2, 2, 5},
		{"Y1/yaml_doc_marker_dash_refused", "a.yaml", "first:\n  a: 1\n\n---\nsecond:\n  b: 2\n", 1, 0, 0},
		{"Y1/yaml_doc_marker_dots_refused", "a.yaml", "first:\n  a: 1\n...\n", 1, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(NormalizeToLF(tc.src), "\n")
			span := ResolveBlockAt(tc.path, lines, tc.anchor)
			resolved := span != nil && span.End > span.Start
			if tc.wantStart == 0 {
				if resolved {
					t.Fatalf("expected refusal, got %d-%d", span.Start, span.End)
				}
				return
			}
			if !resolved || span.Start != tc.wantStart || span.End != tc.wantEnd {
				t.Fatalf("got %+v, want %d-%d", span, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

// Decision (2): uint32 wrap at 4294967295 MUST terminate immediately with "Line 4294967295 does not exist".
func TestUint32MaxLineDoesNotHangOrOOM(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		w := newTestWorkspace(t)
		w.writeFile("a.txt", "one\ntwo\n")
		tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "a.txt"), "one\ntwo\n")

		checks := []struct {
			op   string
			want string
		}{
			{"CUT 4294967295", "is out of range"},
			{"PUT 4294967295.=4294967295:\n+x", "Line 4294967295 does not exist"},
		}
		for _, tc := range checks {
			// Verify ParsePatch terminates cleanly.
			if _, pErr := ParsePatch(tc.op); pErr != nil {
				done <- fmt.Errorf("ParsePatch(%q) unexpected error: %v", tc.op, pErr)
				return
			}
			// Verify StageInput returns out-of-range error without hanging or OOM.
			_, _, err := w.apply(fmt.Sprintf("[a.txt#%s]\n%s\n", tag, tc.op))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				done <- fmt.Errorf("%q: expected %q, got %v", tc.op, tc.want, err)
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("uint32 MaxUint32 line loop hung past 1s")
	}
}

// Decision (3): empty displayed set records sentinel line 0 (NO_FULL_LINE); whole-file read records 1..N.
func TestSeenLinesAllColumnCutAndWholeFileRead(t *testing.T) {
	// 1. Single 2000-char line where every displayed row was column-cut -> seenLines empty -> sentinel 0!
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	wide := strings.Repeat("a", 2000) + "\n"
	w.writeFile("one.txt", wide)
	tag := w.store.RecordReadLines(filepath.Join(w.dir, "one.txt"), wide, nil)
	if _, _, err := w.apply(fmt.Sprintf("[one.txt#%s]\nPUT 1.=1:\n+short\n", tag)); err == nil || !strings.Contains(err.Error(), "one.txt:1:raw") {
		t.Fatalf("expected wide_unseen_lines refusal for all-cut file, got %v", err)
	}
	// Also via legacy RecordSnapshotWithSeenLines with empty slice.
	w2 := newTestWorkspace(t)
	w2.opts.EnforceSeenLines = true
	twoWide := strings.Repeat("a", 1500) + "\n" + strings.Repeat("b", 1500) + "\n"
	w2.writeFile("two.txt", twoWide)
	tag2 := w2.store.RecordSnapshotWithSeenLines(filepath.Join(w2.dir, "two.txt"), twoWide, []int{})
	if _, _, err := w2.apply(fmt.Sprintf("[two.txt#%s]\nPUT 2.=2:\n+short\n", tag2)); err == nil || !strings.Contains(err.Error(), "two.txt:2:raw") {
		t.Fatalf("expected wide_unseen_lines refusal for 2-line all-cut file, got %v", err)
	}

	// 2. RecordReadWholeFile records 1..N (NOT empty authored set):
	//    - After PUT <1: restricts version to reply rows, a whole-file read unions 1..N so PUT >6: succeeds!
	//    - On a fully-read 3-line file, PUT 9.=9: hits the seen-line guard first (oracle edit-failures call 4)!
	w3 := newTestWorkspace(t)
	w3.opts.EnforceSeenLines = true
	w3.writeFile("e.txt", "one\ntwo\nthree\n")
	tagE := w3.store.RecordReadWholeFile(filepath.Join(w3.dir, "e.txt"), "one\ntwo\nthree\n")
	_, _, err := w3.apply(fmt.Sprintf("[e.txt#%s]\nPUT 9.=9:\n+x\n", tagE))
	if err == nil || !strings.Contains(err.Error(), "Re-read them in full first with a ranged read like `e.txt:9`") {
		t.Fatalf("expected unseen-lines reply for PUT 9.=9: on whole-file read, got %v", err)
	}
}

// Decision (5): No gap row between distant hunks in numbered diff; Python enclosing-block rows match oracle mod.py.
func TestDiffNoGapRowAndPythonEnclosingBlockContext(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	oldText := b.String()
	newText := strings.Replace(oldText, "line 2\n", "LINE 2\n", 1)
	newText = strings.Replace(newText, "line 18\n", "LINE 18\n", 1)

	diffOut := GenerateDiffString(oldText, newText, 2, false)
	if strings.Contains(diffOut.Diff, "\n\n") {
		t.Fatalf("numbered diff must not contain blank gap row between distant hunks:\n%s", diffOut.Diff)
	}
	if !strings.Contains(diffOut.Diff, " 4|line 4\n 16|line 16") {
		t.Fatalf("expected adjacent context rows 4 and 16 without gap:\n%s", diffOut.Diff)
	}
	preview := BuildCompactDiffPreview(diffOut.Diff, ResponseAddedRunContextLines).Preview
	if strings.Contains(preview, "4:line 4\n…\n16:line 16") {
		t.Fatalf("compact preview must not emit … between distant hunks:\n%s", preview)
	}
	if !strings.Contains(preview, "4:line 4\n16:line 16") {
		t.Fatalf("expected 4:line 4\\n16:line 16 in preview:\n%s", preview)
	}
}

// Decision (5): PathPolicy::address / expandEditPath, specialFileKind, and literalValue.
func TestPathPolicyAndLiteralValue(t *testing.T) {
	dir := t.TempDir()
	src := &DiskFileSource{
		WorkDir:         dir,
		HomeDir:         dir,
		URLSchemes:      []string{"local", "skill"},
		URLAliasSchemes: []string{"local"},
	}

	// Unknown URI-like target refused for header and MV destination.
	if _, err := src.Resolve("bogus://a.md"); err == nil ||
		err.Error() != "Unknown URI-like edit target 'bogus://a.md'. Prefix the path with './' to edit it as a filesystem path." {
		t.Fatalf("bogus:// err = %v", err)
	}
	// Non-alias registered scheme with single slash refused with suggestion.
	if _, err := src.Resolve("skill:/foo"); err == nil ||
		err.Error() != "Unknown URI-like edit target 'skill:/foo'. Did you mean 'skill://foo'? Prefix the path with './' to edit it as a filesystem path." {
		t.Fatalf("skill:/foo err = %v", err)
	}
	// Leading `:`, `file://`, and NBSP folding.
	r1, err := src.Resolve(":./sub/a.txt")
	if err != nil || r1.Absolute != filepath.Join(dir, "sub", "a.txt") {
		t.Fatalf("leading : resolve = (%+v, %v)", r1, err)
	}
	r2, err := src.Resolve("file://" + filepath.ToSlash(filepath.Join(dir, "a%20b.txt")))
	if err != nil || !strings.HasSuffix(filepath.ToSlash(r2.Absolute), "/a b.txt") {
		t.Fatalf("file:// resolve = (%+v, %v)", r2, err)
	}
	r3, err := src.Resolve("my\u00a0file.txt")
	if err != nil || filepath.Base(r3.Absolute) != "my file.txt" {
		t.Fatalf("NBSP resolve = (%+v, %v)", r3, err)
	}
	// MV bogus://foo.txt refuses instead of creating bogus:/foo.txt directory!
	w := newTestWorkspace(t)
	w.writeFile("q2.txt", "hello\n")
	tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "q2.txt"), "hello\n")
	if _, _, mErr := w.apply(fmt.Sprintf("[q2.txt#%s]\nMV bogus://foo.txt\n", tag)); mErr == nil ||
		!strings.Contains(mErr.Error(), "Unknown URI-like edit target 'bogus://foo.txt'") {
		t.Fatalf("MV bogus:// err = %v", mErr)
	}

	// Special file kind: directory is NOT called "it is a directory" (matches files.rs:112-133 -> IO error on read),
	// and FIFO reports "FIFO" on Unix.
	if runtime.GOOS != "windows" {
		if got := specialFileKind(os.ModeNamedPipe); got != "FIFO" {
			t.Fatalf("FIFO kind = %q", got)
		}
		if got := specialFileKind(os.ModeSocket); got != "socket" {
			t.Fatalf("socket kind = %q", got)
		}
		if got := specialFileKind(os.ModeCharDevice | os.ModeDevice); got != "character device" {
			t.Fatalf("char device kind = %q", got)
		}
		if got := specialFileKind(os.ModeDevice); got != "block device" {
			t.Fatalf("block device kind = %q", got)
		}
	}

	// literalValue: multiple trailing commas trimmed; hex floats and underscores rejected (matches Rust f64::parse).
	if !literalValue("123,,") {
		t.Fatal("expected 123,, to be literal")
	}
	if literalValue("0x1p4") {
		t.Fatal("expected hex float 0x1p4 NOT to be literal")
	}
	if literalValue("1_000") {
		t.Fatal("expected underscored 1_000 NOT to be literal")
	}
}

// Decision (8): Context with no session ID returns private fallback store where reads work
// but anchored edits refuse with a clear error instead of silently disabling seen-line guard.
func TestPrivateFallbackStoreRefusesAnchoredEdits(t *testing.T) {
	priv := ForSession(sdk.Context{})
	if !priv.IsPrivateFallback() {
		t.Fatal("ForSession(zero) must be marked IsPrivateFallback")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Reads still work on the private store.
	tag := priv.RecordReadWholeFile(p, "one\ntwo\n")
	if tag == "" {
		t.Fatal("expected read to return a valid tag")
	}

	// Anchored edits refuse clearly.
	files := &DiskFileSource{WorkDir: dir}
	_, err := StageInput(
		fmt.Sprintf("[a.txt#%s]\nPUT 1.=1:\n+ONE\n", tag),
		files,
		priv,
		StageOptions{EnforceSeenLines: true, MaxColumns: 512},
	)
	if err == nil || !strings.Contains(err.Error(), "session ID is unavailable") {
		t.Fatalf("expected session-ID-unavailable refusal on private store, got %v", err)
	}

	// Unanchored head/tail insert (`PUT >$:`) is not anchor-scoped and still works if tagged.
	if _, err := StageInput(
		fmt.Sprintf("[a.txt#%s]\nPUT >$:\n+three\n", tag),
		files,
		priv,
		StageOptions{EnforceSeenLines: true, MaxColumns: 512},
	); err != nil {
		t.Fatalf("unanchored PUT >$: should still succeed: %v", err)
	}
}

// Decision (6): Full byte-for-byte replay of ALL 20 edit calls across all 4 edit oracle cases
// from /tmp/omp-proof2/reference/oracle/report.md and piglets/oh-my-pig/proof/cases/edit.json.
func TestOracleAll20EditCallsReplay(t *testing.T) {
	// Helper that simulates `read` by recording displayed lines via RecordReadWholeFile / RecordReadLines.
	readWhole := func(w *testWorkspace, rel string) string {
		raw, ok := w.readFile(rel)
		if !ok {
			t.Fatalf("readWhole %s: missing", rel)
		}
		_, body := StripBOM(raw)
		norm := NormalizeToLF(body)
		return w.store.RecordReadWholeFile(filepath.Join(w.dir, rel), norm)
	}

	t.Run("edit-hashline-ops (5 edit calls)", func(t *testing.T) {
		w := newTestWorkspace(t)
		w.opts.EnforceSeenLines = true
		w.writeFile("target.txt", "alpha\nbeta\ngamma\ndelta\nepsilon\n")

		// Call 0: read target.txt -> #FEEF
		tag0 := readWhole(w, "target.txt")
		if tag0 != "FEEF" {
			t.Fatalf("call 0 tag = %s, want FEEF", tag0)
		}

		// Call 1: edit PUT 2.=2: +BETA
		out1, _, err := w.apply("[target.txt#FEEF]\nPUT 2.=2:\n+BETA")
		if err != nil {
			t.Fatalf("call 1: %v", err)
		}
		want1 := "[target.txt#6E81]\n1:alpha\n2:BETA\n3:gamma\n4:delta"
		if out1 != want1 {
			t.Fatalf("call 1 mismatch:\ngot:\n%s\nwant:\n%s", out1, want1)
		}

		// Call 2: edit PUT <1: +header
		out2, _, err := w.apply("[target.txt#6E81]\nPUT <1:\n+header")
		if err != nil {
			t.Fatalf("call 2: %v", err)
		}
		want2 := "[target.txt#77F5]\n1:header\n2:alpha\n3:BETA"
		if out2 != want2 {
			t.Fatalf("call 2 mismatch:\ngot:\n%s\nwant:\n%s", out2, want2)
		}

		// Call 3: read target.txt -> #77F5 (whole-file read records 1..6!)
		tag3 := readWhole(w, "target.txt")
		if tag3 != "77F5" {
			t.Fatalf("call 3 tag = %s, want 77F5", tag3)
		}

		// Call 4: edit PUT >6: +footer (failed before RecordReadWholeFile fix!)
		out4, _, err := w.apply("[target.txt#77F5]\nPUT >6:\n+footer")
		if err != nil {
			t.Fatalf("call 4: %v", err)
		}
		want4 := "[target.txt#1F76]\n5:delta\n6:epsilon\n7:footer"
		if out4 != want4 {
			t.Fatalf("call 4 mismatch:\ngot:\n%s\nwant:\n%s", out4, want4)
		}

		// Call 5: read target.txt -> #1F76
		tag5 := readWhole(w, "target.txt")
		if tag5 != "1F76" {
			t.Fatalf("call 5 tag = %s, want 1F76", tag5)
		}

		// Call 6: CUT 3.=4 @moved + PUT >7 @moved
		out6, _, err := w.apply("[target.txt#1F76]\nCUT 3.=4 @moved\nPUT >7 @moved")
		if err != nil {
			t.Fatalf("call 6: %v", err)
		}
		want6 := "[target.txt#1D7F]\n1:header\n2:alpha\n3:delta\n4:epsilon\n5:footer\n6:BETA\n7:gamma"
		if out6 != want6 {
			t.Fatalf("call 6 mismatch:\ngot:\n%s\nwant:\n%s", out6, want6)
		}

		// Call 7: read target.txt -> #1D7F
		tag7 := readWhole(w, "target.txt")
		if tag7 != "1D7F" {
			t.Fatalf("call 7 tag = %s, want 1D7F", tag7)
		}

		// Call 8: MV target-renamed.txt
		out8, _, err := w.apply("[target.txt#1D7F]\nMV target-renamed.txt")
		if err != nil {
			t.Fatalf("call 8: %v", err)
		}
		want8 := "[target-renamed.txt#1D7F]\nMoved to target-renamed.txt"
		if out8 != want8 {
			t.Fatalf("call 8 mismatch:\ngot:\n%s\nwant:\n%s", out8, want8)
		}
	})

	t.Run("edit-block-and-rem (2 edit calls)", func(t *testing.T) {
		w := newTestWorkspace(t)
		w.opts.EnforceSeenLines = true
		w.writeFile("mod.py", "def greet(name):\n    return name\n\ndef bye():\n    return \"bye\"\n")
		w.writeFile("junk.txt", "temp\n")

		// Call 0: read mod.py -> #AE77
		tag0 := readWhole(w, "mod.py")
		if tag0 != "AE77" {
			t.Fatalf("call 0 tag = %s, want AE77", tag0)
		}

		// Call 1: PUT 1*: on mod.py — MUST match oracle verbatim including line 5 `    return "bye"`!
		out1, _, err := w.apply("[mod.py#AE77]\nPUT 1*:\n+def greet(name):\n+    return f\"hello {name}\"")
		if err != nil {
			t.Fatalf("call 1: %v", err)
		}
		want1 := "[mod.py#CD28]\nPUT 1*: → resolved lines 1-2 (2 lines)\n1:def greet(name):\n2:    return f\"hello {name}\"\n3:\n4:def bye():\n5:    return \"bye\""
		if out1 != want1 {
			t.Fatalf("call 1 mismatch:\ngot:\n%s\nwant:\n%s", out1, want1)
		}

		// Call 2: read junk.txt -> #B839
		tag2 := readWhole(w, "junk.txt")
		if tag2 != "B839" {
			t.Fatalf("call 2 tag = %s, want B839", tag2)
		}

		// Call 3: REM
		out3, _, err := w.apply("[junk.txt#B839]\nREM")
		if err != nil {
			t.Fatalf("call 3: %v", err)
		}
		if out3 != "Deleted junk.txt" {
			t.Fatalf("call 3 = %q", out3)
		}
	})

	t.Run("edit-errors-stale-tag-and-unseen (2 edit calls)", func(t *testing.T) {
		w := newTestWorkspace(t)
		w.opts.EnforceSeenLines = true
		big := "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"
		w.writeFile("big.txt", big)
		// Call 0: read big.txt:1-2 displays lines 1-5 (1 before + 3 after).
		tag0 := w.store.RecordRead(filepath.Join(w.dir, "big.txt"), big, []uint32{1, 2, 3, 4, 5})
		if tag0 != "6E7D" {
			t.Fatalf("tag0 = %s, want 6E7D", tag0)
		}

		// Call 1: bad tag #0000
		_, _, err1 := w.apply("[big.txt#0000]\nPUT 1.=1:\n+bad-tag")
		want1 := "Edit rejected for big.txt: hash #0000 is not from this session.\nThe current file hashes to #6E7D. If the rows below are the ones you mean, resend the edit under #6E7D; otherwise re-read the file with `read` and copy its [path#tag] header verbatim.\n\n*1:l1\n 2:l2\n 3:l3"
		if err1 == nil || err1.Error() != want1 {
			t.Fatalf("call 1 mismatch:\ngot:\n%v\nwant:\n%s", err1, want1)
		}

		// Call 2: unseen line 9
		_, _, err2 := w.apply("[big.txt#6E7D]\nPUT 9.=9:\n+unseen-line")
		want2 := "This edit anchors to lines 9 of big.txt that [big.txt#6E7D] never displayed (it showed a partial range, a search hit, or a folded summary). Actual file content at those lines:\n  9:l9\nVerify the content matches what you intend to touch, then re-issue the edit with the same [path#tag] header — a straight retry now succeeds without a re-read. If the content does NOT match, fix your line numbers."
		if err2 == nil || err2.Error() != want2 {
			t.Fatalf("call 2 mismatch:\ngot:\n%v\nwant:\n%s", err2, want2)
		}
	})

	t.Run("edit-failures (11 edit calls)", func(t *testing.T) {
		w := newTestWorkspace(t)
		w.opts.EnforceSeenLines = true
		w.writeFile("e.txt", "one\ntwo\nthree\n")
		w.writeFile("f.txt", "keep\n")

		// Calls 0-1: whole-file reads
		if tag := readWhole(w, "e.txt"); tag != "2543" {
			t.Fatalf("e.txt tag = %s", tag)
		}
		if tag := readWhole(w, "f.txt"); tag != "CECD" {
			t.Fatalf("f.txt tag = %s", tag)
		}

		// Call 2: missing header
		_, _, err2 := w.apply("PUT 1.=1:\n+x")
		want2 := `input must begin with "[PATH#HASH]" on the first non-blank line for anchored edits; got: "PUT 1.=1:". Example: "[src/foo.ts#1A2B]" then edit ops.`
		if err2 == nil || err2.Error() != want2 {
			t.Fatalf("call 2:\ngot:  %v\nwant: %s", err2, want2)
		}

		// Call 3: missing file
		_, _, err3 := w.apply("[nope.txt#0000]\nPUT 1.=1:\n+x")
		want3 := "File not found: nope.txt. Use the write tool to create new files."
		if err3 == nil || err3.Error() != want3 {
			t.Fatalf("call 3:\ngot:  %v\nwant: %s", err3, want3)
		}

		// Call 4: out-of-range line 9 on whole-file read -> unseen-lines error (exact oracle wording!)
		_, _, err4 := w.apply("[e.txt#2543]\nPUT 9.=9:\n+x")
		want4 := "This edit anchors to lines 9 of e.txt that [e.txt#2543] never displayed (it showed a partial range, a search hit, or a folded summary). Re-read them in full first with a ranged read like `e.txt:9` — it skips summarization and mints a fresh tag (a plain re-read just re-folds them) — then re-issue the edit."
		if err4 == nil || err4.Error() != want4 {
			t.Fatalf("call 4:\ngot:  %v\nwant: %s", err4, want4)
		}

		// Call 5: empty PUT body -> delete + warning
		out5, _, err5 := w.apply("[e.txt#2543]\nPUT 1.=1:")
		want5 := "[e.txt#6DBE]\n1:two\n2:three\n\nWarnings:\nInterpreted an empty `PUT` body as deletion. Use `CUT N.=M` or `CUT N*` for bodyless deletes."
		if err5 != nil || out5 != want5 {
			t.Fatalf("call 5:\ngot:  %s (err=%v)\nwant: %s", out5, err5, want5)
		}

		// Call 6: unified-diff -old ignored + warning
		out6, _, err6 := w.apply("[e.txt#6DBE]\nPUT 1.=1:\n-one\n+ONE")
		want6 := "[e.txt#2B61]\n1:ONE\n2:three\n\nWarnings:\nIgnored unified-diff `-old` row(s); the range already removes old content, so only `+new` rows were kept."
		if err6 != nil || out6 != want6 {
			t.Fatalf("call 6:\ngot:  %s (err=%v)\nwant: %s", out6, err6, want6)
		}

		// Call 7: unknown op FOO 1
		_, _, err7 := w.apply("[e.txt#2B61]\nFOO 1")
		want7 := `line 1: payload line has no preceding hunk header. Use ` + "`PUT N.=M:`" + `, ` + "`CUT N.=M`" + `, or ` + "`PUT <N:`/`PUT >N:`" + ` above the body. Got "FOO 1".`
		if err7 == nil || err7.Error() != want7 {
			t.Fatalf("call 7:\ngot:  %v\nwant: %s", err7, want7)
		}

		// Call 8: overlapping hunks
		_, _, err8 := w.apply("[e.txt#2B61]\nPUT 1.=2:\n+a\nPUT 2.=3:\n+b")
		want8 := "line 3: anchor line 2 is already targeted by another hunk on line 1. Issue ONE hunk per range; payload is only the final desired content, never a before/after pair."
		if err8 == nil || err8.Error() != want8 {
			t.Fatalf("call 8:\ngot:  %v\nwant: %s", err8, want8)
		}

		// Call 9: empty named register paste -> no-op diagnostic
		out9, _, err9 := w.apply("[e.txt#2B61]\nPUT >1 @nope")
		want9 := "Edits to e.txt parsed and applied cleanly, but produced no change: your body row(s) are byte-identical to the file at the targeted lines. The bug is somewhere else — re-read the file before issuing another edit. Do NOT widen the payload or add lines; verify the anchor first."
		if err9 != nil || out9 != want9 {
			t.Fatalf("call 9:\ngot:  %s (err=%v)\nwant: %s", out9, err9, want9)
		}

		// Call 10: multi-file edit
		out10, _, err10 := w.apply("[e.txt#2B61]\nPUT >$:\n+tail\n[f.txt#CECD]\nPUT <1:\n+head")
		want10 := "[e.txt#7587]\n1:ONE\n2:three\n3:tail\n\n[f.txt#2218]\n1:head\n2:keep"
		if err10 != nil || out10 != want10 {
			t.Fatalf("call 10:\ngot:  %s (err=%v)\nwant: %s", out10, err10, want10)
		}

		// Call 11: MV over existing f.txt
		out11, _, err11 := w.apply("[e.txt#7587]\nMV f.txt")
		want11 := "[f.txt#7587]\nMoved to f.txt"
		if err11 != nil || out11 != want11 {
			t.Fatalf("call 11:\ngot:  %s (err=%v)\nwant: %s", out11, err11, want11)
		}

		// Call 12: empty input
		_, _, err12 := w.apply("")
		want12 := `input must begin with "[PATH#HASH]" on the first non-blank line for anchored edits; got: "". Example: "[src/foo.ts#1A2B]" then edit ops.`
		if err12 == nil || err12.Error() != want12 {
			t.Fatalf("call 12:\ngot:  %v\nwant: %s", err12, want12)
		}
	})
}

// Finding N6: empty file read records line 1 as seen so PUT 1.=1: applies without unseen-lines rejection.
func TestEmptyFileReadRecordsLineOneAndAcceptsPut1(t *testing.T) {
	w := newTestWorkspace(t)
	w.opts.EnforceSeenLines = true
	w.writeFile("e.txt", "")
	tag := w.store.RecordReadWholeFile(filepath.Join(w.dir, "e.txt"), "")
	if tag != "5D05" {
		t.Fatalf("empty file tag = %s, want 5D05", tag)
	}
	out, _, err := w.apply("[e.txt#5D05]\nPUT 1.=1:\n+x\n")
	if err != nil {
		t.Fatalf("PUT 1.=1: on empty file failed: %v", err)
	}
	want := "[e.txt#30EA]\n1:x"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if got, _ := w.readFile("e.txt"); got != "x" {
		t.Fatalf("file content = %q, want %q", got, "x")
	}
}
