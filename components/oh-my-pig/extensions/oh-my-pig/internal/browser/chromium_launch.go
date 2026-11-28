package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SystemChromiumCandidates returns platform-specific candidate paths for
// installed Chrome / Chromium / Edge / Brave binaries.
// upstream: systemChromiumCandidates in launch.ts:529
func SystemChromiumCandidates(platform, home string, lookPath func(string) (string, error)) []string {
	if platform == "" {
		platform = runtime.GOOS
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	var out []string
	switch platform {
	case "darwin":
		roots := []string{"/Applications"}
		if home != "" {
			roots = append(roots, filepath.Join(home, "Applications"))
		}
		for _, root := range roots {
			out = append(out,
				filepath.Join(root, "Google Chrome.app/Contents/MacOS/Google Chrome"),
				filepath.Join(root, "Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta"),
				filepath.Join(root, "Google Chrome Dev.app/Contents/MacOS/Google Chrome Dev"),
				filepath.Join(root, "Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary"),
				filepath.Join(root, "Chromium.app/Contents/MacOS/Chromium"),
				filepath.Join(root, "Brave Browser.app/Contents/MacOS/Brave Browser"),
				filepath.Join(root, "Microsoft Edge.app/Contents/MacOS/Microsoft Edge"),
			)
		}
	case "linux":
		for _, name := range []string{
			"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome",
			"brave-browser", "microsoft-edge-stable", "microsoft-edge",
		} {
			if p, err := lookPath(name); err == nil && p != "" {
				out = append(out, p)
			}
		}
		out = append(out,
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/brave-browser",
			"/usr/bin/microsoft-edge-stable",
			"/usr/bin/microsoft-edge",
			"/snap/bin/chromium",
			"/var/lib/flatpak/exports/bin/com.google.Chrome",
			"/var/lib/flatpak/exports/bin/org.chromium.Chromium",
		)
		if _, err := os.Stat("/etc/NIXOS"); err == nil {
			if home != "" {
				out = append(out, filepath.Join(home, ".nix-profile/bin/chromium"))
			}
			out = append(out, "/run/current-system/sw/bin/chromium")
		}
		for _, name := range []string{"ungoogled-chromium", "ungoogled-chromium-browser"} {
			if p, err := lookPath(name); err == nil && p != "" {
				out = append(out, p)
			}
		}
		out = append(out,
			"/usr/bin/ungoogled-chromium",
			"/usr/bin/ungoogled-chromium-browser",
			"/var/lib/flatpak/exports/bin/io.github.ungoogled_software.ungoogled_chromium",
		)
		if home != "" {
			out = append(out, filepath.Join(home, ".local/share/flatpak/exports/bin/io.github.ungoogled_software.ungoogled_chromium"))
		}
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		pf86 := os.Getenv("ProgramFiles(x86)")
		if pf86 == "" {
			pf86 = `C:\Program Files (x86)`
		}
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" && home != "" {
			localApp = filepath.Join(home, `AppData\Local`)
		}
		out = append(out,
			filepath.Join(pf, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(localApp, `Google\Chrome\Application\chrome.exe`),
			filepath.Join(pf, `Chromium\Application\chrome.exe`),
			filepath.Join(localApp, `Chromium\Application\chrome.exe`),
			filepath.Join(pf, `BraveSoftware\Brave-Browser\Application\brave.exe`),
			filepath.Join(localApp, `BraveSoftware\Brave-Browser\Application\brave.exe`),
			filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`),
		)
	}
	return out
}

// FindChromiumExecutable locates a Chrome/Chromium/Brave/Edge executable.
// PUPPETEER_EXECUTABLE_PATH always wins. Managed Chrome for Testing download
// is not ported; when no installed browser is found, returns an omp-style
// guidance error.
// upstream: ensureChromiumExecutable + resolveSystemChromium in launch.ts:354,605
func FindChromiumExecutable(lookup func(string) (string, bool)) (string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if envPath, ok := lookup("PUPPETEER_EXECUTABLE_PATH"); ok && strings.TrimSpace(envPath) != "" {
		p := expandTilde(strings.TrimSpace(envPath))
		if isExecutableFile(p, runtime.GOOS) {
			return p, nil
		}
		return "", fmt.Errorf("PUPPETEER_EXECUTABLE_PATH points to %q, which is not an executable file.", p)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if cached := findCachedChromeForTesting(filepath.Join(home, ".omp", "puppeteer")); cached != "" {
			return cached, nil
		}
	}

	seen := map[string]bool{}
	for _, cand := range SystemChromiumCandidates(runtime.GOOS, "", exec.LookPath) {
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		if isChromiumExecutable(cand) {
			return cand, nil
		}
	}
	return "", fmt.Errorf(
		"No Chrome/Chromium/Brave/Edge installation found (automatic Chrome for Testing download is not ported in oh-my-pig). " +
			"Install Chrome or Chromium, set PUPPETEER_EXECUTABLE_PATH to an existing Chrome/Chromium binary, " +
			`or pass a CDP endpoint via browser.cdpUrl / app: "http://127.0.0.1:9222".`,
	)
}

func findCachedChromeForTesting(cacheDir string) string {
	entries, err := os.ReadDir(filepath.Join(cacheDir, "chrome"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		base := filepath.Join(cacheDir, "chrome", entry.Name())
		var candidates []string
		switch runtime.GOOS {
		case "darwin":
			candidates = []string{
				filepath.Join(base, "chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"),
				filepath.Join(base, "chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"),
			}
		case "linux":
			candidates = []string{filepath.Join(base, "chrome-linux64/chrome")}
		case "windows":
			candidates = []string{
				filepath.Join(base, `chrome-win64\chrome.exe`),
				filepath.Join(base, `chrome-win32\chrome.exe`),
			}
		}
		for _, c := range candidates {
			if isExecutableFile(c, runtime.GOOS) {
				return c
			}
		}
	}
	return ""
}

func isChromiumExecutable(p string) bool {
	if !isExecutableFile(p, runtime.GOOS) {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "--version").Output()
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(out))
	return strings.Contains(lower, "chrom") || strings.Contains(lower, "edg") || strings.Contains(lower, "brave")
}

// IsChromiumBrowserExecutable distinguishes Chromium-family browsers from
// Electron apps that also accept `--remote-debugging-port`.
// upstream: isChromiumBrowserExecutable in attach.ts:228-239
var chromiumBasenameRE = regexp.MustCompile(`(?i)^(?:google[ -]chrome|chrome|chromium|microsoft[ -]edge|msedge|brave|vivaldi|opera|thorium|ungoogled[ -]chromium)(?:[ -](?:beta|dev|canary|unstable|stable|nightly|snapshot|browser|gx|for[ -]testing))*$`)

var chromiumFlatpakIDs = map[string]bool{
	"com.google.Chrome":                               true,
	"org.chromium.Chromium":                           true,
	"io.github.ungoogled_software.ungoogled_chromium": true,
}

func IsChromiumBrowserExecutable(exe string) bool {
	base := filepath.Base(exe)
	if strings.HasSuffix(strings.ToLower(base), ".exe") {
		base = base[:len(base)-4]
	}
	return chromiumBasenameRE.MatchString(base) || chromiumFlatpakIDs[base]
}

// FindUserDataDirInArgs extracts `--user-data-dir=<path>` or `--user-data-dir <path>`.
// upstream: findUserDataDirInArgs in attach.ts:205-221
func FindUserDataDirInArgs(args []string) string {
	var result string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--user-data-dir=") {
			result = strings.TrimPrefix(arg, "--user-data-dir=")
			continue
		}
		if arg == "--user-data-dir" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			result = args[i+1]
			i++
		}
	}
	return result
}

// ResolveSpawnArgs prepares launch argv for a spawned executable:
//   - Non-Chromium executables (e.g. Electron apps) keep their args untouched so
//     their own user-data directory is preserved.
//   - Chromium-family browsers without `--user-data-dir` receive a persistent
//     omp-owned profile directory under `~/.omp/browser-profiles/<slug>-<hash>`
//     plus `--no-first-run`, `--no-default-browser-check`, `--use-mock-keychain`,
//     and `--password-store=basic`.
//
// upstream: resolveSpawnArgs in attach.ts:258-292
func ResolveSpawnArgs(exe string, appArgs []string, cwd string) []string {
	args := append([]string(nil), appArgs...)
	if !IsChromiumBrowserExecutable(exe) {
		return args
	}
	if requested := FindUserDataDirInArgs(args); requested != "" {
		out := make([]string, 0, len(args)+1)
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "--user-data-dir" {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
					i++
				}
				continue
			}
			if strings.HasPrefix(arg, "--user-data-dir=") {
				continue
			}
			out = append(out, arg)
		}
		absProfile := resolveToCwd(requested, cwd)
		if a, err := filepath.Abs(absProfile); err == nil {
			absProfile = a
		}
		out = append(out, "--user-data-dir="+absProfile)
		return out
	}

	base := strings.ToLower(filepath.Base(exe))
	base = strings.TrimSuffix(base, ".exe")
	var slug strings.Builder
	prevDash := false
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			slug.WriteRune(r)
			prevDash = false
		} else if !prevDash {
			slug.WriteByte('-')
			prevDash = true
		}
	}
	slugStr := strings.Trim(slug.String(), "-")
	if slugStr == "" {
		slugStr = "browser"
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(exe))
	profileDir := filepath.Join(browserProfilesDir(), fmt.Sprintf("%s-%016x", slugStr, h.Sum64()))

	for _, flag := range []string{"--no-first-run", "--no-default-browser-check", "--use-mock-keychain"} {
		if !containsStr(args, flag) {
			args = append(args, flag)
		}
	}
	hasPwStore := false
	for _, a := range args {
		if strings.HasPrefix(a, "--password-store") {
			hasPwStore = true
			break
		}
	}
	if !hasPwStore {
		args = append(args, "--password-store=basic")
	}
	args = append(args, "--user-data-dir="+profileDir)
	return args
}

func browserProfilesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "omp-browser-profiles")
	}
	return filepath.Join(home, ".omp", "browser-profiles")
}

// FindFreeTCPPort allocates an ephemeral loopback TCP port (used for spawned
// non-Chromium apps that do not publish DevToolsActivePort).
// upstream: findFreeCdpPort in attach.ts:21
func FindFreeTCPPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to allocate ephemeral CDP port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

// seedOwnedProfilePreferences disables Chrome's print preview and password leak
// dialogs inside an omp-owned profile before launch.
// upstream: seedOwnedProfilePreferences in launch.ts:827
func seedOwnedProfilePreferences(userDataDir string) {
	defDir := filepath.Join(userDataDir, "Default")
	if err := os.MkdirAll(defDir, 0o700); err != nil {
		return
	}
	prefs := map[string]any{
		"printing": map[string]any{"enabled": false},
		"profile":  map[string]any{"password_manager_leak_detection": false},
	}
	data, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(defDir, "Preferences"), data, 0o600)
}

// buildHeadlessLaunchArgs produces Chromium CLI flags matching omp's launch.ts
// plus the Puppeteer 25.3.0 defaultArgs that omp keeps after stealthIgnoreDefaultArgs
// and its ChromeLauncher patch (launch.ts:655-689, 741-743; patches/puppeteer-core@25.3.0.patch).
// Uses `--remote-debugging-port=0` (and `--remote-debugging-pipe` on Unix) so
// Chromium binds an ephemeral port directly and writes `DevToolsActivePort`
// inside our private profile directory, tying Chromium's lifetime to PiG's process.
func buildHeadlessLaunchArgs(vp Viewport, userDataDir string, headless, insecure, usePipe bool) []string {
	args := []string{
		// Puppeteer 25.3.0 defaultArgs retained after omp's stealthIgnoreDefaultArgs:
		"--disable-background-networking",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-breakpad",
		"--disable-crash-reporter",
		"--disable-dev-shm-usage",
		"--disable-hang-monitor",
		"--disable-infobars",
		"--disable-prompt-on-repost",
		"--disable-renderer-backgrounding",
		"--disable-search-engine-choice-screen",
		"--disable-sync",
		"--export-tagged-pdf",
		"--force-color-profile=srgb",
		"--generate-pdf-document-outline",
		"--no-first-run",
		"--no-default-browser-check",
		"--password-store=basic",
		"--use-mock-keychain",
		"--enable-features=PdfOopif",
		// omp buildHeadlessLaunchArgs (launch.ts:659-669):
		"--no-sandbox",
		"--disable-setuid-sandbox",
		"--disable-blink-features=AutomationControlled",
		"--allow-file-access-from-files",
		"--hide-scrollbars",
		fmt.Sprintf("--window-size=%d,%d", vp.Width, vp.Height),
		"--no-startup-window",
		"--remote-debugging-port=0",
		"--user-data-dir=" + userDataDir,
	}
	if usePipe {
		args = append(args, "--remote-debugging-pipe")
	}
	if headless {
		args = append(args, "--headless=new", "--mute-audio")
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "--disable-features=MacAllowBackgroundingRenderProcesses")
		if headless {
			// Port addition (not in omp): on a locked macOS display
			// (CGSSessionScreenIsLocked=Yes), WindowServer/CVDisplayLink pauses
			// BeginFrame ticks after ~0.5s idle, stalling rAF, captureScreenshot,
			// and standalone mouseMoved/hover (5s timeout) in both omp and the port.
			// --run-all-compositor-stages-before-draw keeps headless BeginFrame
			// pumping on demand without disabling GPU/WebGL (see UPSTREAM.md).
			args = append(args, "--run-all-compositor-stages-before-draw")
		}
	}
	if insecure {
		args = append(args, "--ignore-certificate-errors")
	}
	if proxy := os.Getenv("PUPPETEER_PROXY"); proxy != "" {
		args = append(args, "--proxy-server="+proxy)
		if ParseFlag(os.Getenv("PUPPETEER_PROXY_BYPASS_LOOPBACK"), false) {
			args = append(args, "--proxy-bypass-list=<-loopback>")
		}
	}
	if ParseFlag(os.Getenv("PUPPETEER_PROXY_IGNORE_CERT_ERRORS"), false) && !insecure {
		args = append(args, "--ignore-certificate-errors")
	}
	args = append(args, "about:blank")
	return args
}

type launchedBrowser struct {
	cmd         *exec.Cmd
	pid         int
	exePath     string
	cdpURL      string
	wsURL       string
	userDataDir string // non-empty only if we created a temp profile that stop() removes
	owned       bool
	pipeFiles   []*os.File // parent ends of --remote-debugging-pipe
	exited      chan error
	done        chan struct{} // closed once cmd.Wait() returns (safe for multiple waiters)
	stopOnce    sync.Once
}

// launchHeadlessChromiumProcess launches Chromium with `--remote-debugging-port=0`
// (plus `--remote-debugging-pipe` on Unix) and reads the actual ephemeral port
// and browser WebSocket path from `<userDataDir>/DevToolsActivePort`.
// upstream: launchHeadlessBrowser + activePortEndpoint in launch.ts:725-805
const (
	tempProfilePrefix  = "omp-chrome-profile-"
	profileOwnerMarker = ".omp-owner-pid"
)

// cleanupStaleProfiles removes leftover `omp-chrome-profile-*` directories under
// os.TempDir() whose recorded owner AND browser PIDs (plus Chromium's
// SingletonLock PID, if present) are all dead. Never touches live processes,
// symlinks, directories without our prefix, or anything outside os.TempDir().
func cleanupStaleProfiles() {
	cleanupStaleProfilesIn(os.TempDir())
}

func writeProfileOwnerMarker(profileDir string, ownerPID, browserPID int) {
	var content string
	if browserPID > 0 {
		content = fmt.Sprintf("%d\n%d\n", ownerPID, browserPID)
	} else {
		content = fmt.Sprintf("%d\n", ownerPID)
	}
	_ = os.WriteFile(filepath.Join(profileDir, profileOwnerMarker), []byte(content), 0o600)
}

// parseProfileMarkerPIDs parses one or two positive PIDs from the marker file
// (line 1: owner PID; optional line 2: browser PID). Returns nil on malformed input.
func parseProfileMarkerPIDs(raw []byte) []int {
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || len(fields) > 2 {
		return nil
	}
	pids := make([]int, 0, len(fields))
	for _, f := range fields {
		pid, err := strconv.Atoi(f)
		if err != nil || pid <= 0 {
			return nil
		}
		pids = append(pids, pid)
	}
	return pids
}

// singletonLockAlive reports true if Chromium's `SingletonLock` symlink in dir
// points to `<host>-<pid>` where `<pid>` is still alive.
func singletonLockAlive(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	if idx := strings.LastIndexByte(target, '-'); idx >= 0 && idx+1 < len(target) {
		if pid, err := strconv.Atoi(target[idx+1:]); err == nil && pid > 0 {
			return processAlive(pid)
		}
	}
	return false
}

const staleEmptyProfileMinAge = 10 * time.Minute

func cleanupStaleProfilesIn(baseDir string) {
	cleanupStaleProfilesInAt(baseDir, time.Now())
}

func cleanupStaleProfilesInAt(baseDir string, now time.Time) {
	matches, err := filepath.Glob(filepath.Join(baseDir, tempProfilePrefix+"*"))
	if err != nil {
		return
	}
	for _, dir := range matches {
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		markerPath := filepath.Join(dir, profileOwnerMarker)
		mfi, err := os.Lstat(markerPath)
		if err != nil {
			// Sweep completely empty prefixed profile dirs left behind by a
			// crashed/interrupted stop once they are older than a safe age.
			// Uses Lstat (above) and os.Remove (only removes an empty dir).
			if os.IsNotExist(err) && now.Sub(fi.ModTime()) >= staleEmptyProfileMinAge {
				if entries, rerr := os.ReadDir(dir); rerr == nil && len(entries) == 0 {
					_ = os.Remove(dir)
				}
			}
			continue
		}
		if !mfi.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(markerPath)
		if err != nil {
			continue
		}
		pids := parseProfileMarkerPIDs(raw)
		if len(pids) == 0 {
			continue
		}
		alive := false
		for _, pid := range pids {
			if processAlive(pid) {
				alive = true
				break
			}
		}
		if !alive && singletonLockAlive(dir) {
			alive = true
		}
		if !alive {
			_ = os.RemoveAll(dir)
		}
	}
}

func launchHeadlessChromiumProcess(ctx context.Context, exe string, vp Viewport, headless, insecure bool) (*launchedBrowser, error) {
	cleanupStaleProfiles()
	profileDir, err := os.MkdirTemp("", tempProfilePrefix)
	if err != nil {
		return nil, err
	}
	writeProfileOwnerMarker(profileDir, os.Getpid(), 0)
	seedOwnedProfilePreferences(profileDir)
	activePortPath := filepath.Join(profileDir, "DevToolsActivePort")
	staleContent, _ := os.ReadFile(activePortPath)
	cmd := exec.Command(exe)
	configureChildProcess(cmd)
	parentPipes, childPipes, hasPipe := attachDebuggingPipe(cmd)
	args := buildHeadlessLaunchArgs(vp, profileDir, headless, insecure, hasPipe)
	cmd.Args = append([]string{exe}, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		for _, f := range childPipes {
			_ = f.Close()
		}
		for _, f := range parentPipes {
			_ = f.Close()
		}
		_ = os.RemoveAll(profileDir)
		return nil, fmt.Errorf("failed to launch %s: %w", filepath.Base(exe), err)
	}
	// Close child ends in parent immediately after Start so only parentEnds keep the pipe alive.
	for _, f := range childPipes {
		_ = f.Close()
	}
	if cmd.Process != nil && cmd.Process.Pid > 0 {
		writeProfileOwnerMarker(profileDir, os.Getpid(), cmd.Process.Pid)
	}

	exited := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		exited <- cmd.Wait()
		close(done)
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var (
		cdpURL string
		wsURL  string
	)
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		if raw, rerr := os.ReadFile(activePortPath); rerr == nil && len(raw) > 0 && string(raw) != string(staleContent) {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) >= 2 {
				portStr := strings.TrimSpace(lines[0])
				browserPath := strings.TrimSpace(lines[1])
				if portNum, perr := strconv.Atoi(portStr); perr == nil && portNum > 0 && strings.HasPrefix(browserPath, "/") {
					cdpURL = fmt.Sprintf("http://127.0.0.1:%d", portNum)
					wsURL = fmt.Sprintf("ws://127.0.0.1:%d%s", portNum, browserPath)
					break
				}
			}
		}
		select {
		case exitErr := <-exited:
			for _, f := range parentPipes {
				_ = f.Close()
			}
			_ = os.RemoveAll(profileDir)
			return nil, fmt.Errorf("browser %s exited before publishing DevToolsActivePort: %v", filepath.Base(exe), exitErr)
		case <-waitCtx.Done():
			for _, f := range parentPipes {
				_ = f.Close()
			}
			killProcessTree(cmd, 500*time.Millisecond)
			<-exited
			_ = os.RemoveAll(profileDir)
			return nil, fmt.Errorf("Chromium did not publish its DevTools port in %s", activePortPath)
		case <-ticker.C:
		}
	}

	return &launchedBrowser{
		cmd:         cmd,
		pid:         cmd.Process.Pid,
		exePath:     exe,
		cdpURL:      cdpURL,
		wsURL:       wsURL,
		userDataDir: profileDir,
		owned:       true,
		pipeFiles:   parentPipes,
		exited:      exited,
		done:        done,
	}, nil
}

// launchSpawnedProcess launches a user-supplied executable (Chromium or Electron)
// using an ephemeral `--remote-debugging-port` and waits for `/json/version`.
// upstream: openBrowserHandle (spawned) in registry.ts:358-395
func launchSpawnedProcess(ctx context.Context, exe string, args []string, port int) (*launchedBrowser, error) {
	cmd := exec.Command(exe, args...)
	configureChildProcess(cmd)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to launch %s: %w", filepath.Base(exe), err)
	}

	exited := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		exited <- cmd.Wait()
		close(done)
	}()
	cdpURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var wsURL string
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		u, err := FetchCDPWebSocketURL(waitCtx, cdpURL)
		if err == nil {
			wsURL = u
			break
		}
		select {
		case exitErr := <-exited:
			return nil, fmt.Errorf("browser %s exited before DevTools became ready: %v", filepath.Base(exe), exitErr)
		case <-waitCtx.Done():
			killProcessTree(cmd, 500*time.Millisecond)
			<-exited
			return nil, fmt.Errorf("Failed to attach to %s on %s: %v", filepath.Base(exe), cdpURL, err)
		case <-ticker.C:
		}
	}

	return &launchedBrowser{
		cmd:     cmd,
		pid:     cmd.Process.Pid,
		exePath: exe,
		cdpURL:  cdpURL,
		wsURL:   wsURL,
		owned:   true,
		exited:  exited,
		done:    done,
	}, nil
}

func (lb *launchedBrowser) stop() {
	if lb == nil {
		return
	}
	lb.stopOnce.Do(func() {
		for _, f := range lb.pipeFiles {
			_ = f.Close()
		}
		lb.pipeFiles = nil
		if lb.owned && lb.cmd != nil {
			// Owned temp profile: nothing to save, terminate promptly (upstream launch.ts:775).
			grace := 500 * time.Millisecond
			if lb.userDataDir != "" {
				grace = 0
			}
			killProcessTree(lb.cmd, grace)
			if lb.done != nil {
				select {
				case <-lb.done:
				case <-time.After(5 * time.Second):
					_ = lb.cmd.Process.Kill()
					select {
					case <-lb.done:
					case <-time.After(2 * time.Second):
					}
				}
			}
		}
		if lb.userDataDir != "" {
			for range 10 {
				if err := os.RemoveAll(lb.userDataDir); err == nil {
					if _, serr := os.Lstat(lb.userDataDir); os.IsNotExist(serr) {
						break
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	})
}
