package warden

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// The rewritten sqlSegments, readSqlCall and applySqlTargets against the code they replace (sqltarget_ref_test.go),
// over generated commands built to hit every branch of the tokenizer: quotes in every combination, escapes, heredocs of
// each spelling with bodies and delimiters near-missed, comment starts, redirections, operators, invalid UTF-8, Unicode
// spaces, and SQL client heads in every disguise. A difference here would be a missed hold, so the generators are wide
// and the comparison is whole (every word, flag, heredoc, call and the final hits).

var sqlGenHeads = []string{
	"psql", "mysql", "mariadb", "supabase", "/usr/bin/psql", "./psql", "'psql'", `"psql"`, `ps''ql`, `ps""ql`, `p\sql`, `\psql`, "ps'q'l", `"ps"ql`, "PSQL", "psq", "sqlpsql", "psql2",
	"sudo psql", "env psql", "time psql", "command psql", "nice -n 5 psql", "PGHOST=db.neon.tech psql", "PGHOST=127.0.0.1 psql", "PGSERVICE=x psql", "PGHOSTADDR=::1 psql", `PGHOST=$H psql`, `PGHOST="a b" psql`, "FOO=1 BAR=2 mysql",
	"supabase db", "supabase db query", "supabase --linked", "echo", "cat", "git", "ls -la", "go test ./...", "bash -c", "xargs", "tee out.log",
}

var sqlGenArgs = []string{
	"-h", "127.0.0.1", "localhost", "::1", "/var/run/postgresql", "db.example.com", "x.supabase.co", "a.neon.tech", "DB.PLANETSCALE.COM", "-U", "postgres", "-p", "5432", "-d", "app", "--dbname", "--dbname=app", "--db-url", "--db-url=postgres://u:p@h.supabase.com:5432/x",
	"--host", "--host=db.example.com", "--host=localhost", "-hlocalhost", "-h127.0.0.1", "-hdb.neon.tech", "-S", "/tmp/s.sock", "--socket=/tmp/x", "--target", "--target=local", "--target=prod", "--linked", "-1", "--single-transaction",
	"-c", "-e", "--command", "--execute", "--command=DELETE FROM t WHERE id = 1", "-cSELECT 1", "-f", "--file", "--file=x.sql", "-fx.sql", "--comments",
	"postgres://app:secret@db.example.com:5432/app", "postgresql://u@localhost/db", "mysql://root@127.0.0.1/x", "postgres:///db", "postgres://h/db?host=x.neon.tech", "postgres://h/db?host=%2Fvar%2Frun", "host=db.example.com dbname=x", "hostaddr=127.0.0.1", "host=localhost host=x.rds.amazonaws.com",
	"$HOST", "$(cat h)", "`cat h`", "${H}", "\"$H\"", "'$H'", "$", "`", "-h$H", "--host=$H",
	"-v", "ON_ERROR_STOP=1", "-X", "-q", "-t", "-A", "-1x", "-", "--", "--no-psqlrc",
}

var sqlGenSQL = []string{
	"SELECT 1", "select * from users", "DELETE FROM users WHERE id = 1", "delete from users", "DELETE FROM users WHERE 1=1", "DELETE FROM t WHERE true", "delete from t where (1 = 1)", "DELETE FROM t WHERE 'a'='A'", "delete from t where not false",
	"DROP TABLE users", "drop database x", "TRUNCATE users", "truncate table t", "UPDATE users SET plan = 'pro' WHERE id = 7", "INSERT INTO t VALUES (1)", "ALTER TABLE t ADD c int", "CREATE TABLE x (a int)", "GRANT ALL ON t TO u",
	"BEGIN READ ONLY", "BEGIN", "START TRANSACTION READ ONLY", "SET TRANSACTION READ ONLY", "ROLLBACK", "COMMIT", "END", "SAVEPOINT a", "BEGIN READ WRITE", "SET transaction_read_only = off", "-- note", "/* c */", `\\copy t to x`, "select 'a;b'", "delete from t where x like 'a%'",
}

var sqlGenSyntax = []string{
	" ", " ", " ", " ", "  ", "\t", "\n", "\n", "\r\n", "\u00a0", "\u2003", "\u3000", "\ufeff", "\v", "\f",
	"'", "'", `"`, `"`, `\`, `\\`, `\"`, `\'`, `\$`, "\\`", `\` + "\n", `\é`, `\` + "\xff", "$", "`", "$(", ")", "(", ";", ";;", "&", "&&", "||", "|", "|&", ">", ">>", "2>&1", ">&2", "&>", "<", "<<<", "<<", "<<EOF", "<<-EOF", "<<'EOF'", `<<"EOF"`, `<<\EOF`, "<< EOF", "<<- EOF", `<<'E O'`, "<<EOF;", "<<EOF|", "<<",
	"#", "# comment ", "x#y", "EOF", "\tEOF", "\t\tEOF", " EOF", "EOF ", "EOF\n", "eof", "E", "$X", "`x`", "${x}", "é", "日本", "\xff", "\xc3", "\xe6\x97", "\U0001F600", "\ufffd", "0", "-", "=", "/", "~",
}

func genSQLCommand(r *rand.Rand) string {
	var b strings.Builder
	n := 1 + r.IntN(9)
	for range n {
		switch r.IntN(10) {
		case 0, 1:
			b.WriteString(sqlGenHeads[r.IntN(len(sqlGenHeads))])
		case 2, 3, 4:
			b.WriteString(sqlGenArgs[r.IntN(len(sqlGenArgs))])
		case 5:
			sql := sqlGenSQL[r.IntN(len(sqlGenSQL))]
			if r.IntN(3) == 0 {
				sql += "; " + sqlGenSQL[r.IntN(len(sqlGenSQL))]
			}
			if r.IntN(3) == 0 {
				sql += ";\n" + sqlGenSQL[r.IntN(len(sqlGenSQL))]
			}
			switch r.IntN(6) {
			case 0:
				b.WriteString("'" + strings.ReplaceAll(sql, "'", "") + "'")
			case 1:
				b.WriteString(`"` + strings.ReplaceAll(sql, `"`, `\"`) + `"`)
			case 2:
				b.WriteString(`"` + strings.ReplaceAll(sql, `"`, ``) + `"`)
			case 3:
				b.WriteString(strings.ReplaceAll(sql, " ", `\ `))
			case 4:
				b.WriteString(sql)
			default:
				b.WriteString(`"` + strings.ReplaceAll(sql, `"`, ``) + `$X"`)
			}
		default:
			b.WriteString(sqlGenSyntax[r.IntN(len(sqlGenSyntax))])
		}
		if r.IntN(3) > 0 {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// genSQLCharCommand is a string over the shell's special characters alone: the tokenizer's corners, not SQL.
func genSQLCharCommand(r *rand.Rand) string {
	const alphabet = "'\"\\$`<>&|;()#\n \tpsqlEOF-=<<é\xff:/"
	runes := []string{}
	for _, c := range alphabet {
		runes = append(runes, string(c))
	}
	runes = append(runes, "\xff", "\xc3", "<<", "<<-", "psql", "mysql", "EOF", "\n", "\n", "\n", "'", "\"")
	var b strings.Builder
	for range 1 + r.IntN(24) {
		b.WriteString(runes[r.IntN(len(runes))])
	}
	return b.String()
}

// mutateSQLCommand duplicates, drops or swaps a few bytes or fragments of a structured command.
func mutateSQLCommand(r *rand.Rand, s string) string {
	for range 1 + r.IntN(3) {
		if len(s) == 0 {
			return s
		}
		i := r.IntN(len(s))
		switch r.IntN(4) {
		case 0:
			s = s[:i] + s[i+1:]
		case 1:
			s = s[:i] + string(s[i]) + s[i:]
		case 2:
			s = s[:i] + sqlGenSyntax[r.IntN(len(sqlGenSyntax))] + s[i:]
		default:
			j := r.IntN(len(s))
			if i > j {
				i, j = j, i
			}
			s = s[:i] + s[j:]
		}
	}
	return s
}

// genSQLCall is a plausible client call: assignments, a client, host and database options in several spellings, SQL
// as an option value or a heredoc body, then maybe a second segment.
func genSQLCall(r *rand.Rand) string {
	pick := func(list []string) string { return list[r.IntN(len(list))] }
	var b strings.Builder
	if r.IntN(4) == 0 {
		b.WriteString(pick([]string{"PGHOST=127.0.0.1 ", "PGHOST=x.neon.tech ", "PGSERVICE=a ", "PGHOST=$H ", "sudo ", "env FOO=1 "}))
	}
	b.WriteString(pick([]string{"psql", "psql", "mysql", "mariadb", "supabase db query", "'psql'", `ps""ql`, "/usr/bin/psql"}))
	for range r.IntN(5) {
		b.WriteByte(' ')
		switch r.IntN(6) {
		case 0:
			b.WriteString("-h " + pick([]string{"127.0.0.1", "localhost", "::1", "/var/run/postgresql", "db.neon.tech", "x.supabase.co", "db.example.com", "$H", `"$H"`}))
		case 1:
			b.WriteString(pick([]string{"--host=localhost", "--host=db.planetscale.com", "-hlocalhost", "-S /tmp/s.sock", "--socket=/tmp/s"}))
		case 2:
			b.WriteString("-d " + pick([]string{"app", "postgres://u:p@127.0.0.1/app", "postgres://u@h.supabase.com/x", "postgres:///x?host=localhost", "'host=localhost dbname=x'", "mysql://r@db.neon.tech/x"}))
		case 3:
			b.WriteString(pick([]string{"postgres://app:secret@db.example.com:5432/app", "postgresql://u@localhost/db", "--linked", "--target prod", "--target=local", "-1", "--single-transaction", "-U postgres", "-X"}))
		default:
			b.WriteString(pick([]string{"-q", "-t", "-v ON_ERROR_STOP=1", "-p 5432"}))
		}
	}
	sql := pick(sqlGenSQL)
	for range r.IntN(3) {
		sql += pick([]string{"; ", ";\n", "; "}) + pick(sqlGenSQL)
	}
	switch r.IntN(6) {
	case 0:
		b.WriteString(" -c '" + strings.ReplaceAll(sql, "'", "") + "'")
	case 1:
		b.WriteString(` -c "` + strings.ReplaceAll(sql, `"`, ``) + `"`)
	case 2:
		b.WriteString(" -e '" + strings.ReplaceAll(sql, "'", "") + "'")
	case 3:
		b.WriteString(" --command=" + `"` + strings.ReplaceAll(sql, `"`, ``) + `"`)
	case 4:
		b.WriteString(" <<" + pick([]string{"EOF", "'EOF'", `"EOF"`, `\EOF`, "-EOF"}) + "\n" + sql + "\n" + pick([]string{"EOF", "EOF", "\tEOF", "EOFx"}) + "\n")
	default:
		b.WriteString(pick([]string{" -f x.sql", " < x.sql", " <<< 'select 1'", "", " -c $Q", ` -c "$Q"`}))
	}
	if r.IntN(3) == 0 {
		b.WriteString(pick([]string{" && echo done", " | cat", "; psql -h db.neon.tech -c 'drop table x'", "\npsql -c 'delete from t'", " # tail"}))
	}
	return b.String()
}

func sqlGenCases(seed uint64, n int, each func(i int, command string)) {
	r := rand.New(rand.NewPCG(seed, seed+1))
	for i := range n {
		var command string
		switch i % 6 {
		case 0:
			command = genSQLCommand(r)
		case 1:
			command = genSQLCharCommand(r)
		case 2:
			command = mutateSQLCommand(r, genSQLCommand(r))
		case 3:
			command = genSQLCall(r)
		case 4:
			command = mutateSQLCommand(r, genSQLCall(r))
		default:
			command = genSQLCall(r)
		}
		each(i, command)
	}
}

func TestSqlSegmentsAgreeWithTheReference(t *testing.T) {
	multi, heredocs, quoted := 0, 0, 0
	sqlGenCases(101, 400000, func(i int, command string) {
		got, want := sqlSegments(command), sqlSegmentsRef(command)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d %q:\n got  %s\n want %s", i, command, dumpSegments(got), dumpSegments(want))
		}
		if len(want) > 1 {
			multi++
		}
		for _, s := range want {
			if len(s.heredocs) > 0 {
				heredocs++
				break
			}
		}
		if strings.ContainsAny(command, `'"`) {
			quoted++
		}
	})
	if multi < 50000 || heredocs < 5000 || quoted < 100000 {
		t.Fatalf("generator too narrow: %d multi-segment, %d with heredocs, %d quoted", multi, heredocs, quoted)
	}
}

func dumpSegments(segments []*sqlSegment) string {
	var b strings.Builder
	for _, s := range segments {
		fmt.Fprintf(&b, "{words:%+v heredocs:%+v stdin:%v} ", s.words, s.heredocs, s.stdin)
	}
	return b.String()
}

func TestReadSqlCallAgreesWithTheReference(t *testing.T) {
	clients, hosted, loopback, hasSQL := 0, 0, 0, 0
	sqlGenCases(202, 400000, func(i int, command string) {
		for _, s := range sqlSegmentsRef(command) {
			got, want := readSqlCall(s), readSqlCallRef(s)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("case %d %q:\n got  %+v\n want %+v", i, command, got, want)
			}
			if want != nil {
				clients++
				switch want.target {
				case sqlHosted:
					hosted++
				case sqlLoopback:
					loopback++
				}
				if want.hasSQL {
					hasSQL++
				}
			}
		}
	})
	if clients < 40000 || hosted < 5000 || loopback < 5000 || hasSQL < 10000 {
		t.Fatalf("generator too narrow: %d clients, %d hosted, %d loopback, %d with SQL", clients, hosted, loopback, hasSQL)
	}
}

// matchSQLHits runs the rules and the SQL targets the way MatchPatterns does: the shell rules on the data-stripped
// command first, then apply.
func matchSQLHits(command string, exempt map[string]bool, apply func(string, *hitSet, map[string]bool)) []PatternHit {
	hits := newHitSet()
	stripped := StripDataText(command).Text
	for _, rule := range shellRules {
		if !exempt[rule.ID] && rule.Test(stripped) {
			hits.add(PatternHit{ID: rule.ID, Severity: rule.Severity, Label: rule.Label})
		}
	}
	apply(command, hits, exempt)
	return hits.list()
}

func TestApplySqlTargetsAgreesWithTheReference(t *testing.T) {
	ids := []string{"sql-drop", "sql-truncate", "sql-delete", "sql-delete-local", "sql-hosted", "sql-hosted-write"}
	r := rand.New(rand.NewPCG(5, 5))
	withHits, skipped := 0, 0
	sqlGenCases(303, 250000, func(i int, command string) {
		exempt := map[string]bool{}
		if r.IntN(4) == 0 {
			for _, id := range ids {
				if r.IntN(3) == 0 {
					exempt[id] = true
				}
			}
		}
		got := matchSQLHits(command, exempt, applySqlTargets)
		want := matchSQLHits(command, exempt, applySqlTargetsRef)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d %q exempt %v:\n got  %+v\n want %+v", i, command, exempt, got, want)
		}
		if len(want) > 0 {
			withHits++
		}
		if !mayHaveSqlClient(command) {
			skipped++
		}
	})
	if withHits < 30000 || skipped < 20000 {
		t.Fatalf("generator too narrow: %d with hits, %d without a client", withHits, skipped)
	}
}

// mayHaveSqlClient must be true for every command whose segments hold a client call, in every quoting.
func TestMayHaveSqlClientNeverMissesAClient(t *testing.T) {
	sqlGenCases(404, 400000, func(i int, command string) {
		if mayHaveSqlClient(command) {
			return
		}
		for _, s := range sqlSegmentsRef(command) {
			if sqlClient(s) != "" {
				t.Fatalf("case %d %q: a client call, but mayHaveSqlClient is false", i, command)
			}
		}
	})
	for _, command := range []string{"psql", "ps''ql", `p\sql`, `"p"s'q'l`, "/usr/bin/mysql", "mari''adb", `su\pabase`, "PGHOST=x psql -c 'select 1'"} {
		if !mayHaveSqlClient(command) {
			t.Errorf("%q", command)
		}
	}
}

// Each of the four client names is read as a head when each of its letters is spelled bare, quoted, escaped or with an
// empty quote pair before it.
func TestSqlClientHeadsInEveryQuoting(t *testing.T) {
	r := rand.New(rand.NewPCG(8, 8))
	for _, name := range sqlClientNames {
		for range 6000 {
			var head strings.Builder
			for _, c := range name {
				spells := []string{string(c), "'" + string(c) + "'", `"` + string(c) + `"`, `\` + string(c), `''` + string(c), `""` + string(c)}
				head.WriteString(spells[r.IntN(len(spells))])
			}
			command := head.String() + " -h db.neon.tech -c 'DELETE FROM t'"
			got, want := sqlSegments(command), sqlSegmentsRef(command)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q:\n got  %s\n want %s", command, dumpSegments(got), dumpSegments(want))
			}
			if len(got) != 1 || sqlClient(got[0]) != name || !mayHaveSqlClient(command) {
				t.Fatalf("%q: head not read as %s", command, name)
			}
		}
	}
}
