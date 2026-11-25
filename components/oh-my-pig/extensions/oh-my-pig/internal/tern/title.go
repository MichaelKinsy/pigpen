package tern

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// TitleState mirrors omp's `TerminalTitleState`.
// upstream: packages/coding-agent/src/utils/title-generator.ts:864
type TitleState string

const (
	TitleIdle      TitleState = "idle"
	TitleWorking   TitleState = "working"
	TitleAttention TitleState = "attention"
)

// SpinnerStyle mirrors omp's `TerminalTitleSpinnerStyle`.
// upstream: packages/coding-agent/src/utils/title-generator.ts:866
type SpinnerStyle string

const (
	SpinnerBraille SpinnerStyle = "braille"
	SpinnerPulse   SpinnerStyle = "pulse"
	SpinnerDots    SpinnerStyle = "dots"
	SpinnerLine    SpinnerStyle = "line"
)

const (
	// DefaultTerminalTitle is omp's plain-text brand for non-native titles.
	// Plain `π`, because window/tab titles render in the OS UI font.
	// upstream: packages/coding-agent/src/utils/title-generator.ts:44
	DefaultTerminalTitle = "π"
	// NativeTerminalTitle is the tab title without a session name in a native TSP host.
	// upstream: packages/coding-agent/src/utils/title-generator.ts:46
	NativeTerminalTitle = "omp"

	staticTitleWorkingSep = ":"
	titleIdleSep          = ">"
	titleAttentionSep     = "!"

	defaultSpinnerInterval = 80 * time.Millisecond
)

// SpinnerFrames maps each SpinnerStyle to its single-column frames.
// upstream: packages/coding-agent/src/utils/title-generator.ts:875
var SpinnerFrames = map[SpinnerStyle][]string{
	SpinnerBraille: {"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	SpinnerPulse:   {"○", "◔", "◑", "◕", "●", "◕", "◑", "◔"},
	SpinnerDots:    {"⠁", "⠂", "⠄", "⠠", "⠐", "⠈"},
	SpinnerLine:    {"-", "\\", "|", "/"},
}

// SanitizeTitlePart strips C0/C1 control characters and trims whitespace so
// model-generated titles cannot inject terminal escapes.
// upstream: packages/coding-agent/src/utils/title-generator.ts:660
func SanitizeTitlePart(value string) string {
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if r <= 0x1f || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// FallbackTitleFromCwd returns the sanitized basename of cwd, or "" when cwd is
// empty or a filesystem root.
// upstream: packages/coding-agent/src/utils/title-generator.ts:666
func FallbackTitleFromCwd(cwd string) string {
	if cwd == "" {
		return ""
	}
	clean := filepath.Clean(cwd)
	base := filepath.Base(clean)
	if base == "" || base == "." || base == string(filepath.Separator) || base == filepath.VolumeName(clean)+string(filepath.Separator) {
		return ""
	}
	return SanitizeTitlePart(base)
}

// BuildNativeTitle formats the tab title for Tern (native TSP): the session name
// (defaulting to `NativeTerminalTitle` before there is one) and optional PR number.
// Tern shows run state itself, so there is no brand or state separator.
// upstream: packages/coding-agent/src/utils/title-generator.ts:994
func BuildNativeTitle(sessionName string, pullRequest int) string {
	name := SanitizeTitlePart(sessionName)
	if name == "" {
		name = NativeTerminalTitle
	}
	if pullRequest > 0 {
		return name + " · #" + itoa(pullRequest)
	}
	return name
}

// BuildTitleWithState composes the classic terminal title:
//   - idle:      `π > label`
//   - working:   `π ⠋ label` (or static `π : label` under WSL / staticSpinner)
//   - attention: `π ! label`
//   - disabled:  `π: label`
//
// upstream: packages/coding-agent/src/utils/title-generator.ts:965
func BuildTitleWithState(
	label string,
	state TitleState,
	frame int,
	enabled bool,
	style SpinnerStyle,
	lookup EnvLookup,
	staticSpinner bool,
) string {
	return BuildTitleWithStateForPlatform(label, state, frame, enabled, runtime.GOOS, style, lookup, staticSpinner)
}

// BuildTitleWithStateForPlatform is BuildTitleWithState with an explicit GOOS
// platform for deterministic WSL testing.
// upstream: packages/coding-agent/src/utils/title-generator.ts:965
func BuildTitleWithStateForPlatform(
	label string,
	state TitleState,
	frame int,
	enabled bool,
	goos string,
	style SpinnerStyle,
	lookup EnvLookup,
	staticSpinner bool,
) string {
	label = SanitizeTitlePart(label)
	if !enabled {
		if label != "" {
			return DefaultTerminalTitle + ": " + label
		}
		return DefaultTerminalTitle
	}
	frames, ok := SpinnerFrames[style]
	if !ok || len(frames) == 0 {
		frames = SpinnerFrames[SpinnerBraille]
	}
	isStatic := staticSpinner || IsWSLForPlatform(goos, lookup)
	var sep string
	switch state {
	case TitleWorking:
		if isStatic {
			sep = staticTitleWorkingSep
		} else {
			sep = frames[((frame%len(frames))+len(frames))%len(frames)]
		}
	case TitleAttention:
		sep = titleAttentionSep
	default:
		sep = titleIdleSep
	}
	if label != "" {
		return DefaultTerminalTitle + " " + sep + " " + label
	}
	return DefaultTerminalTitle + " " + sep
}

// TitleController manages the session's terminal title through PiG's SDK
// (`ctx.SetTitle`). In Tern (`TERM_PROGRAM=tern`), it emits the plain native
// title (no `π` brand or spinner, since Tern renders run state itself); in
// other terminals it emits `π > label` / `π ⠋ label` / `π ! label` and
// animates the working spinner outside WSL.
//
// Dialog holds (`HoldBlocked` / `ReleaseBlocked`) override the flow state with
// `TitleAttention` (`!`) even if `agent_end` arrives while a dialog is still
// open, matching omp's `utils/run-status.ts:74-76,154-165`.
type TitleController struct {
	mu            sync.Mutex
	lookup        EnvLookup
	goos          string
	sessionName   string
	cwd           string
	pullRequest   int
	flowState     TitleState
	dialogDepth   int
	frame         int
	enabled       bool
	style         SpinnerStyle
	interval      time.Duration
	lastTickTitle string
	disposed      bool
	stopSpinnerCh chan struct{}
	spinnerDoneCh chan struct{}
	setTitleFn    func(title string)
}

// NewTitleController creates a TitleController with omp's defaults.
func NewTitleController(lookup EnvLookup) *TitleController {
	if lookup == nil {
		lookup = osLookupEnv
	}
	return &TitleController{
		lookup:    lookup,
		goos:      runtime.GOOS,
		flowState: TitleIdle,
		enabled:   true,
		style:     SpinnerBraille,
		interval:  defaultSpinnerInterval,
	}
}

// Init claims terminal title ownership, resets the teardown latch and any
// stale dialog holds, and re-arms the spinner if the active state is already
// `working`.
// upstream: packages/coding-agent/src/utils/title-generator.ts:1124-1154
// upstream: packages/coding-agent/src/utils/run-status.ts:204
func (tc *TitleController) Init(ctx sdk.Context) {
	isTUI := ctx.Mode() == "tui" && ctx.HasUI()
	tc.mu.Lock()
	tc.disposed = false
	tc.dialogDepth = 0
	tc.lastTickTitle = ""
	tc.setTitleFn = makeSafeTitleSink(ctx, isTUI)
	shouldSpin := tc.shouldSpinLocked()
	tc.mu.Unlock()

	if shouldSpin {
		tc.startSpinner()
	}
}

// makeSafeTitleSink creates a sink that only emits in TUI mode (never RPC,
// JSON, or print) and recovers from stale-context panics so background spinner
// ticks can never crash the fused PiG process.
func makeSafeTitleSink(ctx sdk.Context, isTUI bool) func(string) {
	if !isTUI {
		return nil
	}
	return func(title string) {
		defer func() { _ = recover() }()
		ctx.SetTitle(title)
	}
}

// SetSession updates the session name and cwd and emits the resulting title.
// Does NOT release the teardown latch (matching `setSessionTerminalTitle`).
// upstream: packages/coding-agent/src/utils/title-generator.ts:750-766
func (tc *TitleController) SetSession(ctx sdk.Context, sessionName, cwd string) {
	isTUI := ctx.Mode() == "tui" && ctx.HasUI()
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.disposed {
		return
	}
	tc.sessionName = SanitizeTitlePart(sessionName)
	if cwd != "" {
		tc.cwd = cwd
	}
	tc.setTitleFn = makeSafeTitleSink(ctx, isTUI)
	tc.emitLocked(false)
}

// SetPullRequest sets the branch PR number shown in the native Tern title
// (`<name> · #<pr>`). Pass 0 to clear.
// upstream: packages/coding-agent/src/utils/title-generator.ts:841
func (tc *TitleController) SetPullRequest(pr int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.disposed {
		return
	}
	tc.pullRequest = pr
	tc.emitLocked(false)
}

// ApplySettings updates title settings (`tui.titleState`, `tui.titleSpinner`,
// `tui.spinnerInterval`) without emitting an intermediate title; callers
// follow immediately with `SetSession` or `SetState`.
func (tc *TitleController) ApplySettings(enabled *bool, style *string, interval *time.Duration) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if enabled != nil {
		tc.enabled = *enabled
	}
	if style != nil {
		switch SpinnerStyle(*style) {
		case SpinnerBraille, SpinnerPulse, SpinnerDots, SpinnerLine:
			tc.style = SpinnerStyle(*style)
		default:
			tc.style = SpinnerBraille
		}
	}
	if interval != nil {
		iv := *interval
		if iv < 0 {
			iv = 0
		}
		tc.interval = iv
	}
}

// SetStateEnabled toggles the run-state separator (`tui.titleState`).
// upstream: packages/coding-agent/src/utils/title-generator.ts:1073
func (tc *TitleController) SetStateEnabled(enabled bool) {
	tc.mu.Lock()
	if tc.disposed {
		tc.mu.Unlock()
		return
	}
	tc.enabled = enabled
	tc.emitLocked(false)
	shouldSpin := tc.shouldSpinLocked()
	tc.mu.Unlock()

	if shouldSpin {
		tc.startSpinner()
	} else {
		tc.stopSpinner()
	}
}

// SetSpinnerStyle selects the working spinner glyph set (`tui.titleSpinner`).
// Unknown values fall back to `braille` and reset the frame to 0.
// upstream: packages/coding-agent/src/utils/title-generator.ts:1086
func (tc *TitleController) SetSpinnerStyle(style string) {
	var next SpinnerStyle
	switch SpinnerStyle(style) {
	case SpinnerBraille, SpinnerPulse, SpinnerDots, SpinnerLine:
		next = SpinnerStyle(style)
	default:
		next = SpinnerBraille
	}
	tc.mu.Lock()
	if tc.disposed {
		tc.mu.Unlock()
		return
	}
	if next == tc.style {
		tc.mu.Unlock()
		return
	}
	tc.style = next
	tc.frame = 0
	tc.emitLocked(false)
	shouldSpin := tc.shouldSpinLocked()
	tc.mu.Unlock()

	tc.stopSpinner()
	if shouldSpin {
		tc.startSpinner()
	}
}

// SetSpinnerInterval sets the working spinner cadence (`tui.spinnerInterval`).
// <= 0 pins a static `:` separator and stops the spinner timer.
// upstream: packages/coding-agent/src/utils/title-generator.ts:1105
func (tc *TitleController) SetSpinnerInterval(interval time.Duration) {
	if interval < 0 {
		interval = 0
	}
	tc.mu.Lock()
	if tc.disposed {
		tc.mu.Unlock()
		return
	}
	if interval == tc.interval {
		tc.mu.Unlock()
		return
	}
	tc.interval = interval
	tc.frame = 0
	tc.emitLocked(false)
	shouldSpin := tc.shouldSpinLocked()
	tc.mu.Unlock()

	tc.stopSpinner()
	if shouldSpin {
		tc.startSpinner()
	}
}

// SetState updates the event-flow run state (`idle` / `working` / `attention`).
// If a dialog hold is active (`dialogDepth > 0`), the displayed state remains
// `TitleAttention` until all dialogs close (matching `run-status.ts:74-76`).
// upstream: packages/coding-agent/src/utils/title-generator.ts:1065
func (tc *TitleController) SetState(ctx sdk.Context, state TitleState) {
	isTUI := ctx.Mode() == "tui" && ctx.HasUI()
	tc.mu.Lock()
	if tc.disposed {
		tc.mu.Unlock()
		return
	}
	tc.flowState = state
	tc.setTitleFn = makeSafeTitleSink(ctx, isTUI)
	tc.emitLocked(false)
	shouldSpin := tc.shouldSpinLocked()
	tc.mu.Unlock()

	if shouldSpin {
		tc.startSpinner()
	} else {
		tc.stopSpinner()
	}
}

// HoldBlocked increments the open-dialog depth so the title displays `!`
// (`TitleAttention`) regardless of background `agent_start`/`agent_end` events.
// upstream: packages/coding-agent/src/utils/run-status.ts:154-164
func (tc *TitleController) HoldBlocked(ctx sdk.Context) {
	isTUI := ctx.Mode() == "tui" && ctx.HasUI()
	tc.mu.Lock()
	if tc.disposed {
		tc.mu.Unlock()
		return
	}
	tc.dialogDepth++
	tc.setTitleFn = makeSafeTitleSink(ctx, isTUI)
	tc.emitLocked(false)
	tc.mu.Unlock()
	tc.stopSpinner()
}

// ReleaseBlocked decrements the open-dialog depth and restores the underlying
// flow state once no dialogs remain open.
// upstream: packages/coding-agent/src/utils/run-status.ts:158-163
func (tc *TitleController) ReleaseBlocked(ctx sdk.Context) {
	isTUI := ctx.Mode() == "tui" && ctx.HasUI()
	tc.mu.Lock()
	if tc.disposed {
		tc.mu.Unlock()
		return
	}
	if tc.dialogDepth > 0 {
		tc.dialogDepth--
	}
	tc.setTitleFn = makeSafeTitleSink(ctx, isTUI)
	tc.emitLocked(false)
	shouldSpin := tc.shouldSpinLocked()
	tc.mu.Unlock()

	if shouldSpin {
		tc.startSpinner()
	} else {
		tc.stopSpinner()
	}
}

// Dispose stops any running spinner and latches the controller off so no late
// events write after session teardown.
// upstream: packages/coding-agent/src/utils/title-generator.ts:1163
func (tc *TitleController) Dispose() {
	tc.mu.Lock()
	tc.disposed = true
	tc.lastTickTitle = ""
	tc.mu.Unlock()
	tc.stopSpinner()
}

// CurrentTitle returns the title that would be emitted for the current state.
func (tc *TitleController) CurrentTitle() string {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.composeLocked()
}

// SpinnerRunning reports whether the background spinner goroutine is active.
func (tc *TitleController) SpinnerRunning() bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.stopSpinnerCh != nil
}

func (tc *TitleController) effectiveStateLocked() TitleState {
	if tc.dialogDepth > 0 {
		return TitleAttention
	}
	return tc.flowState
}

func (tc *TitleController) shouldSpinLocked() bool {
	return !tc.disposed &&
		tc.setTitleFn != nil &&
		tc.effectiveStateLocked() == TitleWorking &&
		tc.enabled &&
		!IsTernProgramEnv(tc.lookup) &&
		!IsWSLForPlatform(tc.goos, tc.lookup) &&
		tc.interval > 0
}

func (tc *TitleController) composeLocked() string {
	if IsTernProgramEnv(tc.lookup) {
		// Native Tern title uses the session name only ("omp" before named),
		// never the cwd basename.
		// upstream: packages/coding-agent/src/utils/title-generator.ts:896-897,994-997,1005
		return BuildNativeTitle(tc.sessionName, tc.pullRequest)
	}
	label := tc.sessionName
	if label == "" {
		label = FallbackTitleFromCwd(tc.cwd)
	}
	return BuildTitleWithStateForPlatform(label, tc.effectiveStateLocked(), tc.frame, tc.enabled, tc.goos, tc.style, tc.lookup, tc.interval <= 0)
}

// emitLocked writes the composed title. Deduplication applies only to spinner
// ticks (dedupeTick=true) so explicit lifecycle/state/session updates always
// overwrite a title the PiG host may have reset in between (e.g. `pig - <dir>`
// after `session_start` or rename).
func (tc *TitleController) emitLocked(dedupeTick bool) {
	if tc.disposed || tc.setTitleFn == nil {
		return
	}
	next := tc.composeLocked()
	if dedupeTick && next == tc.lastTickTitle {
		return
	}
	tc.lastTickTitle = next
	fn := tc.setTitleFn
	fn(next)
}

func (tc *TitleController) startSpinner() {
	tc.mu.Lock()
	if !tc.shouldSpinLocked() || tc.stopSpinnerCh != nil {
		tc.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	interval := tc.interval
	tc.stopSpinnerCh = stop
	tc.spinnerDoneCh = done
	tc.mu.Unlock()

	go func() {
		defer func() {
			// Clear channel pointers if this goroutine exits on its own so a
			// subsequent startSpinner is never blocked by a stale stopSpinnerCh.
			tc.mu.Lock()
			if tc.stopSpinnerCh == stop {
				tc.stopSpinnerCh = nil
				tc.spinnerDoneCh = nil
			}
			tc.mu.Unlock()
			close(done)
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				tc.mu.Lock()
				if !tc.shouldSpinLocked() || tc.stopSpinnerCh != stop {
					tc.mu.Unlock()
					return
				}
				frames := SpinnerFrames[tc.style]
				if len(frames) == 0 {
					frames = SpinnerFrames[SpinnerBraille]
				}
				tc.frame = (tc.frame + 1) % len(frames)
				tc.emitLocked(true)
				tc.mu.Unlock()
			}
		}
	}()
}

func (tc *TitleController) stopSpinner() {
	tc.mu.Lock()
	stop := tc.stopSpinnerCh
	done := tc.spinnerDoneCh
	tc.stopSpinnerCh = nil
	tc.spinnerDoneCh = nil
	tc.mu.Unlock()

	if stop != nil {
		close(stop)
		<-done
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
