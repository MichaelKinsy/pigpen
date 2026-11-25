package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

type fakeFileHandler struct {
	scheme    string
	root      string
	immutable bool
	unbounded bool
}

func (h *fakeFileHandler) Scheme() string { return h.scheme }
func (h *fakeFileHandler) Spec() urls.Spec {
	spec := urls.Spec{
		Backing:   urls.BackingFile,
		Selectors: urls.SelectorsLines,
		Immutable: h.immutable,
		Unbounded: h.unbounded,
	}
	if !h.immutable {
		spec.Write = &urls.WritePolicy{Via: "file", Payload: "text"}
	}
	return spec
}
func (h *fakeFileHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	return nil, fmt.Errorf("unexpected resolve for file-backed %s", u.Href)
}
func (h *fakeFileHandler) Locate(c urls.Context, u urls.URL, opts urls.LocateOptions) (string, bool, error) {
	rel := strings.TrimPrefix(u.Host+u.Path, "/")
	p := filepath.Join(h.root, rel)
	if !opts.Create {
		if _, err := os.Stat(p); err != nil {
			return "", false, nil
		}
	}
	return p, true, nil
}

type fakeResourceHandler struct {
	scheme  string
	res     *urls.Resource
	written string
}

func (h *fakeResourceHandler) Scheme() string { return h.scheme }
func (h *fakeResourceHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingVirtual,
		Selectors: urls.SelectorsLines,
		Write:     &urls.WritePolicy{Via: "handler", Payload: "verbatim"},
	}
}
func (h *fakeResourceHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	return h.res, nil
}
func (h *fakeResourceHandler) Write(c urls.Context, u urls.URL, content string) (*urls.WriteResult, error) {
	h.written = content
	return &urls.WriteResult{Text: "handler-wrote:" + u.Href}, nil
}

type fakeReadOnlyHandler struct {
	scheme string
	res    *urls.Resource
}

func (h *fakeReadOnlyHandler) Scheme() string { return h.scheme }
func (h *fakeReadOnlyHandler) Spec() urls.Spec {
	return urls.Spec{
		Backing:   urls.BackingVirtual,
		Selectors: urls.SelectorsLines,
	}
}
func (h *fakeReadOnlyHandler) Resolve(c urls.Context, u urls.URL) (*urls.Resource, error) {
	return h.res, nil
}

// Covers internal URL routing through internal/urls for read, write, and edit.
func TestInternalURLRouting(t *testing.T) {
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	dir := t.TempDir()
	fileHandler := &fakeFileHandler{scheme: "fakelocal", root: dir}
	resHandler := &fakeResourceHandler{
		scheme: "fakeres",
		res: &urls.Resource{
			URL:         "fakeres://doc",
			Content:     "alpha\nbeta\ngamma\ndelta\n",
			ContentType: "text/plain",
			Notes:       []string{"[note: resolved]"},
		},
	}
	roHandler := &fakeReadOnlyHandler{
		scheme: "fakero",
		res: &urls.Resource{
			URL:     "fakero://val",
			Content: "42",
			Value:   true,
		},
	}
	if err := urls.Register(fileHandler); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(resHandler); err != nil {
		t.Fatal(err)
	}
	if err := urls.Register(roHandler); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()

	// 1. write to file-backed URL uses Locate(Create: true)
	body := "one\ntwo\nthree\n"
	wres, err := executeWrite(sdk.Context{}, store, dir, "fakelocal://notes.txt", &body)
	if err != nil {
		t.Fatalf("write file-backed url: %v", err)
	}
	if !strings.Contains(wres.Content, "Successfully wrote") {
		t.Fatalf("unexpected write output: %s", wres.Content)
	}

	// 2. read file-backed URL goes through normal file pipeline with selector
	rres, err := executeRead(store, dir, "fakelocal://notes.txt:2:raw")
	if err != nil {
		t.Fatalf("read file-backed url: %v", err)
	}
	if rres.Content != "two\n" {
		t.Fatalf("expected \"two\\n\", got %q", rres.Content)
	}
	// 2b. immutable+unbounded file-backed URL prints no tag and reads >300 lines whole
	immHandler := &fakeFileHandler{scheme: "fakeimm", root: dir, immutable: true, unbounded: true}
	if err := urls.Register(immHandler); err != nil {
		t.Fatal(err)
	}
	var bigLines []string
	for i := 1; i <= 400; i++ {
		bigLines = append(bigLines, fmt.Sprintf("imm-%d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte(strings.Join(bigLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	immRes, err := executeRead(store, dir, "fakeimm://doc.md")
	if err != nil {
		t.Fatalf("read immutable unbounded url: %v", err)
	}
	if strings.HasPrefix(immRes.Content, "[fakeimm://") {
		t.Fatalf("immutable file-backed URL must not print hashline header:\n%s", immRes.Content[:min(80, len(immRes.Content))])
	}
	if !strings.Contains(immRes.Content, "imm-400") {
		t.Fatalf("unbounded file-backed URL should read all 400 lines without paging")
	}

	// 3. read resource URL applies selector and does NOT append resource Notes
	// (upstream: read.ts:3601-3604).
	rres, err = executeRead(store, dir, "fakeres://doc:2-3")
	if err != nil {
		t.Fatalf("read resource url: %v", err)
	}
	if !strings.Contains(rres.Content, "2:beta\n3:gamma") {
		t.Fatalf("unexpected resource read:\n%s", rres.Content)
	}
	if strings.Contains(rres.Content, "[note: resolved]") {
		t.Fatalf("resource read must not append resource.Notes to text:\n%s", rres.Content)
	}
	// 4. read Value resource rejects line selector, allows bare and :raw
	if _, err := executeRead(store, dir, "fakero://val:1-2"); err == nil {
		t.Fatalf("expected error combining Value resource with line selector")
	}
	rres, err = executeRead(store, dir, "fakero://val")
	if err != nil || rres.Content != "42" {
		t.Fatalf("expected '42', got %q err=%v", rres.Content, err)
	}

	// 5. write to handler-owned URL dispatches to urls.Write
	payload := "hello"
	wres, err = executeWrite(sdk.Context{}, store, dir, "fakeres://doc", &payload)
	if err != nil {
		t.Fatalf("write handler url: %v", err)
	}
	if wres.Content != "handler-wrote:fakeres://doc" || resHandler.written != "hello" {
		t.Fatalf("unexpected handler write: %q written=%q", wres.Content, resHandler.written)
	}

	// 6. write to read-only URL fails
	if _, err := executeWrite(sdk.Context{}, store, dir, "fakero://val", &payload); err == nil {
		t.Fatalf("expected read-only write error")
	}

	// 7. edit refuses handler-owned and read-only URLs with omp's message
	hErrRes, err := executeEdit(sdk.Context{}, store, dir, "[fakeres://doc#0000]\nPUT 1.=1:\n+x\n")
	if err != nil || !hErrRes.IsError || !strings.Contains(hErrRes.Content, "is written through `write`, not edited") {
		t.Fatalf("expected handler edit refusal, got res=%+v err=%v", hErrRes, err)
	}
	roErrRes, err := executeEdit(sdk.Context{}, store, dir, "[fakero://val#0000]\nPUT 1.=1:\n+x\n")
	if err != nil || !roErrRes.IsError || !strings.Contains(roErrRes.Content, "is read-only") {
		t.Fatalf("expected read-only edit refusal, got res=%+v err=%v", roErrRes, err)
	}
}

// Covers notebook, ZIP/tar.gz archives, mixed tail comma list, and SetDiagnosticsHook.
func TestReadSourcesAndDiagnosticsHook(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// Notebook (.ipynb): 0-based `# %% [type] cell:N` with hashline header, no outputs.
	// upstream: crates/edit/src/notebook.rs:166
	nbJSON := `{"cells":[{"cell_type":"markdown","source":["# Title\n"]},{"cell_type":"code","source":["print(1)\n"],"outputs":[{"output_type":"stream","text":["1\n"]}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "demo.ipynb"), []byte(nbJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	nbRes, err := executeRead(store, dir, "demo.ipynb")
	if err != nil {
		t.Fatalf("read notebook: %v", err)
	}
	if !strings.HasPrefix(nbRes.Content, "[demo.ipynb#") {
		t.Fatalf("expected hashline header on notebook, got:\n%s", nbRes.Content)
	}
	if !strings.Contains(nbRes.Content, "1:# %% [markdown] cell:0") || !strings.Contains(nbRes.Content, "4:# %% [code] cell:1") || strings.Contains(nbRes.Content, "# output:") {
		t.Fatalf("unexpected notebook output:\n%s", nbRes.Content)
	}

	// ZIP archive: root lists one directory level (`sub/`), sub-path lists `hello.txt`, member reads content.
	// upstream: packages/coding-agent/src/tools/read-archive.ts:219
	zipPath := filepath.Join(dir, "bundle.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("sub/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("line1\nline2\nline3\n"))
	_ = zw.Close()
	_ = zf.Close()

	listRes, err := executeRead(store, dir, "bundle.zip")
	if err != nil || listRes.Content != "sub/" {
		t.Fatalf("zip root listing expected 'sub/', got err=%v content=%q", err, listRes.Content)
	}
	if m, ok := listRes.Details.(map[string]any); !ok || m["isDirectory"] != true {
		t.Fatalf("zip listing expected isDirectory=true details, got %#v", listRes.Details)
	}
	subListRes, err := executeRead(store, dir, "bundle.zip:sub")
	if err != nil || !strings.Contains(subListRes.Content, "hello.txt") {
		t.Fatalf("zip sub listing failed: %v\n%s", err, subListRes.Content)
	}
	memRes, err := executeRead(store, dir, "bundle.zip:sub/hello.txt:2:raw")
	if err != nil || memRes.Content != "line2\n" {
		t.Fatalf("zip member read failed: %v got %q", err, memRes.Content)
	}

	// tar.gz archive
	tgzPath := filepath.Join(dir, "bundle.tar.gz")
	tf, err := os.Create(tgzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(tf)
	tw := tar.NewWriter(gw)
	data := []byte("alpha\nbeta\n")
	_ = tw.WriteHeader(&tar.Header{Name: "a.txt", Mode: 0o644, Size: int64(len(data))})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gw.Close()
	_ = tf.Close()

	tgzRes, err := executeRead(store, dir, "bundle.tar.gz:a.txt:raw")
	if err != nil || tgzRes.Content != "alpha\nbeta\n" {
		t.Fatalf("tar.gz member read failed: %v got %q", err, tgzRes.Content)
	}

	// Mixed tail comma list (:1-2,-2)
	if err := os.WriteFile(filepath.Join(dir, "nums.txt"), []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mixRes, err := executeRead(store, dir, "nums.txt:1-2,-2")
	if err != nil {
		t.Fatalf("mixed tail err: %v", err)
	}
	if !strings.Contains(mixRes.Content, "1:1\n2:2\n…\n9:9\n10:10") {
		t.Fatalf("unexpected mixed tail output:\n%s", mixRes.Content)
	}

	// SetDiagnosticsHook is invoked on write only; SetEditDiagnosticsHook on edit only.
	writeHookCalls := 0
	editHookCalls := 0
	SetDiagnosticsHook(func(ctx sdk.Context, path string) string {
		writeHookCalls++
		return "LSP-write: 1 error in " + filepath.Base(path)
	})
	t.Cleanup(func() {
		SetDiagnosticsHook(nil)
		SetEditDiagnosticsHook(nil)
	})

	content := "hello\nworld\n"
	wres, err := executeWrite(sdk.Context{}, store, dir, "diag.go", &content)
	if err != nil {
		t.Fatalf("write diag.go: %v", err)
	}
	if writeHookCalls != 1 || editHookCalls != 0 {
		t.Fatalf("after write: writeHookCalls=%d editHookCalls=%d, want 1, 0", writeHookCalls, editHookCalls)
	}
	if !strings.Contains(wres.Content, "LSP-write: 1 error in diag.go") {
		t.Fatalf("expected write diagnostics appended on write, got:\n%s", wres.Content)
	}
	if d, ok := wres.Details.(map[string]any); !ok || d["diagnostics"] == nil {
		t.Fatalf("expected details.diagnostics on write, got %#v", wres.Details)
	} else if diagMap, ok := d["diagnostics"].(map[string]any); !ok || diagMap["errored"] != true || diagMap["summary"] == "" {
		t.Fatalf("unexpected details.diagnostics shape: %#v", d["diagnostics"])
	}

	// With edit hook unset, edit must NOT invoke the write hook or fall back to it.
	rres, err := executeRead(store, dir, "diag.go")
	if err != nil {
		t.Fatal(err)
	}
	header := strings.SplitN(rres.Content, "\n", 2)[0]
	eresNoHook, err := executeEdit(sdk.Context{}, store, dir, header+"\nPUT 1.=1:\n+hi\n")
	if err != nil {
		t.Fatalf("edit diag.go (no edit hook): %v", err)
	}
	if writeHookCalls != 1 || strings.Contains(eresNoHook.Content, "LSP-write:") {
		t.Fatalf("edit must not call write diagnostics hook: calls=%d content=\n%s", writeHookCalls, eresNoHook.Content)
	}

	// With SetEditDiagnosticsHook installed, edit invokes only the edit hook and write still invokes only the write hook.
	SetEditDiagnosticsHook(func(ctx sdk.Context, path string) string {
		editHookCalls++
		return "LSP-edit: 1 error in " + filepath.Base(path)
	})
	header2 := strings.SplitN(eresNoHook.Content, "\n", 2)[0]
	eres, err := executeEdit(sdk.Context{}, store, dir, header2+"\nPUT 1.=1:\n+hey\n")
	if err != nil {
		t.Fatalf("edit diag.go (with edit hook): %v", err)
	}
	if writeHookCalls != 1 || editHookCalls != 1 {
		t.Fatalf("after edit: writeHookCalls=%d editHookCalls=%d, want 1, 1", writeHookCalls, editHookCalls)
	}
	if !strings.Contains(eres.Content, "LSP-edit: 1 error in diag.go") || strings.Contains(eres.Content, "LSP-write:") {
		t.Fatalf("expected edit diagnostics only on edit, got:\n%s", eres.Content)
	}
	if d, ok := eres.Details.(map[string]any); !ok || d["diagnostics"] == nil {
		t.Fatalf("expected details.diagnostics on edit, got %#v", eres.Details)
	}
}
