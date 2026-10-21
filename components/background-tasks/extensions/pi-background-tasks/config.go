package pi_background_tasks

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The settings reader of the original (extensions/package-config.ts, vendored byte-identical into every kendex
// package). It owns the user directory, the project-trust registry and the `kendex.extensionManager.config` walk, and
// memoizes answers for settingsRecheck so a render or an output chunk never goes to the disk.
//
// upstream: package-config.ts:23-445.

const (
	// settingsChangedChannel is what pi-extension-manager emits on pi.events after it writes a setting.
	settingsChangedChannel = "kendex:extension-settings-changed"
	// settingsRecheck is how long a memoized answer is served before its inputs are read again.
	settingsRecheck = time.Second
	// configID is the package's key under kendex.extensionManager.config.
	configID = "@vanillagreen/pi-background-tasks"
)

// settingsRecord is one settings document or one package's config object.
type settingsRecord = map[string]any

// settingsFile is one settings file that exists. A file that does not parse, or cannot be read for any reason but its
// absence, is malformed; a document that parses to something other than an object reads as an empty one.
type settingsFile struct {
	path      string
	settings  settingsRecord
	malformed bool
	err       string
}

type fileText struct {
	kind string // "absent", "text", "unreadable"
	text string
	err  string
}

func readText(path string) fileText {
	b, err := os.ReadFile(path)
	if err == nil {
		return fileText{kind: "text", text: string(b)}
	}
	if errors.Is(err, fs.ErrNotExist) || isNotDir(err) {
		return fileText{kind: "absent"}
	}
	return fileText{kind: "unreadable", err: err.Error()}
}

// isNotDir is ENOTDIR: a path component that is a file.
func isNotDir(err error) bool {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return strings.Contains(pe.Err.Error(), "not a directory")
	}
	return false
}

// parseSettings is readSettingsFilesAt's parse of one text: JSON.parse, an object kept, anything else {}.
func parseSettings(path string, text fileText) (settingsFile, bool) {
	switch text.kind {
	case "absent":
		return settingsFile{}, false
	case "unreadable":
		return settingsFile{path: path, malformed: true, err: text.err}, true
	}
	var parsed any
	if err := json.Unmarshal([]byte(text.text), &parsed); err != nil {
		return settingsFile{path: path, malformed: true, err: err.Error()}, true
	}
	if m, ok := parsed.(map[string]any); ok {
		return settingsFile{path: path, settings: m}, true
	}
	return settingsFile{path: path, settings: settingsRecord{}}, true
}

func isRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// packageConfigIn is packageId's object under kendex.extensionManager.config in one parsed settings document.
func packageConfigIn(settings settingsRecord, packageID string) settingsRecord {
	kendex, _ := isRecord(settings["kendex"])
	manager, _ := isRecord(kendex["extensionManager"])
	config, _ := isRecord(manager["config"])
	own, _ := isRecord(config[packageID])
	return own
}

// trustRegistry is Pi's trust answer per project settings file. In the original every kendex package in the process
// shares one through a Symbol.for global; a Go extension process has its own, so only this package records into it.
type trustRegistry struct {
	mu       sync.Mutex
	projects map[string]bool
}

func (t *trustRegistry) record(path string, trusted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.projects == nil {
		t.projects = map[string]bool{}
	}
	t.projects[path] = trusted
}

// trusted is read on every call, never memoized: an answer recorded takes effect on the next read.
func (t *trustRegistry) trusted(path string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.projects[path]
}

// cwdSettings is what reads for one cwd resolve to.
type cwdSettings struct {
	readAt  time.Duration
	project string
	trusted bool
	paths   []string
	texts   []fileText
	files   []settingsFile
	configs map[string]settingsRecord
}

// settingsStore is the memoized reader. The zero value is not usable; use newSettingsStore.
type settingsStore struct {
	mu    sync.Mutex
	clock func() time.Duration // monotonic
	trust *trustRegistry
	// userDir resolves the user directory of this call. A test points it at a temporary directory.
	userDir func() string
	cwds    map[string]*cwdSettings
}

func newSettingsStore() *settingsStore {
	start := time.Now()
	return &settingsStore{
		clock:   func() time.Duration { return time.Since(start) },
		trust:   &trustRegistry{},
		userDir: agentDirectory,
		cwds:    map[string]*cwdSettings{},
	}
}

// settings is the process's reader.
var settings = newSettingsStore()

// agentDirectory is the user directory the settings file lives in: the agent directory the host runs with. The
// original uses `PI_CODING_AGENT_DIR` when root-anchored and `~/.pi/agent` otherwise; PiG keeps its own state apart from
// Pi's (divergence D2, as the tintinweb-tasks port), so this is PiG's agent directory (`sdk.AgentDir`, which the host
// resolves with `PIG_CODING_AGENT_DIR`, `PIG_HOME` and `PIG_USE_PI_DIRS`), and the same rule from the environment where
// the host did not provide it.
func agentDirectory() string {
	if dir, err := sdk.AgentDir(); err == nil && dir != "" {
		return dir
	}
	return agentDirFromEnv()
}

func expandHome(p string) string {
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func agentDirFromEnv() string {
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
			return filepath.Clean(expandHome(v))
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".pi", "agent")
	}
	if v := os.Getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return filepath.Clean(expandHome(v))
	}
	if v := os.Getenv("PIG_HOME"); v != "" {
		return filepath.Join(expandHome(v), "agent")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandHome(v), "pig", "agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig", "agent")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// projectSettingsPath is the project settings file for cwd: the nearest ancestor's `.pi/settings.json`, stopping at a
// `.pi`, `.git` or `.kendex-lock.json`. upstream: package-config.ts:119-128.
func projectSettingsPath(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	current := abs
	for {
		candidate := filepath.Join(current, ".pi", "settings.json")
		if exists(candidate) {
			return candidate
		}
		if exists(filepath.Join(current, ".pi")) || exists(filepath.Join(current, ".git")) || exists(filepath.Join(current, ".kendex-lock.json")) {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Join(abs, ".pi", "settings.json")
		}
		current = parent
	}
}

// recordProjectTrust records Pi's trust answer for cwd's project settings file. Only a plain true counts: a host that
// cannot answer, or answers with an error, is not trusted. upstream: package-config.ts:160-180.
func (s *settingsStore) recordProjectTrust(cwd string, trusted func() (bool, error)) {
	if cwd == "" {
		return
	}
	ok, err := trusted()
	s.trust.record(projectSettingsPath(cwd), err == nil && ok)
}

// view is cwdSettingsAt: served for settingsRecheck with a map lookup and the trust lookup, then read again.
func (s *settingsStore) view(cwd string) *cwdSettings {
	now := s.clock()
	known := s.cwds[cwd]
	var open *cwdSettings
	if known != nil && now-known.readAt < settingsRecheck {
		open = known
	}
	if open != nil && s.trust.trusted(open.project) == open.trusted {
		return open
	}
	project := ""
	if open != nil {
		project = open.project
	} else {
		project = projectSettingsPath(cwd)
	}
	trusted := s.trust.trusted(project)
	paths := []string{filepath.Join(s.userDir(), "settings.json")}
	if trusted {
		paths = append(paths, project)
	}
	texts := make([]fileText, len(paths))
	for i, p := range paths {
		texts[i] = readText(p)
	}
	// Text equal to what the last parse saw keeps the last answer, so an unchanged file is parsed once.
	if known != nil && equalPaths(known.paths, paths) && equalTexts(known.texts, texts) {
		view := *known
		view.readAt = now
		view.project = project
		view.trusted = trusted
		s.cwds[cwd] = &view
		return &view
	}
	var files []settingsFile
	for i, p := range paths {
		if f, ok := parseSettings(p, texts[i]); ok {
			files = append(files, f)
		}
	}
	view := &cwdSettings{readAt: now, project: project, trusted: trusted, paths: paths, texts: texts, files: files, configs: map[string]settingsRecord{}}
	s.cwds[cwd] = view
	return view
}

func equalPaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalTexts(a, b []fileText) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// config is readPackageConfig: packageID's config over the user settings file and, when Pi trusts the project, the
// project's, later files overriding earlier ones key by key. A malformed file contributes nothing. The map is shared and
// must not be written. upstream: package-config.ts:344-417.
func (s *settingsStore) config(packageID, cwd string) settingsRecord {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	view := s.view(cwd)
	if known, ok := view.configs[packageID]; ok {
		return known
	}
	merged := settingsRecord{}
	for _, f := range view.files {
		if f.malformed {
			continue
		}
		for k, v := range packageConfigIn(f.settings, packageID) {
			merged[k] = v
		}
	}
	view.configs[packageID] = merged
	return merged
}

// clear drops every memoized answer so the next read goes back to the disk and the environment.
func (s *settingsStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwds = map[string]*cwdSettings{}
}

// trustedProjectSettingsPath is the project settings file for cwd when Pi trusts the project, else "".
func (s *settingsStore) trustedProjectSettingsPath(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	view := s.view(cwd)
	if view.trusted {
		return view.project
	}
	return ""
}
