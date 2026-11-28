package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/ssh/connection-manager.ts

// ConnectionTarget describes one SSH destination.
// upstream: connection-manager.ts:7-14
type ConnectionTarget struct {
	Name     string
	Host     string
	Username string
	Port     int
	KeyPath  string
	Compat   *bool
}

// HostOS classifies a remote host's operating system.
// upstream: connection-manager.ts:16
type HostOS string

const (
	HostOSWindows HostOS = "windows"
	HostOSLinux   HostOS = "linux"
	HostOSMacOS   HostOS = "macos"
	HostOSUnknown HostOS = "unknown"
)

// HostShell classifies a remote host's login shell.
// upstream: connection-manager.ts:17
type HostShell string

const (
	HostShellCmd        HostShell = "cmd"
	HostShellPowershell HostShell = "powershell"
	HostShellBash       HostShell = "bash"
	HostShellZsh        HostShell = "zsh"
	HostShellSh         HostShell = "sh"
	HostShellUnknown    HostShell = "unknown"
)

// HostInfo is the probed capability profile of a remote SSH host.
// upstream: connection-manager.ts:24-39
type HostInfo struct {
	Version       int       `json:"version"`
	OS            HostOS    `json:"os"`
	Shell         HostShell `json:"shell"`
	TransferShell string    `json:"transferShell,omitempty"` // "sh" | "bash" | "zsh"
	CompatShell   string    `json:"compatShell,omitempty"`   // "bash" | "sh"
	CompatEnabled bool      `json:"compatEnabled"`
}

const (
	// controlSocketBasename is OpenSSH's %C.sock template (40-hex digest + ".sock").
	controlSocketBasename  = "%C.sock"
	controlSocketNameBytes = 40 + len(".sock")
	// "." + 16 random chars appended by OpenSSH's muxserver_listen while binding.
	muxTempSuffixBytes = 1 + 16

	hostInfoVersion = 4

	// HostProbeMarker frames the remote OS/shell probe output to ignore login banners.
	// upstream: connection-manager.ts:461
	HostProbeMarker = "PI_HOST_PROBE="
	// TransferProbeMarker frames the transfer-shell capability probe output.
	// upstream: connection-manager.ts:464
	TransferProbeMarker = "PI_TRANSFER_OK|"

	defaultHelperTimeout = 30 * time.Second
	sshDrainGrace        = 2 * time.Second
	connectionVerifyTTL  = 30 * time.Second
)

var transferShellCandidates = []string{"sh", "bash", "zsh"}

// SupportsControlMaster reports whether ControlMaster multiplexing is supported
// on platform (GOOS name, e.g. "darwin", "linux", "windows"; also accepts "win32").
// upstream: connection-manager.ts:20-22
func SupportsControlMaster(platform string) bool {
	return platform != "windows" && platform != "win32"
}

// ControlPathFitsBudget reports whether controlDir leaves room for `%C.sock`
// plus OpenSSH's 17-byte mux temp bind inside `sockaddr_un.sun_path`
// (104 bytes on macOS, 108 elsewhere).
// upstream: connection-manager.ts:62-66
func ControlPathFitsBudget(controlDir, platform string) bool {
	sunPathLimit := 108
	if platform == "darwin" {
		sunPathLimit = 104
	}
	worstCase := len(controlDir) + 1 + controlSocketNameBytes + muxTempSuffixBytes
	return worstCase < sunPathLimit
}

// ControlFallbackDir returns the deterministic, depth-bounded `/tmp/omp-<20hex>`
// fallback directory used when the canonical control dir overflows `sun_path`.
// upstream: connection-manager.ts:76-85
func ControlFallbackDir(canonicalDir string, uid uint32, tmpBase string) string {
	if tmpBase == "" {
		tmpBase = "/tmp"
	}
	h := sha256.New()
	h.Write([]byte(strconv.FormatUint(uint64(uid), 10)))
	h.Write([]byte{0})
	h.Write([]byte(canonicalDir))
	key := hex.EncodeToString(h.Sum(nil))[:20]
	return path.Join(tmpBase, "omp-"+key)
}

// ControlDirChoice is the resolved SSH ControlMaster directory.
type ControlDirChoice struct {
	Dir    string
	Shared bool
}

// ResolveControlDir prefers canonicalDir when it fits `sun_path`, falling back
// to ControlFallbackDir on POSIX platforms when a uid is available.
// upstream: connection-manager.ts:100-109
func ResolveControlDir(canonicalDir, platform string, uid *uint32, tmpBase string) ControlDirChoice {
	if !SupportsControlMaster(platform) || uid == nil {
		return ControlDirChoice{Dir: canonicalDir, Shared: false}
	}
	if ControlPathFitsBudget(canonicalDir, platform) {
		return ControlDirChoice{Dir: canonicalDir, Shared: false}
	}
	return ControlDirChoice{Dir: ControlFallbackDir(canonicalDir, *uid, tmpBase), Shared: true}
}

type controlPathOwner string

const (
	controlOwnerOmp    controlPathOwner = "omp"
	controlOwnerConfig controlPathOwner = "config"
)

// ArgsOptions customizes SSH command argument construction.
type ArgsOptions struct {
	Platform   string
	AllowStdin bool
}

// ExecResult captures one `ssh` child execution.
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// NonZeroExitError records an `ssh` execution that exited with a non-zero code.
type NonZeroExitError struct {
	ExitCode int
	Stderr   string
}

func (e *NonZeroExitError) Error() string {
	// upstream: packages/utils/src/ptree.ts:121
	return fmt.Sprintf("Process exited with code %d:\n%s", e.ExitCode, e.Stderr)
}

type verifiedConn struct {
	at          time.Time
	fingerprint string
}

type pendingConn struct {
	done chan struct{}
	err  error
}

// Manager manages OpenSSH ControlMaster connections, remote host capability
// probing, and remote command execution.
//
// All external state (SSH binary path, control directory, host-info cache dir,
// helper timeout, platform) is configurable on Manager so tests can inject a
// fake `ssh` script and temp dirs without touching `~/.ssh` or real networks.
// upstream: packages/coding-agent/src/ssh/connection-manager.ts
type Manager struct {
	// SSHBin overrides the `ssh` binary path. Empty resolves `ssh` via exec.LookPath.
	SSHBin string
	// ControlDir overrides the canonical ControlMaster directory.
	ControlDir string
	// ControlDirShared forces AssertOwnerPrivateDir checks on ControlDir.
	ControlDirShared bool
	// HostInfoDir overrides the persisted host-info cache directory.
	// Empty uses defaultHostInfoDir(). Set DisableDiskCache to skip disk reads/writes.
	HostInfoDir      string
	DisableDiskCache bool
	// Platform overrides runtime.GOOS for testing (e.g. "darwin", "linux", "windows").
	Platform string
	// HelperTimeout overrides the 30s timeout for control/probe helpers.
	HelperTimeout time.Duration
	// Now overrides time.Now for tests.
	Now func() time.Time
	// ConfigOpts overrides DiscoverHosts options when resolving ssh:// targets.
	ConfigOpts *ConfigOptions

	mu                  sync.Mutex
	activeHosts         map[string]ConnectionTarget
	pendingConnections  map[string]*pendingConn
	hostInfoCache       map[string]HostInfo
	controlPathOwners   map[string]controlPathOwner
	verifiedConnections map[string]verifiedConn
	remoteHomes         map[string]string
	sessionHosts        map[string]map[string]bool // sessionID -> set of host keys touched
}

// NewManager creates an isolated Manager with empty caches.
func NewManager() *Manager {
	return &Manager{
		activeHosts:         make(map[string]ConnectionTarget),
		pendingConnections:  make(map[string]*pendingConn),
		hostInfoCache:       make(map[string]HostInfo),
		controlPathOwners:   make(map[string]controlPathOwner),
		verifiedConnections: make(map[string]verifiedConn),
		remoteHomes:         make(map[string]string),
		sessionHosts:        make(map[string]map[string]bool),
	}
}

var defaultManager = NewManager()

// DefaultManager returns the process-wide SSH connection manager.
func DefaultManager() *Manager { return defaultManager }

func (m *Manager) platform() string {
	if m.Platform != "" {
		return m.Platform
	}
	return runtime.GOOS
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) helperTimeout() time.Duration {
	if m.HelperTimeout > 0 {
		return m.HelperTimeout
	}
	return defaultHelperTimeout
}

// ResolveSSHBinary returns the configured SSHBin or locates `ssh` on PATH.
func (m *Manager) ResolveSSHBinary() (string, error) {
	if m.SSHBin != "" {
		return m.SSHBin, nil
	}
	p, err := exec.LookPath("ssh")
	if err != nil {
		return "", errors.New("ssh binary not found on PATH")
	}
	return p, nil
}

// HasSSHBinary reports whether an `ssh` client binary is available.
func (m *Manager) HasSSHBinary() bool {
	_, err := m.ResolveSSHBinary()
	return err == nil
}
func isUnderTempBase(dir string) bool {
	clean := filepath.Clean(dir)
	for _, base := range []string{os.TempDir(), "/tmp"} {
		if base == "" {
			continue
		}
		cleanBase := filepath.Clean(base)
		if clean == cleanBase || strings.HasPrefix(clean, cleanBase+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func defaultCanonicalControlDir() (dir string, shared bool) {
	home := effectiveHome("")
	cfgDir := sshJSONConfigDirName()
	if filepath.IsAbs(cfgDir) {
		d := filepath.Join(cfgDir, "ssh-control")
		return d, isUnderTempBase(d)
	}
	if home == "" {
		if uid, ok := currentUID(); ok {
			return ControlFallbackDir("omp-ssh-control", uid, os.TempDir()), true
		}
		return filepath.Join(os.TempDir(), "omp-ssh-control"), true
	}
	d := filepath.Join(home, cfgDir, "ssh-control")
	return d, isUnderTempBase(d)
}

func defaultHostInfoDir() (dir string, shared bool) {
	home := effectiveHome("")
	cfgDir := sshJSONConfigDirName()
	if filepath.IsAbs(cfgDir) {
		d := filepath.Join(cfgDir, "remote-host")
		return d, isUnderTempBase(d)
	}
	if home == "" {
		if uid, ok := currentUID(); ok {
			return ControlFallbackDir("omp-remote-host", uid, os.TempDir()), true
		}
		return filepath.Join(os.TempDir(), "omp-remote-host"), true
	}
	d := filepath.Join(home, cfgDir, "remote-host")
	return d, isUnderTempBase(d)
}

func (m *Manager) controlDirChoice() ControlDirChoice {
	if m.ControlDir != "" {
		return ControlDirChoice{
			Dir:    m.ControlDir,
			Shared: m.ControlDirShared || isUnderTempBase(m.ControlDir),
		}
	}
	var uidPtr *uint32
	if uid, ok := currentUID(); ok {
		uidPtr = &uid
	}
	canonical, canonicalShared := defaultCanonicalControlDir()
	choice := ResolveControlDir(canonical, m.platform(), uidPtr, "/tmp")
	if canonicalShared || isUnderTempBase(choice.Dir) {
		choice.Shared = true
	}
	return choice
}

// ControlPathTemplate returns the resolved OpenSSH ControlPath template.
func (m *Manager) ControlPathTemplate() string {
	return filepath.Join(m.controlDirChoice().Dir, controlSocketBasename)
}

// EnsureControlDir creates the ControlMaster directory and enforces owner-private
// permissions when using the shared temp fallback.
// upstream: connection-manager.ts:132-143
func (m *Manager) EnsureControlDir() error {
	choice := m.controlDirChoice()
	if err := os.MkdirAll(choice.Dir, 0o700); err != nil {
		return err
	}
	if choice.Shared {
		return AssertOwnerPrivateDir(choice.Dir, "SSH control directory")
	}
	_ = os.Chmod(choice.Dir, 0o700)
	return nil
}

func (m *Manager) hostInfoDirAndShared() (string, bool) {
	if m.HostInfoDir != "" {
		return m.HostInfoDir, isUnderTempBase(m.HostInfoDir)
	}
	return defaultHostInfoDir()
}

func (m *Manager) hostInfoPath(hostName string) string {
	dir, _ := m.hostInfoDirAndShared()
	return filepath.Join(dir, SanitizeHostName(hostName)+".json")
}

func validateKeyPermissions(keyPath, platform string) error {
	if keyPath == "" {
		return nil
	}
	fi, err := os.Stat(keyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("SSH key not found: %s", keyPath)
		}
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("SSH key is not a file: %s", keyPath)
	}
	if !SupportsControlMaster(platform) {
		return nil
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("SSH key permissions must be 600 or stricter: %s", keyPath)
	}
	return nil
}

func (m *Manager) buildCommonArgs(host ConnectionTarget, owner controlPathOwner, opts ArgsOptions) []string {
	var args []string
	if !opts.AllowStdin {
		args = append(args, "-n")
	}
	plat := opts.Platform
	if plat == "" {
		plat = m.platform()
	}
	if SupportsControlMaster(plat) {
		args = append(args, "-o", "ControlMaster=auto")
		if owner == controlOwnerOmp {
			args = append(args, "-o", "ControlPath="+m.ControlPathTemplate(), "-o", "ControlPersist=3600")
		}
	}
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
	)
	if host.Port > 0 {
		args = append(args, "-p", strconv.Itoa(host.Port))
	}
	if host.KeyPath != "" {
		args = append(args, "-i", host.KeyPath)
	}
	return args
}

// execSSH executes `sshBin` directly via exec.CommandContext (argv vector,
// NEVER a local shell).
func (m *Manager) execSSH(ctx context.Context, args []string, stdin []byte, timeout time.Duration) (ExecResult, error) {
	sshBin, err := m.ResolveSSHBinary()
	if err != nil {
		return ExecResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, sshBin, args...)
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	cmd.WaitDelay = sshDrainGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return ExecResult{
			ExitCode: -1,
			Stdout:   stdout.Bytes(),
			Stderr:   stderr.Bytes(),
		}, ctx.Err()
	}
	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return ExecResult{
				ExitCode: -1,
				Stdout:   stdout.Bytes(),
				Stderr:   stderr.Bytes(),
			}, runErr
		}
	}
	return ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
	}, nil
}

// resolveControlPathOwner checks `ssh -G` to see if the user's ssh_config
// already defines a ControlPath for host. Cached per destination.
// upstream: connection-manager.ts:266-298
func (m *Manager) resolveControlPathOwner(ctx context.Context, host ConnectionTarget, platform string) (controlPathOwner, error) {
	if platform == "" {
		platform = m.platform()
	}
	if !SupportsControlMaster(platform) {
		return controlOwnerOmp, nil
	}
	target, err := BuildSSHTarget(host.Username, host.Host)
	if err != nil {
		return "", err
	}
	key := target + "\x00" + strconv.Itoa(host.Port)
	m.mu.Lock()
	if owner, ok := m.controlPathOwners[key]; ok {
		m.mu.Unlock()
		return owner, nil
	}
	m.mu.Unlock()

	args := []string{"-G"}
	if host.Port > 0 {
		args = append(args, "-p", strconv.Itoa(host.Port))
	}
	args = append(args, "--", target)

	owner := controlOwnerOmp
	res, err := m.execSSH(ctx, args, nil, m.helperTimeout())
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil)) {
		// Never cache a cancelled or timed-out `ssh -G` lookup.
		return "", err
	}
	if err == nil && res.ExitCode == 0 {
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			if !strings.HasPrefix(line, "controlpath ") {
				continue
			}
			val := strings.TrimSpace(strings.TrimPrefix(line, "controlpath "))
			if val != "" && val != "none" {
				owner = controlOwnerConfig
			}
			break
		}
	}

	m.mu.Lock()
	m.controlPathOwners[key] = owner
	m.mu.Unlock()
	return owner, nil
}

// ── Host info parsing & probing ──────────────────────────────────────────────
// upstream: connection-manager.ts:300-640

func parseOS(value string) (HostOS, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "windows":
		return HostOSWindows, true
	case "linux":
		return HostOSLinux, true
	case "macos", "darwin":
		return HostOSMacOS, true
	case "unknown":
		return HostOSUnknown, true
	default:
		return HostOSUnknown, false
	}
}

func parseShell(value string) HostShell {
	norm := strings.ToLower(strings.TrimSpace(value))
	if norm == "" {
		return HostShellUnknown
	}
	if strings.Contains(norm, "bash") {
		return HostShellBash
	}
	if strings.Contains(norm, "zsh") {
		return HostShellZsh
	}
	if strings.Contains(norm, "pwsh") || strings.Contains(norm, "powershell") {
		return HostShellPowershell
	}
	if strings.Contains(norm, "cmd.exe") || norm == "cmd" {
		return HostShellCmd
	}
	base := norm
	if idx := strings.LastIndexByte(norm, '/'); idx != -1 {
		base = norm[idx+1:]
	}
	switch base {
	case "sh", "dash", "ash", "ksh", "mksh":
		return HostShellSh
	default:
		return HostShellUnknown
	}
}

func parseCompatShell(value string) string {
	if value == "bash" || value == "sh" {
		return value
	}
	return ""
}

func parseTransferShell(value string) string {
	if value == "sh" || value == "bash" || value == "zsh" {
		return value
	}
	return ""
}

// ParseHostInfo normalizes a decoded JSON host-info map, dropping invalid fields.
// upstream: connection-manager.ts:368-386
func ParseHostInfo(raw map[string]any) *HostInfo {
	if raw == nil {
		return nil
	}
	osVal := HostOSUnknown
	if s, ok := raw["os"].(string); ok {
		if parsed, valid := parseOS(s); valid {
			osVal = parsed
		}
	}
	shVal := HostShellUnknown
	if s, ok := raw["shell"].(string); ok {
		shVal = parseShell(s)
	}
	compatShell := ""
	if s, ok := raw["compatShell"].(string); ok {
		compatShell = parseCompatShell(s)
	}
	transferShell := ""
	if s, ok := raw["transferShell"].(string); ok {
		transferShell = parseTransferShell(s)
	}
	compatEnabled, _ := raw["compatEnabled"].(bool)
	version := 0
	if v, ok := raw["version"].(float64); ok {
		version = int(v)
	} else if v, ok := raw["version"].(int); ok {
		version = v
	}
	return &HostInfo{
		Version:       version,
		OS:            osVal,
		Shell:         shVal,
		TransferShell: transferShell,
		CompatShell:   compatShell,
		CompatEnabled: compatEnabled,
	}
}

func applyCompatOverride(host ConnectionTarget, info HostInfo) HostInfo {
	compatShell := info.CompatShell
	if compatShell == "" {
		if info.OS == HostOSWindows && info.Shell == HostShellBash {
			compatShell = "bash"
		} else if info.OS == HostOSWindows && info.Shell == HostShellSh {
			compatShell = "sh"
		}
	}
	compatEnabled := info.OS == HostOSWindows && compatShell != ""
	if host.Compat != nil && !*host.Compat {
		compatEnabled = false
	}
	info.CompatShell = compatShell
	info.CompatEnabled = compatEnabled
	return info
}

func shouldRefreshHostInfo(host ConnectionTarget, info HostInfo) bool {
	if info.Version != hostInfoVersion {
		return true
	}
	if info.OS == HostOSUnknown {
		return true
	}
	if info.OS != HostOSWindows && info.CompatEnabled {
		return true
	}
	if info.OS == HostOSWindows && info.CompatEnabled && info.CompatShell == "" {
		return true
	}
	if info.OS == HostOSWindows && info.CompatShell == "bash" && info.Shell == HostShellUnknown {
		return true
	}
	if host.Compat != nil && *host.Compat && info.OS == HostOSWindows && info.CompatShell == "" {
		return true
	}
	if info.OS != HostOSWindows && info.TransferShell == "" {
		return true
	}
	return false
}

// ExtractProbePayload finds the first line in stdout or stderr starting with
// marker (default HostProbeMarker) and returns everything after it.
// upstream: connection-manager.ts:477-488
func ExtractProbePayload(stdout, stderr, marker string) (string, bool) {
	if marker == "" {
		marker = HostProbeMarker
	}
	for _, blob := range []string{stdout, stderr} {
		if blob == "" {
			continue
		}
		for _, line := range strings.Split(blob, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, marker) {
				return strings.TrimPrefix(trimmed, marker), true
			}
		}
	}
	return "", false
}

// FindProbeMarker finds marker anywhere in stdout or stderr and returns the
// trailing text after it.
// upstream: connection-manager.ts:499-506
func FindProbeMarker(stdout, stderr, marker string) (string, bool) {
	for _, blob := range []string{stdout, stderr} {
		if blob == "" {
			continue
		}
		if idx := strings.Index(blob, marker); idx != -1 {
			return blob[idx+len(marker):], true
		}
	}
	return "", false
}

// OSFromUname classifies a `uname -s` output string.
// upstream: connection-manager.ts:509-517
func OSFromUname(value string) (HostOS, bool) {
	u := strings.ToLower(value)
	if strings.Contains(u, "darwin") {
		return HostOSMacOS, true
	}
	if strings.Contains(u, "linux") || strings.Contains(u, "gnu") {
		return HostOSLinux, true
	}
	if strings.Contains(u, "mingw") || strings.Contains(u, "msys") || strings.Contains(u, "cygwin") || strings.Contains(u, "windows") {
		return HostOSWindows, true
	}
	return HostOSUnknown, false
}

func (m *Manager) loadHostInfoFromDisk(host ConnectionTarget) (HostInfo, bool) {
	if m.DisableDiskCache {
		return HostInfo{}, false
	}
	data, err := os.ReadFile(m.hostInfoPath(host.Name))
	if err != nil {
		return HostInfo{}, false
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return HostInfo{}, false
	}
	parsed := ParseHostInfo(raw)
	if parsed == nil {
		return HostInfo{}, false
	}
	resolved := applyCompatOverride(host, *parsed)
	m.mu.Lock()
	m.hostInfoCache[host.Name] = resolved
	m.mu.Unlock()
	return resolved, true
}

func (m *Manager) persistHostInfo(host ConnectionTarget, info HostInfo) {
	info.Version = hostInfoVersion
	m.mu.Lock()
	m.hostInfoCache[host.Name] = info
	m.mu.Unlock()
	if m.DisableDiskCache {
		return
	}
	dir, _ := m.hostInfoDirAndShared()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return
	}
	fileName := SanitizeHostName(host.Name) + ".json"
	_ = writeOwnerPrivateFile(dir, fileName, data)
}

func (m *Manager) probeTransferShell(ctx context.Context, host ConnectionTarget) (shell string, uname string) {
	for _, candidate := range transferShellCandidates {
		remote := fmt.Sprintf(`%s -lc 'printf "%s"; uname -s 2>/dev/null || true'`, candidate, TransferProbeMarker)
		args, err := m.BuildRemoteCommand(ctx, host, remote, ArgsOptions{})
		if err != nil {
			continue
		}
		res, err := m.execSSH(ctx, args, nil, m.helperTimeout())
		if err != nil || res.ExitCode != 0 {
			continue
		}
		if tail, ok := FindProbeMarker(string(res.Stdout), string(res.Stderr), TransferProbeMarker); ok {
			return candidate, strings.TrimSpace(tail)
		}
	}
	return "", ""
}

func (m *Manager) probeHostInfo(ctx context.Context, host ConnectionTarget) (HostInfo, error) {
	command := fmt.Sprintf(`echo "%s$OSTYPE|$SHELL|$BASH_VERSION" 2>/dev/null || echo "%s%%OS%%|%%COMSPEC%%|"`, HostProbeMarker, HostProbeMarker)
	args, err := m.BuildRemoteCommand(ctx, host, command, ArgsOptions{})
	if err != nil {
		return HostInfo{}, err
	}
	res, err := m.execSSH(ctx, args, nil, m.helperTimeout())
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return HostInfo{}, err
	}
	payload, ok := ExtractProbePayload(string(res.Stdout), string(res.Stderr), HostProbeMarker)
	if !ok {
		tShell, uname := m.probeTransferShell(ctx, host)
		osVal := HostOSUnknown
		if tShell != "" {
			if detected, detOK := OSFromUname(uname); detOK {
				osVal = detected
			}
		}
		fallback := HostInfo{
			Version:       hostInfoVersion,
			OS:            osVal,
			Shell:         HostShellUnknown,
			TransferShell: tShell,
		}
		m.mu.Lock()
		m.hostInfoCache[host.Name] = fallback
		m.mu.Unlock()
		return fallback, nil
	}

	parts := strings.Split(payload, "|")
	rawOs := ""
	rawShell := ""
	rawBash := ""
	if len(parts) > 0 {
		rawOs = strings.TrimSpace(parts[0])
	}
	if len(parts) > 1 {
		rawShell = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		rawBash = strings.TrimSpace(parts[2])
	}
	payloadLower := strings.ToLower(payload)
	osLower := strings.ToLower(rawOs)
	shellLower := strings.ToLower(rawShell)

	unexpandedPosix := strings.Contains(payload, "$OSTYPE") || strings.Contains(payload, "$SHELL") || strings.Contains(payload, "$BASH_VERSION")
	windowsDetected := strings.Contains(osLower, "windows") ||
		strings.Contains(osLower, "msys") ||
		strings.Contains(osLower, "cygwin") ||
		strings.Contains(osLower, "mingw") ||
		strings.Contains(payloadLower, "windows_nt") ||
		strings.Contains(payloadLower, "comspec") ||
		strings.Contains(shellLower, "cmd") ||
		strings.Contains(shellLower, "powershell") ||
		unexpandedPosix ||
		strings.Contains(payload, "%OS%")

	osVal := HostOSUnknown
	switch {
	case windowsDetected:
		osVal = HostOSWindows
	case strings.Contains(osLower, "darwin"):
		osVal = HostOSMacOS
	case strings.Contains(osLower, "linux") || strings.Contains(osLower, "gnu"):
		osVal = HostOSLinux
	}

	shellVal := parseShell(shellLower)
	if shellVal == HostShellUnknown && osVal == HostOSWindows && shellLower == "" {
		shellVal = HostShellCmd
	}

	var transferShell string
	if osVal != HostOSWindows {
		tShell, uname := m.probeTransferShell(ctx, host)
		transferShell = tShell
		if transferShell != "" && osVal == HostOSUnknown {
			if detected, detOK := OSFromUname(uname); detOK {
				osVal = detected
			}
		}
	}

	hasBash := !unexpandedPosix && (rawBash != "" || shellVal == HostShellBash)
	compatShell := ""
	if osVal == HostOSWindows && (host.Compat == nil || *host.Compat) {
		if bArgs, bErr := m.BuildRemoteCommand(ctx, host, `bash -lc "echo PI_BASH_OK"`, ArgsOptions{}); bErr == nil {
			if bRes, rErr := m.execSSH(ctx, bArgs, nil, m.helperTimeout()); rErr == nil && bRes.ExitCode == 0 && strings.Contains(string(bRes.Stdout), "PI_BASH_OK") {
				compatShell = "bash"
			}
		}
		if compatShell == "" {
			if sArgs, sErr := m.BuildRemoteCommand(ctx, host, `sh -lc "echo PI_SH_OK"`, ArgsOptions{}); sErr == nil {
				if sRes, rErr := m.execSSH(ctx, sArgs, nil, m.helperTimeout()); rErr == nil && sRes.ExitCode == 0 && strings.Contains(string(sRes.Stdout), "PI_SH_OK") {
					compatShell = "sh"
				}
			}
		}
	} else if osVal == HostOSWindows && hasBash {
		compatShell = "bash"
	} else if osVal == HostOSWindows && shellVal == HostShellSh {
		compatShell = "sh"
	}

	info := applyCompatOverride(host, HostInfo{
		Version:       hostInfoVersion,
		OS:            osVal,
		Shell:         shellVal,
		TransferShell: transferShell,
		CompatShell:   compatShell,
	})
	m.persistHostInfo(host, info)
	return info, nil
}

// BuildRemoteCommand builds the full argv vector (excluding the `ssh` binary
// itself) for executing `command` on `host`, always inserting `--` before the
// destination so a host name can never be parsed as an OpenSSH option.
// upstream: connection-manager.ts:712-720
func (m *Manager) BuildRemoteCommand(ctx context.Context, host ConnectionTarget, command string, opts ArgsOptions) ([]string, error) {
	plat := opts.Platform
	if plat == "" {
		plat = m.platform()
	}
	if err := validateKeyPermissions(host.KeyPath, plat); err != nil {
		return nil, err
	}
	target, err := BuildSSHTarget(host.Username, host.Host)
	if err != nil {
		return nil, err
	}
	owner, err := m.resolveControlPathOwner(ctx, host, plat)
	if err != nil {
		return nil, err
	}
	args := m.buildCommonArgs(host, owner, opts)
	args = append(args, "--", target, command)
	return args, nil
}

// EnsureHostInfo returns cached/persisted HostInfo for host, probing if stale
// or absent.
// upstream: connection-manager.ts:698-710
func (m *Manager) EnsureHostInfo(ctx context.Context, host ConnectionTarget) (HostInfo, error) {
	m.mu.Lock()
	cached, hasCached := m.hostInfoCache[host.Name]
	m.mu.Unlock()
	if hasCached {
		resolved := applyCompatOverride(host, cached)
		m.mu.Lock()
		m.hostInfoCache[host.Name] = resolved
		m.mu.Unlock()
		if !shouldRefreshHostInfo(host, resolved) {
			return resolved, nil
		}
	}
	if fromDisk, ok := m.loadHostInfoFromDisk(host); ok && !shouldRefreshHostInfo(host, fromDisk) {
		return fromDisk, nil
	}
	if err := m.EnsureConnection(ctx, host, ""); err != nil {
		return HostInfo{}, err
	}
	m.mu.Lock()
	current, hasCurrent := m.hostInfoCache[host.Name]
	m.mu.Unlock()
	if hasCurrent && !shouldRefreshHostInfo(host, current) {
		return current, nil
	}
	return m.probeHostInfo(ctx, host)
}

// EnsureConnection verifies or starts a ControlMaster connection for host and
// associates it with sessionID (when non-empty) so session shutdown cleans it up.
// upstream: connection-manager.ts:735-805
func (m *Manager) EnsureConnection(ctx context.Context, host ConnectionTarget, sessionID string) error {
	key := host.Name
	fingerprint := fmt.Sprintf("%s@%s:%d\x00%s", host.Username, host.Host, host.Port, host.KeyPath)

	m.mu.Lock()
	if sessionID != "" {
		if m.sessionHosts[sessionID] == nil {
			m.sessionHosts[sessionID] = make(map[string]bool)
		}
		m.sessionHosts[sessionID][key] = true
	}
	verified, hasVerified := m.verifiedConnections[key]
	_, isActive := m.activeHosts[key]
	_, hasInfo := m.hostInfoCache[key]
	if hasVerified && m.now().Sub(verified.at) < connectionVerifyTTL && verified.fingerprint == fingerprint && isActive && hasInfo {
		m.mu.Unlock()
		return m.EnsureControlDir()
	}
	if pending, ok := m.pendingConnections[key]; ok {
		m.mu.Unlock()
		select {
		case <-pending.done:
			return pending.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	pending := &pendingConn{done: make(chan struct{})}
	m.pendingConnections[key] = pending
	m.mu.Unlock()

	err := m.establishConnection(ctx, host)

	m.mu.Lock()
	if err == nil {
		m.verifiedConnections[key] = verifiedConn{at: m.now(), fingerprint: fingerprint}
	}
	delete(m.pendingConnections, key)
	pending.err = err
	close(pending.done)
	m.mu.Unlock()
	return err
}

func (m *Manager) establishConnection(ctx context.Context, host ConnectionTarget) error {
	if _, err := m.ResolveSSHBinary(); err != nil {
		return err
	}
	if SupportsControlMaster(m.platform()) {
		if err := m.EnsureControlDir(); err != nil {
			return err
		}
	}
	if err := validateKeyPermissions(host.KeyPath, m.platform()); err != nil {
		return err
	}
	target, err := BuildSSHTarget(host.Username, host.Host)
	if err != nil {
		return err
	}
	key := host.Name

	if !SupportsControlMaster(m.platform()) {
		m.mu.Lock()
		m.activeHosts[key] = host
		_, hasInfo := m.hostInfoCache[key]
		m.mu.Unlock()
		if !hasInfo {
			if _, ok := m.loadHostInfoFromDisk(host); !ok {
				if _, pErr := m.probeHostInfo(ctx, host); pErr != nil {
					return pErr
				}
			}
		}
		return nil
	}

	owner, err := m.resolveControlPathOwner(ctx, host, m.platform())
	if err != nil {
		return err
	}
	commonArgs := m.buildCommonArgs(host, owner, ArgsOptions{})

	checkArgs := append([]string{"-O", "check"}, commonArgs...)
	checkArgs = append(checkArgs, "--", target)
	checkRes, checkErr := m.execSSH(ctx, checkArgs, nil, m.helperTimeout())
	if checkErr != nil && (errors.Is(checkErr, context.Canceled) || errors.Is(checkErr, context.DeadlineExceeded)) {
		return checkErr
	}
	if (checkErr == nil && checkRes.ExitCode == 0) || owner == controlOwnerConfig {
		m.mu.Lock()
		m.activeHosts[key] = host
		_, hasInfo := m.hostInfoCache[key]
		m.mu.Unlock()
		if !hasInfo {
			if _, ok := m.loadHostInfoFromDisk(host); !ok {
				if _, pErr := m.probeHostInfo(ctx, host); pErr != nil {
					return pErr
				}
			}
		}
		return nil
	}

	startArgs := append([]string{"-M", "-N", "-f"}, commonArgs...)
	startArgs = append(startArgs, "--", target)
	startRes, startErr := m.execSSH(ctx, startArgs, nil, m.helperTimeout())
	if startErr != nil {
		return fmt.Errorf("Failed to start SSH master for %s: %w", target, startErr)
	}
	if startRes.ExitCode != 0 {
		detail := strings.TrimSpace(string(startRes.Stderr))
		if detail != "" {
			return fmt.Errorf("Failed to start SSH master for %s: %s", target, detail)
		}
		return fmt.Errorf("Failed to start SSH master for %s", target)
	}

	m.mu.Lock()
	m.activeHosts[key] = host
	_, hasInfo := m.hostInfoCache[key]
	m.mu.Unlock()
	if !hasInfo {
		if _, ok := m.loadHostInfoFromDisk(host); !ok {
			if _, pErr := m.probeHostInfo(ctx, host); pErr != nil {
				return pErr
			}
		}
	}
	return nil
}

func (m *Manager) closeConnectionInternal(ctx context.Context, host ConnectionTarget) {
	if !SupportsControlMaster(m.platform()) {
		return
	}
	target, err := BuildSSHTarget(host.Username, host.Host)
	if err != nil {
		return
	}
	owner, err := m.resolveControlPathOwner(ctx, host, m.platform())
	if err != nil || owner == controlOwnerConfig {
		return
	}
	args := append([]string{"-O", "exit"}, m.buildCommonArgs(host, owner, ArgsOptions{})...)
	args = append(args, "--", target)
	_, _ = m.execSSH(ctx, args, nil, 5*time.Second)
}

// CloseSession closes ControlMaster sockets owned exclusively by sessionID
// (leaving connections still referenced by other active sessions untouched).
func (m *Manager) CloseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	touched := m.sessionHosts[sessionID]
	delete(m.sessionHosts, sessionID)
	if len(touched) == 0 {
		m.mu.Unlock()
		return
	}
	var toClose []ConnectionTarget
	for hostKey := range touched {
		stillUsed := false
		for _, otherHosts := range m.sessionHosts {
			if otherHosts[hostKey] {
				stillUsed = true
				break
			}
		}
		if !stillUsed {
			if host, ok := m.activeHosts[hostKey]; ok {
				toClose = append(toClose, host)
				delete(m.activeHosts, hostKey)
				delete(m.verifiedConnections, hostKey)
			}
		}
	}
	m.mu.Unlock()

	ctx := context.Background()
	for _, host := range toClose {
		m.closeConnectionInternal(ctx, host)
	}
}

// CloseAllConnections shuts down every active omp-owned ControlMaster socket.
// upstream: connection-manager.ts:832-838
func (m *Manager) CloseAllConnections() {
	m.mu.Lock()
	hosts := make([]ConnectionTarget, 0, len(m.activeHosts))
	for name, host := range m.activeHosts {
		hosts = append(hosts, host)
		delete(m.activeHosts, name)
		delete(m.verifiedConnections, name)
	}
	m.sessionHosts = make(map[string]map[string]bool)
	m.mu.Unlock()

	ctx := context.Background()
	for _, host := range hosts {
		m.closeConnectionInternal(ctx, host)
	}
}

// ResetForTests clears all in-memory caches without spawning ssh.
func (m *Manager) ResetForTests() {
	m.mu.Lock()
	m.activeHosts = make(map[string]ConnectionTarget)
	m.pendingConnections = make(map[string]*pendingConn)
	m.hostInfoCache = make(map[string]HostInfo)
	m.controlPathOwners = make(map[string]controlPathOwner)
	m.verifiedConnections = make(map[string]verifiedConn)
	m.remoteHomes = make(map[string]string)
	m.sessionHosts = make(map[string]map[string]bool)
	m.mu.Unlock()
}

// SetHostInfoForTests injects a pre-probed HostInfo into the in-memory cache.
func (m *Manager) SetHostInfoForTests(hostName string, info HostInfo) {
	m.mu.Lock()
	m.hostInfoCache[hostName] = info
	m.mu.Unlock()
}
