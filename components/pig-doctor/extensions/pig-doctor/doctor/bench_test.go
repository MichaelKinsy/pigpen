//go:build unix

package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Check walks the PiG home: settings, extension caches, agent directories. It runs only when the user runs
// /doctor, so the interesting question is how it grows with a large home (caches with many small files).
//
//	go test -run xxx -bench . -benchmem
func benchHome(b *testing.B, files int) Options {
	b.Helper()
	root := b.TempDir()
	home, user := filepath.Join(root, "pig"), filepath.Join(root, "user")
	for _, d := range []string{filepath.Join(home, "agent"), user} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte(`{"packages":["npm:pig-thing"]}`), 0o600); err != nil {
		b.Fatal(err)
	}
	for _, cache := range []string{"state/pigsdk", "cache/ext-go", "cache/piglet-cells", "sessions"} {
		dir := filepath.Join(home, cache)
		for i := 0; i < files; i++ {
			sub := filepath.Join(dir, fmt.Sprintf("d%02d", i%20))
			if i < 20 {
				if err := os.MkdirAll(sub, 0o755); err != nil {
					b.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("f%d.dat", i)), []byte("x"), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	now := time.Now()
	return Options{
		Home: home, AgentDir: filepath.Join(home, "agent"), UserHome: user,
		Env: []string{"PATH=/usr/bin:/bin", "HOME=" + user}, Now: func() time.Time { return now }, SelfPID: 999999,
		Procs: &fakeProcs{},
		Exec:  func([]string, string, ...string) (string, error) { return "go version go1.26.1 linux/amd64", nil },
	}
}

func BenchmarkCheck(b *testing.B) {
	for _, files := range []int{100, 2000} {
		b.Run(fmt.Sprintf("%dfilesPerDir", files), func(b *testing.B) {
			o := benchHome(b, files)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Check(o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
