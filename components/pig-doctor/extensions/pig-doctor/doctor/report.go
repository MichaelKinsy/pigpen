package doctor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Check inspects the PiG home and returns a report. It never changes anything.
func Check(o Options) (*Report, error) {
	inv, err := scan(o)
	if err != nil {
		return nil, err
	}
	return inv.report(), nil
}

func (inv *inventory) report() *Report {
	return &Report{Home: inv.home, AgentDir: inv.agent, Findings: inv.findings, Unknown: inv.unknown, Processes: inv.procInfo,
		CacheSizes: inv.sizes, Notes: inv.notes, Generated: inv.now}
}

// Find returns the findings with the given ID.
func (r *Report) Find(id string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

func autoText(s Safety) string {
	switch s {
	case SafeAuto:
		return "yes, safe (applied by --yes)"
	case NeedsConfirm:
		return "yes, after an interactive confirmation"
	case Credentials:
		return "yes, only after you type 'remove credentials'"
	}
	return "no, manual"
}

// Text is the human report: findings grouped by severity, then what was left alone.
func (r *Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pig-doctor check\n  PiG home:  %s\n  agent dir: %s\n\n", r.Home, r.AgentDir)
	sections := []struct {
		sev   Severity
		title string
	}{{SevError, "ERRORS"}, {SevWarn, "WARNINGS"}, {SevInfo, "NOTES"}}
	any := false
	for _, s := range sections {
		var fs []Finding
		for _, f := range r.Findings {
			if f.Severity == s.sev {
				fs = append(fs, f)
			}
		}
		if len(fs) == 0 {
			continue
		}
		any = true
		fmt.Fprintf(&b, "%s (%d)\n", s.title, len(fs))
		for _, f := range fs {
			fmt.Fprintf(&b, "\n  [%s] %s\n", f.ID, f.Title)
			fmt.Fprintf(&b, "     why:      %s\n", f.Why)
			fmt.Fprintf(&b, "     fix:      %s\n", f.Fix)
			fmt.Fprintf(&b, "     auto-fix: %s\n", autoText(f.Safety))
			for _, d := range f.Detail {
				fmt.Fprintf(&b, "       - %s\n", d)
			}
		}
		b.WriteString("\n")
	}
	if !any {
		b.WriteString("No problems found.\n\n")
	}
	if len(r.CacheSizes) > 0 {
		b.WriteString("CACHES\n")
		for _, c := range r.CacheSizes {
			fmt.Fprintf(&b, "  %-12s %s  (%d entries, %d in use, %d prunable)\n", humanBytes(c.Bytes), c.Path, c.Entries, c.InUse, c.Prune)
		}
		b.WriteString("\n")
	}
	if len(r.Processes) > 0 {
		b.WriteString("RUNNING PIG PROCESSES (their locks and files are never touched)\n")
		for _, p := range r.Processes {
			extra := ""
			if p.AgentDir != "" {
				extra = "  agent dir " + p.AgentDir
			}
			fmt.Fprintf(&b, "  %d  %s%s\n", p.PID, p.Executable, extra)
		}
		b.WriteString("\n")
	}
	if len(r.Notes) > 0 {
		b.WriteString("NOT FOLLOWED / LIMITS\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "  %s\n", n)
		}
		b.WriteString("\n")
	}
	if len(r.Unknown) > 0 {
		b.WriteString("Not recognised (unknown, left alone): the doctor never reads, moves or deletes these\n")
		for _, u := range r.Unknown {
			fmt.Fprintf(&b, "  %s\n", u)
		}
		b.WriteString("\n")
	}
	b.WriteString("This was a read-only check. Nothing was changed. `pig-doctor fix --dry-run` prints the exact operations a fix would perform.\n")
	return b.String()
}

// JSON renders the report as indented JSON.
func (r *Report) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}
