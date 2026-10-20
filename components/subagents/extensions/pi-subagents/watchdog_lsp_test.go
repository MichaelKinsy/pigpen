package pi_subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Twins of test/unit/watchdog-lsp-diagnostics.test.ts. The fake language server of the malformed-JSON case is a shell script
// where the original's is a node script.

func wdLspOK(diags ...wdLspDiagnostic) wdLspResult {
	return wdLspResult{Status: "ok", Provider: "stub-lsp", CheckedPaths: []string{"src/file.ts"}, SkippedPaths: []string{}, Diagnostics: diags}
}

func TestWatchdogLspDiagnosticsTwins(t *testing.T) {
	const f = "watchdog-lsp-diagnostics"
	tw(t, f, "formats diagnostics for watchdog review input", func(t *testing.T) {
		block := formatWatchdogLspDiagnosticsBlock(wdLspOK(wdLspDiagnostic{Path: "src/file.ts", Line: 2, Column: 3, Severity: "error", Source: "typescript", Code: "TS2322", Message: "Type mismatch."}))
		wdMatch(t, block, `^LSP diagnostics:`)
		wdMatch(t, block, `src/file\.ts:2:3 error TS2322 typescript: Type mismatch\.`)
	})
	tw(t, f, "omits info and hints from watchdog review input", func(t *testing.T) {
		block := formatWatchdogLspDiagnosticsBlock(wdLspOK(
			wdLspDiagnostic{Path: "src/file.ts", Line: 2, Column: 3, Severity: "info", Source: "typescript", Message: "Helpful note."},
			wdLspDiagnostic{Path: "src/file.ts", Line: 3, Column: 4, Severity: "hint", Source: "typescript", Message: "Suggestion."}))
		wdEq(t, block, "", "block")
	})
	tw(t, f, "maps errors to blockers and warnings to concerns", func(t *testing.T) {
		d := func(sev, msg string) wdLspResult {
			return wdLspOK(wdLspDiagnostic{Path: "src/file.ts", Line: 1, Column: 1, Severity: sev, Source: "typescript", Message: msg})
		}
		blocker := watchdogWarningFromLspDiagnostics(d("error", "Cannot find name 'x'."))
		if blocker == nil {
			t.Fatal("no warning for an error")
		}
		wdEq(t, blocker.Severity, "blocker", "severity")
		wdEq(t, blocker.Source, "lsp", "source")
		concern := watchdogWarningFromLspDiagnostics(d("warning", "Unused value."))
		if concern == nil {
			t.Fatal("no warning for a warning")
		}
		wdEq(t, concern.Severity, "concern", "severity")
		if watchdogWarningFromLspDiagnostics(d("info", "Helpful note.")) != nil {
			t.Error("info produced a warning")
		}
	})
	tw(t, f, "returns a failed result for malformed language-server JSON", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fake server is a shell script")
		}
		temp := tmp(t)
		bin := filepath.Join(temp, "node_modules", ".bin")
		writeFile(t, filepath.Join(temp, "src", "file.ts"), "export const value = 1;\n")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		script := "#!/bin/sh\nprintf 'Content-Length: 8\\r\\n\\r\\nnot-json'\nsleep 0.05\n"
		if err := os.WriteFile(filepath.Join(bin, "typescript-language-server"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		diagnostics := collectWatchdogLspDiagnostics(watchdogLspRequest{Cwd: temp, Root: temp, ChangedPaths: []string{"src/file.ts"},
			Config: wdLsp{Enabled: true, TimeoutMs: 500, MaxFiles: 10, MaxDiagnostics: 10}})
		wdEq(t, diagnostics.Status, "failed", "status")
		wdMatch(t, diagnostics.Message, `Invalid LSP JSON-RPC response`)
	})
	tw(t, f, "suppresses repeated diagnostic identities until the file clears", func(t *testing.T) {
		ledger := newWatchdogLspLedger()
		d := wdLspDiagnostic{Path: "src/file.ts", Line: 1, Column: 1, Severity: "warning", Source: "typescript", Code: "TS6133", Message: "Unused value."}
		at := func(line, col int) wdLspDiagnostic { x := d; x.Line, x.Column = line, col; return x }
		wdEq(t, len(ledger.reduce(wdLspOK(d)).Diagnostics), 1, "first")
		wdEq(t, len(ledger.reduce(wdLspOK(at(4, 9))).Diagnostics), 0, "moved")
		wdEq(t, len(ledger.reduce(wdLspOK()).Diagnostics), 0, "cleared")
		wdEq(t, len(ledger.reduce(wdLspOK(at(8, 1))).Diagnostics), 1, "back")
	})
}
