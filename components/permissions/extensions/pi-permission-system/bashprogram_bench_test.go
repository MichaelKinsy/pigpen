package pi_permission_system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// BenchmarkBashProgram is the latency and allocations of ParseBashProgram on the inputs of port/bashprogram-diff/bench-inputs.json,
// the same commands bench.test.ts times under the original, over a real temporary project directory.
func BenchmarkBashProgram(b *testing.B) {
	raw, err := os.ReadFile("../../port/bashprogram-diff/bench-inputs.json")
	if err != nil {
		b.Fatal(err)
	}
	var inputs map[string]string
	if err := json.Unmarshal(raw, &inputs); err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.ts"), []byte("x"), 0o644); err != nil {
		b.Fatal(err)
	}
	bashparse.WarmBashParser()
	normalizer := NewPathNormalizer(PosixPathFlavor, root)
	for _, name := range []string{"short", "cd-redirect", "chain", "heredoc", "substitution"} {
		command := inputs[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := ParseBashProgram(command, normalizer, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
