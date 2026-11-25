package ast

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Engine wraps the external `ast-grep` / `sg` binary as a child process.
// omp embeds ast-grep via Rust + tree-sitter (crates/ast, crates/natives/src/ast.rs);
// because cgo and third-party modules are forbidden here, we invoke a verified
// `ast-grep` binary (identified via `--version`) with `--json=stream` and translate
// its output to omp's result structures.
//
// CLI contract references (ast-grep upstream https://github.com/ast-grep/ast-grep):
//   - crates/cli/src/run.rs: `ast-grep run --pattern=<PAT> [--rewrite=<FIX>] --lang=<LANG> --json=stream [--stdin | -- <files...>]`
//   - crates/cli/src/run.rs: `RunWithSpecificLang::produce_item` only parses files whose `SgLang::from_path` matches `--lang`;
//     mismatched/unknown extensions are therefore fed via `--stdin` so explicit `--lang` is always honoured.
//   - crates/cli/src/print/json_print.rs: `--json=stream` emits one MatchJSON object per line (available since ast-grep 0.25.0).
//   - crates/cli/src/verify/error.rs & clap: exit 0 = matches found, exit 1 = no matches, exit 2 = clap usage error,
//     exit 8 = pattern failed to compile in the target language ("Cannot parse query as a valid pattern.").
//   - Minimum version 0.40 is required so every language in stockAstGrepLanguages (including YAML >= 0.27.0 and HCL >= 0.39.3)
//     is supported by the binary without triggering clap exit-2 errors.

const (
	// killGrace is the SIGTERM -> SIGKILL escalation window when reaping child processes.
	killGrace = 500 * time.Millisecond
	// maxStderrBytes caps buffered child stderr so runaway diagnostics cannot OOM PiG.
	maxStderrBytes = 256 * 1024
	// minAstGrepMajor / minAstGrepMinor is the minimum CLI version matching
	// stockAstGrepLanguages (YAML arrived in 0.27.0, HCL in 0.39.3; 0.40 covers all 26).
	minAstGrepMajor = 0
	minAstGrepMinor = 40
)

// ErrBinaryNotFound is returned when no verified ast-grep binary (>= 0.40) is on PATH.
var ErrBinaryNotFound = errors.New("ast-grep binary not found on PATH")

// MissingBinaryGuidance is the model-facing message returned when no verified
// ast-grep CLI binary (>= 0.40) is available on PATH.
const MissingBinaryGuidance = "ast-grep (>= 0.40) is not installed or not on PATH (checked `ast-grep` and `sg` via `--version`). " +
	"Install a native ast-grep CLI binary to enable structural AST search and rewrites:\n" +
	"- macOS / Linux (Homebrew): `brew install ast-grep`\n" +
	"- Cargo: `cargo install ast-grep --locked`\n" +
	"- Windows: install the native `ast-grep.exe` (e.g. `cargo install ast-grep --locked`, `winget install ast-grep`, or `scoop install ast-grep`; npm `.cmd` shims are not executed directly for safety)\n" +
	"- npm (Unix): `npm install -g @ast-grep/cli`\n" +
	"- Docs: https://ast-grep.github.io/guide/quick-start.html#installation"

// lookPathFunc allows tests to stub PATH lookups.
var lookPathFunc = exec.LookPath

// astGrepVersionRe matches `ast-grep 0.25.0` or `ast-grep 0.50.0` printed by `<bin> --version`.
var astGrepVersionRe = regexp.MustCompile(`(?i)^ast-grep\s+(\d+)\.(\d+)(?:\.(\d+))?`)

type probedBinary struct {
	ok    bool
	mtime time.Time
	size  int64
}

var (
	probeCacheMu sync.Mutex
	probeCache   = map[string]probedBinary{}
	// versionProbeTimeout bounds `<bin> --version` identity verification. A
	// variable so that a test can shorten it.
	versionProbeTimeout = 5 * time.Second
)

func resetProbeCacheForTests() {
	probeCacheMu.Lock()
	clear(probeCache)
	probeCacheMu.Unlock()
}

// isWindowsBatchShim reports whether path is a Windows .cmd/.bat/.com wrapper that
// would route model-authored patterns through cmd.exe.
func isWindowsBatchShim(p string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".cmd" || ext == ".bat"
}

// verifyAstGrepBinary runs `<path> --version` and accepts the binary only when its
// stdout starts with `ast-grep <major>.<minor>` and version >= 0.40.
// This guarantees we never execute shadow-utils `/usr/bin/sg` on Linux or an
// outdated ast-grep missing languages in stockAstGrepLanguages.
func verifyAstGrepBinary(binPath string) bool {
	if isWindowsBatchShim(binPath) {
		return false
	}
	info, err := os.Stat(binPath)
	if err != nil || info.IsDir() {
		return false
	}
	probeCacheMu.Lock()
	if cached, ok := probeCache[binPath]; ok && cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
		probeCacheMu.Unlock()
		return cached.ok
	}
	probeCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err = cmd.Run()

	ok := false
	if err == nil {
		firstLine := strings.TrimSpace(strings.SplitN(out.String(), "\n", 2)[0])
		if m := astGrepVersionRe.FindStringSubmatch(firstLine); len(m) >= 3 {
			major, err1 := strconv.Atoi(m[1])
			minor, err2 := strconv.Atoi(m[2])
			if err1 == nil && err2 == nil && (major > minAstGrepMajor || (major == minAstGrepMajor && minor >= minAstGrepMinor)) {
				ok = true
			}
		}
	}

	// A probe that ran out of time says nothing about the binary: a loaded
	// machine must not disable the tools for the rest of the process.
	if ctx.Err() != nil {
		return false
	}
	probeCacheMu.Lock()
	probeCache[binPath] = probedBinary{ok: ok, mtime: info.ModTime(), size: info.Size()}
	probeCacheMu.Unlock()
	return ok
}

// findAstGrepBinary returns the resolved path to a verified `ast-grep` (>= 0.40)
// binary on PATH. Checks `ast-grep` first, then `sg`, verifying every candidate
// with `--version` so unrelated binaries (such as Linux shadow-utils `/usr/bin/sg`)
// and Windows `.cmd`/`.bat` shims are rejected.
func findAstGrepBinary() (string, error) {
	for _, name := range []string{"ast-grep", "sg"} {
		p, err := lookPathFunc(name)
		if err != nil || p == "" {
			continue
		}
		if verifyAstGrepBinary(p) {
			return p, nil
		}
	}
	return "", ErrBinaryNotFound
}

// processTracker tracks live child processes per session so one session shutting
// down only reaps its own children and never kills another session's runs.
type processTracker struct {
	mu     sync.Mutex
	bySess map[string]map[int]*trackedProc
}

type trackedProc struct {
	proc *os.Process
	done chan struct{}
}

func newProcessTracker() *processTracker {
	return &processTracker{bySess: make(map[string]map[int]*trackedProc)}
}

func (pt *processTracker) add(session string, proc *os.Process, done chan struct{}) {
	if proc == nil || proc.Pid <= 0 {
		return
	}
	pt.mu.Lock()
	m := pt.bySess[session]
	if m == nil {
		m = make(map[int]*trackedProc)
		pt.bySess[session] = m
	}
	m[proc.Pid] = &trackedProc{proc: proc, done: done}
	pt.mu.Unlock()
}

func (pt *processTracker) remove(session string, pid int) {
	pt.mu.Lock()
	if m := pt.bySess[session]; m != nil {
		delete(m, pid)
		if len(m) == 0 {
			delete(pt.bySess, session)
		}
	}
	pt.mu.Unlock()
}

func (pt *processTracker) killSession(session string) {
	pt.mu.Lock()
	m := pt.bySess[session]
	delete(pt.bySess, session)
	snapshot := make([]*trackedProc, 0, len(m))
	for _, tp := range m {
		snapshot = append(snapshot, tp)
	}
	pt.mu.Unlock()
	for _, tp := range snapshot {
		killProcessTree(tp.proc, tp.done, killGrace)
	}
}

// limitedBuffer caps written bytes at limit while discarding any overflow.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	origLen := len(p)
	if lb.buf.Len() < lb.limit {
		rem := lb.limit - lb.buf.Len()
		if len(p) > rem {
			lb.buf.Write(p[:rem])
			lb.truncated = true
		} else {
			lb.buf.Write(p)
		}
	} else {
		lb.truncated = true
	}
	return origLen, nil
}

func (lb *limitedBuffer) String() string {
	return lb.buf.String()
}

// --- ast-grep --json=stream wire structures ---
// Matches ast-grep CLI 0.25..0.50+ JSON output (upstream: crates/builtins/src/ast_grep.rs:630-698,
// ast-grep crates/cli/src/print/json_print.rs).

type sgPosition struct {
	Line   int `json:"line"`   // 0-based
	Column int `json:"column"` // 0-based
}

type sgByteOffset struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type sgRange struct {
	ByteOffset sgByteOffset `json:"byteOffset"`
	Start      sgPosition   `json:"start"`
	End        sgPosition   `json:"end"`
}

type sgCaptureNode struct {
	Text  string  `json:"text"`
	Range sgRange `json:"range"`
}

type sgMetaVariables struct {
	Single map[string]sgCaptureNode   `json:"single"`
	Multi  map[string][]sgCaptureNode `json:"multi"`
}

type sgMatchJSON struct {
	Text               string           `json:"text"`
	Range              sgRange          `json:"range"`
	File               string           `json:"file"`
	Lines              string           `json:"lines"`
	Replacement        *string          `json:"replacement"`
	ReplacementOffsets *sgByteOffset    `json:"replacementOffsets"`
	Language           string           `json:"language"`
	MetaVariables      *sgMetaVariables `json:"metaVariables"`
}

// astFindMatch mirrors crates/natives/src/ast.rs AstFindMatch (1-based line/column).
type astFindMatch struct {
	Path          string
	Text          string
	ByteStart     int
	ByteEnd       int
	StartLine     int
	StartColumn   int
	EndLine       int
	EndColumn     int
	MetaVariables map[string]string
}

// astReplaceChange mirrors crates/natives/src/ast.rs AstReplaceChange (1-based line/column).
type astReplaceChange struct {
	Path           string
	AbsPath        string
	Before         string
	After          string
	ByteStart      int
	ByteEnd        int
	MatchByteStart int // range.byteOffset.start of Before text (may be wider than replacementOffsets when ast-grep preserves trailing punctuation like `;`)
	MatchByteEnd   int
	DeletedLength  int
	StartLine      int
	StartColumn    int
	EndLine        int
	EndColumn      int
}

// compareAstFindMatch orders matches identically to omp ast-grep.ts:51-60.
func compareAstFindMatch(a, b astFindMatch) int {
	if a.Path != b.Path {
		if a.Path < b.Path {
			return -1
		}
		return 1
	}
	if a.StartLine != b.StartLine {
		return a.StartLine - b.StartLine
	}
	if a.StartColumn != b.StartColumn {
		return a.StartColumn - b.StartColumn
	}
	if a.EndLine != b.EndLine {
		return a.EndLine - b.EndLine
	}
	if a.EndColumn != b.EndColumn {
		return a.EndColumn - b.EndColumn
	}
	if a.ByteStart != b.ByteStart {
		return a.ByteStart - b.ByteStart
	}
	return a.ByteEnd - b.ByteEnd
}

// matchWindow retains at most `capacity` (`skip + limit + 1`) smallest matches
// ordered by compareAstFindMatch while counting total matches and distinct files.
// Mirrors omp's `retainAstFindMatch` (ast-grep.ts:62-76) and `Window` (search/mod.rs:1446-1518).
type matchWindow struct {
	capacity int
	matches  []astFindMatch
	total    int
	files    map[string]struct{}
}

func newMatchWindow(skip, limit int) *matchWindow {
	return &matchWindow{
		capacity: skip + limit + 1,
		matches:  make([]astFindMatch, 0, min(skip+limit+1, 128)),
		files:    make(map[string]struct{}),
	}
}

func (w *matchWindow) add(m astFindMatch) {
	w.total++
	w.files[m.Path] = struct{}{}
	if len(w.matches) < w.capacity {
		w.matches = append(w.matches, m)
		return
	}
	worstIdx := 0
	for i := 1; i < len(w.matches); i++ {
		if compareAstFindMatch(w.matches[i], w.matches[worstIdx]) > 0 {
			worstIdx = i
		}
	}
	if compareAstFindMatch(m, w.matches[worstIdx]) < 0 {
		w.matches[worstIdx] = m
	}
}

func (w *matchWindow) page(skip, limit int) (paged []astFindMatch, limitReached bool) {
	sort.Slice(w.matches, func(i, j int) bool {
		return compareAstFindMatch(w.matches[i], w.matches[j]) < 0
	})
	if skip >= len(w.matches) {
		return nil, false
	}
	visible := w.matches[skip:]
	if len(visible) > limit {
		return visible[:limit], true
	}
	return visible, false
}

// streamAstGrepProcess invokes the verified ast-grep binary, streams its JSON
// stdout incrementally via json.Decoder (handling both `--json=stream` NDJSON and
// JSON arrays of arbitrary match size without buffering whole stdout in memory),
// caps stderr, and fails if the child dies by signal or exits with a CLI/crash
// status (anything other than 0 or 1, or exit 2 clap usage error).
func streamAstGrepProcess(
	ctx context.Context,
	tracker *processTracker,
	session string,
	binary string,
	cwd string,
	args []string,
	stdin io.Reader,
	onMatch func(sgMatchJSON) error,
) (stderr string, exitCode int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	if stdin != nil {
		cmd.Stdin = stdin
	}
	configureProcessGroup(cmd)

	stdoutPipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return "", -1, fmt.Errorf("failed to create stdout pipe for %s: %w", binary, pipeErr)
	}
	stderrBuf := &limitedBuffer{limit: maxStderrBytes}
	cmd.Stderr = stderrBuf

	if startErr := cmd.Start(); startErr != nil {
		return "", -1, fmt.Errorf("failed to start %s: %w", binary, startErr)
	}

	done := make(chan struct{})
	tracker.add(session, cmd.Process, done)

	// Reap the child process group immediately if the caller's context cancels.
	cancelStop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessTree(cmd.Process, done, killGrace)
		case <-cancelStop:
		}
	}()

	// Stream-decode stdout concurrently with child execution so arbitrarily large
	// match payloads never hit a Scanner token cap or buffer the whole stream.
	decodeErr := streamJSONMatches(stdoutPipe, onMatch)
	// Drain any remaining stdout before cmd.Wait so the child does not block on EPIPE.
	_, _ = io.Copy(io.Discard, stdoutPipe)

	waitErr := cmd.Wait()
	close(done)
	close(cancelStop)
	tracker.remove(session, cmd.Process.Pid)
	if ctx.Err() != nil {
		return stderrBuf.String(), -1, fmt.Errorf("ast-grep aborted: %w", ctx.Err())
	}

	exitCode = 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				// Killed by signal (e.g. SIGKILL/SIGSEGV/SIGTERM). Never return partial results.
				return stderrBuf.String(), exitCode, fmt.Errorf("ast-grep process terminated by signal (%v)", waitErr)
			}
		} else {
			return stderrBuf.String(), -1, fmt.Errorf("ast-grep execution failed: %w", waitErr)
		}
	}

	// ast-grep CLI exit codes:
	//   0 = matches found (or completed cleanly)
	//   1 = no matches found
	//   2 = clap argument/usage error (fatal)
	//   8 = pattern failed to compile in this language ("Cannot parse query as a valid pattern."):
	//       non-fatal in mixed-language searches; callers record a per-language parse issue and continue.
	//   other / signal = crash / fatal CLI error
	if exitCode == 2 {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = "invalid ast-grep CLI arguments (exit code 2)"
		}
		return stderrBuf.String(), exitCode, fmt.Errorf("ast-grep CLI usage error: %s", msg)
	}
	if exitCode == 8 && isPatternCompileStderr(stderrBuf.String()) {
		return stderrBuf.String(), exitCode, nil
	}
	if exitCode != 0 && exitCode != 1 {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = fmt.Sprintf("ast-grep exited with code %d", exitCode)
		}
		return stderrBuf.String(), exitCode, fmt.Errorf("ast-grep failed (exit code %d): %s", exitCode, msg)
	}

	if decodeErr != nil {
		return stderrBuf.String(), exitCode, decodeErr
	}
	return stderrBuf.String(), exitCode, nil
}

// isPatternCompileStderr reports whether stderr is ast-grep's exit-8 pattern
// compilation failure ("Cannot parse query as a valid pattern.").
// Reference: https://github.com/ast-grep/ast-grep crates/cli/src/verify/error.rs
// and omp crates/builtins/src/ast_grep.rs:355-366.
func isPatternCompileStderr(stderr string) bool {
	return strings.Contains(stderr, "Cannot parse query as a valid pattern")
}

// formatPatternCompileError turns ast-grep's exit-8 stderr into omp's parse-issue
// shape: `<pat> (<lang>): Invalid pattern: <cause>` (upstream: crates/ast/src/search/mod.rs:801-805).
func formatPatternCompileError(pattern, lang, stderr string) string {
	var causes []string
	for _, rawLine := range strings.Split(stderr, "\n") {
		line := strings.TrimSpace(rawLine)
		if after, ok := strings.CutPrefix(line, "╰▻"); ok {
			if cause := strings.TrimSpace(after); cause != "" {
				causes = append(causes, cause)
			}
		}
	}
	cause := strings.Join(causes, "; ")
	if cause == "" {
		cause = "Cannot parse query as a valid pattern."
	}
	return fmt.Sprintf("%s (%s): Invalid pattern: %s", pattern, lang, cause)
}

// formatPatternPreview bounds a pattern to 48 visible characters for omp's ERROR-node
// diagnostic line (upstream: crates/ast/src/search/mod.rs:1387-1406).
func formatPatternPreview(pattern string) string {
	var b strings.Builder
	width := 0
	for _, ch := range pattern {
		var piece string
		if ch < 0x20 || ch == 0x7f || (ch != ' ' && strings.ContainsRune("\t\n\r\v\f", ch)) {
			piece = fmt.Sprintf("\\u{%x}", ch)
		} else {
			piece = string(ch)
		}
		if width+len([]rune(piece)) > 48 {
			b.WriteRune('…')
			break
		}
		b.WriteString(piece)
		width += len([]rune(piece))
	}
	return b.String()
}

// parseJSONStreamStreaming peeks the first non-whitespace byte and streams either
// a JSON array or newline-delimited JSON objects with json.Decoder.
func streamJSONMatches(r io.Reader, onMatch func(sgMatchJSON) error) error {
	// Read until first non-whitespace byte.
	var firstByte [1]byte
	var lead []byte
	for {
		n, err := r.Read(firstByte[:])
		if n > 0 {
			b := firstByte[0]
			if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
				continue
			}
			lead = []byte{b}
			break
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to read ast-grep stdout: %w", err)
		}
	}
	combined := io.MultiReader(bytes.NewReader(lead), r)
	dec := json.NewDecoder(combined)
	if lead[0] == '[' {
		tok, err := dec.Token()
		if err != nil || tok != json.Delim('[') {
			return fmt.Errorf("failed to decode ast-grep JSON array: %w", err)
		}
		for dec.More() {
			var m sgMatchJSON
			if err := dec.Decode(&m); err != nil {
				return fmt.Errorf("failed to decode ast-grep JSON array item: %w", err)
			}
			if err := onMatch(m); err != nil {
				return err
			}
		}
		_, _ = dec.Token()
		return nil
	}
	for {
		var m sgMatchJSON
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("failed to decode ast-grep JSON stream object: %w", err)
		}
		if err := onMatch(m); err != nil {
			return err
		}
	}
}

// extractMetaVariables converts ast-grep JSON metaVariables to omp's flat map[string]string
// (single captures as text, multi captures as `[a, b]`).
// upstream: crates/natives/src/ast.rs:404-415
func extractMetaVariables(mv *sgMetaVariables) map[string]string {
	if mv == nil || (len(mv.Single) == 0 && len(mv.Multi) == 0) {
		return nil
	}
	out := make(map[string]string, len(mv.Single)+len(mv.Multi))
	for name, capNode := range mv.Single {
		out[name] = capNode.Text
	}
	for name, capNodes := range mv.Multi {
		texts := make([]string, len(capNodes))
		for i, n := range capNodes {
			texts[i] = n.Text
		}
		out[name] = "[" + strings.Join(texts, ", ") + "]"
	}
	return out
}

// parseStderrDiagnostics extracts non-fatal parse/pattern warnings from ast-grep's stderr.
// When ast-grep emits its ERROR-node warning ("Pattern contains an ERROR node..."),
// rewrites it to omp's language-qualified diagnostic (upstream: crates/ast/src/search/mod.rs:1407-1411).
func parseStderrDiagnostics(stderr, pattern, lang string) []string {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return nil
	}
	if strings.Contains(trimmed, "Pattern contains an ERROR node") && lang != "" {
		preview := formatPatternPreview(pattern)
		return []string{
			fmt.Sprintf("%s: pattern is not valid %s on its own (contains an ERROR node); matches may be missing or unexpected", preview, lang),
		}
	}
	var out []string
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		msg := strings.TrimSpace(strings.Join(current, " "))
		current = nil
		if msg == "" {
			return
		}
		if pattern != "" && !strings.Contains(msg, ": ") {
			msg = pattern + ": " + msg
		}
		out = append(out, msg)
	}
	for _, rawLine := range strings.Split(trimmed, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			flush()
			continue
		}
		// Skip decorative tree/help/clap-footer lines.
		if strings.HasPrefix(line, "See also:") || strings.HasPrefix(line, "Help:") ||
			strings.HasPrefix(line, "For more information, try") || strings.HasPrefix(line, "Usage:") {
			continue
		}
		if strings.HasPrefix(line, "╰▻") {
			cause := strings.TrimSpace(strings.TrimPrefix(line, "╰▻"))
			if cause != "" {
				current = append(current, cause)
			}
			continue
		}
		if strings.HasPrefix(line, "✖") || strings.HasPrefix(line, "⚠") {
			continue
		}
		if strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "Warning:") || strings.HasPrefix(line, "ERROR:") {
			flush()
			line = strings.TrimSpace(strings.TrimPrefix(line, "Error:"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "Warning:"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
			if line != "" {
				current = append(current, line)
			}
			continue
		}
		current = append(current, line)
	}
	flush()
	return out
}

// groupCandidatesByLang partitions candidate files by their canonical language while
// preserving deterministic language order. Languages not supported by stock ast-grep
// CLI are returned separately as skippedLangs.
func groupCandidatesByLang(candidates []candidateFile) (langs []string, byLang map[string][]candidateFile, skippedLangs []string) {
	byLang = make(map[string][]candidateFile)
	skippedSet := make(map[string]struct{})
	for _, c := range candidates {
		if !stockAstGrepLanguages[c.lang] {
			skippedSet[c.lang] = struct{}{}
			continue
		}
		if _, ok := byLang[c.lang]; !ok {
			langs = append(langs, c.lang)
		}
		byLang[c.lang] = append(byLang[c.lang], c)
	}
	sort.Strings(langs)
	if len(skippedSet) > 0 {
		skippedLangs = make([]string, 0, len(skippedSet))
		for l := range skippedSet {
			skippedLangs = append(skippedLangs, l)
		}
		sort.Strings(skippedLangs)
	}
	return langs, byLang, skippedLangs
}

// buildCandidateLookup maps resolved file paths (as ast-grep may emit them) back to candidateFile.
func buildCandidateLookup(candidates []candidateFile, cwd string) map[string]candidateFile {
	lookup := make(map[string]candidateFile, len(candidates)*3)
	for _, c := range candidates {
		lookup[c.absPath] = c
		lookup[filepath.ToSlash(c.absPath)] = c
		lookup[canonicalKey(c.absPath)] = c
		if rel, err := filepath.Rel(cwd, c.absPath); err == nil {
			lookup[rel] = c
			lookup[filepath.ToSlash(rel)] = c
		}
	}
	return lookup
}

func resolveCandidateForMatch(matchFile string, cwd string, lookup map[string]candidateFile) (candidateFile, bool) {
	if c, ok := lookup[matchFile]; ok {
		return c, true
	}
	slashed := filepath.ToSlash(matchFile)
	if c, ok := lookup[slashed]; ok {
		return c, true
	}
	abs := matchFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)
	if c, ok := lookup[abs]; ok {
		return c, true
	}
	if c, ok := lookup[canonicalKey(abs)]; ok {
		return c, true
	}
	return candidateFile{}, false
}

// batchCandidates splits a list of file paths into chunks safe for OS argv limits.
const maxFilesPerBatch = 200

func batchCandidates(files []candidateFile) [][]candidateFile {
	if len(files) == 0 {
		return nil
	}
	var batches [][]candidateFile
	for start := 0; start < len(files); start += maxFilesPerBatch {
		end := min(start+maxFilesPerBatch, len(files))
		batches = append(batches, files[start:end])
	}
	return batches
}

// isUTF8Boundary reports whether byte index idx in s is on a valid UTF-8 codepoint boundary.
// Matches Rust str::is_char_boundary used in crates/ast/src/ops.rs:210.
func isUTF8Boundary(s string, idx int) bool {
	if idx < 0 || idx > len(s) {
		return false
	}
	if idx == 0 || idx == len(s) {
		return true
	}
	return utf8.RuneStart(s[idx])
}

// errSourceMismatch signals that byte offsets reported by ast-grep do not match the
// file bytes read for rewriting (e.g. concurrent modification or offset drift).
type errSourceMismatch struct {
	msg string
}

func (e errSourceMismatch) Error() string { return e.msg }

// applyEditsToSource applies byte-offset replacements to source with omp's exact
// deduplication, UTF-8 boundary checks, matched-text verification, and overlap rejection.
// upstream: crates/ast/src/ops.rs:172-221, crates/ast/src/search/mod.rs:1294-1356
func applyEditsToSource(source string, changes []astReplaceChange, displayPath string) (string, []astReplaceChange, error) {
	type editKey struct {
		start  int
		length int
		after  string
	}
	seen := make(map[editKey]struct{}, len(changes))
	deduped := make([]astReplaceChange, 0, len(changes))
	for _, ch := range changes {
		delLen := ch.DeletedLength
		if delLen == 0 && ch.ByteEnd > ch.ByteStart {
			delLen = ch.ByteEnd - ch.ByteStart
		}
		k := editKey{start: ch.ByteStart, length: delLen, after: ch.After}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		ch.DeletedLength = delLen
		deduped = append(deduped, ch)
	}

	sorted := make([]astReplaceChange, len(deduped))
	copy(sorted, deduped)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ByteStart != sorted[j].ByteStart {
			return sorted[i].ByteStart < sorted[j].ByteStart
		}
		if sorted[i].DeletedLength != sorted[j].DeletedLength {
			return sorted[i].DeletedLength < sorted[j].DeletedLength
		}
		return sorted[i].After < sorted[j].After
	})

	prevEnd := 0
	for _, ch := range sorted {
		start := ch.ByteStart
		end := start + ch.DeletedLength
		if start < 0 || end < start || end > len(source) || !isUTF8Boundary(source, start) || !isUTF8Boundary(source, end) {
			return "", nil, errSourceMismatch{
				msg: fmt.Sprintf("%s: computed edit range [%d..%d] is out of bounds or not on a UTF-8 boundary", displayPath, start, end),
			}
		}
		// Verify that the file bytes match what ast-grep reported so a concurrent
		// file change or offset mismatch never corrupts the file. Note: in real
		// ast-grep, `replacementOffsets` can be a strict subspan of `range.byteOffset`
		// (e.g. excluding a trailing `;` that stays intact while `text` includes it),
		// so verify `source[matchStart:matchEnd] == ch.Before` when the full match
		// span is present, and otherwise verify `source[start:end] == ch.Before`.
		if ch.Before != "" {
			mStart, mEnd := start, end
			if ch.MatchByteEnd > ch.MatchByteStart {
				mStart, mEnd = ch.MatchByteStart, ch.MatchByteEnd
			}
			if mStart < 0 || mEnd > len(source) || !isUTF8Boundary(source, mStart) || !isUTF8Boundary(source, mEnd) ||
				start < mStart || end > mEnd || source[mStart:mEnd] != ch.Before {
				found := ""
				if mStart >= 0 && mEnd <= len(source) && mStart <= mEnd {
					found = clipFirstLine(source[mStart:mEnd], 40)
				}
				return "", nil, errSourceMismatch{
					msg: fmt.Sprintf("%s: source changed or offset mismatch at [%d..%d] (expected %q, found %q)",
						displayPath, mStart, mEnd, clipFirstLine(ch.Before, 40), found),
				}
			}
		}
		if !utf8.ValidString(ch.After) {
			return "", nil, fmt.Errorf("%s: replacement text is not valid UTF-8", displayPath)
		}
		if start < prevEnd {
			if displayPath != "" {
				return "", nil, fmt.Errorf("%s: overlapping replacements detected; refine pattern to avoid ambiguous edits", displayPath)
			}
			return "", nil, fmt.Errorf("Overlapping replacements detected; refine pattern to avoid ambiguous edits")
		}
		prevEnd = end
	}

	var b strings.Builder
	b.Grow(len(source))
	cursor := 0
	for _, ch := range sorted {
		b.WriteString(source[cursor:ch.ByteStart])
		b.WriteString(ch.After)
		cursor = ch.ByteStart + ch.DeletedLength
	}
	b.WriteString(source[cursor:])
	return b.String(), deduped, nil
}
