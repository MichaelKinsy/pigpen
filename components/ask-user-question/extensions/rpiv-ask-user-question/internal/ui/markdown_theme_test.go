package ui

import (
	"strings"
	"testing"
)

// tokenTheme wraps every styled run in its token name, so a test reads which token styled what.
type tokenTheme struct{ identityTheme }

func (tokenTheme) Fg(color, text string) string { return "<" + color + ">" + text + "</" + color + ">" }

// getMarkdownTheme().highlightCode: a fenced block in a language highlight.js knows is coloured through the theme's
// syntax tokens (buildCliHighlightTheme); with no language, or one it does not know, every line takes mdCodeBlock.
func TestMarkdownThemeHighlightsFencedCode(t *testing.T) {
	theme := GetMarkdownTheme(tokenTheme{})
	if got := theme.HighlightCode("package main", "go"); len(got) != 1 || !strings.Contains(got[0], "<syntaxKeyword>package</syntaxKeyword>") {
		t.Fatalf("go: %q", got)
	}
	if got := theme.HighlightCode("a: 1", "yaml"); len(got) != 1 || !strings.Contains(got[0], "<syntax") {
		t.Fatalf("yaml (loaded with every language): %q", got)
	}
	if got := theme.HighlightCode("a\nb", "klingon"); len(got) != 2 || got[0] != "<mdCodeBlock>a</mdCodeBlock>" || got[1] != "<mdCodeBlock>b</mdCodeBlock>" {
		t.Fatalf("unknown language: %q", got)
	}
	if got := theme.HighlightCode("plain", ""); len(got) != 1 || got[0] != "<mdCodeBlock>plain</mdCodeBlock>" {
		t.Fatalf("no language: %q", got)
	}
}

// The syntax tokens are the ones theme.js buildCliHighlightTheme maps.
func TestCliHighlightThemeMapsPiTokens(t *testing.T) {
	theme := cliHighlightTheme(tokenTheme{})
	for scope, token := range map[string]string{
		"keyword": "syntaxKeyword", "built_in": "syntaxType", "literal": "syntaxNumber", "number": "syntaxNumber",
		"regexp": "syntaxString", "string": "syntaxString", "subst": "text", "comment": "syntaxComment", "doctag": "syntaxComment",
		"meta": "muted", "function": "syntaxFunction", "title": "syntaxFunction", "class": "syntaxType", "type": "syntaxType",
		"tag": "syntaxPunctuation", "name": "syntaxKeyword", "attr": "syntaxVariable", "variable": "syntaxVariable",
		"params": "syntaxVariable", "operator": "syntaxOperator", "punctuation": "syntaxPunctuation",
		"addition": "toolDiffAdded", "deletion": "toolDiffRemoved",
	} {
		format, ok := theme[scope]
		if !ok {
			t.Errorf("no entry for %s", scope)
			continue
		}
		if got, want := format("x"), "<"+token+">x</"+token+">"; got != want {
			t.Errorf("%s = %q, want %q", scope, got, want)
		}
	}
	for _, scope := range []string{"emphasis", "strong", "link"} {
		if _, ok := theme[scope]; !ok {
			t.Errorf("no entry for %s", scope)
		}
	}
}
