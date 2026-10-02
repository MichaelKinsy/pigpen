package warden

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// The pattern pass (src/guard.ts): cheap, offline, deliberately narrow. The judge supplies the
// judgment; this is the floor. Go's RE2 has no lookahead or backreference, so the rules that need one are
// written as small functions with the same behaviour (each says which regexp it replaces).

const secretKeywordDoc = `\w*(?:key|secret|token|passw(?:or)?d)\w*`

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// wordRun is the maximal \w run of s starting at from.
func wordRun(s string, from int) string {
	end := from
	for end < len(s) && isWordByte(s[end]) {
		end++
	}
	return s[from:end]
}

var secretKeywords = []string{"key", "secret", "token", "passwd", "password"}

// isSecretName: the run is the SECRET_NAME shape (a word holding key, secret, token, passwd or password).
func isSecretName(word string) bool {
	lower := strings.ToLower(word)
	for _, k := range secretKeywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// secretExpansion: `\$\{?SECRET_NAME(?!\w)` (a double-quoted string that expands a credential variable
// prints it, so it is not inert data text).
func secretExpansion(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			continue
		}
		j := i + 1
		if j < len(s) && s[j] == '{' {
			j++
		}
		if isSecretName(wordRun(s, j)) {
			return true
		}
	}
	return false
}

func isSepByte(c byte) bool { return c == '\n' || c == ';' || c == '&' || c == '|' }

// firstSep is the index of the first of [\n;&|] at or after from, or len(s).
func firstSep(s string, from int) int {
	for i := from; i < len(s); i++ {
		if isSepByte(s[i]) {
			return i
		}
	}
	return len(s)
}

var (
	rePrintenv   = regexp.MustCompile(`(?i)\bprintenv\b`)
	reEcho       = regexp.MustCompile(`(?i)\becho\b`)
	reWcAfter    = regexp.MustCompile(`^\s*wc\b`)
	reDevNullOut = regexp.MustCompile(`^\s*>\s*/dev/null`)
)

// pipedToWc: `(?![^\n;&|]*\|\s*wc\b)` negated: the first separator after from is a `|` followed by wc.
func pipedToWc(s string, from int) bool {
	sep := firstSep(s, from)
	return sep < len(s) && s[sep] == '|' && reWcAfter.MatchString(s[sep+1:])
}

// printsSecret: `printenv NAME` or `echo $NAME` / `"${NAME}"` (also `\$NAME` inside `ssh ... "..."`),
// unless piped to `wc` or sent to /dev/null. Replaces PRINTS_SECRET.
func printsSecret(s string) bool {
	for _, m := range rePrintenv.FindAllStringIndex(s, -1) {
		segEnd := firstSep(s, m[1])
		for p := m[1]; p <= segEnd && p < len(s); p++ {
			if !isSpaceByte(s[p]) {
				continue
			}
			w := wordRun(s, p+1)
			if w == "" || !isSecretName(w) {
				continue
			}
			after := p + 1 + len(w)
			if !pipedToWc(s, after) && !reDevNullOut.MatchString(s[after:]) {
				return true
			}
		}
	}
	for _, m := range reEcho.FindAllStringIndex(s, -1) {
		segEnd := firstSep(s, m[1])
		for p := m[1]; p < segEnd; p++ {
			if s[p] != '$' {
				continue
			}
			j := p + 1
			if j < len(s) && s[j] == '{' {
				j++
			}
			w := wordRun(s, j)
			if w == "" || !isSecretName(w) {
				continue
			}
			after := j + len(w)
			if after < len(s) && (s[after] == '+' || (s[after] == ':' && after+1 < len(s) && s[after+1] == '+')) {
				continue
			}
			if !pipedToWc(s, after) {
				return true
			}
		}
	}
	return false
}

const gitPrefix = `\bgit(?:\s+-[-\w.]*(?:=\S*)?(?:\s+(?:"[^"]*"|'[^']*'|[^-\s]\S*))?)*`

var (
	reGitDiscardCheckout = regexp.MustCompile(`\bgit\s+checkout\s+(?:--\s+\S|(?:\.|\*)(?:\s|$))`)
	reGitRestore         = regexp.MustCompile(`\bgit\s+restore\b`)
	reWorktreeFlag       = regexp.MustCompile(`--worktree|\s-\w*W`)
)

// gitCheckoutDiscard replaces the lookahead alternatives of the git-checkout-discard rule.
func gitCheckoutDiscard(s string) bool {
	if reGitDiscardCheckout.MatchString(s) {
		return true
	}
	for _, m := range reGitRestore.FindAllStringIndex(s, -1) {
		rest := s[m[1]:firstSep(s, m[1])]
		if !strings.Contains(rest, "--staged") || reWorktreeFlag.MatchString(rest) {
			return true
		}
	}
	return false
}

// shellRule is one built-in offline rule.
type shellRule struct {
	ID       string
	Severity Severity
	Label    string
	Test     func(string) bool
}

func re(expr string) func(string) bool {
	r := regexp.MustCompile(expr)
	return r.MatchString
}

var shellRules = []shellRule{
	{"git-force-push", SeverityDestructive, "git force push", re(gitPrefix + `\s+push\b[^\n;&|]*\s(?:-f|--force)(?:[^-\w]|$)`)},
	{"git-force-with-lease", SeverityDestructive, "git push --force-with-lease", re(gitPrefix + `\s+push\b[^\n;&|]*--force-with-lease`)},
	{"git-reset-hard", SeverityDestructive, "git reset --hard", re(gitPrefix + `\s+reset\b[^\n;&|]*--hard`)},
	{"git-clean", SeverityDestructive, "git clean (removes untracked files)", re(`\bgit\s+clean\b[^\n;&|]*\s-[a-zA-Z]*[fFxX]`)},
	{"git-checkout-discard", SeverityRisky, "git checkout/restore discards working changes", gitCheckoutDiscard},
	{"git-branch-force-delete", SeverityRisky, "git branch -D", re(`\bgit\s+branch\b[^\n;&|]*\s-D\b`)},
	{"git-stash-drop", SeverityRisky, "git stash drop/clear", re(`\bgit\s+stash\s+(?:drop|clear)\b`)},
	{"sql-drop", SeverityDestructive, "SQL DROP", re(`(?i)\bdrop\s+(?:table|database|schema|index|view|user|role)\b`)},
	{"sql-truncate", SeverityDestructive, "SQL TRUNCATE", re(`(?i)\btruncate\s+(?:table\s+)?\w`)},
	{"sql-delete", SeverityDestructive, "SQL DELETE FROM", re(`(?i)\bdelete\s+from\s+\w`)},
	{"block-device-write", SeverityDestructive, "write to a block device", re(`(?:\bdd\b[^\n;&|]*\bof=/dev/|>\s*/dev/(?:sd|hd|nvme|disk|mmcblk|vd)|\bmkfs(?:\.\w+)?\b|\bwipefs\b|\bfdisk\b|\bparted\b)`)},
	{"chmod-777", SeverityDestructive, "chmod -R 777", re(`\bchmod\b[^\n;&|]*\s-[a-zA-Z]*R[a-zA-Z]*\s+[0-7]*777\b|\bchmod\b[^\n;&|]*\s777\s+[^\n;&|]*\s-[a-zA-Z]*R`)},
	{"fork-bomb", SeverityDestructive, "fork bomb", re(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)},
	{"remote-script-exec", SeverityDestructive, "pipe remote script into a shell", re(`\b(?:curl|wget)\b[^\n;&]*\|\s*(?:sudo\s+)?(?:ba|z|da|k)?sh\b`)},
	{"kill-all", SeverityDestructive, "kill every process", re(`\bkill\s+(?:-\w+\s+)*-1\b|\bkillall5\b`)},
	{"power", SeverityDestructive, "shutdown/reboot", re(`(?m)(?:^|[;&|(]\s*|\bsudo\s+)(?:shutdown|reboot|halt|poweroff)\b`)},
	{"npm-publish", SeverityDestructive, "publish a package", re(`\b(?:npm|pnpm|yarn)\s+publish\b|\bcargo\s+publish\b|\btwine\s+upload\b`)},
	{"infra-destroy", SeverityDestructive, "destroy infrastructure", re(`\b(?:terraform|tofu|pulumi)\s+destroy\b|\bkubectl\s+delete\b|\bhelm\s+(?:uninstall|delete)\b|\bdocker\s+(?:system\s+prune|volume\s+rm|rm\s+-[a-z]*f)`)},
	{"find-delete", SeverityRisky, "find -delete / -exec rm", re(`\bfind\b[^\n;&|]*(?:-delete\b|-exec\w*\s+rm\b)`)},
	{"git-bypass", SeverityRisky, "bypasses commit hooks or signing", re(`\bgit\b[^\n;&|]*(?:--no-verify\b|--no-gpg-sign\b|-c\s+commit\.gpg[sS]ign=false|-c\s+core\.hooksPath=)`)},
	{"pr-merge", SeverityRisky, "merges a pull request", re(`\bgh\s+pr\s+merge\b|\bglab\s+mr\s+merge\b`)},
	{"sudo", SeverityRisky, "sudo", re(`(?:^|[\s;&|(])sudo\s`)},
	{"printenv-secret", SeverityDestructive, "prints a credential variable into the tool result; check that it is set without printing it: `test -n \"$NAME\" && echo set` or `printenv NAME | wc -c`", printsSecret},
}

// exemptableIDs is every id exemptRules can legitimately name.
var exemptableIDs = func() []string {
	var ids []string
	for _, r := range shellRules {
		ids = append(ids, r.ID)
	}
	return append(ids, "rm-recursive", "rm-rf", "rm-recursive-dangerous-target", "sensitive-path")
}()

// ---------------------------------------------------------------------------
// Sensitive paths.

var (
	reDotEnv        = regexp.MustCompile(`(?i)\.env`)
	reSensitiveRest = regexp.MustCompile(`(?i)(?:^|[\s"'=:/~])\.?(?:ssh/(?:id_\w+|authorized_keys|known_hosts)|aws/credentials|gnupg/|netrc\b|npmrc\b|pypirc\b|docker/config\.json|kube/config\b|pi/agent/auth\.json|pig/agent/auth\.json|pi/agent/pi-typesafe/auth\.json)|\b\w+\.(?:pem|p12|pfx|keystore|jks)\b|\bid_(?:rsa|ed25519|ecdsa|dsa)\b`)
	envExampleWords = []string{"example", "sample", "template", "dist"}
)

func isEnvBefore(c byte) bool {
	return isSpaceByte(c) || c == '/' || c == '"' || c == '\'' || c == '=' || c == ':' || c == '('
}

func isEnvAfter(c byte) bool {
	return isSpaceByte(c) || c == '"' || c == '\'' || c == ';' || c == '|' || c == '&' || c == ')'
}

// dotEnvPath replaces the `.env` alternative of SENSITIVE_PATH:
// `(?:^|[\s/"'=:(])\.env(?:\.(?!example\b|sample\b|template\b|dist\b)[\w.-]+)?(?=$|[\s"';|&)])`.
func dotEnvPath(s string) bool {
	for _, m := range reDotEnv.FindAllStringIndex(s, -1) {
		if m[0] > 0 && !isEnvBefore(s[m[0]-1]) {
			continue
		}
		q := m[1]
		if q == len(s) || isEnvAfter(s[q]) {
			return true
		}
		if s[q] != '.' {
			continue
		}
		rest := s[q+1:]
		excluded := false
		for _, w := range envExampleWords {
			if len(rest) >= len(w) && strings.EqualFold(rest[:len(w)], w) && (len(rest) == len(w) || !isWordByte(rest[len(w)])) {
				excluded = true
			}
		}
		if excluded {
			continue
		}
		end := q + 1
		for end < len(s) && (isWordByte(s[end]) || s[end] == '.' || s[end] == '-') {
			end++
		}
		if end > q+1 && (end == len(s) || isEnvAfter(s[end])) {
			return true
		}
	}
	return false
}

// SensitivePath reports whether text names a secrets or credentials file.
func SensitivePath(s string) bool { return dotEnvPath(s) || reSensitiveRest.MatchString(s) }

// ---------------------------------------------------------------------------
// rm classifier.

var (
	reRm          = regexp.MustCompile("(?:^|[\\s\"'(`])rm\\s+(.*)$")
	reFlagLetters = regexp.MustCompile(`^-[a-zA-Z]+$`)
	reSplitSlash  = regexp.MustCompile(`[\\/]`)
)

func isAbsPath(p string) bool {
	// Node's isAbsolute follows the running platform: a rooted `\` path is absolute only on Windows.
	return filepath.IsAbs(p) || (runtime.GOOS == "windows" && (strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)))
}

func isInside(target, cwd string) bool {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absCwd, absTarget)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func classifyRm(segment, cwd string) *PatternHit {
	m := reRm.FindStringSubmatch(segment)
	if m == nil {
		return nil
	}
	var flags, targets []string
	for _, tok := range strings.Fields(m[1]) {
		tok = strings.TrimRight(tok, "\"')`")
		if strings.HasPrefix(tok, "-") {
			flags = append(flags, tok)
		} else {
			targets = append(targets, tok)
		}
	}
	recursive, force := false, false
	for _, f := range flags {
		if f == "--recursive" || (reFlagLetters.MatchString(f) && strings.ContainsAny(f, "rR")) {
			recursive = true
		}
		if f == "--force" || (reFlagLetters.MatchString(f) && strings.Contains(f, "f")) {
			force = true
		}
	}
	if !recursive {
		return nil
	}
	dangerous := false
	for _, t := range targets {
		clean := t
		if strings.HasPrefix(clean, `"`) || strings.HasPrefix(clean, "'") {
			clean = clean[1:]
		}
		if strings.HasSuffix(clean, `"`) || strings.HasSuffix(clean, "'") {
			clean = clean[:len(clean)-1]
		}
		switch {
		case clean == "/" || clean == "~" || clean == "*" || clean == "." || clean == ".." || strings.HasPrefix(clean, "~/") || strings.HasPrefix(clean, "$") || strings.HasPrefix(clean, "/*") || clean == "./" || clean == "../":
			dangerous = true
		case isAbsPath(clean):
			if cwd == "" || !isInside(clean, cwd) {
				dangerous = true
			}
		default:
			for _, part := range reSplitSlash.Split(clean, -1) {
				if part == ".." {
					dangerous = true
				}
			}
		}
	}
	switch {
	case dangerous:
		return &PatternHit{ID: "rm-recursive-dangerous-target", Severity: SeverityDestructive, Label: "recursive rm on an absolute, home, variable, or parent path"}
	case force:
		return &PatternHit{ID: "rm-rf", Severity: SeverityRisky, Label: "rm -rf on a project path"}
	}
	return &PatternHit{ID: "rm-recursive", Severity: SeverityRisky, Label: "recursive rm"}
}

// ---------------------------------------------------------------------------
// Git state: a hard reset of a clean tree loses no uncommitted work, and a lease push to a named feature
// branch cannot overwrite the default branch. Only a plain single `git reset`/`git push` command is read;
// anything else (a `cd`, `-C`, a quote, a variable, a second command) keeps the hold, as does any git call
// that fails or times out.

// GitRunner runs one git command in cwd and returns trimmed stdout and whether it succeeded.
type GitRunner func(cwd string, args ...string) (string, bool)

const gitStateTimeout = 2 * time.Second

var (
	rePlainCommand = regexp.MustCompile(`^[\w@%+=:,./~^-]+(?:[ \t]+[\w@%+=:,./~^-]+)*$`)
	reLeasePush    = regexp.MustCompile(`^(?:--force-with-lease(?:=\S+)?|--force-if-includes|-u|--set-upstream|-q|--quiet|-v|--verbose|-n|--dry-run|--progress|--atomic|--no-verify)$`)
	reRemoteName   = regexp.MustCompile(`^[\w.-]+$`)
	reSplitBlanks  = regexp.MustCompile(`[ \t]+`)
)

func plainGitWords(command, subcommand string) ([]string, bool) {
	text := jsTrim(command)
	if !rePlainCommand.MatchString(text) {
		return nil, false
	}
	words := reSplitBlanks.Split(text, -1)
	if len(words) >= 2 && words[0] == "git" && words[1] == subcommand {
		return words[2:], true
	}
	return nil, false
}

// runGit is the default GitRunner: the real git, no stdin, a 2 s limit, and GIT_OPTIONAL_LOCKS=0 so
// `git status` does not refresh the index and race the agent's own git calls.
func runGit(cwd string, args ...string) (string, bool) {
	done := make(chan struct{})
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	hideWindow(cmd)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return "", false
	}
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(gitStateTimeout):
		_ = cmd.Process.Kill()
		<-done
		return "", false
	}
	if waitErr != nil {
		return "", false
	}
	return strings.TrimSpace(out.String()), true
}

func cleanHardReset(command, cwd string, git GitRunner) bool {
	if cwd == "" {
		return false
	}
	if _, ok := plainGitWords(command, "reset"); !ok {
		return false
	}
	out, ok := git(cwd, "status", "--porcelain")
	return ok && out == ""
}

// safeLeasePush: `git push --force-with-lease` whose every target branch is named or is the current branch,
// and is not main, master or the remote's HEAD branch.
func safeLeasePush(command, cwd string, git GitRunner) bool {
	if cwd == "" {
		return false
	}
	words, ok := plainGitWords(command, "push")
	if !ok {
		return false
	}
	var positionals []string
	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			if !reLeasePush.MatchString(w) {
				return false
			}
		} else {
			positionals = append(positionals, w)
		}
	}
	remote, haveRemote := "", false
	var refspecs []string
	if len(positionals) > 0 {
		remote, haveRemote = positionals[0], true
		refspecs = positionals[1:]
	}
	if haveRemote && !reRemoteName.MatchString(remote) {
		return false
	}
	var targets []string
	current := ""
	currentBranch := func() string {
		if current == "" {
			if out, ok := git(cwd, "symbolic-ref", "-q", "--short", "HEAD"); ok {
				current = out
			}
		}
		return current
	}
	if len(refspecs) == 0 {
		branch := currentBranch()
		if branch == "" {
			return false
		}
		targets = append(targets, branch)
		pd, ok := git(cwd, "config", "--default", "simple", "--get", "push.default")
		if !ok || (pd != "simple" && pd != "current" && pd != "upstream" && pd != "tracking") {
			return false
		}
		refs, ok := git(cwd, "for-each-ref", "--format=%(refname)%09%(upstream:remotename)%09%(upstream:lstrip=3)", "refs/heads/"+branch)
		if !ok {
			return false
		}
		var upstreamRemote, upstreamBranch string
		for _, line := range strings.Split(refs, "\n") {
			cols := strings.Split(line, "\t")
			if cols[0] == "refs/heads/"+branch {
				if len(cols) > 1 {
					upstreamRemote = cols[1]
				}
				if len(cols) > 2 {
					upstreamBranch = cols[2]
				}
				break
			}
		}
		if upstreamBranch != "" {
			targets = append(targets, upstreamBranch)
		}
		if !haveRemote {
			remote = upstreamRemote
			if remote == "" {
				remote = "origin"
			}
			haveRemote = true
		}
	}
	for _, spec := range refspecs {
		if strings.HasPrefix(spec, "+") || strings.HasPrefix(spec, ":") {
			return false
		}
		target := spec
		if i := strings.Index(spec, ":"); i >= 0 {
			target = spec[i+1:]
		}
		if strings.HasPrefix(target, "refs/heads/") {
			target = strings.TrimPrefix(target, "refs/heads/")
		} else if strings.HasPrefix(target, "refs/") {
			return false
		}
		if target == "HEAD" || target == "@" {
			branch := currentBranch()
			if branch == "" {
				return false
			}
			target = branch
		}
		if target == "" {
			return false
		}
		targets = append(targets, target)
	}
	if !haveRemote {
		remote = "origin"
	}
	remoteHead, ok := git(cwd, "for-each-ref", "--format=%(symref:lstrip=3)", "refs/remotes/"+remote+"/HEAD")
	if !ok {
		return false
	}
	defaults := map[string]bool{"main": true, "master": true}
	if remoteHead != "" {
		defaults[remoteHead] = true
	}
	for _, t := range targets {
		if defaults[t] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------

// PatternOptions are the user rules and exempt ids; passed from the config so MatchPatterns stays pure.
type PatternOptions struct {
	CommandRules     []CommandRule
	CommandDenyRules []CommandRule
	ExemptRules      []string
	// Git runs the git state checks (default: the real git).
	Git GitRunner
}

// UnknownExemptIds are exempt ids that name neither a built-in, a classifier id, nor one of the user's own
// rules: inert, but almost certainly not what the user meant.
func UnknownExemptIds(exempt []string, commandRules, commandDenyRules []CommandRule) []string {
	known := map[string]bool{}
	for _, id := range exemptableIDs {
		known[id] = true
	}
	for _, r := range commandRules {
		known[r.ID] = true
	}
	for _, r := range commandDenyRules {
		known[r.ID] = true
	}
	out := []string{}
	for _, id := range exempt {
		if !known[id] {
			out = append(out, id)
		}
	}
	return out
}

// compileUserRule compiles a user rule. Rules use Go (RE2) regular expression syntax, not JavaScript's:
// a pattern that does not compile is skipped (the same outcome as an invalid JavaScript pattern upstream).
func compileUserRule(r CommandRule) *regexp.Regexp {
	pattern := r.Pattern
	if !r.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	c, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return c
}

type hitSet struct {
	order []string
	byID  map[string]PatternHit
}

func newHitSet() *hitSet { return &hitSet{byID: map[string]PatternHit{}} }
func (h *hitSet) has(id string) bool {
	_, ok := h.byID[id]
	return ok
}
func (h *hitSet) add(hit PatternHit) {
	if !h.has(hit.ID) {
		h.order = append(h.order, hit.ID)
		h.byID[hit.ID] = hit
	}
}
func (h *hitSet) set(hit PatternHit) {
	if !h.has(hit.ID) {
		h.order = append(h.order, hit.ID)
	}
	h.byID[hit.ID] = hit
}
func (h *hitSet) list() []PatternHit {
	out := make([]PatternHit, 0, len(h.order))
	for _, id := range h.order {
		out = append(out, h.byID[id])
	}
	return out
}

// MatchPatterns is the offline pass over one tool call: the built-in rules, git state, rm classification,
// sensitive paths and the user's own rules. Labels never contain the matched text.
func MatchPatterns(tool string, input map[string]any, cwd string, opts *PatternOptions) []PatternHit {
	if opts == nil {
		opts = &PatternOptions{}
	}
	git := opts.Git
	if git == nil {
		git = runGit
	}
	hits := newHitSet()
	exempt := map[string]bool{}
	for _, id := range opts.ExemptRules {
		exempt[id] = true
	}
	if view, ok := CommandOf(tool, input); ok && view.Command != "" {
		command := StripDataText(view.Command).Text
		for _, rule := range shellRules {
			if !exempt[rule.ID] && rule.Test(command) {
				hits.add(PatternHit{ID: rule.ID, Severity: rule.Severity, Label: rule.Label})
			}
		}
		if h, ok := hits.byID["git-reset-hard"]; ok && h.Severity == SeverityDestructive && cleanHardReset(view.Command, cwd, git) {
			hits.set(PatternHit{ID: "git-reset-hard", Severity: SeverityRisky, Label: "git reset --hard on a clean working tree"})
		}
		if h, ok := hits.byID["git-force-with-lease"]; ok && h.Severity == SeverityDestructive && !hits.has("git-force-push") && safeLeasePush(view.Command, cwd, git) {
			hits.set(PatternHit{ID: "git-force-with-lease", Severity: SeverityRisky, Label: "git push --force-with-lease to a branch that is not the default"})
		}
		for _, segment := range splitShell(command) {
			if hit := classifyRm(segment, cwd); hit != nil && !exempt[hit.ID] {
				hits.add(*hit)
			}
		}
		if !exempt["sensitive-path"] && SensitivePath(command) {
			hits.add(PatternHit{ID: "sensitive-path", Severity: SeveritySensitive, Label: "touches a secrets or credentials file"})
		}
		for _, r := range opts.CommandDenyRules {
			if exempt[r.ID] {
				continue
			}
			if c := compileUserRule(r); c != nil && c.MatchString(command) {
				hits.add(PatternHit{ID: r.ID, Severity: SeverityDeny, Label: orDefault(r.Message, r.ID)})
			}
		}
		for _, r := range opts.CommandRules {
			if exempt[r.ID] {
				continue
			}
			sev := SeverityRisky
			switch r.Severity {
			case "deny":
				sev = SeverityDeny
			case "confirm":
				sev = SeverityDestructive
			}
			if c := compileUserRule(r); c != nil && c.MatchString(command) {
				hits.add(PatternHit{ID: r.ID, Severity: sev, Label: orDefault(r.Message, r.ID), Action: r.Action})
			}
		}
	}
	if path, ok := input["path"].(string); ok && path != "" && SensitivePath(path) {
		hits.add(PatternHit{ID: "sensitive-path", Severity: SeveritySensitive, Label: "touches a secrets or credentials file"})
	}
	return hits.list()
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}
