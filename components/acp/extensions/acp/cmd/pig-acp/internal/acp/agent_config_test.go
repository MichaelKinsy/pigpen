package acp

// Twins of test/unit/session-config-options.test.ts, test/unit/context-usage.test.ts (agent
// parts) and test/unit/model-thinking-levels.test.ts.

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

var allLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

func models(pairs ...[3]string) map[string]any {
	var list []any
	for _, p := range pairs {
		m := map[string]any{"provider": p[0], "id": p[1]}
		if p[2] != "" {
			m["name"] = p[2]
		}
		list = append(list, m)
	}
	return map[string]any{"models": list}
}

func newSessionReq(cwd string) NewSessionRequest {
	return NewSessionRequest{Cwd: cwd, McpServers: []any{}}
}

func TestSessionConfigOptions(t *testing.T) {
	tw(t, "unit/session-config-options", "PiAcpAgent: newSession returns configOptions for model and thinking selectors", func(t *testing.T) {
		conn := newFakeConn()
		proc := newFakeProc()
		proc.getLevelsFn = func() ([]string, error) { return allLevels, nil }
		proc.getModelsFn = func() (map[string]any, error) {
			return models([3]string{"test", "alpha", "Alpha"}, [3]string{"test", "beta", "Beta"}), nil
		}
		proc.getStateFn = func() (map[string]any, error) {
			return map[string]any{"thinkingLevel": "high", "model": map[string]any{"provider": "test", "id": "beta"}}, nil
		}
		a, _ := testAgent(conn)
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		result, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil {
			t.Fatal(err)
		}
		if result.Models == nil || result.Models.CurrentModelID != "test/beta" || result.Modes.CurrentModeID != "high" {
			t.Fatalf("models=%+v modes=%+v", result.Models, result.Modes)
		}
		var thinking []any
		for _, l := range allLevels {
			thinking = append(thinking, map[string]any{"value": l, "name": "Thinking: " + l, "description": nil})
		}
		jsonEqual(t, result.ConfigOptions, []any{
			map[string]any{"type": "select", "id": "model", "category": "model", "name": "Model", "description": "Select the model for this session", "currentValue": "test/beta",
				"options": []any{
					map[string]any{"value": "test/alpha", "name": "test/Alpha", "description": nil},
					map[string]any{"value": "test/beta", "name": "test/Beta", "description": nil}}},
			map[string]any{"type": "select", "id": "thought_level", "category": "thought_level", "name": "Thinking", "description": "Set the reasoning effort for this session", "currentValue": "high", "options": thinking},
		})
	})

	tw(t, "unit/session-config-options", "PiAcpAgent: setSessionConfigOption maps model changes to pi and emits config_option_update", func(t *testing.T) {
		conn := newFakeConn()
		state := map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "alpha"}}
		var calls []map[string]string
		proc := newFakeProc()
		proc.getLevelsFn = func() ([]string, error) { return allLevels, nil }
		proc.getModelsFn = func() (map[string]any, error) {
			return models([3]string{"test", "alpha", "Alpha"}, [3]string{"test", "beta", "Beta"}), nil
		}
		proc.getStateFn = func() (map[string]any, error) { return state, nil }
		proc.setModelFn = func(p, id string) error {
			calls = append(calls, map[string]string{"provider": p, "modelId": id})
			state["model"] = map[string]any{"provider": p, "id": id}
			return nil
		}
		a, _ := testAgent(conn)
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		result, err := a.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "s1", ConfigID: "model", Value: "test/beta"})
		if err != nil {
			t.Fatal(err)
		}
		jsonEqual(t, calls, []any{map[string]any{"provider": "test", "modelId": "beta"}})
		if v := optionValue(result.ConfigOptions, "model"); v != "test/beta" {
			t.Errorf("model = %q", v)
		}
		var got []Update
		for _, u := range conn.all() {
			got = append(got, u.Update)
		}
		jsonEqual(t, got, []any{
			map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "medium"},
			map[string]any{"sessionUpdate": "config_option_update", "configOptions": result.ConfigOptions},
		})
		for _, u := range conn.all() {
			if u.SessionID != "s1" {
				t.Errorf("sessionId = %q", u.SessionID)
			}
		}
	})

	tw(t, "unit/session-config-options", "PiAcpAgent: setSessionConfigOption maps thought level changes to pi and emits sync updates", func(t *testing.T) {
		conn := newFakeConn()
		state := map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "alpha"}}
		var levels []string
		proc := newFakeProc()
		proc.getLevelsFn = func() ([]string, error) { return allLevels, nil }
		proc.getModelsFn = func() (map[string]any, error) { return models([3]string{"test", "alpha", "Alpha"}), nil }
		proc.getStateFn = func() (map[string]any, error) { return state, nil }
		proc.setThinkingFn = func(l string) error { levels = append(levels, l); state["thinkingLevel"] = l; return nil }
		a, _ := testAgent(conn)
		a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}}
		result, err := a.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "s1", ConfigID: "thought_level", Value: "xhigh"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(levels, []string{"xhigh"}) {
			t.Errorf("levels = %v", levels)
		}
		if v := optionValue(result.ConfigOptions, "thought_level"); v != "xhigh" {
			t.Errorf("thought_level = %q", v)
		}
		var got []Update
		for _, u := range conn.all() {
			got = append(got, u.Update)
		}
		jsonEqual(t, got, []any{
			map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "xhigh"},
			map[string]any{"sessionUpdate": "config_option_update", "configOptions": result.ConfigOptions},
		})
	})
}

func optionValue(opts []ConfigOption, id string) string {
	for _, o := range opts {
		if o.ID == id {
			return o.CurrentValue
		}
	}
	return ""
}

// context-usage.test.ts
func TestContextUsage(t *testing.T) {
	agentWith := func(t *testing.T, proc *fakeProc, conn *fakeConn) (*Agent, *scheduler, *Session) {
		s := newTestSession(cwdNow(t), proc, conn)
		a, sch := testAgent(conn)
		a.sessions = &fakeSessions{session: s, anyID: true}
		return a, sch, s
	}
	// The session's id in these tests is "s1".
	tw(t, "unit/context-usage", "PiAcpSession: context usage request specifies the auxiliary timeout", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		var requested = -1
		proc.getSessionStatsFn = func(timeoutMs int) (SessionStats, error) {
			requested = timeoutMs
			return SessionStats{"contextUsage": map[string]any{"tokens": 100, "contextWindow": 100000}}, nil
		}
		newTestSession(cwdNow(t), proc, conn).PublishContextUsage()
		if requested != SessionStatsTimeoutMs {
			t.Errorf("timeout = %d", requested)
		}
		jsonEqual(t, usageUpdates(conn), []any{map[string]any{"sessionUpdate": "usage_update", "used": 100, "size": 100000}})
	})

	tw(t, "unit/context-usage", "PiAcpAgent: newSession publishes context usage only after the response is returned", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 1234, "contextWindow": 100000}}
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		a, sch, _ := agentWith(t, proc, conn)
		res, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil || res.SessionID != "s1" {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		if len(usageUpdates(conn)) != 0 {
			t.Fatal("usage_update sent before the response was returned")
		}
		sch.drain()
		jsonEqual(t, usageUpdates(conn), []any{map[string]any{"sessionUpdate": "usage_update", "used": 1234, "size": 100000}})
	})

	tw(t, "unit/context-usage", "PiAcpAgent: newSession tolerates a failing get_session_stats", func(t *testing.T) {
		conn, proc := newFakeConn(), newFakeProc()
		proc.sessionStatsError = errors.New("pi get_session_stats failed: unsupported")
		proc.getStateFn = func() (map[string]any, error) { return map[string]any{"thinkingLevel": "medium"}, nil }
		a, sch, _ := agentWith(t, proc, conn)
		res, err := a.NewSession(newSessionReq(cwdNow(t)))
		if err != nil || res.SessionID != "s1" {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		sch.drain()
		if len(usageUpdates(conn)) != 0 {
			t.Error("usage_update emitted")
		}
	})

	switching := func(t *testing.T, prompt bool) (*fakeConn, *fakeProc, *Agent) {
		conn, proc := newFakeConn(), newFakeProc()
		state := map[string]any{"thinkingLevel": "medium", "model": map[string]any{"provider": "test", "id": "alpha"}}
		proc.getModelsFn = func() (map[string]any, error) {
			return models([3]string{"test", "alpha", "Alpha"}, [3]string{"test", "beta", "Beta"}), nil
		}
		proc.getStateFn = func() (map[string]any, error) { return state, nil }
		proc.setModelFn = func(p, id string) error {
			state["model"] = map[string]any{"provider": p, "id": id}
			w := 100000
			if id == "beta" {
				w = 200000
			}
			proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 500, "contextWindow": w}}
			return nil
		}
		proc.setThinkingFn = func(l string) error { state["thinkingLevel"] = l; return nil }
		proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 500, "contextWindow": 100000}}
		a, _, _ := agentWith(t, proc, conn)
		return conn, proc, a
	}

	tw(t, "unit/context-usage", "PiAcpAgent: switching the model config option refreshes context usage", func(t *testing.T) {
		conn, _, a := switching(t, false)
		if _, err := a.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "s1", ConfigID: "model", Value: "test/beta"}); err != nil {
			t.Fatal(err)
		}
		if got := conn.kinds(); !reflect.DeepEqual(got, []string{"current_mode_update", "config_option_update", "usage_update"}) {
			t.Fatalf("kinds = %v", got)
		}
		ups := conn.all()
		jsonEqual(t, ups[len(ups)-1], map[string]any{"SessionID": "s1", "Update": map[string]any{"sessionUpdate": "usage_update", "used": 500, "size": 200000}})
	})

	tw(t, "unit/context-usage", "PiAcpAgent: unstable_setSessionModel refreshes context usage", func(t *testing.T) {
		conn, proc, a := switching(t, false)
		proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 700, "contextWindow": 100000}}
		proc.setModelFn = func(p, id string) error {
			w := 100000
			if id == "beta" {
				w = 200000
			}
			proc.sessionStats = SessionStats{"contextUsage": map[string]any{"tokens": 700, "contextWindow": w}}
			return nil
		}
		if err := a.UnstableSetSessionModel(SetSessionModelRequest{SessionID: "s1", ModelID: "test/beta"}); err != nil {
			t.Fatal(err)
		}
		if proc.statsCount() != 1 {
			t.Errorf("get_session_stats called %d times", proc.statsCount())
		}
		if got := conn.kinds(); !reflect.DeepEqual(got, []string{"current_mode_update", "config_option_update", "usage_update"}) {
			t.Fatalf("kinds = %v", got)
		}
		ups := conn.all()
		jsonEqual(t, ups[len(ups)-1].Update, map[string]any{"sessionUpdate": "usage_update", "used": 700, "size": 200000})
	})

	tw(t, "unit/context-usage", "PiAcpAgent: switching the thinking level does not publish context usage", func(t *testing.T) {
		conn, proc, a := switching(t, false)
		if _, err := a.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "s1", ConfigID: "thought_level", Value: "high"}); err != nil {
			t.Fatal(err)
		}
		if proc.statsCount() != 0 || len(usageUpdates(conn)) != 0 {
			t.Errorf("stats=%d updates=%v", proc.statsCount(), usageUpdates(conn))
		}
	})
}

// model-thinking-levels.test.ts
type levelsFixture struct {
	agent *Agent
	conn  *fakeConn
	proc  *fakeProc
	state map[string]any
	calls *[]string
	sch   *scheduler
}

func newLevelsFixture(t *testing.T) *levelsFixture {
	conn := newFakeConn()
	state := map[string]any{"thinkingLevel": "max", "model": map[string]any{"provider": "test", "id": "reasoning"}}
	calls := &[]string{}
	proc := newFakeProc()
	proc.getStateFn = func() (map[string]any, error) { return state, nil }
	proc.getModelsFn = func() (map[string]any, error) {
		return models([3]string{"test", "reasoning", ""}, [3]string{"test", "plain", ""}), nil
	}
	proc.getLevelsFn = func() ([]string, error) {
		if state["model"].(map[string]any)["id"] == "plain" {
			return []string{"off"}, nil
		}
		return []string{"low", "high", "max"}, nil
	}
	proc.setThinkingFn = func(l string) error { *calls = append(*calls, l); state["thinkingLevel"] = "max"; return nil }
	proc.setModelFn = func(p, id string) error {
		*calls = append(*calls, id)
		state["model"] = map[string]any{"provider": p, "id": id}
		if id == "plain" {
			state["thinkingLevel"] = "off"
		} else {
			state["thinkingLevel"] = "max"
		}
		return nil
	}
	a, sch := testAgent(conn)
	a.sessions = &fakeSessions{session: &fakeSession{id: "s1", cwd: cwdNow(t), proc: proc}, anyID: true}
	return &levelsFixture{a, conn, proc, state, calls, sch}
}

func assertLevelUpdates(t *testing.T, conn *fakeConn, level string, levels []string) {
	t.Helper()
	ups := conn.all()
	if len(ups) != 2 {
		t.Fatalf("%d updates: %+v", len(ups), ups)
	}
	jsonEqual(t, ups[0], map[string]any{"SessionID": "s1", "Update": map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": level}})
	opts, _ := ups[1].Update["configOptions"].([]ConfigOption)
	if opts == nil {
		t.Fatalf("config_option_update = %v", ups[1].Update)
	}
	var thought *ConfigOption
	for i := range opts {
		if opts[i].ID == "thought_level" {
			thought = &opts[i]
		}
	}
	if thought == nil || thought.CurrentValue != level {
		t.Fatalf("thought_level = %+v", thought)
	}
	var vals []string
	for _, o := range thought.Options {
		vals = append(vals, o.Value)
	}
	if !reflect.DeepEqual(vals, levels) {
		t.Errorf("options = %v, want %v", vals, levels)
	}
}

func TestModelThinkingLevels(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		kind := "config"
		if legacy {
			kind = "legacy"
		}
		setLevel := func(f *levelsFixture, level string) (*SetSessionConfigOptionResponse, error) {
			if legacy {
				_, err := f.agent.SetSessionMode(SetSessionModeRequest{SessionID: "s1", ModeID: level})
				return nil, err
			}
			r, err := f.agent.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "s1", ConfigID: "thought_level", Value: level})
			return &r, err
		}
		for _, requested := range []string{"max", "xhigh", "ordinary", " mean ", " "} {
			tw(t, "unit/model-thinking-levels", "${legacy ? 'legacy' : 'config'} reasoning setter reports actual max for ${requested}", func(t *testing.T) {
				t.Run(fmt.Sprintf("%s reasoning setter reports actual max for %s", kind, requested), func(t *testing.T) {
					f := newLevelsFixture(t)
					r, err := setLevel(f, requested)
					if err != nil {
						t.Fatal(err)
					}
					if !legacy && optionValue(r.ConfigOptions, "thought_level") != "max" {
						t.Errorf("thought_level = %q", optionValue(r.ConfigOptions, "thought_level"))
					}
					if !reflect.DeepEqual(*f.calls, []string{requested}) {
						t.Errorf("calls = %q", *f.calls)
					}
					assertLevelUpdates(t, f.conn, "max", []string{"low", "high", "max"})
				})
			})
		}
		tw(t, "unit/model-thinking-levels", "${legacy ? 'legacy' : 'config'} reasoning setter reports an opaque applied level", func(t *testing.T) {
			t.Run(fmt.Sprintf("%s reasoning setter reports an opaque applied level", kind), func(t *testing.T) {
				f := newLevelsFixture(t)
				f.proc.getLevelsFn = func() ([]string, error) { return []string{"ordinary", "mean"}, nil }
				f.proc.setThinkingFn = func(l string) error { *f.calls = append(*f.calls, l); f.state["thinkingLevel"] = "mean"; return nil }
				r, err := setLevel(f, "ordinary")
				if err != nil {
					t.Fatal(err)
				}
				if !legacy && optionValue(r.ConfigOptions, "thought_level") != "mean" {
					t.Errorf("thought_level = %q", optionValue(r.ConfigOptions, "thought_level"))
				}
				if !reflect.DeepEqual(*f.calls, []string{"ordinary"}) {
					t.Errorf("calls = %q", *f.calls)
				}
				assertLevelUpdates(t, f.conn, "mean", []string{"ordinary", "mean"})
			})
		})
		tw(t, "unit/model-thinking-levels", "${legacy ? 'legacy' : 'config'} model setter refreshes model-specific levels and current mode", func(t *testing.T) {
			t.Run(fmt.Sprintf("%s model setter refreshes model-specific levels and current mode", kind), func(t *testing.T) {
				f := newLevelsFixture(t)
				for _, model := range []string{"plain", "reasoning"} {
					f.conn.reset()
					if legacy {
						if err := f.agent.UnstableSetSessionModel(SetSessionModelRequest{SessionID: "s1", ModelID: "test/" + model}); err != nil {
							t.Fatal(err)
						}
					} else if _, err := f.agent.SetSessionConfigOption(SetSessionConfigOptionRequest{SessionID: "s1", ConfigID: "model", Value: "test/" + model}); err != nil {
						t.Fatal(err)
					}
					level, levels := "max", []string{"low", "high", "max"}
					if model == "plain" {
						level, levels = "off", []string{"off"}
					}
					// The model setter also publishes usage; only the two config updates are compared.
					ups := f.conn.all()
					f.conn.reset()
					for _, u := range ups {
						if u.Update["sessionUpdate"] != "usage_update" {
							_ = f.conn.SessionUpdate(u.SessionID, u.Update)
						}
					}
					assertLevelUpdates(t, f.conn, level, levels)
				}
			})
		})
		for _, failure := range []string{"state", "discovery", "empty", "non-string", "inconsistent"} {
			tw(t, "unit/model-thinking-levels", "${legacy ? 'legacy' : 'config'} reasoning setter emits no success on ${failure} read failure", func(t *testing.T) {
				t.Run(fmt.Sprintf("%s reasoning setter emits no success on %s read failure", kind, failure), func(t *testing.T) {
					f := newLevelsFixture(t)
					switch failure {
					case "state":
						f.proc.getStateFn = func() (map[string]any, error) { return nil, errors.New("state failed") }
					case "discovery":
						f.proc.getLevelsFn = func() ([]string, error) { return nil, errors.New("discovery failed") }
					default:
						f.proc.setThinkingFn = func(string) error {
							switch failure {
							case "empty":
								f.state["thinkingLevel"] = ""
							case "non-string":
								f.state["thinkingLevel"] = 1
							default:
								f.state["thinkingLevel"] = "medium"
							}
							return nil
						}
					}
					if _, err := setLevel(f, "max"); err == nil {
						t.Fatal("expected an error")
					}
					if n := len(f.conn.all()); n != 0 {
						t.Errorf("%d updates after a failure", n)
					}
				})
			})
		}
	}

	tw(t, "unit/model-thinking-levels", "invalid configuration and legacy mode requests do not mutate Pi", func(t *testing.T) {
		f := newLevelsFixture(t)
		for _, req := range []SetSessionConfigOptionRequest{
			{SessionID: "s1", ConfigID: "thought_level", Value: ""},
			{SessionID: "s1", ConfigID: "unknown", Value: "high"},
			{SessionID: "s1", ConfigID: "thought_level", Value: 1},
		} {
			_, err := f.agent.SetSessionConfigOption(req)
			if re, ok := err.(*RequestError); !ok || re.Code != -32602 {
				t.Errorf("%+v: err = %v", req, err)
			}
		}
		for _, mode := range []any{"", 1, nil} {
			_, err := f.agent.SetSessionMode(SetSessionModeRequest{SessionID: "s1", ModeID: mode})
			if re, ok := err.(*RequestError); !ok || re.Code != -32602 {
				t.Errorf("mode %v: err = %v", mode, err)
			}
		}
		if len(*f.calls) != 0 || len(f.conn.all()) != 0 {
			t.Errorf("calls=%v updates=%v", *f.calls, f.conn.all())
		}
	})

	for _, load := range []bool{false, true} {
		for _, levels := range [][]string{{"off"}, {"low", "high", "max"}, {"ordinary", "mean"}} {
			name := "new"
			if load {
				name = "load"
			}
			tw(t, "unit/model-thinking-levels", "${load ? 'load' : 'new'} session advertises exact ${levels} levels", func(t *testing.T) {
				t.Run(fmt.Sprintf("%s session advertises exact %v levels", name, levels), func(t *testing.T) {
					f := newLevelsFixture(t)
					f.state["thinkingLevel"] = levels[len(levels)-1]
					f.proc.getLevelsFn = func() ([]string, error) { return levels, nil }
					f.agent.spawn = func(SpawnParams) (Proc, error) { return f.proc, nil }
					f.agent.store = newMemStore(StoredSession{SessionID: "s1", Cwd: cwdNow(t), SessionFile: "/tmp/thinking-test.jsonl"})
					f.agent.sessions = NewSessionManager(f.agent.spawn, f.agent.store)
					var modes ModeState
					var options []ConfigOption
					if load {
						r, err := f.agent.LoadSession(LoadSessionRequest{SessionID: "s1", Cwd: cwdNow(t), McpServers: []any{}})
						if err != nil {
							t.Fatal(err)
						}
						modes, options = r.Modes, r.ConfigOptions
					} else {
						r, err := f.agent.NewSession(newSessionReq(cwdNow(t)))
						if err != nil {
							t.Fatal(err)
						}
						modes, options = r.Modes, r.ConfigOptions
					}
					want := levels[len(levels)-1]
					if modes.CurrentModeID != want {
						t.Errorf("currentModeId = %q", modes.CurrentModeID)
					}
					var ids []string
					for _, m := range modes.AvailableModes {
						ids = append(ids, m.ID)
					}
					if !reflect.DeepEqual(ids, levels) {
						t.Errorf("modes = %v", ids)
					}
					var vals []string
					for _, o := range options {
						if o.ID == "thought_level" {
							if o.CurrentValue != want {
								t.Errorf("currentValue = %q", o.CurrentValue)
							}
							for _, x := range o.Options {
								vals = append(vals, x.Value)
							}
						}
					}
					if !reflect.DeepEqual(vals, levels) {
						t.Errorf("options = %v", vals)
					}
				})
			})
		}
	}
}
