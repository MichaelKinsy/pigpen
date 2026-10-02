package eq

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The PATH shims record only the commands a scenario declares in `commands`.
// A command nobody declared runs unshimmed and unrecorded, so a port that
// starts an extra process (or the original's process under another name, or
// by absolute path) would pass every scenario. The exec coverage check closes
// that hole statically: every command the original or the port names as a
// string literal must be declared by at least one scenario, and must be a bare
// name the shims can intercept.

// ExecUse is one literal command name found in a source file.
type ExecUse struct {
	Name string // the command as written (first word for a shell string)
	File string
	Line int
}

var (
	// pi.exec("git", ...), spawn("git", ...), execFile("git"), execSync("git status"), execa("git")
	tsExecRE = regexp.MustCompile("\\b(?:exec|execSync|execFile|execFileSync|spawn|spawnSync|execa|execaSync)\\s*\\(\\s*[\"'`]([^\"'`$]+)[\"'`]")
	// ctx.Exec("git", ...), ctx.ExecWithOptions("git", ...), exec.Command("git", ...), exec.CommandContext(ctx, "git", ...)
	goExecRE = regexp.MustCompile(`\b(?:Exec|ExecWithOptions)\(\s*"([^"]+)"|\bexec\.Command\(\s*"([^"]+)"|\bexec\.CommandContext\(\s*[^,()]+,\s*"([^"]+)"`)
)

// ScanExecCommands lists the literal command names started in the TypeScript
// file or directory ts and in the Go port directory goDir (test files and
// testdata excluded). Either may be empty.
func ScanExecCommands(ts, goDir string) ([]ExecUse, error) {
	var uses []ExecUse
	scan := func(path string, re *regexp.Regexp, strip bool) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if strip {
			src = stripComments(src)
		}
		for i, line := range strings.Split(src, "\n") {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				for _, g := range m[1:] {
					if g == "" {
						continue
					}
					name := strings.Fields(g)
					if len(name) > 0 {
						uses = append(uses, ExecUse{Name: name[0], File: path, Line: i + 1})
					}
				}
			}
		}
		return nil
	}
	walk := func(root string, keep func(string) bool, re *regexp.Regexp) error {
		info, err := os.Stat(root)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return scan(root, re, true)
		}
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if n := d.Name(); n == "node_modules" || n == "testdata" || n == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			if keep(p) {
				return scan(p, re, true)
			}
			return nil
		})
	}
	if ts != "" {
		if err := walk(ts, isTSSource, tsExecRE); err != nil {
			return nil, err
		}
	}
	if goDir != "" {
		goSource := func(p string) bool { return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") }
		if err := walk(goDir, goSource, goExecRE); err != nil {
			return nil, err
		}
	}
	return uses, nil
}

func isTSSource(p string) bool {
	for _, ext := range []string{".ts", ".mts", ".cts", ".js", ".mjs", ".cjs", ".tsx"} {
		if strings.HasSuffix(p, ext) && !strings.HasSuffix(p, ".d.ts") {
			return true
		}
	}
	return false
}

// ExecCoverageScenario is the pseudo-scenario name of the exec coverage check.
const ExecCoverageScenario = "exec-coverage"

// execCoverage fails when a literal command of the original or the port is not
// declared by any scenario, or is a path the PATH shims cannot intercept.
func execCoverage(scs []*Scenario, ts string, goDirs ...string) (Result, error) {
	return execCoverageAccepting(scs, nil, ts, goDirs...)
}

// execCoverageAccepting is execCoverage with the owner's excuses: accepted maps "exec:<name>" to
// the reason a command that no scenario declares does not block.
func execCoverageAccepting(scs []*Scenario, accepted map[string]string, ts string, goDirs ...string) (Result, error) {
	r := Result{Scenario: ExecCoverageScenario, Pass: true}
	uses, err := ScanExecCommands(ts, "")
	if err != nil {
		return r, err
	}
	for _, dir := range goDirs {
		more, err := ScanExecCommands("", dir)
		if err != nil {
			return r, err
		}
		uses = append(uses, more...)
	}
	declared := map[string]bool{}
	for _, sc := range scs {
		for name := range sc.Commands {
			declared[name] = true
		}
	}
	var problems, seen, excused []string
	for _, u := range uses {
		where := fmt.Sprintf("%s:%d", filepath.Base(u.File), u.Line)
		reason, isAccepted := accepted["exec:"+u.Name]
		switch {
		case strings.ContainsAny(u.Name, `/\`):
			problems = append(problems, fmt.Sprintf("%s starts %q by path: the PATH shims cannot record it; use the bare name as the original does", where, u.Name))
		case !declared[u.Name] && isAccepted:
			excused = append(excused, fmt.Sprintf("%s (accepted: %s)", u.Name, reason))
		case !declared[u.Name]:
			problems = append(problems, fmt.Sprintf("%s starts %q, which no scenario declares in commands: its calls would run unrecorded", where, u.Name))
		default:
			seen = append(seen, u.Name)
		}
	}
	sort.Strings(seen)
	seen = compactStrings(seen)
	sort.Strings(excused)
	excused = compactStrings(excused)
	if len(problems) > 0 {
		r.Pass = false
		r.Detail = strings.Join(problems, "\n")
		return r, nil
	}
	if len(seen) == 0 && len(excused) == 0 {
		r.Detail = "no literal command names in the sources (a computed command name cannot be checked statically: declare it and review)"
	} else if len(seen) == 0 {
		r.Detail = "no declared command; excused by the owner: " + strings.Join(excused, "; ")
	} else {
		r.Detail = "every literal command is declared and shimmed: " + strings.Join(seen, ", ")
		if len(excused) > 0 {
			r.Detail += "; excused by the owner: " + strings.Join(excused, "; ")
		}
	}
	return r, nil
}

func compactStrings(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
