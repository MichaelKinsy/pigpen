package pitypesafe

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	ccKey     = "cc_fake_key_0123456789abcdef"
	gwKey     = "gw_fake_key_0123456789abcdef"
	tsKey     = "ts_fake_key_0123456789abcdef"
	storedKey = "st_fake_key_0123456789abcdef"
)

func gatewayEndpoint() BackendEndpoint {
	return BackendEndpoint{BackendConfig: BackendConfig{Label: "Gateway", Host: "https://gw.example.com", Path: "/jev/v1/systemone", KeyEnv: "GATEWAY_JEV_KEY"}, DefaultModel: "jev-latest"}
}

// endpointMap is a caller-supplied endpoint as a settings file would hold it, so a value of the wrong type is expressible.
func endpointMap(overrides map[string]any) map[string]any {
	m := map[string]any{"label": "Gateway", "host": "https://gw.example.com", "keyEnv": "GATEWAY_JEV_KEY", "defaultModel": "jev-latest"}
	for k, v := range overrides {
		if v == deleted {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

const deleted = "\x00delete"

type refusalOptions struct{ apiKey, model string }

func assertRefuses(t *testing.T, backend any, message string, o refusalOptions) {
	t.Helper()
	var calls atomic.Int32
	_, err := New(Options{Backend: backend, APIKey: o.apiKey, Model: o.model, HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return rawResponse(200, "{}"), nil
	})})
	if !hasCode(err, CodeConfiguration) || err.Error() != message || strings.Contains(err.Error(), "user:pw") {
		t.Errorf("backend %v: err = %v, want configuration %q", backend, err, message)
	}
	if calls.Load() != 0 {
		t.Errorf("a refused backend sent %d requests", calls.Load())
	}
}

func TestBackends(t *testing.T) {
	tw(t, "backends", "a commandcode request goes to the Command Code path with its own key and model", func(t *testing.T) {
		isolate(t)
		t.Setenv("COMMANDCODE_API_KEY", ccKey)
		calls := 0
		client, err := New(Options{Backend: "commandcode", HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() != "https://api.commandcode.ai/provider/v1/systemone" || r.Header.Get("Authorization") != "Bearer "+ccKey {
				t.Errorf("url=%s auth=%s", r.URL, r.Header.Get("Authorization"))
			}
			body := requestBody(t, r)
			if !strings.Contains(string(body), `"model":"typesafe/jev"`) {
				t.Errorf("body = %s", body)
			}
			return responseFor(body), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || calls != 1 {
			t.Fatalf("err = %v calls=%d", err, calls)
		}
	})
	tw(t, "backends", "a per-request model on commandcode is sent unchanged", func(t *testing.T) {
		isolate(t)
		t.Setenv("COMMANDCODE_API_KEY", ccKey)
		client, _ := New(Options{Backend: "commandcode", HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			body := requestBody(t, r)
			if !strings.Contains(string(body), `"model":"jev-2.0"`) {
				t.Errorf("body = %s", body)
			}
			return responseFor(body), nil
		})})
		req := sampleRequest()
		req.Model = "jev-2.0"
		if _, err := client.Evaluate(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	})
	tw(t, "backends", "a caller-supplied endpoint sends to its own path with its own key and unmapped model", func(t *testing.T) {
		isolate(t)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		calls := 0
		client, err := New(Options{Backend: gatewayEndpoint(), HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			body := requestBody(t, r)
			if r.URL.String() != "https://gw.example.com/jev/v1/systemone" || r.Header.Get("Authorization") != "Bearer "+gwKey || !strings.Contains(string(body), `"model":"jev-latest"`) {
				t.Errorf("url=%s auth=%s body=%s", r.URL, r.Header.Get("Authorization"), body)
			}
			return responseFor(body), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || calls != 1 {
			t.Fatalf("err = %v calls=%d", err, calls)
		}
	})
	tw(t, "backends", "an endpoint without a path uses the SDK's own /v1/systemone", func(t *testing.T) {
		isolate(t)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		endpoint := gatewayEndpoint()
		endpoint.Path = ""
		var url string
		client, _ := New(Options{Backend: endpoint, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			url = r.URL.String()
			return responseFor(requestBody(t, r)), nil
		})})
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || url != "https://gw.example.com/v1/systemone" {
			t.Fatalf("err = %v url=%s", err, url)
		}
	})
	tw(t, "backends", "an endpoint may use a loopback http: host", func(t *testing.T) {
		isolate(t)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		endpoint := BackendEndpoint{BackendConfig: BackendConfig{Label: "Local", Host: "http://127.0.0.1:8787", KeyEnv: "GATEWAY_JEV_KEY"}, DefaultModel: "jev-latest"}
		var url string
		client, err := New(Options{Backend: endpoint, HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			url = r.URL.String()
			return responseFor(requestBody(t, r)), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || url != "http://127.0.0.1:8787/v1/systemone" {
			t.Fatalf("err = %v url=%s", err, url)
		}
	})
	tw(t, "backends", "an endpoint label must be a nonempty string of at most 60 characters", func(t *testing.T) {
		for _, label := range []any{7, "", "   ", strings.Repeat("x", 61)} {
			assertRefuses(t, endpointMap(map[string]any{"label": label}), "Backend label must be a nonempty string of at most 60 characters.", refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint host must be an absolute https: URL with no user info, path, query, or fragment", func(t *testing.T) {
		for _, host := range []string{"http://gw.example.com", "https://user:pw@gw.example.com", "https://gw.example.com/prefix", "https://gw.example.com?x=1", "https://gw.example.com#f", "ftp://gw.example.com", "not a url"} {
			assertRefuses(t, endpointMap(map[string]any{"host": host}), hostMessage, refusalOptions{})
		}
	})
	tw(t, "backends", `an endpoint path must be a string that starts with "/"`, func(t *testing.T) {
		for _, path := range []any{"jev", "x?q", "x#f", 7} {
			assertRefuses(t, endpointMap(map[string]any{"path": path}), `Backend path must be a string that starts with "/".`, refusalOptions{})
		}
	})
	tw(t, "backends", `an endpoint modelsPath must be a string that starts with "/"`, func(t *testing.T) {
		for _, p := range []any{"models", "x?q", 7} {
			assertRefuses(t, endpointMap(map[string]any{"modelsPath": p}), `Backend modelsPath must be a string that starts with "/".`, refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint modelsField must be a nonempty string", func(t *testing.T) {
		for _, v := range []any{"", 7} {
			assertRefuses(t, endpointMap(map[string]any{"modelsField": v}), "Backend modelsField must be a nonempty string.", refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint modelsIdField must be a nonempty string", func(t *testing.T) {
		for _, v := range []any{"", 7} {
			assertRefuses(t, endpointMap(map[string]any{"modelsIdField": v}), "Backend modelsIdField must be a nonempty string.", refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint modelsVerifyKey must be a boolean", func(t *testing.T) {
		for _, v := range []any{"yes", 1} {
			assertRefuses(t, endpointMap(map[string]any{"modelsVerifyKey": v}), "Backend modelsVerifyKey must be a boolean.", refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint keyEnv must name an environment variable", func(t *testing.T) {
		for _, v := range []string{"1BAD", "GATEWAY KEY", ""} {
			assertRefuses(t, endpointMap(map[string]any{"keyEnv": v, "path": deleted}), keyEnvMessage, refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint keyEnv must not be TYPESAFE_API_KEY", func(t *testing.T) {
		message := "Backend keyEnv must not be TYPESAFE_API_KEY: the TypeSafe key is only sent to the typesafe backend. Give this endpoint its own variable."
		for _, v := range []string{"TYPESAFE_API_KEY", "typesafe_api_key"} {
			assertRefuses(t, endpointMap(map[string]any{"keyEnv": v}), message, refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint defaultModel must be a nonempty string of at most 100 characters", func(t *testing.T) {
		for _, v := range []any{"", "   ", strings.Repeat("x", 101), 7} {
			assertRefuses(t, endpointMap(map[string]any{"defaultModel": v}), "Backend defaultModel must be a nonempty string of at most 100 characters.", refusalOptions{})
		}
	})
	tw(t, "backends", "an endpoint with no defaultModel needs model on createTypeSafe", func(t *testing.T) {
		assertRefuses(t, endpointMap(map[string]any{"defaultModel": deleted}), `Backend "Gateway" names no defaultModel; pass Model to New.`, refusalOptions{apiKey: "fake_key_0123456789abcdef"})
	})
	tw(t, "backends", "backend must be a registry name or a backend object", func(t *testing.T) {
		// nil is the default backend in Go, so the original's null case has no equivalent; 42 and true remain.
		for _, backend := range []any{42, true} {
			assertRefuses(t, backend, "backend must be a registry name or a backend object.", refusalOptions{})
		}
		// An unknown name keeps the registry's own error. It lists the own-model backend this port adds.
		assertRefuses(t, "unknown-backend", `Unknown judgment backend "unknown-backend". Valid backends: typesafe, openrouter, commandcode, ownmodel.`, refusalOptions{})
	})
	tw(t, "backends", "authState refuses an invalid backend instead of reporting a status", func(t *testing.T) {
		isolate(t)
		_, err := GetAuthState(AuthOptions{Backend: map[string]any{"label": "x", "host": "http://evil.example", "keyEnv": "K"}})
		if !hasCode(err, CodeConfiguration) || err.Error() != hostMessage {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "backends", "an endpoint never reads TYPESAFE_API_KEY or the login store", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_API_KEY", tsKey)
		_, _ = StoreAPIKey(storedKey)
		endpoint := gatewayEndpoint()
		endpoint.Path = ""
		var calls atomic.Int32
		_, err := New(Options{Backend: endpoint, HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) { calls.Add(1); return rawResponse(200, "{}"), nil })})
		if !hasCode(err, CodeConfiguration) || err.Error() != "No API key. Set GATEWAY_JEV_KEY in the environment." || calls.Load() != 0 {
			t.Fatalf("err = %v", err)
		}
		if s, _ := KeySituationFor(gatewayEndpoint()); s.Kind != KeyMissing {
			t.Errorf("situation = %+v", s)
		}
		if mustAuth(t, gatewayEndpoint()).Usable {
			t.Error("an endpoint without its own key is unusable")
		}
	})
	tw(t, "backends", "commandcode with no COMMANDCODE_API_KEY is missing and unusable", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_API_KEY", tsKey)
		_, _ = StoreAPIKey(storedKey)
		_, err := New(Options{Backend: "commandcode", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) { t.Error("no request may be sent"); return nil, nil })})
		if !hasCode(err, CodeConfiguration) || err.Error() != "No API key. Set COMMANDCODE_API_KEY in the environment." {
			t.Fatalf("err = %v", err)
		}
		if s, _ := KeySituationFor("commandcode"); s.Kind != KeyMissing || mustAuth(t, "commandcode").Usable {
			t.Errorf("situation = %+v", s)
		}
	})
	tw(t, "backends", "a request to an endpoint carries its own key, never the TypeSafe key", func(t *testing.T) {
		isolate(t)
		t.Setenv("TYPESAFE_API_KEY", tsKey)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		_, _ = StoreAPIKey(storedKey)
		calls := 0
		client, err := New(Options{Backend: gatewayEndpoint(), HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			for name, values := range r.Header {
				for _, v := range values {
					if strings.Contains(v, tsKey) || strings.Contains(v, storedKey) {
						t.Errorf("header %s carries another backend's key", name)
					}
				}
			}
			if r.Header.Get("Authorization") != "Bearer "+gwKey {
				t.Errorf("authorization = %s", r.Header.Get("Authorization"))
			}
			return responseFor(requestBody(t, r)), nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil || calls != 1 {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "backends", "a public model list on commandcode leaves the auth state unverified", func(t *testing.T) {
		isolate(t)
		t.Setenv("COMMANDCODE_API_KEY", ccKey)
		client, _ := New(Options{Backend: "commandcode", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"data": []any{map[string]any{"id": "typesafe/jev"}}}, nil), nil
		})})
		names, err := client.ListModels(context.Background())
		if err != nil || len(names) != 1 || names[0] != "typesafe/jev" {
			t.Fatalf("models = %v, %v", names, err)
		}
		if s := mustAuth(t, "commandcode"); s.Verified || s.VerifiedAt != "" {
			t.Fatalf("state = %+v", s)
		}
	})
	tw(t, "backends", "a model list on an endpoint without modelsVerifyKey leaves the auth state unverified", func(t *testing.T) {
		isolate(t)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		endpoint := gatewayEndpoint()
		endpoint.Path = ""
		client, _ := New(Options{Backend: endpoint, HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"models": []any{map[string]any{"name": "jev-latest"}}}, nil), nil
		})})
		names, err := client.ListModels(context.Background())
		if err != nil || len(names) != 1 || names[0] != "jev-latest" {
			t.Fatalf("models = %v, %v", names, err)
		}
		if s := mustAuth(t, gatewayEndpoint()); s.Verified || s.VerifiedAt != "" {
			t.Fatalf("state = %+v", s)
		}
	})
	tw(t, "backends", "an endpoint with modelsVerifyKey true records verification", func(t *testing.T) {
		isolate(t)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		endpoint := BackendEndpoint{BackendConfig: BackendConfig{Label: "Gateway", Host: "https://gw.example.com", KeyEnv: "GATEWAY_JEV_KEY", ModelsVerifyKey: boolPtr(true)}, DefaultModel: "jev-latest"}
		client, _ := New(Options{Backend: endpoint, HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"models": []any{map[string]any{"name": "jev-latest"}}}, nil), nil
		})})
		if _, err := client.ListModels(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s := mustAuth(t, endpoint); !s.Verified || s.VerifiedAt == "" {
			t.Fatalf("state = %+v", s)
		}
	})
	tw(t, "backends", "a successful evaluate records verification even after a public model list", func(t *testing.T) {
		isolate(t)
		t.Setenv("COMMANDCODE_API_KEY", ccKey)
		client, _ := New(Options{Backend: "commandcode", HTTPClient: doerFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/models") {
				return jsonResponse(200, map[string]any{"data": []any{map[string]any{"id": "typesafe/jev"}}}, nil), nil
			}
			return responseFor(requestBody(t, r)), nil
		})})
		_, _ = client.ListModels(context.Background())
		if mustAuth(t, "commandcode").Verified {
			t.Fatal("a public list must not verify")
		}
		if _, err := client.Evaluate(context.Background(), sampleRequest()); err != nil {
			t.Fatal(err)
		}
		if !mustAuth(t, "commandcode").Verified {
			t.Fatal("a successful evaluate must verify")
		}
	})
	tw(t, "backends", "a malformed reply on commandcode is a response error", func(t *testing.T) {
		isolate(t)
		t.Setenv("COMMANDCODE_API_KEY", ccKey)
		client, _ := New(Options{Backend: "commandcode", HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"model": "jev-test", "answers": map[string]any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}, nil), nil
		})})
		if _, err := client.Evaluate(context.Background(), sampleRequest()); !hasCode(err, CodeResponse) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "backends", "a malformed reply on an endpoint is a response error", func(t *testing.T) {
		isolate(t)
		t.Setenv("GATEWAY_JEV_KEY", gwKey)
		client, _ := New(Options{Backend: gatewayEndpoint(), HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, map[string]any{"model": "jev-test", "answers": map[string]any{"yes": map[string]any{"type": "noul", "noul": 2}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}, nil), nil
		})})
		if _, err := client.Evaluate(context.Background(), sampleRequest()); !hasCode(err, CodeResponse) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "backends", "401 advice names each backend's own key variable", func(t *testing.T) {
		if got := SafeError(apiError(401), "commandcode").Message; got != "TypeSafe returned HTTP 401. Check COMMANDCODE_API_KEY. No automatic retry was made." {
			t.Errorf("commandcode: %s", got)
		}
		if got := SafeError(apiError(401), gatewayEndpoint()).Message; got != "TypeSafe returned HTTP 401. Check GATEWAY_JEV_KEY. No automatic retry was made." {
			t.Errorf("gateway: %s", got)
		}
	})
	tw(t, "backends", "resolveBackend resolves registry names to the registry entries", func(t *testing.T) {
		ts, _ := ResolveBackend("typesafe")
		if ts.Name != "typesafe" || ts.Label != "TypeSafe" || ts.Host != "https://api.typesafe.ai" || ts.KeyEnv != "TYPESAFE_API_KEY" || ts.DefaultModel != "jev-latest" || !ts.ModelsVerifyKey || ts.Path != "" {
			t.Errorf("typesafe = %+v", ts)
		}
		cc, _ := ResolveBackend("commandcode")
		if cc.Name != "commandcode" || cc.Label != "Command Code" || cc.Host != "https://api.commandcode.ai" || cc.KeyEnv != "COMMANDCODE_API_KEY" || cc.Path != "/provider/v1/systemone" ||
			cc.ModelsPath != "/provider/v1/models" || cc.ModelsField != "data" || cc.ModelsIDField != "id" || cc.DefaultModel != "typesafe/jev" || cc.ModelsVerifyKey {
			t.Errorf("commandcode = %+v", cc)
		}
		if or, _ := ResolveBackend("openrouter"); or.DefaultModel != "typesafe/jev-1.13" {
			t.Errorf("openrouter = %+v", or)
		}
	})
	tw(t, "backends", "backendHost reports the destination host", func(t *testing.T) {
		cases := map[string]any{
			"api.commandcode.ai": "commandcode", "api.typesafe.ai": "typesafe", "openrouter.ai": "openrouter", "gw.example.com": gatewayEndpoint(),
			"gw.example.com:8443": endpointMap(map[string]any{"host": "https://gw.example.com:8443"}),
			"127.0.0.1:8787":      endpointMap(map[string]any{"host": "http://127.0.0.1:8787"}),
		}
		for want, backend := range cases {
			if got, err := BackendHost(backend); err != nil || got != want {
				t.Errorf("BackendHost(%v) = %q, %v; want %q", backend, got, err, want)
			}
		}
	})
}

func TestOwnModelBackendResolves(t *testing.T) {
	b, err := ResolveBackend("ownmodel")
	if err != nil || !b.Local || b.Host != "" || b.Label == "" {
		t.Fatalf("ownmodel = %+v, %v", b, err)
	}
	if host, err := BackendHost("ownmodel"); err != nil || host != "" {
		t.Errorf("host = %q, %v", host, err)
	}
	isolate(t)
	s, err := GetAuthState(AuthOptions{Backend: "ownmodel"})
	if err != nil || s.Kind != KeyNotRequired || !s.Usable {
		t.Fatalf("auth = %+v, %v", s, err)
	}
	if r, _ := DescribeAuth(s); r.Level != LevelOK || !strings.Contains(r.Text, "model PiG is configured with") {
		t.Errorf("report = %+v", r)
	}
}
