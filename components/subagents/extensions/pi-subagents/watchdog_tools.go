package pi_subagents

import (
	"regexp"
)

// The read-only tools of a watchdog reviewer (createReadOnlyTools of the host in the original): read, grep, find and ls, rooted at the
// reviewed cwd. A tool handler of the SDK cannot call the host's own tools from an event handler, so these are the extension's own,
// with the parameters and limits of the host's.

const (
	wdToolMaxBytes     = 50 * 1024
	wdReadMaxLines     = 2000
	wdGrepDefaultLimit = 100
	wdGrepMaxLineChars = 500
	wdFindDefaultLimit = 1000
	wdLsDefaultLimit   = 500
)

func wdResolveToolPath(cwd, p string) string { panic("not implemented") }

func wdArgString(args *jsObject, key string) (string, bool) { panic("not implemented") }

func wdArgInt(args *jsObject, key string, fallback int) int { panic("not implemented") }

func wdTruncateBytes(text string) (string, bool) { panic("not implemented") }

func isRuneStart(b byte) bool { panic("not implemented") }

func wdReadTool(cwd string) wdAgentTool { panic("not implemented") }

// wdListFiles lists the files under root (relative, slash separated): the ones Git knows (tracked and not ignored) in a repository,
// else a walk that skips .git and node_modules.
func wdListFiles(root string) []string { panic("not implemented") }

func wdGlobRegexp(pattern string) *regexp.Regexp { panic("not implemented") }

func wdGlobMatches(re *regexp.Regexp, pattern, rel string) bool { panic("not implemented") }

func wdGrepTool(cwd string) wdAgentTool { panic("not implemented") }

func wdFindTool(cwd string) wdAgentTool { panic("not implemented") }

func wdLsTool(cwd string) wdAgentTool { panic("not implemented") }

// wdReadOnlyTools is createReadOnlyTools: read, grep, find and ls.
func wdReadOnlyTools(cwd string) []wdAgentTool { panic("not implemented") }
