package pi_permission_system

// Port of src/exposure/tool-surface-baseline.ts: which active tools the policy withholds from the model, remembering them so a
// later policy can restore them.

type surfaceBaseline struct {
	baseline []string
	withheld map[string]bool
}

type surfaceResolution struct {
	exposed, withheld, restored []string
	changed                     bool
}

func (b *surfaceBaseline) reset() { b.baseline, b.withheld = nil, nil }

func (b *surfaceBaseline) rebuild(active []string, registered map[string]bool) []string {
	isActive := map[string]bool{}
	for _, n := range active {
		isActive[n] = true
	}
	var base []string
	for _, n := range b.baseline {
		if isActive[n] || (b.withheld[n] && registered[n]) {
			base = append(base, n)
		}
	}
	known := map[string]bool{}
	for _, n := range base {
		known[n] = true
	}
	for _, n := range active {
		if !known[n] {
			known[n] = true
			base = append(base, n)
		}
	}
	return base
}

// resolveExposed splits the baseline into the tools the policy exposes and the ones it withholds.
func (b *surfaceBaseline) resolveExposed(active []string, registered map[string]bool, isExposed func(string) bool) surfaceResolution {
	base := b.rebuild(active, registered)
	var r surfaceResolution
	for _, n := range base {
		if isExposed(n) {
			r.exposed = append(r.exposed, n)
		} else {
			r.withheld = append(r.withheld, n)
		}
	}
	for _, n := range r.exposed {
		if b.withheld[n] {
			r.restored = append(r.restored, n)
		}
	}
	r.changed = len(b.withheld) != len(r.withheld)
	for _, n := range r.withheld {
		if !b.withheld[n] {
			r.changed = true
		}
	}
	b.baseline = base
	b.withheld = map[string]bool{}
	for _, n := range r.withheld {
		b.withheld[n] = true
	}
	return r
}
