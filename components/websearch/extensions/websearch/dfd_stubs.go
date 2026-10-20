package websearch

import (
	"strings"
	"unicode/utf8"
)

// Temporary stand-ins, replaced as the remaining ports land.

func dfdMathMLToLatex(mathml string) (string, bool) { return "", false }

func dfdRenderLatexToMathML(latex string, display bool) string { return "" }

func lastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }

func jsDecodeURI(s string) string { return s }

func jsDecodeURIComponent(s string) (string, bool) { return s, true }

var _ = strings.ToLower
