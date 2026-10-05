package acp

// Layer-2 cases for the adapter's own storage, PiG directory resolution, settings, prompt
// template loading, auth-required detection, bash helpers and the startup info block.

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFileStore(t *testing.T) {
	t.Run("stores, refreshes and deletes entries in a version 1 JSON file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "deep", "session-map.json")
		s := NewFileStore(path)
		s.Upsert(StoredSession{SessionID: "a", Cwd: "/w", SessionFile: "/f1"})
		s.Upsert(StoredSession{SessionID: "a", Cwd: "/w", SessionFile: "/f2"})
		s.Upsert(StoredSession{SessionID: "b", Cwd: "/w", SessionFile: "/g"})
		got := s.Get("a")
		if got == nil || got.SessionFile != "/f2" || got.UpdatedAt == "" {
			t.Fatalf("a = %+v", got)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), `"version": 1`) || !strings.HasSuffix(string(raw), "\n") {
			t.Errorf("file = %s", raw)
		}
		s.Delete("a")
		s.Delete("never-there")
		if s.Get("a") != nil || s.Get("b") == nil {
			t.Error("delete removed the wrong entry")
		}
	})
	t.Run("a missing, corrupt or wrong-version file reads as empty", func(t *testing.T) {
		dir := t.TempDir()
		for name, body := range map[string]string{"corrupt": "{nope", "wrong": `{"version":2,"sessions":{"a":{"sessionId":"a"}}}`, "null": `{"version":1,"sessions":null}`} {
			p := filepath.Join(dir, name+".json")
			write(t, p, body)
			if NewFileStore(p).Get("a") != nil {
				t.Errorf("%s: entry found", name)
			}
		}
		if NewFileStore(filepath.Join(dir, "absent.json")).Get("a") != nil {
			t.Error("absent: entry found")
		}
	})
	t.Run("the default location is under PIG_HOME, not ~/.pi", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("PIG_HOME", home)
		if got, want := SessionMapPath(), filepath.Join(home, "pig-acp", "session-map.json"); got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})
}

func TestPigDirectories(t *testing.T) {
	clear := func(t *testing.T) {
		for _, k := range []string{"PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_USE_PI_DIRS", "XDG_CONFIG_HOME", "PIG_CODING_AGENT_SESSION_DIR", "PI_CODING_AGENT_SESSION_DIR"} {
			t.Setenv(k, "")
		}
	}
	// Found by review. PiG keeps its own state under its config root (PIG_HOME, else
	// XDG_CONFIG_HOME/pig, else ~/.pig) even when PIG_USE_PI_DIRS=1 shares the agent and project
	// directories with Pi (internal/codingagent/paths.go ConfigRoot); the adapter's session map is
	// PiG state, so shared mode must not write it into ~/.pi.
	t.Run("the session map stays under PiG's config root in shared mode", func(t *testing.T) {
		clear(t)
		t.Setenv("HOME", "/home/u")
		t.Setenv("PIG_USE_PI_DIRS", "1")
		if got := SessionMapPath(); got != "/home/u/.pig/pig-acp/session-map.json" {
			t.Errorf("shared mode: session map = %q", got)
		}
		t.Setenv("XDG_CONFIG_HOME", "/x")
		if got := SessionMapPath(); got != "/x/pig/pig-acp/session-map.json" {
			t.Errorf("shared mode with XDG_CONFIG_HOME: session map = %q", got)
		}
	})
	// pig expands a leading ~ in PIG_HOME, the agent directory variables and the session directory
	// variable (ExpandTildePath). An editor passes env values unexpanded, so the adapter must expand
	// them the same way or it looks for sessions somewhere pig never wrote them.
	t.Run("a leading ~ in the directory variables is the home directory, as pig reads it", func(t *testing.T) {
		clear(t)
		t.Setenv("HOME", "/home/u")
		t.Setenv("PIG_HOME", "~/ph")
		if got := SessionMapPath(); got != "/home/u/ph/pig-acp/session-map.json" {
			t.Errorf("PIG_HOME=~/ph: session map = %q", got)
		}
		if got := AgentDir(); got != "/home/u/ph/agent" {
			t.Errorf("PIG_HOME=~/ph: AgentDir = %q", got)
		}
		t.Setenv("PIG_CODING_AGENT_DIR", "~/a")
		if got := AgentDir(); got != "/home/u/a" {
			t.Errorf("PIG_CODING_AGENT_DIR=~/a: AgentDir = %q", got)
		}
		t.Setenv("PIG_CODING_AGENT_SESSION_DIR", "~/s")
		if got := PiSessionsDir(); got != "/home/u/s" {
			t.Errorf("PIG_CODING_AGENT_SESSION_DIR=~/s: PiSessionsDir = %q", got)
		}
		t.Setenv("PIG_USE_PI_DIRS", "1")
		t.Setenv("PI_CODING_AGENT_DIR", "~/pa")
		if got := AgentDir(); got != "/home/u/pa" {
			t.Errorf("shared, PI_CODING_AGENT_DIR=~/pa: AgentDir = %q", got)
		}
	})
	// In shared mode pig reads PI_CODING_AGENT_SESSION_DIR, not PiG's variable (cmd/pig/main.go
	// resolveSessionDir).
	t.Run("shared mode reads PI_CODING_AGENT_SESSION_DIR", func(t *testing.T) {
		clear(t)
		t.Setenv("HOME", "/home/u")
		t.Setenv("PIG_USE_PI_DIRS", "1")
		t.Setenv("PIG_CODING_AGENT_SESSION_DIR", "/pig-sessions")
		t.Setenv("PI_CODING_AGENT_SESSION_DIR", "/pi-sessions")
		if got := PiSessionsDir(); got != "/pi-sessions" {
			t.Errorf("PiSessionsDir = %q", got)
		}
	})
	t.Run("default mode: PIG_CODING_AGENT_DIR wins, then PIG_HOME/agent", func(t *testing.T) {
		clear(t)
		t.Setenv("PIG_HOME", "/h")
		if AgentDir() != "/h/agent" {
			t.Errorf("AgentDir = %q", AgentDir())
		}
		t.Setenv("PIG_CODING_AGENT_DIR", "/a")
		if AgentDir() != "/a" {
			t.Errorf("AgentDir = %q", AgentDir())
		}
	})
	t.Run("default mode without PIG_HOME uses XDG_CONFIG_HOME/pig, then ~/.pig", func(t *testing.T) {
		clear(t)
		t.Setenv("XDG_CONFIG_HOME", "/x")
		if AgentDir() != "/x/pig/agent" {
			t.Errorf("AgentDir = %q", AgentDir())
		}
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/u")
		if AgentDir() != "/home/u/.pig/agent" {
			t.Errorf("AgentDir = %q", AgentDir())
		}
	})
	t.Run("PIG_USE_PI_DIRS=1 selects Pi's directories and project .pi", func(t *testing.T) {
		clear(t)
		t.Setenv("PIG_USE_PI_DIRS", "1")
		t.Setenv("HOME", "/home/u")
		t.Setenv("PIG_CODING_AGENT_DIR", "/ignored")
		if AgentDir() != "/home/u/.pi/agent" || ProjectDirName() != ".pi" {
			t.Errorf("AgentDir=%q project=%q", AgentDir(), ProjectDirName())
		}
		t.Setenv("PI_CODING_AGENT_DIR", "/pi")
		if AgentDir() != "/pi" {
			t.Errorf("AgentDir = %q", AgentDir())
		}
	})
	t.Run("default project directory is .pig; only the exact value 1 selects shared mode", func(t *testing.T) {
		clear(t)
		if ProjectDirName() != ".pig" {
			t.Errorf("project = %q", ProjectDirName())
		}
		t.Setenv("PIG_USE_PI_DIRS", "true")
		if ProjectDirName() != ".pig" {
			t.Errorf("project = %q for PIG_USE_PI_DIRS=true", ProjectDirName())
		}
	})
	t.Run("PiSessionsDir is <agent>/sessions unless settings.json says otherwise", func(t *testing.T) {
		dir := t.TempDir()
		withAgentDir(t, dir)
		if PiSessionsDir() != filepath.Join(dir, "sessions") {
			t.Errorf("dir = %q", PiSessionsDir())
		}
		write(t, filepath.Join(dir, "settings.json"), `{"sessionDir":"rel/sessions"}`)
		if PiSessionsDir() != filepath.Join(dir, "rel", "sessions") {
			t.Errorf("relative sessionDir: %q", PiSessionsDir())
		}
		write(t, filepath.Join(dir, "settings.json"), `{"sessionDir":"  "}`)
		if PiSessionsDir() != filepath.Join(dir, "sessions") {
			t.Errorf("blank sessionDir: %q", PiSessionsDir())
		}
		t.Setenv("PIG_CODING_AGENT_SESSION_DIR", "/env/sessions")
		if PiSessionsDir() != "/env/sessions" {
			t.Errorf("env sessions dir: %q", PiSessionsDir())
		}
	})
}

func TestSettings(t *testing.T) {
	t.Run("project settings override global, objects merge deeply", func(t *testing.T) {
		agent, project := t.TempDir(), t.TempDir()
		withAgentDir(t, agent)
		write(t, filepath.Join(agent, "settings.json"), `{"quietStartup":false,"skills":{"enableSkillCommands":true}}`)
		write(t, filepath.Join(project, ".pig", "settings.json"), `{"quietStartup":true,"skills":{"other":1}}`)
		if !GetQuietStartup(project) {
			t.Error("project quietStartup did not win")
		}
		if !GetEnableSkillCommands(project) {
			t.Error("the nested global enableSkillCommands was lost in the merge")
		}
	})
	t.Run("enableSkillCommands: direct, nested, default true", func(t *testing.T) {
		agent := t.TempDir()
		withAgentDir(t, agent)
		if !GetEnableSkillCommands("/nowhere") {
			t.Error("default should be true")
		}
		write(t, filepath.Join(agent, "settings.json"), `{"skills":{"enableSkillCommands":false}}`)
		if GetEnableSkillCommands("/nowhere") {
			t.Error("nested false ignored")
		}
		write(t, filepath.Join(agent, "settings.json"), `{"enableSkillCommands":true,"skills":{"enableSkillCommands":false}}`)
		if !GetEnableSkillCommands("/nowhere") {
			t.Error("direct value must win")
		}
	})
	t.Run("quietStartup: direct, legacy quietStart, default false, junk ignored", func(t *testing.T) {
		agent := t.TempDir()
		withAgentDir(t, agent)
		if GetQuietStartup("/nowhere") {
			t.Error("default should be false")
		}
		write(t, filepath.Join(agent, "settings.json"), `{"quietStart":true}`)
		if !GetQuietStartup("/nowhere") {
			t.Error("legacy quietStart ignored")
		}
		write(t, filepath.Join(agent, "settings.json"), `not json`)
		if GetQuietStartup("/nowhere") {
			t.Error("a corrupt settings file must read as empty")
		}
		write(t, filepath.Join(agent, "settings.json"), `["array"]`)
		if GetQuietStartup("/nowhere") {
			t.Error("a non-object settings file must read as empty")
		}
	})
	t.Run("shared mode reads .pi/settings.json in the project", func(t *testing.T) {
		agent, project := t.TempDir(), t.TempDir()
		t.Setenv("PIG_USE_PI_DIRS", "1")
		t.Setenv("PI_CODING_AGENT_DIR", agent)
		write(t, filepath.Join(project, ".pi", "settings.json"), `{"quietStartup":true}`)
		if !GetQuietStartup(project) {
			t.Error(".pi/settings.json ignored in shared mode")
		}
	})
}

func TestLoadSlashCommands(t *testing.T) {
	t.Run("loads user then project templates with frontmatter, subdirectories and fallback descriptions", func(t *testing.T) {
		agent, project := t.TempDir(), t.TempDir()
		withAgentDir(t, agent)
		write(t, filepath.Join(agent, "prompts", "review.md"), "---\ndescription: Review code\n---\nReview $1\n")
		write(t, filepath.Join(agent, "prompts", "nodesc.md"), "\n\nFirst line of the body that is definitely longer than sixty characters in total\nmore\n")
		write(t, filepath.Join(agent, "prompts", "ignored.txt"), "x")
		write(t, filepath.Join(project, ".pig", "prompts", "team", "deploy.md"), "Deploy now\n")
		got := LoadSlashCommands(project)
		var names, descs []string
		for _, c := range got {
			names = append(names, c.Name)
			descs = append(descs, c.Description)
		}
		if !reflect.DeepEqual(names, []string{"nodesc", "review", "deploy"}) && !reflect.DeepEqual(names, []string{"review", "nodesc", "deploy"}) {
			t.Fatalf("names = %v", names)
		}
		byName := map[string]FileSlashCommand{}
		for _, c := range got {
			byName[c.Name] = c
		}
		if c := byName["review"]; c.Description != "Review code (user)" || c.Content != "Review $1" || c.Source != "(user)" {
			t.Errorf("review = %+v", c)
		}
		if c := byName["deploy"]; c.Description != "Deploy now (project:team)" || c.Source != "(project:team)" {
			t.Errorf("deploy = %+v", c)
		}
		if c := byName["nodesc"]; !strings.HasPrefix(c.Description, "First line of the body that is definitely longer than sixty ...") || !strings.HasSuffix(c.Description, " (user)") {
			t.Errorf("nodesc = %q", c.Description)
		}
		if got[len(got)-1].Name != "deploy" {
			t.Errorf("project commands must come after user commands: %v", descs)
		}
	})
	t.Run("a missing directory yields no commands", func(t *testing.T) {
		withAgentDir(t, t.TempDir())
		if got := LoadSlashCommands(t.TempDir()); len(got) != 0 {
			t.Errorf("got %+v", got)
		}
	})
}

func TestAuthRequiredError(t *testing.T) {
	for _, msg := range []string{"No API key found", "apikey missing", "missing key for x", "no key", "provider not configured", "Unauthorized", "authentication failed", "permission denied", "Forbidden", "HTTP 401", "status 403"} {
		t.Run("detects "+msg, func(t *testing.T) {
			re := MaybeAuthRequiredError(errors.New(msg))
			if re == nil || re.Code != -32000 || !strings.Contains(re.Message, "Configure an API key or log in with an OAuth provider.") {
				t.Fatalf("re = %+v", re)
			}
			methods := re.Data.(map[string]any)["authMethods"].([]map[string]any)
			if len(methods) != 1 || methods[0]["id"] != PiSetupMethodID {
				t.Errorf("data = %v", re.Data)
			}
		})
	}
	t.Run("ignores other errors and nil", func(t *testing.T) {
		if MaybeAuthRequiredError(errors.New("socket hang up")) != nil || MaybeAuthRequiredError(nil) != nil {
			t.Error("false positive")
		}
	})
}

func TestBashHelpers(t *testing.T) {
	t.Run("BashCommand searches the nested shapes and rejects blank commands", func(t *testing.T) {
		for _, in := range []any{
			map[string]any{"command": "ls"}, map[string]any{"cmd": "ls"}, map[string]any{"args": map[string]any{"command": "ls"}},
			map[string]any{"input": map[string]any{"cmd": "ls"}}, map[string]any{"rawInput": map[string]any{"command": "ls"}},
			map[string]any{"toolInput": map[string]any{"command": "ls"}}, map[string]any{"details": map[string]any{"command": "ls"}}} {
			if c, ok := BashCommand(in); !ok || c != "ls" {
				t.Errorf("%v: %q %v", in, c, ok)
			}
		}
		for _, in := range []any{nil, map[string]any{}, map[string]any{"command": "  "}, map[string]any{"command": 5}} {
			if _, ok := BashCommand(in); ok {
				t.Errorf("%v: found a command", in)
			}
		}
	})
	t.Run("BashResultText prefers content, then details/stdout/output joined with stderr", func(t *testing.T) {
		if got := BashResultText(map[string]any{"content": []any{map[string]any{"type": "text", "text": "a"}, map[string]any{"type": "text", "text": "b"}}}); got != "ab" {
			t.Errorf("content: %q", got)
		}
		if got := BashResultText(map[string]any{"details": map[string]any{"stdout": "out", "stderr": "err"}}); got != "out\nerr" {
			t.Errorf("details: %q", got)
		}
		if got := BashResultText(map[string]any{"output": "o"}); got != "o" {
			t.Errorf("output: %q", got)
		}
		if got := BashResultText(nil); got != "" {
			t.Errorf("nil: %q", got)
		}
	})
	t.Run("BashExitCode reads details.exitCode, exitCode, code, else 0 or 1", func(t *testing.T) {
		cases := []struct {
			in      any
			isError bool
			want    int
		}{
			{map[string]any{"details": map[string]any{"exitCode": 3}}, false, 3},
			{map[string]any{"exitCode": 4}, false, 4},
			{map[string]any{"code": 5}, false, 5},
			{map[string]any{}, false, 0},
			{map[string]any{}, true, 1},
			{nil, true, 1},
		}
		for _, c := range cases {
			if got := BashExitCode(c.in, c.isError); got != c.want {
				t.Errorf("%v: got %d want %d", c.in, got, c.want)
			}
		}
	})
	t.Run("BashOutputDelta appends, or resends when the output was replaced", func(t *testing.T) {
		if BashOutputDelta("ab", "abcd") != "cd" || BashOutputDelta("ab", "xy") != "xy" || BashOutputDelta("", "z") != "z" {
			t.Error("delta")
		}
	})
	t.Run("IsBashTool is case-insensitive", func(t *testing.T) {
		if !IsBashTool("Bash") || IsBashTool("bashful") {
			t.Error("IsBashTool")
		}
	})
}

func TestToolResultTextEdges(t *testing.T) {
	t.Run("an empty result is empty text", func(t *testing.T) {
		if ToolResultToText(nil) != "" {
			t.Error("nil")
		}
	})
	t.Run("stdout, stderr and exit code from the top level", func(t *testing.T) {
		got := ToolResultToText(map[string]any{"stdout": "o", "exitCode": 2})
		if got != "o\n\nexit code: 2" {
			t.Errorf("got %q", got)
		}
	})
}

func TestBuildStartupInfo(t *testing.T) {
	t.Run("lists context, skills, prompts and extensions", func(t *testing.T) {
		agent, project := t.TempDir(), t.TempDir()
		withAgentDir(t, agent)
		home := t.TempDir()
		t.Setenv("HOME", home)
		write(t, filepath.Join(project, "AGENTS.md"), "x")
		write(t, filepath.Join(agent, "skills", "one", "SKILL.md"), "x")
		write(t, filepath.Join(agent, "skills", "flat.md"), "x")
		write(t, filepath.Join(project, ".pig", "skills", "two", "SKILL.md"), "x")
		write(t, filepath.Join(home, ".agents", "skills", "three", "SKILL.md"), "x")
		write(t, filepath.Join(agent, "prompts", "review.md"), "x")
		write(t, filepath.Join(agent, "extensions", "mine.ts"), "x")
		write(t, filepath.Join(agent, "settings.json"), `{"packages":["npm:pkg","/local/pkg"]}`)
		got := BuildStartupInfo(project, "no-such-pig-binary")
		for _, want := range []string{
			"## Context\n- " + filepath.Join(project, "AGENTS.md"),
			"## Skills", filepath.Join(agent, "skills", "one", "SKILL.md"), filepath.Join(agent, "skills", "flat.md"),
			filepath.Join(project, ".pig", "skills", "two", "SKILL.md"), filepath.Join(home, ".agents", "skills", "three", "SKILL.md"),
			"## Prompts\n- /review", "## Extensions", filepath.Join(agent, "extensions", "mine.ts"), "- npm:pkg\n  - index.ts", "- /local/pkg",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("startup info lacks %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "node_modules") {
			t.Error("noise directories must be skipped")
		}
	})
	t.Run("is a single newline when there is nothing to report", func(t *testing.T) {
		withAgentDir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		if got := BuildStartupInfo(t.TempDir(), "no-such-pig-binary"); got != "\n" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("starts with the pig version header when the executable answers --version", func(t *testing.T) {
		withAgentDir(t, t.TempDir())
		t.Setenv("HOME", t.TempDir())
		script := filepath.Join(t.TempDir(), "fakepig")
		write(t, script, "#!/bin/sh\necho v1.2.3\n")
		if err := os.Chmod(script, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := BuildStartupInfo(t.TempDir(), script); !strings.HasPrefix(got, "pig v1.2.3\n---\n") {
			t.Errorf("got %q", got)
		}
	})
}

func TestSessionEditHelpers(t *testing.T) {
	t.Run("findUniqueLineNumber: unique match gives its line, duplicates and misses give none", func(t *testing.T) {
		if n, ok := findUniqueLineNumber("a\nb\nneedle\n", "needle"); !ok || n != 3 {
			t.Errorf("unique: %d %v", n, ok)
		}
		if _, ok := findUniqueLineNumber("needle needle", "needle"); ok {
			t.Error("duplicate matched")
		}
		if _, ok := findUniqueLineNumber("abc", "zzz"); ok {
			t.Error("miss matched")
		}
		if _, ok := findUniqueLineNumber("abc", ""); ok {
			t.Error("empty needle matched")
		}
	})
	t.Run("tool paths accept path and file_path; relative paths resolve against the cwd", func(t *testing.T) {
		if p, ok := getToolPath(map[string]any{"file_path": "x"}); !ok || p != "x" {
			t.Errorf("file_path: %q %v", p, ok)
		}
		locs := toToolCallLocations(map[string]any{"path": "a/b"}, "/w", 7)
		jsonEqual(t, locs, []any{map[string]any{"path": "/w/a/b", "line": 7}})
		jsonEqual(t, toToolCallLocations(map[string]any{"path": "/abs"}, "/w", 0), []any{map[string]any{"path": "/abs"}})
		if toToolCallLocations(map[string]any{}, "/w", 0) != nil {
			t.Error("locations for a call without a path")
		}
	})
	t.Run("edit args accept top-level oldText, an edits array and a stringified array, without duplicates", func(t *testing.T) {
		olds := getEditOldTexts(map[string]any{"oldText": "a", "edits": []any{map[string]any{"oldText": "a"}, map[string]any{"oldText": "b"}}})
		if !reflect.DeepEqual(olds, []string{"a", "b"}) {
			t.Errorf("olds = %v", olds)
		}
		if got := getEditOldTexts(map[string]any{"edits": "not json"}); len(got) != 0 {
			t.Errorf("got %v", got)
		}
	})
}
