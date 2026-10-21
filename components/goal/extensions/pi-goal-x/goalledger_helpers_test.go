package pi_goal_x

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ledgerTempCtx(t *testing.T) storage { t.Helper(); return storage{cwd: t.TempDir()} }

// createdEv is { type: "goal_created", goalId, objective, sisyphus: false, autoContinue: true, at }.
func createdEv(goalID, objective, at string) *jsObject {
	return ledgerEvent("goal_created", "goalId", goalID, "objective", objective, "sisyphus", false, "autoContinue", true, "at", at)
}

func ledgerEv(typ, goalID, at string, kv ...any) *jsObject {
	return ledgerEvent(typ, append([]any{"goalId", goalID}, append(kv, "at", at)...)...)
}

func derefStr(p *string) string {
	if p == nil {
		return "<undefined>"
	}
	return *p
}

func ledgerLines(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func writeLedgerFile(t *testing.T, ctx storage, content string) {
	t.Helper()
	p := goalLedgerPath(ctx)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubSafe runs fn and reports a call into another slice's stub as a failure instead of a panic.
func stubSafe(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if s := fmt.Sprint(r); strings.HasPrefix(s, "stub:") {
				t.Errorf("hit %s", s)
				return
			}
			panic(r)
		}
	}()
	fn()
}
