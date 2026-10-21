package warden

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// SQL targets (src/guard.ts): which database a SQL client command reaches. A scoped DELETE on a loopback database warns;
// a command against a hosted database is elevated unless its SQL runs in a read-only transaction that is rolled back. A
// target or SQL that cannot be read with confidence (a variable host, SQL from a file or a pipe) keeps the plain pattern
// hits.

type sqlTargetKind string

const (
	sqlLoopback sqlTargetKind = "loopback"
	sqlHosted   sqlTargetKind = "hosted"
	sqlUnknown  sqlTargetKind = "unknown"
)

var (
	sqlClientNames = []string{"psql", "mysql", "mariadb", "supabase"}
	sqlClients     = setOf(sqlClientNames...)
	// postgresClients talk to Postgres, which rejects writes inside a read-only transaction.
	postgresClients = setOf("psql", "supabase")
	loopbackHosts   = setOf("127.0.0.1", "localhost", "::1")
	hostedSuffixes  = []string{".supabase.com", ".supabase.co", ".neon.tech", ".rds.amazonaws.com", ".planetscale.com"}

	reConnectionURL = lazyRE(`(?i)^(?:postgres|postgresql|mysql)://(?:[^@/?#]*@)?(\[[^\]]*\]|[^:/?#]*)[^?#]*(?:\?([^#]*))?`)
	reSQLWrite      = lazyRE(`(?i)\b(?:insert|update|delete|alter|create|drop|truncate|grant)\b`)
	reSQLTxControl  = lazyRE(`(?i)^(?:begin|start|commit|end|rollback|abort|savepoint|release|prepare|set\s+(?:session\s+characteristics|transaction))\b`)
	reHeredocStart  = lazyRE(`^<<(-?)\s*(?:"([^"\n]*)"|'([^'\n]*)'|\\?([^\s;&|<>()]+))`)
	reHostKeyword   = lazyRE(`(?:^|\s)host(?:addr)?=(\S+)`)
	rePGHostAssign  = lazyRE(`(?s)^(PGHOST|PGHOSTADDR|PGSERVICE)=(.*)$`)
	reBeginRO       = lazyRE(`(?is)^(?:begin|start\s+transaction)\b.*\bread\s+only\b`)
	reBeginAny      = lazyRE(`(?i)^(?:begin|start\s+transaction)\b`)
	reSetTxRO       = lazyRE(`(?is)^set\s+transaction\b.*\bread\s+only\b`)
	reRollbackOnly  = lazyRE(`(?i)^rollback$`)
	reSQLVoid       = lazyRE("(?i)--|/\\*|\\\\|\\bcommit\\b|\\bread\\s+write\\b|transaction_read_only")
	reSQLComment    = lazyRE(`--|/\*|\\`)
	reDropTruncate  = lazyRE(`(?i)\b(?:drop|truncate)\b`)
	reDeleteFrom    = lazyRE(`(?i)\bdelete\s+from\b`)
	reDeleteFromW   = lazyRE(`(?i)\bdelete\s+from\s+\w`)
	reWhere         = lazyRE(`(?is)\bwhere\b(.*)$`)
	reParenWrapped  = lazyRE(`(?s)^\((.*)\)$`)
	reAlwaysTrue    = lazyRE(`(?i)^(?:true|not\s+false)$`)
	reDigitsEq      = lazyRE(`^(\d+)\s*=\s*(\d+)$`)
	reQuotedEq      = lazyRE(`^'([^']*)'\s*=\s*'([^']*)'$`)
)

type shellWordSQL struct {
	text     string
	expanded bool
}

type sqlHeredoc struct {
	body     string
	expanded bool
}

type sqlSegment struct {
	words    []shellWordSQL
	heredocs []sqlHeredoc
	// stdin: stdin comes from a pipe, a file, or a here-string, so its content is not read.
	stdin bool
}

type sqlPendingHeredoc struct {
	segment   *sqlSegment
	delimiter string
	tabs      bool
	literal   bool
}

// sqlScan is the state of sqlSegments: the segments so far, the open word, and the heredocs waiting for their bodies.
// An open word is a slice of the command while its text is contiguous there (the common case: no allocation), and a
// byte buffer once quotes, escapes or an invalid byte make it differ from the command's bytes.
type sqlScan struct {
	command  string
	segments []*sqlSegment
	segment  *sqlSegment
	// words are the finished words of the open segment, copied to the segment when it ends.
	words []shellWordSQL
	// The open word: open says there is one (it may be empty, as `''` is), buffered says its text is buf, otherwise
	// it is command[lo:hi].
	open, buffered, expanded bool
	lo, hi                   int
	buf                      []byte
	pending                  []sqlPendingHeredoc
}

func (s *sqlScan) endWord() {
	if !s.open {
		return
	}
	text := ""
	if s.buffered {
		text = string(s.buf)
	} else {
		text = s.command[s.lo:s.hi]
	}
	s.words = append(s.words, shellWordSQL{text: text, expanded: s.expanded})
	s.open, s.buffered, s.expanded = false, false, false
	s.buf = s.buf[:0]
}

func (s *sqlScan) endSegment(piped bool) {
	s.endWord()
	if len(s.words) > 0 {
		s.segment.words = append([]shellWordSQL(nil), s.words...)
		s.words = s.words[:0]
	}
	if len(s.segment.words) > 0 || len(s.segment.heredocs) > 0 {
		s.segments = append(s.segments, s.segment)
	}
	s.segment = &sqlSegment{stdin: piped}
}

// appendSlice appends command[a:b] to the open word.
func (s *sqlScan) appendSlice(a, b int, expanded bool) {
	s.expanded = s.expanded || expanded
	switch {
	case !s.open:
		s.open, s.lo, s.hi = true, a, b
	case s.buffered:
		s.buf = append(s.buf, s.command[a:b]...)
	case s.hi == a:
		s.hi = b
	case a == b:
	default:
		s.buf = append(append(s.buf[:0], s.command[s.lo:s.hi]...), s.command[a:b]...)
		s.buffered = true
	}
}

// appendText appends text that is not a slice of the command to the open word.
func (s *sqlScan) appendText(text string) {
	if !s.open {
		s.open, s.buffered, s.buf = true, true, append(s.buf[:0], text...)
		return
	}
	if !s.buffered {
		s.buf = append(s.buf[:0], s.command[s.lo:s.hi]...)
		s.buffered = true
	}
	s.buf = append(s.buf, text...)
}

// appendEmpty opens a word of no text (a backslash at the end of the command).
func (s *sqlScan) appendEmpty() {
	if !s.open {
		s.open, s.lo, s.hi = true, 0, 0
	}
}

// appendRune appends the rune at command[a:a+size], decoded as r: an invalid byte becomes U+FFFD, as string(r) gives.
func (s *sqlScan) appendRune(a, size int, r rune) {
	if r == utf8.RuneError && size == 1 {
		s.appendText("\uFFFD")
		return
	}
	s.appendSlice(a, a+size, false)
}

// sqlSegments are shell segments with quotes resolved and heredoc bodies attached to the segment that reads them. Unlike
// splitShell, a `;` inside quotes does not split, so `psql -c "BEGIN READ ONLY; SELECT 1; ROLLBACK"` stays one segment.
func sqlSegments(command string) []*sqlSegment {
	s := sqlScan{command: command, segment: &sqlSegment{}}
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
			s.appendSlice(index+1, stop, false)
			index = stop + 1
			continue
		case char == '"':
			end := index + 1
			expanded, escaped := false, false
			for end < len(command) && command[end] != '"' {
				if command[end] == '\\' && end+1 < len(command) && strings.IndexByte("\"\\$`", command[end+1]) >= 0 {
					escaped = true
					end += 2
					continue
				}
				if command[end] == '$' || command[end] == '`' {
					expanded = true
				}
				end++
			}
			if !escaped {
				s.appendSlice(index+1, end, expanded)
			} else {
				s.appendText("")
				s.expanded = s.expanded || expanded
				for k := index + 1; k < end; {
					if command[k] == '\\' && k+1 < len(command) && strings.IndexByte("\"\\$`", command[k+1]) >= 0 {
						s.buf = append(s.buf, command[k+1])
						k += 2
						continue
					}
					s.buf = append(s.buf, command[k])
					k++
				}
			}
			index = end + 1
			continue
		case char == '\\':
			if at(index+1) != '\n' {
				if index+1 < len(command) {
					r, size := utf8.DecodeRuneInString(command[index+1:])
					s.appendRune(index+1, size, r)
					index += 1 + size
					continue
				}
				s.appendEmpty()
			}
			index += 2
			continue
		case char == '#' && !s.open:
			for index < len(command) && command[index] != '\n' {
				index++
			}
			continue
		case char == '\n':
			s.endSegment(false)
			index++
			for _, heredoc := range s.pending {
				bodyStart, bodyEnd, any := 0, 0, false
				for index < len(command) {
					lineEnd := strings.IndexByte(command[index:], '\n')
					stop := len(command)
					if lineEnd >= 0 {
						stop = index + lineEnd
					}
					start := index
					cmp := command[start:stop]
					index = stop + 1
					if heredoc.tabs {
						cmp = strings.TrimLeft(cmp, "\t")
					}
					if cmp == heredoc.delimiter {
						break
					}
					if !any {
						bodyStart, any = start, true
					}
					bodyEnd = stop
				}
				text := ""
				if any {
					text = command[bodyStart:bodyEnd]
				}
				heredoc.segment.heredocs = append(heredoc.segment.heredocs, sqlHeredoc{body: text, expanded: !heredoc.literal && strings.ContainsAny(text, "$`\\")})
			}
			s.pending = nil
			continue
		case char == '<' && strings.HasPrefix(command[index:], "<<") && !strings.HasPrefix(command[index:], "<<<"):
			s.endWord()
			m := reHeredocStart.get().FindStringSubmatchIndex(command[index:])
			if m == nil {
				s.segment.stdin = true
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
			s.pending = append(s.pending, sqlPendingHeredoc{segment: s.segment, delimiter: delimiter, tabs: dash == "-", literal: !hasUnquoted || strings.Contains(rest[m[0]:m[1]], "\\")})
			index += m[1]
			continue
		case char == '<':
			s.endWord()
			s.segment.stdin = true
			if strings.HasPrefix(command[index:], "<<<") {
				index += 3
			} else {
				index++
			}
			continue
		case char == '&' && (at(index-1) == '>' || at(index+1) == '>'):
			s.appendSlice(index, index+1, false)
			index++
			continue
		case char == '$' || char == '`':
			s.appendSlice(index, index+1, true)
			index++
			continue
		case char == ';' || char == '&' || char == '|' || char == '(' || char == ')':
			operator := 1
			if strings.HasPrefix(command[index:], "&&") || strings.HasPrefix(command[index:], "||") {
				operator = 2
			}
			s.endSegment(char == '|' && operator == 1)
			index += operator
			continue
		}
		r, size := rune(char), 1
		if char >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(command[index:])
		}
		if isJSSpace(r) {
			s.endWord()
		} else {
			s.appendRune(index, size, r)
		}
		index += size
	}
	s.endSegment(false)
	return s.segments
}

// sqlHeadIndex is the index of the command word: leading assignments and wrappers are skipped, as headOf does.
func sqlHeadIndex(words []shellWordSQL) int {
	i := 0
	for i < len(words) && (isAssignment(words[i].text) || wrappers[words[i].text]) {
		i++
	}
	return i
}

func sqlClient(segment *sqlSegment) string {
	i := sqlHeadIndex(segment.words)
	if i >= len(segment.words) {
		return ""
	}
	head := stripPathPrefix(segment.words[i].text)
	if sqlClients[head] {
		return head
	}
	return ""
}

// Option spellings that carry SQL text, per client; `-c` is SQL for psql but `--comments` for mysql.
var (
	sqlTextFlags = map[string][]string{"psql": {"-c", "--command"}, "mysql": {"-e", "--execute"}, "mariadb": {"-e", "--execute"}, "supabase": {}}
	sqlFileFlags = map[string][]string{"psql": {"-f", "--file"}, "mysql": {}, "mariadb": {}, "supabase": {}}
	// Option spellings that name the server: psql's `-S` is single-line mode, mysql's is the socket path.
	sqlHostFlags = map[string][]string{"psql": {"-h", "--host"}, "mysql": {"-h", "--host", "-S", "--socket"}, "mariadb": {"-h", "--host", "-S", "--socket"}, "supabase": {}}
	dbnameFlags  = []string{"-d", "--dbname", "--db-url"}
	targetFlags  = []string{"--target"}
)

type sqlCall struct {
	client            string
	target            sqlTargetKind
	host              string
	sql               string
	hasSQL            bool
	singleTransaction bool
	text              string
}

// optionResult is an option found at a word: its value (absent for `-c` as the last word) and the index after it.
type optionResult struct {
	found    bool
	hasValue bool
	value    shellWordSQL
	next     int
}

// optionValue is the value of an option at index: `-c X`, `-cX`, `--command X`, or `--command=X`. found is false when
// the word is not the option.
func optionValue(words []shellWordSQL, index int, flags []string) optionResult {
	text := words[index].text
	for _, flag := range flags {
		if text == flag {
			if index+1 < len(words) {
				return optionResult{found: true, hasValue: true, value: words[index+1], next: index + 2}
			}
			return optionResult{found: true, next: index + 2}
		}
		if strings.HasPrefix(flag, "--") && len(text) > len(flag) && text[len(flag)] == '=' && strings.HasPrefix(text, flag) {
			return optionResult{found: true, hasValue: true, value: shellWordSQL{text[len(flag)+1:], words[index].expanded}, next: index + 1}
		}
		if !strings.HasPrefix(flag, "--") && strings.HasPrefix(text, flag) && len(text) > len(flag) {
			return optionResult{found: true, hasValue: true, value: shellWordSQL{text[len(flag):], words[index].expanded}, next: index + 1}
		}
	}
	return optionResult{}
}

// queryParam is URLSearchParams(query).get(name): the first value, percent-decoded, `+` read as a space.
func queryParam(query, name string) (string, bool) {
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		if decodeQuery(key) == name {
			return decodeQuery(value), true
		}
	}
	return "", false
}

func decodeQuery(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if d, err := url.PathUnescape(s); err == nil {
		return d
	}
	return s
}

// connectionHosts are hosts named by a connection string or a `host=` keyword; `postgres:///db` without a host names none.
func connectionHosts(value string) []string {
	if m := reConnectionURL.FindStringSubmatch(value); m != nil {
		host := strings.TrimSuffix(strings.TrimPrefix(m[1], "["), "]")
		if q, _ := queryParam(m[2], "host"); q != "" {
			return []string{q}
		}
		return []string{host}
	}
	var hosts []string
	for _, m := range reHostKeyword.FindAllStringSubmatch(value, -1) {
		hosts = append(hosts, m[1])
	}
	return hosts
}

// readSqlCall is how a SQL client segment reads: its target, the host that decided it, and its SQL text when every piece is literal.
func readSqlCall(segment *sqlSegment) *sqlCall {
	client := sqlClient(segment)
	if client == "" {
		return nil
	}
	words := segment.words
	head := sqlHeadIndex(words)
	text := segmentText(segment)
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
	textFlags, fileFlags, hostFlags := sqlTextFlags[client], sqlFileFlags[client], sqlHostFlags[client]
	for index := head + 1; index < len(words); {
		word := words[index]
		if sqlText := optionValue(words, index, textFlags); sqlText.found {
			if !sqlText.hasValue || sqlText.value.expanded {
				sqlReadable = false
			} else {
				sql = append(sql, sqlText.value.text)
			}
			index = sqlText.next
			continue
		}
		if file := optionValue(words, index, fileFlags); file.found {
			sqlReadable = false
			index = file.next
			continue
		}
		if word.expanded {
			variable = true
			index++
			continue
		}
		host := optionValue(words, index, hostFlags)
		dbname := optionValue(words, index, dbnameFlags)
		var target optionResult
		if client == "supabase" {
			target = optionValue(words, index, targetFlags)
		}
		option := host
		if !option.found {
			option = dbname
		}
		if !option.found {
			option = target
		}
		switch {
		case option.found && option.hasValue && option.value.expanded:
			variable = true
		case host.found:
			if host.hasValue {
				hosts = append(hosts, host.value.text)
			}
		case dbname.found:
			if dbname.hasValue {
				hosts = append(hosts, connectionHosts(dbname.value.text)...)
			}
		case target.found:
			if target.hasValue && target.value.text != "local" && hosted == "" {
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
		if option.found {
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

// sqlTarget is the database a SQL client command reaches, read from the first segment of command: `-h`/`--host`,
// `PGHOST=`, and a `postgres://`, `postgresql://`, or `mysql://` connection string, in the arguments or a
// `-d`/`--dbname` value. `127.0.0.1`, `localhost`, `::1`, and a Unix socket path are loopback; a managed-database
// domain or a supabase `--linked` or non-local `--target` is hosted. A variable anywhere in the arguments, no host, or
// any other host is unknown.
func sqlTarget(command string) sqlTargetKind {
	segments := sqlSegments(command)
	if len(segments) == 0 {
		return sqlUnknown
	}
	if call := readSqlCall(segments[0]); call != nil {
		return call.target
	}
	return sqlUnknown
}

func sqlStatements(sql string) []string {
	var out []string
	for _, s := range strings.Split(sql, ";") {
		if t := jsTrim(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// readOnlyWrapped: SQL wrapped as `BEGIN READ ONLY; … ROLLBACK`, `START TRANSACTION READ ONLY; … ROLLBACK`, or `BEGIN; SET
// TRANSACTION READ ONLY; … ROLLBACK`, with no transaction control in between. A `COMMIT` anywhere, `READ WRITE`, a
// comment, a psql meta-command, or `--single-transaction` (which makes the inner `BEGIN` a no-op) voids it.
func readOnlyWrapped(call *sqlCall) bool {
	if !postgresClients[call.client] || !call.hasSQL || call.singleTransaction {
		return false
	}
	if reSQLVoid.MatchString(call.sql) {
		return false
	}
	statements := sqlStatements(call.sql)
	nth := func(i int) string {
		if i < len(statements) {
			return statements[i]
		}
		return ""
	}
	var start int
	switch {
	case reBeginRO.MatchString(nth(0)):
		start = 1
	case reBeginAny.MatchString(nth(0)) && reSetTxRO.MatchString(nth(1)):
		start = 2
	default:
		return false
	}
	if len(statements) <= start || !reRollbackOnly.MatchString(statements[len(statements)-1]) {
		return false
	}
	for _, s := range statements[start : len(statements)-1] {
		if reSQLTxControl.MatchString(s) {
			return false
		}
	}
	return true
}

// alwaysTrue: a WHERE clause that matches every row: `true`, `NOT false`, or a literal equal to itself (`1=1`, `'a'='a'`).
func alwaysTrue(clause string) bool {
	if reAlwaysTrue.MatchString(clause) {
		return true
	}
	if m := reDigitsEq.FindStringSubmatch(clause); m != nil && m[1] == m[2] {
		return true
	}
	// The upstream pattern is case-insensitive, and so is its backreference.
	if m := reQuotedEq.FindStringSubmatch(clause); m != nil && strings.EqualFold(m[1], m[2]) {
		return true
	}
	return false
}

// scopedDeletesOnly: every DELETE carries a WHERE, and nothing drops or truncates.
func scopedDeletesOnly(sql string) bool {
	if reSQLComment.MatchString(sql) {
		return false
	}
	statements := sqlStatements(sql)
	for _, s := range statements {
		if reDropTruncate.MatchString(s) {
			return false
		}
	}
	deletes := 0
	for _, s := range statements {
		if !reDeleteFrom.MatchString(s) {
			continue
		}
		deletes++
		where := reWhere.FindStringSubmatch(s)
		if where == nil {
			return false
		}
		clause := jsTrim(where[1])
		if m := reParenWrapped.FindStringSubmatch(clause); m != nil {
			clause = jsTrim(m[1])
		}
		if alwaysTrue(clause) {
			return false
		}
	}
	return deletes > 0
}

var sqlRuleIDs = setOf("sql-drop", "sql-truncate", "sql-delete")

type sqlEntry struct {
	segment *sqlSegment
	call    *sqlCall
}

func (e sqlEntry) text() string {
	if e.call != nil {
		return e.call.text
	}
	return segmentText(e.segment)
}

// segmentText is the words and heredoc bodies of a segment joined by spaces.
func segmentText(segment *sqlSegment) string {
	n := len(segment.words) + len(segment.heredocs)
	switch n {
	case 0:
		return ""
	case 1:
		if len(segment.words) == 1 {
			return segment.words[0].text
		}
		return segment.heredocs[0].body
	}
	size := n - 1
	for _, w := range segment.words {
		size += len(w.text)
	}
	for _, h := range segment.heredocs {
		size += len(h.body)
	}
	var b strings.Builder
	b.Grow(size)
	for i, w := range segment.words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w.text)
	}
	for i, h := range segment.heredocs {
		if i > 0 || len(segment.words) > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(h.body)
	}
	return b.String()
}

// mayHaveSqlClient is a necessary condition for any segment of command to be a SQL client call: the client's name is
// the text of a word, and a word's text is made of bytes of the command in order (quotes and backslashes only drop
// bytes), so the name is a subsequence of the command. Without a client call applySqlTargets changes no hit.
func mayHaveSqlClient(command string) bool {
	for _, name := range sqlClientNames {
		rest := command
		found := true
		for i := range len(name) {
			at := strings.IndexByte(rest, name[i])
			if at < 0 {
				found = false
				break
			}
			rest = rest[at+1:]
		}
		if found {
			return true
		}
	}
	return false
}

func (e sqlEntry) readOnlyHosted() bool {
	return e.call != nil && e.call.target == sqlHosted && readOnlyWrapped(e.call)
}

// applySqlTargets replaces or drops the SQL pattern hits and adds the target hits, from the raw command (heredoc bodies included).
func applySqlTargets(raw string, hits *hitSet, exempt map[string]bool) {
	if !mayHaveSqlClient(raw) {
		return
	}
	var entries []sqlEntry
	for _, s := range sqlSegments(raw) {
		entries = append(entries, sqlEntry{s, readSqlCall(s)})
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
