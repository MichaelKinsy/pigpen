// Package ohmyhermes implements the Oh My Hermes (OMH) orchestration, routing,
// DAG, team, loop, evidence-boundary, memory, and TUI/CLI extension for PiG.
package ohmyhermes

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// EnvOMHHome overrides the default ~/.omh state root.
	EnvOMHHome = "OMH_HOME"
)

var (
	omhHomeMu       sync.RWMutex
	omhHomeOverride string

	safeSegmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// SetOMHHomeOverride sets an explicit in-process OMH home directory (e.g. from --omh-home).
func SetOMHHomeOverride(dir string) {
	omhHomeMu.Lock()
	defer omhHomeMu.Unlock()
	if strings.TrimSpace(dir) == "" {
		omhHomeOverride = ""
		return
	}
	omhHomeOverride = filepath.Clean(strings.TrimSpace(dir))
}

// ResolveOMHHome resolves the active OMH state root:
// explicit override > $OMH_HOME > ~/.omh.
func ResolveOMHHome() string {
	omhHomeMu.RLock()
	ov := omhHomeOverride
	omhHomeMu.RUnlock()
	if ov != "" {
		return ov
	}
	if v := strings.TrimSpace(os.Getenv(EnvOMHHome)); v != "" {
		return filepath.Clean(v)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".omh")
	}
	return filepath.Join(home, ".omh")
}

// OMHPaths holds standard file and directory paths under $OMH_HOME.
type OMHPaths struct {
	Home              string
	RoutingDir        string
	ModelChainsFile   string
	IdentitiesFile    string
	ThemeFile         string
	RuntimeDir        string
	DAGDir            string
	TeamDir           string
	CompletionDir     string
	CompletionRecords string
	DelegationDir     string
	LoopsDir          string
	TodosDir          string
	MemoryDir         string
	MemoryNotesFile   string
}

// ResolvePaths computes all standard OMH state paths under home (or ResolveOMHHome if empty).
func ResolvePaths(home string) OMHPaths {
	if strings.TrimSpace(home) == "" {
		home = ResolveOMHHome()
	} else {
		home = filepath.Clean(strings.TrimSpace(home))
	}
	routingDir := filepath.Join(home, "routing")
	runtimeDir := filepath.Join(home, "runtime")
	completionDir := filepath.Join(runtimeDir, "completion")
	memoryDir := filepath.Join(home, "memory")
	return OMHPaths{
		Home:              home,
		RoutingDir:        routingDir,
		ModelChainsFile:   filepath.Join(routingDir, "model-chains.json"),
		IdentitiesFile:    filepath.Join(routingDir, "identities.json"),
		ThemeFile:         filepath.Join(home, "theme.json"),
		RuntimeDir:        runtimeDir,
		DAGDir:            filepath.Join(runtimeDir, "dag"),
		TeamDir:           filepath.Join(runtimeDir, "team"),
		CompletionDir:     completionDir,
		CompletionRecords: filepath.Join(completionDir, "records.json"),
		DelegationDir:     filepath.Join(runtimeDir, "delegation"),
		LoopsDir:          filepath.Join(runtimeDir, "loops"),
		TodosDir:          filepath.Join(runtimeDir, "todos"),
		MemoryDir:         memoryDir,
		MemoryNotesFile:   filepath.Join(memoryDir, "notes.jsonl"),
	}
}

// sanitizeID validates or normalizes a path segment to prevent traversal.
func sanitizeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || !safeSegmentRe.MatchString(id) {
		return "", fmt.Errorf("invalid identifier %q: must match %s", id, safeSegmentRe.String())
	}
	return id, nil
}

// newShortID generates a random 8-hex-character identifier with prefix.
func newShortID(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	if prefix == "" {
		return hex.EncodeToString(b[:])
	}
	return prefix + "-" + hex.EncodeToString(b[:])
}

// atomicWriteBytes writes data to path atomically via a temp file + rename in the same directory.
func atomicWriteBytes(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".omh-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}

// atomicWriteJSON marshals v as indented JSON and writes it atomically with 0600 permissions.
func atomicWriteJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	raw = append(raw, '\n')
	return atomicWriteBytes(path, raw, 0o600)
}

// readJSON reads and unmarshals JSON from path. Returns (false, nil) if the file does not exist.
func readJSON(path string, dst any) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return true, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return true, nil
}

// appendJSONL appends one JSON line to path safely (creating parent dirs if needed).
func appendJSONL(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

// Shared SDK tool result & parameter extraction helpers.

func jsonToolResult(payload any, isErr bool) sdk.ToolResult {
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return errorToolResult(fmt.Sprintf("marshal result: %v", err))
	}
	return sdk.ToolResult{
		Content: string(b),
		IsError: isErr,
	}
}

func errorToolResult(msg string) sdk.ToolResult {
	return jsonToolResult(map[string]any{
		"ok":    false,
		"error": msg,
	}, true)
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if s, ok := params[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func paramInt(params map[string]any, key string, def int) int {
	if params == nil {
		return def
	}
	switch v := params[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}

func paramBool(params map[string]any, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	if b, ok := params[key].(bool); ok {
		return b, true
	}
	return false, false
}

func paramStringSlice(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if t := strings.TrimSpace(s); t != "" {
				out = append(out, t)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					out = append(out, t)
				}
			}
		}
		return out
	}
	return nil
}

func resolveSessionID(ctx sdk.Context) string {
	if id, err := ctx.GetSessionID(); err == nil && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	return "default"
}
