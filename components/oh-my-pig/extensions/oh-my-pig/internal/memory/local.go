package memory

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/memories/index.ts:1456-1584
// upstream: packages/coding-agent/src/memory-backend/local-backend.ts:19-47

const (
	// LearnedLessonsFile is the filename of captured lessons under a project's memory root.
	// upstream: packages/coding-agent/src/memories/index.ts:1456
	LearnedLessonsFile = "learned.md"

	// MaxLearnedLessons caps retained bullet entries (newest-first).
	// upstream: packages/coding-agent/src/memories/index.ts:1458
	MaxLearnedLessons = 100

	// MaxLearnedContentChars bounds a single lesson's content field.
	// upstream: packages/coding-agent/src/memories/index.ts:1460
	MaxLearnedContentChars = 2000

	// MaxLearnedContextChars bounds a single lesson's optional context field.
	// upstream: packages/coding-agent/src/memories/index.ts:1461
	MaxLearnedContextChars = 400

	defaultSummaryInjectionTokenLimit = 5000
)

type localLockEntry struct {
	mu   sync.Mutex
	refs int
}

var (
	localLocksMu sync.Mutex
	localLocks   = map[string]*localLockEntry{}
)

func withLocalFileLock(filePath string, fn func() error) error {
	key := filepath.Clean(filePath)
	localLocksMu.Lock()
	entry := localLocks[key]
	if entry == nil {
		entry = &localLockEntry{}
		localLocks[key] = entry
	}
	entry.refs++
	localLocksMu.Unlock()

	entry.mu.Lock()
	defer func() {
		entry.mu.Unlock()
		localLocksMu.Lock()
		entry.refs--
		if entry.refs == 0 && localLocks[key] == entry {
			delete(localLocks, key)
		}
		localLocksMu.Unlock()
	}()
	return fn()
}

// LocalSaveInput describes one lesson saved to the local `learned.md` backend.
type LocalSaveInput struct {
	Content    string
	Context    string
	Source     string
	Importance float64
}

// LocalSaveResult reports the outcome of SaveLearnedLesson.
type LocalSaveResult struct {
	Backend string
	Stored  int
	Message string
}

// SaveLearnedLesson appends one lesson to `<agentDir>/memories/<encodedCwd>/learned.md`
// (newest-first, deduplicated, 100-entry capped, prompt-injection neutralized,
// secret redacted, per-path serialized, atomic rename).
// upstream: packages/coding-agent/src/memories/index.ts:1502-1563
func SaveLearnedLesson(agentDir, cwd string, input LocalSaveInput) (LocalSaveResult, error) {
	content := NormalizeLearnedText(input.Content, MaxLearnedContentChars)
	if content == "" {
		return LocalSaveResult{
			Backend: BackendLocal,
			Stored:  0,
			Message: "Empty lesson; nothing stored.",
		}, nil
	}
	contextText := ""
	if input.Context != "" {
		contextText = NormalizeLearnedText(input.Context, MaxLearnedContextChars)
	}
	line := "- " + content
	if contextText != "" {
		line = fmt.Sprintf("- %s _(context: %s)_", content, contextText)
	}
	filePath := filepath.Join(MemoryRoot(agentDir, cwd), LearnedLessonsFile)
	if err := withLocalFileLock(filePath, func() error {
		return appendLearnedLine(filePath, line)
	}); err != nil {
		return LocalSaveResult{}, err
	}
	return LocalSaveResult{
		Backend: BackendLocal,
		Stored:  1,
		Message: fmt.Sprintf("Lesson saved to %s.", LearnedLessonsFile),
	}, nil
}

func isLessonBullet(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ")
}

func appendLearnedLine(filePath, line string) error {
	var existing string
	raw, err := os.ReadFile(filePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		existing = string(raw)
	}

	var lines []string
	if existing != "" {
		lines = strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	// Dedupe existing copies in place while preserving headings/prose/blank lines.
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		if isLessonBullet(l) && strings.TrimSpace(l) == line {
			continue
		}
		out = append(out, l)
	}

	// Insert newest lesson at the head of the first bullet run.
	firstBullet := -1
	for i, l := range out {
		if isLessonBullet(l) {
			firstBullet = i
			break
		}
	}
	if firstBullet == -1 {
		out = append(out, line)
	} else {
		out = append(out, "")
		copy(out[firstBullet+1:], out[firstBullet:])
		out[firstBullet] = line
	}

	// Enforce newest-first cap by dropping oldest (bottom-most) bullet lines.
	lessonCount := 0
	for _, l := range out {
		if isLessonBullet(l) {
			lessonCount++
		}
	}
	for i := len(out) - 1; i >= 0 && lessonCount > MaxLearnedLessons; i-- {
		if isLessonBullet(out[i]) {
			out = append(out[:i], out[i+1:]...)
			lessonCount--
		}
	}

	payload := strings.Join(out, "\n") + "\n"
	return writeFileAtomic(filePath, []byte(payload), 0o644)
}

// ReadLearnedLessons reads `learned.md`, neutralizing and redacting every line on read
// so hand-edited files cannot inject delimiters or secrets into the system prompt.
// upstream: packages/coding-agent/src/memories/index.ts:1570-1584
func ReadLearnedLessons(memoryRoot string) string {
	raw, err := os.ReadFile(filepath.Join(memoryRoot, LearnedLessonsFile))
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = RedactMemorySecrets(NeutralizeInjection(line))
	}
	return strings.Join(lines, "\n")
}

// buildLocalInstructions renders `prompts/read-path.md` for `memory.backend == "local"`.
// upstream: packages/coding-agent/src/memories/index.ts:215-242
func buildLocalInstructions(ctx sdk.Context) string {
	root := MemoryRoot(ResolveAgentDir(ctx), ResolveCwd(ctx))
	summaryRaw, _ := os.ReadFile(filepath.Join(root, "memory_summary.md"))
	summary := strings.TrimSpace(string(summaryRaw))
	learned := ReadLearnedLessons(root)
	if summary == "" && learned == "" {
		return ""
	}
	tokenLimit := settingInt(ctx, SettingMemoriesSummaryInjectionLimit, defaultSummaryInjectionTokenLimit)
	if tokenLimit <= 0 {
		tokenLimit = defaultSummaryInjectionTokenLimit
	}
	summaryOut := ""
	if summary != "" {
		summaryOut = strings.TrimSpace(truncateHeadTailByApproxTokens(summary, tokenLimit))
	}
	learnedBudget := tokenLimit - int(math.Ceil(float64(len([]rune(summaryOut)))/4.0))
	if learnedBudget < 0 {
		learnedBudget = 0
	}
	learnedOut := ""
	if learned != "" && learnedBudget > 0 {
		learnedOut = strings.TrimSpace(truncateHeadTailByApproxTokens(learned, learnedBudget))
	}
	if summaryOut == "" && learnedOut == "" {
		return ""
	}
	return renderReadPathTemplate(summaryOut, learnedOut)
}

func renderReadPathTemplate(memorySummary, learned string) string {
	var b strings.Builder
	b.WriteString("# Memory Guidance\n")
	b.WriteString("Root: memory://root\n")
	b.WriteString("Rules:\n")
	if memorySummary != "" {
		b.WriteString("- Read `memory://root/memory_summary.md` first.\n")
		b.WriteString("- If needed, inspect `memory://root/MEMORY.md` and `memory://root/skills/<name>/SKILL.md`.\n")
	}
	b.WriteString("- Memory: heuristics/process context; current repo files, runtime output, user instruction: factual state/final decisions.\n")
	b.WriteString("- Memory changes plan → cite artifact path (e.g. `memory://root/skills/<name>/SKILL.md`) and current-repo evidence.\n")
	b.WriteString("- Memory disagreement with repo state/user instruction → stale; corrected behavior, then update/regenerate memory artifacts.\n")
	b.WriteString("- Confidence only after repository verification; memory alone NEVER sufficient proof.")
	if memorySummary != "" {
		b.WriteString("\nMemory summary:\n")
		b.WriteString(memorySummary)
	}
	if learned != "" {
		b.WriteString("\nLearned lessons (`learn`-captured; durable but may be stale—verify against repo before relying):\n")
		b.WriteString(learned)
	}
	return strings.TrimSpace(b.String())
}
