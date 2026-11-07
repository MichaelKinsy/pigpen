// Package lsdiff compares two request logs written by mockls (the JavaScript original and the Go port) and
// reports the first difference after the documented normalisations (Rules).
package lsdiff

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"lstools/internal/reqlog"
)

// Options configure a comparison.
type Options struct {
	Rules        []Rule   // N7
	PathPrefixes []string // extra N6 prefixes
	Max          int      // how many differences to report (default 1)
	// UnorderedDrain compares consecutive ingest requests (multipart, batch) as a multiset: the batches of one queue drain are
	// sent concurrently, and which reaches the wire first is scheduler order (microtask depth in the JS SDK, goroutines in Go),
	// not a contract. Everything else, and the content of every request, is still compared.
	UnorderedDrain bool
	HostPrompt     bool // N8: A is Pi and B is PiG (or the reverse): host system prompt text is not compared
}

// Diff is one difference between the logs.
type Diff struct {
	Request int    // 1-based request index in both logs (0: logs differ in length)
	Where   string // what differs: header, path, part name, ...
	A, B    string // the values after normalisation (shortened around the first differing byte)
	Note    string
}

// Result is the outcome of a comparison.
type Result struct {
	Diffs          []Diff
	FiredA, FiredB map[string]int
	NA, NB         int
	// Anomalies are things wrong with a log on its own (a body that did not parse, a part whose length= is not its size):
	// two logs can agree on them, so they are listed even when nothing differs, and make the exit status 3.
	Anomalies []string
}

// Same reports whether the logs are identical after normalisation.
func (r *Result) Same() bool { return len(r.Diffs) == 0 }

// Compare normalises both logs and compares them request by request.
func Compare(a, b []*reqlog.Entry, o Options) (*Result, error) {
	if o.Max <= 0 {
		o.Max = 1
	}
	var drainsA, drainsB int
	if o.UnorderedDrain {
		a, drainsA = reorderDrains(a)
		b, drainsB = reorderDrains(b)
	}
	za, err := NewNormalizer(o.Rules, o.PathPrefixes)
	if err != nil {
		return nil, err
	}
	zb, _ := NewNormalizer(o.Rules, o.PathPrefixes)
	za.hostPrompt, zb.hostPrompt = o.HostPrompt, o.HostPrompt
	za.Prepare(a)
	zb.Prepare(b)
	res := &Result{NA: len(a), NB: len(b)}
	na := make([]*nEntry, len(a))
	nb := make([]*nEntry, len(b))
	for i, e := range a {
		na[i] = za.Entry(e)
	}
	for i, e := range b {
		nb[i] = zb.Entry(e)
	}
	za.fire("N9", drainsA)
	zb.fire("N9", drainsB)
	res.FiredA, res.FiredB = za.Fired, zb.Fired
	for _, side := range []struct {
		name string
		es   []*nEntry
	}{{"A", na}, {"B", nb}} {
		for i, e := range side.es {
			if e.ParseError != "" {
				res.Anomalies = append(res.Anomalies, fmt.Sprintf("log %s request %d (%s %s): %s", side.name, i+1, e.Method, e.Path, e.ParseError))
			}
			for _, p := range e.Problems {
				res.Anomalies = append(res.Anomalies, fmt.Sprintf("log %s request %d (%s %s): %s", side.name, i+1, e.Method, e.Path, p))
			}
		}
	}
	add := func(d Diff) bool {
		res.Diffs = append(res.Diffs, d)
		return len(res.Diffs) >= o.Max
	}
	n := min(len(na), len(nb))
	for i := 0; i < n; i++ {
		for _, d := range compareEntry(na[i], nb[i]) {
			d.Request = i + 1
			if add(d) {
				return res, nil
			}
		}
	}
	if len(na) != len(nb) {
		longer, who := na, "A"
		if len(nb) > len(na) {
			longer, who = nb, "B"
		}
		extra := longer[n]
		add(Diff{Where: "request count", A: fmt.Sprint(len(na)), B: fmt.Sprint(len(nb)),
			Note: fmt.Sprintf("log %s has request %d with no counterpart: %s %s%s (route %s)", who, n+1, extra.Method, extra.Path, queryOf(extra), extra.Route)})
	}
	return res, nil
}

func queryOf(e *nEntry) string {
	if e.Query == "" {
		return ""
	}
	return "?" + e.Query
}

func compareEntry(a, b *nEntry) []Diff {
	var out []Diff
	field := func(where, x, y string) bool {
		if x != y {
			out = append(out, Diff{Where: where, A: x, B: y})
			return true
		}
		return false
	}
	field("route", a.Route, b.Route)
	field("method", a.Method, b.Method)
	field("path", a.Path, b.Path)
	field("query", a.Query, b.Query)
	for _, k := range union(a.Headers, b.Headers) {
		x, okx := a.Headers[k]
		y, oky := b.Headers[k]
		if !okx {
			x = "<absent>"
		}
		if !oky {
			y = "<absent>"
		}
		field("header "+k, x, y)
	}
	field("transfer framing", a.Transfer, b.Transfer)
	field("content-encoding", a.Encoding, b.Encoding)
	field("boundary shape", a.BoundaryBad, b.BoundaryBad)
	field("body parse error", a.ParseError, b.ParseError)
	field("body problems", strings.Join(a.Problems, "; "), strings.Join(b.Problems, "; "))
	if a.Multipart != b.Multipart {
		out = append(out, Diff{Where: "body kind", A: kind(a), B: kind(b)})
	} else if a.Multipart {
		out = append(out, compareParts(a, b)...)
	} else {
		if x, y := a.Body, b.Body; x != y {
			off, ca, cb := around(x, y)
			out = append(out, Diff{Where: "body", A: ca, B: cb, Note: fmt.Sprintf("first differing byte at offset %d", off)})
		}
	}
	return out
}

func kind(e *nEntry) string {
	if e.Multipart {
		return "multipart"
	}
	return "not multipart"
}

func compareParts(a, b *nEntry) []Diff {
	var out []Diff
	if len(a.Parts) != len(b.Parts) {
		out = append(out, Diff{Where: "part count", A: fmt.Sprint(len(a.Parts)), B: fmt.Sprint(len(b.Parts)), Note: partNames(a, b)})
	}
	for i := 0; i < min(len(a.Parts), len(b.Parts)); i++ {
		pa, pb := a.Parts[i], b.Parts[i]
		if pa.Name != pb.Name {
			out = append(out, Diff{Where: fmt.Sprintf("part %d name", i+1), A: pa.Name, B: pb.Name})
			continue
		}
		if pa.Header != pb.Header {
			off, ca, cb := around(pa.Header, pb.Header)
			out = append(out, Diff{Where: fmt.Sprintf("part %d %q header", i+1, pa.Name), A: ca, B: cb, Note: fmt.Sprintf("first differing byte at offset %d", off)})
		}
		if pa.Body != pb.Body {
			off, ca, cb := around(pa.Body, pb.Body)
			out = append(out, Diff{Where: fmt.Sprintf("part %d %q bytes", i+1, pa.Name), A: ca, B: cb, Note: fmt.Sprintf("first differing byte at offset %d", off)})
		}
	}
	return out
}

func partNames(a, b *nEntry) string {
	names := func(e *nEntry) string {
		var s []string
		for _, p := range e.Parts {
			s = append(s, p.Name)
		}
		return strings.Join(s, ", ")
	}
	return "A parts: " + names(a) + " | B parts: " + names(b)
}

func union(a, b map[string]string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// around finds the first differing byte of x and y and returns it with ~50 bytes of context from each.
func around(x, y string) (int, string, string) {
	i := 0
	for i < len(x) && i < len(y) && x[i] == y[i] {
		i++
	}
	cut := func(s string) string {
		lo, hi := max(0, i-40), min(len(s), i+60)
		out := s[lo:hi]
		if lo > 0 {
			out = "..." + out
		}
		if hi < len(s) {
			out += "..."
		}
		return out
	}
	return i, cut(x), cut(y)
}

// Report writes the result: the verdict, the differences, the normalisations that fired.
func (r *Result) Report(w io.Writer, nameA, nameB string) {
	if r.Same() {
		fmt.Fprintf(w, "IDENTICAL after normalisation: %d requests (%s) = %d requests (%s)\n", r.NA, nameA, r.NB, nameB)
	} else {
		fmt.Fprintf(w, "DIFFERENT: %s (%d requests) vs %s (%d requests)\n", nameA, r.NA, nameB, r.NB)
		for i, d := range r.Diffs {
			head := fmt.Sprintf("request %d", d.Request)
			if d.Request == 0 {
				head = "logs"
			}
			fmt.Fprintf(w, "%s%d. %s: %s\n", map[bool]string{true: "first difference, ", false: "also "}[i == 0], i+1, head, d.Where)
			fmt.Fprintf(w, "    A: %s\n    B: %s\n", quote(d.A), quote(d.B))
			if d.Note != "" {
				fmt.Fprintf(w, "    %s\n", d.Note)
			}
		}
	}
	for _, a := range r.Anomalies {
		fmt.Fprintf(w, "ANOMALY %s\n", a)
	}
	fmt.Fprintln(w, "normalisations fired (A / B):")
	for _, rule := range Rules {
		a, b := r.FiredA[rule.ID], r.FiredB[rule.ID]
		if a == 0 && b == 0 {
			fmt.Fprintf(w, "  %-3s never\n", rule.ID)
			continue
		}
		uneven := ""
		if a != b {
			uneven = "   (uneven)"
		}
		fmt.Fprintf(w, "  %-3s %d / %d%s\n", rule.ID, a, b, uneven)
	}
}

func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

var (
	reLongDigits = regexp.MustCompile(`[0-9]{10,}`)
	reBoundaryIn = regexp.MustCompile(`----LangSmithFormBoundary[0-9a-z]+`)
)

// reorderDrains returns (and counts the groups of two or more it reordered) the log with every run of consecutive ingest requests sorted by a key that does not depend on ids,
// times or the boundary, so that two logs whose concurrent batches arrived in another order line up (and N1 numbers the ids
// in the same order in both). Requests with the same key are interchangeable.
func reorderDrains(es []*reqlog.Entry) (out []*reqlog.Entry, drains int) {
	out = make([]*reqlog.Entry, 0, len(es))
	for i := 0; i < len(es); {
		if !ingest(es[i]) {
			out = append(out, es[i])
			i++
			continue
		}
		j := i
		for j < len(es) && ingest(es[j]) {
			j++
		}
		if j-i > 1 {
			drains++ // a group of one has nothing to reorder
		}
		group := append([]*reqlog.Entry(nil), es[i:j]...)
		keys := make(map[*reqlog.Entry]string, len(group))
		for _, e := range group {
			keys[e] = drainKey(e)
		}
		sort.SliceStable(group, func(x, y int) bool { return keys[group[x]] < keys[group[y]] })
		out = append(out, group...)
		i = j
	}
	return out, drains
}

func ingest(e *reqlog.Entry) bool { return e.Route == "multipart" || e.Route == "batch" }

func drainKey(e *reqlog.Entry) string {
	k := string(e.Payload())
	k = reBoundaryIn.ReplaceAllString(k, "<b>")
	k = reUUID.ReplaceAllString(k, "<id>")
	k = reDotted.ReplaceAllString(k, "<t>")
	k = reISO.ReplaceAllString(k, "<t>")
	k = reLongDigits.ReplaceAllString(k, "<n>")
	return e.Route + "\x00" + k
}
