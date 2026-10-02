package typesafe

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

// TestEveryUpstreamCaseHasATwinOrANamedSkip proves the 1:1 mapping of the TypeScript SDK
// suite: every case in port/twins/typesafe-sdk-js.txt is claimed by exactly one Go test
// through twin(...) (ported, possibly adapted) or skipTwin(...) (with a reason), and no Go
// test claims a case that does not exist.
func TestEveryUpstreamCaseHasATwinOrANamedSkip(t *testing.T) {
	cases := readCaseList(t, filepath.Join("..", "..", "port", "twins", "typesafe-sdk-js.txt"))
	ported, skipped := scanTwinClaims(t, ".")
	claimed := map[string]int{}
	for id := range ported {
		claimed[id]++
	}
	for id := range skipped {
		claimed[id]++
	}
	var missing, dup, unknown []string
	for _, id := range cases {
		switch claimed[id] {
		case 0:
			missing = append(missing, id)
		case 1:
		default:
			dup = append(dup, id)
		}
	}
	known := map[string]bool{}
	for _, id := range cases {
		known[id] = true
	}
	for id := range claimed {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	if len(missing)+len(dup)+len(unknown) > 0 {
		t.Fatalf("%d upstream cases have no twin:\n%s\nclaimed twice:\n%s\nunknown ids:\n%s",
			len(missing), strings.Join(missing, "\n"), strings.Join(dup, "\n"), strings.Join(unknown, "\n"))
	}
	t.Logf("typesafe-sdk-js: %d cases, %d ported twins, %d named skips", len(cases), len(ported), len(skipped))
}

func readCaseList(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	noErr(t, err)
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	noErr(t, sc.Err())
	return out
}

// scanTwinClaims returns the ids claimed by twin(t, ids...) and skipTwin(t, reason, ids...)
// calls in the package's test files, with skip reasons required to be non-empty.
func scanTwinClaims(t *testing.T, dir string) (ported, skipped map[string]string) {
	t.Helper()
	ported, skipped = map[string]string{}, map[string]string{}
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	noErr(t, err)
	fset := token.NewFileSet()
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		noErr(t, err)
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
			if id.Name == "skipTwin" {
				reason = strLit(t, args[0])
				if strings.TrimSpace(reason) == "" {
					t.Fatalf("%s: skipTwin needs a reason", fset.Position(call.Pos()))
				}
				args = args[1:]
			}
			for _, a := range args {
				key := strLit(t, a)
				dst := ported
				if id.Name == "skipTwin" {
					dst = skipped
				}
				dst[key] = reason
			}
			return true
		})
	}
	return ported, skipped
}

func strLit(t *testing.T, e ast.Expr) string {
	t.Helper()
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		t.Fatalf("twin ids and reasons must be string literals, got %T", e)
	}
	s, err := strconv.Unquote(lit.Value)
	noErr(t, err)
	return s
}
