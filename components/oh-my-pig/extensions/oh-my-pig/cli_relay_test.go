package ohmypig

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/relay"
)

func isolateCLIRelayEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PIG_HOME", filepath.Join(tmp, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(tmp, ".pig", "agent"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	return tmp
}

func TestBrowserRelayCLIHelp(t *testing.T) {
	isolateCLIRelayEnv(t)
	hook := clihook.Registered()
	if hook == nil {
		t.Fatal("clihook not registered")
	}
	for _, args := range [][]string{
		{"browser-relay", "--help"},
		{"browser-relay", "-h"},
		{"browser-relay"},
		{"browser-relay", "install", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		res := hook(clihook.Env{
			Args:   args,
			Stdout: &stdout,
			Stderr: &stderr,
			Getenv: os.Getenv,
		})
		if !res.Handled || res.Exit != 0 {
			t.Fatalf("%v: Handled=%v Exit=%d stderr=%q", args, res.Handled, res.Exit, stderr.String())
		}
		out := stdout.String()
		for _, want := range []string{"oh-my-pig browser-relay", "install | sync | native-host", "--dir", "--no-chrome", "--profile"} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: help output missing %q:\n%s", args, want, out)
			}
		}
	}
}

func TestBrowserRelayCLIRefusesProfileFlag(t *testing.T) {
	isolateCLIRelayEnv(t)
	hook := clihook.Registered()
	for _, args := range [][]string{
		{"browser-relay", "install", "--profile", "Profile 1"},
		{"browser-relay", "install", "--profile=Default"},
	} {
		var stdout, stderr bytes.Buffer
		res := hook(clihook.Env{
			Args:   args,
			Stdout: &stdout,
			Stderr: &stderr,
			Getenv: os.Getenv,
		})
		if !res.Handled || res.Exit == 0 {
			t.Fatalf("%v: expected non-zero handled refusal, got Handled=%v Exit=%d", args, res.Handled, res.Exit)
		}
		msg := strings.ToLower(stderr.String())
		if !strings.Contains(msg, "--profile") || !strings.Contains(msg, "unattended") {
			t.Fatalf("%v: stderr should explain unattended install is not part of this port, got: %q", args, stderr.String())
		}
		if _, err := os.Stat(relay.InstallsFile()); !os.IsNotExist(err) {
			t.Fatalf("%v: refusal must not write installs.json", args)
		}
	}
}

func TestBrowserRelayCLIInstallAndSync(t *testing.T) {
	home := isolateCLIRelayEnv(t)
	hook := clihook.Registered()

	// Sync before any install prints guidance.
	var stdout, stderr bytes.Buffer
	res := hook(clihook.Env{
		Args:   []string{"browser-relay", "sync"},
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("sync empty: Handled=%v Exit=%d stderr=%q", res.Handled, res.Exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No oh-my-pig extension directories registered") {
		t.Fatalf("unexpected empty sync output: %q", stdout.String())
	}

	// Install into default dir (with --no-chrome accepted).
	stdout.Reset()
	stderr.Reset()
	res = hook(clihook.Env{
		Args:   []string{"browser-relay", "install", "--no-chrome"},
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("install: Handled=%v Exit=%d stderr=%q", res.Handled, res.Exit, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Installed the oh-my-pig extension to ",
		"open chrome://extensions, turn on Developer mode, click \"Load unpacked\" and pick ",
		"Its toolbar badge shows 'on' while its native host serves the relay.",
		"Once loaded, this extension can read and act on every site in the Chrome profile it is loaded into, with that profile's signed-in accounts, when an oh-my-pig session uses the relay.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("install output missing %q:\n%s", want, out)
		}
	}

	// Verify custom --dir installs and widens native host manifests.
	customDir := filepath.Join(home, "custom-ext")
	stdout.Reset()
	stderr.Reset()
	res = hook(clihook.Env{
		Args:   []string{"browser-relay", "install", "--dir", customDir},
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("install --dir: Handled=%v Exit=%d stderr=%q", res.Handled, res.Exit, stderr.String())
	}

	// Verify installs.json has both dirs.
	reg, err := relay.ReadRelayInstallRegistry(relay.InstallsFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Installs) != 2 {
		t.Fatalf("expected 2 registered installs, got %#v", reg.Installs)
	}

	// Verify native host manifest on current OS admits both IDs.
	var manifestPath string
	switch runtime.GOOS {
	case "darwin":
		manifestPath = filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts", relay.NativeHostName+".json")
	case "linux":
		manifestPath = filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts", relay.NativeHostName+".json")
	case "windows":
		manifestPath = filepath.Join(relay.Root(), relay.NativeHostName+".json")
	}
	if manifestPath != "" {
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("read native host manifest %s: %v", manifestPath, err)
		}
		var m struct {
			Name           string   `json:"name"`
			AllowedOrigins []string `json:"allowed_origins"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m.Name != relay.NativeHostName {
			t.Fatalf("manifest name = %q, want %q", m.Name, relay.NativeHostName)
		}
		if len(m.AllowedOrigins) != 2 {
			t.Fatalf("expected 2 allowed_origins, got %v", m.AllowedOrigins)
		}
	}

	// Sync updates both registered directories.
	if _, err := relay.WriteRelayExtension(customDir, "0.0.1", nil); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	res = hook(clihook.Env{
		Args:   []string{"browser-relay", "sync"},
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("sync: Handled=%v Exit=%d stderr=%q", res.Handled, res.Exit, stderr.String())
	}
	syncOut := stdout.String()
	if !strings.Contains(syncOut, ": up to date") || !strings.Contains(syncOut, ": written") {
		t.Fatalf("expected one up to date and one written in sync output:\n%s", syncOut)
	}
}

func TestBrowserRelayCLINativeHostDispatch(t *testing.T) {
	isolateCLIRelayEnv(t)
	hook := clihook.Registered()

	// Empty EOF stdin exits cleanly with 0 (Chrome closed port immediately).
	var stdout, stderr bytes.Buffer
	res := hook(clihook.Env{
		Args:   []string{"browser-relay", "native-host", "chrome-extension://abcdefghijklmnopabcdefghijklmnop/"},
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if !res.Handled {
		t.Fatal("native-host must be handled by CLI hook")
	}
	if res.Exit != 0 {
		t.Fatalf("unexpected native-host EOF result: exit=%d stderr=%q", res.Exit, stderr.String())
	}

	// A real `link` frame over stdin receives a `linked` frame on stdout and exits 0 on EOF.
	extID := "abcdefghijklmnopabcdefghijklmnop"
	linkFrame, err := relay.EncodeNativeFrame(relay.ToNativeHost{T: "link", ExtensionID: &extID})
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	res = hook(clihook.Env{
		Args:   []string{"browser-relay", "native-host", "chrome-extension://" + extID + "/"},
		Stdin:  bytes.NewReader(linkFrame),
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if !res.Handled || res.Exit != 0 {
		t.Fatalf("native-host link dispatch failed: handled=%v exit=%d stderr=%q", res.Handled, res.Exit, stderr.String())
	}
}

func TestCLIHookPassesOtherCommandsThrough(t *testing.T) {
	isolateCLIRelayEnv(t)
	hook := clihook.Registered()

	args := []string{"-p", "hello"}
	var stdout, stderr bytes.Buffer
	res := hook(clihook.Env{
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: os.Getenv,
	})
	if res.Handled {
		t.Fatalf("non-browser-relay command must not be handled: %#v", res)
	}
	if len(res.Args) != len(args) || res.Args[0] != args[0] || res.Args[1] != args[1] {
		t.Fatalf("passthrough args = %v, want %v", res.Args, args)
	}
}
