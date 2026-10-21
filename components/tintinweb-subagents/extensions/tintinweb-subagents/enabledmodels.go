package tintinweb_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Reads `enabledModels` from Pi's settings (global <agent dir>/settings.json and the project's .pi/settings.json, the
// project winning wholly: an array replaces, it is not concatenated) and resolves the entries to "provider/modelId"
// keys for the scope check. Only exact "provider/modelId" entries are matched, case-insensitively; a bare id, a glob
// or a `:thinking` suffix is dropped, so a hand-edited list that uses them gives an empty set and the check becomes a
// no-op. upstream: src/enabled-models.ts.

func enabledModelsPaths(cwd string) (project, global string) {
	return filepath.Join(cwd, ".pi", "settings.json"), filepath.Join(agentDir(), "settings.json")
}

// readEnabledModelsField is the `enabledModels` array of one file; nil when the file is missing, corrupt or has no array.
func readEnabledModelsField(path string) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var raw struct {
		EnabledModels *[]any `json:"enabledModels"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.EnabledModels == nil {
		return nil, false
	}
	out := []string{}
	for _, v := range *raw.EnabledModels {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out, true
}

// readEnabledModels reads enabledModels, the project's overriding the global one. ok is false when neither file has it.
func readEnabledModels(cwd string) (patterns []string, ok bool) {
	project, global := enabledModelsPaths(cwd)
	if p, ok := readEnabledModelsField(project); ok {
		return p, true
	}
	return readEnabledModelsField(global)
}

// The cache is keyed on the patterns and on the modification time and size of both files.
var (
	enabledCacheMu    sync.Mutex
	cachedAllowed     map[string]bool
	cachedHash        string
	cachedPatternsKey string
)

func fileHash(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
}

// modelKey is the canonical lowercase "provider/id".
func modelKey(provider, id string) string { return strings.ToLower(provider + "/" + id) }

// resolveEnabledModels resolves the patterns to the set of allowed keys, or nil when there are none or none matches
// an available model (the scope check is then a no-op). hasPatterns false is JavaScript's `undefined`.
func resolveEnabledModels(patterns []string, hasPatterns bool, reg modelRegistry, cwd string) map[string]bool {
	key := "undefined"
	if hasPatterns {
		b, _ := json.Marshal(patterns)
		key = string(b)
	}
	project, global := enabledModelsPaths(cwd)
	hash := fileHash(project) + ";" + fileHash(global)
	enabledCacheMu.Lock()
	defer enabledCacheMu.Unlock()
	if hash == cachedHash && key == cachedPatternsKey {
		return cachedAllowed
	}
	if !hasPatterns || len(patterns) == 0 {
		cachedHash, cachedPatternsKey, cachedAllowed = hash, key, nil
		return nil
	}
	available := reg.available()
	allowed := map[string]bool{}
	for _, pattern := range patterns {
		trimmed := jsTrim(pattern)
		if trimmed == "" {
			continue
		}
		slash := strings.Index(trimmed, "/") // a bare model id is not supported; a colon is part of the id
		if slash == -1 {
			continue
		}
		provider, id := strings.ToLower(trimmed[:slash]), strings.ToLower(trimmed[slash+1:])
		for _, m := range available {
			if strings.ToLower(str(m["provider"])) == provider && strings.ToLower(str(m["id"])) == id {
				allowed[modelKey(str(m["provider"]), str(m["id"]))] = true
				break
			}
		}
	}
	var result map[string]bool
	if len(allowed) > 0 {
		result = allowed
	}
	cachedHash, cachedPatternsKey, cachedAllowed = hash, key, result
	return result
}

// isModelInScope: whether the model is in the allowed set.
func isModelInScope(provider, id string, allowed map[string]bool) bool {
	return allowed[modelKey(provider, id)]
}
