package doctor

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A user's yt-dlp config (~/.config/yt-dlp/config, yt-dlp.conf) may say
// --cookies-from-browser: yt-dlp then reads the browser's cookies for any run
// that sets up its network layer, which `-v` does to print its request handlers
// and a resolve certainly does. pig-music must not touch browser cookies (that is
// milestone 5, behind the owner's consent), and the doctor must see the yt-dlp the
// Source runs, which ignores the user's config.
func TestTheDoctorRunsYtdlpWithoutTheUsersConfig(t *testing.T) {
	w := healthy()
	w.ytVerb = verboseNone
	w.programs["node"] = "/usr/bin/node"
	w.runtimes["/usr/bin/node"] = "v24.19.0"
	env := w.env()
	var calls [][]string
	var mu sync.Mutex // the doctor runs some programs together
	inner := env.Run
	env.Run = func(ctx context.Context, bin string, args []string) ([]byte, []byte, error) {
		if strings.HasSuffix(bin, "yt-dlp") {
			mu.Lock()
			calls = append(calls, args)
			mu.Unlock()
		}
		return inner(ctx, bin, args)
	}
	Run(context.Background(), Config{}, env, Options{})
	verboseRuns := 0
	for _, args := range calls {
		joined := strings.Join(args, " ")
		if joined == "--version" {
			continue
		}
		verboseRuns++
		if !strings.Contains(" "+joined+" ", " --ignore-config ") {
			t.Errorf("yt-dlp %s reads the user's config", joined)
		}
	}
	if verboseRuns < 2 {
		t.Fatalf("expected yt-dlp -v twice (as found, then with node), got %v", calls)
	}
}

func TestTheProbeResolvesWithoutTheUsersConfig(t *testing.T) {
	var got []string
	run := func(_ context.Context, _ string, args []string) ([]byte, []byte, error) {
		got = args
		return nil, []byte("ERROR: nope"), context.Canceled
	}
	_ = probeStream(context.Background(), run, &http.Client{}, "yt-dlp", "", "u")
	if !strings.Contains(" "+strings.Join(got, " ")+" ", " --ignore-config ") {
		t.Fatalf("the probe ran yt-dlp %v", got)
	}
}

// A stream URL is signed and carries the listener's public IP address. A network
// error reading it must not print it (the doctor's report goes to the terminal,
// PiG's notifications and bug reports).
func TestAProbeNetworkErrorDoesNotPrintTheSignedStreamURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now: the read fails to connect
	stream := "http://" + addr + "/videoplayback?expire=1&ip=203.0.113.7&sig=SECRETSIG&lsig=SECRETL"
	run := func(context.Context, string, []string) ([]byte, []byte, error) {
		return []byte(stream + "\n"), nil, nil
	}
	err = probeStream(context.Background(), run, &http.Client{}, "yt-dlp", "", "u")
	if err == nil {
		t.Fatal("no error from a closed port")
	}
	for _, secret := range []string{"203.0.113.7", "SECRETSIG", "SECRETL", "videoplayback?"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error shows %q: %v", secret, err)
		}
	}
}
