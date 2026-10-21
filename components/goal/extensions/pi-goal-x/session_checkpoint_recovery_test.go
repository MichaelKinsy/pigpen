package pi_goal_x

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sessionRecoveryFile = "session-checkpoint-recovery"

type recoveryFixture struct{ dir, file string }

func makeSessionFixture(t *testing.T) recoveryFixture {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "session.jsonl")
	legacyContent := "[GOAL CHECKPOINT goalId=g1]\nContinue working toward the active pi goal.\n" + strings.Repeat("x", 6000)
	lines := []string{marshalJSON(detailsOf("type", "session", "version", 3.0, "id", "sess-1", "timestamp", "2026-08-23T00:00:00.000Z", "cwd", dir), "")}
	parent := "root"
	for i := range 5 {
		id := "cp-" + jsNumber(float64(i))
		var parentID any = parent
		if parent == "root" {
			parentID = nil
		}
		lines = append(lines, marshalJSON(healthEntry("id", id, "parentId", parentID, "content", legacyContent, "details", detailsOf("kind", "checkpoint", "goalId", "g1", "objective", "the whole objective")), ""))
		parent = id
	}
	// Non-goal entries that must stay byte-identical.
	lines = append(lines, marshalJSON(detailsOf("type", "custom", "id", "cust-1", "parentId", parent, "timestamp", "2026-08-23T00:00:01.000Z", "customType", "pi-goal-focus", "data", detailsOf("focusedGoalId", "g1")), ""))
	lines = append(lines, "{broken json line")
	must(0, os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return recoveryFixture{dir: dir, file: file}
}

// runCli is the CLI run: stdout and stderr together, and the exit code.
func runCli(cwd string, args ...string) (string, int) {
	var out, errb bytes.Buffer
	code := recoverSessionCheckpointsMain(args, cwd, &out, &errb)
	return out.String() + errb.String(), code
}

func TestSessionCheckpointRecovery(t *testing.T) {
	tw(t, sessionRecoveryFile, "dry-run reports changes and writes nothing", func(t *testing.T) {
		fx := makeSessionFixture(t)
		before := string(must(os.ReadFile(fx.file)))
		out, code := runCli("", "--session", fx.file)
		eq(t, code, 0, "exit")
		eq(t, regexp.MustCompile(`5 legacy checkpoint\(s\) rewriteable`).MatchString(out), true, out)
		eq(t, regexp.MustCompile(`dry-run: no changes written`).MatchString(out), true, out)
		eq(t, string(must(os.ReadFile(fx.file))), before, "dry-run must not modify the file")
		n := 0
		for _, e := range must(os.ReadDir(fx.dir)) {
			if strings.Contains(e.Name(), "backup") {
				n++
			}
		}
		eq(t, n, 0, "backups")
	})
	tw(t, sessionRecoveryFile, "apply rewrites only checkpoint content/details and preserves the graph", func(t *testing.T) {
		fx := makeSessionFixture(t)
		originalLines := strings.Split(string(must(os.ReadFile(fx.file))), "\n")
		_, code := runCli("", "--session", fx.file, "--apply", "--confirm-pi-closed")
		eq(t, code, 0, "exit")

		afterLines := strings.Split(string(must(os.ReadFile(fx.file))), "\n")
		eq(t, len(afterLines), len(originalLines), "line count unchanged")

		backups := []string{}
		for _, e := range must(os.ReadDir(fx.dir)) {
			if strings.HasPrefix(e.Name(), "session.jsonl.backup-") {
				backups = append(backups, e.Name())
			}
		}
		eq(t, len(backups), 1, "exactly one backup created")
		backupText := string(must(os.ReadFile(filepath.Join(fx.dir, backups[0]))))
		eq(t, regexp.MustCompile(`GOAL CHECKPOINT goalId=g1`).MatchString(backupText), true, "backup preserves the pre-repair content")

		checkpoints := 0
		for i, after := range afterLines {
			beforeLine := originalLines[i]
			if jsTrim(after) == "" {
				continue
			}
			pa, errA := parseJSON([]byte(after))
			pb, errB := parseJSON([]byte(beforeLine))
			if errA != nil || errB != nil {
				eq(t, after, beforeLine, "malformed line byte-identical")
				continue
			}
			parsedAfter, parsedBefore := pa.(*jsObject), pb.(*jsObject)
			if !strings.Contains(after, `"pi-goal-event"`) || parsedBefore.vals["type"] != any("custom_message") {
				eq(t, after, beforeLine, "non-goal line byte-identical")
				continue
			}
			checkpoints++
			eq(t, parsedAfter.vals["id"], parsedBefore.vals["id"], "id")
			eq(t, parsedAfter.vals["parentId"], parsedBefore.vals["parentId"], "parentId")
			eq(t, parsedAfter.vals["content"], any(`<pi_goal_continuation goal_id="g1" kind="checkpoint" v="2"/>`), "content is the bounded v2 marker")
			details := parsedAfter.obj("details")
			eq(t, details.vals["version"], any(2.0), "details.version")
			eq(t, details.has("objective"), false, "objective stripped from details")
		}
		eq(t, checkpoints, 5, "checkpoints")

		second, _ := runCli("", "--session", fx.file)
		eq(t, regexp.MustCompile(`0 legacy checkpoint\(s\) rewriteable`).MatchString(second), true, second)
	})
	tw(t, sessionRecoveryFile, "is idempotent after apply (file bytes stable)", func(t *testing.T) {
		fx := makeSessionFixture(t)
		runCli("", "--session", fx.file, "--apply", "--confirm-pi-closed")
		once := string(must(os.ReadFile(fx.file)))
		runCli("", "--session", fx.file, "--apply", "--confirm-pi-closed")
		eq(t, string(must(os.ReadFile(fx.file))), once, "file bytes stable")
	})
	tw(t, sessionRecoveryFile, "refuses --apply without --confirm-pi-closed", func(t *testing.T) {
		fx := makeSessionFixture(t)
		before := string(must(os.ReadFile(fx.file)))
		out, code := runCli("", "--session", fx.file, "--apply")
		eq(t, code, 1, "exit")
		eq(t, strings.Contains(out, "confirm-pi-closed"), true, out)
		eq(t, string(must(os.ReadFile(fx.file))), before, "refused apply must not modify the file")
	})
	tw(t, sessionRecoveryFile, "refuses a symlink target", func(t *testing.T) {
		fx := makeSessionFixture(t)
		link := filepath.Join(fx.dir, "linked.jsonl")
		must(0, os.Symlink(fx.file, link))
		out, code := runCli("", "--session", link)
		eq(t, code, 1, "exit")
		eq(t, strings.Contains(out, "symbolic link"), true, out)
	})
	tw(t, sessionRecoveryFile, "preserves malformed unrelated lines byte-identically", func(t *testing.T) {
		fx := makeSessionFixture(t)
		runCli("", "--session", fx.file, "--apply", "--confirm-pi-closed")
		text := string(must(os.ReadFile(fx.file)))
		eq(t, strings.Contains(text, "{broken json line"), true, "malformed line preserved verbatim")
	})
	tw(t, sessionRecoveryFile, "accepts POSIX-style relative paths resolved from cwd", func(t *testing.T) {
		fx := makeSessionFixture(t)
		out, code := runCli(fx.dir, "--session", filepath.Base(fx.file))
		eq(t, code, 0, "exit")
		eq(t, regexp.MustCompile(`5 legacy checkpoint\(s\) rewriteable`).MatchString(out), true, out)
	})
}
