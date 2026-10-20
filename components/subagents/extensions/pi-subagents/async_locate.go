package pi_subagents

// Finding a run: the id, prefix or directory a model passes, resolved to a run directory and its result. Ports the location
// half of async-resume.ts and run-id-resolver.ts (foreground and async kinds; nested runs are not ported).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AsyncRunLocation is where a run's files are; either may be absent.
type AsyncRunLocation struct {
	AsyncDir   string // "" when the run directory is gone
	ResultPath string // "" when there is no result file
	ResolvedID string
}

func assertRunID(value, field string) (string, error) {
	if value == "" {
		return "", nil
	}
	if jsTrim(value) == "" {
		return "", fmt.Errorf("%s must not be empty.", field)
	}
	if filepath.IsAbs(value) || strings.ContainsAny(value, `\/`) || strings.Contains(value, "..") {
		return "", fmt.Errorf("%s must be an async run id or prefix, not a path.", field)
	}
	return value, nil
}

func assertInsideRoot(root, target, label string) error {
	r, _ := filepath.Abs(root)
	t, _ := filepath.Abs(target)
	rel, err := filepath.Rel(r, t)
	if err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))) {
		return nil
	}
	return fmt.Errorf("%s must be inside %s.", label, r)
}

func prefixedRunIDs(dir, prefix string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func exactResultPath(resultsDir, runID string) (string, error) {
	if p := resultPayloadPathForIndexedRun(resultsDir, runID); p != "" {
		return p, nil
	}
	p := resultFilePath(resultsDir, runID)
	if err := assertInsideRoot(resultsDir, p, "Async result file"); err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", nil
}

type asyncRunMatch struct {
	ID       string
	Location AsyncRunLocation
}

// findAsyncRunPrefixMatches lists the runs whose id starts with prefix (only for a prefix that may be scanned).
func findAsyncRunPrefixMatches(prefix, asyncDirRoot, resultsDir string) ([]asyncRunMatch, error) {
	requested, err := assertRunID(prefix, "id")
	if err != nil || requested == "" || !canScanAsyncRunPrefix(requested) {
		return nil, err
	}
	asyncRoot, _ := filepath.Abs(asyncDirRoot)
	resultRoot, _ := filepath.Abs(resultsDir)
	var out []asyncRunMatch
	for _, id := range prefixedRunIDs(asyncRoot, requested) {
		dir := filepath.Join(asyncRoot, id)
		if err := assertInsideRoot(asyncRoot, dir, "Async run directory"); err != nil {
			return nil, err
		}
		rp, err := exactResultPath(resultRoot, id)
		if err != nil {
			return nil, err
		}
		loc := AsyncRunLocation{ResultPath: rp, ResolvedID: id}
		if _, err := os.Stat(dir); err == nil {
			loc.AsyncDir = dir
		}
		out = append(out, asyncRunMatch{id, loc})
	}
	return out, nil
}

// RunLocationParams are the id, runId and dir a call names.
type RunLocationParams struct{ ID, RunID, Dir string }

// resolveAsyncRunLocation resolves an id, a prefix or a directory.
func resolveAsyncRunLocation(p RunLocationParams, asyncDirRoot, resultsDir string) (AsyncRunLocation, error) {
	asyncRoot, _ := filepath.Abs(asyncDirRoot)
	resultRoot, _ := filepath.Abs(resultsDir)
	requested, err := assertRunID(p.ID, "id")
	if err != nil {
		return AsyncRunLocation{}, err
	}
	if requested == "" {
		if requested, err = assertRunID(p.RunID, "runId"); err != nil {
			return AsyncRunLocation{}, err
		}
	}
	if p.Dir != "" {
		dir, _ := filepath.Abs(p.Dir)
		if err := assertInsideRoot(asyncRoot, dir, "Async run directory"); err != nil {
			return AsyncRunLocation{}, err
		}
		resolved := firstNonEmpty(requested, filepath.Base(dir))
		if requested != "" && requested != filepath.Base(dir) {
			return AsyncRunLocation{}, fmt.Errorf("Async run id '%s' does not match directory '%s'.", requested, filepath.Base(dir))
		}
		rp, err := exactResultPath(resultRoot, resolved)
		return AsyncRunLocation{AsyncDir: dir, ResultPath: rp, ResolvedID: resolved}, err
	}
	if requested == "" {
		return AsyncRunLocation{}, nil
	}
	direct := filepath.Join(asyncRoot, requested)
	if err := assertInsideRoot(asyncRoot, direct, "Async run directory"); err != nil {
		return AsyncRunLocation{}, err
	}
	rp, err := exactResultPath(resultRoot, requested)
	if err != nil {
		return AsyncRunLocation{}, err
	}
	_, derr := os.Stat(direct)
	if derr == nil || rp != "" {
		loc := AsyncRunLocation{ResultPath: rp, ResolvedID: requested}
		if derr == nil {
			loc.AsyncDir = direct
		}
		return loc, nil
	}
	if len(jsUTF16(requested)) < minSafeAsyncRunPrefixLength {
		return AsyncRunLocation{}, fmt.Errorf("Async run id prefix '%s' is too short. Provide at least %d characters.", requested, minSafeAsyncRunPrefixLength)
	}
	if !canScanAsyncRunPrefix(requested) {
		return AsyncRunLocation{ResolvedID: requested}, nil
	}
	matches, err := findAsyncRunPrefixMatches(requested, asyncRoot, resultRoot)
	if err != nil {
		return AsyncRunLocation{}, err
	}
	switch {
	case len(matches) == 0:
		return AsyncRunLocation{ResolvedID: requested}, nil
	case len(matches) > 1:
		var ids []string
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return AsyncRunLocation{}, fmt.Errorf("Ambiguous async run id prefix '%s' matched: %s. Provide a longer id.", requested, strings.Join(ids, ", "))
	}
	return matches[0].Location, nil
}

// ResolvedRunID is what an id names: a remembered foreground run or an async run.
type ResolvedRunID struct {
	Kind     string // foreground or async
	ID       string
	Location AsyncRunLocation
}

func resultPathFor(resultsDir, runID string) string {
	p := resultPayloadPathForIndexedRun(resultsDir, runID)
	if p == "" {
		p = resultFilePath(resultsDir, runID)
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func foregroundIDs(state *SubagentState) []string {
	var ids []string
	if state == nil || state.CurrentSessionID == "" {
		return nil
	}
	for _, id := range sortedKeys(state.ForegroundRuns) {
		if state.ForegroundRuns[id].SessionID == state.CurrentSessionID {
			ids = append(ids, id)
		}
	}
	return ids
}

// resolveSubagentRunID resolves an id: exact foreground, exact async directory or result, an exact live tool-call id, an
// indexed tool-call id, then a prefix over all of them.
func resolveSubagentRunID(id string, state *SubagentState, asyncDirRoot, resultsDir string) (*ResolvedRunID, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `\/`) || strings.ContainsRune(id, 0) {
		return nil, fmt.Errorf("id must be a safe run identifier.")
	}
	d := defaultAsyncDirs()
	if asyncDirRoot == "" {
		asyncDirRoot = d.Async
	}
	if resultsDir == "" {
		resultsDir = d.Results
	}
	if state != nil && state.ForegroundRuns[id] != nil && state.ForegroundRuns[id].SessionID == state.CurrentSessionID && state.CurrentSessionID != "" {
		return &ResolvedRunID{Kind: "foreground", ID: id}, nil
	}
	dir := filepath.Join(asyncDirRoot, id)
	hasStatus := regularFileExists(filepath.Join(dir, "status.json"))
	rp := resultPathFor(resultsDir, id)
	if hasStatus || rp != "" {
		loc := AsyncRunLocation{ResultPath: rp, ResolvedID: id}
		if hasStatus {
			loc.AsyncDir = dir
		}
		return &ResolvedRunID{Kind: "async", ID: id, Location: loc}, nil
	}
	if state != nil {
		var live []*AsyncJobState
		for _, j := range state.AsyncJobs {
			if j.ToolCallID == id {
				live = append(live, j)
			}
		}
		if len(live) > 1 {
			return nil, fmt.Errorf("Subagent tool-call id '%s' is ambiguous across async runs. Use the returned asyncId instead.", id)
		}
		if len(live) == 1 {
			j := live[0]
			ad := j.AsyncDir
			if _, err := os.Stat(ad); err != nil {
				ad = filepath.Join(asyncDirRoot, j.AsyncID)
			}
			return &ResolvedRunID{Kind: "async", ID: j.AsyncID, Location: AsyncRunLocation{AsyncDir: ad, ResultPath: resultPathFor(resultsDir, j.AsyncID), ResolvedID: j.AsyncID}}, nil
		}
	}
	byTool := indexedToolCallAsyncLocations(id, asyncDirRoot, resultsDir)
	if len(byTool) > 1 {
		return nil, fmt.Errorf("Subagent tool-call id '%s' is ambiguous across async runs. Use the returned asyncId instead.", id)
	}
	if len(byTool) == 1 {
		return &ResolvedRunID{Kind: "async", ID: byTool[0].ID, Location: byTool[0].Location}, nil
	}
	var matches []ResolvedRunID
	for _, fid := range foregroundIDs(state) {
		if strings.HasPrefix(fid, id) {
			matches = append(matches, ResolvedRunID{Kind: "foreground", ID: fid})
		}
	}
	pm, err := findAsyncRunPrefixMatches(id, asyncDirRoot, resultsDir)
	if err != nil {
		return nil, err
	}
	for _, m := range pm {
		if m.Location.ResultPath != "" || (m.Location.AsyncDir != "" && regularFileExists(filepath.Join(m.Location.AsyncDir, "status.json"))) {
			matches = append(matches, ResolvedRunID{Kind: "async", ID: m.ID, Location: m.Location})
		}
	}
	seen := map[string]bool{}
	var unique []ResolvedRunID
	for _, m := range matches {
		k := m.Kind + ":" + m.ID
		if !seen[k] {
			seen[k] = true
			unique = append(unique, m)
		}
	}
	if len(unique) > 1 {
		var names []string
		for _, m := range unique {
			names = append(names, m.Kind+":"+m.ID)
		}
		return nil, fmt.Errorf("Ambiguous subagent run id prefix '%s' matched: %s. Provide a longer id.", id, strings.Join(names, ", "))
	}
	if len(unique) == 1 {
		return &unique[0], nil
	}
	return nil, nil
}

func indexedToolCallAsyncLocations(toolCallID, asyncDirRoot, resultsDir string) []asyncRunMatch {
	byID := map[string]AsyncRunLocation{}
	var order []string
	add := func(id string, loc AsyncRunLocation) {
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		byID[id] = loc
	}
	for _, entry := range readActiveRunToolCallIndex(asyncDirRoot, toolCallID) {
		dir := filepath.Join(asyncDirRoot, entry)
		st, _ := readStatusQuiet(dir)
		if st == nil || st.ToolCallID != toolCallID {
			continue
		}
		runID := firstNonEmpty(st.RunID, entry)
		add(runID, AsyncRunLocation{AsyncDir: dir, ResultPath: resultPathFor(resultsDir, runID), ResolvedID: runID})
	}
	for _, entry := range resultCandidateFilesForToolCall(resultsDir, toolCallID) {
		fromFile := strings.TrimSuffix(entry, ".json")
		rp := resultPayloadPathForIndexedRun(resultsDir, fromFile)
		if rp == "" {
			rp = filepath.Join(resultsDir, entry)
		}
		var data map[string]any
		if err := readJSONFile(rp, &data); err != nil || nonEmptyString(data["toolCallId"]) != toolCallID {
			continue
		}
		runID := firstNonEmpty(nonEmptyString(data["runId"]), nonEmptyString(data["id"]), fromFile)
		dir := filepath.Join(asyncDirRoot, runID)
		loc := AsyncRunLocation{ResultPath: rp, ResolvedID: runID}
		if _, err := os.Stat(dir); err == nil {
			loc.AsyncDir = dir
		}
		add(runID, loc)
	}
	var out []asyncRunMatch
	for _, id := range order {
		out = append(out, asyncRunMatch{id, byID[id]})
	}
	return out
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var errRunNotFound = errors.New("Async run not found. Provide id or dir.")
