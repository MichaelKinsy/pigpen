package websearch

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// Helpers of the model-backed provider twins (openai, xai, kimi, mistral): the upstream tests hand
// the provider a `ctx` object whose modelRegistry has getAll, find and getApiKeyAndHeaders.

// fakeRegistry is that modelRegistry. auth answers getApiKeyAndHeaders; selected records the
// "provider/id" of every model it was asked about.
type fakeRegistry struct {
	mu       sync.Mutex
	models   []map[string]any
	auth     func(model map[string]any) (map[string]any, error)
	selected []string
	getAll   error
}

func (r *fakeRegistry) GetAll() ([]map[string]any, error) {
	if r.getAll != nil {
		return nil, r.getAll
	}
	return r.models, nil
}

func (r *fakeRegistry) Find(provider, id string) map[string]any {
	for _, m := range r.models {
		if m["provider"] == provider && m["id"] == id {
			return m
		}
	}
	return nil
}

func (r *fakeRegistry) GetApiKeyAndHeaders(model map[string]any) (map[string]any, error) {
	r.mu.Lock()
	r.selected = append(r.selected, jsString(model["provider"])+"/"+jsString(model["id"]))
	r.mu.Unlock()
	return r.auth(model)
}

// fakeExt is the ExtensionContext: the selected model, the registry and the session id.
type fakeExt struct {
	model   map[string]any
	reg     *fakeRegistry
	session string
}

func (e *fakeExt) Model() map[string]any        { return e.model }
func (e *fakeExt) ModelRegistry() ModelRegistry { return e.reg }
func (e *fakeExt) SessionID() string            { return e.session }

// withExt attaches an extension context to a call.
func withExt(ec ExtensionContext) context.Context { return WithExtensionContext(bg(), ec) }

func authOK(apiKey string, headers map[string]any) map[string]any {
	m := map[string]any{"ok": true}
	if apiKey != "" {
		m["apiKey"] = apiKey
	}
	if headers != nil {
		m["headers"] = headers
	}
	return m
}

func staticAuth(m map[string]any) func(map[string]any) (map[string]any, error) {
	return func(map[string]any) (map[string]any, error) { return m, nil }
}

func model(provider, id string) map[string]any { return map[string]any{"provider": provider, "id": id} }

var errNoNetwork = errors.New("must not reach the network")

// noNet installs a transport that fails the test when it is reached.
func noNet(t *testing.T) *fakeNet {
	t.Helper()
	return useNet(t, func(c netCall) netReply {
		t.Errorf("unexpected request to %s", c.URL)
		return netReply{Err: errNoNetwork}
	})
}

func mustJSON(v any) []byte {
	b, err := encodeJSONBody(v)
	if err != nil {
		panic(err)
	}
	return b
}
