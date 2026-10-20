package bashanalysis

import (
	"path"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// TokenRole is what a collected token is, as the collector that produced it established. The role
// decides candidacy and the effect decides direction; both are stamped at the same site and never
// re-derived downstream.
type TokenRole string

const (
	// RoleOperand is an operand of unknown path-hood: shape and the existence probe decide.
	RoleOperand TokenRole = "operand"
	// RoleRedirectDestination is a redirect's own target, which the syntax proves names a file.
	RoleRedirectDestination TokenRole = "redirect-destination"
)

// PathToken is a collected path-candidate token paired with the effect its position proved and the
// role its collector gave it. The pairing is made where the token is produced, never by mapping a
// whole result: a nested execution's tokens carry their own command's attribution and must not be
// overwritten by the enclosing one.
type PathToken struct {
	Token  string
	Effect bashparse.TokenEffect
	Role   TokenRole
}

// CollectPathCandidateTokens recursively visits the AST and collects resolved text of nodes that
// represent command arguments, redirect destinations, or a statement's own path operands.
//
// It reads no text from `heredoc_body`, `heredoc_end`, or `comment` subtrees, but still descends an
// execution host for the commands it hosts: an interpolating heredoc body runs its substitution
// even though its prose is never an operand (#741). That is why the execution-host branch sits above
// the skip-subtree check: `heredoc_body` is in both sets, and the host reading must win. For
// commands in the pattern-first table it uses position- and role-based argument skipping to avoid
// collecting inline patterns/scripts as path candidates; for all other commands it collects all
// arguments generically.
func CollectPathCandidateTokens(node *bashparse.Node, words *bashparse.WordReader) []PathToken {
	switch typ := node.Type(); {
	case typ == "command":
		return CollectCommandTokens(node, words)
	case typ == "file_redirect":
		return CollectRedirectTokens(node, words)
	case typ == "for_statement":
		return collectStatementOperandTokens(node, afterIn, words)
	case typ == "case_statement":
		return collectStatementOperandTokens(node, beforeIn, words)
	case bashparse.ExecutionHostTypes[typ]:
		return collectHostedExecutionTokens(node, words)
	case bashparse.SkipSubtreeTypes[typ]:
		return nil
	}
	var tokens []PathToken
	for _, child := range node.Children() {
		tokens = append(tokens, CollectPathCandidateTokens(child, words)...)
	}
	return tokens
}

// CollectCommandTokens selects the collection strategy for a `command` node: pattern-first commands
// use the pattern walker; all others the generic one. Every token the command owns carries the
// effect its head word proves (the pure-reader core, read through the raw head word so a
// path-qualified spelling proves nothing). A nested execution collected along the way keeps its own
// command's attribution instead.
func CollectCommandTokens(node *bashparse.Node, words *bashparse.WordReader) []PathToken {
	head, _ := ExtractCommandWord(node, words)
	effect := ProveCommandEffect(head, commandArgumentWords(node, words))
	if name, ok := ExtractCommandName(node, words); ok && name != "" {
		if config, found := patternFirstCommands[name]; found {
			return collectPatternCommandTokens(node, config, effect, words)
		}
	}
	tokens := collectGenericCommandTokens(node, effect, words)
	return append(tokens, collectEmbeddedOptionValues(node, effect, words)...)
}

// CollectRedirectTokens collects redirect-destination tokens from a `file_redirect` node.
//
// The destination itself is an argument value (`> out.txt`), but it can also host a command that
// really runs (`> $(cat /etc/shadow)`, `< <(cmd)`), whose own operands are path candidates too, so
// each child is both read for its text and searched for nested executions (#741). The operator
// proves the destination's effect outright, and that proof is absolute: it overrides whatever the
// redirected command's own head word proved. A destination the operator names as a file descriptor
// (`2>&1`) contributes no token at all. The redirect's own target carries the redirect-destination
// role when the syntax proves it names a file, so the projection admits it whether or not the file
// exists yet (#609). Every other child is an operand.
func CollectRedirectTokens(node *bashparse.Node, words *bashparse.WordReader) []PathToken {
	target, hasTarget := bashparse.RedirectTargetIndex(node)
	var tokens []PathToken
	for i, child := range node.Children() {
		if bashparse.ArgNodeTypes[child.Type()] {
			if effect, ok := bashparse.RedirectEffectForDestination(node, child); ok {
				token := words.Text(child)
				role := RoleOperand
				if hasTarget && i == target && provesTarget(effect, child, token, words) {
					role = RoleRedirectDestination
				}
				tokens = append(tokens, PathToken{token, effect, role})
			}
		}
		tokens = append(tokens, collectHostedExecutionTokens(child, words)...)
	}
	return tokens
}

// provesTarget is whether a redirect's target is proven to name this literal file. The operator
// must have proved an effect (a redirect the parse could not resolve proves nothing, #814); the
// value must be literal (a computed one names a file only running the command decides); and it must
// be non-empty, since bash refuses `> ""`.
func provesTarget(effect bashparse.TokenEffect, destination *bashparse.Node, token string, words *bashparse.WordReader) bool {
	return effect.Source == bashparse.SourceSyntax && !words.IsComputed(destination) && token != ""
}

// collectHostedExecutionTokens collects the path-candidate tokens of every command nested inside
// node's execution contexts, reading none of the host subtree's own text (#945). node may be a
// context outright or merely contain one, so the traversal is the root-inclusive ForEachExecutionIn.
func collectHostedExecutionTokens(node *bashparse.Node, words *bashparse.WordReader) []PathToken {
	var tokens []PathToken
	bashparse.ForEachExecutionIn(node, func(contextNode *bashparse.Node, _ bashparse.BashCommandContext) {
		tokens = append(tokens, CollectPathCandidateTokens(contextNode, words)...)
	})
	return tokens
}

// operandSide is which side of a statement's `in` keyword carries its path operands: a
// `for`/`select` word list follows `in`; a `case` subject precedes it.
type operandSide int

const (
	beforeIn operandSide = iota
	afterIn
)

// collectStatementOperandTokens collects the tokens of a statement that names its own path
// operands, rather than reaching them through a command (#839). A non-operand child falls through
// to the ordinary recursion (that keeps the `do_group` reaching the loop body's commands); an
// operand-side child outside the argument node types falls through the same way, so a bare
// substitution keeps its own command's attribution (#807); an operand-side argument node is read
// and searched for hosted executions. The token carries the unproven effect: no command word owns
// it and no redirect operator names it.
func collectStatementOperandTokens(node *bashparse.Node, operandSide_ operandSide, words *bashparse.WordReader) []PathToken {
	var tokens []PathToken
	seenIn := false
	for _, child := range node.Children() {
		if !child.IsNamed() {
			if child.Type() == "in" {
				seenIn = true
			}
			continue
		}
		side := beforeIn
		if seenIn {
			side = afterIn
		}
		if side != operandSide_ || !bashparse.ArgNodeTypes[child.Type()] {
			tokens = append(tokens, CollectPathCandidateTokens(child, words)...)
			continue
		}
		tokens = append(tokens, PathToken{words.Text(child), bashparse.UnprovenEffect, RoleOperand})
		tokens = append(tokens, collectHostedExecutionTokens(child, words)...)
	}
	return tokens
}

// ExtractCommandName is the command name of a `command` node: the basename (`/usr/bin/sed` gives
// `sed`), with ok=false if the command name cannot be determined (e.g. variable expansion). The
// basename is what the pattern-first table needs: `/usr/bin/sed` parses its arguments exactly as
// `sed` does. It is the wrong question for a capability claim; use ExtractCommandWord there.
//
// The basename is node:path's, which is the POSIX one on the platforms the extension runs on.
func ExtractCommandName(node *bashparse.Node, words *bashparse.WordReader) (string, bool) {
	word, ok := ExtractCommandWord(node, words)
	if !ok {
		return "", false
	}
	return posixBasename(word), true
}

// posixBasename is path.posix.basename: trailing slashes are ignored, and "/" gives "".
func posixBasename(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	return path.Base(p)
}

// ExtractCommandWord is the head word of a `command` node exactly as it was written, or ok=false
// when it cannot be determined (e.g. variable expansion). Unbasenamed on purpose: `./grep` and
// `/tmp/evil/grep` name programs the pure-reader core's audit never saw, so an effect proof must be
// able to tell them from a bare `grep`.
func ExtractCommandWord(node *bashparse.Node, words *bashparse.WordReader) (string, bool) {
	for _, child := range node.Children() {
		if child.Type() == "command_name" {
			text := words.Text(child)
			return text, text != ""
		}
	}
	return "", false
}

// commandArgumentWords are the command's own argument words, which the retraction guards read.
// They are read from the argument nodes directly rather than the collected tokens, because a guard
// fires on an option (`find -delete`) and no collector emits one. Every named child but the prefix
// and a hosted redirect is a word, including a bare `$opt` or `$(cmd)` outside the argument node
// types: such a word reaches the program as an argument too.
func commandArgumentWords(node *bashparse.Node, words *bashparse.WordReader) []bashparse.ArgWord {
	var argWords []bashparse.ArgWord
	for _, child := range node.Children() {
		if !child.IsNamed() || CommandPrefixTypes[child.Type()] || bashparse.RedirectNodeTypes[child.Type()] {
			continue
		}
		argWords = append(argWords, words.ArgWord(child))
	}
	return argWords
}

// CommandPrefixTypes are the children of a `command` node that supply no operand text of their own:
// its head word, and any env-var prefix assignment. Both are skipped as operands, but either can
// host a substitution that really runs (`FOO=$(cat /etc/shadow) echo hi`, #742).
var CommandPrefixTypes = setOf("command_name", "variable_assignment")

// jsSpaceClass is JavaScript's \s as a character-class body.
const jsSpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsDot is JavaScript's `.`: anything but a line terminator.
const jsDot = `[^\n\r\x{2028}\x{2029}]`

// optionValuePattern is /^-{1,2}[^=\s]+=(.+)$/: a long or short option carrying its value inline,
// one or two leading dashes, a name containing no `=` or whitespace, then `=` and a non-empty value.
// Only the first `=` separates, so `--opt=/tmp/a=b` yields `/tmp/a=b`.
var optionValuePattern = regexp.MustCompile(`^-{1,2}[^=` + jsSpaceClass + `]+=(` + jsDot + `+)$`)

// longOptionValuePattern is /^(--[^=\s]+)=(.+)$/: a long option carrying its value inline.
var longOptionValuePattern = regexp.MustCompile(`^(--[^=` + jsSpaceClass + `]+)=(` + jsDot + `+)$`)

// collectEmbeddedOptionValues collects the values embedded in a generic command's `--opt=value`
// argument tokens. They are read straight from the argument nodes rather than from the collected
// token list, because a collector classifies a flag and never emits it: `tar --directory=/etc`
// would otherwise lose the path. This is token preprocessing, not classification (#645).
func collectEmbeddedOptionValues(node *bashparse.Node, effect bashparse.TokenEffect, words *bashparse.WordReader) []PathToken {
	var values []PathToken
	for _, child := range node.Children() {
		if CommandPrefixTypes[child.Type()] || !bashparse.ArgNodeTypes[child.Type()] {
			continue
		}
		if m := optionValuePattern.FindStringSubmatch(words.Text(child)); m != nil {
			values = append(values, PathToken{m[1], effect, RoleOperand})
		}
	}
	return values
}

// embeddedOptionValueToken is the value embedded in a single `--opt=value` token, if it carries one.
func embeddedOptionValueToken(text string, effect bashparse.TokenEffect) []PathToken {
	if m := optionValuePattern.FindStringSubmatch(text); m != nil {
		return []PathToken{{m[1], effect, RoleOperand}}
	}
	return nil
}

// patternFlagRole is what a recognized flag's argument is, for the pattern-first walker.
type patternFlagRole int

const (
	// roleNone is the lookup miss.
	roleNone patternFlagRole = iota
	// roleScript supplies the pattern/script inline (`grep -e`, `sed --expression`).
	roleScript
	// roleScriptFile supplies the pattern/script from a file (`grep -f`, `sed --file`). Only this
	// role contributes a path candidate.
	roleScriptFile
	// roleValue consumes a value that is neither pattern nor path (`grep -A`, `rg -g`).
	roleValue
	// roleSuffix consumes the following argument only when it is empty. BSD `sed` requires a
	// separate suffix argument (`sed -i '' 's/a/b/' f`) while GNU requires it glued; consuming
	// unconditionally eats the script on the other, leaving the file operand to be skipped as the
	// inline pattern. The argument's own emptiness decides it (#823).
	roleSuffix
	// roleUnknownArity is recognized, but its arity depends on which implementation the command's
	// name resolves to, so it takes neither the following argument nor the pattern positional (`awk
	// --file`, GNU on Fedora/RHEL, ignored by one-true-awk or mawk, #823).
	roleUnknownArity
)

type patternCommandConfig struct {
	// flags are the recognized flag spellings, short and long, mapped to their roles.
	flags map[string]patternFlagRole
	// patternPositionals is the number of leading positional arguments that are patterns/scripts,
	// not paths. The original's default of 1 covers sed, awk, grep, rg; sd uses 2; interpreters 0.
	patternPositionals int
}

func flagsOf(entries ...any) map[string]patternFlagRole {
	m := map[string]patternFlagRole{}
	for i := 0; i < len(entries); i += 2 {
		if base, ok := entries[i].(map[string]patternFlagRole); ok {
			for k, v := range base {
				m[k] = v
			}
			i--
			continue
		}
		m[entries[i].(string)] = entries[i+1].(patternFlagRole)
	}
	return m
}

// grepFlags: `--context` is deliberately absent, though `-C` is present and `rg` lists the long
// form below. grep parses with getopt, which declares `context` with an optional argument, and a
// long option declared that way never takes a separate argv: `grep --context 2 pat f` searches for
// `2` in the files `pat` and `f` (#823).
var grepFlags = flagsOf(
	"-e", roleScript, "--regexp", roleScript,
	"-f", roleScriptFile, "--file", roleScriptFile,
	"-A", roleValue, "--after-context", roleValue,
	"-B", roleValue, "--before-context", roleValue,
	"-C", roleValue,
	"-m", roleValue, "--max-count", roleValue,
)

var sedConfig = patternCommandConfig{patternPositionals: 1, flags: flagsOf(
	"-e", roleScript, "--expression", roleScript,
	"-f", roleScriptFile, "--file", roleScriptFile,
	"-i", roleSuffix,
)}

// awkConfig: the short flags are POSIX and consume on every awk; the GNU long forms are
// unknown-arity because the bare name does not fix the parser (#823).
var awkConfig = patternCommandConfig{patternPositionals: 1, flags: flagsOf(
	"-e", roleScript, "-f", roleScriptFile, "-F", roleValue, "-v", roleValue,
	"--source", roleUnknownArity, "--file", roleUnknownArity,
	"--field-separator", roleUnknownArity, "--assign", roleUnknownArity,
)}

// gawkConfig: `gawk` names GNU awk outright, so its long forms carry their real roles.
var gawkConfig = patternCommandConfig{patternPositionals: 1, flags: flagsOf(
	awkConfig.flags,
	"--source", roleScript, "--file", roleScriptFile,
	"--field-separator", roleValue, "--assign", roleValue,
)}

var grepConfig = patternCommandConfig{patternPositionals: 1, flags: grepFlags}

// rgConfig: rg parses with clap rather than getopt, where `--context` takes a required argument, so
// its separated spelling really does consume (#823).
var rgConfig = patternCommandConfig{patternPositionals: 1, flags: flagsOf(
	grepFlags,
	"--context", roleValue, "-g", roleValue, "--glob", roleValue, "-t", roleValue, "--type", roleValue,
	"-T", roleValue, "--type-not", roleValue, "-j", roleValue, "--threads", roleValue,
	"-M", roleValue, "--max-columns", roleValue, "-r", roleValue, "--replace", roleValue,
	"-E", roleValue, "--encoding", roleValue,
)}

var sdConfig = patternCommandConfig{patternPositionals: 2, flags: flagsOf(
	"-f", roleValue, "--flags", roleValue, "-n", roleValue, "--max-replacements", roleValue,
)}

// nodeConfig: an interpreter takes its inline script from a flag and never from a leading
// positional, so patternPositionals is 0: `node build.js /tmp/x` names two real operands and no
// script. `node -p t.js` evaluates `t.js` as source rather than running the file, so `-p` consumes
// its argument unconditionally.
var nodeConfig = patternCommandConfig{patternPositionals: 0, flags: flagsOf(
	"-e", roleScript, "--eval", roleScript, "-p", roleScript, "--print", roleScript,
)}

// bunConfig asserts the same four spellings as node and is its own object: the table's rule is a
// shared parser, not a shared spelling (#823).
var bunConfig = patternCommandConfig{patternPositionals: 0, flags: flagsOf(
	"-e", roleScript, "--eval", roleScript, "-p", roleScript, "--print", roleScript,
)}

// pythonConfig is shared by python and python3: CPython-compatible front ends where `-c` takes the
// following argument (and a glued `-cu` is the script `u`).
var pythonConfig = patternCommandConfig{patternPositionals: 0, flags: flagsOf("-c", roleScript)}

// perlConfig: `-p` and `-n` are deliberately absent. They take no argument of their own, and the
// cluster spelling that carries the script (`perl -pe 's|a|b|'`) is looked up as `-p` by the glued
// rule, so listing `-p` would consume the following word on the separated spelling too and drop a
// real operand.
var perlConfig = patternCommandConfig{patternPositionals: 0, flags: flagsOf("-e", roleScript, "-E", roleScript)}

// rubyConfig: `-E` is deliberately not listed, though perl lists it: on ruby it is `--encoding`.
var rubyConfig = patternCommandConfig{patternPositionals: 0, flags: flagsOf("-e", roleScript)}

// patternFirstCommands are the commands whose leading positional arguments are inline
// patterns/scripts rather than filesystem paths, and commands whose inline script arrives through a
// flag. Names share a configuration only when they share a parser: `egrep`/`fgrep` are the same
// binary as `grep` here, `nawk` is one-true-awk like `awk`, but `gawk` has its own config (#823).
var patternFirstCommands = map[string]patternCommandConfig{
	"node": nodeConfig, "bun": bunConfig, "python": pythonConfig, "python3": pythonConfig,
	"perl": perlConfig, "ruby": rubyConfig, "sed": sedConfig, "awk": awkConfig, "gawk": gawkConfig,
	"nawk": awkConfig, "grep": grepConfig, "egrep": grepConfig, "fgrep": grepConfig, "rg": rgConfig,
	"sd": sdConfig,
}

type flagDirectiveKind int

const (
	directiveEndOfFlags flagDirectiveKind = iota
	directiveRegularFlag
	// directiveConsumeNext: a recognized flag whose value is the argument that follows it.
	directiveConsumeNext
	// directiveInlineValue: a recognized flag carrying its value in the same token.
	directiveInlineValue
)

type patternCommandFlagDirective struct {
	kind  flagDirectiveKind
	role  patternFlagRole
	value string
}

// classifyPatternCommandFlag classifies a flag word from a pattern-first command into a directive
// that tells the walker how to handle the flag and its value. Matched in the order the tools
// accept: the exact spelling (short or long), then a long option's `=`-embedded value, then a glued
// short value. The glued form matches only the first short flag, which is getopt's own rule:
// `grep -ei pattern` really is `-e` with the value `i`. A cluster whose argument-taking flag is not
// first stays a plain flag, which over-surfaces the pattern rather than dropping the operand.
func classifyPatternCommandFlag(text string, config patternCommandConfig) patternCommandFlagDirective {
	if text == "--" {
		return patternCommandFlagDirective{kind: directiveEndOfFlags}
	}
	if role, ok := config.flags[text]; ok {
		return patternCommandFlagDirective{kind: directiveConsumeNext, role: role}
	}
	if m := longOptionValuePattern.FindStringSubmatch(text); m != nil {
		role, ok := config.flags[m[1]]
		if !ok {
			return patternCommandFlagDirective{kind: directiveRegularFlag}
		}
		return patternCommandFlagDirective{kind: directiveInlineValue, role: role, value: m[2]}
	}
	// text.length > 2 and text.slice(0, 2) read the first two characters; a recognized flag key is
	// ASCII, so comparing bytes finds the same keys.
	if !strings.HasPrefix(text, "--") && len(text) > 2 {
		if role, ok := config.flags[text[:2]]; ok {
			return patternCommandFlagDirective{kind: directiveInlineValue, role: role, value: text[2:]}
		}
	}
	return patternCommandFlagDirective{kind: directiveRegularFlag}
}

// collectPatternCommandTokens collects path-candidate tokens from a command known to have
// pattern/script arguments in leading positional slots. It is position-based: the first N
// positional arguments are assumed to be inline patterns/scripts and are skipped; remaining
// positionals are collected as path candidates. A recognized flag's role decides three things at
// once: whether the pattern positional is still expected, whether the flag's value is a path
// candidate, and, for `suffix`, whether the following argument belongs to the flag at all. The
// `=`-embedded and glued spellings carry the value in the flag's own token, so the walker splits it
// here rather than letting collectEmbeddedOptionValues emit a pattern's text as a path (#823).
func collectPatternCommandTokens(node *bashparse.Node, config patternCommandConfig, effect bashparse.TokenEffect, words *bashparse.WordReader) []PathToken {
	hasExplicitScript := false
	positionalsSeen := 0
	pendingConsumption := roleNone
	pastEndOfFlags := false
	var tokens []PathToken

	for _, child := range node.Children() {
		if CommandPrefixTypes[child.Type()] {
			// Supplies no operand of its own, but may host one that really runs.
			tokens = append(tokens, collectHostedExecutionTokens(child, words)...)
			continue
		}

		isArgNode := bashparse.ArgNodeTypes[child.Type()]
		text := words.Text(child)

		// An argument is read for its text below, and its text alone, but a quoted substitution
		// sitting there really runs, and its own operands are candidates wherever it sits (#945).
		if isArgNode {
			tokens = append(tokens, collectHostedExecutionTokens(child, words)...)
		}

		// Handle the argument a previous flag consumed. The consumption discharges on whatever node
		// type follows, not only on an argument node type: a bare number (`-A 3`), an expansion
		// (`-A $N`), and a substitution (`-A $(echo 3)`) are all this flag's argument (#823).
		if pendingConsumption != roleNone {
			consumption := pendingConsumption
			pendingConsumption = roleNone
			if !isArgNode {
				tokens = append(tokens, CollectPathCandidateTokens(child, words)...)
				continue
			}
			consumed, token := dischargePendingConsumption(consumption, text, effect)
			if token != nil {
				tokens = append(tokens, *token)
			}
			if consumed {
				continue
			}
		}

		// A node outside the argument node types is still one word the shell passes as an argument
		// (`grep 42 f`, `grep $PATTERN f`), so it spends a pattern positional even though no
		// reliable operand text can be read from it (#823). A redirect hosted on the command node is
		// not an argument and is excluded; the exclusion is the narrow side on purpose.
		if !isArgNode {
			if !bashparse.ExecutionHostTypes[child.Type()] && !hasExplicitScript && positionalsSeen < config.patternPositionals {
				positionalsSeen++
			}
			tokens = append(tokens, CollectPathCandidateTokens(child, words)...)
			continue
		}

		// Flag detection (only before the "--" end-of-flags marker). A quoted flag value makes the
		// argument a `concatenation`, so a recognized flag is acted on in that shape too; an
		// unrecognized one is not, and neither is a token quoted whole (#957).
		if !pastEndOfFlags && strings.HasPrefix(text, "-") && len(text) > 1 {
			directive := classifyPatternCommandFlag(text, config)
			if child.Type() == "word" || (directive.kind != directiveRegularFlag && hasUnquotedLeadingDash(child)) {
				switch directive.kind {
				case directiveEndOfFlags:
					pastEndOfFlags = true
				case directiveConsumeNext:
					pendingConsumption = directive.role
					if suppliesScript(directive.role) {
						hasExplicitScript = true
					}
				case directiveInlineValue:
					if directive.role == roleScriptFile {
						tokens = append(tokens, PathToken{directive.value, effect, RoleOperand})
					}
					if suppliesScript(directive.role) {
						hasExplicitScript = true
					}
				case directiveRegularFlag:
					// Unrecognized: fall back to the blind `--opt=value` split, which is safe
					// precisely because the flag's role is unknown (#645).
					tokens = append(tokens, embeddedOptionValueToken(text, effect)...)
				}
				continue
			}
		}

		// Positional argument.
		if !hasExplicitScript && positionalsSeen < config.patternPositionals {
			positionalsSeen++ // Skip: this is an inline pattern/script.
		} else {
			tokens = append(tokens, PathToken{text, effect, RoleOperand})
		}
		// A quoted token that did not act as a flag above has its embedded value split here instead.
		tokens = append(tokens, embeddedOptionValueToken(text, effect)...)
	}
	return tokens
}

// hasUnquotedLeadingDash: whether a `concatenation` argument begins with an unquoted `-`, as a flag
// carrying a quoted value does (`-F':'` is the word `-F` then `':'`). A token quoted whole is a
// `raw_string` or `string`, never a `concatenation`.
func hasUnquotedLeadingDash(child *bashparse.Node) bool {
	if child.Type() != "concatenation" {
		return false
	}
	head := child.Child(0)
	return head != nil && head.Type() == "word" && strings.HasPrefix(head.Text(), "-")
}

// suppliesScript: whether a flag in this role means the inline pattern positional is spent.
// `unknown-arity` says so for the opposite reason to the others: not because the script was
// supplied, but because the walker cannot tell which word the script is.
func suppliesScript(role patternFlagRole) bool {
	return role == roleScript || role == roleScriptFile || role == roleUnknownArity
}

// dischargePendingConsumption applies a pending consumption to the argument text that follows its
// flag. consumed is the flag's own verdict: a `suffix` flag declines a non-empty argument, which
// the walker then reads as an ordinary argument. token is the path candidate the consumed argument
// contributes, if any.
func dischargePendingConsumption(role patternFlagRole, text string, effect bashparse.TokenEffect) (consumed bool, token *PathToken) {
	switch role {
	case roleScriptFile:
		return true, &PathToken{text, effect, RoleOperand}
	case roleScript, roleValue:
		return true, nil
	case roleSuffix:
		return text == "", nil
	}
	return false, nil
}

// collectGenericCommandTokens collects all argument tokens from a generic (non-pattern-first)
// command node, skipping the command name and variable assignments.
func collectGenericCommandTokens(node *bashparse.Node, effect bashparse.TokenEffect, words *bashparse.WordReader) []PathToken {
	var tokens []PathToken
	seenCommandName := false
	for _, child := range node.Children() {
		if CommandPrefixTypes[child.Type()] {
			// Supplies no operand of its own, but may host one that really runs.
			if child.Type() == "command_name" {
				seenCommandName = true
			}
			tokens = append(tokens, collectHostedExecutionTokens(child, words)...)
			continue
		}
		isArg := bashparse.ArgNodeTypes[child.Type()]
		// A quoted substitution among these arguments really runs, and its own operands are
		// candidates wherever it sits (#945).
		if isArg {
			tokens = append(tokens, collectHostedExecutionTokens(child, words)...)
		}
		// If there was no explicit command_name node, the first word-like child is the command name
		// itself: skip it.
		if !seenCommandName && isArg {
			seenCommandName = true
			continue
		}
		if isArg {
			tokens = append(tokens, PathToken{words.Text(child), effect, RoleOperand})
			continue
		}
		// Recurse into other children (e.g. command_substitution nested in args).
		tokens = append(tokens, CollectPathCandidateTokens(child, words)...)
	}
	return tokens
}
