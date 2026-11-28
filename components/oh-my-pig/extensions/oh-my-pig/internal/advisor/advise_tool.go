package advisor

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/advisor/emission-guard.ts
// upstream: packages/coding-agent/src/advisor/advise-tool.ts
// upstream: packages/coding-agent/src/prompts/advisor/advise-tool.md

// Severity mirrors omp's AdvisorSeverity (`"nit" | "concern" | "blocker"`).
type Severity string

const (
	SeverityNit     Severity = "nit"
	SeverityConcern Severity = "concern"
	SeverityBlocker Severity = "blocker"
)

// Note is one admitted advisor note awaiting or recorded for delivery.
type Note struct {
	Note     string   `json:"note"`
	Severity Severity `json:"severity,omitempty"`
	Advisor  string   `json:"advisor,omitempty"`
	Turn     int      `json:"turn,omitempty"`
	HasTurn  bool     `json:"hasTurn,omitempty"`
}

// SuppressionReason classifies why EmissionGuard rejected a note.
type SuppressionReason string

const (
	SuppressEmpty     SuppressionReason = "empty"
	SuppressNoise     SuppressionReason = "noise"
	SuppressDuplicate SuppressionReason = "duplicate"
	SuppressRateLimit SuppressionReason = "rate-limit"
)

const (
	// DefaultBudgetPerUpdate matches ADVISOR_DEFAULT_BUDGET_PER_UPDATE (4).
	DefaultBudgetPerUpdate = 4
	// MaxBudgetPerUpdate matches ADVISOR_MAX_BUDGET_PER_UPDATE (32).
	MaxBudgetPerUpdate = 32
	// DefaultHistoryCapacity matches DEFAULT_HISTORY_CAPACITY (4096).
	DefaultHistoryCapacity = 4096

	// AdvisorGuidance matches ADVISOR_GUIDANCE in advise-tool.ts:38.
	AdvisorGuidance = "weigh, don't blindly obey"

	// AdviseToolDescription mirrors prompts/advisor/advise-tool.md.
	AdviseToolDescription = "Watched agent: send 1 concrete, terse advice.\nUse sparingly; stay silent when nothing matters.\nCall to avert likely-wrong or materially wasteful work."
)

// Ack strings matching advise-tool.ts:192-203.
const (
	ackSent      = "Delivered."
	ackDeferred  = "Queued for the end of the turn. Do not re-raise."
	ackCancelled = "Dropped: this review was cancelled."
)

var ackSuppressed = map[SuppressionReason]string{
	SuppressEmpty:     "Dropped: empty note.",
	SuppressNoise:     "Dropped: nothing actionable.",
	SuppressDuplicate: "Dropped: already raised.",
	SuppressRateLimit: "Dropped: this update's advice budget is spent.",
}

// suppressedNormalizedPhrases mirrors SUPPRESSED_NORMALIZED_PHRASES in emission-guard.ts:52-94.
var suppressedNormalizedPhrases = map[string]bool{
	"stop":                            true,
	"stop here":                       true,
	"stop now":                        true,
	"halt":                            true,
	"abort":                           true,
	"done":                            true,
	"task done":                       true,
	"task complete":                   true,
	"complete":                        true,
	"finished":                        true,
	"ok":                              true,
	"okay":                            true,
	"ok done":                         true,
	"no issue":                        true,
	"no issues":                       true,
	"no issue continue":               true,
	"no concerns":                     true,
	"no concern":                      true,
	"nothing to add":                  true,
	"nothing to flag":                 true,
	"nothing to report":               true,
	"no notes":                        true,
	"no further input":                true,
	"no further input needed":         true,
	"no further input required":       true,
	"no further watcher input":        true,
	"no further watcher input needed": true,
	"no further advice":               true,
	"no further advice needed":        true,
	"lgtm":                            true,
	"looks good":                      true,
	"all good":                        true,
	"agent is on track":               true,
	"agent on track":                  true,
	"on track":                        true,
	"continue":                        true,
	"carry on":                        true,
}

// NormalizeAdvisorNote folds casing and punctuation into a single-space key.
// upstream: packages/coding-agent/src/advisor/emission-guard.ts:33-39
func NormalizeAdvisorNote(note string) string {
	var b strings.Builder
	inSep := false
	for _, r := range strings.ToLower(note) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if inSep && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			inSep = false
		} else {
			inSep = true
		}
	}
	return strings.TrimSpace(b.String())
}

// SeverityRank ranks severities: nit (or empty)=1, concern=2, blocker=3.
func SeverityRank(s Severity) int {
	switch s {
	case SeverityBlocker:
		return 3
	case SeverityConcern:
		return 2
	default:
		return 1
	}
}

// ParseSeverity normalizes a string severity; returns "" for empty/unknown.
func ParseSeverity(raw string) Severity {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "nit":
		return SeverityNit
	case "concern":
		return SeverityConcern
	case "blocker":
		return SeverityBlocker
	default:
		return ""
	}
}

// IsInterruptingSeverity reports whether concern/blocker should interrupt.
func IsInterruptingSeverity(s Severity) bool {
	return s == SeverityConcern || s == SeverityBlocker
}

// MeetsSeverity reports whether s >= minimum.
func MeetsSeverity(s, minimum Severity) bool {
	return SeverityRank(s) >= SeverityRank(minimum)
}

// IsInterruptImmuneActive mirrors isAdvisorInterruptImmuneTurnActive (advise-tool.ts:78-85).
func IsInterruptImmuneActive(completedTurns int, immuneTurnStart *int, immuneTurns int) bool {
	if immuneTurnStart == nil || immuneTurns <= 0 {
		return false
	}
	return completedTurns < *immuneTurnStart+immuneTurns
}

// FormatBatchContent renders notes as `<advisory ...>` XML blocks matching
// omp's `formatAdvisorBatchContent` (advise-tool.ts:46-58).
func FormatBatchContent(notes []Note, currentTurn *int) string {
	if len(notes) == 0 {
		return ""
	}
	ordered := make([]Note, len(notes))
	copy(ordered, notes)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Turn != ordered[j].Turn {
			return ordered[i].Turn > ordered[j].Turn
		}
		return SeverityRank(ordered[i].Severity) > SeverityRank(ordered[j].Severity)
	})
	parts := make([]string, 0, len(ordered))
	for _, n := range ordered {
		var attrs strings.Builder
		if n.Advisor != "" {
			fmt.Fprintf(&attrs, ` advisor="%s"`, escapeXMLAttr(n.Advisor))
		}
		if n.Severity != "" {
			fmt.Fprintf(&attrs, ` severity="%s"`, escapeXMLAttr(string(n.Severity)))
		}
		if currentTurn != nil && n.HasTurn && *currentTurn > n.Turn {
			fmt.Fprintf(&attrs, ` turns_ago="%d"`, *currentTurn-n.Turn)
		}
		fmt.Fprintf(&attrs, ` guidance="%s"`, AdvisorGuidance)
		parts = append(parts, fmt.Sprintf("<advisory%s>\n%s\n</advisory>", attrs.String(), escapeXMLText(n.Note)))
	}
	return strings.Join(parts, "\n")
}

func escapeXMLAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeXMLText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// Admission is the decision returned by EmissionGuard.Admit.
type Admission struct {
	Accepted     bool
	Reason       SuppressionReason
	DisplacedKey string
}

type budgetSlot struct {
	key     string
	rank    int
	pending bool
}

// EmissionGuard enforces noise filtering, rank-aware session dedupe, and
// per-update non-blocker budgets (blockers exempt).
// upstream: packages/coding-agent/src/advisor/emission-guard.ts:164-323
type EmissionGuard struct {
	mu              sync.Mutex
	seen            map[string]int
	seenOrder       []string
	slots           []budgetSlot
	capacity        int
	budgetPerUpdate int
}

// NewEmissionGuard constructs an EmissionGuard with clamped budget and capacity.
func NewEmissionGuard(budgetPerUpdate, capacity int) *EmissionGuard {
	if budgetPerUpdate <= 0 {
		budgetPerUpdate = DefaultBudgetPerUpdate
	}
	if budgetPerUpdate > MaxBudgetPerUpdate {
		budgetPerUpdate = MaxBudgetPerUpdate
	}
	if capacity <= 0 {
		capacity = DefaultHistoryCapacity
	}
	return &EmissionGuard{
		seen:            make(map[string]int),
		capacity:        capacity,
		budgetPerUpdate: budgetPerUpdate,
	}
}

// Reset clears all dedupe and per-update state.
func (g *EmissionGuard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seen = make(map[string]int)
	g.seenOrder = nil
	g.slots = nil
}

// BeginUpdate clears the per-update budget slots for a new review cycle.
func (g *EmissionGuard) BeginUpdate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.slots = nil
}

// Admit checks noise, rank-aware dedupe, and per-update budget.
func (g *EmissionGuard) Admit(note string, rank int, pending bool) Admission {
	g.mu.Lock()
	defer g.mu.Unlock()

	key := NormalizeAdvisorNote(note)
	if key == "" {
		return Admission{Accepted: false, Reason: SuppressEmpty}
	}
	if suppressedNormalizedPhrases[key] {
		return Admission{Accepted: false, Reason: SuppressNoise}
	}
	if rank <= g.seen[key] {
		return Admission{Accepted: false, Reason: SuppressDuplicate}
	}

	var displacedKey string
	ownIdx := -1
	for i := range g.slots {
		if g.slots[i].key == key {
			ownIdx = i
			break
		}
	}

	if rank >= 3 {
		// Blockers are exempt from the per-update budget.
		if ownIdx >= 0 && g.slots[ownIdx].pending {
			g.slots = append(g.slots[:ownIdx], g.slots[ownIdx+1:]...)
		}
	} else if ownIdx >= 0 {
		g.slots[ownIdx].rank = rank
	} else if len(g.slots) < g.budgetPerUpdate {
		g.slots = append(g.slots, budgetSlot{key: key, rank: rank, pending: pending})
	} else {
		minIdx := -1
		for i, sl := range g.slots {
			if !sl.pending {
				continue
			}
			if minIdx < 0 || sl.rank < g.slots[minIdx].rank {
				minIdx = i
			}
		}
		if minIdx >= 0 && rank > g.slots[minIdx].rank {
			displacedKey = g.slots[minIdx].key
			g.slots[minIdx] = budgetSlot{key: key, rank: rank, pending: pending}
		} else {
			return Admission{Accepted: false, Reason: SuppressRateLimit}
		}
	}

	g.recordRankLocked(key, rank)
	return Admission{Accepted: true, DisplacedKey: displacedKey}
}

func (g *EmissionGuard) recordRankLocked(key string, rank int) {
	_, exists := g.seen[key]
	g.seen[key] = rank
	if exists {
		return
	}
	g.seenOrder = append(g.seenOrder, key)
	if len(g.seenOrder) > g.capacity {
		stale := g.seenOrder[0]
		g.seenOrder = g.seenOrder[1:]
		delete(g.seen, stale)
	}
}

// BuildAdviseToolDefinition creates the child-only `advise` custom tool passed
// exclusively to advisor child sessions (NEVER registered as an active tool on
// primary sessions).
// upstream: packages/coding-agent/src/advisor/advise-tool.ts:16-22,205-366
func BuildAdviseToolDefinition(guard *EmissionGuard, advisorName string, coveredTurn int, onAdvice func(n Note)) sdk.ToolDefinition {
	if guard == nil {
		guard = NewEmissionGuard(DefaultBudgetPerUpdate, DefaultHistoryCapacity)
	}
	return sdk.ToolDefinition{
		Name:        "advise",
		Label:       "Advise",
		Description: AdviseToolDescription,
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"note": map[string]any{
					"type":        "string",
					"description": "One concrete piece of advice for the agent you are watching. Terse, specific, actionable.",
				},
				"severity": map[string]any{
					"type":        "string",
					"enum":        []string{"nit", "concern", "blocker"},
					"description": "How strongly to weigh this. Omit for a plain nit.",
				},
			},
			"required":             []string{"note"},
			"additionalProperties": false,
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			if ctx.Done() != nil {
				select {
				case <-ctx.Done():
					return sdk.ToolResult{Content: ackCancelled}, nil
				default:
				}
			}
			noteText, _ := params["note"].(string)
			sevStr, _ := params["severity"].(string)
			sev := ParseSeverity(sevStr)
			rank := SeverityRank(sev)

			dec := guard.Admit(noteText, rank, false)
			if !dec.Accepted {
				msg := ackSuppressed[dec.Reason]
				if msg == "" {
					msg = ackSuppressed[SuppressDuplicate]
				}
				return sdk.ToolResult{
					Content: msg,
					Details: map[string]any{"note": noteText, "severity": string(sev), "suppressed": string(dec.Reason)},
				}, nil
			}
			if onAdvice != nil {
				onAdvice(Note{
					Note:     strings.TrimSpace(noteText),
					Severity: sev,
					Advisor:  advisorName,
					Turn:     coveredTurn,
					HasTurn:  coveredTurn > 0,
				})
			}
			return sdk.ToolResult{
				Content: ackSent,
				Details: map[string]any{"note": noteText, "severity": string(sev), "advisor": advisorName},
			}, nil
		},
	}
}
