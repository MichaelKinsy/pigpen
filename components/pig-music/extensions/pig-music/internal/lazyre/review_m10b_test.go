package lazyre

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review of M10b: the M9b rule (nothing compiled when the package loads; pig-music is loaded by every PiG that selects it)
// held by convention only, and two package-level regexp.MustCompile came back (native/entities.go, prefetch/prefetch.go). This
// keeps the extension module's packages to lazyre.New.
func TestReviewM10bNoPackageLevelRegexpIsCompiledAtLoad(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // another module (cmd/pigmusic is its own binary)
				}
			}
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == filepath.Join(root, "internal", "lazyre", "lazyre.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			ast.Inspect(g, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false // a function body runs when called, not at load
				}
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "regexp" && strings.HasPrefix(sel.Sel.Name, "MustCompile") {
						t.Errorf("%s: a regexp compiled at load; use lazyre.New", fset.Position(sel.Pos()))
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
