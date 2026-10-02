package doctor

import (
	"fmt"
	"strings"
)

// GroupOrder is the order groups are planned and applied in. Credentials come
// last: their directory is moved after the credential files are deleted.
var GroupOrder = []string{"packages", "legacy", "caches", "piglet-cells", "orphans", "orphans-sessions", "credentials"}

var groupTitles = map[string]string{
	"packages":         "Remove duplicate Package entries from settings.json",
	"legacy":           "Move legacy extensions directory and extensions.toml to the backup",
	"caches":           "Prune unused runtime-cell and extension caches (deleted; regenerable)",
	"piglet-cells":     "Move unused Piglet Binary cells to the backup",
	"orphans":          "Move orphaned automation directories to the backup",
	"orphans-sessions": "Move orphaned agent directories that hold sessions to the backup",
	"credentials":      "Delete credential copies in orphaned agent directories, move the rest to the backup",
}

func safetyRank(s Safety) int {
	switch s {
	case SafeAuto:
		return 0
	case NeedsConfirm:
		return 1
	}
	return 2
}

// BuildPlan turns the fixable findings of a report into groups of exact operations.
func BuildPlan(r *Report, sel Selection) (*Plan, error) {
	want := map[string]bool{}
	for _, g := range sel.Groups {
		if !contains(GroupOrder, g) {
			return nil, fmt.Errorf("unknown group %q (groups: %s)", g, strings.Join(GroupOrder, ", "))
		}
		want[g] = true
	}
	p := &Plan{Home: r.Home, AgentDir: r.AgentDir}
	for _, id := range GroupOrder {
		if len(want) > 0 && !want[id] {
			continue
		}
		g := Group{ID: id, Title: groupTitles[id], Safety: SafeAuto}
		seen := map[string]bool{}
		for _, f := range r.Findings {
			if f.Group != id || len(f.ops) == 0 {
				continue
			}
			if safetyRank(f.Safety) > safetyRank(g.Safety) {
				g.Safety = f.Safety
			}
			for _, op := range f.ops {
				k := string(op.Kind) + "\x00" + op.Path + "\x00" + op.Source
				if !seen[k] {
					seen[k] = true
					g.Ops = append(g.Ops, op)
				}
			}
		}
		if len(g.Ops) > 0 {
			p.Groups = append(p.Groups, g)
		}
	}
	return p, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// String is the exact dry-run line for the operation.
func (o Op) String() string {
	switch o.Kind {
	case OpMove:
		return fmt.Sprintf("MOVE %s -> backup (restorable)", o.Path)
	case OpDelete:
		return fmt.Sprintf("DELETE %s (not restorable)", o.Path)
	case OpRemovePackage:
		return fmt.Sprintf("EDIT %s: remove packages[%d] %q (every other byte stays; original backed up)", o.Path, o.Index, o.Source)
	}
	return fmt.Sprintf("%s %s", strings.ToUpper(string(o.Kind)), o.Path)
}

// DryRun is the exact list of operations, nothing else.
func (p *Plan) DryRun() string {
	var b strings.Builder
	if len(p.Groups) == 0 {
		return "Nothing to fix.\n"
	}
	for _, g := range p.Groups {
		fmt.Fprintf(&b, "group %s [%s]: %s (%d operations)\n", g.ID, g.Safety, g.Title, len(g.Ops))
		for _, op := range g.Ops {
			fmt.Fprintf(&b, "  - %s\n", op.String())
		}
	}
	return b.String()
}

// OpCount is the number of operations in the plan.
func (p *Plan) OpCount() int {
	n := 0
	for _, g := range p.Groups {
		n += len(g.Ops)
	}
	return n
}
