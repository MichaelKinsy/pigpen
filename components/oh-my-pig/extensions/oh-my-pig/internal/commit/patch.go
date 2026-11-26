package commit

import (
	"fmt"
	"strings"
)

// upstream: packages/coding-agent/src/commit/selective/patch.ts,
//           packages/coding-agent/src/utils/git-diff-paths.ts

const (
	patchContext = 3
	patchNoEOL   = "\x00no-eol"
	noEOLMarker  = `\ No newline at end of file`
)

// FileState holds a file's text and git mode ("100644", "100755", "160000") on one side of a change.
// upstream: draft.ts:77
type FileState struct {
	Text string
	Mode string
}

// EncodeGitDiffPath encodes a path for a Git diff record, C-quoting bytes that cannot appear verbatim.
// upstream: packages/coding-agent/src/utils/git-diff-paths.ts:23
func EncodeGitDiffPath(p string) string {
	needsQuote := false
	for i := range len(p) {
		b := p[i]
		if b == '"' || b == '\\' || b < 0x20 || b == 0x7f {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return p
	}
	var b strings.Builder
	b.Grow(len(p) + 4)
	b.WriteByte('"')
	for _, r := range p {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\%03o`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func patchLines(text *string) []string {
	if text == nil || *text == "" {
		return nil
	}
	lines := strings.Split(*text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	} else {
		lines[len(lines)-1] += patchNoEOL
	}
	return lines
}

func renderPatchLine(b *strings.Builder, prefix byte, key string) {
	if body, ok := strings.CutSuffix(key, patchNoEOL); ok {
		b.WriteByte(prefix)
		b.WriteString(body)
		b.WriteByte('\n')
		b.WriteString(noEOLMarker)
		b.WriteByte('\n')
		return
	}
	b.WriteByte(prefix)
	b.WriteString(key)
	b.WriteByte('\n')
}

func hunkRange(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

// FormatTextPatch produces a unified git patch turning before into after at repo-relative path.
// nil means file absent. Returns "" when content and mode are equal.
// upstream: patch.ts:40
func FormatTextPatch(path string, before, after *FileState) string {
	if (before == nil && after == nil) || (before != nil && after != nil && before.Text == after.Text && before.Mode == after.Mode) {
		return ""
	}
	var oldTextPtr, newTextPtr *string
	if before != nil {
		oldTextPtr = &before.Text
	}
	if after != nil {
		newTextPtr = &after.Text
	}
	oldLines := patchLines(oldTextPtr)
	newLines := patchLines(newTextPtr)
	oldPath := EncodeGitDiffPath("a/" + path)
	newPath := EncodeGitDiffPath("b/" + path)

	var out strings.Builder
	fmt.Fprintf(&out, "diff --git %s %s\n", oldPath, newPath)
	switch {
	case before == nil && after != nil:
		fmt.Fprintf(&out, "new file mode %s\n", after.Mode)
	case before != nil && after == nil:
		fmt.Fprintf(&out, "deleted file mode %s\n", before.Mode)
	case before != nil && after != nil && before.Mode != after.Mode:
		fmt.Fprintf(&out, "old mode %s\nnew mode %s\n", before.Mode, after.Mode)
	}
	regions := ChangedRegions(oldLines, newLines)
	if len(regions) == 0 {
		return out.String()
	}
	if before == nil {
		out.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(&out, "--- %s\n", oldPath)
	}
	if after == nil {
		out.WriteString("+++ /dev/null\n")
	} else {
		fmt.Fprintf(&out, "+++ %s\n", newPath)
	}

	first := 0
	for first < len(regions) {
		last := first
		for last+1 < len(regions) && regions[last+1].I1-regions[last].I2 <= 2*patchContext {
			last++
		}
		oldStart := regions[first].I1 - patchContext
		if oldStart < 0 {
			oldStart = 0
		}
		oldEnd := regions[last].I2 + patchContext
		if oldEnd > len(oldLines) {
			oldEnd = len(oldLines)
		}
		newStart := regions[first].J1 - (regions[first].I1 - oldStart)
		newEnd := regions[last].J2 + (oldEnd - regions[last].I2)
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(oldStart, oldEnd-oldStart), hunkRange(newStart, newEnd-newStart))
		cursor := oldStart
		for idx := first; idx <= last; idx++ {
			reg := regions[idx]
			for ; cursor < reg.I1; cursor++ {
				renderPatchLine(&out, ' ', oldLines[cursor])
			}
			for n := reg.I1; n < reg.I2; n++ {
				renderPatchLine(&out, '-', oldLines[n])
			}
			for n := reg.J1; n < reg.J2; n++ {
				renderPatchLine(&out, '+', newLines[n])
			}
			cursor = reg.I2
		}
		for ; cursor < oldEnd; cursor++ {
			renderPatchLine(&out, ' ', oldLines[cursor])
		}
		first = last + 1
	}
	return out.String()
}
