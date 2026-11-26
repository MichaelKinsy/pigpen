package relay

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUpstreamTestsMatrixValid parses UPSTREAM-TESTS.md and verifies that:
//  1. Every upstream case row either starts with `skipped:` (with a non-empty reason)
//     or names a real Go test file and `func Test...(t *testing.T)` that exists in the repo.
//  2. No row is UNMAPPED or references a missing/deleted Go test file or function.
//  3. When a row cites an upstream line number inside a `t.Run(...)` file, the test function
//     actually exists and parses cleanly via go/parser.
func TestUpstreamTestsMatrixValid(t *testing.T) {
	data, err := os.ReadFile("UPSTREAM-TESTS.md")
	if err != nil {
		t.Fatalf("read UPSTREAM-TESTS.md: %v", err)
	}

	// Extension module root is two directories above internal/relay.
	modRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// Cache parsed Test functions per Go file.
	funcCache := map[string]map[string]bool{}
	loadFuncs := func(relPath string) (map[string]bool, error) {
		if m, ok := funcCache[relPath]; ok {
			return m, nil
		}
		absPath := filepath.Join(modRoot, filepath.FromSlash(relPath))
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, absPath, nil, 0)
		if err != nil {
			return nil, err
		}
		m := make(map[string]bool)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				m[fn.Name.Name] = true
			}
		}
		funcCache[relPath] = m
		return m, nil
	}

	rowRe := regexp.MustCompile(`^\|\s*(\d+)\s*\|\s*(.*?)\s*\|\s*(.*?)\s*\|$`)
	goRefRe := regexp.MustCompile("^`([^`]+\\.go)`\\s*::\\s*`(Test[A-Za-z0-9_]+)`")

	lines := strings.Split(string(data), "\n")
	var currentSection string
	totalRows := 0
	portedRows := 0
	skippedRows := 0

	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			currentSection = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		m := rowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		totalRows++
		upstreamLine := m[1]
		title := m[2]
		disp := strings.TrimSpace(m[3])
		if title == "" {
			t.Errorf("%s line %d (row %d): empty upstream title", currentSection, i+1, totalRows)
		}
		if strings.HasPrefix(disp, "skipped:") {
			reason := strings.TrimSpace(strings.TrimPrefix(disp, "skipped:"))
			if reason == "" {
				t.Errorf("%s upstream:%s: skipped without a reason", currentSection, upstreamLine)
			}
			skippedRows++
			continue
		}
		ref := goRefRe.FindStringSubmatch(disp)
		if ref == nil {
			t.Errorf("%s upstream:%s: disposition must be \"`file.go` :: `TestName`\" or \"skipped: <reason>\", got %q", currentSection, upstreamLine, disp)
			continue
		}
		goFile, testName := ref[1], ref[2]
		funcs, err := loadFuncs(goFile)
		if err != nil {
			t.Errorf("%s upstream:%s: failed to parse %s: %v", currentSection, upstreamLine, goFile, err)
			continue
		}
		if !funcs[testName] {
			t.Errorf("%s upstream:%s: test function %s not found in %s", currentSection, upstreamLine, testName, goFile)
			continue
		}
		portedRows++
	}

	if totalRows != 219 {
		t.Errorf("expected 219 total upstream rows across the 10 upstream test files, got %d (ported=%d skipped=%d)", totalRows, portedRows, skippedRows)
	}
	t.Logf("UPSTREAM-TESTS.md verified: total=%d ported=%d skipped=%d", totalRows, portedRows, skippedRows)
}
