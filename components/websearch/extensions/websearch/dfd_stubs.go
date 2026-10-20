package websearch

import (
	"strings"
	"unicode/utf8"
)

// Temporary stand-ins, replaced as the remaining ports land.

func lastRune(s string) (rune, int) { return utf8.DecodeLastRuneInString(s) }

var _ = strings.ToLower
