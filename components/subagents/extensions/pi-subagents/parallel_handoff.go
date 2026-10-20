package pi_subagents

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Port of src/runs/shared/parallel-handoff.ts: the handoff manifest of managed worktrees (handoff.json), the durable record of
// which child ran in which worktree, what its patch was, and whether the worktrees may be removed. The manifest is ordered
// JSON (*jsObject): the original validates it in place and keeps what it does not know. writeWorktreeSetupHandoff and
// discardPreservedWorktrees, which take the worktree slice's setup progress and cleanup, belong to that slice.

var (
	handoffCommitPattern         = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
	handoffManifestDigestPattern = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)
	handoffStaleSuffix           = regexp.MustCompile(` evidence is stale$`)
	handoffAgentUnsafe           = regexp.MustCompile(`[^\w.-]`)
)

const (
	maxAttestationActorLength     = 128
	maxAttestationTimestampLength = 64
	maxSupersessionIDLength       = 128
)

// CleanupEligibility is the stored verdict on removing a lane's worktrees.
type CleanupEligibility struct {
	State  string `json:"state"` // active, terminal-eligible, superseded-eligible, terminal-blocked, unknown
	Reason string `json:"reason,omitempty"`
}

func (c CleanupEligibility) object() *jsObject {
	o := jo("state", c.State)
	if c.Reason != "" {
		o.set("reason", c.Reason)
	}
	return o
}

// ParallelHandoffReference is the summary of a manifest (types.ts ParallelHandoffReference).
type ParallelHandoffReference struct {
	Version            int                 `json:"version"`
	Path               string              `json:"path"`
	GroupCount         int                 `json:"groupCount"`
	ChildCount         int                 `json:"childCount"`
	ChangedPatches     int                 `json:"changedPatches"`
	CleanupState       string              `json:"cleanupState"`
	CleanupEligibility *CleanupEligibility `json:"cleanupEligibility,omitempty"`
}

// ParallelHandoffResult is one child's result as the handoff records it.
type ParallelHandoffResult struct {
	Agent                string
	Status               string
	Summary              string
	OutputPath           string
	StructuredOutput     any
	HasStructuredOutput  bool
	StructuredOutputPath string
	SessionPath          string
	WorkflowKey          string
	RunID                string
	Lane                 *jsObject
}

// HandoffWorktree is one managed worktree of a setup.
type HandoffWorktree struct {
	Path     string
	Branch   string
	Index    int
	Provider string
	Naming   string
}

// HandoffSetup is the part of a worktree setup the handoff records.
type HandoffSetup struct {
	Cwd        string
	BaseCommit string
	Worktrees  []HandoffWorktree
}

// HandoffDiff is the patch a worktree produced.
type HandoffDiff struct {
	Index        int
	Agent        string
	Branch       string
	DiffStat     string
	FilesChanged int
	Insertions   int
	Deletions    int
	PatchPath    string
	Error        string
}

func handoffObj(v any) *jsObject {
	o, _ := v.(*jsObject)
	return o
}

func handoffList(v any) ([]any, bool) {
	l, ok := v.([]any)
	return l, ok
}

func handoffInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < 0 || f > 9007199254740991 {
		return 0, false
	}
	return int(f), true
}

func handoffOptionalIdentity(o *jsObject, k, label string) (string, error) {
	v, ok := lpGet(o, k)
	if !ok {
		return "", nil
	}
	s, isStr := v.(string)
	if !isStr || jsTrim(s) == "" {
		return "", fmt.Errorf("%s must be a non-empty string.", label)
	}
	return jsTrim(s), nil
}

func handoffNonNegativeIndex(v any, label string) (int, error) {
	n, ok := handoffInt(v)
	if !ok {
		return 0, fmt.Errorf("%s must be a non-negative integer.", label)
	}
	return n, nil
}

func normalizeHandoffLaneBinding(value any, label string) (*jsObject, error) {
	binding, ok := value.(*jsObject)
	if !ok || binding == nil {
		return nil, fmt.Errorf("%s must be an object.", label)
	}
	var unknown []string
	for _, k := range binding.order() {
		switch k {
		case "index", "taskIndex", "workflowKey", "runId", "lane":
		default:
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("%s has unsupported fields: %s.", label, strings.Join(unknown, ", "))
	}
	index, err := handoffNonNegativeIndex(binding.vals["index"], label+".index")
	if err != nil {
		return nil, err
	}
	taskIndex, err := handoffNonNegativeIndex(binding.vals["taskIndex"], label+".taskIndex")
	if err != nil {
		return nil, err
	}
	workflowKey, err := handoffOptionalIdentity(binding, "workflowKey", label+".workflowKey")
	if err != nil {
		return nil, err
	}
	runID, err := handoffOptionalIdentity(binding, "runId", label+".runId")
	if err != nil {
		return nil, err
	}
	lane, err := NormalizeWorkflowLaneMetadata(anyOrUndef(binding, "lane"), label+".lane")
	if err != nil {
		return nil, err
	}
	if workflowKey != "" {
		if err := AssertWorkflowLaneKey(lane, workflowKey, label+".lane"); err != nil {
			return nil, err
		}
	}
	out := jo("index", float64(index), "taskIndex", float64(taskIndex))
	if workflowKey != "" {
		out.set("workflowKey", workflowKey)
	}
	if runID != "" {
		out.set("runId", runID)
	}
	if lane != nil {
		out.set("lane", lane)
	}
	return out, nil
}

// validateManifestIdentity checks a parsed manifest and normalizes it in place (trimmed identities, normalized lanes).
func validateManifestIdentity(parsed *jsObject, manifestPath string) error {
	invalid := func(format string, a ...any) error {
		return fmt.Errorf("Invalid parallel handoff manifest '%s': %s", manifestPath, fmt.Sprintf(format, a...))
	}
	groups, isList := handoffList(parsed.vals["groups"])
	if parsed.vals["version"] != float64(1) || !isList {
		return fmt.Errorf("Invalid parallel handoff manifest: %s", manifestPath)
	}
	runID, ok := parsed.vals["runId"].(string)
	if !ok || jsTrim(runID) == "" {
		return invalid("runId must be a non-empty string.")
	}
	parsed.set("runId", jsTrim(runID))
	workflowKeys, childRunIDs, childIndexes := map[string]bool{}, map[string]bool{}, map[int]bool{}
	for groupIndex, raw := range groups {
		group := handoffObj(raw)
		var cleanupTasks []any
		var children []any
		var okTasks, okChildren bool
		if group != nil {
			children, okChildren = handoffList(group.vals["children"])
			if cleanup := group.obj("cleanup"); cleanup != nil {
				cleanupTasks, okTasks = handoffList(cleanup.vals["tasks"])
			}
		}
		if group == nil || !okChildren || !okTasks {
			return invalid("groups[%d] is malformed.", groupIndex)
		}
		cleanupIndexes := map[int]bool{}
		for _, t := range cleanupTasks {
			if to := handoffObj(t); to != nil {
				if n, ok := handoffInt(to.vals["index"]); ok {
					cleanupIndexes[n] = true
				}
			}
		}
		if lb, has := group.vals["laneBindings"]; has {
			if _, u := lb.(undef); !u {
				list, ok := handoffList(lb)
				if !ok {
					return invalid("groups[%d].laneBindings must be an array.", groupIndex)
				}
				seen := map[int]bool{}
				normalizedBindings := make([]any, len(list))
				for bi, b := range list {
					n, err := normalizeHandoffLaneBinding(b, fmt.Sprintf("Invalid parallel handoff manifest '%s': groups[%d].laneBindings[%d]", manifestPath, groupIndex, bi))
					if err != nil {
						return err
					}
					taskIndex := int(n.vals["taskIndex"].(float64))
					if seen[taskIndex] {
						return invalid("groups[%d] has duplicate lane binding taskIndex %d.", groupIndex, taskIndex)
					}
					if !cleanupIndexes[taskIndex] {
						return invalid("groups[%d].laneBindings[%d] has no cleanup task for taskIndex %d.", groupIndex, bi, taskIndex)
					}
					seen[taskIndex] = true
					normalizedBindings[bi] = n
				}
				group.set("laneBindings", normalizedBindings)
			}
		}
		taskIndexes := map[int]bool{}
		for childIndex, rawChild := range children {
			child := handoffObj(rawChild)
			if child == nil || child.obj("patch") == nil {
				return invalid("groups[%d].children[%d] is malformed.", groupIndex, childIndex)
			}
			prefix := fmt.Sprintf("Invalid parallel handoff manifest '%s': groups[%d].children[%d]", manifestPath, groupIndex, childIndex)
			index, err := handoffNonNegativeIndex(child.vals["index"], prefix+".index")
			if err != nil {
				return err
			}
			taskIndex, err := handoffNonNegativeIndex(child.vals["taskIndex"], prefix+".taskIndex")
			if err != nil {
				return err
			}
			if taskIndexes[taskIndex] {
				return invalid("groups[%d] has duplicate child taskIndex %d.", groupIndex, taskIndex)
			}
			taskIndexes[taskIndex] = true
			if childIndexes[index] {
				return invalid("duplicate child index %d.", index)
			}
			childIndexes[index] = true
			if !cleanupIndexes[taskIndex] {
				return invalid("groups[%d].children[%d] has no cleanup task for taskIndex %d.", groupIndex, childIndex, taskIndex)
			}
			workflowKey, err := handoffOptionalIdentity(child, "workflowKey", prefix+".workflowKey")
			if err != nil {
				return err
			}
			childRunID, err := handoffOptionalIdentity(child, "runId", prefix+".runId")
			if err != nil {
				return err
			}
			lane, err := NormalizeWorkflowLaneMetadata(anyOrUndef(child, "lane"), prefix+".lane")
			if err != nil {
				return err
			}
			if workflowKey != "" {
				if err := AssertWorkflowLaneKey(lane, workflowKey, prefix+".lane"); err != nil {
					return err
				}
			}
			if workflowKey != "" && workflowKeys[workflowKey] {
				return invalid("duplicate workflow key '%s'.", workflowKey)
			}
			if workflowKey != "" {
				workflowKeys[workflowKey] = true
			}
			if childRunID != "" && childRunIDs[childRunID] {
				return invalid("duplicate child run id '%s'.", childRunID)
			}
			if childRunID != "" {
				childRunIDs[childRunID] = true
			}
			if lane != nil {
				child.set("lane", lane)
			}
			if workflowKey != "" {
				child.set("workflowKey", workflowKey)
			}
			if childRunID != "" {
				child.set("runId", childRunID)
			}
			child.set("index", float64(index))
			child.set("taskIndex", float64(taskIndex))
		}
	}
	return nil
}

// ReadParallelHandoffManifest is readParallelHandoffManifest: nil when the file does not exist.
func ReadParallelHandoffManifest(manifestPath string) (*jsObject, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if msg := workflowJSONError(string(data)); msg != "" {
		return nil, errors.New(msg)
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	parsed, ok := v.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Invalid parallel handoff manifest: %s", manifestPath)
	}
	if err := validateManifestIdentity(parsed, manifestPath); err != nil {
		return nil, err
	}
	return parsed, nil
}

// ParallelHandoffMatch is a child of a manifest with its group.
type ParallelHandoffMatch struct {
	Group *jsObject
	Child *jsObject
}

func handoffChildren(manifest *jsObject) []ParallelHandoffMatch {
	var out []ParallelHandoffMatch
	groups, _ := handoffList(manifest.vals["groups"])
	for _, g := range groups {
		group := handoffObj(g)
		if group == nil {
			continue
		}
		children, _ := handoffList(group.vals["children"])
		for _, c := range children {
			if child := handoffObj(c); child != nil {
				out = append(out, ParallelHandoffMatch{group, child})
			}
		}
	}
	return out
}

// ResolveParallelHandoffChild is resolveParallelHandoffChild: the child a workflow key or child run id names; nil for none.
func ResolveParallelHandoffChild(manifestPath, runID, workflowKey, childRunID string) (*ParallelHandoffMatch, error) {
	manifest, err := ReadParallelHandoffManifest(manifestPath)
	if err != nil || manifest == nil {
		return nil, err
	}
	if m, _ := manifest.vals["runId"].(string); m != runID {
		return nil, fmt.Errorf("Managed worktree handoff belongs to run '%s', not '%s'.", m, runID)
	}
	workflowKey, childRunID = jsTrim(workflowKey), jsTrim(childRunID)
	if workflowKey == "" && childRunID == "" {
		return nil, errors.New("Parallel handoff child resolution requires workflowKey or childRunId.")
	}
	var matches []ParallelHandoffMatch
	for _, m := range handoffChildren(manifest) {
		if workflowKey != "" && receiptStringAt(m.Child, "workflowKey") != workflowKey {
			continue
		}
		if childRunID != "" && receiptStringAt(m.Child, "runId") != childRunID {
			continue
		}
		matches = append(matches, m)
	}
	if len(matches) > 1 {
		who := "'" + childRunID + "'"
		if workflowKey != "" {
			who = "'" + workflowKey + "'"
		}
		return nil, fmt.Errorf("Parallel handoff has multiple children matching workflow identity %s.", who)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return &matches[0], nil
}

func handoffResolveExisting(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func handoffPathInside(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// ResolveRetainedWorktreeCwd is resolveRetainedWorktreeCwd: the cwd a retained child must resume in, inside its managed
// worktree. ok is false when the manifest or the child is unknown.
func ResolveRetainedWorktreeCwd(manifestPath, runID string, childIndex int) (cwd string, ok bool, err error) {
	manifest, err := ReadParallelHandoffManifest(manifestPath)
	if err != nil || manifest == nil {
		return "", false, err
	}
	if m, _ := manifest.vals["runId"].(string); m != runID {
		return "", false, fmt.Errorf("Managed worktree handoff belongs to run '%s', not '%s'.", m, runID)
	}
	var match *ParallelHandoffMatch
	for _, m := range handoffChildren(manifest) {
		if n, _ := handoffInt(m.Child.vals["index"]); n == childIndex {
			mm := m
			match = &mm
			break
		}
	}
	if match == nil {
		return "", false, nil
	}
	var cleanup *jsObject
	tasks, _ := handoffList(match.Group.obj("cleanup").vals["tasks"])
	taskIndex, _ := handoffInt(match.Child.vals["taskIndex"])
	for _, t := range tasks {
		if to := handoffObj(t); to != nil {
			if n, _ := handoffInt(to.vals["index"]); n == taskIndex {
				cleanup = to
				break
			}
		}
	}
	if cleanup == nil {
		return "", false, fmt.Errorf("Async run '%s' child %d has no managed worktree cleanup record.", runID, childIndex)
	}
	relativeCwd, relErr := filepath.Rel(handoffResolveExisting(receiptStringAt(match.Group, "repoRoot")), handoffResolveExisting(receiptStringAt(manifest, "cwd")))
	if relErr != nil || filepath.IsAbs(relativeCwd) || relativeCwd == ".." || strings.HasPrefix(relativeCwd, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("Async run '%s' has an invalid managed worktree cwd.", runID)
	}
	cleanupPath := receiptStringAt(cleanup, "path")
	required := filepath.Join(cleanupPath, relativeCwd)
	if objTruthy(cleanup, "worktreeRemoved") || objTruthy(cleanup, "branchRemoved") {
		return "", false, fmt.Errorf("Async run '%s' required managed worktree was removed: %s", runID, required)
	}
	missing := fmt.Errorf("Async run '%s' required managed worktree cwd is missing: %s", runID, required)
	rootStat, statErr := os.Lstat(cleanupPath)
	if statErr != nil || !rootStat.IsDir() {
		return "", false, missing
	}
	worktreeRoot, statErr := filepath.EvalSymlinks(cleanupPath)
	if statErr != nil {
		return "", false, missing
	}
	if st, statErr := os.Stat(required); statErr != nil || !st.IsDir() {
		return "", false, missing
	}
	resolved, statErr := filepath.EvalSymlinks(required)
	if statErr != nil {
		return "", false, missing
	}
	if !handoffPathInside(worktreeRoot, resolved) {
		return "", false, fmt.Errorf("Async run '%s' has an invalid managed worktree cwd.", runID)
	}
	return required, true, nil
}

func handoffReference(manifestPath string, manifest *jsObject) ParallelHandoffReference {
	groups, _ := handoffList(manifest.vals["groups"])
	children := handoffChildren(manifest)
	changed := 0
	for _, c := range children {
		if objTruthy(c.Child.obj("patch"), "changed") {
			changed++
		}
	}
	state := "complete"
	for _, g := range groups {
		if go_ := handoffObj(g); go_ == nil || go_.obj("cleanup") == nil || go_.obj("cleanup").vals["state"] != "complete" {
			state = "partial"
		}
	}
	ref := ParallelHandoffReference{Version: 1, Path: manifestPath, GroupCount: len(groups), ChildCount: len(children), ChangedPatches: changed, CleanupState: state}
	if _, has := manifest.vals["cleanupEligibility"]; has {
		e := trustedStoredCleanupEligibility(manifest)
		ref.CleanupEligibility = &e
	}
	return ref
}

func handoffBoundedString(value any, label string, maxBytes int) (string, error) {
	s, ok := value.(string)
	if !ok || jsTrim(s) == "" {
		return "", fmt.Errorf("%s must be a non-empty string.", label)
	}
	n := jsTrim(s)
	if strings.ContainsAny(n, "\n\r") {
		return "", fmt.Errorf("%s must not contain newlines.", label)
	}
	if len(n) > maxBytes {
		return "", fmt.Errorf("%s must be at most %d bytes.", label, maxBytes)
	}
	return n, nil
}

func handoffCommitString(value any, label string) (string, error) {
	n, err := handoffBoundedString(value, label, 64)
	if err != nil {
		return "", err
	}
	n = strings.ToLower(n)
	if !handoffCommitPattern.MatchString(n) {
		return "", fmt.Errorf("%s must be a full 40-character commit SHA.", label)
	}
	return n, nil
}

func handoffDigestString(value any, label string) (string, error) {
	n, err := handoffBoundedString(value, label, 64)
	if err != nil {
		return "", err
	}
	n = strings.ToLower(n)
	if !handoffManifestDigestPattern.MatchString(n) {
		return "", fmt.Errorf("%s must be a full 64-character SHA-256 digest.", label)
	}
	return n, nil
}

// stableJSON is stableJson (launch-contract.ts:12): JSON with the keys sorted by localeCompare and undefined entries dropped.
func stableJSON(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = stableJSON(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *jsObject:
		var keys []string
		for _, k := range x.order() {
			if _, u := x.vals[k].(undef); !u {
				keys = append(keys, k)
			}
		}
		sort.SliceStable(keys, func(i, j int) bool { return localeCompare(keys[i], keys[j]) < 0 })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = marshalJSON(k, "") + ":" + stableJSON(x.vals[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	case undef:
		return "null"
	}
	return marshalJSON(v, "")
}

// icuPunctuationOrder is the order of ASCII punctuation in the root collation, which sorts before digits and letters.
const icuPunctuationOrder = "_-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$"

func localeWeight(r rune) (primary int, upper bool) {
	switch {
	case r == ' ' || r == '\t' || r == '\n' || r == '\r':
		return 1, false
	case r >= '0' && r <= '9':
		return 100 + int(r-'0'), false
	case r >= 'a' && r <= 'z':
		return 200 + int(r-'a'), false
	case r >= 'A' && r <= 'Z':
		return 200 + int(r-'A'), true
	}
	if i := strings.IndexRune(icuPunctuationOrder, r); i >= 0 {
		return 10 + i, false
	}
	return 1000 + int(r), false
}

// localeCompare is String.prototype.localeCompare as ICU's root collation orders ASCII text: by letter without case, then
// lower case before upper case. Text outside ASCII compares by code point after it.
func localeCompare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		pa, _ := localeWeight(ra[i])
		pb, _ := localeWeight(rb[i])
		if pa != pb {
			if pa < pb {
				return -1
			}
			return 1
		}
	}
	if len(ra) != len(rb) {
		if len(ra) < len(rb) {
			return -1
		}
		return 1
	}
	for i := range ra {
		_, ua := localeWeight(ra[i])
		_, ub := localeWeight(rb[i])
		if ua != ub {
			if !ua {
				return -1
			}
			return 1
		}
	}
	return 0
}

func manifestFactsDigest(manifest *jsObject) string {
	facts := newObject()
	for _, k := range []string{"version", "runId", "mode", "source", "cwd", "groups"} {
		if v, ok := manifest.vals[k]; ok {
			facts.set(k, v)
		}
	}
	return stableJSONDigest(facts)
}

var handoffDateLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02", "2006-01", "2006", "2006-01-02 15:04:05", "2006-01-02 15:04:05Z07:00",
	time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC, "Jan 2, 2006", "Jan 2, 2006 15:04:05", "January 2, 2006", "2 Jan 2006", "2006/01/02", "1/2/2006",
}

// handoffDateParses approximates Number.isFinite(Date.parse(text)) for the forms an attestation carries (ISO and RFC 2822).
func handoffDateParses(s string) bool {
	for _, l := range handoffDateLayouts {
		if _, err := time.Parse(l, s); err == nil {
			return true
		}
	}
	return false
}

func handoffAttestationTimestamp(value any) (string, error) {
	n, err := handoffBoundedString(value, "attestedAt", maxAttestationTimestampLength)
	if err != nil {
		return "", err
	}
	if !handoffDateParses(n) {
		return "", errors.New("attestedAt must be a valid timestamp.")
	}
	return n, nil
}

func normalizeHandoffMergeEvidence(value any) (*jsObject, error) {
	merge, ok := value.(*jsObject)
	if !ok || merge == nil {
		return nil, errors.New("merge must be an object.")
	}
	pr, isNum := merge.vals["prNumber"].(float64)
	if !isNum || pr != math.Trunc(pr) || pr < 1 {
		return nil, errors.New("merge.prNumber must be a positive integer.")
	}
	te := merge.vals["treeEquivalent"]
	if te != true && te != false && te != "unknown" {
		return nil, errors.New("merge.treeEquivalent must be true, false, or 'unknown'.")
	}
	pm := merge.vals["postMergeChecks"]
	if pm != "recorded" && pm != "unknown" {
		return nil, errors.New("merge.postMergeChecks must be 'recorded' or 'unknown'.")
	}
	head, err := handoffCommitString(merge.vals["reviewedHead"], "merge.reviewedHead")
	if err != nil {
		return nil, err
	}
	commit, err := handoffCommitString(merge.vals["mergeCommit"], "merge.mergeCommit")
	if err != nil {
		return nil, err
	}
	by, err := handoffBoundedString(merge.vals["attestedBy"], "merge.attestedBy", maxAttestationActorLength)
	if err != nil {
		return nil, err
	}
	at, err := handoffAttestationTimestamp(merge.vals["attestedAt"])
	if err != nil {
		return nil, err
	}
	out := jo("prNumber", pr, "reviewedHead", head, "mergeCommit", commit, "treeEquivalent", te, "postMergeChecks", pm, "attestedBy", by, "attestedAt", at)
	if d, has := lpGet(merge, "manifestDigest"); has {
		ds, err := handoffDigestString(d, "merge.manifestDigest")
		if err != nil {
			return nil, err
		}
		out.set("manifestDigest", ds)
	}
	return out, nil
}

func normalizeHandoffSupersessionEvidence(value any) (*jsObject, error) {
	s, ok := value.(*jsObject)
	if !ok || s == nil {
		return nil, errors.New("supersession must be an object.")
	}
	by, err := handoffBoundedString(s.vals["supersededBy"], "supersession.supersededBy", maxSupersessionIDLength)
	if err != nil {
		return nil, err
	}
	attested, err := handoffBoundedString(s.vals["attestedBy"], "supersession.attestedBy", maxAttestationActorLength)
	if err != nil {
		return nil, err
	}
	at, err := handoffAttestationTimestamp(s.vals["attestedAt"])
	if err != nil {
		return nil, err
	}
	out := jo("supersededBy", by, "attestedBy", attested, "attestedAt", at)
	if d, has := lpGet(s, "manifestDigest"); has {
		ds, err := handoffDigestString(d, "supersession.manifestDigest")
		if err != nil {
			return nil, err
		}
		out.set("manifestDigest", ds)
	}
	return out, nil
}

func normalizeStoredCleanupEligibility(value any) CleanupEligibility {
	e, ok := value.(*jsObject)
	if !ok || e == nil {
		return CleanupEligibility{State: "unknown"}
	}
	state, _ := e.vals["state"].(string)
	switch state {
	case "active", "terminal-eligible", "superseded-eligible", "unknown":
		return CleanupEligibility{State: state}
	case "terminal-blocked":
		if reason, ok := e.vals["reason"].(string); ok && jsTrim(reason) != "" && !strings.ContainsAny(reason, "\n\r") {
			return CleanupEligibility{State: "terminal-blocked", Reason: string(utf16Slice(jsTrim(reason), 256))}
		}
	}
	return CleanupEligibility{State: "unknown"}
}

var storedChildStatuses = map[string]bool{"pending": true, "running": true, "completed": true, "complete": true, "failed": true, "paused": true, "stopped": true, "detached": true, "rejected": true}

// IsTerminalParallelHandoffChildStatus is isTerminalParallelHandoffChildStatus.
func IsTerminalParallelHandoffChildStatus(status any) bool {
	switch status {
	case "completed", "complete", "failed", "paused", "stopped", "rejected":
		return true
	}
	return false
}

func hasValidStoredLaneShape(manifest *jsObject) bool {
	groups, ok := handoffList(manifest.vals["groups"])
	runID, _ := manifest.vals["runId"].(string)
	if manifest.vals["version"] != float64(1) || jsTrim(runID) == "" || !ok || len(groups) == 0 {
		return false
	}
	hasManaged := false
	for _, g := range groups {
		group := handoffObj(g)
		if group == nil {
			return false
		}
		base, _ := group.vals["baseCommit"].(string)
		repo, _ := group.vals["repoRoot"].(string)
		if jsTrim(base) == "" || jsTrim(repo) == "" {
			return false
		}
		cleanup := group.obj("cleanup")
		if cleanup == nil {
			return false
		}
		if st := cleanup.vals["state"]; st != "complete" && st != "partial" {
			return false
		}
		tasks, okTasks := handoffList(cleanup.vals["tasks"])
		children, okChildren := handoffList(group.vals["children"])
		if !okTasks || !okChildren {
			return false
		}
		for _, t := range tasks {
			to := handoffObj(t)
			if to == nil {
				return false
			}
			if _, ok := handoffInt(to.vals["index"]); !ok {
				return false
			}
			if p, _ := to.vals["path"].(string); jsTrim(p) == "" {
				return false
			}
			if b, _ := to.vals["branch"].(string); jsTrim(b) == "" {
				return false
			}
		}
		if len(tasks) > 0 {
			hasManaged = true
		}
		for _, c := range children {
			co := handoffObj(c)
			if co == nil {
				return false
			}
			if s, isStr := co.vals["status"].(string); !isStr || !storedChildStatuses[s] {
				return false
			}
		}
	}
	return hasManaged
}

func hasActiveChildren(manifest *jsObject) bool {
	groups, ok := handoffList(manifest.vals["groups"])
	if !ok {
		return false
	}
	for _, g := range groups {
		group := handoffObj(g)
		if group == nil {
			continue
		}
		children, _ := handoffList(group.vals["children"])
		if len(children) == 0 {
			if cleanup := group.obj("cleanup"); cleanup != nil {
				if tasks, ok := handoffList(cleanup.vals["tasks"]); ok && len(tasks) > 0 {
					return true
				}
			}
		}
		for _, c := range children {
			co := handoffObj(c)
			if co == nil || !IsTerminalParallelHandoffChildStatus(co.vals["status"]) {
				return true
			}
		}
	}
	return false
}

func hasManagedWorktreeTasks(manifest *jsObject) bool {
	groups, _ := handoffList(manifest.vals["groups"])
	for _, g := range groups {
		if group := handoffObj(g); group != nil && group.obj("cleanup") != nil {
			if tasks, ok := handoffList(group.obj("cleanup").vals["tasks"]); ok && len(tasks) > 0 {
				return true
			}
		}
	}
	return false
}

func trustedStoredCleanupEligibility(manifest *jsObject) CleanupEligibility {
	eligibility := normalizeStoredCleanupEligibility(manifest.vals["cleanupEligibility"])
	if !hasValidStoredLaneShape(manifest) {
		return CleanupEligibility{State: "unknown"}
	}
	if hasActiveChildren(manifest) {
		return CleanupEligibility{State: "active"}
	}
	switch eligibility.State {
	case "active":
		return cleanupEligibilityForEvidence(manifest)
	case "terminal-eligible", "superseded-eligible":
		current := cleanupEligibilityForEvidence(manifest)
		if current.State == eligibility.State || (current.State == "terminal-blocked" && handoffStaleSuffix.MatchString(current.Reason)) {
			return current
		}
		return CleanupEligibility{State: "unknown"}
	}
	return eligibility
}

func validateManifestForLaneEvidence(manifest *jsObject, laneID string) error {
	if m, _ := manifest.vals["runId"].(string); m != laneID {
		return fmt.Errorf("Lane '%s' does not match manifest run '%s'.", laneID, m)
	}
	if !hasValidStoredLaneShape(manifest) {
		return errors.New("Lane manifest is malformed.")
	}
	groups, _ := handoffList(manifest.vals["groups"])
	if len(groups) == 0 || !hasManagedWorktreeTasks(manifest) {
		return errors.New("Lane manifest has no managed worktree tasks.")
	}
	if hasActiveChildren(manifest) {
		return errors.New("Lane has an active child owner; merge evidence must be recorded after local reconciliation.")
	}
	return nil
}

func cleanupEligibilityForEvidence(manifest *jsObject) CleanupEligibility {
	if hasActiveChildren(manifest) {
		return CleanupEligibility{State: "active"}
	}
	var merge *jsObject
	if m, has := lpGet(manifest, "merge"); has && jsTruthy(m) {
		var err error
		if merge, err = normalizeHandoffMergeEvidence(m); err != nil {
			return CleanupEligibility{State: "terminal-blocked", Reason: "stored merge evidence is invalid"}
		}
	}
	if s, has := lpGet(manifest, "supersession"); has && jsTruthy(s) {
		sup, err := normalizeHandoffSupersessionEvidence(s)
		if err != nil {
			return CleanupEligibility{State: "terminal-blocked", Reason: "stored supersession evidence is invalid"}
		}
		if d, _ := sup.vals["manifestDigest"].(string); d != manifestFactsDigest(manifest) {
			return CleanupEligibility{State: "terminal-blocked", Reason: "stored supersession evidence is stale"}
		}
		return CleanupEligibility{State: "superseded-eligible"}
	}
	if merge == nil {
		return CleanupEligibility{State: "terminal-blocked", Reason: "no merge or supersession evidence recorded"}
	}
	if d, _ := merge.vals["manifestDigest"].(string); d != manifestFactsDigest(manifest) {
		return CleanupEligibility{State: "terminal-blocked", Reason: "stored merge evidence is stale"}
	}
	if merge.vals["treeEquivalent"] != true {
		if merge.vals["treeEquivalent"] == "unknown" {
			return CleanupEligibility{State: "terminal-blocked", Reason: "merged tree equivalence was not attested"}
		}
		return CleanupEligibility{State: "terminal-blocked", Reason: "merged tree is not equivalent to the reviewed head"}
	}
	if merge.vals["postMergeChecks"] != "recorded" {
		return CleanupEligibility{State: "terminal-blocked", Reason: "required post-merge checks were not recorded"}
	}
	return CleanupEligibility{State: "terminal-eligible"}
}

func sameEvidence(l, r *jsObject, fields ...string) bool {
	for _, f := range fields {
		if marshalJSON(l.vals[f], "") != marshalJSON(r.vals[f], "") {
			return false
		}
	}
	return true
}

func handoffFormatCleanupCommand(manifestPath string, manifest *jsObject) string {
	groups, _ := handoffList(manifest.vals["groups"])
	if len(groups) == 0 {
		return ""
	}
	g := handoffObj(groups[0])
	if g == nil {
		return ""
	}
	repo, _ := g.vals["repoRoot"].(string)
	repo = jsTrim(repo)
	if repo == "" {
		return ""
	}
	return fmt.Sprintf(`subagent({ action: "worktree.cleanup", repo: %s, handoffPath: %s, mode: "plan" })`, marshalJSON(repo, ""), marshalJSON(manifestPath, ""))
}

// FormatStoredParallelHandoffCleanup is formatStoredParallelHandoffCleanup. manifest nil reads the file; one that cannot be
// read or is invalid reads "unknown".
func FormatStoredParallelHandoffCleanup(manifestPath string, manifest *jsObject) string {
	stored := manifest
	if stored == nil {
		if m, err := ReadParallelHandoffManifest(manifestPath); err == nil {
			stored = m
		}
	}
	if stored == nil {
		return strings.Join([]string{"Cleanup eligibility: unknown", "Reason: lane manifest is missing or invalid; removal is not safe.",
			fmt.Sprintf(`Plan command: subagent({ action: "worktree.cleanup", handoffPath: %s, mode: "plan" })`, marshalJSON(manifestPath, ""))}, "\n")
	}
	e := trustedStoredCleanupEligibility(stored)
	lines := []string{"Cleanup eligibility: " + e.State}
	switch e.State {
	case "terminal-blocked":
		lines = append(lines, "Reason: "+e.Reason)
	case "unknown":
		lines = append(lines, "Reason: stored eligibility is missing or invalid; removal is not safe.")
	case "active":
		lines = append(lines, "Reason: a child owner is still active; removal is not safe.")
	}
	if cmd := handoffFormatCleanupCommand(manifestPath, stored); cmd != "" {
		lines = append(lines, "Plan command: "+cmd)
	}
	return strings.Join(lines, "\n")
}

// ParallelHandoffEvidenceResult is what recording evidence returns.
type ParallelHandoffEvidenceResult struct {
	Manifest  *jsObject
	Reference ParallelHandoffReference
	Text      string
}

func handoffNow(now *int64) float64 {
	if now != nil {
		return float64(*now)
	}
	return float64(time.Now().UnixMilli())
}

// RecordParallelHandoffMerge is recordParallelHandoffMerge: the merge attestation of a finished lane, bound to the manifest's facts.
func RecordParallelHandoffMerge(manifestPath, laneID string, mergeInput any, now *int64) (ParallelHandoffEvidenceResult, error) {
	manifest, err := ReadParallelHandoffManifest(manifestPath)
	if err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	if manifest == nil {
		return ParallelHandoffEvidenceResult{}, fmt.Errorf("Parallel handoff manifest not found: %s", manifestPath)
	}
	lane, err := handoffBoundedString(laneID, "laneId", maxSupersessionIDLength)
	if err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	if err := validateManifestForLaneEvidence(manifest, lane); err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	if hasActiveChildren(manifest) {
		return ParallelHandoffEvidenceResult{}, errors.New("Lane has an active child owner; merge evidence must be recorded after local reconciliation.")
	}
	merge, err := normalizeHandoffMergeEvidence(mergeInput)
	if err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	merge.set("manifestDigest", manifestFactsDigest(manifest))
	if existingRaw, has := lpGet(manifest, "merge"); has && jsTruthy(existingRaw) {
		existing, err := normalizeHandoffMergeEvidence(existingRaw)
		if err != nil {
			return ParallelHandoffEvidenceResult{}, err
		}
		if existing.vals["reviewedHead"] != merge.vals["reviewedHead"] {
			return ParallelHandoffEvidenceResult{}, fmt.Errorf("Lane manifest is stale: reviewed head is already recorded as %s.", existing.vals["reviewedHead"])
		}
		if !sameEvidence(existing, merge, "prNumber", "reviewedHead", "mergeCommit", "treeEquivalent", "postMergeChecks", "attestedBy", "attestedAt") {
			return ParallelHandoffEvidenceResult{}, errors.New("Lane manifest already contains different merge evidence for this reviewed head.")
		}
	}
	reconciled := manifest.clone()
	reconciled.set("merge", merge)
	reconciled = lpWithout(reconciled, "supersession")
	updated := reconciled.clone()
	updated.set("cleanupEligibility", cleanupEligibilityForEvidence(reconciled).object())
	updated.set("updatedAt", handoffNow(now))
	if err := asyncWriteJSON(manifestPath, updated); err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	return ParallelHandoffEvidenceResult{updated, handoffReference(manifestPath, updated), FormatStoredParallelHandoffCleanup(manifestPath, updated)}, nil
}

// RecordParallelHandoffSupersession is recordParallelHandoffSupersession.
func RecordParallelHandoffSupersession(manifestPath, laneID string, supersessionInput any, now *int64) (ParallelHandoffEvidenceResult, error) {
	manifest, err := ReadParallelHandoffManifest(manifestPath)
	if err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	if manifest == nil {
		return ParallelHandoffEvidenceResult{}, fmt.Errorf("Parallel handoff manifest not found: %s", manifestPath)
	}
	lane, err := handoffBoundedString(laneID, "laneId", maxSupersessionIDLength)
	if err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	if err := validateManifestForLaneEvidence(manifest, lane); err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	if hasActiveChildren(manifest) {
		return ParallelHandoffEvidenceResult{}, errors.New("Lane has an active child owner; supersession must be recorded after local reconciliation.")
	}
	sup, err := normalizeHandoffSupersessionEvidence(supersessionInput)
	if err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	sup.set("manifestDigest", manifestFactsDigest(manifest))
	if sup.vals["supersededBy"] == lane {
		return ParallelHandoffEvidenceResult{}, errors.New("supersession.supersededBy must identify a different replacement lane.")
	}
	if existingRaw, has := lpGet(manifest, "supersession"); has && jsTruthy(existingRaw) {
		existing, err := normalizeHandoffSupersessionEvidence(existingRaw)
		if err != nil {
			return ParallelHandoffEvidenceResult{}, err
		}
		if !sameEvidence(existing, sup, "supersededBy", "attestedBy", "attestedAt") {
			return ParallelHandoffEvidenceResult{}, errors.New("Lane manifest already contains different supersession evidence.")
		}
	}
	withSup := manifest.clone()
	withSup.set("supersession", sup)
	updated := withSup.clone()
	updated.set("cleanupEligibility", cleanupEligibilityForEvidence(withSup).object())
	updated.set("updatedAt", handoffNow(now))
	if err := asyncWriteJSON(manifestPath, updated); err != nil {
		return ParallelHandoffEvidenceResult{}, err
	}
	return ParallelHandoffEvidenceResult{updated, handoffReference(manifestPath, updated), FormatStoredParallelHandoffCleanup(manifestPath, updated)}, nil
}

func handoffMissingDiff(manifestPath string, stepIndex, taskIndex int, agent, branch string) HandoffDiff {
	patchPath := filepath.Join(filepath.Dir(manifestPath), fmt.Sprintf("missing-diff-step-%d-task-%d-%s.patch", stepIndex, taskIndex, handoffAgentUnsafe.ReplaceAllString(agent, "_")))
	// the artifact is best-effort: the handoff records its absence below
	if os.MkdirAll(filepath.Dir(patchPath), 0o755) == nil {
		_ = os.WriteFile(patchPath, nil, 0o644)
	}
	return HandoffDiff{Index: taskIndex, Agent: agent, Branch: branch, PatchPath: patchPath, Error: "diff artifact unavailable; no patch was captured"}
}

// WriteParallelHandoffGroupInput is the argument of writeParallelHandoffGroup.
type WriteParallelHandoffGroupInput struct {
	ManifestPath   string
	RunID          string
	Mode           string // single, parallel, chain
	Source         string // foreground, async
	Cwd            string
	StepIndex      int
	FlatStartIndex int
	Setup          HandoffSetup
	Diffs          []HandoffDiff
	Cleanup        *jsObject
	Results        []ParallelHandoffResult
	LaneBindings   []*jsObject
	Now            *int64
}

// WriteParallelHandoffGroup is writeParallelHandoffGroup: journals one step's worktrees and children into the run's manifest.
func WriteParallelHandoffGroup(in WriteParallelHandoffGroupInput) (ParallelHandoffReference, error) {
	now := handoffNow(in.Now)
	existing, err := ReadParallelHandoffManifest(in.ManifestPath)
	if err != nil {
		return ParallelHandoffReference{}, err
	}
	if existing != nil && (existing.vals["runId"] != in.RunID || existing.vals["mode"] != in.Mode || existing.vals["source"] != in.Source) {
		return ParallelHandoffReference{}, fmt.Errorf("Parallel handoff manifest belongs to a different run: %s", in.ManifestPath)
	}
	var bindings []*jsObject
	if in.LaneBindings != nil {
		seen := map[int]bool{}
		for i, b := range in.LaneBindings {
			n, err := normalizeHandoffLaneBinding(b, fmt.Sprintf("parallel handoff lane binding %d", i))
			if err != nil {
				return ParallelHandoffReference{}, err
			}
			bindings = append(bindings, n)
		}
		for _, b := range bindings {
			taskIndex, index := int(b.vals["taskIndex"].(float64)), int(b.vals["index"].(float64))
			if seen[taskIndex] {
				return ParallelHandoffReference{}, fmt.Errorf("Parallel handoff has duplicate lane binding taskIndex %d.", taskIndex)
			}
			has := false
			for _, w := range in.Setup.Worktrees {
				has = has || w.Index == taskIndex
			}
			if !has {
				return ParallelHandoffReference{}, fmt.Errorf("Parallel handoff lane binding taskIndex %d has no managed worktree.", taskIndex)
			}
			if index != in.FlatStartIndex+taskIndex {
				return ParallelHandoffReference{}, fmt.Errorf("Parallel handoff lane binding index %d does not match taskIndex %d.", index, taskIndex)
			}
			seen[taskIndex] = true
		}
	}
	bindingFor := func(taskIndex int) *jsObject {
		for _, b := range bindings {
			if int(b.vals["taskIndex"].(float64)) == taskIndex {
				return b
			}
		}
		return nil
	}
	children := make([]any, len(in.Results))
	for taskIndex, result := range in.Results {
		binding := bindingFor(taskIndex)
		bindingString := func(k string) string {
			if binding == nil {
				return ""
			}
			return receiptStringAt(binding, k)
		}
		pick := func(a, b string) string {
			if a != "" {
				return a
			}
			return b
		}
		runID, err := handoffOptionalIdentity(jo("v", optString(pick(result.RunID, bindingString("runId")))), "v", fmt.Sprintf("parallel handoff child %d.runId", taskIndex))
		if err != nil {
			return ParallelHandoffReference{}, err
		}
		var laneValue any = undef{}
		if result.Lane != nil {
			laneValue = result.Lane
		} else if binding != nil {
			if l, ok := binding.vals["lane"]; ok {
				laneValue = l
			}
		}
		lane, err := NormalizeWorkflowLaneMetadata(laneValue, fmt.Sprintf("parallel handoff child %d.lane", taskIndex))
		if err != nil {
			return ParallelHandoffReference{}, err
		}
		workflowKey, err := handoffOptionalIdentity(jo("v", optString(pick(result.WorkflowKey, bindingString("workflowKey")))), "v", fmt.Sprintf("parallel handoff child %d.workflowKey", taskIndex))
		if err != nil {
			return ParallelHandoffReference{}, err
		}
		if workflowKey != "" {
			if err := AssertWorkflowLaneKey(lane, workflowKey, fmt.Sprintf("parallel handoff child %d.lane", taskIndex)); err != nil {
				return ParallelHandoffReference{}, err
			}
		}
		var diff HandoffDiff
		if taskIndex < len(in.Diffs) {
			diff = in.Diffs[taskIndex]
		} else {
			branch := ""
			if taskIndex < len(in.Setup.Worktrees) {
				branch = in.Setup.Worktrees[taskIndex].Branch
			}
			diff = handoffMissingDiff(in.ManifestPath, in.StepIndex, taskIndex, result.Agent, branch)
		}
		child := jo("index", float64(in.FlatStartIndex+taskIndex), "taskIndex", float64(taskIndex), "agent", result.Agent)
		if workflowKey != "" {
			child.set("workflowKey", workflowKey)
		}
		if runID != "" {
			child.set("runId", runID)
		}
		if lane != nil {
			child.set("lane", lane)
		}
		child.set("status", result.Status)
		child.set("summary", result.Summary)
		if result.OutputPath != "" {
			child.set("outputPath", result.OutputPath)
		}
		if result.HasStructuredOutput {
			child.set("structuredOutput", result.StructuredOutput)
		}
		if result.StructuredOutputPath != "" {
			child.set("structuredOutputPath", result.StructuredOutputPath)
		}
		if result.SessionPath != "" {
			child.set("sessionPath", result.SessionPath)
		}
		patch := jo("path", diff.PatchPath, "branch", diff.Branch, "changed", diff.FilesChanged > 0 || diff.Insertions > 0 || diff.Deletions > 0 || jsTrim(diff.DiffStat) != "",
			"diffStat", diff.DiffStat, "filesChanged", float64(diff.FilesChanged), "insertions", float64(diff.Insertions), "deletions", float64(diff.Deletions))
		if diff.Error != "" {
			patch.set("error", diff.Error)
		}
		child.set("patch", patch)
		children[taskIndex] = child
	}
	group := jo("stepIndex", float64(in.StepIndex), "baseCommit", in.Setup.BaseCommit, "repoRoot", in.Setup.Cwd, "children", children)
	if len(bindings) > 0 && len(in.Results) == 0 {
		list := make([]any, len(bindings))
		for i, b := range bindings {
			list[i] = b
		}
		group.set("laneBindings", list)
	}
	cleanup := in.Cleanup
	if cleanup == nil {
		tasks := make([]any, len(in.Setup.Worktrees))
		for i, w := range in.Setup.Worktrees {
			t := jo("index", float64(w.Index), "path", w.Path, "branch", w.Branch)
			if w.Provider != "" {
				t.set("provider", w.Provider)
			}
			if w.Naming != "" {
				t.set("naming", w.Naming)
			}
			t.set("worktreeRemoved", false)
			t.set("branchRemoved", false)
			t.set("preserved", true)
			t.set("reason", "cleanup pending durable handoff capture")
			tasks[i] = t
		}
		cleanup = jo("state", "partial", "pruned", false, "tasks", tasks)
	}
	group.set("cleanup", cleanup)

	var groups []any
	if existing != nil {
		existingGroups, _ := handoffList(existing.vals["groups"])
		for _, g := range existingGroups {
			if go_ := handoffObj(g); go_ != nil {
				if si, _ := go_.num("stepIndex"); int(si) == in.StepIndex {
					continue
				}
			}
			groups = append(groups, g)
		}
	}
	groups = append(groups, group)
	sort.SliceStable(groups, func(i, j int) bool {
		a, _ := handoffObj(groups[i]).num("stepIndex")
		b, _ := handoffObj(groups[j]).num("stepIndex")
		return a < b
	})
	createdAt := now
	if existing != nil {
		if c, ok := existing.num("createdAt"); ok {
			createdAt = c
		}
	}
	without := jo("version", float64(1), "runId", in.RunID, "mode", in.Mode, "source", in.Source, "cwd", in.Cwd, "createdAt", createdAt, "updatedAt", now, "groups", groups)
	carries := false
	if existing != nil {
		for _, k := range []string{"merge", "supersession"} {
			if v, has := existing.vals[k]; has {
				without.set(k, v)
				carries = true
			}
		}
		if _, has := existing.vals["cleanupEligibility"]; has {
			carries = true
		}
	}
	manifest := without.clone()
	if carries {
		manifest.set("cleanupEligibility", cleanupEligibilityForEvidence(without).object())
	}
	if err := asyncWriteJSON(in.ManifestPath, manifest); err != nil {
		return ParallelHandoffReference{}, err
	}
	return handoffReference(in.ManifestPath, manifest), nil
}

func optString(s string) any {
	if s == "" {
		return undef{}
	}
	return s
}

// ParallelHandoffPath is parallelHandoffPath: where a run's manifest lives under a base directory.
func ParallelHandoffPath(baseDir, runID string) string {
	if runID != "" {
		return filepath.Join(baseDir, "handoffs", runID+".json")
	}
	return filepath.Join(baseDir, "handoff.json")
}

// FormatParallelHandoffReference is formatParallelHandoffReference.
func FormatParallelHandoffReference(r ParallelHandoffReference) string {
	return fmt.Sprintf("Worktree handoff: %s (%d children, %d changed patches, cleanup %s)", r.Path, r.ChildCount, r.ChangedPatches, r.CleanupState)
}

// FormatParallelHandoffError is formatParallelHandoffError.
func FormatParallelHandoffError(err error) string {
	return "Worktree handoff unavailable: " + err.Error()
}
