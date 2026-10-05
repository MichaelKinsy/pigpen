package warden

import (
	"strconv"
	"strings"
)

// DiffOptions configure StuckDiff.
type DiffOptions struct {
	DiffLimit int
	TailLimit int
	FullPath  string
}

type diffOp struct{ op, line string }

// lineDiff is an LCS-based line diff between two outputs, capped at diffLimit characters. Both inputs must be
// pre-redacted.
func lineDiff(previous, current string, diffLimit int) string {
	prev := strings.Split(previous, "\n")
	curr := strings.Split(current, "\n")
	m, n := len(prev), len(curr)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if prev[i-1] == curr[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	i, j := m, n
	var ops []diffOp
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && prev[i-1] == curr[j-1]:
			ops = append(ops, diffOp{" ", prev[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, diffOp{"+", curr[j-1]})
			j--
		default:
			ops = append(ops, diffOp{"-", prev[i-1]})
			i--
		}
	}
	for a, b := 0, len(ops)-1; a < b; a, b = a+1, b-1 {
		ops[a], ops[b] = ops[b], ops[a]
	}
	var changed []int
	for k, o := range ops {
		if o.op != " " {
			changed = append(changed, k)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	const context = 3
	var ranges [][2]int
	for _, idx := range changed {
		start, end := max(0, idx-context), min(len(ops)-1, idx+context)
		if len(ranges) > 0 && start <= ranges[len(ranges)-1][1]+1 {
			ranges[len(ranges)-1][1] = end
		} else {
			ranges = append(ranges, [2]int{start, end})
		}
	}
	var hunks []string
	for _, r := range ranges {
		prevPos := 0
		for k := 0; k < r[0]; k++ {
			if ops[k].op == " " || ops[k].op == "-" {
				prevPos++
			}
		}
		prevCount, currCount := 0, 0
		var lines []string
		for k := r[0]; k <= r[1]; k++ {
			lines = append(lines, ops[k].op+" "+ops[k].line)
			switch ops[k].op {
			case " ":
				prevCount++
				currCount++
			case "-":
				prevCount++
			default:
				currCount++
			}
		}
		hunks = append(hunks, "@@ -"+strconv.Itoa(prevPos+1)+","+strconv.Itoa(prevCount)+" +"+strconv.Itoa(prevPos+1)+","+strconv.Itoa(currCount)+" @@")
		hunks = append(hunks, lines...)
	}
	text := strings.Join(hunks, "\n")
	if utf16Len(text) <= diffLimit {
		return text
	}
	return utf16Slice(text, 0, diffLimit) + "\n… [diff truncated]"
}

// StuckDiff is the short note for a stuck-loop repeated failure: a header, a capped unified diff against the
// previous output, the tail of the current output and the path of the full copy. All inputs must be pre-redacted.
func StuckDiff(previous, current string, opts DiffOptions) string {
	header := "pi-warden: stuck-loop diff; this output repeated a failure (previous: " + strconv.Itoa(utf16Len(previous)) + " chars, current: " + strconv.Itoa(utf16Len(current)) + " chars)."
	if previous == current {
		return header + "\nOutputs are byte-identical; see the full output at " + opts.FullPath + "."
	}
	diff := lineDiff(previous, current, opts.DiffLimit)
	tail := current
	if n := utf16Len(current); n > opts.TailLimit {
		tail = "… [" + strconv.Itoa(n-opts.TailLimit) + " earlier chars omitted]\n" + utf16Slice(current, n-opts.TailLimit, -1)
	}
	return header + "\n" + diff + "\n" + tail + "\n\nFull output: " + opts.FullPath
}
