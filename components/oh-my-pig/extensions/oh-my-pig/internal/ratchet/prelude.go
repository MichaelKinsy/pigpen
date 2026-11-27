package ratchet

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts,
//           packages/coding-agent/src/ratchet/prelude.js,
//           packages/coding-agent/src/ratchet/prelude.py,
//           packages/coding-agent/src/prompts/tools/ratchet.md

//go:embed assets/ratchet.md
var ratchetDocumentation string

//go:embed assets/prelude.js
var ratchetJavascript string

//go:embed assets/prelude.py
var ratchetPython string

// SettingRatchetEnabled matches omp's cfgRatchetEnabled ("ratchet.enabled", default false).
// upstream: packages/coding-agent/src/tools/settings.ts:720-730
const SettingRatchetEnabled = "ratchet.enabled"

// Setting reads a setting key for ctx (wired by wiring.go -> settings.Any).
var Setting func(ctx sdk.Context, key string) any

// SetSessionOverride updates a setting override for ctx's session without persisting to disk.
// Wired by wiring.go -> func(ctx, key, val) { return settings.Set(ctx, key, val, false) }.
var SetSessionOverride func(ctx sdk.Context, key string, value any) error

// ApprovalOutcome is the structured result returned by `approve()`.
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:132
type ApprovalOutcome struct {
	Approved bool    `json:"approved"`
	Already  *bool   `json:"already,omitempty"`
	Aborted  *bool   `json:"aborted,omitempty"`
	Feedback *string `json:"feedback,omitempty"`
}

// ApprovalPrompter is an optional hook (used by tests or native frontends) to
// override the default `ctx.Select` / `ctx.InputWithoutPlaceholder` approval dialog.
// When nil, askApproval uses PiG's interactive Select/Input dialogs matching
// upstream's fallback path (`prelude-definition.ts:178-183`).
type ApprovalPrompter func(ctx context.Context, sdkCtx sdk.Context, stage Stage, question, preview string) (ApprovalOutcome, error)

// ModelCatalogLister returns raw model objects (matching `sdk.ModelRegistry.GetAll()`)
// for pricing lookups. When nil, catalogPriceLookup queries `ctx.ModelRegistry().GetAll()`.
type ModelCatalogLister func(ctx sdk.Context) ([]map[string]any, error)

// PreludeOptions configures PreludeDefinition (primarily for tests).
type PreludeOptions struct {
	Prompter     ApprovalPrompter
	ModelLister  ModelCatalogLister
	CwdOverride  string
	EnabledCheck func(ctx sdk.Context) bool
}

// RatchetApproval returns the approval tier for one ratchet invocation:
// read-only actions ("status", "check", "train") -> "read"; everything else
// (including invalid/unparseable payloads) -> "write".
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:84-88
func RatchetApproval(raw json.RawMessage) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return "write"
	}
	act, ok := obj["action"].(string)
	if !ok {
		return "write"
	}
	if act == "status" || act == "check" || act == "train" {
		return "read"
	}
	return "write"
}

func ratchetEnabled(ctx sdk.Context) bool {
	if Setting != nil {
		if b, ok := Setting(ctx, SettingRatchetEnabled).(bool); ok {
			return b
		}
		return false
	}
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	var enabled bool
	func() {
		defer func() { _ = recover() }()
		if m, err := ctx.GetSettings(); err == nil && m != nil {
			if raw, ok := m[SettingRatchetEnabled]; ok {
				if b, ok := raw.(bool); ok {
					enabled = b
				}
			} else if sub, ok := m["ratchet"].(map[string]any); ok {
				if b, ok := sub["enabled"].(bool); ok {
					enabled = b
				}
			}
		}
	}()
	return enabled
}

// ---------------------------------------------------------------------------
// Per-flow serialization lock
// ---------------------------------------------------------------------------

// flowLockManager serializes read-modify-write of one flow's `_state.json`
// within this process so concurrent calls (e.g. two approvals) never overwrite
// each other's update.
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:115-130
type flowLockEntry struct {
	mu   sync.Mutex
	refs int
}

var (
	flowLocksMu sync.Mutex
	flowLocks   = map[string]*flowLockEntry{}
)

func withFlowLock[T any](key string, fn func() (T, error)) (T, error) {
	flowLocksMu.Lock()
	entry, ok := flowLocks[key]
	if !ok {
		entry = &flowLockEntry{}
		flowLocks[key] = entry
	}
	entry.refs++
	flowLocksMu.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		flowLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 && flowLocks[key] == entry {
			delete(flowLocks, key)
		}
		flowLocksMu.Unlock()
	}()
	return fn()
}

// ---------------------------------------------------------------------------
// Catalog price lookup
// ---------------------------------------------------------------------------

// catalogPriceLookup resolves a served model id from the host's model registry.
// A served id resolves only when every registry entry with that id (bare or
// `provider/id`) carries the same non-zero rate card. A zero-cost subscription
// entry or conflicting providers leave the id unpriced rather than guessing.
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:96-109
func catalogPriceLookup(ctx sdk.Context, lister ModelCatalogLister) PriceLookup {
	var (
		mu       sync.Mutex
		loaded   bool
		models   []map[string]any
		cache    = map[string]*ModelCost{}
		cachedOK = map[string]bool{}
	)
	loadModels := func() []map[string]any {
		if loaded {
			return models
		}
		loaded = true
		if lister != nil {
			if m, err := lister(ctx); err == nil {
				models = m
			}
			return models
		}
		var zero sdk.Context
		if ctx == zero {
			return nil
		}
		func() {
			defer func() { _ = recover() }()
			if m, err := ctx.ModelRegistry().GetAll(); err == nil {
				models = m
			}
		}()
		return models
	}
	return func(model string) *ModelCost {
		mu.Lock()
		defer mu.Unlock()
		if cachedOK[model] {
			return cache[model]
		}
		all := loadModels()
		type candidate struct {
			cost ModelCost
			key  string
		}
		var matches []candidate
		for _, entry := range all {
			id, _ := entry["id"].(string)
			provider, _ := entry["provider"].(string)
			if id != model && (provider == "" || provider+"/"+id != model) {
				continue
			}
			costMap, _ := entry["cost"].(map[string]any)
			mc := extractModelCost(costMap)
			keyBytes, _ := json.Marshal(costMap)
			matches = append(matches, candidate{cost: mc, key: string(keyBytes)})
		}
		var resolved *ModelCost
		if len(matches) > 0 {
			allPriced := true
			distinct := map[string]struct{}{}
			for _, c := range matches {
				if !(c.cost.Input > 0 || c.cost.Output > 0) {
					allPriced = false
					break
				}
				distinct[c.key] = struct{}{}
			}
			if allPriced && len(distinct) == 1 {
				mc := matches[0].cost
				resolved = &mc
			}
		}
		cache[model] = resolved
		cachedOK[model] = true
		return resolved
	}
}

func extractModelCost(costMap map[string]any) ModelCost {
	if costMap == nil {
		return ModelCost{}
	}
	in, _ := finiteNumber(costMap["input"])
	out, _ := finiteNumber(costMap["output"])
	cr, _ := finiteNumber(costMap["cacheRead"])
	cw, _ := finiteNumber(costMap["cacheWrite"])
	return ModelCost{
		Input:      in,
		Output:     out,
		CacheRead:  cr,
		CacheWrite: cw,
	}
}

// ---------------------------------------------------------------------------
// Strict parameter validation (matches upstream omptype paramsSchema)
// ---------------------------------------------------------------------------

// parsedParams holds validated arguments for one ratchet prelude call.
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:32-82
type parsedParams struct {
	flow   string
	action string

	// init
	cases     []string
	harness   []string
	change    []string
	offLimits []string
	command   *string

	// plan
	goal   *PlanGoalInput
	reps   *int
	stop   *PlanStopInput
	prices map[string]Price

	// split
	splitCases   map[string]string
	testFraction *float64
	seed         *int

	// approve
	stage    Stage
	question string
	preview  string

	// check / train / gate
	variant    string
	gateChange string
}

func parseParams(raw json.RawMessage) (*parsedParams, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("ratchet received invalid arguments: expected an object")
	}
	flow, ok := m["flow"].(string)
	if !ok || len(flow) == 0 {
		return nil, errors.New("ratchet received invalid arguments: flow must be a non-empty string")
	}
	action, ok := m["action"].(string)
	if !ok || len(action) == 0 {
		return nil, errors.New("ratchet received invalid arguments: action must be a string")
	}

	p := &parsedParams{flow: flow, action: action}

	switch action {
	case "init":
		cases, err := requireStringSlice(m, "cases")
		if err != nil {
			return nil, err
		}
		harness, err := requireStringSlice(m, "harness")
		if err != nil {
			return nil, err
		}
		change, err := requireStringSlice(m, "change")
		if err != nil {
			return nil, err
		}
		p.cases = cases
		p.harness = harness
		p.change = change
		if rawOL, exists := m["off_limits"]; exists {
			ol, err := asStringSlice(rawOL, "off_limits")
			if err != nil {
				return nil, err
			}
			p.offLimits = ol
		}
		if rawCmd, exists := m["command"]; exists {
			cmd, err := asNonEmptyString(rawCmd, "command")
			if err != nil {
				return nil, err
			}
			p.command = &cmd
		}
		return p, nil

	case "plan":
		if rawGoal, exists := m["goal"]; exists {
			gm, ok := rawGoal.(map[string]any)
			if !ok || gm == nil {
				return nil, errors.New("ratchet received invalid arguments: goal must be an object")
			}
			target, err := asNonEmptyString(gm["target"], "goal.target")
			if err != nil {
				return nil, err
			}
			goal := &PlanGoalInput{Target: target}
			if rawDir, ok := gm["direction"]; ok {
				dir, err := asDirection(rawDir, "goal.direction")
				if err != nil {
					return nil, err
				}
				goal.Direction = dir
			}
			if rawHold, ok := gm["hold"]; ok {
				hold, err := asStringSlice(rawHold, "goal.hold")
				if err != nil {
					return nil, err
				}
				goal.Hold = hold
			}
			if rawDirs, ok := gm["directions"]; ok {
				od, err := parseOrderedDirectionsFromRaw(raw, rawDirs)
				if err != nil {
					return nil, err
				}
				goal.Directions = od
			}
			p.goal = goal
		}
		if rawReps, exists := m["reps"]; exists {
			n, err := asIntegerMin(rawReps, 1, "reps")
			if err != nil {
				return nil, err
			}
			p.reps = &n
		}
		if rawStop, exists := m["stop"]; exists {
			sm, ok := rawStop.(map[string]any)
			if !ok || sm == nil {
				return nil, errors.New("ratchet received invalid arguments: stop must be an object")
			}
			stop := &PlanStopInput{}
			if rawP, ok := sm["plateau"]; ok {
				n, err := asIntegerMin(rawP, 2, "stop.plateau")
				if err != nil {
					return nil, err
				}
				stop.Plateau = &n
			}
			if rawR, ok := sm["rounds"]; ok {
				n, err := asIntegerMin(rawR, 1, "stop.rounds")
				if err != nil {
					return nil, err
				}
				stop.Rounds = &n
			}
			p.stop = stop
		}
		if rawCmd, exists := m["command"]; exists {
			cmd, err := asNonEmptyString(rawCmd, "command")
			if err != nil {
				return nil, err
			}
			p.command = &cmd
		}
		if rawPrices, exists := m["prices"]; exists {
			pm, ok := rawPrices.(map[string]any)
			if !ok || pm == nil {
				return nil, errors.New("ratchet received invalid arguments: prices must be an object")
			}
			prices := make(map[string]Price, len(pm))
			for model, rawRate := range pm {
				rm, ok := rawRate.(map[string]any)
				if !ok || rm == nil {
					return nil, fmt.Errorf("ratchet received invalid arguments: prices.%s must be an object", model)
				}
				inRate, err := asNonNegNumber(rm["in"], "prices."+model+".in")
				if err != nil {
					return nil, err
				}
				outRate, err := asNonNegNumber(rm["out"], "prices."+model+".out")
				if err != nil {
					return nil, err
				}
				rate := Price{In: inRate, Out: outRate}
				if rawCR, ok := rm["cache_read"]; ok {
					cr, err := asNonNegNumber(rawCR, "prices."+model+".cache_read")
					if err != nil {
						return nil, err
					}
					rate.CacheRead = &cr
				}
				if rawCW, ok := rm["cache_write"]; ok {
					cw, err := asNonNegNumber(rawCW, "prices."+model+".cache_write")
					if err != nil {
						return nil, err
					}
					rate.CacheWrite = &cw
				}
				prices[model] = rate
			}
			p.prices = prices
		}
		return p, nil

	case "split":
		rawCases, exists := m["cases"]
		if !exists {
			return nil, errors.New("ratchet received invalid arguments: cases must be an object mapping case ids to tags")
		}
		cm, ok := rawCases.(map[string]any)
		if !ok || cm == nil {
			return nil, errors.New("ratchet received invalid arguments: cases must be an object mapping case ids to tags")
		}
		cases := make(map[string]string, len(cm))
		for k, v := range cm {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("ratchet received invalid arguments: cases.%s must be a string", k)
			}
			cases[k] = s
		}
		p.splitCases = cases
		if rawTF, exists := m["test_fraction"]; exists {
			f, ok := finiteNumber(rawTF)
			if !ok || !(f > 0 && f < 1) {
				return nil, errors.New("ratchet received invalid arguments: test_fraction must be a number between 0 and 1")
			}
			p.testFraction = &f
		}
		if rawSeed, exists := m["seed"]; exists {
			n, err := asIntegerMin(rawSeed, 0, "seed")
			if err != nil {
				return nil, err
			}
			p.seed = &n
		}
		return p, nil

	case "approve":
		stageStr, ok := m["stage"].(string)
		if !ok || (stageStr != string(StageInputs) && stageStr != string(StageGrader) && stageStr != string(StagePlan)) {
			return nil, errors.New(`ratchet received invalid arguments: stage must be "inputs", "grader", or "plan"`)
		}
		q, err := asNonEmptyString(m["question"], "question")
		if err != nil {
			return nil, err
		}
		prev, err := asNonEmptyString(m["preview"], "preview")
		if err != nil {
			return nil, err
		}
		p.stage = Stage(stageStr)
		p.question = q
		p.preview = prev
		return p, nil

	case "check", "train":
		variant, err := asNonEmptyString(m["variant"], "variant")
		if err != nil {
			return nil, err
		}
		p.variant = variant
		return p, nil

	case "gate":
		variant, err := asNonEmptyString(m["variant"], "variant")
		if err != nil {
			return nil, err
		}
		p.variant = variant
		if rawChange, exists := m["change"]; exists {
			ch, ok := rawChange.(string)
			if !ok {
				return nil, errors.New("ratchet received invalid arguments: change must be a string")
			}
			p.gateChange = ch
		}
		return p, nil

	case "status":
		return p, nil

	default:
		return nil, fmt.Errorf(`ratchet received invalid arguments: unknown action %q`, action)
	}
}

func requireStringSlice(m map[string]any, field string) ([]string, error) {
	raw, exists := m[field]
	if !exists {
		return nil, fmt.Errorf("ratchet received invalid arguments: %s must be a string[]", field)
	}
	return asStringSlice(raw, field)
}

func asStringSlice(raw any, field string) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("ratchet received invalid arguments: %s must be a string[]", field)
	}
	out := make([]string, len(arr))
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("ratchet received invalid arguments: %s[%d] must be a string", field, i)
		}
		out[i] = s
	}
	return out, nil
}

func asNonEmptyString(raw any, field string) (string, error) {
	s, ok := raw.(string)
	if !ok || len(s) == 0 {
		return "", fmt.Errorf("ratchet received invalid arguments: %s must be a non-empty string", field)
	}
	return s, nil
}

func asDirection(raw any, field string) (Direction, error) {
	s, ok := raw.(string)
	if !ok || (s != string(DirectionHigher) && s != string(DirectionLower)) {
		return "", fmt.Errorf(`ratchet received invalid arguments: %s must be "higher" or "lower"`, field)
	}
	return Direction(s), nil
}

const maxSafeJSInt = 9007199254740991.0 // 2^53 - 1

func asIntegerMin(raw any, minVal int, field string) (int, error) {
	f, ok := finiteNumber(raw)
	if !ok || math.Trunc(f) != f || f < float64(minVal) || f > maxSafeJSInt || f > float64(math.MaxInt) {
		return 0, fmt.Errorf("ratchet received invalid arguments: %s must be an integer >= %d", field, minVal)
	}
	return int(f), nil
}

func parseOrderedDirectionsFromRaw(topRaw json.RawMessage, decoded any) (OrderedDirections, error) {
	if _, ok := decoded.(map[string]any); !ok || decoded == nil {
		return OrderedDirections{}, errors.New("ratchet received invalid arguments: goal.directions must be an object")
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(topRaw, &top); err != nil {
		return OrderedDirections{}, errors.New("ratchet received invalid arguments: goal.directions must be an object")
	}
	var goalRaw map[string]json.RawMessage
	if err := json.Unmarshal(top["goal"], &goalRaw); err != nil {
		return OrderedDirections{}, errors.New("ratchet received invalid arguments: goal.directions must be an object")
	}
	dirsRaw := bytes.TrimSpace(goalRaw["directions"])
	if len(dirsRaw) == 0 || bytes.Equal(dirsRaw, []byte("null")) {
		return OrderedDirections{}, errors.New("ratchet received invalid arguments: goal.directions must be an object")
	}
	var od OrderedDirections
	if err := od.UnmarshalJSON(dirsRaw); err != nil {
		return OrderedDirections{}, fmt.Errorf("ratchet received invalid arguments: goal.directions: %w", err)
	}
	for _, entry := range od.Entries() {
		if _, err := asDirection(string(entry.Direction), "goal.directions."+entry.Key); err != nil {
			return OrderedDirections{}, err
		}
	}
	return od, nil
}

func asNonNegNumber(raw any, field string) (float64, error) {
	f, ok := finiteNumber(raw)
	if !ok || f < 0 {
		return 0, fmt.Errorf("ratchet received invalid arguments: %s must be a number >= 0", field)
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// Approval dialog
// ---------------------------------------------------------------------------

// askApproval runs the host-owned approval dialog (no recommended option, no timeout).
// Uses PiG's interactive Select/Input dialogs matching upstream's ui.select/ui.input
// fallback path (`prelude-definition.ts:178-183`).
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:135-184
func askApproval(sdkCtx sdk.Context, prompter ApprovalPrompter, stage Stage, question, preview string) (ApprovalOutcome, error) {
	if prompter != nil {
		ctx := context.Background()
		var zero sdk.Context
		if sdkCtx != zero {
			if done := sdkCtx.Done(); done != nil {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				go func() {
					select {
					case <-done:
						cancel()
					case <-ctx.Done():
					}
				}()
				defer cancel()
			}
		}
		return prompter(ctx, sdkCtx, stage, question, preview)
	}
	if !safeHasUI(sdkCtx) {
		return ApprovalOutcome{}, errors.New(
			"ratchet approvals need an interactive session; headless runs can only continue a flow whose approvals are current",
		)
	}
	header := fmt.Sprintf("ratchet · %s", stage)
	body := fmt.Sprintf("%s\n\n%s", question, preview)
	selected, ok, err := sdkCtx.Select(fmt.Sprintf("%s\n\n%s", header, body), []string{"Approve", "Revise", "Abort"})
	if err != nil {
		return ApprovalOutcome{}, err
	}
	if !ok {
		return ApprovalOutcome{Approved: false, Aborted: new(true)}, nil
	}
	choice, _ := selected.(string)
	if choice == "Approve" {
		return ApprovalOutcome{Approved: true}, nil
	}
	if choice != "Revise" {
		return ApprovalOutcome{Approved: false, Aborted: new(true)}, nil
	}
	fbRaw, fbOK, err := sdkCtx.InputWithoutPlaceholder("What should change?")
	if err != nil {
		return ApprovalOutcome{}, err
	}
	feedback := ""
	if fbOK {
		feedback, _ = fbRaw.(string)
	}
	return ApprovalOutcome{Approved: false, Aborted: new(false), Feedback: new(feedback)}, nil
}

// ---------------------------------------------------------------------------
// Prelude definition & dispatch
// ---------------------------------------------------------------------------

// sessionPriceCache caches per-session catalogPriceLookup closures (upstream:
// prelude-definition.ts:96-109, 280), keyed by session ID (or a single default
// entry when no session ID is present) and evicted via lifecycle.OnEnd.
type sessionPriceCache struct {
	mu     sync.Mutex
	bySess map[string]PriceLookup
}

func newSessionPriceCache() *sessionPriceCache {
	return &sessionPriceCache{bySess: map[string]PriceLookup{}}
}

func (c *sessionPriceCache) get(ctx sdk.Context, lister ModelCatalogLister) PriceLookup {
	sid := safeSessionID(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn, ok := c.bySess[sid]; ok {
		return fn
	}
	fn := catalogPriceLookup(ctx, lister)
	c.bySess[sid] = fn
	return fn
}

func (c *sessionPriceCache) drop(sessionID string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	delete(c.bySess, sessionID)
	c.mu.Unlock()
}

// PreludeDefinition constructs the eval.Prelude for `ratchet`.
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:279-313
func PreludeDefinition(opts ...PreludeOptions) eval.Prelude {
	var opt PreludeOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	enabledFn := ratchetEnabled
	if opt.EnabledCheck != nil {
		enabledFn = opt.EnabledCheck
	}
	priceCache := newSessionPriceCache()
	if opt.ModelLister == nil {
		lifecycle.OnEnd("ratchet", priceCache.drop)
	}
	return eval.Prelude{
		Name: "ratchet",
		Documentation: func() string {
			return strings.TrimSpace(ratchetDocumentation)
		},
		JavaScript:  ratchetJavascript,
		Python:      ratchetPython,
		Exports:     []string{"ratchet"},
		ApprovalFor: RatchetApproval,
		Enabled:     enabledFn,
		Invoke: func(call eval.PreludeCall, rawParams json.RawMessage) (*eval.PreludeResult, error) {
			return invokeRatchetPrelude(call, rawParams, opt, priceCache)
		},
		Status: DescribeCall,
	}
}

// DescribeCall produces omp's status-tree line for a completed ratchet call.
// upstream: packages/coding-agent/src/ratchet/prelude-definition.ts:300-311
func DescribeCall(raw json.RawMessage, outcome *eval.PreludeResult) string {
	p, err := parseParams(raw)
	if err != nil {
		return ""
	}
	target := "()"
	if p.variant != "" {
		target = fmt.Sprintf("(%s)", p.variant)
	} else if p.action == "approve" {
		target = fmt.Sprintf("(%s)", p.stage)
	}
	decision := ""
	if p.action == "gate" && outcome != nil && outcome.Details != nil {
		switch d := outcome.Details.(type) {
		case *GateResult:
			if d != nil {
				decision = fmt.Sprintf(" → %s", d.Decision)
			}
		case GateResult:
			decision = fmt.Sprintf(" → %s", d.Decision)
		case map[string]any:
			if v, ok := d["decision"]; ok {
				decision = fmt.Sprintf(" → %v", v)
			}
		}
	}
	return fmt.Sprintf(`ratchet("%s").%s%s%s`, p.flow, p.action, target, decision)
}

func invokeRatchetPrelude(call eval.PreludeCall, rawParams json.RawMessage, opt PreludeOptions, priceCache *sessionPriceCache) (*eval.PreludeResult, error) {
	p, err := parseParams(rawParams)
	if err != nil {
		return nil, err
	}
	cwd := opt.CwdOverride
	if cwd == "" {
		cwd = safeCwd(call.SDK)
	}
	if cwd == "" {
		return nil, errors.New("ratchet requires a session working directory")
	}
	lookup := priceCache.get(call.SDK, opt.ModelLister)
	lockKey := cwd + "\x00" + p.flow

	switch p.action {
	case "init":
		state, err := withFlowLock(lockKey, func() (*State, error) {
			return InitFlow(cwd, p.flow, InitOptions{
				Cases:     p.cases,
				Harness:   p.harness,
				Change:    p.change,
				OffLimits: p.offLimits,
				Command:   p.command,
			})
		})
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{
			Text:    fmt.Sprintf("ratchet flow %s ready", state.Flow),
			Details: state,
		}, nil

	case "plan":
		state, err := withFlowLock(lockKey, func() (*State, error) {
			fresh, err := RequireState(cwd, p.flow)
			if err != nil {
				return nil, err
			}
			if err := ApplyPlan(fresh, PlanOptions{
				Goal:    p.goal,
				Reps:    p.reps,
				Stop:    p.stop,
				Command: p.command,
				Prices:  p.prices,
			}); err != nil {
				return nil, err
			}
			if err := SaveState(cwd, fresh); err != nil {
				return nil, err
			}
			return fresh, nil
		})
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{
			Text:    fmt.Sprintf("plan updated for %s", state.Flow),
			Details: state,
		}, nil

	case "split":
		state, err := withFlowLock(lockKey, func() (*State, error) {
			fresh, err := RequireState(cwd, p.flow)
			if err != nil {
				return nil, err
			}
			if err := SplitCases(fresh, p.splitCases, p.testFraction, p.seed); err != nil {
				return nil, err
			}
			if err := SaveState(cwd, fresh); err != nil {
				return nil, err
			}
			return fresh, nil
		})
		if err != nil {
			return nil, err
		}
		seedVal := 0
		if state.SplitSeed != nil {
			seedVal = *state.SplitSeed
		}
		return &eval.PreludeResult{
			Text: fmt.Sprintf("split %s: %d train / %d test (seed %d)", state.Flow, len(state.TrainIDs), len(state.TestIDs), seedVal),
			Details: map[string]any{
				"train_ids": state.TrainIDs,
				"test_ids":  state.TestIDs,
				"seed":      seedVal,
			},
		}, nil

	case "approve":
		reviewed, err := RequireState(cwd, p.flow)
		if err != nil {
			return nil, err
		}
		sha, err := StageDigest(cwd, reviewed, p.stage)
		if err != nil {
			return nil, err
		}
		if rec, ok := reviewed.Approvals[p.stage]; ok && rec.SHA == sha {
			return &eval.PreludeResult{
				Text:    "",
				Details: ApprovalOutcome{Approved: true, Already: new(true)},
			}, nil
		}
		outcome, err := askApproval(call.SDK, opt.Prompter, p.stage, p.question, p.preview)
		if err != nil {
			return nil, err
		}
		if !outcome.Approved {
			return &eval.PreludeResult{Text: "", Details: outcome}, nil
		}
		// Record against freshly loaded state so approvals made meanwhile survive,
		// and only if the material is still what the user just reviewed.
		recorded, err := withFlowLock(lockKey, func() (ApprovalOutcome, error) {
			fresh, err := RequireState(cwd, p.flow)
			if err != nil {
				return ApprovalOutcome{}, err
			}
			currentSHA, err := StageDigest(cwd, fresh, p.stage)
			if err != nil {
				return ApprovalOutcome{}, err
			}
			if currentSHA != sha {
				return ApprovalOutcome{
					Approved: false,
					Aborted:  new(false),
					Feedback: new("The material changed during review; approve again"),
				}, nil
			}
			fresh.Approvals[p.stage] = ApprovalRecord{
				SHA: sha,
				At:  time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			}
			if err := SaveState(cwd, fresh); err != nil {
				return ApprovalOutcome{}, err
			}
			return outcome, nil
		})
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Text: "", Details: recorded}, nil

	case "check":
		state, err := RequireState(cwd, p.flow)
		if err != nil {
			return nil, err
		}
		check, err := CheckVariant(cwd, state, p.variant)
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Text: "", Details: check}, nil

	case "gate":
		gateRes, err := withFlowLock(lockKey, func() (*GateResult, error) {
			fresh, err := RequireState(cwd, p.flow)
			if err != nil {
				return nil, err
			}
			return GateVariant(cwd, fresh, p.variant, p.gateChange, lookup)
		})
		if err != nil {
			return nil, err
		}
		lines := []string{fmt.Sprintf("%s: %s — %s", gateRes.Variant, gateRes.Decision, strings.Join(gateRes.Reasons, "; "))}
		for _, w := range gateRes.Warnings {
			lines = append(lines, "warning: "+w)
		}
		if gateRes.Plateau {
			lines = append(lines, "plateau reached: categorize remaining train failures before another content round")
		} else if gateRes.Done {
			lines = append(lines, "round limit reached: report")
		}
		return &eval.PreludeResult{
			Text:    strings.Join(lines, "\n"),
			Details: gateRes,
		}, nil

	case "train":
		state, err := RequireState(cwd, p.flow)
		if err != nil {
			return nil, err
		}
		view, err := GetTrainView(cwd, state, p.variant, lookup)
		if err != nil {
			return nil, err
		}
		return &eval.PreludeResult{Text: "", Details: view}, nil

	case "status":
		state, err := RequireState(cwd, p.flow)
		if err != nil {
			return nil, err
		}
		approvals, err := GetApprovalStatus(cwd, state)
		if err != nil {
			return nil, err
		}
		parts := make([]string, len(Stages))
		for i, stage := range Stages {
			parts[i] = fmt.Sprintf("%s=%s", stage, approvals[stage])
		}
		approvalLine := strings.Join(parts, " ")
		statusDetails := struct {
			*State
			ApprovalStatus ApprovalStatus `json:"approval_status"`
		}{
			State:          state,
			ApprovalStatus: approvals,
		}
		return &eval.PreludeResult{
			Text:    fmt.Sprintf("%s: %s\n%s", state.Flow, approvalLine, RenderStatusTable(state)),
			Details: statusDetails,
		}, nil

	default:
		return nil, fmt.Errorf("ratchet received unknown action %q", p.action)
	}
}

func safeHasUI(ctx sdk.Context) bool {
	var zero sdk.Context
	if ctx == zero {
		return false
	}
	var has bool
	func() {
		defer func() { _ = recover() }()
		has = ctx.HasUI()
	}()
	return has
}

func safeCwd(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var cwd string
	func() {
		defer func() { _ = recover() }()
		cwd = ctx.Cwd()
	}()
	return cwd
}

func safeSessionID(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var id string
	func() {
		defer func() { _ = recover() }()
		if v, err := ctx.GetSessionID(); err == nil {
			id = v
		}
	}()
	return id
}
