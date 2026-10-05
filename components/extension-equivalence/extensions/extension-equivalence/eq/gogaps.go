package eq

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The Go counterpart of the gap scan, for an original that is already Go (a port relocated
// from another repository, or new code on the SDK). There is no Pi API to look for; what
// stops a Go extension from being a faithful, fusable, public-SDK-only Package is:
//
//   - an import of a PiG package other than the public SDK (the "no PiG-internal import" rule),
//   - a call that PiG's fuse check rejects (os.Exit, os.Chdir, os.Stdout, log.Fatal*, fmt.Print*),
//   - an SDK operation the surface table records as a stand-in (its note says what differs).
//
// Test files and `package main` files (companion executables) are not part of the fused
// extension and are not scanned.

const publicSDK = "github.com/MichaelKinsy/PiG/extensions/sdk"

var hazards = map[string]map[string]bool{
	"os":  {"Exit": true, "Chdir": true, "Stdout": true},
	"log": {"Fatal": true, "Fatalf": true, "Fatalln": true},
	"fmt": {"Print": true, "Printf": true, "Println": true},
}

var hazardNote = "process-global: PiG's fuse check rejects it in code the extension imports; return errors and use the host APIs instead"

// goStandInRE matches a Go cell that records a stand-in: `Type.Member`.
var goStandInRE = regexp.MustCompile("^stand-in/partial `(([A-Za-z]+)\\.([A-Za-z]+))[^`]*`")

// parseGoStandIns maps the Go SDK member name of every stand-in row to {qualified name, surface note}.
func parseGoStandIns(markdown string) map[string][2]string {
	out := map[string][2]string{}
	for _, line := range strings.Split(markdown, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 7 || !strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			continue
		}
		goCell := strings.TrimSpace(cells[4])
		m := goStandInRE.FindStringSubmatch(goCell)
		if m == nil || goGeneric[m[3]] {
			continue
		}
		if _, dup := out[m[3]]; !dup {
			out[m[3]] = [2]string{m[1], noteOf(goCell)}
		}
	}
	return out
}

var goGeneric = map[string]bool{"Done": true, "Err": true, "Execute": true, "Timeout": true}

// ScanGoPath scans the Go files under path (a file or a directory) as described above.
func ScanGoPath(path, surfaceMarkdown string) ([]Gap, error) {
	if surfaceMarkdown == "" {
		surfaceMarkdown = embeddedSurface
	}
	standIns := parseGoStandIns(surfaceMarkdown)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	base := path
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", "vendor", "testdata":
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		files, base = []string{path}, filepath.Dir(path)
	}
	type key struct{ symbol, file string }
	found := map[key]*Gap{}
	add := func(symbol, severity, note, file string, line int) {
		k := key{symbol, file}
		if g, ok := found[k]; ok {
			g.Lines = append(g.Lines, line)
			return
		}
		found[k] = &Gap{Symbol: symbol, Severity: severity, Note: note, File: file, Lines: []int{line}}
	}
	for _, file := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		if f.Name.Name == "main" {
			continue
		}
		rel, _ := filepath.Rel(base, file)
		rel = filepath.ToSlash(rel)
		names := map[string]string{} // local package name -> import path
		usesSDK := false
		for _, imp := range f.Imports {
			ip, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(ip)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			names[name] = ip
			if ip == publicSDK || strings.HasPrefix(ip, publicSDK+"/") {
				usesSDK = true
			} else if strings.HasPrefix(ip, "github.com/MichaelKinsy/PiG/") {
				add("import "+ip, "missing", "not the public extension SDK: a Package may import only "+publicSDK+"; copy or re-derive what it needs and keep the credit", rel, fset.Position(imp.Pos()).Line)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			line := fset.Position(sel.Pos()).Line
			if id, ok := sel.X.(*ast.Ident); ok {
				if ip, ok := names[id.Name]; ok && hazards[ip][sel.Sel.Name] {
					add(ip+"."+sel.Sel.Name, "missing", hazardNote, rel, line)
					return true
				}
			}
			if s, ok := standIns[sel.Sel.Name]; ok && usesSDK {
				add(s[0], "partial", s[1], rel, line)
			}
			return true
		})
	}
	var gaps []Gap
	for _, g := range found {
		sort.Ints(g.Lines)
		gaps = append(gaps, *g)
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].Severity != gaps[j].Severity {
			return gaps[i].Severity == "missing"
		}
		if gaps[i].Symbol != gaps[j].Symbol {
			return gaps[i].Symbol < gaps[j].Symbol
		}
		return gaps[i].File < gaps[j].File
	})
	return gaps, nil
}
