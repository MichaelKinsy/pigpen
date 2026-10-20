package warden

import (
	"fmt"
	"testing"
)

const hostedURL = "postgresql://postgres.ref:pw@aws-0-us-east-1.pooler.supabase.com:6543/postgres"

func sqlHits(command string) [][2]string {
	out := [][2]string{}
	for _, h := range MatchPatterns("bash", map[string]any{"command": command}, "", nil) {
		out = append(out, [2]string{h.ID, string(h.Severity)})
	}
	return out
}

// sqlLevel is the offline level: no judge, so the pattern floor decides.
func sqlLevel(command string) Level {
	return eval(ActionInput{Tool: "bash", Input: map[string]any{"command": command}, Cwd: cwd}, EvaluateOptions{Config: cfg()}).Level
}

func wantHits(t *testing.T, command string, want ...[2]string) {
	t.Helper()
	if want == nil {
		want = [][2]string{}
	}
	mustEqual(t, sqlHits(command), want, command)
}

func wantLevel(t *testing.T, command string, want Level) {
	t.Helper()
	if got := sqlLevel(command); got != want {
		t.Errorf("%s: level %s, want %s", command, got, want)
	}
}

// twin: tests/sql-target.test.ts:12 "sqlTarget reads loopback, hosted, and unknown targets"
func TestSqlTargetReadsLoopbackHostedAndUnknown(t *testing.T) {
	for command, want := range map[string]sqlTargetKind{
		"psql -h 127.0.0.1 -U postgres -c 'SELECT 1'":                 sqlLoopback,
		"PGHOST=localhost psql -c 'SELECT 1'":                         sqlLoopback,
		"psql --host=/var/run/postgresql -d app":                      sqlLoopback,
		"psql postgres://user:pw@[::1]:5432/app":                      sqlLoopback,
		"psql -d 'host=localhost dbname=app'":                         sqlLoopback,
		fmt.Sprintf(`psql "%s" -c 'SELECT 1'`, hostedURL):             sqlHosted,
		"psql -h db.abc.supabase.co -c 'SELECT 1'":                    sqlHosted,
		"mysql -h main.abc.us-east-1.rds.amazonaws.com -e 'SELECT 1'": sqlHosted,
		"supabase db dump --linked":                                   sqlHosted,
		"supabase db query --target dev":                              sqlHosted,
		"supabase db query --target local":                            sqlUnknown,
		"psql \"$DATABASE_URL\" -c 'SELECT 1'":                        sqlUnknown,
		"psql -h $DB_HOST -c 'SELECT 1'":                              sqlUnknown,
		"psql -h db.internal -c 'SELECT 1'":                           sqlUnknown,
		"psql -c 'SELECT 1'":                                          sqlUnknown,
		"ls -h localhost":                                             sqlUnknown,
	} {
		if got := sqlTarget(command); got != want {
			t.Errorf("%s: %s, want %s", command, got, want)
		}
	}
}

// twin: tests/sql-target.test.ts:31 "a loopback DELETE … WHERE warns"
func TestALoopbackDeleteWhereWarns(t *testing.T) {
	command := "psql -h 127.0.0.1 -U postgres -d app -c \"DELETE FROM users WHERE email LIKE 'crew-%';\""
	wantHits(t, command, [2]string{"sql-delete-local", "risky"})
	wantLevel(t, command, LevelWarn)
}

// twin: tests/sql-target.test.ts:37 "a loopback TRUNCATE holds"
func TestALoopbackTruncateHolds(t *testing.T) {
	command := "psql -h localhost -c 'TRUNCATE users;'"
	wantHits(t, command, [2]string{"sql-truncate", "destructive"})
	wantLevel(t, command, LevelConfirm)
}

// twin: tests/sql-target.test.ts:43 "a loopback DELETE FROM users; with no WHERE holds"
func TestALoopbackDeleteWithoutWhereHolds(t *testing.T) {
	command := "psql -h 127.0.0.1 -c 'DELETE FROM users;'"
	wantHits(t, command, [2]string{"sql-delete", "destructive"})
	wantLevel(t, command, LevelConfirm)
	wantHits(t, `psql -h 127.0.0.1 -c "DELETE FROM users; SELECT * FROM users WHERE id = 1"`, [2]string{"sql-delete", "destructive"})
	wantHits(t, `psql -h 127.0.0.1 -c "DELETE FROM a WHERE id = 1" && psql -h db.internal -c "DELETE FROM b WHERE id = 1"`, [2]string{"sql-delete", "destructive"})
}

// twin: tests/sql-target.test.ts:51 "a hosted SELECT warns as elevated"
func TestAHostedSelectWarnsAsElevated(t *testing.T) {
	command := fmt.Sprintf(`psql "%s" -c 'SELECT count(*) FROM users'`, hostedURL)
	wantHits(t, command, [2]string{"sql-hosted", "risky"})
	hit := MatchPatterns("bash", map[string]any{"command": command}, "", nil)[0]
	mustMatch(t, hit.Label, `aws-0-us-east-1\.pooler\.supabase\.com`, "label names the host")
	mustNotContain(t, hit.Label, "pw@", "label never carries the credentials")
	wantLevel(t, command, LevelWarn)
}

// twin: tests/sql-target.test.ts:59 "a hosted UPDATE holds"
func TestAHostedUpdateHolds(t *testing.T) {
	command := fmt.Sprintf(`psql "%s" -c "UPDATE users SET plan = 'pro' WHERE id = 7"`, hostedURL)
	both := [][2]string{{"sql-hosted", "risky"}, {"sql-hosted-write", "destructive"}}
	wantHits(t, command, both...)
	wantLevel(t, command, LevelConfirm)
	wantHits(t, fmt.Sprintf("psql \"%s\" <<'SQL'\nUPDATE users SET plan = 'pro';\nSQL", hostedURL), both...)
}

// twin: tests/sql-target.test.ts:66 "a hosted read-only wrapper with a SELECT is quiet"
func TestAHostedReadOnlyWrapperIsQuiet(t *testing.T) {
	command := fmt.Sprintf(`psql "%s" -c "BEGIN READ ONLY; SELECT count(*) FROM users; ROLLBACK;"`, hostedURL)
	wantHits(t, command)
	wantLevel(t, command, LevelAllow)
	wantHits(t, fmt.Sprintf("psql \"%s\" <<'SQL'\nSTART TRANSACTION READ ONLY;\nSELECT 1;\nROLLBACK;\nSQL", hostedURL))
	wantHits(t, fmt.Sprintf(`psql "%s" -c "BEGIN; SET TRANSACTION READ ONLY; DELETE FROM users WHERE id = 1; ROLLBACK;"`, hostedURL))
}

// twin: tests/sql-target.test.ts:74 "a read-only wrapper followed by COMMIT is not exempt"
func TestAReadOnlyWrapperFollowedByCommitIsNotExempt(t *testing.T) {
	command := fmt.Sprintf(`psql "%s" -c "BEGIN READ ONLY; SELECT 1; ROLLBACK; COMMIT;"`, hostedURL)
	wantHits(t, command, [2]string{"sql-hosted", "risky"})
	wantLevel(t, command, LevelWarn)
	wantHits(t, fmt.Sprintf(`psql "%s" -c "BEGIN READ ONLY; SELECT 1; ROLLBACK; DELETE FROM users WHERE id = 1; ROLLBACK;"`, hostedURL),
		[2]string{"sql-delete", "destructive"}, [2]string{"sql-hosted", "risky"}, [2]string{"sql-hosted-write", "destructive"})
	wantHits(t, fmt.Sprintf(`psql -1 "%s" -c "BEGIN READ ONLY; SELECT 1; ROLLBACK;"`, hostedURL), [2]string{"sql-hosted", "risky"})
}

// twin: tests/sql-target.test.ts:82 "$DATABASE_URL keeps today's behaviour"
func TestDatabaseURLKeepsTodaysBehaviour(t *testing.T) {
	wantHits(t, "psql \"$DATABASE_URL\" -c \"DELETE FROM users WHERE email LIKE 'crew-%'\"", [2]string{"sql-delete", "destructive"})
	wantHits(t, "psql \"$DATABASE_URL\" -c 'UPDATE users SET plan = 1'")
	wantHits(t, "psql \"$DATABASE_URL\" -c 'BEGIN READ ONLY; DROP TABLE users; ROLLBACK;'", [2]string{"sql-drop", "destructive"})
	wantHits(t, "psql -h 127.0.0.1 -f cleanup.sql")
}

// twin: tests/sql-target.test.ts:89 "a heredoc DROP TABLE to loopback holds"
func TestAHeredocDropTableToLoopbackHolds(t *testing.T) {
	command := "psql -h 127.0.0.1 <<EOF\nDROP TABLE users;\nEOF"
	wantHits(t, command, [2]string{"sql-drop", "destructive"})
	wantLevel(t, command, LevelConfirm)
}

// twin: tests/sql-target.test.ts:95 "a heredoc DELETE FROM t; to loopback holds"
func TestAHeredocDeleteWithoutWhereHolds(t *testing.T) {
	command := "psql -h localhost <<'SQL'\nDELETE FROM t;\nSQL"
	wantHits(t, command, [2]string{"sql-delete", "destructive"})
	wantLevel(t, command, LevelConfirm)
}

// twin: tests/sql-target.test.ts:101 "a heredoc DELETE FROM t WHERE email LIKE 'x%'; to loopback warns"
func TestAHeredocDeleteWhereToLoopbackWarns(t *testing.T) {
	command := "psql -h 127.0.0.1 <<'SQL'\nDELETE FROM t WHERE email LIKE 'x%';\nSQL"
	wantHits(t, command, [2]string{"sql-delete-local", "risky"})
	wantLevel(t, command, LevelWarn)
}

// twin: tests/sql-target.test.ts:107 "heredoc SQL to cat stays data"
func TestHeredocSQLToCatStaysData(t *testing.T) {
	wantHits(t, "cat > cleanup.sql <<'SQL'\nDROP TABLE users;\nDELETE FROM t;\nSQL")
}

// twin: tests/sql-target.test.ts:111 "WHERE true and WHERE 1=1 on loopback hold"
func TestAlwaysTrueWhereOnLoopbackHolds(t *testing.T) {
	for _, where := range []string{"true", "1=1", "1 = 1", "'a'='a'", "NOT false", "(true)"} {
		command := fmt.Sprintf(`psql -h 127.0.0.1 -c "DELETE FROM t WHERE %s;"`, where)
		wantHits(t, command, [2]string{"sql-delete", "destructive"})
		wantLevel(t, command, LevelConfirm)
	}
}

// twin: tests/sql-target.test.ts:119 "WHERE id = 1 on loopback warns"
func TestWhereIDOnLoopbackWarns(t *testing.T) {
	command := "psql -h 127.0.0.1 -c 'DELETE FROM t WHERE id = 1;'"
	wantHits(t, command, [2]string{"sql-delete-local", "risky"})
	wantLevel(t, command, LevelWarn)
	wantHits(t, "psql -h 127.0.0.1 -c 'DELETE FROM t WHERE 1=1 AND id = 1;'", [2]string{"sql-delete-local", "risky"})
}
