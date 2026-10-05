package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func env(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }

func found(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, x := range names {
			if x == n {
				return "/usr/bin/" + n, nil
			}
		}
		return "", errors.New("not found")
	}
}

func deps(goos string, e map[string]string, look func(string) (string, error)) Deps {
	return Deps{GOOS: goos, Getenv: env(e), LookPath: look, ServeFound: func(music.Settings) error { return nil }}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"": Auto, "auto": Auto, "AUTO": Auto, " mpv ": MPV, "native": Native} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseMode("vlc"); err == nil || !strings.Contains(err.Error(), "auto, mpv or native") {
		t.Fatalf("err = %v", err)
	}
}

func TestAutoPrefersMPVWhenHealthy(t *testing.T) {
	c, err := Select(context.Background(), music.Settings{}, deps("linux", nil, found("mpv", "yt-dlp")))
	if err != nil || c.Kind != MPVEngine || c.Mode != Auto || c.Fallback {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestAutoFallsBackToNative(t *testing.T) {
	for name, tc := range map[string]struct {
		look func(string) (string, error)
		want string
	}{
		"no mpv":    {found("yt-dlp"), "mpv"},
		"no yt-dlp": {found("mpv"), "yt-dlp"},
		"neither":   {found(), "mpv"},
	} {
		c, err := Select(context.Background(), music.Settings{}, deps("linux", nil, tc.look))
		if err != nil || c.Kind != NativeEngine || !c.Fallback || !strings.Contains(c.Reason, tc.want) {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
}

func TestAutoFallsBackWhenTheHealthHookSaysSo(t *testing.T) {
	d := deps("linux", nil, found("mpv", "yt-dlp"))
	d.MPVHealthy = func(context.Context, music.Settings) error {
		return errors.New("yt-dlp 2024.04.09 is too old and no JavaScript runtime was found")
	}
	c, err := Select(context.Background(), music.Settings{}, d)
	if err != nil || c.Kind != NativeEngine || !strings.Contains(c.Reason, "too old") {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestExplicitChoicesAreHonoured(t *testing.T) {
	c, err := Select(context.Background(), music.Settings{Engine: "mpv"}, deps("linux", nil, found()))
	if err != nil || c.Kind != MPVEngine || c.Fallback || c.Warning == "" {
		t.Fatalf("mpv forced without mpv: %+v %v (must stay mpv and warn, not switch silently)", c, err)
	}
	c, err = Select(context.Background(), music.Settings{Engine: "native"}, deps("linux", nil, found("mpv", "yt-dlp")))
	if err != nil || c.Kind != NativeEngine || c.Fallback {
		t.Fatalf("native forced: %+v %v", c, err)
	}
	// The environment wins over the settings file.
	c, err = Select(context.Background(), music.Settings{Engine: "mpv"}, deps("linux", map[string]string{"PIG_MUSIC_ENGINE": "native"}, found("mpv", "yt-dlp")))
	if err != nil || c.Kind != NativeEngine {
		t.Fatalf("env override: %+v %v", c, err)
	}
	if _, err := Select(context.Background(), music.Settings{Engine: "winamp"}, deps("linux", nil, found())); err == nil {
		t.Fatal("a bad engine name must be an error")
	}
}

func TestTermuxIsMPVOnly(t *testing.T) {
	for name, d := range map[string]Deps{
		"android": deps("android", nil, found("mpv", "yt-dlp")),
		"termux":  deps("linux", map[string]string{"TERMUX_VERSION": "0.118"}, found("mpv", "yt-dlp")),
		"prefix":  deps("linux", map[string]string{"PREFIX": "/data/data/com.termux/files/usr"}, found("mpv", "yt-dlp")),
	} {
		c, err := Select(context.Background(), music.Settings{}, d)
		if err != nil || c.Kind != MPVEngine {
			t.Errorf("%s auto: %+v %v", name, c, err)
		}
		_, err = Select(context.Background(), music.Settings{Engine: "native"}, d)
		if err == nil || !strings.Contains(err.Error(), "Termux") || !strings.Contains(err.Error(), "pkg install mpv") {
			t.Errorf("%s native: %v (must say why and what to install)", name, err)
		}
	}
	// Termux without mpv: auto does not fall back to a native engine that cannot run there.
	c, err := Select(context.Background(), music.Settings{}, deps("android", nil, found()))
	if err != nil || c.Kind != MPVEngine || c.Warning == "" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestNativeNeedsItsPlayerProgram(t *testing.T) {
	d := deps("linux", nil, found())
	d.ServeFound = func(music.Settings) error { return errors.New("pigmusic was not found") }
	_, err := Select(context.Background(), music.Settings{}, d)
	if err == nil || !strings.Contains(err.Error(), "mpv") || !strings.Contains(err.Error(), "pigmusic was not found") {
		t.Fatalf("both engines unusable must say both: %v", err)
	}
	_, err = Select(context.Background(), music.Settings{Engine: "native"}, d)
	if err == nil || !strings.Contains(err.Error(), "pigmusic was not found") {
		t.Fatalf("%v", err)
	}
}

func TestConfiguredPathsCountAsFound(t *testing.T) {
	c, err := Select(context.Background(), music.Settings{MPVPath: "/opt/mpv", YtdlpPath: "/opt/yt-dlp"}, deps("linux", nil, func(n string) (string, error) {
		if strings.HasPrefix(n, "/opt/") {
			return n, nil
		}
		return "", errors.New("no")
	}))
	if err != nil || c.Kind != MPVEngine {
		t.Fatalf("%+v %v", c, err)
	}
}

// The doctor's health hook agrees with Select: on Termux the native engine is not available, whatever GOOS the binary was built for.
func TestNativeHealthOnTermuxIsMPVOnly(t *testing.T) {
	for name, d := range map[string]Deps{
		"android": deps("android", nil, found()),
		"termux":  deps("linux", map[string]string{"TERMUX_VERSION": "0.118"}, found()),
		"prefix":  deps("linux", map[string]string{"PREFIX": "/data/data/com.termux/files/usr"}, found()),
	} {
		r := NativeHealth(context.Background(), music.Settings{}, d, false)
		if r.OK() || len(r.Checks) == 0 || r.Checks[0].Name != "platform" || r.Checks[0].OK || !strings.Contains(r.Checks[0].Fix, "pkg install mpv") {
			t.Errorf("%s: %s", name, r.String())
		}
	}
}
