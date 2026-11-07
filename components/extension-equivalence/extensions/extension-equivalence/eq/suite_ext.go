package eq

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Features for suites that seed large sessions and prompts (a real-Pi test file that builds 1.2 MB inputs):
//
//   - {{repeat:C:N}} in a scenario file (load time, any string): the character C written N times, so the
//     scenario file stays small. C is one letter, digit, space, '_', '.', '-' or '#'.
//   - "collapseRuns": MIN: a run of at least MIN identical characters in a trace string is recorded as
//     <run C xN> (character and exact length kept), so a golden trace does not hold the megabytes a scenario
//     fed through the host. Lossless: no extension text changes, only its encoding.
//   - {{cwd}}, {{agent}} and {{sessions}} in session.files, workspace files and setup arguments (a seeded session
//     header names the lane's workspace).

// minCollapseRun is the smallest accepted collapseRuns value; below it ordinary text would be collapsed.
const minCollapseRun = 64

var repeatMacroRE = regexp.MustCompile(`\{\{repeat:([A-Za-z0-9 _.#-]):([0-9]{1,8})\}\}`)

// expandRepeats replaces every {{repeat:C:N}} of a scenario's raw JSON.
func expandRepeats(data []byte) ([]byte, error) {
	var bad error
	out := repeatMacroRE.ReplaceAllFunc(data, func(m []byte) []byte {
		sub := repeatMacroRE.FindSubmatch(m)
		n, err := strconv.Atoi(string(sub[2]))
		if err != nil || n > 16_000_000 {
			bad = fmt.Errorf("%s: repeat count must be at most 16000000", m)
			return m
		}
		return []byte(strings.Repeat(string(sub[1]), n))
	})
	return out, bad
}

// collapseRuns replaces each run of at least min identical runes by <run C xN>.
func collapseRuns(s string, min int) string {
	if len(s) < min {
		return s
	}
	var b strings.Builder
	last := 0
	rs := []rune(s)
	if len(rs) < min {
		return s
	}
	pos := 0 // byte offset of rs[i]
	for i := 0; i < len(rs); {
		j := i + 1
		for j < len(rs) && rs[j] == rs[i] {
			j++
		}
		size := len(string(rs[i])) // bytes of one rune of the run
		if j-i >= min {
			b.WriteString(s[last:pos])
			fmt.Fprintf(&b, "<run %c x%d>", rs[i], j-i)
			last = pos + size*(j-i)
		}
		pos += size * (j - i)
		i = j
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// expandBuiltins replaces {{cwd}}, {{agent}} and {{sessions}} in s.
func expandBuiltins(s, cwd, agent, sessions string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return strings.NewReplacer("{{cwd}}", cwd, "{{agent}}", agent, "{{sessions}}", sessions).Replace(s)
}

// validateSuiteExt checks collapseRuns.
func (s *Scenario) validateSuiteExt() error {
	if s.CollapseRuns != 0 && s.CollapseRuns < minCollapseRun {
		return fmt.Errorf("collapseRuns %d is too small: use 0 (off) or at least %d", s.CollapseRuns, minCollapseRun)
	}
	return nil
}
