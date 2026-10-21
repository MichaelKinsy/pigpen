package pi_goal_x

import (
	"reflect"
	"testing"
)

// canonJS turns ordered objects into plain maps (undefined keys dropped) so deepEqual ignores key order, like assert.deepEqual.
func canonJS(v any) any {
	switch x := v.(type) {
	case *jsObject:
		if x == nil {
			return nil
		}
		m := map[string]any{}
		for _, k := range x.order() {
			val := x.vals[k]
			if _, un := val.(undef); un {
				continue
			}
			m[k] = canonJS(val)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canonJS(e)
		}
		return out
	}
	return v
}

func deepEqualJS(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflect.DeepEqual(canonJS(got), canonJS(want)) {
		t.Errorf("%s: got %s, want %s", what, marshalJSON(canonJS2(got), ""), marshalJSON(canonJS2(want), ""))
	}
}

func canonJS2(v any) any { return v }

type fakeAuditorRegistry struct {
	rt        any
	findFn    func(provider, id string) *auditorModel
	available []*auditorModel
}

func (r *fakeAuditorRegistry) find(provider, id string) *auditorModel {
	if r.findFn == nil {
		return nil
	}
	return r.findFn(provider, id)
}
func (r *fakeAuditorRegistry) getAvailable() []*auditorModel { return r.available }
func (r *fakeAuditorRegistry) runtime() any                  { return r.rt }

type fakeAuditorCtx struct {
	dir string
	mdl *auditorModel
	reg auditorModelRegistry
}

func (c *fakeAuditorCtx) cwd() string                         { return c.dir }
func (c *fakeAuditorCtx) model() *auditorModel                { return c.mdl }
func (c *fakeAuditorCtx) modelRegistry() auditorModelRegistry { return c.reg }

// fakeAuditorSession is the scripted nested session the original tests build as object literals.
type fakeAuditorSession struct {
	onAbort   func()
	onPrompt  func(text string) error
	onDispose func()
	listener  func(auditorSessionEvent)
}

func (s *fakeAuditorSession) subscribe(l func(auditorSessionEvent)) func() {
	s.listener = l
	return func() {}
}
func (s *fakeAuditorSession) prompt(text string) error {
	if s.onPrompt != nil {
		return s.onPrompt(text)
	}
	return nil
}
func (s *fakeAuditorSession) abort() {
	if s.onAbort != nil {
		s.onAbort()
	}
}
func (s *fakeAuditorSession) dispose() {
	if s.onDispose != nil {
		s.onDispose()
	}
}

func auditorGoal(t *testing.T, objective string) *jsObject {
	t.Helper()
	g := createGoal(objective, true, false, false, ms(2026, 5, 12, 0, 0, 0))
	g.set("id", "g1")
	return g
}

func auditorSessionOf(s auditorSession) auditorSessionFactory {
	return auditorSessionFactoryFunc(func(auditorSessionOptions) (auditorSession, error) { return s, nil })
}
