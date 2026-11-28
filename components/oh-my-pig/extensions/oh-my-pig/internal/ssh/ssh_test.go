package ssh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// ── Test helpers: fake `ssh` script (zero network, zero real ~/.ssh) ─────────

type fakeSSHFixture struct {
	binPath    string
	logFile    string
	stateDir   string
	remoteHome string
	manager    *Manager
	handler    *Handler
}

// newFakeSSH creates an executable POSIX sh script acting as `ssh`, executing
// remote commands locally with HOME pinned to remoteHome and logging every
// invocation's argv to logFile.
func newFakeSSH(t *testing.T, opts ...func(*fakeSSHOpts)) *fakeSSHFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake-ssh shell script requires Unix")
	}
	cfg := fakeSSHOpts{
		remoteOS:    "linux-gnu",
		remoteShell: "/bin/sh",
	}
	for _, fn := range opts {
		fn(&cfg)
	}

	root := t.TempDir()
	localHome := filepath.Join(root, "local-home")
	t.Setenv("PI_CONFIG_DIR", filepath.Join(localHome, ".omp"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(localHome, ".omp", "agent"))
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	remoteHome := cfg.remoteHome
	if remoteHome == "" {
		remoteHome = filepath.Join(root, "remote-home")
		if err := os.MkdirAll(remoteHome, 0o755); err != nil {
			t.Fatalf("mkdir remote-home: %v", err)
		}
	}
	logFile := filepath.Join(root, "ssh.log")
	binPath := filepath.Join(root, "fake-ssh")
	homeMapFile := filepath.Join(root, "homes.txt")
	if len(cfg.homesByTarget) > 0 {
		var lines []string
		for k, v := range cfg.homesByTarget {
			lines = append(lines, k+"="+v)
		}
		if err := os.WriteFile(homeMapFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write homes.txt: %v", err)
		}
	}

	// Fake ssh script parses OpenSSH flags (-G, -O check|exit, -M, -N, -f, -n, -o, -p, -i, --),
	// logs the exact argv, simulates ControlMaster state files, and runs the remote command
	// via /bin/sh -c with isolated HOME.
	script := fmt.Sprintf(`#!/bin/sh
set -eu
LOG_FILE=%s
STATE_DIR=%s
DEFAULT_HOME=%s
HOME_MAP=%s
CONFIG_CP=%s
SLEEP_SECS=%s
SLEEP_MARKER=%s
HOLD_CHILD_SECS=%s
FAIL_START=%s
REMOTE_OS=%s
REMOTE_SHELL=%s
DISABLE_TRANSFER_PROBE=%s

# Record full argv joined by '|' for assertions
{
	printf 'ARGV:'
	for a in "$@"; do
		printf ' %%s' "$a"
	done
	printf '\n'
} >> "$LOG_FILE"

if [ -n "$HOLD_CHILD_SECS" ]; then
	# Spawn a descendant that holds stdout/stderr open (regression for hang past timeout).
	sleep "$HOLD_CHILD_SECS" &
fi
if [ -n "$SLEEP_MARKER" ]; then
	touch "$SLEEP_MARKER"
fi
if [ -n "$SLEEP_SECS" ]; then
	sleep "$SLEEP_SECS"
fi

MODE="run"
OP=""
PORT=""
TARGET=""
CMD=""

while [ $# -gt 0 ]; do
	case "$1" in
		-G)
			MODE="G"
			shift
			;;
		-O)
			MODE="O"
			OP="$2"
			shift 2
			;;
		-M)
			MODE="master"
			shift
			;;
		-N|-f|-n)
			shift
			;;
		-o|-i)
			shift 2
			;;
		-p)
			PORT="$2"
			shift 2
			;;
		--)
			shift
			TARGET="${1:-}"
			shift || true
			CMD="${1:-}"
			break
			;;
		-*)
			echo "fake-ssh: unexpected option $1" >&2
			exit 255
			;;
		*)
			TARGET="$1"
			shift
			CMD="${1:-}"
			break
			;;
	esac
done

case "$MODE" in
	G)
		if [ -n "$CONFIG_CP" ]; then
			printf 'controlpath %%s\n' "$CONFIG_CP"
		else
			printf 'controlpath none\n'
		fi
		exit 0
		;;
	O)
		SAFE_T=$(printf '%%s' "$TARGET:$PORT" | tr -c 'A-Za-z0-9._-' '_')
		MARK="$STATE_DIR/master-$SAFE_T"
		if [ "$OP" = "check" ]; then
			if [ -f "$MARK" ]; then
				exit 0
			fi
			exit 255
		elif [ "$OP" = "exit" ]; then
			rm -f "$MARK"
			printf 'EXIT: %%s\n' "$TARGET" >> "$LOG_FILE"
			exit 0
		fi
		exit 1
		;;
	master)
		if [ -n "$FAIL_START" ]; then
			echo "$FAIL_START" >&2
			exit 255
		fi
		SAFE_T=$(printf '%%s' "$TARGET:$PORT" | tr -c 'A-Za-z0-9._-' '_')
		touch "$STATE_DIR/master-$SAFE_T"
		printf 'MASTER_START: %%s\n' "$TARGET" >> "$LOG_FILE"
		exit 0
		;;
esac

# Look up per-target HOME override if present.
EFF_HOME="$DEFAULT_HOME"
if [ -f "$HOME_MAP" ]; then
	KEY="$TARGET"
	if [ -n "$PORT" ]; then
		KEY="$TARGET:$PORT"
	fi
	FOUND=$(awk -F= -v k="$KEY" '$1 == k { print substr($0, length($1)+2); exit }' "$HOME_MAP" || true)
	if [ -z "$FOUND" ]; then
		FOUND=$(awk -F= -v k="$TARGET" '$1 == k { print substr($0, length($1)+2); exit }' "$HOME_MAP" || true)
	fi
	if [ -n "$FOUND" ]; then
		EFF_HOME="$FOUND"
	fi
fi

# Intercept host-info probe to simulate arbitrary remote OS/shell cleanly.
case "$CMD" in
	*PI_HOST_PROBE=*)
		if [ "$REMOTE_OS" = "windows" ]; then
			printf 'PI_HOST_PROBE=Windows_NT|C:\\Windows\\System32\\cmd.exe|\n'
			exit 0
		fi
		printf 'PI_HOST_PROBE=%%s|%%s|\n' "$REMOTE_OS" "$REMOTE_SHELL"
		exit 0
		;;
	*PI_TRANSFER_OK*)
		if [ "$DISABLE_TRANSFER_PROBE" = "1" ]; then
			exit 1
		fi
		;;
esac

HOME="$EFF_HOME"
export HOME
exec /bin/sh -c "$CMD"
`,
		QuotePosixArgument(logFile),
		QuotePosixArgument(stateDir),
		QuotePosixArgument(remoteHome),
		QuotePosixArgument(homeMapFile),
		QuotePosixArgument(cfg.configControlPath),
		QuotePosixArgument(cfg.sleepSecs),
		QuotePosixArgument(cfg.sleepMarker),
		QuotePosixArgument(cfg.holdChildSecs),
		QuotePosixArgument(cfg.failStart),
		QuotePosixArgument(cfg.remoteOS),
		QuotePosixArgument(cfg.remoteShell),
		QuotePosixArgument(cfg.disableTransferProbe),
	)
	if err := os.WriteFile(binPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake-ssh: %v", err)
	}

	ctrlDir := filepath.Join(root, "ctl")
	mgr := NewManager()
	mgr.SSHBin = binPath
	mgr.ControlDir = ctrlDir
	mgr.DisableDiskCache = true
	mgr.ConfigOpts = &ConfigOptions{
		Home:                localHome,
		DisableSystemConfig: true,
	}
	if cfg.configOpts != nil {
		mgr.ConfigOpts = cfg.configOpts
	}
	if cfg.platform != "" {
		mgr.Platform = cfg.platform
	}

	return &fakeSSHFixture{
		binPath:    binPath,
		logFile:    logFile,
		stateDir:   stateDir,
		remoteHome: remoteHome,
		manager:    mgr,
		handler:    &Handler{Manager: mgr},
	}
}

type fakeSSHOpts struct {
	remoteHome           string
	homesByTarget        map[string]string
	configControlPath    string
	sleepSecs            string
	sleepMarker          string
	holdChildSecs        string
	failStart            string
	remoteOS             string
	remoteShell          string
	disableTransferProbe string
	platform             string
	configOpts           *ConfigOptions
}

func (f *fakeSSHFixture) logText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.logFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		t.Fatalf("read log: %v", err)
	}
	return string(b)
}

func mustParseURL(t *testing.T, raw string) urls.URL {
	t.Helper()
	u, err := urls.Parse(raw)
	if err != nil {
		t.Fatalf("urls.Parse(%q): %v", raw, err)
	}
	return u
}

// =============================================================================
// 1. ControlPath budget & owner-private-dir tests (test/ssh-control-path.test.ts)
// =============================================================================

// upstream: test/ssh-control-path.test.ts: "SSH control-path budget (#9070)"
func TestControlPathBudget(t *testing.T) {
	profileDir := "/Users/arthur/.omp/profiles/upstream/ssh-control"
	if len(profileDir) != 48 {
		t.Fatalf("len(profileDir) = %d, want 48", len(profileDir))
	}
	if ControlPathFitsBudget(profileDir, "darwin") {
		t.Errorf("expected 48-byte profileDir to overflow darwin sun_path")
	}
	if !ControlPathFitsBudget("/Users/arthur/.omp/ssh-control", "darwin") {
		t.Errorf("expected default darwin dir to fit sun_path")
	}
	if !ControlPathFitsBudget(strings.Repeat("a", 40), "darwin") {
		t.Errorf("expected 40-byte dir to fit darwin")
	}
	if ControlPathFitsBudget(strings.Repeat("a", 41), "darwin") {
		t.Errorf("expected 41-byte dir to overflow darwin")
	}
	if ControlPathFitsBudget(strings.Repeat("a", 42), "darwin") {
		t.Errorf("expected 42-byte dir to overflow darwin")
	}
	if !ControlPathFitsBudget(strings.Repeat("a", 42), "linux") {
		t.Errorf("expected 42-byte dir to fit linux")
	}
	if !ControlPathFitsBudget(strings.Repeat("a", 44), "linux") {
		t.Errorf("expected 44-byte dir to fit linux")
	}
	if ControlPathFitsBudget(strings.Repeat("a", 45), "linux") {
		t.Errorf("expected 45-byte dir to overflow linux")
	}
}

// upstream: test/ssh-control-path.test.ts: "sshControlFallbackDir", "resolveSshControlDir"
func TestControlFallbackDirAndResolve(t *testing.T) {
	canonical := "/Users/arthur/.omp/profiles/upstream/ssh-control"
	a := ControlFallbackDir(canonical, 501, "/tmp")
	b := ControlFallbackDir(canonical, 501, "/tmp")
	if a != b {
		t.Fatalf("fallback dir not deterministic: %q != %q", a, b)
	}
	// Exact byte-for-byte hash parity with upstream omp test!
	if a != "/tmp/omp-5434354bc38f9a50fbbd" {
		t.Errorf("ControlFallbackDir = %q, want /tmp/omp-5434354bc38f9a50fbbd", a)
	}
	if !ControlPathFitsBudget(a, "darwin") {
		t.Errorf("fallback dir %q should fit darwin budget", a)
	}
	if ControlFallbackDir(canonical, 501, "/tmp") == ControlFallbackDir("/different/xdg/state/omp/ssh-control", 501, "/tmp") {
		t.Errorf("distinct canonical dirs collided")
	}
	if ControlFallbackDir(canonical, 501, "/tmp") == ControlFallbackDir(canonical, 502, "/tmp") {
		t.Errorf("distinct uids collided")
	}

	uid := uint32(501)
	if got := ResolveControlDir("/Users/arthur/.omp/ssh-control", "darwin", &uid, "/tmp"); got.Dir != "/Users/arthur/.omp/ssh-control" || got.Shared {
		t.Errorf("ResolveControlDir fits = %+v", got)
	}
	if got := ResolveControlDir(canonical, "darwin", &uid, "/tmp"); got.Dir != "/tmp/omp-5434354bc38f9a50fbbd" || !got.Shared {
		t.Errorf("ResolveControlDir overflow = %+v", got)
	}
	if got := ResolveControlDir(canonical, "windows", &uid, "/tmp"); got.Dir != canonical || got.Shared {
		t.Errorf("ResolveControlDir windows = %+v", got)
	}
	if got := ResolveControlDir(canonical, "darwin", nil, "/tmp"); got.Dir != canonical || got.Shared {
		t.Errorf("ResolveControlDir nil uid = %+v", got)
	}
}

// upstream: test/ssh-control-path.test.ts: "ownerPrivateDirError", "assertOwnerPrivateDir"
func TestOwnerPrivateDirGuard(t *testing.T) {
	uid := uint32(501)
	ok := OwnerPrivateDirStat{IsSymlink: false, IsDir: true, UID: 501, Mode: 0o700}
	if got := OwnerPrivateDirError(ok, &uid); got != "" {
		t.Errorf("expected ok, got %q", got)
	}
	if got := OwnerPrivateDirError(OwnerPrivateDirStat{IsSymlink: true, IsDir: true, UID: 501, Mode: 0o700}, &uid); got != "is a symlink" {
		t.Errorf("symlink err = %q", got)
	}
	if got := OwnerPrivateDirError(OwnerPrivateDirStat{IsDir: false, UID: 501, Mode: 0o700}, &uid); got != "is not a directory" {
		t.Errorf("non-dir err = %q", got)
	}
	if got := OwnerPrivateDirError(OwnerPrivateDirStat{IsDir: true, UID: 999, Mode: 0o700}, &uid); !strings.Contains(got, "not 501") {
		t.Errorf("foreign uid err = %q", got)
	}
	if got := OwnerPrivateDirError(OwnerPrivateDirStat{IsDir: true, UID: 501, Mode: 0o755}, &uid); !strings.Contains(got, "0700") {
		t.Errorf("loose mode err = %q", got)
	}
	if got := OwnerPrivateDirError(OwnerPrivateDirStat{IsDir: true, UID: 999, Mode: 0o700}, nil); got != "" {
		t.Errorf("nil expectedUID should skip owner check, got %q", got)
	}

	if runtime.GOOS == "windows" {
		return
	}
	scratch := t.TempDir()
	// Normalizes loose 0755 to 0700 in place.
	ctl := filepath.Join(scratch, "ctl")
	if err := os.Mkdir(ctl, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(ctl, 0o755)
	if err := AssertOwnerPrivateDir(ctl, "SSH control directory"); err != nil {
		t.Fatalf("AssertOwnerPrivateDir(ctl): %v", err)
	}
	fi, err := os.Stat(ctl)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode after normalize = %o, want 700", fi.Mode().Perm())
	}

	// Refuses symlinked final component (TOCTOU swap guard).
	victim := filepath.Join(scratch, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(scratch, "link")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := AssertOwnerPrivateDir(link, "SSH control directory"); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("expected symlink refusal, got %v", err)
	}

	// Refuses regular file.
	file := filepath.Join(scratch, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AssertOwnerPrivateDir(file, "SSH control directory"); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("expected non-directory refusal, got %v", err)
	}
}

// =============================================================================
// 2. Host discovery & ~/.ssh/config parsing (test/discovery/ssh-config.test.ts)
// =============================================================================

// upstream: test/discovery/ssh-config.test.ts: "lists concrete Host aliases in config order, following Include"
func TestDiscoverOpenSSHConfigAndShadowing(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CONFIG_DIR", filepath.Join(home, ".omp"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".omp", "agent"))

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(filepath.Join(sshDir, "config.d"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfgContent := strings.Join([]string{
		"# personal hosts",
		"Host web1 web2 *.corp !bastion",
		"  HostName 10.0.0.1",
		"Include config.d/*",
		"Match host foo exec true",
		"  User nobody",
		`Host=eq "quoted alias" # trailing comment`,
		"HOST db?\r",
		"host upper\r",
		"Host *",
		"  ServerAliveInterval 30",
	}, "\n")
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config.d", "b"), []byte("Host from-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config.d", "a"), []byte("Host from-a web1\nInclude ~/.ssh/config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Dotfile in config.d must be skipped by `Include config.d/*` (matching OpenSSH glob(3)).
	if err := os.WriteFile(filepath.Join(sshDir, "config.d", ".hidden-cfg"), []byte("Host from-hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, "dotfiles-host")
	if err := os.WriteFile(external, []byte("Host linked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(external, filepath.Join(sshDir, "config.d", "c")); err != nil {
			t.Fatal(err)
		}
	}

	hosts, _ := DiscoverHosts(ConfigOptions{
		Cwd:                 cwd,
		Home:                home,
		DisableSystemConfig: true,
		Providers:           []string{"ssh-config"},
	})
	var gotNames []string
	for _, h := range hosts {
		gotNames = append(gotNames, h.Name)
	}
	wantNames := []string{"web1", "web2", "from-a", "from-b", "linked", "eq", "quoted alias", "upper"}
	if runtime.GOOS == "windows" {
		wantNames = []string{"web1", "web2", "from-a", "from-b", "eq", "quoted alias", "upper"}
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("discovered names = %v, want %v", gotNames, wantNames)
	}
	if hosts[0].Host != "web1" || hosts[0].Username != "" {
		t.Errorf("expected plain alias entry for web1, got %+v", hosts[0])
	}
	for _, h := range hosts {
		if h.Name == "from-a" && h.Source.Path != filepath.Join(sshDir, "config.d", "a") {
			t.Errorf("from-a source path = %q", h.Source.Path)
		}
	}

	// Verify ~/.ssh/config was never modified (read-only guarantee).
	after, err := os.ReadFile(filepath.Join(sshDir, "config"))
	if err != nil || string(after) != cfgContent {
		t.Fatalf("~/.ssh/config content mutated")
	}

	// ssh.json entry shadows same-named ~/.ssh/config alias.
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host prod staging\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "ssh.json"), []byte(`{"hosts":{"prod":{"host":"10.0.0.9","username":"deploy","port":2222}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	merged, _ := DiscoverHosts(ConfigOptions{
		Cwd:                 cwd,
		Home:                home,
		DisableSystemConfig: true,
	})
	byName := map[string]Host{}
	for _, h := range merged {
		byName[h.Name] = h
	}
	if byName["prod"].Host != "10.0.0.9" || byName["prod"].Username != "deploy" || byName["prod"].Port != 2222 {
		t.Errorf("expected ssh.json to shadow prod, got %+v", byName["prod"])
	}
	if byName["staging"].Host != "staging" || byName["staging"].Source.Provider != "ssh-config" {
		t.Errorf("expected staging from ssh-config, got %+v", byName["staging"])
	}
}

// =============================================================================
// 3. Security invariants & hostile host validation
// =============================================================================

func TestBuildSSHTargetSecurity(t *testing.T) {
	badHosts := []string{
		"",
		"-oProxyCommand=touch /tmp/pwned",
		"-J",
		"host with spaces",
		"host\ninjection",
		"host;rm -rf /",
		"host$(id)",
		"host`id`",
		"host|cat",
	}
	for _, h := range badHosts {
		if _, err := BuildSSHTarget("", h); err == nil {
			t.Errorf("BuildSSHTarget('', %q) unexpectedly succeeded", h)
		}
	}
	badUsers := []string{
		"-oProxyCommand=id",
		"user name",
		"user;id",
		"user\x00name",
	}
	for _, u := range badUsers {
		if _, err := BuildSSHTarget(u, "prod.internal"); err == nil {
			t.Errorf("BuildSSHTarget(%q, prod.internal) unexpectedly succeeded", u)
		}
	}
	if got, err := BuildSSHTarget("deploy", "10.0.0.1"); err != nil || got != "deploy@10.0.0.1" {
		t.Errorf("BuildSSHTarget(deploy, 10.0.0.1) = (%q, %v)", got, err)
	}
	if got, err := BuildSSHTarget("", "::1"); err != nil || got != "::1" {
		t.Errorf("BuildSSHTarget('', ::1) = (%q, %v)", got, err)
	}
}

// =============================================================================
// 4. Protocol authority / path / error matrix (test/internal-urls/ssh-protocol.test.ts)
// =============================================================================

// upstream: test/internal-urls/ssh-protocol.test.ts (authority validation, overrides, bare host index)
func TestProtocolAuthorityAndHostIndex(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("PI_CONFIG_DIR", filepath.Join(home, ".omp"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".omp", "agent"))
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host plainalias\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sshJSON := map[string]any{
		"hosts": map[string]any{
			"icaro":       map[string]any{"host": "10.0.0.1", "username": "deploy", "description": "build box"},
			"alice@prod":  map[string]any{"host": "10.0.0.9", "username": "alice"},
			"[prod:2222]": map[string]any{"host": "prod.internal"},
			"prod:":       map[string]any{"host": "prod.internal"},
		},
	}
	rawJSON, _ := json.Marshal(sshJSON)
	if err := os.WriteFile(filepath.Join(cwd, "ssh.json"), rawJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.DisableDiskCache = true
	mgr.ConfigOpts = &ConfigOptions{Home: home, Cwd: cwd, DisableSystemConfig: true}
	h := &Handler{Manager: mgr}

	// Bare ssh:// lists configured hosts.
	res, err := h.Resolve(urls.Context{Cwd: cwd}, mustParseURL(t, "ssh://"))
	if err != nil {
		t.Fatalf("Resolve(ssh://): %v", err)
	}
	if res.Immutable == nil || !*res.Immutable {
		t.Errorf("host index should be immutable")
	}
	if res.SourcePath != "" {
		t.Errorf("host index must have empty SourcePath, got %q", res.SourcePath)
	}
	if !strings.Contains(res.Content, "[icaro](ssh://icaro/)") || !strings.Contains(res.Content, "deploy@10.0.0.1") || !strings.Contains(res.Content, "build box") {
		t.Errorf("unexpected host index content:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "[plainalias](ssh://plainalias/)") {
		t.Errorf("missing ssh-config host in index:\n%s", res.Content)
	}

	// Complete returns all configured hosts.
	comps, err := h.Complete(urls.Context{Cwd: cwd}, "")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(comps) != 5 {
		t.Errorf("Complete len = %d, want 5: %+v", len(comps), comps)
	}

	// Empty config message.
	emptyMgr := NewManager()
	emptyMgr.ConfigOpts = &ConfigOptions{Home: t.TempDir(), Cwd: t.TempDir(), DisableSystemConfig: true}
	emptyH := &Handler{Manager: emptyMgr}
	emptyRes, err := emptyH.Resolve(urls.Context{}, mustParseURL(t, "ssh://"))
	if err != nil || !strings.Contains(emptyRes.Content, "No SSH hosts are configured") {
		t.Fatalf("empty host index = (%+v, %v)", emptyRes, err)
	}

	// Host-less URL with path is rejected.
	if _, err := h.Resolve(urls.Context{Cwd: cwd}, mustParseURL(t, "ssh:///etc/hosts")); err == nil || !strings.Contains(err.Error(), "requires a host") {
		t.Errorf("expected host-less path error, got %v", err)
	}

	// Target resolution cases.
	if _, err := h.resolveTarget(mustParseURL(t, "ssh://user@icaro:22/x"), cwd); err == nil || !strings.Contains(err.Error(), "user/port overrides") {
		t.Errorf("expected override rejection on pinned host, got %v", err)
	}
	if _, err := h.resolveTarget(mustParseURL(t, "ssh://bob@alice%40prod/tmp/x"), cwd); err == nil || !strings.Contains(err.Error(), "user/port overrides") {
		t.Errorf("expected override rejection on encoded pinned alias, got %v", err)
	}
	if _, err := h.resolveTarget(mustParseURL(t, "ssh://alice%40prod:22/tmp/x"), cwd); err == nil || !strings.Contains(err.Error(), "user/port overrides") {
		t.Errorf("expected port override rejection on encoded pinned alias, got %v", err)
	}

	// Plain ~/.ssh/config alias passes user/port overrides through.
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://root@plainalias:2222/x"), cwd); err != nil || tgt.Host != "plainalias" || tgt.Username != "root" || tgt.Port != 2222 || tgt.Name != "root@plainalias:2222" {
		t.Errorf("plain alias override = (%+v, %v)", tgt, err)
	}

	// Encoded reserved-char aliases resolve to their configured target.
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://alice%40prod/etc/hostname"), cwd); err != nil || tgt.Host != "10.0.0.9" || tgt.Username != "alice" || tgt.Name != "alice@prod" {
		t.Errorf("encoded alice@prod = (%+v, %v)", tgt, err)
	}
	// Literal alice@prod is opaque user=alice host=prod (not the encoded alias).
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://alice@prod/etc/hostname"), cwd); err != nil || tgt.Host != "prod" || tgt.Username != "alice" {
		t.Errorf("literal alice@prod = (%+v, %v)", tgt, err)
	}
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://%5Bprod%3A2222%5D/etc/hostname"), cwd); err != nil || tgt.Host != "prod.internal" {
		t.Errorf("encoded bracketed alias = (%+v, %v)", tgt, err)
	}
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://prod%3A/etc/hostname"), cwd); err != nil || tgt.Host != "prod.internal" {
		t.Errorf("encoded colon alias = (%+v, %v)", tgt, err)
	}
	// IPv6 literal brackets stripped.
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://[::1]/etc/hostname"), cwd); err != nil || tgt.Host != "::1" {
		t.Errorf("IPv6 literal = (%+v, %v)", tgt, err)
	}
	// Percent-encoded user and host on opaque override.
	if tgt, err := h.resolveTarget(mustParseURL(t, "ssh://user%40corp@prod%2Dblue/etc/hostname"), cwd); err != nil || tgt.Username != "user@corp" || tgt.Host != "prod-blue" || tgt.Name != "user@corp@prod-blue" {
		t.Errorf("encoded override = (%+v, %v)", tgt, err)
	}

	// Malformed authorities rejected.
	badAuths := []struct {
		raw  string
		want string
	}{
		{"ssh://icaro:0/etc/hostname", "port 0"},
		{"ssh://prod:abc/etc", "invalid host or port"},
		{"ssh://prod:65536/etc", "invalid host or port"},
		{"ssh://prod:/etc/hosts", "empty port"},
		{"ssh://user@prod:/etc/hosts", "empty port"},
		{"ssh://[::1]:/etc/hosts", "empty port"},
		{"ssh://prod%2Dblue:/etc/hosts", "empty port"},
		{"ssh://u%2Dname@prod:/etc/hosts", "empty port"},
		{"ssh://user:pass@prod/etc/hosts", "password"},
		{"ssh://:pw@prod/etc/hosts", "password"},
		{"ssh://@prod/etc/hosts", "empty username"},
		{"ssh://@prod:22/etc/hosts", "empty username"},
		{"ssh://user:@prod/etc/hosts", "malformed authority"},
		{"ssh://:@prod/etc/hosts", "malformed authority"},
		{"ssh://prod%ZZ/etc/hosts", "percent-escape"},
		{"ssh://user%ZZ@prod/etc/hosts", "percent-escape"},
	}
	for _, tc := range badAuths {
		_, err := h.resolveTarget(mustParseURL(t, tc.raw), cwd)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
			t.Errorf("resolveTarget(%q) err = %v, want substring %q", tc.raw, err, tc.want)
		}
	}

	// Query and fragment rejections.
	if _, err := h.Resolve(urls.Context{Cwd: cwd}, mustParseURL(t, "ssh://h/tmp/a?draft")); err == nil || !strings.Contains(err.Error(), "query") {
		t.Errorf("expected query rejection, got %v", err)
	}
	if _, err := h.Resolve(urls.Context{Cwd: cwd}, mustParseURL(t, "ssh://h/tmp/a#draft")); err == nil || !strings.Contains(err.Error(), "fragment") {
		t.Errorf("expected fragment rejection, got %v", err)
	}
}

// =============================================================================
// 5. Fake-ssh end-to-end tests: read, write, dir list, HOME, binary, timeout,
//    hostile host, ControlMaster lifecycle & Windows fallback
// =============================================================================

func TestFakeSSH_ReadWriteListAndLineSelectors(t *testing.T) {
	fx := newFakeSSH(t)
	remoteDir := t.TempDir()

	// Seed remote files and subdirs.
	textPath := filepath.Join(remoteDir, "notes.md")
	if err := os.WriteFile(textPath, []byte("line1\n\tline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(remoteDir, "data.json")
	if err := os.WriteFile(jsonPath, []byte(`{"ok":true}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(remoteDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteDir, ".hidden"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	emptyDir := filepath.Join(remoteDir, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Register handler temporarily so urls.Split / urls.Resolve work end-to-end.
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)
	if err := urls.Register(fx.handler); err != nil {
		t.Fatalf("urls.Register: %v", err)
	}

	// Verify selector splitting with PortAuthority=true.
	if base, sel := urls.Split("ssh://box:2222"); base != "ssh://box:2222" || sel != "" {
		t.Errorf("Split port-only = (%q, %q)", base, sel)
	}
	if base, sel := urls.Split("ssh://box:2222" + textPath + ":2-3"); base != "ssh://box:2222"+textPath || sel != "2-3" {
		t.Errorf("Split with line range = (%q, %q)", base, sel)
	}

	// Read markdown file byte-exact, verify contentType & no SourcePath.
	res, err := urls.Resolve(urls.Context{}, "ssh://box"+textPath)
	if err != nil {
		t.Fatalf("Resolve(notes.md): %v", err)
	}
	if res.Content != "line1\n\tline2\nline3\n" || res.ContentType != "text/markdown" || res.SourcePath != "" {
		t.Errorf("unexpected notes.md resource: %+v", res)
	}

	// Read JSON file derives application/json.
	jres, err := urls.Resolve(urls.Context{}, "ssh://box"+jsonPath)
	if err != nil || jres.ContentType != "application/json" {
		t.Fatalf("Resolve(data.json) = (%+v, %v)", jres, err)
	}

	// Percent-encoded literal '?' in filename works.
	qFile := filepath.Join(remoteDir, "a?draft")
	if err := os.WriteFile(qFile, []byte("question\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	qres, err := urls.Resolve(urls.Context{}, "ssh://box"+filepath.Join(remoteDir, "a%3Fdraft"))
	if err != nil || qres.Content != "question\n" {
		t.Fatalf("Resolve(a%%3Fdraft) = (%+v, %v)", qres, err)
	}

	// Directory listing: dirs first, dotfiles included, immutable, no SourcePath.
	dres, err := urls.Resolve(urls.Context{}, "ssh://box"+remoteDir)
	if err != nil {
		t.Fatalf("Resolve(dir): %v", err)
	}
	if !dres.IsDirectory || dres.Immutable == nil || !*dres.Immutable || dres.SourcePath != "" {
		t.Fatalf("unexpected dir resource flags: %+v", dres)
	}
	lines := strings.Split(dres.Content, "\n")
	if len(lines) < 5 || lines[0] != "empty/" || lines[1] != "sub/" {
		t.Errorf("expected directories sorted first, got %v", lines)
	}
	if !slices.Contains(lines, ".hidden") || !slices.Contains(lines, "notes.md") {
		t.Errorf("expected dotfiles and regular files in listing, got %v", lines)
	}

	// SkipDirectoryListing skips listing command and returns empty content.
	sres, err := urls.Resolve(urls.Context{SkipDirectoryListing: true}, "ssh://box"+remoteDir)
	if err != nil || !sres.IsDirectory || sres.Content != "" {
		t.Fatalf("SkipDirectoryListing = (%+v, %v)", sres, err)
	}

	// Empty directory renders "(empty directory)".
	eres, err := urls.Resolve(urls.Context{}, "ssh://box"+emptyDir)
	if err != nil || !eres.IsDirectory || eres.Content != "(empty directory)" {
		t.Fatalf("empty dir = (%+v, %v)", eres, err)
	}

	// ReadDirectory returns structured DirEntries for grep/glob traversal.
	entries, ok, err := urls.ReadDirectory(urls.Context{}, "ssh://box"+remoteDir)
	if err != nil || !ok {
		t.Fatalf("ReadDirectory = (%+v, %v, %v)", entries, ok, err)
	}
	if len(entries) < 5 || !entries[0].IsDir || entries[0].Name != "empty" {
		t.Errorf("unexpected ReadDirectory entries: %+v", entries)
	}

	// Write new nested file (creates parent dirs, leaves no .omp-tmp).
	nestedPath := filepath.Join(remoteDir, "new", "nested", "out.txt")
	if _, err := urls.Write(urls.Context{}, "ssh://box"+nestedPath, "hello\n\tworld\n"); err != nil {
		t.Fatalf("Write(nested): %v", err)
	}
	gotBytes, err := os.ReadFile(nestedPath)
	if err != nil || string(gotBytes) != "hello\n\tworld\n" {
		t.Fatalf("written bytes = (%q, %v)", string(gotBytes), err)
	}
	dirEntries, _ := os.ReadDir(filepath.Dir(nestedPath))
	for _, de := range dirEntries {
		if strings.Contains(de.Name(), "omp-tmp") {
			t.Errorf("leftover temp file %q after write", de.Name())
		}
	}

	// Overwrite existing 0600 regular file in place preserves mode.
	secretPath := filepath.Join(remoteDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := urls.Write(urls.Context{}, "ssh://box"+secretPath, "new\n"); err != nil {
		t.Fatalf("Write(secret): %v", err)
	}
	fi, err := os.Stat(secretPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after overwrite = %o (err=%v), want 600", fi.Mode().Perm(), err)
	}

	// Trailing slash write is refused before touching filesystem.
	if _, err := urls.Write(urls.Context{}, "ssh://box"+filepath.Join(remoteDir, "newdir")+"/", "x"); err == nil || !strings.Contains(err.Error(), "directory path") {
		t.Errorf("expected trailing slash error, got %v", err)
	}

	// Writing to an existing directory is refused and leaves no temp file.
	if _, err := urls.Write(urls.Context{}, "ssh://box"+subDir, "x"); err == nil || !strings.Contains(err.Error(), "destination is a directory") {
		t.Errorf("expected directory write refusal, got %v", err)
	}
	parentEntries, _ := os.ReadDir(remoteDir)
	for _, de := range parentEntries {
		if strings.Contains(de.Name(), "omp-tmp") {
			t.Errorf("leftover temp file %q after refused dir write", de.Name())
		}
	}

	// Missing file propagates remote missing error (wrapped os.ErrNotExist).
	_, missingErr := urls.Resolve(urls.Context{}, "ssh://box"+filepath.Join(remoteDir, "does-not-exist"))
	if missingErr == nil || !errors.Is(missingErr, os.ErrNotExist) {
		t.Errorf("expected RemoteNotFoundError wrapping os.ErrNotExist, got %v", missingErr)
	}
}

func TestFakeSSH_BinaryOversizedAndSpecialFileRefusal(t *testing.T) {
	fx := newFakeSSH(t)
	remoteDir := t.TempDir()

	// Binary with NUL byte.
	elfPath := filepath.Join(remoteDir, "bin.elf")
	if err := os.WriteFile(elfPath, []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://box"+elfPath)); err == nil || !strings.Contains(err.Error(), "binary or non-UTF-8") {
		t.Errorf("expected binary refusal, got %v", err)
	}

	// Invalid UTF-8 byte past 8 KiB window.
	lateBad := make([]byte, 9001)
	for i := range 9000 {
		lateBad[i] = 'a'
	}
	lateBad[9000] = 0xff
	latePath := filepath.Join(remoteDir, "late.log")
	if err := os.WriteFile(latePath, lateBad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://box"+latePath)); err == nil || !strings.Contains(err.Error(), "binary or non-UTF-8") {
		t.Errorf("expected full-buffer UTF-8 refusal, got %v", err)
	}

	// File exceeding 1 MiB cap is refused.
	bigPath := filepath.Join(remoteDir, "big.txt")
	bigData := make([]byte, MaxTextFileBytes+1)
	for i := range bigData {
		bigData[i] = 'x'
	}
	if err := os.WriteFile(bigPath, bigData, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://box"+bigPath)); err == nil || !strings.Contains(err.Error(), "exceeds the 1 MiB limit") {
		t.Errorf("expected 1 MiB limit refusal, got %v", err)
	}

	// Special file (/dev/zero) is refused by stat without ever reading it.
	if _, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://box/dev/zero")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("expected special-file refusal for /dev/zero, got %v", err)
	}
}

// upstream: test/internal-urls/ssh-home-paths.test.ts
func TestFakeSSH_RemoteHomePaths(t *testing.T) {
	root := t.TempDir()
	homeA := filepath.Join(root, "remote account's home")
	homeB := filepath.Join(root, "bob-home")
	if err := os.MkdirAll(homeA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(homeB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeA, "sentinel.txt"), []byte("remote alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeB, "sentinel.txt"), []byte("remote bob\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "parent.txt"), []byte("parent sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specialName := "space #?% ' $(touch INJECTED);.txt"
	if err := os.WriteFile(filepath.Join(homeA, specialName), []byte("literal filename\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fx := newFakeSSH(t, func(o *fakeSSHOpts) {
		o.remoteHome = homeA
		o.homesByTarget = map[string]string{
			"alice@box": homeA,
			"bob@box":   homeB,
		}
	})

	// /~/sentinel.txt resolves against remote HOME (with space & quote in path!).
	res, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box/~/sentinel.txt"))
	if err != nil || res.Content != "remote alice\n" {
		t.Fatalf("Resolve(alice /~/sentinel.txt) = (%+v, %v)", res, err)
	}
	// Separate account gets separate cached HOME.
	resB, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://bob@box/~/sentinel.txt"))
	if err != nil || resB.Content != "remote bob\n" {
		t.Fatalf("Resolve(bob /~/sentinel.txt) = (%+v, %v)", resB, err)
	}
	// Write under /~/new.txt lands in remote HOME.
	if _, err := fx.handler.Write(urls.Context{}, mustParseURL(t, "ssh://alice@box/~/new.txt"), "written remotely\n"); err != nil {
		t.Fatalf("Write(/~/new.txt): %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(homeA, "new.txt")); err != nil || string(b) != "written remotely\n" {
		t.Fatalf("remote new.txt = (%q, %v)", string(b), err)
	}
	// /~ lists remote HOME directory.
	dirRes, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box/~"))
	if err != nil || !dirRes.IsDirectory || !strings.Contains(dirRes.Content, "sentinel.txt") || !strings.Contains(dirRes.Content, "new.txt") {
		t.Fatalf("Resolve(/~) = (%+v, %v)", dirRes, err)
	}
	// Remote parent traversal `/~/../parent.txt` resolves on remote filesystem.
	pRes, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box/~/../parent.txt"))
	if err != nil || pRes.Content != "parent sentinel\n" {
		t.Fatalf("Resolve(/~/../parent.txt) = (%+v, %v)", pRes, err)
	}
	// Percent-encoded shell metacharacters stay literal (no command injection).
	encName := strings.NewReplacer(" ", "%20", "#", "%23", "?", "%3F", "%", "%25", "'", "%27", ";", "%3B").Replace(specialName)
	sRes, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box/~/"+encName))
	if err != nil || sRes.Content != "literal filename\n" {
		t.Fatalf("Resolve(metachar filename) = (%+v, %v)", sRes, err)
	}
	if _, err := os.Stat(filepath.Join(homeA, "INJECTED")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("INJECTED file was created — shell injection occurred!")
	}
	// /~user is rejected without spawning ssh.
	logBefore := fx.logText(t)
	if _, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box/~other/file")); err == nil || !strings.Contains(err.Error(), "/~user") {
		t.Errorf("expected /~user rejection, got %v", err)
	}
	if fx.logText(t) != logBefore {
		t.Errorf("ssh was spawned for rejected /~other/file")
	}
}

func TestFakeSSH_HostileHostRefusedBeforeSpawn(t *testing.T) {
	fx := newFakeSSH(t)
	pwn := filepath.Join(t.TempDir(), "PWNED")
	raw := "ssh://-oProxyCommand=touch%20" + strings.ReplaceAll(pwn, "/", "%2F") + "/etc/hosts"
	_, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, raw))
	if err == nil || !strings.Contains(err.Error(), `must not begin with "-"`) {
		t.Fatalf("expected argument-injection refusal, got %v", err)
	}
	if log := fx.logText(t); log != "" {
		t.Fatalf("ssh binary should NEVER have been spawned for hostile host, log:\n%s", log)
	}
	if _, statErr := os.Stat(pwn); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("PWNED side effect ran!")
	}
}

func TestFakeSSH_WindowsRemoteAndMissingPosixShellRefused(t *testing.T) {
	fxWin := newFakeSSH(t, func(o *fakeSSHOpts) { o.remoteOS = "windows" })
	if _, err := fxWin.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://winbox/C:/hosts")); err == nil || !strings.Contains(err.Error(), "Windows host") {
		t.Errorf("expected Windows host refusal, got %v", err)
	}

	fxNoShell := newFakeSSH(t, func(o *fakeSSHOpts) { o.disableTransferProbe = "1" })
	if _, err := fxNoShell.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://fishbox/etc/hosts")); err == nil || !strings.Contains(err.Error(), "no verified POSIX shell") {
		t.Errorf("expected missing POSIX shell refusal, got %v", err)
	}
}

func TestFakeSSH_TimeoutEnforcement(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "entered-sleep")
	fx := newFakeSSH(t, func(o *fakeSSHOpts) {
		o.sleepSecs = "12"
		o.holdChildSecs = "12"
		o.sleepMarker = marker
	})
	fx.manager.HelperTimeout = 1500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := fx.manager.ReadRemoteFile(ctx, ConnectionTarget{Name: "slow", Host: "slow"}, "/etc/hosts", RemoteFileReadOptions{
		MaxBytes: 1024,
		Timeout:  1500 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("expected deadline exceeded error, got %v", err)
	}
	// Prove the fake script actually reached the sleep (not killed before exec).
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("fake-ssh did not reach sleep marker before timeout (vacuous test): %v", statErr)
	}
	if !strings.Contains(fx.logText(t), "ARGV:") {
		t.Fatalf("fake-ssh log missing ARGV line")
	}
	// Must return promptly after 1.5s timeout, nowhere near 12s sleep.
	if elapsed > 6*time.Second {
		t.Errorf("timeout took %v (expected well under 12s sleep)", elapsed)
	}
}

func TestFakeSSH_ListRemoteDirTimeoutWithOpenPipeDescendant(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "list-entered-sleep")
	fx := newFakeSSH(t, func(o *fakeSSHOpts) {
		o.sleepMarker = marker
	})
	// Pre-populate verified host info + master so only ListRemoteDir runs with timeout.
	tgt := ConnectionTarget{Name: "slowlist", Host: "slowlist"}
	if err := fx.manager.EnsureConnection(context.Background(), tgt, ""); err != nil {
		t.Fatalf("EnsureConnection: %v", err)
	}
	// Swap in a slow fake ssh script that spawns a descendant holding stdout open.
	slowBin := filepath.Join(t.TempDir(), "slow-list-ssh")
	slowScript := fmt.Sprintf("#!/bin/sh\ntouch %s\nsleep 12 &\nsleep 12\n", QuotePosixArgument(marker))
	if err := os.WriteFile(slowBin, []byte(slowScript), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(marker)
	fx.manager.SSHBin = slowBin

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := fx.manager.ListRemoteDir(ctx, tgt, "/tmp", RemoteOpOptions{Timeout: 1200 * time.Millisecond})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error from ListRemoteDir")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("slow-list-ssh did not reach sleep marker: %v", statErr)
	}
	if elapsed > 6*time.Second {
		t.Errorf("ListRemoteDir timeout took %v (hung on descendant pipe)", elapsed)
	}
}

// Regression for #3: cancelled/timed-out `ssh -G` must NOT poison controlPathOwners cache as `omp`.
func TestFakeSSH_CancelledControlPathLookupNotCached(t *testing.T) {
	fx := newFakeSSH(t, func(o *fakeSSHOpts) {
		o.configControlPath = "/tmp/user-mux-%C"
		o.sleepSecs = "5"
	})
	tgt := ConnectionTarget{Name: "mfa-retry", Host: "mfa-retry"}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := fx.manager.resolveControlPathOwner(ctx, tgt, "darwin"); err == nil {
		t.Fatalf("expected error on timed-out ssh -G")
	}

	// Now remove sleep and verify the next lookup succeeds and returns controlOwnerConfig.
	fastFx := newFakeSSH(t, func(o *fakeSSHOpts) {
		o.configControlPath = "/tmp/user-mux-%C"
	})
	fx.manager.SSHBin = fastFx.binPath
	owner, err := fx.manager.resolveControlPathOwner(context.Background(), tgt, "darwin")
	if err != nil {
		t.Fatalf("second resolveControlPathOwner failed: %v", err)
	}
	if owner != controlOwnerConfig {
		t.Errorf("owner = %q, want %q (cancelled lookup poisoned cache)", owner, controlOwnerConfig)
	}
}

// Regression for #2: temp-rooted control & host-info dirs always enforce Shared/owner-private hardening.
func TestTempFallbackControlAndCacheDirsHardened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX owner-private symlink guard")
	}
	scratch := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("PI_CONFIG_DIR", filepath.Join(scratch, ".omp"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(scratch, ".omp", "agent"))

	// ControlDir under os.TempDir() must be marked Shared=true and refuse symlinks.
	victim := filepath.Join(scratch, "victim")
	if err := os.Mkdir(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	symCtl := filepath.Join(scratch, "sym-ctl")
	if err := os.Symlink(victim, symCtl); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager()
	mgr.ControlDir = symCtl
	choice := mgr.controlDirChoice()
	if !choice.Shared {
		t.Fatalf("expected temp-rooted ControlDir to have Shared=true, got %+v", choice)
	}
	if err := mgr.EnsureControlDir(); err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Errorf("expected symlink refusal on temp-rooted ControlDir, got %v", err)
	}

	// HostInfoDir pointing at a symlink under /tmp must NOT write through the symlink.
	symCache := filepath.Join(scratch, "sym-cache")
	if err := os.Symlink(victim, symCache); err != nil {
		t.Fatal(err)
	}
	mgr.HostInfoDir = symCache
	mgr.DisableDiskCache = false
	mgr.persistHostInfo(ConnectionTarget{Name: "evil", Host: "evil"}, HostInfo{OS: HostOSLinux, TransferShell: "sh"})
	if _, err := os.Stat(filepath.Join(victim, "evil.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("persistHostInfo wrote through symlink into victim dir!")
	}

	// Default canonical dirs when HOME is empty still fall back to user.Current() or hardened temp.
	t.Setenv("PI_CONFIG_DIR", ".omp")
	dir, shared := defaultCanonicalControlDir()
	if dir == "" {
		t.Fatalf("defaultCanonicalControlDir returned empty dir")
	}
	if isUnderTempBase(dir) && !shared {
		t.Fatalf("temp-rooted defaultCanonicalControlDir %q must set shared=true", dir)
	}
}

// Regressions for #4, #5, #7, #9, #11: remaining HOME edge cases, content-types,
// BOM stripping, per-entry ssh.json resilience & key-order preservation, and
// NonZeroExitError wording.
func TestEdgeCases_HomePathsContentTypeBOMAndJSONOrder(t *testing.T) {
	// #11: DecodeUTF8Text strips leading UTF-8 BOM.
	bomText, ok := DecodeUTF8Text(append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello\n")...))
	if !ok || bomText != "hello\n" {
		t.Errorf("DecodeUTF8Text(BOM) = (%q, %v), want (\"hello\\n\", true)", bomText, ok)
	}

	// #7: contentTypeForRemotePath matches upstream lang-from-path.ts + dotfile .json.
	ctCases := []struct {
		path string
		want string
	}{
		{"/a/readme.md", "text/markdown"},
		{"/a/doc.markdown", "text/markdown"},
		{"/a/page.mdx", "text/markdown"},
		{"/a/rule.mdc", "text/markdown"},
		{"/a/note.mkd", "text/markdown"},
		{"/a/file.mdown", "text/markdown"},
		{"/a/file.mkdn", "text/plain"},
		{"/a/data.json", "application/json"},
		{"/a/.json", "text/plain"},
		{"/a/.md", "text/plain"},
		{"/a/plain.txt", "text/plain"},
	}
	for _, tc := range ctCases {
		if got := contentTypeForRemotePath(tc.path); got != tc.want {
			t.Errorf("contentTypeForRemotePath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}

	// #5: NonZeroExitError formats `Process exited with code N:\n<stderr>`.
	nz := &NonZeroExitError{ExitCode: 2, Stderr: "permission denied"}
	if got := nz.Error(); got != "Process exited with code 2:\npermission denied" {
		t.Errorf("NonZeroExitError.Error() = %q", got)
	}

	// #9: ssh.json preserves source order and skips malformed non-object entry without dropping file.
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("PI_CONFIG_DIR", filepath.Join(home, ".omp"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, ".omp", "agent"))
	raw := `{"hosts":{"zeta":{"host":"10.0.0.2"},"bad":"not-an-object","alpha":{"host":"10.0.0.1"}}}`
	if err := os.WriteFile(filepath.Join(cwd, "ssh.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, warns := DiscoverHosts(ConfigOptions{Cwd: cwd, Home: home, DisableSystemConfig: true, Providers: []string{"ssh-json"}})
	if len(hosts) != 2 || hosts[0].Name != "zeta" || hosts[1].Name != "alpha" {
		t.Errorf("expected preserved order [zeta alpha] skipping bad entry, got %+v (warns=%v)", hosts, warns)
	}
	if len(warns) == 0 {
		t.Errorf("expected warning for malformed non-object entry")
	}

	// #4: Additional ssh-home-paths cases (literal /%7E/ & /~%2F, root HOME "/",
	// separate HOME cache by port and configured alias target, failed HOME not cached).
	if runtime.GOOS == "windows" {
		return
	}
	root := t.TempDir()
	homeP1 := filepath.Join(root, "home-p1")
	homeP2 := filepath.Join(root, "home-p2")
	if err := os.MkdirAll(homeP1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(homeP2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeP1, "f.txt"), []byte("p1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeP2, "f.txt"), []byte("p2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fx := newFakeSSH(t, func(o *fakeSSHOpts) {
		o.remoteHome = homeP1
		o.homesByTarget = map[string]string{
			"alice@box":      homeP1,
			"alice@box:2222": homeP2,
			"rootbox":        "/",
			"badhome":        "relative-not-abs",
		}
	})

	// Literal /%7E/ and /~%2F do NOT trigger HOME expansion.
	tgt := ConnectionTarget{Name: "alice@box", Host: "box", Username: "alice"}
	for _, rawURL := range []string{"ssh://alice@box/%7E/sentinel.txt", "ssh://alice@box/~%2Fsentinel.txt"} {
		gotPath, err := fx.handler.remotePathFromURL(context.Background(), mustParseURL(t, rawURL), tgt, "")
		if err != nil || gotPath != "/~/sentinel.txt" {
			t.Errorf("remotePathFromURL(%q) = (%q, %v), want /~/sentinel.txt", rawURL, gotPath, err)
		}
	}
	if strings.Contains(fx.logText(t), "$HOME") {
		t.Errorf("literal /%%7E/ or /~%%2F should not have queried remote $HOME")
	}

	// Root HOME "/" joins cleanly to "/sentinel.txt" without double slash.
	rootPath, err := fx.handler.remotePathFromURL(context.Background(), mustParseURL(t, "ssh://rootbox/~/sentinel.txt"), ConnectionTarget{Name: "rootbox", Host: "rootbox"}, "")
	if err != nil || rootPath != "/sentinel.txt" {
		t.Errorf("root HOME join = (%q, %v), want /sentinel.txt", rootPath, err)
	}

	// HOME cache separates same user@host by port.
	r1, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box/~/f.txt"))
	if err != nil || r1.Content != "p1\n" {
		t.Fatalf("port-default HOME = (%+v, %v)", r1, err)
	}
	r2, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box:2222/~/f.txt"))
	if err != nil || r2.Content != "p2\n" {
		t.Fatalf("port-2222 HOME = (%+v, %v)", r2, err)
	}

	// Failed HOME lookup (non-absolute) is NOT cached: fixing remote HOME recovers.
	if _, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://badhome/~/f.txt")); err == nil || !strings.Contains(err.Error(), "HOME") {
		t.Fatalf("expected non-absolute HOME error, got %v", err)
	}
	// Update homes.txt so badhome now returns homeP1; must succeed (not stuck on cached error).
	homesFile := filepath.Join(filepath.Dir(fx.binPath), "homes.txt")
	if err := os.WriteFile(homesFile, []byte("badhome="+homeP1+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rRec, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://badhome/~/f.txt"))
	if err != nil || rRec.Content != "p1\n" {
		t.Fatalf("recovered HOME = (%+v, %v)", rRec, err)
	}

	// Missing file error carries upstream `Process exited with code 66:` wording AND os.ErrNotExist.
	_, missErr := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://alice@box"+filepath.Join(root, "nope")))
	if missErr == nil || !errors.Is(missErr, os.ErrNotExist) || !strings.Contains(missErr.Error(), "Process exited with code 66:") {
		t.Errorf("missing file err = %v, want os.ErrNotExist + 'Process exited with code 66:'", missErr)
	}
}
func TestFakeSSH_ControlMasterLifecycleAndSessionEndTeardown(t *testing.T) {
	fx := newFakeSSH(t)
	remoteFile := filepath.Join(t.TempDir(), "ok.txt")
	if err := os.WriteFile(remoteFile, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate session-scoped EnsureConnection + lifecycle.OnEnd cleanup.
	lifecycle.ResetForTests()
	t.Cleanup(lifecycle.ResetForTests)
	lifecycle.OnEnd("ssh-test", func(sid string) {
		fx.manager.CloseSession(sid)
	})

	if err := fx.manager.EnsureConnection(context.Background(), ConnectionTarget{Name: "prod", Host: "prod"}, "sess-1"); err != nil {
		t.Fatalf("EnsureConnection: %v", err)
	}
	log := fx.logText(t)
	if !strings.Contains(log, "MASTER_START: prod") {
		t.Errorf("expected ControlMaster start in log:\n%s", log)
	}
	// Verify `--` precedes host in ssh argv.
	if !strings.Contains(log, "-- prod") {
		t.Errorf("expected '-- prod' in ssh argv log:\n%s", log)
	}

	// Second session shares the same host: ending sess-1 must NOT close master yet.
	if err := fx.manager.EnsureConnection(context.Background(), ConnectionTarget{Name: "prod", Host: "prod"}, "sess-2"); err != nil {
		t.Fatalf("EnsureConnection(sess-2): %v", err)
	}
	lifecycle.End("sess-1")
	if strings.Contains(fx.logText(t), "EXIT: prod") {
		t.Errorf("master closed prematurely while sess-2 still active:\n%s", fx.logText(t))
	}

	// Ending sess-2 tears down the ControlMaster socket.
	lifecycle.End("sess-2")
	if !strings.Contains(fx.logText(t), "EXIT: prod") {
		t.Errorf("expected ControlMaster -O exit on last session end:\n%s", fx.logText(t))
	}
}

func TestFakeSSH_UserOwnedControlPathNeverExited(t *testing.T) {
	fx := newFakeSSH(t, func(o *fakeSSHOpts) { o.configControlPath = "/tmp/user-mux-%C" })
	if err := fx.manager.EnsureConnection(context.Background(), ConnectionTarget{Name: "mfa", Host: "mfa"}, "sess-1"); err != nil {
		t.Fatalf("EnsureConnection: %v", err)
	}
	fx.manager.CloseAllConnections()
	log := fx.logText(t)
	if strings.Contains(log, "MASTER_START:") {
		t.Errorf("must not start omp master when ssh_config owns ControlPath:\n%s", log)
	}
	if strings.Contains(log, "EXIT:") {
		t.Errorf("must not exit user-owned ControlPath socket:\n%s", log)
	}
}

func TestFakeSSH_WindowsPlatformFallbackSkipsControlMaster(t *testing.T) {
	fx := newFakeSSH(t, func(o *fakeSSHOpts) { o.platform = "windows" })
	remoteFile := filepath.Join(t.TempDir(), "win-client.txt")
	if err := os.WriteFile(remoteFile, []byte("hello from win client\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := fx.handler.Resolve(urls.Context{}, mustParseURL(t, "ssh://linuxbox"+remoteFile))
	if err != nil || res.Content != "hello from win client\n" {
		t.Fatalf("Resolve under windows client platform = (%+v, %v)", res, err)
	}
	log := fx.logText(t)
	if strings.Contains(log, "ControlMaster=") || strings.Contains(log, "ControlPath=") {
		t.Errorf("Windows client platform must not pass ControlMaster/ControlPath flags:\n%s", log)
	}
}

// =============================================================================
// 6. PromptDoc / PromptReference PATH gating & omp verbatim parity
// =============================================================================

func TestPromptDocMatchesOmpVerbatim(t *testing.T) {
	const wantDoc = "ssh://host/<path>: remote UTF-8 file/dir for read/write/grep; bare lists hosts."
	const wantRef = "ssh://host/<path>: max 1 MiB. /~/path and /~ use the connected remote account's HOME, not local HOME; /~user is unsupported, /%7E/ is literal. Encode : ? # as %3A %3F %23. Needs verified POSIX shell; else bash remote SSH or sshfs."

	// With ssh available (even with empty HOME / no ~/.ssh/config): advertised verbatim.
	fx := newFakeSSH(t)
	if got := fx.handler.PromptDoc(urls.SchemeHost{}); got != wantDoc {
		t.Errorf("PromptDoc = %q, want %q", got, wantDoc)
	}
	if got := fx.handler.PromptReference(); got != wantRef {
		t.Errorf("PromptReference = %q, want %q", got, wantRef)
	}

	// Without ssh on PATH (empty PATH and no SSHBin): omitted.
	emptyPathDir := t.TempDir()
	t.Setenv("PATH", emptyPathDir)
	noBinHandler := &Handler{Manager: NewManager()}
	if got := noBinHandler.PromptDoc(urls.SchemeHost{}); got != "" {
		t.Errorf("PromptDoc without ssh on PATH = %q, want empty", got)
	}
	if got := noBinHandler.PromptReference(); got != "" {
		t.Errorf("PromptReference without ssh on PATH = %q, want empty", got)
	}
}

// Verify Register registers ssh:// cleanly and attaches lifecycle cleanup.
func TestRegisterSmoke(t *testing.T) {
	urls.ResetForTests()
	lifecycle.ResetForTests()
	t.Cleanup(func() {
		urls.ResetForTests()
		lifecycle.ResetForTests()
	})
	Register(sdk.New("test"))
	if !urls.CanHandle("ssh://prod/etc/hosts") {
		t.Fatalf("expected ssh:// to be registered")
	}
	spec, ok := urls.LookupSpec("ssh")
	if !ok || spec.Backing != urls.BackingRemote || spec.ReadTier != "exec" || !spec.PortAuthority || spec.Write == nil || spec.Write.Via != "handler" {
		t.Fatalf("unexpected ssh spec: %+v ok=%v", spec, ok)
	}
}
