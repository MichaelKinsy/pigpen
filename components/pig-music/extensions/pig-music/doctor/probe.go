package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// probeTrack is a long-lived, widely mirrored upload used only to see that resolving and streaming work.
const probeTrack = "https://music.youtube.com/watch?v=dQw4w9WgXcQ"

const probeBytes = 64 << 10

// ProbeStream resolves the probe track with yt-dlp and reads its first 64 KiB with a range request. A 403 comes back as
// an error that says so, which Explain recognises.
func ProbeStream(ctx context.Context, ytdlpPath, jsArg string) error {
	return probeStream(ctx, ytdlp.ExecRunner, http.DefaultClient, ytdlpPath, jsArg, probeTrack)
}

func probeStream(ctx context.Context, run ytdlp.Runner, client *http.Client, ytdlpPath, jsArg, track string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := []string{"--ignore-config", "--no-playlist", "-f", "bestaudio", "-g", track}
	if jsArg != "" {
		args = append([]string{"--js-runtimes", jsArg}, args...)
	}
	stdout, stderr, err := run(ctx, ytdlpPath, args)
	url := firstLine(stdout)
	if err != nil || !strings.HasPrefix(url, "http") {
		if err == nil {
			err = errNoURL
		}
		return fmt.Errorf("%s: %w", firstNonEmpty(lastLine(stderr), err.Error()), err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", probeBytes-1))
	resp, err := client.Do(req)
	if err != nil {
		// The URL is signed and names this machine's public IP address: report what failed, not where.
		var uerr *neturl.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("reading the stream: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("reading the stream: HTTP Error %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	n, err := io.CopyN(io.Discard, resp.Body, probeBytes)
	if err != nil {
		return fmt.Errorf("the stream gave %d of %d bytes: %w", n, probeBytes, err)
	}
	return nil
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
