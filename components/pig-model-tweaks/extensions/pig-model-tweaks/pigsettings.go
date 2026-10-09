package pigmodeltweaks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// agentDir is PiG's agent directory, where settings.json and this extension's
// own file live. It mirrors PiG's own rule - PIG_HOME when set, otherwise
// ~/.pig - so the extension writes where PiG reads, including under an isolated
// PIG_HOME used by tests or containers.
func agentDir(configHome string) string {
	if configHome == "" {
		configHome = defaultConfigHome()
	}
	return filepath.Join(configHome, "agent")
}

// defaultConfigHome resolves the config root the same way PiG does, for the
// moments before a session context is available.
func defaultConfigHome() string {
	if home := os.Getenv("PIG_HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pig"
	}
	return filepath.Join(home, ".pig")
}

// settingsFile is PiG's own settings.json inside the agent directory.
func settingsFile(dir string) string {
	return filepath.Join(dir, "settings.json")
}

// readDefaultModel reads the provider and model PiG itself starts on. A missing,
// unreadable or half-written file yields empty halves rather than an error: this
// runs from an event handler, where a failure must not break model selection.
func readDefaultModel(dir string) (provider, model string) {
	raw, err := os.ReadFile(settingsFile(dir))
	if err != nil {
		return "", ""
	}
	var document struct {
		DefaultProvider string `json:"defaultProvider"`
		DefaultModel    string `json:"defaultModel"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		logf("%s is not valid JSON; not reading the default model", settingsFile(dir))
		return "", ""
	}
	return strings.TrimSpace(document.DefaultProvider), strings.TrimSpace(document.DefaultModel)
}

// persistDefaultModel writes defaultProvider and defaultModel into PiG's
// settings.json so the next session starts on the remembered model even before
// this extension runs.
//
// The written id is the base model id, never the `:provider` variant. The pi
// original wrote the variant and repaired it at session start, because pi's
// resolver could not read the suffix either; writing an id PiG cannot parse is a
// worse trade, because a broken value in settings.json outlives the session that
// wrote it. Routing is unaffected: the provider lock is applied at request time as
// OpenRouter's provider.order.
//
// The rewritten file keeps every other key untouched, and a missing or invalid
// settings.json is reported by returning an empty id rather than an error: this
// runs from an event handler where a failure must not break model selection.
func persistDefaultModel(agentDir string, provider, baseID string) string {
	path := settingsFile(agentDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		logf("read %s: %v", path, err)
		return ""
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		logf("%s is not valid JSON; not persisting the default model", path)
		return ""
	}
	document["defaultProvider"] = provider
	document["defaultModel"] = baseID
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		logf("encode %s: %v", path, err)
		return ""
	}
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		logf("write %s: %v", path, err)
		return ""
	}
	return baseID
}

// writeFileAtomic replaces a file through a sibling temporary file and a rename,
// so PiG never reads a half-written settings.json.
func writeFileAtomic(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".pmt-settings-tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Keep the mode the file already had; PiG owns it.
	if info, err := os.Stat(path); err == nil {
		_ = os.Chmod(temp.Name(), info.Mode().Perm())
	}
	return os.Rename(temp.Name(), path)
}
