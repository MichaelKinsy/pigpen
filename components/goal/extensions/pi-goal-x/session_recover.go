package pi_goal_x

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ported from scripts/recover-session-checkpoints.mjs (pi-goal-x-recover): offline repair of session files affected by
// issue #30. It rewrites only legacy checkpoint entries to the bounded v2 marker; the line count, ids, the header and every
// other line stay byte-identical. Dry-run is the default; --apply needs --confirm-pi-closed.

var (
	recoverXMLGoalID  = regexp.MustCompile(`^<pi_goal_continuation` + jsS + `+goal_id="([^"]+)"`)
	recoverBracketRE  = regexp.MustCompile(`^\[(?:GOAL CHECKPOINT|GOAL CONTINUATION|GOAL STALE) goalId=([^\]\s]+)\]`)
	recoverXMLEscaper = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
)

func recoverCheckpointTriggerPrompt(goalID string) string {
	content := `<pi_goal_continuation goal_id="` + recoverXMLEscaper.Replace(goalID) + `" kind="checkpoint" v="2"/>`
	if utf16Len(content) > 160 {
		panic(fmt.Sprintf("checkpoint marker too large: %d", utf16Len(content)))
	}
	return content
}

// recoverCheckpointGoalID extracts the goalId of a checkpoint entry from its details or content (v2 and legacy forms).
func recoverCheckpointGoalID(entry any) (string, bool) {
	raw := sessionRecord(entry)
	if raw == nil {
		return "", false
	}
	if d := sessionRecord(raw.vals["details"]); d != nil {
		if id, ok := d.vals["goalId"].(string); ok && id != "" {
			return id, true
		}
	}
	content, ok := raw.vals["content"].(string)
	if !ok {
		return "", false
	}
	if m := recoverXMLGoalID.FindStringSubmatch(content); m != nil && m[1] != "" {
		return m[1], true
	}
	// `[^\]\s]` with JavaScript whitespace.
	if m := recoverBracketRE.FindStringSubmatch(content); m != nil && !strings.ContainsFunc(m[1], isJSSpace) {
		return m[1], true
	}
	return "", false
}

func isGoalCheckpointCustomMessage(entry any) bool {
	raw := sessionRecord(entry)
	if raw == nil || raw.vals["type"] != any("custom_message") || raw.vals["customType"] != any(goalEventEntry) {
		return false
	}
	content, ok := raw.vals["content"].(string)
	return ok && !v2MarkerPattern.MatchString(content)
}

func recoverGraphIdentity(entry any) *jsObject {
	g := newObject()
	raw := sessionRecord(entry)
	for _, k := range []string{"id", "parentId"} {
		var v any = undef{}
		if raw != nil {
			if x, ok := raw.vals[k]; ok {
				v = x
			}
		}
		g.set(k, v)
	}
	return g
}

type sessionTransform struct {
	outputLines   []string
	originalGraph []any
	outputGraph   []any
	changed       int
	savedChars    int
	malformed     int
}

// transformSessionLines is the pure line-level transform; it never touches the filesystem.
func transformSessionLines(originalText string) sessionTransform {
	var t sessionTransform
	t.outputLines = []string{}
	for _, rawLine := range strings.Split(originalText, "\n") {
		if jsTrim(rawLine) == "" {
			t.outputLines = append(t.outputLines, rawLine)
			continue
		}
		entry, err := parseJSON([]byte(rawLine))
		if err != nil {
			t.malformed++
			t.outputLines = append(t.outputLines, rawLine) // never discard unrelated malformed data
			continue
		}
		t.originalGraph = append(t.originalGraph, recoverGraphIdentity(entry))
		if !isGoalCheckpointCustomMessage(entry) {
			t.outputLines = append(t.outputLines, rawLine)
			t.outputGraph = append(t.outputGraph, recoverGraphIdentity(entry))
			continue
		}
		goalID, ok := recoverCheckpointGoalID(entry)
		if !ok {
			t.outputLines = append(t.outputLines, rawLine)
			t.outputGraph = append(t.outputGraph, recoverGraphIdentity(entry))
			continue
		}
		rewritten := entry.(*jsObject).clone()
		details := newObject()
		if old := sessionRecord(rewritten.vals["details"]); old != nil {
			for _, k := range old.keys {
				if k != "objective" {
					details.set(k, old.vals[k])
				}
			}
		}
		details.set("version", 2.0)
		details.set("kind", "checkpoint")
		details.set("goalId", goalID)
		if _, isNum := details.vals["timestamp"].(float64); !isNum {
			details.set("timestamp", float64(clockMs()))
		}
		rewritten.set("details", details)
		rewritten.set("content", recoverCheckpointTriggerPrompt(goalID))
		line := marshalJSON(rewritten, "")
		t.outputLines = append(t.outputLines, line)
		t.outputGraph = append(t.outputGraph, recoverGraphIdentity(rewritten))
		t.changed++
		t.savedChars += utf16Len(rawLine) - utf16Len(line)
	}
	return t
}

func recoverValueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return jsNumber(x)
	}
	return marshalJSON(v, "")
}

func recoverSession(inputPath string, apply bool, cwd string, stdout io.Writer) error {
	absolute := inputPath
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(cwd, absolute)
	}
	absolute = filepath.Clean(absolute)
	lstat, err := os.Lstat(absolute)
	if err != nil {
		return fmt.Errorf("cannot stat %s: %v", absolute, err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symbolic link: %s", absolute)
	}
	if !lstat.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", absolute)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return err
	}
	original := string(data)
	tr := transformSessionLines(original)

	// invariants before any write
	if marshalJSON(tr.outputGraph, "") != marshalJSON(tr.originalGraph, "") {
		return errors.New("graph identity mismatch: ids/parentIds would change \u2014 aborting")
	}
	originalLines := strings.Split(original, "\n")
	if len(originalLines) != len(tr.outputLines) {
		return errors.New("line count would change \u2014 aborting")
	}
	for i := range originalLines {
		if originalLines[i] == tr.outputLines[i] {
			continue
		}
		parsed, perr := parseJSON([]byte(originalLines[i]))
		if perr != nil {
			return perr
		}
		rec := sessionRecord(parsed)
		if rec == nil || rec.vals["type"] != any("custom_message") || rec.vals["customType"] != any(goalEventEntry) {
			return fmt.Errorf("non-goal line %d would change \u2014 aborting", i+1)
		}
	}
	ids := map[string]bool{}
	for _, g := range tr.outputGraph {
		if id := g.(*jsObject).vals["id"]; truthy(id) {
			ids[marshalJSON(id, "")] = true
		}
	}
	for _, g := range tr.outputGraph {
		p := g.(*jsObject).vals["parentId"]
		if _, u := p.(undef); p != nil && !u && !ids[marshalJSON(p, "")] {
			return fmt.Errorf("parentId %s has no matching entry \u2014 aborting", recoverValueString(p))
		}
	}

	fmt.Fprintf(stdout, "Session checkpoints: %d legacy checkpoint(s) rewriteable\n", tr.changed)
	fmt.Fprintf(stdout, "Estimated savings: %d chars (%d malformed unrelated line(s) preserved)\n", tr.savedChars, tr.malformed)
	if !apply {
		fmt.Fprintln(stdout, "dry-run: no changes written.")
		return nil
	}

	timestamp := strings.NewReplacer(":", "-", ".", "-").Replace(nowIso())
	backup := absolute + ".backup-" + timestamp
	if err := copyFileBytes(absolute, backup); err != nil {
		return err
	}
	mode := lstat.Mode().Perm()
	if err := os.Chmod(backup, mode); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(absolute), fmt.Sprintf(".%s.%d.%d.tmp", filepath.Base(absolute), os.Getpid(), clockMs()))
	out := strings.Join(tr.outputLines, "\n")
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	werr := error(nil)
	if _, werr = f.WriteString(out); werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(temp, absolute)
	}
	if werr != nil {
		_ = os.Remove(temp)
		return werr
	}
	if d, err := os.Open(filepath.Dir(absolute)); err == nil { // directory fsync is best effort
		_ = d.Sync()
		_ = d.Close()
	}
	reread, err := os.ReadFile(absolute)
	if err != nil {
		return err
	}
	rereadLines := strings.Split(string(reread), "\n")
	if len(rereadLines) != len(tr.outputLines) {
		return errors.New("post-write validation failed: line count changed")
	}
	for i := range rereadLines {
		if rereadLines[i] != tr.outputLines[i] {
			return fmt.Errorf("post-write validation failed at line %d", i+1)
		}
	}
	fmt.Fprintf(stdout, "Applied: %d checkpoint(s) repaired.\n", tr.changed)
	fmt.Fprintf(stdout, "Backup: %s\n", backup)
	return nil
}

// recoverSessionCheckpointsMain is main(argv): it returns the exit code. cwd resolves a relative --session path.
func recoverSessionCheckpointsMain(argv []string, cwd string, stdout, stderr io.Writer) int {
	session, hasSession := "", false
	apply, confirmPiClosed := false, false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--session":
			i++
			session, hasSession = "", false
			if i < len(argv) {
				session, hasSession = argv[i], true
			}
		case strings.HasPrefix(arg, "--session="):
			session, hasSession = strings.TrimPrefix(arg, "--session="), true
		case arg == "--apply":
			apply = true
		case arg == "--confirm-pi-closed":
			confirmPiClosed = true
		case arg == "--dry-run":
			apply = false
		case arg == "--help" || arg == "-h":
			fmt.Fprintln(stdout, "Usage: pi-goal-x-recover --session <session.jsonl> [--dry-run]")
			fmt.Fprintln(stdout, "       pi-goal-x-recover --session <session.jsonl> --apply --confirm-pi-closed")
			return 0
		default:
			fmt.Fprintf(stderr, "error: unknown argument: %s\n", arg)
			return 1
		}
	}
	if !hasSession || session == "" {
		fmt.Fprintln(stderr, "error: --session <path> is required")
		return 1
	}
	if apply && !confirmPiClosed {
		fmt.Fprintln(stderr, "error: --apply requires --confirm-pi-closed.")
		fmt.Fprintln(stderr, "Close Pi first: rewriting the session behind Pi's in-memory state is unsafe.")
		return 1
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if err := recoverSession(session, apply, cwd, stdout); err != nil {
		fmt.Fprintf(stderr, "error: %s\n", err.Error())
		return 1
	}
	return 0
}
