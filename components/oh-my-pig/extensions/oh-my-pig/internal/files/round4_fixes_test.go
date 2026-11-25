package files

import (
	"archive/tar"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
)

// upstream: packages/coding-agent/src/tools/read-format.ts:86-99,222-237
// Reading conflict://N or conflict://N/<scope> must head with the backing FILE's
// tag (not the excerpt's hash) and record the backing file's snapshot with offset
// seen lines so a subsequent edit referencing that header succeeds.
func TestConflictExcerptReadTagsBackingFileAndAllowsEdit(t *testing.T) {
	urls.ResetForTests()
	urls.ResetConflictsForTests()
	t.Cleanup(func() {
		urls.ResetConflictsForTests()
		urls.ResetForTests()
	})
	if err := urls.Register(urls.ConflictHandler{}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	sid := "sess-conflict-excerpt-tag"
	t.Cleanup(func() { lifecycle.End(sid) })

	fileText := strings.Join([]string{
		"line1",
		"line2",
		"<<<<<<< HEAD",
		"ours-a",
		"=======",
		"theirs-a",
		">>>>>>> main",
		"tail",
	}, "\n") + "\n"
	targetFile := filepath.Join(dir, "merge.txt")
	if err := os.WriteFile(targetFile, []byte(fileText), 0o644); err != nil {
		t.Fatal(err)
	}
	wantTag := hashline.FileHash(fileText)

	ext := sdk.New("test-conflict-excerpt-tag")
	Register(ext)
	host := startFilesHost(t, ext, dir, sid)

	// Register via :conflicts (note: :conflicts does NOT put a snapshot in the edit store).
	sumRes, errMsg := host.callTool("read", map[string]any{"path": "merge.txt:conflicts"})
	if errMsg != "" || sumRes.IsError {
		t.Fatalf("read merge.txt:conflicts failed: err=%q res=%+v", errMsg, sumRes)
	}

	// Read conflict://1 and conflict://1/ours — BOTH must be headed with [merge.txt#<wantTag>].
	for _, uri := range []string{"conflict://1", "conflict://1/ours", "conflict://1/theirs"} {
		res, errMsg := host.callTool("read", map[string]any{"path": uri})
		if errMsg != "" || res.IsError {
			t.Fatalf("read %s failed: err=%q res=%+v", uri, errMsg, res)
		}
		header, _, _ := strings.Cut(res.Content, "\n")
		wantHeader := "[merge.txt#" + wantTag + "]"
		if header != wantHeader {
			t.Fatalf("read %s header = %q, want %q (backing file tag)", uri, header, wantHeader)
		}
	}

	// A follow-up edit using the header from conflict://1 on a line shown by conflict://1 (line 4: ours-a) succeeds!
	editInput := "[merge.txt#" + wantTag + "]\nPUT 4.=4:\n+ours-edited\n"
	editRes, errMsg := host.callTool("edit", map[string]any{"input": editInput})
	if errMsg != "" || editRes.IsError {
		t.Fatalf("edit after conflict://1 read failed: err=%q res=%+v", errMsg, editRes)
	}
	afterBytes, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(afterBytes), "ours-edited\n") {
		t.Fatalf("expected edit after conflict://1 to modify disk, got:\n%s", string(afterBytes))
	}
}

// upstream: packages/coding-agent/src/tools/approval.ts:594-598,
//
//	internal-urls/out-protocol.ts:57-64, internal-urls/router.ts:414-419
func TestWriteRefusedOutAndReadOnlyURLUseToolPolicyPrefix(t *testing.T) {
	urls.ResetForTests()
	urls.ResetOutPolicyForTests()
	t.Cleanup(func() {
		urls.ResetOutPolicyForTests()
		urls.ResetForTests()
	})
	if err := urls.Register(urls.OutHandler{}); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(&fakeReadOnlyHandler{
		scheme: "roscheme",
		res:    &urls.Resource{URL: "roscheme://x", Content: "v", Value: true},
	}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	store := hashline.NewStore()
	msg := "hello"

	// 1. Disabled out:// write returns tool-policy denial prefix.
	_, err := executeWrite(sdk.Context{}, store, dir, "out://", &msg)
	if err == nil {
		t.Fatalf("expected out:// denial")
	}
	wantDisabledPrefix := "Tool \"write\" is blocked by tool policy.\nReason: out:// is off in this session"
	if !strings.HasPrefix(err.Error(), wantDisabledPrefix) {
		t.Fatalf("out:// disabled error = %q, want prefix %q", err.Error(), wantDisabledPrefix)
	}

	// 2. Subagent out:// write returns tool-policy denial prefix.
	urls.SetOutPolicyProvider(func(urls.Context) urls.OutPolicy {
		return urls.OutPolicy{Enabled: false, Subagent: true}
	})
	_, err = executeWrite(sdk.Context{}, store, dir, "out://", &msg)
	if err == nil {
		t.Fatalf("expected subagent out:// denial")
	}
	wantSubPrefix := "Tool \"write\" is blocked by tool policy.\nReason: Subagents cannot reply"
	if !strings.HasPrefix(err.Error(), wantSubPrefix) {
		t.Fatalf("out:// subagent error = %q, want prefix %q", err.Error(), wantSubPrefix)
	}

	// 3. Read-only URL scheme returns tool-policy denial prefix + omp's reason wording.
	_, err = executeWrite(sdk.Context{}, store, dir, "roscheme://x", &msg)
	if err == nil {
		t.Fatalf("expected read-only URL denial")
	}
	wantRO := "Tool \"write\" is blocked by tool policy.\nReason: roscheme:// URLs are read-only"
	if err.Error() != wantRO {
		t.Fatalf("read-only URL error = %q, want %q", err.Error(), wantRO)
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:2294-2303
func TestLocatedURLSkillProvenanceAndMarkdownContentType(t *testing.T) {
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	dir := t.TempDir()
	skillRoot := t.TempDir()
	skillFile := filepath.Join(skillRoot, "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# Demo Skill\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentRoot := t.TempDir()
	agentFile := filepath.Join(agentRoot, "UrlChild.md")
	if err := os.WriteFile(agentFile, []byte("# Child Output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := urls.Register(&fakeFileHandler{scheme: "skill", root: skillRoot, immutable: true, unbounded: true}); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(&fakeFileHandler{scheme: "agent", root: agentRoot, immutable: true, unbounded: true}); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()

	// Non-raw skill:// prepends [Skill file: <path>] and sets contentType: text/markdown.
	skillRes, err := executeRead(store, dir, "skill://demo/SKILL.md")
	if err != nil {
		t.Fatalf("read skill://demo/SKILL.md: %v", err)
	}
	wantFirst := "[Skill file: " + dirs.StandardizeMacOSPath(skillFile) + "]"
	firstLine, _, _ := strings.Cut(skillRes.Content, "\n")
	if firstLine != wantFirst {
		t.Fatalf("skill:// first line = %q, want %q", firstLine, wantFirst)
	}
	if d, _ := skillRes.Details.(map[string]any); d["fileSize"] != nil {
		t.Fatalf("unbounded skill:// read must not emit extra fileSize, got %#v", d["fileSize"])
	}
	if d, _ := skillRes.Details.(map[string]any); d["contentType"] != "text/markdown" {
		t.Fatalf("skill:// details.contentType = %#v, want text/markdown", d["contentType"])
	}

	// :raw skill:// does NOT prepend [Skill file: ...] provenance.
	rawSkillRes, err := executeRead(store, dir, "skill://demo/SKILL.md:raw")
	if err != nil {
		t.Fatalf("read skill://demo/SKILL.md:raw: %v", err)
	}
	if strings.HasPrefix(rawSkillRes.Content, "[Skill file:") {
		t.Fatalf("raw skill:// read must not prepend provenance: %q", rawSkillRes.Content)
	}

	// Located agent:// Markdown file sets contentType: text/markdown without [Skill file:].
	agentRes, err := executeRead(store, dir, "agent://UrlChild.md")
	if err != nil {
		t.Fatalf("read agent://UrlChild.md: %v", err)
	}
	if strings.HasPrefix(agentRes.Content, "[Skill file:") {
		t.Fatalf("agent:// read must not prepend skill provenance")
	}
	if d, _ := agentRes.Details.(map[string]any); d["contentType"] != "text/markdown" {
		t.Fatalf("agent:// details.contentType = %#v, want text/markdown", d["contentType"])
	}
}

// upstream: packages/coding-agent/src/edit/index.ts:625-631
// Failed edits over the wire must preserve details {diff:"", errorKind} alongside isError:true.
func TestEditFailurePreservesDetailsOverWire(t *testing.T) {
	dir := t.TempDir()
	sid := "sess-edit-error-details"
	t.Cleanup(func() { lifecycle.End(sid) })

	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ext := sdk.New("test-edit-error-details")
	Register(ext)
	host := startFilesHost(t, ext, dir, sid)

	res, errMsg := host.callTool("edit", map[string]any{"input": "[f.txt#DEAD]\nPUT 1.=1:\n+x\n"})
	if errMsg != "" {
		t.Fatalf("edit failure should return ToolResult with IsError, not RPC error %q", errMsg)
	}
	if !res.IsError {
		t.Fatalf("expected IsError=true on failed edit")
	}
	if diff, ok := res.Details["diff"].(string); !ok || diff != "" {
		t.Fatalf("expected details.diff == \"\", got %#v", res.Details)
	}
	if kind, ok := res.Details["errorKind"].(string); !ok || kind == "" {
		t.Fatalf("expected non-empty details.errorKind, got %#v", res.Details)
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:3204-3214,3373-3375, read-format.ts:169-171
// :raw reads emit details.displayContent using countTextLines(text), so a trailing
// newline yields one more numbered row; covers all four oracle :raw cases plus
// no-trailing-newline, single non-raw range context rows, and comma-list :raw.
func TestRawReadEmitsDisplayContent(t *testing.T) {
	dir := t.TempDir()

	// 1. read-file-range-raw#2: sample.txt:8-10:raw -> [8, 9, 10, 11]
	var sampleLines []string
	for i := 1; i <= 20; i++ {
		sampleLines = append(sampleLines, "line "+string(rune('0'+i/10))+string(rune('0'+i%10)))
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(strings.Join(sampleLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. read-selectors#6 & #7: big.txt:raw:2-4 -> [2, 3, 4, 5], big.txt:-3:raw -> [398, 399, 400, 401]
	var bigLines []string
	for i := 1; i <= 400; i++ {
		bigLines = append(bigLines, "row")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(bigLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. read-truncation#3: wide.txt:raw (3000 x's + "\nshort\n") -> [1, 2, 3]
	wideText := strings.Repeat("x", 3000) + "\nshort\n"
	if err := os.WriteFile(filepath.Join(dir, "wide.txt"), []byte(wideText), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. Unterminated final line: no trailing newline -> exact count without extra row
	if err := os.WriteFile(filepath.Join(dir, "notrail.txt"), []byte("a\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	cases := []struct {
		name      string
		path      string
		wantStart int
		wantNums  []int
	}{
		{"read-file-range-raw#2", "sample.txt:8-10:raw", 8, []int{8, 9, 10, 11}},
		{"read-selectors#6", "big.txt:raw:2-4", 2, []int{2, 3, 4, 5}},
		{"read-selectors#7", "big.txt:-3:raw", 398, []int{398, 399, 400, 401}},
		{"read-truncation#3", "wide.txt:raw", 1, []int{1, 2, 3}},
		{"unterminated-eof-raw", "notrail.txt:2-3:raw", 2, []int{2, 3}},
		{"non-raw-single-range-context", "sample.txt:8-10", 7, []int{7, 8, 9, 10, 11, 12, 13}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := executeRead(store, dir, tc.path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			d, _ := res.Details.(map[string]any)
			dc, ok := d["displayContent"].(map[string]any)
			if !ok {
				t.Fatalf("expected details.displayContent on %s, got %#v", tc.path, d)
			}
			if dc["startLine"] != tc.wantStart {
				t.Fatalf("displayContent.startLine = %#v, want %d", dc["startLine"], tc.wantStart)
			}
			lns, _ := dc["lineNumbers"].([]any)
			if len(lns) != len(tc.wantNums) {
				t.Fatalf("displayContent.lineNumbers = %#v, want %v", lns, tc.wantNums)
			}
			for i, want := range tc.wantNums {
				if lns[i] != want {
					t.Fatalf("displayContent.lineNumbers[%d] = %#v, want %d (full=%#v)", i, lns[i], want, lns)
				}
			}
			if strings.Contains(tc.path, ":raw") && dc["text"] != res.Content {
				t.Fatalf("displayContent.text = %q, want %q", dc["text"], res.Content)
			}
		})
	}

	// Comma-list :raw omits displayContent, matching upstream read.ts:1970-2007,2027.
	multiRaw, err := executeRead(store, dir, "sample.txt:1-2,5-6:raw")
	if err != nil {
		t.Fatalf("read sample.txt:1-2,5-6:raw: %v", err)
	}
	if d, _ := multiRaw.Details.(map[string]any); d["displayContent"] != nil {
		t.Fatalf("comma-list :raw must omit displayContent, got %#v", d["displayContent"])
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:3154-3165,3485-3496
// Column-truncated reads must emit meta.limits.columnTruncated, and truncated
// window reads must set meta.truncation.totalBytes to the selected window's bytes
// (not the whole file size).
func TestReadColumnTruncatedMetaAndSelectedWindowTotalBytes(t *testing.T) {
	dir := t.TempDir()

	// 1. Column-truncated file sets meta.limits.columnTruncated.
	wide := "short\n" + strings.Repeat("A", 900) + "\nend\n"
	if err := os.WriteFile(filepath.Join(dir, "wide.txt"), []byte(wide), 0o644); err != nil {
		t.Fatal(err)
	}
	store := hashline.NewStore()
	wideRes, err := executeRead(store, dir, "wide.txt")
	if err != nil {
		t.Fatalf("read wide.txt: %v", err)
	}
	d, _ := wideRes.Details.(map[string]any)
	meta, _ := d["meta"].(map[string]any)
	limits, _ := meta["limits"].(map[string]any)
	ct, ok := limits["columnTruncated"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta.limits.columnTruncated, got details=%#v", d)
	}
	if ct["maxColumn"] != DefaultOutputMaxColumns || ct["unit"] != "chars" {
		t.Fatalf("unexpected columnTruncated maxColumn/unit: %#v", ct)
	}
	linesMap, _ := ct["lines"].(map[string]any)
	if linesMap["first"] != 2 || linesMap["last"] != 2 {
		t.Fatalf("unexpected columnTruncated.lines: %#v", linesMap)
	}

	// 2. Windowed read over a larger file sets meta.truncation.totalBytes to the selected window bytes, not fileSize.
	var bigLines []string
	for i := 1; i <= 400; i++ {
		bigLines = append(bigLines, "abcdefghij") // 10 bytes each -> 4400 bytes total
	}
	bigText := strings.Join(bigLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(bigText), 0o644); err != nil {
		t.Fatal(err)
	}
	winRes, err := executeRead(store, dir, "big.txt:1-3")
	if err != nil {
		t.Fatalf("read big.txt:1-3: %v", err)
	}
	wd, _ := winRes.Details.(map[string]any)
	wmeta, _ := wd["meta"].(map[string]any)
	wtrunc, ok := wmeta["truncation"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta.truncation on big.txt:1-3, got %#v", wd)
	}
	// Window is lines 1..6 (3 requested + 3 trailing context): 6*10 + 5 newlines = 65 bytes (NOT 4400).
	if wtrunc["totalBytes"] != 65 {
		t.Fatalf("meta.truncation.totalBytes = %#v, want 65 (selected window bytes, not fileSize %d)", wtrunc["totalBytes"], len(bigText))
	}
}

// upstream: packages/coding-agent/src/tools/fetch.ts:1477
// Raw HTTP HTML read must report details.contentType == "text/html".
func TestRawURLReadHTMLContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body><h1>Hi</h1></body></html>"))
	}))
	defer srv.Close()

	prev := urlFetcher
	urlFetcher = func(ctx context.Context, rawURL string, raw bool) (string, error) {
		return web.Fetch(ctx, rawURL, raw)
	}
	defer func() { urlFetcher = prev }()

	res, err := executeRead(hashline.NewStore(), t.TempDir(), srv.URL+"/:raw")
	if err != nil {
		t.Fatalf("read url :raw: %v", err)
	}
	d, _ := res.Details.(map[string]any)
	if d["contentType"] != "text/html" {
		t.Fatalf("raw HTML URL contentType = %#v, want text/html", d["contentType"])
	}
}

// Reading `<existing-file>?q=<jq>` must say jq filter queries are not supported
// rather than misleadingly saying `Path '...?q=...' not found`.
func TestUnsupportedJSONQueryMessage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := executeRead(hashline.NewStore(), dir, "data.json?q=.a")
	if err == nil {
		t.Fatalf("expected error for data.json?q=.a")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must not misleadingly claim path not found: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported jq query message, got %q", err.Error())
	}
}

// upstream: packages/coding-agent/src/tools/read-archive.ts:430-435, read.ts:2755-2759, read-format.ts:393-395
// Archive-member and notebook reads must emit meta.source = {type: "path", value: <absPath>}.
func TestArchiveMemberAndNotebookEmitMetaSource(t *testing.T) {
	dir := t.TempDir()

	// 1. Archive member (read-structured#4: pack.tar:pack/in.txt)
	tarPath := filepath.Join(dir, "pack.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	body := []byte("inner\n")
	if err := tw.WriteHeader(&tar.Header{Name: "pack/in.txt", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// 2. Notebook (read-structured#6: nb.ipynb)
	nbPath := filepath.Join(dir, "nb.ipynb")
	nbJSON := `{"cells":[{"cell_type":"markdown","metadata":{},"source":["# T"]},{"cell_type":"code","execution_count":null,"metadata":{},"outputs":[],"source":["print(1)"]}],"metadata":{},"nbformat":4,"nbformat_minor":5}` + "\n"
	if err := os.WriteFile(nbPath, []byte(nbJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	for _, tc := range []struct {
		arg      string
		wantPath string
	}{
		{"pack.tar:pack/in.txt", tarPath},
		{"nb.ipynb", nbPath},
	} {
		res, err := executeRead(store, dir, tc.arg)
		if err != nil {
			t.Fatalf("read %s: %v", tc.arg, err)
		}
		d, _ := res.Details.(map[string]any)
		meta, ok := d["meta"].(map[string]any)
		if !ok {
			t.Fatalf("read %s missing details.meta: %#v", tc.arg, d)
		}
		src, ok := meta["source"].(map[string]any)
		if !ok || src["type"] != "path" || src["value"] != tc.wantPath {
			t.Fatalf("read %s meta.source = %#v, want {type:path value:%s}", tc.arg, src, tc.wantPath)
		}
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:2803-2811,3061-3075,3648-3651,2026-2030
// Directory, binary, and out-of-range reads must emit details.resolvedPath (standardized),
// and multi-range / binary / out-of-range / unbounded reads must not emit port-only
// totalLines, fileSize, or meta.source where omp omits them.
func TestReadResolvedPathAndOmittedPortOnlyDetails(t *testing.T) {
	dir := t.TempDir()
	stdDir := dirs.StandardizeMacOSPath(dir)
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	textPath := filepath.Join(dir, "big.txt")
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "row")
	}
	if err := os.WriteFile(textPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()

	// 1. Directory read emits resolvedPath.
	dirRes, err := executeRead(store, dir, "sub")
	if err != nil {
		t.Fatalf("read sub: %v", err)
	}
	if d, _ := dirRes.Details.(map[string]any); d["resolvedPath"] != filepath.Join(stdDir, "sub") || d["isDirectory"] != true {
		t.Fatalf("directory read details = %#v, want isDirectory=true and resolvedPath=%q", d, filepath.Join(stdDir, "sub"))
	}

	// 2. Binary read emits resolvedPath and omits port-only fileSize.
	binRes, err := executeRead(store, dir, "blob.bin")
	if err != nil {
		t.Fatalf("read blob.bin: %v", err)
	}
	if d, _ := binRes.Details.(map[string]any); d["resolvedPath"] != filepath.Join(stdDir, "blob.bin") {
		t.Fatalf("binary read resolvedPath = %#v, want %q", d["resolvedPath"], filepath.Join(stdDir, "blob.bin"))
	} else if _, hasFS := d["fileSize"]; hasFS {
		t.Fatalf("binary read must omit fileSize, got %#v", d)
	}

	// 3. Out-of-range read emits resolvedPath and omits port-only totalLines, fileSize, and meta.
	oobRes, err := executeRead(store, dir, "big.txt:900")
	if err != nil {
		t.Fatalf("read big.txt:900: %v", err)
	}
	if d, _ := oobRes.Details.(map[string]any); d["resolvedPath"] != filepath.Join(stdDir, "big.txt") {
		t.Fatalf("out-of-range read resolvedPath = %#v, want %q", d["resolvedPath"], filepath.Join(stdDir, "big.txt"))
	} else {
		for _, forbidden := range []string{"totalLines", "fileSize", "meta"} {
			if _, exists := d[forbidden]; exists {
				t.Fatalf("out-of-range read must omit %s, got details=%#v", forbidden, d)
			}
		}
	}

	// 4. Multi-range read omits port-only totalLines.
	mrRes, err := executeRead(store, dir, "big.txt:1-2,10-11")
	if err != nil {
		t.Fatalf("read big.txt:1-2,10-11: %v", err)
	}
	if d, _ := mrRes.Details.(map[string]any); d["totalLines"] != nil {
		t.Fatalf("multi-range read must omit totalLines, got %#v", d)
	}
}

// upstream: packages/utils/src/dirs.ts:148-158 (standardizeMacOSPath)
// On macOS, /private/tmp/... paths printed in [Skill file: ...], hashline headers,
// and details.resolvedPath must be standardized to /tmp/... when both resolve alike.
func TestMacOSPrivatePathStandardizedInSkillAndHeaders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only /private/tmp alias check")
	}
	tmpRoot, err := os.MkdirTemp("/private/tmp", "omp-files-std-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpRoot) })
	if !strings.HasPrefix(tmpRoot, "/private/tmp/") {
		t.Fatalf("expected /private/tmp prefix, got %q", tmpRoot)
	}
	wantRoot := strings.TrimPrefix(tmpRoot, "/private")

	skillFile := filepath.Join(tmpRoot, "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# Skill\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(tmpRoot, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	if err := urls.Register(&fakeFileHandler{scheme: "skill", root: tmpRoot, immutable: true, unbounded: true}); err != nil {
		t.Fatal(err)
	}

	cwd := t.TempDir()
	store := hashline.NewStore()

	// 1. skill:// provenance and details.resolvedPath strip /private.
	skillRes, err := executeRead(store, cwd, "skill://demo/SKILL.md")
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	firstLine, _, _ := strings.Cut(skillRes.Content, "\n")
	wantProv := "[Skill file: " + filepath.Join(wantRoot, "demo", "SKILL.md") + "]"
	if firstLine != wantProv {
		t.Fatalf("skill provenance = %q, want %q", firstLine, wantProv)
	}
	if d, _ := skillRes.Details.(map[string]any); d["resolvedPath"] != filepath.Join(wantRoot, "demo", "SKILL.md") {
		t.Fatalf("skill resolvedPath = %#v, want %q", d["resolvedPath"], filepath.Join(wantRoot, "demo", "SKILL.md"))
	}

	// 2. Out-of-cwd absolute file read header keeps the explicit path spelling as given.
	outRes, err := executeRead(store, cwd, outsideFile)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	hdr, _, _ := strings.Cut(outRes.Content, "\n")
	wantHdrPrefix := "[" + outsideFile + "#"
	if !strings.HasPrefix(hdr, wantHdrPrefix) {
		t.Fatalf("header = %q, want prefix %q", hdr, wantHdrPrefix)
	}

	// 3. local:// read and write under /private/tmp keep the long realpath in resolvedPath and header.
	localRoot := filepath.Join(tmpRoot, "local-root")
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(&fakeFileHandler{scheme: "local", root: localRoot}); err != nil {
		t.Fatal(err)
	}
	noteBody := "local note\n"
	wLocal, err := executeWrite(sdk.Context{}, store, cwd, "local://note.md", &noteBody)
	if err != nil {
		t.Fatalf("write local://note.md: %v", err)
	}
	wantLocalAbs := filepath.Join(localRoot, "note.md")
	if d, _ := wLocal.Details.(map[string]any); d["resolvedPath"] != wantLocalAbs {
		t.Fatalf("write local:// resolvedPath = %#v, want %q", d["resolvedPath"], wantLocalAbs)
	}
	rLocal, err := executeRead(store, cwd, "local://note.md")
	if err != nil {
		t.Fatalf("read local://note.md: %v", err)
	}
	if d, _ := rLocal.Details.(map[string]any); d["resolvedPath"] != wantLocalAbs {
		t.Fatalf("read local:// resolvedPath = %#v, want %q", d["resolvedPath"], wantLocalAbs)
	}
	localHdr, _, _ := strings.Cut(rLocal.Content, "\n")
	if !strings.HasPrefix(localHdr, "["+wantLocalAbs+"#") {
		t.Fatalf("read local:// header = %q, want prefix %q", localHdr, "["+wantLocalAbs+"#")
	}
}

// upstream: packages/utils/src/dirs.ts:148-158 (standardizeMacOSPath),
// packages/coding-agent/src/tools/write.ts:1157, edit/index.ts:259-267, read.ts:2611
func TestWriteEditAndReadStandardizeMacOSPaths(t *testing.T) {
	rawDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(rawDir); err == nil {
		rawDir = resolved
	}
	wantDir := dirs.StandardizeMacOSPath(rawDir)
	if runtime.GOOS == "darwin" && strings.HasPrefix(rawDir, "/private/") && strings.HasPrefix(wantDir, "/private/") {
		t.Fatalf("expected darwin TempDir %q to strip /private, got %q", rawDir, wantDir)
	}
	if runtime.GOOS != "darwin" && wantDir != rawDir {
		t.Fatalf("expected non-darwin path unchanged: got %q, want %q", wantDir, rawDir)
	}

	store := hashline.NewStore()
	contentA := "alpha\n"
	contentB := "beta\n"

	// 1. write details.resolvedPath
	wRes, err := executeWrite(sdk.Context{}, store, rawDir, "a.txt", &contentA)
	if err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	wantA := filepath.Join(wantDir, "a.txt")
	if d, _ := wRes.Details.(map[string]any); d["resolvedPath"] != wantA {
		t.Fatalf("write details.resolvedPath = %#v, want %q", d["resolvedPath"], wantA)
	}
	if _, err := executeWrite(sdk.Context{}, store, rawDir, "b.txt", &contentB); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	// 2. read details.meta.source.value
	rA, err := executeRead(store, rawDir, "a.txt")
	if err != nil {
		t.Fatalf("read a.txt: %v", err)
	}
	if d, _ := rA.Details.(map[string]any); d != nil {
		meta, _ := d["meta"].(map[string]any)
		src, _ := meta["source"].(map[string]any)
		if src["value"] != wantA {
			t.Fatalf("read meta.source.value = %#v, want %q", src["value"], wantA)
		}
	}
	rB, err := executeRead(store, rawDir, "b.txt")
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}

	hdrA, _, _ := strings.Cut(rA.Content, "\n")
	hdrB, _, _ := strings.Cut(rB.Content, "\n")

	// 3. single-file edit details.path
	eSingle, err := executeEdit(sdk.Context{}, store, rawDir, hdrA+"\nPUT 1.=1:\n+alpha2\n")
	if err != nil || eSingle.IsError {
		t.Fatalf("single edit: err=%v res=%+v", err, eSingle)
	}
	if d, _ := eSingle.Details.(map[string]any); d["path"] != wantA {
		t.Fatalf("single edit details.path = %#v, want %q", d["path"], wantA)
	}

	// 4. multi-file edit details.perFileResults[].path
	rA2, _ := executeRead(store, rawDir, "a.txt")
	hdrA2, _, _ := strings.Cut(rA2.Content, "\n")
	wantB := filepath.Join(wantDir, "b.txt")
	eMulti, err := executeEdit(sdk.Context{}, store, rawDir, hdrA2+"\nPUT 1.=1:\n+alpha3\n"+hdrB+"\nPUT 1.=1:\n+beta2\n")
	if err != nil || eMulti.IsError {
		t.Fatalf("multi edit: err=%v res=%+v", err, eMulti)
	}
	if d, _ := eMulti.Details.(map[string]any); d != nil {
		pfr, _ := d["perFileResults"].([]map[string]any)
		if len(pfr) != 2 || pfr[0]["path"] != wantA || pfr[1]["path"] != wantB {
			t.Fatalf("multi edit perFileResults paths = %#v, want [%q, %q]", pfr, wantA, wantB)
		}
	}

	// 5. cross-directory move edit: details.path, details.move, details.sourcePath, and "Moved to <abs>" text
	rA3, _ := executeRead(store, rawDir, "a.txt")
	hdrA3, _, _ := strings.Cut(rA3.Content, "\n")
	wantMoved := filepath.Join(wantDir, "sub", "moved.txt")
	eMove, err := executeEdit(sdk.Context{}, store, rawDir, hdrA3+"\nMV sub/moved.txt\n")
	if err != nil || eMove.IsError {
		t.Fatalf("move edit: err=%v res=%+v", err, eMove)
	}
	if d, _ := eMove.Details.(map[string]any); d["path"] != wantMoved || d["move"] != wantMoved || d["sourcePath"] != wantA {
		t.Fatalf("move edit details = %#v, want path=%q move=%q sourcePath=%q", d, wantMoved, wantMoved, wantA)
	}
	if !strings.Contains(eMove.Content, "Moved to "+wantMoved) {
		t.Fatalf("move edit text = %q, want substring %q", eMove.Content, "Moved to "+wantMoved)
	}
}

// upstream: packages/coding-agent/src/tools/conflict-uri.ts:144,182,
// packages/coding-agent/src/internal-urls/conflict-protocol.ts:45,
// packages/coding-agent/src/internal-urls/rule-protocol.ts:64,
// packages/utils/src/dirs.ts:148-158
// Project-derived internal URLs (`conflict://`, `rule://`) must emit standardized
// (short on macOS) `details.resolvedPath` on both `read` and `write`.
func TestProjectInternalURLsStandardizeMacOSResolvedPath(t *testing.T) {
	rawDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(rawDir); err == nil {
		rawDir = resolved
	}
	wantDir := dirs.StandardizeMacOSPath(rawDir)
	if runtime.GOOS == "darwin" && strings.HasPrefix(rawDir, "/private/") && strings.HasPrefix(wantDir, "/private/") {
		t.Fatalf("expected darwin TempDir %q to strip /private, got %q", rawDir, wantDir)
	}
	if runtime.GOOS != "darwin" && wantDir != rawDir {
		t.Fatalf("expected non-darwin path unchanged: got %q, want %q", wantDir, rawDir)
	}

	urls.ResetForTests()
	urls.ResetRulesForTests()
	t.Cleanup(func() {
		urls.ResetRulesForTests()
		urls.ResetForTests()
	})
	if err := urls.Register(urls.ConflictHandler{}); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(urls.RuleHandler{}); err != nil {
		t.Fatal(err)
	}

	mergeFile := filepath.Join(rawDir, "merge.txt")
	mergeContent := "top\n<<<<<<< ours\nleft\n=======\nright\n>>>>>>> theirs\nbottom\n"
	if err := os.WriteFile(mergeFile, []byte(mergeContent), 0o644); err != nil {
		t.Fatal(err)
	}
	ruleFile := filepath.Join(rawDir, ".omp", "rules", "demo.md")
	if err := os.MkdirAll(filepath.Dir(ruleFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ruleFile, []byte("# Demo rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	urls.SetActiveRules([]urls.RuleEntry{{Name: "demo", Path: ruleFile, Content: "# Demo rule\n"}})

	sid := "sess-project-urls-macos"
	t.Cleanup(func() { lifecycle.End(sid) })
	ext := sdk.New("files-project-urls-macos")
	Register(ext)
	host := startFilesHost(t, ext, rawDir, sid)

	// 1. Register conflicts via read merge.txt:conflicts
	wantMerge := filepath.Join(wantDir, "merge.txt")
	sumRes, errMsg := host.callTool("read", map[string]any{"path": "merge.txt:conflicts"})
	if errMsg != "" || sumRes.IsError {
		t.Fatalf("read merge.txt:conflicts failed: err=%q res=%+v", errMsg, sumRes)
	}
	if sumRes.Details["resolvedPath"] != wantMerge {
		t.Fatalf("read :conflicts resolvedPath = %#v, want %q", sumRes.Details["resolvedPath"], wantMerge)
	}

	// 2. read conflict://1 and conflict://1/ours emit short resolvedPath
	for _, uri := range []string{"conflict://1", "conflict://1/ours"} {
		cRes, errMsg := host.callTool("read", map[string]any{"path": uri})
		if errMsg != "" || cRes.IsError {
			t.Fatalf("read %s failed: err=%q res=%+v", uri, errMsg, cRes)
		}
		if cRes.Details["resolvedPath"] != wantMerge {
			t.Fatalf("read %s resolvedPath = %#v, want %q", uri, cRes.Details["resolvedPath"], wantMerge)
		}
	}

	// 3. write conflict://1 emits short resolvedPath
	wRes, errMsg := host.callTool("write", map[string]any{"path": "conflict://1", "content": "@ours"})
	if errMsg != "" || wRes.IsError {
		t.Fatalf("write conflict://1 failed: err=%q res=%+v", errMsg, wRes)
	}
	if wRes.Details["resolvedPath"] != wantMerge {
		t.Fatalf("write conflict://1 resolvedPath = %#v, want %q", wRes.Details["resolvedPath"], wantMerge)
	}

	// 4. read rule://demo emits short resolvedPath
	wantRule := filepath.Join(wantDir, ".omp", "rules", "demo.md")
	rRes, errMsg := host.callTool("read", map[string]any{"path": "rule://demo"})
	if errMsg != "" || rRes.IsError {
		t.Fatalf("read rule://demo failed: err=%q res=%+v", errMsg, rRes)
	}
	if rRes.Details["resolvedPath"] != wantRule {
		t.Fatalf("read rule://demo resolvedPath = %#v, want %q", rRes.Details["resolvedPath"], wantRule)
	}
}

// upstream: packages/coding-agent/src/tools/read.ts:2082-2083,3322-3331,
// packages/coding-agent/src/tools/output-meta.ts:181-197,
// packages/coding-agent/src/tools/write.ts:951-966
func TestReadMaxBytesDisplayTargetAndOutTouchesNoFiles(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// 1. Line-truncated read emits meta.truncation.maxBytes matching omp's computeReadByteBudget.
	var lines []string
	for i := 1; i <= 400; i++ {
		lines = append(lines, "row")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// big.txt:50 expands to lines 49..53 (5 lines, maxLinesToCollect=5, prefixWidth=len("53")+1=3 -> 51200-15=51185).
	r50, err := executeRead(store, dir, "big.txt:50")
	if err != nil {
		t.Fatalf("read big.txt:50: %v", err)
	}
	d50, _ := r50.Details.(map[string]any)
	meta50, _ := d50["meta"].(map[string]any)
	trunc50, _ := meta50["truncation"].(map[string]any)
	if trunc50 == nil || trunc50["truncatedBy"] != "lines" || trunc50["maxBytes"] != 51185 {
		t.Fatalf("big.txt:50 meta.truncation = %#v, want truncatedBy=lines maxBytes=51185", trunc50)
	}
	// big.txt bare read (300 lines, prefixWidth=len("300")+1=4 -> 51200-1200=50000).
	rBare, err := executeRead(store, dir, "big.txt")
	if err != nil {
		t.Fatalf("read big.txt: %v", err)
	}
	dBare, _ := rBare.Details.(map[string]any)
	metaBare, _ := dBare["meta"].(map[string]any)
	truncBare, _ := metaBare["truncation"].(map[string]any)
	if truncBare == nil || truncBare["truncatedBy"] != "lines" || truncBare["maxBytes"] != 50000 {
		t.Fatalf("big.txt bare meta.truncation = %#v, want truncatedBy=lines maxBytes=50000", truncBare)
	}

	// 2. local:// read emits displayTarget equal to urls.LocalHandler.LocateSync (keeps /private realpath on macOS).
	urls.ResetForTests()
	urls.ResetOutPolicyForTests()
	urls.SetLocalRootOverrideForTests(nil)
	t.Cleanup(func() {
		urls.SetLocalRootOverrideForTests(nil)
		urls.ResetOutPolicyForTests()
		urls.ResetForTests()
	})
	if err := urls.Register(urls.LocalHandler{}); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(urls.OutHandler{}); err != nil {
		t.Fatal(err)
	}
	localRoot := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(localRoot); err == nil {
		localRoot = resolved
	}
	urls.SetLocalRootOverrideForTests(func(sdk.Context) (string, error) { return localRoot, nil })
	noteBody := "line 1\nline 2\n"
	if _, err := executeWrite(sdk.Context{}, store, dir, "local://note.md", &noteBody); err != nil {
		t.Fatalf("write local://note.md: %v", err)
	}
	rLocal, err := executeRead(store, dir, "local://note.md")
	if err != nil {
		t.Fatalf("read local://note.md: %v", err)
	}
	wantTarget := filepath.Join(localRoot, "note.md")
	if dLocal, _ := rLocal.Details.(map[string]any); dLocal["displayTarget"] != wantTarget || dLocal["resolvedPath"] != wantTarget {
		t.Fatalf("local:// read details = %#v, want displayTarget=resolvedPath=%q", dLocal, wantTarget)
	}
	// Selector suffix (e.g. local://note.md:-1) still resolves displayTarget cleanly without the selector.
	rLocalTail, err := executeRead(store, dir, "local://note.md:-1")
	if err != nil {
		t.Fatalf("read local://note.md:-1: %v", err)
	}
	if dTail, _ := rLocalTail.Details.(map[string]any); dTail["displayTarget"] != wantTarget {
		t.Fatalf("local://note.md:-1 displayTarget = %#v, want %q", dTail["displayTarget"], wantTarget)
	}

	// 3. write to enabled out:// emits details: {"touchesNoFiles": true} (never nil).
	urls.SetOutPolicyProvider(func(urls.Context) urls.OutPolicy {
		return urls.OutPolicy{Enabled: true}
	})
	reply := "Progress update from print mode."
	wOut, err := executeWrite(sdk.Context{}, store, dir, "out://", &reply)
	if err != nil {
		t.Fatalf("write out://: %v", err)
	}
	dOut, ok := wOut.Details.(map[string]any)
	if !ok || len(dOut) != 1 || dOut["touchesNoFiles"] != true {
		t.Fatalf("write out:// details = %#v, want map[touchesNoFiles:true]", wOut.Details)
	}
}
