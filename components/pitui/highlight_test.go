package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

// Tests of the highlight.js bridge, adapted from PiG v0.5.0's highlight_test.go: PiG's version drives the active theme,
// pitui's HighlightMarkdownCode takes the theme as arguments.

// cliTestTheme maps the scopes the tests use to markers, so the assertions read the scope that styled each run.
func cliTestTheme() HighlightTheme {
	mark := func(name string) HighlightFormatter {
		return func(s string) string { return "<" + name + ">" + s + "</" + name + ">" }
	}
	return HighlightTheme{
		"keyword": mark("kw"), "string": mark("str"), "number": mark("num"), "comment": mark("cm"), "subst": mark("sub"),
		"built_in": mark("bi"), "title": mark("fn"), "function": mark("fn"), "params": mark("var"), "literal": mark("num"),
	}
}

func codeBlockMark(s string) string { return "[" + s + "]" }

func TestHighlightMarkdownCodeColorsKeywords(t *testing.T) {
	LoadAllHighlightLanguages()
	out := HighlightMarkdownCode("package main", "go", cliTestTheme(), codeBlockMark)
	if len(out) != 1 || !strings.Contains(out[0], "<kw>package</kw>") {
		t.Fatalf("package is not a keyword run: %q", out)
	}
}

func TestHighlightMarkdownCodeKeepsLineStructure(t *testing.T) {
	LoadAllHighlightLanguages()
	out := HighlightMarkdownCode("func f() {\n    return\n}", "go", cliTestTheme(), codeBlockMark)
	if len(out) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(out), out)
	}
	if !strings.Contains(out[0], "<kw>func</kw>") || !strings.Contains(out[2], "}") {
		t.Errorf("content lost: %q", out)
	}
	if got := HighlightMarkdownCode("a\nb\nc\n", "python", cliTestTheme(), codeBlockMark); len(got) != 4 {
		t.Errorf("line count drift: got %d want 4: %q", len(got), got)
	}
}

// packages/coding-agent/test/syntax-highlight.test.ts "colors each line of Python docstrings independently": a token
// that spans lines is formatted once per non-empty line, so every line keeps its color and an empty line stays empty.
func TestHighlightMarkdownCodeColorsEachLineOfPythonDocstringsIndependently(t *testing.T) {
	LoadAllHighlightLanguages()
	code := "\"\"\"\nline one\n\nline two\n\"\"\"\nafter"
	want := []string{"<str>\"\"\"</str>", "<str>line one</str>", "", "<str>line two</str>", "<str>\"\"\"</str>", "after"}
	if got := HighlightMarkdownCode(code, "python", cliTestTheme(), codeBlockMark); !slices.Equal(got, want) {
		t.Errorf("HighlightMarkdownCode = %q, want %q", got, want)
	}
}

// A language highlight.js does not support, or none, takes the code block style on every line; Pi never auto-detects.
func TestHighlightMarkdownCodeFallsBackToTheCodeBlockStyle(t *testing.T) {
	LoadAllHighlightLanguages()
	want := []string{"[hello]", "[world]"}
	if got := HighlightMarkdownCode("hello\nworld", "klingon", cliTestTheme(), codeBlockMark); !slices.Equal(got, want) {
		t.Errorf("unknown language: %q, want %q", got, want)
	}
	if got := HighlightMarkdownCode("hello\nworld", "", cliTestTheme(), codeBlockMark); !slices.Equal(got, want) {
		t.Errorf("no language: %q, want %q", got, want)
	}
}

// The eager languages are registered before LoadAllHighlightLanguages and the rest after it (utils/syntax-highlight.ts).
func TestSupportsLanguageEagerAndAll(t *testing.T) {
	if !SupportsLanguage("typescript") || !SupportsLanguage("py") {
		t.Fatal("the eager languages and aliases are supported before the full load")
	}
	LoadAllHighlightLanguages()
	for _, name := range []string{"yaml", "toml", "sql", "html", "xml", "diff"} {
		if !SupportsLanguage(name) {
			t.Errorf("%s is not supported after loading every language", name)
		}
	}
	if SupportsLanguage("klingon") {
		t.Error("klingon is not a highlight.js language")
	}
}

func TestHighlightIsRaceFree(t *testing.T) {
	LoadAllHighlightLanguages()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				HighlightMarkdownCode("func main() {\n\treturn\n}\n", "go", cliTestTheme(), codeBlockMark)
			}
		})
	}
	wg.Wait()
}

// renderHighlightedHtml reads theme[scope] on an object literal, so a scope naming an Object.prototype member finds
// that member and calls it with an undefined receiver, and an own undefined property shadows the member
// (utils/syntax-highlight.ts getScopeFormatter and getActiveFormatter). Expected strings and TypeError messages are
// Pi 1.0.0's renderHighlightedHtml output under Node 24, as PiG records them.
func TestRenderHighlightedHtmlReadsObjectPrototypeMembers(t *testing.T) {
	const notObject = "Cannot convert undefined or null to object"
	for _, tc := range []struct {
		scope, want, err string
	}{
		{"constructor", "xy", ""},
		{"toString", "[object Undefined]y", ""},
		{"toString.x", "[object Undefined]y", ""},
		{"isPrototypeOf", "falsey", ""},
		{"isPrototypeOf-y", "falsey", ""},
		{"default", "xy", ""},
		{"toLocaleString", "", "Object.prototype.toLocaleString called on null or undefined"},
		{"__proto__", "", "formatter is not a function"},
		{"valueOf", "", notObject},
		{"hasOwnProperty", "", notObject},
		{"propertyIsEnumerable", "", notObject},
		{"__defineGetter__", "", notObject},
		{"__defineSetter__", "", notObject},
		{"__lookupGetter__", "", notObject},
		{"__lookupSetter__", "", notObject},
	} {
		got, err := renderHighlightedHTML(`<span class="hljs-`+tc.scope+`">x</span>y`, HighlightTheme{})
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if got != tc.want || gotErr != tc.err {
			t.Errorf("%s: got %q, error %q; want %q, error %q", tc.scope, got, gotErr, tc.want, tc.err)
		}
	}
	defaultFormatter := func(text string) string { return "D" + text }
	if got := RenderHighlightedHtml(`<span class="hljs-toString">x</span>`, HighlightTheme{"toString": nil, "default": defaultFormatter}); got != "Dx" {
		t.Errorf("own undefined toString: got %q, want %q", got, "Dx")
	}
	if got := RenderHighlightedHtml(`<span class="hljs-keyword">x</span>y`, HighlightTheme{"keyword": nil, "default": defaultFormatter}); got != "DxDy" {
		t.Errorf("own undefined keyword: got %q, want %q", got, "DxDy")
	}
}
