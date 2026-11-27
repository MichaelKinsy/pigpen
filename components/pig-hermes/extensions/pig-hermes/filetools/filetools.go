// Package filetools implements Hermes Agent's file toolset (read_file, write_file,
// patch, search_files) for pig-hermes using pure Go stdlib and the PiG v0.8.0 SDK.
package filetools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/config"
	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

const (
	defaultReadLimit     = 500
	maxReadLimit         = 2000
	defaultMaxReadChars  = 100000
	defaultMaxLineLength = 2000
	defaultSearchLimit   = 50
)

type readRecord struct {
	modTime  time.Time
	size     int64
	fullRead bool
}

// state holds per-extension session state shared across file tools.
type state struct {
	cfg *config.Config

	mu        sync.RWMutex
	readFiles map[string]readRecord
}

func newState(cfg *config.Config) *state {
	if cfg == nil {
		cfg = config.Default()
	}
	return &state{
		cfg:       cfg,
		readFiles: make(map[string]readRecord),
	}
}

func (s *state) maxReadChars() int {
	if s.cfg != nil && s.cfg.MaxReadChars > 0 {
		return s.cfg.MaxReadChars
	}
	return defaultMaxReadChars
}

func (s *state) maxLineLength() int {
	if s.cfg != nil && s.cfg.MaxLineLength > 0 {
		return s.cfg.MaxLineLength
	}
	return defaultMaxLineLength
}

func (s *state) recordRead(absPath string, info os.FileInfo, fullRead bool) {
	if info == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, exists := s.readFiles[absPath]
	if exists && prev.fullRead && prev.modTime.Equal(info.ModTime()) && prev.size == info.Size() {
		fullRead = true
	}
	s.readFiles[absPath] = readRecord{
		modTime:  info.ModTime(),
		size:     info.Size(),
		fullRead: fullRead,
	}
}

func (s *state) checkStaleness(absPath string) error {
	info, err := os.Stat(absPath)
	if err != nil {
		return nil // new file or unreadable (handled by caller)
	}
	s.mu.RLock()
	rec, tracked := s.readFiles[absPath]
	s.mu.RUnlock()
	if !tracked {
		return nil
	}
	if !rec.fullRead {
		return fmt.Errorf(
			"file %q was only partially read; call read_file to read the full file before overwriting with write_file (or use patch for targeted edits)",
			absPath,
		)
	}
	if !rec.modTime.Equal(info.ModTime()) || rec.size != info.Size() {
		return fmt.Errorf(
			"file %q has been modified on disk since it was last read (stale baseline); call read_file again before overwriting",
			absPath,
		)
	}
	return nil
}

func isActiveHermesConfigPath(normSlashPath string) bool {
	cfgFile := strings.TrimSpace(paths.ResolveProfilePaths("").ConfigFile)
	if cfgFile == "" {
		return false
	}
	cfgNorm := filepath.ToSlash(filepath.Clean(cfgFile))
	if real, err := filepath.EvalSymlinks(cfgFile); err == nil {
		cfgNorm = filepath.ToSlash(filepath.Clean(real))
	} else if parentReal, err := filepath.EvalSymlinks(filepath.Dir(cfgFile)); err == nil {
		cfgNorm = filepath.ToSlash(filepath.Join(parentReal, filepath.Base(cfgFile)))
	}
	return normSlashPath == cfgNorm
}

// Register registers read_file, write_file, patch, and search_files on ext.
func Register(ext *sdk.Extension, cfg *config.Config) {
	st := newState(cfg)
	ext.RegisterTool(st.readFileToolDef())
	ext.RegisterTool(st.writeFileToolDef())
	ext.RegisterTool(st.patchToolDef())
	ext.RegisterTool(st.searchFilesToolDef())
}

func resolvePath(cwd, raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if !filepath.IsAbs(p) {
		base := cwd
		if base == "" {
			if wd, err := os.Getwd(); err == nil {
				base = wd
			}
		}
		if base != "" {
			p = filepath.Join(base, p)
		}
	}
	return filepath.Clean(p), nil
}

func safeCwd(ctx sdk.Context) string {
	defer func() { _ = recover() }()
	return ctx.Cwd()
}

func safeHasUI(ctx sdk.Context) bool {
	defer func() { _ = recover() }()
	return ctx.HasUI()
}

func paramString(params map[string]any, key string) (string, bool) {
	v, ok := params[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func paramInt(params map[string]any, key string, def int) int {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return def
	}
}

func paramBool(params map[string]any, key string, def bool) bool {
	v, ok := params[key]
	if !ok || v == nil {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func errResult(msg string) sdk.ToolResult {
	return sdk.ToolResult{
		Content: msg,
		IsError: true,
	}
}

func okResult(content string, details any) sdk.ToolResult {
	return sdk.ToolResult{
		Content: content,
		Details: details,
	}
}
