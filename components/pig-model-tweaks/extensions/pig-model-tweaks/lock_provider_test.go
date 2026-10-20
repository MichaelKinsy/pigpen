package pigmodeltweaks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lockHome gives the extension an agent directory with a settings.json, and
// starts the session the way the host does so the store knows where to write.
func lockHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func startLockHost(t *testing.T, model map[string]any) *Host {
	t.Helper()
	h := StartHost(t, Extension(), HostOptions{Model: model})
	if failure := fire(h, "session_start", map[string]any{"reason": "resume"}); failure != "" {
		t.Fatalf("session_start: %s", failure)
	}
	return h
}

func notifications(h *Host) []string {
	var out []string
	for _, call := range h.CallsTo("ui.notify") {
		message, _ := call.Args["message"].(string)
		out = append(out, message)
	}
	return out
}

func lastNotification(h *Host) string {
	all := notifications(h)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

func providerRequest(t *testing.T, h *Host, model string) map[string]any {
	t.Helper()
	raw := h.Fire("before_provider_request", map[string]any{"payload": map[string]any{"model": model}})
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("provider request result %s: %v", raw, err)
	}
	return out
}

func orderOf(body map[string]any) []any {
	provider, _ := body["provider"].(map[string]any)
	order, _ := provider["order"].([]any)
	return order
}

// A lock belongs to an OpenRouter model. A request for another backend that
// happens to carry the same model id must neither lose a suffix nor gain a
// provider.order, which that backend would reject or misread.
func TestProviderRequestIsLeftAloneForAnotherProvider(t *testing.T) {
	lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "acme"})
	if failure := h.Command(lockProviderName, "lock vendor/m deepseek"); failure != "" {
		t.Fatal(failure)
	}
	if got := providerRequest(t, h, "vendor/m:deepseek"); got != nil {
		t.Fatalf("a non-OpenRouter request was rewritten: %v", got)
	}
	if got := providerRequest(t, h, "vendor/m"); got != nil {
		t.Fatalf("a non-OpenRouter request got a provider order: %v", got)
	}
}

func TestProviderRequestIsRoutedForOpenRouter(t *testing.T) {
	lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	if failure := h.Command(lockProviderName, "lock vendor/m deepseek"); failure != "" {
		t.Fatal(failure)
	}
	got := providerRequest(t, h, "vendor/m:deepseek")
	if got == nil || got["model"] != "vendor/m" {
		t.Fatalf("model = %v, want the suffix stripped", got)
	}
	if order := orderOf(got); len(order) != 1 || order[0] != "deepseek" {
		t.Fatalf("provider.order = %v, want [deepseek]", order)
	}
}

// `/cmd <provider>` locks the model the session is running, as the original does.
func TestSingleArgumentLocksTheCurrentModel(t *testing.T) {
	home := lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	if failure := h.Command(lockProviderName, "deepseek"); failure != "" {
		t.Fatal(failure)
	}
	if message := lastNotification(h); !strings.Contains(message, "vendor/m") || !strings.Contains(message, "deepseek") {
		t.Fatalf("notification = %q, want the model and provider", message)
	}
	if order := orderOf(providerRequest(t, h, "vendor/m")); len(order) != 1 || order[0] != "deepseek" {
		t.Fatalf("provider.order = %v, want [deepseek]", order)
	}
	// The lock is persisted next to the default model, as the original's applyLock does.
	raw, err := os.ReadFile(filepath.Join(home, "agent", "settings.json"))
	if err != nil || !strings.Contains(string(raw), `"vendor/m"`) || !strings.Contains(string(raw), `"openrouter"`) {
		t.Fatalf("settings.json = %s, %v; want the locked model as the default", raw, err)
	}
}

func TestSingleArgumentRefusesANonOpenRouterModel(t *testing.T) {
	lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "acme"})
	if failure := h.Command(lockProviderName, "deepseek"); failure != "" {
		t.Fatal(failure)
	}
	if message := lastNotification(h); !strings.Contains(message, "not an OpenRouter model") {
		t.Fatalf("notification = %q, want the not-OpenRouter warning", message)
	}
	if got := providerRequest(t, h, "vendor/m"); got != nil {
		t.Fatalf("no lock should exist: %v", got)
	}
}

func TestBareClearRemovesTheLockOfTheCurrentModel(t *testing.T) {
	lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	if failure := h.Command(lockProviderName, "deepseek"); failure != "" {
		t.Fatal(failure)
	}
	if failure := h.Command(lockProviderName, "clear"); failure != "" {
		t.Fatal(failure)
	}
	if message := lastNotification(h); !strings.Contains(message, "Cleared provider lock for vendor/m") {
		t.Fatalf("notification = %q, want the cleared line", message)
	}
	if got := providerRequest(t, h, "vendor/m"); got != nil {
		t.Fatalf("the lock survived clear: %v", got)
	}
}

func TestLockAndClearPersistTheDefaultModel(t *testing.T) {
	home := lockHome(t)
	h := startLockHost(t, map[string]any{"id": "other/x", "provider": "openrouter"})
	if failure := h.Command(lockProviderName, "lock vendor/m deepseek"); failure != "" {
		t.Fatal(failure)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "agent", "settings.json"))
	if !strings.Contains(string(raw), `"vendor/m"`) {
		t.Fatalf("settings.json = %s, want vendor/m after lock", raw)
	}
	if failure := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte("{}\n"), 0o644); failure != nil {
		t.Fatal(failure)
	}
	if failure := h.Command(lockProviderName, "clear vendor/m"); failure != "" {
		t.Fatal(failure)
	}
	raw, _ = os.ReadFile(filepath.Join(home, "agent", "settings.json"))
	if !strings.Contains(string(raw), `"vendor/m"`) {
		t.Fatalf("settings.json = %s, want vendor/m after clear", raw)
	}
}

func TestNoArgumentOutsideTheTUIPrintsUsage(t *testing.T) {
	lockHome(t)
	h := StartHost(t, Extension(), HostOptions{Mode: "print", Model: map[string]any{"id": "vendor/m", "provider": "openrouter"}})
	if failure := h.Command(lockProviderName, ""); failure != "" {
		t.Fatal(failure)
	}
	if message := lastNotification(h); !strings.HasPrefix(message, "Usage: /"+lockProviderName) {
		t.Fatalf("notification = %q, want usage", message)
	}
	for _, call := range h.CallsTo("ui.select") {
		t.Fatalf("a picker was opened outside the TUI: %v", call)
	}
}
