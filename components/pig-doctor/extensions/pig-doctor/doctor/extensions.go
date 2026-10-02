package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// extRecord is one extension the doctor found on a load path.
type extRecord struct {
	Name   string
	Dir    string
	Origin string // "legacy", "user", "package"
	Lang   string // go, node, other
	Module string
	GoReq  string   // go directive
	Causes []string // why it cannot build or load
}

func (e *extRecord) inHome(home string) bool { return within(home, e.Dir) }

// discoverDir lists the extensions directly inside dir.
func discoverDir(dir, origin string) []*extRecord {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []*extRecord
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if r := inspectExtension(filepath.Join(dir, e.Name()), origin); r != nil {
			out = append(out, r)
		}
	}
	return out
}

func inspectExtension(path, origin string) *extRecord {
	info, err := os.Lstat(path)
	if err != nil || isSymlink(info) {
		return nil
	}
	r := &extRecord{Dir: path, Origin: origin, Name: filepath.Base(path), Lang: "other"}
	if !info.IsDir() {
		ext := filepath.Ext(path)
		if ext == ".ts" || ext == ".js" || ext == ".mjs" {
			r.Lang = "node"
			r.Name = strings.TrimSuffix(r.Name, ext)
			return r
		}
		return nil
	}
	if _, err := os.Lstat(filepath.Join(path, "go.mod")); err == nil {
		r.Lang = "go"
		inspectGo(r)
	} else if _, err := os.Lstat(filepath.Join(path, "package.json")); err == nil {
		r.Lang = "node"
	}
	return r
}

var (
	moduleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	goDirRe  = regexp.MustCompile(`(?m)^go\s+(\d+(?:\.\d+){1,2})\s*$`)
)

func inspectGo(r *extRecord) {
	mod, err := readFileGuarded(filepath.Join(r.Dir, "go.mod"))
	if err != nil {
		r.Causes = append(r.Causes, "go.mod unreadable: "+err.Error())
		return
	}
	if m := moduleRe.FindSubmatch(mod); m != nil {
		r.Module = string(m[1])
	}
	if m := goDirRe.FindSubmatch(mod); m != nil {
		r.GoReq = string(m[1])
	}
	entries, _ := os.ReadDir(r.Dir)
	goFiles := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		goFiles++
	}
	if goFiles == 0 {
		if _, err := os.Lstat(filepath.Join(r.Dir, "cmd")); err != nil {
			r.Causes = append(r.Causes, "no Go source files")
		}
	}
}

// versionParts parses "1.26.7" or "go1.26.7" (also "go1.26.7+auto", "1.26rc1").
func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "go")
	if i := strings.IndexAny(v, "+ "); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		digits := p
		for j, c := range p {
			if c < '0' || c > '9' {
				digits = p[:j]
				break
			}
		}
		n, _ := strconv.Atoi(digits)
		out[i] = n
	}
	return out
}

func versionLess(a, b string) bool {
	x, y := versionParts(a), versionParts(b)
	for i := range x {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return false
}

// logCauses reads the tail of each recent extension log and returns one cause per extension name.
var logPatterns = []struct {
	re   *regexp.Regexp
	text func(m []string) string
}{
	{regexp.MustCompile(`compile: version "(go[^"]+)" does not match go tool version "(go[^"]+)"`), func(m []string) string {
		return fmt.Sprintf(`compile: version %q does not match go tool version %q`, m[1], m[2])
	}},
	{regexp.MustCompile(`(?:cannot find module providing package|no required module provides package) (\S+)`), func(m []string) string {
		return "no module provides package " + strings.TrimRight(m[1], ":;")
	}},
	{regexp.MustCompile(`(?i)go: (?:updates to go\.mod needed|.*requires go >= [\d.]+)[^\n]*`), func(m []string) string { return strings.TrimSpace(m[0]) }},
}

func scanExtensionLogs(dir string) map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		b, err := readTail(filepath.Join(dir, e.Name()), 64<<10)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		for _, line := range strings.Split(string(b), "\n") {
			for _, p := range logPatterns {
				if m := p.re.FindStringSubmatch(line); m != nil {
					if _, ok := out[name]; !ok {
						out[name] = p.text(m) + " (from " + e.Name() + ")"
					}
				}
			}
		}
	}
	return out
}
