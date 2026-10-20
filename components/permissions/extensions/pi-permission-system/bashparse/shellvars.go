package bashparse

import (
	"os"
	"regexp"
	"strings"
)

// Resolution of the shell variable references the bash path projection can settle statically
// (shell-variable-expansion.ts).
//
// Runs at token collection, upstream of classification: by the time a token reaches path
// classification it already carries the expanded path, so `$HOME/x` is accepted by the ordinary
// absolute-shape branch and needs no per-variable knowledge in the classifiers (#694).
//
// The resolvable set is deliberately tiny and closed. HOME is the spelling expandHomePath already
// resolves for config patterns and path literals; PWD reads no environment at all. Every other
// name keeps its literal text, so ADR 0003's exclusion of ambient host state stands.

// homedir is node:os homedir(): the HOME environment variable, else the user's home directory.
// An unavailable home reads "" (Node would consult the password database).
func homedir() string {
	h, _ := os.UserHomeDir()
	return h
}

// homePrefixes are the spellings of the home directory this package resolves (path/expand-home.ts
// HOME_PREFIXES). `$HOME` and `${HOME}` are the two spellings of one shell variable and stay
// interchangeable.
var homePrefixes = []string{"~", "$HOME", "${HOME}"}

// splitHomePrefix is what follows a recognized home prefix ("", or a separator and the rest), or
// ok=false when value has none (path/expand-home.ts splitHomePrefix). Copied here because the
// package owning it is the root; the paths lane may dedupe.
func splitHomePrefix(value string) (rest string, ok bool) {
	for _, prefix := range homePrefixes {
		if !strings.HasPrefix(value, prefix) {
			continue
		}
		rest := value[len(prefix):]
		if rest == "" || strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "\\") {
			return rest, true
		}
	}
	return "", false
}

// hasHomePrefix is path/expand-home.ts hasHomePrefix.
func hasHomePrefix(value string) bool {
	_, ok := splitHomePrefix(value)
	return ok
}

// TildeReading is what a leading tilde prefix expands to, as far as the program shows.
type TildeReading struct {
	// Known is false once the program rebinds HOME, when bash 3.2 expands the tilde from the
	// reassigned value.
	Known bool
	// LeadsWithDash is meaningful when Known: whether the inherited HOME begins with `-`.
	LeadsWithDash bool
}

// ShellVariables are the resolvable variables as one program sees them: which of them the
// program rebinds, so that a reference to a rebound one no longer reads as its startup value. A
// closed set of rebound names, never their values: tracking what a program assigns is the dataflow
// ADR 0009 declines.
type ShellVariables struct {
	rebound map[string]bool
}

// UnreboundVariables is a program that rebinds none of the resolvable variables.
var UnreboundVariables = &ShellVariables{rebound: map[string]bool{}}

// ScanShellVariables is the resolvable variables roots rebind: the primary parse and every
// salvaged region, since an assignment in one governs a reference in another.
//
// A resolvable name rebinds when a variable_name carries it anywhere but as a plain reference's
// name: an assignment (a prefix one too), a declaration, a `for` variable, `unset`, an arithmetic
// assignment, `${HOME:=x}`. It also rebinds when a name-binding builtin is handed it as an argument
// (`read`, `printf -v`, `let`, a quoted `export "HOME=..."`), and both rebind under a command
// running code the walk never parses (`eval`, `source`, `.`, `trap`). Position is ignored, because
// a loop or a function body can run a later assignment first; a prefix assignment and an operator
// read (`${HOME:-x}`) count although neither rebinds the current shell, which costs only the
// projection of a program already spelling the name oddly.
func ScanShellVariables(roots []*Node) *ShellVariables {
	rebound := map[string]bool{}
	for _, root := range roots {
		collectRebound(root, rebound, false)
	}
	if len(rebound) == 0 {
		return UnreboundVariables
	}
	return &ShellVariables{rebound: rebound}
}

// resolvableVariables is how each resolvable variable is spelled as a path. PWD resolves to the
// base-relative marker rather than a directory: the shell's working directory at a given point is
// the projection's effective base, which the resolver already applies via resolveBase (#393).
var resolvableVariables = map[string]func() string{
	"HOME": homedir,
	"PWD":  func() string { return "." },
}

// ResolveReference is the value of a plain `$NAME` / `${NAME}` reference, or ok=false when the node
// is not a plain reference or names a variable outside the resolvable set.
//
// Plainness is decided structurally, not by matching the node's text: a plain reference carries
// exactly one variable_name child and nothing else but delimiters. An operator form
// (`${HOME:-/tmp}`, `${#HOME}`, `${HOME%/*}`) carries additional children and is therefore
// rejected without this module needing to enumerate bash's expansion operators.
func (v *ShellVariables) ResolveReference(node *Node) (string, bool) {
	name, ok := plainVariableName(node)
	if !ok || v.rebound[name] {
		return "", false
	}
	resolve, ok := resolvableVariables[name]
	if !ok {
		return "", false
	}
	return resolve(), true
}

// SpellsReboundHome reports whether a collected path token is spelled from a HOME this program
// rebinds (`$HOME/x`, `${HOME}`, `~/x`). Such a token names no path the projection can know, yet
// path normalization expands its prefix to the startup home exactly as it does for a config
// pattern, so it must leave the path surfaces before it reaches them.
func (v *ShellVariables) SpellsReboundHome(token string) bool {
	return v.rebound["HOME"] && hasHomePrefix(token)
}

// SpellHomeAtStart is text with a leading `~` / `$HOME` / `${HOME}` spelled as the startup home,
// or ok=false when it opens with none or the program rebinds HOME.
//
// The complement of SpellsReboundHome: the spelling a command-pattern rule written with a home
// prefix is compiled to, so the rule matches the command as the shell runs it. The rest is kept
// verbatim, never path-normalized, because `..` in an argument is not a path step of the command's
// name. A backslash after the prefix gets none, because bash does not expand a tilde prefix holding
// a quoted character.
func (v *ShellVariables) SpellHomeAtStart(text string) (string, bool) {
	if v.rebound["HOME"] {
		return "", false
	}
	rest, ok := splitHomePrefix(text)
	if !ok || strings.HasPrefix(rest, "\\") {
		return "", false
	}
	return homedir() + rest, true
}

// ReadTilde is how a word's leading unquoted text expands when it opens with a tilde prefix that
// reads HOME (`~` or `~/...`; `~user` reads the password database instead), or ok=false when it
// does not.
//
// Unknown once the program rebinds HOME: bash 3.2, which Pi runs as /bin/bash on macOS, expands it
// from the reassigned value. Otherwise it is the inherited HOME, which homedir() returns verbatim,
// so the word leads with whatever that does.
func (v *ShellVariables) ReadTilde(leadingText string) (TildeReading, bool) {
	if leadingText != "~" && !strings.HasPrefix(leadingText, "~/") {
		return TildeReading{}, false
	}
	if v.rebound["HOME"] {
		return TildeReading{Known: false}, true
	}
	return TildeReading{Known: true, LeadsWithDash: strings.HasPrefix(homedir(), "-")}, true
}

// collectRebound records each resolvable name node binds. bindsNames holds beneath a builtin that
// binds the names it is handed (`read HOME`, `export "HOME=/etc"`, `declare -n r=HOME`), and only
// there does an argument spelling a name count: `grep HOME ~/.bashrc` binds nothing, and reading it
// as a rebinding would drop the `~/.bashrc` it does read.
func collectRebound(node *Node, rebound map[string]bool, inheritedBindsNames bool) {
	if referenceTypes[node.typ] {
		if _, ok := plainVariableName(node); ok {
			return
		}
	}
	bindsNames := bindsNamesBeneath(node, inheritedBindsNames)
	if node.typ == "variable_name" {
		if _, ok := resolvableVariables[node.Text()]; ok {
			rebound[node.Text()] = true
		}
	}
	if bindsNames && nameArgumentTypes[node.typ] {
		if m := namedArgument.FindStringSubmatch(quoting.ReplaceAllString(node.Text(), "")); m != nil {
			rebound[m[1]] = true
		}
	}
	if node.typ == "command" && unseenCodeCommands[commandName(node)] {
		for name := range resolvableVariables {
			rebound[name] = true
		}
	}
	for _, child := range node.children {
		collectRebound(child, rebound, bindsNames)
	}
}

// bindsNamesBeneath reports whether the arguments beneath node are names its command binds. A
// command decides for itself; a `declare`-family or `unset` statement always binds; anything else
// inherits.
func bindsNamesBeneath(node *Node, inherited bool) bool {
	if node.typ == "declaration_command" || node.typ == "unset_command" {
		return true
	}
	if node.typ != "command" {
		return inherited
	}
	name := commandName(node)
	if name == "printf" {
		return hasArgument(node, "-v")
	}
	return nameBindingCommands[name]
}

// nameBindingCommands are builtins that bind a name passed as an argument. `declare` and its family
// parse as a declaration_command when spelled plainly and as a command when quoted, so they are
// listed here too; `printf` binds only under `-v`.
var nameBindingCommands = setOf("read", "mapfile", "readarray", "getopts", "let", "unset", "declare",
	"typeset", "local", "export", "readonly")

// nameArgumentTypes are argument nodes that can spell a name a builtin binds, whole or as the head
// of an assignment (`HOME=...`, `HOME+=...`, `HOME[0]=...`, `let HOME++`).
var nameArgumentTypes = setOf("word", "string", "raw_string", "concatenation")

// namedArgument matches a resolvable name leading an argument, not followed by more of an
// identifier (JavaScript's `(?![A-Za-z0-9_])` as a consumed non-identifier character or the end).
var namedArgument = regexp.MustCompile(`^(HOME|PWD)(?:[^A-Za-z0-9_]|$)`)

// quoting matches the quote and escape characters the shell removes before a builtin sees a name.
var quoting = regexp.MustCompile(`["'\\]`)

// commandName is a command's name as the shell reads it, quotes and escapes removed.
func commandName(command *Node) string {
	for _, child := range command.children {
		if child.typ == "command_name" {
			return quoting.ReplaceAllString(child.Text(), "")
		}
	}
	return ""
}

// hasArgument reports whether a command carries flag as one of its own arguments.
func hasArgument(command *Node, flag string) bool {
	for _, child := range command.children {
		if child.typ != "command_name" && child.Text() == flag {
			return true
		}
	}
	return false
}

// unseenCodeCommands run code the walk never parses, which may rebind anything: eval's joined
// arguments, a file `source` / `.` reads, and a `trap` action.
var unseenCodeCommands = setOf("eval", "source", ".", "trap")

// referenceTypes are the node types a variable reference parses as.
var referenceTypes = setOf("simple_expansion", "expansion")

// expansionDelimiters delimit an expansion without altering what it evaluates to.
var expansionDelimiters = setOf("$", "${", "}")

// plainVariableName is the variable a node plainly references, or ok=false when it references none:
// it has no variable_name child, has more than one, or carries a child that is neither the name nor
// a delimiter (an expansion operator and its operand, or an assignment's `=` and value).
func plainVariableName(node *Node) (string, bool) {
	name := ""
	found := false
	for _, child := range node.children {
		if child.typ == "variable_name" {
			if found {
				return "", false
			}
			name = child.Text()
			found = true
			continue
		}
		if !expansionDelimiters[child.typ] {
			return "", false
		}
	}
	return name, found
}

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}
