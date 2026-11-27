package urls

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/test/internal-urls/docs-index.test.ts
// upstream: packages/coding-agent/test/internal-urls/omp-protocol.test.ts

func makeTestDocsEmbed(t *testing.T, files, bodies []string) string {
	t.Helper()
	filesJSON, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	bodiesJSON, err := json.Marshal(bodies)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(bodiesJSON); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return string(filesJSON) + "\n" + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDecodeDocsIndex(t *testing.T) {
	files := []string{"agent.md", "tools/read.md"}
	bodies := []string{"agent body", "read body"}

	t.Run("lists filenames without inflating body blob", func(t *testing.T) {
		filesJSON, _ := json.Marshal(files)
		idx, err := DecodeDocsIndex(string(filesJSON) + "\n@@@not-a-valid-gzip-blob@@@")
		if err != nil {
			t.Fatalf("DecodeDocsIndex: %v", err)
		}
		if idx == nil || strings.Join(idx.Filenames(), ",") != "agent.md,tools/read.md" {
			t.Fatalf("Filenames = %v, want %v", idx.Filenames(), files)
		}
	})

	t.Run("resolves bodies lazily on first read", func(t *testing.T) {
		idx, err := DecodeDocsIndex(makeTestDocsEmbed(t, files, bodies))
		if err != nil {
			t.Fatalf("DecodeDocsIndex: %v", err)
		}
		body, ok, err := idx.GetBody("agent.md")
		if err != nil || !ok || body != "agent body" {
			t.Fatalf("GetBody(agent.md) = (%q, %v, %v)", body, ok, err)
		}
		body, ok, err = idx.GetBody("tools/read.md")
		if err != nil || !ok || body != "read body" {
			t.Fatalf("GetBody(tools/read.md) = (%q, %v, %v)", body, ok, err)
		}
		if _, ok, err := idx.GetBody("missing.md"); err != nil || ok {
			t.Fatalf("GetBody(missing.md) = (_, %v, %v), want false", ok, err)
		}
	})

	t.Run("returns nil when no newline separator", func(t *testing.T) {
		idx, err := DecodeDocsIndex("")
		if err != nil || idx != nil {
			t.Fatalf("DecodeDocsIndex('') = (%v, %v), want (nil, nil)", idx, err)
		}
	})
}

func TestOmpProtocolHandler(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	t.Run("PromptDoc matches omp verbatim", func(t *testing.T) {
		doc := OmpHandler{}.PromptDoc(SchemeHost{})
		want := "omp://: harness docs, AVOID unless asked."
		if doc != want {
			t.Fatalf("PromptDoc = %q, want %q", doc, want)
		}
	})

	t.Run("treats omp:// and omp://docs as documentation root", func(t *testing.T) {
		for _, raw := range []string{"omp://", "omp://docs", "omp://docs/"} {
			res, err := Resolve(Context{}, raw)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", raw, err)
			}
			if !strings.Contains(res.Content, "# Documentation") {
				t.Fatalf("Resolve(%q) missing heading:\n%s", raw, res.Content)
			}
			if !strings.Contains(res.Content, "tools/read.md") {
				t.Fatalf("Resolve(%q) missing tools/read.md", raw)
			}
		}
	})

	t.Run("resolves direct and docs-prefixed paths identically", func(t *testing.T) {
		direct, err := Resolve(Context{}, "omp://tools/read.md")
		if err != nil {
			t.Fatalf("Resolve direct: %v", err)
		}
		prefixed, err := Resolve(Context{}, "omp://docs/tools/read.md")
		if err != nil {
			t.Fatalf("Resolve prefixed: %v", err)
		}
		if prefixed.Content != direct.Content {
			t.Fatalf("prefixed content differs from direct")
		}
		if !strings.Contains(direct.Content, "# read") {
			t.Fatalf("expected '# read' in tools/read.md")
		}
	})

	t.Run("rejects path traversal, absolute paths, and trailing slash on doc path", func(t *testing.T) {
		if _, err := Resolve(Context{}, "omp://../AGENTS.md"); err == nil || !strings.Contains(err.Error(), "Path traversal") {
			t.Fatalf("expected traversal error, got %v", err)
		}
		if _, err := Resolve(Context{}, "omp://tools/read.md/"); err == nil || !strings.Contains(err.Error(), "Documentation file not found") {
			t.Fatalf("expected not found for trailing slash on doc path, got %v", err)
		}
	})

	t.Run("unknown doc suggests close matches", func(t *testing.T) {
		_, err := Resolve(Context{}, "omp://read")
		if err == nil || !strings.Contains(err.Error(), "Did you mean:") || !strings.Contains(err.Error(), "tools/read.md") {
			t.Fatalf("expected suggestion error, got %v", err)
		}
		_, err = Resolve(Context{}, "omp://zzzz-no-such-doc-xyz.md")
		if err == nil || !strings.Contains(err.Error(), "Use omp:// to list available files.") {
			t.Fatalf("expected list hint, got %v", err)
		}
	})

	t.Run("Locate and LocateSync return false for embedded corpus", func(t *testing.T) {
		for _, raw := range []string{"omp://", "omp://docs", "omp://tools/read.md", "omp://no-such-doc.md", "omp://../AGENTS.md"} {
			if _, ok, err := Locate(Context{}, raw, LocateOptions{}); err != nil || ok {
				t.Fatalf("Locate(%q) = (_, %v, %v), want (_, false, nil)", raw, ok, err)
			}
			if _, ok := LocateSync(Context{}, raw); ok {
				t.Fatalf("LocateSync(%q) = true, want false", raw)
			}
		}
	})

	t.Run("Complete returns full list unfiltered and Enumerate works", func(t *testing.T) {
		allCount := len(getDefaultDocsIndex().Filenames())
		items, ok, err := Complete(Context{}, "omp", "tools/read")
		if err != nil || !ok || len(items) != allCount {
			t.Fatalf("Complete = (len=%d, ok=%v, err=%v), want len=%d (unfiltered)", len(items), ok, err, allCount)
		}
		found := false
		for _, it := range items {
			if it.Value == "tools/read.md" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected tools/read.md in completions")
		}

		leaves, ok, err := Enumerate(Context{}, "omp://tools/read.md")
		if err != nil || !ok || len(leaves) != 1 || leaves[0].URL != "omp://tools/read.md" {
			t.Fatalf("Enumerate single = (%d, %v, %v)", len(leaves), ok, err)
		}
	})

	t.Run("supports line selectors via ResolveTarget", func(t *testing.T) {
		target, err := ResolveTarget(Context{}, "omp://tools/read.md:1-10")
		if err != nil {
			t.Fatalf("ResolveTarget: %v", err)
		}
		if target.Kind != "resource" || target.Selector != "1-10" || target.URL.Href != "omp://tools/read.md" {
			t.Fatalf("unexpected target: %+v", target)
		}
	})
}

func TestDocsIndexManifestIntegrity(t *testing.T) {
	rawManifest, err := os.ReadFile("docs_index.manifest.json")
	if err != nil {
		t.Fatalf("read docs_index.manifest.json: %v", err)
	}
	var m struct {
		Generator string `json:"generator"`
		OmpTag    string `json:"ompTag"`
		OmpCommit string `json:"ompCommit"`
		FileCount int    `json:"fileCount"`
		Output    struct {
			File       string `json:"file"`
			Bytes      int    `json:"bytes"`
			SHA256     string `json:"sha256"`
			GzipBytes  int    `json:"gzipBytes"`
			GzipSHA256 string `json:"gzipSha256"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rawManifest, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.OmpTag != "v19.1.1" || m.OmpCommit != "31ffee513f27a01bba7d4dfbe12bae239cdc646a" {
		t.Fatalf("unexpected omp pin: tag=%q commit=%q", m.OmpTag, m.OmpCommit)
	}
	rawEmbed, err := os.ReadFile(m.Output.File)
	if err != nil {
		t.Fatalf("read %s: %v", m.Output.File, err)
	}
	if len(rawEmbed) != m.Output.Bytes {
		t.Fatalf("bytes = %d, manifest = %d", len(rawEmbed), m.Output.Bytes)
	}
	sum := sha256.Sum256(rawEmbed)
	if got := hex.EncodeToString(sum[:]); got != m.Output.SHA256 {
		t.Fatalf("sha256 = %s, manifest = %s", got, m.Output.SHA256)
	}
	idx := getDefaultDocsIndex()
	if len(idx.Filenames()) != m.FileCount {
		t.Fatalf("fileCount = %d, manifest = %d", len(idx.Filenames()), m.FileCount)
	}
	nl := bytes.IndexByte(rawEmbed, '\n')
	gzRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(rawEmbed[nl+1:])))
	if err != nil {
		t.Fatalf("decode b64: %v", err)
	}
	if len(gzRaw) != m.Output.GzipBytes {
		t.Fatalf("gzipBytes = %d, manifest = %d", len(gzRaw), m.Output.GzipBytes)
	}
	gzSum := sha256.Sum256(gzRaw)
	if got := hex.EncodeToString(gzSum[:]); got != m.Output.GzipSHA256 {
		t.Fatalf("gzipSha256 = %s, manifest = %s", got, m.Output.GzipSHA256)
	}
	if _, err := os.Stat("gen/gen_docs_index.py"); err != nil {
		t.Fatalf("missing generator: %v", err)
	}
}

func TestDocsIndexMatchesOmpSourceWhenSet(t *testing.T) {
	ompSrc := strings.TrimSpace(os.Getenv("OMP_SRC"))
	if ompSrc == "" {
		t.Skip("OMP_SRC is unset")
	}
	docsDir := filepath.Join(ompSrc, "docs")
	var files []string
	err := filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return err
		}
		rel, err := filepath.Rel(docsDir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", docsDir, err)
	}
	sort.Strings(files)
	idx := getDefaultDocsIndex()
	if strings.Join(idx.Filenames(), "\n") != strings.Join(files, "\n") {
		t.Fatalf("embedded filenames differ from $OMP_SRC/docs")
	}
	for _, f := range files {
		want, err := os.ReadFile(filepath.Join(docsDir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := idx.GetBody(f)
		if err != nil || !ok || got != string(want) {
			t.Fatalf("body mismatch for %s (ok=%v err=%v)", f, ok, err)
		}
	}
}
