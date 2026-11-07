package eq

import "strings"

// SystemDiff modes of a scenario (Scenario.SystemDiff).
const (
	// SystemDiffSpan is the original delta: the text between the longest common prefix and suffix, which holds
	// every host line that sits between two of the extension's changes.
	SystemDiffSpan = "span"
	// SystemDiffHunks reports each changed run of lines on its own, so host wording between an extension's
	// separate changes (a tool listed in the tools section and guidance appended at the end) is never compared.
	SystemDiffHunks = "hunks"
	// SystemDiffInserted is SystemDiffHunks reduced to what each hunk inserted: the lines a request has that the
	// host's own baseline prompt lacks. For a request whose base text is host-owned and worded differently by
	// design (the host's summarization request): the host's own removed wording is not compared, the added text is.
	SystemDiffInserted = "inserted"
)

// maxDiffCells bounds the line-diff table; a prompt larger than this falls back to the span delta.
const maxDiffCells = 4 << 20

// systemDeltaMode is the delta of one request under the scenario's SystemDiff mode.
func systemDeltaMode(mode, base, got string) (any, bool) {
	switch mode {
	case SystemDiffHunks:
		return systemHunks(base, got)
	case SystemDiffInserted:
		v, ok := systemHunks(base, got)
		m, isHunks := v.(map[string]any)
		if !ok || !isHunks {
			return v, ok
		}
		hs, _ := m["hunks"].([]any)
		var out []any
		for _, h := range hs {
			if hm := h.(map[string]any); hm["inserted"] != "" {
				out = append(out, map[string]any{"inserted": hm["inserted"]})
			}
		}
		if len(out) == 0 {
			return nil, false
		}
		return map[string]any{"hunks": out}, true
	}
	return systemDelta(base, got)
}

// systemHunks diffs the two prompts line by line (longest common subsequence) and reports every changed run of
// lines, in prompt order, as {"inserted","removed"} (each the run's lines joined by newlines). It reports false when
// the texts are equal. The position of a hunk is its order only: the lines around it are the host's own wording.
func systemHunks(base, got string) (any, bool) {
	if base == got {
		return nil, false
	}
	a, b := strings.Split(base, "\n"), strings.Split(got, "\n")
	// Trim the common prefix and suffix first: it keeps the table small.
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	a, b = a[p:len(a)-s], b[p:len(b)-s]
	if (len(a)+1)*(len(b)+1) > maxDiffCells {
		return systemDelta(base, got)
	}
	// lcs[i][j]: length of the longest common subsequence of a[i:] and b[j:].
	w := len(b) + 1
	lcs := make([]int32, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else {
				lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
			}
		}
	}
	var hunks []any
	var removed, inserted []string
	flush := func() {
		if len(removed) > 0 || len(inserted) > 0 {
			hunks = append(hunks, map[string]any{"inserted": strings.Join(inserted, "\n"), "removed": strings.Join(removed, "\n")})
			removed, inserted = nil, nil
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			flush()
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i*w+j+1] >= lcs[(i+1)*w+j]):
			inserted = append(inserted, b[j])
			j++
		default:
			removed = append(removed, a[i])
			i++
		}
	}
	flush()
	return map[string]any{"hunks": hunks}, true
}
