package bashanalysis

import "testing"

const ba = "access-intent/bash/bash-arity"

func TestArity(t *testing.T) {
	tw(t, ba, "is exported as a plain object", func(t *testing.T) { eq(t, Arity != nil, true) })
	tw(t, ba, "maps 'git' to arity 2", func(t *testing.T) { eq(t, Arity["git"], 2) })
	tw(t, ba, "maps 'npm run' to arity 3", func(t *testing.T) { eq(t, Arity["npm run"], 3) })
	tw(t, ba, "maps 'npm' to arity 2 (fallback when 'npm run' does not match)", func(t *testing.T) { eq(t, Arity["npm"], 2) })
	tw(t, ba, "maps 'docker compose' to arity 3", func(t *testing.T) { eq(t, Arity["docker compose"], 3) })
	tw(t, ba, "maps 'docker' to arity 2 (fallback)", func(t *testing.T) { eq(t, Arity["docker"], 2) })
}

func s(v ...string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func TestPrefix(t *testing.T) {
	tw(t, ba, "returns empty array for empty input", func(t *testing.T) { eq(t, Prefix(nil), s()) })
	tw(t, ba, "returns single-element array for a bare known command", func(t *testing.T) { eq(t, Prefix(s("git")), s("git")) })
	tw(t, ba, "returns arity-2 prefix for git subcommands", func(t *testing.T) { eq(t, Prefix(s("git", "checkout", "main")), s("git", "checkout")) })
	tw(t, ba, "returns arity-2 prefix for git status with flags", func(t *testing.T) { eq(t, Prefix(s("git", "status", "--short")), s("git", "status")) })
	tw(t, ba, "returns arity-3 prefix for npm run (longest match wins over npm arity-2)", func(t *testing.T) {
		eq(t, Prefix(s("npm", "run", "dev")), s("npm", "run", "dev"))
	})
	tw(t, ba, "returns arity-2 prefix for npm install (npm fallback, npm run does not match)", func(t *testing.T) {
		eq(t, Prefix(s("npm", "install", "lodash")), s("npm", "install"))
	})
	tw(t, ba, "returns arity-3 prefix for docker compose subcommands", func(t *testing.T) {
		eq(t, Prefix(s("docker", "compose", "up", "--build")), s("docker", "compose", "up"))
	})
	tw(t, ba, "returns arity-2 prefix for docker pull (docker fallback)", func(t *testing.T) {
		eq(t, Prefix(s("docker", "pull", "ubuntu")), s("docker", "pull"))
	})
	tw(t, ba, "returns arity-1 prefix for unknown commands", func(t *testing.T) { eq(t, Prefix(s("unknown-tool", "--flag")), s("unknown-tool")) })
	tw(t, ba, "returns arity-1 prefix for rm (args are targets, not subcommands)", func(t *testing.T) {
		eq(t, Prefix(s("rm", "-rf", "node_modules")), s("rm"))
	})
	tw(t, ba, "returns arity-1 prefix for cat", func(t *testing.T) { eq(t, Prefix(s("cat", "file.txt")), s("cat")) })
	tw(t, ba, "is case-insensitive: 'Git' looks up as 'git'", func(t *testing.T) {
		eq(t, Prefix(s("Git", "checkout", "main")), s("Git", "checkout"))
	})
	tw(t, ba, "clamps arity to available token count when command is shorter than arity", func(t *testing.T) {
		eq(t, Prefix(s("npm", "run")), s("npm", "run"))
	})
	tw(t, ba, "returns arity-2 prefix for pnpm run (longest match wins over pnpm)", func(t *testing.T) {
		eq(t, Prefix(s("pnpm", "run", "build")), s("pnpm", "run", "build"))
	})
	tw(t, ba, "returns arity-2 prefix for cargo subcommands", func(t *testing.T) { eq(t, Prefix(s("cargo", "build", "--release")), s("cargo", "build")) })
	tw(t, ba, "returns arity-2 prefix for kubectl subcommands", func(t *testing.T) { eq(t, Prefix(s("kubectl", "get", "pods")), s("kubectl", "get")) })
	tw(t, ba, "returns arity-1 for bare 'ls' (args are paths)", func(t *testing.T) { eq(t, Prefix(s("ls", "-la", "/tmp")), s("ls")) })
	// Not in the upstream suite: the object-key quirk Prefix documents.
	t.Run("object keys resolve to no tokens", func(t *testing.T) {
		eq(t, Prefix(s("constructor", "x")), s())
		eq(t, Prefix(s("__proto__")), s())
	})
}

func TestStripBashCommentLines(t *testing.T) {
	tw(t, ba, "removes a single leading comment line", func(t *testing.T) {
		eq(t, StripBashCommentLines("# Check debug logs\nfind /home -type f"), "find /home -type f")
	})
	tw(t, ba, "removes multiple leading comment lines", func(t *testing.T) {
		eq(t, StripBashCommentLines("# Step 1\n# Step 2\ngit status --short"), "git status --short")
	})
	tw(t, ba, "returns empty string when all lines are comments", func(t *testing.T) { eq(t, StripBashCommentLines("# just a comment"), "") })
	tw(t, ba, "returns empty string for blank input", func(t *testing.T) { eq(t, StripBashCommentLines(""), "") })
	tw(t, ba, "returns the command unchanged when no comment lines are present", func(t *testing.T) {
		eq(t, StripBashCommentLines("grep -rn foo src/"), "grep -rn foo src/")
	})
	tw(t, ba, "trims surrounding whitespace from the result", func(t *testing.T) { eq(t, StripBashCommentLines("\n\n  ls -la  \n"), "ls -la") })
	tw(t, ba, "treats indented comment lines as comments", func(t *testing.T) {
		eq(t, StripBashCommentLines("    # indented comment\necho hi"), "echo hi")
	})
}
