package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// probeTrack is a long-lived, widely mirrored upload used only to see that resolving and streaming work.
const probeTrack = "https://music.youtube.com/watch?v=dQw4w9WgXcQ"

const probeBytes = 64 << 10

// probeMaxHold is the longest the probe waits for a stream YouTube does not serve yet (yt-dlp's available_at): what the player's
// yt-dlp shim waits (package prefetch), so that the probe fails where playback would.
const probeMaxHold = time.Minute

// ProbeStream resolves the probe track with yt-dlp and reads its first 64 KiB with a range request, once YouTube serves it.
// A 403 comes back as an error that says so, which Explain recognises.
func ProbeStream(ctx context.Context, ytdlpPath, jsArg string) error {
	return probeStream(ctx, ytdlp.ExecRunner, http.DefaultClient, ytdlpPath, jsArg, probeTrack)
}

func probeStream(ctx context.Context, run ytdlp.Runner, client *http.Client, ytdlpPath, jsArg, track string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second+probeMaxHold)
	defer cancel()
	args := []string{"--ignore-config", "--no-playlist", "-f", "bestaudio", "-J", track}
	if jsArg != "" {
		args = append([]string{"--js-runtimes", jsArg}, args...)
	}
	stdout, stderr, err := run(ctx, ytdlpPath, args)
	url, at := probedStream(stdout)
	if err != nil || !strings.HasPrefix(url, "http") {
		if err == nil {
			err = errNoURL
		}
		return fmt.Errorf("%s: %w", firstNonEmpty(lastLine(stderr), err.Error()), err)
	}
	// yt-dlp 2026.10.10 and later resolve with tv_samsung, whose stream YouTube refuses with 403 until a pre-roll ad could have
	// been skipped (5 s after the resolve, measured); the player waits for it too.
	if wait := time.Until(at); wait > probeMaxHold {
		return fmt.Errorf("YouTube holds the stream back for %s (available_at), longer than the player waits", wait.Round(time.Second))
	} else if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for the stream to become available: %w", ctx.Err())
		}
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

// probedStream reads yt-dlp's -J listing: the URL of the stream it chose (its first requested download, or the listing's own
// URL) and when YouTube serves it, the latest available_at of the requested downloads (zero when yt-dlp gives none). The other
// formats do not count: they come from other clients, which may be held back longer.
func probedStream(out []byte) (url string, at time.Time) {
	type stream struct {
		URL         string  `json:"url"`
		AvailableAt float64 `json:"available_at"`
	}
	var v struct {
		stream
		Requested []stream `json:"requested_downloads"`
	}
	if json.Unmarshal(out, &v) != nil {
		return "", time.Time{}
	}
	chosen := []stream{v.stream}
	if len(v.Requested) > 0 {
		chosen = v.Requested
	}
	var latest float64
	for _, s := range chosen {
		latest = max(latest, s.AvailableAt)
	}
	if latest > 0 {
		at = time.Unix(int64(math.Ceil(latest)), 0)
	}
	return chosen[0].URL, at
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
