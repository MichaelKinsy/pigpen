package pi_permission_system

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// The differential of BashProgram: testdata/bashprogram-oracle.jsonl.gz is what the original's BashProgram.parse answered (command
// units, external accesses, path-rule candidates) for every input of the bash analysis differential plus the path-bearing commands
// of port/bashprogram-diff/gen-paths.mjs, under six path configurations, recorded by port/bashprogram-diff/run.sh. Each record is
// compared with this package's answer for the same input. The filesystem is the fixture of buildBashProgramFixture (the probe builds
// the same tree); outside it the fs answers as a host with no symlinks and no files, so the recording is host independent.

// bashProgramFixture is the tree the probe reads: root is the project cwd of the "root" configurations and out an outside directory.
func buildBashProgramFixture(t testing.TB) (base, root, out string) {
	t.Helper()
	base, err := os.MkdirTemp("", "pbp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	// The root sits 16 directories below the base, so no `..` chain of the corpus climbs out of the base (whose own ancestors
	// differ from host to host).
	deep := base
	for range 16 {
		deep = filepath.Join(deep, "d")
	}
	root, out = filepath.Join(deep, "r"), filepath.Join(base, "o")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(deep, 0o755))
	must(os.Mkdir(root, 0o755))
	must(os.Mkdir(out, 0o755))
	for _, f := range []string{"id_rsa", "inside.txt", ".env", ".some.secret"} {
		must(os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644))
	}
	for _, d := range []string{"src", "nested", "nested/sub", "a", "a/b", "packages", "packages/sub", "docs"} {
		must(os.Mkdir(filepath.Join(root, d), 0o755))
	}
	for _, f := range []string{"foo.ts", "index.ts", "main.ts", "file.txt", "secret.env"} {
		must(os.WriteFile(filepath.Join(root, "src", f), []byte("x"), 0o644))
	}
	must(os.WriteFile(filepath.Join(root, "nested", "inner.txt"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(out, "secret"), []byte("s"), 0o644))
	must(os.WriteFile(filepath.Join(out, "other"), []byte("s"), 0o644))
	must(os.Mkdir(filepath.Join(out, "sub"), 0o755))
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	link(filepath.Join(root, ".some.secret"), "a_sym")
	link(filepath.Join(out, "secret"), "outside-link")
	link(out, "vault")
	link(filepath.Join(root, "gone"), "dangling")
	link(filepath.Join(out, "secret"), "v1..v2")
	link(out, "link")
	link(filepath.Join(root, "src"), "inlink")
	return base, root, out
}

func bashCommandJSON(c BashCommand) map[string]any {
	m := map[string]any{"text": c.Text}
	if c.Context != "" {
		m["context"] = string(c.Context)
	}
	if c.WrapperKind != "" {
		m["wrapperKind"] = string(c.WrapperKind)
	}
	if c.ExecutedUnit != "" {
		m["executedUnit"] = c.ExecutedUnit
	}
	if c.FloorExemption != "" {
		m["floorExemption"] = string(c.FloorExemption)
	}
	if c.ParseUnresolved {
		m["parseUnresolved"] = true
	}
	if c.Salvaged {
		m["salvaged"] = true
	}
	if c.Spellings != nil {
		m["spellings"] = c.Spellings
	}
	return m
}

type programConfig struct {
	name string
	// placed: `@R@` and `@O@` in the input are the fixture paths (the configurations whose cwd is the fixture root); the others
	// read them as literal text.
	placed     bool
	normalizer *PathNormalizer
	workdir    *string
}

// programView is the probe's record of one program under one configuration.
func programView(program *BashProgram, withCommands bool) map[string]any {
	ext := []any{}
	for _, e := range program.ExternalAccesses() {
		ext = append(ext, map[string]any{"p": e.Path.Value(), "b": e.Path.BoundaryValue(), "e": string(e.Effect.Effect), "s": string(e.Effect.Source)})
	}
	rules := []any{}
	for _, c := range program.PathRuleCandidates() {
		rules = append(rules, map[string]any{
			"t": c.Token, "p": c.Path.Value(), "b": c.Path.BoundaryValue(), "m": c.Path.MatchValues(),
			"e": string(c.Effect.Effect), "s": string(c.Effect.Source),
		})
	}
	view := map[string]any{"ext": ext, "rules": rules}
	if withCommands {
		cmds := []any{}
		for _, c := range program.Commands() {
			cmds = append(cmds, bashCommandJSON(c))
		}
		view["cmds"] = cmds
	}
	return view
}

// withFixtureFS replaces the realpath and lstat hooks with the probe's: the real answers under the fixture, a host with no symlinks
// and no files anywhere else.
func withFixtureFS(t testing.TB, root, out string) {
	t.Helper()
	inFixture := func(p string) bool {
		return p == root || strings.HasPrefix(p, root+"/") || p == out || strings.HasPrefix(p, out+"/")
	}
	oldReal, oldLstat := realpathSync, lstatSync
	realpathSync = func(p string) (string, error) {
		if inFixture(p) {
			return oldReal(p)
		}
		return p, nil
	}
	lstatSync = func(p string) bool { return inFixture(p) && oldLstat(p) }
	t.Cleanup(func() { realpathSync, lstatSync = oldReal, oldLstat })
}

func TestBashProgramDifferential(t *testing.T) {
	bashparse.WarmBashParser()
	// A home that exists on no host, as in the recording.
	t.Setenv("HOME", "/probe-home/user")
	base, root, out := buildBashProgramFixture(t)
	withFixtureFS(t, root, out)

	const fakeCwd, winCwd = "/projects/my-app", `C:\Projects\App`
	configs := []programConfig{
		{"root", true, NewPathNormalizer(PosixPathFlavor, root), nil},
		{"fake", false, NewPathNormalizer(PosixPathFlavor, fakeCwd), nil},
		{"win", false, NewPathNormalizer(Win32PathFlavor, winCwd), nil},
		{"rootwd", true, NewPathNormalizer(PosixPathFlavor, root), ptr(root + "/nested")},
		{"fakewd", false, NewPathNormalizer(PosixPathFlavor, fakeCwd), ptr("/etc")},
		{"winwd", false, NewPathNormalizer(Win32PathFlavor, winCwd), ptr("/c/work")},
	}
	place := strings.NewReplacer("@R@", root, "@O@", out)
	unplace := strings.NewReplacer(root, "@R@", out, "@O@", base, "@B@")

	f, err := os.Open("testdata/bashprogram-oracle.jsonl.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<28)

	type job struct{ line []byte }
	jobs := make(chan job, 256)
	var mu sync.Mutex
	total, bad := 0, 0
	fields := map[string]int{}
	examples := map[string][]string{}
	record := func(input, field string, want, got any) {
		mu.Lock()
		defer mu.Unlock()
		bad++
		fields[field]++
		if len(examples[field]) < 4 {
			wb, _ := json.Marshal(want)
			gb, _ := json.Marshal(got)
			examples[field] = append(examples[field], fmt.Sprintf("%q field %s\n  original: %s\n  go:       %s", input, field, wb, gb))
		}
	}
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				var want map[string]any
				if err := json.Unmarshal(j.line, &want); err != nil {
					t.Error(err)
					return
				}
				input := want["s"].(string)
				got := map[string]any{}
				for _, c := range configs {
					command := input
					if c.placed {
						command = place.Replace(input)
					}
					program, err := ParseBashProgram(command, c.normalizer, &BashProgramOptions{Workdir: c.workdir})
					if err != nil {
						got[c.name] = map[string]any{"throw": err.Error()}
						continue
					}
					got[c.name] = programView(program, c.name == "root")
				}
				b, _ := json.Marshal(got)
				var norm map[string]any
				if err := json.Unmarshal([]byte(unplace.Replace(string(b))), &norm); err != nil {
					t.Error(err)
					return
				}
				for _, c := range configs {
					if !reflect.DeepEqual(norm[c.name], want[c.name]) {
						record(input, c.name, want[c.name], norm[c.name])
					}
				}
			}
		}()
	}
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		total++
		jobs <- job{line}
	}
	close(jobs)
	wg.Wait()
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if total < bashProgramMinInputs {
		t.Fatalf("only %d recorded inputs", total)
	}
	t.Logf("%d inputs compared under %d configurations, %d differences (fields %v)", total, len(configs), bad, fields)
	if bad > 0 {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var shown []string
		for _, k := range keys {
			shown = append(shown, examples[k]...)
		}
		t.Fatalf("%d unexplained differences:\n%s", bad, strings.Join(shown, "\n"))
	}
}

// bashProgramMinInputs is the size of the recording; a shorter file is a truncated one.
const bashProgramMinInputs = 80000
