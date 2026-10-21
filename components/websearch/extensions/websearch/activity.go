package websearch

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/width"
)

// Port of activity.ts: the log of the last ten search API calls and page fetches that the activity
// widget shows, with the rate limit a provider reports. One monitor per process, like the
// original's module-level `activityMonitor`.

// ActivityEntry is one logged call. Status is nil while the call is pending or after a network
// error, 0 for an aborted call, else the HTTP status. EndTime is 0 while the call is open.
type ActivityEntry struct {
	ID        string
	Type      string // "api" or "fetch"
	StartTime int64
	EndTime   int64
	Query     string // api calls
	URL       string // fetches
	Status    *int
	Error     string
}

// RateLimitInfo is the rate limit state a provider reported.
type RateLimitInfo struct {
	Used            int
	Max             int
	OldestTimestamp *int64
	WindowMs        int64
}

func defaultRateLimit() RateLimitInfo { return RateLimitInfo{Used: 0, Max: 10, WindowMs: 60000} }

const maxActivityEntries = 10

var httpSchemeRE = regexp.MustCompile(`^https?://`)

// ActivityMonitor is ActivityMonitor of activity.ts.
type ActivityMonitor struct {
	mu        sync.Mutex
	entries   []ActivityEntry
	rate      RateLimitInfo
	nextID    int
	listeners map[int]func()
	nextL     int
}

// NewActivityMonitor returns an empty monitor.
func NewActivityMonitor() *ActivityMonitor {
	return &ActivityMonitor{rate: defaultRateLimit(), nextID: 1, listeners: map[int]func(){}}
}

// Activity is the process-wide monitor the providers and fetches log to.
var Activity = NewActivityMonitor()

// LogStart opens an entry (logStart) and returns its id. kind is "api" (target is the query) or
// "fetch" (target is the URL).
func (m *ActivityMonitor) LogStart(kind, target string) string {
	m.mu.Lock()
	id := "act-" + strconv.Itoa(m.nextID)
	m.nextID++
	e := ActivityEntry{ID: id, Type: kind, StartTime: nowMs()}
	if kind == "api" {
		e.Query = target
	} else {
		e.URL = target
	}
	m.entries = append(m.entries, e)
	if len(m.entries) > maxActivityEntries {
		m.entries = m.entries[1:]
	}
	m.mu.Unlock()
	m.notify()
	return id
}

// LogComplete closes an entry with an HTTP status (logComplete).
func (m *ActivityMonitor) LogComplete(id string, status int) {
	m.mu.Lock()
	found := false
	for i := range m.entries {
		if m.entries[i].ID == id {
			m.entries[i].EndTime = nowMs()
			s := status
			m.entries[i].Status = &s
			found = true
			break
		}
	}
	m.mu.Unlock()
	if found {
		m.notify()
	}
}

// LogError closes an entry with an error (logError).
func (m *ActivityMonitor) LogError(id, message string) {
	m.mu.Lock()
	found := false
	for i := range m.entries {
		if m.entries[i].ID == id {
			m.entries[i].EndTime = nowMs()
			m.entries[i].Error = message
			found = true
			break
		}
	}
	m.mu.Unlock()
	if found {
		m.notify()
	}
}

// Entries is a copy of the logged entries, oldest first.
func (m *ActivityMonitor) Entries() []ActivityEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ActivityEntry, len(m.entries))
	for i, e := range m.entries {
		out[i] = e
		if e.Status != nil {
			s := *e.Status
			out[i].Status = &s
		}
	}
	return out
}

// RateLimit is the last reported rate limit (getRateLimitInfo).
func (m *ActivityMonitor) RateLimit() RateLimitInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rate
}

// UpdateRateLimit stores a provider's rate limit state (updateRateLimit).
func (m *ActivityMonitor) UpdateRateLimit(info RateLimitInfo) {
	m.mu.Lock()
	m.rate = info
	m.mu.Unlock()
	m.notify()
}

// OnUpdate subscribes cb to every change and returns the unsubscribe function (onUpdate).
func (m *ActivityMonitor) OnUpdate(cb func()) func() {
	m.mu.Lock()
	id := m.nextL
	m.nextL++
	m.listeners[id] = cb
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.listeners, id)
		m.mu.Unlock()
	}
}

// Clear drops the entries and the rate limit state (clear). The id counter keeps counting, as it
// does upstream.
func (m *ActivityMonitor) Clear() {
	m.mu.Lock()
	m.entries = nil
	m.rate = defaultRateLimit()
	m.mu.Unlock()
	m.notify()
}

// notify calls the listeners in subscription order outside the lock; a panic in one is ignored
// (the original swallows listener errors).
func (m *ActivityMonitor) notify() {
	m.mu.Lock()
	ids := make([]int, 0, len(m.listeners))
	for id := range m.listeners {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sortInts(ids)
	for _, id := range ids {
		m.mu.Lock()
		cb := m.listeners[id]
		m.mu.Unlock()
		if cb == nil {
			continue
		}
		func() {
			defer func() { _ = recover() }()
			cb()
		}()
	}
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// ---- call sites -------------------------------------------------------------------------------------

// activityRef is the id a provider or fetch logs its outcome under.
type activityRef struct{ id string }

func logAPIStart(query string) activityRef { return activityRef{Activity.LogStart("api", query)} }
func logFetchStart(url string) activityRef { return activityRef{Activity.LogStart("fetch", url)} }

// complete is logComplete(id, status).
func (a activityRef) complete(status int) { Activity.LogComplete(a.id, status) }

// logError is logError(id, message).
func (a activityRef) logError(message string) { Activity.LogError(a.id, message) }

// failed is the catch block the providers share: a message that mentions an abort closes the
// entry as aborted (status 0), anything else records the message.
func (a activityRef) failed(message string) {
	if strings.Contains(strings.ToLower(message), "abort") {
		a.complete(0)
		return
	}
	a.logError(message)
}

// failedErr is failed for an error, returning it so a return site reads `return nil, act.failedErr(err)`.
// A cancellation completes the entry as aborted and reads as the AbortError the original's fetch rejects
// with, "This operation was aborted" (the cancellation stays the cause).
func (a activityRef) failedErr(err error) error {
	if errors.Is(err, context.Canceled) {
		a.complete(0)
		return &redactedError{"This operation was aborted", err}
	}
	a.failed(err.Error())
	return err
}

// failedBy is the catch block of the providers that tell the caller's cancellation from their own
// failure (`if (options.signal?.aborted)`): a cancelled caller completes the entry with status 0,
// anything else records the message with the credential removed. It returns err for `return nil, ...`.
func (a activityRef) failedBy(parent context.Context, err error, secret string) error {
	if parent.Err() != nil {
		a.complete(0)
		return err
	}
	a.logError(RedactCredential(err.Error(), secret))
	return err
}

// logged runs fn as the try block of the providers that complete their entry with status 200 when
// it returns and send a failure through their catch block: the message with the credential removed
// closes the entry as aborted (status 0) when it mentions an abort, else it is recorded.
func logged[T any](act activityRef, secret string, fn func() (T, error)) (T, error) {
	v, err := fn()
	if err != nil {
		var zero T
		return zero, act.failedErr(redactErr(err, secret))
	}
	act.complete(200)
	return v, nil
}

// loggedBy is logged for the providers whose catch block asks the caller's signal instead of the message
// (`if (options.signal?.aborted)`).
func loggedBy[T any](act activityRef, parent context.Context, secret string, fn func() (T, error)) (T, error) {
	v, err := fn()
	if err != nil {
		var zero T
		return zero, act.failedBy(parent, redactErr(err, secret), secret)
	}
	act.complete(200)
	return v, nil
}

// ---- the widget -----------------------------------------------------------------------------

// WidgetTheme is the part of the UI theme the widget and the renderers use (theme.fg, theme.bg,
// theme.bold of Pi's Theme).
type WidgetTheme interface {
	Fg(token, text string) string
	Bg(token, text string) string
	Bold(text string) string
}

// ActivityWidgetKey is the key of the widget (ctx.ui.setWidget("web-activity", ...)).
const ActivityWidgetKey = "web-activity"

// ActivityWidgetLines is updateWidget() of index.ts:995-1021 as the lines it passes to setWidget.
// now is Date.now.
func ActivityWidgetLines(m *ActivityMonitor, th WidgetTheme, now int64) []string {
	entries := m.Entries()
	lines := []string{th.Fg("accent", "─── Web Search Activity "+strings.Repeat("─", 36))}
	if len(entries) == 0 {
		lines = append(lines, th.Fg("muted", "  No activity yet"))
	} else {
		for _, e := range entries {
			lines = append(lines, "  "+formatActivityEntryLine(e, th, now))
		}
	}
	lines = append(lines, th.Fg("accent", strings.Repeat("─", 60)))

	rate := m.RateLimit()
	var resetMs int64
	if rate.OldestTimestamp != nil && *rate.OldestTimestamp != 0 {
		resetMs = max(0, *rate.OldestTimestamp+rate.WindowMs-now)
	}
	resetSec := (resetMs + 999) / 1000
	line := th.Fg("muted", fmt.Sprintf("Rate: %d/%d", rate.Used, rate.Max))
	if resetMs > 0 {
		line += th.Fg("dim", fmt.Sprintf(" (resets in %ds)", resetSec))
	}
	return append(lines, line)
}

// formatActivityEntryLine is formatEntryLine (index.ts:1023-1054).
func formatActivityEntryLine(e ActivityEntry, th WidgetTheme, now int64) string {
	typeStr := "GET"
	var target string
	if e.Type == "api" {
		typeStr = "API"
		target = `"` + truncateToWidth(e.Query, 28) + `"`
	} else {
		target = truncateToWidth(httpSchemeRE.ReplaceAllString(e.URL, ""), 30)
	}
	end := now
	if e.EndTime != 0 {
		end = e.EndTime
	}
	duration := jsToFixed1(float64(end-e.StartTime)/1000) + "s"

	var statusStr, indicator string
	switch {
	case e.Error != "":
		statusStr, indicator = "err", th.Fg("error", "✗")
	case e.Status == nil:
		statusStr, indicator = "...", th.Fg("warning", "⋯")
	case *e.Status == 0:
		statusStr, indicator = "abort", th.Fg("muted", "○")
	default:
		statusStr = strconv.Itoa(*e.Status)
		if *e.Status >= 200 && *e.Status < 300 {
			indicator = th.Fg("success", "✓")
		} else {
			indicator = th.Fg("error", "✗")
		}
	}
	return padEndWidth(typeStr, 4) + " " + padEndWidth(target, 32) + " " + padStart(statusStr, 5) + " " + padStart(duration, 5) + " " + indicator
}

// jsToFixed1 is Number.prototype.toFixed(1): the integer n for which
// n/10 is closest to f, the larger one on an exact tie, computed on the exact binary value.
func jsToFixed1(f float64) string {
	if f < 0 {
		return "-" + jsToFixed1(-f)
	}
	x := new(big.Float).SetPrec(256).SetFloat64(f)
	x.Mul(x, big.NewFloat(10))
	x.Add(x, big.NewFloat(0.5))
	n, _ := x.Int(nil)
	s := n.String()
	if len(s) == 1 {
		s = "0" + s
	}
	return s[:len(s)-1] + "." + s[len(s)-1:]
}

// padStart is String.padStart by UTF-16 length.
func padStart(s string, n int) string {
	if l := jsLen(s); l < n {
		return strings.Repeat(" ", n-l) + s
	}
	return s
}

// padEndWidth is String.padEnd by UTF-16 length (the original pads by .length, not by cells).
func padEndWidth(s string, n int) string {
	if l := jsLen(s); l < n {
		return s + strings.Repeat(" ", n-l)
	}
	return s
}

// truncateToWidth is pi-tui's truncateToWidth(text, maxWidth, "") for plain text: the longest
// prefix that fits maxWidth terminal cells.
func truncateToWidth(text string, maxWidth int) string {
	w := 0
	for i, r := range text {
		rw := runeCells(r)
		if w+rw > maxWidth {
			return text[:i]
		}
		w += rw
	}
	return text
}

// runeCells is the width of one rune in terminal cells: 0 for combining marks and joiners, 2 for
// wide and fullwidth characters, else 1.
func runeCells(r rune) int {
	switch {
	case r == 0x200d || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r):
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}
