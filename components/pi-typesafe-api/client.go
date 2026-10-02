package pitypesafe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

const (
	sdkPath       = "/v1/systemone"
	sdkModelsPath = "/v1/models"
	// DefaultMaxRequests is the default number of attempts per client instance; the extension quotes the same number in its consent copy.
	DefaultMaxRequests = 20
	// DefaultTimeout is the default per-request timeout.
	DefaultTimeout = 15 * time.Second
)

// backendDoer sends the SDK's fixed paths to the backend's own, preserving any caller-supplied transport. A
// backend that serves its model list under another path also gets that list renamed to the field the SDK reads.
type backendDoer struct {
	inner   typesafe.HTTPDoer
	backend ResolvedBackend
}

func (d backendDoer) Do(req *http.Request) (*http.Response, error) {
	inner := d.inner
	if inner == nil {
		inner = http.DefaultClient
	}
	models := d.backend.ModelsPath != "" && strings.Contains(req.URL.Path, sdkModelsPath)
	rewrite := d.backend.Path
	from := sdkPath
	if models {
		rewrite, from = d.backend.ModelsPath, sdkModelsPath
	}
	if rewrite != "" {
		clone := req.Clone(req.Context())
		u := *req.URL
		u.Path = strings.Replace(u.Path, from, rewrite, 1)
		u.RawPath = ""
		clone.URL = &u
		req = clone
	}
	resp, err := inner.Do(req)
	if err != nil || !models || d.backend.ModelsField == "" {
		return resp, err
	}
	return translateModels(resp, d.backend)
}

// translateModels hands the SDK the list it expects: the field it reads, and the entry value callers pass as
// model when the backend labels models differently. Status and headers survive; a body without the declared
// field is passed through unchanged, so the SDK still reports its own shape error.
func translateModels(resp *http.Response, backend ResolvedBackend) (*http.Response, error) {
	text, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	send := func(body []byte) *http.Response {
		out := *resp
		out.Header = resp.Header.Clone()
		// The body is replaced, so a copied length would describe the old one.
		out.Header.Del("Content-Length")
		out.Header.Del("Content-Encoding")
		out.ContentLength = int64(len(body))
		out.Body = io.NopCloser(bytes.NewReader(body))
		return &out
	}
	wire, perr := ParseJSON(text)
	obj, isObj := wire.(*Object)
	if perr != nil || !isObj {
		return send(text), nil
	}
	list, _ := obj.Get(backend.ModelsField)
	entries, isList := list.([]any)
	if !isList {
		return send(text), nil
	}
	models := make([]any, len(entries))
	for i, e := range entries {
		entry, ok := e.(*Object)
		if backend.ModelsIDField == "" || !ok {
			models[i] = e
			continue
		}
		id, _ := entry.Get(backend.ModelsIDField)
		if s, ok := id.(string); ok && s != "" {
			c := entry.Clone()
			c.Set("name", s)
			models[i] = c
		} else {
			models[i] = e
		}
	}
	wrapper := NewObject()
	wrapper.Set("models", models)
	body, err := EncodeJSON(wrapper)
	if err != nil {
		return nil, err
	}
	return send(body), nil
}

// Options configure New.
type Options struct {
	// APIKey defaults to the backend's key (TYPESAFE_API_KEY, then the key saved by /typesafe login for the
	// TypeSafe backend); it is never returned.
	APIKey string
	// Backend is the judgment backend: a registry name or a caller-supplied endpoint. Nil routes to the default TypeSafe host.
	Backend any
	// Model defaults to the backend's own default (jev-latest, typesafe/jev-1.13 on OpenRouter); a bare Jev id is
	// mapped to the backend's id form before sending. No model is inferred from submitted content.
	Model string
	// Timeout is per request. Default: 15 seconds. There are no automatic retries.
	Timeout time.Duration
	// MaxInputBytes is the UTF-8 JSON bytes including model and questions. Default: 64 KiB. Not a token limit.
	MaxInputBytes int
	// MaxRequests is the attempts per client instance, including failed network requests. Default: 20.
	MaxRequests int
	// MaxRequestsPerDay is requests per local day, counted across processes and restarts. Unlimited by default.
	MaxRequestsPerDay int
	// MaxInputTokensPerDay is input tokens per local day. Unlimited by default.
	MaxInputTokensPerDay int
	// MaxUSDPerDay is estimated spend per local day, in US dollars. Unlimited by default.
	MaxUSDPerDay float64
	// UsdPerMTok is the price used for the cost estimate and the USD cap. Default: DefaultUSDPerMTok.
	UsdPerMTok float64
	// Ledger is the usage ledger; it defaults to the store next to the key. Injected by tests.
	Ledger UsageLedger
	// HTTPClient is a transport injection for extension authors and offline tests.
	HTTPClient typesafe.HTTPDoer
	// Evaluator answers the questions instead of a TypeSafe client. The own-model backend requires one (an
	// ownmodel.Backend on the model PiG is configured with); on any other backend it replaces the HTTP client.
	Evaluator typesafe.Evaluator
}

// Evaluation is a typed result plus the time it took and the question ids in request order.
type Evaluation struct {
	*typesafe.SystemOneResult
	ElapsedMs int64
	// Order lists the answer ids in the order the questions were asked; Go maps have none.
	Order []string
}

// UsageSnapshot holds session counters for one client instance, plus the cost estimate they add up to.
type UsageSnapshot struct {
	RequestsStarted   int
	RequestsSucceeded int
	RequestsFailed    int
	InputTokens       int
	OutputTokens      int
	EstimatedUSD      float64
}

// SpendReport holds session counters, today's persisted counters, the caps in force, and the cap that is currently reached.
type SpendReport struct {
	Session    UsageSnapshot
	Today      UsageReport
	Caps       SpendCaps
	UsdPerMTok float64
	Blocked    *BlockedCap
}

// TypeSafe is a bounded client independent of PiG's runtime.
type TypeSafe struct {
	backend       ResolvedBackend
	evaluator     typesafe.Evaluator
	client        *typesafe.Client
	local         bool
	model         string
	timeout       time.Duration
	maxInputBytes int
	maxRequests   int
	usdPerMTok    float64
	caps          SpendCaps
	ledger        UsageLedger

	mu                   sync.Mutex
	usage                UsageSnapshot
	verificationRecorded bool
	lastFailureRecorded  string
}

func positiveInt(v int, def int, label string) (int, error) {
	if v == 0 {
		return def, nil
	}
	if v < 0 {
		return 0, errorf(CodeConfiguration, "%s must be a positive safe integer.", label)
	}
	return v, nil
}

func positiveNumber(v, def float64, label string) (float64, error) {
	if v == 0 {
		return def, nil
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, errorf(CodeConfiguration, "%s must be a positive number.", label)
	}
	return v, nil
}

// New builds a client. It reads the key, validates every option, and never sends anything.
func New(opts Options) (*TypeSafe, error) {
	backend, err := ResolveBackend(opts.Backend)
	if err != nil {
		return nil, err
	}
	c := &TypeSafe{backend: backend, local: backend.Local, evaluator: opts.Evaluator}
	apiKey := strings.TrimSpace(opts.APIKey)
	if !c.local {
		if apiKey == "" {
			// The same resolution that GetAuthState reports, so the status line and the request agree.
			situation, err := KeySituationFor(opts.Backend)
			if err != nil {
				return nil, err
			}
			switch situation.Kind {
			case KeyUnusable:
				return nil, newError(CodeConfiguration, situation.Reason)
			case KeyEnvironment, KeyStored:
				apiKey = situation.Key
			}
		}
		if apiKey == "" {
			how := "Set " + backend.KeyEnv
			if UsesTypeSafeKey(backend.BackendConfig) {
				how = "Run /typesafe login in PiG, or set " + typesafeKeyEnv
			}
			return nil, newError(CodeConfiguration, "No API key. "+how+" in the environment.")
		}
	} else if opts.Evaluator == nil {
		return nil, newError(CodeConfiguration, `The "ownmodel" backend answers with the model PiG is configured with; pass Evaluator (an ownmodel.Backend).`)
	}
	if c.timeout, err = durationOrDefault(opts.Timeout, DefaultTimeout, "timeoutMs"); err != nil {
		return nil, err
	}
	if c.maxInputBytes, err = positiveInt(opts.MaxInputBytes, DefaultMaxInputBytes, "maxInputBytes"); err != nil {
		return nil, err
	}
	if c.maxRequests, err = positiveInt(opts.MaxRequests, DefaultMaxRequests, "maxRequests"); err != nil {
		return nil, err
	}
	if c.usdPerMTok, err = positiveNumber(opts.UsdPerMTok, DefaultUSDPerMTok, "usdPerMTok"); err != nil {
		return nil, err
	}
	explicit := SpendCaps{MaxRequests: c.maxRequests}
	if explicit.MaxRequestsPerDay, err = positiveInt(opts.MaxRequestsPerDay, 0, "maxRequestsPerDay"); err != nil {
		return nil, err
	}
	if explicit.MaxInputTokensPerDay, err = positiveInt(opts.MaxInputTokensPerDay, 0, "maxInputTokensPerDay"); err != nil {
		return nil, err
	}
	if explicit.MaxUSDPerDay, err = positiveNumber(opts.MaxUSDPerDay, 0, "maxUsdPerDay"); err != nil {
		return nil, err
	}
	c.caps = MergeCaps(explicit, CapsFromEnvironment(nil))
	// The caller's input is validated as written, then mapped to the backend's id form; omitting it sends the
	// backend's own default, which the mapping leaves unchanged.
	requested := opts.Model
	if requested == "" {
		requested = backend.DefaultModel
	}
	if requested == "" && !c.local {
		return nil, errorf(CodeConfiguration, "Backend %q names no defaultModel; pass Model to New.", backend.Label)
	}
	if utf16Len(requested) > 100 || (requested != "" && strings.TrimSpace(requested) == "") {
		return nil, newError(CodeConfiguration, "model must be a nonempty string of at most 100 characters.")
	}
	c.model = c.mapModel(requested)
	c.ledger = opts.Ledger
	if c.ledger == nil {
		c.ledger = OpenUsageLedger(LedgerOptions{UsdPerMTok: c.usdPerMTok})
	}
	if c.evaluator == nil {
		transport := opts.HTTPClient
		if backend.Path != "" || backend.ModelsPath != "" {
			transport = backendDoer{inner: opts.HTTPClient, backend: backend}
		}
		// Do not inherit SDK debug logging or alternate destinations from the environment.
		client, err := typesafe.NewClient(typesafe.Config{
			APIKey:       apiKey,
			BaseURL:      backend.Host,
			DefaultModel: c.model,
			LogLevel:     typesafe.LogOff,
			Retry:        typesafe.RetryOverrides{MaxRetries: typesafe.Ptr(0)},
			Timeout:      c.timeout,
			HTTPClient:   transport,
			Getenv:       func(string) string { return "" },
		})
		if err != nil {
			return nil, SafeError(err, opts.Backend)
		}
		c.client, c.evaluator = client, client
	}
	return c, nil
}

func durationOrDefault(v, def time.Duration, label string) (time.Duration, error) {
	if v == 0 {
		return def, nil
	}
	if v < 0 {
		return 0, errorf(CodeConfiguration, "%s must be a positive safe integer.", label)
	}
	return v, nil
}

// mapModel maps a model to the backend's id form. Only a registry backend maps ids; a caller-supplied
// endpoint's model is sent as the caller wrote it.
func (c *TypeSafe) mapModel(model string) string {
	if c.backend.Name == "" {
		return model
	}
	return BackendModelID(c.backend.Name, model)
}

// Backend is the resolved backend this client sends to.
func (c *TypeSafe) Backend() ResolvedBackend { return c.backend }

// Model is the model id sent when a request names none.
func (c *TypeSafe) Model() string { return c.model }

func (c *TypeSafe) snapshotLocked() UsageSnapshot {
	s := c.usage
	s.EstimatedUSD = EstimateUSD(s.InputTokens, c.usdPerMTok)
	return s
}

// GetUsage returns this client's session counters.
func (c *TypeSafe) GetUsage() UsageSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *TypeSafe) blockedLocked() *BlockedCap {
	if c.usage.RequestsStarted >= c.maxRequests {
		return nil
	}
	return c.ledger.Blocked(c.caps)
}

// GetSpend returns session counters, today's persisted totals, and the caps that stop the next request.
func (c *TypeSafe) GetSpend() SpendReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	return SpendReport{Session: c.snapshotLocked(), Today: c.ledger.Today(), Caps: c.caps, UsdPerMTok: c.usdPerMTok, Blocked: c.blockedLocked()}
}

var capLabels = map[BlockedCapName]string{
	CapRequestsPerDay:    "daily request cap",
	CapInputTokensPerDay: "daily input-token cap",
	CapUSDPerDay:         "daily spend cap",
}

func capsDescription(caps SpendCaps) string {
	var parts []string
	if caps.MaxRequests > 0 {
		parts = append(parts, fmt.Sprintf("%d per session", caps.MaxRequests))
	}
	if caps.MaxRequestsPerDay > 0 {
		parts = append(parts, fmt.Sprintf("%d requests per day", caps.MaxRequestsPerDay))
	}
	if caps.MaxInputTokensPerDay > 0 {
		parts = append(parts, fmt.Sprintf("%d input tokens per day", caps.MaxInputTokensPerDay))
	}
	if caps.MaxUSDPerDay > 0 {
		parts = append(parts, "$"+jsNumber(caps.MaxUSDPerDay)+" per day")
	}
	if len(parts) == 0 {
		return "No request or spend caps are set."
	}
	return "Caps: " + strings.Join(parts, ", ") + "."
}

// ListModels returns the model names available to the account. It verifies the key and does not count toward
// MaxRequests. The own-model backend has no list: its model is the one PiG is configured with.
func (c *TypeSafe) ListModels(ctx context.Context) ([]string, error) {
	if c.client == nil {
		return nil, newError(CodeConfiguration, "This backend has no model list: the model is the one PiG is configured with.")
	}
	// The list is read leniently, as the original does: an entry without a usable name is skipped, not an error.
	raw, err := c.client.Models().ListRaw(ctx, nil)
	if err != nil {
		return nil, SafeError(err, c.specForErrors())
	}
	tree, perr := ParseJSON(raw.Body)
	wire, _ := tree.(*Object)
	var list any
	if perr == nil && wire != nil {
		v, _ := wire.Get("models")
		list = v
	}
	entries, isList := list.([]any)
	if !isList {
		return nil, newError(CodeResponse, "TypeSafe returned an unexpected model list.")
	}
	// A backend that serves its list publicly accepts any key, so a success there proves nothing about one.
	if c.backend.ModelsVerifyKey {
		c.recordVerifiedOnce()
	}
	names := []string{}
	for _, e := range entries {
		card, _ := e.(*Object)
		if card == nil {
			continue
		}
		if name, ok := card.vals["name"].(string); ok && name != "" && utf16Len(name) <= 100 {
			names = append(names, name)
		}
	}
	return names, nil
}

// specForErrors is the backend as SafeError wants it: the registry name when there is one, else the resolved endpoint.
func (c *TypeSafe) specForErrors() any {
	if c.backend.Name != "" {
		return c.backend.Name
	}
	return map[string]any{"label": c.backend.Label, "host": c.backend.Host, "keyEnv": c.backend.KeyEnv}
}

func (c *TypeSafe) recordVerifiedOnce() {
	if c.local {
		return
	}
	c.mu.Lock()
	first := !c.verificationRecorded
	c.verificationRecorded = true
	c.mu.Unlock()
	if first {
		RecordAuthVerified(time.Now())
	}
}

// Evaluate sends one typed request through the admission rule and the caps.
func (c *TypeSafe) Evaluate(ctx context.Context, request typesafe.SystemOneRequest) (*Evaluation, error) {
	return c.EvaluateRaw(ctx, request)
}

// EvaluateRaw is Evaluate for a request that arrives as untyped JSON (a decoded map, an *Object, a
// json.RawMessage): the tool call and the playground. It admits the same near-miss aliases as the tool.
func (c *TypeSafe) EvaluateRaw(ctx context.Context, input any) (*Evaluation, error) {
	validated, err := PrepareEvaluationRequest(input, PrepareOptions{MaxInputBytes: c.maxInputBytes})
	if err != nil {
		return nil, err
	}
	// A per-request model meets the same mapping as the client default; the schema already limited the caller's own id.
	model := c.model
	if validated.Model != "" {
		model = c.mapModel(validated.Model)
	}
	sent := validated.WithModel(model)
	body, err := sent.MarshalJSON()
	if err != nil {
		return nil, errorf(CodeValidation, "Invalid evaluation request: %s", jsonSafetyMessage)
	}
	if err := AssertWithinByteLimit(body, c.maxInputBytes); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, newError(CodeAborted, "TypeSafe request cancelled before submission.")
	}
	// Snapshot before awaiting so later mutations cannot change the request or validation.
	typed, err := sent.Typed()
	if err != nil {
		return nil, errorf(CodeValidation, "Invalid evaluation request: %s", jsonSafetyMessage)
	}
	c.mu.Lock()
	if c.usage.RequestsStarted >= c.maxRequests {
		c.mu.Unlock()
		return nil, errorf(CodeBudget, "TypeSafe request limit reached (%d attempts per client instance). %s", c.maxRequests, capsDescription(c.caps))
	}
	if reached := c.ledger.Blocked(c.caps); reached != nil {
		c.mu.Unlock()
		// Name the cap, the used amount, and the day, so a long run stops loudly instead of burning tokens unnoticed.
		return nil, errorf(CodeBudget, "TypeSafe %s reached (%s of %s on %s); no request was submitted. Requests resume after the local day rolls over, or raise the cap deliberately.",
			capLabels[reached.Cap], jsNumber(reached.Used), jsNumber(reached.Limit), reached.Day)
	}
	c.usage.RequestsStarted++
	c.ledger.RecordStart()
	c.mu.Unlock()

	start := time.Now()
	callCtx := ctx
	if c.client == nil || c.evaluator != typesafe.Evaluator(c.client) {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	result, err := c.evaluator.SystemOne(callCtx, typed, nil)
	if err == nil {
		order := sent.Questions.Keys()
		if !validResult(result, sent.Questions) {
			err = newError(CodeResponse, "TypeSafe returned an unexpected answer or usage format.")
		} else {
			c.mu.Lock()
			c.usage.RequestsSucceeded++
			c.usage.InputTokens += result.Usage.InputTokens
			c.usage.OutputTokens += result.Usage.OutputTokens
			c.ledger.RecordSuccess(result.Usage.InputTokens, result.Usage.OutputTokens)
			c.mu.Unlock()
			c.recordVerifiedOnce()
			return &Evaluation{SystemOneResult: result, ElapsedMs: time.Since(start).Milliseconds(), Order: order}, nil
		}
	}
	if callCtx != ctx && callCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		err = context.DeadlineExceeded
	}
	safe := SafeError(err, c.specForErrors())
	// The request was submitted, so it counts even when it fails; the reason stays visible in GetAuthState.
	c.mu.Lock()
	c.usage.RequestsFailed++
	c.ledger.RecordFailure()
	fingerprint := fmt.Sprintf("%s:%s:%s", safe.Code, statusText(safe.Status), safe.Message)
	record := c.lastFailureRecorded != fingerprint
	c.lastFailureRecorded = fingerprint
	c.mu.Unlock()
	if record && !c.local {
		RecordAuthFailure(safe, time.Now())
	}
	return nil, safe
}

func statusText(status int) string {
	if status == 0 {
		return ""
	}
	return strconv.Itoa(status)
}

// EvaluateMany sends several requests with bounded concurrency; answers, usage, and model merged. It never fails.
func (c *TypeSafe) EvaluateMany(ctx context.Context, requests []typesafe.SystemOneRequest, opts BatchOptions) *BatchEvaluation {
	return EvaluateMany(ctx, c, requests, opts)
}

// EvaluateAll takes one state and any number of questions: chunk to the per-request limit, then fan out. It never fails.
func (c *TypeSafe) EvaluateAll(ctx context.Context, request typesafe.SystemOneRequest, opts BatchOptions) *BatchEvaluation {
	return EvaluateAll(ctx, c, request, opts)
}

var _ Judge = (*TypeSafe)(nil)
var _ Evaluator = (*TypeSafe)(nil)

// validResult checks a result against the questions that were asked.
func validResult(result *typesafe.SystemOneResult, questions *Object) bool {
	probability := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
	if result == nil || result.Model == "" || result.Answers == nil {
		return false
	}
	if result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0 {
		return false
	}
	if len(result.Answers) != questions.Len() {
		return false
	}
	for _, id := range questions.keys {
		q, _ := questions.vals[id].(*Object)
		kind, _ := q.vals["type"].(string)
		answer, ok := result.Answers[id]
		if !ok || answer == nil || string(answer.AnswerType()) != kind {
			return false
		}
		criteria, _ := q.Get("criteria")
		switch a := answer.(type) {
		case typesafe.NoulAnswer:
			if !probability(a.Noul) {
				return false
			}
		case typesafe.ChoiceAnswer:
			labels, _ := criteria.(*Object)
			if labels == nil || !probability(a.Confidence) || a.Probabilities == nil || len(a.Probabilities) != labels.Len() {
				return false
			}
			for _, key := range labels.keys {
				p, ok := a.Probabilities[key]
				if !ok || !probability(p) {
					return false
				}
			}
			if !labels.Has(a.Choice) {
				return false
			}
		case typesafe.ScoreAnswer:
			levels, _ := criteria.([]any)
			if !probability(a.Confidence) || a.Probabilities == nil || len(a.Probabilities) != len(levels) {
				return false
			}
			for i := range levels {
				p, ok := a.Probabilities[i]
				if !ok || !probability(p) {
					return false
				}
			}
			if math.IsNaN(a.Score) || math.IsInf(a.Score, 0) || a.Score < 0 || a.Score > float64(len(levels)-1) || a.Legend == nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}
