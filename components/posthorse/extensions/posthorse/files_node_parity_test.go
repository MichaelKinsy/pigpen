package posthorse

// Review probes (CoreLane scope). Each DIVERGENCE test states the TS behaviour observed under Node 24.22.1 /
// Pi 1.5.0 on xd6702 and fails while the port differs. The probe scripts are in
// port/review/corereview-probes/ (probe.ts, schema.ts, decoder.ts, jsdiff.ts, gitdir.ts); the differential tests
// read their JSON output from testdata/core_*.json.gz (port/tools/gen-testdata.sh regenerates it) and fail when it is absent.

import (
	"context"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func rvGitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

// DIVERGENCE: files.ts:214-219 path.relative(x, x) is "" (excluded); Go filepath.Rel gives "." (accepted).
// Probe: notesDir(<agent>/git) where <agent>/git is a Git root -> "<agent>/git/.pi/notes" in TS.
func TestReviewNotesDirInstallsRootStaysLocal(t *testing.T) {
	base := pnDir(t)
	pnAgent(t, filepath.Join(base, "agent"))
	installs := filepath.Join(base, "agent", "git")
	pnMkdir(t, installs, ".pi", "notes")
	rvGitInit(t, installs)
	got, err := notesDir(installs)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(installs, ".pi", "notes"); got != want {
		info, _ := os.Lstat(filepath.Join(installs, ".pi", "notes"))
		t.Fatalf("notesDir = %q, want %q (TS); local notes now a symlink: %v", got, want, info != nil && info.Mode()&os.ModeSymlink != 0)
	}
}

// DIVERGENCE: files.ts:214-219 via projectPackageNotes (files.ts:220-240): cwd = <project>/.pi/git as a Git root.
// Probe: TS keeps "<project>/.pi/git/.pi/notes".
func TestReviewNotesDirProjectContainerRootStaysLocal(t *testing.T) {
	base := pnDir(t)
	pnAgent(t, filepath.Join(base, "agent"))
	container := filepath.Join(base, "proj", ".pi", "git")
	pnMkdir(t, container, ".pi", "notes")
	rvGitInit(t, container)
	got, err := notesDir(container)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(container, ".pi", "notes"); got != want {
		t.Fatalf("notesDir = %q, want %q (TS)", got, want)
	}
}

func TestReviewPackageRelativePathSame(t *testing.T) {
	// DIVERGENCE: files.ts:215-216 relative("/a/b", "/a/b") === "" -> undefined.
	if path, ok := packageRelativePath("/a/b", "/a/b"); ok {
		t.Fatalf("packageRelativePath(same) = %q, true; TS returns undefined", path)
	}
}

// DIVERGENCE: files.ts:95 `signal?.throwIfAborted()` throws Pi's abort reason, DOMException
// "This operation was aborted" (Pi aborts with AbortController.abort() and no reason).
func TestReviewPublishFileAbortMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := publishFile(ctx, filepath.Join(t.TempDir(), "n.md"), "x")
	if err == nil || err.Error() != "This operation was aborted" {
		t.Fatalf("publishFile aborted: %v, want \"This operation was aborted\" (TS)", err)
	}
}

// DIVERGENCE: session-lines.ts:64 `signal?.throwIfAborted()` in rangeText: "This operation was aborted".
func TestReviewRangeTextAbortMessage(t *testing.T) {
	file := filepath.Join(t.TempDir(), "s.jsonl")
	pnWrite(t, file, "{\"type\":\"x\"}\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for line, err := range sessionLines(ctx, file, nil) {
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		_, err := line.text()
		if err == nil || err.Error() != "This operation was aborted" {
			t.Fatalf("text() after abort: %v, want \"This operation was aborted\" (TS)", err)
		}
		break
	}
}

// DIVERGENCE: files.ts:180 readFile(dir) rejects with "EISDIR: illegal operation on a directory, read" (no path).
func TestReviewReadTextDirectoryMessage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("probe recorded on Linux")
	}
	_, _, err := readText(nil, t.TempDir())
	if err == nil || err.Error() != "EISDIR: illegal operation on a directory, read" {
		t.Fatalf("readText(dir): %v, want \"EISDIR: illegal operation on a directory, read\" (TS)", err)
	}
}

// DIVERGENCE: files.ts:170 open(socket) rejects with "ENXIO: no such device or address, open '<path>'";
// errnoCodes has no ENXIO, so the raw Go error text leaks.
func TestReviewIsBinaryFileSocketMessage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("probe recorded on Linux")
	}
	dir, err := os.MkdirTemp("/tmp", "rv")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, err = isBinaryFile(sock)
	want := "ENXIO: no such device or address, open '" + sock + "'"
	if err == nil || err.Error() != want {
		t.Fatalf("isBinaryFile(socket): %v, want %q (TS)", err, want)
	}
}

// The TypeBox 1.3.27 JSON of the two context-tool schemas (context-tools.ts:64-71,97), measured with
// `JSON.stringify(Type.Object(...))` under Node 24.
func TestReviewContextToolSchemas(t *testing.T) {
	want := map[string]string{
		"new_context":           `{"type":"object","properties":{"handoff":{"type":"string","description":"What the fresh window needs to continue, as readable prose","maxLength":20000}}}`,
		"get_context_remaining": `{"type":"object","properties":{}}`,
	}
	tools, _ := fxCaptureRegistration(t, Extension())
	for _, tool := range tools {
		if w, ok := want[tool.Name]; ok {
			if got := string(tool.Parameters); got != w {
				t.Errorf("%s parameters on the wire:\n got %s\nwant %s", tool.Name, got, w)
			}
		}
	}
}

// DIVERGENCE: session-lines.ts:33,40 Node's StringDecoder("utf8") holds back any lead-shaped tail (e.g. F4 9F,
// C2 F4 9F) until the next write/end; utf8Decoder emits its U+FFFD at once. Totals agree, the per-window
// distribution does not (decoder.ts on xd6702 vs utf8Decoder).
func TestReviewUTF8DecoderDifferential(t *testing.T) {
	raw := testdataGz(t, "core_decoder.json.gz")
	var cases []struct {
		Chunks [][]byte `json:"-"`
		C      [][]int  `json:"chunks"`
		Parts  [][]int  `json:"parts"`
	}
	if err := sdkjson.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	bad, totals := 0, 0
	for _, c := range cases {
		var all []uint16
		var allWant []int
		var whole utf8Decoder
		for i, chunk := range c.C {
			b := make([]byte, len(chunk))
			for j, v := range chunk {
				b[j] = byte(v)
			}
			all = append(all, u16units(string(whole.write(nil, b)))...)
			allWant = append(allWant, c.Parts[i]...)
		}
		all = append(all, u16units(string(whole.end(nil)))...)
		allWant = append(allWant, c.Parts[len(c.C)]...)
		if !rvSameUnits(all, allWant) && totals < 10 {
			totals++
			t.Errorf("TOTAL chunks %v: got %v want %v", c.C, all, allWant)
		}
		var d utf8Decoder
		for i, chunk := range c.C {
			b := make([]byte, len(chunk))
			for j, v := range chunk {
				b[j] = byte(v)
			}
			got := u16units(string(d.write(nil, b)))
			if !rvSameUnits(got, c.Parts[i]) && bad < 10 {
				bad++
				t.Errorf("chunks %v write %d: got %v want %v", c.C, i, got, c.Parts[i])
			}
		}
		got := u16units(string(d.end(nil)))
		if !rvSameUnits(got, c.Parts[len(c.C)]) && bad < 10 {
			bad++
			t.Errorf("chunks %v end: got %v want %v", c.C, got, c.Parts[len(c.C)])
		}
	}
}

func rvSameUnits(a []uint16, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if int(a[i]) != b[i] {
			return false
		}
	}
	return true
}

// Differential against V8 (jsdiff.ts on xd6702): js.go and message_text.go string helpers over
// random strings with lone surrogates, astral characters, sigma contexts and JS whitespace.
func TestReviewJSDifferential(t *testing.T) {
	raw := testdataGz(t, "core_jsdiff.json.gz")
	var data struct {
		Cases []struct {
			S, Sub, Header                                    []uint16
			A, B, From, Lim                                   int
			Lower, Trim, TrimStart, TrimEnd, Slice, Substring []uint16
			IndexOf, LastIndexOf, CharCodeAt                  int
			CharAt, Excerpt, Around, Bounded                  []uint16
			Short                                             string
		}
		Nums []struct {
			N     int
			Loc   string
			F     float64
			Round string
		}
	}
	if err := sdkjson.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	fails := map[string]int{}
	check := func(what string, s string, got string, want []uint16) {
		if !slicesEqual16(u16units(got), want) {
			fails[what]++
			if fails[what] <= 3 {
				t.Errorf("%s(%U): got %U want %U", what, u16units(s), u16units(got), want)
			}
		}
	}
	for _, c := range data.Cases {
		s, sub, header := u16string(c.S), u16string(c.Sub), u16string(c.Header)
		check("toLowerCase", s, jsToLowerCase(s), c.Lower)
		check("trim", s, jsTrim(s), c.Trim)
		check("trimStart", s, jsTrimStart(s), c.TrimStart)
		check("trimEnd", s, jsTrimEnd(s), c.TrimEnd)
		check("slice", s, u16slice(s, c.A, c.B), c.Slice)
		check("substring", s, u16substring(s, c.A, c.B), c.Substring)
		check("charAt", s, u16at(s, c.A), c.CharAt)
		if got := u16index(s, sub, c.From); got != c.IndexOf {
			fails["indexOf"]++
			if fails["indexOf"] <= 3 {
				t.Errorf("indexOf(%U, %U, %d) = %d want %d", c.S, c.Sub, c.From, got, c.IndexOf)
			}
		}
		if got := u16lastIndex(s, sub, c.From); got != c.LastIndexOf {
			fails["lastIndexOf"]++
			if fails["lastIndexOf"] <= 3 {
				t.Errorf("lastIndexOf(%U, %U, %d) = %d want %d", c.S, c.Sub, c.From, got, c.LastIndexOf)
			}
		}
		code, ok := u16code(s, c.A)
		if gotCode := map[bool]int{true: int(code), false: -1}[ok]; gotCode != c.CharCodeAt {
			fails["charCodeAt"]++
			if fails["charCodeAt"] <= 3 {
				t.Errorf("charCodeAt(%U, %d) = %d want %d", c.S, c.A, gotCode, c.CharCodeAt)
			}
		}
		check("excerpt", s, excerpt(strings.Repeat(s, 4), c.Lim), c.Excerpt)
		check("bounded", s, boundedBlock(header, strings.Repeat(s, 3), c.Lim+5), c.Bounded)
		if got := shortKey(s); got != c.Short {
			fails["shortKey"]++
			if fails["shortKey"] <= 3 {
				t.Errorf("shortKey(%U) = %s want %s", c.S, got, c.Short)
			}
		}
	}
	for _, n := range data.Nums {
		if n.Loc == "-0" {
			continue // JS -0: no integer the port formats can be -0 (Go int)
		}
		if got := jsToLocaleInt(n.N); got != n.Loc {
			fails["toLocaleString"]++
			if fails["toLocaleString"] <= 3 {
				t.Errorf("toLocaleString(%d) = %s want %s", n.N, got, n.Loc)
			}
		}
		r := jsRound(n.F)
		got := jsIntegerString(r)
		if r == 0 && math.Signbit(r) {
			got = "-0"
		}
		if got != n.Round {
			fails["round"]++
			if fails["round"] <= 3 {
				t.Errorf("Math.round(%v) = %s want %s", n.F, got, n.Round)
			}
		}
	}
	t.Logf("cases=%d nums=%d fails=%v", len(data.Cases), len(data.Nums), fails)
}

func slicesEqual16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Differential against V8 (gitdir.ts): gitdirOf is `text.match(/^gitdir:\s*(.+?)\s*$/m)?.[1]` (files.ts:187).
func TestReviewGitdirDifferential(t *testing.T) {
	raw := testdataGz(t, "core_gitdir.json.gz")
	var cases []struct {
		S string
		M *string
	}
	if err := sdkjson.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	fails := 0
	for _, c := range cases {
		got, ok := gitdirOf(c.S)
		if ok != (c.M != nil) || ok && got != *c.M {
			if fails++; fails <= 5 {
				t.Errorf("gitdirOf(%q) = %q, %v; want %v", c.S, got, ok, c.M)
			}
		}
	}
	t.Logf("cases=%d fails=%d", len(cases), fails)
}

// resolveFileTarget against the TS on the same layout (resolve.ts on xd6702, Node 24.22.1).
func TestReviewResolveFileTargetLayout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("probe recorded on Linux")
	}
	want := `[["d/f.md","<base>/d/f.md"],["link-f","<base>/d/f.md"],["link-d/f.md","<base>/d/f.md"],["link-d/new.md","<base>/d/new.md"],["dangling","ERR:ENOENT: no such file or directory, stat '<base>/missing'|ENOENT"],["cyc1","ERR:Symlink cycle: <base>/cyc1|ELOOP"],["file/x.md","ERR:Not a directory: <base>/file|ENOTDIR"],["file/../d/f.md","<base>/d/f.md"],["d/","<base>/d"],["link-d/","<base>/d"],["missingdir/x.md","ERR:ENOENT: no such file or directory, stat '<base>/missingdir'|ENOENT"],["sub/back","<base>/d/f.md"],["d/f.md/","ERR:ENOTDIR: not a directory, stat '<base>/d/f.md/'|ENOTDIR"],["up","<base>/d/f.md"],["abs-d-slash","<base>/d"],["loopdir/x.md","ERR:ELOOP: too many symbolic links encountered, stat '<base>/loopdir'|ELOOP"],["d/./f.md","<base>/d/f.md"],["d//f.md","<base>/d/f.md"],["link-d/../file","<base>/file"]]`
	base := pnDir(t)
	pnMkdir(t, base, "d")
	pnWrite(t, filepath.Join(base, "d", "f.md"), "x")
	pnWrite(t, filepath.Join(base, "file"), "x")
	pnMkdir(t, base, "sub")
	for _, l := range [][2]string{{"d", "link-d"}, {"d/f.md", "link-f"}, {"missing/new.md", "dangling"}, {"cyc2", "cyc1"}, {"cyc1", "cyc2"},
		{"link-d/../d/f.md", "up"}, {"../link-d/f.md", "sub/back"}, {filepath.Join(base, "d") + "/", "abs-d-slash"}, {"loopdir", "loopdir"}} {
		pnMust(t, os.Symlink(l[0], filepath.Join(base, l[1])))
	}
	inputs := []string{"d/f.md", "link-f", "link-d/f.md", "link-d/new.md", "dangling", "cyc1", "file/x.md", "file/../d/f.md", "d/", "link-d/", "missingdir/x.md", "sub/back", "d/f.md/", "up", "abs-d-slash", "loopdir/x.md", "d/./f.md", "d//f.md", "link-d/../file"}
	var b strings.Builder
	b.WriteByte('[')
	for i, rel := range inputs {
		if i > 0 {
			b.WriteByte(',')
		}
		path := filepath.Join(base, rel)
		if strings.HasSuffix(rel, "/") {
			path += "/"
		}
		got, err := resolveFileTarget(path)
		text := strings.ReplaceAll(got, base, "<base>")
		if err != nil {
			code := ""
			if fe, ok := err.(*fsError); ok {
				code = fe.Code
			}
			text = "ERR:" + strings.ReplaceAll(err.Error(), base, "<base>") + "|" + code
		}
		b.WriteString(`["` + rel + `","` + text + `"]`)
	}
	b.WriteByte(']')
	if b.String() != want {
		t.Fatalf("resolveFileTarget layout:\n got %s\nwant %s", b.String(), want)
	}
}

// SDK GAP (not CoreLane code): sdk.EstimateTokens counts a lone surrogate in its WTF-8 form (the form the SDK
// delivers it in) as three units; Pi's estimateTokens counts "\ud800".length === 1. Used by retention.go messageCost.
func TestReviewSDKEstimateTokensLoneSurrogate(t *testing.T) {
	t.Skip("SDK gap S1 (PiG sdk.EstimateTokens, compaction_estimate.go utf16Length ranges runes: each byte of a WTF-8 lone surrogate is one RuneError, so 3 units instead of 1): " +
		"sdk.EstimateTokens(rec{role: user, content: 8 lone surrogates}) = 6, Pi's estimateTokens = 2. Remove this skip when the SDK counts a WTF-8 surrogate as one unit.")
	lone := u16string([]uint16{0xD800, 0xD800, 0xD800, 0xD800, 0xD800, 0xD800, 0xD800, 0xD800})
	if n := u16len(lone); n != 8 {
		t.Fatalf("u16len = %d", n)
	}
	// JS: Math.ceil("\ud800".repeat(8).length / 4) === 2.
	if got := sdk.EstimateTokens(rec{"role": "user", "content": lone}); got != 2 {
		t.Fatalf("sdk.EstimateTokens(8 lone surrogates) = %d, want 2 (Pi)", got)
	}
}
