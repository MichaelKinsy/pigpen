package memory

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/hindsight/config.ts:1-139
// upstream: packages/coding-agent/src/hindsight/bank.ts:1-168
// upstream: packages/coding-agent/src/hindsight/client.ts:1-723
// upstream: packages/coding-agent/src/hindsight/content.ts:65-85
// upstream: packages/coding-agent/src/hindsight/outbox.ts:1-389
// upstream: packages/coding-agent/src/hindsight/state.ts:327-355

const (
	defaultHindsightBankName         = "omp"
	defaultHindsightRetainContext    = "omp"
	defaultHindsightRecallBudget     = "mid"
	defaultHindsightRecallMaxTokens  = 1024
	defaultHindsightRequestTimeoutMs = 30_000
	defaultHindsightRecallTimeoutMs  = 30_000
	defaultHindsightReflectTimeoutMs = 120_000
	defaultHindsightRetainTimeoutMs  = 60_000
	hindsightOutboxMaxEvents         = 1000
	hindsightOutboxBatchSize         = 16
	hindsightMaxErrorDetailChars     = 2000
	hindsightMaxRespBytes            = 8 * 1024 * 1024
	hindsightMaxOutboxLineBytes      = 4 * 1024 * 1024
	maxHTTPRedirects                 = 5
)

var defaultHindsightRecallTypes = []string{"world", "experience"}

// HindsightConfig holds resolved Hindsight settings for a session.
// upstream: packages/coding-agent/src/hindsight/config.ts:41-82
type HindsightConfig struct {
	APIURL           string
	APIToken         string
	BankID           string
	BankIDPrefix     string
	Scoping          string // "global" | "per-project" | "per-project-tagged"
	GlobalScope      bool
	BankMission      string
	RetainMission    string
	RetainContext    string
	RecallBudget     string
	RecallMaxTokens  int
	RecallTypes      []string
	RequestTimeoutMs int
	RecallTimeoutMs  int
	ReflectTimeoutMs int
	RetainTimeoutMs  int
}

// HindsightBankScope describes the resolved bank ID and project tags.
// upstream: packages/coding-agent/src/hindsight/bank.ts:48-56
type HindsightBankScope struct {
	BankID          string
	RetainTags      []string
	RecallTags      []string
	RecallTagsMatch string
}

// HindsightRetainInput is one item queued for retention.
type HindsightRetainInput struct {
	Content string `json:"content"`
	Context string `json:"context,omitempty"`
	Scope   string `json:"scope,omitempty"` // "" | "project" | "global"
}

// HindsightOutboxEvent is one JSONL line persisted under `<agentDir>/memories/hindsight/`.
// upstream: packages/coding-agent/src/hindsight/outbox.ts:28-38
type HindsightOutboxEvent struct {
	Version   int      `json:"version"`
	ID        string   `json:"id"`
	Scope     string   `json:"scope"`
	Tags      []string `json:"tags"`
	Timestamp string   `json:"timestamp"`
	Content   string   `json:"content"`
	Context   string   `json:"context"`
	SessionID string   `json:"sessionId"`
}

// HindsightRecallItem mirrors one result item from `POST /memories/recall`.
type HindsightRecallItem struct {
	ID          string `json:"id,omitempty"`
	Text        string `json:"text"`
	Type        string `json:"type,omitempty"`
	MentionedAt string `json:"mentioned_at,omitempty"`
}

// HindsightRecallResponse mirrors the response of `POST /memories/recall`.
type HindsightRecallResponse struct {
	Results []HindsightRecallItem `json:"results"`
}

// HindsightReflectResponse mirrors the response of `POST /reflect`.
type HindsightReflectResponse struct {
	Text string `json:"text"`
}

// HindsightError carries HTTP status details from the Hindsight server.
// upstream: packages/coding-agent/src/hindsight/client.ts:226-236
type HindsightError struct {
	Message    string
	StatusCode int
}

func (e *HindsightError) Error() string { return e.Message }

// LoadHindsightConfig resolves Hindsight settings for ctx.
// upstream: packages/coding-agent/src/hindsight/config.ts:92-132
func LoadHindsightConfig(ctx sdk.Context) HindsightConfig {
	apiURL := settingString(ctx, SettingHindsightAPIURL, "")
	apiToken := settingString(ctx, SettingHindsightAPIToken, "")
	if apiToken == "" {
		apiToken = settingString(ctx, SettingHindsightAPIKey, "")
	}
	scoping := strings.ToLower(settingString(ctx, SettingHindsightScoping, "per-project-tagged"))
	switch scoping {
	case "global", "per-project", "per-project-tagged":
	default:
		scoping = "per-project-tagged"
	}
	recallTypes := settingStringSlice(ctx, SettingHindsightRecallTypes, defaultHindsightRecallTypes)

	reqTimeout := settingInt(ctx, SettingHindsightRequestTimeoutMs, defaultHindsightRequestTimeoutMs)
	if reqTimeout <= 0 {
		reqTimeout = defaultHindsightRequestTimeoutMs
	}
	recallTimeout := settingInt(ctx, SettingHindsightRecallTimeoutMs, defaultHindsightRecallTimeoutMs)
	if recallTimeout <= 0 {
		recallTimeout = defaultHindsightRecallTimeoutMs
	}
	reflectTimeout := settingInt(ctx, SettingHindsightReflectTimeoutMs, defaultHindsightReflectTimeoutMs)
	if reflectTimeout <= 0 {
		reflectTimeout = defaultHindsightReflectTimeoutMs
	}
	retainTimeout := settingInt(ctx, SettingHindsightRetainTimeoutMs, defaultHindsightRetainTimeoutMs)
	if retainTimeout <= 0 {
		retainTimeout = defaultHindsightRetainTimeoutMs
	}
	maxTokens := settingInt(ctx, SettingHindsightRecallMaxTokens, defaultHindsightRecallMaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultHindsightRecallMaxTokens
	}
	budget := strings.ToLower(settingString(ctx, SettingHindsightRecallBudget, defaultHindsightRecallBudget))
	if budget == "" {
		budget = defaultHindsightRecallBudget
	}
	retainCtx := settingString(ctx, SettingHindsightRetainContext, defaultHindsightRetainContext)
	if retainCtx == "" {
		retainCtx = defaultHindsightRetainContext
	}

	return HindsightConfig{
		APIURL:           apiURL,
		APIToken:         apiToken,
		BankID:           settingString(ctx, SettingHindsightBankID, ""),
		BankIDPrefix:     settingString(ctx, SettingHindsightBankIDPrefix, ""),
		Scoping:          scoping,
		GlobalScope:      settingBool(ctx, SettingHindsightGlobalScope, false),
		BankMission:      settingString(ctx, SettingHindsightBankMission, ""),
		RetainMission:    settingString(ctx, SettingHindsightRetainMission, ""),
		RetainContext:    retainCtx,
		RecallBudget:     budget,
		RecallMaxTokens:  maxTokens,
		RecallTypes:      recallTypes,
		RequestTimeoutMs: reqTimeout,
		RecallTimeoutMs:  recallTimeout,
		ReflectTimeoutMs: reflectTimeout,
		RetainTimeoutMs:  retainTimeout,
	}
}

// AssertGlobalRetainAvailable validates whether `scope: "global"` is permitted.
// upstream: packages/coding-agent/src/hindsight/state.ts:338-345
func (cfg HindsightConfig) AssertGlobalRetainAvailable() error {
	if !cfg.GlobalScope {
		return errors.New("Hindsight global scope is off; set hindsight.globalScope to true to enable it.")
	}
	if cfg.Scoping == "per-project" {
		return errors.New("Hindsight global scope requires global or per-project-tagged scoping.")
	}
	return nil
}

// ComputeHindsightBankScope resolves bankId and project tags from config and cwd.
// upstream: packages/coding-agent/src/hindsight/bank.ts:59-114
func ComputeHindsightBankScope(cfg HindsightConfig, cwd string) HindsightBankScope {
	base := strings.TrimSpace(cfg.BankID)
	if base == "" {
		base = defaultHindsightBankName
	}
	if prefix := strings.TrimSpace(cfg.BankIDPrefix); prefix != "" {
		base = prefix + "-" + base
	}
	label := "unknown"
	if strings.TrimSpace(cwd) != "" {
		b := strings.ToLower(filepath.Base(filepath.Clean(cwd)))
		if b != "" && b != "." && b != string(filepath.Separator) {
			label = b
		}
	}
	switch cfg.Scoping {
	case "global":
		return HindsightBankScope{BankID: base}
	case "per-project":
		return HindsightBankScope{BankID: base + "-" + label}
	default: // "per-project-tagged"
		tag := "project:" + label
		return HindsightBankScope{
			BankID:          base,
			RetainTags:      []string{tag},
			RecallTags:      []string{tag},
			RecallTagsMatch: "any",
		}
	}
}

// validateHTTPURL enforces http(s) scheme, non-empty host, and blocks cloud
// metadata SSRF endpoints (169.254.169.254 / link-local metadata). Loopback
// addresses (127.0.0.1 / localhost / ::1) remain permitted for self-hosted
// Hindsight servers and local tests.
func validateHTTPURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q (expected http or https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL %q is missing a host", raw)
	}
	if ip := net.ParseIP(host); ip != nil {
		// Block AWS/GCP/Azure link-local metadata range 169.254.0.0/16 and fd00:ec2::254.
		if ip.IsLinkLocalUnicast() || ip.String() == "fd00:ec2::254" {
			return fmt.Errorf("refusing request to link-local/metadata IP %s", ip.String())
		}
	}
	lowerHost := strings.ToLower(host)
	if lowerHost == "metadata.google.internal" || lowerHost == "metadata" {
		return fmt.Errorf("refusing request to metadata host %s", host)
	}
	return nil
}
func safeHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxHTTPRedirects {
				return fmt.Errorf("stopped after %d redirects", maxHTTPRedirects)
			}
			if err := validateHTTPURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect blocked: %w", err)
			}
			return nil
		},
	}
}

// HindsightClient is a pure-stdlib `net/http` client for the Hindsight API.
// upstream: packages/coding-agent/src/hindsight/client.ts:277-633
type HindsightClient struct {
	baseURL          string
	apiToken         string
	httpClient       *http.Client
	requestTimeoutMs int
	recallTimeoutMs  int
	reflectTimeoutMs int
	retainTimeoutMs  int
}

// NewHindsightClient constructs a validated Hindsight HTTP client.
func NewHindsightClient(cfg HindsightConfig) (*HindsightClient, error) {
	if err := validateHTTPURL(cfg.APIURL); err != nil {
		return nil, fmt.Errorf("invalid hindsight.apiUrl: %w", err)
	}
	return &HindsightClient{
		baseURL:          strings.TrimRight(strings.TrimSpace(cfg.APIURL), "/"),
		apiToken:         cfg.APIToken,
		httpClient:       safeHTTPClient(),
		requestTimeoutMs: cfg.RequestTimeoutMs,
		recallTimeoutMs:  cfg.RecallTimeoutMs,
		reflectTimeoutMs: cfg.ReflectTimeoutMs,
		retainTimeoutMs:  cfg.RetainTimeoutMs,
	}, nil
}

func (c *HindsightClient) doJSON(ctx context.Context, method, path, operation string, body any, timeoutMs int, out any) error {
	if timeoutMs <= 0 {
		timeoutMs = c.requestTimeoutMs
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return &HindsightError{Message: fmt.Sprintf("%s request failed: %v", operation, err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "oh-my-pig")
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &HindsightError{Message: fmt.Sprintf("%s request timed out after %ds", operation, (timeoutMs+500)/1000)}
		}
		return &HindsightError{Message: fmt.Sprintf("%s request failed: %v", operation, err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, hindsightMaxRespBytes))
	if err != nil {
		return &HindsightError{Message: fmt.Sprintf("%s read response failed: %v", operation, err), StatusCode: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(raw))
		var errObj map[string]any
		if json.Unmarshal(raw, &errObj) == nil {
			if d, ok := errObj["detail"].(string); ok && d != "" {
				detail = d
			} else if m, ok := errObj["message"].(string); ok && m != "" {
				detail = m
			}
		}
		if runes := []rune(detail); len(runes) > hindsightMaxErrorDetailChars {
			detail = string(runes[:hindsightMaxErrorDetailChars])
		}
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return &HindsightError{
			Message:    fmt.Sprintf("%s failed: %s", operation, detail),
			StatusCode: resp.StatusCode,
		}
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &HindsightError{Message: fmt.Sprintf("%s decode failed: %v", operation, err), StatusCode: resp.StatusCode}
		}
	}
	return nil
}

// CreateBank ensures bankId exists on the Hindsight server (idempotent PUT).
// upstream: packages/coding-agent/src/hindsight/client.ts:395-408
func (c *HindsightClient) CreateBank(ctx context.Context, bankID, reflectMission, retainMission string) error {
	payload := map[string]any{}
	if reflectMission != "" {
		payload["reflect_mission"] = reflectMission
	}
	if retainMission != "" {
		payload["retain_mission"] = retainMission
	}
	path := "/v1/default/banks/" + url.PathEscape(bankID)
	return c.doJSON(ctx, http.MethodPut, path, "createBank", payload, c.requestTimeoutMs, nil)
}

// RetainBatch submits one batch of memories to `POST /v1/default/banks/{bank_id}/memories`.
// upstream: packages/coding-agent/src/hindsight/client.ts:331-354
func (c *HindsightClient) RetainBatch(ctx context.Context, bankID string, events []HindsightOutboxEvent, async bool) error {
	items := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		item := map[string]any{
			"content":     ev.Content,
			"context":     ev.Context,
			"timestamp":   ev.Timestamp,
			"document_id": ev.ID,
			"update_mode": "replace",
		}
		if ev.SessionID != "" {
			item["metadata"] = map[string]string{"session_id": ev.SessionID}
		}
		if len(ev.Tags) > 0 {
			item["tags"] = ev.Tags
		}
		items = append(items, item)
	}
	payload := map[string]any{
		"items": items,
		"async": async,
	}
	path := "/v1/default/banks/" + url.PathEscape(bankID) + "/memories"
	return c.doJSON(ctx, http.MethodPost, path, "retainBatch", payload, c.retainTimeoutMs, nil)
}

// Recall queries `POST /v1/default/banks/{bank_id}/memories/recall`.
// upstream: packages/coding-agent/src/hindsight/client.ts:356-374
func (c *HindsightClient) Recall(ctx context.Context, bankID, query string, cfg HindsightConfig, scope HindsightBankScope) (*HindsightRecallResponse, error) {
	payload := map[string]any{
		"query":      query,
		"budget":     cfg.RecallBudget,
		"max_tokens": cfg.RecallMaxTokens,
	}
	if len(cfg.RecallTypes) > 0 {
		payload["types"] = cfg.RecallTypes
	}
	if len(scope.RecallTags) > 0 {
		payload["tags"] = scope.RecallTags
		if scope.RecallTagsMatch != "" {
			payload["tags_match"] = scope.RecallTagsMatch
		}
	}
	var resp HindsightRecallResponse
	path := "/v1/default/banks/" + url.PathEscape(bankID) + "/memories/recall"
	if err := c.doJSON(ctx, http.MethodPost, path, "recall", payload, c.recallTimeoutMs, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Reflect queries `POST /v1/default/banks/{bank_id}/reflect`.
// upstream: packages/coding-agent/src/hindsight/client.ts:376-393
func (c *HindsightClient) Reflect(ctx context.Context, bankID, query, extraContext string, cfg HindsightConfig, scope HindsightBankScope) (*HindsightReflectResponse, error) {
	payload := map[string]any{
		"query":  query,
		"budget": cfg.RecallBudget,
	}
	if strings.TrimSpace(extraContext) != "" {
		payload["context"] = extraContext
	}
	if len(scope.RecallTags) > 0 {
		payload["tags"] = scope.RecallTags
		if scope.RecallTagsMatch != "" {
			payload["tags_match"] = scope.RecallTagsMatch
		}
	}
	var resp HindsightReflectResponse
	path := "/v1/default/banks/" + url.PathEscape(bankID) + "/reflect"
	if err := c.doJSON(ctx, http.MethodPost, path, "reflect", payload, c.reflectTimeoutMs, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FormatHindsightMemories formats recall items as upstream bullet list.
// upstream: packages/coding-agent/src/hindsight/content.ts:66-75
func FormatHindsightMemories(results []HindsightRecallItem) string {
	if len(results) == 0 {
		return ""
	}
	lines := make([]string, len(results))
	for i, r := range results {
		typePart := ""
		if r.Type != "" {
			typePart = fmt.Sprintf(" [%s]", r.Type)
		}
		datePart := ""
		if r.MentionedAt != "" {
			datePart = fmt.Sprintf(" (%s)", r.MentionedAt)
		}
		lines[i] = fmt.Sprintf("- %s%s%s", r.Text, typePart, datePart)
	}
	return strings.Join(lines, "\n\n")
}

// ── Durable JSONL Outbox ─────────────────────────────────────────────────────

var (
	outboxLocksMu sync.Mutex
	outboxLocks   = map[string]*localLockEntry{}

	ensuredBanksMu sync.Mutex
	ensuredBanks   = map[string]bool{}
)

func withOutboxLock(path string, fn func() error) error {
	key := filepath.Clean(path)
	outboxLocksMu.Lock()
	entry := outboxLocks[key]
	if entry == nil {
		entry = &localLockEntry{}
		outboxLocks[key] = entry
	}
	entry.refs++
	outboxLocksMu.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		outboxLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 && outboxLocks[key] == entry {
			delete(outboxLocks, key)
		}
		outboxLocksMu.Unlock()
	}()
	unlockFile, err := acquireFileLock(key+".lock", fileLockTimeout)
	if err != nil {
		return err
	}
	defer unlockFile()
	return fn()
}

func hindsightDestinationKey(apiURL, bankID string) string {
	identity := strings.TrimRight(strings.TrimSpace(apiURL), "/") + "\x00" + bankID
	if u, err := url.Parse(strings.TrimSpace(apiURL)); err == nil && u.Host != "" {
		identity = u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/") + "\x00" + bankID
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:8])
}

// HindsightOutboxPath returns `<agentDir>/memories/hindsight/outbox-<destKey>.jsonl`.
func HindsightOutboxPath(agentDir, apiURL, bankID string) string {
	return filepath.Join(agentDir, "memories", "hindsight", "outbox-"+hindsightDestinationKey(apiURL, bankID)+".jsonl")
}

func cleanOutboxText(text, apiToken string) string {
	s := text
	if apiToken != "" {
		s = strings.ReplaceAll(s, apiToken, "[REDACTED]")
	}
	return RedactMemorySecrets(sanitizeControlText(s))
}

func buildOutboxEvent(sessionID, toolCallID string, index int, item HindsightRetainInput, cfg HindsightConfig, scope HindsightBankScope) HindsightOutboxEvent {
	writeScope := item.Scope
	if writeScope == "" {
		writeScope = "project"
	}
	ctxText := item.Context
	if strings.TrimSpace(ctxText) == "" {
		ctxText = cfg.RetainContext
	}
	ctxClean := cleanOutboxText(ctxText, cfg.APIToken)
	contentClean := cleanOutboxText(item.Content, cfg.APIToken)
	sessionClean := cleanOutboxText(sessionID, cfg.APIToken)

	var tags []string
	if writeScope != "global" {
		for _, t := range scope.RetainTags {
			tags = append(tags, cleanOutboxText(t, cfg.APIToken))
		}
	}
	if tags == nil {
		tags = []string{}
	}

	identityJSON, _ := json.Marshal([]any{sessionID, toolCallID, index, writeScope, ctxClean, contentClean})
	sum := sha256.Sum256(identityJSON)
	id := "omp-retain-" + hex.EncodeToString(sum[:16])

	return HindsightOutboxEvent{
		Version:   1,
		ID:        id,
		Scope:     writeScope,
		Tags:      tags,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Content:   contentClean,
		Context:   ctxClean,
		SessionID: sessionClean,
	}
}

func readOutboxEventsLocked(outboxPath string) ([]HindsightOutboxEvent, error) {
	f, err := os.Open(outboxPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []HindsightOutboxEvent
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readBoundedLine(reader, hindsightMaxOutboxLineBytes)
		if err != nil && !errors.Is(err, io.EOF) {
			if errors.Is(err, errLineTooLong) {
				continue
			}
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var ev HindsightOutboxEvent
			if json.Unmarshal([]byte(trimmed), &ev) == nil && ev.Version == 1 && ev.ID != "" {
				events = append(events, ev)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return events, nil
}

var errLineTooLong = errors.New("outbox line exceeds maximum size")

// readBoundedLine reads up to the next '\n' (or EOF). If a single line exceeds
// maxBytes, it drains the remainder of that line cleanly and returns
// errLineTooLong so subsequent lines remain readable.
func readBoundedLine(r *bufio.Reader, maxBytes int) (string, error) {
	var buf []byte
	oversized := false
	for {
		frag, err := r.ReadSlice('\n')
		if !oversized {
			if len(buf)+len(frag) > maxBytes {
				oversized = true
				buf = nil
			} else {
				buf = append(buf, frag...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if oversized {
					return "", errLineTooLong
				}
				return string(buf), io.EOF
			}
			return "", err
		}
		if oversized {
			return "", errLineTooLong
		}
		return string(buf), nil
	}
}

func writeOutboxEventsLocked(outboxPath string, events []HindsightOutboxEvent) error {
	if len(events) == 0 {
		err := os.Remove(outboxPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var buf bytes.Buffer
	for _, ev := range events {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeFileAtomic(outboxPath, buf.Bytes(), 0o600)
}

// EnqueueHindsightRetain validates global scope, persists items to the JSONL outbox
// atomically, and returns the count of newly queued events.
// upstream: packages/coding-agent/src/hindsight/outbox.ts:110-134
func EnqueueHindsightRetain(agentDir, sessionID, toolCallID string, cfg HindsightConfig, scope HindsightBankScope, items []HindsightRetainInput) (int, error) {
	for _, item := range items {
		if item.Scope == "global" {
			if err := cfg.AssertGlobalRetainAvailable(); err != nil {
				return 0, err
			}
		}
	}
	if err := validateHTTPURL(cfg.APIURL); err != nil {
		return 0, fmt.Errorf("invalid hindsight.apiUrl: %w", err)
	}
	if sessionID == "" {
		sessionID = "default"
	}
	if toolCallID == "" {
		toolCallID = generateMemoryID("call")
	}

	outboxPath := HindsightOutboxPath(agentDir, cfg.APIURL, scope.BankID)
	var added int
	err := withOutboxLock(outboxPath, func() error {
		existing, err := readOutboxEventsLocked(outboxPath)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(existing))
		for _, ev := range existing {
			seen[ev.ID] = true
		}
		var fresh []HindsightOutboxEvent
		for i, item := range items {
			ev := buildOutboxEvent(sessionID, toolCallID, i, item, cfg, scope)
			if !seen[ev.ID] {
				seen[ev.ID] = true
				fresh = append(fresh, ev)
			}
		}
		if len(existing)+len(fresh) > hindsightOutboxMaxEvents {
			return fmt.Errorf("Hindsight outbox full (%d undelivered memories); memory NOT queued.", len(existing))
		}
		if len(fresh) == 0 {
			return nil
		}
		if err := writeOutboxEventsLocked(outboxPath, append(existing, fresh...)); err != nil {
			return fmt.Errorf("Hindsight outbox write failed; memory NOT queued: %w", err)
		}
		added = len(fresh)
		return nil
	})
	return added, err
}

func ensureHindsightBank(ctx context.Context, client *HindsightClient, bankID string, cfg HindsightConfig) {
	key := client.baseURL + "\x00" + bankID
	ensuredBanksMu.Lock()
	already := ensuredBanks[key]
	ensuredBanksMu.Unlock()
	if already {
		return
	}
	if err := client.CreateBank(ctx, bankID, cfg.BankMission, cfg.RetainMission); err == nil {
		ensuredBanksMu.Lock()
		if len(ensuredBanks) > 10_000 {
			ensuredBanks = map[string]bool{}
		}
		ensuredBanks[key] = true
		ensuredBanksMu.Unlock()
	}
}

// FlushHindsightOutbox best-effort delivers pending JSONL outbox events to the
// Hindsight server in batches grouped by tag set. Delivered events are removed;
// retryable failures remain in the outbox for future retries.
// upstream: packages/coding-agent/src/hindsight/outbox.ts:200-289
func FlushHindsightOutbox(ctx context.Context, agentDir string, cfg HindsightConfig, scope HindsightBankScope) (delivered int, kept int, flushErr error) {
	client, err := NewHindsightClient(cfg)
	if err != nil {
		return 0, 0, err
	}
	outboxPath := HindsightOutboxPath(agentDir, cfg.APIURL, scope.BankID)

	// Step 1: Lock -> read snapshot -> unlock (never hold file lock across HTTP).
	var events []HindsightOutboxEvent
	if err := withOutboxLock(outboxPath, func() error {
		var readErr error
		events, readErr = readOutboxEventsLocked(outboxPath)
		return readErr
	}); err != nil {
		return 0, 0, err
	}
	if len(events) == 0 {
		return 0, 0, nil
	}

	// Group by sorted tag set so untagged global events never share a request
	// with project-tagged events.
	type group struct {
		key    string
		events []HindsightOutboxEvent
	}
	groupMap := map[string]*group{}
	var groupOrder []string
	for _, ev := range events {
		sortedTags := append([]string(nil), ev.Tags...)
		sort.Strings(sortedTags)
		keyBytes, _ := json.Marshal(sortedTags)
		key := string(keyBytes)
		g := groupMap[key]
		if g == nil {
			g = &group{key: key}
			groupMap[key] = g
			groupOrder = append(groupOrder, key)
		}
		g.events = append(g.events, ev)
	}

	// Step 2: Send HTTP batches without holding the outbox lock.
	ensureHindsightBank(ctx, client, scope.BankID, cfg)

	deliveredIDs := map[string]bool{}
	rejectedIDs := map[string]bool{}
	for _, key := range groupOrder {
		g := groupMap[key]
		for start := 0; start < len(g.events); start += hindsightOutboxBatchSize {
			end := start + hindsightOutboxBatchSize
			if end > len(g.events) {
				end = len(g.events)
			}
			batch := g.events[start:end]
			sendErr := client.RetainBatch(ctx, scope.BankID, batch, true)
			if sendErr == nil {
				for _, ev := range batch {
					deliveredIDs[ev.ID] = true
					delivered++
				}
				continue
			}
			var hErr *HindsightError
			if errors.As(sendErr, &hErr) && (hErr.StatusCode == 400 || hErr.StatusCode == 413 || hErr.StatusCode == 422) {
				// Drop server-rejected payloads (mirrors upstream quarantine on 400/413/422).
				if len(batch) == 1 {
					rejectedIDs[batch[0].ID] = true
					continue
				}
				for _, single := range batch {
					singleErr := client.RetainBatch(ctx, scope.BankID, []HindsightOutboxEvent{single}, true)
					if singleErr == nil {
						deliveredIDs[single.ID] = true
						delivered++
					} else if errors.As(singleErr, &hErr) && (hErr.StatusCode == 400 || hErr.StatusCode == 413 || hErr.StatusCode == 422) {
						rejectedIDs[single.ID] = true
					} else {
						flushErr = singleErr
						break
					}
				}
				if flushErr != nil {
					break
				}
				continue
			}
			flushErr = sendErr
			break
		}
		if flushErr != nil {
			break
		}
	}

	// Step 3: Lock -> re-read current file (preserving concurrently enqueued
	// items) -> remove only delivered/rejected IDs -> atomic rewrite -> unlock.
	writeErr := withOutboxLock(outboxPath, func() error {
		current, err := readOutboxEventsLocked(outboxPath)
		if err != nil {
			return err
		}
		remaining := make([]HindsightOutboxEvent, 0, len(current))
		for _, ev := range current {
			if deliveredIDs[ev.ID] || rejectedIDs[ev.ID] {
				continue
			}
			remaining = append(remaining, ev)
		}
		kept = len(remaining)
		if len(deliveredIDs) == 0 && len(rejectedIDs) == 0 {
			return nil
		}
		return writeOutboxEventsLocked(outboxPath, remaining)
	})
	if writeErr != nil && flushErr == nil {
		flushErr = writeErr
	}
	return delivered, kept, flushErr
}

// resetHindsightBanksForTests clears cached ensured-bank entries (used in unit tests).
func resetHindsightBanksForTests() {
	ensuredBanksMu.Lock()
	ensuredBanks = map[string]bool{}
	ensuredBanksMu.Unlock()
}
