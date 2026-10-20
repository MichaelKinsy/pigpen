package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Benchmarks for the paths this slice puts on or near a hook: loading, the per-call change check, the status rows and a
// panel render. Run: go test -run '^$' -bench . -benchmem -count=10

func BenchmarkApplyUserOverridesFullFile(b *testing.B) {
	data, _ := json.Marshal(DefaultConfig())
	var raw any
	_ = json.Unmarshal(data, &raw)
	base := DefaultConfig()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ApplyUserOverrides(base, raw)
	}
}

func BenchmarkLoadConfigFromDisk(b *testing.B) {
	path := filepath.Join(b.TempDir(), "config.json")
	data, _ := json.Marshal(DefaultConfig())
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := LoadConfig(path); err != nil {
			b.Fatal(err)
		}
	}
}

// The check each hook now makes for a changed file: two stats.
func BenchmarkConfigChangeCheck(b *testing.B) {
	dir := b.TempDir()
	w := &warden{cfgPath: filepath.Join(dir, "a.json"), projectPath: filepath.Join(dir, "b.json"), projectTrusted: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = w.configSig()
	}
}

func BenchmarkWidgetLines(b *testing.B) {
	e := entries("action", allowLine, "rules", "warden · rules · write src/clean.ts · 2 rules · none · ok",
		"prose", "warden · prose · wordy 0.36 · clichés 0.09 · jargon 0.19 · ok", "stuck", "warden · stuck · 3 failures · exact repeat · stuck",
		"done", "warden · done-check · 1 changes · claims done 0.92 · complete · unverified", "context", "warden · context · bash · duplicate · saved 1024 bytes")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = WidgetLines(e, bareTheme{}, 80)
	}
}

func BenchmarkTracePanelRender100Entries(b *testing.B) {
	trace := newTrace(100)
	for i := 0; i < 100; i++ {
		trace.Push(&TraceEntry{At: time.Now(), Guard: "action", Line: "warden · bash · irreversible 0.20 · off-task 0.10 · expected step · allow", Details: []string{"ran: npm test", "jev: irreversible 0.20"}})
	}
	p := NewTracePanel(trace, bareTheme{}, panelActions{})
	p.SetViewport(40)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = p.Render(80)
	}
}
