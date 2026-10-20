package warden

import (
	"os"
	"path/filepath"
	"testing"
)

func bp(v bool) *bool { return &v }

func pathHits(tool string, input map[string]any, dir string, rules []PathRule, exempt ...string) []PatternHit {
	return MatchPatterns(tool, input, dir, &PatternOptions{PathRules: rules, ExemptRules: exempt})
}

func hitsNamed(hits []PatternHit, ids ...string) []PatternHit {
	var out []PatternHit
	for _, h := range hits {
		for _, id := range ids {
			if h.ID == id {
				out = append(out, h)
			}
		}
	}
	return out
}

func pathBashHits(command string, rules []PathRule, id string) int {
	return len(hitsNamed(pathHits("bash", map[string]any{"command": command}, cwd, rules), id))
}

// twin: tests/guard.test.ts:1239 "pathRules: file surface gates on the access dimension"
func TestPathRulesFileSurfaceGatesOnTheAccessDimension(t *testing.T) {
	// access names the side that FLOWS: "read" = reads flow, writes are held; "write" = writes flow, reads are held.
	rules := []PathRule{
		{ID: "repo-readonly", Paths: []string{"**/deploy.yaml"}, Access: "read", Tools: []string{"write", "edit", "read"}, Action: "confirm"},
		{ID: "audit-log", Paths: []string{"audit/app.log"}, Access: "write", Tools: []string{"write", "edit", "read"}, Action: "block"},
	}
	mine := func(h []PatternHit) []PatternHit { return hitsNamed(h, "repo-readonly", "audit-log") }
	if err := os.WriteFile(filepath.Join(cwd, "deploy.yaml"), []byte("image: app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(cwd, "deploy.yaml"))
	write := mine(pathHits("write", map[string]any{"path": "deploy.yaml"}, cwd, rules))
	if len(write) != 1 || write[0].Severity != SeverityDestructive || write[0].Action != "dialog" {
		t.Fatalf("a write to deploy.yaml is held by the read-flow rule: %+v", write)
	}
	if len(mine(pathHits("read", map[string]any{"path": "deploy.yaml"}, cwd, rules))) != 0 {
		t.Fatal("a read of deploy.yaml flows under the read-flow rule")
	}
	if err := os.MkdirAll(filepath.Join(cwd, "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Join(cwd, "audit"))
	if err := os.WriteFile(filepath.Join(cwd, "audit", "app.log"), []byte("entry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	held := mine(pathHits("read", map[string]any{"path": "audit/app.log"}, cwd, rules))
	if len(held) != 1 || held[0].Severity != SeverityDeny {
		t.Fatalf("a read of the write-flow path is held, block maps to deny: %+v", held)
	}
	if len(mine(pathHits("write", map[string]any{"path": "audit/app.log"}, cwd, rules))) != 0 {
		t.Fatal("a write to the write-flow path flows")
	}
}

// twin: tests/guard.test.ts:1265 "pathRules: onlyIfExists skips phantom paths, and create-protect opts out"
func TestPathRulesOnlyIfExistsSkipsPhantomPaths(t *testing.T) {
	rule := PathRule{ID: "env", Paths: []string{".env"}, Access: "none", Tools: []string{"write", "edit"}, Action: "warn"}
	hits := func(r PathRule) int {
		return len(hitsNamed(pathHits("write", map[string]any{"path": ".env"}, cwd, []PathRule{r}), "env"))
	}
	if hits(rule) != 0 {
		t.Fatal("a .env that does not exist does not fire")
	}
	create := rule
	create.OnlyIfExists = bp(false)
	if hits(create) != 1 {
		t.Fatal("onlyIfExists false fires on the missing file (create-protect)")
	}
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(cwd, ".env"))
	if hits(rule) != 1 {
		t.Fatal("an existing .env fires")
	}
}

// twin: tests/guard.test.ts:1275 "pathRules: the command surface matches write sinks and bare mentions per the access dimension"
func TestPathRulesCommandSurfaceMatchesSinksAndMentions(t *testing.T) {
	none := []PathRule{{ID: "env", Paths: []string{".env"}, Access: "none", Tools: []string{"*"}, Action: "note"}}
	read := []PathRule{{ID: "ssh", Paths: []string{"~/.ssh/authorized_keys"}, Access: "read", Tools: []string{"*"}, Action: "block"}}
	write := []PathRule{{ID: "audit", Paths: []string{"/var/audit/*.log"}, Access: "write", Tools: []string{"*"}, Action: "confirm"}}
	for _, c := range []struct {
		command string
		rules   []PathRule
		id      string
		want    int
	}{
		{"grep KEY .env", none, "env", 1},
		{"cat ~/.ssh/authorized_keys", read, "ssh", 0},
		{"echo bad >> ~/.ssh/authorized_keys", read, "ssh", 1},
		{"cat x | tee /var/audit/node1.log", write, "audit", 0},
		{"cat /var/audit/node1.log", write, "audit", 1},
		{"ls /var/audit/", read, "audit", 0},
	} {
		if got := pathBashHits(c.command, c.rules, c.id); got != c.want {
			t.Errorf("%s: %d, want %d", c.command, got, c.want)
		}
	}
}

// twin: tests/guard.test.ts:1290 "pathRules: a data heredoc mentioning the path does not fire on the command surface"
func TestPathRulesADataHeredocDoesNotFire(t *testing.T) {
	rules := []PathRule{{ID: "env", Paths: []string{".env"}, Access: "none", Tools: []string{"*"}, Action: "warn"}}
	if pathBashHits("cat <<'EOF'\nthe .env file is documented here\nEOF", rules, "env") != 0 {
		t.Fatal("heredoc data text is not a command surface match")
	}
	if pathBashHits("bash <<'EOF'\ncat .env\nEOF", rules, "env") != 1 {
		t.Fatal("a shell-sink heredoc body is in scope")
	}
}

// twin: tests/guard.test.ts:1298 "pathRules: glob semantics, ~ expansion, and regex opt-in"
func TestPathRulesGlobSemanticsTildeAndRegex(t *testing.T) {
	glob := []PathRule{{ID: "keys", Paths: []string{"~/.ssh/id_*"}, Access: "none", Tools: []string{"read"}, Action: "warn"}}
	if len(pathHits("read", map[string]any{"path": "~/.ssh/id_ed25519"}, cwd, glob)) != 1 {
		t.Fatal("~ expands and * matches within the segment")
	}
	if len(pathHits("read", map[string]any{"path": "/home/michaelmacleod/.ssh/id_ed25519"}, cwd, glob)) != 1 {
		t.Fatal("the absolute spelling of the same path matches too")
	}
	deep := []PathRule{{ID: "env-anywhere", Paths: []string{"**/.env"}, Access: "none", Tools: []string{"read"}, Action: "warn"}}
	if len(pathHits("read", map[string]any{"path": "apps/api/.env"}, cwd, deep)) != 1 {
		t.Fatal("** matches at any depth")
	}
	regex := []PathRule{{ID: "dated", Paths: []string{`\.env\.[0-9]{4}`}, Regex: true, Access: "none", Tools: []string{"read"}, Action: "warn"}}
	if len(pathHits("read", map[string]any{"path": ".env.2024"}, cwd, regex)) != 1 {
		t.Fatal("regex opt-in matches shapes globs cannot express")
	}
}

// twin: tests/guard.test.ts:1308 "pathRules: exemptRules silences a user path rule, and its id is known"
func TestPathRulesExemptRulesSilencesAUserPathRule(t *testing.T) {
	rules := []PathRule{{ID: "env", Paths: []string{"**/.env"}, Access: "none", Tools: []string{"*"}, Action: "block"}}
	if pathBashHits("cat .env", rules, "env") != 1 {
		t.Fatal("the rule fires on a bash mention")
	}
	if n := len(hitsNamed(pathHits("bash", map[string]any{"command": "cat .env"}, cwd, rules, "env"), "env")); n != 0 {
		t.Fatal("exemptRules silences the path rule by id")
	}
	mustEqual(t, UnknownExemptIds([]string{"env"}, nil, nil, rules, nil), []string{}, "a path rule id counts as known")
}

// twin: tests/guard.test.ts:1316 "pathRules: a block hit outranks a built-in confirm on the ladder"
func TestPathRulesABlockHitOutranksABuiltInConfirm(t *testing.T) {
	config := cfg()
	config.PathRules = []PathRule{{ID: "never-dd", Paths: []string{"/dev/sda"}, Access: "write", Tools: []string{"*"}, Action: "block"}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "cat /dev/sda"}, Task: "inspect the disk"}, EvaluateOptions{Config: config})
	if v.Level != LevelDeny {
		t.Fatal(v.Level)
	}
	mustMatch(t, reasonsJoined(v), `never-dd`, "the path rule's id is named in the reasons")
}

// twin: tests/guard.test.ts:1323 "pathRules: default config keeps sensitive-path behavior unchanged"
func TestPathRulesDefaultConfigKeepsSensitivePathBehaviour(t *testing.T) {
	if len(hitsNamed(pathHits("bash", map[string]any{"command": "cat ~/.ssh/id_rsa"}, cwd, nil), "sensitive-path")) == 0 {
		t.Fatal("bash")
	}
	if len(hitsNamed(pathHits("read", map[string]any{"path": "/home/michaelmacleod/.netrc"}, cwd, nil), "sensitive-path")) == 0 {
		t.Fatal("read")
	}
	if len(pathHits("read", map[string]any{"path": "src/index.ts"}, cwd, nil)) != 0 {
		t.Fatal("ordinary paths fire nothing")
	}
}

// twin: tests/guard.test.ts:1330 "pathRules: access:write fires the read side when the command both reads and writes matching paths"
func TestPathRulesAccessWriteFiresTheReadSide(t *testing.T) {
	rules := []PathRule{{ID: "audit", Paths: []string{"/var/audit/*.log"}, Access: "write", Tools: []string{"*"}, Action: "confirm", OnlyIfExists: bp(false)}}
	if pathBashHits("cat /var/audit/a.log | tee /var/audit/b.log", rules, "audit") != 1 {
		t.Fatal("the read side fires")
	}
	if pathBashHits("cat x | tee /var/audit/node1.log", rules, "audit") != 0 {
		t.Fatal("a write to the write-only path flows")
	}
}

// twin: tests/guard.test.ts:1338 "pathRules: a rule with explicit bash in tools fires on bash commands"
func TestPathRulesExplicitBashInToolsFires(t *testing.T) {
	rules := []PathRule{{ID: "ssh-keys", Paths: []string{"~/.ssh/id_*"}, Access: "read", Tools: []string{"write", "edit", "bash"}, Action: "confirm", OnlyIfExists: bp(false)}}
	hits := pathHits("bash", map[string]any{"command": "bash -c 'echo x > /home/test/.ssh/id_rsa'"}, "/home/test", rules)
	if len(hitsNamed(hits, "ssh-keys")) == 0 {
		t.Fatal("the bash tool name enables the command surface")
	}
}

// twin: tests/guard.test.ts:1344 "pathRules: ctx_execute_file reads fire access:none rules on the file surface"
func TestPathRulesCtxExecuteFileReadsFire(t *testing.T) {
	rules := []PathRule{{ID: "ssh-keys", Paths: []string{"~/.ssh/id_*"}, Access: "none", Tools: []string{"*"}, Action: "confirm", OnlyIfExists: bp(false)}}
	if len(hitsNamed(pathHits("ctx_execute_file", map[string]any{"path": "/home/test/.ssh/id_rsa"}, "/home/test", rules), "ssh-keys")) == 0 {
		t.Fatal("ctx_execute_file is a read, not excluded from the file surface")
	}
}

// twin: tests/guard.test.ts:1350 "pathRules: mention tail anchor — .env.example does not fire a **/.env rule, .env does"
func TestPathRulesMentionTailAnchor(t *testing.T) {
	rules := []PathRule{{ID: "env", Paths: []string{"**/.env"}, Access: "none", Tools: []string{"*"}, Action: "warn", OnlyIfExists: bp(false)}}
	if pathBashHits("cat .env.example", rules, "env") != 0 {
		t.Fatal(".env.example does not match a .env rule")
	}
	if pathBashHits("cat .env", rules, "env") != 1 {
		t.Fatal(".env does match")
	}
	if pathBashHits("bash <<'EOF'\ncat .env\nEOF", rules, "env") != 1 {
		t.Fatal(".env on its own line in a heredoc body matches")
	}
}

// twin: tests/guard.test.ts:1358 "pathRules: write sinks — >| and >& operators, tee with multiple targets and --append, grep-tee is not a phantom"
func TestPathRulesWriteSinks(t *testing.T) {
	rules := []PathRule{{ID: "audit", Paths: []string{"/var/audit/*.log"}, Access: "read", Tools: []string{"*"}, Action: "warn", OnlyIfExists: bp(false)}}
	for _, command := range []string{"sort x >| /var/audit/f.log", "sort x >& /var/audit/f.log", "cat x | tee /var/audit/a.log /var/audit/b.log", "cat x | tee --append /var/audit/app.log"} {
		if pathBashHits(command, rules, "audit") == 0 {
			t.Errorf("%s does not fire", command)
		}
	}
	if pathBashHits("grep tee /var/audit/app.log", rules, "audit") != 0 {
		t.Fatal("grep mentioning tee is not a phantom tee target")
	}
}

// twin: tests/guard.test.ts:1367 "pathRules: inertPathRules detects access:write with only write tools and read-scoped rules without read in action.tools"
func TestInertPathRules(t *testing.T) {
	tools := []string{"bash", "write", "edit"}
	writeOnly := []PathRule{{ID: "audit", Paths: []string{"/var/audit/*.log"}, Access: "write", Tools: []string{"write", "edit"}, Action: "confirm"}}
	if !contains(inertPathRules(writeOnly, tools), "audit") {
		t.Fatal("access:write with only write tools is inert")
	}
	readOnly := []PathRule{{ID: "keys", Paths: []string{"~/.ssh/id_*"}, Access: "read", Tools: []string{"read"}, Action: "confirm"}}
	if !contains(inertPathRules(readOnly, tools), "keys") {
		t.Fatal("read-scoped rule without read in action.tools is inert")
	}
	notInert := []PathRule{{ID: "repo", Paths: []string{"deploy.yaml"}, Access: "read", Tools: []string{"write", "edit"}, Action: "confirm"}}
	if len(inertPathRules(notInert, tools)) != 0 {
		t.Fatal("access:read with write tools is not inert")
	}
}
