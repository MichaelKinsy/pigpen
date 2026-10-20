package warden

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pure report over the local rules records (src/rules-report.ts): for each rule, how often it was judged, how often it
// fired, how often a fired rule was cleared on a later edit, and the mean violation score, with a flag for the rules
// worth a look. It also lists the rules in the current rule set with no records. Nothing here reads or sends anything;
// the caller supplies the records and the current rule list.

// Rule flags.
const (
	flagNeverFires        = "never fires"
	flagFiresOnEverything = "fires on everything"
	flagUndecided         = "undecided"
)

const (
	reportDefaultDays = 30
	// neverFiresMin: a rule with this many judgments and no fire is flagged `never fires`.
	neverFiresMin = 20
	// firesRateMax: a fired rate above this is flagged `fires on everything`.
	firesRateMax  = 0.5
	undecidedLow  = 0.3
	undecidedHigh = 0.5
	dayMs         = 24 * 60 * 60 * 1000
)

// RuleReportRow is one rule's row of the report.
type RuleReportRow struct {
	ID, Name string
	Judged   int
	Fired    int
	// FiredRate is fired / judged.
	FiredRate float64
	// Cleared counts the judgments that cleared an earlier finding on the same path.
	Cleared       int
	MeanViolation float64
	Flags         []string
}

// NamedRule is a rule's id and name.
type NamedRule struct{ ID, Name string }

// RulesReport is the report over a window of records.
type RulesReport struct {
	Rows []RuleReportRow
	// Unheard are the rules in the current rule set with no records in the window.
	Unheard []NamedRule
	Days    int
	Since   float64
	Until   float64
	// Records counts the records inside the window.
	Records int
}

var flagRank = map[string]int{flagFiresOnEverything: 0, flagUndecided: 1, flagNeverFires: 2}

func worstRank(row RuleReportRow) int {
	if len(row.Flags) == 0 {
		return 3
	}
	worst := 3
	for _, f := range row.Flags {
		if r := flagRank[f]; r < worst {
			worst = r
		}
	}
	return worst
}

// jsLocaleCompare approximates String.prototype.localeCompare for names: letters compare without regard to case and an
// otherwise equal pair puts the lowercase form first, as ICU's default collation does.
func jsLocaleCompare(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return -strings.Compare(a, b)
}

// compareRows orders worst first: a noisy rule, then an undecided one, then a rule that never fires; ties by fired
// rate, then volume.
func compareRows(a, b RuleReportRow) int {
	if d := worstRank(a) - worstRank(b); d != 0 {
		return d
	}
	if d := b.FiredRate - a.FiredRate; d != 0 {
		if d < 0 {
			return -1
		}
		return 1
	}
	if d := b.Judged - a.Judged; d != 0 {
		return d
	}
	return jsLocaleCompare(a.Name, b.Name)
}

// ReportOptions tune buildRulesReport.
type ReportOptions struct {
	// Days is the window in days; 0 means the default.
	Days         int
	Now          time.Time
	CurrentRules []NamedRule
	// Source: live (no `source` field), calibrate (history replays), or all; "" means all.
	Source string
}

func millis(t time.Time) float64 { return float64(t.UnixNano()) / 1e6 }

func buildRulesReport(records []RuleRecord, options ReportOptions) RulesReport {
	days := options.Days
	if days == 0 {
		days = reportDefaultDays
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	until := millis(now)
	since := until - float64(days)*dayMs
	wanted := options.Source
	if wanted == "" {
		wanted = "all"
	}
	var inWindow []RuleRecord
	for _, record := range records {
		if wanted == "live" && record.Source == "calibrate" {
			continue
		}
		if wanted == "calibrate" && record.Source != "calibrate" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, record.At)
		if err != nil {
			continue
		}
		if ms := millis(at); ms >= since && ms <= until {
			inWindow = append(inWindow, record)
		}
	}
	var order []string
	groups := map[string][]RuleRecord{}
	for _, record := range inWindow {
		if _, ok := groups[record.ID]; !ok {
			order = append(order, record.ID)
		}
		groups[record.ID] = append(groups[record.ID], record)
	}
	rows := make([]RuleReportRow, 0, len(order))
	for _, id := range order {
		list := groups[id]
		judged := len(list)
		fired, cleared, middle := 0, 0, 0
		sum := 0.0
		for _, r := range list {
			if r.Finding {
				fired++
			}
			if r.Cleared {
				cleared++
			}
			sum += r.Violation
			if r.Violation > undecidedLow && r.Violation < undecidedHigh {
				middle++
			}
		}
		firedRate := float64(fired) / float64(judged)
		var flags []string
		if judged >= neverFiresMin && fired == 0 {
			flags = append(flags, flagNeverFires)
		}
		if firedRate > firesRateMax {
			flags = append(flags, flagFiresOnEverything)
		}
		if float64(middle) > float64(judged)/2 {
			flags = append(flags, flagUndecided)
		}
		rows = append(rows, RuleReportRow{ID: id, Name: list[len(list)-1].Name, Judged: judged, Fired: fired, FiredRate: firedRate, Cleared: cleared, MeanViolation: sum / float64(judged), Flags: flags})
	}
	sort.SliceStable(rows, func(i, j int) bool { return compareRows(rows[i], rows[j]) < 0 })
	heard := map[string]bool{}
	for _, row := range rows {
		heard[row.ID] = true
	}
	var unheard []NamedRule
	for _, rule := range options.CurrentRules {
		if !heard[rule.ID] {
			unheard = append(unheard, rule)
		}
	}
	return RulesReport{Rows: rows, Unheard: unheard, Days: days, Since: since, Until: until, Records: len(inWindow)}
}

// shortName collapses a rule name to one line of at most limit characters.
func shortName(name string, limit int) string {
	clean := jsTrim(reSpaceRunsJS.ReplaceAllString(name, " "))
	if utf16Len(clean) <= limit {
		return clean
	}
	return utf16Slice(clean, 0, limit-1) + "…"
}

// formatRulesReport is one rule per short line; the flags and the no-record list are named after the rows.
func formatRulesReport(report RulesReport) string {
	window := "last " + strconv.Itoa(report.Days) + " " + plural(report.Days, "day", "days")
	var lines []string
	if len(report.Rows) == 0 {
		lines = append(lines, "Rules report: no judgments in the "+window+".")
	} else {
		lines = append(lines, "Rules report: "+strconv.Itoa(report.Records)+" "+plural(report.Records, "judgment", "judgments")+", "+strconv.Itoa(len(report.Rows))+" "+plural(len(report.Rows), "rule", "rules")+", "+window+".")
		lines = append(lines, "Worst first:")
		for i, row := range report.Rows {
			firedRate := strconv.Itoa(jsRound(row.FiredRate*100)) + "%"
			flag := ""
			if len(row.Flags) > 0 {
				flag = " · " + strings.Join(row.Flags, " · ")
			}
			lines = append(lines, strconv.Itoa(i+1)+". "+shortName(row.Name, 30)+" · "+strconv.Itoa(row.Judged)+" judged · "+strconv.Itoa(row.Fired)+" fired "+firedRate+" · "+strconv.Itoa(row.Cleared)+" cleared · mean "+toFixed(row.MeanViolation, 2)+flag)
		}
	}
	if len(report.Unheard) > 0 {
		names := make([]string, len(report.Unheard))
		for i, rule := range report.Unheard {
			names[i] = shortName(rule.Name, 30)
		}
		lines = append(lines, "No records ("+strconv.Itoa(len(report.Unheard))+"): "+strings.Join(names, ", ")+".")
	}
	lines = append(lines, "Local log only; nothing is sent.")
	return strings.Join(lines, "\n")
}
