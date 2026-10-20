package warden

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Twins of tests/recall.test.ts.

const recallPath = "/tmp/pi-warden-output-x/output.txt"

// twin: tests/recall.test.ts:7
func TestRecallConfiguredToolIsUsedWithoutProbingAndAutoProbesTheChain(t *testing.T) {
	probed := 0
	saved := probeCommand
	probeCommand = func(string, []string, time.Duration) bool { probed++; return false }
	if got := detectSearchTool("findstr", probeTimeout); got != "findstr" {
		t.Fatalf("findstr: %s", got)
	}
	if got := detectSearchTool("none", probeTimeout); got != "none" {
		t.Fatalf("none: %s", got)
	}
	if probed != 0 {
		t.Fatalf("a configured tool was probed %d times", probed)
	}
	// With nothing on the machine, auto settles on none.
	if got := detectSearchTool("auto", probeTimeout); got != "none" {
		t.Fatalf("auto on an empty machine: %s", got)
	}
	// With only grep, the chain reaches it after rg, ag, ugrep and git.
	var order []string
	probeCommand = func(command string, _ []string, _ time.Duration) bool {
		order = append(order, command)
		return command == "grep"
	}
	if got := detectSearchTool("auto", probeTimeout); got != "grep" || !slices.Equal(order, []string{"rg", "ag", "ugrep", "git", "grep"}) {
		t.Fatalf("auto: %s after %v", got, order)
	}
	probeCommand = saved
	// The real machine: a real tool, and a POSIX machine has at least grep.
	detected := detectSearchTool("auto", 5*time.Second)
	if !slices.Contains([]SearchTool{"rg", "ag", "ugrep", "git-grep", "grep", "select-string", "findstr", "none"}, detected) {
		t.Fatalf("detected %s", detected)
	}
	if runtime.GOOS != "windows" && detected == "none" {
		t.Fatal("a POSIX machine has at least grep")
	}
}

// twin: tests/recall.test.ts:15
func TestRecallFooterNamesTheFileASearchAndARangedRead(t *testing.T) {
	rg := recallInstruction("rg", recallPath)
	for _, want := range []string{"Full output: " + recallPath, "rg -n -C 3 '<pattern>'", "offset and limit", "Do not read the whole file"} {
		if !strings.Contains(rg, want) {
			t.Errorf("rg footer lacks %q: %s", want, rg)
		}
	}
	for tool, want := range map[SearchTool]string{"git-grep": "git grep --no-index", "select-string": "Select-String -Path", "findstr": `findstr /n /c:"<pattern>"`} {
		if got := recallInstruction(tool, recallPath); !strings.Contains(got, want) {
			t.Errorf("%s footer lacks %q: %s", tool, want, got)
		}
	}
	none := recallInstruction("none", recallPath)
	if strings.Contains(none, "<pattern>") || !strings.Contains(none, "offset and limit") {
		t.Fatalf("none footer: %s", none)
	}
}

// twin: tests/recall.test.ts:29
func TestRecallClassification(t *testing.T) {
	for _, c := range []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"read", map[string]any{"path": recallPath}, "full"},
		{"read", map[string]any{"path": recallPath, "offset": float64(100), "limit": float64(40)}, "scoped"},
		{"bash", map[string]any{"command": "cat " + recallPath}, "full"},
		// The segment that names the file dumps it whole.
		{"bash", map[string]any{"command": "cat " + recallPath + " | grep -n error"}, "full"},
		{"bash", map[string]any{"command": "rg -n 'error' " + recallPath}, "scoped"},
		{"bash", map[string]any{"command": "sed -n '100,140p' " + recallPath}, "scoped"},
		{"bash", map[string]any{"command": "tail -n 50 " + recallPath}, "scoped"},
		{"powershell", map[string]any{"command": "Get-Content " + recallPath}, "full"},
		{"powershell", map[string]any{"command": "Get-Content " + recallPath + " -Tail 50"}, "scoped"},
		{"ctx_execute_file", map[string]any{"path": recallPath, "code": "console.log(FILE_CONTENT.length)"}, "scoped"},
	} {
		if got := classifyRecall(c.tool, c.input, recallPath); got != c.want {
			t.Errorf("%s %v: %s, want %s", c.tool, c.input, got, c.want)
		}
	}
}
