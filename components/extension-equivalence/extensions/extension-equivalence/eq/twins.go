package eq

import (
	"fmt"
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

// The twin contract: every upstream test case has a Go twin that carries the upstream title
// unchanged, or a named skip with its reason. A ledger lists the upstream titles per test
// file; the Go tests declare twins with tw(t, file, title, fn) and skips with
// tskip(t, file, title, reason) (helpers in references/twin_test.go.txt).

var (
	testFileRE  = regexp.MustCompile(`\.(?:test|spec)\.(?:ts|tsx|js|mjs|cjs|mts|cts)$`)
	testTitleRE = regexp.MustCompile("(?m)(?:^|[^\\w.$\"'`])(?:test|it)(?:\\.skip|\\.todo|\\.only)?\\(\\s*(?:\"((?:[^\"\\\\]|\\\\.)*)\"|'((?:[^'\\\\]|\\\\.)*)'|`((?:[^`\\\\]|\\\\.)*)`)")
	unescapeRE  = regexp.MustCompile("\\\\([\"'`\\\\])")
)

// ListUpstreamTitles returns the test titles of every test file under dir, keyed by the
// file's path without its .test/.spec extension, in source order.
func ListUpstreamTitles(dir string) (map[string][]string, error) {
	out := map[string][]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "_test.go.txt") {
			rel, _ := filepath.Rel(dir, p)
			key := strings.TrimSuffix(strings.TrimSuffix(filepath.ToSlash(rel), ".txt"), "_test.go")
			titles, err := goTestTitles(p)
			if err != nil {
				return err
			}
			out[key] = append(out[key], titles...)
			return nil
		}
		if !testFileRE.MatchString(p) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		key := testFileRE.ReplaceAllString(filepath.ToSlash(rel), "")
		for _, m := range testTitleRE.FindAllStringSubmatch(stripComments(string(b)), -1) {
			raw := m[1] + m[2] + m[3]
			out[key] = append(out[key], unescapeRE.ReplaceAllString(raw, "$1"))
		}
		return nil
	})
	return out, err
}

// goTestTitles lists the top-level Test functions of a Go test file and their literal subtests
// (TestX/sub name): the cases of a Go original.
func goTestTitles(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var titles []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !isGoTest(fn) {
			continue
		}
		titles = append(titles, fn.Name.Name)
		for _, sub := range literalSubtests(fn) {
			titles = append(titles, fn.Name.Name+"/"+sub)
		}
	}
	return titles, nil
}

func isGoTest(fn *ast.FuncDecl) bool {
	if !strings.HasPrefix(fn.Name.Name, "Test") || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

// literalSubtests returns the literal names of t.Run("name", ...) calls in a test function.
func literalSubtests(fn *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" || len(call.Args) < 1 {
			return true
		}
		if name, ok := stringLit(call.Args[0]); ok {
			names = append(names, name)
		}
		return true
	})
	return names
}

// skipCall reports whether call is t.Skip("..."), t.Skipf(...) or t.SkipNow(), and its reason.
func skipCall(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	switch sel.Sel.Name {
	case "SkipNow":
		return "<no reason: t.SkipNow>", true
	case "Skip", "Skipf":
		if len(call.Args) == 0 {
			return "<no reason: t." + sel.Sel.Name + ">", true
		}
		if reason, ok := stringLit(call.Args[0]); ok {
			return reason, true
		}
		return "<not a literal>", true
	}
	return "", false
}

// skipHelpers returns the names of the functions in files whose body skips the test unconditionally
// (a t.Skip, t.Skipf or t.SkipNow statement at the top level of the body): pig-snake's
// `func skipUpstream(t *testing.T, why string) { t.Helper(); t.Skip("gap: " + why) }`.
func skipHelpers(files []*ast.File) map[string]bool {
	helpers := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || isGoTest(fn) {
				continue
			}
			for _, stmt := range fn.Body.List {
				if expr, ok := stmt.(*ast.ExprStmt); ok {
					if call, ok := expr.X.(*ast.CallExpr); ok {
						if _, skips := skipCall(call); skips {
							helpers[fn.Name.Name] = true
						}
					}
				}
			}
		}
	}
	return helpers
}

// firstStatementSkip returns the reason of a skip that is the first statement of the function: a
// t.Skip("...") / t.Skipf / t.SkipNow, or a call to a skip helper of the same package (its first
// literal argument is the reason). It is the Go form of a named skip; nothing after it runs.
func firstStatementSkip(fn *ast.FuncDecl, helpers map[string]bool) (string, bool) {
	if fn.Body == nil || len(fn.Body.List) == 0 {
		return "", false
	}
	expr, ok := fn.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	if reason, ok := skipCall(call); ok {
		return reason, true
	}
	if id, ok := call.Fun.(*ast.Ident); ok && helpers[id.Name] {
		for _, arg := range call.Args {
			if reason, ok := stringLit(arg); ok {
				return reason, true
			}
		}
		return "<not a literal>", true
	}
	return "", false
}

// TwinOptions tunes CheckTwins.
type TwinOptions struct {
	Files         []string // restrict the ledger to these files (the slice shipped so far)
	MaxSameReason int      // a skip reason used more often than this is a blanket reason (default 3)
	TwinFunc      string   // default "tw"
	SkipFunc      string   // default "tskip"
	// Deferred maps a ledger file to the reason all of its cases without a twin or a named skip are
	// left for a later slice. Stated once per file; the cases stay counted (Report.Deferred).
	Deferred map[string]string
}

// TwinSkip is a named skip.
type TwinSkip struct{ Title, Reason string }

// TwinReport is the outcome of CheckTwins.
type TwinReport struct {
	Twins   int
	Skipped []TwinSkip
	Missing []string // "<file>: <title>" in the ledger with no twin and no skip
	Unknown []string // a twin or skip title the ledger does not have (misspelt or renamed)
	Blanket []string // reasons shared by more skips than allowed
	Dynamic []string // twins declared with a computed title (a loop): not matched, listed for review
	// Deferred counts, per ledger file, the cases left to a later slice by TwinOptions.Deferred.
	Deferred       map[string]int
	DeferredReason map[string]string
}

// OK reports whether the contract holds.
func (r TwinReport) OK() bool {
	return len(r.Missing) == 0 && len(r.Unknown) == 0 && len(r.Blanket) == 0
}

// Summary renders the line for PORT.md and the problems.
func (r TwinReport) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d exact twins, %d skipped", r.Twins, len(r.Skipped))
	if len(r.Skipped) > 0 {
		b.WriteString(":")
	}
	b.WriteString("\n")
	for _, s := range r.Skipped {
		fmt.Fprintf(&b, "  SKIP %q: %s\n", s.Title, s.Reason)
	}
	deferred := make([]string, 0, len(r.Deferred))
	for f := range r.Deferred {
		deferred = append(deferred, f)
	}
	sort.Strings(deferred)
	for _, f := range deferred {
		fmt.Fprintf(&b, "DEFERRED %s (%d cases): %s\n", f, r.Deferred[f], r.DeferredReason[f])
	}
	for _, m := range r.Missing {
		fmt.Fprintf(&b, "MISSING %s\n", m)
	}
	for _, u := range r.Unknown {
		fmt.Fprintf(&b, "UNKNOWN %s (no upstream test has this title)\n", u)
	}
	for _, d := range r.Dynamic {
		fmt.Fprintf(&b, "NOTE %s: not matched against the ledger; review by hand\n", d)
	}
	for _, x := range r.Blanket {
		fmt.Fprintf(&b, "BLANKET %q: one reason must not cover many cases; give each its own\n", x)
	}
	return b.String()
}

// CheckTwins compares the ledger with the twins and skips declared in the Go tests under goDir.
func CheckTwins(ledger map[string][]string, goDir string, o TwinOptions) (TwinReport, error) {
	var rep TwinReport
	if o.TwinFunc == "" {
		o.TwinFunc = "tw"
	}
	if o.SkipFunc == "" {
		o.SkipFunc = "tskip"
	}
	if o.MaxSameReason == 0 {
		o.MaxSameReason = 3
	}
	files := make([]string, 0, len(ledger))
	if len(o.Files) > 0 {
		for _, f := range o.Files {
			if _, ok := ledger[f]; !ok {
				return rep, fmt.Errorf("ledger has no test file %q", f)
			}
			files = append(files, f)
		}
	} else {
		for f := range ledger {
			files = append(files, f)
		}
	}
	sort.Strings(files)

	// Declared twins (tw calls) and skips (tskip calls), keyed by twinKey: a literal file argument must
	// name the ledger file whose case it covers; a computed one (a const) is matched by title alone.
	twins := map[string]int{}
	skips := map[string][]string{}
	// Test functions of the port that keep an original Go case's name. Only a ledger title can
	// consume them: the port's own extra tests are not errors.
	fnTwins := map[string]int{}
	fnSkips := map[string][]string{}
	var dynamic []string
	// Parse every test file first: a skip helper may live in another file of the package.
	type parsed struct {
		path string
		fset *token.FileSet
		file *ast.File
	}
	byDir := map[string][]parsed{}
	var dirs []string
	err := filepath.WalkDir(goDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		dir := filepath.Dir(p)
		if _, seen := byDir[dir]; !seen {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], parsed{p, fset, f})
		return nil
	})
	if err != nil {
		return rep, err
	}
	for _, dir := range dirs {
		var files []*ast.File
		for _, pf := range byDir[dir] {
			files = append(files, pf.file)
		}
		helpers := skipHelpers(files)
		for _, pf := range byDir[dir] {
			p, fset, f := pf.path, pf.fset, pf.file
			// twin and skip helper calls; inside a test function that skips first, a twin never runs and
			// is a named skip with the function's reason.
			visit := func(node ast.Node, skipped bool, skipReason string) {
				ast.Inspect(node, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					id, ok := call.Fun.(*ast.Ident)
					if !ok || (id.Name != o.TwinFunc && id.Name != o.SkipFunc) || len(call.Args) < 3 {
						return true
					}
					title, ok := stringLit(call.Args[2])
					if !ok {
						dynamic = append(dynamic, fmt.Sprintf("%s:%d: non-literal title", filepath.Base(p), fset.Position(call.Pos()).Line))
						return true
					}
					file, literal := stringLit(call.Args[1])
					title = twinKey(file, literal, title)
					if id.Name == o.TwinFunc {
						if skipped {
							skips[title] = append(skips[title], skipReason)
						} else {
							twins[title]++
						}
						return true
					}
					reason := "<not a literal>"
					if len(call.Args) > 3 {
						if r, ok := stringLit(call.Args[3]); ok {
							reason = r
						}
					}
					skips[title] = append(skips[title], reason)
					return true
				})
			}
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !isGoTest(fn) {
					visit(decl, false, "")
					continue
				}
				// A Go original's cases keep their names in the port: the Test function is the twin, and a
				// skip as its first statement (t.Skip, or a skip helper) is the named skip, which also
				// covers its subtests.
				reason, skipped := firstStatementSkip(fn, helpers)
				if skipped {
					fnSkips[fn.Name.Name] = append(fnSkips[fn.Name.Name], reason)
				} else {
					fnTwins[fn.Name.Name]++
				}
				for _, sub := range literalSubtests(fn) {
					if skipped {
						fnSkips[fn.Name.Name+"/"+sub] = append(fnSkips[fn.Name.Name+"/"+sub], reason)
					} else {
						fnTwins[fn.Name.Name+"/"+sub]++
					}
				}
				visit(fn, skipped, reason)
			}
		}
	}

	for f, reason := range o.Deferred {
		if _, ok := ledger[f]; !ok {
			return rep, fmt.Errorf("deferred slice %q is not a file of the ledger", f)
		}
		if strings.TrimSpace(reason) == "" {
			return rep, fmt.Errorf("deferred slice %q needs a reason", f)
		}
	}
	known := map[string]bool{}
	for _, f := range files {
		for _, title := range ledger[f] {
			known[title] = true
			switch {
			case twins[twinKey(f, true, title)] > 0:
				twins[twinKey(f, true, title)]--
				rep.Twins++
			case twins[twinKey("", false, title)] > 0:
				twins[twinKey("", false, title)]--
				rep.Twins++
			case fnTwins[title] > 0:
				fnTwins[title]--
				rep.Twins++
			case len(skips[twinKey(f, true, title)]) > 0:
				k := twinKey(f, true, title)
				rep.Skipped = append(rep.Skipped, TwinSkip{Title: title, Reason: skips[k][0]})
				skips[k] = skips[k][1:]
			case len(skips[twinKey("", false, title)]) > 0:
				k := twinKey("", false, title)
				rep.Skipped = append(rep.Skipped, TwinSkip{Title: title, Reason: skips[k][0]})
				skips[k] = skips[k][1:]
			case len(fnSkips[title]) > 0:
				rep.Skipped = append(rep.Skipped, TwinSkip{Title: title, Reason: fnSkips[title][0]})
				fnSkips[title] = fnSkips[title][1:]
			case o.Deferred[f] != "":
				if rep.Deferred == nil {
					rep.Deferred, rep.DeferredReason = map[string]int{}, map[string]string{}
				}
				rep.Deferred[f]++
				rep.DeferredReason[f] = o.Deferred[f]
			default:
				rep.Missing = append(rep.Missing, f+": "+title)
			}
		}
	}
	// A declared twin or skip that the (restricted) ledger did not consume is a misspelling when its
	// title is not a case of the file it names (literal file), or of any file (computed file); a title
	// of an unshipped file is not.
	all := map[string]bool{}
	for _, titles := range ledger {
		for _, title := range titles {
			all[twinKey("", false, title)] = true
		}
	}
	for file, titles := range ledger {
		for _, title := range titles {
			all[twinKey(file, true, title)] = true
		}
	}
	unknown := map[string]bool{}
	for key, n := range twins {
		if n > 0 && !all[key] {
			unknown[key] = true
		}
	}
	for key, rs := range skips {
		if len(rs) > 0 && !all[key] {
			unknown[key] = true
		}
	}
	for key := range unknown {
		rep.Unknown = append(rep.Unknown, twinKeyString(key))
	}
	sort.Strings(rep.Unknown)
	rep.Dynamic = dynamic
	byReason := map[string]int{}
	for _, s := range rep.Skipped {
		byReason[s.Reason]++
	}
	for reason, n := range byReason {
		if n > o.MaxSameReason {
			rep.Blanket = append(rep.Blanket, reason)
		}
	}
	sort.Strings(rep.Blanket)
	return rep, nil
}

// twinKey keys a declared twin or skip: by ledger file and title when the file argument is a literal,
// by title alone when it is computed.
func twinKey(file string, literal bool, title string) string {
	if !literal {
		return "\x01" + title
	}
	return file + "\x00" + title
}

// twinKeyString renders a key as the report does: "file: title", or the title alone.
func twinKeyString(key string) string {
	if title, ok := strings.CutPrefix(key, "\x01"); ok {
		return title
	}
	file, title, _ := strings.Cut(key, "\x00")
	return file + ": " + title
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}
