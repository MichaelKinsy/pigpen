package ownmodel

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// skipRules name, per upstream test file, why its cases have no Go twin. A case that is
// not claimed by twin()/skipTwin() must match a rule; the reasons are specific to the file.
var skipRules = []struct{ prefix, reason string }{
	{"tests/test_client_with_live_apis.py::", "replays live OpenAI, Anthropic and Gemini calls from vcr cassettes; the Go backend has no provider clients. What these check (prompts and schema shape, answers) is checked by the differential goldens recorded from the oracle (TestEquivalence_GoBackendMatchesThePythonOracle); a live check needs an owner-supplied key"},
	{"tests/test_openai_transports.py::", "OpenAI Responses and Chat Completions transports of the oracle's provider; PiG's model access (package pigmodel) replaces the transport"},
	{"tests/test_gemini_transports.py::", "Gemini Interactions API transport of the oracle's provider; replaced by PiG's model access"},
	{"tests/test_provider_lifecycle.py::", "close/aclose of the provider SDK clients the oracle constructs; the Go backend and pigmodel.Model own no client or connection"},
	{"tests/test_provider_requests.py::", "the exact SDK request arguments of the oracle's OpenAI, Anthropic and Gemini providers; PiG's model access builds the provider request"},
	{"tests/test_provider_retries.py::", "retry budgets inside the provider SDKs' HTTP clients; the transient retry policy here is the shared typesafe.Retry (tested through the Backend)"},
	{"tests/test_provider_nonanswers.py::", "refusals, incomplete generations and output-limit stops in the provider-specific response shapes; PiG's model access reports them as a stop reason, mapped and tested in package pigmodel (TestComplete_StopReasons*)"},
	{"tests/utils/test_error_handling.py::test_status_errors_map_and_preserve_status_and_body", "translation of the OpenAI/Anthropic/Gemini SDK exceptions to SDK errors; a Model returns typesafe errors directly"},
	{"tests/utils/test_error_handling.py::test_timeout_and_connection_errors_map", "translation of provider SDK timeout and connection exceptions; a Model returns typesafe errors directly"},
	{"tests/utils/test_error_handling.py::test_unknown_and_sdk_errors_pass_through", "pass-through rules of the provider SDK exception translators"},
	{"tests/utils/test_error_handling.py::test_translating_context_manager_reraises_translated_error", "the translating() context manager of the provider layer"},
}

func TestEveryUpstreamCaseHasATwinOrANamedSkip(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "port", "twins", "system-one-adapter-python.txt"))
	noErr(t, err)
	defer f.Close()
	var cases []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			cases = append(cases, line)
		}
	}
	ported, skipped := scanTwinClaims(t, ".")
	known := map[string]bool{}
	for _, c := range cases {
		known[c] = true
	}
	var missing, dup, unknown []string
	byRule := map[string]int{}
	nPorted, nNamed := 0, 0
	for _, c := range cases {
		_, isPorted := ported[c]
		_, isSkipped := skipped[c]
		switch {
		case isPorted && isSkipped:
			dup = append(dup, c)
		case isPorted:
			nPorted++
		case isSkipped:
			nNamed++
		default:
			matched := false
			for _, r := range skipRules {
				if strings.HasPrefix(c, r.prefix) {
					byRule[r.prefix]++
					matched = true
					break
				}
			}
			if !matched {
				missing = append(missing, c)
			}
		}
	}
	for id := range ported {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	for id := range skipped {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	if len(missing)+len(dup)+len(unknown) > 0 {
		t.Fatalf("%d cases without a twin or a skip rule:\n%s\nclaimed as both ported and skipped:\n%s\nunknown ids:\n%s",
			len(missing), strings.Join(missing, "\n"), strings.Join(dup, "\n"), strings.Join(unknown, "\n"))
	}
	ruled := 0
	for _, n := range byRule {
		ruled += n
	}
	t.Logf("system-one-adapter-python: %d cases: %d ported twins, %d named skips, %d skipped by file rule", len(cases), nPorted, nNamed, ruled)
}

func scanTwinClaims(t *testing.T, dir string) (ported, skipped map[string]string) {
	t.Helper()
	ported, skipped = map[string]string{}, map[string]string{}
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	noErr(t, err)
	fset := token.NewFileSet()
	consts := map[string]string{}
	var parsed []*ast.File
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		noErr(t, err)
		parsed = append(parsed, f)
		ast.Inspect(f, func(n ast.Node) bool { // string constants used to build ids
			if vs, ok := n.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							s, _ := strconv.Unquote(lit.Value)
							consts[name.Name] = s
						}
					}
				}
			}
			return true
		})
	}
	var eval func(e ast.Expr) string
	eval = func(e ast.Expr) string {
		switch x := e.(type) {
		case *ast.BasicLit:
			s, err := strconv.Unquote(x.Value)
			noErr(t, err)
			return s
		case *ast.Ident:
			s, ok := consts[x.Name]
			if !ok {
				t.Fatalf("unknown constant %s in a twin id", x.Name)
			}
			return s
		case *ast.BinaryExpr:
			return eval(x.X) + eval(x.Y)
		}
		t.Fatalf("twin ids must be string literals, constants or their concatenation, got %T", e)
		return ""
	}
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || (id.Name != "twin" && id.Name != "skipTwin") {
				return true
			}
			args := call.Args[1:]
			reason := ""
			dst := ported
			if id.Name == "skipTwin" {
				reason = eval(args[0])
				if strings.TrimSpace(reason) == "" {
					t.Fatalf("%s: skipTwin needs a reason", fset.Position(call.Pos()))
				}
				args, dst = args[1:], skipped
			}
			for _, a := range args {
				dst[eval(a)] = reason
			}
			return true
		})
	}
	return ported, skipped
}
