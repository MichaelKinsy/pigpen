package svc

import "strings"

// matchesGlob reports whether a slash-separated relative path matches a glob pattern with the
// semantics of Node's path.matchesGlob (minimatch): `*` and `?` stay inside one segment, `**`
// spans whole segments, `[...]` is a character class, `{a,b}` alternates, and wildcards never
// match a segment starting with a dot unless the pattern names the dot itself. A trailing `**`
// needs at least one segment (so `a/**` does not match `a`).
func matchesGlob(path, pattern string) bool {
	segments := strings.Split(path, "/")
	for _, alt := range expandBraces(pattern) {
		if matchSegments(strings.Split(alt, "/"), segments) {
			return true
		}
	}
	return false
}

func expandBraces(pattern string) []string {
	open := -1
	depth := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 {
				open = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 {
				body := pattern[open+1 : i]
				var parts []string
				level, start := 0, 0
				for j := 0; j < len(body); j++ {
					switch body[j] {
					case '\\':
						j++
					case '{':
						level++
					case '}':
						level--
					case ',':
						if level == 0 {
							parts = append(parts, body[start:j])
							start = j + 1
						}
					}
				}
				parts = append(parts, body[start:])
				if len(parts) == 1 {
					// {a} is literal in minimatch, like a brace without a comma.
					return []string{pattern}
				}
				var out []string
				for _, p := range parts {
					out = append(out, expandBraces(pattern[:open]+p+pattern[i+1:])...)
				}
				return out
			}
		}
	}
	return []string{pattern}
}

func matchSegments(pattern, path []string) bool {
	if len(pattern) == 0 {
		return len(path) == 0
	}
	if pattern[0] == "**" {
		if len(pattern) == 1 {
			if len(path) == 0 {
				return false
			}
			for _, s := range path {
				if strings.HasPrefix(s, ".") {
					return false
				}
			}
			return true
		}
		for skip := 0; skip <= len(path); skip++ {
			if matchSegments(pattern[1:], path[skip:]) {
				return true
			}
			if skip < len(path) && strings.HasPrefix(path[skip], ".") {
				break // ** never swallows a dot segment
			}
		}
		return false
	}
	if len(path) == 0 {
		return false
	}
	if strings.HasPrefix(path[0], ".") && !strings.HasPrefix(pattern[0], ".") {
		return false
	}
	return matchSegment(pattern[0], path[0]) && matchSegments(pattern[1:], path[1:])
}

func matchSegment(pattern, name string) bool {
	return matchRunes([]rune(pattern), []rune(name))
}

func matchRunes(p, s []rune) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 0 && p[0] == '*' {
				p = p[1:]
			}
			if len(p) == 0 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if matchRunes(p, s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			p, s = p[1:], s[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			ok, rest, valid := matchClass(p, s[0])
			if !valid {
				// An unterminated class is a literal '['.
				if s[0] != '[' {
					return false
				}
				p, s = p[1:], s[1:]
				continue
			}
			if !ok {
				return false
			}
			p, s = rest, s[1:]
		case '\\':
			if len(p) > 1 {
				p = p[1:]
			}
			fallthrough
		default:
			if len(s) == 0 || s[0] != p[0] {
				return false
			}
			p, s = p[1:], s[1:]
		}
	}
	return len(s) == 0
}

// matchClass matches r against the class at the start of p; valid is false when it never closes.
func matchClass(p []rune, r rune) (ok bool, rest []rune, valid bool) {
	i := 1
	negate := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		negate = true
		i++
	}
	first := true
	matched := false
	for ; i < len(p); i++ {
		if p[i] == ']' && !first {
			return matched != negate, p[i+1:], true
		}
		first = false
		lo := p[i]
		if p[i] == '\\' && i+1 < len(p) {
			i++
			lo = p[i]
		}
		if i+2 < len(p) && p[i+1] == '-' && p[i+2] != ']' {
			hi := p[i+2]
			if lo <= r && r <= hi {
				matched = true
			}
			i += 2
			continue
		}
		if r == lo {
			matched = true
		}
	}
	return false, nil, false
}
