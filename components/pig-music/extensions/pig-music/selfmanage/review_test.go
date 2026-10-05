package selfmanage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `yt-dlp -U` sets up yt-dlp's network layer, which loads the cookies a user's
// yt-dlp config names (--cookies-from-browser). pig-music does not touch browser
// cookies, so its automatic updates ignore that config.
func TestUpdateIgnoresTheUsersYtdlpConfig(t *testing.T) {
	in := installer(t, "linux", "amd64", nil)
	var got []string
	in.Run = func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		got = args
		return []byte("yt-dlp is up to date"), nil, nil
	}
	if _, _, err := in.Update(context.Background(), filepath.Join(in.Dir, "bin", "yt-dlp"), false); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "--ignore-config -U" {
		t.Fatalf("ran yt-dlp %v", got)
	}
}

// /music setup has no way to cancel a download, so a server that stops sending
// must not hold the command (and the user's PiG) forever.
func TestAStalledDownloadGivesUp(t *testing.T) {
	stall := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SHA2-256SUMS") {
			_, _ = w.Write([]byte(strings.Repeat("0", 64) + "  yt-dlp\n"))
			return
		}
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-stall
	}))
	defer srv.Close()
	defer close(stall)
	in := installer(t, "linux", "amd64", nil)
	in.Client = srv.Client()
	in.Stall = 300 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- in.Install(context.Background(), plan(srv, in, "yt-dlp", false, "yt-dlp")) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped sending") {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the download is still waiting on a server that stopped sending")
	}
}

// A server that never answers is given up too; one that is slow but keeps
// sending is not.
func TestASilentServerIsGivenUpButASlowOneIsNot(t *testing.T) {
	body := []byte(strings.Repeat("y", 10*1024))
	stall := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "SHA2-256SUMS"):
			_, _ = w.Write([]byte(sum(body) + "  slow\n" + sum(body) + "  silent\n"))
		case strings.HasSuffix(r.URL.Path, "/silent"):
			<-stall
		default:
			for i := 0; i < 10; i++ { // 1 s in all, 100 ms apart: never 300 ms without a byte
				_, _ = w.Write(body[i*1024 : (i+1)*1024])
				w.(http.Flusher).Flush()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}))
	defer srv.Close()
	defer close(stall)
	in := installer(t, "linux", "amd64", nil)
	in.Client = srv.Client()
	in.Stall = 300 * time.Millisecond
	if err := in.Install(context.Background(), plan(srv, in, "slow", false, "slow")); err != nil {
		t.Fatalf("a slow download that kept sending: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- in.Install(context.Background(), plan(srv, in, "silent", false, "silent")) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped sending") {
			t.Fatalf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the download is still waiting on a server that never answered")
	}
}
