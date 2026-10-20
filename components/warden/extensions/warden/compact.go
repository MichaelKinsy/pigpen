package warden

import "strings"

// Compaction evidence appendix (src/compact.ts). Deterministic, pure, no judge request. After the host compacts the
// session, the extension builds a snapshot from session memory and passes it to compactAppendix. The returned text
// is sent as one custom message so the agent does not retry approaches that already failed, knows whether its last
// passing check still covers the code, and can prefer saved paths over re-running commands.

const (
	compactMaxChars      = 2000
	compactMaxFailed     = 5
	failedEntryChars     = 160
	failedCallChars      = 70
	savedKeepWhenOver    = 3
	taskCharsWhenOver    = 300
	compactMaxChecks     = 5
	compactMaxHolds      = 10
	compactEvidenceType  = "pi-warden-compact-evidence"
	compactTruncatedMark = "\n… [truncated]"
)

// SavedOutput is one stored full output.
type SavedOutput struct {
	// Tool is "" when the recorder did not say; the appendix then prints only what it knows.
	Tool string
	Path string
	// Bytes is the uncompressed size, when HasBytes.
	Bytes    int
	HasBytes bool
}

// CheckEntry is one check of the run, as the appendix lists it.
type CheckEntry struct {
	Command string
	Passed  bool
	// When is a short label: "current run", "run 2", or "current run, #3".
	When string
}

// HeldEntry is one held action.
type HeldEntry struct {
	Tool string
	// Preview is a redacted preview of the call (command, path, or input); at most 120 characters.
	Preview string
	Outcome string
}

// FailedAttempt is one failed call with its error line.
type FailedAttempt struct {
	// Call is the redacted call view.
	Call string
	// Error is the last meaningful line of the failed output; empty when the output had none.
	Error string
}

// Verification says whether the code as it stands was covered by a passing check.
type Verification struct {
	// Passed is false for "no check has passed yet; code was changed".
	Passed       bool
	Command      string
	ChangedSince bool
}

// StuckState is the stuck guard's failure count.
type StuckState struct {
	Failures int
	// Window is the number of tool results in the attempt window the failures were counted in.
	Window int
}

// CompactSnapshot is what the appendix and warden_recall print.
type CompactSnapshot struct {
	SavedOutputs []SavedOutput
	// Checks are capped at 5 before this is called.
	Checks []CheckEntry
	// Holds are capped at 10 before this is called.
	Holds []HeldEntry
	// FailedAttempts are distinct failed calls, oldest first, at most 5.
	FailedAttempts []FailedAttempt
	Verification   *Verification
	Stuck          *StuckState
	ActiveTask     string
	// OpenLoops is the session's open loops, already formatted and capped by formatOpenLoops; empty when none is open.
	OpenLoops string
}

// outputLedger is the stored-output ledger as the snapshot reads it. The context saver implements it.
type outputLedger interface {
	storedOutputs() []SavedOutput
}

// holdRecords is the hold records as the snapshot reads them: the held calls, each with its outcome. The hold
// feedback store implements it.
type holdRecords interface {
	heldRecords() []HeldEntry
}

func compactClip(text string, limit int) string {
	if utf16Len(text) <= limit {
		return text
	}
	keep := limit - 1
	if keep < 0 {
		keep = 0
	}
	return utf16Slice(text, 0, keep) + "…"
}

type sectionName string

type section struct {
	name sectionName
	text string
}

// renderSections is every section in appendix order; warden_recall prints some of the same strings, so the two never disagree.
func renderSections(s CompactSnapshot) []section {
	var sections []section
	if len(s.FailedAttempts) > 0 {
		var items []string
		for _, attempt := range s.FailedAttempts {
			call := compactClip(redactFast(attempt.Call), failedCallChars)
			errText := redactFast(attempt.Error)
			entry := call
			if errText != "" {
				entry = call + " → " + compactClip(errText, failedEntryChars-utf16Len(call)-3)
			}
			items = append(items, "- "+entry)
		}
		sections = append(sections, section{"failed", "### Tried and failed\nDo not retry these unchanged.\n" + strings.Join(items, "\n")})
	}
	if v := s.Verification; v != nil {
		line := "- no check has passed yet; code was changed"
		if v.Passed {
			changed := "no"
			if v.ChangedSince {
				changed = "yes"
			}
			line = "- last passing check: " + redactFast(v.Command) + "; code changed since last passing check: " + changed
		}
		sections = append(sections, section{"verification", "### Verification\n" + line})
	}
	if s.OpenLoops != "" {
		sections = append(sections, section{"loops", "### Open loops\nPromised earlier in this session and not done yet; close each with warden_loops done or drop.\n" + s.OpenLoops})
	}
	if len(s.Checks) > 0 {
		var items []string
		for _, c := range s.Checks {
			mark := "✗"
			if c.Passed {
				mark = "✓"
			}
			items = append(items, "- "+mark+" "+c.When+": "+redactFast(c.Command))
		}
		sections = append(sections, section{"checks", "### Last checks\n" + strings.Join(items, "\n")})
	}
	if s.Stuck != nil {
		sections = append(sections, section{"stuck", "### Stuck state\n- failures: " + itoaInt(s.Stuck.Failures) + " of the last " + itoaInt(s.Stuck.Window) + " tool results"})
	}
	if len(s.Holds) > 0 {
		var items []string
		for _, h := range s.Holds {
			item := "- " + redactFast(h.Tool) + ": " + redactFast(h.Outcome)
			if h.Preview != "" {
				item += " (" + redactFast(h.Preview) + ")"
			}
			items = append(items, item)
		}
		sections = append(sections, section{"holds", "### Held actions\n" + strings.Join(items, "\n")})
	}
	if len(s.SavedOutputs) > 0 {
		var items []string
		for _, o := range s.SavedOutputs {
			item := "- "
			if o.Tool != "" {
				item += redactFast(o.Tool) + " → "
			}
			item += redactFast(o.Path)
			if o.HasBytes {
				item += " (" + itoaInt(o.Bytes) + " bytes)"
			}
			items = append(items, item)
		}
		sections = append(sections, section{"saved", "### Saved full outputs\n" + strings.Join(items, "\n")})
	}
	if s.ActiveTask != "" {
		sections = append(sections, section{"task", "### Active task\n" + redactFast(s.ActiveTask)})
	}
	return sections
}

func renderCompact(s CompactSnapshot) string {
	sections := renderSections(s)
	if len(sections) == 0 {
		return ""
	}
	texts := make([]string, len(sections))
	for i, sec := range sections {
		texts[i] = sec.text
	}
	return strings.Join([]string{
		"=== PI-WARDEN COMPACT EVIDENCE ===",
		"Evidence warden kept across compaction. Prefer the saved-output paths below over re-running commands that produced them.",
		"",
		strings.Join(texts, "\n\n"),
		"=== END PI-WARDEN COMPACT EVIDENCE ===",
	}, "\n")
}

func lastN[T any](items []T, n int) []T {
	if len(items) > n {
		return items[len(items)-n:]
	}
	return items
}

// compactReducers drop lower-value content first when the appendix is over the cap. Failed attempts, the
// verification line, and the open loops (at most 600 characters) are never reduced here; they come first in the
// text, so the final cut reaches them last.
var compactReducers = []func(CompactSnapshot) CompactSnapshot{
	func(s CompactSnapshot) CompactSnapshot {
		s.SavedOutputs = lastN(s.SavedOutputs, savedKeepWhenOver)
		return s
	},
	func(s CompactSnapshot) CompactSnapshot { s.Holds = nil; return s },
	func(s CompactSnapshot) CompactSnapshot {
		if s.ActiveTask != "" {
			s.ActiveTask = compactClip(s.ActiveTask, taskCharsWhenOver)
		}
		return s
	},
	func(s CompactSnapshot) CompactSnapshot { s.Stuck = nil; return s },
	func(s CompactSnapshot) CompactSnapshot { s.Checks = lastN(s.Checks, 2); return s },
	func(s CompactSnapshot) CompactSnapshot { s.SavedOutputs = nil; return s },
	func(s CompactSnapshot) CompactSnapshot { s.ActiveTask = ""; return s },
	func(s CompactSnapshot) CompactSnapshot { s.Checks = nil; return s },
}

// compactAppendix returns "" for an empty snapshot. Capped at 2 000 characters.
func compactAppendix(s CompactSnapshot) string {
	appendix := renderCompact(s)
	for _, reduce := range compactReducers {
		if utf16Len(appendix) <= compactMaxChars {
			return appendix
		}
		s = reduce(s)
		appendix = renderCompact(s)
	}
	if utf16Len(appendix) <= compactMaxChars {
		return appendix
	}
	return utf16Slice(appendix, 0, compactMaxChars-utf16Len(compactTruncatedMark)) + compactTruncatedMark
}

// recallText is warden_recall: what was already tried in this session, from the same snapshot and the same section
// text as the compaction appendix: the failed attempts with their error lines, the last passing check with whether
// the code changed since, and the saved-output paths. Capped like the appendix.
func recallText(s CompactSnapshot) string {
	var kept []string
	for _, sec := range renderSections(s) {
		if sec.name == "failed" || sec.name == "verification" || sec.name == "saved" {
			kept = append(kept, sec.text)
		}
	}
	text := "Nothing recorded yet in this session: no failed attempt, no check, and no saved output."
	if len(kept) > 0 {
		text = strings.Join(kept, "\n\n")
	}
	if utf16Len(text) <= compactMaxChars {
		return text
	}
	return utf16Slice(text, 0, compactMaxChars-utf16Len(compactTruncatedMark)) + compactTruncatedMark
}

var (
	reErrorLine = prefsRE("i", `\b(?:error|errors|fail(?:ed|ure|s)?|exception|cannot|can't|could not|not found|no such|denied|refused|invalid|unexpected|missing|undefined|timed? ?out|abort(?:ed)?|panic|fatal|traceback)\b|✗|✖|✘`)
	// SUMMARY_LINE: runner tallies ("Found 1 error.", "3 failing") say that something failed, not what; an earlier line says what.
	reSummaryLine   = prefsRE("i", `^(?:found \d+ errors?\b|\d+ (?:failing|failed|errors?)\b|tests?:\s|ℹ fail \d+|npm (?:err!|error) (?:code|errno|a complete log|command failed|lifecycle)|error: command failed|command failed with exit code)`)
	reEarlierChars  = prefsRE("", `^\[\d+ earlier chars\] …`)
	reDecorationRow = prefsRE("", "^[-=─—*_~.`]+$")
)

// compactErrorLine is the line of a failed output most likely to name the error: the last specific error line, else the last line.
func compactErrorLine(output string) string {
	var lines []string
	for _, raw := range strings.Split(reEarlierChars.ReplaceAllString(output, ""), "\n") {
		line := jsTrim(raw)
		if line != "" && !reDecorationRow.MatchString(line) {
			lines = append(lines, line)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if reErrorLine.MatchString(lines[i]) && !reSummaryLine.MatchString(lines[i]) {
			return lines[i]
		}
	}
	if len(lines) > 0 {
		return lines[len(lines)-1]
	}
	return ""
}

// compactAttempt is the part of a stuck-window attempt the snapshot reads.
type compactAttempt struct {
	Key, Call string
	Failed    bool
	Output    string
}

// distinctFailures are the distinct failed calls, the latest occurrence of each, oldest first; at most compactMaxFailed.
func distinctFailures(attempts []compactAttempt) []FailedAttempt {
	var order []string
	latest := map[string]FailedAttempt{}
	for _, a := range attempts {
		if !a.Failed {
			continue
		}
		if _, ok := latest[a.Key]; ok {
			for i, key := range order {
				if key == a.Key {
					order = append(order[:i], order[i+1:]...)
					break
				}
			}
		}
		order = append(order, a.Key)
		latest[a.Key] = FailedAttempt{Call: a.Call, Error: compactErrorLine(a.Output)}
	}
	out := make([]FailedAttempt, 0, len(order))
	for _, key := range order {
		out = append(out, latest[key])
	}
	return lastN(out, compactMaxFailed)
}

// verificationOf is nil when no check passed and no code changed: there is nothing to say.
func verificationOf(e *RunEvidence) *Verification {
	lastPass := len(e.Checks) - 1
	for lastPass >= 0 && !e.Checks[lastPass].Passed {
		lastPass--
	}
	if lastPass >= 0 {
		before := -1
		if e.ChecksBeforeMutation != nil {
			before = *e.ChecksBeforeMutation
		}
		return &Verification{Passed: true, Command: e.Checks[lastPass].Call, ChangedSince: before > lastPass}
	}
	if e.Mutations > 0 {
		return &Verification{}
	}
	return nil
}

// compactCheck is one check as the hook hands it over.
type compactCheck struct {
	Command    string
	Passed     bool
	RunIndex   int
	IndexInRun int
}

// snapshotInput is buildCompactSnapshot's options.
type snapshotInput struct {
	SavedOutputs []SavedOutput
	Checks       []compactCheck
	Holds        []HeldEntry
	// Attempts is the stuck guard's attempt window, oldest first.
	Attempts   []compactAttempt
	Evidence   *RunEvidence
	ActiveTask string
	Runs       int
	OpenLoops  string
}

// buildCompactSnapshot builds a CompactSnapshot from the extension's session-scoped state. All string values are
// redacted when printed; saved-output paths are local and allowed.
func buildCompactSnapshot(in snapshotInput) CompactSnapshot {
	snap := CompactSnapshot{SavedOutputs: append([]SavedOutput(nil), in.SavedOutputs...)}
	for _, c := range lastN(in.Checks, compactMaxChecks) {
		when := "current run"
		if in.Runs > 1 {
			when = "run " + itoaInt(c.RunIndex)
			if c.IndexInRun > 0 {
				when += ", #" + itoaInt(c.IndexInRun+1)
			}
		}
		snap.Checks = append(snap.Checks, CheckEntry{Command: c.Command, Passed: c.Passed, When: when})
	}
	snap.Holds = append([]HeldEntry(nil), lastN(in.Holds, compactMaxHolds)...)
	failures := 0
	for _, a := range in.Attempts {
		if a.Failed {
			failures++
		}
	}
	if failures > 0 {
		snap.Stuck = &StuckState{Failures: failures, Window: len(in.Attempts)}
	}
	if in.ActiveTask != "" {
		snap.ActiveTask = redactFast(in.ActiveTask)
	}
	snap.FailedAttempts = distinctFailures(in.Attempts)
	if in.Evidence != nil {
		snap.Verification = verificationOf(in.Evidence)
	}
	snap.OpenLoops = in.OpenLoops
	return snap
}
