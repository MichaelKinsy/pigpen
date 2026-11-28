package toolcmd

import "regexp"

// upstream: packages/coding-agent/src/tools/essential-tools.ts
// upstream: packages/coding-agent/src/tools/tool-commands.ts
// upstream: packages/coding-agent/src/tools/builtin-names.ts
// upstream: crates/shell/src/commands.rs (reserved_command_names)

// LoadMode mirrors omp's ToolLoadMode ("essential" | "discoverable").
type LoadMode string

const (
	LoadModeEssential    LoadMode = "essential"
	LoadModeDiscoverable LoadMode = "discoverable"
)

// EssentialBuiltinToolNames are built-in tool names whose classes declare
// `loadMode = "essential"` in omp v19.1.1 (31ffee513f).
// upstream: packages/coding-agent/src/tools/essential-tools.ts:23-37
var EssentialBuiltinToolNames = map[string]bool{
	"read":          true,
	"write":         true,
	"bash":          true,
	"edit":          true,
	"glob":          true,
	"find":          true,
	"eval":          true,
	"task":          true,
	"wait":          true,
	"learn":         true,
	"manage_skill":  true,
	"context_notes": true,
	"new_context":   true,
}

// DiscoverableBuiltinToolNames are built-in tool names whose classes declare
// `loadMode = "discoverable"` in omp v19.1.1 (31ffee513f).
// Every built-in in BUILTIN_TOOLS is either in EssentialBuiltinToolNames or here.
var DiscoverableBuiltinToolNames = map[string]bool{
	"ask":           true,
	"ast_edit":      true,
	"ast_grep":      true,
	"checkpoint":    true,
	"debug":         true,
	"github":        true,
	"grep":          true,
	"lsp":           true,
	"memory_edit":   true,
	"recall":        true,
	"reflect":       true,
	"retain":        true,
	"rewind":        true,
	"security_scan": true,
	"todo":          true,
	"web_search":    true,
}

// BuiltinToolNames is the complete set of omp built-in tools (BUILTIN_TOOLS + HIDDEN_TOOLS).
// upstream: packages/coding-agent/src/tools/builtin-names.ts:3-33,85
var BuiltinToolNames = map[string]bool{
	"read":          true,
	"bash":          true,
	"edit":          true,
	"ast_grep":      true,
	"ast_edit":      true,
	"ask":           true,
	"debug":         true,
	"eval":          true,
	"github":        true,
	"glob":          true,
	"grep":          true,
	"find":          true,
	"lsp":           true,
	"checkpoint":    true,
	"rewind":        true,
	"context_notes": true,
	"new_context":   true,
	"security_scan": true,
	"task":          true,
	"wait":          true,
	"todo":          true,
	"web_search":    true,
	"write":         true,
	"memory_edit":   true,
	"retain":        true,
	"recall":        true,
	"reflect":       true,
	"learn":         true,
	"manage_skill":  true,
	// Hidden built-ins:
	"yield": true,
	"goal":  true,
	"think": true,
}

// HiddenToolNames are omp's HIDDEN_TOOL_NAMES (yield, goal, think).
// upstream: packages/coding-agent/src/tools/builtin-names.ts:85
var HiddenToolNames = map[string]bool{
	"yield": true,
	"goal":  true,
	"think": true,
}

// SpecialToolNames are omp's SPECIAL_TOOL_NAMES: staged-action commands that run
// as bash commands outside the catalog while the command transport is active,
// and present top-level only when the transport is off and they apply.
// upstream: packages/coding-agent/src/tools/index.ts:966-971
var SpecialToolNames = map[string]bool{
	"resolve":      true,
	"reject":       true,
	"propose":      true,
	"report_issue": true,
}

// KeepTopLevel are discoverable built-ins that stay top-level even when tool
// commands are active.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:53-59
var KeepTopLevel = map[string]bool{
	"todo":       true,
	"yield":      true,
	"ask":        true,
	"grep":       true,
	"web_search": true,
}

// TransportTools carry the command transport itself and can never be mounted.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:67
var TransportTools = map[string]bool{
	"bash": true,
}

// BuiltinToolOrder is omp's BUILTIN_TOOLS registration order in tools/index.ts:622-652,
// which drives the presentation order of built-ins in `# Tool Commands`
// (e.g. ast_edit, debug, lsp in a default session).
// upstream: packages/coding-agent/src/tools/index.ts:622-652
var BuiltinToolOrder = []string{
	"read",
	"security_scan",
	"bash",
	"edit",
	"ast_grep",
	"ast_edit",
	"ask",
	"debug",
	"eval",
	"github",
	"glob",
	"grep",
	"find",
	"lsp",
	"checkpoint",
	"rewind",
	"context_notes",
	"new_context",
	"task",
	"wait",
	"todo",
	"web_search",
	"write",
	"memory_edit",
	"retain",
	"recall",
	"reflect",
	"learn",
	"manage_skill",
}

var builtinOrderIndex = func() map[string]int {
	m := make(map[string]int, len(BuiltinToolOrder))
	for i, name := range BuiltinToolOrder {
		m[name] = i
	}
	return m
}()

// BuiltinToolSummaries holds each built-in tool's `readonly summary` string from
// omp v19.1.1 (31ffee513f), used by toolSummary() before falling back to the
// description's first line.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:181-185
var BuiltinToolSummaries = map[string]string{
	"ask":           "Ask the user to fill in a form",
	"ast_edit":      "Perform AST-aware code edits (SHOULD be preferred for codemods)",
	"ast_grep":      "Search code with AST patterns (SHOULD be preferred for structural search)",
	"checkpoint":    "Create a git-based checkpoint to save and restore session state",
	"context_notes": "Read or replace persistent experimental context notes",
	"debug":         "Debug a running process with DAP (debugger adapter protocol)",
	"find":          "Semantic grep: find files and line ranges by describing what they do",
	"github":        "Interact with GitHub repositories, files, pull requests, and Actions",
	"grep":          "Search file contents by regex",
	"learn":         "Capture a reusable lesson to memory (and optionally a managed skill)",
	"lsp":           "Query LSP (language server) for diagnostics, hover info, and references",
	"manage_skill":  "Create, update, or delete an isolated managed skill",
	"memory_edit":   "Update, forget, or invalidate Mnemopi memories",
	"new_context":   "Request a fresh context window",
	"propose":       "Submit the plan for approval",
	"recall":        "Search memory for relevant prior context",
	"reflect":       "Synthesize an answer from long-term memory",
	"reject":        "Discard the pending staged action",
	"resolve":       "Apply the pending staged action",
	"retain":        "Store important facts in long-term memory",
	"rewind":        "Rewind to a previously created checkpoint",
	"security_scan": "Run OMP-native scans and explicit Codex Security cloud operations",
	"task":          "Spawn subagents to complete delegated tasks",
	"think":         "Record private scratchpad thoughts",
	"todo":          "Write a structured todo list to track progress within a session",
	"wait":          "Wait for the next result of a background job or service you started, or a reply you asked for",
	"web_search":    "Search the web for up-to-date information",
}

// DefaultLoadModeForToolName resolves a tool's presentation mode at an adapter
// boundary. An explicit declared mode always wins. When omitted, known
// essential built-in names default to "essential" (so re-registering a built-in
// never demotes it); everything else defaults to "discoverable".
// upstream: packages/coding-agent/src/tools/essential-tools.ts:45-48
func DefaultLoadModeForToolName(name string, declared LoadMode) LoadMode {
	if declared != "" {
		return declared
	}
	if EssentialBuiltinToolNames[name] {
		return LoadModeEssential
	}
	return LoadModeDiscoverable
}

// BuiltinLoadMode returns the loadMode declared by an omp built-in tool class,
// or "" for tools that declare no loadMode (hidden tools think/goal/yield and
// special tools resolve/reject/propose/report_issue). For non-built-in
// (extension/MCP) tools, it applies DefaultLoadModeForToolName.
func BuiltinLoadMode(name string) LoadMode {
	if SpecialToolNames[name] || HiddenToolNames[name] {
		return ""
	}
	if EssentialBuiltinToolNames[name] {
		return LoadModeEssential
	}
	if DiscoverableBuiltinToolNames[name] {
		return LoadModeDiscoverable
	}
	return DefaultLoadModeForToolName(name, "")
}

var (
	commandWordRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	leadingWordRE = regexp.MustCompile(`^\s*([^\s;&|<>()]+)`)
)

// shellReservedCommandNames is omp's `omp_shell::reserved_command_names()` for
// Dialect::Bash: bash reserved words, bash-available intrinsics, pi shell tools
// (timeout, urlmap, nohup, git), pi extras (crates/builtins/src/extras.rs), and
// SELF_SHELLS (sh, bash, zsh).
// upstream: crates/shell/src/commands.rs:290-305
var shellReservedCommandNames = map[string]bool{
	// Bash reserved words (crates/shell/src/bash/syntax/parser/lex.rs:187-210)
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"case": true, "esac": true, "for": true, "select": true, "while": true,
	"until": true, "do": true, "done": true, "in": true, "function": true,
	"time": true, "{": true, "}": true, "!": true, "[[": true, "]]": true,
	"coproc": true,

	// Bash-available IntrinsicId (crates/shell/src/bash/ids.rs:317-628)
	":": true, ".": true, "[": true,
	"alias": true, "bg": true, "bind": true, "break": true, "builtin": true,
	"caller": true, "cd": true, "command": true, "compgen": true, "complete": true,
	"compopt": true, "continue": true, "declare": true, "dirs": true, "disown": true,
	"echo": true, "emulate": true, "enable": true, "eval": true, "exec": true,
	"exit": true, "export": true, "false": true, "fc": true, "fg": true,
	"getopts": true, "hash": true, "help": true, "history": true, "jobs": true,
	"kill": true, "let": true, "local": true, "logout": true, "mapfile": true,
	"popd": true, "printf": true, "pushd": true, "pwd": true, "read": true,
	"readarray": true, "readonly": true, "return": true, "set": true, "shift": true,
	"shopt": true, "source": true, "strmatch": true, "suspend": true, "test": true,
	"times": true, "trap": true, "true": true, "type": true, "typeset": true,
	"ulimit": true, "umask": true, "unalias": true, "unset": true, "wait": true,

	// Shell tools (crates/shell/src/tools/mod.rs:64-65)
	"timeout": true, "urlmap": true, "nohup": true, "git": true,

	// Self shells (crates/shell/src/sys/shebang.rs:23)
	"sh": true, "bash": true, "zsh": true,

	// In-process extras (crates/builtins/src/extras.rs:37-143)
	"ast-grep": true, "sg": true, "awk": true, "b2sum": true, "base32": true,
	"base64": true, "basename": true, "cat": true, "chmod": true, "cksum": true,
	"cmp": true, "column": true, "comm": true, "cp": true, "combine": true,
	"cut": true, "date": true, "dd": true, "df": true, "diff": true,
	"direnv": true, "dirname": true, "du": true, "entr": true, "env": true,
	"errno": true, "fd": true, "file": true, "find": true, "flock": true,
	"fold": true, "fswatch": true, "grep": true, "rg": true, "head": true,
	"hostname": true, "id": true, "ifne": true, "isutf8": true, "jq": true,
	"ln": true, "ls": true, "md5": true, "md5sum": true, "mkdir": true,
	"mktemp": true, "mv": true, "nl": true, "nproc": true, "od": true,
	"open": true, "xdg-open": true, "start": true, "paste": true, "printenv": true,
	"readlink": true, "realpath": true, "rm": true, "rmdir": true, "rsync": true,
	"say": true, "sd": true, "sed": true, "seq": true, "sha1sum": true,
	"sha224sum": true, "sha256sum": true, "sha384sum": true, "sha512sum": true,
	"shasum": true, "sort": true, "sponge": true, "stat": true, "strings": true,
	"tac": true, "tail": true, "tar": true, "tee": true, "touch": true,
	"tr": true, "truncate": true, "ts": true, "uname": true, "uniq": true,
	"uptime": true, "watchexec": true, "wc": true, "which": true, "whoami": true,
	"xargs": true, "yes": true, "pgrep": true, "pidwait": true, "pkill": true,
	"lsof": true, "ps": true, "sleep": true, "top": true,
}

// IsToolCommandName reports whether name is a valid shell command word that the
// shell does not reserve.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:79-83
func IsToolCommandName(name string) bool {
	if !commandWordRE.MatchString(name) {
		return false
	}
	return !shellReservedCommandNames[name]
}

// IsMountableAsToolCommand reports whether an enabled tool is presented as a
// bash command rather than top-level while the command transport is active.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:93-97
func IsMountableAsToolCommand(name string, loadMode LoadMode) bool {
	if TransportTools[name] || KeepTopLevel[name] {
		return false
	}
	return loadMode == LoadModeDiscoverable && IsToolCommandName(name)
}

// ToolCommandCallName returns the command name a `bash` tool call invokes when
// its command line starts with a valid tool command word; empty string otherwise.
// upstream: packages/coding-agent/src/tools/tool-commands.ts:495-504
func ToolCommandCallName(toolName string, args map[string]any) string {
	if !TransportTools[toolName] || args == nil {
		return ""
	}
	cmd, ok := args["command"].(string)
	if !ok {
		return ""
	}
	m := leadingWordRE.FindStringSubmatch(cmd)
	if len(m) < 2 {
		return ""
	}
	if IsToolCommandName(m[1]) {
		return m[1]
	}
	return ""
}
