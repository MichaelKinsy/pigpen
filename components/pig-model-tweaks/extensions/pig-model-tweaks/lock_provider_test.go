package pigmodeltweaks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// setActiveModel tells the extension which model the session runs, the way the
// host's state_update does.
func setActiveModel(h *Host, model map[string]any) {
	h.write(map[string]any{"type": "notify", "notify": map[string]any{
		"method": "state_update", "args": map[string]any{"state": map[string]any{"model": model}},
	}})
}

func startLockHost(t *testing.T, model map[string]any) *Host {
	t.Helper()
	h := StartHost(t, Extension(), HostOptions{})
	setActiveModel(h, model)
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
	// The original's message, byte for byte: the lock and the variant written
	// into settings.json.
	if message, want := lastNotification(h), "OpenRouter provider lock for vendor/m: deepseek\ndefaultModel -> vendor/m:deepseek"; message != want {
		t.Fatalf("notification = %q, want %q", message, want)
	}
	if order := orderOf(providerRequest(t, h, "vendor/m")); len(order) != 1 || order[0] != "deepseek" {
		t.Fatalf("provider.order = %v, want [deepseek]", order)
	}
	// The lock is persisted next to the default model as `base:provider`, as the
	// original's applyLock does.
	wantDefaultModel(t, home, "openrouter", "vendor/m:deepseek")
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
	wantDefaultModel(t, home, "openrouter", "vendor/m:deepseek")
	if failure := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte("{}\n"), 0o644); failure != nil {
		t.Fatal(failure)
	}
	if failure := h.Command(lockProviderName, "clear vendor/m"); failure != "" {
		t.Fatal(failure)
	}
	wantDefaultModel(t, home, "openrouter", "vendor/m")
}

// wantDefaultModel checks the provider and model PiG's settings.json names.
func wantDefaultModel(t *testing.T, home, provider, model string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "agent", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("settings.json = %s: %v", raw, err)
	}
	if document["defaultProvider"] != provider || document["defaultModel"] != model {
		t.Fatalf("settings.json = %s, want defaultProvider %q and defaultModel %q", raw, provider, model)
	}
}

// With the lock feature off, nothing is locked, so the default stays the base id
// (the original's getLock answers nothing while the feature is disabled).
func TestDisabledLockPersistsTheBaseIDOnly(t *testing.T) {
	home := lockHome(t)
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	if failure := h.Command(lockProviderName, "off"); failure != "" {
		t.Fatal(failure)
	}
	if failure := h.Command(lockProviderName, "deepseek"); failure != "" {
		t.Fatal(failure)
	}
	wantDefaultModel(t, home, "openrouter", "vendor/m")
}

// A remembered selection of a locked OpenRouter model writes `base:provider`
// into settings.json and says so in its toast, as the original does; a model of
// another provider with the same id stays plain.
func TestRememberedSelectionOfALockedModelWritesTheVariant(t *testing.T) {
	home := lockHome(t)
	seedSettings(t, home, map[string]any{"version": 1,
		"rememberModel":               map[string]any{"enabled": true},
		"openrouterModelProviderPref": map[string]any{"enabled": true, "locks": map[string]string{"vendor/m": "deepseek"}}})
	h := startLockHost(t, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	h.FireAll("model_select", map[string]any{"model": map[string]any{"id": "vendor/m", "provider": "openrouter"}})
	wantDefaultModel(t, home, "openrouter", "vendor/m:deepseek")
	last := readSettings(filepath.Join(home, "agent", settingsFileName)).RememberModel.Last
	if last == nil || last.Provider != "openrouter" || last.ModelID != "vendor/m" {
		t.Fatalf("remembered = %+v, want the base id", last)
	}
	want := rememberModelName + ": remembered model: openrouter/vendor/m:deepseek in settings.json"
	deadline := time.Now().Add(2 * time.Second)
	for lastNotification(h) != want {
		if time.Now().After(deadline) {
			t.Fatalf("notifications = %q, want %q", notifications(h), want)
		}
		time.Sleep(5 * time.Millisecond)
	}

	h.FireAll("model_select", map[string]any{"model": map[string]any{"id": "vendor/m", "provider": "acme"}})
	wantDefaultModel(t, home, "acme", "vendor/m")
}

func TestNoArgumentOutsideTheTUIPrintsUsage(t *testing.T) {
	lockHome(t)
	h := StartHost(t, Extension(), HostOptions{Mode: "print"})
	setActiveModel(h, map[string]any{"id": "vendor/m", "provider": "openrouter"})
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
