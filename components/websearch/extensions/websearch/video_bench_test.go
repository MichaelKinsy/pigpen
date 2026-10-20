package websearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Benchmarks of the video routes with canned programs and a canned Gemini API (no process is
// started, no socket opened), compared with the original's by port/bench-video.mjs, which patches
// execFileSync and fetch the same way.

func benchVideoEnv(b *testing.B) string {
	b.Helper()
	home := b.TempDir()
	agent := filepath.Join(home, "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		b.Fatal(err)
	}
	for k, v := range map[string]string{"HOME": home, "USERPROFILE": home, "PIG_HOME": home + "/pig", "PIG_CODING_AGENT_DIR": agent, "GEMINI_API_KEY": "synthetic-gemini-key"} {
		b.Setenv(k, v)
	}
	ResetCaches()
	b.Cleanup(ResetCaches)
	if err := os.WriteFile(filepath.Join(agent, "web-search.json"), []byte(`{"image":{"enabled":true}}`), 0o600); err != nil {
		b.Fatal(err)
	}
	clip := filepath.Join(agent, "clip.mp4")
	if err := os.WriteFile(clip, []byte("synthetic media"), 0o600); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(SetMediaRunner(func(_ context.Context, name string, _ []string, _ mediaOptions) ([]byte, error) {
		switch name {
		case "yt-dlp":
			return []byte("125.5\nhttps://stream.example/video.mp4\n"), nil
		case "ffprobe":
			return []byte("75.25\n"), nil
		}
		return []byte("JPEGBYTES"), nil
	}))
	return clip
}

func BenchmarkVideoExtract(b *testing.B) {
	clip := benchVideoEnv(b)
	const yt = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	for _, c := range []struct {
		name, url string
		o         ExtractOptions
	}{
		{"youtube-frames", yt, ExtractOptions{Frames: 3, Lookup: publicLookup}},
		{"youtube-frame", yt, ExtractOptions{Timestamp: "1:23", Lookup: publicLookup}},
		{"local-frames", clip, ExtractOptions{Frames: 4}},
		{"local-range", clip, ExtractOptions{Timestamp: "0:10-1:00"}},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if r := ExtractContent(context.Background(), c.url, c.o); r.Error != nil {
					b.Fatal(*r.Error)
				}
			}
		})
	}
}
