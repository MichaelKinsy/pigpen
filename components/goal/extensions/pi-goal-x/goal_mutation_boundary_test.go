package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var gsImplementationFiles = []string{"goal.go", "extension.go", "core.go", "goal_scheduler.go", "goal_tools.go", "goal_commands.go", "goal_events.go", "goal_widget.go", "goal_widget_keys.go", "format.go"}

// gsImplementationSources joins the implementation files that exist (Go twin of the TS IMPLEMENTATION_MODULES).
func gsImplementationSources(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, name := range gsImplementationFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}

// Mutation primitives that must NOT be invoked from implementation files directly (Go names of the TS primitives).
var gsForbiddenCallSites = []string{"writeActiveGoalFile(", "archiveGoalFile(", "atomicWrite(", "appendGoalEvent(", "appendGoalEvents(", "ensureDir(", "safeUnlink("}

func TestGoalMutationBoundary(t *testing.T) {
	tw(t, "goal-mutation-boundary", "routes every goal-file write and ledger append through GoalService", func(t *testing.T) {
		src := gsImplementationSources(t)
		var hits []string
		for _, p := range gsForbiddenCallSites {
			if strings.Contains(src, p) {
				hits = append(hits, p)
			}
		}
		eq(t, hits, []string(nil), "implementation files must not call mutation primitives directly")
	})

	tw(t, "goal-mutation-boundary", "does not import the mutation primitives from storage/goal-ledger", func(t *testing.T) {
		// Go has no import lists: the primitives must not even be referenced as values (method values, callbacks).
		src := gsImplementationSources(t)
		for _, p := range gsForbiddenCallSites {
			name := strings.TrimSuffix(p, "(")
			for _, form := range []string{"." + name + ",", "." + name + ")", "= " + name + "\n", "." + name + "\n"} {
				if strings.Contains(src, form) {
					t.Errorf("implementation files must not reference %s", name)
				}
			}
		}
	})

	tw(t, "goal-mutation-boundary", "instantiates the GoalService and uses it for persistence", func(t *testing.T) {
		core, err := os.ReadFile("core.go")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"newGoalService(", "goalService.persist(", "goalService.apply(", "goalService.appendEvents("} {
			if !strings.Contains(string(core), want) {
				t.Errorf("core.go must contain %s", want)
			}
		}
	})

	tw(t, "goal-mutation-boundary", "goal.ts is a thin installer with no direct mutation or ledger calls", func(t *testing.T) {
		ext, err := os.ReadFile("goal.go")
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range gsForbiddenCallSites {
			if strings.Contains(string(ext), p) {
				t.Errorf("goal.go must not call %s", p)
			}
		}
		if strings.Contains(string(ext), "goalService.") {
			t.Error("goal.go must not reference the service directly")
		}
		if strings.Contains(string(ext), "appendEvents") {
			t.Error("goal.go must not append ledger events")
		}
	})

	tw(t, "goal-mutation-boundary", "keeps pure serializers and readers importable (no mutation)", func(t *testing.T) {
		ext, err := os.ReadFile("goal.go")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(ext), "newGoalCore(") {
			t.Error("goal.go must install the core")
		}
		widget, err := os.ReadFile(filepath.Join("goal_widget.go"))
		if err != nil {
			t.Fatalf("goal_widget.go (owned by GoalWidgets) is missing: %v", err)
		}
		// The debug panel of widgets/goal-widget.ts reads the pure serializer; the extension's goal-widget.ts (the debug writer) is
		// goal_widget_keys.go.
		keys, err := os.ReadFile("goal_widget_keys.go")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(widget)+string(keys), "serializeGoalFile") {
			t.Error("the widget files may use the pure serializer")
		}
	})
}
