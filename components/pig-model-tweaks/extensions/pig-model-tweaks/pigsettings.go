package pigmodeltweaks

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
// For an OpenRouter model with an active provider lock the id written is the
// `base:provider` variant, as the original's persistDefaultModel writes it
// (pi-settings.ts); the remembered-model restore at session start puts the real
// model back, because a default resolver that does not know the suffix cannot.
// Routing does not depend on this value: the lock is applied at request time as
// OpenRouter's provider.order. The returned id is what was written.
//
// The rewritten file keeps every other key untouched, and a missing or invalid
// settings.json is reported by returning an empty id rather than an error: this
// runs from an event handler where a failure must not break model selection.
func persistDefaultModel(agentDir string, state *settings, provider, baseID string) string {
	path := settingsFile(agentDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		logf("read %s: %v", path, err)
		return ""
	}
	modelID := baseID
	if provider == openRouterProvider {
		if lock := lockFor(state, baseID); lock != "" {
			modelID = makeVariantID(baseID, lock)
		}
	}
	data, err := setTopLevelKeys(raw, [][2]string{
		{"defaultProvider", provider},
		{"defaultModel", modelID},
	})
	if err != nil {
		logf("%s is not a JSON object (%v); not persisting the default model", path, err)
		return ""
	}
	if err := writeFileAtomic(path, data); err != nil {
		logf("write %s: %v", path, err)
		return ""
	}
	return modelID
}

// setTopLevelKeys sets string values on the top-level keys of a JSON object the
// way JavaScript's read, assign and `JSON.stringify(settings, null, 2)` does, which
// is what the original does to PiG's settings.json: every other key keeps its
// position and its value, an existing key keeps its place, a new key goes last, and
// the document ends with a newline. Decoding into a Go map would sort every key of
// a file the user keeps by hand.
func setTopLevelKeys(raw []byte, updates [][2]string) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, isDelim := opening.(json.Delim); !isDelim || delim != '{' {
		return nil, errors.New("not an object")
	}
	var keys []string
	values := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := token.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the object")
	}
	for _, update := range updates {
		encoded, err := marshalString(update[1])
		if err != nil {
			return nil, err
		}
		if _, seen := values[update[0]]; !seen {
			keys = append(keys, update[0])
		}
		values[update[0]] = encoded
	}
	if len(keys) == 0 {
		return []byte("{}\n"), nil
	}
	var out bytes.Buffer
	out.WriteString("{\n")
	for index, key := range keys {
		name, err := marshalString(key)
		if err != nil {
			return nil, err
		}
		out.WriteString("  ")
		out.Write(name)
		out.WriteString(": ")
		if err := json.Indent(&out, values[key], "  ", "  "); err != nil {
			return nil, err
		}
		if index < len(keys)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("}\n")
	return out.Bytes(), nil
}

// marshalString encodes a string as JavaScript's JSON.stringify does: no HTML escaping.
func marshalString(text string) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(text); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
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
