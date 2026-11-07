package posthorse

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Port of src/notes.ts: the `notes` tool.

// notesParams is NotesParams (notes.ts:26-33). The has* flags carry TS undefined.
type notesParams struct {
	op         string
	path       string
	content    string
	query      string
	scope      string
	offset     float64 // a JS number: a schema integer may exceed 2^53 (notes.ts:30)
	hasPath    bool
	hasContent bool
	hasQuery   bool
	hasScope   bool
}

// noteHits is NoteHits (notes.ts:34).
type noteHits struct {
	rows    []string
	more    bool
	skipped int
}

// notesAccess is NotesAccess (notes.ts:36-42). host takes a fresh policy snapshot after the
// filesystem awaits.
type notesAccess struct {
	ctx       sdk.Context
	host      func() policyContext
	pages     pageAccess
	call      noteCall
	discovery *noteDiscovery
	// TEMPORARY-GATE: TEMPORARY: remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph).
	// order is this execution's place in the start order of file mutations (notes_order.go); nil outside the tool.
	order *noteTicket
}

const (
	// Larger files are logs or evidence rather than notes (notes.ts:44).
	searchFileBytes = 1024 * 1024
	// Overlapping reads cut a full search of a 212,000-file notes tree from 39 s to 14 s (notes.ts:46).
	searchBatch = 16
	// Formatting all 212,751 rows of one listing at once stalled Pi for 156-163 ms (notes.ts:48).
	formatBatch = 10_000
	// Search rescans up to the requested offset, so this also caps the matches one call keeps (notes.ts:50).
	searchOffsetLimit = 1_000_000
)

// notesParamsOf decodes the tool arguments; the host has validated them against the schema.
func notesParamsOf(params map[string]any) notesParams {
	var p notesParams
	p.op, _ = params["op"].(string)
	p.path, p.hasPath = params["path"].(string)
	p.content, p.hasContent = params["content"].(string)
	p.query, p.hasQuery = params["query"].(string)
	p.scope, p.hasScope = params["scope"].(string)
	if offset, ok := params["offset"].(float64); ok {
		p.offset = offset
	}
	return p
}

// jsNumberString is the text of a JS number in a template literal (`${offset}`): Number::toString,
// which switches to exponent form at 1e21.
func jsNumberString(x float64) string {
	if x == 0 {
		return "0"
	}
	if x >= 1e21 || x <= -1e21 {
		return strconv.FormatFloat(x, 'e', -1, 64)
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// pagedOffset is the offset handed to the page helpers, which count in int. Offsets beyond 2^53
// only reach them in a listing (read and search check the offset against the text first), where
// they are past every possible end; the clamp keeps the conversion defined (docs: lane-NotesToolsLane.md).
func (p notesParams) pagedOffset() int { return int(min(p.offset, 1<<62)) }

// cooperate is `await setImmediate(); signal?.throwIfAborted()` (notes.ts:68-69): other
// goroutines run, then a cancelled call stops.
func cooperate(ctx sdk.Context) error {
	runtime.Gosched()
	return throwIfAborted(signalOf(ctx))
}

// appendISOMinute appends `new Date(ms).toISOString().slice(0, 16)` (notes.ts:78).
func appendISOMinute(dst []byte, mtimeMs float64) []byte {
	t := time.UnixMilli(int64(mtimeMs)).UTC()
	if year := t.Year(); year < 0 || year > 9999 {
		sign := "+"
		if year < 0 {
			sign, year = "-", -year
		}
		dst = append(dst, sign...)
		dst = append(dst, leftPad(strconv.Itoa(year), 6)...)
		// toISOString writes an expanded year as "+010000-01-01T00:00:00.000Z": slice(0, 16) ends after the hour.
		return t.AppendFormat(dst, "-01-02T15")
	}
	return t.AppendFormat(dst, "2006-01-02T15:04")
}

func leftPad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

func optionalScope(params notesParams) *string {
	if !params.hasScope {
		return nil
	}
	return &params.scope
}

func listNotes(session *sessionNotes, params notesParams, access notesAccess) (any, error) {
	ctx := access.ctx
	var folder *string
	if params.hasPath {
		folder = &params.path
	}
	scope := optionalScope(params)
	// notes.ts:54-62 runs list and selectScope together; both reject an unknown scope alike.
	dir := session.directory()
	if scope != nil {
		selected, err := session.selectScope(ctx, *scope)
		if err != nil {
			return nil, err
		}
		dir = scopeDirectory(selected)
	}
	inventory, err := access.discovery.list(ctx, session, discoveryOptions{path: folder, scope: scope})
	if err != nil {
		return nil, err
	}
	files := inventory.files
	rows := make([]string, 0, len(files)+len(inventory.notices))
	var row []byte
	for start := 0; start < len(files); start += formatBatch {
		// Pi gets an event-loop turn before each batch of rows (notes.ts:68).
		if err := cooperate(ctx); err != nil {
			return nil, err
		}
		end := min(start+formatBatch, len(files))
		for i := start; i < end; i++ {
			file := &files[i]
			row = append(row[:0], noteLabel(file, session, inventory.labeled)...)
			row = append(row, "  "...)
			row = append(row, sdk.FormatSize(int(file.size))...)
			row = append(row, "  "...)
			row = appendISOMinute(row, float64(file.mtimeMs))
			row = append(row, 'Z')
			rows = append(rows, string(row))
		}
	}
	rows = append(rows, inventory.notices...)
	empty := "(no notes yet)"
	if folder != nil && *folder != "" {
		empty = "(no notes in " + *folder + ")"
	}
	return access.pages.notes(access.host(), notesPage{
		rows:   rows,
		offset: params.pagedOffset(),
		kind:   "notes-list",
		empty:  empty,
		header: "Notes directory: " + dir + "\n",
	})
}

// statNoThrow is `statSync(path, { throwIfNoEntry: false })`: ENOENT and ENOTDIR give nil
// (Node 24: `statSync("/etc/passwd/x", {throwIfNoEntry:false})` is undefined).
func statNoThrow(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if noteMissing(err) {
			return nil, nil
		}
		return nil, nodeFsError(err, "stat", path)
	}
	return info, nil
}

func readNote(session *sessionNotes, params notesParams, access notesAccess) (any, error) {
	ctx, pages := access.ctx, access.pages
	relative, err := requireValue(params.path, params.hasPath, "path", params.op)
	if err != nil {
		return nil, err
	}
	target, err := session.read(ctx, relative, optionalScope(params))
	if err != nil {
		return nil, err
	}
	path := target.path
	info, err := statNoThrow(path)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("No note at " + relative + ". Use op \"list\" to see available notes.")
	}
	if info.IsDir() {
		folder := params
		folder.scope, folder.hasScope = scopeDirectory(target.address), true
		return listNotes(session, folder, access)
	}
	binary, err := isBinaryFile(path)
	if err != nil {
		return nil, err
	}
	if binary {
		return nil, errors.New(relative + " is a binary file (" + sdk.FormatSize(int(info.Size())) + "); notes read returns text only.")
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, nodeFsError(err, "open", path)
	}
	text := jsFromBytes(bytes)
	total := u16len(text)
	offset := params.offset
	if offset > 0 && offset >= float64(total) {
		return nil, errors.New("Offset " + jsNumberString(offset) + " is past the end of " + relative + " (" + strconv.Itoa(total) + " chars).")
	}
	// Past the guard the offset is below total, so it is an exact int.
	at := int(offset)
	var header string
	if target.writable {
		header = "File: " + path + "\n"
	} else {
		header = "Read-only live reference: notes read " + jsonObject2("scope", scopeDirectory(target.address), "path", target.address.path) + "\n"
	}
	host := access.host()
	headerLength := u16len(header)
	size, err := pages.size(host, pageRequest{offset: at})
	if err != nil {
		return nil, err
	}
	chars := size - headerLength
	if chars <= 0 {
		return nil, pages.error(host, "Too little context remains to include the note path. Call new_context first, then retry with offset "+strconv.Itoa(at)+".", "")
	}
	end := min(total, at+chars)
	more := ""
	if end < total {
		more = "\n[chars " + strconv.Itoa(at) + "-" + strconv.Itoa(end) + " of " + strconv.Itoa(total) + "; continue with offset " + strconv.Itoa(end) + "]"
	}
	if err := session.record(ctx, []noteTouchFile{{noteAddress: target.address, kind: "read"}}, access.call); err != nil {
		return nil, err
	}
	return pages.result(header+u16slice(text, at, end)+more, nil, displayNoteRead(headerLength, at, end, total)), nil
}

// jsonObject2 is JSON.stringify({ k1: v1, k2: v2 }) for two string members.
func jsonObject2(k1, v1, k2, v2 string) string {
	buf := make([]byte, 0, len(k1)+len(v1)+len(k2)+len(v2)+16)
	buf = append(buf, '{')
	buf = json.AppendStringifyString(buf, k1)
	buf = append(buf, ':')
	buf = json.AppendStringifyString(buf, v1)
	buf = append(buf, ',')
	buf = json.AppendStringifyString(buf, k2)
	buf = append(buf, ':')
	buf = json.AppendStringifyString(buf, v2)
	return string(append(buf, '}'))
}

func sessionTouch(session *sessionNotes, relative, kind string) []noteTouchFile {
	return []noteTouchFile{{
		noteAddress: noteAddress{noteScope: noteScope{root: session.binding.root, scope: session.binding.scope}, path: relative},
		kind:        kind,
	}}
}

func writeNote(session *sessionNotes, params notesParams, access notesAccess) (any, error) {
	ctx := access.ctx
	relative, err := requireValue(params.path, params.hasPath, "path", params.op)
	if err != nil {
		return nil, err
	}
	if !params.hasContent {
		return nil, errors.New("\"content\" is required for op \"write\" (use \"\" to clear a note).")
	}
	content := params.content
	path, err := notePath(session.binding, relative)
	if err != nil {
		return nil, err
	}
	// TEMPORARY-GATE: TEMPORARY: remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph).
	_, err = queuedInOrder(access.order, ctx.Done(), path, func() (struct{}, error) {
		if err := session.prepareWrite(); err != nil {
			return struct{}{}, err
		}
		dir := filepath.Dir(path)
		if err := mkdirRecursive(dir); err != nil {
			return struct{}{}, err
		}
		if err := publishFile(signalOf(ctx), path, content); err != nil {
			return struct{}{}, err
		}
		kind := "write"
		if content == "" {
			kind = "clear"
		}
		return struct{}{}, session.record(ctx, sessionTouch(session, relative, kind), access.call)
	})
	if err != nil {
		return nil, err
	}
	return textResult("Wrote "+path, nil, displayKind("note-write")), nil
}

// needsSeparator is `existing !== "" && !existing.endsWith("\n")` over the file's last
// byte: a final 0x0A always decodes to "\n", and a non-empty file decodes to a non-empty string.
func needsSeparator(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil // existsSync is false for any stat failure (notes.ts:150)
	}
	file, err := os.Open(path)
	if err != nil {
		return false, nodeFsError(err, "open", path)
	}
	defer file.Close()
	// A note another process truncates between the size and the read (`: > note`) is read as it is
	// now, as readFileSync would; a few retries bound the race.
	for range 8 {
		info, err := file.Stat()
		if err != nil {
			return false, nodeFsError(err, "fstat", "")
		}
		if info.IsDir() {
			// readFileSync(dir, "utf8") throws EISDIR with no path (notes.ts:183).
			return false, &fsError{Code: "EISDIR", Syscall: "read", Err: syscall.EISDIR}
		}
		size := info.Size()
		if size == 0 {
			return false, nil
		}
		var last [1]byte
		n, err := file.ReadAt(last[:], size-1)
		if n == 1 {
			return last[0] != '\n', nil
		}
		if !errors.Is(err, io.EOF) {
			return false, nodeFsError(err, "read", "")
		}
	}
	return false, nil
}

func appendNote(session *sessionNotes, params notesParams, access notesAccess) (any, error) {
	ctx := access.ctx
	relative, err := requireValue(params.path, params.hasPath, "path", params.op)
	if err != nil {
		return nil, err
	}
	content, err := requireValue(params.content, params.hasContent, "content", params.op)
	if err != nil {
		return nil, err
	}
	path, err := notePath(session.binding, relative)
	if err != nil {
		return nil, err
	}
	// TEMPORARY-GATE: TEMPORARY: remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph).
	_, err = queuedInOrder(access.order, ctx.Done(), path, func() (struct{}, error) {
		if err := throwIfAborted(signalOf(ctx)); err != nil {
			return struct{}{}, err
		}
		if _, err := session.adopt(ctx, relative, access.call); err != nil {
			return struct{}{}, err
		}
		if err := throwIfAborted(signalOf(ctx)); err != nil {
			return struct{}{}, err
		}
		if err := session.prepareWrite(); err != nil {
			return struct{}{}, err
		}
		dir := filepath.Dir(path)
		if err := mkdirRecursive(dir); err != nil {
			return struct{}{}, err
		}
		if err := appendRecord(path, content); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, session.record(ctx, sessionTouch(session, relative, "write"), access.call)
	})
	if err != nil {
		return nil, err
	}
	return textResult("Appended to "+path, nil, displayKind("note-append")), nil
}

// lowerBuffers holds the lower-cased copy foldIndex searches.
var lowerBuffers = sync.Pool{New: func() any { b := make([]byte, 0, 4096); return &b }}

// foldIndex is hay.toLowerCase().indexOf(needle) for an ASCII hay and an already lower-cased
// ASCII needle. A hay without capitals is searched in place; otherwise one lower-cased copy goes
// through the standard library's Index, which stays linear on repetitive input.
func foldIndex(hay, needle string) int {
	if needle == "" {
		return 0
	}
	upper := false
	for i := 0; i < len(hay); i++ {
		if c := hay[i]; c >= 'A' && c <= 'Z' {
			upper = true
			break
		}
	}
	if !upper {
		return strings.Index(hay, needle)
	}
	buf := lowerBuffers.Get().(*[]byte)
	lowered := append((*buf)[:0], hay...)
	for i, c := range lowered {
		if c >= 'A' && c <= 'Z' {
			lowered[i] = c + 'a' - 'A'
		}
	}
	index := strings.Index(unsafe.String(unsafe.SliceData(lowered), len(lowered)), needle)
	if cap(lowered) <= 4<<20 {
		*buf = lowered[:0]
		lowerBuffers.Put(buf)
	}
	return index
}

// matchingLines: one note's matching lines as `path:line: excerpt` rows (notes.ts:202-214).
// The match index comes from the lower-cased line but excerptAround cuts the original line,
// as in the TS.
func matchingLines(name, text, query string) []string {
	asciiText, queryASCII := isASCII(text), isASCII(query)
	if asciiText && !queryASCII {
		return nil // ASCII text lower-cases to ASCII and cannot contain a non-ASCII query
	}
	if asciiText {
		if foldIndex(text, query) < 0 {
			return nil
		}
	} else {
		lowered := jsToLowerCase(text)
		if hasSurrogateForm(query) || hasSurrogateForm(lowered) {
			// includes() compares UTF-16 units: a lone surrogate also matches inside an astral character.
			if u16index(lowered, query, 0) < 0 {
				return nil
			}
		} else if !strings.Contains(lowered, query) {
			return nil
		}
	}
	var rows []string
	index := 0
	for rest := text; ; index++ {
		line, tail, more := strings.Cut(rest, "\n")
		trimmed := jsTrim(line)
		match := -1
		switch {
		case isASCII(trimmed):
			// An ASCII line lower-cases to ASCII: no copy, and no match for a non-ASCII query.
			if queryASCII {
				match = foldIndex(trimmed, query)
			}
		default:
			match = u16index(jsToLowerCase(trimmed), query, 0)
		}
		if match >= 0 {
			rows = append(rows, name+":"+strconv.Itoa(index+1)+": "+excerptAround(trimmed, match, 50, 200))
		}
		if !more {
			return rows
		}
		rest = tail
	}
}

// searchedNote is one note's outcome in a search batch: skipped (over 1 MiB) or its rows.
type searchedNote struct {
	rows    []string
	skipped bool
	err     error
}

// noteHitsFor: matches in newest-first note order, stopping once they pass `limit`
// characters (notes.ts:215-255).
func noteHitsFor(session *sessionNotes, query string, limit int, scope *string, access notesAccess) (noteHits, error) {
	ctx := access.ctx
	inventory, err := access.discovery.list(ctx, session, discoveryOptions{scope: scope})
	if err != nil {
		return noteHits{}, err
	}
	notes := inventory.files
	var rows []string
	length := -1
	skipped := 0
	signal := signalOf(ctx)
	for start := 0; start < len(notes); start += searchBatch {
		// A batch of notes over 1 MiB reads nothing that would notice cancellation (notes.ts:230).
		if err := throwIfAborted(signalOf(ctx)); err != nil {
			return noteHits{}, err
		}
		// Batches keep note order, so a search stops within one batch of the page limit.
		batch := notes[start:min(start+searchBatch, len(notes))]
		found := make([]searchedNote, len(batch))
		var wg sync.WaitGroup
		for i := range batch {
			note := &batch[i]
			if note.size > searchFileBytes {
				found[i].skipped = true
				continue
			}
			name := noteLabel(note, session, inventory.labeled)
			wg.Add(1)
			go func(slot *searchedNote, path string) {
				defer wg.Done()
				text, ok, err := readText(signal, path)
				if err != nil {
					slot.err = err
					return
				}
				if !ok {
					text = ""
				}
				slot.rows = matchingLines(name, text, query)
			}(&found[i], note.path)
		}
		wg.Wait()
		for i := range found {
			if found[i].err != nil {
				return noteHits{}, found[i].err
			}
			if found[i].skipped {
				skipped++
			}
		}
		for i := range found {
			for _, row := range found[i].rows {
				rows = append(rows, row)
				length += u16len(row) + 1
				if length > limit {
					return noteHits{rows: rows, more: true, skipped: skipped}, nil
				}
			}
		}
	}
	return noteHits{rows: append(rows, inventory.notices...), skipped: skipped}, nil
}

func searchNotes(session *sessionNotes, params notesParams, access notesAccess) (any, error) {
	pages := access.pages
	query, err := requireValue(params.query, params.hasQuery, "query", params.op)
	if err != nil {
		return nil, err
	}
	offset := params.offset
	if offset >= searchOffsetLimit {
		return nil, pages.error(access.host(), "Notes search pages end before offset "+strconv.Itoa(searchOffsetLimit)+"; search for something more specific.", "")
	}
	// Below the limit the offset is an exact int.
	at := int(offset)
	hits, err := noteHitsFor(session, jsToLowerCase(query), at+maxPageChars, optionalScope(params), access)
	if err != nil {
		return nil, err
	}
	skipped := strconv.Itoa(hits.skipped) + " notes"
	if hits.skipped == 1 {
		skipped = "1 note"
	}
	header := ""
	if hits.skipped > 0 {
		header = "[Not searched: " + skipped + " over 1 MiB.]\n"
	}
	return pages.notes(access.host(), notesPage{
		rows:   hits.rows,
		more:   hits.more,
		offset: at,
		kind:   "notes-search",
		empty:  "No notes match \"" + excerpt(query, 200) + "\".",
		header: header,
	})
}

// notesDescription and friends are notes.ts:296-298, byte for byte.
const notesDescription = "Durable notes across Git worktrees and context resets. Forks own private copies of selected-branch notes. Ops: list (newest first, size/date; optional folder), read, search (case-insensitive text), write (replace; empty clears), append (one newline-terminated record). list/search discover project-wide notes with scope selectors; use scope for read-only comparisons or a single-scope view. Omit scope for current notes and live references. list/read/search are paged; continue with the returned offset. list shows the current writable directory; current-file reads show an editable path, foreign reads a read-only reference. Edit the returned current path for small changes. A blocked foreign edit may have created a private copy: read that copy before retrying. Append adopts unowned references without losing their text. Keep one concise current-state note and link fuller evidence."

const notesPromptSnippet = "save and recall durable state with private notes for forks"

// notesSchema is the TypeBox schema of notes.ts:300-340, built with the SDK's TypeBox port so
// the wire JSON has TypeBox's member order (the model sees these bytes).
func notesSchema() sdk.Schema {
	return sdk.Type.Object(sdk.NewOrderedObject(
		"op", sdk.Type.Union([]any{
			sdk.Type.Literal("list"), sdk.Type.Literal("read"), sdk.Type.Literal("write"),
			sdk.Type.Literal("append"), sdk.Type.Literal("search"),
		}, sdk.NewOrderedObject("description", "Operation to perform")),
		"path", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject(
			"description", "Note or folder path relative to the notes directory"))),
		"scope", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject(
			"description", "Absolute scope selector from list/search; only for read/list/search. Omit for current notes and project-wide discovery."))),
		"content", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject(
			"description", "Full file content (write) or text to add (append)"))),
		"query", sdk.Type.Optional(sdk.Type.String(sdk.NewOrderedObject(
			"description", "Substring to find in notes (search)"))),
		"offset", sdk.Type.Optional(sdk.Type.Integer(sdk.NewOrderedObject(
			"description", "Character offset for list/read/search (default 0)",
			"minimum", 0))),
	)).Schema()
}

// registerNotes is notes.ts:286-365.
func registerNotes(ext *sdk.Extension, policy snapshotReader, pages pageAccess, store *notesStore) {
	discovery := newNoteDiscovery()
	definition := sdk.ToolDefinition{
		Name:          "notes",
		Label:         "Notes",
		Description:   notesDescription,
		PromptSnippet: notesPromptSnippet,
		Parameters:    notesSchema(),
		Execute: func(ctx sdk.Context, raw map[string]any) (any, error) {
			// TEMPORARY-GATE: TEMPORARY: remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph).
			// The place in the start order of file mutations is taken before anything else (notes_order.go).
			order := takeNoteTicket()
			defer order.pass()
			params := notesParamsOf(raw)
			// Migrate newly returned legacy package notes without changing this session’s binding (notes.ts:342).
			if _, err := notesDir(ctx.Cwd()); err != nil {
				return nil, err
			}
			session, err := store.get(ctx)
			if err != nil {
				return nil, err
			}
			if params.hasScope && (params.op == "write" || params.op == "append") {
				return nil, errors.New("scope is only valid for read/list/search; writes use this session’s notes scope.")
			}
			access := notesAccess{
				ctx:       ctx,
				host:      func() policyContext { return policy(ctx, nil) },
				pages:     pages,
				call:      session.call(ctx.ToolCallID()),
				discovery: discovery,
				order:     order,
			}
			// TEMPORARY-GATE: TEMPORARY: remove when the host's file-mutation queue grants turns in tool-start order (PiG lane fix/071-host-parity-ph).
			if params.op != "write" && params.op != "append" {
				order.pass() // reads never queue: they must not hold the writes behind them
			}
			switch params.op {
			case "list":
				return listNotes(session, params, access)
			case "read":
				if params.hasPath && params.path == "." {
					return listNotes(session, params, access)
				}
				return readNote(session, params, access)
			case "write":
				return writeNote(session, params, access)
			case "append":
				return appendNote(session, params, access)
			case "search":
				return searchNotes(session, params, access)
			}
			return nil, errors.New("Unknown notes op " + strconv.Quote(params.op))
		},
	}
	applyToolCards(&definition, "notes")
	ext.RegisterTool(definition)
}

// appendRecord is the `appendFileSync` of notes.ts:155-158: the content as one newline-terminated
// record, preceded by a separator when the note does not end in one.
func appendRecord(path, content string) error {
	separate, err := needsSeparator(path)
	if err != nil {
		return err
	}
	// One O_APPEND write keeps records separate across processes. A racing
	// unterminated write can at worst introduce an extra blank separator (notes.ts:156-157).
	record := make([]byte, 0, len(content)+2)
	if separate {
		record = append(record, '\n')
	}
	if hasSurrogateForm(content) {
		// appendFileSync writes a lone surrogate as U+FFFD, not the SDK's WTF-8 bytes.
		record = append(record, jsUTF8Bytes(content)...)
	} else {
		record = append(record, content...)
	}
	if !strings.HasSuffix(content, "\n") { // content.replace(/\n?$/, "\n")
		record = append(record, '\n')
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		return nodeFsError(err, "open", path)
	}
	// Errors of the open descriptor (write, close) carry no path in Node, only open does.
	if _, err := file.Write(record); err != nil {
		_ = file.Close()
		return nodeFsError(err, "write", "")
	}
	if err := file.Close(); err != nil {
		return nodeFsError(err, "close", "")
	}
	return nil
}
