package warden

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// auditBenchWorkspace is a workspace of 5 projects with 60 source files each (the discovery cap is 200 per project).
func auditBenchWorkspace(b *testing.B) (string, []string) {
	b.Helper()
	root := filepath.Join(b.TempDir(), "workspace")
	files := map[string]string{"package.json": "{}"}
	projects := []string{root}
	for p := 0; p < 4; p++ {
		name := fmt.Sprintf("p%d", p)
		files[name+"/package.json"] = "{}"
		projects = append(projects, filepath.Join(root, name))
		for f := 0; f < 60; f++ {
			files[fmt.Sprintf("%s/src/d%d/f%d.ts", name, f%5, f)] = "x"
		}
	}
	for f := 0; f < 60; f++ {
		files[fmt.Sprintf("src/f%d.go", f)] = "x"
	}
	for rel, body := range files {
		mk(b, filepath.Join(root, filepath.FromSlash(rel)), body)
	}
	return root, projects
}

// Run with: go test -run xxx -bench 'Audit|Discover' -benchmem
func BenchmarkBuildAuditPrompt(b *testing.B) {
	root, projects := auditBenchWorkspace(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		BuildAuditPrompt(root, projects)
	}
}

func BenchmarkFindProjects(b *testing.B) {
	root, _ := auditBenchWorkspace(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FindProjects(root)
	}
}

func mk(b *testing.B, path, body string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		b.Fatal(err)
	}
}
