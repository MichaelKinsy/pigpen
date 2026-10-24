package pi_permission_system

import (
	"runtime"
	"strings"
)

// The path and external_directory gates for what the port can decide without the original's parser and
// canonicalizer: the cross-cutting gates of handlers/gates/path.ts and external-directory.ts over the path a call
// names, and a word-level scan of a simple bash command's words. Paths resolve against cwd (comparablePath, also
// used by the skill-read gate); the original additionally matches each path's symlink-resolved canonical alias
// (#418), which the port does not read — a config pattern that names only a canonical spelling is not matched.

// currentPathFlavor is the platform's path flavor: path surfaces fold case and separators on Windows, exactly on
// POSIX (path-surfaces.ts PATH_SURFACES routed through the session's PathFlavor).
var currentPathFlavor = func() PathFlavor {
	if runtime.GOOS == "windows" {
		return Win32PathFlavor
	}
	return PosixPathFlavor
}()

// readOnlyPathTools are the path-bearing tools that only read the filesystem (READ_ONLY_PATH_BEARING_TOOLS).
var readOnlyPathTools = map[string]bool{"read": true, "find": true, "grep": true, "ls": true}

// capabilitySurfaceForTool is the narrowest family surface a tool's identity proves (capabilitySurfaceForTool):
// the read-only tools prove a read, write proves a write, everything else proves nothing and reaches the bare
// family, whose members are folded most-restrictive.
func capabilitySurfaceForTool(family, tool string) string {
	switch {
	case readOnlyPathTools[tool]:
		return family + "_read"
	case tool == "write":
		return family + "_write"
	}
	return family
}

// surfaceFamilyOf is the family a surface belongs to (surfaceFamilyOf): itself, unless it is one of the directional
// members of the path or external_directory families.
func surfaceFamilyOf(surface string) string {
	for _, suffix := range []string{"_read", "_write"} {
		if strings.HasSuffix(surface, suffix) {
			family := strings.TrimSuffix(surface, suffix)
			if family == "path" || family == "external_directory" {
				return family
			}
		}
	}
	return surface
}

// checkPathSurface answers one path-surface check: a bare family folds over both directional members, most
// restrictive first (the resolver's family fold); any other surface evaluates its values as aliases of one access
// (evaluateAnyValue).
func (p *Policy) checkPathSurface(surface string, values []string, session Ruleset) Found {
	rules := p.sessionWith(session)
	members := sessionSurfaces // the two directional members of a bare family, else the surface itself
	if surface != "path" && surface != "external_directory" {
		return EvaluateAnyValue(surface, values, rules, currentPathFlavor)
	}
	worst := Found{}
	haveWorst := false
	for _, member := range members(surface) {
		found := EvaluateAnyValue(member, values, rules, currentPathFlavor)
		switch {
		case found.Rule.Action == "deny":
			return found
		case found.Rule.Action == "ask" && (!haveWorst || worst.Rule.Action != "ask"):
			worst, haveWorst = found, true
		case !haveWorst:
			worst, haveWorst = found, true
		}
	}
	return worst
}

// pathGateVerdict is a path gate's verdict for a check that resolved to deny or ask, and allow for allow.
func (p *Policy) pathGateVerdict(found Found, surface, value string) Verdict {
	switch found.Rule.Action {
	case "allow":
		return Verdict{}
	case "ask":
		return pathAsk(surface, found.Rule.Pattern, value)
	}
	return Verdict{Block: true, Reason: tagged(denySentence(surface, found.Rule))}
}

// denySentence is the original's policy-denial render in the port's compact clause style (agent-renderer.ts
// renderPolicyDenial): the surface, the rule when a config or session rule matched, and the operator's reason.
func denySentence(surface string, r Rule) string {
	var b strings.Builder
	b.WriteString("Denied by policy: '" + surface + "'")
	if r.Layer == "config" || r.Layer == "session" {
		b.WriteString(" (rule '" + r.Pattern + "')")
	}
	b.WriteString(".")
	if r.Reason != nil && *r.Reason != "" {
		b.WriteString(" Reason: " + *r.Reason + ".")
	}
	return b.String()
}

// extractToolPath is the path the cross-cutting gates read from a tool call: the `path` string input every
// path-bearing tool and, by convention, any other tool carries (getToolInputPath).
func extractToolPath(tool string, input map[string]any) string {
	if tool == "bash" || input == nil {
		return ""
	}
	p, _ := input["path"].(string)
	return jsTrim(p)
}

// pathGate is the cross-cutting path gate (describePathGate): it applies when the path-family check resolves to
// deny or ask through a rule the operator wrote — a config or session rule. When only the universal default fired
// the gate steps aside, so a config without a `path` key does not prompt on its paths (#58).
func (p *Policy) pathGate(tool, path, cwd string, session Ruleset) Verdict {
	if cwd == "" || path == "" {
		return Verdict{}
	}
	resolved := comparablePath(path, cwd)
	if resolved == "" {
		return Verdict{}
	}
	surface := capabilitySurfaceForTool("path", tool)
	found := p.checkPathSurface(surface, []string{resolved, path}, session)
	if found.Rule.Action == "allow" {
		return Verdict{}
	}
	if found.Rule.Layer != "config" && found.Rule.Layer != "session" {
		return Verdict{}
	}
	return p.pathGateVerdict(found, surface, path)
}

// externalDirectoryGate is the external-directory boundary gate (describeExternalDirectoryGate): it applies when
// the path resolves outside the working directory. Unlike the path gate it has no backward-compatibility skip —
// the original asks about an external path through the `external_directory` surface and, when the config names
// none, through the universal default.
func (p *Policy) externalDirectoryGate(tool, path, cwd string, session Ruleset) Verdict {
	if cwd == "" || path == "" {
		return Verdict{}
	}
	resolved := comparablePath(path, cwd)
	if resolved == "" || isWithinDir(resolved, cwd) {
		return Verdict{}
	}
	surface := capabilitySurfaceForTool("external_directory", tool)
	found := p.checkPathSurface(surface, []string{resolved, path}, session)
	return p.pathGateVerdict(found, surface, path)
}

// ── the bash word scan ───────────────────────────────────────────────────────

// bashReadCommands and bashWriteCommands are the command names whose word arguments prove a direction for the
// bash external-directory gate (the original's command-effects table, a small subset): a read-only command's
// external words are reads, a writing command's are writes, and an unknown command proves nothing, so its words
// reach the bare family and are folded most-restrictive (fail-closed).
var (
	bashReadCommands = map[string]bool{
		"cat": true, "head": true, "tail": true, "less": true, "more": true, "ls": true, "dir": true, "wc": true,
		"file": true, "stat": true, "du": true, "df": true, "grep": true, "rg": true, "egrep": true, "fgrep": true,
		"diff": true, "cmp": true, "md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true, "cksum": true,
		"which": true, "whereis": true, "man": true, "echo": true, "printf": true, "pwd": true, "tree": true,
		"strings": true, "hexdump": true, "od": true, "sort": true, "uniq": true, "cut": true, "tr": true,
		"basename": true, "dirname": true, "realpath": true, "readlink": true, "test": true, "hostname": true,
		"uname": true, "date": true, "id": true, "whoami": true, "groups": true, "env": true, "printenv": true,
		"sleep": true, "true": true, "false": true,
	}
	bashWriteCommands = map[string]bool{
		"rm": true, "rmdir": true, "mv": true, "touch": true, "mkdir": true, "chmod": true, "chown": true,
		"chgrp": true, "ln": true, "tee": true, "dd": true, "truncate": true, "install": true, "cp": true,
	}
)

// tokenDirection is the direction a command's word arguments prove: "read", "write" or "unproven".
func tokenDirection(unit string) string {
	words := strings.Fields(jsTrim(unit))
	if len(words) == 0 {
		return "unproven"
	}
	name := commandBasename(words[0])
	switch {
	case bashReadCommands[name]:
		return "read"
	case bashWriteCommands[name]:
		return "write"
	}
	return "unproven"
}

// isPathToken reports whether a word of a simple command looks like a filesystem path the external gate could
// judge: a POSIX absolute path, a Windows drive path, a UNC path, a home prefix, or a relative path that climbs
// out of the working directory.
func isPathToken(word string) bool {
	if word == "" || strings.HasPrefix(word, "-") {
		return false
	}
	if strings.HasPrefix(word, "/") {
		return true
	}
	if strings.HasPrefix(word, "~") {
		return true
	}
	if len(word) >= 3 && word[1] == ':' && (word[2] == '\\' || word[2] == '/') {
		c := word[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	if strings.HasPrefix(word, "..") {
		rest := word[2:]
		return rest == "" || rest[0] == '/' || rest[0] == '\\'
	}
	return false
}

// bashExternalGate scans a chain of simple commands' words for paths outside the working directory and resolves
// each on the external_directory surface, the direction its command proves. It is the word-level subset of
// describeBashExternalDirectoryGate: the original's parser finds paths in any command shape (redirections,
// heredocs, expansion); the port finds them only where its simple-command reader is exact, and a command it cannot
// read is asked about on the bash surface anyway.
func (p *Policy) bashExternalGate(cmd, cwd string, session Ruleset) Verdict {
	if cwd == "" {
		return Verdict{}
	}
	units, ok := bashUnits(cmd)
	if !ok {
		return Verdict{}
	}
	worst := Found{}
	haveWorst := false
	worstSurface, worstValue := "", ""
	for _, unit := range units {
		direction := tokenDirection(unit)
		surface := "external_directory"
		switch direction {
		case "read":
			surface = "external_directory_read"
		case "write":
			surface = "external_directory_write"
		}
		for _, word := range strings.Fields(unit) {
			if !isPathToken(word) {
				continue
			}
			resolved := comparablePath(word, cwd)
			if resolved == "" || isWithinDir(resolved, cwd) {
				continue
			}
			found := p.checkPathSurface(surface, []string{resolved, word}, session)
			switch {
			case found.Rule.Action == "deny":
				return p.pathGateVerdict(found, surface, word)
			case found.Rule.Action == "ask" && !haveWorst:
				worst, worstSurface, worstValue, haveWorst = found, surface, word, true
			}
		}
	}
	if !haveWorst {
		return Verdict{}
	}
	return p.pathGateVerdict(worst, worstSurface, worstValue)
}

// withSession is the composed ruleset the checks run on: the policy's rules with the session approvals appended
// after them, as the original composes them at check time (permission-manager.ts composedWithSession, last match
// wins).
func (p *Policy) sessionWith(session Ruleset) Ruleset {
	if len(session) == 0 {
		return p.rules
	}
	out := make(Ruleset, 0, len(p.rules)+len(session))
	out = append(out, p.rules...)
	return append(out, session...)
}
