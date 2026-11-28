// Native messaging host launcher and manifest installation for Chromium-family browsers.
//
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts (omp v19.1.1)
package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// chromeRegistryKey is the HKCU registry key Chrome reads for native messaging hosts on Windows.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:11
const chromeRegistryKey = `HKCU\Software\Google\Chrome\NativeMessagingHosts\` + NativeHostName

// NativeHostOptions overrides locations and executable used to install the native messaging host.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:14-20
type NativeHostOptions struct {
	Platform   string   // "darwin", "linux", "windows" (or "win32"); defaults to runtime.GOOS
	Home       string   // overrides user home for Chromium browser manifest locations
	ConfigHome string   // Linux $XDG_CONFIG_HOME
	Dir        string   // directory holding the launcher (and Windows manifest); defaults to Root()
	Command    []string // executable + leading args; defaults to [os.Executable()]
}

type nativeHostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func nativeHostLauncherPath(dir, platform string) string {
	if isWindowsPlatform(platform) {
		return filepath.Join(dir, "native-host.cmd")
	}
	return filepath.Join(dir, "native-host")
}

func isWindowsPlatform(platform string) bool {
	return platform == "windows" || platform == "win32"
}

// nativeHostManifestPaths returns every manifest file path written for platform.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:30-38
func nativeHostManifestPaths(platform string, opts NativeHostOptions, dir string) []string {
	file := NativeHostName + ".json"
	if isWindowsPlatform(platform) {
		return []string{filepath.Join(dir, file)}
	}
	browsers := ChromiumUserDataDirs(ChromiumLocations{
		Platform:   platform,
		Home:       opts.Home,
		ConfigHome: opts.ConfigHome,
	})
	paths := make([]string, 0, len(browsers))
	for _, b := range browsers {
		paths = append(paths, filepath.Join(b.Dir, "NativeMessagingHosts", file))
	}
	return paths
}

// quotePosixArgument quotes one argument for a POSIX /bin/sh command line.
// upstream: packages/coding-agent/src/utils/shell-quote.ts:4-6
func quotePosixArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// quotePosixArgv quotes argv for a POSIX /bin/sh command line.
// upstream: packages/coding-agent/src/utils/shell-quote.ts:9-11
func quotePosixArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = quotePosixArgument(arg)
	}
	return strings.Join(quoted, " ")
}

// nativeHostLauncherText renders the native host wrapper script for platform.
// Chrome passes origin/window metadata arguments to the launcher, which the wrapper discards.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:40-46
func nativeHostLauncherText(platform string, command []string) string {
	argv := make([]string, 0, len(command)+2)
	argv = append(argv, command...)
	argv = append(argv, CLICommand, "native-host")
	if !isWindowsPlatform(platform) {
		return "#!/bin/sh\nexec " + quotePosixArgv(argv) + "\n"
	}
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = `"` + strings.ReplaceAll(arg, "%", "%%") + `"`
	}
	return "@echo off\r\nsetlocal DisableDelayedExpansion\r\n" + strings.Join(parts, " ") + "\r\nexit /b %errorlevel%\r\n"
}

func resolveNativeHostCommand(explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		return explicit, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path for native host launcher: %w", err)
	}
	return []string{exe}, nil
}

func writeNativeHostLauncher(file, platform string, command []string) error {
	script := nativeHostLauncherText(platform, command)
	if prior, err := os.ReadFile(file); err == nil {
		if string(prior) == script {
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeFileAtomic(file, []byte(script), 0o700)
}

func resolveNativeHostTargets(opts NativeHostOptions) (platform, dir, launcher string, targets []string, err error) {
	platform = opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	baseDir := opts.Dir
	if baseDir == "" {
		baseDir = filepath.Dir(InstallsFile())
	}
	dir, err = filepath.Abs(baseDir)
	if err != nil {
		return "", "", "", nil, err
	}
	targets = nativeHostManifestPaths(platform, opts, dir)
	launcher = nativeHostLauncherPath(dir, platform)
	return platform, dir, launcher, targets, nil
}

// nativeHostManifestText renders the Chrome native messaging host manifest JSON.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:72-81
func nativeHostManifestText(launcher string, extensionIDs []string) ([]byte, error) {
	seen := make(map[string]struct{}, len(extensionIDs))
	unique := make([]string, 0, len(extensionIDs))
	for _, id := range extensionIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	sort.Strings(unique)
	origins := make([]string, len(unique))
	for i, id := range unique {
		origins[i] = "chrome-extension://" + id + "/"
	}
	m := nativeHostManifest{
		Name:           NativeHostName,
		Description:    ExtensionName + " browser native messaging host",
		Path:           launcher,
		Type:           "stdio",
		AllowedOrigins: origins,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteRelayNativeHost materializes the native host launcher and Chromium manifests admitting extensionIDs.
// On Windows the returned manifest path must also be registered in HKCU (see RegisterRelayNativeHost).
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:87-103
func WriteRelayNativeHost(extensionIDs []string, opts NativeHostOptions) ([]string, error) {
	platform, dir, launcher, targets, err := resolveNativeHostTargets(opts)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return targets, nil
	}
	cmd, err := resolveNativeHostCommand(opts.Command)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := writeNativeHostLauncher(launcher, platform, cmd); err != nil {
		return nil, err
	}
	content, err := nativeHostManifestText(launcher, extensionIDs)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		if err := writeFileAtomic(target, content, 0o644); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// regAddFunc runs `reg.exe add <key> /ve /t REG_SZ /d <manifest> /f` on Windows; overridden in tests.
var regAddFunc = func(key, manifest string) error {
	cmd := exec.Command("reg.exe", "add", key, "/ve", "/t", "REG_SZ", "/d", manifest, "/f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("Could not register the %s native messaging host: %s", ExtensionName, msg)
	}
	return nil
}

// RegisterRelayNativeHost writes the native host launcher and manifests, and on Windows registers
// the manifest path under HKCU\Software\Google\Chrome\NativeMessagingHosts\<NativeHostName>.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:106-113
func RegisterRelayNativeHost(extensionIDs []string) error {
	return RegisterRelayNativeHostWithOptions(extensionIDs, NativeHostOptions{})
}

// RegisterRelayNativeHostWithOptions is RegisterRelayNativeHost with explicit options (used by tests).
func RegisterRelayNativeHostWithOptions(extensionIDs []string, opts NativeHostOptions) error {
	targets, err := WriteRelayNativeHost(extensionIDs, opts)
	if err != nil {
		return err
	}
	platform := opts.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if !isWindowsPlatform(platform) || len(targets) == 0 {
		return nil
	}
	return regAddFunc(chromeRegistryKey, targets[0])
}

// RefreshRelayNativeHost updates an existing launcher and widens already-existing browser manifests
// to extensionIDs without creating registrations for browsers that had none.
// upstream: packages/coding-agent/src/tools/browser/relay/native-host-manifest.ts:119-138
func RefreshRelayNativeHost(extensionIDs []string, opts NativeHostOptions) error {
	platform, _, launcher, targets, err := resolveNativeHostTargets(opts)
	if err != nil {
		return err
	}
	if _, err := os.Stat(launcher); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	cmd, err := resolveNativeHostCommand(opts.Command)
	if err != nil {
		return err
	}
	if err := writeNativeHostLauncher(launcher, platform, cmd); err != nil {
		return err
	}
	content, err := nativeHostManifestText(launcher, extensionIDs)
	if err != nil {
		return err
	}
	for _, target := range targets {
		prior, err := os.ReadFile(target)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		if string(prior) != string(content) {
			if err := writeFileAtomic(target, content, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
