package warden

import (
	"strconv"
	"strings"
)

// The markdown parsing of rules.go as it was before the scanner and prefilter changes: rules_parse_fast_test.go
// holds parseRules, isRuleShaped, taggedLines and ruleLevel to these.

// taggedLines are the lines of a document tagged with whether they sit inside a fenced code block, where `#` is code.
func taggedLinesRef(markdown string) []taggedLine {
	var out []taggedLine
	fence := ""
	for _, text := range reLineBreak.Split(markdown, -1) {
		if m := reFence.FindStringSubmatch(text); m != nil {
			marker := m[1]
			if fence == "" {
				fence = marker
			} else if marker[0] == fence[0] && len(marker) >= len(fence) {
				fence = ""
			}
			out = append(out, taggedLine{text, true})
			continue
		}
		out = append(out, taggedLine{text, fence != ""})
	}
	return out
}

// ruleLevel is the highest heading level present outside code fences, or 7 when there is none.
func ruleLevelRef(lines []taggedLine) int {
	level := 7
	for _, line := range lines {
		if line.fenced {
			continue
		}
		if m := reHeading.FindStringSubmatch(line.text); m != nil && len(m[1]) < level {
			level = len(m[1])
		}
	}
	return level
}

func parseRulesRef(markdown string) []Rule {
	lines := taggedLinesRef(markdown)
	level := ruleLevelRef(lines)
	if level == 7 {
		return nil
	}
	type draft struct {
		name  string
		lines []string
	}
	var drafts []*draft
	for _, line := range lines {
		if !line.fenced {
			if m := reHeading.FindStringSubmatch(line.text); m != nil && len(m[1]) == level {
				drafts = append(drafts, &draft{name: m[2]})
				continue
			}
		}
		if len(drafts) > 0 {
			d := drafts[len(drafts)-1]
			d.lines = append(d.lines, line.text)
		}
	}
	used := map[string]bool{}
	rules := make([]Rule, 0, len(drafts))
	for _, d := range drafts {
		body := append([]string(nil), d.lines...)
		var rule Rule
		var warnings []string
		seenPaths, seenThreshold, seenSeverity, seenSource, seenWhen := false, false, false, false, false
		// Header lines may sit in any order and at most once. A bad value is dropped with a warning; the line never
		// stays in the body.
		for {
			first := -1
			for i, text := range body {
				if jsTrim(text) != "" {
					first = i
					break
				}
			}
			if first < 0 {
				break
			}
			line := body[first]
			remove := func() { body = append(body[:first], body[first+1:]...) }
			if m := rePathsLine.FindStringSubmatch(line); m != nil {
				remove()
				if seenPaths {
					warnings = append(warnings, "paths: appears more than once; the first is kept")
				} else {
					seenPaths = true
					rule.Paths = nil
					for _, glob := range rePathSep.Split(m[1], -1) {
						if glob = cutBackticks(glob); glob != "" {
							rule.Paths = append(rule.Paths, glob)
						}
					}
				}
				continue
			}
			if m := reThresholdLn.FindStringSubmatch(line); m != nil {
				remove()
				raw := jsTrim(cutBackticks(m[1]))
				value := jsToNumber(raw)
				if seenThreshold {
					warnings = append(warnings, "threshold: appears more than once; the first is kept")
				} else if raw == "" || !isFiniteNumber(value) || value < 0 || value > 1 {
					shown := raw
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "threshold: "+shown+" is not a number from 0 to 1; ignored")
				} else {
					v := value
					rule.Threshold = &v
					seenThreshold = true
				}
				continue
			}
			if m := reSeverityLine.FindStringSubmatch(line); m != nil {
				remove()
				raw := strings.ToLower(jsTrim(m[1]))
				if seenSeverity {
					warnings = append(warnings, "severity: appears more than once; the first is kept")
				} else if raw != "high" && raw != "normal" && raw != "low" {
					shown := jsTrim(m[1])
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "severity: "+shown+" is not high, normal, or low; ignored")
				} else {
					rule.Severity = raw
					seenSeverity = true
				}
				continue
			}
			// One small block for the `source: <file>:<line>` citation header, beside the other header lines.
			if m := reSourceRef.FindStringSubmatch(line); m != nil {
				remove()
				raw := jsTrim(cutBackticks(m[1]))
				ref := reRefParts.FindStringSubmatch(raw)
				file := ""
				at := 0.0
				if ref != nil {
					file = jsTrim(ref[1])
					at, _ = strconv.ParseFloat(ref[2], 64)
				}
				if seenSource {
					warnings = append(warnings, "source: appears more than once; the first is kept")
				} else if file == "" || at == 0 {
					shown := raw
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "source: "+shown+" is not a file:line reference; ignored")
				} else {
					rule.SourceRef = &SourceRef{File: file, Line: int(at)}
					seenSource = true
				}
				continue
			}
			if m := reWhenLine.FindStringSubmatch(line); m != nil {
				remove()
				raw := strings.ToLower(jsTrim(m[1]))
				if seenWhen {
					warnings = append(warnings, "when: appears more than once; the first is kept")
				} else if raw != "edit" && raw != "turn" {
					shown := jsTrim(m[1])
					if shown == "" {
						shown = "(empty)"
					}
					warnings = append(warnings, "when: "+shown+" is not edit or turn; ignored")
				} else {
					rule.When = raw
					seenWhen = true
				}
				continue
			}
			break
		}
		rule.ID = slug(d.name, used)
		rule.Name = jsTrim(d.name)
		rule.Body = jsTrim(strings.Join(body, "\n"))
		rule.HeaderWarnings = warnings
		rules = append(rules, rule)
	}
	return rules
}

func isRuleShapedRef(markdown string) bool {
	lines := taggedLinesRef(markdown)
	level := ruleLevelRef(lines)
	if level == 7 {
		return false
	}
	inSection := false
	inBody := 0
	for _, line := range lines {
		if line.fenced {
			continue
		}
		if m := reHeading.FindStringSubmatch(line.text); m != nil && len(m[1]) == level {
			inSection = true
			inBody = 0
			continue
		}
		if inSection {
			inBody++
			if inBody > ruleShapeContextLines {
				inSection = false
				continue
			}
			if reBulletLine.MatchString(line.text) || reModalWords.MatchString(jsTrim(line.text)) {
				return true
			}
		}
	}
	return false
}

// condense cuts a fallback document to limit characters while keeping its shape: every heading stays and each section
// keeps its head, so a rule stated in the last section of a very long AGENTS.md is still seen.
func condenseRef(text string, limit int) string {
	if utf16Len(text) <= limit {
		return text
	}
	type section struct {
		heading    string
		hasHeading bool
		body       []string
	}
	sections := []*section{{}}
	for _, line := range taggedLinesRef(text) {
		if !line.fenced && reHeading.MatchString(line.text) {
			sections = append(sections, &section{heading: line.text, hasHeading: true})
		} else {
			last := sections[len(sections)-1]
			last.body = append(last.body, line.text)
		}
	}
	headings := 0
	for _, s := range sections {
		headings += utf16Len(s.heading) + 4
	}
	perSection := (limit - headings) / len(sections)
	if limit-headings < 0 {
		perSection = 0
	}
	if perSection < 80 {
		perSection = 80
	}
	parts := make([]string, len(sections))
	for i, s := range sections {
		body := jsTrim(strings.Join(s.body, "\n"))
		if utf16Len(body) > perSection {
			body = utf16Slice(body, 0, perSection) + "…"
		}
		head := ""
		if s.hasHeading {
			head = s.heading + "\n"
		}
		parts[i] = head + body
	}
	out := jsTrim(strings.Join(parts, "\n\n"))
	if utf16Len(out) <= limit {
		return out
	}
	return utf16Slice(out, 0, limit) + "… [" + itoaInt(utf16Len(text)-limit) + " more chars in the document]"
}
