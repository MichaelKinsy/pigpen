package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// The settings editor changes bytes, not values. It finds the byte span of one
// element of the top-level "packages" array and deletes exactly that span (and
// the comma that belongs to it), so formatting, key order, comments-free
// whitespace, line endings and every other key stay byte-identical.

type span struct{ start, end int }

type scanner struct {
	b []byte
	i int
}

func (s *scanner) ws() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return
		}
	}
}

func (s *scanner) fail(msg string) error { return fmt.Errorf("invalid JSON at byte %d: %s", s.i, msg) }

// value scans one JSON value and returns its span.
func (s *scanner) value() (span, error) {
	s.ws()
	start := s.i
	if s.i >= len(s.b) {
		return span{}, s.fail("unexpected end")
	}
	switch c := s.b[s.i]; {
	case c == '"':
		if err := s.str(); err != nil {
			return span{}, err
		}
	case c == '{':
		s.i++
		s.ws()
		if s.peek('}') {
			s.i++
			break
		}
		for {
			s.ws()
			if err := s.str(); err != nil {
				return span{}, err
			}
			s.ws()
			if !s.peek(':') {
				return span{}, s.fail("expected ':'")
			}
			s.i++
			if _, err := s.value(); err != nil {
				return span{}, err
			}
			s.ws()
			if s.peek(',') {
				s.i++
				continue
			}
			if s.peek('}') {
				s.i++
				break
			}
			return span{}, s.fail("expected ',' or '}'")
		}
	case c == '[':
		s.i++
		s.ws()
		if s.peek(']') {
			s.i++
			break
		}
		for {
			if _, err := s.value(); err != nil {
				return span{}, err
			}
			s.ws()
			if s.peek(',') {
				s.i++
				continue
			}
			if s.peek(']') {
				s.i++
				break
			}
			return span{}, s.fail("expected ',' or ']'")
		}
	default:
		for s.i < len(s.b) && !bytes.ContainsRune([]byte(" \t\r\n,]}"), rune(s.b[s.i])) {
			s.i++
		}
		if s.i == start {
			return span{}, s.fail("unexpected character")
		}
	}
	return span{start, s.i}, nil
}

func (s *scanner) peek(c byte) bool { return s.i < len(s.b) && s.b[s.i] == c }

func (s *scanner) str() error {
	if !s.peek('"') {
		return s.fail("expected string")
	}
	s.i++
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case '\\':
			s.i += 2
			continue
		case '"':
			s.i++
			return nil
		}
		s.i++
	}
	return s.fail("unterminated string")
}

// topLevelValue returns the span of the value of a top-level key.
func topLevelValue(raw []byte, key string) (span, error) {
	s := &scanner{b: raw}
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		s.i = 3
	}
	s.ws()
	if !s.peek('{') {
		return span{}, errors.New("settings.json is not a JSON object")
	}
	s.i++
	s.ws()
	if s.peek('}') {
		return span{}, fmt.Errorf("no %q key", key)
	}
	for {
		s.ws()
		ks := s.i
		if err := s.str(); err != nil {
			return span{}, err
		}
		var name string
		if err := json.Unmarshal(raw[ks:s.i], &name); err != nil {
			return span{}, err
		}
		s.ws()
		if !s.peek(':') {
			return span{}, s.fail("expected ':'")
		}
		s.i++
		v, err := s.value()
		if err != nil {
			return span{}, err
		}
		if name == key {
			return v, nil
		}
		s.ws()
		if s.peek(',') {
			s.i++
			continue
		}
		return span{}, fmt.Errorf("no %q key", key)
	}
}

type element struct{ ps, vs, ve int }

func arrayElements(raw []byte, arr span) ([]element, error) {
	s := &scanner{b: raw, i: arr.start}
	if !s.peek('[') {
		return nil, errors.New("not an array")
	}
	s.i++
	var out []element
	s.ws()
	if s.peek(']') {
		return nil, nil
	}
	ps := arr.start + 1
	for {
		v, err := s.value()
		if err != nil {
			return nil, err
		}
		out = append(out, element{ps: ps, vs: v.start, ve: v.end})
		s.ws()
		if s.peek(',') {
			s.i++
			ps = s.i
			continue
		}
		return out, nil
	}
}

// removeArrayElements deletes elements (by index) from the top-level array key.
// The result is verified to be valid JSON with every other byte untouched.
func removeArrayElements(raw []byte, key string, idx []int) ([]byte, error) {
	sorted := append([]int(nil), idx...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	out := raw
	for n, k := range sorted {
		if n > 0 && sorted[n-1] == k {
			return nil, fmt.Errorf("index %d listed twice", k)
		}
		v, err := topLevelValue(out, key)
		if err != nil {
			return nil, err
		}
		if out[v.start] != '[' {
			return nil, fmt.Errorf("%q is not an array", key)
		}
		els, err := arrayElements(out, v)
		if err != nil {
			return nil, err
		}
		if k < 0 || k >= len(els) {
			return nil, fmt.Errorf("%s[%d] does not exist (%d entries)", key, k, len(els))
		}
		e := els[k]
		var del span
		switch {
		case len(els) == 1:
			del = span{e.ps, e.ve}
		case k < len(els)-1:
			del = span{e.ps, els[k+1].ps}
		default:
			del = span{els[k-1].ve, e.ve}
		}
		next := make([]byte, 0, len(out)-(del.end-del.start))
		next = append(next, out[:del.start]...)
		next = append(next, out[del.end:]...)
		out = next
	}
	if !json.Valid(bytes.TrimPrefix(out, []byte("\xef\xbb\xbf"))) {
		return nil, errors.New("edit produced invalid JSON; refusing")
	}
	return out, nil
}

// PackageEntry is one element of settings.json "packages".
type PackageEntry struct {
	Index      int      `json:"index"`
	Source     string   `json:"source"`
	Filtered   bool     `json:"filtered"`
	Extensions []string `json:"extensions,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Prompts    []string `json:"prompts,omitempty"`
	Themes     []string `json:"themes,omitempty"`
}

func (p PackageEntry) filter(kind string) []string {
	switch kind {
	case "extensions":
		return p.Extensions
	case "skills":
		return p.Skills
	case "prompts":
		return p.Prompts
	}
	return p.Themes
}

// parsePackages reads the packages array: each entry is a string or an object with resource filters.
func parsePackages(raw []byte) ([]PackageEntry, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &top); err != nil {
		return nil, err
	}
	pk, ok := top["packages"]
	if !ok {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(pk, &items); err != nil {
		return nil, fmt.Errorf("packages: %w", err)
	}
	out := make([]PackageEntry, 0, len(items))
	for i, it := range items {
		e := PackageEntry{Index: i}
		var s string
		if err := json.Unmarshal(it, &s); err == nil {
			e.Source = s
			out = append(out, e)
			continue
		}
		var obj struct {
			Source     string   `json:"source"`
			Extensions []string `json:"extensions"`
			Skills     []string `json:"skills"`
			Prompts    []string `json:"prompts"`
			Themes     []string `json:"themes"`
		}
		if err := json.Unmarshal(it, &obj); err != nil || obj.Source == "" {
			return nil, fmt.Errorf("packages[%d] is neither a source string nor an object with a source", i)
		}
		e.Source, e.Filtered = obj.Source, true
		e.Extensions, e.Skills, e.Prompts, e.Themes = obj.Extensions, obj.Skills, obj.Prompts, obj.Themes
		out = append(out, e)
	}
	return out, nil
}

// applyPatterns returns the members a Package filter enables, exactly as PiG's
// packagecontent.ResourceEnabled does: with no filter every member; with an
// allowlist (plain patterns) only matches; "-x" excludes; "+x" and "!x" both
// include (PiG does not treat "!" as an exclusion); patterns apply in order.
func applyPatterns(members, patterns []string) []string {
	if patterns == nil {
		return append([]string(nil), members...)
	}
	if len(patterns) == 0 {
		return nil
	}
	allow := false
	for _, p := range patterns {
		if p != "" && p[0] != '-' && p[0] != '+' && p[0] != '!' {
			allow = true
		}
	}
	var out []string
	for _, m := range members {
		enabled := !allow
		for _, p := range patterns {
			if p == "" {
				continue
			}
			negated := false
			switch p[0] {
			case '-':
				negated = true
				p = p[1:]
			case '+', '!':
				p = p[1:]
			}
			p = filepath.ToSlash(p)
			if ok, _ := filepath.Match(p, m); ok || p == m {
				enabled = !negated
			}
		}
		if enabled {
			out = append(out, m)
		}
	}
	return out
}

// noopFilter reports whether a filter only has "+" patterns: PiG treats "+x" as a
// force-include, so such a filter enables every member instead of narrowing to x.
func noopFilter(patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, p := range patterns {
		if !strings.HasPrefix(p, "+") {
			return false
		}
	}
	return true
}

func hasBang(patterns []string) bool {
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			return true
		}
	}
	return false
}
