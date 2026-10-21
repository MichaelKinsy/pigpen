package warden

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Benchmarks of the conscience paths on the inputs port/bench-conscience-original.ts times in the original under
// Node: an assessment against an instant judge, candidate filtering over files on disk, index validation and I/O,
// the question hash, the description sanitizer, a skill load.

func benchSkills(n int, dir string) []Skill {
	out := make([]Skill, n)
	for i := range out {
		path := filepath.Join(dir, fmt.Sprintf("skill-%d.md", i))
		if dir != "" {
			_ = os.WriteFile(path, []byte(fmt.Sprintf("---\nname: skill-%d\ndescription: Skill %d does a thing\n---\n\nBody of skill %d.\n", i, i, i)), 0o644)
		}
		out[i] = Skill{Name: fmt.Sprintf("skill-%d", i), Description: fmt.Sprintf("Skill %d does a thing with files and tests", i), FilePath: path}
	}
	return out
}

func benchJudge() Judge {
	return csJudge(func(_ context.Context, req Request) (Evaluation, error) {
		answers := make(map[string]Answer, len(req.Questions))
		answers["conscience_disposition"] = csChoice("advance", 0.9)
		for id := range req.Questions {
			if id != "conscience_disposition" {
				answers[id] = csScore(2, 0.1, 0.2, 0.5, 0.2)
			}
		}
		return Evaluation{Answers: answers, Model: "jev-1.13.0"}, nil
	})
}

func benchAssess(b *testing.B, skills []Skill, tools []CapabilityTool) {
	deps := ConscienceDeps{Judge: benchJudge(), Config: csConfig(func(c *ConscienceConfig) { c.RecommendThreshold = 0.5 }), SharedTimeoutMs: 5000}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		assess(ctx, "design a landing page for the repository", "", skills, tools, nil, nil, deps, nil)
	}
}

func BenchmarkConscienceAssess40Skills(b *testing.B) {
	benchAssess(b, benchSkills(40, b.TempDir()), nil)
}

func BenchmarkConscienceAssess250SkillsAnd60Tools(b *testing.B) {
	tools := make([]CapabilityTool, 60)
	for i := range tools {
		tools[i] = CapabilityTool{Name: fmt.Sprintf("tool_%d", i), Description: fmt.Sprintf("Tool %d lists things in the repository", i)}
	}
	benchAssess(b, benchSkills(250, b.TempDir()), tools)
}

func BenchmarkConscienceEligibleCandidates100SkillFiles(b *testing.B) {
	skills := benchSkills(100, b.TempDir())
	tools := make([]CapabilityTool, 100)
	for i := range tools {
		tools[i] = CapabilityTool{Name: fmt.Sprintf("tool_%d", i), Description: fmt.Sprintf("Tool %d lists things in the repository", i)}
	}
	cfg := csConfig()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eligibleCandidates(skills, tools, cfg, nil, nil, nil, nil, "linux")
	}
}

func BenchmarkConscienceQuestionHash31Candidates(b *testing.B) {
	items := make([]string, 31)
	for i := range items {
		items[i] = fmt.Sprintf("s%d", i)
	}
	batch := csBatch("skill", items...)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q, _, _ := buildBatchQuestions(batch, "task", "", nil, nil)
		questionHash(q)
	}
}

func BenchmarkConscienceSanitizeDescription(b *testing.B) {
	text := strings.Repeat("Use this to read /Users/anon/project/file.ts and see https://docs.example.com/guide, config at ~/config.json; token: abcdefghijklmnop12345 for auth. ", 12)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sanitizeDescription(text)
	}
}

func benchIndexEntries(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{
			"kind": "skill", "name": fmt.Sprintf("skill-%d", i), "scope": "global", "sourceHash": "abc123def4567890", "role": "evidence",
			"lead": "Search the code knowledge graph", "useWhen": []any{"When you need to find definitions", "When grep cannot find structural code"},
			"notWhen": []any{"When a plain search works do that"}, "inputs": "A search query string",
			"examples": []any{"find the config loader function", "search for all calls"}, "sourceQuality": "strong",
		}
	}
	return out
}

func BenchmarkConscienceValidateIndex200Entries(b *testing.B) {
	raw := map[string]any{"formatVersion": float64(1), "builtAt": "t", "model": "m", "entries": benchIndexEntries(200)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := validateIndex(raw); r.File == nil {
			b.Fatal(r.Rejections)
		}
	}
}

func BenchmarkConscienceWriteAndReadIndex200Entries(b *testing.B) {
	file := validateIndex(map[string]any{"formatVersion": float64(1), "builtAt": "t", "model": "m", "entries": benchIndexEntries(200)}).File
	path := filepath.Join(b.TempDir(), "global.json")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := writeIndex(path, file); err != nil {
			b.Fatal(err)
		}
		if readIndex(path) == nil {
			b.Fatal("unreadable")
		}
	}
}

func BenchmarkConscienceWriteIndex200Entries(b *testing.B) {
	file := validateIndex(map[string]any{"formatVersion": float64(1), "builtAt": "t", "model": "m", "entries": benchIndexEntries(200)}).File
	path := filepath.Join(b.TempDir(), "global.json")
	b.ReportAllocs()
	for b.Loop() {
		if err := writeIndex(path, file); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConscienceReadIndex200Entries(b *testing.B) {
	file := validateIndex(map[string]any{"formatVersion": float64(1), "builtAt": "t", "model": "m", "entries": benchIndexEntries(200)}).File
	path := filepath.Join(b.TempDir(), "global.json")
	if err := writeIndex(path, file); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if readIndex(path) == nil {
			b.Fatal("unreadable")
		}
	}
}

func BenchmarkConscienceBuildIndexPrompt100Skills60Tools(b *testing.B) {
	skills := make([]IndexSkill, 100)
	for i := range skills {
		skills[i] = IndexSkill{Name: fmt.Sprintf("skill-%d", i), Description: "Skill does a thing with files and tests", FilePath: fmt.Sprintf("/home/u/.pi/agent/skills/skill-%d/SKILL.md", i)}
	}
	tools := make([]CapabilityTool, 60)
	for i := range tools {
		tools[i] = CapabilityTool{Name: fmt.Sprintf("tool_%d", i), Description: "Tool lists things in the repository"}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buildIndexPrompt("/p", skills, tools, "/g.json", "/p.json", "/home/u/.pi/agent")
	}
}

func BenchmarkConscienceLoadSkillBody8KB(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	body := strings.Repeat("Polish the interface: align spacing, tighten type, check contrast.\n", 120)
	_ = os.WriteFile(path, []byte("---\nname: bench\ndescription: Bench skill\n---\n\n"+body), 0o644)
	skill := Skill{Name: "bench", Description: "Bench skill", FilePath: path}
	cfg := csConfig(func(c *ConscienceConfig) { c.Skills.Mode = "load" })
	opts := LoadOpts{RemainingMs: 5000, ConsentGiven: true, ProjectTrusted: true, CatalogName: "bench", CatalogDesc: "Bench skill", ContextWindow: intPtr(200000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clearFileIdentityCache()
		if r := loadSkillBody(skill, cfg, opts); r.Body == nil {
			b.Fatal(r.SkipReason)
		}
	}
}
