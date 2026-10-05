package pig_music

import (
	"os/exec"
	"strings"
	"testing"
)

// The extension is loaded by every PiG that selects it, so it must stay small: the native engine (WaxTap with its JavaScript
// engine, WaxFlow's decoders, oto) lives only in the pigmusic program, which the extension talks to over a socket and runs as a
// helper. Linking any of it back in costs the batteries Binary about 15 MB and 10-20 MiB of resident memory.
func TestTheExtensionDoesNotLinkTheNativeEngine(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	for _, pkg := range []string{".", "./cli", "./engine", "./native"} {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			t.Fatalf("go list -deps %s: %v\n%s", pkg, err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, heavy := range []string{"github.com/colespringer/", "github.com/dop251/goja", "github.com/ebitengine/", "github.com/jfreymuth/", "google.golang.org/protobuf", "pig-music/native/wax"} {
				if strings.Contains(dep, heavy) {
					t.Errorf("%s links %s (the native engine belongs to cmd/pigmusic only)", pkg, dep)
				}
			}
		}
	}
}
