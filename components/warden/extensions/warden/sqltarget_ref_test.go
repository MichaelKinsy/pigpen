package warden

import (
	"strings"
	"unicode/utf8"
)

// sqlSegmentsRef is sqlSegments as it was before the allocation-free rewrite (sqltarget.go): the differential tests
// (sqltarget_fast_test.go) hold the rewrite to it.
func sqlSegmentsRef(command string) []*sqlSegment {
	var segments []*sqlSegment
	segment := &sqlSegment{}
	var word *shellWordSQL
	var pending []sqlPendingHeredoc
	endWord := func() {
		if word != nil {
			segment.words = append(segment.words, *word)
		}
		word = nil
	}
	endSegment := func(piped bool) {
		endWord()
		if len(segment.words) > 0 || len(segment.heredocs) > 0 {
			segments = append(segments, segment)
		}
		segment = &sqlSegment{stdin: piped}
	}
	appendText := func(text string, expanded bool) {
		if word == nil {
			word = &shellWordSQL{}
		}
		word.text += text
		word.expanded = word.expanded || expanded
	}
	at := func(i int) byte {
		if i >= 0 && i < len(command) {
			return command[i]
		}
		return 0
	}
	for index := 0; index < len(command); {
		char := command[index]
		switch {
		case char == '\'':
			end := strings.IndexByte(command[index+1:], '\'')
			stop := len(command)
			if end >= 0 {
				stop = index + 1 + end
			}
			appendText(command[index+1:stop], false)
			index = stop + 1
			continue
		case char == '"':
			end := index + 1
			var text strings.Builder
			expanded := false
			for end < len(command) && command[end] != '"' {
				if command[end] == '\\' && end+1 < len(command) && strings.IndexByte("\"\\$`", command[end+1]) >= 0 {
					text.WriteByte(command[end+1])
					end += 2
					continue
				}
				if command[end] == '$' || command[end] == '`' {
					expanded = true
				}
				text.WriteByte(command[end])
				end++
			}
			appendText(text.String(), expanded)
			index = end + 1
			continue
		case char == '\\':
			if at(index+1) != '\n' {
				if index+1 < len(command) {
					r, size := utf8.DecodeRuneInString(command[index+1:])
					appendText(string(r), false)
					index += 1 + size
					continue
				}
				appendText("", false)
			}
			index += 2
			continue
		case char == '#' && word == nil:
			for index < len(command) && command[index] != '\n' {
				index++
			}
			continue
		case char == '\n':
			endSegment(false)
			index++
			for _, heredoc := range pending {
				var body []string
				for index < len(command) {
					lineEnd := strings.IndexByte(command[index:], '\n')
					stop := len(command)
					if lineEnd >= 0 {
						stop = index + lineEnd
					}
					line := command[index:stop]
					index = stop + 1
					cmp := line
					if heredoc.tabs {
						cmp = reLeadingTabs.ReplaceAllString(line, "")
					}
					if cmp == heredoc.delimiter {
						break
					}
					body = append(body, line)
				}
				text := strings.Join(body, "\n")
				heredoc.segment.heredocs = append(heredoc.segment.heredocs, sqlHeredoc{body: text, expanded: !heredoc.literal && strings.ContainsAny(text, "$`\\")})
			}
			pending = nil
			continue
		case char == '<' && strings.HasPrefix(command[index:], "<<") && !strings.HasPrefix(command[index:], "<<<"):
			endWord()
			m := reHeredocStart.get().FindStringSubmatchIndex(command[index:])
			if m == nil {
				segment.stdin = true
				index += 2
				continue
			}
			rest := command[index:]
			group := func(n int) (string, bool) {
				if m[2*n] < 0 {
					return "", false
				}
				return rest[m[2*n]:m[2*n+1]], true
			}
			var delimiter string
			if g, ok := group(2); ok {
				delimiter = g
			} else if g, ok := group(3); ok {
				delimiter = g
			} else {
				delimiter, _ = group(4)
			}
			_, hasUnquoted := group(4)
			dash, _ := group(1)
			pending = append(pending, sqlPendingHeredoc{segment: segment, delimiter: delimiter, tabs: dash == "-", literal: !hasUnquoted || strings.Contains(rest[m[0]:m[1]], "\\")})
			index += m[1]
			continue
		case char == '<':
			endWord()
			segment.stdin = true
			if strings.HasPrefix(command[index:], "<<<") {
				index += 3
			} else {
				index++
			}
			continue
		case char == '&' && (at(index-1) == '>' || at(index+1) == '>'):
			appendText("&", false)
			index++
			continue
		case char == '$' || char == '`':
			appendText(string(char), true)
			index++
			continue
		case char == ';' || char == '&' || char == '|' || char == '(' || char == ')':
			operator := 1
			if strings.HasPrefix(command[index:], "&&") || strings.HasPrefix(command[index:], "||") {
				operator = 2
			}
			endSegment(char == '|' && operator == 1)
			index += operator
			continue
		}
		r, size := utf8.DecodeRuneInString(command[index:])
		if isJSSpace(r) {
			endWord()
		} else {
			appendText(string(r), false)
		}
		index += size
	}
	endSegment(false)
	return segments
}

// readSqlCallRef, sqlEntryRef and applySqlTargetsRef are the callers as they were: they read sqlSegmentsRef.
func readSqlCallRef(segment *sqlSegment) *sqlCall {
	client := sqlClient(segment)
	if client == "" {
		return nil
	}
	words := segment.words
	head := sqlHeadIndex(words)
	var all []string
	for _, w := range words {
		all = append(all, w.text)
	}
	for _, h := range segment.heredocs {
		all = append(all, h.body)
	}
	text := strings.Join(all, " ")
	var hosts, sql []string
	sqlReadable := !segment.stdin
	variable, singleTransaction := false, false
	hosted := ""
	for _, word := range words[:head] {
		m := rePGHostAssign.FindStringSubmatch(word.text)
		if m == nil {
			continue
		}
		if word.expanded || m[1] == "PGSERVICE" {
			variable = true
		} else {
			hosts = append(hosts, m[2])
		}
	}
	for index := head + 1; index < len(words); {
		word := words[index]
		if sqlText := optionValueRef(words, index, sqlTextFlags[client]); sqlText != nil {
			if sqlText.value == nil || sqlText.value.expanded {
				sqlReadable = false
			} else {
				sql = append(sql, sqlText.value.text)
			}
			index = sqlText.next
			continue
		}
		if file := optionValueRef(words, index, sqlFileFlags[client]); file != nil {
			sqlReadable = false
			index = file.next
			continue
		}
		if word.expanded {
			variable = true
			index++
			continue
		}
		host := optionValueRef(words, index, sqlHostFlags[client])
		dbname := optionValueRef(words, index, []string{"-d", "--dbname", "--db-url"})
		var target *optionResultRef
		if client == "supabase" {
			target = optionValueRef(words, index, []string{"--target"})
		}
		option := host
		if option == nil {
			option = dbname
		}
		if option == nil {
			option = target
		}
		switch {
		case option != nil && option.value != nil && option.value.expanded:
			variable = true
		case host != nil:
			if host.value != nil {
				hosts = append(hosts, host.value.text)
			}
		case dbname != nil:
			if dbname.value != nil {
				hosts = append(hosts, connectionHosts(dbname.value.text)...)
			}
		case target != nil:
			if target.value != nil && target.value.text != "local" && hosted == "" {
				hosted = "Supabase target " + target.value.text
			}
		case client == "supabase" && word.text == "--linked":
			if hosted == "" {
				hosted = "the linked Supabase project"
			}
		case word.text == "-1" || word.text == "--single-transaction":
			singleTransaction = true
		case strings.Contains(word.text, "://"):
			hosts = append(hosts, connectionHosts(word.text)...)
		}
		if option != nil {
			index = option.next
		} else {
			index++
		}
	}
	for _, h := range segment.heredocs {
		if h.expanded {
			sqlReadable = false
		} else {
			sql = append(sql, h.body)
		}
	}
	call := &sqlCall{client: client, singleTransaction: singleTransaction, text: text}
	if sqlReadable && len(sql) > 0 {
		call.sql, call.hasSQL = strings.Join(sql, ";\n"), true
	}
	if variable {
		call.target = sqlUnknown
		return call
	}
	named := make([]string, len(hosts))
	for i, h := range hosts {
		named[i] = strings.ToLower(h)
	}
	hostedName := hosted
	if hostedName == "" {
		for _, h := range named {
			for _, suffix := range hostedSuffixes {
				if strings.HasSuffix(h, suffix) {
					hostedName = h
					break
				}
			}
			if hostedName != "" {
				break
			}
		}
	}
	if hostedName != "" {
		call.target, call.host = sqlHosted, hostedName
		return call
	}
	if len(named) > 0 {
		all := true
		for _, h := range named {
			all = all && (loopbackHosts[h] || strings.HasPrefix(h, "/"))
		}
		if all {
			call.target, call.host = sqlLoopback, named[0]
			return call
		}
	}
	call.target = sqlUnknown
	return call
}

type sqlEntryRef struct {
	segment *sqlSegment
	call    *sqlCall
}

func (e sqlEntryRef) text() string {
	if e.call != nil {
		return e.call.text
	}
	var all []string
	for _, w := range e.segment.words {
		all = append(all, w.text)
	}
	for _, h := range e.segment.heredocs {
		all = append(all, h.body)
	}
	return strings.Join(all, " ")
}

func (e sqlEntryRef) readOnlyHosted() bool {
	return e.call != nil && e.call.target == sqlHosted && readOnlyWrapped(e.call)
}

// applySqlTargets replaces or drops the SQL pattern hits and adds the target hits, from the raw command (heredoc bodies included).
func applySqlTargetsRef(raw string, hits *hitSet, exempt map[string]bool) {
	var entries []sqlEntryRef
	for _, s := range sqlSegmentsRef(raw) {
		entries = append(entries, sqlEntryRef{s, readSqlCallRef(s)})
	}
	// A heredoc body fed to a SQL client is SQL, not data: the SQL rules read it as they read a `-c` value.
	for _, rule := range shellRules {
		if !sqlRuleIDs[rule.ID] || exempt[rule.ID] || hits.has(rule.ID) {
			continue
		}
		for _, e := range entries {
			if e.call != nil && rule.Test(e.call.text) {
				hits.set(PatternHit{ID: rule.ID, Severity: rule.Severity, Label: rule.Label})
				break
			}
		}
	}
	for _, rule := range shellRules {
		if !sqlRuleIDs[rule.ID] || !hits.has(rule.ID) {
			continue
		}
		matching, allReadOnly := 0, true
		for _, e := range entries {
			if rule.Test(e.text()) {
				matching++
				allReadOnly = allReadOnly && e.readOnlyHosted()
			}
		}
		if matching > 0 && allReadOnly {
			hits.del(rule.ID)
		}
	}
	if hits.has("sql-delete") {
		matching, allLocal := 0, true
		for _, e := range entries {
			if reDeleteFromW.MatchString(e.text()) {
				matching++
				allLocal = allLocal && e.call != nil && e.call.target == sqlLoopback && e.call.hasSQL && scopedDeletesOnly(e.call.sql)
			}
		}
		if matching > 0 && allLocal {
			hits.del("sql-delete")
			if !exempt["sql-delete-local"] {
				hits.set(PatternHit{ID: "sql-delete-local", Severity: SeverityRisky, Label: "SQL DELETE FROM … WHERE on a loopback database"})
			}
		}
	}
	for _, e := range entries {
		call := e.call
		if call == nil || call.target != sqlHosted || e.readOnlyHosted() {
			continue
		}
		if !exempt["sql-hosted"] && !hits.has("sql-hosted") {
			hits.set(PatternHit{ID: "sql-hosted", Severity: SeverityRisky, Label: "SQL client against a hosted database (" + call.host + ")"})
		}
		if call.hasSQL && reSQLWrite.MatchString(call.sql) && !exempt["sql-hosted-write"] && !hits.has("sql-hosted-write") {
			hits.set(PatternHit{ID: "sql-hosted-write", Severity: SeverityDestructive, Label: "SQL write against a hosted database (" + call.host + ")"})
		}
	}
}

var reLeadingTabs = lazyRE(`^\t+`)

type optionResultRef struct {
	value *shellWordSQL
	next  int
}

// optionValue is the value of an option at index: `-c X`, `-cX`, `--command X`, or `--command=X`. nil when the word is not the option.
func optionValueRef(words []shellWordSQL, index int, flags []string) *optionResultRef {
	text := words[index].text
	for _, flag := range flags {
		if text == flag {
			var v *shellWordSQL
			if index+1 < len(words) {
				v = &words[index+1]
			}
			return &optionResultRef{v, index + 2}
		}
		if strings.HasPrefix(flag, "--") && strings.HasPrefix(text, flag+"=") {
			return &optionResultRef{&shellWordSQL{text[len(flag)+1:], words[index].expanded}, index + 1}
		}
		if !strings.HasPrefix(flag, "--") && strings.HasPrefix(text, flag) && len(text) > len(flag) {
			return &optionResultRef{&shellWordSQL{text[len(flag):], words[index].expanded}, index + 1}
		}
	}
	return nil
}
