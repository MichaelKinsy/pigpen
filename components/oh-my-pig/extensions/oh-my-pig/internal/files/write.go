package files

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

var (
	diagMu             sync.RWMutex
	diagHook           func(ctx sdk.Context, path string) string
	editDiagHook       func(ctx sdk.Context, path string) string
	recordChangeHook   func(ctx sdk.Context, tool, absPath string, before, after *string)
	recordChangeActive func(ctx sdk.Context) bool
)

// SetDiagnosticsHook installs a callback invoked after `write` mutates a file
// (matching omp's `tools/write.ts` `lsp.diagnosticsOnWrite` path).
// When non-empty, its return value is appended to the tool's result text and details.
func SetDiagnosticsHook(fn func(ctx sdk.Context, path string) string) {
	diagMu.Lock()
	defer diagMu.Unlock()
	diagHook = fn
}

// SetEditDiagnosticsHook installs a callback invoked after `edit` mutates a file
// (matching omp's `edit/index.ts` `lsp.diagnosticsOnEdit` path).
// When unset, `edit` appends no diagnostics (never falling back to SetDiagnosticsHook).
func SetEditDiagnosticsHook(fn func(ctx sdk.Context, path string) string) {
	diagMu.Lock()
	defer diagMu.Unlock()
	editDiagHook = fn
}

// SetRecordFileChangeHook installs a callback invoked after `write` or `edit`
// mutates a file on disk, matching omp's `ToolSession.recordFileChange`.
// upstream: packages/coding-agent/src/tools/write.ts:1080-1089, edit/index.ts:791-801
func SetRecordFileChangeHook(fn func(ctx sdk.Context, tool, absPath string, before, after *string)) {
	diagMu.Lock()
	defer diagMu.Unlock()
	recordChangeHook = fn
}

// SetRecordFileChangeActive installs a cheap predicate checked before doing any
// extra pre-write file reads for journaling (returns false when `commit.enabled`
// is false or the session has no artifacts directory).
func SetRecordFileChangeActive(fn func(ctx sdk.Context) bool) {
	diagMu.Lock()
	defer diagMu.Unlock()
	recordChangeActive = fn
}

func runRecordFileChangeHook(ctx sdk.Context, tool, absPath string, before, after *string) {
	diagMu.RLock()
	fn := recordChangeHook
	diagMu.RUnlock()
	if fn != nil {
		fn(ctx, tool, absPath, before, after)
	}
}

func isRecordFileChangeActive(ctx sdk.Context) bool {
	diagMu.RLock()
	fn := recordChangeHook
	active := recordChangeActive
	diagMu.RUnlock()
	if fn == nil {
		return false
	}
	if active != nil {
		return active(ctx)
	}
	return true
}

func runDiagnosticsHook(ctx sdk.Context, path string) string {
	diagMu.RLock()
	fn := diagHook
	diagMu.RUnlock()
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(ctx, path))
}

func runEditDiagnosticsHook(ctx sdk.Context, path string) string {
	diagMu.RLock()
	fn := editDiagHook
	diagMu.RUnlock()
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(ctx, path))
}

// executeWrite implements omp's `write` tool for local files and internal URLs.
// upstream: packages/coding-agent/src/tools/write.ts
func executeWrite(ctx sdk.Context, store *hashline.Store, cwd, rawPath string, content *string) (sdk.ToolResult, error) {
	unwrapped := hashline.UnwrapHeaderPath(rawPath)
	if content == nil {
		return sdk.ToolResult{}, fmt.Errorf("content is required for %s.", unwrapped)
	}
	// Peel a read-tool selector (`:raw`, `:conflicts`) from internal URLs and
	// recover an erroneous `<file>:conflict://N` prefix before routing.
	// upstream: packages/coding-agent/src/tools/write.ts:898-900
	peeled, peelErr := urls.PeelWriteSelector(unwrapped, "write")
	if peelErr != nil {
		return sdk.ToolResult{}, peelErr
	}
	unwrapped, recoveredNote := urls.RecoverConflictURIPrefix(peeled)
	rawContent := *content

	var (
		isURLTarget bool
		urlDisplay  string
		verbatim    bool
	)
	// Check URI-like near-misses before routing.
	// upstream: packages/coding-agent/src/tools/write.ts:921
	if err := assertWriteTargetAddressable(unwrapped); err != nil {
		return sdk.ToolResult{}, err
	}

	// Internal URL routing: handler-owned writes go to urls.Write; file-backed
	// schemes locate a path with Create: true; read-only schemes are rejected.
	// upstream: packages/coding-agent/src/tools/write.ts:922
	if urls.CanHandle(unwrapped) {
		u, err := urls.Parse(unwrapped)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		h, ok := urls.Lookup(u.Scheme)
		if !ok {
			return sdk.ToolResult{}, fmt.Errorf("Unknown internal URL: %s", unwrapped)
		}
		spec := h.Spec()
		uc := urls.Context{SDK: ctx, Cwd: cwd}
		if spec.Write == nil {
			// upstream: internal-urls/router.ts:414-419, tools/approval.ts:594-598
			return sdk.ToolResult{}, fmt.Errorf("Tool \"write\" is blocked by tool policy.\nReason: %s:// URLs are read-only", u.Scheme)
		}
		verbatim = spec.Write.Payload == "verbatim"

		if spec.Write.Via == "handler" {
			cleanContent := rawContent
			stripped := false
			if !verbatim {
				cleanContent, stripped = stripWriteContent(store, rawContent, "")
			}
			if !verbatim && endsWithReadTruncationNotice(rawContent) {
				var cur *string
				if res, rerr := urls.Resolve(uc, unwrapped); rerr == nil && res != nil && res.Bytes == nil {
					s := res.Content
					cur = &s
				}
				if err := assertNotShorterReadProjection(unwrapped, rawContent, cur, cleanContent); err != nil {
					return sdk.ToolResult{}, err
				}
			}
			touchesNoFiles := spec.Write.Scope == "coordination" ||
				spec.Write.Scope == "reply" ||
				(spec.Write.Cancels != nil && spec.Write.Cancels(u))
			if !touchesNoFiles {
				if err := gate.Check(ctx, gate.Request{Tool: "write", Op: gate.OpWrite, URL: unwrapped}); err != nil {
					return sdk.ToolResult{}, err
				}
			}
			wres, err := urls.Write(uc, unwrapped, cleanContent)
			if err != nil {
				return sdk.ToolResult{}, err
			}
			// upstream: packages/coding-agent/src/tools/write.ts:951-966
			// File-free handler writes (coordination, reply, cancel) always carry details.touchesNoFiles: true;
			// non-file-free handler writes return an empty details map rather than nil.
			if wres != nil {
				details := make(map[string]any, len(wres.Details)+1)
				for k, v := range wres.Details {
					details[k] = v
				}
				if u.Scheme == "conflict" {
					if rp, ok := details["resolvedPath"].(string); ok && rp != "" {
						details["resolvedPath"] = dirs.StandardizeMacOSPath(rp)
					}
				}
				if touchesNoFiles {
					details["touchesNoFiles"] = true
				}
				text := wres.Text
				if recoveredNote != "" {
					if text != "" {
						text += "\n" + recoveredNote
					} else {
						text = recoveredNote
					}
				}
				return sdk.ToolResult{Content: text, Details: details, IsError: wres.IsError}, nil
			}
			out := fmt.Sprintf("Successfully wrote %d bytes to %s", len(cleanContent), unwrapped)
			if stripped {
				out += "\nNote: auto-stripped hashline display prefixes from content before writing."
			}
			fileFreeDetails := map[string]any{}
			if touchesNoFiles {
				fileFreeDetails["touchesNoFiles"] = true
			}
			return sdk.ToolResult{Content: out, Details: fileFreeDetails}, nil
		}
		if spec.Write.Via == "file" {
			located, found, err := urls.Locate(uc, unwrapped, urls.LocateOptions{Create: true})
			if err != nil {
				return sdk.ToolResult{}, err
			}
			if !found || located == "" {
				return sdk.ToolResult{}, fmt.Errorf("Cannot locate writable path for %s", unwrapped)
			}
			isURLTarget = true
			urlDisplay = unwrapped
			unwrapped = located
		}
	}
	// Archive member writes (`archive.ext:member/path`) take precedence over literal
	// disk files, matching omp's write.ts:974-1006. For file-backed internal URLs
	// (`via: "file"`), `unwrapped` is the located filesystem path.
	if !isURLTarget {
		resolvedArchive, archErr := resolveArchiveWritePath(unwrapped, cwd)
		if archErr != nil {
			return sdk.ToolResult{}, archErr
		}
		if resolvedArchive != nil {
			cleanContent := rawContent
			stripped := false
			if !verbatim {
				cleanContent, stripped = stripWriteContent(store, rawContent, resolveToCwd(unwrapped, cwd))
			}
			return executeArchiveWrite(ctx, cwd, resolvedArchive, cleanContent, rawContent, stripped)
		}
	}

	absPath := resolveToCwd(unwrapped, cwd)
	// A located URL write keeps its URL identity in results and the hashline header.
	// upstream: packages/coding-agent/src/tools/write.ts:1029
	displayPath := formatPathRelativeToCwd(absPath, cwd)
	if isURLTarget {
		displayPath = urlDisplay
	}

	cleanContent := rawContent
	stripped := false
	if !verbatim {
		stripTarget := absPath
		if isURLTarget {
			stripTarget = ""
		}
		cleanContent, stripped = stripWriteContent(store, rawContent, stripTarget)
	}
	// upstream: packages/coding-agent/src/tools/write.ts:1026
	if !isURLTarget {
		if err := assertNotReadSelectorMisfire(unwrapped, cleanContent, cwd); err != nil {
			return sdk.ToolResult{}, err
		}
	}

	// Mode/plan gate check after resolving real target and before touching the file.
	// upstream: packages/coding-agent/src/tools/write.ts:1027
	if err := gate.Check(ctx, gate.Request{Tool: "write", Op: gate.OpWrite, Path: absPath, URL: urlDisplay}); err != nil {
		return sdk.ToolResult{}, err
	}

	var resultText string
	var details map[string]any
	writeFn := func() error {
		var oldText string
		var hasOldText bool
		var existingInfo os.FileInfo
		if info, err := os.Stat(absPath); err == nil {
			existingInfo = info
			if info.IsDir() {
				if isURLTarget {
					u, _ := urls.Parse(urlDisplay)
					return fmt.Errorf("%s:// URL must resolve to a file: %s", u.Scheme, urlDisplay)
				}
				return fmt.Errorf("Cannot write '%s': it is a directory.", displayPath)
			}
			if kind := specialFileKind(info); kind != "" {
				return fmt.Errorf("Cannot write '%s': it is a %s, not a regular file or directory.", displayPath, kind)
			}
			// Read only the head for the auto-generated marker guard; read the full old file
			// only when within SnapshotMaxBytes (4 MiB) so overwriting a huge file never blows memory.
			if head, headErr := readFilePrefix(absPath, 1024); headErr == nil {
				if msg := autoGeneratedMessage(displayPath, head); msg != "" {
					return fmt.Errorf("%s", msg)
				}
			}
			if info.Size() <= SnapshotMaxBytes {
				if prevBytes, readErr := os.ReadFile(absPath); readErr == nil {
					oldText = string(prevBytes)
					hasOldText = true
				}
			}
		}

		// Truncated-read projection guard.
		// upstream: packages/coding-agent/src/tools/write.ts:1053
		if endsWithReadTruncationNotice(rawContent) && existingInfo != nil {
			if hasOldText {
				cur := oldText
				if err := assertNotShorterReadProjection(displayPath, rawContent, &cur, cleanContent); err != nil {
					return err
				}
			} else {
				if err := assertNotShorterReadProjectionWithSize(displayPath, rawContent, existingInfo.Size(), cleanContent); err != nil {
					return err
				}
			}
		}
		// Preserve existing file's CRLF and UTF-8 BOM convention.
		// upstream: packages/coding-agent/src/tools/write.ts:1069
		var conv *textFileConvention
		if existingInfo != nil && existingInfo.Mode().IsRegular() {
			if c, err := readTextFileConvention(absPath); err == nil {
				conv = c
			}
		}
		writeContent, applied := applyTextFileConvention(cleanContent, conv)
		var journalBefore *string
		journalBeforeOK := false
		if isRecordFileChangeActive(ctx) {
			switch {
			case existingInfo == nil:
				// File did not exist at os.Stat above; no extra read needed.
				journalBefore = nil
				journalBeforeOK = true
			case hasOldText:
				// Reuse the pre-write bytes already read above for the diff/projection guard.
				s := oldText
				journalBefore = &s
				journalBeforeOK = true
			case existingInfo.Size() <= 8*1024*1024:
				// File between SnapshotMaxBytes (4 MiB) and journal MAX_SNAPSHOT_CHARS (8 MiB):
				// read once for journal replay.
				if b, rErr := os.ReadFile(absPath); rErr == nil {
					s := string(b)
					journalBefore = &s
					journalBeforeOK = true
				} else if os.IsNotExist(rErr) {
					journalBefore = nil
					journalBeforeOK = true
				}
			default:
				// File > 8 MiB exceeds MAX_SNAPSHOT_CHARS: record an unstored change without
				// reading the huge file into memory by passing a NUL sentinel (unstored in journal).
				s := "\x00"
				journalBefore = &s
				journalBeforeOK = true
			}
		}

		parent := filepath.Dir(absPath)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(absPath, []byte(writeContent), 0o644); err != nil {
			return err
		}
		if journalBeforeOK {
			after := writeContent
			runRecordFileChangeHook(ctx, "write", absPath, journalBefore, &after)
		}
		madeExecutable := maybeMarkExecutableForShebang(absPath, writeContent)

		_, bomStripped := hashline.StripBOM(writeContent)
		normalized := normalizeCRLFToLF(bomStripped)
		tag := store.RecordSnapshot(absPath, normalized)
		header := hashline.FormatHeader(displayPath, tag)
		writeLine := fmt.Sprintf("Successfully wrote %d bytes to %s", len(writeContent), displayPath)
		resultText = header + "\n" + writeLine
		if stripped {
			resultText += "\nNote: auto-stripped hashline display prefixes from content before writing."
		}
		if convNote := textFileConventionNote(applied, writeContent); convNote != "" {
			resultText += "\n" + convNote
		}
		if madeExecutable {
			resultText += "\n" + executableNotice
		}

		var patch string
		if (existingInfo == nil || hasOldText) && len(writeContent) <= SnapshotMaxBytes {
			patch = generateUnifiedPatch(ctx, displayPath, oldText, writeContent)
		}
		resolvedOut := absPath
		if !isURLTarget {
			resolvedOut = dirs.StandardizeMacOSPath(absPath)
		}
		details = map[string]any{
			"patch":        patch,
			"diff":         patch,
			"resolvedPath": resolvedOut,
		}
		if madeExecutable {
			details["madeExecutable"] = true
		}
		return nil
	}

	if err := withMutationQueue(ctx, absPath, writeFn); err != nil {
		return sdk.ToolResult{}, err
	}
	if diag := runDiagnosticsHook(ctx, absPath); diag != "" {
		resultText += "\n" + diag
		if details == nil {
			details = map[string]any{}
		}
		diagObj := parseDiagnosticsDetail(diag)
		details["diagnostics"] = diagObj
		details["meta"] = map[string]any{
			"diagnostics": map[string]any{
				"summary":  diagObj["summary"],
				"messages": diagObj["messages"],
			},
		}
	}
	return sdk.ToolResult{Content: resultText, Details: details}, nil
}

// parseDiagnosticsDetail converts the diagnostics hook's formatted text into omp's
// FileDiagnosticsResult shape (`summary`, `messages`, `errored`) for TUI cards.
// upstream: packages/tui/src/tools/lsp.ts:79
func parseDiagnosticsDetail(diag string) map[string]any {
	var messages []string
	summary := ""
	errored := false
	for line := range strings.SplitSeq(diag, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if summary == "" && (strings.HasPrefix(t, "LSP Diagnostics (") || strings.HasPrefix(t, "Diagnostics")) {
			if start := strings.IndexByte(t, '('); start >= 0 {
				if end := strings.LastIndexByte(t, ')'); end > start {
					summary = t[start+1 : end]
				}
			}
			continue
		}
		messages = append(messages, t)
		low := strings.ToLower(t)
		if strings.Contains(low, "error") || strings.Contains(low, "[err") {
			errored = true
		}
	}
	if len(messages) == 0 && strings.TrimSpace(diag) != "" {
		messages = []string{strings.TrimSpace(diag)}
		low := strings.ToLower(diag)
		errored = strings.Contains(low, "error")
	}
	if summary == "" {
		if len(messages) == 1 {
			summary = "1 diagnostic"
		} else {
			summary = fmt.Sprintf("%d diagnostics", len(messages))
		}
	}
	return map[string]any{
		"summary":  summary,
		"messages": messages,
		"errored":  errored,
	}
}

var (
	localQueueMu sync.Mutex
	localQueues  = map[string]*sync.Mutex{}
)

func localFileMutex(absPath string) *sync.Mutex {
	key := filepath.Clean(absPath)
	localQueueMu.Lock()
	defer localQueueMu.Unlock()
	m, ok := localQueues[key]
	if !ok {
		m = &sync.Mutex{}
		localQueues[key] = m
	}
	return m
}

func readFilePrefix(absPath string, limit int) ([]byte, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// withMutationQueue serializes writes through sdk.Context.WithFileMutationQueue when
// connected to a host (both subprocess and in-process fused hosts), falling back to
// a per-path mutex when ctx is zero or not connected (standalone unit tests).
// Panics thrown by fn itself are never swallowed: only a panic before fn starts
// (e.g. zero sdk.Context nil-receiver dereference inside hostConnection) triggers fallback.
func withMutationQueue(ctx sdk.Context, absPath string, fn func() error) error {
	ran := false
	var hostErr error
	var panicVal any
	var panicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicVal = r
				panicked = true
			}
		}()
		hostErr = ctx.WithFileMutationQueue(absPath, func() error {
			ran = true
			return fn()
		})
	}()
	if panicked {
		if ran {
			panic(panicVal)
		}
		m := localFileMutex(absPath)
		m.Lock()
		defer m.Unlock()
		return fn()
	}
	if ran {
		return hostErr
	}
	if hostErr != nil && strings.Contains(hostErr.Error(), "not connected to the host") {
		m := localFileMutex(absPath)
		m.Lock()
		defer m.Unlock()
		return fn()
	}
	return hostErr
}
