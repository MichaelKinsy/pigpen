package search

import (
	"embed"
	"slices"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

//go:embed prompts/grep.md prompts/glob.md prompts/find.md
var promptFS embed.FS

// Setting reads a session setting by omp setting key (e.g. "find.enabled",
// "find.hitThreshold"). Nil uses omp's defaults ("auto", 0.2).
// Prefer SetSetting to install or replace it without racing concurrent sessions;
// direct assignment to Setting before any session starts remains supported.
var Setting func(ctx sdk.Context, key string) any

var settingMu sync.RWMutex

// SetSetting installs the settings reader under lock, avoiding data races when
// live sessions are already running.
func SetSetting(fn func(ctx sdk.Context, key string) any) {
	settingMu.Lock()
	Setting = fn
	settingMu.Unlock()
}

func readSetting(ctx sdk.Context, key string) any {
	settingMu.RLock()
	fn := Setting
	settingMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, key)
}

// ArchivePathCandidate is a (ArchivePath, SubPath) split of a path selector.
type ArchivePathCandidate struct {
	ArchivePath string
	SubPath     string
}

// ArchiveMemberReader provides archive path parsing and member reading without
// importing internal/files from internal/search.
type ArchiveMemberReader struct {
	ParseCandidates func(filePath string) []ArchivePathCandidate
	ReadMember      func(archiveAbs, memberPath string) (data []byte, openErr error, readErr error)
}

var (
	archiveReaderMu sync.RWMutex
	archiveReader   *ArchiveMemberReader
)

// SetArchiveReader installs the archive member reader used by grep.
func SetArchiveReader(r *ArchiveMemberReader) {
	archiveReaderMu.Lock()
	archiveReader = r
	archiveReaderMu.Unlock()
}

func getArchiveReader() *ArchiveMemberReader {
	archiveReaderMu.RLock()
	r := archiveReader
	archiveReaderMu.RUnlock()
	return r
}

// Per-session `find` activation tracking, shared across extension instances
// because PiG constructs a fresh extension per session while package state is
// process-wide. Keyed strictly by non-empty sessionID and released via
// lifecycle.OnEnd("search", ...).
type findSessionState struct {
	autoActivated bool // true once this extension activated `find` for this session
	userRemoved   bool // true if `find` was removed after we activated it
}

var (
	findSessionsMu sync.Mutex
	findSessions   = map[string]*findSessionState{}
)

func cleanupSearchSession(sessionID string) {
	if sessionID == "" {
		return
	}
	findSessionsMu.Lock()
	delete(findSessions, sessionID)
	findSessionsMu.Unlock()
}

const (
	grepFindHintLine = "Behavior/unknown symbol → `find`; literals/regex → `grep`."
	globFindHintLine = "Behavior search → `find`."
)

func mustPromptRaw(name string) string {
	data, err := promptFS.ReadFile("prompts/" + name)
	if err != nil {
		panic("search: missing embedded prompt " + name + ": " + err.Error())
	}
	return string(data)
}

func mustPrompt(name string) string {
	return strings.TrimSpace(mustPromptRaw(name))
}

func grepDescription(hasFind bool) string {
	base := mustPrompt("grep.md")
	if hasFind {
		return base + "\n" + grepFindHintLine
	}
	return base
}

func globDescription(hasFind bool) string {
	base := mustPrompt("glob.md")
	if hasFind {
		return base + "\n" + globFindHintLine
	}
	return base
}

func findEnabledSetting(ctx sdk.Context) string {
	if s, ok := readSetting(ctx, "find.enabled").(string); ok {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "on":
			return "on"
		case "off":
			return "off"
		case "auto":
			return "auto"
		}
	}
	return "auto"
}

func findHitThresholdSetting(ctx sdk.Context) float64 {
	if v, ok := floatFromAny(readSetting(ctx, "find.hitThreshold")); ok && v > 0 && v <= 1 {
		return v
	}
	return findDefaultThreshold
}

// Register registers omp's search tools (grep, glob, find) on the extension.
// `find` registers with DefaultActive: false (matching omp's `find.enabled: "auto"`
// default when no native judge is present). On `session_start` / `model_select`,
// `find` is activated only when `isFindEnabled(ctx)` is true — which under `"auto"`
// requires a native classifier (`ModelTypeClassifier`) — and never re-added if the
// user explicitly removed `find` from the active tools.
// `grep` and `glob` register with `hasFind=false` descriptions and use `PrepareLoadout`
// to switch to `hasFind=true` whenever `find` is declared active in the loadout.
// Safe to call multiple times in one process (once per PiG session instance).
func Register(e *sdk.Extension) {
	lifecycle.OnEnd("search", cleanupSearchSession)

	grepBaseDesc := grepDescription(false)
	globBaseDesc := globDescription(false)

	prepareSearchLoadout := func(loadout sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
		hasFind := false
		for _, tool := range loadout.Declared {
			if tool.Name == "find" {
				hasFind = true
				break
			}
		}
		if !hasFind {
			return nil
		}
		changes := make(map[string]string, 2)
		for _, tool := range loadout.Declared {
			switch tool.Name {
			case "grep":
				changes["grep"] = grepDescription(true)
			case "glob":
				changes["glob"] = globDescription(true)
			}
		}
		if len(changes) == 0 {
			return nil
		}
		return &sdk.ToolLoadoutChanges{Descriptions: changes}
	}

	// upstream: packages/coding-agent/src/tools/grep.ts:80-86
	grepDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:        "grep",
		Label:       "Grep",
		Description: grepBaseDesc,
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"pattern":   map[string]any{"type": "string"},
				"path":      map[string]any{"type": "string"},
				"case":      map[string]any{"type": "boolean"},
				"gitignore": map[string]any{"type": "boolean"},
				"skip":      map[string]any{"type": []string{"number", "null"}},
			},
			"required":             []string{"pattern"},
			"additionalProperties": false,
		},
		PrepareLoadout: prepareSearchLoadout,
		Execute:        executeGrep,
	})
	e.RegisterTool(intent.Tool(grepDef))

	// upstream: packages/coding-agent/src/tools/glob.ts:37-42,207-248
	// Schema matches omp's closed schema (`additionalProperties: false`); PrepareArguments
	// runs before host schema validation so misplaced `pattern` is merged into `path`
	// and unknown keys fail with omp's exact message (`glob has no \`k\` argument; put the glob in \`path\``).
	globDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:        "glob",
		Label:       "Glob",
		Description: globBaseDesc,
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string"},
				"hidden":    map[string]any{"type": "boolean"},
				"gitignore": map[string]any{"type": "boolean"},
				"limit": map[string]any{
					"type":    "integer",
					"minimum": 1,
					"maximum": 200,
				},
			},
			"additionalProperties": false,
		},
		PrepareArguments: prepareGlobArguments,
		PrepareLoadout:   prepareSearchLoadout,
		Execute:          executeGlob,
	})
	e.RegisterTool(intent.Tool(globDef))

	// upstream: packages/coding-agent/src/tools/jfind/index.ts:42-46,83
	// omp assigns raw `findDescription` without prompt.render trimming, so it keeps its trailing newline.
	defaultOff := false
	findDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "find",
		Label:         "Find",
		Description:   mustPromptRaw("find.md"),
		DefaultActive: &defaultOff,
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
				"grep_keywords": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"path": map[string]any{"type": "string"},
			},
			"required":             []string{"query", "grep_keywords"},
			"additionalProperties": false,
		},
		Execute: executeFind,
	})
	e.RegisterTool(intent.Tool(findDef))
	toolcmd.RegisterAutoActivate("find", isFindEnabled)
	intent.RegisterSync(e, grepDef, globDef, findDef)
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid, err := ctx.GetSessionID(); err == nil && sid != "" {
			findSessionsMu.Lock()
			findSessions[sid] = &findSessionState{}
			findSessionsMu.Unlock()
		}
		updateFindToolActivation(ctx)
		return nil, nil
	})
	e.OnEvent(sdk.EventModelSelect, func(ctx sdk.Context, _ map[string]any) (any, error) {
		updateFindToolActivation(ctx)
		return nil, nil
	})
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sid, err := ctx.GetSessionID(); err == nil && sid != "" {
			lifecycle.End(sid)
		}
		return nil, nil
	})
}

// prepareGlobArguments mirrors omp's lenientArgValidation handling in glob.ts:207,238-248:
// validates undeclared keys, `pattern`, and declared parameter types/bounds with
// omp's exact summary text, then folds a misplaced `pattern` into `path` before
// PiG validates the closed `additionalProperties: false` schema.
func prepareGlobArguments(params map[string]any) (map[string]any, error) {
	if err := validateGlobArguments(params); err != nil {
		return nil, err
	}
	if _, hasPat := params["pattern"]; !hasPat {
		return params, nil
	}
	merged, err := globSearchPath(params)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if k == "pattern" {
			continue
		}
		out[k] = v
	}
	if merged != "" {
		out["path"] = merged
	}
	return out, nil
}

// isFindEnabled mirrors packages/coding-agent/src/tools/jfind/index.ts:62-69.
// Under "auto" (the default), find is enabled only when a native classifier
// model (ModelTypeClassifier) is available. Under "on", find is enabled whenever
// either a native classifier or the session's active chat model is available.
func isFindEnabled(ctx sdk.Context) bool {
	switch findEnabledSetting(ctx) {
	case "off":
		return false
	case "on":
		_, ok := resolveSessionJudge(ctx, true)
		return ok
	default: // "auto"
		return hasNativeJudge(ctx)
	}
}

func updateFindToolActivation(ctx sdk.Context) {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return
	}
	has := slices.Contains(active, "find")

	sid, _ := ctx.GetSessionID()
	var st *findSessionState
	if sid != "" {
		findSessionsMu.Lock()
		st = findSessions[sid]
		if st == nil {
			st = &findSessionState{}
			findSessions[sid] = st
		}
		if st.autoActivated && !has {
			// The user (or --tools / another command) removed `find` after we activated it.
			st.userRemoved = true
		}
		removed := st.userRemoved
		findSessionsMu.Unlock()
		if removed {
			if !isFindEnabled(ctx) && has {
				removeFindFromActive(ctx, active)
			}
			return
		}
	}

	want := isFindEnabled(ctx)
	if want && has {
		// Already activated (e.g. via toolcmd.RegisterAutoActivate at session_start);
		// record autoActivated so a later user removal is still respected.
		if sid != "" {
			findSessionsMu.Lock()
			if cur := findSessions[sid]; cur != nil {
				cur.autoActivated = true
			}
			findSessionsMu.Unlock()
		}
	} else if want && !has {
		ctx.SetActiveTools(insertFindInOrder(active))
		toolcmd.Sync(ctx)
		if sid != "" {
			findSessionsMu.Lock()
			if cur := findSessions[sid]; cur != nil {
				cur.autoActivated = true
			}
			findSessionsMu.Unlock()
		}
	} else if !want && has {
		removeFindFromActive(ctx, active)
		toolcmd.Sync(ctx)
		if sid != "" {
			findSessionsMu.Lock()
			if cur := findSessions[sid]; cur != nil {
				cur.autoActivated = false
			}
			findSessionsMu.Unlock()
		}
	}
}

// insertFindInOrder places "find" at its omp BUILTIN_TOOLS position (right after
// "grep", or before the first later built-in in toolcmd.BuiltinToolOrder) so
// auto-activation preserves omp's tool order without reordering non-default lists.
// upstream: packages/coding-agent/src/tools/index.ts:622-652
func insertFindInOrder(active []string) []string {
	order := toolcmd.BuiltinToolOrder
	findRank := slices.Index(order, "find")
	if findRank < 0 {
		return append(slices.Clone(active), "find")
	}
	rankOf := make(map[string]int, len(order))
	for i, name := range order {
		rankOf[name] = i
	}
	insertAt := len(active)
	for i, name := range active {
		if r, ok := rankOf[name]; ok && r > findRank {
			insertAt = i
			break
		}
	}
	out := make([]string, 0, len(active)+1)
	out = append(out, active[:insertAt]...)
	out = append(out, "find")
	out = append(out, active[insertAt:]...)
	return out
}

func removeFindFromActive(ctx sdk.Context, active []string) {
	filtered := make([]string, 0, len(active)-1)
	for _, name := range active {
		if name != "find" {
			filtered = append(filtered, name)
		}
	}
	ctx.SetActiveTools(filtered)
}
