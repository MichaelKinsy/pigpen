package files

import (
	"fmt"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// generateUnifiedPatch calls sdk.GenerateUnifiedPatch when connected to a PiG host
// (or when SDK host helper hooks are installed), and falls back to a standard unified
// patch generator in standalone unit tests without a host socket.
func generateUnifiedPatch(ctx sdk.Context, path, oldContent, newContent string) string {
	if os.Getenv("PIG_EXT_SOCKET") != "" {
		if patch, err := ctx.GenerateUnifiedPatch(path, oldContent, newContent); err == nil {
			return patch
		}
	}
	if patch, err := sdk.GenerateUnifiedPatch(path, oldContent, newContent); err == nil {
		return patch
	}
	return fallbackUnifiedPatch(path, oldContent, newContent, 4)
}

// fallbackUnifiedPatch builds a standard unified patch with `--- <path>` / `+++ <path>`
// headers matching PiG's GenerateUnifiedPatch for standalone unit tests without a host.
func fallbackUnifiedPatch(path, oldContent, newContent string, context int) string {
	if oldContent == newContent {
		return "--- " + path + "\n+++ " + path + "\n"
	}
	oldTokens := splitPatchTokens(oldContent)
	newTokens := splitPatchTokens(newContent)

	// Find common prefix and suffix.
	prefix := 0
	for prefix < len(oldTokens) && prefix < len(newTokens) && oldTokens[prefix] == newTokens[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldTokens)-prefix && suffix < len(newTokens)-prefix &&
		oldTokens[len(oldTokens)-1-suffix] == newTokens[len(newTokens)-1-suffix] {
		suffix++
	}

	ctxStart := max(0, prefix-context)
	oldEnd := min(len(oldTokens), len(oldTokens)-suffix+context)
	newEnd := min(len(newTokens), len(newTokens)-suffix+context)

	oldCount := oldEnd - ctxStart
	newCount := newEnd - ctxStart
	oldStart := ctxStart + 1
	if oldCount == 0 {
		oldStart = 0
	}
	newStart := ctxStart + 1
	if newCount == 0 {
		newStart = 0
	}

	var b strings.Builder
	b.WriteString("--- ")
	b.WriteString(path)
	b.WriteByte('\n')
	b.WriteString("+++ ")
	b.WriteString(path)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)

	for i := ctxStart; i < prefix; i++ {
		writePatchToken(&b, ' ', oldTokens[i])
	}
	for i := prefix; i < len(oldTokens)-suffix; i++ {
		writePatchToken(&b, '-', oldTokens[i])
	}
	for i := prefix; i < len(newTokens)-suffix; i++ {
		writePatchToken(&b, '+', newTokens[i])
	}
	for i := len(newTokens) - suffix; i < newEnd; i++ {
		writePatchToken(&b, ' ', newTokens[i])
	}
	return b.String()
}

func splitPatchTokens(s string) []string {
	if s == "" {
		return nil
	}
	var tokens []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			tokens = append(tokens, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		tokens = append(tokens, s[start:])
	}
	return tokens
}

func writePatchToken(b *strings.Builder, prefix byte, tok string) {
	if body, ok := strings.CutSuffix(tok, "\n"); ok {
		b.WriteByte(prefix)
		b.WriteString(body)
		b.WriteByte('\n')
		return
	}
	b.WriteByte(prefix)
	b.WriteString(tok)
	b.WriteString("\n\\ No newline at end of file\n")
}
