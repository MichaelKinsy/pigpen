package pi_goal_x

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tempStore is tempCtx of the original storage tests: a fresh working directory.
func tempStore(t *testing.T) storage {
	t.Helper()
	invalidateGoalPoolCache()
	return storage{cwd: t.TempDir()}
}

// gtpMakeGoal is makeGoal of the original tests: createGoal(...) then overrides in key order.
func gtpMakeGoal(at int64, objective string, kv ...any) *jsObject {
	g := createGoal(objective, true, false, false, at)
	for i := 0; i+1 < len(kv); i += 2 {
		g.set(kv[i].(string), kv[i+1])
	}
	return g
}

// with is {...g, k: v, ...}.
func with(g *jsObject, kv ...any) *jsObject {
	c := g.clone()
	for i := 0; i+1 < len(kv); i += 2 {
		c.set(kv[i].(string), kv[i+1])
	}
	return c
}

func mustWrite(t *testing.T, s storage, g *jsObject) *jsObject {
	t.Helper()
	r, err := s.writeActiveGoalFile(g)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustArchive(t *testing.T, s storage, g *jsObject) *jsObject {
	t.Helper()
	r, err := s.archiveGoalFile(g)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readRel(t *testing.T, s storage, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.cwd, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileExistsAt(s storage, rel string) bool {
	_, err := os.ReadFile(filepath.Join(s.cwd, rel))
	return err == nil
}

func matches(t *testing.T, got, re, what string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(got) {
		t.Errorf("%s: %q does not match %s", what, got, re)
	}
}

func contains(t *testing.T, got, want, what string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s: %q lacks %q", what, got, want)
	}
}

func notContains(t *testing.T, got, bad, what string) {
	t.Helper()
	if strings.Contains(got, bad) {
		t.Errorf("%s: %q contains %q", what, got, bad)
	}
}

func hasAnyKey(g *jsObject, k string) bool { return g.has(k) }
