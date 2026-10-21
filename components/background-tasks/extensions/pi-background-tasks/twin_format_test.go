package pi_background_tasks

import (
	"math"
	"testing"
)

// Twins of the original's tests of formatting and the small leaf modules.

func TestTwinFormatting(t *testing.T) {
	capN := wakeManifestFieldMaxChars

	tw(t, "compact-log-snapshot", "compact log snapshot field rows", func(t *testing.T) {
		rows := []struct {
			name, field, input, expected string
		}{
			{"huge command retains its bounded prefix", "command", repeat("C", 200_000), repeat("C", capN-1) + "…"},
			{"huge title retains its bounded prefix", "title", repeat("T", 2_000), repeat("T", capN-1) + "…"},
			{"huge cwd retains its bounded prefix", "cwd", "/path/" + repeat("P", 5_000), "/path/" + repeat("P", capN-7) + "…"},
			{"huge logFile retains its bounded prefix", "logFile", "/tmp/" + repeat("L", 5_000), "/tmp/" + repeat("L", capN-6) + "…"},
			{"small command is unchanged", "command", "echo log", "echo log"},
		}
		for _, row := range rows {
			s := fakeSnapshot(func(s *snapshot) {
				switch row.field {
				case "command":
					s.Command = row.input
				case "title":
					s.Title = row.input
				case "cwd":
					s.Cwd = row.input
				case "logFile":
					s.LogFile = row.input
				}
			})
			c := compactBackgroundTaskSnapshot(&s)
			got := map[string]string{"command": c.Command, "title": c.Title, "cwd": c.Cwd, "logFile": c.LogFile}[row.field]
			eq(t, got, row.expected, row.name)
			mustTrue(t, jsLen(got) <= capN, row.name+": bounded")
		}
	})

	tw(t, "task-status-format", "task status formatting rows", func(t *testing.T) {
		rows := []struct {
			name, status, reason, expected string
			exit                           *int
		}{
			{"completed self-exit omits reason", statusCompleted, "self-exit", "completed (exit 0)", exitCode(0)},
			{"failed self-exit omits reason", statusFailed, "self-exit", "failed (exit 137)", exitCode(137)},
			{"extension stop appends reason", statusStopped, "extension-stop", "stopped (extension-stop)", nil},
			{"restart reconciliation appends reason", statusStopped, "reconcile-on-restart", "stopped (reconcile-on-restart)", nil},
			{"gone orphan appends reason", statusFailed, "orphaned-pid-gone", "failed (exit ?) (orphaned-pid-gone)", nil},
			{"external exit appends reason", statusFailed, "external", "failed (exit ?) (external)", nil},
			{"stopped without reason", statusStopped, "", "stopped", nil},
			{"completed without reason", statusCompleted, "", "completed (exit 0)", exitCode(0)},
		}
		for _, row := range rows {
			eq(t, summarizeTaskStatus(row.status, row.exit, row.reason), row.expected, row.name)
		}
	})

	tw(t, "task-display-summary", "transcript display and summary rows", func(t *testing.T) {
		const dcap = taskDisplayNameMaxChars
		rows := []struct{ name, title, command, expected string }{
			{"summary bounds huge title and command", repeat("T", 50_000), repeat("Q", 50_000), repeat("T", dcap-1) + "…"},
			{"empty title falls back to bounded command", "", repeat("Q", 5_000), repeat("Q", dcap-1) + "…"},
			{"short title is retained", "log", "echo log", "log"},
		}
		for _, row := range rows {
			s := fakeSnapshot(func(s *snapshot) {
				s.Title, s.Command, s.ID, s.Pid, s.Status, s.ExitCode = row.title, row.command, "bg-log-1", 4242, statusCompleted, exitCode(0)
			})
			name := taskDisplayNameForTranscript(s.Title, s.Command)
			line := buildTaskSummaryLine(&s, s.UpdatedAt)
			eq(t, name, row.expected, row.name+": name")
			eq(t, line, "bg-log-1 · completed (exit 0) · pid 4242 · "+row.expected+" · now", row.name+": line")
			mustTrue(t, jsLen(name) <= dcap && jsLen(line) < dcap+128, row.name+": bounded")
		}
	})

	tw(t, "transcript-truncation", "transcript truncation rows", func(t *testing.T) {
		rows := []struct {
			name     string
			value    string
			expected string
			verb     string
		}{
			{"Stopped template bounds huge command", repeat("B", 100_000), repeat("B", capN-1) + "…", "Stopped"},
			{"Stopping template bounds huge command", repeat("B", 100_000), repeat("B", capN-1) + "…", "Stopping"},
			{"small value is unchanged", "echo log", "echo log", "Stopped"},
			{"empty value is unchanged", "", "", "Stopped"},
			{"exact cap is unchanged", repeat("B", capN), repeat("B", capN), "Stopped"},
		}
		for _, row := range rows {
			command := truncateForTranscript(row.value, capN)
			message := row.verb + " bg-log-1 (" + command + ")."
			eq(t, command, row.expected, row.name)
			eq(t, message, row.verb+" bg-log-1 ("+row.expected+").", row.name+": message")
			mustTrue(t, len(message) < capN+128, row.name+": bounded message")
		}
		// the missing value (undefined in the original) is the zero string here; truncateForTranscript keeps it empty
		eq(t, truncateForTranscript("", capN), "", "missing value remains empty")
	})

	tw(t, "task-log-format", "log formatting and descriptor rows", func(t *testing.T) {
		agent := testAgent(t)
		_ = agent
		const lcap = defaultLogTailMaxChars
		marker := "retained log tail\n"
		tail := marker + repeat("y", lcap-len(marker)-1) + "!"
		rows := []struct {
			name, output, path string
			clipped            bool
		}{
			{"short descriptor is absent", repeat("short\n", 10), "/tmp/log", false},
			{"small output is unchanged", repeat("all good\n", 50), "/tmp/log", false},
			{"exact cap is unchanged", repeat("x", lcap), "/tmp/log", false},
			{"huge output retains the exact tail and descriptor", repeat("x", lcap*4) + tail, "/tmp/big.log", true},
			{"empty output uses sentinel", "", "/tmp/log", false},
			{"long log path is bounded in banner and descriptor", repeat("z", lcap*3) + tail, "/tmp/" + repeat("L", 10_000), true},
		}
		cwd := t.TempDir()
		for _, row := range rows {
			formatted := formatTaskLog(row.output, row.path, cwd)
			descriptor := taskLogTruncation(row.output, row.path, cwd)
			safePath := row.path
			if jsLen(row.path) > capN {
				safePath = jsHead(row.path, capN-1) + "…"
			}
			expected := row.output
			if expected == "" {
				expected = "(empty)"
			}
			if row.clipped {
				expected = "[...truncated]\n" + tail + "\n\n[Background log truncated. Showing last " + itoa(lcap) + " of " + itoa(jsLen(row.output)) + " character(s). Full log: " + safePath + "]"
			}
			eq(t, formatted, expected, row.name)
			if row.clipped {
				eq(t, descriptor, &logTruncation{Direction: "tail", FullOutputPath: safePath, ShownChars: lcap, TotalChars: jsLen(row.output), Truncated: true}, row.name+": descriptor")
			} else {
				eq(t, descriptor == nil, true, row.name+": no descriptor")
			}
			extra := 0
			if jsLen(row.path) > capN {
				extra = capN
			}
			mustTrue(t, jsLen(formatted) < lcap+extra+256, row.name+": bounded")
			mustTrue(t, !containsStr(formatted, repeat("L", capN+1)), row.name+": excludes the long path")
		}
	})

	tw(t, "line-count", "line count", func(t *testing.T) {
		for _, row := range []struct {
			name, text string
			expected   int
		}{
			{"empty string", "", 0}, {"single line", "hello", 1}, {"two lines", "a\nb", 2}, {"trailing LF", "a\nb\n", 2},
			{"trailing CRLF", "a\r\nb\r\n", 2}, {"lone newline", "\n", 0}, {"blank interior line", "a\n\nb\n", 3},
		} {
			eq(t, lineCount(row.text), row.expected, row.name)
		}
	})

	tw(t, "wake-budget", "output wake budget limits", func(t *testing.T) {
		limits := outputWakeBudgetLimits{maxWakes: defaultWakeBudgetMaxWakes, maxBytes: defaultWakeBudgetMaxBytes}
		for _, row := range []struct {
			name      string
			budget    outputWakeBudget
			limits    outputWakeBudgetLimits
			nextBytes int
			expected  bool
		}{
			{"wake count cap", outputWakeBudget{Wakes: limits.maxWakes}, limits, 0, true},
			{"byte cap", outputWakeBudget{Bytes: limits.maxBytes}, limits, 1, true},
			{"disabled caps", outputWakeBudget{Wakes: 100, Bytes: 1_000_000}, outputWakeBudgetLimits{}, 1_000, false},
			{"remaining capacity", outputWakeBudget{}, limits, 1, false},
		} {
			b := row.budget
			eq(t, wouldExhaustOutputWakeBudget(&b, row.limits, row.nextBytes), row.expected, row.name)
		}
	})

	tw(t, "wake-notify-mode", "default output notify mode", func(t *testing.T) {
		for _, row := range []struct{ name, pattern, expected string }{
			{"pattern", "READY", "first-match-only"}, {"missing pattern", "", "transition"}, {"blank pattern", "   ", "transition"},
		} {
			eq(t, defaultNotifyMode(row.pattern), row.expected, row.name)
		}
	})

	tw(t, "wake-notify-mode", "resolved output notify mode", func(t *testing.T) {
		for _, row := range []struct{ name, mode, pattern, expected string }{
			{"explicit always", "always", "", "always"}, {"explicit transition", "transition", "READY", "transition"},
			{"explicit first match", "first-match-only", "", "first-match-only"}, {"pattern fallback", "", "READY", "first-match-only"},
			{"missing fallback", "", "", "transition"}, {"invalid fallback", "garbage", "READY", "first-match-only"},
		} {
			eq(t, resolveNotifyMode(row.mode, row.pattern), row.expected, row.name)
		}
	})

	tw(t, "wake-budget-normalization", "normalizes each input into independent budget state", func(t *testing.T) {
		zero := outputWakeBudget{}
		at := func(n int64) *int64 { return &n }
		for _, row := range []struct {
			name     string
			input    any
			expected outputWakeBudget
		}{
			{"undefined budget", nil, zero},
			{"null budget", (*outputWakeBudget)(nil), zero},
			{"malformed wake count preserves other fields", map[string]any{"wakes": "garbage", "bytes": 100.0, "exhausted": true, "announcedAt": 999.0}, outputWakeBudget{Bytes: 100, Exhausted: true, AnnouncedAt: at(999)}},
			{"negative byte count preserves other fields", map[string]any{"wakes": 3.0, "bytes": -5.0, "exhausted": true, "announcedAt": 999.0}, outputWakeBudget{Wakes: 3, Exhausted: true, AnnouncedAt: at(999)}},
			{"malformed exhausted flag preserves other fields", map[string]any{"wakes": 3.0, "bytes": 100.0, "exhausted": "yes", "announcedAt": 999.0}, outputWakeBudget{Wakes: 3, Bytes: 100, AnnouncedAt: at(999)}},
			{"malformed announcement time preserves other fields", map[string]any{"wakes": 3.0, "bytes": 100.0, "exhausted": true, "announcedAt": "later"}, outputWakeBudget{Wakes: 3, Bytes: 100, Exhausted: true}},
			{"normalized budget does not share the source object", outputWakeBudget{Wakes: 3, Bytes: 100, Exhausted: true, AnnouncedAt: at(999)}, outputWakeBudget{Wakes: 3, Bytes: 100, Exhausted: true, AnnouncedAt: at(999)}},
			{"JSON budget round trip retains every field", map[string]any{"wakes": 7.0, "bytes": 1234.0, "exhausted": true, "announcedAt": 555.0}, outputWakeBudget{Wakes: 7, Bytes: 1234, Exhausted: true, AnnouncedAt: at(555)}},
			{"combined malformed budget fields", map[string]any{"wakes": "garbage", "bytes": -5.0, "exhausted": "yes", "announcedAt": "later"}, zero},
		} {
			var sourceBefore outputWakeBudget
			source, isStruct := row.input.(outputWakeBudget)
			if isStruct {
				sourceBefore = source
				if source.AnnouncedAt != nil {
					v := *source.AnnouncedAt
					sourceBefore.AnnouncedAt = &v
				}
			}
			normalized := normalizeOutputWakeBudget(row.input)
			before := *normalized
			if before.AnnouncedAt != nil {
				v := *before.AnnouncedAt
				before.AnnouncedAt = &v
			}
			// Mutating the normalized value must not reach the source.
			normalized.Wakes, normalized.Bytes, normalized.Exhausted = 7, 222, false
			normalized.AnnouncedAt = at(123)
			eq(t, before, row.expected, row.name)
			if isStruct {
				eq(t, source, sourceBefore, row.name+": the source is untouched")
			}
		}
	})

	tw(t, "tool-result-details", "bounded tool-result list rows", func(t *testing.T) {
		for _, row := range []struct {
			name           string
			count, firstID int
			huge           bool
			reason         string
			sample, omit   int
		}{
			{"small list retains full serialized snapshots", 2, 1, false, "", 0, 0},
			{"empty list retains the empty array", 0, 0, false, "", 0, 0},
			{"exact count threshold retains full snapshots", 50, 0, false, "", 0, 0},
			{"count overflow uses the sampled manifest", 100, 0, false, "task-count-threshold", 20, 80},
			{"byte overflow below count threshold uses the manifest", 1, 0, true, "payload-too-large", 1, 0},
		} {
			tasks := make([]snapshot, row.count)
			for i := range tasks {
				i := i
				tasks[i] = fakeSnapshot(func(s *snapshot) {
					s.ID, s.Pid, s.Command = "bg-"+itoa(i+row.firstID), 10_000+i, "printf ready"
					if row.huge {
						s.Command = repeat("x", 80*1024)
					}
				})
			}
			result := bgToolResultTasks(tasks, toolResultOptions{})
			if row.reason == "" {
				list, ok := result.([]snapshot)
				mustTrue(t, ok && !isBgToolResultBoundedTasks(result), row.name+": full list")
				eq(t, len(list), row.count, row.name+": count")
				continue
			}
			m, ok := result.(*bgToolResultBounded)
			mustTrue(t, ok && isBgToolResultBoundedTasks(result), row.name+": manifest")
			eq(t, m.Reason, row.reason, row.name+": reason")
			eq(t, m.Counts.Tasks, row.count, row.name+": counts")
			eq(t, len(m.TaskIDs), row.sample, row.name+": sample")
			eq(t, m.Omitted.Tasks, row.omit, row.name+": omitted")
			eq(t, m.FullSnapshot, false, row.name+": fullSnapshot")
			wrapped := jsonLen(map[string]any{"action": "list", "tasks": result})
			mustTrue(t, jsonLen(result) <= 4*1024 && wrapped <= 4*1024, row.name+": bounded")
		}
	})

	tw(t, "tool-result-restore-barrier", "tool-result restore barrier rows", func(t *testing.T) {
		for _, row := range []struct {
			name           string
			sidecarLoaded  bool
			emptySidecar   bool
			expectedIDs    []string
			expectedClears int
		}{
			{"manifest restores the sidecar after an older full list", true, false, []string{"bg-sidecar"}, 1},
			{"manifest without a sidecar keeps the accumulated full list", false, false, []string{"bg-sidecar", "bg-old"}, 0},
			{"loaded empty sidecar clears accumulated older tasks", true, true, []string{}, 1},
		} {
			sidecar := fakeSnapshot(func(s *snapshot) { s.ID, s.UpdatedAt = "bg-sidecar", 2000 })
			older := fakeSnapshot(func(s *snapshot) { s.ID, s.UpdatedAt = "bg-old", 1000 })
			current := []snapshot{sidecar}
			clears := 0
			sidecarTasks := []snapshot{sidecar}
			if row.emptySidecar {
				sidecarTasks = []snapshot{}
			}
			apply := func(s snapshot) { current = append(current, s) }
			clear := func() { clears++; current = nil }
			applyBgToolResultTasksWithBarrier([]snapshot{older}, row.sidecarLoaded, sidecarTasks, clear, apply)
			eq(t, idsOf(current), []string{"bg-sidecar", "bg-old"}, row.name+": after the full list")
			eq(t, clears, 0, row.name+": no clear for a full list")
			many := make([]snapshot, 100)
			for i := range many {
				many[i] = fakeSnapshot(func(s *snapshot) { s.ID = "bg-" + itoa(i) })
			}
			applyBgToolResultTasksWithBarrier(bgToolResultTasks(many, toolResultOptions{}), row.sidecarLoaded, sidecarTasks, clear, apply)
			eq(t, idsOf(current), nonNilStrings(row.expectedIDs), row.name+": after the manifest")
			eq(t, clears, row.expectedClears, row.name+": clears")
		}
	})
}

func idsOf(list []snapshot) []string {
	out := []string{}
	for _, s := range list {
		out = append(out, s.ID)
	}
	return out
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

var _ = math.Inf
