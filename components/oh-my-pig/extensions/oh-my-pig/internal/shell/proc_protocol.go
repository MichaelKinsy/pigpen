package shell

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/internal-urls/proc-protocol.ts

//go:embed proc.md
var procPromptDoc string

//go:embed proc_reference.md
var procReferenceDoc string

// KillAgentHook, when set by the integrator (e.g. `agents.KillAgent`), kills a
// subagent registration in `sessionID` before `proc://<id>/kill` cancels its
// task job, matching omp's `cancelAgentRegistration` (`async/job-control.ts:641-681`).
// Signature: func(sessionID, agentID string) (status string, message string)
// returning status ("cancelled", "already_completed", or "not_found") and
// omp's message ("Killed agent <id>; its transcript stays at history://<id>.").
// When nil, killing a running `jobs.KindAgent` job falls back to cancelling the
// job and emitting omp's killed-agent message.
var KillAgentHook func(sessionID, agentID string) (status string, message string)

var procStreamsPathRE = regexp.MustCompile(`^/streams(?:/(\d+))?/?$`)

type procTarget struct {
	id        string
	action    string // "stdin", "mode", "kill", "streams"
	hasStream bool
	stream    int
}

func parseProcTarget(u urls.URL) (procTarget, error) {
	path := u.Path
	streamsMatch := procStreamsPathRE.FindStringSubmatch(path)
	if u.RawQuery != "" || (streamsMatch == nil && path != "" && path != "/" && path != "/mode" && path != "/kill") {
		href := u.RawHref
		if href == "" {
			href = u.Href
		}
		return procTarget{}, fmt.Errorf(
			"Invalid proc:// URL: %s. Use proc://, proc://<id>, proc://<id>/streams[/<n>], proc://<id>/kill, or proc://<id>/mode.",
			href,
		)
	}
	id := u.Host
	if strings.Contains(id, "/") || id == "." || id == ".." {
		return procTarget{}, fmt.Errorf("Invalid process id: %s", id)
	}
	if streamsMatch != nil {
		if id == "" {
			return procTarget{}, fmt.Errorf("proc://<id>/streams needs a job id, e.g. proc://bg_3/streams")
		}
		t := procTarget{id: id, action: "streams"}
		if streamsMatch[1] != "" {
			n, err := strconv.Atoi(streamsMatch[1])
			if err != nil {
				return procTarget{}, fmt.Errorf("Invalid stream index: %s", streamsMatch[1])
			}
			t.hasStream = true
			t.stream = n
		}
		return t, nil
	}
	action := "stdin"
	switch path {
	case "/mode":
		action = "mode"
	case "/kill":
		action = "kill"
	}
	return procTarget{id: id, action: action}, nil
}

func isProcKill(u urls.URL) bool {
	return strings.HasSuffix(u.Path, "/kill")
}

func procKillCall(id string) string {
	b, _ := json.Marshal(map[string]string{
		"path":    "proc://" + id + "/kill",
		"content": "",
	})
	return "call write with " + string(b)
}

func procWriteOnlyError(id, action string) error {
	if action == "kill" {
		return fmt.Errorf(
			"proc://%s/kill is write-only; reading it did not stop anything. To cancel %s, %s; read proc://%s for its status.",
			id, id, procKillCall(id), id,
		)
	}
	modeCall, _ := json.Marshal(map[string]string{
		"path":    "proc://" + id + "/mode",
		"content": "persist",
	})
	return fmt.Errorf(
		"proc://%s/mode is write-only; modes and stdin are services-only, not available for in-process jobs or agents. To set a service's mode, call write with %s (persist, session, or detached); read proc://%s for its current mode.",
		id, string(modeCall), id,
	)
}

// ProcProtocolHandler serves the `proc://` internal URL scheme for background
// jobs, agents, and named services tracked in `internal/jobs`.
// upstream: packages/coding-agent/src/internal-urls/proc-protocol.ts
type ProcProtocolHandler struct {
	runtime *Runtime
}

// NewProcProtocolHandler constructs a ProcProtocolHandler backed by rt (or
// DefaultRuntime when nil) and the process-wide `internal/jobs` registry.
func NewProcProtocolHandler(rt *Runtime) *ProcProtocolHandler {
	if rt == nil {
		rt = defaultRuntime
	}
	return &ProcProtocolHandler{runtime: rt}
}

// Compile-time interface assertions.
var (
	_ urls.Handler          = (*ProcProtocolHandler)(nil)
	_ urls.Writer           = (*ProcProtocolHandler)(nil)
	_ urls.Locator          = (*ProcProtocolHandler)(nil)
	_ urls.PromptDocer      = (*ProcProtocolHandler)(nil)
	_ urls.PromptReferencer = (*ProcProtocolHandler)(nil)
)

func (h *ProcProtocolHandler) Scheme() string { return "proc" }

func (h *ProcProtocolHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingDevice,
		Selectors: urls.SelectorsLines,
		Immutable: true,
		Write: &urls.WritePolicy{
			Via:     "handler",
			Payload: "verbatim",
			Scope:   "workspace",
			Cancels: isProcKill,
		},
	}
}

func (h *ProcProtocolHandler) PromptDoc(_ urls.SchemeHost) string {
	return strings.TrimSpace(procPromptDoc)
}

func (h *ProcProtocolHandler) PromptReference() string {
	return strings.TrimSpace(procReferenceDoc)
}

func procSessionID(c urls.Context) string {
	return sessionKeyFromContext(c.SDK)
}

// Locate returns the backing output log file path for a named service, or
// ok=false for jobs, agents, and listings.
// upstream: proc-protocol.ts:209
func (h *ProcProtocolHandler) Locate(c urls.Context, u urls.URL, opts urls.LocateOptions) (string, bool, error) {
	t, err := parseProcTarget(u)
	if err != nil {
		return "", false, err
	}
	if t.id == "" || t.action != "stdin" {
		return "", false, nil
	}
	sessionID := procSessionID(c)
	j, ok := jobs.Get(sessionID, t.id)
	if !ok || j.Kind() != jobs.KindService {
		return "", false, nil
	}
	sc, ok := j.(jobs.ServiceController)
	if !ok {
		return "", false, nil
	}
	logPath := sc.ServiceLogPath()
	if logPath == "" {
		return "", false, nil
	}
	if opts.Create {
		return logPath, true, nil
	}
	if _, statErr := os.Stat(logPath); statErr == nil {
		return logPath, true, nil
	}
	return "", false, nil
}

// Resolve serves `proc://`, `proc://<id>`, and `proc://<id>/streams[/<n>]`.
// upstream: proc-protocol.ts:221
func (h *ProcProtocolHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	t, err := parseProcTarget(u)
	if err != nil {
		return nil, err
	}
	sessionID := procSessionID(c)
	if t.action == "streams" {
		return h.resolveStreams(sessionID, u, t)
	}
	if t.action != "stdin" {
		id := t.id
		if id == "" {
			id = "<id>"
		}
		return nil, procWriteOnlyError(id, t.action)
	}

	all := jobs.List(sessionID)
	var bgJobs []jobs.Job
	var services []jobs.Job
	for _, j := range all {
		if j.Kind() == jobs.KindService {
			services = append(services, j)
		} else {
			bgJobs = append(bgJobs, j)
		}
	}

	now := time.Now()
	if t.id == "" {
		var rows []string
		jobSnaps := make([]map[string]any, 0, len(bgJobs))
		for _, j := range bgJobs {
			rows = append(rows, formatJobListRow(j, now))
			jobSnaps = append(jobSnaps, procJobSnapshotMap(sessionID, j, now, false))
		}
		svcSnaps := make([]map[string]any, 0, len(services))
		for _, s := range services {
			rows = append(rows, formatServiceStatusLine(s, now))
			svcSnaps = append(svcSnaps, procServiceSnapshotMap(s))
		}
		content := strings.Join(rows, "\n")
		if content == "" {
			content = "No background jobs or services."
		}
		return procTextResource(u, content, "", true, map[string]any{
			"jobs":    jobSnaps,
			"agents":  []any{},
			"daemons": svcSnaps,
		}), nil
	}

	j, ok := jobs.Get(sessionID, t.id)
	if ok && j.Kind() == jobs.KindService {
		statusLine := formatServiceStatusLine(j, now)
		ready := false
		mode := "session"
		var exitCode *int
		var sourcePath string
		if sc, isSvc := j.(jobs.ServiceController); isSvc {
			st := sc.ServiceState()
			ready = sc.ServiceReady() || st == "ready" || st == "running"
			mode = sc.ServiceMode()
			exitCode = sc.ServiceExitCode()
			if p := sc.ServiceLogPath(); p != "" {
				if _, statErr := os.Stat(p); statErr == nil {
					sourcePath = p
				}
			}
		}
		header := fmt.Sprintf("%s\nready=%v mode=%s", statusLine, ready, mode)
		if exitCode != nil {
			header += fmt.Sprintf(" exit=%d", *exitCode)
		}
		logs := j.Output()
		content := header
		if logs != "" {
			content += "\n" + logs
		}
		return procTextResource(u, content, sourcePath, false, map[string]any{
			"daemon": procServiceSnapshotMap(j),
			"log":    logs,
		}), nil
	}

	if ok {
		snap := procJobSnapshotMap(sessionID, j, now, false)
		intent := ""
		if d, hasDet := j.(jobs.JobDetailer); hasDet && d.Intent() != "" {
			intent = d.Intent() + " — "
		}
		lines := []string{
			fmt.Sprintf("%s [%s] — %s — %s%s", j.ID(), procJobType(j.Kind()), j.Status(), intent, collapseSingleLine(j.Label())),
		}
		if res := j.Result(); res != "" {
			lines = append(lines, res)
		}
		if d, hasDet := j.(jobs.JobDetailer); hasDet && d.ErrorText() != "" {
			lines = append(lines, "Error: "+d.ErrorText())
		}
		if j.Status() == "running" {
			if out := j.Output(); out != "" {
				lines = append(lines, out)
			}
			if j.Kind() == jobs.KindAgent {
				lines = append(lines, "agent://"+j.ID())
			}
		}
		return procTextResource(u, strings.Join(lines, "\n"), "", false, map[string]any{
			"job": snap,
			"log": strings.Join(lines[1:], "\n"),
		}), nil
	}

	if evicted, found := jobs.GetEvicted(sessionID, t.id); found {
		return procTextResource(u, jobs.DescribeEvicted(evicted, now), "", false, nil), nil
	}
	return nil, fmt.Errorf("Background job or service not found: %s", t.id)
}

func (h *ProcProtocolHandler) resolveStreams(sessionID string, u urls.URL, t procTarget) (*urls.Resource, error) {
	j, ok := jobs.Get(sessionID, t.id)
	if !ok {
		if evicted, found := jobs.GetEvicted(sessionID, t.id); found {
			return nil, fmt.Errorf("No streams kept: %s", jobs.DescribeEvicted(evicted, time.Now()))
		}
		return nil, fmt.Errorf("Background job not found: %s", t.id)
	}
	if j.Kind() == jobs.KindService {
		return nil, fmt.Errorf("%s is a service; it has no streams. Read proc://%s for its output.", t.id, t.id)
	}
	if j.Kind() == jobs.KindAgent {
		return nil, fmt.Errorf("%s is an agent; it has no streams. Read proc://%s for its output.", t.id, t.id)
	}
	if j.Kind() != jobs.KindBash {
		return nil, fmt.Errorf(
			"proc://%s has no streams: only bash jobs record their `|` stages and redirected files. Read proc://%s for its output.",
			t.id, t.id,
		)
	}

	sp, hasStreams := j.(jobs.StreamProvider)
	if !t.hasStream {
		if !hasStreams {
			return procTextResource(
				u,
				fmt.Sprintf("%s has piped nothing between `|` stages and redirected nothing into a file yet. Read proc://%s for its output.", t.id, t.id),
				"",
				true,
				nil,
			), nil
		}
		list := sp.ListStreams()
		if len(list) == 0 {
			return procTextResource(
				u,
				fmt.Sprintf("%s has piped nothing between `|` stages and redirected nothing into a file yet. Read proc://%s for its output.", t.id, t.id),
				"",
				true,
				nil,
			), nil
		}
		rows := make([]string, 0, len(list))
		for _, info := range list {
			producer := truncateRunes(collapseSingleLine(info.Producer), 80)
			consumer := info.Consumer
			if !info.File {
				consumer = truncateRunes(collapseSingleLine(consumer), 80)
			}
			state := "closed"
			if info.File {
				state = "file"
			} else if info.Open {
				state = "open"
			}
			rows = append(rows, fmt.Sprintf("proc://%s/streams/%d %s %s — %s → %s",
				t.id, info.ID, state, formatBytes(int(info.Total)), producer, consumer))
		}
		return procTextResource(u, strings.Join(rows, "\n"), "", true, nil), nil
	}

	if !hasStreams {
		return nil, fmt.Errorf("Stream not found: proc://%s/streams/%d. List them with proc://%s/streams.", t.id, t.stream, t.id)
	}
	chunk, found := sp.ReadStream(t.stream)
	if !found {
		return nil, fmt.Errorf("Stream not found: proc://%s/streams/%d. List them with proc://%s/streams.", t.id, t.stream, t.id)
	}
	text := strings.ReplaceAll(string(chunk.Data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if chunk.Start > 0 {
		text = fmt.Sprintf("[first %s no longer kept]\n%s", formatBytes(int(chunk.Start)), text)
	}
	return procTextResource(u, text, "", false, nil), nil
}

// Write serves `proc://<id>/kill`, `proc://<service>` (stdin), and
// `proc://<service>/mode`.
// upstream: proc-protocol.ts:313
func (h *ProcProtocolHandler) Write(c urls.Context, u urls.URL, content string) (*urls.WriteResult, error) {
	t, err := parseProcTarget(u)
	if err != nil {
		return nil, err
	}
	if t.action == "streams" {
		return nil, fmt.Errorf("proc://%s/streams is read-only", t.id)
	}
	if t.id == "" {
		return nil, fmt.Errorf("Write requires proc://<service> for stdin, proc://<id>/kill, or proc://<service>/mode; stdin and modes are services-only.")
	}

	sessionID := procSessionID(c)
	j, ok := jobs.Get(sessionID, t.id)
	isService := ok && j.Kind() == jobs.KindService

	if ok && !isService && t.action != "kill" {
		kindLabel := "job"
		if j.Kind() == jobs.KindAgent {
			kindLabel = "agent"
		}
		return nil, fmt.Errorf(
			"%s is an in-process %s; stdin and persist/session/detached modes are services-only. Start long-lived work as a named service when it needs stdin or lifetime controls. To cancel, %s.",
			t.id, kindLabel, procKillCall(t.id),
		)
	}

	now := time.Now()
	if t.action == "mode" {
		if !isService {
			return nil, fmt.Errorf("Service not found: %s", t.id)
		}
		mode := strings.TrimSpace(content)
		if mode != "persist" && mode != "session" && mode != "detached" {
			return nil, fmt.Errorf("Service mode must be persist, session, or detached")
		}
		sc, hasCtrl := j.(jobs.ServiceController)
		if !hasCtrl {
			return nil, fmt.Errorf("Service %s does not support mode changes", t.id)
		}
		if err := sc.SetServiceMode(mode); err != nil {
			return nil, err
		}
		return &urls.WriteResult{
			Text: fmt.Sprintf("%s; mode=%s", formatServiceStatusLine(j, time.Now()), mode),
			Details: map[string]any{
				"proc": map[string]any{
					"action": "mode",
					"daemon": procServiceSnapshotMap(j),
					"mode":   mode,
				},
				"touchesNoFiles": true,
			},
		}, nil
	}

	if t.action == "stdin" {
		if !isService {
			return nil, fmt.Errorf("Service not found: %s", t.id)
		}
		sc, hasCtrl := j.(jobs.ServiceController)
		if !hasCtrl {
			return nil, fmt.Errorf("Service %s does not accept stdin", t.id)
		}
		if err := sc.SendStdin(content); err != nil {
			return nil, err
		}
		return &urls.WriteResult{
			Text: fmt.Sprintf("Sent input to %s", formatServiceStatusLine(j, time.Now())),
			Details: map[string]any{
				"proc": map[string]any{
					"action": "stdin",
					"daemon": procServiceSnapshotMap(j),
					"input":  content,
				},
				"touchesNoFiles": true,
			},
		}, nil
	}

	// action == "kill"
	if isService {
		j.Cancel()
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		select {
		case <-j.Done():
		case <-waitCtx.Done():
		}
		return &urls.WriteResult{
			Text: fmt.Sprintf("Stopped %s", formatServiceStatusLine(j, time.Now())),
			Details: map[string]any{
				"proc": map[string]any{
					"action": "stop",
					"daemon": procServiceSnapshotMap(j),
				},
				"touchesNoFiles": true,
			},
		}, nil
	}

	if ok {
		status := j.Status()
		var outcomeStatus, outcomeMsg string
		if status == "running" {
			// Match omp (async/job-control.ts:549-568): for a task job, kill the
			// agent registration before cancelling the job so no queued message
			// restarts it, and include the agent kill message.
			var agentKillMsg string
			if j.Kind() == jobs.KindAgent {
				agentID := j.ID()
				if KillAgentHook != nil {
					if kStatus, kMsg := KillAgentHook(sessionID, agentID); kStatus == "cancelled" && kMsg != "" {
						agentKillMsg = kMsg
					}
				} else {
					agentKillMsg = fmt.Sprintf("Killed agent %s; its transcript stays at history://%s.", agentID, agentID)
				}
			}
			j.Cancel()
			outcomeStatus = "cancelled"
			if agentKillMsg != "" {
				outcomeMsg = fmt.Sprintf("Cancelled background job %s. %s", t.id, agentKillMsg)
			} else {
				outcomeMsg = fmt.Sprintf("Cancelled background job %s.", t.id)
			}
		} else {
			if j.Kind() == jobs.KindAgent && KillAgentHook != nil {
				if kStatus, kMsg := KillAgentHook(sessionID, j.ID()); kStatus == "cancelled" && kMsg != "" {
					outcomeStatus = "cancelled"
					outcomeMsg = kMsg
				}
			}
			if outcomeStatus == "" {
				outcomeStatus = "already_completed"
				outcomeMsg = fmt.Sprintf("Background job %s is already %s.", t.id, status)
			}
		}
		curStatus := j.Status()
		if outcomeStatus == "cancelled" && curStatus == "running" {
			// Wait briefly for j.Cancel() to transition the job status (e.g.
			// AgentJob whose Cancel only calls context.CancelFunc).
			select {
			case <-j.Done():
			case <-time.After(250 * time.Millisecond):
			}
			curStatus = j.Status()
			if curStatus == "running" {
				curStatus = "cancelled"
			}
		}
		// Match omp (`async/job-control.ts:387,397`, `async/job-manager.ts:1164-1168`):
		// build the kill response snapshot BEFORE waiting for the aborted run to
		// settle so the kill response itself omits resultText/errorText/meta/settledAtMs
		// (`task-delayed-cancel#2`, `bash-notify-streams-cancel#11`).
		snap := procJobSnapshotMap(sessionID, j, time.Now(), outcomeStatus == "cancelled")
		if outcomeStatus == "cancelled" {
			if snap["status"] == "running" {
				snap["status"] = "cancelled"
			}
			delete(snap, "resultText")
			delete(snap, "errorText")
			delete(snap, "meta")
		}
		// Match omp (`async/job-control.ts:387,397`, `async/job-manager.ts:1164-1168`):
		// On a freshly cancelled job (`outcomeStatus == "cancelled"`), omp calls
		// `consumeJobResult` synchronously before the aborted run promise resolves,
		// when both `resultText` and `errorText` are still undefined, so it returns
		// false and does NOT mark the job consumed. Only already-settled jobs with
		// a result or error are consumed here.
		if outcomeStatus != "cancelled" {
			hasResultOrErr := j.Result() != ""
			if !hasResultOrErr {
				if d, hasDet := j.(jobs.JobDetailer); hasDet && d.ErrorText() != "" {
					hasResultOrErr = true
				}
			}
			if curStatus != "running" && hasResultOrErr {
				jobs.MarkConsumed(sessionID, t.id)
			}
		}
		var lines []string
		lines = append(lines, "## Cancelled (1)", "", "- "+outcomeMsg)
		if curStatus != "running" {
			lines = append(lines, "", "## Completed (1)", "",
				fmt.Sprintf("### %s [%s] — %s", j.ID(), procJobType(j.Kind()), curStatus),
			)
			if d, hasDet := j.(jobs.JobDetailer); hasDet && d.Intent() != "" {
				lines = append(lines, "Intent: "+d.Intent())
			}
			lines = append(lines, "Label: "+collapseSingleLine(j.Label()))
			if curStatus != "cancelled" {
				if res := j.Result(); res != "" {
					lines = append(lines, "```", res, "```")
				}
				if d, hasDet := j.(jobs.JobDetailer); hasDet && d.ErrorText() != "" {
					lines = append(lines, "Error: "+d.ErrorText())
				}
			}
		}
		// Deterministically wait for the cancelled job's run to settle and record
		// its errorText BEFORE returning the kill response so no subsequent call
		// (e.g. an immediate `read proc://`) can ever observe the cancelled job
		// without its cancellation error text (matching omp where the aborted
		// run resolves on the microtask queue before the next tool call starts).
		// Bounded by termCleanupGrace as a backstop; if a non-bash job still has
		// not settled after the grace backstop, managedJob guarantees errorText
		// is populated via ensureCancelledErrorText.
		if outcomeStatus == "cancelled" {
			select {
			case <-j.Done():
			case <-time.After(termCleanupGrace):
				if mj, isManaged := j.(*managedJob); isManaged {
					mj.ensureCancelledErrorText()
				}
			}
		}
		return &urls.WriteResult{
			Text: strings.Join(lines, "\n"),
			Details: map[string]any{
				"proc": map[string]any{
					"op": "cancel",
					"meta": map[string]any{
						"source": map[string]any{
							"type":  "report",
							"value": "background jobs snapshot",
						},
					},
					"jobs": []map[string]any{snap},
					"cancelled": []map[string]any{
						{"id": t.id, "status": outcomeStatus},
					},
				},
				"touchesNoFiles": true,
			},
		}, nil
	}

	// No job row found: try killing a jobless agent registration (e.g. an idle
	// or parked keep-alive subagent whose job row was evicted).
	// upstream: async/job-control.ts:525-531
	if KillAgentHook != nil {
		if kStatus, kMsg := KillAgentHook(sessionID, t.id); kStatus == "cancelled" {
			return &urls.WriteResult{
				Text: strings.Join([]string{"## Cancelled (1)", "", "- " + kMsg}, "\n"),
				Details: map[string]any{
					"proc": map[string]any{
						"op": "cancel",
						"meta": map[string]any{
							"source": map[string]any{
								"type":  "report",
								"value": "background jobs snapshot",
							},
						},
						"jobs": []any{},
						"cancelled": []map[string]any{
							{"id": t.id, "status": kStatus},
						},
					},
					"touchesNoFiles": true,
				},
			}, nil
		}
	}

	if evicted, found := jobs.GetEvicted(sessionID, t.id); found {
		return &urls.WriteResult{
			Text: fmt.Sprintf("Nothing to cancel: %s", jobs.DescribeEvicted(evicted, now)),
			Details: map[string]any{
				"proc": map[string]any{
					"op":   "cancel",
					"jobs": []any{},
					"cancelled": []map[string]any{
						{"id": t.id, "status": "already_completed"},
					},
				},
				"touchesNoFiles": true,
			},
		}, nil
	}

	return nil, fmt.Errorf("Background job or service not found: %s", t.id)
}

func procTextResource(u urls.URL, content, sourcePath string, isDir bool, procDetails map[string]any) *urls.Resource {
	href := u.RawHref
	if href == "" {
		href = u.Href
	}
	res := &urls.Resource{
		URL:         href,
		Content:     content,
		ContentType: "text/plain",
		SourcePath:  sourcePath,
		IsDirectory: isDir,
	}
	if procDetails != nil {
		res.Details = map[string]any{"proc": procDetails}
	}
	return res
}

func procJobType(k jobs.Kind) string {
	switch k {
	case jobs.KindAgent:
		return "task"
	case jobs.KindBash:
		return "bash"
	case jobs.KindEval:
		return "eval"
	case jobs.KindService:
		return "service"
	default:
		return string(k)
	}
}

func formatJobListRow(j jobs.Job, now time.Time) string {
	var endTime time.Time
	var intent string
	if d, ok := j.(jobs.JobDetailer); ok {
		endTime = d.Ended()
		if d.Intent() != "" {
			intent = d.Intent() + " — "
		}
	}
	status := j.Status()
	finished := status != "running" && status != "queued" && status != "starting"
	var dur string
	if !endTime.IsZero() {
		dur = "in " + jobs.FormatDuration(endTime.Sub(j.Started()))
	} else if finished {
		dur = "in " + jobs.FormatDuration(now.Sub(j.Started()))
	} else {
		dur = "up " + jobs.FormatDuration(now.Sub(j.Started()))
	}
	return fmt.Sprintf("%s [%s] %s %s — %s%s",
		j.ID(), procJobType(j.Kind()), status, dur, intent, collapseSingleLine(j.Label()))
}

func formatServiceStatusLine(j jobs.Job, now time.Time) string {
	state := j.Status()
	pid := 0
	mode := "session"
	var endTime time.Time
	if sc, ok := j.(jobs.ServiceController); ok {
		state = sc.ServiceState()
		pid = sc.ServicePID()
		mode = sc.ServiceMode()
	}
	if d, ok := j.(jobs.JobDetailer); ok {
		endTime = d.Ended()
	}
	refTime := now
	if !endTime.IsZero() {
		refTime = endTime
	}
	age := jobs.FormatDuration(refTime.Sub(j.Started()))
	out := fmt.Sprintf("%s [service] — %s — up %s", j.ID(), state, age)
	if pid > 0 && state != "exited" && state != "failed" && state != "stopped" {
		out += fmt.Sprintf(" — pid %d", pid)
	}
	switch mode {
	case "persist":
		out += " — persistent"
	case "detached":
		out += " — detached"
	}
	return out
}

func procJobSnapshotMap(sessionID string, j jobs.Job, now time.Time, omitSettledAt bool) map[string]any {
	var endTime time.Time
	var intent, errText string
	var det map[string]any
	if d, ok := j.(jobs.JobDetailer); ok {
		endTime = d.Ended()
		intent = d.Intent()
		errText = d.ErrorText()
		det = d.Details()
	}
	ref := now
	if !endTime.IsZero() {
		ref = endTime
	}
	durMS := ref.Sub(j.Started()).Milliseconds()
	if durMS < 0 {
		durMS = 0
	}
	resultConsumed := sessionID != "" && jobs.IsConsumed(sessionID, j.ID())
	m := map[string]any{
		"id":         j.ID(),
		"type":       procJobType(j.Kind()),
		"status":     j.Status(),
		"label":      j.Label(),
		"durationMs": durMS,
	}
	if intent != "" {
		m["intent"] = intent
	}
	if !endTime.IsZero() && !omitSettledAt {
		m["settledAtMs"] = endTime.UnixMilli()
	}
	if wp, hasW := j.(jobs.WidgetProvider); hasW {
		if wv := wp.WidgetView(now); wv != nil {
			m["widget"] = jobs.WidgetViewToMap(wv)
		}
	} else if j.Kind() == jobs.KindBash || j.Kind() == jobs.KindAgent {
		displayLabel := j.Label()
		if intent != "" {
			displayLabel = intent
		}
		m["widget"] = jobs.WidgetViewToMap(jobs.BuildFallbackWidget(displayLabel, "", j.Started()))
	}
	if !resultConsumed {
		if res := j.Result(); res != "" {
			m["resultText"] = res
		}
		if errText != "" {
			m["errorText"] = errText
		}
		if det != nil {
			if meta, ok := det["meta"]; ok {
				m["meta"] = meta
			}
		}
	}
	if det != nil {
		if ec, ok := det["exitCode"]; ok {
			m["exitCode"] = ec
		}
		if j.Kind() == jobs.KindAgent {
			if rm, ok := det["resolvedModel"].(string); ok && strings.TrimSpace(rm) != "" {
				m["resolvedModel"] = strings.TrimSpace(rm)
			}
			if rmi, ok := det["resolvedModelIdentity"].(string); ok && strings.TrimSpace(rmi) != "" {
				m["resolvedModelIdentity"] = strings.TrimSpace(rmi)
			}
			if rtl, ok := det["resolvedThinkingLevel"].(string); ok && strings.TrimSpace(rtl) != "" {
				m["resolvedThinkingLevel"] = strings.TrimSpace(rtl)
			}
		}
	}
	if j.Kind() == jobs.KindAgent {
		m["agentUrlId"] = j.ID()
	}
	return m
}

func procServiceSnapshotMap(j jobs.Job) map[string]any {
	state := j.Status()
	pid := 0
	mode := "session"
	ready := false
	var exitCode *int
	var exitReason string
	var endTime time.Time
	if sc, ok := j.(jobs.ServiceController); ok {
		state = sc.ServiceState()
		pid = sc.ServicePID()
		mode = sc.ServiceMode()
		ready = sc.ServiceReady()
		exitCode = sc.ServiceExitCode()
		exitReason = sc.ServiceExitReason()
	}
	if d, ok := j.(jobs.JobDetailer); ok {
		endTime = d.Ended()
	}
	hold := "leases"
	switch mode {
	case "persist":
		hold = "persist"
	case "detached":
		hold = "detached"
	}
	pids := []int{}
	if pid > 0 && state != "exited" && state != "failed" && state != "stopped" {
		pids = []int{pid}
	}
	m := map[string]any{
		"id":        j.ID(),
		"name":      j.ID(),
		"kind":      "proc",
		"key":       j.ID(),
		"label":     j.Label(),
		"state":     state,
		"pids":      pids,
		"persist":   mode == "persist",
		"detached":  mode == "detached",
		"createdAt": j.Started().UnixMilli(),
		"startedAt": j.Started().UnixMilli(),
		"policy": map[string]any{
			"hold":    hold,
			"idleMs":  0,
			"onIdle":  "stop",
			"restart": "no",
			"survive": "exec",
		},
		"supervised": true,
	}
	if pid > 0 && state != "exited" && state != "failed" && state != "stopped" {
		m["pid"] = pid
	}
	if ready {
		m["readyAt"] = j.Started().UnixMilli()
	}
	if !endTime.IsZero() {
		m["exitedAt"] = endTime.UnixMilli()
	}
	if exitCode != nil {
		m["exitCode"] = *exitCode
	}
	if exitReason != "" {
		m["exitReason"] = exitReason
	}
	return m
}

func collapseSingleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
