package pi_subagents

// The background (async) run model: the directories, the status file, the atomic JSON writer and the small formatters the
// background modules share. Ports src/shared/types.ts (DIRS, AsyncStatus), src/shared/utils.ts (readStatus),
// src/shared/atomic-json.ts and the pieces of src/shared/formatters.ts the async status text uses.

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The event channels and the widget the original uses (types.ts).
const (
	subagentAsyncStartedEvent   = "subagent:async-started"
	subagentAsyncCompleteEvent  = "subagent:async-complete"
	subagentProcessTerminalEvt  = "subagent:process-terminal"
	subagentForegroundCompleteE = "subagent:foreground-complete"
	subagentControlEvent        = "subagent:control-event"
	subagentControlIntercomEvt  = "subagent:control-intercom"
	subagentResultIntercomEvt   = "subagent:result-intercom"
	intercomDetachRequestEvent  = "pi-intercom:detach-request"
	lifecycleArtifactVersion    = 3
	promptRedacted              = "[prompt redacted]"
	pollIntervalMs              = 250
	defaultMaxSubagentDepthBg   = 2
)

// asyncDirs are the original's DIRS (results and the runs): kept in a value so a test can point them at a temp directory.
type asyncDirs struct{ Results, Async string }

func defaultAsyncDirs() asyncDirs {
	root := tempRootDir()
	return asyncDirs{Results: filepath.Join(root, "async-subagent-results"), Async: filepath.Join(root, "async-subagent-runs")}
}

// ms is a millisecond timestamp or duration. The original writes JavaScript numbers, so a fractional one (an mtime) is read
// and truncated.
type ms int64

func (m *ms) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*m = ms(f)
	return nil
}

func nowMs() int64 { return time.Now().UnixMilli() }

// TokenUsage and CostSummary are the totals a status carries.
type TokenUsage struct {
	Input      int64    `json:"input"`
	Output     int64    `json:"output"`
	Total      int64    `json:"total"`
	Window     *int64   `json:"window,omitempty"`
	WindowPeak *int64   `json:"windowPeak,omitempty"`
	Extra      struct{} `json:"-"`
}

type CostSummary struct {
	Total float64 `json:"total"`
}

// ProcessInstanceExit is one observed process exit of a run: the runner, or a writer (a child) with its process tree.
type ProcessInstanceExit struct {
	Kind              string            `json:"kind"`
	ProcessInstanceID string            `json:"processInstanceId"`
	CloseObservedAt   ms                `json:"closeObservedAt"`
	ExitCode          *int              `json:"exitCode"`
	Signal            *string           `json:"signal"`
	Attempt           *int              `json:"attempt,omitempty"`
	ProcessTree       *ProcessTreeProof `json:"processTree,omitempty"`
}

// ProcessTreeProof says whether a writer's whole process group was seen gone.
type ProcessTreeProof struct {
	State          string `json:"state"`
	Mechanism      string `json:"mechanism,omitempty"`
	ProcessGroupID int    `json:"processGroupId,omitempty"`
	VerifiedAt     ms     `json:"verifiedAt,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Diagnostic     string `json:"diagnostic,omitempty"`
}

// ProcessTerminal is the proof that a run's processes ended (process-terminal.json, and the overlay in status.json).
type ProcessTerminal struct {
	Version                 int                       `json:"version"`
	State                   string                    `json:"state"` // pending, observed, unknown, not-started
	RunID                   string                    `json:"runId"`
	ChildIndex              *int                      `json:"childIndex,omitempty"`
	RunnerProcessInstanceID string                    `json:"runnerProcessInstanceId"`
	ObservedAt              *ms                       `json:"observedAt,omitempty"`
	Reason                  string                    `json:"reason,omitempty"`
	Diagnostic              string                    `json:"diagnostic,omitempty"`
	Instances               []ProcessInstanceExit     `json:"instances,omitempty"`
	ResumeDisposition       string                    `json:"resumeDisposition,omitempty"`
	CanonicalSession        *CanonicalSessionTerminal `json:"canonicalSession,omitempty"`
}

// AsyncStep is one child of a run in status.json.
type AsyncStep struct {
	ChildID          string                     `json:"childId,omitempty"`
	Agent            string                     `json:"agent"`
	SessionName      string                     `json:"sessionName,omitempty"`
	Context          string                     `json:"context,omitempty"`
	Description      string                     `json:"description,omitempty"`
	RunID            string                     `json:"runId,omitempty"`
	Async            *bool                      `json:"async,omitempty"`
	WorkflowKey      string                     `json:"workflowKey,omitempty"`
	Status           string                     `json:"status"` // pending, running, complete, completed, failed, partial, paused, stopped, rejected
	StopRequested    *bool                      `json:"stopRequested,omitempty"`
	StopRequestedAt  *ms                        `json:"stopRequestedAt,omitempty"`
	SessionFile      string                     `json:"sessionFile,omitempty"`
	TranscriptPath   string                     `json:"transcriptPath,omitempty"`
	ActivityState    string                     `json:"activityState,omitempty"`
	LastActivityAt   *ms                        `json:"lastActivityAt,omitempty"`
	CurrentTool      string                     `json:"currentTool,omitempty"`
	CurrentToolArgs  string                     `json:"currentToolArgs,omitempty"`
	CurrentToolStart *ms                        `json:"currentToolStartedAt,omitempty"`
	CurrentPath      string                     `json:"currentPath,omitempty"`
	TurnCount        *int                       `json:"turnCount,omitempty"`
	ToolCount        *int                       `json:"toolCount,omitempty"`
	StartedAt        *ms                        `json:"startedAt,omitempty"`
	EndedAt          *ms                        `json:"endedAt,omitempty"`
	DurationMs       *ms                        `json:"durationMs,omitempty"`
	ExitCode         *int                       `json:"exitCode,omitempty"`
	TimedOut         *bool                      `json:"timedOut,omitempty"`
	Stopped          *bool                      `json:"stopped,omitempty"`
	Tokens           *TokenUsage                `json:"tokens,omitempty"`
	Skills           []string                   `json:"skills,omitempty"`
	Model            string                     `json:"model,omitempty"`
	Thinking         string                     `json:"thinking,omitempty"`
	ContextLimit     *int64                     `json:"contextLimit,omitempty"`
	RequestedModel   string                     `json:"requestedModel,omitempty"`
	ContextOverflow  *bool                      `json:"contextOverflow,omitempty"`
	TotalCost        *CostSummary               `json:"totalCost,omitempty"`
	Error            string                     `json:"error,omitempty"`
	TimeoutRecovery  map[string]any             `json:"timeoutRecovery,omitempty"`
	LaunchDigest     string                     `json:"launchContractDigest,omitempty"`
	ProcessTerminal  *ProcessTerminal           `json:"processTerminal,omitempty"`
	Extra            map[string]json.RawMessage `json:"-"`
}

// AsyncStatus is status.json of a run directory.
type AsyncStatus struct {
	LifecycleArtifactVersion int                        `json:"lifecycleArtifactVersion,omitempty"`
	RunID                    string                     `json:"runId"`
	CompletionOwnerID        string                     `json:"completionOwnerId,omitempty"`
	ToolCallID               string                     `json:"toolCallId,omitempty"`
	SessionID                string                     `json:"sessionId,omitempty"`
	Mode                     string                     `json:"mode"`
	Context                  string                     `json:"context,omitempty"`
	State                    string                     `json:"state"`
	DisplayDismissedAt       *ms                        `json:"displayDismissedAt,omitempty"`
	Error                    string                     `json:"error,omitempty"`
	ActivityState            string                     `json:"activityState,omitempty"`
	LastActivityAt           *ms                        `json:"lastActivityAt,omitempty"`
	CurrentTool              string                     `json:"currentTool,omitempty"`
	CurrentToolStartedAt     *ms                        `json:"currentToolStartedAt,omitempty"`
	CurrentPath              string                     `json:"currentPath,omitempty"`
	TurnCount                *int                       `json:"turnCount,omitempty"`
	ToolCount                *int                       `json:"toolCount,omitempty"`
	StartedAt                ms                         `json:"startedAt"`
	EndedAt                  *ms                        `json:"endedAt,omitempty"`
	LastUpdate               *ms                        `json:"lastUpdate,omitempty"`
	TimeoutMs                *ms                        `json:"timeoutMs,omitempty"`
	DeadlineAt               *ms                        `json:"deadlineAt,omitempty"`
	TimedOut                 *bool                      `json:"timedOut,omitempty"`
	Stopped                  *bool                      `json:"stopped,omitempty"`
	PID                      *int                       `json:"pid,omitempty"`
	PidNamespaceScope        string                     `json:"pidNamespaceScope,omitempty"`
	Cwd                      string                     `json:"cwd,omitempty"`
	SessionRoot              string                     `json:"sessionRoot,omitempty"`
	CurrentStep              *int                       `json:"currentStep,omitempty"`
	ChainStepCount           *int                       `json:"chainStepCount,omitempty"`
	ProcessTerminal          *ProcessTerminal           `json:"processTerminal,omitempty"`
	LaunchContractDigest     string                     `json:"launchContractDigest,omitempty"`
	Steps                    []AsyncStep                `json:"steps,omitempty"`
	SessionDir               string                     `json:"sessionDir,omitempty"`
	OutputFile               string                     `json:"outputFile,omitempty"`
	TotalTokens              *TokenUsage                `json:"totalTokens,omitempty"`
	TotalCost                *CostSummary               `json:"totalCost,omitempty"`
	SessionFile              string                     `json:"sessionFile,omitempty"`
	Extra                    map[string]json.RawMessage `json:"-"`
}

// jsonFieldNames lists the JSON names of the tagged fields of a struct type.
func jsonFieldNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}

// unmarshalKeepingExtra decodes data into dst (a pointer to a struct with an `Extra` field) and keeps the keys the struct
// does not model, so a status file written by the original loses nothing when the port rewrites it.
func unmarshalKeepingExtra(data []byte, dst any, extra *map[string]json.RawMessage, known map[string]bool, alias any) error {
	if err := json.Unmarshal(data, alias); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	*extra = nil
	for k, v := range all {
		if !known[k] {
			if *extra == nil {
				*extra = map[string]json.RawMessage{}
			}
			(*extra)[k] = v
		}
	}
	return nil
}

func marshalWithExtra(base any, extra map[string]json.RawMessage) ([]byte, error) {
	b, err := json.Marshal(base)
	if err != nil || len(extra) == 0 {
		return b, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range extra {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

var (
	asyncStatusKnown = jsonFieldNames(reflect.TypeOf(AsyncStatus{}))
	asyncStepKnown   = jsonFieldNames(reflect.TypeOf(AsyncStep{}))
)

type asyncStatusAlias AsyncStatus
type asyncStepAlias AsyncStep

func (s *AsyncStatus) UnmarshalJSON(b []byte) error {
	return unmarshalKeepingExtra(b, s, &s.Extra, asyncStatusKnown, (*asyncStatusAlias)(s))
}

func (s AsyncStatus) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(asyncStatusAlias(s), s.Extra)
}

func (s *AsyncStep) UnmarshalJSON(b []byte) error {
	return unmarshalKeepingExtra(b, s, &s.Extra, asyncStepKnown, (*asyncStepAlias)(s))
}

func (s AsyncStep) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(asyncStepAlias(s), s.Extra)
}

func isActiveAsyncState(state string) bool { return state == "queued" || state == "running" }

func isTerminalAsyncState(state string) bool {
	switch state {
	case "complete", "failed", "partial", "paused", "stopped", "rejected":
		return true
	}
	return false
}

// ── the atomic JSON writer (atomic-json.ts): temp file in the same directory, then rename ──

const maxPathComponentBytes = 255

func randomToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// asyncWriteJSON writes payload (indented as JSON.stringify(payload, null, 2) does) to filePath atomically.
func asyncWriteJSON(filePath string, payload any) error {
	return asyncWriteJSONMode(filePath, payload, 0o644)
}

// asyncWritePrivateJSON is asyncWriteJSON with mode 0600 (the original's writePrivateAtomicJson).
func asyncWritePrivateJSON(filePath string, payload any) error {
	return asyncWriteJSONMode(filePath, payload, 0o600)
}

func asyncWriteJSONMode(filePath string, payload any, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // JSON.stringify does not escape <, > and &
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return err
	}
	body := strings.TrimSuffix(buf.String(), "\n")
	tmp := filepath.Join(filepath.Dir(filePath), tempBaseName(filePath, os.Getpid(), nowMs(), randomToken()))
	if err := os.WriteFile(tmp, []byte(body), mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func isNotExist(err error) bool { return err != nil && errors.Is(err, fs.ErrNotExist) }

// The file operations of the status and index readers, as variables so a test can count them or make one fail.
var (
	asyncStat     = os.Stat
	asyncReadFile = os.ReadFile
	asyncReadDir  = os.ReadDir
)

// readStatus is utils.ts readStatus: nil for a directory without a status file (or a name over 255 bytes), an error for a
// file that cannot be inspected, read or parsed (the errors wrap their cause).
func readStatus(asyncDir string) (*AsyncStatus, error) {
	if len(filepath.Base(asyncDir)) > 255 {
		return nil, nil
	}
	statusPath := filepath.Join(asyncDir, "status.json")
	if _, err := asyncStat(statusPath); err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to inspect async status file '%s': %w", statusPath, err)
	}
	data, err := asyncReadFile(statusPath)
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("Failed to read async status file '%s': %w", statusPath, err)
	}
	var st AsyncStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("Failed to parse async status file '%s': %s", statusPath, err.Error())
	}
	return &st, nil
}

func writeStatus(asyncDir string, st *AsyncStatus) error {
	return asyncWriteJSON(filepath.Join(asyncDir, "status.json"), st)
}

// ── index segments (index-segment.ts) ──

const maxIndexSegmentBytes = 255
const hashedIndexSegmentPrefix = "~sha256-"

var (
	windowsReservedName = regexp.MustCompile(`(?i)^(?:con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)`)
	encodedBackslash    = regexp.MustCompile(`(?i)%5C`)
	trailingExtension   = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,7}$`)
)

func hashedSegment(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hashedIndexSegmentPrefix + hex.EncodeToString(sum[:])
}

// jsEncodeURIComponent is encodeURIComponent: it fails (ok false) for a lone surrogate, which Go strings cannot hold, so the
// only failure left is invalid UTF-8.
func jsEncodeURIComponent(s string) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '!', c == '~', c == '*', c == '\'', c == '(', c == ')':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String(), true
}

func isPortableSegment(v string) bool {
	return v != "" && v != "." && v != ".." && !strings.HasSuffix(v, ".") && !windowsReservedName.MatchString(v) &&
		!encodedBackslash.MatchString(v) && !trailingExtension.MatchString(v)
}

// encodeIndexSegment keeps opaque index keys below common filesystem component limits.
func encodeIndexSegment(value string) string {
	return encodeIndexSegmentMax(value, maxIndexSegmentBytes)
}

func encodeIndexSegmentMax(value string, maxBytes int) string {
	enc, ok := jsEncodeURIComponent(value)
	if !ok {
		return hashedSegment(value)
	}
	if len(enc) <= maxBytes && isPortableSegment(enc) {
		return enc
	}
	return hashedSegment(value)
}

func historicallyReadableSegment(value string, maxBytes int) string {
	enc, ok := jsEncodeURIComponent(value)
	if !ok || enc == "" || enc == "." || enc == ".." || strings.HasSuffix(enc, ".") || windowsReservedName.MatchString(enc) || len(enc) > maxBytes {
		return ""
	}
	return enc
}

// indexSegmentAliases is the current write key first, then the pre-hash key when it still fits.
func indexSegmentAliases(value string, maxBytes int) []string {
	current := encodeIndexSegmentMax(value, maxBytes)
	if h := historicallyReadableSegment(value, maxBytes); h != "" && h != current {
		return []string{current, h}
	}
	return []string{current}
}

// ── formatters (formatters.ts, status-format.ts) ──

// bgFormatDuration is formatDuration.
func bgFormatDuration(msv int64) string {
	switch {
	case msv < 1000:
		return fmt.Sprintf("%dms", msv)
	case msv < 60000:
		return jsToFixed(float64(msv)/1000, 1) + "s"
	}
	return fmt.Sprintf("%dm%ds", msv/60000, (msv%60000)/1000)
}

// bgShortenPath is shortenPath: the home directory reads as ~.
func bgShortenPath(p string) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func bgFormatTokens(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 10000:
		return jsToFixed(float64(n)/1000, 1) + "k"
	case n < 999_500:
		return strconv.FormatInt(int64(math.Floor(float64(n)/1000+0.5)), 10) + "k"
	}
	return jsNumber(math.Round(float64(n)/1_000_000*10)/10) + "M"
}

func bgActivityAge(msv int64) string {
	switch {
	case msv < 1000:
		return "now"
	case msv < 60000:
		return fmt.Sprintf("%ds", msv/1000)
	}
	return fmt.Sprintf("%dm", msv/60000)
}

// bgActivityLabel is status-format.ts formatActivityLabel.
func bgActivityLabel(lastActivityAt *ms, activityState string, now int64) string {
	if lastActivityAt == nil {
		switch activityState {
		case "needs_attention":
			return "needs attention"
		case "active_long_running":
			return "active but long-running"
		}
		return ""
	}
	age := bgActivityAge(max(0, now-int64(*lastActivityAt)))
	switch activityState {
	case "needs_attention":
		return "no activity for " + age
	case "active_long_running":
		a := "now"
		if age != "now" {
			a = age + " ago"
		}
		return "active but long-running · last activity " + a
	}
	if age == "now" {
		return "active now"
	}
	return "active " + age + " ago"
}

var thinkingLevelNames = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// bgFormatModelThinking is formatModelThinking.
func bgFormatModelThinking(model, thinking string) string {
	display := model
	suffix := ""
	if i := strings.LastIndex(model, ":"); i >= 0 && thinkingLevels[model[i+1:]] {
		display, suffix = model[:i], model[i+1:]
	}
	level := ""
	for _, l := range thinkingLevelNames {
		if l == jsTrim(thinking) {
			level = l
		}
	}
	if suffix != "" {
		level = suffix
	}
	if display != "" {
		if i := strings.LastIndex(display, "/"); i != -1 {
			display = display[i+1:]
		}
	}
	var parts []string
	if display != "" {
		parts = append(parts, display)
	}
	if level != "" {
		parts = append(parts, "thinking "+level)
	}
	return strings.Join(parts, " · ")
}

// ── small shared helpers ──

var statusMu sync.Mutex // serializes read-modify-write of one process's status files

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func queryEscape(s string) string { return url.QueryEscape(s) }

// randomUUID is a version 4 UUID (crypto.randomUUID).
func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
