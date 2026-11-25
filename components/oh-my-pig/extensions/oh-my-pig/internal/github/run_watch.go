package github

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	runWatchTailDefault     = 15
	runWatchTailMax         = 200
	runJobsPageSize         = 100
	runWatchMaxPollFailures = 5
	runWatchLogErrMaxBytes  = 512
)

// Overridable timings for deterministic fast tests.
var (
	runWatchIntervalDefault = 3 * time.Second
	runWatchIntervalSlow    = 15 * time.Second
	runWatchFastWindow      = 60 * time.Second
	runWatchNoRunsGiveUp    = 90 * time.Second
	runWatchGraceDefault    = 5 * time.Second
)

var (
	runURLPattern     = regexp.MustCompile(`^https://([^/]+)/([^/]+/[^/]+)/actions/runs/(\d+)(?:/.*)?$`)
	ghRateLimitErrRe  = regexp.MustCompile(`(?i)rate limit|HTTP 429|abuse detection`)
	unknownAllowEscRe = regexp.MustCompile(`^unknown flag: --allow-escape-sequences(?:\r?\n|$)`)
	runSuccessSet     = map[string]bool{"success": true, "neutral": true, "skipped": true}
	runFailureSet     = map[string]bool{"failure": true, "timed_out": true, "cancelled": true, "action_required": true, "startup_failure": true}
	jobFailureSet     = map[string]bool{"failure": true, "timed_out": true, "cancelled": true, "action_required": true}
)

type ghActionsStepAPI struct {
	Number      int    `json:"number"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
}

type ghActionsJobAPI struct {
	ID          *int                `json:"id"`
	Name        string              `json:"name"`
	Status      string              `json:"status"`
	Conclusion  string              `json:"conclusion"`
	StartedAt   string              `json:"started_at"`
	CompletedAt string              `json:"completed_at"`
	HTMLURL     string              `json:"html_url"`
	Steps       *[]ghActionsStepAPI `json:"steps"`
}

type ghActionsJobsResponse struct {
	TotalCount int               `json:"total_count"`
	Jobs       []ghActionsJobAPI `json:"jobs"`
}

type ghActionsRunAPI struct {
	ID           *int   `json:"id"`
	Name         string `json:"name"`
	DisplayTitle string `json:"display_title"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	HTMLURL      string `json:"html_url"`
}

type ghActionsRunListResponse struct {
	WorkflowRuns []ghActionsRunAPI `json:"workflow_runs"`
}

type ghRunStepSnapshot struct {
	Number      int
	Name        string
	Status      string
	Conclusion  string
	StartedAt   string
	CompletedAt string
}

type ghRunJobSnapshot struct {
	ID          int
	Name        string
	Status      string
	Conclusion  string
	StartedAt   string
	CompletedAt string
	URL         string
	HasSteps    bool
	Steps       []ghRunStepSnapshot
}

type ghRunSnapshot struct {
	ID           int
	WorkflowName string
	DisplayTitle string
	Status       string
	Conclusion   string
	Branch       string
	HeadSHA      string
	CreatedAt    string
	UpdatedAt    string
	URL          string
	Jobs         []ghRunJobSnapshot
}

type ghFailedJobLog struct {
	Run               ghRunSnapshot
	Job               ghRunJobSnapshot
	Full              string
	Tail              string
	Available         bool
	UnavailableReason string
	Truncated         bool
}

func resolveTailLimit(params map[string]any) (int, error) {
	raw, exists := params["tail"]
	if !exists || raw == nil {
		return runWatchTailDefault, nil
	}
	var val float64
	switch v := raw.(type) {
	case float64:
		val = v
	case int:
		val = float64(v)
	default:
		return 0, fmt.Errorf("tail must be a positive number")
	}
	if math.IsNaN(val) || math.IsInf(val, 0) || val <= 0 {
		return 0, fmt.Errorf("tail must be a positive number")
	}
	n := int(math.Floor(val))
	if n <= 0 {
		return 0, fmt.Errorf("tail must be a positive number")
	}
	return min(n, runWatchTailMax), nil
}

func parseRunReference(raw string) (repo string, runID int, hasRun bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, false, nil
	}
	if n, ok := ParsePositiveDecimalInt(raw); ok {
		return "", n, true, nil
	}
	m := runURLPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", 0, false, fmt.Errorf("run must be a numeric workflow run ID or a full GitHub Actions run URL")
	}
	n, _ := strconv.Atoi(m[3])
	return FormatRepoRef(m[1], m[2]), n, true, nil
}

func parseTimestampMs(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			ms := t.UnixMilli()
			if ms > 0 {
				return ms, true
			}
			return 0, false
		}
	}
	return 0, false
}

func normalizeGithubTimestamp(s string) string {
	s = strings.TrimSpace(s)
	if _, ok := parseTimestampMs(s); ok {
		return s
	}
	return ""
}

func durationSeconds(startedAt, completedAt string, observedAtMs int64) *int {
	startMs, ok := parseTimestampMs(startedAt)
	if !ok {
		return nil
	}
	endMs := observedAtMs
	if cMs, ok := parseTimestampMs(completedAt); ok {
		endMs = cMs
	}
	sec := max(0, int((endMs-startMs)/1000))
	return &sec
}

func normalizeRunJob(j ghActionsJobAPI) (ghRunJobSnapshot, bool) {
	if j.ID == nil {
		return ghRunJobSnapshot{}, false
	}
	name := strings.TrimSpace(j.Name)
	if name == "" {
		name = fmt.Sprintf("job-%d", *j.ID)
	}
	snap := ghRunJobSnapshot{
		ID:          *j.ID,
		Name:        name,
		Status:      strings.TrimSpace(j.Status),
		Conclusion:  strings.TrimSpace(j.Conclusion),
		StartedAt:   normalizeGithubTimestamp(j.StartedAt),
		CompletedAt: normalizeGithubTimestamp(j.CompletedAt),
		URL:         strings.TrimSpace(j.HTMLURL),
	}
	if j.Steps != nil {
		snap.HasSteps = true
		snap.Steps = make([]ghRunStepSnapshot, 0, len(*j.Steps))
		for _, st := range *j.Steps {
			if st.Number < 1 {
				continue
			}
			sn := strings.TrimSpace(st.Name)
			if sn == "" {
				sn = fmt.Sprintf("step-%d", st.Number)
			}
			snap.Steps = append(snap.Steps, ghRunStepSnapshot{
				Number:      st.Number,
				Name:        sn,
				Status:      strings.TrimSpace(st.Status),
				Conclusion:  strings.TrimSpace(st.Conclusion),
				StartedAt:   normalizeGithubTimestamp(st.StartedAt),
				CompletedAt: normalizeGithubTimestamp(st.CompletedAt),
			})
		}
	}
	return snap, true
}

func normalizeRunSnapshot(r ghActionsRunAPI, jobs []ghRunJobSnapshot) (ghRunSnapshot, error) {
	if r.ID == nil {
		return ghRunSnapshot{}, fmt.Errorf("GitHub Actions run response did not include a run ID.")
	}
	return ghRunSnapshot{
		ID:           *r.ID,
		WorkflowName: strings.TrimSpace(r.Name),
		DisplayTitle: strings.TrimSpace(r.DisplayTitle),
		Status:       strings.TrimSpace(r.Status),
		Conclusion:   strings.TrimSpace(r.Conclusion),
		Branch:       strings.TrimSpace(r.HeadBranch),
		HeadSHA:      strings.TrimSpace(r.HeadSHA),
		CreatedAt:    strings.TrimSpace(r.CreatedAt),
		UpdatedAt:    strings.TrimSpace(r.UpdatedAt),
		URL:          strings.TrimSpace(r.HTMLURL),
		Jobs:         jobs,
	}, nil
}

func isFailedJob(j ghRunJobSnapshot) bool {
	return j.Conclusion != "" && jobFailureSet[j.Conclusion]
}

func getRunOutcome(conclusion string) string {
	if runSuccessSet[conclusion] {
		return "success"
	}
	if runFailureSet[conclusion] {
		return "failure"
	}
	return "pending"
}

func getRunSnapshotOutcome(r ghRunSnapshot) string {
	if r.Status != "completed" {
		return "pending"
	}
	return getRunOutcome(r.Conclusion)
}

func getRunCollectionOutcome(runs []ghRunSnapshot) string {
	if len(runs) == 0 {
		return "pending"
	}
	pending := false
	for _, r := range runs {
		for _, j := range r.Jobs {
			if isFailedJob(j) {
				return "failure"
			}
		}
		switch getRunSnapshotOutcome(r) {
		case "failure":
			return "failure"
		case "pending":
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "success"
}

func getRunCollectionSignature(runs []ghRunSnapshot) string {
	ids := make([]int, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	slices.Sort(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func formatJobState(j ghRunJobSnapshot) string {
	if j.Conclusion != "" {
		return j.Conclusion
	}
	if j.Status != "" {
		return j.Status
	}
	return "unknown"
}

func renderJobsSection(jobs []ghRunJobSnapshot) []string {
	if len(jobs) == 0 {
		return []string{"## Jobs", "", "No jobs reported yet."}
	}
	lines := []string{fmt.Sprintf("## Jobs (%d)", len(jobs)), ""}
	for _, j := range jobs {
		lines = append(lines, fmt.Sprintf("- [%s] %s", formatJobState(j), j.Name))
		for _, st := range j.Steps {
			if st.Conclusion != "" && runFailureSet[st.Conclusion] {
				pushLine(&lines, "  Failed step", fmt.Sprintf("#%d %s", st.Number, sanitizeText(st.Name)))
				break
			}
		}
		pushLine(&lines, "  Started", j.StartedAt)
		pushLine(&lines, "  Completed", j.CompletedAt)
		pushLine(&lines, "  URL", j.URL)
	}
	return lines
}

func renderRunSection(r ghRunSnapshot) []string {
	label := fmt.Sprintf("### Run #%d", r.ID)
	if r.WorkflowName != "" {
		label = fmt.Sprintf("### Run #%d - %s", r.ID, r.WorkflowName)
	}
	lines := []string{label, ""}
	pushLine(&lines, "Title", r.DisplayTitle)
	pushLine(&lines, "Branch", r.Branch)
	pushLine(&lines, "Commit", formatShortSHA(r.HeadSHA))
	pushLine(&lines, "Status", r.Status)
	pushLine(&lines, "Conclusion", r.Conclusion)
	pushLine(&lines, "Created", r.CreatedAt)
	pushLine(&lines, "Updated", r.UpdatedAt)
	pushLine(&lines, "URL", r.URL)
	lines = append(lines, "")
	lines = append(lines, renderJobsSection(r.Jobs)...)
	return lines
}

func renderFailedJobLogs(logs []ghFailedJobLog, tail int, full bool) []string {
	if len(logs) == 0 {
		return nil
	}
	lines := []string{"## Failed Jobs", ""}
	for _, entry := range logs {
		conc := entry.Job.Conclusion
		if conc == "" {
			conc = "failed"
		}
		lines = append(lines, fmt.Sprintf("### %s [%s]", entry.Job.Name, conc))
		pushLine(&lines, "Run", fmt.Sprintf("#%d", entry.Run.ID))
		pushLine(&lines, "Workflow", entry.Run.WorkflowName)
		pushLine(&lines, "Started", entry.Job.StartedAt)
		pushLine(&lines, "Completed", entry.Job.CompletedAt)
		pushLine(&lines, "URL", entry.Job.URL)
		lines = append(lines, "")
		logText := entry.Tail
		if full {
			logText = entry.Full
		}
		if entry.Available && logText != "" {
			if full {
				if entry.Truncated {
					lines = append(lines, "Partial log (capture truncated):")
				} else {
					lines = append(lines, "Full log:")
				}
			} else {
				if entry.Truncated {
					lines = append(lines, fmt.Sprintf("Last %d captured log lines (log capture truncated):", tail))
				} else {
					lines = append(lines, fmt.Sprintf("Last %d log lines:", tail))
				}
			}
			lines = append(lines, "```text", logText, "```")
		} else {
			if full {
				lines = append(lines, "Full log unavailable.")
			} else {
				lines = append(lines, "Log tail unavailable.")
			}
			pushLine(&lines, "Reason", entry.UnavailableReason)
		}
		lines = append(lines, "")
	}
	return lines
}

// formatRunWatchSnapshot matches tools/gh-run-watch.ts:317-352.
func formatRunWatchSnapshot(repo string, r ghRunSnapshot, pollCount int, note string) string {
	failedCount := 0
	for _, j := range r.Jobs {
		if isFailedJob(j) {
			failedCount++
		}
	}
	lines := []string{fmt.Sprintf("# Watching GitHub Actions Run #%d", r.ID), ""}
	pushLine(&lines, "Repository", repo)
	pushLine(&lines, "Workflow", r.WorkflowName)
	pushLine(&lines, "Title", r.DisplayTitle)
	pushLine(&lines, "Branch", r.Branch)
	pushLine(&lines, "Status", r.Status)
	pushLine(&lines, "Conclusion", r.Conclusion)
	pushLine(&lines, "Created", r.CreatedAt)
	pushLine(&lines, "Updated", r.UpdatedAt)
	pushLine(&lines, "URL", r.URL)
	pushLineInt(&lines, "Poll", &pollCount)
	if failedCount > 0 {
		pushLineInt(&lines, "Failed jobs", &failedCount)
	}
	if note != "" {
		lines = append(lines, "", "Note: "+note)
	}
	lines = append(lines, "")
	lines = append(lines, renderJobsSection(r.Jobs)...)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// formatCommitRunWatchSnapshot matches tools/gh-run-watch.ts:392-428.
func formatCommitRunWatchSnapshot(repo, headSHA, branch string, runs []ghRunSnapshot, pollCount int, note string) string {
	failedCount := 0
	completedRuns := 0
	for _, r := range runs {
		if r.Status == "completed" {
			completedRuns++
		}
		for _, j := range r.Jobs {
			if isFailedJob(j) {
				failedCount++
			}
		}
	}
	short := formatShortSHA(headSHA)
	if short == "" {
		short = headSHA
	}
	runCount := len(runs)
	lines := []string{fmt.Sprintf("# Watching GitHub Actions for %s", short), ""}
	pushLine(&lines, "Repository", repo)
	pushLine(&lines, "Branch", branch)
	pushLine(&lines, "Commit", headSHA)
	pushLineInt(&lines, "Poll", &pollCount)
	pushLineInt(&lines, "Runs", &runCount)
	pushLine(&lines, "Completed runs", fmt.Sprintf("%d/%d", completedRuns, runCount))
	if failedCount > 0 {
		pushLineInt(&lines, "Failed jobs", &failedCount)
	}
	if note != "" {
		lines = append(lines, "", "Note: "+note)
	}
	if len(runs) == 0 {
		lines = append(lines, "", "Waiting for workflow runs for this commit.")
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	for _, r := range runs {
		lines = append(lines, "")
		lines = append(lines, renderRunSection(r)...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildRunWatchRunDetails(r ghRunSnapshot, observedAtMs int64) GhRunWatchRunDetails {
	jobs := make([]GhRunWatchJobDetails, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		jd := GhRunWatchJobDetails{
			ID:              j.ID,
			Name:            j.Name,
			Status:          j.Status,
			Conclusion:      j.Conclusion,
			DurationSeconds: durationSeconds(j.StartedAt, j.CompletedAt, observedAtMs),
			URL:             j.URL,
		}
		if j.HasSteps {
			steps := make([]GhRunWatchStepDetails, 0, len(j.Steps))
			for _, st := range j.Steps {
				steps = append(steps, GhRunWatchStepDetails{
					Number:          st.Number,
					Name:            st.Name,
					Status:          st.Status,
					Conclusion:      st.Conclusion,
					DurationSeconds: durationSeconds(st.StartedAt, st.CompletedAt, observedAtMs),
				})
			}
			jd.Steps = &steps
		}
		jobs = append(jobs, jd)
	}
	return GhRunWatchRunDetails{
		ID:           r.ID,
		WorkflowName: r.WorkflowName,
		DisplayTitle: r.DisplayTitle,
		Status:       r.Status,
		Conclusion:   r.Conclusion,
		Branch:       r.Branch,
		HeadSHA:      r.HeadSHA,
		URL:          r.URL,
		Jobs:         jobs,
	}
}

func buildFailedLogDetails(logs []ghFailedJobLog) []GhRunWatchFailedLogDetails {
	out := make([]GhRunWatchFailedLogDetails, 0, len(logs))
	for _, e := range logs {
		out = append(out, GhRunWatchFailedLogDetails{
			RunID:             e.Run.ID,
			WorkflowName:      e.Run.WorkflowName,
			JobName:           e.Job.Name,
			Conclusion:        e.Job.Conclusion,
			Tail:              e.Tail,
			Available:         e.Available,
			UnavailableReason: e.UnavailableReason,
		})
	}
	return out
}

func buildRunWatchDetails(repo string, r ghRunSnapshot, state string, pollCount *int, note string, logs []ghFailedJobLog) GhToolDetails {
	nowMs := time.Now().UnixMilli()
	failedNames := make([]string, 0)
	for _, j := range r.Jobs {
		if isFailedJob(j) {
			failedNames = append(failedNames, j.Name)
		}
	}
	runIDs := []int{r.ID}
	rd := buildRunWatchRunDetails(r, nowMs)
	return GhToolDetails{
		Meta:       metaWithSourceURL(r.URL),
		Repo:       repo,
		Branch:     r.Branch,
		HeadSHA:    r.HeadSHA,
		RunID:      r.ID,
		RunIDs:     &runIDs,
		Status:     r.Status,
		Conclusion: r.Conclusion,
		FailedJobs: &failedNames,
		Watch: &GhRunWatchViewDetails{
			Mode:       "run",
			State:      state,
			Repo:       repo,
			Branch:     r.Branch,
			HeadSHA:    r.HeadSHA,
			PollCount:  pollCount,
			Note:       note,
			Run:        &rd,
			FailedLogs: buildFailedLogDetails(logs),
		},
	}
}

func buildCommitRunWatchDetails(repo, headSHA, branch string, runs []ghRunSnapshot, state string, pollCount *int, note string, logs []ghFailedJobLog) GhToolDetails {
	nowMs := time.Now().UnixMilli()
	runIDs := make([]int, 0, len(runs))
	runDetails := make([]GhRunWatchRunDetails, 0, len(runs))
	failedNames := make([]string, 0)
	allCompleted := len(runs) > 0
	for _, r := range runs {
		runIDs = append(runIDs, r.ID)
		runDetails = append(runDetails, buildRunWatchRunDetails(r, nowMs))
		if r.Status != "completed" {
			allCompleted = false
		}
		for _, j := range r.Jobs {
			if isFailedJob(j) {
				wf := r.WorkflowName
				if wf == "" {
					wf = fmt.Sprintf("run %d", r.ID)
				}
				failedNames = append(failedNames, fmt.Sprintf("%s: %s", wf, j.Name))
			}
		}
	}
	status := "in_progress"
	if allCompleted {
		status = "completed"
	}
	return GhToolDetails{
		Repo:       repo,
		Branch:     branch,
		HeadSHA:    headSHA,
		RunIDs:     &runIDs,
		Status:     status,
		Conclusion: getRunCollectionOutcome(runs),
		FailedJobs: &failedNames,
		Watch: &GhRunWatchViewDetails{
			Mode:       "commit",
			State:      state,
			Repo:       repo,
			Branch:     branch,
			HeadSHA:    headSHA,
			PollCount:  pollCount,
			Note:       note,
			Runs:       &runDetails,
			FailedLogs: buildFailedLogDetails(logs),
		},
	}
}

func formatRunWatchResult(repo string, r ghRunSnapshot, logs []ghFailedJobLog, tail int, full bool) string {
	lines := []string{fmt.Sprintf("# GitHub Actions Run #%d", r.ID), ""}
	pushLine(&lines, "Repository", repo)
	pushLine(&lines, "Workflow", r.WorkflowName)
	pushLine(&lines, "Title", r.DisplayTitle)
	pushLine(&lines, "Branch", r.Branch)
	pushLine(&lines, "Status", r.Status)
	pushLine(&lines, "Conclusion", r.Conclusion)
	pushLine(&lines, "Created", r.CreatedAt)
	pushLine(&lines, "Updated", r.UpdatedAt)
	pushLine(&lines, "URL", r.URL)
	lines = append(lines, "")
	lines = append(lines, renderJobsSection(r.Jobs)...)

	hasFailed := false
	for _, j := range r.Jobs {
		if isFailedJob(j) {
			hasFailed = true
			break
		}
	}
	if hasFailed {
		lines = append(lines, "")
		lines = append(lines, renderFailedJobLogs(logs, tail, full)...)
		lines = append(lines, "Run failed.")
	} else if getRunOutcome(r.Conclusion) == "success" {
		lines = append(lines, "", "All jobs passed.")
	} else {
		lines = append(lines, "", "Run completed without successful jobs, but no failed job logs were available.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatCommitRunWatchResult(repo, headSHA, branch string, runs []ghRunSnapshot, logs []ghFailedJobLog, tail int, full bool) string {
	short := formatShortSHA(headSHA)
	if short == "" {
		short = headSHA
	}
	count := len(runs)
	lines := []string{fmt.Sprintf("# GitHub Actions for %s", short), ""}
	pushLine(&lines, "Repository", repo)
	pushLine(&lines, "Branch", branch)
	pushLine(&lines, "Commit", headSHA)
	pushLineInt(&lines, "Runs", &count)
	for _, r := range runs {
		lines = append(lines, "")
		lines = append(lines, renderRunSection(r)...)
	}
	if len(logs) > 0 {
		lines = append(lines, "")
		lines = append(lines, renderFailedJobLogs(logs, tail, full)...)
		lines = append(lines, "Workflow runs for this commit failed.")
	} else if getRunCollectionOutcome(runs) == "success" {
		lines = append(lines, "", "All workflow runs for this commit passed.")
	} else {
		lines = append(lines, "", "Workflow runs for this commit did not complete successfully.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func fetchRunJobs(ctx context.Context, cwd, repo string, runID int) ([]ghRunJobSnapshot, error) {
	ref := ParseRepoRef(repo)
	var jobs []ghRunJobSnapshot
	for page := 1; ; page++ {
		args := append([]string{"api"}, ghAPIHostArgs(ref)...)
		args = append(args,
			"--method", "GET",
			fmt.Sprintf("/repos/%s/actions/runs/%d/jobs", ref.Slug, runID),
			"-F", fmt.Sprintf("per_page=%d", runJobsPageSize),
			"-F", fmt.Sprintf("page=%d", page),
		)
		var resp ghActionsJobsResponse
		if err := ghJSON(ctx, cwd, args, &resp, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
			return nil, err
		}
		for _, j := range resp.Jobs {
			if norm, ok := normalizeRunJob(j); ok {
				jobs = append(jobs, norm)
			}
		}
		if len(resp.Jobs) < runJobsPageSize || resp.TotalCount <= len(jobs) {
			break
		}
	}
	return jobs, nil
}

func fetchRunSnapshot(ctx context.Context, cwd, repo string, runID int) (ghRunSnapshot, error) {
	ref := ParseRepoRef(repo)
	args := append([]string{"api"}, ghAPIHostArgs(ref)...)
	args = append(args, "--method", "GET", fmt.Sprintf("/repos/%s/actions/runs/%d", ref.Slug, runID))
	var run ghActionsRunAPI
	if err := ghJSON(ctx, cwd, args, &run, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
		return ghRunSnapshot{}, err
	}
	jobs, err := fetchRunJobs(ctx, cwd, repo, runID)
	if err != nil {
		return ghRunSnapshot{}, err
	}
	return normalizeRunSnapshot(run, jobs)
}

func fetchRunsForCommit(ctx context.Context, cwd, repo, headSHA string, completedCache map[int][]ghRunJobSnapshot) ([]ghRunSnapshot, error) {
	ref := ParseRepoRef(repo)
	args := append([]string{"api"}, ghAPIHostArgs(ref)...)
	args = append(args,
		"--method", "GET",
		fmt.Sprintf("/repos/%s/actions/runs", ref.Slug),
		"-F", "head_sha="+headSHA,
		"-F", "per_page=100",
	)
	var resp ghActionsRunListResponse
	if err := ghJSON(ctx, cwd, args, &resp, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
		return nil, err
	}
	var out []ghRunSnapshot
	for _, r := range resp.WorkflowRuns {
		if r.ID == nil {
			continue
		}
		completed := r.Status == "completed"
		if !completed {
			delete(completedCache, *r.ID)
		}
		jobs, ok := completedCache[*r.ID]
		if !completed || !ok {
			var err error
			jobs, err = fetchRunJobs(ctx, cwd, repo, *r.ID)
			if err != nil {
				return nil, err
			}
			if completed {
				completedCache[*r.ID] = jobs
			}
		}
		snap, err := normalizeRunSnapshot(r, jobs)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, nil
}

func tailLogLines(logText string, tail int) string {
	norm := normalizeBlock(logText)
	if norm == "" {
		return ""
	}
	lines := strings.Split(norm, "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), " \t\n\r")
}

// fetchFailedJobLogs propagates transport/abort/install errors from ghRun
// rather than masking them as unavailableReason.
// upstream: tools/gh-run-watch.ts:704-741
func fetchFailedJobLogs(ctx context.Context, cwd, repo string, failed []struct {
	run ghRunSnapshot
	job ghRunJobSnapshot
}, tail int) ([]ghFailedJobLog, error) {
	ref := ParseRepoRef(repo)
	out := make([]ghFailedJobLog, 0, len(failed))
	for _, entry := range failed {
		baseArgs := append([]string{"api"}, ghAPIHostArgs(ref)...)
		baseArgs = append(baseArgs, fmt.Sprintf("/repos/%s/actions/jobs/%d/logs", ref.Slug, entry.job.ID))
		argsWithEsc := append(append([]string(nil), baseArgs...), "--allow-escape-sequences")
		res, err := ghRun(ctx, cwd, argsWithEsc, ghCommandOptions{trimOutput: false})
		if err != nil {
			return nil, err
		}
		if res.exitCode != 0 && unknownAllowEscRe.MatchString(res.stderr) {
			res, err = ghRun(ctx, cwd, baseArgs, ghCommandOptions{trimOutput: false})
			if err != nil {
				return nil, err
			}
		}
		var fullLog, logTail, reason string
		if res.exitCode == 0 {
			fullLog = normalizeBlock(res.stdout)
			if fullLog != "" {
				logTail = tailLogLines(fullLog, tail)
			} else {
				reason = "gh returned empty log content (exit code 0)."
			}
		} else {
			stderr := normalizeText(res.stderr)
			if stderr != "" {
				reason = fmt.Sprintf("gh failed with exit code %d: %s", res.exitCode, stderr)
			} else {
				reason = fmt.Sprintf("gh failed with exit code %d.", res.exitCode)
			}
		}
		if reason != "" {
			reason = truncateLineBytes(reason, runWatchLogErrMaxBytes)
		}
		out = append(out, ghFailedJobLog{
			Run:               entry.run,
			Job:               entry.job,
			Full:              fullLog,
			Tail:              logTail,
			Available:         fullLog != "",
			UnavailableReason: reason,
			Truncated:         res.stdoutTruncated,
		})
	}
	return out, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func emitUpdate(sdkCtx sdk.Context, text string, details GhToolDetails) {
	var zero sdk.Context
	if sdkCtx == zero {
		return
	}
	_ = sdkCtx.OnUpdate(sdk.ToolResult{
		Content: text,
		Details: details,
	})
}

func executeRunWatch(ctx context.Context, sdkCtx sdk.Context, cwd string, params map[string]any) (sdk.ToolResult, error) {
	branchInput := optString(params, "branch")
	explicitRepo := optString(params, "repo")
	if err := validateRepoParam(sessionIDFromCtx(ctx), explicitRepo); err != nil {
		return sdk.ToolResult{}, err
	}
	runRepo, runID, hasRunID, err := parseRunReference(optString(params, "run"))
	if err != nil {
		return sdk.ToolResult{}, err
	}
	if err := validateRepoParam(sessionIDFromCtx(ctx), runRepo); err != nil {
		return sdk.ToolResult{}, err
	}
	repo, err := resolveGitHubRepo(ctx, cwd, explicitRepo, runRepo)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	tail, err := resolveTailLimit(params)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	startTime := time.Now()
	currentInterval := func() time.Duration {
		if time.Since(startTime) < runWatchFastWindow {
			return runWatchIntervalDefault
		}
		return runWatchIntervalSlow
	}
	consecutiveFailures := 0
	handlePollErr := func(perr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		consecutiveFailures++
		if !ghRateLimitErrRe.MatchString(perr.Error()) || consecutiveFailures > runWatchMaxPollFailures {
			return perr
		}
		return sleepCtx(ctx, runWatchIntervalSlow)
	}

	if hasRunID {
		for pollCount := 1; ; pollCount++ {
			if err := ctx.Err(); err != nil {
				return sdk.ToolResult{}, err
			}
			run, err := fetchRunSnapshot(ctx, cwd, repo, runID)
			if err != nil {
				if herr := handlePollErr(err); herr != nil {
					return sdk.ToolResult{}, herr
				}
				continue
			}
			consecutiveFailures = 0

			pc := pollCount
			emitUpdate(sdkCtx, formatRunWatchSnapshot(repo, run, pollCount, ""), buildRunWatchDetails(repo, run, "watching", &pc, "", nil))

			var failedJobs []ghRunJobSnapshot
			for _, j := range run.Jobs {
				if isFailedJob(j) {
					failedJobs = append(failedJobs, j)
				}
			}
			if len(failedJobs) > 0 {
				if run.Status != "completed" && runWatchGraceDefault > 0 {
					graceSec := int(runWatchGraceDefault.Seconds())
					note := fmt.Sprintf("Failure detected. Waiting %ds to capture concurrent failures before fetching logs.", graceSec)
					emitUpdate(sdkCtx, formatRunWatchSnapshot(repo, run, pollCount, note), buildRunWatchDetails(repo, run, "watching", &pc, note, nil))
					if err := sleepCtx(ctx, runWatchGraceDefault); err != nil {
						return sdk.ToolResult{}, err
					}
					refetched, rerr := fetchRunSnapshot(ctx, cwd, repo, runID)
					if rerr != nil {
						if ctx.Err() != nil {
							return sdk.ToolResult{}, ctx.Err()
						}
					} else {
						var rf []ghRunJobSnapshot
						for _, j := range refetched.Jobs {
							if isFailedJob(j) {
								rf = append(rf, j)
							}
						}
						if len(rf) > 0 {
							run = refetched
							failedJobs = rf
						}
					}
				}
				var pairs []struct {
					run ghRunSnapshot
					job ghRunJobSnapshot
				}
				for _, j := range failedJobs {
					pairs = append(pairs, struct {
						run ghRunSnapshot
						job ghRunJobSnapshot
					}{run: run, job: j})
				}
				logs, lerr := fetchFailedJobLogs(ctx, cwd, repo, pairs, tail)
				if lerr != nil {
					return sdk.ToolResult{}, lerr
				}
				return sdk.ToolResult{
					Content: formatRunWatchResult(repo, run, logs, tail, false),
					Details: buildRunWatchDetails(repo, run, "completed", nil, "", logs),
				}, nil
			}
			if run.Status == "completed" {
				return sdk.ToolResult{
					Content: formatRunWatchResult(repo, run, nil, tail, false),
					Details: buildRunWatchDetails(repo, run, "completed", nil, "", nil),
				}, nil
			}
			if err := sleepCtx(ctx, currentInterval()); err != nil {
				return sdk.ToolResult{}, err
			}
		}
	}

	var branch, headSHA string
	if branchInput != "" {
		branch = branchInput
		ref := ParseRepoRef(repo)
		args := append([]string{"api"}, ghAPIHostArgs(ref)...)
		args = append(args, "--method", "GET", fmt.Sprintf("/repos/%s/branches/%s", ref.Slug, encodeURIComponent(branch)))
		var resp struct {
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		if err := ghJSON(ctx, cwd, args, &resp, ghCommandOptions{repoProvided: true, trimOutput: true}); err != nil {
			return sdk.ToolResult{}, err
		}
		if strings.TrimSpace(resp.Commit.SHA) == "" {
			return sdk.ToolResult{}, fmt.Errorf("head SHA for branch %s must not be empty", branch)
		}
		headSHA = strings.TrimSpace(resp.Commit.SHA)
	} else {
		cwdRepo := tryResolveCurrentRepoFresh(ctx, cwd)
		if !githubRepoSlugEquals(cwdRepo, repo) {
			if cwdRepo == "" {
				cwdRepo = "not a GitHub repository"
			}
			return sdk.ToolResult{}, fmt.Errorf("Cannot infer the watched commit for %s: current checkout is %s. Pass `branch` or `run` to scope the watch.", repo, cwdRepo)
		}
		branch, err = requireCurrentGitBranch(ctx, cwd)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		headSHA, err = requireCurrentGitHead(ctx, cwd)
		if err != nil {
			return sdk.ToolResult{}, err
		}
	}

	completedCache := make(map[int][]ghRunJobSnapshot)
	everSawRuns := false
	settledSig := ""
	for pollCount := 1; ; pollCount++ {
		if err := ctx.Err(); err != nil {
			return sdk.ToolResult{}, err
		}
		runs, err := fetchRunsForCommit(ctx, cwd, repo, headSHA, completedCache)
		if err != nil {
			if herr := handlePollErr(err); herr != nil {
				return sdk.ToolResult{}, herr
			}
			continue
		}
		consecutiveFailures = 0
		if len(runs) > 0 {
			everSawRuns = true
		}

		pc := pollCount
		emitUpdate(sdkCtx, formatCommitRunWatchSnapshot(repo, headSHA, branch, runs, pollCount, ""), buildCommitRunWatchDetails(repo, headSHA, branch, runs, "watching", &pc, "", nil))

		outcome := getRunCollectionOutcome(runs)
		if outcome == "failure" {
			var pairs []struct {
				run ghRunSnapshot
				job ghRunJobSnapshot
			}
			for _, r := range runs {
				for _, j := range r.Jobs {
					if isFailedJob(j) {
						pairs = append(pairs, struct {
							run ghRunSnapshot
							job ghRunJobSnapshot
						}{run: r, job: j})
					}
				}
			}
			if runWatchGraceDefault > 0 {
				graceSec := int(runWatchGraceDefault.Seconds())
				note := fmt.Sprintf("Failure detected. Waiting %ds to capture concurrent failures before fetching logs.", graceSec)
				emitUpdate(sdkCtx, formatCommitRunWatchSnapshot(repo, headSHA, branch, runs, pollCount, note), buildCommitRunWatchDetails(repo, headSHA, branch, runs, "watching", &pc, note, nil))
				if err := sleepCtx(ctx, runWatchGraceDefault); err != nil {
					return sdk.ToolResult{}, err
				}
				refetched, rerr := fetchRunsForCommit(ctx, cwd, repo, headSHA, completedCache)
				if rerr != nil {
					if ctx.Err() != nil {
						return sdk.ToolResult{}, ctx.Err()
					}
				} else {
					var rp []struct {
						run ghRunSnapshot
						job ghRunJobSnapshot
					}
					for _, r := range refetched {
						for _, j := range r.Jobs {
							if isFailedJob(j) {
								rp = append(rp, struct {
									run ghRunSnapshot
									job ghRunJobSnapshot
								}{run: r, job: j})
							}
						}
					}
					if len(rp) > 0 {
						runs = refetched
						pairs = rp
					}
				}
			}
			logs, lerr := fetchFailedJobLogs(ctx, cwd, repo, pairs, tail)
			if lerr != nil {
				return sdk.ToolResult{}, lerr
			}
			return sdk.ToolResult{
				Content: formatCommitRunWatchResult(repo, headSHA, branch, runs, logs, tail, false),
				Details: buildCommitRunWatchDetails(repo, headSHA, branch, runs, "completed", nil, "", logs),
			}, nil
		}

		if outcome == "success" {
			sig := getRunCollectionSignature(runs)
			if sig == settledSig {
				return sdk.ToolResult{
					Content: formatCommitRunWatchResult(repo, headSHA, branch, runs, nil, tail, false),
					Details: buildCommitRunWatchDetails(repo, headSHA, branch, runs, "completed", nil, "", nil),
				}, nil
			}
			settledSig = sig
			confirmDur := currentInterval()
			confirmSec := int(confirmDur.Seconds())
			note := fmt.Sprintf("All known workflow runs completed successfully. Waiting %ds to ensure no additional runs appear for this commit.", confirmSec)
			emitUpdate(sdkCtx, formatCommitRunWatchSnapshot(repo, headSHA, branch, runs, pollCount, note), buildCommitRunWatchDetails(repo, headSHA, branch, runs, "watching", &pc, note, nil))
			if err := sleepCtx(ctx, confirmDur); err != nil {
				return sdk.ToolResult{}, err
			}
			continue
		}

		settledSig = ""
		if !everSawRuns && time.Since(startTime) >= runWatchNoRunsGiveUp {
			elapsed := int(math.Round(time.Since(startTime).Seconds()))
			short := formatShortSHA(headSHA)
			if short == "" {
				short = headSHA
			}
			return sdk.ToolResult{
				Content: fmt.Sprintf("No workflow runs found for %s@%s after %ds (%d polls). The commit may not trigger any GitHub Actions workflows, or Actions may be disabled for this repository. Pass `run` to watch a specific run.", repo, short, elapsed, pollCount),
				Details: buildCommitRunWatchDetails(repo, headSHA, branch, runs, "completed", &pc, "", nil),
			}, nil
		}
		if err := sleepCtx(ctx, currentInterval()); err != nil {
			return sdk.ToolResult{}, err
		}
	}
}
