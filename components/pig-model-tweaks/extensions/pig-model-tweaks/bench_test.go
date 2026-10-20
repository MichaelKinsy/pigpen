package pigmodeltweaks

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPerf measures round trips through the fake host: one command call and its
// notification. The host side is identical for any revision of the extension, so
// differences are the extension's own work. It runs only with PIGPEN_PERF=1 and
// prints ns/op, B/op and allocs/op per command:
//
//	PIGPEN_PERF=1 go test -run TestPerf -v
func TestPerf(t *testing.T) {
	if os.Getenv("PIGPEN_PERF") == "" {
		t.Skip("set PIGPEN_PERF=1 to measure")
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := StartHost(t, Extension(), HostOptions{})
	if failure := fire(h, "session_start", map[string]any{"reason": "resume"}); failure != "" {
		t.Fatal(failure)
	}
	for _, c := range []struct{ name, command, args string }{
		{"remember-status", rememberModelName, ""},
		{"guard-list", modelGuardName, "list"},
		{"guard-toggle", modelGuardName, "toggle"},
		{"lock-list", lockProviderName, "list"},
	} {
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if failure := h.Command(c.command, c.args); failure != "" {
					b.Fatal(failure)
				}
			}
		})
		t.Logf("%-16s %8d ns/op %7d B/op %5d allocs/op", c.name, result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
	}
	// A single-argument lock on an OpenRouter model: store update plus the
	// settings.json rewrite, the heaviest thing either command does.
	setActiveModel(h, map[string]any{"id": "vendor/m", "provider": "openrouter"})
	lock := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if failure := h.Command(lockProviderName, "deepseek"); failure != "" {
				b.Fatal(failure)
			}
		}
	})
	t.Logf("%-16s %8d ns/op %7d B/op %5d allocs/op", "lock-set", lock.NsPerOp(), lock.AllocedBytesPerOp(), lock.AllocsPerOp())
	register := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			_ = Extension()
		}
	})
	t.Logf("%-16s %8d ns/op %7d B/op %5d allocs/op", "construct", register.NsPerOp(), register.AllocedBytesPerOp(), register.AllocsPerOp())
}
