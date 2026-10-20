package bashanalysis

import "strings"

// Arity is the curated arity dictionary for common CLI commands (bash-arity.ts ARITY). Keys are
// lowercase, space-joined command prefixes; values are the total token count that defines the
// human-understandable subcommand, i.e. how many tokens a session-approval pattern includes.
// Multi-level entries ("npm run": 3) take precedence over shorter ones ("npm": 2) because Prefix
// is longest-match-wins.
var Arity = map[string]int{
	// Version control
	"git": 2, "hg": 2, "svn": 2,
	// Node.js package managers
	"npm": 2, "npm run": 3, "npm exec": 3, "npx": 2,
	"pnpm": 2, "pnpm run": 3, "pnpm exec": 3, "pnpm dlx": 3,
	"yarn": 2, "yarn run": 3,
	"bun": 2, "bun run": 3, "bun add": 2, "bun x": 3,
	// Runtimes
	"deno": 2, "deno run": 3, "deno task": 3, "deno compile": 3,
	// Python
	"pip": 2, "pip3": 2, "uv": 2, "uv run": 3, "uv pip": 3,
	// Rust
	"cargo": 2,
	// Go
	"go": 2, "go run": 3,
	// Ruby
	"bundle": 2, "bundle exec": 3,
	// Docker / container
	"docker": 2, "docker compose": 3, "docker container": 3, "docker image": 3, "docker network": 3, "docker volume": 3,
	"podman": 2, "podman compose": 3,
	// Kubernetes
	"kubectl": 2, "helm": 2,
	// Cloud CLIs
	"aws": 3, "az": 3, "gcloud": 3, "gh": 2, "gh pr": 3, "gh issue": 3, "gh repo": 3,
	"fly": 2, "vercel": 2, "wrangler": 2,
	// Build tools
	"make": 1, "bazel": 2,
	// Infrastructure
	"terraform": 2, "tofu": 2, "pulumi": 2,
	// System service management
	"systemctl": 2, "service": 2,
	// Shell file-ops: args are paths/targets, not subcommands
	"ls": 1, "ll": 1, "la": 1, "cat": 1, "less": 1, "more": 1, "head": 1, "tail": 1, "grep": 1, "rg": 1, "ag": 1,
	"find": 1, "touch": 1, "mkdir": 1, "rm": 1, "cp": 1, "mv": 1, "ln": 1, "chmod": 1, "chown": 1, "du": 1, "df": 1,
	"echo": 1, "printf": 1, "diff": 1, "patch": 1, "wc": 1, "sort": 1, "uniq": 1, "awk": 1, "sed": 1, "tar": 1,
	"zip": 1, "unzip": 1,
	// Network
	"curl": 1, "wget": 1, "ssh": 1, "scp": 1, "rsync": 1, "ping": 1,
	// Process management
	"kill": 1, "killall": 1, "pkill": 1,
	// Package managers (system)
	"brew": 2, "apt": 2, "apt-get": 2, "yum": 2, "dnf": 2,
}

// Prefix is the semantically meaningful prefix of a tokenized command: a longest-match-wins lookup
// against Arity from the longest possible prefix down to a single token. Lookup is
// case-insensitive; the returned tokens keep their casing. With no entry the arity is 1, and an
// arity beyond the available tokens is clamped.
//
// The original looks the key up on a plain object, so the two keys an object always has resolve
// to a non-number: `constructor` and `__proto__` (lowercase, as the lookup lowercases) make
// Math.min(arity, n) NaN and the slice empty. A single-token lookup of either returns no tokens
// here too, rather than the default arity 1.
func Prefix(tokens []string) []string {
	if len(tokens) == 0 {
		return []string{}
	}
	for n := len(tokens); n >= 1; n-- {
		lowered := make([]string, n)
		for i, t := range tokens[:n] {
			lowered[i] = jsLower(t)
		}
		key := strings.Join(lowered, " ")
		if key == "constructor" || key == "__proto__" {
			return []string{}
		}
		if arity, ok := Arity[key]; ok {
			return tokens[:min(arity, len(tokens))]
		}
	}
	return []string{tokens[0]}
}

// StripBashCommentLines removes shell comment lines from a bash command string: a comment line is
// one whose first non-whitespace character is `#`. Agents often prepend descriptive comments
// before the real command, which defeat wildcard matching and session-approval suggestions. The
// original command is never returned: when every line is a comment (or the input is blank) the
// result is "", and each caller applies its own fallback.
func StripBashCommentLines(command string) string {
	lines := strings.Split(command, "\n")
	meaningful := lines[:0:0]
	for _, line := range lines {
		if !isCommentLine(line) {
			meaningful = append(meaningful, line)
		}
	}
	return jsTrim(strings.Join(meaningful, "\n"))
}

// isCommentLine is /^\s*#/ with JavaScript's \s.
func isCommentLine(line string) bool {
	for _, r := range line {
		if r == '#' {
			return true
		}
		if !isJSSpace(r) {
			return false
		}
	}
	return false
}
