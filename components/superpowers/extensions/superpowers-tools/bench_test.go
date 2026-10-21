package superpowers_tools

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkServeRoot is one authorized GET / of a framed screen over a keep-alive connection, client included.
func BenchmarkServeRoot(b *testing.B) {
	c, info, err := StartCompanion(CompanionOptions{SessionDir: filepath.Join(b.TempDir(), "s"), Env: envOf(nil)})
	if err != nil {
		b.Fatal(err)
	}
	defer c.Stop("bench")
	_ = os.WriteFile(filepath.Join(info["screen_dir"].(string), "a.html"), []byte("<h2>Fragment</h2>"), 0o644)
	token, cookie := c.currentToken()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", info["port"]), nil)
	req.Header.Set("Cookie", cookie+"="+token)
	client := &http.Client{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkRenderGraphs is one render_skill_graphs call (three diagrams, a stand-in dot) in process.
func BenchmarkRenderGraphs(b *testing.B) {
	dir := b.TempDir()
	dot := filepath.Join(dir, "dot")
	_ = os.WriteFile(dot, []byte("#!/bin/sh\nif [ \"$1\" = -V ]; then exit 0; fi\necho '<svg>'; cat; echo '</svg>'\n"), 0o755)
	skill := filepath.Join(dir, "my-skill")
	_ = os.MkdirAll(skill, 0o755)
	_ = os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(skillWithGraphs), 0o644)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RenderGraphs(skill, false, dot); err != nil {
			b.Fatal(err)
		}
	}
}
