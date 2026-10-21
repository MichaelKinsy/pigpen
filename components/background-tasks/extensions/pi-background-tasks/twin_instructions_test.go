package pi_background_tasks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The original delivers instructions.md through its npm postinstall, which upserts a marked block into APPEND_SYSTEM.md; Pi and
// PiG ignore the manifest's `appendSystem`. The port delivers the block from before_agent_start. This test runs the original's
// own script and requires the prompt text to be exactly what the installed original leaves in the file.

func TestInstructionsAreWhatTheOriginalsInstallStepWrites(t *testing.T) {
	if _, err := os.Stat("../../package.json"); err != nil {
		t.Skip("not inside the component tree (a mutant's private copy holds only this directory)")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; the original's install script cannot run")
	}
	oracle, err := filepath.Abs("../../port/oracle")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(oracle, "instructions.md"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, instructions, string(original), "the embedded instructions are the original's file")
	install := func(existing *string) string {
		dir := t.TempDir()
		if existing != nil {
			if err := os.WriteFile(filepath.Join(dir, "APPEND_SYSTEM.md"), []byte(*existing), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(node, filepath.Join(oracle, "scripts", "append-system.mjs"), "install")
		cmd.Env = append(os.Environ(), "PI_CODING_AGENT_DIR="+dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		b, err := os.ReadFile(filepath.Join(dir, "APPEND_SYSTEM.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ported := func(current string) string {
		options := map[string]any{}
		if current != "" {
			options["appendSystemPrompt"] = current
		}
		appendInstructions(map[string]any{"systemPromptOptions": options})
		got, _ := options["appendSystemPrompt"].(string)
		return got
	}
	t.Run("no file", func(t *testing.T) {
		eq(t, ported(""), install(nil), "prompt text")
	})
	t.Run("other rules in the file", func(t *testing.T) {
		other := "\n\nUse tabs.\n\n\n\nNo emoji.\n\n"
		eq(t, ported(other), install(&other), "prompt text")
	})
	t.Run("the block is already there", func(t *testing.T) {
		installed := install(nil)
		eq(t, ported(installed), installed, "an install by the original or the kendex CLI is not doubled")
		eq(t, strings.Count(ported(installed), instructionsBegin), 1, "one block")
		again := install(&installed)
		eq(t, again, installed, "the original's own upsert is idempotent")
	})
}
