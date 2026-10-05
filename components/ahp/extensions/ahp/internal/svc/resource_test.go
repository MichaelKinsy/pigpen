package svc

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/resource.test.ts: the resource* family driven end-to-end through a host
// and a protocol client, over a real temporary directory.

func fileURI(path string) string { return wire.PathToFileURI(path) }

type resourceFixture struct {
	client    *testkit.Client
	workspace string
}

func startResourceFixture(t *testing.T, roots ...string) *resourceFixture {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPathPolicy(roots...)
	if err != nil {
		t.Fatal(err)
	}
	h := testkit.NewHost(host.Options{})
	h.Serve(host.Capabilities{Resources: NewResourceService(ResourceOptions{Paths: policy})})
	c := testkit.Connect(t, h)
	t.Cleanup(c.Close)
	c.Initialize("resource-client", nil)
	return &resourceFixture{client: c, workspace: workspace}
}

func (f *resourceFixture) path(parts ...string) string {
	return filepath.Join(append([]string{f.workspace}, parts...)...)
}

func (f *resourceFixture) call(t *testing.T, method string, params map[string]any) json.RawMessage {
	t.Helper()
	params["channel"] = wire.RootChannel
	return f.client.Must(method, params)
}

func (f *resourceFixture) expectError(t *testing.T, code int, method string, params map[string]any) {
	t.Helper()
	params["channel"] = wire.RootChannel
	f.client.ExpectError(method, params, code)
}

type readResult struct {
	Data        string `json:"data"`
	Encoding    string `json:"encoding"`
	ContentType string `json:"contentType"`
}

func (f *resourceFixture) read(t *testing.T, path string, extra map[string]any) readResult {
	t.Helper()
	params := map[string]any{"uri": fileURI(path)}
	for k, v := range extra {
		params[k] = v
	}
	raw := f.call(t, "resourceRead", params)
	var out readResult
	f.client.Decode(raw, &out)
	return out
}

type resolveResult struct {
	URI         string `json:"uri"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
	Etag        string `json:"etag"`
}

func (f *resourceFixture) resolve(t *testing.T, path string, extra map[string]any) resolveResult {
	t.Helper()
	params := map[string]any{"uri": fileURI(path)}
	for k, v := range extra {
		params[k] = v
	}
	raw := f.call(t, "resourceResolve", params)
	var out resolveResult
	f.client.Decode(raw, &out)
	var generic any
	_ = json.Unmarshal(raw, &generic)
	testkit.AssertValid(t, "commands", "ResourceResolveResult", generic)
	return out
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o666); err != nil {
		t.Fatal(err)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func TestResourceOperations(t *testing.T) {
	f := startResourceFixture(t)
	writeFile(t, f.path("note.txt"), []byte("ALPHA BETA GAMMA\n"))
	writeFile(t, f.path("data.json"), []byte("{}\n"))
	writeFile(t, f.path("source.swift"), []byte("struct Example {}\n"))
	writeFile(t, f.path("invalid.txt"), []byte{0xff, 0xfe})
	if err := os.Mkdir(f.path("nested"), 0o777); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.path("nested", "inner.md"), []byte("# Inner\n"))
	if err := os.Symlink(f.path("nested"), f.path("nested-link")); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	writeFile(t, f.path("blob.bin"), []byte{0, 1, 2})
	writeFile(t, f.path("image.png"), []byte{0x89, 0x50, 0x4e, 0x47})

	twin.Run(t, "resource", "reads a text file as utf-8 with a content type", func(t *testing.T) {
		raw := f.call(t, "resourceRead", map[string]any{"uri": fileURI(f.path("note.txt"))})
		var result readResult
		f.client.Decode(raw, &result)
		if result.Encoding != "utf-8" || result.Data != "ALPHA BETA GAMMA\n" || result.ContentType != "text/plain" {
			t.Fatalf("%+v", result)
		}
		var generic any
		_ = json.Unmarshal(raw, &generic)
		testkit.AssertValid(t, "commands", "ResourceReadResult", generic)
	})

	twin.Run(t, "resource", "detects valid UTF-8 independently of the extension and reports known MIME types", func(t *testing.T) {
		source := f.read(t, f.path("source.swift"), nil)
		if source.Encoding != "utf-8" || source.Data != "struct Example {}\n" || source.ContentType != "text/plain" {
			t.Fatalf("%+v", source)
		}
		j := f.read(t, f.path("data.json"), nil)
		if j.Encoding != "utf-8" || j.ContentType != "application/json" {
			t.Fatalf("%+v", j)
		}
		image := f.read(t, f.path("image.png"), nil)
		if image.Encoding != "base64" || image.ContentType != "image/png" {
			t.Fatalf("%+v", image)
		}
	})

	twin.Run(t, "resource", "falls back to base64 when content is not valid UTF-8", func(t *testing.T) {
		binary := f.read(t, f.path("blob.bin"), nil)
		got, _ := base64.StdEncoding.DecodeString(binary.Data)
		if binary.Encoding != "base64" || !reflect.DeepEqual(got, []byte{0, 1, 2}) {
			t.Fatalf("%+v", binary)
		}
		misleading := f.read(t, f.path("invalid.txt"), map[string]any{"encoding": "utf-8"})
		got, _ = base64.StdEncoding.DecodeString(misleading.Data)
		if misleading.Encoding != "base64" || !reflect.DeepEqual(got, []byte{0xff, 0xfe}) {
			t.Fatalf("%+v", misleading)
		}
	})

	twin.Run(t, "resource", "honours an explicitly requested encoding", func(t *testing.T) {
		result := f.read(t, f.path("note.txt"), map[string]any{"encoding": "base64"})
		got, _ := base64.StdEncoding.DecodeString(result.Data)
		if result.Encoding != "base64" || string(got) != "ALPHA BETA GAMMA\n" {
			t.Fatalf("%+v", result)
		}
	})

	twin.Run(t, "resource", "lists a directory with directories first", func(t *testing.T) {
		raw := f.call(t, "resourceList", map[string]any{"uri": fileURI(f.workspace)})
		var result struct{ Entries []struct{ Name, Type string } }
		f.client.Decode(raw, &result)
		if result.Entries[0].Type != "directory" || result.Entries[0].Name != "nested" {
			t.Fatalf("first entry: %+v", result.Entries[0])
		}
		has := func(name, kind string) bool {
			for _, e := range result.Entries {
				if e.Name == name && e.Type == kind {
					return true
				}
			}
			return false
		}
		if !has("note.txt", "file") || !has("nested-link", "directory") {
			t.Fatalf("entries: %+v", result.Entries)
		}
		var generic any
		_ = json.Unmarshal(raw, &generic)
		testkit.AssertValid(t, "commands", "ResourceListResult", generic)
	})

	twin.Run(t, "resource", "resolves files and symlinks with canonical metadata", func(t *testing.T) {
		result := f.resolve(t, f.path("note.txt"), nil)
		if result.Type != "file" || result.Size != 17 || result.ContentType != "text/plain" || result.Etag == "" {
			t.Fatalf("%+v", result)
		}
		if image := f.resolve(t, f.path("image.png"), nil); image.ContentType != "image/png" {
			t.Fatalf("%+v", image)
		}
		link := f.resolve(t, f.path("nested-link"), map[string]any{"followSymlinks": false})
		if link.Type != "symlink" || link.URI != fileURI(f.path("nested-link")) {
			t.Fatalf("%+v", link)
		}
		target := f.resolve(t, f.path("nested-link"), nil)
		real, _ := filepath.EvalSymlinks(f.path("nested"))
		if target.Type != "directory" || target.URI != fileURI(real) {
			t.Fatalf("%+v", target)
		}
	})

	twin.Run(t, "resource", "writes, then reads back what it wrote", func(t *testing.T) {
		target := f.path("written.txt")
		f.call(t, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "hello", "encoding": "utf-8"})
		if got := readString(t, target); got != "hello" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "resource", "reports a missing parent directory instead of creating it implicitly", func(t *testing.T) {
		target := f.path("missing", "file.txt")
		f.expectError(t, -32008, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "x", "encoding": "utf-8"})
		if exists(target) {
			t.Fatal("the file was created")
		}
	})

	twin.Run(t, "resource", "appends without clobbering", func(t *testing.T) {
		target := f.path("append.txt")
		f.call(t, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "one", "encoding": "utf-8"})
		f.call(t, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "-two", "encoding": "utf-8", "mode": "append"})
		if got := readString(t, target); got != "one-two" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "resource", "refuses to overwrite when createOnly is set", func(t *testing.T) {
		target := f.path("once.txt")
		f.call(t, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "first", "encoding": "utf-8"})
		f.expectError(t, -32010, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "second", "encoding": "utf-8", "createOnly": true})
		if got := readString(t, target); got != "first" {
			t.Fatalf("%q", got)
		}

		raced := f.path("create-race.txt")
		var wg sync.WaitGroup
		errs := make([]*testkit.RPCError, 2)
		for i, data := range []string{"one", "two"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = f.client.Request("resourceWrite", map[string]any{"channel": wire.RootChannel, "uri": fileURI(raced), "data": data, "encoding": "utf-8", "createOnly": true})
			}()
		}
		wg.Wait()
		failed := 0
		for _, e := range errs {
			if e != nil {
				failed++
				if e.Code != -32010 {
					t.Fatalf("rejection code %d", e.Code)
				}
			}
		}
		if failed != 1 {
			t.Fatalf("exactly one createOnly write must win, %d failed", failed)
		}
		if got := readString(t, raced); got != "one" && got != "two" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "resource", "applies byte positions for truncate, append, and insert", func(t *testing.T) {
		cases := []struct{ file, initial, mode, data, want string }{
			{"truncate-position.txt", "abcdef", "truncate", "XY", "abcXY"},
			{"append-position.txt", "abcdef", "append", "XY", "abcdXYef"},
			{"insert-position.txt", "éZ", "insert", "!", "é!Z"},
		}
		positions := map[string]int{"truncate-position.txt": 3, "append-position.txt": 2, "insert-position.txt": 2}
		for _, c := range cases {
			writeFile(t, f.path(c.file), []byte(c.initial))
			f.call(t, "resourceWrite", map[string]any{"uri": fileURI(f.path(c.file)), "data": c.data, "encoding": "utf-8", "mode": c.mode, "position": positions[c.file]})
			if got := readString(t, f.path(c.file)); got != c.want {
				t.Fatalf("%s: %q, want %q", c.file, got, c.want)
			}
		}
	})

	twin.Run(t, "resource", "rejects invalid write modes and positions", func(t *testing.T) {
		for _, extra := range []map[string]any{
			{"mode": "overwrite"},
			{"mode": "insert", "position": -1},
			{"mode": "insert", "position": 1.5},
		} {
			params := map[string]any{"uri": fileURI(f.path("invalid-write.txt")), "data": "x", "encoding": "utf-8"}
			for k, v := range extra {
				params[k] = v
			}
			f.expectError(t, -32602, "resourceWrite", params)
		}
	})

	twin.Run(t, "resource", "enforces ifMatch and serializes competing conditional writes", func(t *testing.T) {
		missing := f.path("conditional-missing.txt")
		f.expectError(t, -32011, "resourceWrite", map[string]any{"uri": fileURI(missing), "data": "new", "encoding": "utf-8", "ifMatch": "stale"})
		if exists(missing) {
			t.Fatal("a stale ifMatch created the file")
		}

		target := f.path("conditional.txt")
		writeFile(t, target, []byte("original"))
		initial := f.resolve(t, target, nil)
		if initial.Etag == "" {
			t.Fatal("no etag")
		}
		f.call(t, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "first update", "encoding": "utf-8", "ifMatch": initial.Etag})
		if got := readString(t, target); got != "first update" {
			t.Fatalf("%q", got)
		}
		f.expectError(t, -32011, "resourceWrite", map[string]any{"uri": fileURI(target), "data": "stale update", "encoding": "utf-8", "ifMatch": initial.Etag})
		if got := readString(t, target); got != "first update" {
			t.Fatalf("%q", got)
		}

		current := f.resolve(t, target, nil)
		var wg sync.WaitGroup
		errs := make([]*testkit.RPCError, 2)
		for i, data := range []string{"winner one", "winner number two"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = f.client.Request("resourceWrite", map[string]any{"channel": wire.RootChannel, "uri": fileURI(target), "data": data, "encoding": "utf-8", "ifMatch": current.Etag})
			}()
		}
		wg.Wait()
		failed := 0
		for _, e := range errs {
			if e != nil {
				failed++
				if e.Code != -32011 {
					t.Fatalf("rejection code %d", e.Code)
				}
			}
		}
		if failed != 1 {
			t.Fatalf("exactly one conditional write must win, %d failed", failed)
		}
	})

	twin.Run(t, "resource", "makes directories with mkdir -p semantics", func(t *testing.T) {
		target := f.path("a", "b", "c")
		f.call(t, "resourceMkdir", map[string]any{"uri": fileURI(target)})
		// Idempotent, like `mkdir -p`.
		f.call(t, "resourceMkdir", map[string]any{"uri": fileURI(target)})
		if !exists(target) {
			t.Fatal("directory missing")
		}
	})

	twin.Run(t, "resource", "copies and moves files", func(t *testing.T) {
		source := f.path("copy-source.txt")
		writeFile(t, source, []byte("payload"))
		f.call(t, "resourceCopy", map[string]any{"source": fileURI(source), "destination": fileURI(f.path("copied.txt"))})
		if got := readString(t, f.path("copied.txt")); got != "payload" {
			t.Fatalf("%q", got)
		}
		f.call(t, "resourceMove", map[string]any{"source": fileURI(source), "destination": fileURI(f.path("moved.txt"))})
		if exists(source) {
			t.Fatal("the source survived the move")
		}
		if got := readString(t, f.path("moved.txt")); got != "payload" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "resource", "fails a copy onto an existing destination when asked to", func(t *testing.T) {
		source, destination := f.path("guard-source.txt"), f.path("guard-dest.txt")
		writeFile(t, source, []byte("new"))
		writeFile(t, destination, []byte("existing"))
		f.expectError(t, -32010, "resourceCopy", map[string]any{"source": fileURI(source), "destination": fileURI(destination), "failIfExists": true})
		if got := readString(t, destination); got != "existing" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "resource", "deletes files, and directories only when recursion is requested", func(t *testing.T) {
		doomed := f.path("doomed")
		if err := os.MkdirAll(doomed, 0o777); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(doomed, "child.txt"), []byte("x"))
		// PermissionDenied, not the AlreadyExists the other refusals use: a client that branches on
		// the code has to tell a directory it may not remove from a file that is already there.
		f.expectError(t, -32009, "resourceDelete", map[string]any{"uri": fileURI(doomed)})
		if !exists(doomed) {
			t.Fatal("a non-recursive delete must not take the tree with it")
		}
		f.call(t, "resourceDelete", map[string]any{"uri": fileURI(doomed), "recursive": true})
		if exists(doomed) {
			t.Fatal("the directory survived a recursive delete")
		}
	})

	twin.Run(t, "resource", "reports a missing file as NotFound", func(t *testing.T) {
		f.expectError(t, -32008, "resourceRead", map[string]any{"uri": fileURI(f.path("absent.txt"))})
	})

	twin.Run(t, "resource", "rejects a non-file scheme", func(t *testing.T) {
		f.expectError(t, -32602, "resourceRead", map[string]any{"uri": "https://example.com/x"})
	})

	twin.Run(t, "resource", "grants resourceRequest without tracking a ledger", func(t *testing.T) {
		// The host keeps no per-resource grants; a client that reached this endpoint can already
		// start a session and run commands.
		raw := f.call(t, "resourceRequest", map[string]any{"uri": fileURI(f.workspace), "read": true})
		var got map[string]any
		f.client.Decode(raw, &got)
		if len(got) != 0 {
			t.Fatalf("%v", got)
		}
	})
}

func TestResourceRoots(t *testing.T) {
	rootDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	writeFile(t, filepath.Join(rootDir, "inside.txt"), []byte("INSIDE\n"))
	writeFile(t, filepath.Join(outside, "secret.txt"), []byte("TOP SECRET\n"))
	f := startResourceFixture(t, rootDir)

	twin.Run(t, "resource", "allows paths inside a configured root after canonicalization", func(t *testing.T) {
		if got := f.read(t, filepath.Join(rootDir, "inside.txt"), nil); got.Data != "INSIDE\n" {
			t.Fatalf("%+v", got)
		}
	})

	twin.Run(t, "resource", "denies paths outside the configured roots, including prefix lookalikes", func(t *testing.T) {
		f.expectError(t, -32009, "resourceRead", map[string]any{"uri": fileURI(filepath.Join(outside, "secret.txt"))})
		lookalike := rootDir + "-other"
		if err := os.Mkdir(lookalike, 0o777); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(lookalike) })
		writeFile(t, filepath.Join(lookalike, "secret.txt"), []byte("PREFIX ESCAPE\n"))
		f.expectError(t, -32009, "resourceRead", map[string]any{"uri": fileURI(filepath.Join(lookalike, "secret.txt"))})
	})
}

func TestResourceRootsSymlinkEscape(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	dangling := filepath.Join(outside, "created-through-link.txt")
	writeFile(t, filepath.Join(outside, "secret.txt"), []byte("TOP SECRET\n"))
	if err := os.Mkdir(filepath.Join(outside, "secret-directory"), 0o777); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"link.txt": filepath.Join(outside, "secret.txt"), "directory-link": filepath.Join(outside, "secret-directory"), "dangling-link.txt": dangling} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Skip("symlinks unavailable: ", err)
		}
	}
	f := startResourceFixture(t, root)

	twin.Run(t, "resource", "resolves symlinks before checking the allowlist", func(t *testing.T) {
		raw := f.call(t, "resourceList", map[string]any{"uri": fileURI(root)})
		var listed struct{ Entries []struct{ Name, Type string } }
		f.client.Decode(raw, &listed)
		found := false
		for _, e := range listed.Entries {
			found = found || (e.Name == "directory-link" && e.Type == "file")
		}
		if !found {
			t.Fatalf("an escaping directory link must list as a file: %+v", listed.Entries)
		}
		// a symlink out of the root must not be readable
		f.expectError(t, -32009, "resourceRead", map[string]any{"uri": fileURI(filepath.Join(root, "link.txt"))})
		// a dangling symlink must not create a file outside the root
		f.expectError(t, -32009, "resourceWrite", map[string]any{"uri": fileURI(filepath.Join(root, "dangling-link.txt")), "data": "escaped", "encoding": "utf-8"})
		if exists(dangling) {
			t.Fatal("a file was created outside the root")
		}
	})
}
