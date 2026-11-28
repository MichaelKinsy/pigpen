package shell

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	stateDirPrefix = "omp-shell-state-"
	// maxStateRecordBytes caps any single variable/function/alias declaration
	// written into state.sh so one huge variable (e.g., 10 MiB) is skipped with
	// a notice rather than blowing the capture budget for every other variable.
	maxStateRecordBytes = 4 * 1024 * 1024
	// maxStateCaptureBytes caps the in-memory pipe read buffer (64 MiB), large
	// enough to carry multi-MiB snapshots plus base+post dumps without silently
	// cutting off the completion sentinel.
	maxStateCaptureBytes = 64 * 1024 * 1024
)

var sweepStaleOnce sync.Once

// sweepStaleStateDirs removes leftover omp-shell-state-<pid>-* directories from
// dead processes (crashes, SIGKILL, SIGINT without shutdown).
func sweepStaleStateDirs() {
	sweepStaleOnce.Do(func() {
		tmp := os.TempDir()
		entries, err := os.ReadDir(tmp)
		if err != nil {
			return
		}
		myPID := os.Getpid()
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, stateDirPrefix) {
				continue
			}
			rest := strings.TrimPrefix(name, stateDirPrefix)
			pidStr, _, hasDash := strings.Cut(rest, "-")
			if !hasDash {
				continue
			}
			pid, err := strconv.Atoi(pidStr)
			if err != nil || pid <= 0 || pid == myPID {
				continue
			}
			if !isProcessAlive(pid) {
				_ = os.RemoveAll(filepath.Join(tmp, name))
			}
		}
	})
}

func randomStateNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "ompnonce"
	}
	return hex.EncodeToString(b[:])
}

// randomStatePipeFD picks a per-call high FD in [30, 189] so no fixed FD
// number is predictable across calls, while staying safely below macOS's
// default 256 soft open-file limit.
func randomStatePipeFD() int {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 73
	}
	return 30 + int(binary.BigEndian.Uint16(b[:])%160)
}

// shellSnapshot is a structured, nonce-framed representation of shell state
// (variables, functions, aliases, shopt/set-o options). Record bodies are
// treated as opaque text so multi-line `declare -p` values (macOS bash 3.2),
// multi-line aliases, and multi-line functions round-trip verbatim without
// line-oriented splitting or data-to-code promotion.
type shellSnapshot struct {
	vars      map[string]string // varName -> full `declare ...` text
	unsetVars map[string]bool   // inherited vars explicitly unset by user
	funcs     map[string]string // fnName -> full `declare -f` body
	aliases   map[string]string // aliasName -> full `alias ...` definition
	shoptOpts map[string]bool   // shopt option name -> true (-s) / false (-u)
	setOpts   map[string]bool   // set -o option name -> true (-o) / false (+o)
}

func newShellSnapshot() *shellSnapshot {
	return &shellSnapshot{
		vars:      make(map[string]string),
		unsetVars: make(map[string]bool),
		funcs:     make(map[string]string),
		aliases:   make(map[string]string),
		shoptOpts: make(map[string]bool),
		setOpts:   make(map[string]bool),
	}
}

// shellDelta represents ONLY the mutations one command made relative to its
// own post-load/post-preamble baseline, so concurrent/overlapping calls merge
// cleanly (later finisher wins only on names/options it actually changed) and
// `unset` / `unset -f` / `unalias` removals persist.
type shellDelta struct {
	setVars     map[string]string
	unsetVars   map[string]bool
	setFuncs    map[string]string
	unsetFuncs  map[string]bool
	setAliases  map[string]string
	unsetAlias  map[string]bool
	shoptChange map[string]bool
	setChange   map[string]bool
	skippedVars []string // oversized declarations skipped instead of dropping whole delta
}

func computeShellDelta(base, post *shellSnapshot) shellDelta {
	d := shellDelta{
		setVars:     make(map[string]string),
		unsetVars:   make(map[string]bool),
		setFuncs:    make(map[string]string),
		unsetFuncs:  make(map[string]bool),
		setAliases:  make(map[string]string),
		unsetAlias:  make(map[string]bool),
		shoptChange: make(map[string]bool),
		setChange:   make(map[string]bool),
	}
	if base == nil || post == nil {
		return d
	}

	for name, decl := range post.vars {
		if isReadonlyDeclare(decl) {
			continue
		}
		if prev, existed := base.vars[name]; !existed || prev != decl {
			if len(decl) > maxStateRecordBytes {
				d.skippedVars = append(d.skippedVars, name)
				continue
			}
			d.setVars[name] = decl
		}
	}
	for name := range base.vars {
		if _, exists := post.vars[name]; !exists {
			d.unsetVars[name] = true
		}
	}

	for name, body := range post.funcs {
		if prev, existed := base.funcs[name]; !existed || prev != body {
			if len(body) > maxStateRecordBytes {
				d.skippedVars = append(d.skippedVars, name+"()")
				continue
			}
			d.setFuncs[name] = body
		}
	}
	for name := range base.funcs {
		if _, exists := post.funcs[name]; !exists {
			d.unsetFuncs[name] = true
		}
	}

	for name, def := range post.aliases {
		if prev, existed := base.aliases[name]; !existed || prev != def {
			if len(def) > maxStateRecordBytes {
				d.skippedVars = append(d.skippedVars, "alias:"+name)
				continue
			}
			d.setAliases[name] = def
		}
	}
	for name := range base.aliases {
		if _, exists := post.aliases[name]; !exists {
			d.unsetAlias[name] = true
		}
	}

	for opt, val := range post.shoptOpts {
		if prev, existed := base.shoptOpts[opt]; !existed || prev != val {
			d.shoptChange[opt] = val
		}
	}
	for opt, val := range post.setOpts {
		if prev, existed := base.setOpts[opt]; !existed || prev != val {
			d.setChange[opt] = val
		}
	}
	sort.Strings(d.skippedVars)
	return d
}

// isEmpty reports whether delta contains no changes at all.
func (d shellDelta) isEmpty() bool {
	return len(d.setVars) == 0 &&
		len(d.unsetVars) == 0 &&
		len(d.setFuncs) == 0 &&
		len(d.unsetFuncs) == 0 &&
		len(d.setAliases) == 0 &&
		len(d.unsetAlias) == 0 &&
		len(d.shoptChange) == 0 &&
		len(d.setChange) == 0
}

// applyDelta merges delta into snap in place.
func (snap *shellSnapshot) applyDelta(d shellDelta) {
	for name := range d.unsetVars {
		delete(snap.vars, name)
		snap.unsetVars[name] = true
	}
	for name, decl := range d.setVars {
		delete(snap.unsetVars, name)
		snap.vars[name] = decl
	}
	for name := range d.unsetFuncs {
		delete(snap.funcs, name)
	}
	for name, body := range d.setFuncs {
		snap.funcs[name] = body
	}
	for name := range d.unsetAlias {
		delete(snap.aliases, name)
	}
	for name, def := range d.setAliases {
		snap.aliases[name] = def
	}
	for opt, val := range d.shoptChange {
		snap.shoptOpts[opt] = val
	}
	for opt, val := range d.setChange {
		snap.setOpts[opt] = val
	}
}

// renderScript emits executable bash source restoring snap into a fresh bash
// process. Contains ONLY user-created/modified state (never inherited env vars).
// Order:
//  1. `shopt -u expand_aliases` while defining functions so an existing alias
//     with the same name cannot cause a syntax error during function parse.
//  2. `unset -v` for explicitly unset inherited variables.
//  3. User variables (`declare ...`), functions (`name () { ... }`).
//  4. `shopt -s expand_aliases`, then user aliases (`alias ...`).
//  5. `shopt` options, then `set -o`/`set +o` options LAST (`errexit`, `nounset`,
//     `allexport`, `noclobber`, `xtrace` at the very end) so options never
//     interfere with sourcing the state itself while still being active before
//     the tool-command preamble and user command run.
func (snap *shellSnapshot) renderScript() string {
	var b strings.Builder
	b.WriteString("shopt -u expand_aliases 2>/dev/null || true\n")

	if len(snap.unsetVars) > 0 {
		for _, name := range sortedKeysBool(snap.unsetVars) {
			fmt.Fprintf(&b, "unset -v %s 2>/dev/null || true\n", name)
		}
	}
	if len(snap.vars) > 0 {
		for _, name := range sortedKeysString(snap.vars) {
			decl := strings.TrimSuffix(snap.vars[name], "\n")
			b.WriteString(decl)
			b.WriteByte('\n')
		}
	}
	if len(snap.funcs) > 0 {
		for _, name := range sortedKeysString(snap.funcs) {
			body := strings.TrimSuffix(snap.funcs[name], "\n")
			b.WriteString(body)
			b.WriteByte('\n')
		}
	}

	b.WriteString("shopt -s expand_aliases 2>/dev/null || true\n")
	if len(snap.aliases) > 0 {
		for _, name := range sortedKeysString(snap.aliases) {
			def := normalizeAliasDecl(strings.TrimSuffix(snap.aliases[name], "\n"))
			b.WriteString(def)
			b.WriteByte('\n')
		}
	}

	if len(snap.shoptOpts) > 0 {
		for _, opt := range sortedKeysBool(snap.shoptOpts) {
			if snap.shoptOpts[opt] {
				fmt.Fprintf(&b, "shopt -s %s 2>/dev/null || true\n", opt)
			} else {
				fmt.Fprintf(&b, "shopt -u %s 2>/dev/null || true\n", opt)
			}
		}
	}
	if len(snap.setOpts) > 0 {
		// Emit errexit/nounset/allexport/noclobber/xtrace last among set -o
		// options so earlier lines execute cleanly without tracing state.sh.
		var deferred []string
		hasXtrace := false
		xtraceEnabled := false
		for _, opt := range sortedKeysBool(snap.setOpts) {
			switch opt {
			case "xtrace":
				hasXtrace = true
				xtraceEnabled = snap.setOpts[opt]
			case "errexit", "nounset", "allexport", "noclobber":
				deferred = append(deferred, opt)
			default:
				writeSetOpt(&b, opt, snap.setOpts[opt])
			}
		}
		for _, opt := range deferred {
			writeSetOpt(&b, opt, snap.setOpts[opt])
		}
		if hasXtrace {
			writeSetOpt(&b, "xtrace", xtraceEnabled)
		}
	}
	return b.String()
}

func writeSetOpt(b *strings.Builder, opt string, enabled bool) {
	if enabled {
		fmt.Fprintf(b, "set -o %s 2>/dev/null || true\n", opt)
	} else {
		fmt.Fprintf(b, "set +o %s 2>/dev/null || true\n", opt)
	}
}

// normalizeAliasDecl makes bash 3.2 `alias -x='...'` safe to re-source by
// inserting `--` when the alias name begins with `-` (bash 5.3 already emits
// `alias -- -x='...'`, while bash 3.2 omits `--` and then rejects `-x` as an
// invalid option when sourced).
func normalizeAliasDecl(decl string) string {
	if !strings.HasPrefix(decl, "alias ") {
		return decl
	}
	rest := strings.TrimPrefix(decl, "alias ")
	if strings.HasPrefix(rest, "-- ") {
		return decl
	}
	if strings.HasPrefix(rest, "-") {
		return "alias -- " + rest
	}
	return decl
}

// isReadonlyDeclare checks ONLY the flag word (second token of `declare -<flags> NAME...`)
// for `r` (read-only), so variable names/values containing `r` and spaces (e.g.
// `result="foo bar"`, `dir="/My Documents"`, `PATH`) are never dropped.
func isReadonlyDeclare(decl string) bool {
	firstLine := decl
	if nl := strings.IndexByte(decl, '\n'); nl >= 0 {
		firstLine = decl[:nl]
	}
	if !strings.HasPrefix(firstLine, "declare ") {
		return false
	}
	rest := strings.TrimPrefix(firstLine, "declare ")
	flagWord, _, _ := strings.Cut(rest, " ")
	return strings.HasPrefix(flagWord, "-") && strings.ContainsRune(flagWord, 'r')
}

func sortedKeysString(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysBool(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseCapturedStateStream parses either:
//  1. The Unix pipe format: nonce-framed BASE + POST full snapshots (never on disk).
//  2. The Windows file-delta format (`#__OMP_DELTA_<nonce>__`): pre-diffed
//     records where unchanged inherited variables NEVER touch disk.
//
// Returns (delta, ok) where ok is true ONLY when the stream completed with its
// nonce-tagged end sentinel (so `exec`, user-overridden EXIT traps, closed fd,
// or killed runs never corrupt the session snapshot).
func parseCapturedStateStream(raw, nonce string) (shellDelta, bool) {
	if raw == "" || nonce == "" {
		return shellDelta{}, false
	}
	// Check Windows file-delta format first.
	deltaHeader := "#__OMP_DELTA_" + nonce + "__"
	if hdrIdx := findMarkerLine(raw, deltaHeader); hdrIdx >= 0 {
		endPost := "#__OMP_END_POST_" + nonce + "__"
		postEndIdx := findMarkerLine(raw[hdrIdx:], endPost)
		if postEndIdx < 0 {
			return shellDelta{}, false
		}
		body := raw[hdrIdx+len(deltaHeader) : hdrIdx+postEndIdx]
		return parseFramedDelta(body, nonce), true
	}

	// Unix pipe format: BASE snapshot followed by POST snapshot.
	endBase := "#__OMP_END_BASE_" + nonce + "__"
	endPost := "#__OMP_END_POST_" + nonce + "__"

	baseEndIdx := findMarkerLine(raw, endBase)
	if baseEndIdx < 0 {
		return shellDelta{}, false
	}
	basePart := raw[:baseEndIdx]
	afterBase := raw[baseEndIdx+len(endBase):]
	if strings.HasPrefix(afterBase, "\n") {
		afterBase = afterBase[1:]
	}
	postEndIdx := findMarkerLine(afterBase, endPost)
	if postEndIdx < 0 {
		return shellDelta{}, false
	}
	postPart := afterBase[:postEndIdx]

	base := parseFramedSnapshot(basePart, nonce)
	post := parseFramedSnapshot(postPart, nonce)
	return computeShellDelta(base, post), true
}

// parseFramedDelta parses the pre-diffed Windows file format produced by the
// in-bash awk delta filter (containing ONLY changed/added/removed records,
// never unchanged inherited variables).
func parseFramedDelta(text, nonce string) shellDelta {
	d := shellDelta{
		setVars:     make(map[string]string),
		unsetVars:   make(map[string]bool),
		setFuncs:    make(map[string]string),
		unsetFuncs:  make(map[string]bool),
		setAliases:  make(map[string]string),
		unsetAlias:  make(map[string]bool),
		shoptChange: make(map[string]bool),
		setChange:   make(map[string]bool),
	}
	setV := "#__OMP_DV_SET_" + nonce + "__ "
	unsetV := "#__OMP_DV_UNSET_" + nonce + "__ "
	setF := "#__OMP_DF_SET_" + nonce + "__ "
	unsetF := "#__OMP_DF_UNSET_" + nonce + "__ "
	setA := "#__OMP_DA_SET_" + nonce + "__ "
	unsetA := "#__OMP_DA_UNSET_" + nonce + "__ "
	optSet := "#__OMP_DO_" + nonce + "__ "
	optEnd := "#__OMP_DEND_" + nonce + "__"

	curKind := ""
	curName := ""
	var curBuf strings.Builder

	flush := func() {
		if curKind == "" || curName == "" {
			curKind = ""
			curName = ""
			curBuf.Reset()
			return
		}
		body := strings.TrimSuffix(curBuf.String(), "\n")
		if body != "" {
			switch curKind {
			case "v":
				if strings.HasPrefix(body, "declare ") && !isReadonlyDeclare(body) {
					if len(body) > maxStateRecordBytes {
						d.skippedVars = append(d.skippedVars, curName)
					} else {
						d.setVars[curName] = body
					}
				}
			case "f":
				if len(body) > maxStateRecordBytes {
					d.skippedVars = append(d.skippedVars, curName+"()")
				} else {
					d.setFuncs[curName] = body
				}
			case "a":
				if strings.HasPrefix(body, "alias ") {
					if len(body) > maxStateRecordBytes {
						d.skippedVars = append(d.skippedVars, "alias:"+curName)
					} else {
						d.setAliases[curName] = body
					}
				}
			}
		}
		curKind = ""
		curName = ""
		curBuf.Reset()
	}

	tmpSnap := newShellSnapshot()
	for _, line := range strings.Split(text, "\n") {
		clean := strings.TrimSuffix(line, "\r")
		switch {
		case clean == optEnd:
			flush()
			continue
		case strings.HasPrefix(clean, setV):
			flush()
			curKind = "v"
			curName = strings.TrimSpace(strings.TrimPrefix(clean, setV))
			continue
		case strings.HasPrefix(clean, unsetV):
			flush()
			if name := strings.TrimSpace(strings.TrimPrefix(clean, unsetV)); name != "" {
				d.unsetVars[name] = true
			}
			continue
		case strings.HasPrefix(clean, setF):
			flush()
			curKind = "f"
			curName = strings.TrimSpace(strings.TrimPrefix(clean, setF))
			continue
		case strings.HasPrefix(clean, unsetF):
			flush()
			if name := strings.TrimSpace(strings.TrimPrefix(clean, unsetF)); name != "" {
				d.unsetFuncs[name] = true
			}
			continue
		case strings.HasPrefix(clean, setA):
			flush()
			curKind = "a"
			curName = strings.TrimSpace(strings.TrimPrefix(clean, setA))
			continue
		case strings.HasPrefix(clean, unsetA):
			flush()
			if name := strings.TrimSpace(strings.TrimPrefix(clean, unsetA)); name != "" {
				d.unsetAlias[name] = true
			}
			continue
		case strings.HasPrefix(clean, optSet):
			flush()
			optLine := strings.TrimPrefix(clean, optSet)
			if strings.HasPrefix(optLine, "FL ") {
				rest := strings.TrimPrefix(optLine, "FL ")
				flags, shellopts, _ := strings.Cut(rest, " ")
				d.setChange["errexit"] = strings.ContainsRune(flags, 'e')
				d.setChange["pipefail"] = strings.Contains(":"+shellopts+":", ":pipefail:")
				d.setChange["xtrace"] = strings.ContainsRune(flags, 'x')
			} else {
				parseOptionLine(tmpSnap, optLine)
			}
			continue
		}
		if curKind != "" {
			curBuf.WriteString(line)
			curBuf.WriteByte('\n')
		}
	}
	flush()
	for k, v := range tmpSnap.shoptOpts {
		d.shoptChange[k] = v
	}
	for k, v := range tmpSnap.setOpts {
		d.setChange[k] = v
	}
	sort.Strings(d.skippedVars)
	return d
}

// findMarkerLine finds marker as an exact whole line in s and returns its
// byte index, or -1 if absent.
func findMarkerLine(s, marker string) int {
	pos := 0
	for {
		idx := strings.Index(s[pos:], marker)
		if idx < 0 {
			return -1
		}
		abs := pos + idx
		atStart := abs == 0 || s[abs-1] == '\n'
		end := abs + len(marker)
		atEnd := end == len(s) || s[end] == '\n' || (s[end] == '\r' && (end+1 == len(s) || s[end+1] == '\n'))
		if atStart && atEnd {
			return abs
		}
		pos = abs + 1
	}
}

func parseFramedSnapshot(text, nonce string) *shellSnapshot {
	snap := newShellSnapshot()
	vStart := "#__OMP_V0_" + nonce + "__"
	vRec := "#__OMP_VR_" + nonce + "__ "
	fStart := "#__OMP_F0_" + nonce + "__"
	fRec := "#__OMP_FR_" + nonce + "__ "
	aStart := "#__OMP_A0_" + nonce + "__"
	aRec := "#__OMP_AR_" + nonce + "__ "
	oStart := "#__OMP_O0_" + nonce + "__"
	flRec := "#__OMP_FL_" + nonce + "__ "

	section := ""
	curKind := ""
	curName := ""
	var curBuf strings.Builder

	flush := func() {
		if curKind == "" || curName == "" {
			curKind = ""
			curName = ""
			curBuf.Reset()
			return
		}
		body := strings.TrimSuffix(curBuf.String(), "\n")
		if body != "" {
			switch curKind {
			case "v":
				if strings.HasPrefix(body, "declare ") {
					snap.vars[curName] = body
				}
			case "f":
				snap.funcs[curName] = body
			case "a":
				if strings.HasPrefix(body, "alias ") {
					snap.aliases[curName] = body
				}
			}
		}
		curKind = ""
		curName = ""
		curBuf.Reset()
	}

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		cleanLine := strings.TrimSuffix(line, "\r")
		switch {
		case cleanLine == vStart:
			flush()
			section = "v"
			continue
		case cleanLine == fStart:
			flush()
			section = "f"
			continue
		case cleanLine == aStart:
			flush()
			section = "a"
			continue
		case cleanLine == oStart:
			flush()
			section = "o"
			continue
		case section == "v" && strings.HasPrefix(cleanLine, vRec):
			flush()
			curKind = "v"
			curName = strings.TrimSpace(strings.TrimPrefix(cleanLine, vRec))
			continue
		case section == "f" && strings.HasPrefix(cleanLine, fRec):
			flush()
			curKind = "f"
			curName = strings.TrimSpace(strings.TrimPrefix(cleanLine, fRec))
			continue
		case section == "a" && strings.HasPrefix(cleanLine, aRec):
			flush()
			curKind = "a"
			curName = strings.TrimSpace(strings.TrimPrefix(cleanLine, aRec))
			continue
		case section == "o" && strings.HasPrefix(cleanLine, flRec):
			flush()
			rest := strings.TrimPrefix(cleanLine, flRec)
			flags, shellopts, _ := strings.Cut(rest, " ")
			// Authoritative errexit/xtrace from `$-` and pipefail from `$SHELLOPTS`.
			snap.setOpts["errexit"] = strings.ContainsRune(flags, 'e')
			snap.setOpts["pipefail"] = strings.Contains(":"+shellopts+":", ":pipefail:")
			snap.setOpts["xtrace"] = strings.ContainsRune(flags, 'x')
			continue
		}

		switch section {
		case "v", "f", "a":
			if curKind != "" {
				curBuf.WriteString(line)
				curBuf.WriteByte('\n')
			}
		case "o":
			parseOptionLine(snap, cleanLine)
		}
	}
	flush()
	return snap
}

func parseOptionLine(snap *shellSnapshot, line string) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "shopt -s ") || strings.HasPrefix(trimmed, "shopt -u ") {
		enabled := strings.HasPrefix(trimmed, "shopt -s ")
		opt := strings.TrimSpace(trimmed[len("shopt -s "):])
		switch opt {
		case "", "login_shell", "restricted_shell", "expand_aliases":
			return
		}
		snap.shoptOpts[opt] = enabled
		return
	}
	if strings.HasPrefix(trimmed, "set -o ") || strings.HasPrefix(trimmed, "set +o ") {
		enabled := strings.HasPrefix(trimmed, "set -o ")
		opt := strings.TrimSpace(trimmed[len("set -o "):])
		switch opt {
		case "", "monitor", "interactive", "stdin", "hashall", "history", "histexpand", "errexit", "pipefail", "xtrace":
			// errexit, pipefail, and xtrace are captured authoritatively from $- / $SHELLOPTS.
			return
		}
		snap.setOpts[opt] = enabled
	}
}

// sessionState persists shell state (new/changed variables, functions, aliases,
// shell options, and removals) across calls for one session using structured
// in-memory state + an atomic 0600 state.sh file for bash to source.
//
// Also holds this session's active PGIDs so every per-session shell state
// lives in ONE table keyed by sessionID.
type sessionState struct {
	mu      sync.Mutex
	dir     string // directory holding state.sh (mode 0700)
	file    string // path to state.sh (mode 0600)
	created bool
	snap    *shellSnapshot
	pgids   map[int]struct{} // active process groups spawned in this session, reaped on EndSession
}

func (s *sessionState) ensureDirLocked() error {
	if s.file != "" {
		return nil
	}
	sweepStaleStateDirs()
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s%d-", stateDirPrefix, os.Getpid()))
	if err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	s.dir = dir
	s.file = filepath.Join(dir, "state.sh")
	return nil
}

// prepareRun returns a read-only copy of the current snapshot (or "" if none
// yet), an optional fallback capture file path (used only on platforms without
// ExtraFiles pipes, i.e. Windows), a per-run framing nonce, a per-run high FD
// number, and a commit callback invoked with (ok, captured, truncated) when the
// command finishes. commit returns an optional user/model-facing notice when
// state capture was incomplete or skipped oversized records.
func (s *sessionState) prepareRun() (
	loadPath string,
	fallbackSavePath string,
	nonce string,
	pipeFD int,
	commit func(ok bool, captured string, truncated bool) string,
) {
	nonce = randomStateNonce()
	pipeFD = randomStatePipeFD()
	s.mu.Lock()
	_ = s.ensureDirLocked()
	dir := s.dir
	if s.created && s.file != "" {
		// Copy current snapshot to a per-run temp file so a concurrent commit
		// cannot swap it out while bash is sourcing it.
		if data, err := os.ReadFile(s.file); err == nil {
			if f, err := os.CreateTemp(dir, "load-*.sh"); err == nil {
				loadPath = f.Name()
				_ = f.Chmod(0o600)
				_, _ = f.Write(data)
				_ = f.Close()
			}
		}
	}
	if !statePipeSupported && dir != "" {
		if f, err := os.CreateTemp(dir, "save-*.sh"); err == nil {
			fallbackSavePath = f.Name()
			_ = f.Close()
			_ = os.Remove(fallbackSavePath)
		}
	}
	s.mu.Unlock()

	return loadPath, fallbackSavePath, nonce, pipeFD, func(ok bool, captured string, truncated bool) string {
		if loadPath != "" {
			_ = os.Remove(loadPath)
		}
		if fallbackSavePath != "" {
			if ok && captured == "" {
				if raw, err := os.ReadFile(fallbackSavePath); err == nil {
					captured = string(raw)
				}
			}
			_ = os.Remove(fallbackSavePath)
		}
		if !ok {
			return ""
		}
		if truncated {
			return fmt.Sprintf("Shell state changes of this call were not saved (capture exceeded %s limit); earlier session state was kept.", formatBytes(maxStateCaptureBytes))
		}
		if captured == "" {
			return "Shell state changes of this call were not saved (state capture was closed or intercepted); earlier session state was kept."
		}
		delta, complete := parseCapturedStateStream(captured, nonce)
		if !complete {
			return "Shell state changes of this call were not saved (shell exited without running the state capture trap, e.g. exec or overridden EXIT trap); earlier session state was kept."
		}

		var notice string
		if len(delta.skippedVars) > 0 {
			notice = fmt.Sprintf("Skipped persisting oversized shell entries (>%s): %s.", formatBytes(maxStateRecordBytes), strings.Join(delta.skippedVars, ", "))
		}

		// Skip rewriting state.sh when nothing actually changed (e.g. trivial
		// commands with no variable/function/alias/option mutations).
		if delta.isEmpty() {
			return notice
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.dir == "" || s.file == "" {
			// Session was disposed while this call was in flight.
			return notice
		}
		if s.snap == nil {
			s.snap = newShellSnapshot()
		}
		s.snap.applyDelta(delta)
		rendered := s.snap.renderScript()

		tmp, tmpErr := os.CreateTemp(s.dir, "commit-*.sh")
		if tmpErr != nil {
			return notice
		}
		tmpPath := tmp.Name()
		_ = tmp.Chmod(0o600)
		_, wErr := tmp.WriteString(rendered)
		cErr := tmp.Close()
		if wErr != nil || cErr != nil {
			_ = os.Remove(tmpPath)
			return notice
		}
		if err := os.Rename(tmpPath, s.file); err != nil {
			_ = os.WriteFile(s.file, []byte(rendered), 0o600)
			_ = os.Remove(tmpPath)
		}
		s.created = true
		return notice
	}
}

// snapshotReadOnly returns a temporary copy of the current snapshot (plus a
// cleanup func) so async jobs and services can seed from session state without
// mutating it or racing with commits.
func (s *sessionState) snapshotReadOnly() (string, func()) {
	s.mu.Lock()
	if !s.created || s.file == "" || s.dir == "" {
		s.mu.Unlock()
		return "", func() {}
	}
	data, err := os.ReadFile(s.file)
	dir := s.dir
	s.mu.Unlock()
	if err != nil || len(data) == 0 {
		return "", func() {}
	}
	f, err := os.CreateTemp(dir, "seed-*.sh")
	if err != nil {
		return "", func() {}
	}
	p := f.Name()
	_ = f.Chmod(0o600)
	_, _ = f.Write(data)
	_ = f.Close()
	return p, func() { _ = os.Remove(p) }
}

// trackPGID records a spawned process group belonging to this session so any
// `&`-backgrounded descendants are reaped when the session ends, pruning dead
// groups on every spawn so a recycled PGID is never killed later.
func (s *sessionState) trackPGID(pgid int) {
	if pgid <= 0 {
		return
	}
	s.mu.Lock()
	if s.pgids == nil {
		s.pgids = make(map[int]struct{})
	}
	for id := range s.pgids {
		if id != pgid && !isProcessGroupAlive(id) {
			delete(s.pgids, id)
		}
	}
	s.pgids[pgid] = struct{}{}
	s.mu.Unlock()
}

// releasePGID removes pgid as soon as the command's process group has emptied
// (and prunes any other dead groups) so a later PID wrap can never cause
// session disposal to signal an unrelated process group.
func (s *sessionState) releasePGID(pgid int) {
	if pgid <= 0 {
		return
	}
	s.mu.Lock()
	if !isProcessGroupAlive(pgid) {
		delete(s.pgids, pgid)
	}
	for id := range s.pgids {
		if id != pgid && !isProcessGroupAlive(id) {
			delete(s.pgids, id)
		}
	}
	s.mu.Unlock()
}

// dispose kills any lingering process groups started in this session and
// removes the session's snapshot directory and approval cache.
func (s *sessionState) dispose() {
	s.mu.Lock()
	dir := s.dir
	s.dir = ""
	s.file = ""
	s.created = false
	s.snap = nil
	pgids := make([]int, 0, len(s.pgids))
	for id := range s.pgids {
		pgids = append(pgids, id)
	}
	s.pgids = nil
	s.mu.Unlock()
	for _, pgid := range pgids {
		if isProcessGroupAlive(pgid) {
			killProcessGroupImmediate(pgid)
		}
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// stateIgnoredVarCase lists internal / self-changing bash & runtime variables
// excluded from snapshot diffing.
// Note: `IFS` and `PS4` are intentionally NOT ignored (persisted like omp's
// persistent shell; `IFS` is dumped before we reset `IFS` inside the dump).
// Self-changing bash variables (`BASHPID`, `EPOCHREALTIME`, `EPOCHSECONDS`,
// `RANDOM`, `SRANDOM`, `SECONDS`, `LINENO`, `BASH_COMMAND`, `BASH_*`, `_`,
// `PIPESTATUS`, `FUNCNAME`, `SHLVL`, `PWD`, `OLDPWD`, `PPID`, `UID`, `EUID`,
// `GROUPS`, `HISTCMD`, `REPLY`, `OPTIND`, `OPTARG`) are excluded so unchanged
// calls do not produce spurious diffs.
const stateIgnoredVarCase = `BASH|BASH_*|BASHPID|COMP_*|COPROC|DIRSTACK|EPOCHREALTIME|EPOCHSECONDS|EUID|FUNCNAME|GROUPS|HISTCMD|HOSTNAME|HOSTTYPE|IFS|LINENO|MACHTYPE|OLDPWD|OPTARG|OPTIND|OSTYPE|PIPESTATUS|PPID|PS1|PS2|PS3|PWD|RANDOM|READLINE_*|REPLY|SECONDS|SHELLOPTS|SHLVL|SRANDOM|UID|_|__omp_*|PI_SESSION_ID|PI_SESSION_FILE|PI_TOOL_CALL_ID`

func buildStateDumpBodyOrdered(nonce, endMarker string, inTrap bool) string {
	var b strings.Builder
	if !inTrap {
		b.WriteString("__omp_fl=\"${__omp_base_fl:-$-}\"\n")
		b.WriteString("__omp_so=\"${__omp_base_so:-${SHELLOPTS:-}}\"\n")
	}
	// Emit options header + `set +o` BEFORE disabling `-e/-u/-a/-x/-v` so every
	// user option (including allexport, nounset, noclobber) is recorded accurately.
	fmt.Fprintf(&b, "printf '#__OMP_O0_%s__\\n'\n", nonce)
	b.WriteString("set +o 2>/dev/null || true\n")
	b.WriteString("set +e +u +a +x +v 2>/dev/null || true\n")
	b.WriteString("shopt -p 2>/dev/null || true\n")
	fmt.Fprintf(&b, "printf '#__OMP_FL_%s__ %%s %%s\\n' \"$__omp_fl\" \"$__omp_so\"\n", nonce)

	// Variables: capture `IFS` FIRST before setting `IFS=$' \t\n'` for `compgen`,
	// then frame each `declare -p "$v"` with a nonce header so multi-line values
	// (bash 3.2) and values containing `declare ...` stay opaque.
	fmt.Fprintf(&b, "printf '#__OMP_V0_%s__\\n'\n", nonce)
	fmt.Fprintf(&b, "if [ -n \"${IFS+x}\" ]; then printf '#__OMP_VR_%s__ IFS\\n'; declare -p IFS 2>/dev/null || true; fi\n", nonce)
	b.WriteString("IFS=$' \\t\\n'\n")
	b.WriteString("for __omp_v in $(compgen -v 2>/dev/null); do\n")
	b.WriteString("  case \"$__omp_v\" in\n")
	fmt.Fprintf(&b, "    (%s) continue ;;\n", stateIgnoredVarCase)
	b.WriteString("  esac\n")
	fmt.Fprintf(&b, "  printf '#__OMP_VR_%s__ %%s\\n' \"$__omp_v\"\n", nonce)
	b.WriteString("  declare -p \"$__omp_v\" 2>/dev/null || true\n")
	b.WriteString("done\n")

	// Functions: frame each `declare -f "$fn"` with a nonce header.
	fmt.Fprintf(&b, "printf '#__OMP_F0_%s__\\n'\n", nonce)
	b.WriteString("for __omp_v in $(compgen -A function 2>/dev/null); do\n")
	b.WriteString("  case \"$__omp_v\" in (__omp_*) continue ;; esac\n")
	fmt.Fprintf(&b, "  printf '#__OMP_FR_%s__ %%s\\n' \"$__omp_v\"\n", nonce)
	b.WriteString("  declare -f \"$__omp_v\" 2>/dev/null || true\n")
	b.WriteString("done\n")

	// Aliases: frame each `alias -- "$a"` with a nonce header so multi-line
	// aliases cannot spoof records.
	fmt.Fprintf(&b, "printf '#__OMP_A0_%s__\\n'\n", nonce)
	b.WriteString("for __omp_v in $(compgen -a 2>/dev/null); do\n")
	fmt.Fprintf(&b, "  printf '#__OMP_AR_%s__ %%s\\n' \"$__omp_v\"\n", nonce)
	b.WriteString("  alias -- \"$__omp_v\" 2>/dev/null || true\n")
	b.WriteString("done\n")

	fmt.Fprintf(&b, "printf '%s\\n'\n", endMarker)
	return b.String()
}

// buildWindowsAwkDeltaScript emits a POSIX awk program (run inside the EXIT
// trap on platforms without ExtraFiles pipes) that diffs in-memory BASE and
// POST snapshots and writes ONLY changed/added/removed records to saveFile,
// guaranteeing inherited environment secrets NEVER touch disk unless the user
// command itself modified them.
func buildWindowsAwkDeltaScript(nonce string) string {
	return fmt.Sprintf(`
function flush_rec() {
  if (cur_k == "" || cur_n == "") { cur_k = ""; cur_n = ""; cur_b = ""; return }
  if (phase == 0) {
    if (cur_k == "v") bv[cur_n] = cur_b
    else if (cur_k == "f") bf[cur_n] = cur_b
    else if (cur_k == "a") ba[cur_n] = cur_b
  } else if (phase == 1) {
    if (cur_k == "v") pv[cur_n] = cur_b
    else if (cur_k == "f") pf[cur_n] = cur_b
    else if (cur_k == "a") pa[cur_n] = cur_b
  }
  cur_k = ""; cur_n = ""; cur_b = ""
}
BEGIN {
  phase = 0; sec = ""; cur_k = ""; cur_n = ""; cur_b = ""
  m_end_b = "#__OMP_END_BASE_%s__"
  m_end_p = "#__OMP_END_POST_%s__"
  m_v0 = "#__OMP_V0_%s__"; m_vr = "#__OMP_VR_%s__ "
  m_f0 = "#__OMP_F0_%s__"; m_fr = "#__OMP_FR_%s__ "
  m_a0 = "#__OMP_A0_%s__"; m_ar = "#__OMP_AR_%s__ "
  m_o0 = "#__OMP_O0_%s__"; m_fl = "#__OMP_FL_%s__ "
}
{
  line = $0
  sub(/\r$/, "", line)
  if (line == m_end_b) { flush_rec(); phase = 1; sec = ""; next }
  if (line == m_end_p) { flush_rec(); phase = 2; sec = ""; next }
  if (line == m_v0) { flush_rec(); sec = "v"; next }
  if (line == m_f0) { flush_rec(); sec = "f"; next }
  if (line == m_a0) { flush_rec(); sec = "a"; next }
  if (line == m_o0) { flush_rec(); sec = "o"; next }
  if (sec == "v" && index(line, m_vr) == 1) { flush_rec(); cur_k = "v"; cur_n = substr(line, length(m_vr)+1); next }
  if (sec == "f" && index(line, m_fr) == 1) { flush_rec(); cur_k = "f"; cur_n = substr(line, length(m_fr)+1); next }
  if (sec == "a" && index(line, m_ar) == 1) { flush_rec(); cur_k = "a"; cur_n = substr(line, length(m_ar)+1); next }
  if (sec == "o") {
    if (index(line, m_fl) == 1) {
      if (phase == 0) b_fl = substr(line, length(m_fl)+1)
      else if (phase == 1) p_fl = substr(line, length(m_fl)+1)
    } else if (line != "") {
      if (phase == 0) bo[line] = 1
      else if (phase == 1) po[line] = 1
    }
    next
  }
  if (cur_k != "") {
    cur_b = (cur_b == "" ? $0 : cur_b "\n" $0)
  }
}
END {
  if (phase < 2) exit 0
  print "#__OMP_DELTA_%s__"
  for (n in bv) { if (!(n in pv)) print "#__OMP_DV_UNSET_%s__ " n }
  for (n in pv) { if (!(n in bv) || bv[n] != pv[n]) { print "#__OMP_DV_SET_%s__ " n; print pv[n] } }
  for (n in bf) { if (!(n in pf)) print "#__OMP_DF_UNSET_%s__ " n }
  for (n in pf) { if (!(n in bf) || bf[n] != pf[n]) { print "#__OMP_DF_SET_%s__ " n; print pf[n] } }
  for (n in ba) { if (!(n in pa)) print "#__OMP_DA_UNSET_%s__ " n }
  for (n in pa) { if (!(n in ba) || ba[n] != pa[n]) { print "#__OMP_DA_SET_%s__ " n; print pa[n] } }
  print "#__OMP_DEND_%s__"
  for (o in po) { if (!(o in bo)) print "#__OMP_DO_%s__ " o }
  if (p_fl != b_fl && p_fl != "") print "#__OMP_DO_%s__ FL " p_fl
  print "#__OMP_END_POST_%s__"
}
`, nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce,
		nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce, nonce)
}

// wrapCommandWithState wraps a bash command so it:
//  1. Restores prior session state from loadFile (if non-empty), immediately
//     suppressing persisted `set -x` around the wrapper's own setup lines and
//     restoring it right before `eval` so traces never include wrapper lines.
//  2. Sources preambleFile (if non-empty) silently AFTER restoring session
//     options (including `allexport`) so tool-command functions take precedence
//     over user aliases/functions and preamble variables/functions appear in the
//     baseline (never emitted into the user delta or written to state.sh).
//  3. When capturing state:
//     - Unix (`usePipe == true`): moves ExtraFiles[0] (fd 3) onto per-call
//     random high FD `pipeFD`, streams BASE in an isolated subshell (zero
//     `__omp_*` vars/funcs exposed to user command), installs an EXIT trap
//     that verifies `/dev/fd/<pipeFD>` is still a pipe (`[ -p ... ] && [ ! -f ... ]`)
//     before streaming POST (preventing `exec <fd>>file` from capturing the
//     post dump or inherited environment into a file).
//     - Windows (`usePipe == false`, `saveFile != ""`): keeps BASE in memory
//     (never writes BASE or inherited env to disk!) and diffs BASE vs POST
//     in-memory inside the EXIT trap via `awk`, writing ONLY changed/added/
//     removed records to `saveFile`.
func wrapCommandWithState(command, loadFile, saveFile, preambleFile, nonce string, usePipe bool, pipeFD int) string {
	if pipeFD <= 0 {
		pipeFD = 73
	}
	var b strings.Builder
	b.WriteString("shopt -s expand_aliases 2>/dev/null || true\n")

	if loadFile != "" {
		fmt.Fprintf(&b, "if [ -r %s ]; then . %s 2>/dev/null || true; fi\n", singleQuotePosix(loadFile), singleQuotePosix(loadFile))
		// Immediately suppress persisted xtrace around wrapper internals (T5).
		b.WriteString("{ __omp_xt=\"\"; case \"$-\" in *x*) __omp_xt=1; set +x ;; esac; } 2>/dev/null\n")
		b.WriteString("shopt -s expand_aliases 2>/dev/null || true\n")
	}

	if preambleFile != "" {
		qPreamble := singleQuotePosix(preambleFile)
		fmt.Fprintf(&b, "if [ -r %s ]; then . %s >/dev/null 2>&1 || true; fi\n", qPreamble, qPreamble)
		b.WriteString("shopt -s expand_aliases 2>/dev/null || true\n")
	}

	capturing := nonce != "" && (usePipe || saveFile != "")
	if capturing {
		endBase := "#__OMP_END_BASE_" + nonce + "__"
		endPost := "#__OMP_END_POST_" + nonce + "__"
		baseBody := buildStateDumpBodyOrdered(nonce, endBase, false)
		postBody := buildStateDumpBodyOrdered(nonce, endPost, true)

		if usePipe {
			// Move Go's ExtraFiles[0] (fd 3) onto per-call high pipeFD so fd 3
			// stays free and no fixed FD number is predictable.
			fmt.Fprintf(&b, "{ exec %d>&3 3>&-; } 2>/dev/null || true\n", pipeFD)
			// Capture baseline in an isolated subshell (passing saved xtrace flag
			// via __omp_base_fl so `$-` reflects persisted xtrace even while
			// xtrace is suppressed during wrapper setup). Zero `__omp_*` vars or
			// functions remain in the parent shell when `eval` runs.
			b.WriteString("(\n")
			b.WriteString("__omp_base_fl=\"$-\"; if [ -n \"${__omp_xt:-}\" ]; then __omp_base_fl=\"${__omp_base_fl}x\"; fi\n")
			b.WriteString("__omp_base_so=\"${SHELLOPTS:-}\"; if [ -n \"${__omp_xt:-}\" ] && [ \"${__omp_base_so#*xtrace}\" = \"$__omp_base_so\" ]; then __omp_base_so=\"${__omp_base_so}:xtrace\"; fi\n")
			fmt.Fprintf(&b, "%s:\n) >&%d 2>/dev/null || true\n", baseBody, pipeFD)

			// Before emitting POST into pipeFD, guard against a user command
			// redirecting pipeFD to a regular file (`exec <fd>>file` on bash 5):
			// only write when `/dev/fd/<pipeFD>` is a pipe and not a regular file
			// (when /dev/fd exists).
			fdGuard := fmt.Sprintf("if [ ! -e /dev/fd/%d ] || { [ -p /dev/fd/%d ] && [ ! -f /dev/fd/%d ]; }; then\n", pipeFD, pipeFD, pipeFD)
			trapScript := fmt.Sprintf(
				"{ __omp_rc=$?; __omp_fl=\"${__omp_exit_fl:-$-}\"; __omp_so=\"${__omp_exit_so:-${SHELLOPTS:-}}\"; set +x; } 2>/dev/null\n%s{\n%s} >&%d 2>/dev/null || true\nfi\n{ exec %d>&-; } 2>/dev/null || true\nexit $__omp_rc",
				fdGuard, postBody, pipeFD, pipeFD,
			)
			fmt.Fprintf(&b, "trap %s EXIT\n", singleQuotePosix(trapScript))
		} else {
			// Windows file-fallback path (no ExtraFiles pipe): keep BASE strictly
			// in memory (`__omp_win_base`), mark it `readonly` so user commands
			// cannot clear or overwrite it, and diff BASE vs POST inside the EXIT
			// trap with `awk` ONLY when `__omp_win_base` is non-empty and carries
			// its end marker — writing ONLY changed/added/removed records to
			// saveFile so inherited environment secrets NEVER touch disk (T1, U3).
			qSave := singleQuotePosix(saveFile)
			b.WriteString("__omp_win_base=$(\n")
			b.WriteString("__omp_base_fl=\"$-\"; if [ -n \"${__omp_xt:-}\" ]; then __omp_base_fl=\"${__omp_base_fl}x\"; fi\n")
			b.WriteString("__omp_base_so=\"${SHELLOPTS:-}\"; if [ -n \"${__omp_xt:-}\" ] && [ \"${__omp_base_so#*xtrace}\" = \"$__omp_base_so\" ]; then __omp_base_so=\"${__omp_base_so}:xtrace\"; fi\n")
			fmt.Fprintf(&b, "%s:\n)\n", baseBody)
			b.WriteString("export -n __omp_win_base 2>/dev/null || true\n")
			b.WriteString("readonly __omp_win_base 2>/dev/null || true\n")

			awkProg := buildWindowsAwkDeltaScript(nonce)
			trapScript := fmt.Sprintf(
				"{ __omp_rc=$?; __omp_fl=\"${__omp_exit_fl:-$-}\"; __omp_so=\"${__omp_exit_so:-${SHELLOPTS:-}}\"; set +x; } 2>/dev/null\ncase \"${__omp_win_base:-}\" in\n  (*%s*)\n    __omp_win_post=$(\n%s:\n    )\n    printf '%%s\\n%%s\\n' \"$__omp_win_base\" \"$__omp_win_post\" | awk %s >| %s 2>/dev/null || true\n    ;;\nesac\nexit $__omp_rc",
				endBase, postBody, singleQuotePosix(awkProg), qSave,
			)
			fmt.Fprintf(&b, "trap %s EXIT\n", singleQuotePosix(trapScript))
		}
	}

	if loadFile != "" {
		// Restore persisted xtrace right before `eval` and drop `__omp_xt` so
		// no `__omp_*` variable is visible to the user command.
		b.WriteString("if [ -n \"${__omp_xt:-}\" ]; then unset __omp_xt; set -x; else unset __omp_xt; fi\n")
	}

	// Append a trailing newline inside the eval string AND follow `eval` with
	// a silent xtrace-saving exit so:
	//  - bash 3.2 with an EXIT trap does not trigger its EOF-last-eval parser
	//    bug on multi-line aliases at EOF, and
	//  - persisted `set -x` does not print `+ exit 0` from the wrapper.
	evalCmd := command
	if !strings.HasSuffix(evalCmd, "\n") {
		evalCmd += "\n"
	}
	fmt.Fprintf(&b, "eval %s\n", singleQuotePosix(evalCmd))
	b.WriteString("{ __omp_rc=$?; __omp_exit_fl=\"$-\"; __omp_exit_so=\"${SHELLOPTS:-}\"; set +x; } 2>/dev/null; exit $__omp_rc\n")
	return b.String()
}

// writePreambleTempFile writes preamble text to a temporary 0600 file and
// returns its path and a cleanup function. Returns ("", noop, nil) if preamble
// is empty.
func writePreambleTempFile(preamble string) (string, func(), error) {
	if strings.TrimSpace(preamble) == "" {
		return "", func() {}, nil
	}
	f, err := os.CreateTemp("", "omp-shell-preamble-*.sh")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	_ = f.Chmod(0o600)
	if _, wErr := f.WriteString(preamble); wErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", func() {}, wErr
	}
	if cErr := f.Close(); cErr != nil {
		_ = os.Remove(path)
		return "", func() {}, cErr
	}
	return path, func() { _ = os.Remove(path) }, nil
}
