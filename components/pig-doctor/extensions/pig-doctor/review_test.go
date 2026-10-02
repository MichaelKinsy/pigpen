package pig_doctor_test

import (
	"testing"

	pigdoctor "github.com/MichaelKinsy/pigpen/pig-doctor"
)

// Inside pig (a fused Piglet Binary runs the extension in pig's own process) the
// process the doctor runs in is a running pig: it must count as one, so its
// executable, directory and agent directory are treated as in use.
func TestExtensionDoesNotHideTheHostPig(t *testing.T) {
	root := t.TempDir()
	o, err := pigdoctor.EnvLoader(func() []string { return []string{"HOME=" + root} })()
	if err != nil {
		t.Fatal(err)
	}
	if o.SelfPID != 0 {
		t.Errorf("the extension excludes its own process (%d) from the running pig processes", o.SelfPID)
	}
}
