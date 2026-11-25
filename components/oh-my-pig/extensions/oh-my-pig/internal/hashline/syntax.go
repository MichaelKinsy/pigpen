package hashline

import (
	"path/filepath"
	"strings"
)

// Language classification and syntax helpers for block coverage and syntax checks.
// Note: oh-my-pig is pure Go with no tree-sitter/cgo, so tree-sitter-backed
// parse probes (parsesCleanly, syntaxErrorSpans, nodeChain) are unavailable and
// degrade conservatively as documented in UPSTREAM.md.
// upstream: crates/edit/src/modes/hashline/syntax.rs, crates/ast/src/language/mod.rs

type langKind int

const (
	langUnknown    langKind = iota
	langC                   // unused for block resolution (C/C++ routes to langOtherKnown per W8)
	langCSS                 // unused for block resolution (CSS/SCSS routes to langOtherKnown per C1)
	langJSON                // JSON (.json)
	langJS                  // JS, MJS, CJS, TS, MTS, CTS (brace + template literals + regex literals)
	langJSX                 // JSX, TSX (JS + JSX tags)
	langRust                // Rust (brace + lifetimes 'a + raw strings r#"..."# + outer attributes #[...] / ///)
	langGo                  // Go (brace + backtick raw strings)
	langPython              // Python (.py, .pyi)
	langYAML                // YAML (.yml, .yaml)
	langMarkdown            // Markdown (.md, .markdown, .mdx)
	langEmbedded            // Vue, Svelte, Astro (NoEmbedded coverage)
	langOtherKnown          // Known language whose grammar is not linked or lexical/block rules are not modeled in pure Go
)

func detectLang(path string) langKind {
	if path == "" {
		return langUnknown
	}
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile", "makefile", "justfile", "cmakelists.txt":
		return langOtherKnown
	}
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		return langJS
	case ".jsx", ".tsx":
		return langJSX
	case ".rs":
		return langRust
	case ".go":
		return langGo
	case ".py", ".pyi":
		return langPython
	case ".yml", ".yaml":
		return langYAML
	case ".md", ".markdown", ".mdx":
		return langMarkdown
	case ".vue", ".svelte", ".astro":
		return langEmbedded
	case ".json":
		return langJSON
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh",
		".css", ".scss",
		".proto", ".sol", ".zig", ".bzl", ".star",
		".sh", ".bash", ".zsh",
		".cs", ".java", ".kt", ".kts", ".swift", ".dart", ".scala",
		".php", ".hcl", ".tf", ".tfvars", ".m", ".mm",
		".groovy", ".gradle", ".nix", ".odin", ".ps1", ".psm1", ".r", ".graphql", ".gql",
		".rb", ".lua", ".html", ".htm", ".xml", ".svg", ".xhtml",
		".sql", ".toml", ".ini", ".cfg", ".conf", ".ex", ".exs",
		".erl", ".hs", ".ml", ".mli", ".jl", ".clj", ".cljs", ".el",
		".f", ".f90", ".v", ".sv", ".tla", ".diff", ".patch", ".cmake", ".mk", ".just":
		return langOtherKnown
	default:
		// Includes .jsonc, .hxx, .pyw (not in omp's crates/ast/src/language/mod.rs extension table -> BlockCoverageNoGrammar).
		return langUnknown
	}
}

// BlockCoverageForPath returns how far block locators reach in path's language.
// upstream: crates/edit/src/modes/hashline/block.rs:305
func BlockCoverageForPath(path string) BlockCoverage {
	switch detectLang(path) {
	case langUnknown:
		return BlockCoverageNoGrammar
	case langEmbedded:
		return BlockCoverageNoEmbedded
	default:
		return BlockCoverageFull
	}
}

// hasSyntaxOracle reports whether a language would have a syntax oracle in omp.
// Because this Go port has no tree-sitter runtime, parsesCleanly always returns false.
// upstream: crates/edit/src/modes/hashline/syntax.rs:162
func hasSyntaxOracle(path string) bool {
	return false
}

// parsesCleanly is unavailable without tree-sitter; returns false conservatively.
// upstream: crates/edit/src/modes/hashline/syntax.rs:100
func parsesCleanly(path, text string) bool {
	return false
}
