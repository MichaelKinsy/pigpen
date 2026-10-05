package pig_music_test

import (
	"os/exec"
	"strings"
	"testing"
)

// Review of M9: the extension is fused into the pig-with-batteries Binary, which was a static executable. The extension
// never plays audio itself (the native engine's sound comes from a separate `pigmusic serve`), but it linked the audio
// device library anyway (oto, through cli -> native.Serve), and oto's purego turns a CGO_ENABLED=0 build into a dynamically
// linked one that needs glibc's loader (/lib64/ld-linux-x86-64.so.2, libc, libdl, libpthread): the whole Binary then fails
// to start on musl, in a static container or under Termux. The audio device belongs to the pigmusic command only.
func TestTheExtensionLinksNoAudioDeviceLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"github.com/ebitengine/purego", "github.com/ebitengine/oto", "github.com/jfreymuth/pulse"} {
			if strings.HasPrefix(dep, banned) {
				t.Errorf("the extension links %s (an audio device library that makes the Binary dynamically linked)", dep)
			}
		}
	}
}
