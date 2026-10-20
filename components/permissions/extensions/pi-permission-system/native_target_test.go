package pi_permission_system

import (
	"path/filepath"
	"runtime"
	"testing"
)

// Twins of test/path/native-tool-target.test.ts.

const nativeCwd = "/projects/app"

// resolveWith resolves with an injected set of existing absolute paths.
func resolveWith(raw string, existing []string, readFallbacks bool) NativeToolTarget {
	set := map[string]bool{}
	for _, e := range existing {
		set[e] = true
	}
	return ResolveNativeToolTarget(raw, NativeToolTargetOptions{Cwd: nativeCwd, Flavor: PosixPathFlavor, ReadFallbacks: readFallbacks, Exists: func(p string) bool { return set[p] }})
}

func TestNativeToolTargetTwins(t *testing.T) {
	const f = "path/native-tool-target"
	home := func() string { return homeDir() }
	rw := func(raw string, existing ...string) NativeToolTarget { return resolveWith(raw, existing, true) }
	for _, c := range [][2]string{{"a no-break space", "/x/my\u00A0file.txt"}, {"a figure space", "/x/my\u2007file.txt"},
		{"an ideographic space", "/x/my\u3000file.txt"}, {"a narrow no-break space", "/x/my\u202Ffile.txt"}} {
		eqv(t, rw(c[1]).Target, "/x/my file.txt")
	}
	tw(t, f, "strips one leading @", func(t *testing.T) {
		eqv(t, rw("@src/a.ts").Target, "/projects/app/src/a.ts")
		eqv(t, rw("@@a").Target, "/projects/app/@a")
	})
	tw(t, f, "expands a bare ~ and ~/", func(t *testing.T) {
		eqv(t, rw("~").Target, home())
		eqv(t, rw("~/notes.md").Target, HostPath.Join(home(), "notes.md"))
	})
	tw(t, f, "decodes a file URL, percent-escapes included", func(t *testing.T) {
		eqv(t, rw("file:///etc/hosts").Target, "/etc/hosts")
		eqv(t, rw("file:///etc/h%6Fsts").Target, "/etc/hosts")
	})
	for _, c := range [][3]string{{"$HOME", "$HOME/x", "/projects/app/$HOME/x"}, {"${HOME}", "${HOME}/x", "/projects/app/${HOME}/x"},
		{"wrapping quotes", "'x.txt'", "/projects/app/'x.txt'"}, {"surrounding whitespace", " x.txt ", "/projects/app/ x.txt "},
		{"~user", "~user/x", "/projects/app/~user/x"}} {
		eqv(t, rw(c[1]).Target, c[2])
	}
	tw(t, f, "resolves a relative spelling against cwd", func(t *testing.T) { eqv(t, rw("../other/x").Target, "/projects/other/x") })
	tw(t, f, "translates a win32 drive mount under the win32 flavor", func(t *testing.T) {
		n := ResolveNativeToolTarget("/mnt/d/a/b.txt", NativeToolTargetOptions{Cwd: `C:\proj`, Flavor: Win32PathFlavor, Exists: func(string) bool { return false }})
		eqv(t, n.Target, `D:\a\b.txt`)
		eqv(t, n.Rewritten, true)
	})

	tw(t, f, "tries the AM/PM narrow no-break space variant", func(t *testing.T) {
		on := "/x/Shot 1.02.03\u202FPM.png"
		eqv(t, rw("/x/Shot 1.02.03 PM.png", on).Target, on)
	})
	tw(t, f, "tries the NFD variant", func(t *testing.T) {
		on := "/x/cafe\u0301.txt"
		eqv(t, rw("/x/caf\u00e9.txt", on).Target, on)
	})
	tw(t, f, "tries the curly-quote variant", func(t *testing.T) {
		on := "/x/d\u2019x.txt"
		eqv(t, rw("/x/d'x.txt", on).Target, on)
	})
	tw(t, f, "tries the NFD plus curly-quote variant", func(t *testing.T) {
		on := "/x/d\u2019e\u0301.txt"
		eqv(t, rw("/x/d'\u00e9.txt", on).Target, on)
	})
	tw(t, f, "prefers the typed spelling when it exists", func(t *testing.T) {
		eqv(t, rw("/x/d'x.txt", "/x/d'x.txt", "/x/d\u2019x.txt").Target, "/x/d'x.txt")
	})
	tw(t, f, "prefers the AM/PM variant over the curly-quote variant, in Pi's order", func(t *testing.T) {
		eqv(t, rw("/x/d'x 1 PM.png", "/x/d'x 1\u202FPM.png", "/x/d\u2019x 1 PM.png").Target, "/x/d'x 1\u202FPM.png")
	})
	tw(t, f, "keeps the typed spelling when no variant exists", func(t *testing.T) { eqv(t, rw("/x/d'x.txt").Target, "/x/d'x.txt") })
	tw(t, f, "tries no variant when the tool has no read fallbacks", func(t *testing.T) {
		eqv(t, resolveWith("/x/d'x.txt", []string{"/x/d\u2019x.txt"}, false).Target, "/x/d'x.txt")
	})

	for _, c := range []string{"src/a.ts", "/abs/x", "~/x", "@x", "/x/d'x.txt"} {
		eqv(t, rw(c).Rewritten, false)
	}
	eqv(t, rw("/x/my\u00A0file.txt").Rewritten, true)
	eqv(t, rw("file:///etc/hosts").Rewritten, true)
	eqv(t, rw("/x/d'x.txt", "/x/d\u2019x.txt").Rewritten, true)

	tw(t, f, "is the normalized relative spelling", func(t *testing.T) {
		eqv(t, *rw("../other/x").RelativeSpelling, "../other/x")
		eqv(t, *rw("@src/my\u00A0a.ts").RelativeSpelling, "src/my a.ts")
	})
	tw(t, f, "is absent for an absolute spelling", func(t *testing.T) {
		eqv(t, rw("/abs/x").RelativeSpelling == nil, true)
		eqv(t, rw("~/x").RelativeSpelling == nil, true)
	})
	tw(t, f, "is absent after a read fallback", func(t *testing.T) {
		eqv(t, rw("d'x.txt", "/projects/app/d\u2019x.txt").RelativeSpelling == nil, true)
	})
	tw(t, f, "falls back to the spelling resolved against cwd", func(t *testing.T) {
		n := rw("file://host/x")
		eqv(t, n.Target, "/projects/app/file:/host/x")
		eqv(t, n.Rewritten, false)
	})

	// Parity with Pi's resolver: the expected targets are Pi's own resolveReadPath / resolveToCwd results, recorded under the
	// same fixture layout (testdata/native-target.json, `<DIR>` and `<CWD>` stand for the fixture directories). A fixture that
	// the filesystem folds (APFS and NFD names) can only be compared on Linux.
	t.Run("parity with Pi's resolver", func(t *testing.T) {
		rec := loadJSONFixture[nativeParity](t, "native-target.json")
		dir, cwd := realTmp(t), realTmp(t)
		for _, name := range rec.Files {
			writeFile(t, dir, name, "x")
		}
		symlink(t, dir, "dang'x.txt", filepath.Join(dir, "missing-target"))
		sub := func(s string) string {
			return replaceAll(replaceAll(replaceAll(s, "<DIR>", dir), "<CWD>", cwd), "<HOME>", homeDir())
		}
		unsub := func(s string) string {
			return replaceAll(replaceAll(replaceAll(s, dir, "<DIR>"), cwd, "<CWD>"), homeDir(), "<HOME>")
		}
		check := func(t *testing.T, readFallbacks bool, want map[string]*string) {
			for _, spelling := range rec.Spellings {
				if runtime.GOOS != "linux" && rec.FsDependent[spelling] {
					continue
				}
				n := ResolveNativeToolTarget(sub(spelling), NativeToolTargetOptions{Cwd: cwd, Flavor: PosixPathFlavor, ReadFallbacks: readFallbacks, Exists: existsSync})
				exp := want[spelling]
				got := unsub(n.Target)
				if exp != nil {
					got = unsub(n.Target)
					if got != *exp {
						t.Errorf("%q: got %q, Pi opens %q", spelling, got, *exp)
					}
				}
			}
		}
		tw(t, f, "opens the same file as Pi's read for every spelling", func(t *testing.T) { check(t, true, rec.Read) })
		tw(t, f, "opens the same file as Pi's write/edit/ls/find/grep for every spelling", func(t *testing.T) { check(t, false, rec.Resolve) })
	})
}
