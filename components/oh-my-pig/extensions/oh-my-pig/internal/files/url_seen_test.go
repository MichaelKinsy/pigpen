package files

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
)

// Covers URL reads with reader output, :raw, line selectors, host:port/, and head truncation.
func TestReadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/notes.txt":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("plain line 1\nplain line 2\nplain line 3\n"))
		case "/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html>\n<h1>Fixture page</h1>\n<p>Body paragraph.</p>\n</html>\n"))
		case "/long":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			for i := 1; i <= 350; i++ {
				_, _ = fmt.Fprintf(w, "row %03d\n", i)
			}
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><body><h1>Hello Web</h1><p>Paragraph one.</p></body></html>`))
		}
	}))
	defer srv.Close()

	// Route via web.Fetch against the local httptest server (no external network).
	prev := urlFetcher
	urlFetcher = func(ctx context.Context, rawURL string, raw bool) (string, error) {
		return web.Fetch(ctx, rawURL, raw)
	}
	defer func() { urlFetcher = prev }()

	store := hashline.NewStore()
	dir := t.TempDir()

	// Default whole-page reader text (HTML -> Markdown) with metadata header.
	res, err := executeRead(store, dir, srv.URL+"/")
	if err != nil {
		t.Fatalf("read url: %v", err)
	}
	if !strings.Contains(res.Content, "URL: "+srv.URL+"/") || !strings.Contains(res.Content, "Hello Web") {
		t.Fatalf("unexpected url reader output:\n%s", res.Content)
	}
	if d, ok := res.Details.(map[string]any); !ok || d["kind"] != "url" || d["url"] != srv.URL+"/" || d["finalUrl"] == "" || d["contentType"] == "" || d["method"] == "" || d["truncated"] != false {
		t.Fatalf("expected ReadUrlToolDetails on URL read, got %#v", res.Details)
	}

	// Bare :raw returns original HTML verbatim with metadata header.
	rawRes, err := executeRead(store, dir, srv.URL+"/:raw")
	if err != nil {
		t.Fatalf("read url :raw: %v", err)
	}
	if !strings.Contains(rawRes.Content, "<h1>Hello Web</h1>") {
		t.Fatalf("unexpected url :raw output:\n%s", rawRes.Content)
	}

	// 1. Ranged text URL read: body only (no header), 1-before/3-after context, no line numbers, no footer when at EOF.
	notesRes, err := executeRead(store, dir, srv.URL+"/notes.txt:2-3")
	if err != nil {
		t.Fatalf("read notes.txt:2-3: %v", err)
	}
	wantNotes := "plain line 1\nplain line 2\nplain line 3"
	if notesRes.Content != wantNotes {
		t.Fatalf("notes.txt:2-3 mismatch:\ngot:\n%q\nwant:\n%q", notesRes.Content, wantNotes)
	}

	// 2. Ranged :raw URL read: exact raw body lines 2-3 only, no header, no context, no footer.
	rawRangeRes, err := executeRead(store, dir, srv.URL+"/index.html:raw:2-3")
	if err != nil {
		t.Fatalf("read index.html:raw:2-3: %v", err)
	}
	wantRawRange := "<h1>Fixture page</h1>\n<p>Body paragraph.</p>"
	if rawRangeRes.Content != wantRawRange {
		t.Fatalf("index.html:raw:2-3 mismatch:\ngot:\n%q\nwant:\n%q", rawRangeRes.Content, wantRawRange)
	}

	// 3. Ranged URL read on a longer page: 1-before/3-after context (9..15) + remaining-lines footer.
	selRes, err := executeRead(store, dir, srv.URL+"/long:10-12")
	if err != nil {
		t.Fatalf("read url :10-12: %v", err)
	}
	wantSel := strings.Join([]string{
		"row 009",
		"row 010",
		"row 011",
		"row 012",
		"row 013",
		"row 014",
		"row 015",
		"",
		"[335 more lines in URL output. Use :16- to continue]",
	}, "\n")
	if selRes.Content != wantSel {
		t.Fatalf("url /long:10-12 mismatch:\ngot:\n%s\n\nwant:\n%s", selRes.Content, wantSel)
	}

	// Whole URL read truncates at 300 lines with omp's continuation notice.
	longRes, err := executeRead(store, dir, srv.URL+"/long")
	if err != nil {
		t.Fatalf("read url /long: %v", err)
	}
	if !strings.Contains(longRes.Content, "[Showing lines 1-300 of ") || !strings.Contains(longRes.Content, "Use :301- to continue]") {
		t.Fatalf("expected 300-line truncation notice, got tail:\n%s", longRes.Content[max(0, len(longRes.Content)-200):])
	}
	if d, ok := longRes.Details.(map[string]any); !ok || d["kind"] != "url" || d["truncated"] != true {
		t.Fatalf("expected truncated ReadUrlToolDetails, got %#v", longRes.Details)
	} else {
		meta, _ := d["meta"].(map[string]any)
		if meta == nil || meta["truncation"] == nil {
			t.Fatalf("expected meta.truncation on truncated URL read, got %#v", d)
		}
	}

	// Bare host:port/ parsing (without scheme).
	hostPort := strings.TrimPrefix(srv.URL, "http://")
	target, err := parseReadURLTarget(hostPort + "/")
	if err != nil || target == nil || target.URL != hostPort+"/" {
		t.Fatalf("bare host:port/ not recognized: target=%+v err=%v", target, err)
	}
	// Bare host:port without trailing slash is NOT treated as a URL.
	if target2, err := parseReadURLTarget(hostPort); err != nil || target2 != nil {
		t.Fatalf("bare host:port without slash should not be URL: target=%+v err=%v", target2, err)
	}
}

// Covers seen-line enforcement refusing unseen anchors and succeeding on straight retry.
func TestSeenLineEnforcementAndRetry(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line_%02d = %d", i, i))
	}
	content := strings.Join(lines, "\n") + "\n"
	path := filepath.Join(dir, "script.py")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()

	// Partial ranged read :10-12 displays lines 9..15 (with 1-before, 3-after context).
	readRes, err := executeRead(store, dir, "script.py:10-12")
	if err != nil {
		t.Fatalf("read :10-12: %v", err)
	}
	header := strings.SplitN(readRes.Content, "\n", 2)[0]
	tag := strings.TrimSuffix(strings.TrimPrefix(header, "[script.py#"), "]")

	// Edit targeting unseen lines 2 and 24 must be refused with omp's exact message.
	patch := header + "\nPUT 2.=2:\n+line_02 = 200\nPUT 24.=24:\n+line_24 = 2400\n"
	seenRes, err := executeEdit(sdk.Context{}, store, dir, patch)
	if err != nil {
		t.Fatalf("expected nil Go error with IsError:true, got %v", err)
	}
	if !seenRes.IsError {
		t.Fatalf("expected seen-line refusal")
	}
	wantErr := fmt.Sprintf(
		"This edit anchors to lines 2, 24 of script.py that [script.py#%s] never displayed (it showed a partial range, a search hit, or a folded summary). Actual file content at those lines:\n  2:line_02 = 2\n  24:line_24 = 24\nVerify the content matches what you intend to touch, then re-issue the edit with the same [path#tag] header — a straight retry now succeeds without a re-read. If the content does NOT match, fix your line numbers.",
		tag,
	)
	if seenRes.Content != wantErr {
		t.Fatalf("seen-line error mismatch:\ngot:\n%s\n\nwant:\n%s", seenRes.Content, wantErr)
	}
	// Identical straight retry must now succeed without a re-read.
	retryRes, err := executeEdit(sdk.Context{}, store, dir, patch)
	if err != nil {
		t.Fatalf("straight retry failed: %v", err)
	}
	if !strings.Contains(retryRes.Content, "2:line_02 = 200") || !strings.Contains(retryRes.Content, "24:line_24 = 2400") {
		t.Fatalf("unexpected retry output:\n%s", retryRes.Content)
	}
}

// Covers details["patch"] / details["diff"] populated for write and edit results.
func TestEditAndWriteDetailsPatch(t *testing.T) {
	dir := t.TempDir()
	store := hashline.NewStore()

	// 1. New file via write
	wNew, err := executeWrite(sdk.Context{}, store, dir, "hello.txt", new("alpha\nbeta\n"))
	if err != nil {
		t.Fatalf("write new: %v", err)
	}
	dNew, ok := wNew.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected map details on write, got %T", wNew.Details)
	}
	wantNewPatch := "--- hello.txt\n+++ hello.txt\n@@ -0,0 +1,2 @@\n+alpha\n+beta\n"
	if got, _ := dNew["patch"].(string); got != wantNewPatch {
		t.Fatalf("write new patch mismatch:\ngot:\n%q\nwant:\n%q", got, wantNewPatch)
	}

	// 2. Overwrite via write
	wOver, err := executeWrite(sdk.Context{}, store, dir, "hello.txt", new("alpha\nBETA\n"))
	if err != nil {
		t.Fatalf("write overwrite: %v", err)
	}
	dOver, _ := wOver.Details.(map[string]any)
	wantOverPatch := "--- hello.txt\n+++ hello.txt\n@@ -1,2 +1,2 @@\n alpha\n-beta\n+BETA\n"
	if got, _ := dOver["patch"].(string); got != wantOverPatch {
		t.Fatalf("write overwrite patch mismatch:\ngot:\n%q\nwant:\n%q", got, wantOverPatch)
	}

	// 3. Small edit via edit
	header := strings.SplitN(wOver.Content, "\n", 2)[0]
	eRes, err := executeEdit(sdk.Context{}, store, dir, header+"\nPUT 1.=1:\n+ALPHA\n")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	dEdit, ok := eRes.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected map details on edit, got %T", eRes.Details)
	}
	wantEditPatch := "--- hello.txt\n+++ hello.txt\n@@ -1,2 +1,2 @@\n-alpha\n+ALPHA\n BETA\n"
	if got, _ := dEdit["patch"].(string); got != "" && got != wantEditPatch {
		t.Fatalf("edit patch mismatch:\ngot:\n%q\nwant:\n%q", got, wantEditPatch)
	}
	if got, _ := dEdit["diff"].(string); !strings.Contains(got, "-1|alpha") && got != wantEditPatch {
		t.Fatalf("edit diff mismatch:\ngot:\n%q", got)
	}
	if fcl, _ := dEdit["firstChangedLine"].(int); fcl != 1 {
		t.Fatalf("expected firstChangedLine=1, got %v", dEdit["firstChangedLine"])
	}
}
