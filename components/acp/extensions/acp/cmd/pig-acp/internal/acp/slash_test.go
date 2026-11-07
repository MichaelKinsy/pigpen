package acp

// Twins of test/unit/slash-commands.test.ts, merge-commands.test.ts, pi-commands.test.ts,
// builtin command lists, and auth-methods-terminal-auth-meta.test.ts.

import (
	"reflect"
	"testing"
)

func TestSlashCommands(t *testing.T) {
	tw(t, "unit/slash-commands", "parseCommandArgs: handles quotes", func(t *testing.T) {
		for in, want := range map[string][]string{"a b": {"a", "b"}, "'a b' c": {"a b", "c"}, `"a b" c`: {"a b", "c"}} {
			if got := ParseCommandArgs(in); !reflect.DeepEqual(got, want) {
				t.Errorf("%q: got %q, want %q", in, got, want)
			}
		}
	})
	tw(t, "unit/slash-commands", "substituteArgs: replaces $1.. and $@", func(t *testing.T) {
		if got := SubstituteArgs("x=$1 y=$2 all=$@", []string{"one", "two"}); got != "x=one y=two all=one two" {
			t.Errorf("got %q", got)
		}
		if got := SubstituteArgs("$3", []string{"one"}); got != "" {
			t.Errorf("got %q", got)
		}
	})
	tw(t, "unit/slash-commands", "expandSlashCommand: expands known command", func(t *testing.T) {
		cmds := []FileSlashCommand{{Name: "hello", Description: "(user)", Content: "Say hi to $1", Source: "(user)"}}
		for in, want := range map[string]string{"/hello world": "Say hi to world", "/unknown world": "/unknown world", "not a command": "not a command"} {
			if got := ExpandSlashCommand(in, cmds); got != want {
				t.Errorf("%q: got %q, want %q", in, got, want)
			}
		}
	})
	tw(t, "unit/slash-commands", "toAvailableCommands: de-dupes by name (first wins)", func(t *testing.T) {
		got := ToAvailableCommands([]FileSlashCommand{
			{Name: "x", Description: "first", Content: "1", Source: "(user)"},
			{Name: "x", Description: "second", Content: "2", Source: "(project)"}})
		jsonEqual(t, got, []any{map[string]any{"name": "x", "description": "first"}})
	})
	tw(t, "unit/merge-commands", "mergeCommands: preserves order and de-dupes (first wins)", func(t *testing.T) {
		mk := func(names ...string) []AvailableCommand {
			var out []AvailableCommand
			for _, n := range names {
				out = append(out, AvailableCommand{Name: n})
			}
			return out
		}
		got := MergeCommands(mk("a", "b"), mk("b", "c"))
		var names []string
		for _, c := range got {
			names = append(names, c.Name)
		}
		if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
			t.Errorf("got %v", names)
		}
	})
	tw(t, "unit/pi-commands", "toAvailableCommandsFromPiGetCommands: hides extension commands by default and filters skill commands", func(t *testing.T) {
		data := map[string]any{"commands": []any{
			map[string]any{"name": "x", "description": "X", "source": "extension"},
			map[string]any{"name": "skill:foo", "description": "Foo", "source": "skill", "location": "user"},
			map[string]any{"name": "y", "source": "prompt", "location": "project"},
		}}
		all := ToAvailableCommandsFromPiGetCommands(data, PiCommandsOptions{EnableSkillCommands: true})
		jsonEqual(t, all, []any{
			map[string]any{"name": "skill:foo", "description": "Foo"},
			map[string]any{"name": "y", "description": "(prompt:project)"},
		})
		withExt := ToAvailableCommandsFromPiGetCommands(data, PiCommandsOptions{EnableSkillCommands: true, IncludeExtensionCommands: true})
		jsonEqual(t, withExt, []any{
			map[string]any{"name": "x", "description": "X"},
			map[string]any{"name": "skill:foo", "description": "Foo"},
			map[string]any{"name": "y", "description": "(prompt:project)"},
		})
		noSkills := ToAvailableCommandsFromPiGetCommands(data, PiCommandsOptions{EnableSkillCommands: false})
		jsonEqual(t, noSkills, []any{map[string]any{"name": "y", "description": "(prompt:project)"}})
	})
	t.Run("builtin commands are the eight the adapter handles itself", func(t *testing.T) {
		var names []string
		for _, c := range BuiltinAvailableCommands() {
			names = append(names, c.Name)
		}
		want := []string{"compact", "autocompact", "export", "session", "name", "steering", "follow-up", "changelog"}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("got %v, want %v", names, want)
		}
	})
}

func TestAuthMethods(t *testing.T) {
	tw(t, "unit/auth-methods-terminal-auth-meta", "getAuthMethods: includes Zed terminal-auth metadata when enabled", func(t *testing.T) {
		methods := AuthMethods(AuthSupport{Terminal: true, ZedMeta: true})
		if len(methods) != 1 {
			t.Fatalf("%d methods", len(methods))
		}
		m := methods[0]
		if m["id"] != PiSetupMethodID {
			t.Errorf("id = %v", m["id"])
		}
		meta, _ := m["_meta"].(map[string]any)
		ta, _ := meta["terminal-auth"].(map[string]any)
		if ta == nil {
			t.Fatal("no terminal-auth metadata")
		}
		if _, ok := ta["command"].(string); !ok {
			t.Errorf("command = %v", ta["command"])
		}
		jsonEqual(t, ta["args"], []any{"--terminal-login"})
		if ta["label"] != "Launch pig" {
			t.Errorf("label = %v", ta["label"])
		}
		if m["type"] != "terminal" {
			t.Errorf("type = %v", m["type"])
		}
		jsonEqual(t, m["args"], []any{"--terminal-login"})
	})
	tw(t, "unit/auth-methods-terminal-auth-meta", "getAuthMethods: omits Zed terminal-auth metadata when disabled", func(t *testing.T) {
		methods := AuthMethods(AuthSupport{Terminal: true})
		if len(methods) != 1 {
			t.Fatalf("%d methods", len(methods))
		}
		m := methods[0]
		meta, _ := m["_meta"].(map[string]any)
		if _, ok := meta["terminal-auth"]; ok {
			t.Error("terminal-auth present")
		}
	})

	// ACP v1 (Authentication): an agent may advertise a `terminal` method only when the client advertised
	// clientCapabilities.auth.terminal. acp-tck ACP-AUTH-002 sends `clientCapabilities: {}`.
	t.Run("no terminal method for a client that advertised no sign-in capability", func(t *testing.T) {
		methods := AuthMethods(AuthSupportOf(map[string]any{}))
		if methods == nil || len(methods) != 0 {
			t.Fatalf("methods = %#v, want an empty, non-nil list", methods)
		}
		if got := AuthMethods(AuthSupportOf(nil)); got == nil || len(got) != 0 {
			t.Fatalf("methods = %#v for no capabilities at all", got)
		}
		// `terminal: false` and a non-boolean value are not the capability.
		for _, caps := range []map[string]any{
			{"auth": map[string]any{"terminal": false}}, {"auth": map[string]any{"terminal": "true"}}, {"auth": "terminal"},
			{"terminal": true}, {"_meta": map[string]any{"terminal-auth": false}},
		} {
			if got := AuthMethods(AuthSupportOf(caps)); len(got) != 0 {
				t.Errorf("%v advertised %v", caps, got)
			}
		}
	})
	t.Run("auth.terminal gets the spec's terminal method and no Zed banner", func(t *testing.T) {
		methods := AuthMethods(AuthSupportOf(map[string]any{"auth": map[string]any{"terminal": true}}))
		if len(methods) != 1 || methods[0]["type"] != "terminal" || methods[0]["id"] != PiSetupMethodID {
			t.Fatalf("methods = %v", methods)
		}
		jsonEqual(t, methods[0]["args"], []any{"--terminal-login"})
		jsonEqual(t, methods[0]["env"], map[string]any{})
		if _, ok := methods[0]["_meta"]; ok {
			t.Error("_meta without the Zed flag")
		}
	})
	t.Run("a Zed-only client gets the banner method without a terminal type", func(t *testing.T) {
		methods := AuthMethods(AuthSupportOf(map[string]any{"_meta": map[string]any{"terminal-auth": true}}))
		if len(methods) != 1 {
			t.Fatalf("methods = %v", methods)
		}
		m := methods[0]
		if _, ok := m["type"]; ok {
			t.Errorf("type = %v: a terminal method needs auth.terminal", m["type"])
		}
		if _, ok := m["args"]; ok {
			t.Errorf("args = %v without type terminal", m["args"])
		}
		if _, ok := m["_meta"].(map[string]any)["terminal-auth"]; !ok {
			t.Errorf("no terminal-auth banner: %v", m)
		}
	})
}
