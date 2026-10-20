package warden

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twin: tests/no-pi-import.test.ts:43
// The Go analogue of "the library entry imports without the optional Pi peers": the package's shipped files import the
// standard library, the public SDK, and the shared pigpen components, and no PiG-internal package. (The original proves
// it by blocking `@earendil-works/pi-coding-agent` and `@earendil-works/pi-tui` at resolution time; Go resolves imports
// at build time, so the import lists are the whole proof.)
func TestShippedFilesImportOnlyThePublicSDKAndSharedComponents(t *testing.T) {
	allowed := []string{
		"github.com/MichaelKinsy/PiG/extensions/sdk",
		"github.com/MichaelKinsy/pigpen/components/typesafe/",
		"github.com/MichaelKinsy/pigpen/components/pitui",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				continue // standard library
			}
			if strings.Contains(path, "/internal/") {
				t.Errorf("%s imports the PiG-internal package %s", file, path)
				continue
			}
			ok := false
			for _, prefix := range allowed {
				if path == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(path, prefix) {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s imports %s, which is neither the public SDK nor a shared pigpen component", file, path)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d shipped files found", checked)
	}
	if _, err := os.Stat("go.mod"); err != nil {
		t.Fatal(err)
	}
}
