// Package ratchet ports omp's host-side state, statistics, and keep/revert gate
// for the `/ratchet` eval hillclimb, its `ratchet` eval prelude, and the
// `/ratchet` slash command.
//
// upstream: packages/coding-agent/src/ratchet/ratchet.ts
package ratchet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RootRel is the workspace-relative root for ratchet flows (`.omp/ratchet`).
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:19
var RootRel = filepath.Join(".omp", "ratchet")

// Stages lists the three approval stages in canonical order.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:20
var Stages = []Stage{StageInputs, StageGrader, StagePlan}

// Stage identifies an approval stage.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:21
type Stage string

const (
	StageInputs Stage = "inputs"
	StageGrader Stage = "grader"
	StagePlan   Stage = "plan"
)

// Direction specifies whether higher or lower metric values are better.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:22
type Direction string

const (
	DirectionHigher Direction = "higher"
	DirectionLower  Direction = "lower"
)

// RoundDecision is the gate's verdict for one round.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:23
type RoundDecision string

const (
	DecisionBaseline RoundDecision = "baseline"
	DecisionKeep     RoundDecision = "keep"
	DecisionRevert   RoundDecision = "revert"
	DecisionRerun    RoundDecision = "rerun"
)

// Verdict is the paired comparison verdict for a single metric on one split.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:24
type Verdict string

const (
	VerdictUp   Verdict = "up"
	VerdictDown Verdict = "down"
	VerdictFlat Verdict = "flat"
)

// Goal defines the target metric and guardrails.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:26-34
type Goal struct {
	Target     string            `json:"target"`
	Direction  Direction         `json:"direction"`
	Hold       []string          `json:"hold"`
	Directions OrderedDirections `json:"directions"`
}

// Price specifies per-million-token rates for a model the catalog cannot price.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:37-42
type Price struct {
	In         float64  `json:"in"`
	Out        float64  `json:"out"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

// ModelCost is the resolved per-million-token rate card (catalog or override).
// upstream: packages/catalog/src/types.ts ModelCost (flat rates)
type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Usage is a normalized token-usage block.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:518-539
type Usage struct {
	Input       float64
	Output      float64
	CacheRead   float64
	CacheWrite  float64
	TotalTokens float64
}

// SplitSummary is the mean and 95% CI half-width over case means.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:44-50
type SplitSummary struct {
	N    int      `json:"n"`
	Mean *float64 `json:"mean"`
	Half *float64 `json:"half"`
}

// PairedDelta is the paired difference summary plus directional verdict.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:52-54
type PairedDelta struct {
	N       int      `json:"n"`
	Mean    *float64 `json:"mean"`
	Half    *float64 `json:"half"`
	Verdict Verdict  `json:"verdict"`
}

// MetricDeltas pairs train and test deltas for one metric.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:102
type MetricDeltas struct {
	Train PairedDelta `json:"train"`
	Test  PairedDelta `json:"test"`
}

// Round records one gated round in `_state.json`.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:56-67
type Round struct {
	Round     int                     `json:"round"`
	Variant   string                  `json:"variant"`
	Change    string                  `json:"change"`
	Decision  RoundDecision           `json:"decision"`
	Reasons   []string                `json:"reasons"`
	Train     map[string]SplitSummary `json:"train"`
	Test      map[string]SplitSummary `json:"test"`
	Errors    int                     `json:"errors"`
	Truncated int                     `json:"truncated"`
	At        string                  `json:"at"`
}

// StopRule defines when the hillclimb stops.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:80
type StopRule struct {
	Plateau int  `json:"plateau"`
	Rounds  *int `json:"rounds,omitempty"`
}

// ApprovalRecord stores the content digest and timestamp of one stage approval.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:86
type ApprovalRecord struct {
	SHA string `json:"sha"`
	At  string `json:"at"`
}

// BestRecord points to the best variant gated so far.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:88
type BestRecord struct {
	Round     int      `json:"round"`
	Variant   string   `json:"variant"`
	TestScore *float64 `json:"test_score"`
}

// State is the persisted `_state.json` for a ratchet flow.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:69-92
type State struct {
	Version           int                      `json:"version"`
	Flow              string                   `json:"flow"`
	CasesPaths        []string                 `json:"cases_paths"`
	HarnessPaths      []string                 `json:"harness_paths"`
	ChangePaths       []string                 `json:"change_paths"`
	OffLimits         []string                 `json:"off_limits"`
	Command           *string                  `json:"command,omitempty"`
	Goal              *Goal                    `json:"goal,omitempty"`
	Reps              *int                     `json:"reps,omitempty"`
	Stop              StopRule                 `json:"stop"`
	TrainIDs          []string                 `json:"train_ids"`
	TestIDs           []string                 `json:"test_ids"`
	SplitSeed         *int                     `json:"split_seed,omitempty"`
	BaselineInputsSHA *string                  `json:"baseline_inputs_sha,omitempty"`
	Approvals         map[Stage]ApprovalRecord `json:"approvals"`
	Rounds            []Round                  `json:"rounds"`
	Best              *BestRecord              `json:"best,omitempty"`
	CurrentRound      int                      `json:"current_round"`
	FlatRounds        int                      `json:"flat_rounds"`
	Prices            map[string]Price         `json:"prices"`
}

// PriceLookup resolves a served model id to catalog rates; nil when unknown or ambiguous.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:95
type PriceLookup func(model string) *ModelCost

// GateResult is returned by GateVariant.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:97-107
type GateResult struct {
	Variant  string                  `json:"variant"`
	Decision RoundDecision           `json:"decision"`
	Reasons  []string                `json:"reasons"`
	Warnings []string                `json:"warnings"`
	Deltas   map[string]MetricDeltas `json:"deltas"`
	Best     *BestRecord             `json:"best,omitempty"`
	Plateau  bool                    `json:"plateau"`
	Done     bool                    `json:"done"`
	Table    string                  `json:"table"`
}

// ApprovalStatus maps each stage to "missing", "stale", or "current".
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:416
type ApprovalStatus map[Stage]string

// CheckResult is returned by CheckVariant.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:665-672
type CheckResult struct {
	Variant      string `json:"variant"`
	Command      string `json:"command"`
	Cases        int    `json:"cases"`
	Reps         int    `json:"reps"`
	ExpectedRows int    `json:"expected_rows"`
	OutDir       string `json:"out_dir"`
}

// TrainView holds everything the analyzer may read for a round.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:942-948
type TrainView struct {
	Variant string                         `json:"variant"`
	Rows    []map[string]any               `json:"rows"`
	Traces  []string                       `json:"traces"`
	History map[string]map[string]*float64 `json:"history"`
}

// Error is a domain error surfaced as a ToolError in the eval prelude.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:118-120
type Error struct {
	Message string
}

func (e *Error) Error() string { return e.Message }

func newRatchetError(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}

var (
	flowNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	variantNameRe = regexp.MustCompile(`^(baseline|v[1-9][0-9]*)$`)
	traceFileRe   = regexp.MustCompile(`^(.*)_rep\d+\.json$`)
)

var lowerByDefault = map[string]bool{
	"cost_usd":  true,
	"latency_s": true,
}

// Two-sided 95% Student-t critical values for df 1..30; larger samples use the normal value.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:112-116
var tCriticalTable = [...]float64{
	12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228,
	2.201, 2.179, 2.16, 2.145, 2.131, 2.12, 2.11, 2.101, 2.093, 2.086,
	2.08, 2.074, 2.069, 2.064, 2.06, 2.056, 2.052, 2.048, 2.045, 2.042,
}

func tCritical(df int) float64 {
	if df >= 1 && df <= len(tCriticalTable) {
		return tCriticalTable[df-1]
	}
	return 1.96
}

// meanAndHalf computes mean and 95% CI half-width over case values.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:126-133
func meanAndHalf(values []float64) SplitSummary {
	n := len(values)
	if n == 0 {
		return SplitSummary{N: 0, Mean: nil, Half: nil}
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)
	if n < 2 {
		return SplitSummary{N: n, Mean: new(mean), Half: nil}
	}
	varSum := 0.0
	for _, v := range values {
		d := v - mean
		varSum += float64(d * d)
	}
	variance := varSum / float64(n-1)
	half := tCritical(n-1) * math.Sqrt(variance/float64(n))
	return SplitSummary{N: n, Mean: new(mean), Half: new(half)}
}

// MetricDirection returns the effective optimization direction for metric under goal.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:135-138
func MetricDirection(goal Goal, metric string) Direction {
	if metric == goal.Target {
		return goal.Direction
	}
	if dir, ok := goal.Directions.Get(metric); ok && dir != "" {
		return dir
	}
	if lowerByDefault[metric] {
		return DirectionLower
	}
	return DirectionHigher
}

// ---------------------------------------------------------------------------
// Paths and persistence
// ---------------------------------------------------------------------------

// FlowDir returns `<cwd>/.omp/ratchet/<flow>` after validating flow's name.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:144-149
func FlowDir(cwd, flow string) (string, error) {
	if !flowNameRe.MatchString(flow) {
		return "", newRatchetError(`Invalid flow name %s: use lowercase letters, digits, "-" or "_"`, jsStringifyString(flow))
	}
	return filepath.Join(cwd, RootRel, flow), nil
}

func toPosix(p string) string {
	return filepath.ToSlash(p)
}

// normalizeRepoPath normalizes a repo-relative path and rejects workspace escapes.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:156-163
func normalizeRepoPath(cwd, value string) (string, error) {
	base, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	var absolute string
	if filepath.IsAbs(value) {
		absolute = filepath.Clean(value)
	} else {
		absolute = filepath.Join(base, value)
	}
	rel, err := filepath.Rel(base, absolute)
	if err != nil {
		return "", newRatchetError("Path must be inside the workspace: %s", value)
	}
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", newRatchetError("Path must be inside the workspace: %s", value)
	}
	return toPosix(rel), nil
}

func pathExists(absolute string) (bool, error) {
	_, err := os.Stat(absolute)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func normalizeLoadedState(state *State) {
	if state.CasesPaths == nil {
		state.CasesPaths = []string{}
	}
	if state.HarnessPaths == nil {
		state.HarnessPaths = []string{}
	}
	if state.ChangePaths == nil {
		state.ChangePaths = []string{}
	}
	if state.OffLimits == nil {
		state.OffLimits = []string{}
	}
	if state.TrainIDs == nil {
		state.TrainIDs = []string{}
	}
	if state.TestIDs == nil {
		state.TestIDs = []string{}
	}
	if state.Approvals == nil {
		state.Approvals = map[Stage]ApprovalRecord{}
	}
	if state.Rounds == nil {
		state.Rounds = []Round{}
	}
	if state.Prices == nil {
		state.Prices = map[string]Price{}
	}
	if state.Goal != nil {
		if state.Goal.Hold == nil {
			state.Goal.Hold = []string{}
		}
	}
}

// LoadState reads `_state.json` for flow, returning (nil, nil) if absent.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:175-182
func LoadState(cwd, flow string) (*State, error) {
	dir, err := FlowDir(cwd, flow)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "_state.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	normalizeLoadedState(&state)
	return &state, nil
}

// RequireState loads flow's state or returns a RatchetError if it does not exist.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:184-188
func RequireState(cwd, flow string) (*State, error) {
	state, err := LoadState(cwd, flow)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, newRatchetError(`Flow %q does not exist; call init() first`, flow)
	}
	return state, nil
}

// SaveState writes `_state.json` pretty-printed with a trailing newline.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:190-192
func SaveState(cwd string, state *State) error {
	dir, err := FlowDir(cwd, state.Flow)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	normalizeLoadedState(state)
	data, err := marshalNoEscapeIndent(state)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "_state.json"), append(data, '\n'), 0o644)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Chmod(perm)
	_ = tmp.Sync()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameAtomic(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func marshalNoEscapeIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// ---------------------------------------------------------------------------
// Setup: init, plan, split
// ---------------------------------------------------------------------------

// InitOptions configures InitFlow.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:198-204
type InitOptions struct {
	Cases     []string
	Harness   []string
	Change    []string
	OffLimits []string
	Command   *string
}

// InitFlow creates or updates a flow's tracked paths and optional command.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:206-253
func InitFlow(cwd, flow string, options InitOptions) (*State, error) {
	dir, err := FlowDir(cwd, flow)
	if err != nil {
		return nil, err
	}
	cases := make([]string, len(options.Cases))
	for i, v := range options.Cases {
		norm, err := normalizeRepoPath(cwd, v)
		if err != nil {
			return nil, err
		}
		cases[i] = norm
	}
	harness := make([]string, len(options.Harness))
	for i, v := range options.Harness {
		norm, err := normalizeRepoPath(cwd, v)
		if err != nil {
			return nil, err
		}
		harness[i] = norm
	}
	change := make([]string, len(options.Change))
	for i, v := range options.Change {
		norm, err := normalizeRepoPath(cwd, v)
		if err != nil {
			return nil, err
		}
		change[i] = norm
	}
	offLimits := make([]string, len(options.OffLimits))
	for i, v := range options.OffLimits {
		norm, err := normalizeRepoPath(cwd, v)
		if err != nil {
			return nil, err
		}
		offLimits[i] = norm
	}
	if len(cases) == 0 {
		return nil, newRatchetError("init() needs at least one cases path")
	}
	if len(harness) == 0 {
		return nil, newRatchetError("init() needs at least one harness path (runner and grader)")
	}
	if len(change) == 0 {
		return nil, newRatchetError("init() needs at least one change path (the surface being tuned)")
	}
	for _, file := range append(append([]string(nil), cases...), harness...) {
		ok, err := pathExists(filepath.Join(cwd, filepath.FromSlash(file)))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, newRatchetError("Path does not exist: %s", file)
		}
	}
	relDir, err := filepath.Rel(cwd, dir)
	if err != nil {
		return nil, err
	}
	flowRel := toPosix(relDir)
	frozenList := make([]string, 0, len(cases)+len(harness)+len(offLimits)+1)
	frozenList = append(frozenList, cases...)
	frozenList = append(frozenList, harness...)
	frozenList = append(frozenList, offLimits...)
	frozenList = append(frozenList, flowRel)
	for _, target := range change {
		for _, other := range frozenList {
			if target == other || strings.HasPrefix(target, other+"/") || strings.HasPrefix(other, target+"/") {
				return nil, newRatchetError(
					"Change path %s overlaps %s; cases, harness, off-limits, and flow state must stay out of the tuned surface",
					target, other,
				)
			}
		}
	}

	existing, err := LoadState(cwd, flow)
	if err != nil {
		return nil, err
	}
	state := existing
	if state == nil {
		state = &State{
			Version:      1,
			Flow:         flow,
			CasesPaths:   []string{},
			HarnessPaths: []string{},
			ChangePaths:  []string{},
			OffLimits:    []string{},
			Stop:         StopRule{Plateau: 3},
			TrainIDs:     []string{},
			TestIDs:      []string{},
			Approvals:    map[Stage]ApprovalRecord{},
			Rounds:       []Round{},
			CurrentRound: 0,
			FlatRounds:   0,
			Prices:       map[string]Price{},
		}
	}
	state.CasesPaths = cases
	state.HarnessPaths = harness
	state.ChangePaths = change
	state.OffLimits = offLimits
	if options.Command != nil {
		cmd := *options.Command
		state.Command = &cmd
	}
	if err := SaveState(cwd, state); err != nil {
		return nil, err
	}
	return state, nil
}

// PlanGoalInput is the goal payload accepted by ApplyPlan.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:256
type PlanGoalInput struct {
	Target     string
	Direction  Direction
	Hold       []string
	Directions OrderedDirections
}

// PlanStopInput is the stop payload accepted by ApplyPlan.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:258
type PlanStopInput struct {
	Plateau *int
	Rounds  *int
}

// PlanOptions configures ApplyPlan.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:255-261
type PlanOptions struct {
	Goal    *PlanGoalInput
	Reps    *int
	Stop    *PlanStopInput
	Command *string
	Prices  map[string]Price
}

// ApplyPlan updates goal, reps, stop rules, command, and price overrides in place.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:263-294
func ApplyPlan(state *State, options PlanOptions) error {
	baselineTaken := len(state.Rounds) > 0
	if options.Goal != nil {
		dir := options.Goal.Direction
		if dir == "" {
			if lowerByDefault[options.Goal.Target] {
				dir = DirectionLower
			} else {
				dir = DirectionHigher
			}
		}
		hold := make([]string, len(options.Goal.Hold))
		copy(hold, options.Goal.Hold)
		dirs := options.Goal.Directions.Clone()
		for _, h := range hold {
			if h == options.Goal.Target {
				return newRatchetError("goal.hold must not include the target metric")
			}
		}
		state.Goal = &Goal{
			Target:     options.Goal.Target,
			Direction:  dir,
			Hold:       hold,
			Directions: dirs,
		}
	}
	if options.Reps != nil {
		if *options.Reps < 1 {
			return newRatchetError("reps must be a positive integer")
		}
		if baselineTaken && state.Reps != nil && *options.Reps < *state.Reps {
			return newRatchetError("reps cannot shrink after the baseline; rounds would stop being comparable")
		}
		reps := *options.Reps
		state.Reps = &reps
	}
	if options.Stop != nil {
		plateau := state.Stop.Plateau
		if options.Stop.Plateau != nil {
			plateau = *options.Stop.Plateau
		}
		if plateau < 2 {
			return newRatchetError("stop.plateau must be an integer >= 2")
		}
		var rounds *int
		if options.Stop.Rounds != nil {
			r := *options.Stop.Rounds
			rounds = &r
		} else if state.Stop.Rounds != nil {
			r := *state.Stop.Rounds
			rounds = &r
		}
		if rounds != nil && *rounds < 1 {
			return newRatchetError("stop.rounds must be a positive integer")
		}
		state.Stop = StopRule{Plateau: plateau, Rounds: rounds}
	}
	if options.Command != nil {
		cmd := *options.Command
		state.Command = &cmd
	}
	if options.Prices != nil {
		if state.Prices == nil {
			state.Prices = map[string]Price{}
		}
		for k, v := range options.Prices {
			state.Prices[k] = v
		}
	}
	return nil
}

// seededRandom implements mulberry32 identically to upstream TypeScript so a
// recorded seed reproduces the exact same train/test split byte-for-byte.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:297-306
func seededRandom(seed uint32) func() float64 {
	value := seed
	return func() float64 {
		value += 0x6d2b79f5
		t := value
		t = uint32(int32(t^(t>>15)) * int32(t|1))
		t ^= t + uint32(int32(t^(t>>7))*int32(t|61))
		return float64(t^(t>>14)) / 4294967296.0
	}
}

// SplitCases computes a random train/test split stratified by primary tag; frozen once a baseline exists.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:309-348
func SplitCases(state *State, cases map[string]string, testFraction *float64, seed *int) error {
	frac := 0.4
	if testFraction != nil {
		frac = *testFraction
	}
	if !(frac > 0 && frac < 1) {
		return newRatchetError("test_fraction must be between 0 and 1")
	}
	ids := make([]string, 0, len(cases))
	for id := range cases {
		ids = append(ids, id)
	}
	if len(ids) < 4 {
		return newRatchetError("split() needs at least 4 cases; score the whole set instead")
	}
	if len(state.Rounds) > 0 {
		return newRatchetError("The split is frozen once the baseline is gated; re-splitting would invalidate every round")
	}
	var actualSeed int
	if seed != nil {
		actualSeed = *seed
	} else {
		actualSeed = rand.IntN(1 << 31)
	}
	random := seededRandom(uint32(actualSeed))
	sortStringsUTF16(ids)

	// Preserve insertion order of tag buckets (first appearance in sorted ids),
	// matching JavaScript's Map iteration order.
	byTag := map[string][]string{}
	var tagOrder []string
	for _, id := range ids {
		tag := cases[id]
		if _, exists := byTag[tag]; !exists {
			tagOrder = append(tagOrder, tag)
		}
		byTag[tag] = append(byTag[tag], id)
	}
	train := []string{}
	test := []string{}
	for _, tag := range tagOrder {
		bucket := byTag[tag]
		for index := len(bucket) - 1; index > 0; index-- {
			swap := int(math.Floor(random() * float64(index+1)))
			bucket[index], bucket[swap] = bucket[swap], bucket[index]
		}
		testCount := jsRound(float64(len(bucket)) * frac)
		test = append(test, bucket[:testCount]...)
		train = append(train, bucket[testCount:]...)
	}
	if len(test) == 0 {
		test = append(test, train[len(train)-1])
		train = train[:len(train)-1]
	}
	if len(train) == 0 {
		train = append(train, test[len(test)-1])
		test = test[:len(test)-1]
	}
	sortStringsUTF16(train)
	sortStringsUTF16(test)
	state.TrainIDs = train
	state.TestIDs = test
	state.SplitSeed = &actualSeed
	return nil
}

// ---------------------------------------------------------------------------
// Approvals
// ---------------------------------------------------------------------------

// listFiles lists all files under absolute in deterministic lexicographical order.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:354-364
func listFiles(absolute string) ([]string, error) {
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{absolute}, nil
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareUTF16(entries[i].Name(), entries[j].Name()) < 0
	})
	var out []string
	for _, entry := range entries {
		nested, err := listFiles(filepath.Join(absolute, entry.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

// hashPaths computes the SHA-256 digest of the sorted path list and file contents.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:366-381
func hashPaths(cwd string, paths []string) (string, error) {
	sorted := append([]string(nil), paths...)
	sortStringsUTF16(sorted)
	h := sha256.New()
	for _, rel := range sorted {
		absolute := filepath.Join(cwd, filepath.FromSlash(rel))
		exists, err := pathExists(absolute)
		if err != nil {
			return "", err
		}
		if !exists {
			_, _ = io.WriteString(h, "missing:"+rel+"\x00")
			continue
		}
		files, err := listFiles(absolute)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			relFile, err := filepath.Rel(cwd, file)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(h, toPosix(relFile)+"\x00")
			content, err := os.ReadFile(file)
			if err != nil {
				return "", err
			}
			_, _ = h.Write(content)
			_, _ = h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// StageDigest returns the content SHA-256 digest an approval of stage is bound to.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:384-414
func StageDigest(cwd string, state *State, stage Stage) (string, error) {
	switch stage {
	case StageInputs:
		return hashPaths(cwd, state.CasesPaths)
	case StageGrader:
		return hashPaths(cwd, state.HarnessPaths)
	case StagePlan:
		if state.Goal == nil || state.Reps == nil || state.Command == nil || *state.Command == "" {
			return "", newRatchetError("The plan needs goal, reps, and command before it can be approved (call plan())")
		}
		if len(state.TrainIDs) == 0 || len(state.TestIDs) == 0 {
			return "", newRatchetError("The plan needs a train/test split before it can be approved (call split())")
		}
		// Build exact upstream canonical plan payload for SHA-256 hashing.
		modelNames := make([]string, 0, len(state.Prices))
		for m := range state.Prices {
			modelNames = append(modelNames, m)
		}
		sortStringsUTF16(modelNames)
		prices := make([][5]any, len(modelNames))
		for i, m := range modelNames {
			r := state.Prices[m]
			var cr, cw any
			if r.CacheRead != nil {
				cr = *r.CacheRead
			}
			if r.CacheWrite != nil {
				cw = *r.CacheWrite
			}
			prices[i] = [5]any{m, r.In, r.Out, cr, cw}
		}
		plan := struct {
			Goal        *Goal    `json:"goal"`
			Reps        int      `json:"reps"`
			Stop        StopRule `json:"stop"`
			Command     string   `json:"command"`
			ChangePaths []string `json:"change_paths"`
			OffLimits   []string `json:"off_limits"`
			TrainIDs    []string `json:"train_ids"`
			TestIDs     []string `json:"test_ids"`
			Prices      [][5]any `json:"prices"`
		}{
			Goal:        state.Goal,
			Reps:        *state.Reps,
			Stop:        state.Stop,
			Command:     *state.Command,
			ChangePaths: state.ChangePaths,
			OffLimits:   state.OffLimits,
			TrainIDs:    state.TrainIDs,
			TestIDs:     state.TestIDs,
			Prices:      prices,
		}
		raw, err := marshalJSCompact(plan)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:]), nil
	default:
		return "", newRatchetError("Unknown approval stage: %s", stage)
	}
}

// GetApprovalStatus checks each stage against its recorded digest.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:418-435
func GetApprovalStatus(cwd string, state *State) (ApprovalStatus, error) {
	status := make(ApprovalStatus, len(Stages))
	for _, stage := range Stages {
		recorded, ok := state.Approvals[stage]
		if !ok {
			status[stage] = "missing"
			continue
		}
		digest, err := StageDigest(cwd, state, stage)
		if err != nil {
			var rerr *Error
			if !errors.As(err, &rerr) {
				return nil, err
			}
			status[stage] = "stale"
			continue
		}
		if digest == recorded.SHA {
			status[stage] = "current"
		} else {
			status[stage] = "stale"
		}
	}
	return status, nil
}

// RequireApprovals verifies that all three stages have current approvals.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:437-446
func RequireApprovals(cwd string, state *State) error {
	status, err := GetApprovalStatus(cwd, state)
	if err != nil {
		return err
	}
	var blocked []string
	for _, stage := range Stages {
		if status[stage] != "current" {
			blocked = append(blocked, fmt.Sprintf("%s: %s", stage, status[stage]))
		}
	}
	if len(blocked) > 0 {
		return newRatchetError(
			"Approvals are not current (%s). A stale approval means its files or plan changed after the user approved them; re-run approve() for each stage.",
			strings.Join(blocked, ", "),
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Results, metrics, cost
// ---------------------------------------------------------------------------

type resultRow struct {
	PromptID string
	Rep      int
	Status   string
	Raw      map[string]any
}

type variantData struct {
	rows   []resultRow
	errors []resultRow
	traces []string
}

// parseJSONL parses newline-delimited JSON values, matching Bun.JSONL.parse
// (skipping blank lines / whitespace between JSON values).
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:465-471
func parseJSONL(text, file string) ([]any, error) {
	var out []any
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(trimmed))
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, newRatchetError("Malformed JSONL in %s: %v", file, err)
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			return nil, newRatchetError("Malformed JSONL in %s: unexpected trailing tokens", file)
		}
		out = append(out, v)
	}
	return out, nil
}

func readJSONL(file string) ([]any, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []any{}, nil
		}
		return nil, err
	}
	return parseJSONL(string(data), file)
}

func asRow(value any, file string) (resultRow, error) {
	m, ok := value.(map[string]any)
	if !ok || m == nil {
		return resultRow{}, newRatchetError("Every row in %s needs a string prompt_id", file)
	}
	promptID, ok := m["prompt_id"].(string)
	if !ok {
		return resultRow{}, newRatchetError("Every row in %s needs a string prompt_id", file)
	}
	repVal, hasRep := m["rep"]
	rep := 0
	if hasRep && repVal != nil {
		f, ok := repVal.(float64)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f < 0 {
			return resultRow{}, newRatchetError("Row %s in %s has an invalid rep", promptID, file)
		}
		rep = int(f)
	}
	copied := make(map[string]any, len(m)+2)
	for k, v := range m {
		copied[k] = v
	}
	copied["prompt_id"] = promptID
	copied["rep"] = rep
	status, _ := copied["status"].(string)
	return resultRow{
		PromptID: promptID,
		Rep:      rep,
		Status:   status,
		Raw:      copied,
	}, nil
}

func readVariant(dir, variant string) (*variantData, error) {
	variantDir := filepath.Join(dir, variant)
	resultsFile := filepath.Join(variantDir, "results.jsonl")
	exists, err := pathExists(resultsFile)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, newRatchetError("No results for %s: %s is missing", variant, resultsFile)
	}
	rawRows, err := readJSONL(resultsFile)
	if err != nil {
		return nil, err
	}
	rows := make([]resultRow, len(rawRows))
	for i, v := range rawRows {
		row, err := asRow(v, resultsFile)
		if err != nil {
			return nil, err
		}
		rows[i] = row
	}
	errorsFile := filepath.Join(variantDir, "errors.jsonl")
	rawErrors, err := readJSONL(errorsFile)
	if err != nil {
		return nil, err
	}
	errRows := make([]resultRow, len(rawErrors))
	for i, v := range rawErrors {
		row, err := asRow(v, errorsFile)
		if err != nil {
			return nil, err
		}
		errRows[i] = row
	}
	var traces []string
	entries, err := os.ReadDir(filepath.Join(variantDir, "traces"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else {
		traces = make([]string, len(entries))
		for i, e := range entries {
			traces[i] = e.Name()
		}
	}
	return &variantData{rows: rows, errors: errRows, traces: traces}, nil
}

func finiteNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func firstNumber(record map[string]any, keys []string) (float64, bool) {
	for _, k := range keys {
		if f, ok := finiteNumber(record[k]); ok {
			return f, true
		}
	}
	return 0, false
}

// NormalizeUsage normalizes Anthropic-, OpenAI-, and catalog-shaped usage blocks.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:518-539
func NormalizeUsage(raw any) *Usage {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	input, _ := firstNumber(m, []string{"input_tokens", "prompt_tokens", "input"})
	output, _ := firstNumber(m, []string{"output_tokens", "completion_tokens", "output"})
	cacheRead, _ := firstNumber(m, []string{"cache_read_input_tokens", "cache_read_tokens", "cacheRead"})
	cacheWrite, _ := firstNumber(m, []string{"cache_creation_input_tokens", "cache_write_tokens", "cacheWrite"})
	// OpenAI counts cached prompt tokens inside prompt_tokens.
	if _, hasPromptTokens := finiteNumber(m["prompt_tokens"]); hasPromptTokens {
		if details, ok := m["prompt_tokens_details"].(map[string]any); ok {
			if cached, ok := finiteNumber(details["cached_tokens"]); ok {
				cacheRead = cached
				input = math.Max(0, input-cached)
			}
		}
	}
	total := input + output + cacheRead + cacheWrite
	if total == 0 {
		return nil
	}
	return &Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: total,
	}
}

func calculateUsageCostTotal(cost ModelCost, usage Usage) float64 {
	// upstream: packages/catalog/src/models.ts:210-215 (flat rates, no cttl/orchestration on normalized usage)
	inputCost := float64(float64(cost.Input/1_000_000.0) * usage.Input)
	outputCost := float64(float64(cost.Output/1_000_000.0) * usage.Output)
	cacheReadCost := float64(float64(cost.CacheRead/1_000_000.0) * usage.CacheRead)
	cacheWriteCost := float64(float64(cost.CacheWrite/1_000_000.0) * usage.CacheWrite)
	return float64(float64(inputCost+outputCost)+cacheReadCost) + cacheWriteCost
}

func priceFor(state *State, lookup PriceLookup, model string) *ModelCost {
	if override, ok := state.Prices[model]; ok {
		cr := override.In
		if override.CacheRead != nil {
			cr = *override.CacheRead
		}
		cw := override.In
		if override.CacheWrite != nil {
			cw = *override.CacheWrite
		}
		return &ModelCost{
			Input:      override.In,
			Output:     override.Out,
			CacheRead:  cr,
			CacheWrite: cw,
		}
	}
	if lookup == nil {
		return nil
	}
	return lookup(model)
}

// rowCost computes row cost in USD: explicit `cost_usd`, else model x usage (+ judge);
// returns (0, false) when unpriceable.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:555-577
func rowCost(state *State, lookup PriceLookup, row resultRow, unpriced map[string]struct{}) (float64, bool) {
	if c, ok := finiteNumber(row.Raw["cost_usd"]); ok {
		return c, true
	}
	total := 0.0
	pairs := [2][2]string{
		{"model", "usage"},
		{"judge_model", "judge_usage"},
	}
	for _, pair := range pairs {
		modelKey, usageKey := pair[0], pair[1]
		usage := NormalizeUsage(row.Raw[usageKey])
		if usage == nil {
			if modelKey == "model" {
				return 0, false
			}
			continue
		}
		model, ok := row.Raw[modelKey].(string)
		if !ok {
			return 0, false
		}
		cost := priceFor(state, lookup, model)
		if cost == nil {
			if unpriced != nil {
				unpriced[model] = struct{}{}
			}
			return 0, false
		}
		total += calculateUsageCostTotal(*cost, *usage)
	}
	return total, true
}

func rowMetrics(row resultRow, cost *float64) map[string]float64 {
	metrics := map[string]float64{}
	truncated := row.Status == "truncated"
	for k, v := range row.Raw {
		if k == "rep" || k == "cost_usd" {
			continue
		}
		if f, ok := finiteNumber(v); ok {
			metrics[k] = f
		}
	}
	if !truncated {
		switch g := row.Raw["grade"].(type) {
		case bool:
			if g {
				metrics["score"] = 1
			} else {
				metrics["score"] = 0
			}
		case float64:
			if !math.IsNaN(g) && !math.IsInf(g, 0) {
				metrics["score"] = g
			}
		case map[string]any:
			for k, v := range g {
				switch gv := v.(type) {
				case bool:
					if gv {
						metrics[k] = 1
					} else {
						metrics[k] = 0
					}
				case float64:
					if !math.IsNaN(gv) && !math.IsInf(gv, 0) {
						metrics[k] = gv
					}
				}
			}
		}
	}
	if cost != nil {
		metrics["cost_usd"] = *cost
	}
	return metrics
}

type caseMeansMap map[string]map[string]float64

func computeCaseMeans(rows []resultRow, costs []*float64) caseMeansMap {
	type acc struct {
		sum   float64
		count int
	}
	sums := map[string]map[string]*acc{}
	for i, row := range rows {
		perCase, ok := sums[row.PromptID]
		if !ok {
			perCase = map[string]*acc{}
			sums[row.PromptID] = perCase
		}
		for metric, value := range rowMetrics(row, costs[i]) {
			entry, ok := perCase[metric]
			if !ok {
				entry = &acc{}
				perCase[metric] = entry
			}
			entry.sum += value
			entry.count++
		}
	}
	means := make(caseMeansMap, len(sums))
	for id, perCase := range sums {
		m := make(map[string]float64, len(perCase))
		for metric, entry := range perCase {
			m[metric] = entry.sum / float64(entry.count)
		}
		means[id] = m
	}
	return means
}

func summarize(means caseMeansMap, ids []string, metric string) SplitSummary {
	var values []float64
	for _, id := range ids {
		if m, ok := means[id]; ok {
			if v, ok := m[metric]; ok {
				values = append(values, v)
			}
		}
	}
	return meanAndHalf(values)
}

func paired(candidate, reference caseMeansMap, ids []string, metric string, direction Direction) PairedDelta {
	var diffs []float64
	for _, id := range ids {
		cm, okA := candidate[id]
		rm, okB := reference[id]
		if !okA || !okB {
			continue
		}
		a, hasA := cm[metric]
		b, hasB := rm[metric]
		if hasA && hasB {
			diffs = append(diffs, a-b)
		}
	}
	summary := meanAndHalf(diffs)
	verdict := VerdictFlat
	if summary.Mean != nil && summary.Half != nil {
		gain := *summary.Mean
		if direction == DirectionLower {
			gain = -gain
		}
		if gain-*summary.Half > 0 {
			verdict = VerdictUp
		} else if gain+*summary.Half < 0 {
			verdict = VerdictDown
		}
	}
	return PairedDelta{
		N:       summary.N,
		Mean:    summary.Mean,
		Half:    summary.Half,
		Verdict: verdict,
	}
}

// ---------------------------------------------------------------------------
// Check and gate
// ---------------------------------------------------------------------------

func expectedVariant(state *State) string {
	if len(state.Rounds) == 0 {
		return "baseline"
	}
	last := state.Rounds[len(state.Rounds)-1]
	return fmt.Sprintf("v%d", last.Round+1)
}

// CheckVariant preflights a round: approvals current, inputs unchanged since baseline,
// variant next in sequence, and command resolved.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:675-700
func CheckVariant(cwd string, state *State, variant string) (*CheckResult, error) {
	if !variantNameRe.MatchString(variant) {
		return nil, newRatchetError(`Variant must be "baseline" or v<N>, got %s`, variant)
	}
	if err := RequireApprovals(cwd, state); err != nil {
		return nil, err
	}
	if state.BaselineInputsSHA != nil {
		digest, err := StageDigest(cwd, state, StageInputs)
		if err != nil {
			return nil, err
		}
		if digest != *state.BaselineInputsSHA {
			return nil, newRatchetError(
				"The cases changed after the baseline was gated; the frozen split and baseline no longer cover them. Start a new flow with init() under a new name.",
			)
		}
	}
	rerun := false
	if len(state.Rounds) > 0 {
		last := state.Rounds[len(state.Rounds)-1]
		rerun = last.Variant == variant && last.Decision == DecisionRerun
	}
	expected := expectedVariant(state)
	if !rerun && variant != expected {
		return nil, newRatchetError("Next variant is %s; got %s", expected, variant)
	}
	dir, err := FlowDir(cwd, state.Flow)
	if err != nil {
		return nil, err
	}
	relFlow, err := filepath.Rel(cwd, dir)
	if err != nil {
		return nil, err
	}
	relOut, err := filepath.Rel(cwd, filepath.Join(dir, variant))
	if err != nil {
		return nil, err
	}
	outDir := toPosix(relOut)
	cases := len(state.TrainIDs) + len(state.TestIDs)
	reps := *state.Reps
	command := strings.ReplaceAll(*state.Command, "{variant}", variant)
	command = strings.ReplaceAll(command, "{flow_dir}", toPosix(relFlow))
	return &CheckResult{
		Variant:      variant,
		Command:      command,
		Cases:        cases,
		Reps:         reps,
		ExpectedRows: cases * reps,
		OutDir:       outDir,
	}, nil
}

func formatSummary(summary SplitSummary, has bool, metric string) string {
	if !has || summary.Mean == nil {
		return "–"
	}
	digits := 3
	if metric == "cost_usd" {
		digits = 4
	}
	mean := jsToFixed(*summary.Mean, digits)
	if summary.Half == nil {
		return mean
	}
	return fmt.Sprintf("%s ±%s", mean, jsToFixed(*summary.Half, digits))
}

// RenderStatusTable formats the markdown status table for state.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:709-725
func RenderStatusTable(state *State) string {
	goal := state.Goal
	if goal == nil || len(state.Rounds) == 0 {
		return "(no rounds gated yet)"
	}
	metrics := []string{goal.Target}
	for _, m := range goal.Hold {
		if m != "cost_usd" {
			metrics = append(metrics, m)
		}
	}
	metrics = append(metrics, "cost_usd")

	header := []string{"round", "variant", "change", "decision"}
	for _, m := range metrics {
		header = append(header, "train "+m, "test "+m)
	}
	dashes := make([]string, len(header))
	for i := range dashes {
		dashes[i] = "---"
	}
	lines := []string{
		"| " + strings.Join(header, " | ") + " |",
		"|" + strings.Join(dashes, "|") + "|",
	}
	for _, round := range state.Rounds {
		best := ""
		if state.Best != nil && state.Best.Variant == round.Variant {
			best = " ★"
		}
		cells := []string{
			strconv.Itoa(round.Round),
			round.Variant + best,
			strings.ReplaceAll(round.Change, "|", `\|`),
			string(round.Decision),
		}
		for _, m := range metrics {
			tr, hasTr := round.Train[m]
			te, hasTe := round.Test[m]
			cells = append(cells, formatSummary(tr, hasTr, m), formatSummary(te, hasTe, m))
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

// GateVariant validates completeness and held-out isolation, computes paired
// case-level deltas vs the best round, records the round, and writes `status.md`.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:727-936
func GateVariant(cwd string, state *State, variant, change string, lookup PriceLookup) (*GateResult, error) {
	if _, err := CheckVariant(cwd, state, variant); err != nil {
		return nil, err
	}
	goal := *state.Goal
	reps := *state.Reps
	dir, err := FlowDir(cwd, state.Flow)
	if err != nil {
		return nil, err
	}
	data, err := readVariant(dir, variant)
	if err != nil {
		return nil, err
	}
	splitIDs := make([]string, 0, len(state.TrainIDs)+len(state.TestIDs))
	splitIDs = append(splitIDs, state.TrainIDs...)
	splitIDs = append(splitIDs, state.TestIDs...)
	known := make(map[string]struct{}, len(splitIDs))
	for _, id := range splitIDs {
		known[id] = struct{}{}
	}
	warnings := []string{}

	// Completeness & duplicate detection.
	slotKey := func(row resultRow) string { return fmt.Sprintf("%s#%d", row.PromptID, row.Rep) }
	resultSlots := map[string]struct{}{}
	dupOrder := []string{}
	dupSeen := map[string]struct{}{}
	for _, row := range data.rows {
		key := slotKey(row)
		if _, exists := resultSlots[key]; exists {
			if _, recorded := dupSeen[key]; !recorded {
				dupSeen[key] = struct{}{}
				dupOrder = append(dupOrder, key)
			}
		}
		resultSlots[key] = struct{}{}
	}
	if len(dupOrder) > 0 {
		shown := dupOrder
		if len(shown) > 10 {
			shown = shown[:10]
		}
		return nil, newRatchetError(
			"%s/results.jsonl has duplicate (case, rep) rows (%s); keep one row per slot and re-gate",
			variant, strings.Join(shown, ", "),
		)
	}
	errorSlots := make(map[string]struct{}, len(data.errors))
	for _, row := range data.errors {
		errorSlots[slotKey(row)] = struct{}{}
	}
	var missing []string
	var erroredOnly []string
	for _, id := range splitIDs {
		for rep := range reps {
			key := fmt.Sprintf("%s#%d", id, rep)
			if _, ok := resultSlots[key]; ok {
				continue
			}
			if _, ok := errorSlots[key]; ok {
				erroredOnly = append(erroredOnly, key)
			} else {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		shown := missing
		ellipsis := ""
		if len(shown) > 10 {
			shown = shown[:10]
			ellipsis = ", …"
		}
		return nil, newRatchetError(
			"%s is incomplete: %d (case, rep) slots have no result or error row (%s%s)",
			variant, len(missing), strings.Join(shown, ", "), ellipsis,
		)
	}
	if variant == "baseline" && len(erroredOnly) > 0 {
		shown := erroredOnly
		if len(shown) > 10 {
			shown = shown[:10]
		}
		return nil, newRatchetError(
			"baseline has %d slots that only errored (%s); fix the harness and resume the run until every slot is scored",
			len(erroredOnly), strings.Join(shown, ", "),
		)
	}

	// Held-out isolation: no transcript may exist for a test case.
	testIDs := make(map[string]struct{}, len(state.TestIDs))
	for _, id := range state.TestIDs {
		testIDs[id] = struct{}{}
	}
	var leaked []string
	for _, file := range data.traces {
		m := traceFileRe.FindStringSubmatch(file)
		if m != nil {
			if _, ok := testIDs[m[1]]; ok {
				leaked = append(leaked, file)
			}
		}
	}
	if len(leaked) > 0 {
		shown := leaked
		if len(shown) > 5 {
			shown = shown[:5]
		}
		return nil, newRatchetError(
			"%s/traces holds transcripts for test cases (%s). The runner must write traces for train cases only; delete them, fix the runner, and re-run.",
			variant, strings.Join(shown, ", "),
		)
	}

	strays := map[string]struct{}{}
	var scored []resultRow
	for _, row := range data.rows {
		if _, ok := known[row.PromptID]; !ok {
			strays[row.PromptID] = struct{}{}
			continue
		}
		scored = append(scored, row)
	}
	if len(strays) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d result ids are outside the split and were ignored", len(strays)))
	}

	unpricedSet := map[string]struct{}{}
	var unpricedOrder []string
	costs := make([]*float64, len(scored))
	for i, row := range scored {
		rowUnpriced := map[string]struct{}{}
		if c, ok := rowCost(state, lookup, row, rowUnpriced); ok {
			costs[i] = new(c)
		}
		for m := range rowUnpriced {
			if _, seen := unpricedSet[m]; !seen {
				unpricedSet[m] = struct{}{}
				unpricedOrder = append(unpricedOrder, m)
			}
		}
	}
	if len(unpricedOrder) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"No catalog price for %s; add prices via plan(prices={model: {in, out}}) to report cost",
			strings.Join(unpricedOrder, ", "),
		))
	}

	means := computeCaseMeans(scored, costs)
	metrics := uniqueStrings(append(append([]string{goal.Target}, goal.Hold...), "cost_usd"))
	train := make(map[string]SplitSummary, len(metrics))
	test := make(map[string]SplitSummary, len(metrics))
	for _, metric := range metrics {
		train[metric] = summarize(means, state.TrainIDs, metric)
		test[metric] = summarize(means, state.TestIDs, metric)
	}
	if train[goal.Target].N == 0 && test[goal.Target].N == 0 {
		return nil, newRatchetError(
			`No row carries the target metric "%s". Targets are grade-dict keys, "score" for a bool/number grade, "cost_usd", or a numeric row field.`,
			goal.Target,
		)
	}

	truncated := 0
	for _, row := range scored {
		if row.Status == "truncated" {
			truncated++
		}
	}
	if truncated > 0 {
		warnings = append(warnings, fmt.Sprintf("%d truncated rows excluded from quality means", truncated))
	}
	if len(data.errors) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d infra error rows recorded in errors.jsonl", len(data.errors)))
	}

	// Guardrail measurability check.
	measuredSummary := func(s SplitSummary, ok bool) bool { return ok && s.N >= 2 }
	var unmeasured []string
	for _, metric := range goal.Hold {
		tr, hasTr := train[metric]
		te, hasTe := test[metric]
		if !measuredSummary(tr, hasTr) || !measuredSummary(te, hasTe) {
			unmeasured = append(unmeasured, metric)
		}
	}
	if len(unmeasured) > 0 {
		hint := ""
		if len(unpricedOrder) > 0 {
			hint = " (unpriced models; add plan(prices=…))"
		}
		return nil, newRatchetError(
			"Guardrail %s has fewer than 2 measured cases on train or test in %s%s; the gate cannot hold a guardrail it cannot measure",
			strings.Join(unmeasured, ", "), variant, hint,
		)
	}

	rerun := false
	roundNumber := 0
	if len(state.Rounds) > 0 {
		last := state.Rounds[len(state.Rounds)-1]
		if last.Variant == variant && last.Decision == DecisionRerun {
			rerun = true
			roundNumber = last.Round
		} else {
			roundNumber = last.Round + 1
		}
	}
	reasons := []string{}
	deltas := map[string]MetricDeltas{}
	var decision RoundDecision

	if variant == "baseline" {
		decision = DecisionBaseline
		target := test[goal.Target]
		if target.Half != nil {
			reasons = append(reasons, fmt.Sprintf("test noise floor on %s: ±%s", goal.Target, jsToFixed(*target.Half, 3)))
		}
		overall := summarize(means, splitIDs, goal.Target)
		unitInterval := true
		for i, row := range scored {
			if v, ok := rowMetrics(row, costs[i])[goal.Target]; ok {
				if v < 0 || v > 1 {
					unitInterval = false
					break
				}
			}
		}
		if goal.Direction == DirectionHigher && unitInterval && overall.Mean != nil && *overall.Mean >= 0.95 {
			warnings = append(warnings, fmt.Sprintf(
				"Baseline %s is %s: near ceiling; aim the goal at cost or latency, or add harder cases",
				goal.Target, jsToFixed(*overall.Mean, 3),
			))
		}
		trainTarget := train[goal.Target]
		if trainTarget.Mean != nil && target.Mean != nil && trainTarget.Half != nil && target.Half != nil {
			gap := math.Abs(*trainTarget.Mean - *target.Mean)
			if gap > math.Hypot(*trainTarget.Half, *target.Half) {
				warnings = append(warnings, fmt.Sprintf(
					"Train and test %s means differ beyond noise; the split may be unrepresentative",
					goal.Target,
				))
			}
		}
	} else if len(erroredOnly) > 0 {
		decision = DecisionRerun
		shown := erroredOnly
		if len(shown) > 5 {
			shown = shown[:5]
		}
		reasons = append(reasons, fmt.Sprintf(
			"%d slots only errored (%s); resume %s until every slot is scored, then gate it again",
			len(erroredOnly), strings.Join(shown, ", "), variant,
		))
	} else {
		if state.Best == nil || state.Best.Variant == "" {
			return nil, newRatchetError("Cannot gate %s: flow %q has no baseline/best round recorded", variant, state.Flow)
		}
		reference := state.Best.Variant
		referenceData, err := readVariant(dir, reference)
		if err != nil {
			return nil, err
		}
		var referenceRows []resultRow
		for _, row := range referenceData.rows {
			if _, ok := known[row.PromptID]; ok {
				referenceRows = append(referenceRows, row)
			}
		}
		refCosts := make([]*float64, len(referenceRows))
		for i, row := range referenceRows {
			if c, ok := rowCost(state, lookup, row, nil); ok {
				refCosts[i] = new(c)
			}
		}
		referenceMeans := computeCaseMeans(referenceRows, refCosts)
		for _, metric := range metrics {
			direction := MetricDirection(goal, metric)
			deltas[metric] = MetricDeltas{
				Train: paired(means, referenceMeans, state.TrainIDs, metric, direction),
				Test:  paired(means, referenceMeans, state.TestIDs, metric, direction),
			}
		}
		var unpaired []string
		for _, metric := range goal.Hold {
			d := deltas[metric]
			if d.Train.N < 2 || d.Test.N < 2 {
				unpaired = append(unpaired, metric)
			}
		}
		if len(unpaired) > 0 {
			return nil, newRatchetError(
				"Guardrail %s has fewer than 2 cases measured in both %s and %s on train or test; the gate cannot compare it",
				strings.Join(unpaired, ", "), variant, reference,
			)
		}
		var regressed []string
		for _, metric := range goal.Hold {
			d := deltas[metric]
			if d.Train.Verdict == VerdictDown || d.Test.Verdict == VerdictDown {
				regressed = append(regressed, metric)
			}
		}
		target := deltas[goal.Target]
		switch {
		case len(regressed) > 0:
			decision = DecisionRevert
			reasons = append(reasons, fmt.Sprintf("guardrail regressed vs %s: %s", reference, strings.Join(regressed, ", ")))
		case target.Train.Verdict == VerdictDown || target.Test.Verdict == VerdictDown:
			decision = DecisionRevert
			reasons = append(reasons, fmt.Sprintf("%s regressed vs %s", goal.Target, reference))
		case target.Train.Verdict == VerdictUp && target.Test.Verdict == VerdictUp:
			decision = DecisionKeep
			reasons = append(reasons, fmt.Sprintf("%s improved on train and test vs %s", goal.Target, reference))
		case target.Train.Verdict == VerdictUp:
			decision = DecisionRevert
			reasons = append(reasons, fmt.Sprintf("overfit suspect: train improved but test is flat vs %s", reference))
		case target.Test.Verdict == VerdictUp:
			decision = DecisionRerun
			reasons = append(reasons, fmt.Sprintf(
				"test improved but train did not; append reps to %s and %s, then gate %s again",
				variant, reference, variant,
			))
		default:
			decision = DecisionRevert
			reasons = append(reasons, fmt.Sprintf("no change outside noise vs %s", reference))
		}
	}

	roundChange := change
	if variant == "baseline" {
		roundChange = "baseline"
	}
	round := Round{
		Round:     roundNumber,
		Variant:   variant,
		Change:    roundChange,
		Decision:  decision,
		Reasons:   reasons,
		Train:     train,
		Test:      test,
		Errors:    len(data.errors),
		Truncated: truncated,
		At:        time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if rerun {
		state.Rounds[len(state.Rounds)-1] = round
	} else {
		state.Rounds = append(state.Rounds, round)
	}
	state.CurrentRound = roundNumber
	if decision == DecisionBaseline {
		digest, err := StageDigest(cwd, state, StageInputs)
		if err != nil {
			return nil, err
		}
		state.BaselineInputsSHA = &digest
	}
	if decision == DecisionBaseline || decision == DecisionKeep {
		var testScore *float64
		if test[goal.Target].Mean != nil {
			v := *test[goal.Target].Mean
			testScore = &v
		}
		state.Best = &BestRecord{
			Round:     roundNumber,
			Variant:   variant,
			TestScore: testScore,
		}
		state.FlatRounds = 0
	} else if decision == DecisionRevert {
		state.FlatRounds++
	}
	plateau := state.FlatRounds >= state.Stop.Plateau
	climbed := 0
	for _, entry := range state.Rounds {
		if entry.Variant != "baseline" {
			climbed++
		}
	}
	done := plateau || (state.Stop.Rounds != nil && climbed >= *state.Stop.Rounds)
	if err := SaveState(cwd, state); err != nil {
		return nil, err
	}
	table := RenderStatusTable(state)
	statusContent := fmt.Sprintf("# %s\n\n%s\n", state.Flow, table)
	if err := os.WriteFile(filepath.Join(dir, "status.md"), []byte(statusContent), 0o644); err != nil {
		return nil, err
	}
	return &GateResult{
		Variant:  variant,
		Decision: decision,
		Reasons:  reasons,
		Warnings: warnings,
		Deltas:   deltas,
		Best:     state.Best,
		Plateau:  plateau,
		Done:     done,
		Table:    table,
	}, nil
}

// ---------------------------------------------------------------------------
// Analyzer view
// ---------------------------------------------------------------------------

// GetTrainView returns everything the analyzer may read for a round: train rows,
// train trace paths, and train score history across gated rounds.
// upstream: packages/coding-agent/src/ratchet/ratchet.ts:951-980
func GetTrainView(cwd string, state *State, variant string, lookup PriceLookup) (*TrainView, error) {
	if state.Goal == nil {
		return nil, newRatchetError("Set a goal with plan() before reading the train view")
	}
	dir, err := FlowDir(cwd, state.Flow)
	if err != nil {
		return nil, err
	}
	trainIDs := make(map[string]struct{}, len(state.TrainIDs))
	for _, id := range state.TrainIDs {
		trainIDs[id] = struct{}{}
	}
	data, err := readVariant(dir, variant)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, row := range data.rows {
		if _, ok := trainIDs[row.PromptID]; ok {
			rows = append(rows, row.Raw)
		}
	}
	relDir, err := filepath.Rel(cwd, dir)
	if err != nil {
		return nil, err
	}
	relFlow := toPosix(relDir)
	traces := []string{}
	for _, file := range data.traces {
		m := traceFileRe.FindStringSubmatch(file)
		if m != nil {
			if _, ok := trainIDs[m[1]]; ok {
				traces = append(traces, path.Join(relFlow, variant, "traces", file))
			}
		}
	}
	history := make(map[string]map[string]*float64, len(state.TrainIDs))
	for _, id := range state.TrainIDs {
		history[id] = map[string]*float64{}
	}
	for _, round := range state.Rounds {
		roundData, err := readVariant(dir, round.Variant)
		if err != nil {
			return nil, err
		}
		var roundRows []resultRow
		for _, row := range roundData.rows {
			if _, ok := trainIDs[row.PromptID]; ok {
				roundRows = append(roundRows, row)
			}
		}
		roundCosts := make([]*float64, len(roundRows))
		for i, row := range roundRows {
			if c, ok := rowCost(state, lookup, row, nil); ok {
				roundCosts[i] = new(c)
			}
		}
		means := computeCaseMeans(roundRows, roundCosts)
		for _, id := range state.TrainIDs {
			if m, ok := means[id]; ok {
				if v, ok := m[state.Goal.Target]; ok {
					history[id][round.Variant] = new(v)
					continue
				}
			}
			history[id][round.Variant] = nil
		}
	}
	return &TrainView{
		Variant: variant,
		Rows:    rows,
		Traces:  traces,
		History: history,
	}, nil
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
