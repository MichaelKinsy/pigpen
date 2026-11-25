package agents

import (
	"context"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// upstream: packages/coding-agent/src/task/name-generator.ts, output-manager.ts, parallel.ts

// PinnedHUDToggleID and AdvisorTranscriptStem are reserved output IDs that cannot
// be allocated to subagents.
// upstream: packages/coding-agent/src/task/output-manager.ts:18,42
const (
	AdvisorTranscriptStem = "__advisor"
	PinnedHUDToggleID     = "@omp:toggle-pinned-hud"
)

// Concise memorable adjectives and nouns in omp's AdjectiveNoun format.
// upstream: packages/coding-agent/src/task/name-generator.ts
var nameAdjectives = []string{
	"able", "active", "agile", "alert", "amber", "ample", "apt", "arctic",
	"bold", "brave", "breezy", "brief", "bright", "brisk", "calm", "candid",
	"clean", "clear", "clever", "cobalt", "cool", "coral", "crisp", "curious",
	"dapper", "deft", "direct", "eager", "earnest", "easy", "fair", "fast",
	"firm", "fleet", "focal", "fond", "frank", "fresh", "gentle", "glad",
	"golden", "grand", "green", "hale", "happy", "hardy", "honest", "humble",
	"ivory", "jade", "jaunty", "jolly", "keen", "kind", "lively", "lucid",
	"lunar", "merry", "mild", "mint", "modest", "neat", "nimble", "noble",
	"olive", "open", "patient", "peppy", "plain", "plucky", "poised", "polite",
	"prime", "prompt", "proud", "pure", "quick", "quiet", "rapid", "ready",
	"regal", "robust", "rosy", "ruby", "sage", "serene", "sharp", "shiny",
	"silent", "silver", "simple", "sleek", "smart", "smooth", "snappy", "solar",
	"solid", "sound", "spry", "stable", "steady", "stout", "sunny", "sure",
	"swift", "tactful", "teal", "tidy", "timely", "true", "upbeat", "valiant",
	"vibrant", "vivid", "warm", "watchful", "wise", "witty", "zealous", "zesty",
}

var nameNouns = []string{
	"albatross", "alpaca", "antelope", "badger", "beaver", "bison", "bobcat", "camel",
	"capybara", "cardinal", "caribou", "cheetah", "chipmunk", "cobra", "condor", "cougar",
	"coyote", "crane", "crow", "dolphin", "dove", "dragon", "duck", "eagle",
	"egret", "elk", "ermine", "falcon", "ferret", "finch", "flamingo", "fox",
	"gazelle", "gecko", "gibbon", "giraffe", "gopher", "grouse", "gull", "hare",
	"hawk", "hedgehog", "heron", "hornet", "husky", "ibex", "ibis", "iguana",
	"impala", "jackal", "jaguar", "jay", "kestrel", "kiwi", "koala", "lemur",
	"leopard", "lion", "lizard", "llama", "lobster", "loon", "lynx", "macaw",
	"magpie", "mallard", "manatee", "marmot", "marten", "meerkat", "mink", "moose",
	"narwhal", "newt", "ocelot", "octopus", "okapi", "oriole", "osprey", "otter",
	"owl", "panda", "panther", "parrot", "pelican", "penguin", "petrel", "pheasant",
	"pika", "plover", "puffin", "puma", "quail", "quokka", "rabbit", "raccoon",
	"raven", "robin", "salmon", "seal", "serval", "shark", "shrike", "sloth",
	"sparrow", "spider", "squid", "starling", "stork", "swallow", "swan", "swift",
	"tapir", "tern", "thrush", "tiger", "toucan", "trout", "turtle", "viper",
	"vulture", "wallaby", "walrus", "weasel", "whale", "wolf", "wombat", "wren",
}

var (
	nameGenMu   sync.Mutex
	usedNames   = map[string]bool{}
	nameCounter int
)

func capitalizeWord(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// GenerateTaskName generates a unique two-word AdjectiveNoun identifier.
// upstream: packages/coding-agent/src/task/name-generator.ts:1537
func GenerateTaskName() string {
	nameGenMu.Lock()
	defer nameGenMu.Unlock()
	for range 50 {
		adj := nameAdjectives[rand.IntN(len(nameAdjectives))]
		noun := nameNouns[rand.IntN(len(nameNouns))]
		candidate := capitalizeWord(adj) + capitalizeWord(noun)
		if !usedNames[candidate] {
			usedNames[candidate] = true
			return candidate
		}
	}
	for _, adj := range nameAdjectives {
		for _, noun := range nameNouns {
			candidate := capitalizeWord(adj) + capitalizeWord(noun)
			if !usedNames[candidate] {
				usedNames[candidate] = true
				return candidate
			}
		}
	}
	for {
		candidate := "Task" + itoa(int64(nameCounter))
		nameCounter++
		if !usedNames[candidate] {
			usedNames[candidate] = true
			return candidate
		}
	}
}

// ResetTaskNames clears the generated name set (for tests).
func ResetTaskNames() {
	nameGenMu.Lock()
	usedNames = map[string]bool{}
	nameCounter = 0
	nameGenMu.Unlock()
}

var (
	nonAgentIDChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
	digitsOnlyRE    = regexp.MustCompile(`^\d+$`)
)

// SanitizeAgentID extracts a valid agent ID from a user-supplied label:
// keeps [A-Za-z0-9_-], max 48 chars; empty if nothing survives.
// upstream: packages/coding-agent/src/task/name-generator.ts:1573
func SanitizeAgentID(value string) string {
	cleaned := nonAgentIDChars.ReplaceAllString(strings.TrimSpace(value), "")
	if len(cleaned) > 48 {
		cleaned = cleaned[:48]
	}
	return cleaned
}

// OutputManager allocates unique session-scoped subagent IDs, seeding from disk
// on first use so resumed sessions never overwrite prior outputs.
// upstream: packages/coding-agent/src/task/output-manager.ts:26
type OutputManager struct {
	mu              sync.Mutex
	initialized     bool
	taken           map[string]bool
	getArtifactsDir func() string
	parentPrefix    string
	crossInstance   bool
}

// NewOutputManager creates a session-scoped ID allocator.
func NewOutputManager(getArtifactsDir func() string, parentPrefix string, crossInstance bool) *OutputManager {
	taken := map[string]bool{
		AdvisorTranscriptStem: true,
		PinnedHUDToggleID:     true,
	}
	return &OutputManager{
		taken:           taken,
		getArtifactsDir: getArtifactsDir,
		parentPrefix:    parentPrefix,
		crossInstance:   crossInstance,
	}
}

func (m *OutputManager) seedFromDiskLocked() {
	if m.initialized {
		return
	}
	m.initialized = true
	if m.getArtifactsDir == nil {
		return
	}
	dir := m.getArtifactsDir()
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := ""
	if m.parentPrefix != "" {
		prefix = m.parentPrefix + "."
	}
	for _, entry := range entries {
		name := entry.Name()
		var rest string
		switch {
		case strings.HasSuffix(name, ".jsonl"):
			rest = strings.TrimSuffix(name, ".jsonl")
		case strings.HasSuffix(name, ".md"):
			rest = strings.TrimSuffix(name, ".md")
		default:
			continue
		}
		if prefix != "" {
			if !strings.HasPrefix(rest, prefix) {
				continue
			}
			rest = strings.TrimPrefix(rest, prefix)
		}
		seg := rest
		if dot := strings.IndexByte(rest, '.'); dot >= 0 {
			seg = rest[:dot]
		}
		if seg != "" {
			m.taken[seg] = true
		}
	}
}

// Reserve marks externally known IDs as taken in this allocator's scope.
// upstream: packages/coding-agent/src/task/output-manager.ts:98
func (m *OutputManager) Reserve(ids []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seedFromDiskLocked()
	prefix := ""
	if m.parentPrefix != "" {
		prefix = m.parentPrefix + "."
	}
	for _, id := range ids {
		rest := id
		if prefix != "" {
			if !strings.HasPrefix(rest, prefix) {
				continue
			}
			rest = strings.TrimPrefix(rest, prefix)
		}
		seg := rest
		if dot := strings.IndexByte(rest, '.'); dot >= 0 {
			seg = rest[:dot]
		}
		if seg != "" {
			m.taken[seg] = true
		}
	}
}

// SetCrossInstance updates whether numeric-only names get an `Agent` prefix
// (matching omp's live `cfgTaskCrossInstance.get(this.#settings)` check in
// `output-manager.ts:121`).
func (m *OutputManager) SetCrossInstance(enabled bool) {
	m.mu.Lock()
	m.crossInstance = enabled
	m.mu.Unlock()
}

// Allocate returns a unique ID ("Anna", then "Anna-2", "Anna-3", ...),
// nested under parentPrefix when set ("Parent.Child").
// upstream: packages/coding-agent/src/task/output-manager.ts:119
func (m *OutputManager) Allocate(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seedFromDiskLocked()
	base := id
	if base == "" {
		base = GenerateTaskName()
	}
	if m.crossInstance && digitsOnlyRE.MatchString(base) {
		base = "Agent" + base
	}
	candidate := base
	for n := 2; m.taken[candidate]; n++ {
		candidate = base + "-" + itoa(int64(n))
	}
	m.taken[candidate] = true
	if m.parentPrefix != "" {
		return m.parentPrefix + "." + candidate
	}
	return candidate
}

// Semaphore is a resizable, cancellable counting semaphore matching omp's Semaphore.
// max <= 0 means unbounded (every Acquire returns immediately).
// upstream: packages/coding-agent/src/task/parallel.ts:140
type Semaphore struct {
	mu      sync.Mutex
	max     int // 0 means unlimited
	current int
	waiters []chan struct{}
}

// NewSemaphore creates a Semaphore with ceiling max (<=0 means unlimited).
func NewSemaphore(max int) *Semaphore {
	if max < 0 {
		max = 0
	}
	return &Semaphore{max: max}
}

// Acquire blocks until a slot is available or ctx is cancelled.
func (s *Semaphore) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.max == 0 || s.current < s.max {
		s.current++
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	s.waiters = append(s.waiters, ch)
	s.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		// Remove ch if still queued; otherwise a Release already handed us a slot.
		removed := false
		for i, w := range s.waiters {
			if w == ch {
				s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			// A concurrent Release popped ch and incremented current for us; return the slot.
			s.releaseLocked()
		}
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Release returns a slot and wakes the next queued waiter if under the ceiling.
func (s *Semaphore) Release() {
	s.mu.Lock()
	s.releaseLocked()
	s.mu.Unlock()
}

func (s *Semaphore) releaseLocked() {
	if s.current > 0 {
		s.current--
	}
	s.admitLocked()
}

func (s *Semaphore) admitLocked() {
	for len(s.waiters) > 0 && (s.max == 0 || s.current < s.max) {
		ch := s.waiters[0]
		s.waiters = s.waiters[1:]
		s.current++
		close(ch)
	}
}

// Resize updates the concurrency ceiling in place and admits queued waiters that now fit.
func (s *Semaphore) Resize(max int) {
	if max < 0 {
		max = 0
	}
	s.mu.Lock()
	s.max = max
	s.admitLocked()
	s.mu.Unlock()
}

// artifactsDirForSession derives the artifacts directory for a session file (`<file without .jsonl>`).
func artifactsDirForSession(sessionFile string) string {
	if sessionFile == "" {
		return ""
	}
	if strings.HasSuffix(sessionFile, ".jsonl") {
		return strings.TrimSuffix(sessionFile, ".jsonl")
	}
	return filepath.Clean(sessionFile) + ".artifacts"
}
