package posthorse

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The ports of node:path (note_nodepath.go) against Node 24.22.1's own answers
// (testdata/note_node_paths.json.gz, written by running path.posix / path.win32 with process.cwd mocked, and the
// noteRelative of note-scopes.ts:93 over each flavour): resolve (1, 2 and 3 arguments), relative, dirname,
// isAbsolute and noteRelative.

type nodePathTable struct {
	Cwd     string `json:"cwd"`
	Resolve []struct {
		Args []string `json:"args"`
		R    string   `json:"r"`
	} `json:"resolve"`
	Relative []struct {
		From string `json:"from"`
		To   string `json:"to"`
		R    string `json:"r"`
	} `json:"relative"`
	Dirname []struct {
		P string `json:"p"`
		R string `json:"r"`
	} `json:"dirname"`
	IsAbs []struct {
		P string `json:"p"`
		R bool   `json:"r"`
	} `json:"isAbs"`
	Note []struct {
		P   string  `json:"p"`
		Ok  *string `json:"ok"`
		Err string  `json:"err"`
	} `json:"note"`
}

func loadNodePathTables(t *testing.T) (posix, win32 nodePathTable) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "note_node_paths.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var tables struct {
		Posix nodePathTable `json:"posix"`
		Win32 nodePathTable `json:"win32"`
	}
	if err := sdkjson.Unmarshal(raw, &tables); err != nil {
		t.Fatal(err)
	}
	return tables.Posix, tables.Win32
}

func TestNoteNodePath_matchesNode(t *testing.T) {
	posix, win32 := loadNodePathTables(t)
	for _, c := range []struct {
		name  string
		style nodeStyle
		table nodePathTable
	}{{"posix", posixStyle, posix}, {"win32", win32Style, win32}} {
		t.Run(c.name, func(t *testing.T) {
			cwd, style := c.table.Cwd, c.style
			bad := 0
			fail := func(format string, args ...any) {
				if bad++; bad <= 20 {
					t.Errorf(format, args...)
				}
			}
			for _, x := range c.table.Resolve {
				if got := style.resolve(cwd, x.Args...); got != x.R {
					fail("resolve(%q) = %q, node %q", x.Args, got, x.R)
				}
			}
			for _, x := range c.table.Relative {
				if got := style.relative(cwd, x.From, x.To); got != x.R {
					fail("relative(%q, %q) = %q, node %q", x.From, x.To, got, x.R)
				}
			}
			for _, x := range c.table.Dirname {
				if got := style.dirname(x.P); got != x.R {
					fail("dirname(%q) = %q, node %q", x.P, got, x.R)
				}
			}
			for _, x := range c.table.IsAbs {
				if got := style.isAbsolute(x.P); got != x.R {
					fail("isAbsolute(%q) = %v, node %v", x.P, got, x.R)
				}
			}
			for _, x := range c.table.Note {
				got, err := noteRelativeFor(style, cwd, x.P)
				switch {
				case x.Ok != nil && (err != nil || got != *x.Ok):
					fail("noteRelative(%q) = %q, %v; node %q", x.P, got, err, *x.Ok)
				case x.Ok == nil && (err == nil || err.Error() != x.Err):
					fail("noteRelative(%q) = %q, %v; node error %q", x.P, got, err, x.Err)
				}
				if !style.win32 {
					fast, ferr := noteRelativePosix(x.P)
					if (ferr == nil) != (err == nil) || fast != got || (err != nil && ferr.Error() != err.Error()) {
						fail("noteRelativePosix(%q) = %q, %v; generic %q, %v", x.P, fast, ferr, got, err)
					}
				}
			}
			t.Logf("%d resolve, %d relative, %d dirname, %d isAbsolute, %d noteRelative cases: %d mismatches",
				len(c.table.Resolve), len(c.table.Relative), len(c.table.Dirname), len(c.table.IsAbs), len(c.table.Note), bad)
		})
	}
}

// A drive-relative name resolves to the drive's root on win32: `C:x.md` is the note `x.md`, not an alternate
// data stream of a file `C` (note-scopes.ts:96, path.win32.resolve).
func TestNoteNodePath_win32DriveRelativeNoteName(t *testing.T) {
	got, err := noteRelativeFor(win32Style, `C:\Work\proj`, "C:x.md")
	if err != nil || got != "x.md" {
		t.Fatalf("noteRelative(C:x.md) = %q, %v", got, err)
	}
	// validNoteName compares the normalization with the name: "C:x.md" is not a stored note name.
	if got == "C:x.md" {
		t.Fatal("the normalization of a drive-relative name must differ from it")
	}
}

// realpathSync resolves `..` lexically before it follows any link, names the component it stats and names the
// syscall that failed.
func TestNoteNodePath_realpathErrorsNameTheComponent(t *testing.T) {
	root := nsRealTemp(t)
	if err := os.Symlink("loop", filepath.Join(root, "loop")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(root, "loop", "a", "b"):     "ELOOP: too many symbolic links encountered, stat '" + filepath.Join(root, "loop") + "'",
		filepath.Join(root, "file", "x"):          "ENOTDIR: not a directory, lstat '" + filepath.Join(root, "file", "x") + "'",
		filepath.Join(root, "nothing", "x"):       "ENOENT: no such file or directory, lstat '" + filepath.Join(root, "nothing") + "'",
		filepath.Join(root, "dangling"):           "ENOENT: no such file or directory, stat '" + filepath.Join(root, "dangling") + "'",
		filepath.Join(root, "dangling", "x"):      "ENOENT: no such file or directory, stat '" + filepath.Join(root, "dangling") + "'",
		filepath.Join(root, "loop", "..", "file"): "",
	}
	for in, want := range cases {
		got, err := nodeRealpath(in)
		switch {
		case want == "" && (err != nil || got != filepath.Join(root, "file")):
			t.Errorf("nodeRealpath(%s) = %q, %v; `..` is removed before the link is followed", in, got, err)
		case want != "" && (err == nil || err.Error() != want):
			t.Errorf("nodeRealpath(%s) error\n got %v\nwant %s", in, err, want)
		}
	}
	// A link followed before `..` keeps Node's order: the target's parent, not the link's.
	if err := os.MkdirAll(filepath.Join(root, "elsewhere", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere", "deep"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if got, err := nodeRealpath(filepath.Join(root, "link", "..", "elsewhere")); err != nil || got != filepath.Join(root, "elsewhere") {
		t.Errorf("link/../elsewhere = %q, %v", got, err)
	}
	if got, err := nodeRealpath(filepath.Join(root, "link") + string(filepath.Separator)); err != nil || got != filepath.Join(root, "elsewhere", "deep") {
		t.Errorf("trailing separator on a link = %q, %v", got, err)
	}
}
