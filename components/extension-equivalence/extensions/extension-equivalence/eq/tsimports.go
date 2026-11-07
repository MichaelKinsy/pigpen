package eq

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// ExtensionFilesClosure is the trace header's extensionFiles value for a hash over an entry's import closure.
const ExtensionFilesClosure = "closure"

// TSClosureHash hashes a TypeScript original the way Pi loads it: the entry, every local module it reaches
// (localModules) and the package.json in the entry's directory, if there is one. A change to any of those files
// changes the hash, which the entry file's bytes alone cannot show for an original split over ui.ts and src/.
// Each file contributes its path relative to the entry's directory and its bytes, in path order; a dependency
// directory is never entered.
func TSClosureHash(entry string) (string, error) {
	files, err := localModules(filepath.Clean(entry))
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(filepath.Clean(entry))
	if pkg := filepath.Join(dir, "package.json"); fileExists(pkg) {
		files = append(files, pkg)
	}
	rels := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := filepath.Rel(dir, f)
		if err != nil {
			return "", err
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(b))
		h.Write(b)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
