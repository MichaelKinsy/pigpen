package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The per-call run parameters this port honors: the deadline aliases and the
// durable output binding.

func TestOutputBindingResolution(t *testing.T) {
	cwd := t.TempDir()
	// A relative path resolves against the run's cwd.
	b, err := resolveOutputBinding(map[string]any{"output": "out/answer.md"}, cwd, "Single run (x)")
	eq(t, err, "", "relative")
	eq(t, b.Path, filepath.Join(cwd, "out", "answer.md"), "resolved")
	// An absolute path stays.
	b, err = resolveOutputBinding(map[string]any{"output": filepath.Join(cwd, "abs.md")}, cwd, "Single run (x)")
	eq(t, err, "", "absolute")
	eq(t, filepath.IsAbs(b.Path), true, "absolute stays")
	// output: false suppresses the inline output.
	b, err = resolveOutputBinding(map[string]any{"output": false}, cwd, "Single run (x)")
	eq(t, err, "", "false")
	eq(t, b.Suppress, true, "suppressed")
	// file-only requires a path, with the original's message.
	b, err = resolveOutputBinding(map[string]any{"outputMode": "file-only"}, cwd, "Single run (worker)")
	eq(t, err, `Single run (worker) sets outputMode: "file-only" but does not configure an output file. Set output to a path or use outputMode: "inline".`, "file-only error")
	eq(t, b.Path, "", "no path")
	// An unknown mode keeps the schema's error.
	_, err = resolveOutputBinding(map[string]any{"outputMode": "both"}, cwd, "Single run (x)")
	eq(t, err, "outputMode must be 'inline' or 'file-only'.", "mode error")
}

func TestDeadlineAliases(t *testing.T) {
	d, err := resolveRunDeadlineMs(map[string]any{})
	eq(t, err, "", "unset")
	eq(t, d, int64(0), "default")
	d, err = resolveRunDeadlineMs(map[string]any{"timeoutMs": float64(5000)})
	eq(t, err, "", "timeout")
	eq(t, d, int64(5000), "timeout value")
	d, err = resolveRunDeadlineMs(map[string]any{"timeoutMs": float64(5000), "maxRuntimeMs": float64(5000)})
	eq(t, err, "", "agreeing aliases")
	eq(t, d, int64(5000), "alias value")
	_, err = resolveRunDeadlineMs(map[string]any{"timeoutMs": float64(5000), "maxRuntimeMs": float64(7000)})
	eq(t, err, "timeoutMs and maxRuntimeMs are aliases; provide only one value or use the same value for both.", "disagreement")
	_, err = resolveRunDeadlineMs(map[string]any{"timeoutMs": "soon"})
	eq(t, err, "timeoutMs must be an integer >= 1.", "invalid value")
	_, err = resolveRunDeadlineMs(map[string]any{"maxRuntimeMs": float64(0)})
	eq(t, err, "maxRuntimeMs must be an integer >= 1.", "invalid value zero")
}

func TestOutputInstructionAndReference(t *testing.T) {
	// The child's task carries the original's instruction block.
	task := injectSingleOutputInstruction("Do it", "/w/answer.md")
	if !strings.HasSuffix(task, "---\n**Output:**\nWrite your findings to exactly this path: /w/answer.md\nThis path is authoritative for this run.\nIgnore any other output filename or output path mentioned elsewhere, including output destinations in the base agent prompt, system prompt, or task instructions.") {
		t.Errorf("instruction:\n%s", task)
	}
	eq(t, injectSingleOutputInstruction("Do it", ""), "Do it", "no binding, no instruction")
	// The saved reference is the original's message.
	dir := t.TempDir()
	path := filepath.Join(dir, "answer.md")
	msg, err := savedOutputReference(path, "line one\nline two\n")
	eq(t, err, nil, "save")
	abs, _ := filepath.Abs(path)
	eq(t, msg, "Output saved to: "+abs+" (18 B, 2 lines). Read this file if needed.", "reference")
	body, _ := os.ReadFile(path)
	eq(t, string(body), "line one\nline two\n", "the artifact carries the answer")
	// The byte formatter is the original's.
	eq(t, formatByteSize(12), "12 B", "bytes")
	eq(t, formatByteSize(2048), "2.0 KB", "kb")
	eq(t, formatByteSize(1024*1024*3), "3.0 MB", "mb")
	// The line count matches the original (a trailing newline adds none).
	eq(t, countOutputLines(""), 0, "empty")
	eq(t, countOutputLines("a"), 1, "one")
	eq(t, countOutputLines("a\n"), 1, "trailing newline")
	eq(t, countOutputLines("a\nb\n"), 2, "two")
}
