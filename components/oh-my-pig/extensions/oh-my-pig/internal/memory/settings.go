package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/autolearn"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/settings"
)

// upstream: packages/coding-agent/src/memory-backend/settings.ts:12-40
// upstream: packages/coding-agent/src/memory-backend/global-scope.ts:23-32
// upstream: packages/coding-agent/src/tools/index.ts:788-796,842-857

const (
	BackendOff          = "off"
	BackendLocal        = "local"
	BackendMnemopi      = "mnemopi"
	BackendHindsight    = "hindsight"
	BackendSharpshooter = "sharpshooter"

	SettingMemoryBackend = "memory.backend"

	// Mnemopi settings
	SettingMnemopiDbPath          = "mnemopi.dbPath"
	SettingMnemopiBank            = "mnemopi.bank"
	SettingMnemopiBankID          = "mnemopi.bankId" // alias supported for ergonomics
	SettingMnemopiScoping         = "mnemopi.scoping"
	SettingMnemopiRecallLimit     = "mnemopi.recallLimit"
	SettingMnemopiNoEmbeddings    = "mnemopi.noEmbeddings"
	SettingMnemopiEmbeddingModel  = "mnemopi.embeddingModel"
	SettingMnemopiEmbeddingAPIURL = "mnemopi.embeddingApiUrl"
	SettingMnemopiEmbeddingAPIKey = "mnemopi.embeddingApiKey"
	SettingMnemopiEmbeddingPrefix = "mnemopi.embeddingQueryPrefix"
	SettingMnemopiInjectionLimit  = "mnemopi.injectionTokenLimit"

	// Hindsight settings
	SettingHindsightAPIURL           = "hindsight.apiUrl"
	SettingHindsightAPIToken         = "hindsight.apiToken"
	SettingHindsightAPIKey           = "hindsight.apiKey" // alias supported for ergonomics
	SettingHindsightBankID           = "hindsight.bankId"
	SettingHindsightBankIDPrefix     = "hindsight.bankIdPrefix"
	SettingHindsightScoping          = "hindsight.scoping"
	SettingHindsightGlobalScope      = "hindsight.globalScope"
	SettingHindsightBankMission      = "hindsight.bankMission"
	SettingHindsightRetainMission    = "hindsight.retainMission"
	SettingHindsightRetainContext    = "hindsight.retainContext"
	SettingHindsightRecallBudget     = "hindsight.recallBudget"
	SettingHindsightRecallMaxTokens  = "hindsight.recallMaxTokens"
	SettingHindsightRecallTypes      = "hindsight.recallTypes"
	SettingHindsightRequestTimeoutMs = "hindsight.requestTimeoutMs"
	SettingHindsightRecallTimeoutMs  = "hindsight.recallTimeoutMs"
	SettingHindsightReflectTimeoutMs = "hindsight.reflectTimeoutMs"
	SettingHindsightRetainTimeoutMs  = "hindsight.retainTimeoutMs"

	// Local memories setting
	SettingMemoriesSummaryInjectionLimit = "memories.summaryInjectionTokenLimit"
)

// Setting reads a raw setting value for ctx.
// Wired by wiring.go -> settings.Any. Defaults to nil (all backends off).
var Setting func(ctx sdk.Context, key string) any

// IsSubagent reports whether ctx belongs to a subagent session (taskDepth > 0).
// Wired by wiring.go -> agents.IsSubagent. Defaults to nil (false).
var IsSubagent func(ctx sdk.Context) bool

// ExplicitTools returns a session's explicit caller/frontmatter tool whitelist.
// Wired by wiring.go -> agents.ExplicitTools. Defaults to nil.
var ExplicitTools func(ctx sdk.Context) []string

// AgentDirOverride overrides agent directory resolution (primarily for tests).
var AgentDirOverride func() string

// CwdOverride overrides working directory resolution when sdk.Context is zero (primarily for tests).
var CwdOverride func() string

// RefreshSkills is invoked after `learn` creates/updates a managed skill when non-nil.
// Falls back to `autolearn.RefreshSkills` if nil.
var RefreshSkills func(ctx sdk.Context) error

func settingString(ctx sdk.Context, key, def string) string {
	if Setting == nil {
		return def
	}
	if s, ok := Setting(ctx, key).(string); ok {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed
		}
		return ""
	}
	return def
}

func settingStringRaw(ctx sdk.Context, key string) (string, bool) {
	if Setting == nil {
		return "", false
	}
	s, ok := Setting(ctx, key).(string)
	return s, ok
}

func settingBool(ctx sdk.Context, key string, def bool) bool {
	if Setting == nil {
		return def
	}
	if b, ok := Setting(ctx, key).(bool); ok {
		return b
	}
	return def
}

func settingInt(ctx sdk.Context, key string, def int) int {
	if Setting == nil {
		return def
	}
	switch v := Setting(ctx, key).(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return int(v)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}

func settingStringSlice(ctx sdk.Context, key string, def []string) []string {
	if Setting == nil {
		return def
	}
	switch v := Setting(ctx, key).(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return def
	}
}

// BackendFor returns the active `memory.backend` for ctx, defaulting to `"off"`.
// upstream: packages/coding-agent/src/memory-backend/settings.ts:12-17
func BackendFor(ctx sdk.Context) string {
	b := strings.ToLower(settingString(ctx, SettingMemoryBackend, BackendOff))
	switch b {
	case BackendLocal, BackendMnemopi, BackendHindsight, BackendSharpshooter:
		return b
	default:
		return BackendOff
	}
}

func isHindsightURLConfigured(ctx sdk.Context) bool {
	return strings.TrimSpace(settingString(ctx, SettingHindsightAPIURL, "")) != ""
}

// RetainRecallReflectActive reports whether `retain`, `recall`, and `reflect`
// should be auto-activated for ctx.
// upstream: packages/coding-agent/src/tools/memory-{retain,recall,reflect}.ts (`createIf`)
func RetainRecallReflectActive(ctx sdk.Context) bool {
	switch BackendFor(ctx) {
	case BackendMnemopi:
		return true
	case BackendHindsight:
		return isHindsightURLConfigured(ctx)
	default:
		return false
	}
}

// MemoryEditActive reports whether `memory_edit` should be auto-activated for ctx.
// upstream: packages/coding-agent/src/tools/memory-edit.ts:33-37
func MemoryEditActive(ctx sdk.Context) bool {
	return BackendFor(ctx) == BackendMnemopi
}

// LearnActive reports whether `learn` should be auto-activated for ctx.
// Requires `autolearn.enabled == true`, a live memory backend (`mnemopi`,
// `hindsight` with non-empty `hindsight.apiUrl`, or `local`), and either
// top-level (!IsSubagent) or an explicit tool list containing `learn`.
// upstream: packages/coding-agent/src/tools/learn.ts:71-77
// upstream: packages/coding-agent/src/tools/index.ts:788-796,851-857
func LearnActive(ctx sdk.Context) bool {
	if !settingBool(ctx, autolearn.SettingEnabled, false) {
		return false
	}
	switch BackendFor(ctx) {
	case BackendMnemopi, BackendLocal:
	case BackendHindsight:
		if !isHindsightURLConfigured(ctx) {
			return false
		}
	default:
		return false
	}
	if IsSubagent != nil && IsSubagent(ctx) {
		if ExplicitTools != nil && slices.Contains(ExplicitTools(ctx), "learn") {
			return true
		}
		return false
	}
	return true
}

// IsGlobalMemoryScopeAvailable reports whether `retain`/`learn` may accept
// `scope: "global"` for ctx's current backend and scoping settings.
// upstream: packages/coding-agent/src/memory-backend/global-scope.ts:23-32
func IsGlobalMemoryScopeAvailable(ctx sdk.Context) bool {
	switch BackendFor(ctx) {
	case BackendMnemopi:
		scoping := strings.ToLower(settingString(ctx, SettingMnemopiScoping, "per-project"))
		return scoping != "per-project"
	case BackendHindsight:
		if !isHindsightURLConfigured(ctx) {
			return false
		}
		if !settingBool(ctx, SettingHindsightGlobalScope, false) {
			return false
		}
		scoping := strings.ToLower(settingString(ctx, SettingHindsightScoping, "per-project-tagged"))
		return scoping != "per-project"
	default:
		return false
	}
}

// ResolveAgentDir returns the agent directory for ctx.
func ResolveAgentDir(ctx sdk.Context) string {
	if fn := AgentDirOverride; fn != nil {
		if d := strings.TrimSpace(fn()); d != "" {
			return d
		}
	}
	var zero sdk.Context
	if ctx != zero {
		if d := settings.AgentDirForContext(ctx); d != "" {
			return d
		}
	}
	return autolearn.DefaultAgentDir()
}

// ResolveCwd returns the working directory for ctx.
func ResolveCwd(ctx sdk.Context) string {
	if fn := CwdOverride; fn != nil {
		if d := strings.TrimSpace(fn()); d != "" {
			return d
		}
	}
	var zero sdk.Context
	if ctx != zero {
		var cwd string
		func() {
			defer func() { _ = recover() }()
			cwd = strings.TrimSpace(ctx.Cwd())
		}()
		if cwd != "" {
			return cwd
		}
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		return cwd
	}
	return "."
}

func sessionIDForContext(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var sid string
	func() {
		defer func() { _ = recover() }()
		if id, err := ctx.GetSessionID(); err == nil {
			sid = strings.TrimSpace(id)
		}
	}()
	return sid
}

func toolCallIDForContext(ctx sdk.Context) string {
	var zero sdk.Context
	if ctx == zero {
		return ""
	}
	var id string
	func() {
		defer func() { _ = recover() }()
		id = strings.TrimSpace(ctx.ToolCallID())
	}()
	return id
}

// NormalizeScopeCwd lowercases paths on Windows so case-insensitive paths share one key.
// upstream: packages/coding-agent/src/memories/storage.ts:55-57
func NormalizeScopeCwd(cwd string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(cwd)
	}
	return cwd
}

// EncodeProjectPath encodes cwd into omp's memory-root directory name `--<encoded>--`.
// upstream: packages/coding-agent/src/memories/index.ts:1586-1588
func EncodeProjectPath(cwd string) string {
	trimmed := strings.TrimLeft(NormalizeScopeCwd(cwd), `/\`)
	var b strings.Builder
	b.Grow(len(trimmed) + 4)
	b.WriteString("--")
	for i := range len(trimmed) {
		switch trimmed[i] {
		case '/', '\\', ':':
			b.WriteByte('-')
		default:
			b.WriteByte(trimmed[i])
		}
	}
	b.WriteString("--")
	return b.String()
}

// MemoryRoot returns `<agentDir>/memories/<encodedCwd>`.
// upstream: packages/coding-agent/src/memories/index.ts:1444-1446
func MemoryRoot(agentDir, cwd string) string {
	return filepath.Join(agentDir, "memories", EncodeProjectPath(cwd))
}

// FormatCurrentTime formats UTC time as `YYYY-MM-DD HH:MM` for recall preambles.
// upstream: packages/coding-agent/src/hindsight/content.ts:78-85
func FormatCurrentTime(now time.Time) string {
	return now.UTC().Format("2006-01-02 15:04")
}

// expandTilde expands a leading `~` or `~/` to the user's home directory.
func expandTilde(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if p[1] == '/' || p[1] == '\\' {
		return filepath.Join(home, p[2:])
	}
	return p
}

// writeFileAtomic writes data to a sibling temp file in the target directory,
// syncs it, and renames it atomically over path.
func writeFileAtomic(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomic rename to %s: %w", targetPath, err)
	}
	cleanup = false
	return nil
}

// truncateApproxTokens truncates text to approximately tokenLimit*4 runes with a trailing `…`.
// upstream: packages/coding-agent/src/mnemopi/config.ts:306-310
func truncateApproxTokens(text string, tokenLimit int) string {
	if tokenLimit <= 0 {
		return ""
	}
	maxChars := tokenLimit * 4
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars <= 1 {
		return "…"
	}
	return strings.TrimRight(string(runes[:maxChars-1]), " \t\n\r") + "…"
}

// truncateHeadTailByApproxTokens truncates local memory text preserving 60% head and 40% tail.
// upstream: packages/coding-agent/src/memories/index.ts:1384-1391
func truncateHeadTailByApproxTokens(text string, tokenLimit int) string {
	if tokenLimit <= 0 {
		return ""
	}
	maxChars := tokenLimit * 4
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	head := int(float64(maxChars) * 0.6)
	tail := maxChars - head
	return string(runes[:head]) + "\n\n...[truncated]...\n\n" + string(runes[len(runes)-tail:])
}
