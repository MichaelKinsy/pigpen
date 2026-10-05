package native

import (
	"strings"
	"testing"
)

// Review of M9: a program that links the native package without its audio device (the extension) refuses a device output
// with a message that says where the sound comes from, instead of failing obscurely or playing to nothing.
func TestWithoutADeviceTheOutputIsRefusedClearly(t *testing.T) {
	d := &ServeDeps{}
	d.fill()
	for _, kind := range []string{"", "auto", "oto"} {
		out, err := d.NewOutput(kind)
		if err == nil || out != nil {
			t.Fatalf("output %q: %v, %v", kind, out, err)
		}
		if !strings.Contains(err.Error(), "pigmusic") {
			t.Errorf("output %q: %v does not name the program that plays", kind, err)
		}
	}
	if out, err := d.NewOutput("null"); err != nil || out == nil {
		t.Fatalf("null output: %v, %v", out, err)
	}
}
