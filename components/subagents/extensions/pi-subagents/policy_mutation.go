package pi_subagents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Port of src/runs/shared/mutation-evidence.ts: which tracked files a child changed (by git diff fingerprints taken before and
// after), and the bounded recovery summary of a timed-out child.

const (
	maxTrackedPaths = 500
	maxTimeoutFiles = 20
)

// trackedMutationSnapshot is the tracked-file state at child start.
type trackedMutationSnapshot struct {
	Cwd          string
	DirtyFiles   []string
	Fingerprints map[string]string // path -> sha256 of its diff against HEAD
	Truncated    bool
	Unavailable  string
}

// trackedMutationEvidence is what changed between the snapshot and now.
type trackedMutationEvidence struct {
	ChangedFiles      []string
	AttemptedMutation bool
	Truncated         bool
	Unavailable       string
}

// gitOutput runs git without the repository's fsmonitor (a stale daemon must not block child start-up).
func gitOutput(cwd string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false"}, args...)...)
	cmd.Dir = cwd
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	hideWindow(cmd)
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

func splitNul(out []byte) []string {
	var parts []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func listChangedTrackedFiles(cwd string) (paths []string, truncated bool, err error) {
	out, err := gitOutput(cwd, "diff", "--no-ext-diff", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return nil, false, err
	}
	all := splitNul(out)
	if len(all) > maxTrackedPaths {
		return all[:maxTrackedPaths], true, nil
	}
	return all, false, nil
}

// fingerprintPath is the sha256 of the file's binary diff against HEAD (the original hashes a very large diff through a file;
// the digest is of the same bytes).
func fingerprintPath(cwd, rel string) (string, error) {
	out, err := gitOutput(cwd, "diff", "--no-ext-diff", "--binary", "HEAD", "--", rel)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:]), nil
}

// snapshotTrackedMutations records the tracked files that differ from HEAD and a fingerprint of each diff.
func snapshotTrackedMutations(cwd string) trackedMutationSnapshot {
	paths, truncated, err := listChangedTrackedFiles(cwd)
	if err != nil {
		return trackedMutationSnapshot{Cwd: cwd, DirtyFiles: []string{}, Fingerprints: map[string]string{}, Unavailable: err.Error()}
	}
	fp := map[string]string{}
	for _, p := range paths {
		d, err := fingerprintPath(cwd, p)
		if err != nil {
			return trackedMutationSnapshot{Cwd: cwd, DirtyFiles: []string{}, Fingerprints: map[string]string{}, Unavailable: err.Error()}
		}
		fp[p] = d
	}
	return trackedMutationSnapshot{Cwd: cwd, DirtyFiles: paths, Fingerprints: fp, Truncated: truncated}
}

// collectTrackedMutationEvidence compares the tracked files now with the snapshot (cwd "" is the snapshot's own).
func collectTrackedMutationEvidence(s trackedMutationSnapshot, cwd string) trackedMutationEvidence {
	if cwd == "" {
		cwd = s.Cwd
	}
	if s.Unavailable != "" {
		return trackedMutationEvidence{ChangedFiles: []string{}, Unavailable: s.Unavailable}
	}
	current, currentTruncated, err := listChangedTrackedFiles(cwd)
	if err != nil {
		return trackedMutationEvidence{ChangedFiles: []string{}, Unavailable: err.Error()}
	}
	startDirty := map[string]bool{}
	candidates := map[string]bool{}
	for _, f := range s.DirtyFiles {
		startDirty[f], candidates[f] = true, true
	}
	for _, f := range current {
		candidates[f] = true
	}
	changed := []string{}
	for file := range candidates {
		if !startDirty[file] {
			if !s.Truncated {
				changed = append(changed, file)
			}
			continue
		}
		d, err := fingerprintPath(cwd, file)
		if err != nil {
			return trackedMutationEvidence{ChangedFiles: []string{}, Unavailable: err.Error()}
		}
		if s.Fingerprints[file] != d {
			changed = append(changed, file)
		}
	}
	sort.Strings(changed)
	return trackedMutationEvidence{ChangedFiles: changed, AttemptedMutation: len(changed) > 0, Truncated: s.Truncated || currentTruncated}
}

func formatPathList(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	if len(paths) > maxTimeoutFiles {
		return fmt.Sprintf("%s, ... (%d more)", strings.Join(paths[:maxTimeoutFiles], ", "), len(paths)-maxTimeoutFiles)
	}
	return strings.Join(paths, ", ")
}

// artifactPaths is the original's ArtifactPaths.
type artifactPaths struct{ InputPath, OutputPath, JSONLPath, TranscriptPath, MetadataPath string }

// timeoutRecoverySummary is the original's TimeoutRecoverySummary (the fields this port sets).
type timeoutRecoverySummary struct {
	Termination     string // timed-out, stopped
	ChangedFiles    []string
	Truncated       bool
	RecoveryNeeded  bool
	Reason          string
	ReportStatus    string // missing, written, not-requested, unknown
	CurrentTool     string
	CurrentToolArgs string
	CurrentPath     string
	SessionFile     string
	TranscriptPath  string
	ArtifactPaths   *artifactPaths
	Warning         string
	Message         string
}

// timeoutRecoveryInput is the input of buildTimeoutRecoverySummary; RequiredOutputMissing is nil when no report was requested.
type timeoutRecoveryInput struct {
	Termination           string
	Evidence              trackedMutationEvidence
	RequiredOutputMissing *bool
	CurrentTool           string
	CurrentToolArgs       string
	CurrentPath           string
	SessionFile           string
	TranscriptPath        string
	ArtifactPaths         *artifactPaths
}

// buildTimeoutRecoverySummary is the bounded recovery data and text of a timed-out or stopped child.
func buildTimeoutRecoverySummary(in timeoutRecoveryInput) timeoutRecoverySummary {
	const warning = "Inspect partial changes before retrying or resuming the child."
	changed := in.Evidence.ChangedFiles
	if len(changed) > maxTimeoutFiles {
		changed = changed[:maxTimeoutFiles]
	}
	reportStatus := "not-requested"
	if in.RequiredOutputMissing != nil {
		if *in.RequiredOutputMissing {
			reportStatus = "missing"
		} else {
			reportStatus = "written"
		}
	}
	recoveryNeeded := in.Termination == "timed-out" && reportStatus == "missing" && len(in.Evidence.ChangedFiles) > 0
	files := formatPathList(in.Evidence.ChangedFiles)
	if in.Evidence.Unavailable != "" {
		files = "unavailable (" + in.Evidence.Unavailable + ")"
	}
	lines := []string{"Recovery summary:", "- termination: " + in.Termination, "- changed tracked files: " + files}
	if in.RequiredOutputMissing != nil {
		lines = append(lines, "- requested report: "+reportStatus)
	}
	if recoveryNeeded {
		lines = append(lines, "- Recovery needed: review the diff and artifacts before resuming or launching dependent stages.")
	}
	if in.CurrentTool != "" {
		line := "- active tool: " + in.CurrentTool
		if in.CurrentToolArgs != "" {
			line += " — " + in.CurrentToolArgs
		}
		lines = append(lines, line)
	}
	if in.CurrentPath != "" {
		lines = append(lines, "- active path: "+in.CurrentPath)
	}
	if in.SessionFile != "" {
		lines = append(lines, "- session file: "+in.SessionFile)
	}
	if in.TranscriptPath != "" {
		lines = append(lines, "- transcript: "+in.TranscriptPath)
	}
	if in.ArtifactPaths != nil && in.ArtifactPaths.OutputPath != "" {
		lines = append(lines, "- output artifact: "+in.ArtifactPaths.OutputPath)
	}
	if in.ArtifactPaths != nil && in.ArtifactPaths.MetadataPath != "" {
		lines = append(lines, "- metadata artifact: "+in.ArtifactPaths.MetadataPath)
	}
	lines = append(lines, "Warning: "+warning)
	s := timeoutRecoverySummary{
		Termination: in.Termination, ChangedFiles: changed,
		Truncated:    len(in.Evidence.ChangedFiles) > len(changed) || in.Evidence.Truncated,
		ReportStatus: reportStatus, CurrentTool: in.CurrentTool, CurrentToolArgs: in.CurrentToolArgs, CurrentPath: in.CurrentPath,
		SessionFile: in.SessionFile, TranscriptPath: in.TranscriptPath, ArtifactPaths: in.ArtifactPaths, Warning: warning, Message: strings.Join(lines, "\n"),
	}
	if recoveryNeeded {
		s.RecoveryNeeded, s.Reason = true, "timed-out-with-dirty-worktree"
	}
	return s
}

// timeoutRecoveryProjection is the safe parent-facing subset of a recovery summary.
type timeoutRecoveryProjection struct {
	Termination    string
	ChangedFiles   []string
	Truncated      bool
	RecoveryNeeded bool
	Reason         string
	ReportStatus   string
}

// projectTimeoutRecovery projects a decoded recovery summary (nil when value is not one).
func projectTimeoutRecovery(value any) *timeoutRecoveryProjection {
	src, ok := asObject(value)
	if !ok {
		return nil
	}
	term, _ := src.vals["termination"].(string)
	changed, isArr := asArray(src.vals["changedFiles"])
	if (term != "timed-out" && term != "stopped") || !isArr {
		return nil
	}
	var all []string
	for _, f := range changed {
		if s, ok := f.(string); ok {
			all = append(all, s)
		}
	}
	kept := all
	if len(kept) > maxTimeoutFiles {
		kept = kept[:maxTimeoutFiles]
	}
	if kept == nil {
		kept = []string{}
	}
	p := &timeoutRecoveryProjection{Termination: term, ChangedFiles: kept}
	p.Truncated = src.vals["truncated"] == true || len(all) > len(kept)
	p.RecoveryNeeded = src.vals["recoveryNeeded"] == true
	if src.vals["reason"] == "timed-out-with-dirty-worktree" {
		p.Reason = "timed-out-with-dirty-worktree"
	}
	if rs, _ := src.vals["reportStatus"].(string); rs == "missing" || rs == "written" || rs == "not-requested" || rs == "unknown" {
		p.ReportStatus = rs
	}
	return p
}

// formatTimeoutRecoveryLines renders only the bounded recovery route a parent or operator needs.
func formatTimeoutRecoveryLines(value any, indent string) []string {
	r := projectTimeoutRecovery(value)
	if r == nil || !r.RecoveryNeeded {
		return nil
	}
	changed, count := "none", "0"
	if len(r.ChangedFiles) > 0 {
		changed = strings.Join(r.ChangedFiles, ", ")
		count = fmt.Sprint(len(r.ChangedFiles))
		if r.Truncated {
			changed += ", …"
			count += "+"
		}
	}
	report := r.ReportStatus
	if report == "" {
		report = "unknown"
	}
	class := r.Reason
	if class == "" {
		class = r.Termination
	}
	return []string{
		indent + "Recovery needed: review the diff and artifacts before resuming or launching dependent stages.",
		fmt.Sprintf("%sRecovery evidence: requested report: %s; changed tracked files: %s (%s); classification: %s", indent, report, changed, count, class),
	}
}
