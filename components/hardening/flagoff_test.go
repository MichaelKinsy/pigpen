package hardening_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/audit"
	"github.com/MichaelKinsy/pigpen/components/hardening/headless"
	"github.com/MichaelKinsy/pigpen/components/hardening/policy"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func tree(t *testing.T, roots ...string) []string {
	t.Helper()
	var out []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			info, _ := d.Info()
			out = append(out, p+"|"+info.Mode().String()+"|"+info.ModTime().String()+"|"+strconv.FormatInt(info.Size(), 10))
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// With every flag off the library does nothing: it reads at most the profile path, and writes nothing, starts
// nothing, and leaves every emitter and guard inert.
func TestFlagOffDoesNothing(t *testing.T) {
	agent, cwd := t.TempDir(), t.TempDir()
	t.Chdir(cwd)
	for _, profileJSON := range []string{"", "{}", `{"headless":{"enabled":false},"audit":{"enabled":false}}`} {
		if profileJSON != "" {
			if err := os.MkdirAll(filepath.Join(agent, profile.Dir), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agent, profile.Dir, "warden.json"), []byte(profileJSON), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		before, goroutines := tree(t, agent, cwd), runtime.NumGoroutine()

		flags, err := profile.Load("warden", agent, func(string) string { return "" })
		if err != nil || flags.Any() {
			t.Fatalf("%q: %+v %v", profileJSON, flags, err)
		}
		if err := flags.Require(); err != nil {
			t.Fatal(err)
		}
		_ = flags.Status().String()

		// the helpers a Package calls on the flag-off path are inert
		var emitter audit.Emitter = audit.New(flags.Audit, os.Stderr)
		emitter.Emit(audit.Event{Event: audit.EventToolCall, Package: "warden", Outcome: audit.OutcomeOK, Reason: audit.ReasonNone})
		if err := headless.Guard(context.Background(), flags, "warden", "gate-confirm"); err != nil {
			t.Fatal(err)
		}
		if !headless.UI(flags, true) || headless.UI(flags, false) {
			t.Fatal("headless changed the host's HasUI")
		}
		_ = policy.FailClosed(nil)

		time.Sleep(10 * time.Millisecond)
		if after := tree(t, agent, cwd); len(after) != len(before) {
			t.Fatalf("%q: files changed:\n%v\n%v", profileJSON, before, after)
		} else {
			for i := range after {
				if after[i] != before[i] {
					t.Fatalf("%q: files changed:\n%v\n%v", profileJSON, before, after)
				}
			}
		}
		if n := runtime.NumGoroutine(); n != goroutines {
			t.Fatalf("%q: goroutines %d -> %d", profileJSON, goroutines, n)
		}
	}
}
