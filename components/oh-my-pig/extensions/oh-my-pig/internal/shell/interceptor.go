package shell

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// upstream: packages/coding-agent/src/tools/bash-interceptor.ts, packages/coding-agent/src/tools/bash-pty-selection.ts

// InterceptorRule mirrors omp's BashInterceptorRule (exec/settings.ts:8).
type InterceptorRule struct {
	Pattern string
	Tool    string
	Message string
}

// InterceptionResult mirrors omp's InterceptionResult (bash-interceptor.ts:11).
type InterceptionResult struct {
	Block         bool
	Message       string
	SuggestedTool string
}

// DefaultInterceptorRules mirrors omp's DEFAULT_BASH_INTERCEPTOR_RULES (exec/settings.ts:17)
// for rules expressible in Go's RE2 syntax (omitting lookbehind/lookahead rules that
// require PCRE/JS regex or tools not yet ported; see UPSTREAM.md).
var DefaultInterceptorRules = []InterceptorRule{
	{
		Pattern: `^\s*(cat|head|tail|less|more)\s+`,
		Tool:    "read",
		Message: "Use the `read` tool instead of cat/head/tail. It provides better context and handles binary files.",
	},
	{
		Pattern: `^\s*(grep|rg|ripgrep|ag|ack)\s+`,
		Tool:    "grep",
		Message: "Use the `grep` tool instead of grep/rg. It respects .gitignore and provides structured output.",
	},
	{
		Pattern: `^\s*(find|fd|locate)\s+.*(-name|-iname|-type|--type|-glob)`,
		Tool:    "glob",
		Message: "Use the `glob` tool instead of find/fd. It respects .gitignore and is faster for glob patterns.",
	},
	{
		Pattern: `^\s*sed\s+(-i|--in-place)`,
		Tool:    "edit",
		Message: "Use the `edit` tool instead of sed -i. It provides diff preview and fuzzy matching.",
	},
	{
		Pattern: `^\s*perl\s+.*-[pn]?i`,
		Tool:    "edit",
		Message: "Use the `edit` tool instead of perl -i. It provides diff preview and fuzzy matching.",
	},
	{
		Pattern: `^\s*awk\s+.*-i\s+inplace`,
		Tool:    "edit",
		Message: "Use the `edit` tool instead of awk -i inplace. It provides diff preview and fuzzy matching.",
	},
}

// CheckBashInterception checks if a bash command should be redirected to a
// specialized tool.
// upstream: bash-interceptor.ts:119
func CheckBashInterception(command string, availableTools []string, rules []InterceptorRule, originalCommand string) InterceptionResult {
	if originalCommand == "" {
		originalCommand = command
	}
	candidates := interceptionCandidates(command)
	for _, rule := range rules {
		if !slices.Contains(availableTools, rule.Tool) {
			continue
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		for _, candidate := range candidates {
			if re.MatchString(candidate) {
				return InterceptionResult{
					Block:         true,
					Message:       fmt.Sprintf("Blocked: %s\n\nOriginal command: %s", rule.Message, originalCommand),
					SuggestedTool: rule.Tool,
				}
			}
		}
	}
	return InterceptionResult{Block: false}
}

func interceptionCandidates(command string) []string {
	candidates := []string{strings.TrimSpace(command)}
	for _, seg := range extractFlatShellCommandSegments(command) {
		if seg.pipedStdin {
			continue
		}
		candidates = append(candidates, seg.text)
		if stripped := withoutLeadingEnvironmentAssignments(seg.text); stripped != "" {
			candidates = append(candidates, stripped)
		}
	}
	return candidates
}

// skipShellWord finds the end of a shell word, respecting quotes and escapes;
// returns -1 for incomplete syntax.
// upstream: bash-interceptor.ts:37
func skipShellWord(command string, start int) int {
	inSingle := false
	inDouble := false
	for i := start; i < len(command); i++ {
		ch := command[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' {
				if i+1 >= len(command) {
					return -1
				}
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '\\' {
			if i+1 >= len(command) {
				return -1
			}
			i++
			continue
		}
		if ch == ' ' || ch == '\t' {
			return i
		}
	}
	if inSingle || inDouble {
		return -1
	}
	return len(command)
}

// withoutLeadingEnvironmentAssignments removes leading `NAME=value` assignments
// without interpreting shell syntax.
// upstream: bash-interceptor.ts:74
func withoutLeadingEnvironmentAssignments(command string) string {
	index := 0
	found := false
	for index < len(command) {
		for index < len(command) && (command[index] == ' ' || command[index] == '\t') {
			index++
		}
		assignStart := index
		if index >= len(command) || !isIdentStart(command[index]) {
			break
		}
		nameEnd := index + 1
		for nameEnd < len(command) && isIdentPart(command[nameEnd]) {
			nameEnd++
		}
		if nameEnd >= len(command) || command[nameEnd] != '=' {
			if found {
				return strings.TrimLeft(command[assignStart:], " \t")
			}
			return ""
		}
		wordEnd := skipShellWord(command, nameEnd+1)
		if wordEnd < 0 {
			return ""
		}
		found = true
		index = wordEnd
		if index == len(command) {
			return ""
		}
	}
	if !found {
		return ""
	}
	rest := strings.TrimLeft(command[index:], " \t")
	return rest
}

func isIdentStart(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

// CanUseInteractiveBashPty reports whether a bash call can use the local
// interactive PTY overlay. Wave 1 has no PTY backend, so callers still fall
// back with omp's notice; this helper preserves omp's selection predicate.
// upstream: bash-pty-selection.ts:10
func CanUseInteractiveBashPty(pty bool, hasUI bool) bool {
	if !pty {
		return false
	}
	if os.Getenv("PI_NO_PTY") == "1" {
		return false
	}
	return hasUI
}
