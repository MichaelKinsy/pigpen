package warden

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Recall (src/recall.ts). A recall is the agent going back to a stored full output. A whole-file read undoes the
// saving; a scoped search or a ranged read keeps it. The footer under every excerpt names a search command that
// exists on this machine, detected once per session. Detection never runs inside a tool_result handler: the
// session start starts it, and the handlers wait for its answer (searchTool.go).

// SearchTool names the search command in the recall footer: a RecallTool other than "auto".
type SearchTool = RecallTool

// searchChain is the probe order, fastest first; the last entries cover Windows shells.
var searchChain = []struct {
	tool    SearchTool
	command string
	args    []string
}{
	{"rg", "rg", []string{"--version"}},
	{"ag", "ag", []string{"--version"}},
	{"ugrep", "ugrep", []string{"--version"}},
	{"git-grep", "git", []string{"--version"}},
	{"grep", "grep", []string{"--version"}},
	{"select-string", powerShellCommand(), []string{"-NoProfile", "-NonInteractive", "-Command", "exit 0"}},
	{"findstr", "where.exe", []string{"findstr"}},
}

func powerShellCommand() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "pwsh"
}

// probeTimeout is detectSearchTool's default per-command limit.
const probeTimeout = 1500 * time.Millisecond

// probeCommand reports whether command runs and exits 0 within timeout. It is a variable so tests can fake the
// machine.
var probeCommand = func(command string, args []string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	hideWindow(cmd)
	return cmd.Run() == nil
}

// detectSearchTool returns the first working tool in the chain, or "none". A configured tool other than "auto" is
// trusted without a probe.
func detectSearchTool(configured RecallTool, timeout time.Duration) SearchTool {
	if configured != "auto" && configured != "" {
		return configured
	}
	for _, entry := range searchChain {
		if entry.tool == "findstr" && runtime.GOOS != "windows" {
			continue
		}
		if probeCommand(entry.command, entry.args, timeout) {
			return entry.tool
		}
	}
	return "none"
}

func searchExample(tool SearchTool, path string) string {
	switch tool {
	case "rg":
		return "rg -n -C 3 '<pattern>' '" + path + "'"
	case "ag":
		return "ag -C 3 '<pattern>' '" + path + "'"
	case "ugrep":
		return "ugrep -n -C 3 '<pattern>' '" + path + "'"
	case "git-grep":
		return "git grep --no-index -n -C 3 '<pattern>' -- '" + path + "'"
	case "grep":
		return "grep -n -C 3 -E '<pattern>' '" + path + "'"
	case "select-string":
		return "Select-String -Path '" + path + "' -Pattern '<pattern>' -Context 3"
	case "findstr":
		return "findstr /n /c:\"<pattern>\" \"" + path + "\""
	}
	return ""
}

// recallInstruction is the footer under an excerpt or duplicate note. It tells the agent how to get a part back
// without re-reading everything.
func recallInstruction(tool SearchTool, path string) string {
	how := "read a known region with offset and limit"
	if example := searchExample(tool, path); example != "" {
		how = "search it (" + example + ") or read a known region with offset and limit"
	}
	return "Full output: " + path + "\nTo recall a part, " + how + ". Do not read the whole file."
}

var (
	reRecallSegments = prefsRE("", `&&|\|\||[;|]|\n`)
	reBareDump       = prefsRE("", `^\s*(?:cat|type|Get-Content|gc)\b`)
	reDumpLimited    = prefsRE("i", `\s-(?:TotalCount|Head|Tail)\b`)
)

// classifyRecall is "full" when the call loads the entire stored file into context; "scoped" for searches, ranged
// reads, and sandboxed processing.
func classifyRecall(toolName string, input map[string]any, path string) string {
	if toolName == "read" {
		if isJSNumber(input["offset"]) || isJSNumber(input["limit"]) {
			return "scoped"
		}
		return "full"
	}
	command, ok := input["command"].(string)
	if !ok {
		command, ok = input["code"].(string)
	}
	if !ok || command == "" {
		return "scoped"
	}
	// The pipeline segment that names the path decides: a bare cat/type/Get-Content dumps everything; anything else scopes it.
	for _, segment := range reRecallSegments.Split(command, -1) {
		if !strings.Contains(segment, path) {
			continue
		}
		if reBareDump.MatchString(segment) && !reDumpLimited.MatchString(segment) {
			return "full"
		}
	}
	return "scoped"
}

// isJSNumber is `typeof value === "number"` for a decoded JSON value (or one a test built).
func isJSNumber(v any) bool {
	switch v.(type) {
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return true
	}
	return false
}
