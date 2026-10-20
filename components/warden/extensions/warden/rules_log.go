package warden

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Local record of the rules guard's verdicts (src/rules-log.ts). After each verdict from the judge, one JSON line per
// scored rule goes to one file per project under warden's data folder, so a report can say how often each rule was
// judged, how often it fired, and how often a fired rule was fixed on a later edit. The record holds the judgment only:
// the rule name, the score, and the outcome, never the written content and never the rule text. Nothing here is sent
// anywhere.
//
// One file per project, keyed by a hash of the working directory, so the project path itself is not stored in clear.
// The file keeps the newest rulesLogMaxRecords records; past that the oldest are dropped (trim, not rotate). A write
// failure is silent to the agent and is reported once through the onFailure callback (fail open).

// rulesLogMaxRecords is the records kept per project file; the oldest are trimmed first.
const rulesLogMaxRecords = 5000

// RuleRecord is one scored rule of one judgment. The JSON names are the upstream ones, so a log written by either
// implementation reads in the other.
type RuleRecord struct {
	// At is the ISO timestamp of the judgment.
	At string `json:"at"`
	// Session is the session id as a file-name-safe value.
	Session string `json:"session"`
	// Path is the project-relative path the judgment was about.
	Path string `json:"path"`
	// Tool is the tool that triggered the judgment.
	Tool string `json:"tool"`
	ID   string `json:"id"`
	// Name is the rule's heading, never its body.
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	// Violation is P(violation) from the judge.
	Violation float64 `json:"violation"`
	// Threshold is the cutoff in force at the time (the rule's own when it set one).
	Threshold float64 `json:"threshold"`
	// Finding: violation at or above the cutoff.
	Finding bool `json:"finding"`
	// Soft is true when the score reached rules.softThreshold without reaching the cutoff.
	Soft bool `json:"soft,omitempty"`
	// Cleared is true when this judgment clears an earlier finding of the same rule on the same path in the same session.
	Cleared bool `json:"cleared,omitempty"`
	// Source is "calibrate" on scores from a history replay; live verdicts leave it unset.
	Source string `json:"source,omitempty"`
}

// RuleObservation is one judgment within a verdict, after clear detection.
type RuleObservation struct {
	ID, Name, Outcome string
	Violation         float64
	Finding, Soft     bool
	Cleared           bool
}

var errNotARuleRecord = errors.New("not a rules record")

// UnmarshalJSON accepts only the records upstream's reader accepts: every required field present with its type.
func (r *RuleRecord) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var out RuleRecord
	for key, dst := range map[string]*string{"at": &out.At, "session": &out.Session, "path": &out.Path, "tool": &out.Tool, "id": &out.ID, "name": &out.Name, "outcome": &out.Outcome} {
		v, ok := raw[key]
		if !ok || json.Unmarshal(v, dst) != nil {
			return errNotARuleRecord
		}
	}
	for key, dst := range map[string]*float64{"violation": &out.Violation, "threshold": &out.Threshold} {
		v, ok := raw[key]
		if !ok || json.Unmarshal(v, dst) != nil {
			return errNotARuleRecord
		}
	}
	if v, ok := raw["finding"]; !ok || json.Unmarshal(v, &out.Finding) != nil {
		return errNotARuleRecord
	}
	if v, ok := raw["soft"]; ok {
		if json.Unmarshal(v, &out.Soft) != nil || !out.Soft {
			return errNotARuleRecord
		}
	}
	if v, ok := raw["cleared"]; ok {
		_ = json.Unmarshal(v, &out.Cleared)
	}
	if v, ok := raw["source"]; ok {
		if json.Unmarshal(v, &out.Source) != nil || out.Source != "calibrate" {
			return errNotARuleRecord
		}
	}
	*r = out
	return nil
}

// rulesLogPath is the log file for a project: the working directory's hash keeps two projects apart without naming
// either. dataDir is warden's data folder (DataDir of the PiG config home).
func rulesLogPath(cwd, dataDir string) string {
	sum := sha256.Sum256([]byte(cwd))
	return filepath.Join(dataDir, "rules", hex.EncodeToString(sum[:])[:12]+".jsonl")
}

// trimRecords keeps the newest max lines, dropping the oldest.
func trimRecords(lines []string, max int) []string {
	var kept []string
	for _, line := range lines {
		if jsTrim(line) != "" {
			kept = append(kept, line)
		}
	}
	if len(kept) <= max {
		return kept
	}
	return kept[len(kept)-max:]
}

// RuleClearTracker is the per-session memory of the last judgment per rule and path, so a score below the threshold
// right after a finding is a clear. A second finding resets the entry, so flag then flag is not a clear, and a
// different path has its own entry.
type RuleClearTracker struct {
	mu          sync.Mutex
	lastFinding map[string]bool
}

// NewRuleClearTracker returns an empty tracker.
func NewRuleClearTracker() *RuleClearTracker {
	return &RuleClearTracker{lastFinding: map[string]bool{}}
}

// Observe records the scores of one verdict and returns them as observations.
func (t *RuleClearTracker) Observe(path string, scores []RuleScore, threshold, softThreshold float64) []RuleObservation {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]RuleObservation, 0, len(scores))
	for _, score := range scores {
		key := path + "\x00" + score.ID
		was := t.lastFinding[key]
		cutoff := threshold
		if score.Threshold != nil {
			cutoff = *score.Threshold
		}
		finding := score.Violation >= cutoff
		soft := !finding && softThreshold > 0 && score.Violation >= softThreshold
		cleared := was && !finding
		t.lastFinding[key] = finding
		out = append(out, RuleObservation{ID: score.ID, Name: score.Name, Outcome: score.Outcome, Violation: score.Violation, Finding: finding, Soft: soft, Cleared: cleared})
	}
	return out
}

// Reset forgets every finding.
func (t *RuleClearTracker) Reset() {
	t.mu.Lock()
	t.lastFinding = map[string]bool{}
	t.mu.Unlock()
}

func lineCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	count := 0
	reader := bufio.NewReaderSize(f, 1<<16)
	for {
		line, err := reader.ReadString('\n')
		if jsTrim(line) != "" {
			count++
		}
		if err != nil {
			break
		}
	}
	return count, nil
}

// RulesLogOptions tune a RulesLog (tests).
type RulesLogOptions struct {
	// Path overrides the file path.
	Path string
	// MaxRecords is the records kept before trimming.
	MaxRecords int
	// OnFailure is called once with a message when a write fails; the failure never reaches the agent.
	OnFailure func(message string)
}

// RulesLog appends rules verdicts to this project's log, one write at a time, and trims the file when it grows past the
// cap.
type RulesLog struct {
	Path       string
	session    string
	tracker    *RuleClearTracker
	maxRecords int
	onFailure  func(string)

	mu       sync.Mutex
	pending  [][]string
	running  bool
	failure  string
	reported bool
	// lines in the file, read once on the first append and tracked after that.
	lines int
	idle  sync.WaitGroup
}

// NewRulesLog opens the log of a project; dataDir is warden's data folder.
func NewRulesLog(cwd, dataDir, session string, options RulesLogOptions) *RulesLog {
	path := options.Path
	if path == "" {
		path = rulesLogPath(cwd, dataDir)
	}
	max := options.MaxRecords
	if max <= 0 {
		max = rulesLogMaxRecords
	}
	return &RulesLog{Path: path, session: session, tracker: NewRuleClearTracker(), maxRecords: max, onFailure: options.OnFailure, lines: -1}
}

// Record records one verdict and returns its judgments, so the caller can trace the ones that cleared an earlier
// finding. It batches the whole verdict into one write and never fails.
func (l *RulesLog) Record(verdict RulesVerdict, threshold float64, at time.Time, softThreshold float64) []RuleObservation {
	if len(verdict.Scores) == 0 {
		return nil
	}
	observations := l.tracker.Observe(verdict.Path, verdict.Scores, threshold, softThreshold)
	lines := make([]string, 0, len(observations))
	for i, observation := range observations {
		cutoff := threshold
		if t := verdict.Scores[i].Threshold; t != nil {
			cutoff = *t
		}
		record := RuleRecord{
			At: isoTime(at), Session: l.session, Path: verdict.Path, Tool: verdict.Tool, ID: observation.ID, Name: observation.Name,
			Outcome: observation.Outcome, Violation: observation.Violation, Threshold: cutoff, Finding: observation.Finding,
			Soft: observation.Soft, Cleared: observation.Cleared,
		}
		lines = append(lines, marshalRecord(record))
	}
	l.append(lines)
	return observations
}

// AppendRecords appends records built elsewhere (a history replay carries its own source tag); it never fails.
func (l *RulesLog) AppendRecords(records []RuleRecord) {
	lines := make([]string, len(records))
	for i, r := range records {
		lines[i] = marshalRecord(r)
	}
	l.append(lines)
}

// isoTime is Date.prototype.toISOString.
func isoTime(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z") }

func marshalRecord(r RuleRecord) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(r)
	return strings.TrimSuffix(b.String(), "\n")
}

// LastFailure is the last write error, if the most recent append failed.
func (l *RulesLog) LastFailure() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failure
}

// Flush returns when the writes queued so far are on disk or the log has stopped.
func (l *RulesLog) Flush() { l.idle.Wait() }

func (l *RulesLog) append(lines []string) {
	if len(lines) == 0 {
		return
	}
	l.mu.Lock()
	l.pending = append(l.pending, lines)
	if !l.running {
		l.running = true
		l.idle.Add(1)
		go l.drain()
	}
	l.mu.Unlock()
}

// drain writes the queued batches in order, one at a time.
func (l *RulesLog) drain() {
	defer l.idle.Done()
	for {
		l.mu.Lock()
		if len(l.pending) == 0 {
			l.running = false
			l.mu.Unlock()
			return
		}
		batch := l.pending[0]
		l.pending = l.pending[1:]
		l.mu.Unlock()
		if err := l.write(batch); err != nil {
			l.fail(err)
		}
	}
}

func (l *RulesLog) write(lines []string) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	if l.lines < 0 {
		n, err := lineCount(l.Path)
		if err != nil {
			return err
		}
		l.lines = n
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(strings.Join(lines, "\n") + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	l.lines += len(lines)
	if l.lines > l.maxRecords {
		return l.trim()
	}
	return nil
}

func (l *RulesLog) trim() error {
	data, err := os.ReadFile(l.Path)
	if err != nil {
		return err
	}
	kept := trimRecords(strings.Split(decodeUTF8(data), "\n"), l.maxRecords)
	if err := WriteFileAtomic(l.Path, []byte(strings.Join(kept, "\n")+"\n")); err != nil {
		return err
	}
	l.lines = len(kept)
	return nil
}

func (l *RulesLog) fail(err error) {
	l.mu.Lock()
	l.failure = err.Error()
	already := l.reported
	l.reported = true
	l.mu.Unlock()
	if already || l.onFailure == nil {
		return
	}
	defer func() { _ = recover() }()
	l.onFailure("warden: rules log " + l.Path + " could not be written (" + err.Error() + "); rules verdicts are not recorded this session.")
}

// readRulesLog reads this project's records; a missing file is an empty log and a torn line is skipped.
func readRulesLog(path string) ([]RuleRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var records []RuleRecord
	for _, line := range strings.Split(decodeUTF8(data), "\n") {
		if jsTrim(line) == "" {
			continue
		}
		var record RuleRecord
		// A torn final line from an interrupted write is skipped.
		if json.Unmarshal([]byte(line), &record) == nil {
			records = append(records, record)
		}
	}
	return records, nil
}
