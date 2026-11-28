package modes

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/modes/loop-limit.ts
// upstream: packages/coding-agent/src/modes/loop-condition.ts
// upstream: packages/coding-agent/src/tools/shell-tokenize.ts (readShellWord)
// upstream: packages/coding-agent/src/modes/interactive-mode.ts (loop mode)

const (
	defaultLoopConditionTimeoutMs = 30_000
	defaultLoopResubmitDelay      = 800 * time.Millisecond // upstream: interactive-mode.ts:3541
	loopUsageMessage              = "Usage: /loop [count|duration] [--while|--until '<command>'] [prompt]. Examples: /loop 10, /loop 10m, /loop 20 --until 'bun test' fix the failing tests."
)

// LoopLimitConfig is the parsed iteration or duration cap for `/loop`.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:4-12
type LoopLimitConfig struct {
	Kind       string // "iterations" | "duration"
	Iterations int
	DurationMs int64
}

// LoopLimitRuntime tracks remaining iterations or the deadline while looping.
type LoopLimitRuntime struct {
	Kind       string // "iterations" | "duration"
	Initial    int
	Remaining  int
	DurationMs int64
	DeadlineMs int64
}

// LoopConditionConfig is the `--while` / `--until` shell predicate for `/loop`.
type LoopConditionConfig struct {
	Command string
	Until   bool // false = --while (exit 0 continues), true = --until (exit 1 continues)
}

// ParsedLoopArgs holds the parsed components of a `/loop` argument string.
type ParsedLoopArgs struct {
	Limit     *LoopLimitConfig
	Condition *LoopConditionConfig
	Prompt    string
}

// LoopConditionVerdict is the outcome of evaluating a `--while`/`--until` command.
// upstream: packages/coding-agent/src/modes/loop-condition.ts:25-33
type LoopConditionVerdict struct {
	Kind    string // "continue" | "halt" | "error" | "aborted"
	Message string
}

var (
	loopTimeUnitsMs = map[string]int64{
		"s":       1_000,
		"sec":     1_000,
		"secs":    1_000,
		"second":  1_000,
		"seconds": 1_000,
		"m":       60_000,
		"min":     60_000,
		"mins":    60_000,
		"minute":  60_000,
		"minutes": 60_000,
		"h":       3_600_000,
		"hr":      3_600_000,
		"hrs":     3_600_000,
		"hour":    3_600_000,
		"hours":   3_600_000,
	}
	loopStartsLikeLimitRE = regexp.MustCompile(`^[+-]?\d`)
	loopDigitsOnlyRE      = regexp.MustCompile(`^\d+$`)
	loopCompoundRE        = regexp.MustCompile(`^(?:\d+[a-z]+)+$`)
	loopCompoundSegRE     = regexp.MustCompile(`(\d+)([a-z]+)`)
	loopFlagNameRE        = regexp.MustCompile(`^(--[a-z][a-z-]*)(?:[\s=]|$)`)
)

// ParseLoopArgs parses `/loop [count|duration] [--while|--until '<cmd>'] [prompt]`.
// Returns an error string matching omp on invalid limit or flag syntax.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:63-78
func ParseLoopArgs(args string) (ParsedLoopArgs, string) {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ParsedLoopArgs{}, ""
	}
	limit, afterLimit, errMsg := takeLoopLimit(trimmed)
	if errMsg != "" {
		return ParsedLoopArgs{}, errMsg
	}
	cond, rest, errMsg := takeLoopCondition(afterLimit)
	if errMsg != "" {
		return ParsedLoopArgs{}, errMsg
	}
	return ParsedLoopArgs{
		Limit:     limit,
		Condition: cond,
		Prompt:    strings.TrimSpace(rest),
	}, ""
}

func takeLoopLimit(input string) (*LoopLimitConfig, string, string) {
	firstSpace := strings.IndexAny(input, " \t\r\n")
	firstToken := input
	rest := ""
	if firstSpace >= 0 {
		firstToken = input[:firstSpace]
		rest = strings.TrimSpace(input[firstSpace+1:])
	}
	token := strings.ToLower(firstToken)
	if !loopStartsLikeLimitRE.MatchString(token) {
		return nil, input, ""
	}

	if loopDigitsOnlyRE.MatchString(token) {
		if rest != "" {
			unitEnd := strings.IndexAny(rest, " \t\r\n")
			unitToken := rest
			afterUnit := ""
			if unitEnd >= 0 {
				unitToken = rest[:unitEnd]
				afterUnit = strings.TrimSpace(rest[unitEnd+1:])
			}
			if unitMs, ok := loopTimeUnitsMs[strings.ToLower(unitToken)]; ok {
				lim, errMsg := makeLoopDuration(token, unitMs)
				if errMsg != "" {
					return nil, "", errMsg
				}
				return lim, afterUnit, ""
			}
		}
		lim, errMsg := makeLoopIterations(token)
		if errMsg != "" {
			return nil, "", errMsg
		}
		return lim, rest, ""
	}

	if lim, matched, errMsg := parseCompoundDuration(token); matched {
		if errMsg != "" {
			return nil, "", errMsg
		}
		return lim, rest, ""
	}

	return nil, "", loopUsageMessage
}

func makeLoopIterations(amountText string) (*LoopLimitConfig, string) {
	n, err := strconv.Atoi(amountText)
	if err != nil || n <= 0 {
		return nil, "Loop count must be a positive integer."
	}
	return &LoopLimitConfig{Kind: "iterations", Iterations: n}, ""
}

func makeLoopDuration(amountText string, unitMs int64) (*LoopLimitConfig, string) {
	n, err := strconv.ParseInt(amountText, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/unitMs {
		return nil, "Loop duration must be positive."
	}
	return &LoopLimitConfig{Kind: "duration", DurationMs: n * unitMs}, ""
}

func parseCompoundDuration(token string) (*LoopLimitConfig, bool, string) {
	if !loopCompoundRE.MatchString(token) {
		return nil, false, ""
	}
	matches := loopCompoundSegRE.FindAllStringSubmatch(token, -1)
	if len(matches) == 0 {
		return nil, false, ""
	}
	var totalMs int64
	for _, m := range matches {
		unitMs, ok := loopTimeUnitsMs[m[2]]
		if !ok {
			return nil, true, "Loop duration unit must be seconds, minutes, or hours."
		}
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || n <= 0 || n > math.MaxInt64/unitMs {
			return nil, true, "Loop duration must be positive."
		}
		totalMs += n * unitMs
	}
	if totalMs <= 0 {
		return nil, true, "Loop duration must be positive."
	}
	return &LoopLimitConfig{Kind: "duration", DurationMs: totalMs}, true, ""
}

func takeLoopCondition(input string) (*LoopConditionConfig, string, string) {
	rest := strings.TrimSpace(input)
	var cond *LoopConditionConfig

	for strings.HasPrefix(rest, "--") {
		m := loopFlagNameRE.FindStringSubmatch(rest)
		var name string
		if m != nil {
			name = m[1]
		}
		var until bool
		switch name {
		case "--while":
			until = false
		case "--until":
			until = true
		default:
			bad := name
			if bad == "" {
				bad = strings.Fields(rest)[0]
			}
			return nil, "", fmt.Sprintf("Unknown /loop flag %s. %s", bad, loopUsageMessage)
		}
		if cond != nil {
			return nil, "", "Use only one of --while or --until."
		}
		afterName := rest[len(name):]
		valueText := afterName
		if strings.HasPrefix(valueText, "=") {
			valueText = valueText[1:]
		}
		word, afterWord, state := ReadShellWord(valueText)
		if state == shellWordUnterminated {
			return nil, "", fmt.Sprintf("%s has an unterminated quote.", name)
		}
		if state == shellWordEmpty || strings.TrimSpace(word) == "" || strings.HasPrefix(strings.TrimSpace(valueText), "-") {
			return nil, "", fmt.Sprintf("%s needs a shell command. Quote it when it contains spaces: /loop %s 'bun test'.", name, name)
		}
		cond = &LoopConditionConfig{Command: strings.TrimSpace(word), Until: until}
		rest = afterWord
	}
	return cond, rest, ""
}

type shellWordState int

const (
	shellWordOK shellWordState = iota
	shellWordEmpty
	shellWordUnterminated
)

// ReadShellWord reads one shell-quoted/escaped word from the start of text.
// upstream: packages/coding-agent/src/tools/shell-tokenize.ts:944-996
func ReadShellWord(text string) (value, rest string, state shellWordState) {
	i := 0
	for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
		i++
	}
	if i >= len(text) {
		return "", "", shellWordEmpty
	}
	var b strings.Builder
	inSingle := false
	inDouble := false
	for ; i < len(text); i++ {
		ch := text[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
				continue
			}
			b.WriteByte(ch)
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(text) {
				next := text[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					b.WriteByte(next)
					i++
					continue
				}
			}
			if ch == '"' {
				inDouble = false
				continue
			}
			b.WriteByte(ch)
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '\\':
			if i+1 < len(text) {
				b.WriteByte(text[i+1])
				i++
			}
		case ' ', '\t', '\n', '\r':
			return b.String(), strings.TrimSpace(text[i:]), shellWordOK
		default:
			b.WriteByte(ch)
		}
	}
	if inSingle || inDouble {
		return "", "", shellWordUnterminated
	}
	return b.String(), "", shellWordOK
}

// CreateLoopLimitRuntime initializes a runtime counter/deadline from a parsed config.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:188-197
func CreateLoopLimitRuntime(cfg *LoopLimitConfig, nowMs int64) *LoopLimitRuntime {
	if cfg == nil {
		return nil
	}
	if cfg.Kind == "iterations" {
		return &LoopLimitRuntime{
			Kind:      "iterations",
			Initial:   cfg.Iterations,
			Remaining: cfg.Iterations,
		}
	}
	return &LoopLimitRuntime{
		Kind:       "duration",
		DurationMs: cfg.DurationMs,
		DeadlineMs: nowMs + cfg.DurationMs,
	}
}

// IsLoopLimitExhausted reports whether the loop budget is already spent without mutating it.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:220-224
func IsLoopLimitExhausted(limit *LoopLimitRuntime, nowMs int64) bool {
	if limit == nil {
		return false
	}
	if limit.Kind == "duration" {
		return nowMs >= limit.DeadlineMs
	}
	return limit.Remaining <= 0
}

// ConsumeLoopLimitIteration decrements an iteration budget or checks a duration deadline.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:199-207
func ConsumeLoopLimitIteration(limit *LoopLimitRuntime, nowMs int64) bool {
	if limit == nil {
		return true
	}
	if limit.Kind == "duration" {
		return nowMs < limit.DeadlineMs
	}
	if limit.Remaining <= 0 {
		return false
	}
	limit.Remaining--
	return true
}

// DescribeLoopLimit formats a LoopLimitConfig for enable messages.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:226-231
func DescribeLoopLimit(cfg LoopLimitConfig) string {
	if cfg.Kind == "iterations" {
		noun := "iterations"
		if cfg.Iterations == 1 {
			noun = "iteration"
		}
		return fmt.Sprintf("%d %s", cfg.Iterations, noun)
	}
	return formatLoopBudget(cfg.DurationMs)
}

// DescribeLoopLimitRuntime formats remaining loop budget for status display.
// upstream: packages/coding-agent/src/modes/loop-limit.ts:233-238
func DescribeLoopLimitRuntime(limit LoopLimitRuntime) string {
	if limit.Kind == "iterations" {
		noun := "iterations"
		if limit.Initial == 1 {
			noun = "iteration"
		}
		return fmt.Sprintf("%d of %d %s remaining", limit.Remaining, limit.Initial, noun)
	}
	return fmt.Sprintf("%s limit", formatLoopBudget(limit.DurationMs))
}

func formatLoopBudget(durationMs int64) string {
	if durationMs%3_600_000 == 0 {
		h := durationMs / 3_600_000
		noun := "hours"
		if h == 1 {
			noun = "hour"
		}
		return fmt.Sprintf("%d %s", h, noun)
	}
	if durationMs%60_000 == 0 {
		m := durationMs / 60_000
		noun := "minutes"
		if m == 1 {
			noun = "minute"
		}
		return fmt.Sprintf("%d %s", m, noun)
	}
	s := durationMs / 1_000
	noun := "seconds"
	if s == 1 {
		noun = "second"
	}
	return fmt.Sprintf("%d %s", s, noun)
}

// DescribeLoopCondition formats `--while` / `--until` for status messages.
// upstream: packages/coding-agent/src/modes/loop-condition.ts:84-86
func DescribeLoopCondition(cond LoopConditionConfig) string {
	verb := "while"
	if cond.Until {
		verb = "until"
	}
	return fmt.Sprintf("%s %s succeeds", verb, quoteLoopCommand(cond.Command))
}

func quoteLoopCommand(cmd string) string {
	clean := sanitizeSingleLine(cmd, 80)
	return "`" + clean + "`"
}

func sanitizeSingleLine(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return s
}

func previewLoopOutput(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return sanitizeSingleLine(trimmed, 80)
		}
	}
	return ""
}

func formatTimeoutMs(ms int) string {
	if ms%1000 == 0 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dms", ms)
}

// resolveWindowsBashShell resolves Git Bash or a POSIX `bash.exe`/`sh.exe` on
// Windows without falling back to `cmd.exe /C` (cmd exits 1 for unknown POSIX
// builtins such as `test -f done`, misclassifying a broken condition as false).
func resolveWindowsBashShell(
	programFiles, programFilesX86 string,
	exists func(string) bool,
	lookPath func(string) (string, error),
) (shellPath string, shellArgs []string, err error) {
	var searched []string
	for _, base := range []string{programFiles, programFilesX86} {
		if base == "" {
			continue
		}
		p := base + `\Git\bin\bash.exe`
		searched = append(searched, "  "+p)
		if exists(p) {
			return p, []string{"-c"}, nil
		}
	}
	if p, lookErr := lookPath("bash.exe"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	if p, lookErr := lookPath("sh.exe"); lookErr == nil && p != "" {
		return p, []string{"-c"}, nil
	}
	if len(searched) > 0 {
		return "", nil, fmt.Errorf("no POSIX bash shell found on Windows (install Git for Windows; searched:\n%s; cmd.exe is not supported for /loop conditions)", strings.Join(searched, "\n"))
	}
	return "", nil, fmt.Errorf("no POSIX bash shell found on Windows (install Git for Windows; cmd.exe is not supported for /loop conditions)")
}

// EvaluateLoopCondition runs the `--while`/`--until` shell command using a
// cross-platform POSIX shell (`bash`/`sh` on Unix, Git Bash/`bash.exe`/`sh.exe`
// on Windows — never `cmd.exe`) with an explicit cancellable signal. Distinguishes
// user/loop cancellation (`cancelCtx.Err() != nil`) from timeout
// (`res.Killed && !res.Truncated`) and buffer overflow (`res.Truncated`).
// upstream: packages/coding-agent/src/modes/loop-condition.ts:94-150
func EvaluateLoopCondition(ctx sdk.Context, cancelCtx context.Context, cond LoopConditionConfig, timeoutMs int) LoopConditionVerdict {
	shellPath, shellArgs, shellErr := resolveConditionShell()
	if shellErr != nil {
		return LoopConditionVerdict{
			Kind:    "error",
			Message: fmt.Sprintf("Loop condition %s could not run: %s. Loop mode disabled.", quoteLoopCommand(cond.Command), sanitizeSingleLine(shellErr.Error(), 80)),
		}
	}

	opts := sdk.ExecOptions{
		Cwd:    ctx.Cwd(),
		Signal: cancelCtx,
	}
	if timeoutMs > 0 {
		opts.Timeout = float64(timeoutMs)
	}

	args := append(append([]string(nil), shellArgs...), cond.Command)
	res, err := ctx.ExecWithOptions(shellPath, args, opts)
	if err != nil {
		if cancelCtx != nil && cancelCtx.Err() != nil {
			return LoopConditionVerdict{Kind: "aborted"}
		}
		return LoopConditionVerdict{
			Kind:    "error",
			Message: fmt.Sprintf("Loop condition %s could not run: %s. Loop mode disabled.", quoteLoopCommand(cond.Command), sanitizeSingleLine(err.Error(), 80)),
		}
	}
	if res == nil {
		return LoopConditionVerdict{
			Kind:    "error",
			Message: fmt.Sprintf("Loop condition %s failed (no exit status). Loop mode disabled.", quoteLoopCommand(cond.Command)),
		}
	}
	if res.Killed {
		// Explicit cancellation via `/loop` or pauseLoop takes priority over timeout.
		if cancelCtx != nil && cancelCtx.Err() != nil {
			return LoopConditionVerdict{Kind: "aborted"}
		}
		if res.Truncated {
			return LoopConditionVerdict{
				Kind:    "error",
				Message: fmt.Sprintf("Loop condition %s exceeded output buffer limit. Loop mode disabled.", quoteLoopCommand(cond.Command)),
			}
		}
		if timeoutMs > 0 {
			return LoopConditionVerdict{
				Kind:    "error",
				Message: fmt.Sprintf("Loop condition %s timed out after %s. Loop mode disabled.", quoteLoopCommand(cond.Command), formatTimeoutMs(timeoutMs)),
			}
		}
		return LoopConditionVerdict{Kind: "aborted"}
	}
	if res.ExitCode == 0 {
		if !cond.Until {
			return LoopConditionVerdict{Kind: "continue"}
		}
		return LoopConditionVerdict{
			Kind:    "halt",
			Message: fmt.Sprintf("Loop condition %s is now satisfied. Loop mode disabled.", quoteLoopCommand(cond.Command)),
		}
	}
	if res.ExitCode == 1 {
		if cond.Until {
			return LoopConditionVerdict{Kind: "continue"}
		}
		return LoopConditionVerdict{
			Kind:    "halt",
			Message: fmt.Sprintf("Loop condition %s no longer holds. Loop mode disabled.", quoteLoopCommand(cond.Command)),
		}
	}

	preview := previewLoopOutput(res.Stderr)
	if preview == "" {
		preview = previewLoopOutput(res.Stdout)
	}
	detail := ""
	if preview != "" {
		detail = ": " + preview
	}
	return LoopConditionVerdict{
		Kind:    "error",
		Message: fmt.Sprintf("Loop condition %s failed (exit %d)%s. Loop mode disabled.", quoteLoopCommand(cond.Command), res.ExitCode, detail),
	}
}

// handleLoopCommand implements `/loop [count|duration] [--while|--until '<cmd>'] [prompt]`.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:3828-3866
func (m *manager) handleLoopCommand(ctx sdk.Context, args string) error {
	sid := strictSessionID(ctx)
	if sid == "" {
		notifyError(ctx, "Cannot use loop mode without an active session ID.")
		return nil
	}
	m.mu.Lock()
	st := m.stateLocked(sid)
	if st.loopEnabled {
		cancel, stopCh := m.disableLoopLocked(ctx, st, "Loop mode disabled.")
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if stopCh != nil {
			close(stopCh)
		}
		return nil
	}
	m.mu.Unlock()

	parsed, errMsg := ParseLoopArgs(args)
	if errMsg != "" {
		notifyError(ctx, errMsg)
		return nil
	}

	now := m.now()
	m.mu.Lock()
	st = m.stateLocked(sid)
	st.loopEnabled = true
	st.loopPaused = false
	st.loopPrompt = parsed.Prompt
	st.loopLimit = CreateLoopLimitRuntime(parsed.Limit, now)
	st.loopCondition = parsed.Condition
	m.syncStatus(ctx, st)

	var b strings.Builder
	b.WriteString("Loop mode enabled.")
	if parsed.Limit != nil {
		b.WriteString(" Limited to ")
		b.WriteString(DescribeLoopLimit(*parsed.Limit))
		b.WriteString(".")
	}
	if st.loopLimit != nil {
		b.WriteString(" ")
		b.WriteString(DescribeLoopLimitRuntime(*st.loopLimit))
		b.WriteString(".")
	}
	if parsed.Condition != nil {
		b.WriteString(" Continuing ")
		b.WriteString(DescribeLoopCondition(*parsed.Condition))
		b.WriteString(".")
	}
	if parsed.Prompt != "" {
		b.WriteString(" Repeating it after each turn.")
	} else {
		b.WriteString(" Your next prompt will repeat after each turn.")
	}
	b.WriteString(" Escape suspends the ongoing loop; /loop again to disable.")
	msg := b.String()
	m.mu.Unlock()

	notifyInfo(ctx, msg)
	if parsed.Prompt != "" {
		return sendPromptOrSteer(ctx, parsed.Prompt)
	}
	return nil
}

func (m *manager) disableLoopLocked(ctx sdk.Context, st *sessionState, message string) (context.CancelFunc, chan struct{}) {
	was := st.loopEnabled
	st.loopEnabled = false
	st.loopPaused = false
	st.loopPrompt = ""
	st.loopLimit = nil
	st.loopCondition = nil
	cancel := st.loopCondCancel
	st.loopCondCancel = nil
	stopCh := st.loopResubmitStop
	st.loopResubmitStop = nil
	m.syncStatus(ctx, st)
	if was && message != "" {
		notifyInfo(ctx, message)
	}
	return cancel, stopCh
}

func (m *manager) pauseLoop(ctx sdk.Context, sid string) {
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || !st.loopEnabled {
		m.mu.Unlock()
		return
	}
	st.loopPrompt = ""
	st.loopPaused = true
	cancel := st.loopCondCancel
	st.loopCondCancel = nil
	stopCh := st.loopResubmitStop
	st.loopResubmitStop = nil
	m.syncStatus(ctx, st)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stopCh != nil {
		close(stopCh)
	}
}

// maybeRunLoopIteration evaluates loop limits/conditions after a brief delay
// (matching omp's 800 ms Escape grace window in interactive-mode.ts:3531-3541)
// and returns the prompt to auto-resubmit, or "" when the loop should not continue.
func (m *manager) maybeRunLoopIteration(ctx sdk.Context, sid string) string {
	m.mu.Lock()
	st := m.sessions[sid]
	if st == nil || !st.loopEnabled || st.loopPaused || st.loopPrompt == "" {
		m.mu.Unlock()
		return ""
	}
	prompt := st.loopPrompt
	cond := st.loopCondition
	if IsLoopLimitExhausted(st.loopLimit, m.now()) {
		cancel, stopCh := m.disableLoopLocked(ctx, st, "Loop limit reached. Loop mode disabled.")
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if stopCh != nil {
			close(stopCh)
		}
		return ""
	}
	if st.loopResubmitStop != nil {
		close(st.loopResubmitStop)
	}
	stopCh := make(chan struct{})
	st.loopResubmitStop = stopCh
	delay := m.loopWait
	m.mu.Unlock()

	runSig := ctx.Signal()
	var sigDone <-chan struct{}
	if runSig != nil {
		sigDone = runSig.Done()
	}
	// Observe Escape/run-abort during the 800 ms grace window and pause the loop
	// immediately (`modes/controllers/input-controller.ts:607`).
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-stopCh:
			timer.Stop()
			return ""
		case <-sigDone:
			timer.Stop()
			m.pauseLoop(ctx, sid)
			return ""
		case <-timer.C:
		}
	} else if sigDone != nil {
		select {
		case <-sigDone:
			m.pauseLoop(ctx, sid)
			return ""
		default:
		}
	}

	if cond != nil {
		parentCtx := context.Background()
		if runSig != nil {
			parentCtx = runSig
		}
		condCtx, condCancel := context.WithCancel(parentCtx)
		m.mu.Lock()
		st = m.sessions[sid]
		if st == nil || !st.loopEnabled || st.loopPaused || st.loopPrompt != prompt {
			m.mu.Unlock()
			condCancel()
			return ""
		}
		prevCancel := st.loopCondCancel
		st.loopCondCancel = condCancel
		m.mu.Unlock()
		if prevCancel != nil {
			prevCancel()
		}

		verdict := EvaluateLoopCondition(ctx, condCtx, *cond, loopConditionTimeoutMs(ctx))
		condCancel()

		// If Escape aborted the run while the condition command was running,
		// pause the loop (keeping it armed for the user's next prompt) matching
		// upstream `interactive-mode.ts:3820-3826`.
		if runSig != nil && runSig.Err() != nil {
			m.pauseLoop(ctx, sid)
			return ""
		}

		m.mu.Lock()
		st = m.sessions[sid]
		if st != nil && st.loopCondCancel != nil {
			st.loopCondCancel = nil
		}
		if st == nil || !st.loopEnabled || st.loopPaused || st.loopPrompt != prompt {
			m.mu.Unlock()
			return ""
		}
		if verdict.Kind != "continue" {
			var cancel context.CancelFunc
			var oldStop chan struct{}
			if verdict.Kind != "aborted" {
				cancel, oldStop = m.disableLoopLocked(ctx, st, verdict.Message)
			}
			m.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if oldStop != nil {
				close(oldStop)
			}
			return ""
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	st = m.sessions[sid]
	if st == nil || !st.loopEnabled || st.loopPaused || st.loopPrompt != prompt {
		m.mu.Unlock()
		return ""
	}
	if !ConsumeLoopLimitIteration(st.loopLimit, m.now()) {
		cancel, oldStop := m.disableLoopLocked(ctx, st, "Loop limit reached. Loop mode disabled.")
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if oldStop != nil {
			close(oldStop)
		}
		return ""
	}
	m.syncStatus(ctx, st)
	m.mu.Unlock()
	return prompt
}
