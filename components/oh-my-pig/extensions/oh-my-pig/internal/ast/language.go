package ast

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Supported language aliases and extension inference ported from
// upstream: crates/ast/src/language/mod.rs.
//
// Stock ast-grep CLI language table and case-sensitive extension mapping derived
// from ast-grep upstream (https://github.com/ast-grep/ast-grep):
//   - crates/language/src/lib.rs (SupportLang enum, file_types / from_extension)
//   - crates/cli/src/run.rs (RunWithSpecificLang::produce_item)

// langAliases maps lowercased alias names to canonical omp language names.
// upstream: crates/ast/src/language/mod.rs:783-942
var langAliases = map[string]string{
	"astro":         "astro",
	"bash":          "bash",
	"sh":            "bash",
	"zsh":           "bash",
	"ksh":           "bash",
	"bats":          "bash",
	"c":             "c",
	"h":             "c",
	"cmake":         "cmake",
	"cpp":           "cpp",
	"c++":           "cpp",
	"cc":            "cpp",
	"cxx":           "cpp",
	"hh":            "cpp",
	"hpp":           "cpp",
	"cu":            "cpp",
	"cuh":           "cpp",
	"ino":           "cpp",
	"csharp":        "csharp",
	"c#":            "csharp",
	"cs":            "csharp",
	"dart":          "dart",
	"css":           "css",
	"scss":          "css",
	"clj":           "clojure",
	"cljc":          "clojure",
	"cljs":          "clojure",
	"clojure":       "clojure",
	"clojurescript": "clojure",
	"edn":           "clojure",
	"diff":          "diff",
	"patch":         "diff",
	"docker":        "dockerfile",
	"dockerfile":    "dockerfile",
	"containerfile": "dockerfile",
	"emacs-lisp":    "emacs-lisp",
	"emacslisp":     "emacs-lisp",
	"elisp":         "emacs-lisp",
	"el":            "emacs-lisp",
	"elixir":        "elixir",
	"ex":            "elixir",
	"exs":           "elixir",
	"erlang":        "erlang",
	"erl":           "erlang",
	"hrl":           "erlang",
	"fortran":       "fortran",
	"f90":           "fortran",
	"f95":           "fortran",
	"f03":           "fortran",
	"f08":           "fortran",
	"go":            "go",
	"golang":        "go",
	"graphql":       "graphql",
	"gql":           "graphql",
	"haskell":       "haskell",
	"hs":            "haskell",
	"hcl":           "hcl",
	"tf":            "hcl",
	"tfvars":        "hcl",
	"terraform":     "hcl",
	"html":          "html",
	"htm":           "html",
	"xhtml":         "html",
	"ini":           "ini",
	"cfg":           "ini",
	"conf":          "ini",
	"config":        "ini",
	"properties":    "ini",
	"java":          "java",
	"javascript":    "javascript",
	"js":            "javascript",
	"jsx":           "javascript",
	"mjs":           "javascript",
	"cjs":           "javascript",
	"json":          "json",
	"just":          "just",
	"justfile":      "just",
	"julia":         "julia",
	"jl":            "julia",
	"kotlin":        "kotlin",
	"kt":            "kotlin",
	"kts":           "kotlin",
	"ktm":           "kotlin",
	"lua":           "lua",
	"make":          "make",
	"makefile":      "make",
	"gnumake":       "make",
	"mk":            "make",
	"mak":           "make",
	"markdown":      "markdown",
	"md":            "markdown",
	"mdx":           "markdown",
	"nix":           "nix",
	"objc":          "objc",
	"obj-c":         "objc",
	"objective-c":   "objc",
	"m":             "objc",
	"mm":            "objc",
	"ocaml":         "ocaml",
	"ml":            "ocaml",
	"odin":          "odin",
	"php":           "php",
	"powershell":    "powershell",
	"ps1":           "powershell",
	"psm1":          "powershell",
	"protobuf":      "protobuf",
	"proto":         "protobuf",
	"python":        "python",
	"py":            "python",
	"py3":           "python",
	"pyi":           "python",
	"r":             "r",
	"regex":         "regex",
	"re":            "regex",
	"ruby":          "ruby",
	"rb":            "ruby",
	"rbw":           "ruby",
	"gemspec":       "ruby",
	"rust":          "rust",
	"rs":            "rust",
	"scala":         "scala",
	"sc":            "scala",
	"sbt":           "scala",
	"solidity":      "solidity",
	"sol":           "solidity",
	"sql":           "sql",
	"starlark":      "starlark",
	"star":          "starlark",
	"bzl":           "starlark",
	"bazel":         "starlark",
	"skylark":       "starlark",
	"svelte":        "svelte",
	"swift":         "swift",
	"toml":          "toml",
	"tla":           "tlaplus",
	"tla+":          "tlaplus",
	"tlaplus":       "tlaplus",
	"pluscal":       "tlaplus",
	"pcal":          "tlaplus",
	"tsx":           "tsx",
	"typescript":    "typescript",
	"ts":            "typescript",
	"mts":           "typescript",
	"cts":           "typescript",
	"verilog":       "verilog",
	"systemverilog": "verilog",
	"sv":            "verilog",
	"svh":           "verilog",
	"vh":            "verilog",
	"v":             "verilog",
	"vue":           "vue",
	"xml":           "xml",
	"xsl":           "xml",
	"xslt":          "xml",
	"svg":           "xml",
	"plist":         "xml",
	"yaml":          "yaml",
	"yml":           "yaml",
	"zig":           "zig",
}

// sortedLangAliases caches the sorted alias list for omp's unsupported-language error.
var sortedLangAliases = func() string {
	keys := make([]string, 0, len(langAliases))
	for k := range langAliases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}()

// unsupportedLangError matches crates/ast/src/ops.rs:25-29.
func unsupportedLangError(value string) error {
	return fmt.Errorf("Unsupported language '%s'. Supported: %s", value, sortedLangAliases)
}

// extToLang maps lowercased file extensions (without leading dot) to canonical language names.
// upstream: crates/ast/src/language/mod.rs:654-717
var extToLang = map[string]string{
	"astro": "astro",
	"bash":  "bash", "bats": "bash", "cgi": "bash", "command": "bash", "env": "bash", "fcgi": "bash", "ksh": "bash", "sh": "bash", "tmux": "bash", "tool": "bash", "zsh": "bash",
	"c": "c", "h": "c",
	"cmake": "cmake",
	"cc":    "cpp", "hpp": "cpp", "cpp": "cpp", "c++": "cpp", "hh": "cpp", "cxx": "cpp", "cu": "cpp", "cuh": "cpp", "ino": "cpp",
	"cs":   "csharp",
	"dart": "dart",
	"clj":  "clojure", "cljs": "clojure", "cljc": "clojure", "edn": "clojure",
	"css": "css", "scss": "css",
	"diff": "diff", "patch": "diff",
	"dockerfile": "dockerfile",
	"el":         "emacs-lisp",
	"ex":         "elixir", "exs": "elixir",
	"erl": "erlang", "hrl": "erlang",
	"f90": "fortran", "f95": "fortran", "f03": "fortran", "f08": "fortran",
	"go":      "go",
	"graphql": "graphql", "gql": "graphql",
	"hs":  "haskell",
	"hcl": "hcl", "tf": "hcl", "tfvars": "hcl",
	"html": "html", "htm": "html", "xhtml": "html",
	"ini": "ini", "cfg": "ini", "conf": "ini", "properties": "ini",
	"java": "java",
	"cjs":  "javascript", "js": "javascript", "mjs": "javascript", "jsx": "javascript",
	"json": "json",
	"jl":   "julia",
	"kt":   "kotlin", "ktm": "kotlin", "kts": "kotlin",
	"lua": "lua",
	"mk":  "make", "mak": "make",
	"md": "markdown", "markdown": "markdown", "mdx": "markdown",
	"nix":  "nix",
	"m":    "objc",
	"ml":   "ocaml",
	"odin": "odin",
	"php":  "php",
	"ps1":  "powershell", "psm1": "powershell",
	"proto": "protobuf",
	"py":    "python", "py3": "python", "pyi": "python",
	"r":  "r",
	"rb": "ruby", "rbw": "ruby", "gemspec": "ruby",
	"rs":    "rust",
	"scala": "scala", "sc": "scala", "sbt": "scala",
	"sol":  "solidity",
	"sql":  "sql",
	"star": "starlark", "bzl": "starlark",
	"svelte": "svelte",
	"swift":  "swift",
	"toml":   "toml",
	"tla":    "tlaplus",
	"tsx":    "tsx",
	"ts":     "typescript", "cts": "typescript", "mts": "typescript",
	"v": "verilog", "sv": "verilog", "svh": "verilog", "vh": "verilog",
	"vue": "vue",
	"xml": "xml", "xsl": "xml", "xslt": "xml", "svg": "xml", "plist": "xml",
	"yaml": "yaml", "yml": "yaml",
	"zig": "zig",
}

// stockAstGrepLanguages is the set of canonical omp language names built into
// stock ast-grep CLI (>= 0.25, https://ast-grep.github.io/reference/languages.html,
// ast-grep crates/language/src/lib.rs SupportLang enum).
// Languages outside this set (e.g. toml, make, dockerfile, xml, sql, tlaplus, …)
// are rejected by clap in stock ast-grep and are skipped with omp's missing-grammar
// note instead of producing bogus parse errors on mixed directories.
var stockAstGrepLanguages = map[string]bool{
	"bash":       true,
	"c":          true,
	"cpp":        true,
	"csharp":     true,
	"css":        true,
	"elixir":     true,
	"go":         true,
	"haskell":    true,
	"hcl":        true,
	"html":       true,
	"java":       true,
	"javascript": true,
	"json":       true,
	"kotlin":     true,
	"lua":        true,
	"nix":        true,
	"php":        true,
	"python":     true,
	"ruby":       true,
	"rust":       true,
	"scala":      true,
	"solidity":   true,
	"swift":      true,
	"tsx":        true,
	"typescript": true,
	"yaml":       true,
}

// stockAstGrepExtToLang mirrors stock ast-grep's own case-sensitive file extension
// inference (`SgLang::from_path` in ast-grep crates/language/src/lib.rs).
// When a file's extension does not map here to the exact requested language
// (for example `.h` under `--lang cpp`, `.ts` under `--lang tsx`, `.js` under
// `--lang typescript`, uppercase `.PY`, dotfiles like `.zshrc`, or extensionless
// files), stock ast-grep's `RunWithSpecificLang::produce_item` skips the file or
// only runs injected sub-language matching. Those files must be fed via `--stdin`
// instead of passing their path to `ast-grep run`.
var stockAstGrepExtToLang = map[string]string{
	"bash": "bash", "bats": "bash", "cgi": "bash", "command": "bash", "env": "bash", "fcgi": "bash", "ksh": "bash", "sh": "bash", "tmux": "bash", "tool": "bash", "zsh": "bash",
	"c": "c", "h": "c",
	"cc": "cpp", "hpp": "cpp", "cpp": "cpp", "c++": "cpp", "hh": "cpp", "cxx": "cpp", "cu": "cpp", "ino": "cpp",
	"cs":  "csharp",
	"css": "css", "scss": "css",
	"ex": "elixir", "exs": "elixir",
	"go":  "go",
	"hs":  "haskell",
	"hcl": "hcl", "tf": "hcl", "tfvars": "hcl",
	"html": "html", "htm": "html", "xhtml": "html",
	"java": "java",
	"cjs":  "javascript", "js": "javascript", "mjs": "javascript", "jsx": "javascript",
	"json": "json",
	"kt":   "kotlin", "ktm": "kotlin", "kts": "kotlin",
	"lua": "lua",
	"nix": "nix",
	"php": "php",
	"py":  "python", "py3": "python", "pyi": "python",
	"rb": "ruby", "rbw": "ruby", "gemspec": "ruby",
	"rs":    "rust",
	"scala": "scala", "sc": "scala", "sbt": "scala",
	"sol":   "solidity",
	"swift": "swift",
	"tsx":   "tsx",
	"ts":    "typescript", "cts": "typescript", "mts": "typescript",
	"yaml": "yaml", "yml": "yaml",
}

// rustPathExtension replicates Rust's std::path::Path::extension semantics:
// returns the substring after the last '.' in the file name, EXCEPT when the
// only '.' is at index 0 (dotfiles like `.env` or `.bashrc` have no extension).
// upstream: crates/ast/src/language/mod.rs:769
func rustPathExtension(name string) (string, bool) {
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || dot == len(name)-1 {
		return "", false
	}
	return name[dot+1:], true
}

// cliNativeLangForPath returns the language stock ast-grep CLI's SgLang::from_path
// infers for path p (case-sensitive extension lookup, no dotfile/rc special cases).
// Source: https://github.com/ast-grep/ast-grep crates/language/src/lib.rs (from_extension).
func cliNativeLangForPath(p string) (string, bool) {
	ext, ok := rustPathExtension(filepath.Base(p))
	if !ok {
		return "", false
	}
	lang, found := stockAstGrepExtToLang[ext]
	return lang, found
}

// needsStdinForCLI reports whether stock ast-grep CLI will refuse to parse path p
// directly as targetLang under `ast-grep run --lang <targetLang> -- <p>`.
// In ast-grep crates/cli/src/run.rs (`RunWithSpecificLang::produce_item`),
// files whose `SgLang::from_path(p)` is None or != targetLang are skipped or
// treated as injection hosts rather than parsed in targetLang. Feeding the file
// bytes via `--stdin` forces ast-grep to parse the entire file as targetLang.
func needsStdinForCLI(p, targetLang string) bool {
	native, ok := cliNativeLangForPath(p)
	return !ok || native != targetLang
}

// resolveLangAlias normalizes a language alias to its canonical name.
// Returns ("", false) when unrecognized.
func resolveLangAlias(raw string) (string, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return "", false
	}
	canon, ok := langAliases[trimmed]
	return canon, ok
}

// inferLangFromPath guesses a canonical language name from the filename or extension.
// upstream: crates/ast/src/language/mod.rs:720-781
func inferLangFromPath(p string) (string, bool) {
	name := filepath.Base(p)
	switch name {
	case "Makefile", "makefile", "GNUmakefile":
		return "make", true
	case "Justfile", "justfile":
		return "just", true
	case "CMakeLists.txt":
		return "cmake", true
	case ".emacs":
		return "emacs-lisp", true
	}
	if name == "Dockerfile" || name == "dockerfile" ||
		strings.HasPrefix(name, "Dockerfile.") || strings.HasPrefix(name, "dockerfile.") ||
		name == "Containerfile" || name == "containerfile" {
		return "dockerfile", true
	}
	// upstream: crates/ast/src/language/mod.rs:747-767
	stem := strings.TrimPrefix(name, ".")
	switch stem {
	case "zshrc", "zshenv", "zprofile", "zlogin", "zlogout", "zsh_aliases",
		"bashrc", "bash_profile", "bash_login", "bash_logout", "bash_aliases",
		"profile", "kshrc", "mkshrc", "shrc":
		return "bash", true
	}
	ext, ok := rustPathExtension(name)
	if !ok {
		return "", false
	}
	// GCC's convention: uppercase `.C` and `.H` are C++, not C.
	// upstream: crates/ast/src/language/mod.rs:770-773
	if ext == "C" || ext == "H" {
		return "cpp", true
	}
	canon, found := extToLang[strings.ToLower(ext)]
	return canon, found
}

// langParsesAs reports whether a file inferred as inferredLang should be kept
// when explicitLang is requested.
// upstream: crates/ast/src/language/mod.rs:491-498
func langParsesAs(inferredLang, explicitLang string) bool {
	if inferredLang == explicitLang {
		return true
	}
	switch {
	case inferredLang == "c" && (explicitLang == "cpp" || explicitLang == "objc"):
		return true
	case inferredLang == "javascript" && (explicitLang == "typescript" || explicitLang == "tsx"):
		return true
	case inferredLang == "typescript" && explicitLang == "tsx":
		return true
	default:
		return false
	}
}

// missingLanguagesNote formats omp's missing-grammar note for skipped languages.
// upstream: packages/coding-agent/src/utils/grammars.ts:151-153
func missingLanguagesNote(langs []string) string {
	if len(langs) == 0 {
		return ""
	}
	return fmt.Sprintf("Skipped %s files: language is not supported by the installed ast-grep binary; results exclude them.", strings.Join(langs, ", "))
}
