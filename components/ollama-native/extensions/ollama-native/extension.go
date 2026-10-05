// Package ollamanative adds Ollama as a model provider for PiG, through Ollama's
// native /api/chat endpoint.
//
// It uses the native API instead of the OpenAI-compatible /v1 one so that tool
// calls, thinking and images go through Ollama's own message format. The model
// list is never written down here: it comes from Ollama's /api/tags (with
// /api/show for each model's context length and capabilities) whenever PiG
// refreshes provider catalogs. Nothing touches the network at startup, so pig
// starts the same whether or not Ollama is running; the extension stays silent
// until PiG asks it for models or for a response.
//
// The server address is OLLAMA_HOST, the variable Ollama's own tools read, and
// defaults to http://127.0.0.1:11434.
//
// The first version of this extension was contributed by Peder Munksgaard
// (pigpen pull request 3); see CREDITS.md.
package ollamanative

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ProviderID is the provider name: models are selected as ollama-native/<model>.
const ProviderID = "ollama-native"

// Extension returns the extension that registers the ollama-native provider.
// It does no I/O.
func Extension() *sdk.Extension {
	ext := sdk.New(ProviderID)
	c := &client{
		host:  func() string { return os.Getenv("OLLAMA_HOST") },
		http:  &http.Client{Transport: transport()},
		newID: randomID,
	}
	if err := ext.RegisterNativeProvider(newProvider(c)); err != nil {
		panic(err) // a static declaration: this is a programming error, caught by the tests
	}
	ext.Command("ollama", "Ollama: /ollama refresh reads the installed models from the server", refreshCommand)
	return ext
}

// refreshCommand is /ollama [refresh]. PiG refreshes provider catalogs when an
// interactive session starts; this does it on request, which is also how a
// one-shot `pig -p` run gets its first catalog.
func refreshCommand(ctx sdk.Context, args string) error {
	if a := strings.TrimSpace(args); a != "" && a != "refresh" {
		ctx.Notify("Usage: /ollama refresh", "warning")
		return nil
	}
	allow := true
	registry := ctx.ModelRegistry()
	result, err := registry.Refresh(sdk.ModelsRefreshOptions{AllowNetwork: &allow, Providers: []string{ProviderID}})
	if err != nil {
		return err
	}
	if msg := result.Errors[ProviderID]; msg != "" {
		ctx.Notify(msg, "error")
		return nil
	}
	all, err := registry.GetAll()
	if err != nil {
		return err
	}
	n := 0
	for _, m := range all {
		if m["provider"] == ProviderID {
			n++
		}
	}
	ctx.Notify(fmt.Sprintf("Ollama: %d model(s) available as %s/<model>", n, ProviderID), "info")
	return nil
}

// newProvider describes the provider. Its catalog starts empty and only changes
// when the host calls RefreshModels with the network allowed.
func newProvider(c *client) *sdk.Provider {
	var mu sync.Mutex
	models := []map[string]any{}
	base := c.baseURL()
	p := &sdk.Provider{
		ID:      ProviderID,
		Name:    "Ollama (native API)",
		BaseURL: &base,
		Auth: sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{
			Name: "Ollama (no key needed; OLLAMA_API_KEY for a protected server)",
			Resolve: func(sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
				key := os.Getenv("OLLAMA_API_KEY")
				if key == "" {
					key = localKey
				}
				return &sdk.AuthResult{Auth: map[string]any{"apiKey": key}}, nil
			},
		}},
		GetModels: func() ([]map[string]any, error) {
			mu.Lock()
			defer mu.Unlock()
			return append([]map[string]any{}, models...), nil
		},
		RefreshModels: func(in sdk.RefreshModelsContext) error {
			apply := func(next []map[string]any) func() error {
				return func() error {
					mu.Lock()
					models = next
					mu.Unlock()
					return nil
				}
			}
			if !in.AllowNetwork {
				// PiG's start-up pass: no network, and the catalog stored by the last
				// refresh comes back in Stored. Restoring it is how a one-shot
				// `pig -p --model ollama-native/<model>` finds its model.
				restored := restore(in.Stored)
				if len(restored) == 0 {
					return nil
				}
				_, err := in.Publish(sdk.ModelsPublication{Update: apply(restored)})
				return err
			}
			found, err := c.discover(in.Signal)
			if err != nil {
				return err
			}
			// The host stores Persist for the next start and applies the new catalog
			// by calling Update, then reads GetModels.
			stored, err := json.Marshal(map[string]any{"models": found, "checkedAt": time.Now().Unix()})
			if err != nil {
				return err
			}
			_, err = in.Publish(sdk.ModelsPublication{Persist: stored, Update: apply(found)})
			return err
		},
		Stream:       c.stream,
		StreamSimple: c.stream,
	}
	return p
}

// localKey is what Ollama gets as an API key when none is configured: PiG wants
// a key for every provider, a local Ollama ignores it, and it is never sent.
const localKey = "ollama"

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "call_" + hex.EncodeToString(b)
}

// restore reads back the catalog a refresh stored. Entries without an id are
// dropped, so a damaged store cannot put a nameless model in the list.
func restore(stored map[string]any) []map[string]any {
	items, _ := stored["models"].([]any)
	var out []map[string]any
	for _, item := range items {
		m, _ := item.(map[string]any)
		if id, _ := m["id"].(string); id != "" {
			out = append(out, m)
		}
	}
	return out
}
