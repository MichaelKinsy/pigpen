package tintinweb_subagents

import (
	"runtime"
	"strings"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func goosForTest() string { return runtime.GOOS }
