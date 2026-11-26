package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRealChromeLoadExtensionAndNativeHost verifies end-to-end against real Chrome for Testing
// (in --headless=new with a temporary --user-data-dir and temporary HOME/PIG_CODING_AGENT_DIR):
//  1. `InstallRelayExtension` writes a valid MV3 extension Chrome loads via --load-extension.
//  2. The extension ID Chrome assigns matches `ExtensionIDForPath` exactly.
//  3. Putting the generated native messaging host manifest in `<user-data-dir>/NativeMessagingHosts/`
//     causes Chrome's service worker (`chrome.runtime.connectNative(NativeHostName)`) to execute
//     the native host launcher with the matching extension origin.
//
// Gated behind OMP_REAL_BROWSER=1 (same pattern as internal/browser/pass2_real_test.go).
func TestRealChromeLoadExtensionAndNativeHost(t *testing.T) {
	if os.Getenv("OMP_REAL_BROWSER") == "" {
		t.Skip("skipping real Chrome for Testing test (set OMP_REAL_BROWSER=1 to run)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper script test; Windows uses HKCU registry lookup")
	}

	chromeBin := findChromeForTesting()
	if chromeBin == "" {
		t.Skip("Chrome for Testing binary not found")
	}

	tmp := isolateRelayEnv(t)
	installDir, err := InstallRelayExtension(DefaultExtensionDir(), InstallLocations{
		RelayURLSet: true,
		Version:     "0.1.0",
	})
	if err != nil {
		t.Fatalf("InstallRelayExtension: %v", err)
	}
	computedID := ExtensionIDForPath(installDir, runtime.GOOS)

	// Create a probe script that records Chrome's invocation and exits 1 (matching stub RunNativeHost).
	traceLog := filepath.Join(tmp, "native-host-trace.log")
	probeCmd := filepath.Join(tmp, "probe-host.sh")
	probeScript := fmt.Sprintf("#!/bin/sh\necho \"INVOKED:$*\" >> %q\necho \"browser-relay native-host: reached\" >&2\nexit 1\n", traceLog)
	if err := os.WriteFile(probeCmd, []byte(probeScript), 0o700); err != nil {
		t.Fatal(err)
	}

	// Write the native host launcher + manifests into temp HOME via WriteRelayNativeHost.
	targets, err := WriteRelayNativeHost([]string{computedID}, NativeHostOptions{
		Platform: runtime.GOOS,
		Home:     tmp,
		Dir:      Root(),
		Command:  []string{probeCmd},
	})
	if err != nil {
		t.Fatalf("WriteRelayNativeHost: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("WriteRelayNativeHost returned no manifest paths")
	}
	manifestBytes, err := os.ReadFile(targets[0])
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}

	// Chromium looks up user-level native messaging hosts inside `<user-data-dir>/NativeMessagingHosts/`
	// (DIR_USER_NATIVE_MESSAGING is derived from DIR_USER_DATA in Chromium's chrome_paths.cc).
	userDataDir := filepath.Join(tmp, "chrome-profile")
	nmhPath := filepath.Join(userDataDir, "NativeMessagingHosts", NativeHostName+".json")
	if err := writeFileAtomic(nmhPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write profile NativeMessagingHosts manifest: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, chromeBin,
		"--headless=new",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-gpu",
		"--remote-debugging-port=0",
		"--user-data-dir="+userDataDir,
		"--disable-extensions-except="+installDir,
		"--load-extension="+installDir,
		"about:blank",
	)
	cmd.Env = append(os.Environ(), "HOME="+tmp)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start Chrome for Testing: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	// Wait for the native host launcher to be invoked by Chrome's extension service worker.
	deadline := time.Now().Add(15 * time.Second)
	var traceText string
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(traceLog); err == nil && len(b) > 0 {
			traceText = string(b)
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if traceText == "" {
		t.Fatalf("Chrome did not invoke native host %s within timeout", NativeHostName)
	}
	// Note: the generated launcher discards Chrome's origin arg and execs `<probeCmd> browser-relay native-host`.
	if !strings.Contains(traceText, "INVOKED:browser-relay native-host") {
		t.Fatalf("unexpected native host invocation trace: %q", traceText)
	}

	// Verify via DevTools /json/list that Chrome assigned the exact computed extension ID.
	portFile := filepath.Join(userDataDir, "DevToolsActivePort")
	portBytes, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read DevToolsActivePort: %v", err)
	}
	port := strings.TrimSpace(strings.Split(string(portBytes), "\n")[0])
	resp, err := http.Get("http://127.0.0.1:" + port + "/json/list")
	if err != nil {
		t.Fatalf("GET /json/list: %v", err)
	}
	defer resp.Body.Close()
	var targetsList []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targetsList); err != nil {
		t.Fatalf("decode /json/list: %v", err)
	}
	wantWorkerURL := "chrome-extension://" + computedID + "/background.js"
	foundWorker := false
	for _, target := range targetsList {
		if target.Type == "service_worker" && target.URL == wantWorkerURL {
			foundWorker = true
			break
		}
	}
	if !foundWorker {
		t.Fatalf("expected service_worker %q in /json/list, got %#v", wantWorkerURL, targetsList)
	}
}

func findChromeForTesting() string {
	// Check explicit env override first, then ~/.omp/puppeteer/chrome (real HOME before isolateRelayEnv).
	if p := os.Getenv("OMP_CHROME_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	realHome := os.Getenv("HOME")
	if realHome == "" {
		return ""
	}
	candidates, _ := filepath.Glob(filepath.Join(
		realHome, ".omp", "puppeteer", "chrome", "*", "*",
		"Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing",
	))
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	linuxCandidates, _ := filepath.Glob(filepath.Join(
		realHome, ".omp", "puppeteer", "chrome", "*", "chrome-linux64", "chrome",
	))
	if len(linuxCandidates) > 0 {
		return linuxCandidates[len(linuxCandidates)-1]
	}
	return ""
}
