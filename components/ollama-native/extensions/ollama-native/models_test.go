package ollamanative

import (
	"context"
	"encoding/json"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeHost(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "http://127.0.0.1:11434",
		"   ":                        "http://127.0.0.1:11434",
		"http://127.0.0.1:11434":     "http://127.0.0.1:11434",
		"http://box:8080/":           "http://box:8080",
		"box":                        "http://box:11434",
		"box:8080":                   "http://box:8080",
		":9000":                      "http://127.0.0.1:9000",
		"https://ollama.example.com": "https://ollama.example.com:443",
		"https://o.example.com:8443": "https://o.example.com:8443",
		"http://box":                 "http://box:80",
		"[::1]:11434":                "http://[::1]:11434",
	} {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// Registering the provider must not touch the network: pig starts without Ollama
// running, and a model list is only fetched when the host asks for a refresh.
func TestConstructionAndGetModelsMakeNoRequests(t *testing.T) {
	f := newFake(t)
	t.Setenv("OLLAMA_HOST", f.URL)
	_ = Extension()
	p := newProvider(newClient(f.URL))
	models, err := p.GetModels()
	if err != nil || len(models) != 0 {
		t.Fatalf("GetModels before any refresh = %v, %v; want empty", models, err)
	}
	if f.count() != 0 {
		t.Fatalf("requests at startup: %v", f.requests)
	}
}

func TestDiscoverReadsModelsFromTagsAndShow(t *testing.T) {
	f := newFake(t)
	models, err := newClient(f.URL).discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// nomic-embed-text only embeds: it cannot chat, so it is not offered.
	if len(models) != 2 {
		t.Fatalf("models = %v", models)
	}
	g, q := models[0], models[1]
	if g["id"] != "granite4.1:3b" || g["api"] != "ollama-native" || g["contextWindow"] != 131072 || g["reasoning"] != false {
		t.Errorf("granite = %v", g)
	}
	if in := g["input"].([]string); len(in) != 1 || in[0] != "text" {
		t.Errorf("granite input = %v", in)
	}
	if q["id"] != "qwen3:8b" || q["contextWindow"] != 40960 || q["reasoning"] != true {
		t.Errorf("qwen3 = %v", q)
	}
	if in := q["input"].([]string); len(in) != 2 || in[1] != "image" {
		t.Errorf("qwen3 input = %v", in)
	}
}

// /api/show is best effort: a model it cannot describe still gets listed, with
// plain defaults, so one bad model does not hide the rest.
func TestDiscoverFallsBackToDefaultsWhenShowFails(t *testing.T) {
	f := newFake(t)
	f.tags = "tags_empty.json"
	f.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"mystery:1b"}]}`))
			return
		}
		http.Error(w, `{"error":"boom"}`, 500)
	})
	models, err := newClient(f.URL).discover(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("models = %v, err = %v", models, err)
	}
	m := models[0]
	if m["id"] != "mystery:1b" || m["contextWindow"] != defaultContextWindow || m["reasoning"] != false {
		t.Errorf("defaults = %v", m)
	}
}

func TestDiscoverEmptyListIsNotAnError(t *testing.T) {
	f := newFake(t)
	f.tags = "tags_empty.json"
	models, err := newClient(f.URL).discover(context.Background())
	if err != nil || len(models) != 0 {
		t.Fatalf("models = %v, err = %v", models, err)
	}
}

func TestDiscoverReportsOllamaNotRunning(t *testing.T) {
	f := newFake(t)
	url := f.URL
	f.Close()
	_, err := newClient(url).discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not reachable at "+url) || !strings.Contains(err.Error(), "OLLAMA_HOST") {
		t.Fatalf("err = %v", err)
	}
}

func TestRefreshModelsPublishesAndSwapsTheCatalog(t *testing.T) {
	f := newFake(t)
	p := newProvider(newClient(f.URL))

	// Offline: no request, catalog untouched.
	if err := p.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: false, Signal: context.Background(), Publish: func(sdk.ModelsPublication) (bool, error) { t.Fatal("published offline"); return false, nil }}); err != nil {
		t.Fatal(err)
	}
	if f.count() != 0 {
		t.Fatalf("offline refresh made requests: %v", f.requests)
	}

	published := 0
	err := p.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: true, Signal: context.Background(), Publish: func(pub sdk.ModelsPublication) (bool, error) {
		published++
		// The catalog is stored for the next start, which must not need Ollama.
		var entry struct {
			Models    []map[string]any `json:"models"`
			CheckedAt float64          `json:"checkedAt"`
		}
		if err := json.Unmarshal(pub.Persist, &entry); err != nil || len(entry.Models) != 2 || entry.CheckedAt == 0 {
			t.Errorf("persist = %s (%v)", pub.Persist, err)
		}
		if before, _ := p.GetModels(); len(before) != 0 {
			t.Error("catalog changed before the host applied the update")
		}
		return true, pub.Update()
	}})
	if err != nil || published != 1 {
		t.Fatalf("refresh err = %v, published = %d", err, published)
	}
	models, _ := p.GetModels()
	if len(models) != 2 || models[0]["id"] != "granite4.1:3b" {
		t.Fatalf("models after refresh = %v", models)
	}
}

// A failed refresh keeps the last good catalog and reports the reason.
func TestRefreshModelsKeepsLastCatalogWhenOllamaStops(t *testing.T) {
	f := newFake(t)
	c := newClient(f.URL)
	p := newProvider(c)
	publish := func(pub sdk.ModelsPublication) (bool, error) { return true, pub.Update() }
	if err := p.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: true, Signal: context.Background(), Publish: publish}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	err := p.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: true, Signal: context.Background(), Publish: publish})
	if err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("err = %v", err)
	}
	if models, _ := p.GetModels(); len(models) != 2 {
		t.Fatalf("catalog dropped: %v", models)
	}
}

// PiG calls RefreshModels once without the network at start, handing back what
// was stored. That is how `pig -p --model ollama-native/x` finds its model with
// Ollama not even asked.
func TestOfflinePassRestoresTheStoredCatalogWithoutRequests(t *testing.T) {
	f := newFake(t)
	// A first process refreshes and persists.
	var stored json.RawMessage
	first := newProvider(newClient(f.URL))
	err := first.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: true, Signal: context.Background(), Publish: func(pub sdk.ModelsPublication) (bool, error) {
		stored = pub.Persist
		return true, pub.Update()
	}})
	if err != nil {
		t.Fatal(err)
	}
	before := f.count()

	// A second process starts: PiG hands the stored entry back, as decoded JSON.
	var entry map[string]any
	if err := json.Unmarshal(stored, &entry); err != nil {
		t.Fatal(err)
	}
	second := newProvider(newClient(f.URL))
	applied := 0
	err = second.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: false, Stored: entry, Signal: context.Background(), Publish: func(pub sdk.ModelsPublication) (bool, error) {
		applied++
		if pub.Persist != nil {
			t.Error("the offline pass must not rewrite the store")
		}
		return true, pub.Update()
	}})
	if err != nil || applied != 1 {
		t.Fatalf("err = %v, applied = %d", err, applied)
	}
	if f.count() != before {
		t.Fatalf("the offline pass made requests: %v", f.requests[before:])
	}
	models, _ := second.GetModels()
	if len(models) != 2 || models[1]["id"] != "qwen3:8b" || models[1]["api"] != "ollama-native" {
		t.Fatalf("restored = %v", models)
	}
}

func TestOfflinePassIgnoresAMalformedStore(t *testing.T) {
	p := newProvider(newClient("http://127.0.0.1:1"))
	err := p.RefreshModels(sdk.RefreshModelsContext{AllowNetwork: false, Stored: map[string]any{"models": []any{"junk", map[string]any{"name": "no id"}, map[string]any{"id": "ok:1b", "api": "ollama-native"}}},
		Publish: func(pub sdk.ModelsPublication) (bool, error) { return true, pub.Update() }})
	if err != nil {
		t.Fatal(err)
	}
	if models, _ := p.GetModels(); len(models) != 1 || models[0]["id"] != "ok:1b" {
		t.Fatalf("models = %v", models)
	}
}
