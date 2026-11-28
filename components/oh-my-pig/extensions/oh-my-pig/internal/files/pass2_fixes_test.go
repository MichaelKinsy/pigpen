package files

import (
	"archive/zip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

type testRegTool struct {
	Name          string         `json:"name"`
	Label         string         `json:"label"`
	Description   string         `json:"description"`
	ExecutionMode string         `json:"execution_mode"`
	Parameters    map[string]any `json:"parameters"`
}

type testWireFrame struct {
	Type     string `json:"type"`
	ID       string `json:"id,omitempty"`
	Register *struct {
		Tools []testRegTool `json:"tools"`
	} `json:"register,omitempty"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response,omitempty"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call,omitempty"`
}

type filesFakeHost struct {
	t         *testing.T
	nc        net.Conn
	cwd       string
	sessionID string
	runDone   chan error
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan json.RawMessage
	failures  map[string]string
	tools     map[string]testRegTool
	nextID    int
	stopped   bool
}

func startFilesHost(t *testing.T, ext *sdk.Extension, cwd, sessionID string) *filesFakeHost {
	t.Helper()
	hostSide, extSide := net.Pipe()
	h := &filesFakeHost{
		t:         t,
		nc:        hostSide,
		cwd:       cwd,
		sessionID: sessionID,
		runDone:   make(chan error, 1),
		pending:   map[string]chan json.RawMessage{},
		failures:  map[string]string{},
		tools:     map[string]testRegTool{},
	}
	go func() { h.runDone <- ext.RunWithConn(extSide) }()
	first := h.readFrame()
	for first.Type == "call" && first.Call != nil {
		h.answerCall(first)
		first = h.readFrame()
	}
	if first.Type != "register" || first.Register == nil {
		t.Fatalf("expected register frame, got %+v", first)
	}
	for _, tl := range first.Register.Tools {
		h.tools[tl.Name] = tl
	}
	state, _ := json.Marshal(map[string]any{"hasUI": true, "sessionId": h.sessionID})
	h.writeFrame(map[string]any{"type": "ready", "ready": map[string]any{
		"session_name": "",
		"cwd":          h.cwd,
		"mode":         "tui",
		"width":        80,
		"state":        json.RawMessage(state),
	}})
	go h.serve()
	t.Cleanup(h.stop)
	return h
}

type filesToolWireResult struct {
	Content string         `json:"content"`
	Details map[string]any `json:"details"`
	IsError bool           `json:"is_error"`
}

func (h *filesFakeHost) callTool(name string, params map[string]any) (filesToolWireResult, string) {
	h.t.Helper()
	argv, _ := json.Marshal(params)
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.writeFrame(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":       "tool_call",
			"tool":         name,
			"tool_call_id": "call-" + id,
			"args":         json.RawMessage(argv),
		},
	})
	select {
	case raw := <-ch:
		h.mu.Lock()
		errMsg := h.failures[id]
		h.mu.Unlock()
		if errMsg != "" {
			return filesToolWireResult{}, errMsg
		}
		var res filesToolWireResult
		if err := json.Unmarshal(raw, &res); err != nil {
			h.t.Fatalf("unmarshal tool result %s: %v", string(raw), err)
		}
		return res, ""
	case <-time.After(10 * time.Second):
		h.t.Fatalf("timeout waiting for tool %s", name)
		return filesToolWireResult{}, ""
	}
}

func (h *filesFakeHost) serve() {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
			return
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(h.nc, data); err != nil {
			return
		}
		var f testWireFrame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		switch f.Type {
		case "call":
			if f.Call != nil {
				h.answerCall(f)
			}
		case "response":
			h.mu.Lock()
			ch := h.pending[f.ID]
			delete(h.pending, f.ID)
			h.mu.Unlock()
			if ch == nil || f.Response == nil {
				continue
			}
			if f.Response.Error != nil {
				h.mu.Lock()
				h.failures[f.ID] = f.Response.Error.Message
				h.mu.Unlock()
			}
			ch <- f.Response.Result
		}
	}
}

func (h *filesFakeHost) answerCall(f testWireFrame) {
	args := map[string]any{}
	_ = json.Unmarshal(f.Call.Args, &args)
	go func() {
		var result any
		switch f.Call.Method {
		case "getSessionId", "getSessionID":
			result = map[string]any{"sessionId": h.sessionID}
		case "sessionRead":
			if m, _ := args["method"].(string); m == "getSessionId" {
				result = h.sessionID
			} else {
				result = map[string]any{}
			}
		case "fileMutation.enter":
			h.mu.Lock()
			h.nextID++
			ticket := fmt.Sprintf("fm-%d", h.nextID)
			h.mu.Unlock()
			result = map[string]any{"ticket": ticket}
		case "fileMutation.exit":
			result = map[string]any{}
		case "diff.patch":
			result = map[string]any{"patch": ""}
		default:
			result = map[string]any{}
		}
		h.writeFrame(map[string]any{
			"type": "call_result",
			"id":   f.ID,
			"call_result": map[string]any{
				"result": result,
			},
		})
	}()
}

func (h *filesFakeHost) readFrame() testWireFrame {
	var hdr [4]byte
	if _, err := io.ReadFull(h.nc, hdr[:]); err != nil {
		h.t.Fatalf("read header: %v", err)
	}
	data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(h.nc, data); err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	var f testWireFrame
	if err := json.Unmarshal(data, &f); err != nil {
		h.t.Fatalf("decode frame: %v", err)
	}
	return f
}

func (h *filesFakeHost) writeFrame(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.t.Errorf("marshal: %v", err)
		return
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.nc.Write(hdr[:]); err != nil {
		return
	}
	_, _ = h.nc.Write(data)
}

func (h *filesFakeHost) stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.mu.Unlock()
	h.writeFrame(map[string]any{"type": "shutdown", "shutdown": map[string]any{"reason": "test"}})
	select {
	case <-h.runDone:
	case <-time.After(5 * time.Second):
		h.t.Errorf("extension did not stop after shutdown")
	}
	_ = h.nc.Close()
}

// Covers N1 (shared hashline.ForSession store across registered read/write/edit with session-bearing Context)
// and N5 (exact JSON schema "type": "object" on read, write, edit).
func TestSessionSharedStoreReadThenEditAndSchemas(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line_%02d = %d", i, i))
	}
	target := filepath.Join(dir, "app.py")
	if err := os.WriteFile(target, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sid := "sess-shared-store-test"
	t.Cleanup(func() { lifecycle.End(sid) })

	ext := sdk.New("test-files-session")
	Register(ext)
	host := startFilesHost(t, ext, dir, sid)

	// N5: verify wire schemas carry "type": "object" and "additionalProperties": false for read, write, edit.
	for _, name := range []string{"read", "write", "edit"} {
		tl, ok := host.tools[name]
		if !ok {
			t.Fatalf("missing registered tool %q", name)
		}
		if tl.Parameters["type"] != "object" {
			t.Fatalf("tool %q schema missing type=object: %#v", name, tl.Parameters)
		}
		if ap, ok := tl.Parameters["additionalProperties"].(bool); !ok || ap {
			t.Fatalf("tool %q schema missing additionalProperties=false: %#v", name, tl.Parameters)
		}
	}

	// N1: read a partial window (lines 10-12 -> displays 9..15 with context) through the registered tool.
	readRes, errMsg := host.callTool("read", map[string]any{"path": "app.py:10-12"})
	if errMsg != "" || readRes.IsError {
		t.Fatalf("read failed: err=%q res=%+v", errMsg, readRes)
	}
	header := strings.SplitN(readRes.Content, "\n", 2)[0]
	if !strings.HasPrefix(header, "[app.py#") {
		t.Fatalf("expected hashline header, got %q", header)
	}

	// Edit a line OUTSIDE the displayed window (line 35) -> must be refused as unseen (proving edit
	// sees the snapshot and partial seen-lines recorded by read in the SAME session store).
	outRes, outErr := host.callTool("edit", map[string]any{
		"input": header + "\nPUT 35.=35:\n+line_35 = 3500\n",
	})
	if outErr == "" && !outRes.IsError {
		t.Fatalf("expected edit outside displayed window to be refused, got: %+v", outRes)
	}
	refusal := outErr
	if refusal == "" {
		refusal = outRes.Content
	}
	if !strings.Contains(refusal, "never displayed") {
		t.Fatalf("expected seen-line refusal (not unknown-tag error), got: %s", refusal)
	}

	// Edit a line INSIDE the displayed window (line 11) -> must succeed.
	inRes, inErr := host.callTool("edit", map[string]any{
		"input": header + "\nPUT 11.=11:\n+line_11 = 1100\n",
	})
	if inErr != "" || inRes.IsError {
		t.Fatalf("expected edit inside displayed window to succeed, got err=%q res=%+v", inErr, inRes)
	}
	gotBytes, _ := os.ReadFile(target)
	if !strings.Contains(string(gotBytes), "line_11 = 1100\n") {
		t.Fatalf("edit did not persist line 11 change")
	}
}

// upstream: packages/coding-agent/test/tools/read-raw-range.test.ts: "still reports a real line ceiling for an open-ended raw read"
// upstream: packages/coding-agent/test/tools/read-raw-range.test.ts: "returns only a closed raw range from a newline-terminated file and counts its lines"
func TestRawTruncationAndClosedRangeTerminator(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Open-ended :raw on 320-line file must emit continuation notice and meta.truncation.
	var lines320 []string
	for i := 1; i <= 320; i++ {
		lines320 = append(lines320, fmt.Sprintf("line %d", i))
	}
	p320 := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(p320, []byte(strings.Join(lines320, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	rawOpen, err := executeRead(store, dir, "big.txt:raw")
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := "\n\n[Showing lines 1-300 of 320. Use :301- to continue]"
	if !strings.HasSuffix(rawOpen.Content, wantSuffix) {
		t.Fatalf("open-ended :raw missing continuation footer:\n%s", rawOpen.Content[max(0, len(rawOpen.Content)-120):])
	}
	d, _ := rawOpen.Details.(map[string]any)
	meta, _ := d["meta"].(map[string]any)
	trunc, _ := meta["truncation"].(map[string]any)
	if trunc == nil || trunc["truncatedBy"] != "lines" || trunc["nextOffset"] != 301 {
		t.Fatalf("expected meta.truncation for truncated :raw read, got %#v", d)
	}

	// Closed raw range :raw:5-8 on newline-terminated file keeps trailing '\n' and has NO footer.
	var lines40 []string
	for i := 1; i <= 40; i++ {
		lines40 = append(lines40, fmt.Sprintf("line %d", i))
	}
	p40 := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(p40, []byte(strings.Join(lines40, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	closedRes, err := executeRead(store, dir, "small.txt:raw:5-8")
	if err != nil {
		t.Fatal(err)
	}
	wantClosed := strings.Join(lines40[4:8], "\n") + "\n"
	if closedRes.Content != wantClosed {
		t.Fatalf("closed raw range mismatch: got %q want %q", closedRes.Content, wantClosed)
	}
	cd, _ := closedRes.Details.(map[string]any)
	if cd["totalLines"] != 40 {
		t.Fatalf("expected totalLines=40, got %#v", cd["totalLines"])
	}
	if cmeta, _ := cd["meta"].(map[string]any); cmeta["truncation"] != nil {
		t.Fatalf("satisfied closed raw range must not set meta.truncation: %#v", cmeta)
	}

	// Single oversized line in :raw returns byte-capped snippet with partialLine truncation notice.
	hugeLine := strings.Repeat("x", DefaultMaxBytes+10)
	pHuge := filepath.Join(dir, "huge-line.txt")
	if err := os.WriteFile(pHuge, []byte("first\n"+hugeLine+"\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hugeRes, err := executeRead(store, dir, "huge-line.txt:2:raw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hugeRes.Content, strings.Repeat("x", 100)) || !strings.Contains(hugeRes.Content, "Showing line 2 (partial,") {
		t.Fatalf("unexpected oversized raw line output: len=%d tail=%q", len(hugeRes.Content), hugeRes.Content[max(0, len(hugeRes.Content)-120):])
	}
}

// Covers N3 (in-memory reads have no 300-line default cap; bounded by bytes and 3000 lines).
func TestInMemoryArchiveNo300LineCap(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	zipPath := filepath.Join(dir, "many.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	fw, err := zw.Create("lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 1; i <= 1000; i++ {
		lines = append(lines, fmt.Sprintf("r%d", i))
	}
	_, _ = fw.Write([]byte(strings.Join(lines, "\n") + "\n"))
	_ = zw.Close()
	_ = zf.Close()

	res, err := executeRead(store, dir, "many.zip:lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "r1000") {
		t.Fatalf("expected 1000-line archive member to read past line 300, got tail:\n%s", res.Content[max(0, len(res.Content)-120):])
	}
}

// Covers N4 (withMutationQueue re-panics if fn panics after starting).
func TestWithMutationQueuePropagatesPanicFromFn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic from fn to propagate, but it was swallowed")
		}
		if r != "boom-inside-write" {
			t.Fatalf("unexpected panic value: %v", r)
		}
	}()
	_ = withMutationQueue(sdk.Context{}, p, func() error {
		panic("boom-inside-write")
	})
}

// Covers N6 (streaming [path#TAG] header on >4 MiB files up to 64 MiB and :-N tailing on >4 MiB files)
// and #32 (streaming reader caps single huge line; write bounds existing-file read).
func TestLargeFileStreamingTagTailAndHugeLine(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Build a ~4.5 MiB file (> SnapshotMaxBytes = 4 MiB) with distinct numbered lines.
	largePath := filepath.Join(dir, "large.log")
	var b strings.Builder
	b.Grow(4*1024*1024 + 512*1024)
	pad := strings.Repeat("a", 60)
	const totalLines = 70000
	for i := 1; i <= totalLines; i++ {
		fmt.Fprintf(&b, "L%06d %s\n", i, pad)
	}
	content := b.String()
	if int64(len(content)) <= SnapshotMaxBytes {
		t.Fatalf("fixture must exceed SnapshotMaxBytes (%d), got %d", SnapshotMaxBytes, len(content))
	}
	if err := os.WriteFile(largePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify streamFileHash matches hashline.FileHash exactly.
	wantTag := hashline.FileHash(content)
	gotTag, err := streamFileHash(largePath, int64(len(content)))
	if err != nil || gotTag != wantTag {
		t.Fatalf("streamFileHash = %q (err=%v), want %q", gotTag, err, wantTag)
	}

	// 1. Bare read of >4 MiB file includes [large.log#TAG] header and continuation footer.
	headRes, err := executeRead(store, dir, "large.log")
	if err != nil {
		t.Fatalf("read large.log: %v", err)
	}
	wantHeader := fmt.Sprintf("[large.log#%s]", wantTag)
	if !strings.HasPrefix(headRes.Content, wantHeader+"\n1:L000001 ") {
		t.Fatalf("expected streaming header %q + line 1, got:\n%s", wantHeader, headRes.Content[:min(120, len(headRes.Content))])
	}
	if !strings.Contains(headRes.Content, "Use :301- to continue]") {
		t.Fatalf("expected continuation footer on large file read")
	}

	// 2. Tail :-5 on >4 MiB file returns the last 5 lines (+ 1 leading context line) with tag.
	tailRes, err := executeRead(store, dir, "large.log:-5")
	if err != nil {
		t.Fatalf("tail large.log:-5: %v", err)
	}
	if !strings.HasPrefix(tailRes.Content, wantHeader+"\n") {
		t.Fatalf("tail missing header %q", wantHeader)
	}
	if !strings.Contains(tailRes.Content, fmt.Sprintf("%d:L%06d ", totalLines-5, totalLines-5)) ||
		!strings.Contains(tailRes.Content, fmt.Sprintf("%d:L%06d ", totalLines, totalLines)) {
		t.Fatalf("tail missing expected lines:\n%s", tailRes.Content)
	}

	// 3. Raw closed range on >4 MiB file returns exact lines with terminator and no footer.
	// upstream: packages/coding-agent/test/tools/read-raw-range.test.ts: "returns a closed raw range without a paging footer when the file is streamed"
	rawRangeRes, err := executeRead(store, dir, "large.log:raw:2-3")
	if err != nil {
		t.Fatalf("raw range on large file: %v", err)
	}
	wantRaw := fmt.Sprintf("L000002 %s\nL000003 %s\n", pad, pad)
	if rawRangeRes.Content != wantRaw {
		t.Fatalf("streamed raw range mismatch:\ngot:  %q\nwant: %q", rawRangeRes.Content, wantRaw)
	}

	// 4. Single huge line (>4 MiB) in streamed file is bounded and column-capped cleanly.
	hugePath := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(hugePath, []byte(strings.Repeat("z", SnapshotMaxBytes+1024)+"\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hugeRes, err := executeRead(store, dir, "huge.txt")
	if err != nil {
		t.Fatalf("read huge.txt: %v", err)
	}
	if !strings.Contains(hugeRes.Content, "…") || !strings.Contains(hugeRes.Content, "2:second") {
		t.Fatalf("expected capped line 1 + line 2, got:\n%s", hugeRes.Content[:min(200, len(hugeRes.Content))])
	}
	hugeRawRes, err := executeRead(store, dir, "huge.txt:1:raw")
	if err != nil {
		t.Fatalf("read huge.txt:1:raw: %v", err)
	}
	if !strings.Contains(hugeRawRes.Content, "Showing line 1 (partial,") {
		t.Fatalf("expected partial line notice on huge raw line")
	}

	// 5. Write overwriting a >4 MiB file still enforces truncated-read guard without reading full 4+ MiB into oldText.
	badProj := "L000001\n[Showing lines 1-1 of 70000. Use :2- to continue]\n"
	_, err = executeWrite(sdk.Context{}, store, dir, "large.log", &badProj)
	if err == nil || !strings.Contains(err.Error(), "incomplete read projection") {
		t.Fatalf("expected truncated-read guard on >4 MiB file, got %v", err)
	}
}

// Covers wire details.conflictCount on regular reads containing git merge-conflict blocks
// (2-way and diff3 3-way), and verifies clean/unclosed/non-column-0 markers omit conflictCount.
// upstream: packages/coding-agent/src/tools/read.ts:3378-3419, conflict-detect.ts:42-124
func TestReadConflictCountWireDetails(t *testing.T) {
	dir := t.TempDir()
	sid := "sess-conflict-details-test"
	t.Cleanup(func() { lifecycle.End(sid) })

	conflicted := strings.Join([]string{
		"header",
		"<<<<<<< HEAD",
		"ours 1",
		"=======",
		"theirs 1",
		">>>>>>> main",
		"middle",
		"<<<<<<< feature",
		"ours 2",
		"||||||| base",
		"ancestor",
		"=======",
		"theirs 2",
		">>>>>>> origin/main",
		"  <<<<<<< indented-not-a-marker",
		"=======",
		">>>>>>> end",
		"<<<<<<< unclosed",
		"=======",
		"footer",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "merged.txt"), []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("just normal text\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := sdk.New("test-files-conflict-details")
	Register(ext)
	host := startFilesHost(t, ext, dir, sid)

	// Wire tool_call JSON result carries details.conflictCount == 2 (float64 after JSON decode).
	res, errMsg := host.callTool("read", map[string]any{"path": "merged.txt"})
	if errMsg != "" || res.IsError {
		t.Fatalf("read merged.txt failed: err=%q res=%+v", errMsg, res)
	}
	if got, ok := res.Details["conflictCount"].(float64); !ok || int(got) != 2 {
		t.Fatalf("wire details.conflictCount = %#v, want 2 (details=%#v)", res.Details["conflictCount"], res.Details)
	}

	// Windowed read covering only the first conflict reports conflictCount == 1.
	winRes, errMsg := host.callTool("read", map[string]any{"path": "merged.txt:1-6"})
	if errMsg != "" || winRes.IsError {
		t.Fatalf("read merged.txt:1-6 failed: err=%q res=%+v", errMsg, winRes)
	}
	if got, ok := winRes.Details["conflictCount"].(float64); !ok || int(got) != 1 {
		t.Fatalf("windowed wire details.conflictCount = %#v, want 1", winRes.Details["conflictCount"])
	}

	// Clean file omits conflictCount.
	cleanRes, errMsg := host.callTool("read", map[string]any{"path": "clean.txt"})
	if errMsg != "" || cleanRes.IsError {
		t.Fatalf("read clean.txt failed: err=%q res=%+v", errMsg, cleanRes)
	}
	if _, exists := cleanRes.Details["conflictCount"]; exists {
		t.Fatalf("clean file must omit details.conflictCount, got %#v", cleanRes.Details["conflictCount"])
	}
}

// Covers empty-file read emitting `[path#TAG]\n1:` and allowing `PUT 1.=1:` edit.
func TestReadEmptyFileEmitsLineOneAndAllowsEdit(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "e.txt")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	store := hashline.NewStore()

	res, err := executeRead(store, dir, "e.txt")
	if err != nil {
		t.Fatalf("read empty file: %v", err)
	}
	wantRead := "[e.txt#5D05]\n1:"
	if res.Content != wantRead {
		t.Fatalf("empty file read = %q, want %q", res.Content, wantRead)
	}

	// :raw on empty file stays "".
	rawRes, err := executeRead(store, dir, "e.txt:raw")
	if err != nil || rawRes.Content != "" {
		t.Fatalf("empty file :raw = %q (err=%v), want empty string", rawRes.Content, err)
	}

	// Editing line 1 of the read empty file succeeds.
	editRes, err := executeEdit(sdk.Context{}, store, dir, "[e.txt#5D05]\nPUT 1.=1:\n+x\n")
	if err != nil {
		t.Fatalf("edit empty file failed: %v", err)
	}
	if !strings.Contains(editRes.Content, "1:x") {
		t.Fatalf("unexpected edit reply:\n%s", editRes.Content)
	}
	got, _ := os.ReadFile(emptyPath)
	if string(got) != "x" {
		t.Fatalf("disk content = %q, want %q", string(got), "x")
	}
}
