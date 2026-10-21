package pi_background_tasks

import (
	"strings"
	"testing"
)

// Twins of the original's wake-event tests.

func at(n int64) *int64 { return &n }

func TestTwinWakeEvents(t *testing.T) {
	capN := wakeManifestFieldMaxChars

	tw(t, "wake-events", "wake send lifecycle rows", func(t *testing.T) {
		rows := []struct {
			name, event, status string
			eventAt             int64
			void, exhausted     bool
			sent                bool
			reason              string
			wakes, bytes        int
		}{
			{"output metadata and accounting", "output", "running", 1_111, false, false, true, "", 1, 64},
			{"exit metadata without output accounting", "exit", "completed", 1_222, false, false, true, "", 0, 0},
			{"exhausted budget still sends exit", "exit", "completed", 7_000, false, true, true, "", 20, 0},
			{"stopped output is suppressed", "output", "stopped", 1_001, false, false, false, "output-after-stop-suppressed", 0, 0},
			{"voided output is suppressed", "output", "running", 1_333, true, false, false, "voided", 0, 0},
		}
		for _, row := range rows {
			task := fakeTask(func(x *task) {
				x.ID, x.Status, x.NotifyOnOutput, x.NotifyMode = "bg-7", row.status, row.event == "output", "always"
				if row.status == "stopped" {
					x.stopReason = "user"
				}
				wakes := 0
				if row.exhausted {
					wakes = 20
				}
				x.OutputWakeBudget = &outputWakeBudget{Wakes: wakes, Exhausted: row.exhausted}
			})
			pending := scheduleTaskWake(task, row.event, row.eventAt)
			diagnostics := noDiagnostics()
			voided := 0
			if row.void {
				voided = voidPendingTaskWakes(task, "stop", func(d wakeDiagnostic) { *diagnostics = append(*diagnostics, d) }, func() int64 { return 1_400 })
			}
			deps, messages := sendDeps(repeat("y", 64), diagnostics)
			sent := sendTaskWake(deps, row.event, task, sendTaskWakeOptions{eventAt: &pending.EventAt, sequence: &pending.Sequence})
			record := wakeEvent{EventAt: row.eventAt, EventType: row.event, Sequence: 1, TaskStatusAtEmit: row.status, DroppedReason: row.reason}
			if row.sent {
				record.DeliveredAt = at(2_000)
			}
			eq(t, sent, row.sent, row.name+": sent")
			wantVoided := 0
			if row.void {
				wantVoided = 1
			}
			eq(t, voided, wantVoided, row.name+": voided")
			wantMembership := []int{}
			if row.void {
				wantMembership = []int{1}
			}
			eq(t, sortedVoided(task.voidedWakes), wantMembership, row.name+": membership")
			eq(t, task.PendingWakes, []wakePending{}, row.name+": pending")
			eq(t, taskSnapshot(task).WakeEvents, []wakeEvent{record}, row.name+": events")
			eq(t, task.OutputWakeBudget.Wakes, row.wakes, row.name+": wakes")
			eq(t, task.OutputWakeBudget.Bytes, row.bytes, row.name+": bytes")
			eq(t, task.OutputWakeBudget.Exhausted, row.exhausted, row.name+": exhausted")
			if row.sent {
				eq(t, len(*messages), 1, row.name+": one message")
				d := (*messages)[0].message.Details.(eventDetails)
				eq(t, wakeEvent{DeliveredAt: &d.DeliveredAt, EventAt: d.EventAt, EventType: d.EventType, Sequence: d.Sequence, TaskStatusAtEmit: d.TaskStatusAtEmit}, record, row.name+": details")
			} else {
				eq(t, len(*messages), 0, row.name+": no message")
			}
			type diag struct {
				reason    string
				sequence  int
				timestamp int64
			}
			var gotDiag []diag
			for _, d := range *diagnostics {
				gotDiag = append(gotDiag, diag{d.Reason, *d.Sequence, d.Timestamp})
			}
			var wantDiag []diag
			switch {
			case row.void:
				wantDiag = []diag{{"wake-voided", 1, 1_400}, {"voided-wake-fired", 1, 2_000}}
			case !row.sent:
				wantDiag = []diag{{row.reason, 1, 2_000}}
			}
			eq(t, gotDiag, wantDiag, row.name+": diagnostics")
		}
	})

	tw(t, "wake-events", "wake payload retained content", func(t *testing.T) {
		type fields struct{ command, title, cwd, pattern, dedupe, logFile string }
		short := fields{"printf ready", "task", "/repo", "READY", "key", "/private/log"}
		long := fields{repeat("Z", 100_000), repeat("T", 2_000), "/path/" + repeat("C", 2_000), repeat("P", 2_000), repeat("D", 2_000), "/tmp/" + repeat("L", 2_000)}
		bounded := fields{repeat("Z", capN-1) + "…", repeat("T", capN-1) + "…", "/path/" + repeat("C", capN-7) + "…", repeat("P", capN-1) + "…", repeat("D", capN-1) + "…", "/tmp/" + repeat("L", capN-6) + "…"}
		alert := defaultOutputAlertMaxChars
		rows := []struct {
			name, event, output string
			in, want            fields
			matched             string
			tail                string
			truncated           bool
			wantPattern         string
		}{
			{"truncated x output", "output", repeat("x", 1_000_000), short, short, "", "[...truncated]\n" + repeat("x", alert), true, ""},
			{"truncated y output", "output", repeat("y", 1_000_000), short, short, "", "[...truncated]\n" + repeat("y", alert), true, ""},
			{"exit uses full output tail", "exit", "line-a\nline-b\nline-c\n", short, short, "", "line-a\nline-b\nline-c\n", false, ""},
			{"oversized manifest fields", "output", repeat("y", 1_000_000), long, bounded, "", "[...truncated]\n" + repeat("y", alert), true, ""},
			{"oversized matched pattern", "output", repeat("y", 1_000), fields{short.command, short.title, short.cwd, repeat("P", 100_000), short.dedupe, short.logFile}, fields{short.command, short.title, short.cwd, repeat("P", capN-1) + "…", short.dedupe, short.logFile}, repeat("P", 100_000), repeat("y", 1_000), false, repeat("P", capN-1) + "…"},
		}
		for _, row := range rows {
			task := fakeTask(func(x *task) {
				x.Command, x.Title, x.Cwd, x.NotifyPattern, x.DedupeKey, x.LogFile = row.in.command, row.in.title, row.in.cwd, row.in.pattern, row.in.dedupe, row.in.logFile
				x.ID, x.NotifyOnOutput = "bg-7", true
				x.Status = "running"
				if row.event == "exit" {
					x.Status = "completed"
				}
				x.ProcIdent = fakeIdent(4242)
				x.WakeEvents, x.PendingWakes, x.VoidedWakeSequences = []wakeEvent{}, []wakePending{}, []int{}
				x.LastOutputDedupeBy = map[string]string{"key": "hash"}
			})
			deps, messages := sendDeps(row.output, noDiagnostics())
			opts := sendTaskWakeOptions{eventAt: at(1_111), matchedPattern: row.matched}
			if row.event == "output" {
				opts.newOutputTail = row.tail
			}
			sent := sendTaskWake(deps, row.event, task, opts)
			d := (*messages)[0].message.Details.(eventDetails)
			preview := row.in.command
			if jsLen(preview) > wakeContentCommandMaxChars {
				preview = repeat("Z", wakeContentCommandMaxChars-1) + "…"
			}
			eq(t, sent, true, row.name+": sent")
			eq(t, len(*messages), 1, row.name+": count")
			eq(t, d.OutputTail, row.tail, row.name+": tail")
			eq(t, d.OutputTailTruncated, row.truncated, row.name+": truncated")
			eq(t, d.MatchedPattern, row.wantPattern, row.name+": matched")
			mustTrue(t, strings.Contains((*messages)[0].message.Content, preview), row.name+": content has the preview")
			s := d.Task
			eq(t, fields{s.Command, s.Title, s.Cwd, s.NotifyPattern, s.DedupeKey, s.LogFile}, row.want, row.name+": fields")
			// The compact view carries none of the internal state arrays or the process identity (no such fields exist).
			raw := jsonString(s)
			for _, key := range []string{"wakeEvents", "pendingWakes", "voidedWakeSequences", "lastOutputDedupeByKey", "procIdent"} {
				mustTrue(t, !strings.Contains(raw, `"`+key+`"`), row.name+": omits "+key)
			}
			mustTrue(t, !strings.Contains(jsonString(d), "newOutputTail"), row.name+": no newOutputTail key")
		}
	})

	tw(t, "wake-events", "wake payload byte and character bounds", func(t *testing.T) {
		rows := []struct {
			name, measure string
			limit         int
			pattern       bool
		}{
			{"details bytes", "details", 4_096, false},
			{"wrapped message bytes", "message", 4_096, false},
			{"matched pattern message bytes", "message", 4_096, true},
			{"command content characters", "content", wakeContentCommandMaxChars + 128, false},
			{"overlong command run is absent", "forbidden", wakeContentCommandMaxChars + 1, false},
		}
		for _, row := range rows {
			task := fakeTask(func(x *task) {
				x.ID, x.NotifyOnOutput, x.Command, x.Title, x.Cwd = "bg-7", true, repeat("Z", 100_000), repeat("T", 2_000), repeat("C", 2_000)
				x.NotifyPattern, x.DedupeKey, x.LogFile = repeat("P", 2_000), repeat("D", 2_000), repeat("L", 2_000)
			})
			size := 1_000_000
			if row.pattern {
				size = 1_000
			}
			deps, messages := sendDeps(repeat("y", size), noDiagnostics())
			matched := ""
			if row.pattern {
				matched = repeat("P", 100_000)
			}
			sendTaskWake(deps, "output", task, sendTaskWakeOptions{eventAt: at(1_111), matchedPattern: matched})
			m := (*messages)[0].message
			switch row.measure {
			case "forbidden":
				mustTrue(t, !strings.Contains(m.Content, repeat("Z", row.limit)), row.name)
			case "content":
				mustTrue(t, jsLen(m.Content) < row.limit, row.name)
			case "details":
				mustTrue(t, jsonLen(m.Details) < row.limit, row.name)
			default:
				mustTrue(t, jsonLen(map[string]any{"content": m.Content, "details": m.Details}) < row.limit, row.name)
			}
		}
	})

	tw(t, "wake-budget-notice", "budget exhaustion notice content and once-only delivery", func(t *testing.T) {
		rows := []struct{ name, command, title, log, wantCommand, wantTitle, wantLog string }{
			{"short log notice", "printf ready", "task", "/private/log", "printf ready", "task", "/private/log"},
			{"bounded log notice", repeat("Q", 200_000), repeat("T", 5_000), "/tmp/" + repeat("B", 5_000), repeat("Q", capN-1) + "…", repeat("T", capN-1) + "…", "/tmp/" + repeat("B", capN-6) + "…"},
		}
		for _, row := range rows {
			task := fakeTask(func(x *task) {
				x.ID, x.Pid, x.Command, x.Title, x.LogFile = "bg-budget", 4243, row.command, row.title, row.log
				x.OutputWakeBudget = &outputWakeBudget{Wakes: 20}
			})
			diagnostics := noDiagnostics()
			d, messages := sendDeps("ready\n", diagnostics)
			deps := sendTaskNoticeDeps{logDiagnostic: d.logDiagnostic, messageType: d.messageType, now: d.now, rememberSnapshot: d.rememberSnapshot, sendMessage: d.sendMessage}
			first := sendOutputWakeBudgetExhaustedNotice(deps, task, outputWakeBudgetLimits{20, 20_000})
			second := sendOutputWakeBudgetExhaustedNotice(deps, task, outputWakeBudgetLimits{20, 20_000})
			det := (*messages)[0].message.Details.(budgetExhaustedDetails)
			eq(t, first, true, row.name+": first")
			eq(t, second, false, row.name+": second")
			eq(t, len(*messages), 1, row.name+": count")
			eq(t, det.EventType, "output-budget-exhausted", row.name+": event")
			eq(t, det.LogFile, row.wantLog, row.name+": log")
			eq(t, det.Task.Command, row.wantCommand, row.name+": command")
			eq(t, det.Task.Title, row.wantTitle, row.name+": title")
			mustTrue(t, strings.Contains((*messages)[0].message.Content, row.wantLog), row.name+": content names the log")
			eq(t, task.OutputWakeBudget, &outputWakeBudget{Wakes: 20, Exhausted: true, AnnouncedAt: at(2_000)}, row.name+": budget")
			reasons := []string{}
			for _, dg := range *diagnostics {
				reasons = append(reasons, dg.Reason)
			}
			eq(t, reasons, []string{"wake-budget-exhausted"}, row.name+": reasons")
		}
	})

	tw(t, "wake-budget-notice", "bounded exhaustion notice stays below the payload byte cap", func(t *testing.T) {
		task := fakeTask(func(x *task) {
			x.ID, x.Command, x.Title, x.LogFile = "bg-budget", repeat("Q", 200_000), repeat("T", 5_000), "/tmp/"+repeat("B", 5_000)
		})
		d, messages := sendDeps("ready\n", noDiagnostics())
		deps := sendTaskNoticeDeps{logDiagnostic: d.logDiagnostic, messageType: d.messageType, now: d.now, rememberSnapshot: d.rememberSnapshot, sendMessage: d.sendMessage}
		sendOutputWakeBudgetExhaustedNotice(deps, task, outputWakeBudgetLimits{20, 20_000})
		m := (*messages)[0].message
		mustTrue(t, jsonLen(map[string]any{"content": m.Content, "details": m.Details}) < 4_096, "below 4096 bytes")
	})

	tw(t, "wake-compact-snapshot", "compact wake snapshot fields", func(t *testing.T) {
		rows := []struct {
			name             string
			fields, expected [6]string
		}{
			{"bounded fields", [6]string{repeat("X", 5000), repeat("Y", 5000), repeat("Z", 5000), repeat("P", 5000), repeat("D", 5000), repeat("L", 5000)},
				[6]string{repeat("X", capN-1) + "…", repeat("Y", capN-1) + "…", repeat("Z", capN-1) + "…", repeat("P", capN-1) + "…", repeat("D", capN-1) + "…", repeat("L", capN-1) + "…"}},
			{"short fields", [6]string{"echo ok", "Hi", "/repo", "READY", "short", "/private/log.txt"}, [6]string{"echo ok", "Hi", "/repo", "READY", "short", "/private/log.txt"}},
		}
		for _, row := range rows {
			full := fakeSnapshot(func(s *snapshot) {
				s.Command, s.Title, s.Cwd, s.NotifyPattern, s.DedupeKey, s.LogFile = row.fields[0], row.fields[1], row.fields[2], row.fields[3], row.fields[4], row.fields[5]
				s.ID, s.Pid, s.OutputBytes, s.StartedAt, s.UpdatedAt = "bg-budget", 4243, 12, 1_700_000_000_000, 1_700_000_000_050
				s.WakeEvents = []wakeEvent{{DeliveredAt: at(1), EventAt: 1, EventType: "output", Sequence: 1, TaskStatusAtEmit: "running"}}
				s.VoidedWakeSequences = []int{1, 2, 3}
				s.PendingWakes = []wakePending{{EventAt: 1, EventType: "output", Sequence: 9}}
				s.LastOutputDedupeBy = map[string]string{"key": "hash"}
				s.ProcIdent = fakeIdent(4243)
			})
			c := compactBackgroundTaskSnapshot(&full)
			eq(t, [6]string{c.Command, c.Title, c.Cwd, c.NotifyPattern, c.DedupeKey, c.LogFile}, row.expected, row.name+": fields")
			eq(t, [7]any{c.ID, c.Pid, c.Status, c.ExitCode, c.OutputBytes, c.StartedAt, c.UpdatedAt}, [7]any{"bg-budget", 4243, "running", (*int)(nil), int64(12), int64(1_700_000_000_000), int64(1_700_000_000_050)}, row.name+": lifecycle")
			raw := jsonString(c)
			for _, key := range []string{"wakeEvents", "pendingWakes", "voidedWakeSequences", "lastOutputDedupeByKey", "procIdent"} {
				mustTrue(t, !strings.Contains(raw, `"`+key+`"`), row.name+": omits "+key)
			}
		}
	})

	tw(t, "wake-output-decision", "output wake decision traces", func(t *testing.T) {
		limits := outputWakeBudgetLimits{maxWakes: defaultWakeBudgetMaxWakes, maxBytes: defaultWakeBudgetMaxBytes}
		type step struct {
			task     int
			at       int64
			text     string
			sequence int
			key      string
			note     bool
		}
		type diag struct{ reason, taskID string }
		rows := []struct {
			name    string
			tasks   []*task
			limits  *outputWakeBudgetLimits
			steps   []step
			allowed []bool
			diags   []diag
		}{
			{"same tail suppressed then changed tail allowed", []*task{fakeTask(func(x *task) {
				x.ID, x.NotifyOnOutput, x.NotifyMode, x.DedupeKey = "bg-7", true, "transition", "pane-idle"
			})}, nil,
				[]step{{0, 1_000, "IDLE pid=123\n", 1, "", false}, {0, 1_100, "IDLE pid=123\n", 2, "", false}, {0, 1_200, "BUSY pid=123\n", 3, "", false}},
				[]bool{true, false, true}, []diag{{"output-transition-dedupe", "bg-7"}}},
			{"shared key coalesces different tasks", []*task{
				fakeTask(func(x *task) {
					x.ID, x.NotifyOnOutput, x.NotifyMode, x.DedupeKey = "bg-first", true, "transition", "shared-monitor"
				}),
				fakeTask(func(x *task) {
					x.ID, x.NotifyOnOutput, x.NotifyMode, x.DedupeKey = "bg-second", true, "transition", "shared-monitor"
				})}, nil,
				[]step{{0, 2_000, "IDLE pid=777\n", 1, "", false}, {1, 2_100, "IDLE pid=777\n", 1, "", false}},
				[]bool{true, false}, []diag{{"output-transition-dedupe", "bg-second"}}},
			{"different keys stay independent on one task", []*task{fakeTask(func(x *task) {
				x.ID, x.NotifyOnOutput, x.NotifyMode, x.DedupeKey = "bg-shared", true, "transition", "monitor-a"
			})}, nil,
				[]step{{0, 2_200, "IDLE pid=777\n", 1, "", false}, {0, 2_300, "IDLE pid=777\n", 2, "monitor-b", false}},
				[]bool{true, true}, nil},
			{"first match suppresses output only after delivery", []*task{fakeTask(func(x *task) {
				x.ID, x.NotifyOnOutput, x.NotifyMode, x.NotifyPattern = "bg-7", true, "first-match-only", "READY"
			})}, nil,
				[]step{{0, 1_000, "READY\n", 1, "", true}, {0, 1_100, "READY again\n", 2, "", false}},
				[]bool{true, false}, []diag{{"first-match-only-suppressed", "bg-7"}}},
			{"configured budget suppresses output at the cap", []*task{fakeTask(func(x *task) {
				x.ID, x.NotifyOnOutput, x.NotifyMode, x.OutputWakeBudget = "bg-budget", true, "always", &outputWakeBudget{Wakes: limits.maxWakes}
			})}, &limits, []step{{0, 3_000, "more output\n", 9, "", false}}, []bool{false}, []diag{{"wake-budget-exhausted", "bg-budget"}}},
			{"omitted limits leave the budget guard disabled", []*task{fakeTask(func(x *task) {
				x.ID, x.NotifyOnOutput, x.NotifyMode, x.OutputWakeBudget = "bg-budget", true, "always", &outputWakeBudget{Wakes: limits.maxWakes * 10, Bytes: limits.maxBytes * 10}
			})}, nil, []step{{0, 4_000, "still going\n", 10, "", false}}, []bool{true}, nil},
		}
		for _, row := range rows {
			var diags []diag
			hashes := map[string]string{}
			var allowed []bool
			for _, s := range row.steps {
				task := row.tasks[s.task]
				if s.key != "" {
					task.DedupeKey = s.key
				}
				seq := s.sequence
				ok := shouldEmitOutputWake(task, outputDecisionInput{dedupeHashes: hashes, eventAt: s.at, now: func() int64 { return 8_000 }, newOutput: s.text, newOutputTail: s.text,
					patternMatched: true, sequence: &seq, wakeBudgetLimits: row.limits, logDiagnostic: func(d wakeDiagnostic) { diags = append(diags, diag{d.Reason, d.TaskID}) }})
				if s.note {
					noteOutputWakeSent(task)
				}
				allowed = append(allowed, ok)
			}
			eq(t, allowed, row.allowed, row.name+": allowed")
			eq(t, diags, row.diags, row.name+": diagnostics")
		}
	})

	tw(t, "wake-persistence", "appended snapshot retains delivered wake metadata", func(t *testing.T) {
		agent := testAgent(t)
		_ = agent
		task := fakeTask(func(x *task) { x.ID, x.Status, x.NotifyOnOutput, x.LastOutputAt = "bg-7", "running", true, at(1_555) })
		pending := scheduleTaskWake(task, "output", 1_555)
		deps, _ := sendDeps("ready\n", noDiagnostics())
		sent := sendTaskWake(deps, "output", task, sendTaskWakeOptions{eventAt: &pending.EventAt, sequence: &pending.Sequence, newOutputTail: "ready\n"})
		var appended []struct {
			customType string
			payload    persistencePayload
		}
		p := newPersistence(persistenceDeps{
			customType: "pi-bg-state",
			appendEntry: func(customType string, payload any) error {
				appended = append(appended, struct {
					customType string
					payload    persistencePayload
				}{customType, payload.(persistencePayload)})
				return nil
			},
			// A null context appends session data without writing a user sidecar.
			activeSession: func() (sessionRef, bool) { return sessionRef{}, false },
			listSnapshots: func() []snapshot { return []snapshot{*taskSnapshot(task)} },
		})
		result := p.persistSnapshots()
		eq(t, sent, true, "sent")
		eq(t, result.appendEntry, true, "appended")
		eq(t, len(appended), 1, "one record")
		eq(t, appended[0].customType, "pi-bg-state", "custom type")
		eq(t, appended[0].payload.Tasks[0].WakeEvents, []wakeEvent{{DeliveredAt: at(2_000), EventAt: 1_555, EventType: "output", Sequence: 1, TaskStatusAtEmit: "running"}}, "events")
	})

	tw(t, "wake-scheduled-drop", "scheduled output drop records task-exit cleanup", func(t *testing.T) {
		task := fakeTask(func(x *task) { x.ID, x.Status = "bg-7", "completed" })
		pending := scheduleTaskWake(task, "output", 1_666)
		var diags []wakeDiagnostic
		recordScheduledOutputDrop(task, pending, "cleared-on-task-exit", wakeDiagnostic{}, func(d wakeDiagnostic) { diags = append(diags, d) }, func() int64 { return 1_777 })
		eq(t, task.PendingWakes, []wakePending{}, "pending")
		eq(t, task.WakeEvents, []wakeEvent{{DroppedReason: "cleared-on-task-exit", EventAt: 1_666, EventType: "output", Sequence: 1, TaskStatusAtEmit: "completed"}}, "events")
		eq(t, len(diags), 1, "one diagnostic")
		eq(t, [3]any{diags[0].Reason, diags[0].Timestamp, *diags[0].Sequence}, [3]any{"cleared-on-task-exit", int64(1_777), 1}, "diagnostic")
	})

	tw(t, "output-wake-budget-persistence", "retains each budget and restore state before the wake decision", func(t *testing.T) {
		const now = int64(1_700_000_001_000)
		limits := outputWakeBudgetLimits{maxWakes: defaultWakeBudgetMaxWakes, maxBytes: defaultWakeBudgetMaxBytes}
		type restoredState struct {
			status     string
			closed     bool
			stopReason string
			updatedAt  int64
			budget     outputWakeBudget
		}
		rows := []struct {
			name         string
			task         *task
			identity     *processIdentity
			decide       bool
			snapBudget   *outputWakeBudget
			restored     restoredState
			probed       []int
			allowed      *bool
			wantDiagnose bool
		}{}
		mk := func(budget *outputWakeBudget, status string) *task {
			return fakeTask(func(x *task) {
				x.ID, x.Pid, x.ProcIdent, x.Status, x.NotifyMode, x.NotifyOnOutput, x.UpdatedAt = "bg-budget-roundtrip", 4242, fakeIdent(4242), status, "always", true, 1_700_000_000_500
				x.OutputWakeBudget = budget
			})
		}
		tr, fa := true, false
		rows = append(rows,
			struct {
				name         string
				task         *task
				identity     *processIdentity
				decide       bool
				snapBudget   *outputWakeBudget
				restored     restoredState
				probed       []int
				allowed      *bool
				wantDiagnose bool
			}{"serialized nonzero budget retains capacity after a live restore", mk(&outputWakeBudget{Wakes: 6, Bytes: 4096}, "running"), fakeIdent(4242), true,
				&outputWakeBudget{Wakes: 6, Bytes: 4096}, restoredState{"running", false, "", 1_700_000_000_500, outputWakeBudget{Wakes: 6, Bytes: 4096}}, []int{4242}, &tr, false},
			struct {
				name         string
				task         *task
				identity     *processIdentity
				decide       bool
				snapBudget   *outputWakeBudget
				restored     restoredState
				probed       []int
				allowed      *bool
				wantDiagnose bool
			}{"nonzero budget survives a dead-process restore", mk(&outputWakeBudget{Wakes: 19, Bytes: 18_000}, "running"), nil, false,
				&outputWakeBudget{Wakes: 19, Bytes: 18_000}, restoredState{"stopped", true, "shutdown", now, outputWakeBudget{Wakes: 19, Bytes: 18_000}}, []int{4242}, nil, false},
			struct {
				name         string
				task         *task
				identity     *processIdentity
				decide       bool
				snapBudget   *outputWakeBudget
				restored     restoredState
				probed       []int
				allowed      *bool
				wantDiagnose bool
			}{"exhausted budget retains its announcement and suppresses a live restored task", mk(&outputWakeBudget{Wakes: limits.maxWakes, Bytes: limits.maxBytes, Exhausted: true, AnnouncedAt: at(1_700_000_000_400)}, "running"), fakeIdent(4242), true,
				&outputWakeBudget{Wakes: limits.maxWakes, Bytes: limits.maxBytes, Exhausted: true, AnnouncedAt: at(1_700_000_000_400)},
				restoredState{"running", false, "", 1_700_000_000_500, outputWakeBudget{Wakes: limits.maxWakes, Bytes: limits.maxBytes, Exhausted: true, AnnouncedAt: at(1_700_000_000_400)}}, []int{4242}, &fa, true},
			struct {
				name         string
				task         *task
				identity     *processIdentity
				decide       bool
				snapBudget   *outputWakeBudget
				restored     restoredState
				probed       []int
				allowed      *bool
				wantDiagnose bool
			}{"absent budget restores every zero-state field", fakeTask(func(x *task) {
				x.ID, x.Pid, x.Status, x.NotifyMode, x.UpdatedAt, x.OutputWakeBudget = "bg-without-budget", 1, "completed", "always", 1, nil
			}), nil, false, nil, restoredState{"completed", true, "", 1, outputWakeBudget{}}, nil, nil, false},
		)
		for _, row := range rows {
			snapshot := taskSnapshot(row.task)
			var probed []int
			restored := restoredTaskFromSnapshot(*snapshot, restoreOptions{now: now, identity: func(pid int) identityReading { probed = append(probed, pid); return reading(row.identity) }})
			got := restoredState{restored.Status, restored.closed, restored.stopReason, restored.UpdatedAt, *restored.OutputWakeBudget}
			eq(t, snapshot.OutputWakeBudget, row.snapBudget, row.name+": snapshot budget")
			eq(t, got, row.restored, row.name+": restored")
			eq(t, probed, row.probed, row.name+": probed pids")
			if row.decide {
				var diags []wakeDiagnostic
				seq := 99
				ok := shouldEmitOutputWake(restored, outputDecisionInput{eventAt: now, now: func() int64 { return now }, newOutput: "more\n", newOutputTail: "more\n", patternMatched: true,
					sequence: &seq, wakeBudgetLimits: &limits, logDiagnostic: func(d wakeDiagnostic) { diags = append(diags, d) }})
				eq(t, ok, *row.allowed, row.name+": decision")
				if row.wantDiagnose {
					eq(t, len(diags), 1, row.name+": one diagnostic")
					d := diags[0]
					eq(t, [6]any{*d.EventAt, d.EventType, *d.Sequence, d.TaskID, d.TaskStatus, d.Reason}, [6]any{now, "output", 99, "bg-budget-roundtrip", "running", "wake-budget-exhausted"}, row.name+": diagnostic")
				} else {
					eq(t, len(diags), 0, row.name+": no diagnostic")
				}
			}
		}
	})
}

func jsonString(v any) string { return string(mustJSON(v)) }
