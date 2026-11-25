package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type jsRewriterManifest struct {
	Generator          string `json:"generator"`
	OmpTag             string `json:"ompTag"`
	OmpCommit          string `json:"ompCommit"`
	BabelParserVersion string `json:"babelParserVersion"`
	BunVersion         string `json:"bunVersion"`
	Output             struct {
		File   string `json:"file"`
		Bytes  int    `json:"bytes"`
		SHA256 string `json:"sha256"`
	} `json:"output"`
	Inputs []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"inputs"`
}

// TestJSRewriterAssetMatchesManifest pins the embedded js_rewriter.cjs asset
// and on-disk file to js_rewriter.manifest.json.
func TestJSRewriterAssetMatchesManifest(t *testing.T) {
	manifestBytes, err := os.ReadFile("js_rewriter.manifest.json")
	if err != nil {
		t.Fatalf("read js_rewriter.manifest.json: %v", err)
	}
	var m jsRewriterManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatalf("parse js_rewriter.manifest.json: %v", err)
	}
	if m.OmpTag != "v19.1.1" {
		t.Fatalf("manifest ompTag = %q, want v19.1.1", m.OmpTag)
	}
	if m.BabelParserVersion != "7.29.9" {
		t.Fatalf("manifest babelParserVersion = %q, want 7.29.9", m.BabelParserVersion)
	}
	if m.Output.File != "js_rewriter.cjs" {
		t.Fatalf("manifest output.file = %q, want js_rewriter.cjs", m.Output.File)
	}
	if len(m.Inputs) < 5 {
		t.Fatalf("expected at least 5 manifest inputs, got %d", len(m.Inputs))
	}

	embeddedSum := sha256.Sum256([]byte(jsRewriterBundle))
	embeddedHex := hex.EncodeToString(embeddedSum[:])
	if embeddedHex != m.Output.SHA256 {
		t.Fatalf("embedded js_rewriter.cjs sha256 = %s, manifest expects %s", embeddedHex, m.Output.SHA256)
	}
	if len(jsRewriterBundle) != m.Output.Bytes {
		t.Fatalf("embedded js_rewriter.cjs size = %d, manifest expects %d", len(jsRewriterBundle), m.Output.Bytes)
	}

	diskBytes, err := os.ReadFile(m.Output.File)
	if err != nil {
		t.Fatalf("read %s: %v", m.Output.File, err)
	}
	diskSum := sha256.Sum256(diskBytes)
	if hex.EncodeToString(diskSum[:]) != m.Output.SHA256 {
		t.Fatalf("on-disk %s sha256 mismatch with manifest", m.Output.File)
	}
}

// TestJSRewriterAssetRegeneratesDeterministically proves that the checked-in
// js_rewriter.cjs asset regenerates byte-for-byte identically via
// gen/build-rewriter.ts from omp v19.1.1 and @babel/parser@7.29.9.
// Skipped when bun is unavailable, OMP_SRC is unset, or @babel/parser@7.29.9
// is not present in BABEL_NODE_MODULES or $OMP_SRC/node_modules.
func TestJSRewriterAssetRegeneratesDeterministically(t *testing.T) {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found on PATH; skipping js_rewriter.cjs regeneration check")
	}
	ompSrc := os.Getenv("OMP_SRC")
	if ompSrc == "" {
		t.Skip("OMP_SRC is unset; set OMP_SRC to omp tag v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a)")
	}
	if _, err := os.Stat(filepath.Join(ompSrc, "packages/coding-agent/src/eval/js/shared/rewrite-imports.ts")); err != nil {
		t.Skipf("omp v19.1.1 source export not present at %s; skipping regeneration check", ompSrc)
	}
	babelCandidates := []string{
		os.Getenv("BABEL_NODE_MODULES"),
		filepath.Join(ompSrc, "node_modules"),
	}
	var babelDir string
	for _, c := range babelCandidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c, "@babel/parser/package.json")); err == nil {
			babelDir = c
			break
		}
	}
	if babelDir == "" {
		t.Skip("BABEL_NODE_MODULES is unset and @babel/parser@7.29.9 was not found in $OMP_SRC/node_modules; set BABEL_NODE_MODULES to a node_modules directory containing @babel/parser@7.29.9")
	}

	outDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, "run", filepath.Join("gen", "build-rewriter.ts"),
		"--omp-src", ompSrc,
		"--babel-node-modules", babelDir,
		"--out-dir", outDir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build-rewriter.ts failed: %v\n%s", err, string(out))
	}

	gotBytes, err := os.ReadFile(filepath.Join(outDir, "js_rewriter.cjs"))
	if err != nil {
		t.Fatalf("read regenerated asset: %v", err)
	}
	wantBytes, err := os.ReadFile("js_rewriter.cjs")
	if err != nil {
		t.Fatalf("read checked-in asset: %v", err)
	}
	if string(gotBytes) != string(wantBytes) {
		gotSum := sha256.Sum256(gotBytes)
		wantSum := sha256.Sum256(wantBytes)
		t.Fatalf("regenerated js_rewriter.cjs differs from checked-in asset: got=%s want=%s (bun version may differ from manifest)",
			hex.EncodeToString(gotSum[:]), hex.EncodeToString(wantSum[:]))
	}
}

// TestNodeStringWidthShimMatchesBun compares js_runner.js's Node Bun.stringWidth
// shim against native Bun.stringWidth across ASCII, C0/C1 controls, combining
// marks, CJK fullwidth, halfwidth, emoji, ZWJ sequences, regional indicators,
// keycaps, and ANSI escape sequences.
// Runs directly against the runner's `nodeStringWidth` helper (since `Bun` is
// intentionally NOT exposed on `globalThis` under Node).
func TestNodeStringWidthShimMatchesBun(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	var bunRT, nodeRT *jsRuntimeInfo
	for i := range runtimes {
		if runtimes[i].Kind == "bun" {
			bunRT = &runtimes[i]
		} else if runtimes[i].Kind == "node" {
			nodeRT = &runtimes[i]
		}
	}
	if bunRT == nil || nodeRT == nil {
		t.Skip("both bun and node 22+ are required to compare Bun.stringWidth against the Node shim")
	}

	samplesJSON := `["","a","Z","0"," ","\t","\n","\r","\u0000","\u0007","\u001b","\u007f","é","e\u0301","a\u0300\u0301","\u200b","\u200d","\ufeff","\u00ad","汉","字","あ","ア","한","가","Ａ","１","！","￥","　","、","。","「","」","①","─","│","┌","…","—","•","±","×","÷","π","∑","∞","😀","🔥","🎉","🚀","👍","👍🏽","❤️","❤","☀️","☀","⚠️","⚠","👨‍👩‍👧‍👦","🇺🇸","🇯🇵","1️⃣","#️⃣","𠀀","丽","\uff61","\uff9f","\u001b[31mred\u001b[0m","hello 汉 world","const 汉 = ;","\u0301","\ufe0f","🇯","©️","©","®️","®","™️","™","⌚","⌛","⏩","☕","⚡","✨","한글テスト漢字"]`

	// Native Bun.stringWidth widths.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bunCmd := exec.CommandContext(ctx, bunRT.BinaryPath, "-e", `console.log(JSON.stringify((`+samplesJSON+`).map(s => Bun.stringWidth(s))))`)
	bunOutBytes, err := bunCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bun stringWidth probe failed: %v\n%s", err, string(bunOutBytes))
	}

	// Extract nodeStringWidth from js_runner.js in a Node subprocess (without running
	// the NDJSON loop) and evaluate the exact same 88 strings.
	nodeScript := `
const fs = require("node:fs");
const util = require("node:util");
const vm = require("node:vm");
const src = fs.readFileSync(process.argv[1], "utf8");
const start = src.indexOf("const COMBINING_MARK_RE =");
const end = src.indexOf("function suppressStripTypeScriptWarning()");
if (start < 0 || end < 0) throw new Error("failed to locate nodeStringWidth in js_runner.js");
const fn = vm.compileFunction(src.slice(start, end) + "\nreturn nodeStringWidth;", ["util"]);
const nodeStringWidth = fn(util);
const samples = ` + samplesJSON + `;
console.log(JSON.stringify(samples.map(s => nodeStringWidth(s))));
`
	nodeCmd := exec.CommandContext(ctx, nodeRT.BinaryPath, "-e", nodeScript, "js_runner.js")
	nodeOutBytes, err := nodeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node stringWidth probe failed: %v\n%s", err, string(nodeOutBytes))
	}

	bunOut := strings.TrimSpace(string(bunOutBytes))
	nodeOut := strings.TrimSpace(string(nodeOutBytes))
	if bunOut != nodeOut {
		t.Fatalf("Bun.stringWidth mismatch between bun and node shim:\nbun:  %s\nnode: %s", bunOut, nodeOut)
	}
}

// TestNodeBunShimNotExposedToCells verifies that under Node, `typeof Bun` is
// "undefined" inside user cells (so `typeof Bun !== "undefined"` guards take
// the Node branch and `Bun.file` throws ReferenceError rather than TypeError),
// while the bundled omp rewriter still receives `Bun.stringWidth` (wide-char
// caret alignment) and `Bun.Transpiler` (TypeScript syntax stripping).
func TestNodeBunShimNotExposedToCells(t *testing.T) {
	var nodeRT *jsRuntimeInfo
	for _, rt := range availableJSRuntimes(t) {
		if rt.Kind == "node" {
			r := rt
			nodeRT = &r
			break
		}
	}
	if nodeRT == nil {
		t.Skip("node 22+ not found on PATH; skipping Node Bun-shim isolation test")
	}

	cwd := t.TempDir()
	k, err := startJSKernelWithRuntime(context.Background(), *nodeRT, cwd, "node-bun-iso", nil, nil)
	if err != nil {
		t.Fatalf("start node kernel: %v", err)
	}
	defer k.shutdown()

	execCell := func(code string) (string, bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res, err := k.exec(ctx, "iso-"+newRandomID(), code, "iso.js", cwd, nil, nil, "node-bun-iso", nil, nil, nil)
		if err != nil {
			t.Fatalf("exec error: %v", err)
		}
		failed := res.Cancelled || (res.ExitCode != nil && *res.ExitCode != 0)
		return strings.TrimSpace(res.Output), failed
	}

	// 1. typeof Bun === "undefined" and `"Bun" in globalThis === false`.
	out, failed := execCell(`[typeof Bun, "Bun" in globalThis].join(":")`)
	if failed || out != "undefined:false" {
		t.Fatalf("expected typeof Bun === undefined and not in globalThis, got failed=%v out=%q", failed, out)
	}

	// 2. Calling Bun.file throws ReferenceError (not TypeError).
	out, failed = execCell(`Bun.file("x")`)
	if !failed || !strings.Contains(out, "ReferenceError") {
		t.Fatalf("expected ReferenceError for Bun.file under node, got failed=%v out=%q", failed, out)
	}

	// 3. Rewriter still gets Bun.Transpiler (TypeScript stripping works).
	out, failed = execCell(`interface Pair { a: number; b: number }; const p: Pair = { a: 20, b: 22 }; p.a + p.b`)
	if failed || out != "42" {
		t.Fatalf("expected TS stripping to succeed via private Bun shim, got failed=%v out=%q", failed, out)
	}

	// 4. Rewriter still gets Bun.stringWidth (wide Unicode caret aligned to 11 spaces).
	out, failed = execCell("const 汉 = ;")
	wantWide := "SyntaxError: Unexpected token (line 1, column 11)\n1 | const 汉 = ;\n  | " + strings.Repeat(" ", 11) + "^"
	if !failed || !strings.Contains(out, wantWide) {
		t.Fatalf("expected wide caret alignment via private Bun.stringWidth shim, got failed=%v out=%q", failed, out)
	}
}

// TestNodeTransitiveLocalModuleReload verifies that editing a dependency
// (`dep.js`) imported by an entry module (`a.js`) causes a subsequent
// `import("./a.js")` under Node (and Bun) to reload both `a.js` and `dep.js`
// and return the updated value, including across cycles.
func TestNodeTransitiveLocalModuleReload(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping transitive module reload test")
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			depPath := filepath.Join(cwd, "dep.js")
			entryPath := filepath.Join(cwd, "a.js")
			if err := os.WriteFile(depPath, []byte("export const x = 1;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(entryPath, []byte("import { x } from './dep.js';\nexport const val = [2, x];\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			k, err := startJSKernelWithRuntime(context.Background(), rt, cwd, "reload-"+rt.Kind, nil, nil)
			if err != nil {
				t.Fatalf("start %s kernel: %v", rt.Kind, err)
			}
			defer k.shutdown()

			execCell := func(code string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				res, err := k.exec(ctx, "rel-"+newRandomID(), code, "reload.js", cwd, nil, nil, "reload-"+rt.Kind, nil, nil, nil)
				if err != nil {
					t.Fatalf("%s exec error: %v", rt.Kind, err)
				}
				if res.ExitCode == nil || *res.ExitCode != 0 {
					t.Fatalf("%s cell failed: %s", rt.Kind, res.Output)
				}
				return strings.TrimSpace(res.Output)
			}

			got1 := execCell(`JSON.stringify((await import("./a.js")).val)`)
			if got1 != "[2,1]" {
				t.Fatalf("[%s] initial import = %q, want [2,1]", rt.Kind, got1)
			}

			// Edit ONLY dep.js (advance mtime explicitly to avoid filesystem timestamp granularity).
			if err := os.WriteFile(depPath, []byte("export const x = 99;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(2 * time.Second)
			if err := os.Chtimes(depPath, future, future); err != nil {
				t.Fatal(err)
			}

			got2 := execCell(`JSON.stringify((await import("./a.js")).val)`)
			if got2 != "[2,99]" {
				t.Fatalf("[%s] after editing dep.js, import(./a.js).val = %q, want [2,99]", rt.Kind, got2)
			}

			// Cycle-safe transitive reload: cycA -> cycB -> cycA.
			cycAPath := filepath.Join(cwd, "cycA.mjs")
			cycBPath := filepath.Join(cwd, "cycB.mjs")
			if err := os.WriteFile(cycAPath, []byte("import { getB } from './cycB.mjs';\nexport const a = 1;\nexport const pair = () => [a, getB()];\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cycBPath, []byte("import { a } from './cycA.mjs';\nexport const b = 10;\nexport const getB = () => b + a;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := execCell(`JSON.stringify((await import("./cycA.mjs")).pair())`); got != "[1,11]" {
				t.Fatalf("[%s] initial cycle import = %q, want [1,11]", rt.Kind, got)
			}
			if err := os.WriteFile(cycBPath, []byte("import { a } from './cycA.mjs';\nexport const b = 200;\nexport const getB = () => b + a;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			future2 := time.Now().Add(4 * time.Second)
			if err := os.Chtimes(cycBPath, future2, future2); err != nil {
				t.Fatal(err)
			}
			if got := execCell(`JSON.stringify((await import("./cycA.mjs")).pair())`); got != "[1,201]" {
				t.Fatalf("[%s] after editing cycB.mjs, cycle import = %q, want [1,201]", rt.Kind, got)
			}
		})
	}
}

type jsOracleCase struct {
	name           string
	code           string
	wantEcho       string
	wantError      string
	checkExpr      string
	wantCheck      string
	wantCheckError string
}

func runJSOracleCases(t *testing.T, rt jsRuntimeInfo, cwd string, cases []jsOracleCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh kernel per case matches the oracle's per-case process isolation
			// so prior bindings never bleed between independent cases.
			k, err := startJSKernelWithRuntime(context.Background(), rt, cwd, "oracle-"+tc.name+"-"+rt.Kind, nil, nil)
			if err != nil {
				t.Fatalf("start %s kernel: %v", rt.Kind, err)
			}
			defer k.shutdown()

			execCell := func(code, filename string) (string, bool) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				res, err := k.exec(ctx, "c-"+newRandomID(), code, filename, cwd, nil, nil, "oracle-"+tc.name+"-"+rt.Kind, nil, nil, nil)
				if err != nil {
					t.Fatalf("%s exec error for %q: %v", rt.Kind, code, err)
				}
				failed := res.Cancelled || (res.ExitCode != nil && *res.ExitCode != 0)
				return strings.TrimSpace(res.Output), failed
			}

			out, failed := execCell(tc.code, "cell.js")
			if tc.wantError != "" {
				if !failed {
					t.Fatalf("[%s] %s: expected error %q, got OK %q", rt.Kind, tc.name, tc.wantError, out)
				}
				if !strings.Contains(out, tc.wantError) {
					t.Fatalf("[%s] %s: expected error containing %q, got %q", rt.Kind, tc.name, tc.wantError, out)
				}
				return
			}
			if failed {
				t.Fatalf("[%s] %s: cell failed unexpectedly: %s", rt.Kind, tc.name, out)
			}
			if out != tc.wantEcho {
				t.Fatalf("[%s] %s: cell echo = %q, want %q", rt.Kind, tc.name, out, tc.wantEcho)
			}
			if tc.checkExpr != "" {
				chkOut, chkFail := execCell(tc.checkExpr, "check.js")
				if tc.wantCheckError != "" {
					if !chkFail {
						t.Fatalf("[%s] %s: expected check error %q, got OK %q", rt.Kind, tc.name, tc.wantCheckError, chkOut)
					}
					if !strings.Contains(chkOut, tc.wantCheckError) {
						t.Fatalf("[%s] %s: expected check error containing %q, got %q", rt.Kind, tc.name, tc.wantCheckError, chkOut)
					}
					return
				}
				if chkFail {
					t.Fatalf("[%s] %s: check %q failed unexpectedly: %s", rt.Kind, tc.name, tc.checkExpr, chkOut)
				}
				if chkOut != tc.wantCheck {
					t.Fatalf("[%s] %s: check %q = %q, want %q", rt.Kind, tc.name, tc.checkExpr, chkOut, tc.wantCheck)
				}
			}
		})
	}
}

// upstream: packages/coding-agent/src/eval/js/shared/rewrite-imports.ts (wrapCode, diagnoseCellSyntaxError)
// The 40 adversarial cases from the independent omp oracle harness, with omp's
// real output/error as the expectation, run under both bun and node 22+.
func TestJSOracleAdversarial40(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping oracle 40 test")
	}

	cases := []jsOracleCase{
		{
			name:      "adv_01_async_gen_decl_followed_by_array",
			code:      "async function* ag() { yield 1; yield 2 }\n[typeof ag, ag.constructor.name].join(':')",
			wantEcho:  "function:AsyncGeneratorFunction",
			checkExpr: "typeof ag",
			wantCheck: "function",
		},
		{
			name:      "adv_02_const_obj_literal_followed_by_paren_no_asi",
			code:      "const obj1 = { a: 1 }\n(1 + 2)",
			wantError: "TypeError",
		},
		{
			name:      "adv_03_const_obj_literal_followed_by_bracket_no_asi",
			code:      "const obj2 = { a: 7 }\n['a']",
			checkExpr: "typeof obj2",
			wantCheck: "number",
		},
		{
			name:      "adv_04_const_obj_literal_followed_by_plus_no_asi",
			code:      "const obj3 = { valueOf() { return 10 } }\n+5",
			checkExpr: "typeof obj3",
			wantCheck: "number",
		},
		{
			name:      "adv_05_class_decl_followed_by_minus",
			code:      "class CM { static n = 8 }\n-CM.n",
			wantEcho:  "-8",
			checkExpr: "CM.n",
			wantCheck: "8",
		},
		{
			name:      "adv_06_var_fn_expr_followed_by_paren_iife_no_asi",
			code:      "var fExpr = function(x) { return x * 3 }\n(4)",
			checkExpr: "fExpr",
			wantCheck: "12",
		},
		{
			name:      "adv_07_let_arrow_block_followed_by_template_no_asi",
			code:      "let tagFn = s => s[0] + '!'\n`hi`",
			checkExpr: "typeof tagFn",
			wantCheck: "function",
		},
		{
			name:      "adv_08_let_destructure_obj_followed_by_slash_regex",
			code:      "let { x: dx } = { x: 'abc' }\n/b/.test(dx)",
			wantError: "SyntaxError",
		},
		{
			name:      "adv_09_postfix_inc_on_newline_prefix_next",
			code:      "let a = 1\nlet b = 10\na\n++b;\n`${a},${b}`",
			wantEcho:  "1,11",
			checkExpr: "`${a},${b}`",
			wantCheck: "1,11",
		},
		{
			name:      "adv_10_postfix_dec_on_newline_prefix_next",
			code:      "let a = 5\nlet b = 20\na\n--b;\n`${a},${b}`",
			wantEcho:  "5,19",
			checkExpr: "`${a},${b}`",
			wantCheck: "5,19",
		},
		{
			name:      "adv_11_inc_at_start_of_trailing_expr",
			code:      "let c = 41\n++c",
			wantEcho:  "42",
			checkExpr: "c",
			wantCheck: "42",
		},
		{
			name:      "adv_12_restricted_return_newline_inside_fn",
			code:      "function rnl() {\n  return\n  42\n}\nString(rnl())",
			wantEcho:  "undefined",
			checkExpr: "String(rnl())",
			wantCheck: "undefined",
		},
		{
			name:      "adv_13_top_level_bare_return_newline_expr",
			code:      "const rv = 7\nreturn\nrv + 1",
			checkExpr: "rv",
			wantCheck: "7",
		},
		{
			name:      "adv_14_top_level_return_value_after_const",
			code:      "const rk = 5\nreturn rk * 2",
			wantEcho:  "10",
			checkExpr: "rk",
			wantCheck: "5",
		},
		{
			name:      "adv_15_break_label_newline_in_loop",
			code:      "let bl = 0\nouter: for (let i = 0; i < 3; i++) {\n  bl = i\n  break\n  outer\n}\nbl",
			wantEcho:  "0",
			checkExpr: "bl",
			wantCheck: "0",
		},
		{
			name:      "adv_16_async_as_identifier_and_call",
			code:      "function async(x) { return x + 1 }\nasync(41)",
			wantEcho:  "42",
			checkExpr: "async(9)",
			wantCheck: "10",
		},
		{
			name:      "adv_17_async_as_var_followed_by_function_newline",
			code:      "const async = 10\nfunction fAfterAsync() { return async + 5 }\nfAfterAsync()",
			wantEcho:  "15",
			checkExpr: "`${async}:${fAfterAsync()}`",
			wantCheck: "10:15",
		},
		{
			name:      "adv_18_let_as_identifier_in_sloppy_mode",
			code:      "var let = 123\nlet + 1",
			wantEcho:  "124",
			checkExpr: "let",
			wantCheck: "123",
		},
		{
			name:      "adv_19_let_assignment_in_sloppy_mode",
			code:      "var let = 1\nlet = 9\nlet",
			wantEcho:  "9",
			checkExpr: "let",
			wantCheck: "9",
		},
		{
			name:      "adv_20_of_get_set_static_as_identifiers",
			code:      "const of = 1, get = 2, set = 3, static = 4\nof + get + set + static",
			wantEcho:  "10",
			checkExpr: "`${of},${get},${set},${static}`",
			wantCheck: "1,2,3,4",
		},
		{
			name:      "adv_21_bare_block_with_label_not_object_literal",
			code:      "{ a: 1 }",
			checkExpr: "typeof a",
			wantCheck: "undefined",
		},
		{
			name:      "adv_22_bare_block_with_var_followed_by_obj_in_parens",
			code:      "{ var bv1 = 3 }\n({ k: bv1 }).k",
			wantEcho:  "3",
			checkExpr: "bv1",
			wantCheck: "3",
		},
		{
			name:      "adv_23_async_block_with_two_semicolonless_vars",
			code:      "if (true) { var nv = 1\nvar nv2 = 2 }\nawait null\nnv + nv2",
			wantEcho:  "3",
			checkExpr: "`${nv}:${nv2}`",
			wantCheck: "1:2",
		},
		{
			name:      "adv_24_labelled_function_declaration",
			code:      "lbl: function lf() { return 55 }\nlf()",
			wantEcho:  "55",
			checkExpr: "typeof lf",
			wantCheck: "function",
		},
		{
			name:      "adv_25_for_const_of_without_braces_followed_by_expr",
			code:      "let tot = 0\nfor (const x of [1, 2, 3]) tot += x\ntot",
			wantEcho:  "6",
			checkExpr: "tot",
			wantCheck: "6",
		},
		{
			name:      "adv_26_for_braceless_var_inside_async",
			code:      "for (let i = 0; i < 2; i++) var leaked = i\nawait null\nleaked",
			wantEcho:  "1",
			checkExpr: "leaked",
			wantCheck: "1",
		},
		{
			name:      "adv_27_for_const_of_braceless_followed_by_array_next_line",
			code:      "let s = ''\nfor (const c of ['a', 'b']) s += c\n[s, 'z'].join('-')",
			wantError: "TypeError",
		},
		{
			name:      "adv_28_nested_template_with_braces_and_regex",
			code:      "const r = `a-${`${({ k: /\\}/.test('}') ? 'Y' : 'N' }).k}`}-b`;\nconst afterT = 4;\n`${r}:${afterT}`",
			wantEcho:  "a-Y-b:4",
			checkExpr: "const afterT = 5; `${r}:${afterT}`",
			wantCheck: "a-Y-b:5",
		},
		{
			name:      "adv_29_template_with_regex_containing_backtick_and_brace",
			code:      "const tr = `${/`|\\}/.test('`')}`;\nconst tr2 = 9;\n`${tr}:${tr2}`",
			wantEcho:  "true:9",
			checkExpr: "const tr2 = 10; `${tr}:${tr2}`",
			wantCheck: "true:10",
		},
		{
			name:      "adv_30_class_private_method_and_in_brand_check",
			code:      "class Priv {\n  #v = 10;\n  #double() { return this.#v * 2; }\n  static has(o) { return #v in o; }\n  val() { return this.#double(); }\n}\nconst p = new Priv();\n`${Priv.has(p)}:${p.val()}`",
			wantEcho:  "true:20",
			checkExpr: "`${Priv.has(p)}:${Priv.has({})}:${p.val()}`",
			wantCheck: "true:false:20",
		},
		{
			name:      "adv_31_async_class_private_method_persists",
			code:      "class APriv {\n  #n = 7;\n  async #get() { return await Promise.resolve(this.#n); }\n  run() { return this.#get(); }\n}\nconst ap = new APriv();\nawait ap.run()",
			wantEcho:  "7",
			checkExpr: "await ap.run()",
			wantCheck: "7",
		},
		{
			name:      "adv_32_optional_chaining_with_template_is_syntax_error",
			code:      "const fn = x => x\nfn?.`abc`",
			wantError: "SyntaxError",
		},
		{
			name:      "adv_33_optional_chaining_call_with_template_arg",
			code:      "const o = { f(s) { return s.toUpperCase() } }\no?.f?.(`hi${1 + 1}`)",
			wantEcho:  "HI2",
			checkExpr: "typeof o.f",
			wantCheck: "function",
		},
		{
			name:      "adv_34_string_with_block_comment_close_and_open",
			code:      "const s1 = '/* not a comment */';\n/* real comment */\nconst s2 = '*/';\n`${s1}|${s2}`",
			wantEcho:  "/* not a comment */|*/",
			checkExpr: "const s1 = 'ok'; const s2 = 'ok2'; `${s1}|${s2}`",
			wantCheck: "ok|ok2",
		},
		{
			name:           "adv_35_html_like_comments_in_script",
			code:           "const hc = 1\n<!-- html open comment with const fake = 999\n--> html close comment\nconst hc2 = hc + 2\nhc2",
			wantEcho:       "3",
			checkExpr:      "`${hc}:${hc2}:${typeof fake}`",
			wantCheckError: "ReferenceError",
		},
		{
			name:      "adv_36_unicode_escape_identifier_persistence",
			code:      "const \\u0061bc = 77\n\\u0061bc + 3",
			wantEcho:  "80",
			checkExpr: "abc",
			wantCheck: "77",
		},
		{
			name:      "adv_37_unicode_escape_func_and_class_in_async_cell",
			code:      "function \\u0066n() { return 5 }\nclass \\u0043lsA { static v = 9 }\nawait Promise.resolve(\\u0066n() + \\u0043lsA.v)",
			wantEcho:  "14",
			checkExpr: "fn() + ClsA.v",
			wantCheck: "14",
		},
		{
			name:      "adv_38_using_declaration_at_top_level",
			code:      "let disposed = false\nusing r = { [Symbol.dispose]() { disposed = true } }\ndisposed",
			wantError: "SyntaxError",
		},
		{
			name:      "adv_39_top_level_await_in_destructuring_decl_and_template",
			code:      "const { a: ta1, b: [ta2] } = await Promise.resolve({ a: 3, b: [4] });\n`sum=${ta1 + (await Promise.resolve(ta2))}`",
			wantEcho:  "sum=7",
			checkExpr: "`${ta1}:${ta2}`",
			wantCheck: "3:4",
		},
		{
			name:      "adv_40_top_level_for_await_and_binding_mutation",
			code:      "let acc = 0\nfunction inc() { acc += 10 }\nfor await (const v of [Promise.resolve(1), Promise.resolve(2)]) acc += v\ninc()\nacc",
			wantEcho:  "13",
			checkExpr: "inc(); acc",
			wantCheck: "23",
		},
	}

	if len(cases) != 40 {
		t.Fatalf("expected 40 adversarial cases, got %d", len(cases))
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			runJSOracleCases(t, rt, cwd, cases)
		})
	}
}

// upstream: packages/coding-agent/src/eval/js/shared/rewrite-imports.ts (wrapCode, diagnoseCellSyntaxError),
//
//	packages/coding-agent/src/eval/js/shared/runtime.ts:540-599,
//	packages/coding-agent/src/eval/js/shared/local-module-loader.ts
//
// 80 additional adversarial cases covering TypeScript syntax, static & dynamic
// imports, export statements, top-level await, top-level return, directives,
// labels, getters/setters, parser-plugin boundaries (decorators/JSX), and wide
// Unicode syntax-error code frames — verified against omp's real pipeline under
// bun and run under both bun and node 22+.
func TestJSOracleAdversarialExtra80(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping extra 80 oracle test")
	}

	cases := []jsOracleCase{
		{
			name:      "ts_01_primitive_type_annotations",
			code:      "const n: number = 10;\nlet s: string = 'hi';\nconst b: boolean = true;\n`${n}:${s}:${b}`",
			wantEcho:  "10:hi:true",
			checkExpr: "`${n}:${s}:${b}`",
			wantCheck: "10:hi:true",
		},
		{
			name:      "ts_02_interface_and_typed_object",
			code:      "interface User { id: number; name: string; active?: boolean }\nconst u: User = { id: 7, name: 'ada' };\n`${u.id}:${u.name}`",
			wantEcho:  "7:ada",
			checkExpr: "u.name",
			wantCheck: "ada",
		},
		{
			name:      "ts_03_type_alias_union_intersection",
			code:      "type ID = string | number;\ntype Named = { name: string };\ntype Entity = Named & { id: ID };\nconst e: Entity = { id: 42, name: 'x' };\n`${e.id}-${e.name}`",
			wantEcho:  "42-x",
			checkExpr: "e.id",
			wantCheck: "42",
		},
		{
			name:      "ts_04_numeric_enum_with_annotation",
			code:      "enum Direction { Up = 1, Down, Left = 10, Right }\nconst d: Direction = Direction.Right;\n`${Direction.Up}:${Direction.Down}:${d}`",
			wantEcho:  "1:2:11",
			checkExpr: "Direction.Left",
			wantCheck: "10",
		},
		{
			name:      "ts_05_string_enum_with_annotation",
			code:      "enum Mode { Fast = 'FAST', Safe = 'SAFE' }\nconst m: Mode = Mode.Safe\nm",
			wantEcho:  "SAFE",
			checkExpr: "Mode.Fast",
			wantCheck: "FAST",
		},
		{
			name:      "ts_06_const_enum_with_annotation",
			code:      "const enum Bits { A = 1, B = 2, C = 4 }\nconst mask: number = Bits.A | Bits.C\nmask",
			wantEcho:  "5",
			checkExpr: "mask",
			wantCheck: "5",
		},
		{
			name:      "ts_07_generic_function_declaration",
			code:      "function identity<T>(x: T): T { return x }\nidentity<number>(123) + 1",
			wantEcho:  "124",
			checkExpr: "identity('ok')",
			wantCheck: "ok",
		},
		{
			name:      "ts_08_generic_constrained_arrow_function",
			code:      "const getLen = <T extends { length: number }>(v: T): number => v.length\ngetLen<string>('hello')",
			wantEcho:  "5",
			checkExpr: "getLen([1, 2, 3])",
			wantCheck: "3",
		},
		{
			name:      "ts_09_generic_class_parameter_properties",
			code:      "class Box<T> { constructor(public readonly value: T, private tag: string = 'box') {} describe(): string { return `${this.tag}:${this.value}` } }\nconst bx = new Box<number>(55)\nbx.describe()",
			wantEcho:  "box:55",
			checkExpr: "bx.value",
			wantCheck: "55",
		},
		{
			name:      "ts_10_as_type_assertion",
			code:      "const raw: unknown = 'hello';\n(raw as string).toUpperCase()",
			wantEcho:  "HELLO",
			checkExpr: "raw",
			wantCheck: "hello",
		},
		{
			name:      "ts_11_as_const_assertion",
			code:      "const tuple = [1, 'two', true] as const\ntuple.join(',')",
			wantEcho:  "1,two,true",
			checkExpr: "tuple.length",
			wantCheck: "3",
		},
		{
			name:      "ts_12_satisfies_operator",
			code:      "type Palette = Record<string, [number, number, number]>\nconst pal = { red: [255, 0, 0], blue: [0, 0, 255] } satisfies Palette\npal.red[0]",
			wantEcho:  "255",
			checkExpr: "pal.blue[2]",
			wantCheck: "255",
		},
		{
			name:      "ts_13_non_null_assertion_and_optional",
			code:      "function firstChar(s: string | null): string { return s!.slice(0, 1) }\nfirstChar('zebra')",
			wantEcho:  "z",
			checkExpr: "firstChar('alpha')",
			wantCheck: "a",
		},
		{
			name:      "ts_14_function_overloads",
			code:      "function fmt(x: number): string;\nfunction fmt(x: string): string;\nfunction fmt(x: number | string): string { return typeof x === 'number' ? `#${x}` : x.toUpperCase() }\n`${fmt(5)}:${fmt('ab')}`",
			wantEcho:  "#5:AB",
			checkExpr: "fmt(9)",
			wantCheck: "#9",
		},
		{
			name:      "ts_15_abstract_class_and_implements",
			code:      "interface Measurable { size(): number }\nabstract class Shape implements Measurable { abstract size(): number }\nclass Square extends Shape { constructor(public side: number) { super() } size(): number { return this.side * this.side } }\nnew Square(6).size()",
			wantEcho:  "36",
			checkExpr: "new Square(4).size()",
			wantCheck: "16",
		},
		{
			name:      "ts_16_declare_statement_stripped",
			code:      "declare const __never_defined__: number;\nconst okVal: number = 88\nokVal",
			wantEcho:  "88",
			checkExpr: "typeof __never_defined__",
			wantCheck: "undefined",
		},
		{
			name:      "ts_17_type_only_import_stripped",
			code:      "import type { NonExistentType } from './does-not-exist-at-all';\nconst kept: number = 17\nkept",
			wantEcho:  "17",
			checkExpr: "kept",
			wantCheck: "17",
		},
		{
			name:      "ts_18_inline_type_import_specifier_stripped",
			code:      "import { type Pair, sum } from './tsmod.ts';\nconst p: Pair = { a: 4, b: 9 }\nsum(p)",
			wantEcho:  "13",
			checkExpr: "sum({ a: 1, b: 2 })",
			wantCheck: "3",
		},
		{
			name:      "ts_19_async_ts_with_top_level_await",
			code:      "interface Res { n: number }\nconst r: Res = await Promise.resolve({ n: 21 } as Res)\nconst doubled: number = r.n * 2\ndoubled",
			wantEcho:  "42",
			checkExpr: "doubled",
			wantCheck: "42",
		},
		{
			name:      "ts_20_ts_top_level_return_with_annotations",
			code:      "const base: number = 30\nreturn (base + 12) as number",
			wantEcho:  "42",
			checkExpr: "base",
			wantCheck: "30",
		},
		{
			name:      "ts_21_ts_namespace_with_annotation",
			code:      "namespace Calc { export const k: number = 9; export function inc(x: number): number { return x + k } }\nCalc.inc(3)",
			wantEcho:  "12",
			checkExpr: "Calc.k",
			wantCheck: "9",
		},
		{
			name:      "ts_22_bare_enum_without_looks_like_ts_hint",
			code:      "enum Bare { X = 1, Y = 2 }\nBare.Y",
			wantError: "SyntaxError",
		},
		{
			name:      "imp_01_builtin_named_import",
			code:      "import { join } from 'node:path'\njoin('a', 'b')",
			wantEcho:  "a/b",
			checkExpr: "typeof join",
			wantCheck: "function",
		},
		{
			name:      "imp_02_builtin_named_rename_import",
			code:      "import { join as pathJoin, basename as base } from 'node:path'\nbase(pathJoin('x', 'y.txt'))",
			wantEcho:  "y.txt",
			checkExpr: "typeof pathJoin",
			wantCheck: "function",
		},
		{
			name:      "imp_03_builtin_namespace_import",
			code:      "import * as pathNs from 'node:path'\npathNs.basename('/foo/bar.js')",
			wantEcho:  "bar.js",
			checkExpr: "typeof pathNs.join",
			wantCheck: "function",
		},
		{
			name:      "imp_04_builtin_default_import",
			code:      "import pathDef from 'node:path'\npathDef.extname('file.ts')",
			wantEcho:  ".ts",
			checkExpr: "typeof pathDef.resolve",
			wantCheck: "function",
		},
		{
			name:      "imp_05_local_default_and_named_import",
			code:      "import defName, { PI, add as sumFn } from './math.mjs'\n`${defName}:${sumFn(PI, 4)}`",
			wantEcho:  "math-default:7",
			checkExpr: "sumFn(10, 20)",
			wantCheck: "30",
		},
		{
			name:      "imp_06_local_default_and_namespace_import",
			code:      "import defM, * as mathNs from './math.mjs'\n`${defM}:${mathNs.mul(3, 5)}`",
			wantEcho:  "math-default:15",
			checkExpr: "mathNs.PI",
			wantCheck: "3",
		},
		{
			name:      "imp_07_local_side_effect_only_import",
			code:      "import './side-effect.mjs'\nglobalThis.__sideEffectCount >= 1",
			wantEcho:  "true",
			checkExpr: "globalThis.__sideEffectCount >= 1",
			wantCheck: "true",
		},
		{
			name:      "imp_08_local_json_import_with_attribute",
			code:      "import cfg from './data.json' with { type: 'json' }\n`${cfg.version}:${cfg.tag}`",
			wantEcho:  "7:ok",
			checkExpr: "cfg.version",
			wantCheck: "7",
		},
		{
			name:      "imp_09_local_string_named_import",
			code:      "import { 'kebab-key' as kebab } from './string-export.mjs'\nkebab",
			wantError: "SyntaxError",
		},
		{
			name:      "imp_10_static_import_after_statement_mid_cell",
			code:      "const pre = 10\nimport { add } from './math.mjs'\nadd(pre, 5)",
			wantEcho:  "15",
			checkExpr: "add(1, 2)",
			wantCheck: "3",
		},
		{
			name:      "imp_11_static_import_at_end_of_cell",
			code:      "const beforeImp = 3\nimport { mul } from './math.mjs'",
			checkExpr: "mul(beforeImp, 4)",
			wantCheck: "12",
		},
		{
			name:      "imp_12_import_text_inside_string_and_template_not_rewritten",
			code:      "const fake1 = \"import x from 'nope'\";\nconst fake2 = `import y from 'nope2'`;\n`${fake1.startsWith('import')}:${fake2.startsWith('import')}`",
			wantEcho:  "true:true",
			checkExpr: "fake1.length > 0",
			wantCheck: "true",
		},
		{
			name:      "imp_13_dynamic_import_variable_specifier",
			code:      "const spec = './math.mjs'\nconst mod = await import(spec)\nmod.add(7, 8)",
			wantEcho:  "15",
			checkExpr: "mod.default",
			wantCheck: "math-default",
		},
		{
			name:      "imp_14_local_ts_module_static_import",
			code:      "import defTs, { sum } from './tsmod.ts'\n`${defTs}:${sum({ a: 2, b: 3 })}`",
			wantEcho:  "99:5",
			checkExpr: "defTs",
			wantCheck: "99",
		},
		{
			name:      "exp_01_export_const_declaration",
			code:      "export const exportedX = 1\nexportedX",
			wantError: "SyntaxError",
		},
		{
			name:      "exp_02_export_default_expression",
			code:      "export default 42",
			wantError: "SyntaxError",
		},
		{
			name:      "exp_03_export_named_list",
			code:      "const exA = 1\nexport { exA }",
			wantError: "SyntaxError",
		},
		{
			name:      "exp_04_export_function_declaration",
			code:      "export function exFn() { return 5 }\nexFn()",
			wantError: "SyntaxError",
		},
		{
			name:      "exp_05_export_type_stripped_by_ts",
			code:      "type Foo = { a: number }\nexport type { Foo }\nconst afterExpType: number = 9\nafterExpType",
			wantEcho:  "9",
			checkExpr: "afterExpType",
			wantCheck: "9",
		},
		{
			name:      "exp_06_export_all_from_module",
			code:      "export * from './math.mjs'",
			wantError: "SyntaxError",
		},
		{
			name:      "await_01_await_in_try_catch_finally",
			code:      "let caught = ''\ntry { await Promise.reject(new Error('nope')) } catch (e) { caught = e.message } finally { caught += '!' }\ncaught",
			wantEcho:  "nope!",
			checkExpr: "caught",
			wantCheck: "nope!",
		},
		{
			name:      "await_02_await_in_while_loop",
			code:      "let n = 0\nconst items = [1, 2, 3]\nwhile (items.length) { n += await Promise.resolve(items.pop()) }\nn",
			wantEcho:  "6",
			checkExpr: "n",
			wantCheck: "6",
		},
		{
			name:      "await_03_await_in_switch_discriminant_and_case",
			code:      "let swRes = ''\nswitch (await Promise.resolve('b')) { case 'a': swRes = 'A'; break; case 'b': swRes = await Promise.resolve('B'); break }\nswRes",
			wantEcho:  "B",
			checkExpr: "swRes",
			wantCheck: "B",
		},
		{
			name:      "await_04_await_in_object_computed_key_and_shorthand",
			code:      "const objA = { [await Promise.resolve('k')]: await Promise.resolve(77) }\nobjA.k",
			wantEcho:  "77",
			checkExpr: "objA.k",
			wantCheck: "77",
		},
		{
			name:      "await_05_await_in_array_spread_and_default_destructure",
			code:      "const [firstA = await Promise.resolve(9), ...restA] = [undefined, ...(await Promise.resolve([1, 2]))];\n`${firstA}:${restA.join(',')}`",
			wantEcho:  "9:1,2",
			checkExpr: "firstA + restA.length",
			wantCheck: "11",
		},
		{
			name:      "await_06_await_in_class_extends_and_computed_method",
			code:      "class BaseA { base() { return 10 } }\nclass ChildA extends (await Promise.resolve(BaseA)) { [await Promise.resolve('go')]() { return this.base() + 5 } }\nnew ChildA().go()",
			wantEcho:  "15",
			checkExpr: "new ChildA().go()",
			wantCheck: "15",
		},
		{
			name:      "await_07_await_in_logical_assignment_and_nullish",
			code:      "let la = null\nla ??= await Promise.resolve(33)\nla",
			wantEcho:  "33",
			checkExpr: "la",
			wantCheck: "33",
		},
		{
			name:      "await_08_nested_var_in_catch_redeclaring_catch_param_annex_b",
			code:      "try { throw 'boom' } catch (ce) { var ce = 'reassigned'; var ceCopy = ce }\nawait null;\n`${typeof ce}:${ceCopy}`",
			wantEcho:  "undefined:reassigned",
			checkExpr: "`${typeof ce}:${ceCopy}`",
			wantCheck: "undefined:reassigned",
		},
		{
			name:      "await_09_for_in_var_in_async_cell",
			code:      "const keysSeen = []\nfor (var fk in { a: 1, b: 2 }) { keysSeen.push(await Promise.resolve(fk)) }\n`${keysSeen.join(',')}:${fk}`",
			wantEcho:  "a,b:b",
			checkExpr: "fk",
			wantCheck: "b",
		},
		{
			name:      "await_10_assignment_to_persisted_var_inside_nested_closure_in_async_cell",
			code:      "let sharedN = 1\nconst bump = () => ++sharedN\nawait Promise.resolve()\nbump()\nsharedN",
			wantEcho:  "2",
			checkExpr: "bump(); sharedN",
			wantCheck: "3",
		},
		{
			name:      "ret_01_trailing_return_value",
			code:      "const rx = 20\nreturn rx + 22",
			wantEcho:  "42",
			checkExpr: "rx",
			wantCheck: "20",
		},
		{
			name:      "ret_02_trailing_return_no_arg",
			code:      "const rx2 = 5\nreturn",
			checkExpr: "rx2",
			wantCheck: "5",
		},
		{
			name:      "ret_03_early_return_inside_if_skips_trailing_expr",
			code:      "const rx3 = 1\nif (rx3 === 1) return 'early'\n'late'",
			wantEcho:  "early",
			checkExpr: "rx3",
			wantCheck: "1",
		},
		{
			name:      "ret_04_early_return_not_taken_falls_through_to_trailing_expr",
			code:      "const rx4 = 2\nif (rx4 === 1) return 'early'\n'fell-through'",
			wantEcho:  "fell-through",
			checkExpr: "rx4",
			wantCheck: "2",
		},
		{
			name:      "ret_05_return_inside_try_with_finally_mutation",
			code:      "let finFlag = false\ntry { return 'from-try' } finally { finFlag = true }",
			wantEcho:  "from-try",
			checkExpr: "finFlag",
			wantCheck: "true",
		},
		{
			name:      "ret_06_return_promise_value_awaited",
			code:      "return Promise.resolve('resolved-ret')",
			wantEcho:  "resolved-ret",
			checkExpr: "1",
			wantCheck: "1",
		},
		{
			name:      "ret_07_return_undefined_prints_nothing",
			code:      "const rUndef = 3\nreturn undefined",
			checkExpr: "rUndef",
			wantCheck: "3",
		},
		{
			name:      "ret_08_return_object_literal_multiline",
			code:      "return {\n  a: 1,\n  b: 2\n}",
			wantEcho:  "{ a: 1, b: 2 }",
			checkExpr: "1",
			wantCheck: "1",
		},
		{
			name:           "dir_01_use_strict_then_binding_and_expr",
			code:           "'use strict';\nconst ds = 11\nds + 1",
			wantEcho:       "12",
			checkExpr:      "ds",
			wantCheckError: "ReferenceError",
		},
		{
			name:      "dir_02_use_strict_only_cell",
			code:      "'use strict';",
			wantEcho:  "use strict",
			checkExpr: "1",
			wantCheck: "1",
		},
		{
			name:      "dir_03_multiple_string_directives_only",
			code:      "'first';\n'second';",
			wantEcho:  "second",
			checkExpr: "1",
			wantCheck: "1",
		},
		{
			name:      "dir_04_use_strict_rejects_octal_or_undeclared_assign",
			code:      "'use strict';\nundeclaredStrictVar = 1",
			wantError: "ReferenceError",
		},
		{
			name:      "dir_05_use_strict_with_top_level_await",
			code:      "'use strict';\nconst dsa = await Promise.resolve(19)\ndsa",
			wantEcho:  "19",
			checkExpr: "dsa",
			wantCheck: "19",
		},
		{
			name:      "lbl_01_continue_outer_label_in_nested_loops",
			code:      "let hits = 0\nouter: for (let i = 0; i < 3; i++) {\n  for (let j = 0; j < 3; j++) {\n    if (j === 1) continue outer\n    hits++\n  }\n}\nhits",
			wantEcho:  "3",
			checkExpr: "hits",
			wantCheck: "3",
		},
		{
			name:      "lbl_02_nested_labelled_blocks_break_outer",
			code:      "let trace = ''\nA: {\n  trace += '1'\n  B: {\n    trace += '2'\n    break A\n  }\n  trace += '3'\n}\ntrace",
			wantEcho:  "12",
			checkExpr: "trace",
			wantCheck: "12",
		},
		{
			name:      "lbl_03_labelled_statement_as_last_statement_suppressed",
			code:      "let llast = 0\nmyLbl: llast = 99",
			checkExpr: "llast",
			wantCheck: "99",
		},
		{
			name:      "lbl_04_labelled_async_loop_with_await",
			code:      "let laSum = 0\nscan: for (let i = 1; i <= 5; i++) {\n  laSum += await Promise.resolve(i)\n  if (i === 3) break scan\n}\nlaSum",
			wantEcho:  "6",
			checkExpr: "laSum",
			wantCheck: "6",
		},
		{
			name:      "lbl_05_break_outside_loop_diagnosed_syntax_error",
			code:      "break;",
			wantError: "SyntaxError",
		},
		{
			name:      "gs_01_object_literal_getter_and_setter",
			code:      "const boxGS = { _v: 1, get v() { return this._v * 10 }, set v(x) { this._v = x } }\nboxGS.v = 4\nboxGS.v",
			wantEcho:  "40",
			checkExpr: "boxGS._v",
			wantCheck: "4",
		},
		{
			name:      "gs_02_class_static_and_instance_getter_setter",
			code:      "class Meter {\n  static #scale = 2;\n  #raw = 5;\n  static get scale() { return this.#scale }\n  static set scale(s) { this.#scale = s }\n  get value() { return this.#raw * Meter.scale }\n  set value(v) { this.#raw = v }\n}\nconst m = new Meter()\nMeter.scale = 3\nm.value = 7\nm.value",
			wantEcho:  "21",
			checkExpr: "m.value",
			wantCheck: "21",
		},
		{
			name:      "gs_03_computed_getter_setter_names",
			code:      "const propKey = 'dyn'\nconst objGS = { _d: 0, get [propKey]() { return this._d + 1 }, set [propKey](n) { this._d = n } }\nobjGS.dyn = 8\nobjGS.dyn",
			wantEcho:  "9",
			checkExpr: "objGS.dyn",
			wantCheck: "9",
		},
		{
			name:      "gs_04_async_cell_class_with_getters_persists",
			code:      "class AsyncGS { constructor(v) { this._v = v } get doubled() { return this._v * 2 } }\nconst ags = new AsyncGS(await Promise.resolve(11))\nags.doubled",
			wantEcho:  "22",
			checkExpr: "ags.doubled",
			wantCheck: "22",
		},
		{
			name:      "gs_05_object_getter_named_get_and_set",
			code:      "const weird = { get get() { return 'G' }, get set() { return 'S' } };\n`${weird.get}:${weird.set}`",
			wantEcho:  "G:S",
			checkExpr: "weird.get",
			wantCheck: "G",
		},
		{
			name:      "plug_01_class_decorator_not_enabled_in_babel_plugins",
			code:      "function dec(v) { return v }\n@dec\nclass Decorated {}",
			wantError: "SyntaxError",
		},
		{
			name:      "plug_02_jsx_element_not_enabled_in_babel_plugins",
			code:      "const el = <div>hello</div>\nel",
			wantError: "SyntaxError",
		},
		{
			name:      "plug_03_ts_angle_bracket_type_assertion_works_because_jsx_disabled",
			code:      "const x: unknown = 50\nconst y: number = <number>x + 5\ny",
			wantEcho:  "55",
			checkExpr: "y",
			wantCheck: "55",
		},
		{
			name:      "plug_04_syntax_error_with_wide_unicode_caret_alignment",
			code:      "const 汉 = ;",
			wantError: "SyntaxError",
		},
		{
			name:      "plug_05_runtime_syntax_error_not_replaced_by_babel_diagnosis",
			code:      "JSON.parse('{')",
			wantError: "SyntaxError",
		},
	}

	if len(cases) != 80 {
		t.Fatalf("expected 80 extra adversarial cases, got %d", len(cases))
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			// Write local import fixtures matching the oracle harness.
			fixtures := map[string]string{
				"math.mjs":          "export const PI = 3;\nexport const add = (a, b) => a + b;\nexport const mul = (a, b) => a * b;\nexport default \"math-default\";\n",
				"side-effect.mjs":   "globalThis.__sideEffectCount = (globalThis.__sideEffectCount || 0) + 1;\nexport const touched = true;\n",
				"data.json":         `{"version":7,"tag":"ok"}`,
				"tsmod.ts":          "export interface Pair { a: number; b: number }\nexport const sum = (p: Pair): number => p.a + p.b;\nexport default 99 as number;\n",
				"string-export.mjs": "const val = \"kebab-ok\";\nexport { val as \"kebab-key\" };\n",
			}
			for name, body := range fixtures {
				if err := os.WriteFile(filepath.Join(cwd, name), []byte(body), 0o600); err != nil {
					t.Fatalf("write fixture %s: %v", name, err)
				}
			}
			runJSOracleCases(t, rt, cwd, cases)
		})
	}
}

// upstream: packages/coding-agent/test/eval/js-cell-syntax-error.test.ts: "aligns the caret by terminal width after wide characters"
func TestJSCellSyntaxErrorDiagnosisAndCaretAlignment(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping syntax error diagnosis test")
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			k, err := startJSKernelWithRuntime(context.Background(), rt, cwd, "diag-"+rt.Kind, nil, nil)
			if err != nil {
				t.Fatalf("start %s kernel: %v", rt.Kind, err)
			}
			defer k.shutdown()

			execErr := func(code string) string {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				res, err := k.exec(ctx, "d-"+newRandomID(), code, "diag.js", cwd, nil, nil, "diag-"+rt.Kind, nil, nil, nil)
				if err != nil {
					t.Fatalf("%s exec error: %v", rt.Kind, err)
				}
				if res.ExitCode == nil || *res.ExitCode == 0 {
					t.Fatalf("%s expected error for %q, got ok: %q", rt.Kind, code, res.Output)
				}
				return res.Output
			}

			// Wide-character caret alignment (11 spaces before ^).
			wideOut := execErr("const 汉 = ;")
			wantWide := "SyntaxError: Unexpected token (line 1, column 11)\n1 | const 汉 = ;\n  | " + strings.Repeat(" ", 11) + "^"
			if !strings.Contains(wideOut, wantWide) {
				t.Fatalf("[%s] wide caret diagnosis mismatch:\ngot:\n%s\nwant substring:\n%s", rt.Kind, wideOut, wantWide)
			}

			// Recovered Babel error (break; outside loop).
			breakOut := execErr("break;")
			wantBreak := "SyntaxError: Unsyntactic break. (line 1, column 1)\n1 | break;\n  | ^"
			if !strings.Contains(breakOut, wantBreak) {
				t.Fatalf("[%s] recovered error diagnosis mismatch:\ngot:\n%s\nwant substring:\n%s", rt.Kind, breakOut, wantBreak)
			}

			// Runtime SyntaxError (JSON.parse) must NOT be replaced with Babel line/column frame.
			rtOut := execErr(`JSON.parse("{")`)
			if strings.Contains(rtOut, "(line 1, column") {
				t.Fatalf("[%s] runtime JSON.parse SyntaxError should not be replaced by Babel frame: %s", rt.Kind, rtOut)
			}
		})
	}
}

// upstream: packages/coding-agent/src/eval/js/error-text.ts:80-86
// Verifies that hidden internal frames in JS errors append omp's
// `(N omp frame(s) hidden; catch the error and print e.stack to see them)` line.
func TestJSErrorTextIncludesHiddenOmpFramesLine(t *testing.T) {
	runtimes := availableJSRuntimes(t)
	if len(runtimes) == 0 {
		t.Skip("neither bun nor node 22+ found on PATH; skipping JS hidden-frames test")
	}

	for _, rt := range runtimes {
		t.Run(rt.Kind, func(t *testing.T) {
			cwd := t.TempDir()
			k, err := startJSKernelWithRuntime(context.Background(), rt, cwd, "errframes-"+rt.Kind, nil, nil)
			if err != nil {
				t.Fatalf("start %s kernel: %v", rt.Kind, err)
			}
			defer k.shutdown()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Exact match with omp's `eval-js-state-print-error#2`: `throw new Error('js_boom');`
			// must report `(4 omp frames hidden; catch the error and print e.stack to see them)` under Bun
			// (not 6 from extra wrapper / microtask-pump frames).
			res, err := k.exec(ctx, "ef-1", "throw new Error('js_boom');", "js-cell-00000000-0000-4000-8000-000000000001.js", cwd, nil, nil, "errframes-"+rt.Kind, nil, nil, nil)
			if err != nil {
				t.Fatalf("%s exec error: %v", rt.Kind, err)
			}
			if res.ExitCode == nil || *res.ExitCode == 0 {
				t.Fatalf("%s expected error, got ok: %q", rt.Kind, res.Output)
			}
			if !strings.Contains(res.Output, "Error: js_boom") {
				t.Fatalf("[%s] missing error message in output: %q", rt.Kind, res.Output)
			}
			if rt.Kind == "bun" {
				wantLine := "    (4 omp frames hidden; catch the error and print e.stack to see them)"
				if !strings.Contains(res.Output, wantLine) {
					t.Fatalf("[bun] hidden-frames line mismatch:\ngot:\n%s\nwant line:\n%s", res.Output, wantLine)
				}
			} else if !strings.Contains(res.Output, "omp frame") || !strings.Contains(res.Output, "hidden; catch the error and print e.stack to see them)") {
				t.Fatalf("[%s] missing omp hidden-frames footer in error output:\n%s", rt.Kind, res.Output)
			}
			if strings.Contains(res.Output, "omp-js-runner") {
				t.Fatalf("[%s] runner frame leaked into error output:\n%s", rt.Kind, res.Output)
			}
		})
	}
}
