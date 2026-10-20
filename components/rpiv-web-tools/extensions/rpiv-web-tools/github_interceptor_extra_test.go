package rpiv_web_tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// Cases beyond the original's: the exact commands and their limits, the clone cache, and the Node
// behaviours the port reproduces (expected values taken from Node 24).

func TestJSCompat(t *testing.T) {
	t.Run("Buffer base64 decoding", func(t *testing.T) {
		for in, want := range map[string]string{
			"QQ": "A", "+/+/": "\xfb\xff\xbf", "QUI": "AB", "QUJD\nREVG": "ABCDEF", "QQ==QQ==": "A", "Q": "", "QUJD=REVG": "ABC", "  QU JD  ": "ABC",
			"-_-_": "\xfb\xff\xbf",
		} {
			eq(t, string(decodeBase64Lenient(in)), want)
		}
	})
	t.Run("path.extname", func(t *testing.T) {
		for in, want := range map[string]string{"a.png": ".png", ".bashrc": "", "a.": ".", "a/b.c/d": "", "..": "", "..a": ".a", "a.b.c": ".c", "x/.y": "", "file.PNG": ".PNG", "dir.d/": ".d"} {
			eq(t, posixExtname(in), want)
		}
	})
	t.Run("toFixed(1) rounds a tie up", func(t *testing.T) {
		for in, want := range map[int64]string{1: "1 B", 1023: "1023 B", 1024: "1.0 KB", 1075: "1.0 KB", 1126: "1.1 KB", 1280: "1.3 KB", 1539: "1.5 KB", 1100000: "1.0 MB", 1310720: "1.3 MB"} {
			eq(t, formatFileSize(in), want)
		}
	})
	t.Run("Buffer utf-8 decoding replaces each maximal invalid prefix once", func(t *testing.T) {
		for in, want := range map[string]string{"\xe2\x82": "\ufffd", "\xf0\x9f\x98": "\ufffd", "\xc0\x80": "\ufffd\ufffd", "a\xffb": "a\ufffdb", "\xed\xa0\x80": "\ufffd\ufffd\ufffd", "\xe0\x80\x80": "\ufffd\ufffd\ufffd", "\xf0\x80\x80\x80": "\ufffd\ufffd\ufffd\ufffd", "\xf4\x90\x80\x80": "\ufffd\ufffd\ufffd\ufffd", "\xf0\x9f\x98\x80": "😀", "\xe4\xbd\xa0": "你", "ok": "ok", "é": "é"} {
			eq(t, decodeUTF8Lossy([]byte(in)), want)
		}
	})
	t.Run("decodeURIComponent", func(t *testing.T) {
		for in, want := range map[string]string{"%E4%BD%A0": "你", "a%20b": "a b", "plain": "plain"} {
			got, ok := decodeURIComponent(in)
			eq(t, ok, true)
			eq(t, got, want)
		}
		for _, in := range []string{"%FF", "100%", "%zz", "%C0%80", "%e4%bd", "%4"} {
			_, ok := decodeURIComponent(in)
			eq(t, ok, false)
		}
	})
	t.Run("parseInt", func(t *testing.T) {
		for in, want := range map[string]float64{"12345": 12345, "7": 7, "-3x": -3, "20000000UkVB": 20000000, "+5": 5, "1e3": 1, "0x10": 0} {
			got, ok := jsParseInt10(in)
			eq(t, ok, true)
			eq(t, got, want)
		}
		_, ok := jsParseInt10("abc")
		eq(t, ok, false)
	})
	t.Run("UTF-16 lengths and slices", func(t *testing.T) {
		eq(t, utf16Len("a😀b"), 4)
		eq(t, utf16Prefix("a😀b", 3), "a😀")
		eq(t, utf16Prefix("a😀b", 2), "a\ufffd")
		eq(t, utf16Prefix("abc", 10), "abc")
		eq(t, utf16Prefix("abc", 0), "")
	})
	t.Run("UTF-16 length and slice agree with the code unit sequence on mixed strings", func(t *testing.T) {
		for _, s := range []string{"", "abcdefgh", "abcdefghi", strings.Repeat("a", 23) + "é" + strings.Repeat("b", 17) + "😀" + "tail", strings.Repeat("x", 40) + "😀😀" + strings.Repeat("y", 9), "😀" + strings.Repeat("z", 16), "日本語" + strings.Repeat("q", 8)} {
			units := utf16.Encode([]rune(s))
			eq(t, utf16Len(s), len(units))
			for k := 0; k <= len(units)+2; k++ {
				want := string(utf16.Decode(units[:min(k, len(units))]))
				eq(t, utf16Prefix(s, k), want)
			}
		}
	})
	t.Run("default sort order is by UTF-16 code unit", func(t *testing.T) {
		eq(t, compareUTF16("😀", "\ue000"), -1)
		eq(t, compareUTF16("b", "a"), 1)
		eq(t, compareUTF16("a", "a"), 0)
		eq(t, compareUTF16("a", "ab"), -1)
		eq(t, compareUTF16("ab", "a"), 1)
		eq(t, compareUTF16("😀", "😁"), -1)
		eq(t, compareUTF16("😁", "😀"), 1)
	})
}

func TestGitHubURLActions(t *testing.T) {
	eq(t, parseGitHubURL("https://github.com/o/r/Issues"), (*githubURLInfo)(nil))
	eq(t, parseGitHubURL("https://github.com/o/r/foo/bar"), (*githubURLInfo)(nil))
	eq(t, parseGitHubURL("https://github.com/o/r/blob/main").Type, "blob")
}

func TestGitHubURLHosts(t *testing.T) {
	// Hostnames as `new URL(...)` in Node 24 gives them.
	for in, want := range map[string]bool{
		"https://%67ithub.com/o/r":            true,
		"https://\uff47ithub.com/o/r":         true,
		"https://GITHUB.COM:/o/r":             true,
		"https://github.com:abc/o/r":          false,
		"https://u:p@www.GitHub.com:8443/o/r": true,
		"https://github.com./o/r":             false,
		"HTTPS://github.com/o/r":              true,
		"https:github.com/o/r":                true,
		"https:///github.com/o/r":             true,
		"https://[::1]/o/r":                   false,
		"https://127.1/o/r":                   false,
	} {
		if (parseGitHubURL(in) != nil) != want {
			t.Errorf("%q: parsed = %v", in, !want)
		}
	}
}

func TestGitHubURLNormalization(t *testing.T) {
	// Expected paths from `new URL(...).pathname` in Node 24.
	for in, want := range map[string]string{
		"https://github.com/a/%2e%2e/b/./c":      "b/c",
		"https://github.com\\o\\r\\blob\\main":   "o/r/blob/main",
		"https://github.com/o//..//r":            "o/r",
		"https://github.com/o/r/blob/main/a%2Fb": "o/r/blob/main/a/b",
		"  https://GitHub.com/o/r\t":             "o/r",
		"https://github.com:443/o/r?x=1#frag":    "o/r",
		"https://github.com/o/r/blob/main/%FF":   "o/r/blob/main/%FF",
		"https://user@github.com/o/r":            "o/r",
	} {
		_, segs, ok := githubURLParts(in)
		eq(t, ok, true)
		eq(t, strings.Join(segs, "/"), want)
	}
	for _, in := range []string{"ftp://github.com/o/r", "github.com/o/r", "https://github.com.evil.com/o/r", "https://notgithub.com/o/r", "https://github.com@evil.com/o/r"} {
		eq(t, parseGitHubURL(in), (*githubURLInfo)(nil))
	}
	info := parseGitHubURL("https://github.com/o/r/tree/main/%2e%2e/x")
	eq(t, [2]string{info.Ref, info.Path}, [2]string{"x", ""})
	eq(t, parseGitHubURL("https://github.com/o/r/tree/%2e%2e/x"), (*githubURLInfo)(nil))
}

func TestGitHubCommandsAndLimits(t *testing.T) {
	calls := func(fe *fakeExec) [][]string {
		var out [][]string
		for _, c := range fe.all() {
			out = append(out, append([]string{c.Name}, c.Args...))
		}
		return out
	}
	t.Run("gh clone of a ref: the probes, the size check and the clone command", func(t *testing.T) {
		dir := t.TempDir()
		ctx, fe := ghExec(t, func(name string, args []string) (string, error) {
			switch {
			case args[0] == "--version":
				return "gh", nil
			case args[0] == "api":
				return "100", nil
			case args[0] == "repo":
				os.MkdirAll(args[3], 0o755)
				os.WriteFile(filepath.Join(args[3], "a.txt"), []byte("x"), 0o644)
				return "", nil
			}
			return "", errors.New("unexpected")
		})
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		r := ghIntercept(ctx, g, "https://github.com/owner/repo/tree/v1/docs")
		eq(t, r.Title, "owner/repo - docs")
		clone := filepath.Join(dir, "owner", "repo@v1")
		eq(t, calls(fe), [][]string{
			{"gh", "--version"},
			{"gh", "api", "repos/owner/repo", "--jq", ".size"},
			{"gh", "repo", "clone", "owner/repo", clone, "--", "--depth", "1", "--single-branch", "--branch", "v1"},
		})
		got := fe.all()
		eq(t, []time.Duration{got[0].Opts.Timeout, got[1].Opts.Timeout, got[2].Opts.Timeout}, []time.Duration{5 * time.Second, 10 * time.Second, 30 * time.Second})
		eq(t, got[2].Opts.MaxBuffer, 0)
		ghHas(t, r.Text, "Path `docs` not found in clone. Showing repository root instead.")
	})
	t.Run("git clone of the default branch with the configured timeout", func(t *testing.T) {
		dir := t.TempDir()
		ctx, fe := ghExec(t, func(name string, args []string) (string, error) {
			if args[0] == "clone" {
				os.MkdirAll(args[len(args)-1], 0o755)
				return "", nil
			}
			return "", errors.New("no gh")
		})
		g := ghMake(t, githubUserOptions{ClonePath: &dir, CloneTimeoutSeconds: new(7.0)})
		if ghIntercept(ctx, g, "https://github.com/owner/repo.git") == nil {
			t.Fatal("null")
		}
		eq(t, calls(fe), [][]string{
			{"gh", "--version"},
			{"git", "clone", "--depth", "1", "--single-branch", "https://github.com/owner/repo.git", filepath.Join(dir, "owner", "repo")},
		})
		eq(t, fe.all()[1].Opts.Timeout, 7*time.Second)
	})
	t.Run("the API reads carry the original's timeouts and buffers", func(t *testing.T) {
		sha := strings.Repeat("c", 40)
		ctx, fe := ghExec(t, ghAPIMock([2]string{".tree[].path", "a"}, [2]string{"readme", ghB64("r")}, [2]string{".content", ghB64("f")}))
		g := ghMake(t, githubUserOptions{})
		ghIntercept(ctx, g, "https://github.com/o/r/tree/"+sha)
		ghIntercept(ctx, g, "https://github.com/o/r/blob/"+sha+"/src/a.ts")
		type call struct {
			path    string
			timeout time.Duration
			buf     int
		}
		var got []call
		for _, c := range fe.all() {
			if len(c.Args) > 1 && c.Args[0] == "api" {
				got = append(got, call{c.Args[1], c.Opts.Timeout, c.Opts.MaxBuffer})
			}
		}
		want := map[call]bool{
			{"repos/o/r/git/trees/" + sha + "?recursive=1", 15 * time.Second, 5 * 1024 * 1024}: true,
			{"repos/o/r/readme?ref=" + sha, 10 * time.Second, 0}:                               true,
			{"repos/o/r/contents/src/a.ts?ref=" + sha, 10 * time.Second, 2 * 1024 * 1024}:      true,
		}
		eq(t, len(got), 3)
		for _, c := range got {
			if !want[c] {
				t.Fatalf("unexpected call %+v in %+v", c, got)
			}
		}
	})
	t.Run("a commit tree URL uses the API view and never clones or probes the size", func(t *testing.T) {
		sha := strings.Repeat("c", 40)
		ctx, fe := ghExec(t, ghAPIMock([2]string{".tree[].path", "a\nb"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/tree/"+sha+"/lib")
		eq(t, r.Title, "o/r - lib")
		eq(t, r.Text, "Note: Commit SHA URLs use the GitHub API instead of cloning.\n\n## Structure\na\nb\n\nThis is an API-only view. Clone the repo or use `read`/`bash` for deeper exploration.")
		eq(t, fe.count(func(c execCall) bool {
			return c.Args[0] == "repo" || c.Name == "git" || c.Args[len(c.Args)-1] == ".size"
		}), 0)
	})
	t.Run("an oversized repository gets the exact note, rounded to whole MB", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".size", "400000"}, [2]string{".default_branch", "main"}, [2]string{".tree[].path", "f"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r")
		eq(t, strings.SplitN(r.Text, "\n", 2)[0], "Note: Repository is 391MB (threshold: 350MB). Showing API-fetched content instead of full clone. Ask the user if they'd like to clone the full repo — if yes, call web_fetch again with the same URL.")
	})
	t.Run("a size equal to the threshold is cloned", func(t *testing.T) {
		dir := t.TempDir()
		ctx, fe := ghExec(t, func(name string, args []string) (string, error) {
			switch {
			case args[0] == "--version":
				return "gh", nil
			case args[0] == "api":
				return "1024", nil // 1 MB
			case args[0] == "repo":
				os.MkdirAll(args[3], 0o755)
				return "", nil
			}
			return "", errors.New("unexpected")
		})
		g := ghMake(t, githubUserOptions{ClonePath: &dir, MaxRepoSizeMB: new(1.0)})
		ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r").Text, "Repository cloned to:")
		eq(t, fe.count(func(c execCall) bool { return c.Args[0] == "repo" }), 1)
	})
	t.Run("a tree of more than 200 paths is cut with the total", func(t *testing.T) {
		var paths []string
		for i := range 250 {
			paths = append(paths, "p"+pad3(i))
		}
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{".tree[].path", strings.Join(paths, "\n") + "\n"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/tree/main")
		ghHas(t, r.Text, "p199\n... (250 total entries)\n")
		ghHasNot(t, r.Text, "p200")
	})
	t.Run("a README of 9000 UTF-16 units is cut at 8192 units, not at runes", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{"readme", ghB64(strings.Repeat("😀", 4500))}, [2]string{".tree", ""}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r")
		ghHas(t, r.Text, "## README.md\n"+strings.Repeat("😀", 4096)+"\n\n[README truncated at 8K chars]\n")
	})
	t.Run("an empty file is not a result", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".default_branch", "main"}, [2]string{".content", ""}))
		eq(t, ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/blob/main/x"), (*fetchResponse)(nil))
	})
	t.Run("a gh output past the buffer is a failure", func(t *testing.T) {
		g := ghMake(t, githubUserOptions{})
		ctx := withExec(context.Background(), func(_ context.Context, name string, args []string, o execOptions) (string, error) {
			return "", errors.New("maxBuffer")
		})
		_, ok := g.fetchFileViaAPI(ctx, "o", "r", "x", "main")
		eq(t, ok, false)
	})
}

func TestGitHubCloneCache(t *testing.T) {
	cloning := func(dir string, release <-chan struct{}) func(string, []string) (string, error) {
		return func(name string, args []string) (string, error) {
			switch {
			case args[0] == "--version":
				return "", errors.New("no gh")
			case args[0] == "clone":
				if release != nil {
					<-release
				}
				os.MkdirAll(args[len(args)-1], 0o755)
				os.WriteFile(filepath.Join(args[len(args)-1], "f.txt"), []byte("x"), 0o644)
				return "", nil
			}
			return "", errors.New("unexpected")
		}
	}
	isClone := func(c execCall) bool { return c.Name == "git" }
	t.Run("a second request for the same repository and ref reuses the clone", func(t *testing.T) {
		dir := t.TempDir()
		ctx, fe := ghExec(t, cloning(dir, nil))
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		a := ghIntercept(ctx, g, "https://github.com/o/r")
		b := ghIntercept(ctx, g, "https://github.com/o/r/blob/main/f.txt")
		c := ghIntercept(ctx, g, "https://github.com/o/r/blob/main/f.txt")
		eq(t, fe.count(isClone), 2) // the root and ref main are different keys
		ghHas(t, a.Text, "f.txt")
		eq(t, b.Text, c.Text)
		eq(t, sortedStrings(g.cacheKeys()), []string{"o/r", "o/r@main"})
	})
	t.Run("a failed clone removes the directory and the cache entry, and the next request tries again", func(t *testing.T) {
		dir := t.TempDir()
		ctx, fe := ghExec(t, func(name string, args []string) (string, error) {
			if args[0] == "clone" {
				os.MkdirAll(args[len(args)-1], 0o755)
				return "", errors.New("fatal")
			}
			return "", errors.New("no gh")
		})
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		eq(t, ghIntercept(ctx, g, "https://github.com/o/r"), (*fetchResponse)(nil))
		if _, err := os.Stat(filepath.Join(dir, "o", "r")); !os.IsNotExist(err) {
			t.Fatal("the failed clone's directory is still there")
		}
		eq(t, len(g.cacheKeys()), 0)
		ghIntercept(ctx, g, "https://github.com/o/r")
		eq(t, fe.count(isClone), 2)
	})
	t.Run("the install hint is written once, to the hint writer", func(t *testing.T) {
		dir := t.TempDir()
		var sb strings.Builder
		ctx, _ := ghExec(t, cloning(dir, nil))
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		ghHintWriter = &sb
		defer func() { ghHintWriter = discardWriter{} }()
		ghIntercept(ctx, g, "https://github.com/o/a")
		ghIntercept(ctx, g, "https://github.com/o/b")
		eq(t, strings.Count(sb.String(), "Install `gh` CLI"), 1)
		g.reset()
		ghIntercept(ctx, g, "https://github.com/o/c")
		eq(t, strings.Count(sb.String(), "Install `gh` CLI"), 2)
	})
	t.Run("concurrent requests for one repository run one clone and wait for it", func(t *testing.T) {
		dir := t.TempDir()
		release := make(chan struct{})
		ctx, fe := ghExec(t, cloning(dir, release))
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		var wg sync.WaitGroup
		res := make([]*fetchResponse, 3)
		for i := range res {
			wg.Add(1)
			go func() { defer wg.Done(); res[i] = ghIntercept(ctx, g, "https://github.com/o/r") }()
		}
		for fe.count(isClone) == 0 {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
		close(release)
		wg.Wait()
		eq(t, fe.count(isClone), 1)
		for _, r := range res {
			if r == nil {
				t.Fatal("a waiting request got null")
			}
			eq(t, r.Text, res[0].Text)
		}
	})
	t.Run("a waiting request that is cancelled returns null while the clone goes on", func(t *testing.T) {
		dir := t.TempDir()
		release := make(chan struct{})
		ctx, fe := ghExec(t, cloning(dir, release))
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		done := make(chan *fetchResponse)
		go func() { done <- ghIntercept(ctx, g, "https://github.com/o/r") }()
		for fe.count(isClone) == 0 {
			time.Sleep(time.Millisecond)
		}
		waiter, cancel := context.WithCancel(ctx)
		out := make(chan *fetchResponse)
		go func() { out <- ghIntercept(waiter, g, "https://github.com/o/r") }()
		time.Sleep(20 * time.Millisecond)
		cancel()
		eq(t, <-out, (*fetchResponse)(nil))
		close(release)
		if <-done == nil {
			t.Fatal("the first request lost its clone")
		}
	})
	t.Run("a probe that failed only because the request was cancelled is not remembered", func(t *testing.T) {
		g := ghMake(t, githubUserOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		fe := &fakeExec{fn: func(string, []string) (string, error) { cancel(); return "", errors.New("killed") }}
		eq(t, g.checkGhAvailable(withExec(ctx, fe.run)), false)
		ok, _ := ghExec(t, ghMakeAvailableAPIsFail)
		eq(t, g.checkGhAvailable(ok), true)
	})
	t.Run("without a runner every command fails", func(t *testing.T) {
		g := ghMake(t, githubUserOptions{})
		eq(t, g.checkGhAvailable(context.Background()), false)
		eq(t, ghIntercept(context.Background(), g, "https://github.com/o/r/blob/main/x"), (*fetchResponse)(nil))
	})
	t.Run("reset removes the clone directories", func(t *testing.T) {
		dir := t.TempDir()
		ctx, _ := ghExec(t, cloning(dir, nil))
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		ghIntercept(ctx, g, "https://github.com/o/r")
		g.reset()
		if _, err := os.Stat(filepath.Join(dir, "o", "r")); !os.IsNotExist(err) {
			t.Fatal("the clone is still there")
		}
		eq(t, len(g.cacheKeys()), 0)
	})
}

func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestGitHubCloneRendering(t *testing.T) {
	setup := func(t *testing.T, key string, files map[string]string) (context.Context, *githubInterceptor, string) {
		t.Helper()
		dir := t.TempDir()
		for name, content := range files {
			p := filepath.Join(dir, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if strings.HasSuffix(name, "/") {
				os.MkdirAll(p, 0o755)
				continue
			}
			os.WriteFile(p, []byte(content), 0o644)
		}
		ctx, _ := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{})
		g.seedCache(key, dir, ghResolved(dir))
		return ctx, g, dir
	}
	hint := "Use `read` and `bash` tools at the path above to explore further."
	t.Run("root: the exact text, .git hidden, directories before their children, a lone README", func(t *testing.T) {
		ctx, g, dir := setup(t, "o/r", map[string]string{"b.txt": "", "a/x.txt": "", ".git/config": "", "README.md": "hi"})
		r := ghIntercept(ctx, g, "https://github.com/o/r")
		eq(t, r.Text, "Repository cloned to: "+dir+"\n\n## Structure\nREADME.md\na/\na/x.txt\nb.txt\n\n## README.md\nhi\n\n"+hint)
	})
	t.Run("root: names sort by UTF-16 code unit", func(t *testing.T) {
		ctx, g, _ := setup(t, "o/r", map[string]string{"b": "", "a": "", "\ue000": "", "😀": "", "é": ""})
		r := ghIntercept(ctx, g, "https://github.com/o/r")
		ghHas(t, r.Text, "## Structure\na\nb\né\n😀\n\ue000\n")
	})
	t.Run("root: exactly 200 entries still report the cut", func(t *testing.T) {
		files := map[string]string{}
		for i := range 200 {
			files["f"+pad3(i)] = ""
		}
		ctx, g, _ := setup(t, "o/r", files)
		ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r").Text, "f199\n... (truncated at 200 entries)\n")
	})
	t.Run("root: an empty README is left out and the next candidate is not read", func(t *testing.T) {
		ctx, g, _ := setup(t, "o/r", map[string]string{"README.md": "", "README": "second"})
		ghHasNot(t, ghIntercept(ctx, g, "https://github.com/o/r").Text, "## README.md")
	})
	t.Run("tree: a listing with sizes and slashes", func(t *testing.T) {
		ctx, g, dir := setup(t, "o/r@main", map[string]string{"d/a.ts": "x", "d/sub/": "", "d/big.bin": strings.Repeat("x", 1536)})
		r := ghIntercept(ctx, g, "https://github.com/o/r/tree/main/d")
		eq(t, r.Text, "Repository cloned to: "+dir+"\n\n## d\n  a.ts  (1 B)\n  big.bin  (1.5 KB)\n  sub/\n\n"+hint)
		eq(t, r.Title, "o/r - d")
	})
	t.Run("tree: the root of a ref lists with the slash title", func(t *testing.T) {
		ctx, g, dir := setup(t, "o/r@main", map[string]string{"a.ts": "x"})
		eq(t, ghIntercept(ctx, g, "https://github.com/o/r/tree/main").Text, "Repository cloned to: "+dir+"\n\n## /\n  a.ts  (1 B)\n\n"+hint)
	})
	t.Run("blob: the exact text of a file", func(t *testing.T) {
		ctx, g, dir := setup(t, "o/r@main", map[string]string{"f.ts": "line"})
		eq(t, ghIntercept(ctx, g, "https://github.com/o/r/blob/main/f.ts").Text, "Repository cloned to: "+dir+"\n\n## f.ts\nline\n\n"+hint)
	})
	t.Run("blob: a file over 100K UTF-16 units is cut with the full path", func(t *testing.T) {
		ctx, g, dir := setup(t, "o/r@main", map[string]string{"big.ts": strings.Repeat("😀", 50001)})
		r := ghIntercept(ctx, g, "https://github.com/o/r/blob/main/big.ts")
		ghHas(t, r.Text, "## big.ts\n"+strings.Repeat("😀", 50000)+"\n\n[File truncated at 100K chars. Full file: "+filepath.Join(dir, "big.ts")+"]\n\n"+hint)
	})
	t.Run("blob: the exact binary message", func(t *testing.T) {
		ctx, g, dir := setup(t, "o/r@main", map[string]string{"a.PNG": "x", "n": "a\x00b"})
		eq(t, ghIntercept(ctx, g, "https://github.com/o/r/blob/main/a.PNG").Text, "Repository cloned to: "+dir+"\n\n## a.PNG\nBinary file (PNG, 1 B). Use `read` or `bash` tools at the path above to inspect.")
		eq(t, ghIntercept(ctx, g, "https://github.com/o/r/blob/main/n").Text, "Repository cloned to: "+dir+"\n\n## n\nBinary file (, 3 B). Use `read` or `bash` tools at the path above to inspect.")
	})
	t.Run("blob: a path that climbs out of the clone is treated as missing", func(t *testing.T) {
		ctx, g, _ := setup(t, "o/r@main", map[string]string{"a.ts": ""})
		r := ghIntercept(ctx, g, "https://github.com/o/r/blob/main/x%2F..%2F..%2Fetc%2Fpasswd")
		ghHas(t, r.Text, "Path `x/../../etc/passwd` not found in clone. Showing repository root instead.\n\n## Structure\na.ts\n\n"+hint)
		r = ghIntercept(ctx, g, "https://github.com/o/r/blob/main/%2Fetc%2Fpasswd")
		ghHas(t, r.Text, "not found in clone")
	})
	t.Run("blob: an absolute path is resolved as an absolute path, not under the clone", func(t *testing.T) {
		ctx, g, _ := setup(t, "o/r@main", map[string]string{"etc/passwd": "inside"})
		ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r/blob/main/%2Fetc%2Fpasswd").Text, "not found in clone")
	})
	t.Run("blob: a file with invalid UTF-8 is shown with replacement characters", func(t *testing.T) {
		ctx, g, _ := setup(t, "o/r@main", map[string]string{"f.txt": "a\xe2\x82b"})
		ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r/blob/main/f.txt").Text, "## f.txt\na\ufffdb\n")
	})
	t.Run("a repository whose clone left nothing renders an empty structure", func(t *testing.T) {
		ctx, g, _ := setup(t, "o/r", nil)
		os.RemoveAll(g.cache["o/r"].localPath)
		r := ghIntercept(ctx, g, "https://github.com/o/r")
		eq(t, strings.HasSuffix(r.Text, "\n\n## Structure\n\n\n"+hint), true)
	})
}

func TestGitHubMoreCases(t *testing.T) {
	t.Run("the size and default branch reads allow 10 seconds and the branch is trimmed", func(t *testing.T) {
		ctx, fe := ghExec(t, ghAPIMock([2]string{".size", "400000\n"}, [2]string{".default_branch", "main\n"}, [2]string{".tree[].path", "f"}))
		ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r")
		got := map[string]time.Duration{}
		for _, c := range fe.all() {
			if len(c.Args) > 3 {
				got[c.Args[1]+"|"+c.Args[3]] = c.Opts.Timeout
			}
		}
		eq(t, got["repos/o/r|.size"], 10*time.Second)
		eq(t, got["repos/o/r|.default_branch"], 10*time.Second)
		eq(t, got["repos/o/r/git/trees/main?recursive=1|.tree[].path"], 15*time.Second)
	})
	t.Run("a gh that is not available gets no api call", func(t *testing.T) {
		ctx, fe := ghExec(t, func(name string, args []string) (string, error) {
			if args[0] == "--version" {
				return "", errors.New("not found")
			}
			return "100", nil
		})
		_, ok := ghMake(t, githubUserOptions{}).checkRepoSize(ctx, "o", "r")
		eq(t, ok, false)
		eq(t, len(fe.all()), 1)
	})
	t.Run("a size with a trailing newline is read", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".size", "12345\n"}))
		size, ok := ghMake(t, githubUserOptions{}).checkRepoSize(ctx, "o", "r")
		eq(t, [2]any{size, ok}, [2]any{12345.0, true})
	})
	t.Run("a tree of exactly 200 paths has no total line", func(t *testing.T) {
		var paths []string
		for i := range 200 {
			paths = append(paths, "p"+pad3(i))
		}
		ctx, _ := ghExec(t, ghAPIMock([2]string{".tree[].path", strings.Join(paths, "\n")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/tree/main")
		ghHas(t, r.Text, "p199\n\nThis is an API-only view")
	})
	t.Run("blank lines of the tree are no entries, whether or not the tree is cut", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".tree[].path", "a\n\nb\n\n\nc"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/tree/main")
		ghHas(t, r.Text, "## Structure\na\nb\nc\n\nThis is an API-only view")
		var paths []string
		for i := range 201 {
			paths = append(paths, "p"+pad3(i))
		}
		ctx, _ = ghExec(t, ghAPIMock([2]string{".tree[].path", strings.Join(paths, "\n\n")}))
		r = ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/tree/main")
		ghHas(t, r.Text, "p199\n... (201 total entries)\n")
		ghHasNot(t, r.Text, "p200")
	})
	t.Run("a blob URL without a path shows the tree view", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{".tree[].path", "a"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/blob/main")
		eq(t, r.Title, "o/r")
		ghHas(t, r.Text, "## Structure\na\n")
	})
	t.Run("a README without a tree is shown alone", func(t *testing.T) {
		ctx, _ := ghExec(t, ghAPIMock([2]string{"readme", ghB64("only")}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/tree/main")
		eq(t, r.Text, "## README.md\nonly\n\nThis is an API-only view. Clone the repo or use `read`/`bash` for deeper exploration.")
	})
	t.Run("contents arrive as base64 in lines of 60 characters", func(t *testing.T) {
		body := strings.Repeat("0123456789", 20)
		enc := ghB64(body)
		var wrapped []string
		for len(enc) > 60 {
			wrapped = append(wrapped, enc[:60])
			enc = enc[60:]
		}
		wrapped = append(wrapped, enc)
		ctx, _ := ghExec(t, ghAPIMock([2]string{".content", strings.Join(wrapped, "\n") + "\n"}))
		r := ghIntercept(ctx, ghMake(t, githubUserOptions{}), "https://github.com/o/r/blob/main/f")
		eq(t, r.Text, "## f\n"+body)
	})
	t.Run("git clones a ref with --branch", func(t *testing.T) {
		dir := t.TempDir()
		ctx, fe := ghExec(t, func(name string, args []string) (string, error) {
			if args[0] == "clone" {
				os.MkdirAll(args[len(args)-1], 0o755)
				return "", nil
			}
			return "", errors.New("no gh")
		})
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		ghIntercept(ctx, g, "https://github.com/o/r/blob/feat/x.go")
		eq(t, fe.all()[1].Args, []string{"clone", "--depth", "1", "--single-branch", "--branch", "feat", "https://github.com/o/r.git", filepath.Join(dir, "o", "r@feat")})
	})
	t.Run("a stale directory is removed before the clone", func(t *testing.T) {
		dir := t.TempDir()
		stale := filepath.Join(dir, "o", "r")
		os.MkdirAll(stale, 0o755)
		os.WriteFile(filepath.Join(stale, "old.txt"), nil, 0o644)
		ctx, _ := ghExec(t, func(name string, args []string) (string, error) {
			if args[0] == "clone" {
				if _, err := os.Stat(filepath.Join(stale, "old.txt")); err == nil {
					return "", errors.New("the stale file was still there")
				}
				os.MkdirAll(args[len(args)-1], 0o755)
				return "", nil
			}
			return "", errors.New("no gh")
		})
		g := ghMake(t, githubUserOptions{ClonePath: &dir})
		if ghIntercept(ctx, g, "https://github.com/o/r") == nil {
			t.Fatal("the clone failed")
		}
	})
	t.Run("the options of the config object reach the interceptor", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"interceptors": obj{"github": obj{"maxRepoSizeMB": 12.0, "cloneTimeoutSeconds": 3.0, "clonePath": "/srv/c"}}})
		t.Cleanup(resetWebToolsInterceptors)
		buildInterceptors(nil)
		eq(t, getActiveGitHubInterceptor().options, resolvedGitHubOptions{Enabled: true, MaxRepoSizeMB: 12, CloneTimeoutSeconds: 3, ClonePath: "/srv/c"})
	})
	t.Run("the config reader keeps an unset stanza unset and an enabled object enabled", func(t *testing.T) {
		eq(t, readUserGitHubConfig(webToolsConfig{}), nil)
		eq(t, readUserGitHubConfig(webToolsConfig{"interceptors": obj{}}), nil)
		eq(t, readUserGitHubConfig(webToolsConfig{"interceptors": obj{"github": obj{"enabled": false}}}), githubUserOptions{Enabled: new(false)})
	})
}

func TestGitHubCloneListings(t *testing.T) {
	seed := func(t *testing.T, key string) (context.Context, *githubInterceptor, string) {
		t.Helper()
		dir := t.TempDir()
		ctx, _ := ghExec(t, ghMakeUnavailable)
		g := ghMake(t, githubUserOptions{})
		g.seedCache(key, dir, ghResolved(dir))
		return ctx, g, dir
	}
	t.Run("a symlink out of the clone is listed as outside, and a broken one as unreadable", func(t *testing.T) {
		ctx, g, dir := seed(t, "o/r@main")
		if err := os.Symlink(os.TempDir(), filepath.Join(dir, "escape")); err != nil {
			t.Skip("symlinks: " + err.Error())
		}
		os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "broken"))
		r := ghIntercept(ctx, g, "https://github.com/o/r/tree/main")
		ghHas(t, r.Text, "## /\n  broken  (unreadable)\n  escape  (outside repo)")
	})
	t.Run("a symlink that stays inside the clone is followed and one deeper down that leaves is skipped", func(t *testing.T) {
		ctx, g, dir := seed(t, "o/r")
		os.MkdirAll(filepath.Join(dir, "real"), 0o755)
		os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
		os.WriteFile(filepath.Join(dir, "real", "x.txt"), nil, 0o644)
		if err := os.Symlink("real", filepath.Join(dir, "link")); err != nil {
			t.Skip("symlinks: " + err.Error())
		}
		os.Symlink(os.TempDir(), filepath.Join(dir, "sub", "out"))
		r := ghIntercept(ctx, g, "https://github.com/o/r")
		ghHas(t, r.Text, "## Structure\nlink/\nlink/x.txt\nreal/\nreal/x.txt\nsub/\nsub/out  [outside repo skipped]\n")
	})
	t.Run("a tree URL naming a file lists nothing readable", func(t *testing.T) {
		ctx, g, dir := seed(t, "o/r@main")
		os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
		ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r/tree/main/f.txt").Text, "## f.txt\n(directory not readable)\n")
	})
	t.Run("README candidates are tried in the original's order", func(t *testing.T) {
		for _, name := range []string{"README.txt", "README.rst"} {
			ctx, g, dir := seed(t, "o/r")
			os.WriteFile(filepath.Join(dir, name), []byte("from "+name), 0o644)
			ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r").Text, "## README.md\nfrom "+name+"\n")
		}
	})
	t.Run("a README directory is skipped for the next candidate", func(t *testing.T) {
		ctx, g, dir := seed(t, "o/r")
		os.MkdirAll(filepath.Join(dir, "README.md"), 0o755)
		os.WriteFile(filepath.Join(dir, "README"), []byte("plain"), 0o644)
		ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r").Text, "## README.md\nplain\n")
	})
	t.Run("the other noise directories are skipped", func(t *testing.T) {
		ctx, g, dir := seed(t, "o/r")
		for _, n := range []string{"vendor", ".next", "dist", "build", "__pycache__", ".venv", "venv", ".tox", ".mypy_cache", ".pytest_cache", "target", ".gradle", ".idea", ".vscode"} {
			os.MkdirAll(filepath.Join(dir, n), 0o755)
			os.WriteFile(filepath.Join(dir, n, "in.txt"), nil, 0o644)
		}
		r := ghIntercept(ctx, g, "https://github.com/o/r")
		ghHasNot(t, r.Text, "in.txt")
		eq(t, strings.Count(r.Text, "/  [skipped]"), 14)
	})
	t.Run("every binary extension of the original is binary", func(t *testing.T) {
		for ext := range githubBinaryExtensions {
			ctx, g, dir := seed(t, "o/r@main")
			os.WriteFile(filepath.Join(dir, "f"+ext), []byte("text"), 0o644)
			ghHas(t, ghIntercept(ctx, g, "https://github.com/o/r/blob/main/f"+ext).Text, "Binary file ("+ext[1:]+", 4 B)")
		}
		eq(t, len(githubBinaryExtensions), 61)
	})
}
