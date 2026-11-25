package eval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// availableJSRuntimes returns every working JS runtime installed on PATH (both bun and node 22+ when present).
func availableJSRuntimes(t *testing.T) []jsRuntimeInfo {
	t.Helper()
	var out []jsRuntimeInfo
	pathEnv := os.Getenv("PATH")
	for _, bunPath := range lookPathAll("bun", pathEnv) {
		ctx, cancel := context.WithTimeout(context.Background(), jsProbeTimeout)
		v, err := exec.CommandContext(ctx, bunPath, "--version").Output()
		cancel()
		if err == nil {
			out = append(out, jsRuntimeInfo{Kind: "bun", BinaryPath: bunPath, Version: strings.TrimSpace(string(v))})
			break
		}
	}
	for _, nodePath := range lookPathAll("node", pathEnv) {
		ctx, cancel := context.WithTimeout(context.Background(), jsProbeTimeout)
		v, err := exec.CommandContext(ctx, nodePath, "--version").Output()
		cancel()
		if err != nil {
			continue
		}
		ver := strings.TrimSpace(string(v))
		m := jsNodeVersionRe.FindStringSubmatch(ver)
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		if major >= jsMinNodeMajor {
			out = append(out, jsRuntimeInfo{Kind: "node", BinaryPath: nodePath, Version: ver})
			break
		}
	}
	return out
}

type jsCellCase struct {
	name      string
	code      string
	wantEcho  string // expected trimmed output of the cell ("" for no output)
	wantError string // non-empty substring when cell must error (e.g. JS no-ASI rule)
	checkExpr string // optional follow-up expression verifying persisted bindings
	wantCheck string // expected trimmed output of checkExpr
}

// upstream: packages/coding-agent/src/eval/js/shared/rewrite-imports.ts:1012-1132
// Table-driven test of 65+ semicolon-less and mixed JS cells covering:
//   - all 7 cells from pass-2 review finding 1
//   - declarations followed by IIFE, array literal, template literal, regex, unary expr, parenthesized expr
//   - nested blocks, labels, do..while, if without braces, object literal in parens
//   - arrow functions with block bodies, class with static block, generators, async arrow as last expr
//   - regex directly after control-statement `)` (if/while/for/with)
//   - comments and blank lines between statements
//   - JS no-ASI cases (`a\n(b)`, `tag\n`str“) that must NOT be split
//
// Run under BOTH bun and node 22+ when installed.
func TestJSStatementSplitterAndRewriterMatrix(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS rewriter matrix")
	}

	cases := []jsCellCase{
		// --- The 7 cells from pass-2 review finding 1 ---
		{
			name:      "p2_1_async_let_followed_by_while",
			code:      "let j = 0\nwhile (j < 3) { j++; await null }\nj",
			wantEcho:  "3",
			checkExpr: "j",
			wantCheck: "3",
		},
		{
			name:      "p2_2_async_func_followed_by_while",
			code:      "function fw() { return 1 }\nwhile (false) {}\nawait null\nfw()",
			wantEcho:  "1",
			checkExpr: "fw()",
			wantCheck: "1",
		},
		{
			name:      "p2_3_async_func_followed_by_async_iife",
			code:      "function fi() { return 1 }\n(async () => { print(\"iife\") })()\nawait null",
			wantEcho:  "iife\nnull",
			checkExpr: "fi()",
			wantCheck: "1",
		},
		{
			name:      "p2_4_sync_class_followed_by_array_literal",
			code:      "class CA {}\n[1, 2, 3].map(x => x * 2).join(',')",
			wantEcho:  "2,4,6",
			checkExpr: "typeof CA",
			wantCheck: "function",
		},
		{
			name:      "p2_5_func_followed_by_paren_expr",
			code:      "function fp() { return 41 }\n(fp() + 1)",
			wantEcho:  "42",
			checkExpr: "fp()",
			wantCheck: "41",
		},
		{
			name:      "p2_6_if_block_followed_by_array_literal",
			code:      "let blk = 1\nif (true) { blk = 9 }\n[blk, 2].join(':')",
			wantEcho:  "9:2",
			checkExpr: "blk",
			wantCheck: "9",
		},
		{
			name:      "p2_7_generator_followed_by_spread_array",
			code:      "function* g() { yield 1; yield 2 }\n[...g()].join(',')",
			wantEcho:  "1,2",
			checkExpr: "typeof g",
			wantCheck: "function",
		},

		// --- Declarations followed by IIFE, array, template, regex, unary, paren ---
		{
			name:      "func_followed_by_sync_iife",
			code:      "function f1() { return 7 }\n(() => f1() * 2)()",
			wantEcho:  "14",
			checkExpr: "f1()",
			wantCheck: "7",
		},
		{
			name:      "async_func_followed_by_iife",
			code:      "async function af1() { return 8 }\n(await af1()) + 1",
			wantEcho:  "9",
			checkExpr: "typeof af1",
			wantCheck: "function",
		},
		{
			name:      "func_followed_by_array_literal",
			code:      "function f2() { return 5 }\n[f2(), 6].join('-')",
			wantEcho:  "5-6",
			checkExpr: "f2()",
			wantCheck: "5",
		},
		{
			name:      "func_followed_by_template_literal",
			code:      "function f3() { return 'ok' }\n`val:${f3()}`",
			wantEcho:  "val:ok",
			checkExpr: "f3()",
			wantCheck: "ok",
		},
		{
			name:      "func_followed_by_regex_literal",
			code:      "function f4() { return 'abc' }\n/b/.test(f4())",
			wantEcho:  "true",
			checkExpr: "f4()",
			wantCheck: "abc",
		},
		{
			name:      "func_followed_by_unary_plus",
			code:      "function f5() { return '42' }\n+f5()",
			wantEcho:  "42",
			checkExpr: "f5()",
			wantCheck: "42",
		},
		{
			name:      "func_followed_by_unary_minus",
			code:      "function f6() { return 9 }\n-f6()",
			wantEcho:  "-9",
			checkExpr: "f6()",
			wantCheck: "9",
		},
		{
			name:      "func_followed_by_unary_not",
			code:      "function f7() { return false }\n!f7()",
			wantEcho:  "true",
			checkExpr: "f7()",
			wantCheck: "false",
		},
		{
			name:      "class_followed_by_paren_expr",
			code:      "class C1 { v() { return 3 } }\n(new C1().v() + 4)",
			wantEcho:  "7",
			checkExpr: "new C1().v()",
			wantCheck: "3",
		},
		{
			name:      "class_followed_by_template_literal",
			code:      "class C2 { static tag = 'X' }\n`cls:${C2.tag}`",
			wantEcho:  "cls:X",
			checkExpr: "C2.tag",
			wantCheck: "X",
		},
		{
			name:      "class_followed_by_regex",
			code:      "class C3 {}\n/C3/.test(C3.name)",
			wantEcho:  "true",
			checkExpr: "C3.name",
			wantCheck: "C3",
		},
		{
			name:      "class_followed_by_unary_plus",
			code:      "class C4 { static n = 11 }\n+C4.n",
			wantEcho:  "11",
			checkExpr: "C4.n",
			wantCheck: "11",
		},
		{
			name:      "class_with_static_block_followed_by_array",
			code:      "class CStat { static x; static { this.x = 22 } }\n[CStat.x, 33].join(',')",
			wantEcho:  "22,33",
			checkExpr: "CStat.x",
			wantCheck: "22",
		},
		{
			name:      "async_class_with_static_block_and_await",
			code:      "class CStatA { static x; static { this.x = 44 } }\nconst rStat = await Promise.resolve(CStatA.x)\nrStat",
			wantEcho:  "44",
			checkExpr: "CStatA.x + rStat",
			wantCheck: "88",
		},

		// --- Blocks, control flow, nested blocks, labels, do..while, if without braces ---
		{
			name:      "bare_block_followed_by_paren_expr",
			code:      "let bb = 1\n{ bb = 5 }\n(bb + 1)",
			wantEcho:  "6",
			checkExpr: "bb",
			wantCheck: "5",
		},
		{
			name:      "bare_block_followed_by_array",
			code:      "{ var bv = 12 }\n[bv, 13].join(',')",
			wantEcho:  "12,13",
			checkExpr: "bv",
			wantCheck: "12",
		},
		{
			name:      "nested_blocks_followed_by_template",
			code:      "let nb = 0\n{ { nb = 7 } }\n`nb=${nb}`",
			wantEcho:  "nb=7",
			checkExpr: "nb",
			wantCheck: "7",
		},
		{
			name:      "if_else_followed_by_array",
			code:      "let ie = 0\nif (false) { ie = 1 } else { ie = 2 }\n[ie].join('')",
			wantEcho:  "2",
			checkExpr: "ie",
			wantCheck: "2",
		},
		{
			name:      "if_without_braces_followed_by_expr",
			code:      "let ib = 0\nif (true) ib = 15\nib + 5",
			wantEcho:  "20",
			checkExpr: "ib",
			wantCheck: "15",
		},
		{
			name:      "if_else_without_braces_followed_by_expr",
			code:      "let ieb = 0\nif (false) ieb = 1\nelse ieb = 25\nieb",
			wantEcho:  "25",
			checkExpr: "ieb",
			wantCheck: "25",
		},
		{
			name:      "for_loop_followed_by_paren_expr",
			code:      "let sumF = 0\nfor (let i = 1; i <= 4; i++) { sumF += i }\n(sumF * 2)",
			wantEcho:  "20",
			checkExpr: "sumF",
			wantCheck: "10",
		},
		{
			name:      "for_of_async_followed_by_array",
			code:      "let sumFA = 0\nfor (const n of [1, 2, 3]) { sumFA += await Promise.resolve(n) }\n[sumFA].join('')",
			wantEcho:  "6",
			checkExpr: "sumFA",
			wantCheck: "6",
		},
		{
			name:      "while_loop_followed_by_array",
			code:      "let wv = 0\nwhile (wv < 3) { wv++ }\n[wv, wv + 1].join(',')",
			wantEcho:  "3,4",
			checkExpr: "wv",
			wantCheck: "3",
		},
		{
			name:      "do_while_block_not_split_before_while",
			code:      "let dw = 0\ndo { dw++ } while (dw < 4)\ndw",
			wantEcho:  "4",
			checkExpr: "dw",
			wantCheck: "4",
		},
		{
			name:      "do_while_multiline_not_split_before_while",
			code:      "let dw2 = 0\ndo {\n  dw2 += 2\n}\nwhile (dw2 < 6)\ndw2",
			wantEcho:  "6",
			checkExpr: "dw2",
			wantCheck: "6",
		},
		{
			name:      "do_while_async_not_split_before_while",
			code:      "let dw3 = 0\ndo {\n  dw3 += await Promise.resolve(3)\n} while (dw3 < 9)\ndw3",
			wantEcho:  "9",
			checkExpr: "dw3",
			wantCheck: "9",
		},
		{
			name:      "switch_followed_by_paren_expr",
			code:      "let sw = 0\nswitch (2) { case 2: sw = 20; break }\n(sw + 1)",
			wantEcho:  "21",
			checkExpr: "sw",
			wantCheck: "20",
		},
		{
			name:      "try_catch_followed_by_array",
			code:      "let tc = ''\ntry { throw new Error('e') } catch (err) { tc = err.message }\n[tc, 'ok'].join(':')",
			wantEcho:  "e:ok",
			checkExpr: "tc",
			wantCheck: "e",
		},
		{
			name:      "try_catch_finally_followed_by_template",
			code:      "let tcf = ''\ntry { tcf += 'a' } catch { tcf += 'b' } finally { tcf += 'c' }\n`res=${tcf}`",
			wantEcho:  "res=ac",
			checkExpr: "tcf",
			wantCheck: "ac",
		},
		{
			name:      "labelled_block_followed_by_array",
			code:      "let lb = 0\nouter: { lb = 1; break outer; lb = 2 }\n[lb].join('')",
			wantEcho:  "1",
			checkExpr: "lb",
			wantCheck: "1",
		},
		{
			name:      "labelled_loop_followed_by_paren",
			code:      "let ll = 0\nloop1: for (let i = 0; i < 5; i++) { ll = i; if (i === 2) break loop1 }\n(ll + 10)",
			wantEcho:  "12",
			checkExpr: "ll",
			wantCheck: "2",
		},
		{
			name:      "labelled_do_while_followed_by_expr",
			code:      "let ldw = 0\nld: do { ldw++; if (ldw === 2) break ld } while (ldw < 5)\nldw",
			wantEcho:  "2",
			checkExpr: "ldw",
			wantCheck: "2",
		},

		// --- Object literal in parens, arrow functions, async arrow as last expr ---
		{
			name:      "object_literal_in_parens_echoed",
			code:      "const o1 = 1;\n({ a: o1, b: 2 }).a + 10",
			wantEcho:  "11",
			checkExpr: "o1",
			wantCheck: "1",
		},
		{
			name:      "object_destructuring_in_parens_suppressed",
			code:      "let da, db\n({ a: da, b: db } = { a: 3, b: 4 })",
			wantEcho:  "",
			checkExpr: "da + db",
			wantCheck: "7",
		},
		{
			name:      "arrow_with_block_body_persists_and_echoes_later",
			code:      "const add1 = (x) => {\n  return x + 1\n}\nadd1(41)",
			wantEcho:  "42",
			checkExpr: "add1(9)",
			wantCheck: "10",
		},
		{
			name:      "async_arrow_as_last_expression_echoed_not_corrupted",
			code:      "const baseA = 5\nasync (x) => { return x + baseA }",
			wantEcho:  "[AsyncFunction (anonymous)]",
			checkExpr: "baseA",
			wantCheck: "5",
		},
		{
			name:      "async_arrow_defined_and_awaited",
			code:      "const dbl = async x => {\n  return await Promise.resolve(x * 2)\n}\nawait dbl(21)",
			wantEcho:  "42",
			checkExpr: "typeof dbl",
			wantCheck: "function",
		},
		{
			name:      "generator_redeclared_across_cells",
			code:      "function* genA() { yield 'x'; yield 'y' }\n[...genA()].join('')",
			wantEcho:  "xy",
			checkExpr: "[...genA()].length",
			wantCheck: "2",
		},
		{
			name:      "async_generator_with_for_await",
			code:      "async function* agen() { yield 10; yield 20 }\nlet agSum = 0\nfor await (const v of agen()) { agSum += v }\nagSum",
			wantEcho:  "30",
			checkExpr: "agSum",
			wantCheck: "30",
		},

		// --- Regex directly after control-statement `)` (finding 4) ---
		{
			name:      "regex_after_if_paren_keeps_const_demotion",
			code:      "const s8 = \"{\"\nif (s8) /[{]/.test(s8)\nconst after8 = 1",
			wantEcho:  "",
			checkExpr: "const after8 = 2; after8",
			wantCheck: "2",
		},
		{
			name:      "regex_after_while_paren",
			code:      "let rwDone = false\nwhile (!rwDone) /[{]/.test('{') && (rwDone = true)\nconst afterW = 7",
			wantEcho:  "",
			checkExpr: "const afterW = 8; afterW",
			wantCheck: "8",
		},
		{
			name:      "regex_after_for_paren",
			code:      "let rfHit = false\nfor (let i = 0; i < 1; i++) /[{]/.test('{') && (rfHit = true)\nconst afterF = 3",
			wantEcho:  "",
			checkExpr: "const afterF = 4; `${rfHit},${afterF}`",
			wantCheck: "true,4",
		},
		{
			name:      "division_after_grouping_paren_not_regex",
			code:      "const divR = (10 + 2) / 3\nconst divAfter = divR + 1\ndivAfter",
			wantEcho:  "5",
			checkExpr: "const divAfter = 6; divAfter",
			wantCheck: "6",
		},
		{
			name:      "regex_after_if_paren_in_async_cell",
			code:      "const as8 = await Promise.resolve(\"{\")\nif (as8) /[{]/.test(as8)\nconst asyncAfter8 = 99",
			wantEcho:  "",
			checkExpr: "asyncAfter8",
			wantCheck: "99",
		},

		// --- Comments, blank lines, trailing semicolons, directives, returns ---
		{
			name:      "comments_and_blank_lines_between_statements",
			code:      "// leading comment\nconst c1 = 10\n\n/* block\n   comment */\n\nconst c2 = 20 // inline\n\nc1 + c2",
			wantEcho:  "30",
			checkExpr: "c1 * c2",
			wantCheck: "200",
		},
		{
			name:     "string_only_cell_echoed",
			code:     "\"hello\"",
			wantEcho: "hello",
		},
		{
			name:      "trailing_semicolon_expression_echoed",
			code:      "const ts = 6; ts * 7;",
			wantEcho:  "42",
			checkExpr: "ts",
			wantCheck: "6",
		},
		{
			name:     "trailing_block_statement_suppressed",
			code:     "if (true) { 42; }",
			wantEcho: "",
		},
		{
			name:      "expression_followed_by_declaration_suppressed",
			code:      "42; const afterDecl = 1;",
			wantEcho:  "",
			checkExpr: "afterDecl",
			wantCheck: "1",
		},
		{
			name:      "trailing_assignment_suppressed",
			code:      "let ta = 0; ta = 99",
			wantEcho:  "",
			checkExpr: "ta",
			wantCheck: "99",
		},
		{
			name:      "parenthesized_assignment_echoed",
			code:      "let pa = 0; (pa = 19)",
			wantEcho:  "19",
			checkExpr: "pa",
			wantCheck: "19",
		},
		{
			name:      "sequence_ending_in_assignment_suppressed",
			code:      "let sa = 0, sb = 0; sa = 1, sb = 2",
			wantEcho:  "",
			checkExpr: "sa + sb",
			wantCheck: "3",
		},
		{
			name:      "sequence_ending_in_expression_echoed",
			code:      "let sc = 0; sc = 1, sc + 4",
			wantEcho:  "5",
			checkExpr: "sc",
			wantCheck: "1",
		},
		{
			name:      "top_level_return_in_async_cell",
			code:      "const rt1 = await Promise.resolve(3)\nif (true) return rt1 + 4\nconst unreachable = 99",
			wantEcho:  "7",
			checkExpr: "rt1",
			wantCheck: "3",
		},
		{
			name:      "multiline_binary_plus_not_corrupted",
			code:      "const mb1 = 'a' +\n  'b'",
			wantEcho:  "",
			checkExpr: "mb1",
			wantCheck: "ab",
		},
		{
			name:      "multiline_logical_or_not_corrupted",
			code:      "var mb2 = 0 ||\n  7",
			wantEcho:  "",
			checkExpr: "mb2",
			wantCheck: "7",
		},
		{
			name:     "multiline_return_binary_not_corrupted",
			code:     "return 10 +\n  32",
			wantEcho: "42",
		},
		{
			name:     "multiline_method_chain_echoed",
			code:     "[1, 2, 3]\n  .filter(x => x > 1)\n  .join('-')",
			wantEcho: "2-3",
		},
		{
			name:      "nested_template_with_backtick_in_string",
			code:      "const nt1 = `${\"`\"}-ok`\nconst nt2 = 17;\n`${nt1}:${nt2}`",
			wantEcho:  "`-ok:17",
			checkExpr: "const nt2 = 18; nt2",
			wantCheck: "18",
		},

		// --- JS automatic semicolon rules: lines that JS joins MUST NOT be split ---
		{
			name:     "js_no_asi_call_across_newline_joins",
			code:     "const fnCall = x => x + 1\nfnCall\n(41)",
			wantEcho: "42",
		},
		{
			name:      "js_no_asi_number_followed_by_paren_errors_like_js",
			code:      "1\n(function(){})()",
			wantError: "is not a function",
		},
		{
			name:     "js_no_asi_tagged_template_across_newline_joins",
			code:     "const myTag = s => s[0].toUpperCase()\nmyTag\n`hello`",
			wantEcho: "HELLO",
		},
		{
			name:     "js_no_asi_member_index_across_newline_joins",
			code:     "const arrIdx = [10, 20, 30]\narrIdx\n[1]",
			wantEcho: "20",
		},
	}

	if len(cases) < 60 {
		t.Fatalf("expected at least 60 matrix cases, got %d", len(cases))
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			k, err := startJSKernelWithRuntime(context.Background(), rt, cwd, "js-matrix-"+rt.Kind, nil, nil)
			if err != nil {
				t.Fatalf("start %s kernel: %v", rt.Kind, err)
			}
			defer k.shutdown()

			execCell := func(code string) (string, bool) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				res, err := k.exec(ctx, "cell-"+newRandomID(), code, "matrix.js", cwd, nil, nil, "js-matrix-"+rt.Kind, nil, nil, nil)
				if err != nil {
					t.Fatalf("%s exec error for %q: %v", rt.Kind, code, err)
				}
				failed := res.Cancelled || (res.ExitCode != nil && *res.ExitCode != 0)
				return strings.TrimSpace(res.Output), failed
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					out, failed := execCell(tc.code)
					if tc.wantError != "" {
						if !failed || !strings.Contains(out, tc.wantError) {
							t.Fatalf("[%s] cell %q: expected error containing %q, got failed=%v out=%q", rt.Kind, tc.code, tc.wantError, failed, out)
						}
						return
					}
					if failed {
						t.Fatalf("[%s] cell %q failed unexpectedly: %s", rt.Kind, tc.code, out)
					}
					if out != tc.wantEcho {
						t.Fatalf("[%s] cell %q: echo = %q, want %q", rt.Kind, tc.code, out, tc.wantEcho)
					}
					if tc.checkExpr != "" {
						chkOut, chkFail := execCell(tc.checkExpr)
						if chkFail {
							t.Fatalf("[%s] check %q failed: %s", rt.Kind, tc.checkExpr, chkOut)
						}
						if chkOut != tc.wantCheck {
							t.Fatalf("[%s] check %q = %q, want %q", rt.Kind, tc.checkExpr, chkOut, tc.wantCheck)
						}
					}
				})
			}
		})
	}
}

// upstream: packages/coding-agent/src/eval/js/shared/rewrite-imports.ts:541-565 (__omp_import__)
func TestJSDynamicImportBuiltinAndRelativeToSessionCwd(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS dynamic import test")
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			modPath := filepath.Join(cwd, "local-mod.mjs")
			if err := os.WriteFile(modPath, []byte("export const answer = 42;\nexport default 'from-cwd';\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			k, err := startJSKernelWithRuntime(context.Background(), rt, cwd, "js-import-"+rt.Kind, nil, nil)
			if err != nil {
				t.Fatalf("start %s kernel: %v", rt.Kind, err)
			}
			defer k.shutdown()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := k.exec(ctx, "imp-1", `
const osMod = await import("node:os");
const localMod = await import("./local-mod.mjs");
print(typeof osMod.platform, localMod.answer, localMod.default);
`, "imp.js", cwd, nil, nil, "js-import-"+rt.Kind, nil, nil, nil)
			if err != nil {
				t.Fatalf("%s import exec err: %v", rt.Kind, err)
			}
			if res.ExitCode == nil || *res.ExitCode != 0 {
				t.Fatalf("%s import cell failed: %s", rt.Kind, res.Output)
			}
			if strings.TrimSpace(res.Output) != "function 42 from-cwd" {
				t.Fatalf("%s import output = %q, want %q", rt.Kind, strings.TrimSpace(res.Output), "function 42 from-cwd")
			}
		})
	}
}

func TestJSErrorHasNoBlankLineAfterNewlineOutput(t *testing.T) {
	if !hasJSRuntime(t) {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS error formatting test")
	}
	ext := sdk.New("test-eval-js-errfmt")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "js",
		"code":     "print('before');\nthrow new Error('boom');",
	})
	if errMsg != "" {
		t.Fatalf("eval error: %s", errMsg)
	}
	txt, _, _, isErr := decodeToolResult(t, raw)
	if !isErr {
		t.Fatalf("expected error result, got ok: %q", txt)
	}
	if strings.Contains(txt, "before\n\n") {
		t.Fatalf("unexpected blank line directly after newline-terminated output: %q", txt)
	}
	if !strings.HasPrefix(txt, "before\n") || !strings.Contains(txt, "Error: boom") {
		t.Fatalf("expected error directly after 'before\\n', got: %q", txt)
	}
}

func TestNoLiveUpdateDeliveredAfterFinalResult(t *testing.T) {
	if !hasPython3(t) {
		t.Skip("python3 3.9+ not found on PATH; skipping live update ordering test")
	}
	ext := sdk.New("test-eval-update-order")
	mgr := NewManager()
	t.Cleanup(mgr.ShutdownAll)
	registerWithManager(ext, mgr)
	h := startHost(t, ext, hostOptions{})

	raw, errMsg := h.callTool("eval", map[string]any{
		"language": "py",
		"code":     "import time\nfor i in range(5):\n    print(i, flush=True)\n    time.sleep(0.02)",
	})
	if errMsg != "" {
		t.Fatalf("eval error: %s", errMsg)
	}
	_, _, _, isErr := decodeToolResult(t, raw)
	if isErr {
		t.Fatalf("unexpected cell error")
	}
	countAtReturn := len(h.getUpdates())
	time.Sleep(120 * time.Millisecond)
	countAfterWait := len(h.getUpdates())
	if countAfterWait != countAtReturn {
		t.Fatalf("live updates arrived after final result returned: before=%d after=%d", countAtReturn, countAfterWait)
	}
}
