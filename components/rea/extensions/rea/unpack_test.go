package rea

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/klauspost/compress/zstd"
)

type fixtureFile struct {
	name string
	body string
	exec bool
}

// fixture packs files as payload.mjs does (sorted, directories first by path order, payload.json outside the tree digest)
// and returns a payload source over an in-memory file system.
func fixture(t *testing.T, files []fixtureFile, extra ...*tar.Header) (payloadSource, payloadInfo) {
	t.Helper()
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	dirs := map[string]bool{}
	tree := sha256.New()
	for _, f := range files {
		for dir := filepath.ToSlash(filepath.Dir(f.name)); dir != "." && !dirs[dir]; dir = filepath.ToSlash(filepath.Dir(dir)) {
			dirs[dir] = true
		}
	}
	var names []string
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		if err := tw.WriteHeader(&tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		mode := int64(0o644)
		if f.exec {
			mode = 0o755
		}
		if f.name != "payload.json" {
			sum := sha256.Sum256([]byte(f.body))
			fmt.Fprintf(tree, "%o %s %s\n", mode, hex.EncodeToString(sum[:]), f.name)
		}
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(f.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range extra {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	enc, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(compressed.Bytes())
	info := payloadInfo{Target: runtime.GOOS + "/" + runtime.GOARCH, Entry: "rea/scripts/rea.mjs", TreeSha256: hex.EncodeToString(tree.Sum(nil)), ArchiveSha256: hex.EncodeToString(sum[:]), Files: len(files)}
	info.Rea.Package, info.Rea.Version = "rea-agents", "6.8.0"
	info.Node.Version, info.Node.Executable = "24.22.2", "node/bin/node"
	description, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	stem := "payload/" + runtime.GOOS + "_" + runtime.GOARCH
	fsys := fstest.MapFS{stem + ".tar.zst": {Data: compressed.Bytes()}, stem + ".json": {Data: description}}
	return payloadSource{fsys: fsys, stem: stem}, info
}

var sampleFiles = []fixtureFile{
	{name: "node/bin/node", body: "#!node", exec: true},
	{name: "node/LICENSE", body: "MIT"},
	{name: "rea/scripts/rea.mjs", body: "console.log('rea')", exec: true},
	{name: "rea/package.json", body: `{"name":"rea-agents"}`},
	{name: "payload.json", body: "{}"},
}

func TestUnpackWritesPayloadOnceAndReusesIt(t *testing.T) {
	src, info := fixture(t, sampleFiles)
	root := t.TempDir()
	l, unpacked, err := ensureUnpacked(root, src, info)
	if err != nil || !unpacked {
		t.Fatalf("first unpack: unpacked=%v err=%v", unpacked, err)
	}
	if l.dir != filepath.Join(root, info.ArchiveSha256[:16]) || l.node != filepath.Join(l.dir, "node", "bin", "node") || l.entry != filepath.Join(l.dir, "rea", "scripts", "rea.mjs") {
		t.Fatalf("layout %+v", l)
	}
	if got, _ := os.ReadFile(l.entry); string(got) != "console.log('rea')" {
		t.Fatalf("entry content %q", got)
	}
	if executableBits {
		if st, _ := os.Stat(l.node); st.Mode()&0o111 == 0 {
			t.Fatalf("node is not executable: %v", st.Mode())
		}
		if st, _ := os.Stat(filepath.Join(l.dir, "node", "LICENSE")); st.Mode()&0o111 != 0 {
			t.Fatalf("LICENSE is executable: %v", st.Mode())
		}
	}
	if _, unpacked, err := ensureUnpacked(root, src, info); err != nil || unpacked {
		t.Fatalf("second start must reuse the payload: unpacked=%v err=%v", unpacked, err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("staging left behind: %v", entries)
	}
}

func TestUnpackReplacesDirectoryWithoutSentinel(t *testing.T) {
	src, info := fixture(t, sampleFiles)
	root := t.TempDir()
	stale := filepath.Join(root, info.ArchiveSha256[:16])
	if err := os.MkdirAll(filepath.Join(stale, "junk"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, unpacked, err := ensureUnpacked(root, src, info)
	if err != nil || !unpacked {
		t.Fatalf("unpacked=%v err=%v", unpacked, err)
	}
	if _, err := os.Stat(filepath.Join(l.dir, "junk")); !os.IsNotExist(err) {
		t.Fatalf("the incomplete directory survived: %v", err)
	}
	if _, err := verify(src, info, l.dir); err != nil {
		t.Fatal(err)
	}
}

func TestUnpackRejectsEntriesOutsideTheDirectory(t *testing.T) {
	for _, name := range []string{"../escape", "/abs/path", "a/../../b", sentinelName} {
		t.Run(name, func(t *testing.T) {
			src, info := fixture(t, sampleFiles, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644})
			root := t.TempDir()
			if _, _, err := ensureUnpacked(root, src, info); err == nil || !strings.Contains(err.Error(), "not a local path") {
				t.Fatalf("err = %v", err)
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatalf("a failed unpack left %v", entries)
			}
		})
	}
}

func TestUnpackRejectsLinks(t *testing.T) {
	src, info := fixture(t, sampleFiles, &tar.Header{Name: "rea/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	if _, _, err := ensureUnpacked(t.TempDir(), src, info); err == nil || !strings.Contains(err.Error(), "is not packed") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnpackChecksArchiveDigest(t *testing.T) {
	src, info := fixture(t, sampleFiles)
	info.ArchiveSha256 = strings.Repeat("0", 64)
	root := t.TempDir()
	if _, _, err := ensureUnpacked(root, src, info); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Fatalf("an unpack with a wrong digest left %v", entries)
	}
}

func TestVerifyReportsChangedMissingAndExtraFiles(t *testing.T) {
	src, info := fixture(t, sampleFiles)
	l, _, err := ensureUnpacked(t.TempDir(), src, info)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := verify(src, info, l.dir); err != nil || n != len(sampleFiles) {
		t.Fatalf("clean verify: n=%d err=%v", n, err)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "rea", "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(l.dir, "node", "LICENSE")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "rea", "added.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = verify(src, info, l.dir)
	for _, want := range []string{"rea/package.json: content differs", "node/LICENSE: missing", "rea/added.js: not in the payload"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("verify error %v does not report %q", err, want)
		}
	}
}

func TestVerifyChecksRecordedTreeDigest(t *testing.T) {
	src, info := fixture(t, sampleFiles)
	l, _, err := ensureUnpacked(t.TempDir(), src, info)
	if err != nil {
		t.Fatal(err)
	}
	info.TreeSha256 = strings.Repeat("f", 64)
	if _, err := verify(src, info, l.dir); err == nil || !strings.Contains(err.Error(), "tree digest") {
		t.Fatalf("err = %v", err)
	}
}

func TestGCKeepsCurrentAndFreshStaging(t *testing.T) {
	root := t.TempDir()
	keep, old := "0123456789abcdef", "fedcba9876543210"
	for _, name := range []string{keep, old, stagingPrefix + "fresh", stagingPrefix + "stale", "unrelated", "ABCDEF0123456789"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(root, stagingPrefix+"stale"), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	removed, err := gc(root, keep, now)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if strings.Join(removed, ",") != stagingPrefix+"stale,"+old {
		t.Fatalf("removed %v", removed)
	}
	for _, name := range []string{keep, stagingPrefix + "fresh", "unrelated", "ABCDEF0123456789"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s was removed", name)
		}
	}
}

func TestPrepareRegistersTheUnpackedPayload(t *testing.T) {
	src, info := fixture(t, sampleFiles)
	agentDir := t.TempDir()
	m := prepare(src, func() (string, error) { return agentDir, nil })
	if m.err != nil {
		t.Fatal(m.err)
	}
	config := m.serverConfig()
	dir := filepath.Join(agentDir, "rea", info.ArchiveSha256[:16])
	if config.Type != "stdio" || config.Command != filepath.Join(dir, "node", "bin", "node") || strings.Join(config.Args, " ") != filepath.Join(dir, "rea", "scripts", "rea.mjs")+" mcp" {
		t.Fatalf("config %+v", config)
	}
	if config.Env != nil || config.Exposure != "" || config.Description != "" || config.Cwd != "" {
		t.Fatalf("the registration overrides what rea setup leaves unset: %+v", config)
	}
}

func TestPrepareReportsMissingPayload(t *testing.T) {
	m := prepare(payloadSource{fsys: fstest.MapFS{}, stem: "payload/none"}, func() (string, error) { return t.TempDir(), nil })
	if m.err == nil || !strings.Contains(m.err.Error(), "carries no REA payload") {
		t.Fatalf("err = %v", m.err)
	}
	if _, err := m.run("status", false); err == nil {
		t.Fatal("status of a Binary without payload must fail")
	}
}

func TestRunRejectsUnknownAction(t *testing.T) {
	m := &member{}
	if _, err := m.run("install", false); err == nil || err.Error() != usage {
		t.Fatalf("err = %v", err)
	}
}

// TestEmbeddedPayloadUnpacksAndVerifies runs the real payload when this checkout has one (payload.mjs ran for this machine).
func TestEmbeddedPayloadUnpacksAndVerifies(t *testing.T) {
	src := embeddedPayload()
	info, err := src.info()
	if err != nil {
		t.Skipf("no payload for this target: %v", err)
	}
	if testing.Short() {
		t.Skip("unpacks the full REA payload")
	}
	l, _, err := ensureUnpacked(t.TempDir(), src, info)
	if err != nil {
		t.Fatal(err)
	}
	n, err := verify(src, info, l.dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != info.Files+1 { // payload.json is packed but not counted in the tree
		t.Fatalf("verified %d files, the payload lists %d plus payload.json", n, info.Files)
	}
}
