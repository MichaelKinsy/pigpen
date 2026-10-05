package eq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Mutation is one deliberate defect. Find must occur exactly once in File.
type Mutation struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// MutationResult reports whether the scenarios noticed the defect.
type MutationResult struct {
	Mutation Mutation
	Killed   bool   // a scenario diverged from the golden trace
	Detail   string // the divergence, or why the mutant could not run
	Invalid  bool   // the mutant did not build or run, so it proves nothing
}

// LoadMutations reads a mutation list.
func LoadMutations(path string) ([]Mutation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []Mutation
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, m := range list {
		if m.Name == "" || m.File == "" || m.Find == "" || m.Find == m.Replace {
			return nil, fmt.Errorf("%s: mutation %q needs a name, a file, and a find text that differs from replace", path, m.Name)
		}
		if seen[m.Name] {
			return nil, fmt.Errorf("%s: duplicate mutation %q", path, m.Name)
		}
		seen[m.Name] = true
	}
	return list, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// NewUnitTest returns the unit-test runner of a mutation run. Each `go test` is bounded: a mutant
// that removes a cancellation or a timeout would otherwise hang for go test's default ten minutes.
func NewUnitTest(sdkDir string) *UnitTest {
	return &UnitTest{SDKDir: sdkDir, Args: []string{"test", "-count=1", "-timeout=180s", "./..."}}
}

// UnitTest runs the port's own Go tests (the fake-host layer) on a mutant. A
// mutant is killed by a scenario or by these tests: some branches, such as
// hasUI == false, cannot be reached through RPC.
type UnitTest struct {
	SDKDir string   // PiG's staged SDK, mapped into a temporary go.work
	Args   []string // arguments after `go`, for example ["test", "-count=1", "./..."]
}

func (u UnitTest) run(dir string, siblings []string) (failed bool, detail string, err error) {
	work, err := os.MkdirTemp("", "eq-gowork")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(work)
	goWork := filepath.Join(work, "go.work")
	all := append([]string{dir}, siblings...)
	if err := os.WriteFile(goWork, []byte(GoWork(u.SDKDir, all)), 0o644); err != nil {
		return false, "", err
	}
	// The port's own tests, then those of every sibling module its go.work uses (the shared
	// library carries most of the game and sprite tests).
	var firstFailure string
	for _, moduleDir := range all {
		cmd := exec.Command("go", u.Args...)
		cmd.Dir = moduleDir
		marker := newRunMarker()
		cmd.Env = append(os.Environ(), "GOWORK="+goWork, "GOFLAGS=", runMarkerVar+"="+marker)
		out, runErr := cmd.CombinedOutput()
		// The tests are over. A mutant that broke a worker's shutdown (its escalation to KILL, its
		// cancellation) left processes behind; they must not outlive the run.
		killRun(marker)
		if runErr == nil {
			continue
		}
		text := string(out)
		if strings.Contains(text, "build failed") || strings.Contains(text, "[setup failed]") || strings.Contains(text, "cannot find") {
			return false, "", fmt.Errorf("mutant does not build: %s", firstLine(text))
		}
		// Only a test failure the go command reports is a kill. A non-zero exit with no test
		// output (a version-manager shim that cannot start the toolchain, a module that cannot
		// load, a missing SDK) means the tests never ran and says nothing about the mutant.
		detail = ""
		for _, line := range strings.Split(text, "\n") {
			switch {
			case strings.HasPrefix(line, "--- FAIL"):
				if detail == "" || !strings.HasPrefix(detail, "--- FAIL") {
					detail = strings.TrimSpace(line)
				}
			case strings.HasPrefix(line, "panic:") && detail == "":
				detail = strings.TrimSpace(line)
			case strings.HasPrefix(line, "FAIL\t") && detail == "":
				detail = strings.TrimSpace(line)
			}
		}
		if detail == "" {
			return false, "", fmt.Errorf("the go command did not run the tests: %s", firstLine(text))
		}
		firstFailure = detail
		break
	}
	if firstFailure != "" {
		return true, firstFailure, nil
	}
	return false, "", nil
}

// baseline runs the unit tests on an unmutated copy of the port. A mutant can be judged only if
// the same run passes without the defect.
func (u UnitTest) baseline(port string) error {
	dir, siblings, cleanup, err := prepareMutantTree(port)
	if err != nil {
		return err
	}
	defer cleanup()
	failed, detail, err := u.run(dir, siblings)
	switch {
	case err != nil:
		return fmt.Errorf("the unmutated port's unit tests could not run, so no mutant can be judged: %w", err)
	case failed:
		return fmt.Errorf("the unmutated port's own unit tests fail (%s): fix them before mutating", detail)
	}
	return nil
}

// Mutate applies each mutation to a copy of the Go port and requires the
// golden traces to catch it. A mutant that does not build is Invalid, not
// killed: a build failure says nothing about the scenarios.
// mutantOptions gives a mutant run a shorter default step timeout: a mutant that removes a
// dialog would otherwise leave every later dialog step waiting the full minute, and a hung
// mutant is a killed mutant. An explicit StepTimeout is kept.
func mutantOptions(o Options) Options {
	if o.StepTimeout == 0 {
		o.StepTimeout = 20 * time.Second
	}
	return o
}

func Mutate(scs []*Scenario, cfg Config, goldenDir string, mutations []Mutation, unit *UnitTest) ([]MutationResult, error) {
	if cfg.UnitOnly && unit == nil {
		return nil, errors.New("unit-only mutation needs the unit-test runner (--sdk-dir)")
	}
	if unit != nil {
		if err := unit.baseline(cfg.Go); err != nil {
			return nil, err
		}
	}
	jobs := cfg.Jobs
	if jobs < 1 {
		jobs = 1
	}
	results := make([]MutationResult, len(mutations))
	errs := make([]error, len(mutations))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, m := range mutations {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = runMutant(scs, cfg, goldenDir, m, unit)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// runMutant applies one mutation to its own copy of the port and judges it.
func runMutant(scs []*Scenario, cfg Config, goldenDir string, m Mutation, unit *UnitTest) (MutationResult, error) {
	res := MutationResult{Mutation: m}
	// An extension's identity is its directory name, so the copy keeps it. Modules its go.work
	// uses (a shared library Package) are copied to the same relative places.
	dir, siblings, cleanup, err := prepareMutantTree(cfg.Go)
	if err != nil {
		return res, err
	}
	defer cleanup()
	target, err := mutationTarget(dir, siblings, m.File)
	if err != nil {
		return res, fmt.Errorf("mutation %s: %w", m.Name, err)
	}
	src, err := os.ReadFile(target)
	if err != nil {
		return res, fmt.Errorf("mutation %s: %w", m.Name, err)
	}
	if n := strings.Count(string(src), m.Find); n != 1 {
		return res, fmt.Errorf("mutation %s: find text occurs %d times in %s, want exactly 1", m.Name, n, m.File)
	}
	if err := os.WriteFile(target, []byte(strings.Replace(string(src), m.Find, m.Replace, 1)), 0o644); err != nil {
		return res, err
	}
	if unit != nil {
		failed, detail, uerr := unit.run(dir, siblings)
		if uerr != nil {
			res.Invalid, res.Detail = true, uerr.Error()
			return res, nil
		}
		if failed {
			res.Killed, res.Detail = true, "unit test: "+detail
			return res, nil
		}
	}
	if cfg.UnitOnly {
		res.Detail = "the unit tests passed on the mutant"
		return res, nil
	}
	mc := cfg
	mc.Go = dir
	mc.Options = mutantOptions(mc.Options)
	mc.Options.Stderr = nil // a mutant that does not build is reported as Invalid, not printed 17 times
	traces, terr := os.MkdirTemp("", "eq-mutant-traces")
	if terr != nil {
		return res, terr
	}
	defer os.RemoveAll(traces)
	mc.Out = traces
	checked, err := check(scs, mc, goldenDir, true)
	switch {
	case err != nil:
		res.Invalid, res.Detail = true, err.Error()
	default:
		for _, r := range checked {
			if r.Fatal {
				res.Invalid, res.Detail = true, r.Scenario+": "+firstLine(r.Detail)
				break
			}
			if !r.Pass {
				res.Killed, res.Detail = true, r.Scenario+": "+firstLine(r.Detail)
				break
			}
		}
	}
	return res, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// prepareMutantTree copies the Go port to a scratch directory that keeps its directory name, and
// also copies every module its go.work uses from outside the port (a shared library Package) to
// the same relative place, so the go.work path still resolves. It returns the port copy, the
// sibling module copies, the scratch root and a cleanup function.
func prepareMutantTree(port string) (dir string, siblings []string, cleanup func(), err error) {
	port, err = filepath.Abs(port)
	if err != nil {
		return "", nil, nil, err
	}
	var outside []string
	if data, rerr := os.ReadFile(filepath.Join(port, "go.work")); rerr == nil {
		for _, use := range goWorkUses(string(data)) {
			module := use
			if !filepath.IsAbs(module) {
				module = filepath.Join(port, module)
			}
			module = filepath.Clean(module)
			if module != port {
				outside = append(outside, module)
			}
		}
	}
	// The copy root mirrors the tree from the deepest common ancestor of the port and its siblings.
	ancestor := filepath.Dir(port)
	for _, m := range outside {
		for {
			if rel, rerr := filepath.Rel(ancestor, m); rerr == nil && !strings.HasPrefix(rel, "..") {
				break
			}
			ancestor = filepath.Dir(ancestor)
		}
	}
	scratch, err := os.MkdirTemp("", "eq-mutant")
	if err != nil {
		return "", nil, nil, err
	}
	cleanup = func() { os.RemoveAll(scratch) }
	copyTo := func(src string) (string, error) {
		rel, rerr := filepath.Rel(ancestor, src)
		if rerr != nil {
			return "", rerr
		}
		dst := filepath.Join(scratch, rel)
		if merr := os.MkdirAll(dst, 0o755); merr != nil {
			return "", merr
		}
		return dst, copyTree(src, dst)
	}
	if dir, err = copyTo(port); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	for _, m := range outside {
		copied, cerr := copyTo(m)
		if cerr != nil {
			cleanup()
			return "", nil, nil, cerr
		}
		siblings = append(siblings, copied)
	}
	return dir, siblings, cleanup, nil
}

// goWorkUses returns the directories named by the use directives of a go.work file
// (single-line and block form; comments and quotes are handled).
func goWorkUses(text string) []string {
	var uses []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "":
			uses = append(uses, strings.Trim(line, "\"`"))
		case line == "use (":
			inBlock = true
		case strings.HasPrefix(line, "use ") && !strings.HasSuffix(line, "("):
			uses = append(uses, strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "use ")), "\"`"))
		}
	}
	return uses
}

// mutationTarget resolves a mutation's file against the port copy and refuses any path that
// leaves the copy of the port or of a module its go.work uses: mutations.json is joined to a
// scratch directory, and enough "../" would otherwise reach any file on the machine (the
// equivalence_run tool runs mutate for a model, too).
func mutationTarget(dir string, modules []string, file string) (string, error) {
	target := filepath.Join(dir, file)
	for _, root := range append([]string{dir}, modules...) {
		if rel, err := filepath.Rel(root, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return target, nil
		}
	}
	return "", fmt.Errorf("file %q is outside the port and the modules its go.work uses", file)
}
