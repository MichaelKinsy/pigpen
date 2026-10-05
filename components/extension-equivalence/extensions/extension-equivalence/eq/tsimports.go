package eq

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// An original named by one file may keep its code in local modules: an entry that re-exports the vendored
// original (`export { default } from "./oracle/src/extension.ts"`, pigpen-warden's port/eq-entry.ts), or an
// index.ts that imports its helpers. The static checks (gaps, exec coverage, the extension test) read the file
// and every local module it reaches, so such an entry is not reported as "not an extension" and a helper's API
// use is not missed.

// localSpecifierRE matches a relative module specifier in an import, export ... from, import() or require().
var localSpecifierRE = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(?\s*|\brequire\s*\(\s*)["'](\.{1,2}/[^"'\n]+)["']`)

// localModules returns entry followed by the local modules it imports or re-exports, transitively, each once.
// A specifier that names no file (a type-only path, a missing build output) is skipped; node_modules is never entered.
func localModules(entry string) ([]string, error) {
	b, err := os.ReadFile(entry)
	if err != nil {
		return nil, err
	}
	out := []string{entry}
	seen := map[string]bool{filepath.Clean(entry): true}
	queue := []struct {
		path string
		src  string
	}{{entry, string(b)}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, m := range localSpecifierRE.FindAllStringSubmatch(stripComments(cur.src), -1) {
			p := resolveLocalModule(filepath.Dir(cur.path), m[1])
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			src, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
			queue = append(queue, struct {
				path string
				src  string
			}{p, string(src)})
		}
	}
	return out, nil
}

// resolveLocalModule finds the source file a relative specifier names, as TypeScript's resolution does for the
// shapes extensions use: the exact file, the file with a source extension, a `.js` specifier written for a
// `.ts` file, or a directory's index.
func resolveLocalModule(dir, spec string) string {
	base := filepath.Join(dir, filepath.FromSlash(spec))
	if strings.Contains(base, string(filepath.Separator)+"node_modules"+string(filepath.Separator)) {
		return ""
	}
	exts := []string{".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs"}
	candidates := []string{base}
	for _, ext := range exts {
		candidates = append(candidates, base+ext)
	}
	for _, pair := range [][2]string{{".js", ".ts"}, {".mjs", ".mts"}, {".cjs", ".cts"}, {".jsx", ".tsx"}} {
		if strings.HasSuffix(base, pair[0]) {
			candidates = append(candidates, strings.TrimSuffix(base, pair[0])+pair[1])
		}
	}
	for _, ext := range exts {
		candidates = append(candidates, filepath.Join(base, "index"+ext))
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && isTSSource(c) {
			return filepath.Clean(c)
		}
	}
	return ""
}
