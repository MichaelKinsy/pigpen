package pi_subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSingleOutputPaths(t *testing.T) {
	norm := func(v any, def string, hasDef bool) [3]any {
		p, dis, ok := normalizeSingleOutputOverride(v, def, hasDef)
		return [3]any{p, dis, ok}
	}
	tw(t, "single-output", "treats boolean and string false as disabled output", func(t *testing.T) {
		equal(t, norm(false, "default.md", true), [3]any{"", true, true}, "false")
		equal(t, norm("false", "default.md", true), [3]any{"", true, true}, `"false"`)
	})
	tw(t, "single-output", "treats boolean and string true as the configured default output", func(t *testing.T) {
		equal(t, norm(true, "default.md", true), [3]any{"default.md", false, true}, "true")
		equal(t, norm("true", "default.md", true), [3]any{"default.md", false, true}, `"true"`)
		equal(t, norm("true", "", false), [3]any{"", false, false}, "no default")
	})
	tw(t, "single-output", "passes explicit non-empty output paths through", func(t *testing.T) {
		equal(t, norm("reports/out.md", "default.md", true), [3]any{"reports/out.md", false, true}, "explicit")
		equal(t, norm("", "default.md", true), [3]any{"", false, false}, "empty")
		equal(t, norm(nil, "default.md", true), [3]any{"", false, false}, "undefined")
	})
	tw(t, "single-output", "does not resolve disabled or boolean-like output values", func(t *testing.T) {
		for _, v := range []any{false, "false", true, "true"} {
			equal(t, resolveSingleOutputPath(v, "/repo", "", ""), "", "value")
		}
	})
	tw(t, "single-output", "keeps absolute paths unchanged", func(t *testing.T) {
		abs := filepath.Join(os.TempDir(), "pi-subagents-abs", "report.md")
		equal(t, resolveSingleOutputPath(abs, "/repo", "/override", ""), abs, "absolute")
	})
	if runtime.GOOS == "windows" {
		t.Skip("the path expectations are POSIX")
	}
	tw(t, "single-output", "resolves relative paths against requested cwd", func(t *testing.T) {
		equal(t, resolveSingleOutputPath("reviews/report.md", "/runtime", "/requested", ""), "/requested/reviews/report.md", "path")
	})
	tw(t, "single-output", "resolves relative paths against runtime cwd when requested cwd is absent", func(t *testing.T) {
		equal(t, resolveSingleOutputPath("reviews/report.md", "/runtime", "", ""), "/runtime/reviews/report.md", "path")
	})
	tw(t, "single-output", "resolves relative requested cwd from runtime cwd before resolving output", func(t *testing.T) {
		equal(t, resolveSingleOutputPath("reviews/report.md", "/runtime", "nested/work", ""), "/runtime/nested/work/reviews/report.md", "path")
	})
	tw(t, "single-output", "resolves relative output paths against an explicit artifact base", func(t *testing.T) {
		equal(t, resolveSingleOutputPath("reviews/report.md", "/runtime", "/requested", "/repo/.pi/subagents/artifacts/outputs/run-1"), "/repo/.pi/subagents/artifacts/outputs/run-1/reviews/report.md", "path")
	})
}

func TestSingleOutputInstructions(t *testing.T) {
	caps := func(tools ...string) *outputInstructionCapabilities {
		return &outputInstructionCapabilities{Tools: tools, ToolsSet: true}
	}
	tw(t, "single-output", "appends direct-write instructions for mutation-capable agents", func(t *testing.T) {
		out := injectSingleOutputInstruction("Analyze this", "/tmp/report.md", caps("read", "write"))
		polMatches(t, out, `Write your findings to exactly this path: /tmp/report.md`)
		polMatches(t, out, `This path is authoritative for this run\.`)
		polMatches(t, out, `Ignore any other output filename or output path mentioned elsewhere`)
	})
	tw(t, "single-output", "tells read-only agents to return the artifact for runtime persistence", func(t *testing.T) {
		out := injectSingleOutputInstruction("Analyze this", "/tmp/report.md", caps("read", "grep", "find", "ls"))
		polMatches(t, out, `Return the complete artifact in your final response\.`)
		polMatches(t, out, `runtime will persist it to exactly this path: /tmp/report\.md`)
		polMatches(t, out, `Do not call contact_supervisor merely because no write-capable tool is available\.`)
		polNotMatches(t, out, `Write your findings to exactly this path`)
	})
	tw(t, "single-output", "treats the bundled reviewer profile as read-only in task and system-prompt instructions", func(t *testing.T) {
		c := caps("read", "grep", "find", "ls", "watchdog_diff", "contact_supervisor")
		for _, out := range []string{injectSingleOutputInstruction("Review this", "/tmp/review.md", c), injectOutputPathSystemPrompt("Review only", "/tmp/review.md", c)} {
			polMatches(t, out, `Return the complete artifact in your final response\.`)
			polMatches(t, out, `runtime will persist it to exactly this path: /tmp/review\.md`)
			polNotMatches(t, out, `Write your findings to exactly this path`)
		}
	})
	tw(t, "single-output", "extracts direct-write and runtime-persisted report paths", func(t *testing.T) {
		p, _ := requestedOutputPathFromTask("Write your findings to exactly this path: /tmp/direct.md")
		equal(t, p, "/tmp/direct.md", "direct")
		p, _ = requestedOutputPathFromTask("The runtime will persist it to exactly this path: `/tmp/persisted.md`")
		equal(t, p, "/tmp/persisted.md", "persisted")
	})
	tw(t, "single-output", "adds the authoritative runtime output path to the system prompt", func(t *testing.T) {
		out := injectOutputPathSystemPrompt("Output format (`old.md`):", "/tmp/new.md", nil)
		polMatches(t, out, "^Output format \\(`old\\.md`\\):")
		polMatches(t, out, `Runtime output path override:`)
		polMatches(t, out, `Write your findings to exactly this path: /tmp/new\.md`)
		polMatches(t, out, `Ignore any other output filename or output path mentioned elsewhere`)
	})
	tw(t, "single-output", "uses runtime-persistence instructions in read-only system prompts", func(t *testing.T) {
		out := injectOutputPathSystemPrompt("Analyze only", "/tmp/new.md", caps("read"))
		polMatches(t, out, `Return the complete artifact in your final response\.`)
		polMatches(t, out, `runtime will persist it to exactly this path: /tmp/new\.md`)
		polNotMatches(t, out, `Write your findings to exactly this path`)
	})
	tw(t, "single-output", "leaves prompts unchanged when no output path is active", func(t *testing.T) {
		equal(t, injectOutputPathSystemPrompt("Base prompt", "", nil), "Base prompt", "prompt")
	})
}

func TestResolveSingleOutput(t *testing.T) {
	tw(t, "single-output", "keeps agent-written file content when the file changed during the run", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "review.md")
		before := captureSingleOutputSnapshot(p)
		writeFile(t, p, "real file content")
		r := resolveSingleOutput(p, "receipt text", before, "")
		equal(t, r.FullOutput, "real file content", "output")
		equal(t, r.SavedPath, p, "saved")
		b, _ := os.ReadFile(p)
		equal(t, string(b), "real file content", "file")
	})
	tw(t, "single-output", "falls back to persisting the assistant output when the file was not changed", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "review.md")
		writeFile(t, p, "stale content")
		before := captureSingleOutputSnapshot(p)
		r := resolveSingleOutput(p, "fresh assistant output", before, "")
		equal(t, r.FullOutput, "fresh assistant output", "output")
		equal(t, r.SavedPath, p, "saved")
		b, _ := os.ReadFile(p)
		equal(t, string(b), "fresh assistant output", "file")
	})
	tw(t, "single-output", "preserves read errors from changed output paths", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "review.md")
		before := captureSingleOutputSnapshot(p)
		os.Mkdir(p, 0o755)
		r := resolveSingleOutput(p, "fallback output", before, "")
		equal(t, r.FullOutput, "fallback output", "output")
		equal(t, r.SavedPath, "", "saved")
		polMatches(t, r.SaveError, `Failed to read changed output file`)
	})
	tw(t, "single-output", "fails closed when the pre-run output snapshot could not be inspected", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "review.md")
		r := resolveSingleOutput(p, "fallback output", &singleOutputSnapshot{Error: "permission denied"}, "")
		equal(t, r.FullOutput, "fallback output", "output")
		equal(t, r.SavedPath, "", "saved")
		polMatches(t, r.SaveError, `Failed to inspect output file: permission denied`)
		if _, err := os.Stat(p); err == nil {
			t.Error("the output file was created")
		}
	})
}

func TestExtractChildWrittenOutput(t *testing.T) {
	call := func(id, name string, args map[string]any) any {
		return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": id, "name": name, "arguments": args}}, "stopReason": "toolUse"}
	}
	result := func(id string, isError bool) any {
		text := "ok"
		if isError {
			text = "write failed"
		}
		return map[string]any{"role": "toolResult", "toolCallId": id, "toolName": "write", "content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
	}
	completed := func(id, path, content string) []any {
		return []any{call(id, "write", map[string]any{"path": path, "content": content}), result(id, false)}
	}
	cat := func(parts ...[]any) []any {
		var out []any
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	extract := func(m []any, path, cwd string) any {
		s, ok := extractChildWrittenOutput(m, path, cwd)
		if !ok {
			return nil
		}
		return s
	}
	tw(t, "single-output", "returns the last successfully written content for the configured path", func(t *testing.T) {
		m := cat(completed("w1", "/tmp/out.md", "draft"), completed("w2", "/tmp/other.md", "unrelated"), completed("w3", "/tmp/out.md", "final report"))
		equal(t, extract(m, "/tmp/out.md", "/repo"), any("final report"), "content")
	})
	tw(t, "single-output", "ignores write calls whose tool result failed", func(t *testing.T) {
		failedOnly := []any{call("w1", "write", map[string]any{"path": "/tmp/out.md", "content": "never landed"}), result("w1", true)}
		equal(t, extract(failedOnly, "/tmp/out.md", "/repo"), nil, "failed only")
		after := cat(completed("w1", "/tmp/out.md", "landed"), []any{call("w2", "write", map[string]any{"path": "/tmp/out.md", "content": "never landed"}), result("w2", true)})
		equal(t, extract(after, "/tmp/out.md", "/repo"), any("landed"), "failed after success")
	})
	tw(t, "single-output", "ignores write calls with no confirmed successful tool result", func(t *testing.T) {
		missing := []any{call("w1", "write", map[string]any{"path": "/tmp/out.md", "content": "unconfirmed"})}
		equal(t, extract(missing, "/tmp/out.md", "/repo"), nil, "missing result")
		noStatus := []any{missing[0], map[string]any{"role": "toolResult", "toolCallId": "w1", "toolName": "write", "content": []any{map[string]any{"type": "text", "text": "unknown"}}}}
		equal(t, extract(noStatus, "/tmp/out.md", "/repo"), nil, "missing status")
	})
	tw(t, "single-output", "resolves relative write paths against the child cwd", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX paths")
		}
		m := completed("w1", "reports/out.md", "relative content")
		equal(t, extract(m, "/repo/reports/out.md", "/repo"), any("relative content"), "inside")
		equal(t, extract(m, "/elsewhere/reports/out.md", "/repo"), nil, "elsewhere")
	})
	if runtime.GOOS == "windows" {
		tw(t, "single-output", "matches configured output paths case-insensitively on Windows", func(t *testing.T) {
			writePath := filepath.Join(`C:\Repo`, "Reports", "Output.md")
			equal(t, extract(completed("w1", writePath, "Windows report"), strings.ToLower(writePath), `C:\Repo`), any("Windows report"), "content")
		})
	} else {
		tskip(t, "single-output", "matches configured output paths case-insensitively on Windows", "the upstream case itself is skipped off Windows (Windows path comparison); the Go twin runs only on a Windows host")
	}
	tw(t, "single-output", "ignores non-write tools and missing arguments", func(t *testing.T) {
		m := []any{call("e1", "edit", map[string]any{"path": "/tmp/out.md", "oldText": "a", "newText": "b"}), result("e1", false), call("w1", "write", map[string]any{"path": "/tmp/out.md"}), result("w1", false)}
		equal(t, extract(m, "/tmp/out.md", "/repo"), nil, "edit and missing content")
		equal(t, extract(nil, "/tmp/out.md", "/repo"), nil, "no messages")
		equal(t, extract(m, "", "/repo"), nil, "no path")
	})
}

func TestSavedOutputReference(t *testing.T) {
	tw(t, "single-output", "includes absolute path, human-readable size, and line count", func(t *testing.T) {
		p := filepath.Join(os.TempDir(), "report.md")
		ref := formatSavedOutputReference(p, "line 1\nline 2")
		equal(t, ref.Path, nodeResolve(p), "path")
		equal(t, ref.Bytes, 13, "bytes")
		equal(t, ref.Lines, 2, "lines")
		equal(t, ref.Message, "Output saved to: "+ref.Path+" (13 B, 2 lines). Read this file if needed.", "message")
	})
	tw(t, "single-output", "formats larger byte sizes in KB", func(t *testing.T) {
		polMatches(t, formatSavedOutputReference("/tmp/large.md", strings.Repeat("a", 49357)).Message, `\(48\.2 KB, 1 line\)`)
	})
	tw(t, "single-output", "requires an output path for file-only mode", func(t *testing.T) {
		polMatches(t, validateFileOnlyOutputMode("file-only", "", "Single run"), `Single run sets outputMode: "file-only"`)
		equal(t, validateFileOnlyOutputMode("file-only", "/tmp/report.md", "Single run"), "", "with path")
		equal(t, validateFileOnlyOutputMode("inline", "", "Single run"), "", "inline")
	})
}

func TestFinalizeSingleOutput(t *testing.T) {
	tw(t, "single-output", "formats saved-path messaging around the already-resolved output", func(t *testing.T) {
		r := finalizeSingleOutput(finalizeSingleOutputInput{FullOutput: "line 1\nline 2\nline 3", TruncatedOutput: "[TRUNCATED]\nline 1", OutputPath: "/tmp/review.md", SavedPath: "/tmp/review.md"})
		polMatches(t, r.DisplayOutput, `^\[TRUNCATED\]\nline 1`)
		polMatches(t, r.DisplayOutput, `Output saved to:`)
		polMatches(t, r.DisplayOutput, `3 lines`)
	})
	tw(t, "single-output", "returns only the saved-output reference in file-only mode", func(t *testing.T) {
		r := finalizeSingleOutput(finalizeSingleOutputInput{FullOutput: "line 1\nline 2\nline 3", OutputPath: "/tmp/review.md", SavedPath: "/tmp/review.md", OutputMode: "file-only"})
		polNotMatches(t, r.DisplayOutput, `line 1`)
		polMatches(t, r.DisplayOutput, `^Output saved to:`)
		polMatches(t, r.DisplayOutput, `3 lines`)
	})
	tw(t, "single-output", "does not add save messaging on failed runs", func(t *testing.T) {
		r := finalizeSingleOutput(finalizeSingleOutputInput{FullOutput: "full output", TruncatedOutput: "truncated output", OutputPath: "/tmp/review.md", SavedPath: "/tmp/review.md", ExitCode: 1})
		equal(t, r.DisplayOutput, "truncated output", "display")
	})
	tw(t, "single-output", "preserves the saved-output reference for acceptance-only failures", func(t *testing.T) {
		r := finalizeSingleOutput(finalizeSingleOutputInput{FullOutput: "useful output", OutputPath: "/tmp/review.md", OutputMode: "file-only", ExitCode: 1, PreserveSavedOutput: true, SavedPath: "/tmp/review.md"})
		polMatches(t, r.DisplayOutput, `^Output saved to:`)
		polNotMatches(t, r.DisplayOutput, `useful output`)
	})
	tw(t, "single-output", "does not duplicate an existing saved-output reference", func(t *testing.T) {
		ref := formatSavedOutputReference("/tmp/review.md", "useful output")
		r := finalizeSingleOutput(finalizeSingleOutputInput{FullOutput: "useful output\n\n" + ref.Message, OutputPath: "/tmp/review.md", ExitCode: 1, PreserveSavedOutput: true, SavedPath: "/tmp/review.md", OutputReference: &ref})
		equal(t, strings.Count(r.DisplayOutput, "Output saved to:"), 1, "references")
	})
}
