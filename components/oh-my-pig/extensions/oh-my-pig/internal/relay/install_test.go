package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// isolateRelayEnv points HOME, PIG_HOME, and PIG_CODING_AGENT_DIR at a temp directory.
func isolateRelayEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("PIG_HOME", filepath.Join(tmp, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(tmp, ".pig", "agent"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	return tmp
}

// TestExtensionIDForPath covers Chromium's GenerateIdForPath across Unix UTF-8 and Windows UTF-16LE
// (including ASCII drive-letter uppercasing).
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:159-173
func TestExtensionIDForPath(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		platform string
		want     string
	}{
		{
			name:     "darwin ASCII path",
			path:     "/Users/alice/.pig/agent/browser-relay/extension",
			platform: "darwin",
			want:     "ckfomhmpmlmggknfnhkmdnjnkgjkkckg",
		},
		{
			name:     "linux UTF-8 non-ASCII path",
			path:     "/home/café/oh-my-pig/extension",
			platform: "linux",
			want:     "cagkclojfllfofijbmkbgbknlkcjijdi",
		},
		{
			name:     "windows lowercase drive letter uppercased",
			path:     `c:\Users\alice\extension`,
			platform: "windows",
			want:     ExtensionIDForPath(`C:\Users\alice\extension`, "windows"),
		},
		{
			name:     "windows win32 alias matches windows",
			path:     `C:\Users\alice\extension`,
			platform: "win32",
			want:     "kngkclngckngnlejjgmbnkcpmpnnkkjh",
		},
	}

	// Verify exact cryptographic derivation against independent SHA-256 over raw bytes.
	computeRef := func(raw []byte) string {
		sum := sha256.Sum256(raw)
		var out strings.Builder
		for _, b := range sum[:16] {
			out.WriteByte('a' + (b >> 4))
			out.WriteByte('a' + (b & 0x0f))
		}
		return out.String()
	}

	cases[0].want = computeRef([]byte("/Users/alice/.pig/agent/browser-relay/extension"))
	cases[1].want = computeRef([]byte("/home/café/oh-my-pig/extension"))
	// UTF-16LE of `C:\Users\alice\extension`
	winRunes := []rune(`C:\Users\alice\extension`)
	winBytes := make([]byte, 0, len(winRunes)*2)
	for _, r := range winRunes {
		winBytes = append(winBytes, byte(r), byte(r>>8))
	}
	cases[3].want = computeRef(winBytes)
	cases[2].want = cases[3].want

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtensionIDForPath(tc.path, tc.platform)
			if got != tc.want {
				t.Fatalf("ExtensionIDForPath(%q, %q) = %q, want %q", tc.path, tc.platform, got, tc.want)
			}
			if len(got) != 32 {
				t.Fatalf("id length = %d, want 32", len(got))
			}
			for _, ch := range got {
				if ch < 'a' || ch > 'p' {
					t.Fatalf("id %q contains character outside [a-p]: %q", got, ch)
				}
			}
		})
	}

	// Also confirm Windows UTF-16LE differs from UTF-8 for the same string.
	if ExtensionIDForPath(`C:\ext`, "windows") == ExtensionIDForPath(`C:\ext`, "linux") {
		t.Fatal("expected Windows UTF-16LE extension ID to differ from Linux UTF-8 ID")
	}
}

// TestEmbeddedExtensionManifestAndBundle verifies the embedded relay-only extension's manifest,
// permissions, native host name, lack of side panel, and SHA-256 pin against extension.manifest.json.
func TestEmbeddedExtensionManifestAndBundle(t *testing.T) {
	bundle, err := LoadExtensionBundle()
	if err != nil {
		t.Fatalf("LoadExtensionBundle: %v", err)
	}

	var manifest struct {
		ManifestVersion int               `json:"manifest_version"`
		Name            string            `json:"name"`
		Version         string            `json:"version"`
		Description     string            `json:"description"`
		Author          string            `json:"author"`
		HomepageURL     string            `json:"homepage_url"`
		Permissions     []string          `json:"permissions"`
		HostPermissions []string          `json:"host_permissions"`
		SidePanel       any               `json:"side_panel"`
		Commands        any               `json:"commands"`
		Key             string            `json:"key"`
		Background      map[string]string `json:"background"`
		Icons           map[string]string `json:"icons"`
		Action          struct {
			DefaultTitle string            `json:"default_title"`
			DefaultIcon  map[string]string `json:"default_icon"`
		} `json:"action"`
	}
	if err := json.Unmarshal(bundle.Files["manifest.json"], &manifest); err != nil {
		t.Fatalf("parse embedded manifest.json: %v", err)
	}
	if manifest.ManifestVersion != 3 {
		t.Fatalf("manifest_version = %d, want 3", manifest.ManifestVersion)
	}
	if manifest.Name != ExtensionName {
		t.Fatalf("manifest.name = %q, want %q", manifest.Name, ExtensionName)
	}
	if manifest.Key != "" {
		t.Fatalf("manifest must not set key (path-derived IDs required), got %q", manifest.Key)
	}
	if strings.Contains(manifest.Author, "Stencil") {
		t.Fatalf("manifest.author must not attribute publication to Stencil Labs: %q", manifest.Author)
	}
	if !strings.Contains(manifest.Author, ExtensionName) {
		t.Fatalf("manifest.author = %q, expected project name %q", manifest.Author, ExtensionName)
	}
	if !strings.Contains(strings.ToLower(manifest.Description), "unofficial") || !strings.Contains(manifest.Description, "omp") {
		t.Fatalf("manifest.description must state unofficial and based on omp: %q", manifest.Description)
	}
	if strings.Contains(manifest.HomepageURL, "omp.sh") {
		t.Fatalf("manifest.homepage_url must not point at omp.sh: %q", manifest.HomepageURL)
	}
	if manifest.SidePanel != nil {
		t.Fatalf("manifest must not contain side_panel: %#v", manifest.SidePanel)
	}
	if manifest.Commands != nil {
		t.Fatalf("manifest must not contain commands: %#v", manifest.Commands)
	}
	if manifest.Background["service_worker"] != "background.js" {
		t.Fatalf("background.service_worker = %q, want background.js", manifest.Background["service_worker"])
	}
	if manifest.Action.DefaultTitle != ExtensionName {
		t.Fatalf("action.default_title = %q, want %q", manifest.Action.DefaultTitle, ExtensionName)
	}
	// Verify oh-my-pig's own generated PNG icons are referenced and present (and omp's icons are absent).
	pngSig := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	for _, sz := range []string{"16", "32", "48", "128"} {
		wantIcon := "icons/oh-my-pig-" + sz + ".png"
		if manifest.Icons[sz] != wantIcon {
			t.Fatalf("icons[%s] = %q, want %q", sz, manifest.Icons[sz], wantIcon)
		}
		if manifest.Action.DefaultIcon[sz] != wantIcon {
			t.Fatalf("action.default_icon[%s] = %q, want %q", sz, manifest.Action.DefaultIcon[sz], wantIcon)
		}
		rawIcon, ok := bundle.Files[wantIcon]
		if !ok || len(rawIcon) < len(pngSig) || !slices.Equal(rawIcon[:8], pngSig) {
			t.Fatalf("embedded icon %q missing or not a valid PNG", wantIcon)
		}
	}
	if _, ok := bundle.Files["LICENSE"]; !ok {
		t.Fatal("embedded extension must keep upstream LICENSE file")
	}

	wantPerms := []string{
		"alarms",
		"debugger",
		"downloads",
		"nativeMessaging",
		"scripting",
		"storage",
		"tabGroups",
		"tabs",
	}
	gotPerms := slices.Clone(manifest.Permissions)
	slices.Sort(gotPerms)
	if !slices.Equal(gotPerms, wantPerms) {
		t.Fatalf("manifest permissions = %v, want %v", gotPerms, wantPerms)
	}
	if !slices.Equal(manifest.HostPermissions, []string{"<all_urls>"}) {
		t.Fatalf("host_permissions = %v, want [<all_urls>]", manifest.HostPermissions)
	}

	// Ensure side panel / teach / offscreen artifacts are absent from embedded files.
	for name := range bundle.Files {
		if strings.HasPrefix(name, "icons/omp-") {
			t.Fatalf("embedded extension must not ship omp icon %s", name)
		}
		for _, forbidden := range []string{"panel.html", "panel.js", "panel.css", "offscreen.html", "offscreen.js", "recorder.js"} {
			if name == forbidden {
				t.Fatalf("embedded extension unexpectedly ships %s", name)
			}
		}
	}

	// Verify background.js connects to NativeHostName and does not reference sh.omp.browser or sidePanel.
	bg := string(bundle.Files["background.js"])
	if !strings.Contains(bg, NativeHostName) {
		t.Fatalf("background.js does not contain NativeHostName %q", NativeHostName)
	}
	if strings.Contains(bg, "sh.omp.browser") {
		t.Fatal("background.js must not reference omp's native host name sh.omp.browser")
	}
	if strings.Contains(bg, "chrome.sidePanel") {
		t.Fatal("background.js must not call chrome.sidePanel")
	}
	if !strings.Contains(bg, "setBadgeText") || !strings.Contains(bg, "setTitle") {
		t.Fatal("background.js must still update the toolbar badge/title on relay connect/disconnect")
	}

	// Verify checked-in extension.manifest.json matches every embedded file and build ID.
	manifestBytes, err := os.ReadFile("extension.manifest.json")
	if err != nil {
		t.Fatalf("read extension.manifest.json: %v", err)
	}
	var genManifest struct {
		Generator  string `json:"generator"`
		OmpTag     string `json:"ompTag"`
		OmpCommit  string `json:"ompCommit"`
		BunVersion string `json:"bunVersion"`
		Build      string `json:"build"`
		TotalBytes int    `json:"totalBytes"`
		Outputs    []struct {
			File   string `json:"file"`
			Bytes  int    `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"outputs"`
		Inputs []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(manifestBytes, &genManifest); err != nil {
		t.Fatalf("parse extension.manifest.json: %v", err)
	}
	if genManifest.OmpTag != "v19.1.1" {
		t.Fatalf("ompTag = %q, want v19.1.1", genManifest.OmpTag)
	}
	if genManifest.Build != bundle.Build {
		t.Fatalf("manifest build = %q, embedded build = %q", genManifest.Build, bundle.Build)
	}
	if len(genManifest.Outputs) != len(bundle.Files) {
		t.Fatalf("manifest outputs count = %d, embedded files = %d", len(genManifest.Outputs), len(bundle.Files))
	}
	var totalBytes int
	for _, out := range genManifest.Outputs {
		data, ok := bundle.Files[out.File]
		if !ok {
			t.Fatalf("manifest output %q missing from embedded bundle", out.File)
		}
		if len(data) != out.Bytes {
			t.Fatalf("%s bytes = %d, manifest expects %d", out.File, len(data), out.Bytes)
		}
		sum := sha256.Sum256(data)
		if gotHex := hex.EncodeToString(sum[:]); gotHex != out.SHA256 {
			t.Fatalf("%s sha256 = %s, manifest expects %s", out.File, gotHex, out.SHA256)
		}
		totalBytes += len(data)
	}
	if totalBytes != genManifest.TotalBytes {
		t.Fatalf("totalBytes = %d, manifest expects %d", totalBytes, genManifest.TotalBytes)
	}
	if len(genManifest.Inputs) < 10 {
		t.Fatalf("expected manifest inputs list, got %d", len(genManifest.Inputs))
	}
}

// TestEmbeddedExtensionRegeneratesDeterministically proves the checked-in extension bundle
// regenerates byte-for-byte identically from $OMP_SRC via gen/build-extension.ts.
// Runs automatically when bun is on PATH and OMP_SRC is set (or when OMP_REGEN_RELAY_EXTENSION=1).
func TestEmbeddedExtensionRegeneratesDeterministically(t *testing.T) {
	bunPath, err := exec.LookPath("bun")
	if err != nil {
		if os.Getenv("OMP_REGEN_RELAY_EXTENSION") == "1" {
			t.Fatalf("OMP_REGEN_RELAY_EXTENSION=1 set but bun not found on PATH: %v", err)
		}
		t.Skip("bun not found on PATH; skipping extension regeneration check")
	}
	ompSrc := os.Getenv("OMP_SRC")
	if ompSrc == "" {
		if os.Getenv("OMP_REGEN_RELAY_EXTENSION") == "1" {
			t.Fatal("OMP_REGEN_RELAY_EXTENSION=1 set but OMP_SRC is unset")
		}
		t.Skip("OMP_SRC is unset; set OMP_SRC to omp v19.1.1 source to verify deterministic regeneration")
	}
	if _, err := os.Stat(filepath.Join(ompSrc, "packages/browser-relay/extension/manifest.json")); err != nil {
		t.Skipf("omp v19.1.1 browser-relay source not present at %s: %v", ompSrc, err)
	}

	outDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bunPath, "run", filepath.Join("gen", "build-extension.ts"),
		"--omp-src", ompSrc,
		"--out-dir", outDir,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-extension.ts failed: %v\n%s", err, string(out))
	}

	gotManifest, err := os.ReadFile(filepath.Join(outDir, "extension.manifest.json"))
	if err != nil {
		t.Fatalf("read regenerated extension.manifest.json: %v", err)
	}
	wantManifest, err := os.ReadFile("extension.manifest.json")
	if err != nil {
		t.Fatalf("read checked-in extension.manifest.json: %v", err)
	}
	if string(gotManifest) != string(wantManifest) {
		t.Fatalf("regenerated extension.manifest.json differs from checked-in copy:\ngot:\n%s\nwant:\n%s", gotManifest, wantManifest)
	}

	bundle, err := LoadExtensionBundle()
	if err != nil {
		t.Fatal(err)
	}
	for name, wantBytes := range bundle.Files {
		gotBytes, err := os.ReadFile(filepath.Join(outDir, "extension", filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read regenerated %s: %v", name, err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatalf("regenerated %s differs from checked-in embedded file", name)
		}
	}
}

// TestWriteRelayExtensionAtomicAndIdempotent verifies atomic writes, version/build stamping,
// idempotent re-writes, and no stray temp files.
// upstream: packages/coding-agent/test/cli/browser-relay-cli.test.ts:154-162
func TestWriteRelayExtensionAtomicAndIdempotent(t *testing.T) {
	home := isolateRelayEnv(t)
	dir := filepath.Join(home, "ext")

	changed, err := WriteRelayExtension(dir, "0.1.0", nil)
	if err != nil {
		t.Fatalf("WriteRelayExtension: %v", err)
	}
	if !changed {
		t.Fatal("first WriteRelayExtension should report changed=true")
	}

	// Second write with same version is a no-op.
	changed, err = WriteRelayExtension(dir, "0.1.0", nil)
	if err != nil {
		t.Fatalf("second WriteRelayExtension: %v", err)
	}
	if changed {
		t.Fatal("identical WriteRelayExtension should report changed=false")
	}

	// Check manifest and background.js stamps.
	if got := InstalledExtensionVersion(dir); got != "0.1.0" {
		t.Fatalf("InstalledExtensionVersion = %q, want 0.1.0", got)
	}
	stamp := InstalledExtensionStamp(dir)
	build, err := EmbeddedExtensionBuild()
	if err != nil {
		t.Fatal(err)
	}
	if stamp == nil || stamp.Version != "0.1.0" || !stamp.HasBuild || stamp.Build != build {
		t.Fatalf("InstalledExtensionStamp = %#v, want version=0.1.0 build=%s", stamp, build)
	}

	// Ensure no leftover .tmp files remain.
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasSuffix(d.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Updating version rewrites manifest.json and background.js.
	changed, err = WriteRelayExtension(dir, "0.2.0", nil)
	if err != nil {
		t.Fatalf("update WriteRelayExtension: %v", err)
	}
	if !changed {
		t.Fatal("version change should report changed=true")
	}
	if got := InstalledExtensionVersion(dir); got != "0.2.0" {
		t.Fatalf("InstalledExtensionVersion after update = %q, want 0.2.0", got)
	}
}

// TestInstallRegistryAdoptionAndCanonicalAliases ports upstream browser-relay-installs.test.ts:59-88.
// upstream: packages/coding-agent/test/tools/browser-relay-installs.test.ts:59-88
func TestInstallRegistryAdoptionAndCanonicalAliases(t *testing.T) {
	home := isolateRelayEnv(t)
	registryFile := filepath.Join(home, "state", "installs.json")

	reg, err := ReadRelayInstallRegistry(registryFile)
	if err != nil {
		t.Fatalf("ReadRelayInstallRegistry missing file: %v", err)
	}
	if len(reg.Installs) != 0 {
		t.Fatalf("expected empty installs, got %#v", reg.Installs)
	}

	defaultDir := filepath.Join(home, "default extension")
	if _, err := WriteRelayExtension(defaultDir, "1.2.3", nil); err != nil {
		t.Fatal(err)
	}
	canonicalDefault, err := canonicalDir(defaultDir)
	if err != nil {
		t.Fatal(err)
	}

	customDir := filepath.Join(home, "custom extension")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(customDir, alias); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	opts := InstallLocations{
		RegistryFile: registryFile,
		DefaultDir:   defaultDir,
		RelayURLSet:  true,
		Version:      "0.1.0",
	}
	canonical, err := InstallRelayExtension(alias, opts)
	if err != nil {
		t.Fatalf("InstallRelayExtension(alias): %v", err)
	}
	if _, err := InstallRelayExtension(customDir, opts); err != nil {
		t.Fatalf("InstallRelayExtension(customDir): %v", err)
	}

	wantCanonical, err := canonicalDir(customDir)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != wantCanonical {
		t.Fatalf("canonical = %q, want %q", canonical, wantCanonical)
	}

	reg, err = ReadRelayInstallRegistry(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Installs) != 2 {
		t.Fatalf("expected 2 installs, got %#v", reg.Installs)
	}
	if reg.Installs[0].Dir != wantCanonical || reg.Installs[0].OmpVersion == nil || *reg.Installs[0].OmpVersion != "0.1.0" {
		t.Fatalf("first install = %#v", reg.Installs[0])
	}
	if reg.Installs[1].Dir != canonicalDefault || reg.Installs[1].OmpVersion != nil {
		t.Fatalf("second (adopted default) install = %#v", reg.Installs[1])
	}

	summaries, err := ReadRelayInstalls(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 || summaries[0].Missing || summaries[1].Missing {
		t.Fatalf("summaries = %#v", summaries)
	}
}

// TestSyncOverTwoDirsMissingAndRecheck ports upstream browser-relay-installs.test.ts:90-115
// and verifies sync over two present directories, missing directory reporting, startup skip,
// native host manifest refresh, and POST /omp/extensions/recheck notification (and tolerating no relay).
// upstream: packages/coding-agent/test/tools/browser-relay-installs.test.ts:90-115
func TestSyncOverTwoDirsMissingAndRecheck(t *testing.T) {
	home := isolateRelayEnv(t)
	registryFile := filepath.Join(home, "state", "installs.json")

	var recheckHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == relayRecheckPath {
			recheckHits.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	baseOpts := InstallLocations{
		RegistryFile: registryFile,
		RelayURL:     srv.URL,
		RelayURLSet:  true,
		Version:      "0.1.0",
		NativeHost: &NativeHostOptions{
			Platform: "darwin",
			Home:     home,
			Dir:      filepath.Dir(registryFile),
			Command:  []string{"/usr/local/bin/oh-my-pig"},
		},
	}

	dirA, err := InstallRelayExtension(filepath.Join(home, "dir-a"), baseOpts)
	if err != nil {
		t.Fatal(err)
	}
	dirB, err := InstallRelayExtension(filepath.Join(home, "dir-b"), baseOpts)
	if err != nil {
		t.Fatal(err)
	}
	dirDeleted, err := InstallRelayExtension(filepath.Join(home, "deleted"), baseOpts)
	if err != nil {
		t.Fatal(err)
	}

	// Materialize a native host registration for one browser only (so refresh updates it without creating others).
	if _, err := WriteRelayNativeHost([]string{ExtensionIDForPath(dirA, runtime.GOOS)}, *baseOpts.NativeHost); err != nil {
		t.Fatal(err)
	}

	// Make dirA and dirB stale, and delete dirDeleted.
	if _, err := WriteRelayExtension(dirA, "0.0.1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRelayExtension(dirB, "0.0.2", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dirDeleted); err != nil {
		t.Fatal(err)
	}
	recheckHits.Store(0)

	// Startup sync rewrites both stale dirs, reports deleted without recreating it, refreshes manifests, and POSTs recheck.
	results, err := SyncRelayInstalls(InstallSyncOptions{
		InstallLocations: baseOpts,
		Mode:             SyncModeStartup,
	})
	if err != nil {
		t.Fatalf("SyncRelayInstalls(startup): %v", err)
	}
	want := []InstallSyncResult{
		{Dir: dirA, Status: SyncStatusWritten, OmpVersion: "0.1.0"},
		{Dir: dirB, Status: SyncStatusWritten, OmpVersion: "0.1.0"},
		{Dir: dirDeleted, Status: SyncStatusMissing},
	}
	if !slices.Equal(results, want) {
		t.Fatalf("sync results = %#v, want %#v", results, want)
	}
	if got := InstalledExtensionVersion(dirA); got != "0.1.0" {
		t.Fatalf("dirA version = %q, want 0.1.0", got)
	}
	if got := InstalledExtensionVersion(dirB); got != "0.1.0" {
		t.Fatalf("dirB version = %q, want 0.1.0", got)
	}
	if _, err := os.Stat(dirDeleted); !os.IsNotExist(err) {
		t.Fatalf("deleted dir should not be recreated, stat err = %v", err)
	}
	if recheckHits.Load() != 1 {
		t.Fatalf("expected 1 POST %s hit, got %d", relayRecheckPath, recheckHits.Load())
	}

	// Manifest was widened by RefreshRelayNativeHost to admit dirB and dirDeleted too.
	manifestPath := filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts", NativeHostName+".json")
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read refreshed manifest: %v", err)
	}
	var parsedManifest nativeHostManifest
	if err := json.Unmarshal(rawManifest, &parsedManifest); err != nil {
		t.Fatal(err)
	}
	wantOriginB := "chrome-extension://" + ExtensionIDForPath(dirB, runtime.GOOS) + "/"
	if !slices.Contains(parsedManifest.AllowedOrigins, wantOriginB) {
		t.Fatalf("refreshed manifest allowed_origins = %v, missing %s", parsedManifest.AllowedOrigins, wantOriginB)
	}

	// Registry lastSynced matches version+build.
	build, err := EmbeddedExtensionBuild()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := ReadRelayInstallRegistry(registryFile)
	if err != nil {
		t.Fatal(err)
	}
	if reg.LastSynced != "0.1.0+"+build {
		t.Fatalf("lastSynced = %q, want %q", reg.LastSynced, "0.1.0+"+build)
	}

	// Second startup sync is skipped because lastSynced matches.
	skipped, err := SyncRelayInstalls(InstallSyncOptions{
		InstallLocations: baseOpts,
		Mode:             SyncModeStartup,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("expected second startup sync to skip, got %#v", skipped)
	}

	// Explicit sync reports up to date / missing and succeeds even when no relay is reachable.
	noRelayOpts := baseOpts
	noRelayOpts.RelayURL = "http://127.0.0.1:1"
	explicit, err := SyncRelayInstalls(InstallSyncOptions{
		InstallLocations: noRelayOpts,
		Mode:             SyncModeExplicit,
	})
	if err != nil {
		t.Fatalf("explicit sync with unreachable relay failed: %v", err)
	}
	wantExplicit := []InstallSyncResult{
		{Dir: dirA, Status: SyncStatusUpToDate, OmpVersion: "0.1.0"},
		{Dir: dirB, Status: SyncStatusUpToDate, OmpVersion: "0.1.0"},
		{Dir: dirDeleted, Status: SyncStatusMissing},
	}
	if !slices.Equal(explicit, wantExplicit) {
		t.Fatalf("explicit sync = %#v, want %#v", explicit, wantExplicit)
	}

	// Mode "relay" protects newer installs from downgrade.
	if _, err := WriteRelayExtension(dirA, "999.0.0", nil); err != nil {
		t.Fatal(err)
	}
	relayMode, err := SyncRelayInstalls(InstallSyncOptions{
		InstallLocations: baseOpts,
		Mode:             SyncModeRelay,
	})
	if err != nil {
		t.Fatal(err)
	}
	if relayMode[0].Status != SyncStatusUpToDate || relayMode[0].OmpVersion != "999.0.0" {
		t.Fatalf("relay mode should not downgrade newer dirA: %#v", relayMode[0])
	}
	if got := InstalledExtensionVersion(dirA); got != "999.0.0" {
		t.Fatalf("dirA was downgraded to %q", got)
	}
}

// TestWriteRelayNativeHostPerOS ports upstream browser-relay-installs.test.ts:227-272.
// upstream: packages/coding-agent/test/tools/browser-relay-installs.test.ts:227-272
func TestWriteRelayNativeHostPerOS(t *testing.T) {
	manifestFile := NativeHostName + ".json"

	cases := []struct {
		platform string
		roots    []string
		launcher string
	}{
		{
			platform: "darwin",
			roots: []string{
				"Library/Application Support/Google/Chrome/NativeMessagingHosts",
				"Library/Application Support/Google/Chrome Beta/NativeMessagingHosts",
				"Library/Application Support/Google/Chrome Dev/NativeMessagingHosts",
				"Library/Application Support/Google/Chrome Canary/NativeMessagingHosts",
				"Library/Application Support/Chromium/NativeMessagingHosts",
				"Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts",
			},
			launcher: "native-host",
		},
		{
			platform: "linux",
			roots: []string{
				".config/google-chrome/NativeMessagingHosts",
				".config/google-chrome-beta/NativeMessagingHosts",
				".config/google-chrome-unstable/NativeMessagingHosts",
				".config/google-chrome-canary/NativeMessagingHosts",
				".config/chromium/NativeMessagingHosts",
				".config/BraveSoftware/Brave-Browser/NativeMessagingHosts",
			},
			launcher: "native-host",
		},
		{
			platform: "windows",
			roots:    []string{".pig/agent/browser-relay"},
			launcher: "native-host.cmd",
		},
	}

	ids := []string{
		"pppppppppppppppppppppppppppppppp",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"pppppppppppppppppppppppppppppppp",
	}

	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			home := isolateRelayEnv(t)
			dir := filepath.Join(home, ".pig", "agent", "browser-relay")
			targets, err := WriteRelayNativeHost(ids, NativeHostOptions{
				Platform: tc.platform,
				Home:     home,
				Dir:      dir,
				Command:  []string{"/opt/oh-my-pig"},
			})
			if err != nil {
				t.Fatalf("WriteRelayNativeHost(%s): %v", tc.platform, err)
			}
			wantTargets := make([]string, len(tc.roots))
			for i, r := range tc.roots {
				wantTargets[i] = filepath.Join(home, filepath.FromSlash(r), manifestFile)
			}
			if !slices.Equal(targets, wantTargets) {
				t.Fatalf("targets = %v\nwant    = %v", targets, wantTargets)
			}

			wantLauncher := filepath.Join(dir, tc.launcher)
			launcherBytes, err := os.ReadFile(wantLauncher)
			if err != nil {
				t.Fatalf("read launcher %s: %v", wantLauncher, err)
			}
			if tc.platform == "windows" {
				if !strings.Contains(string(launcherBytes), "\"/opt/oh-my-pig\" \"browser-relay\" \"native-host\"") {
					t.Fatalf("unexpected Windows launcher: %q", string(launcherBytes))
				}
			} else {
				wantScript := "#!/bin/sh\nexec '/opt/oh-my-pig' 'browser-relay' 'native-host'\n"
				if string(launcherBytes) != wantScript {
					t.Fatalf("launcher = %q, want %q", string(launcherBytes), wantScript)
				}
			}

			for _, target := range targets {
				data, err := os.ReadFile(target)
				if err != nil {
					t.Fatalf("read manifest %s: %v", target, err)
				}
				var m nativeHostManifest
				if err := json.Unmarshal(data, &m); err != nil {
					t.Fatalf("parse manifest %s: %v", target, err)
				}
				if m.Name != NativeHostName {
					t.Errorf("%s name = %q, want %q", target, m.Name, NativeHostName)
				}
				if m.Description != ExtensionName+" browser native messaging host" {
					t.Errorf("%s description = %q", target, m.Description)
				}
				if m.Path != wantLauncher {
					t.Errorf("%s path = %q, want %q", target, m.Path, wantLauncher)
				}
				if m.Type != "stdio" {
					t.Errorf("%s type = %q, want stdio", target, m.Type)
				}
				wantOrigins := []string{
					"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/",
					"chrome-extension://pppppppppppppppppppppppppppppppp/",
				}
				if !slices.Equal(m.AllowedOrigins, wantOrigins) {
					t.Errorf("%s allowed_origins = %v, want %v", target, m.AllowedOrigins, wantOrigins)
				}
			}
		})
	}
}

// TestRegisterRelayNativeHostWindowsRegistry verifies Windows HKCU registry registration via regAddFunc.
func TestRegisterRelayNativeHostWindowsRegistry(t *testing.T) {
	home := isolateRelayEnv(t)
	dir := filepath.Join(home, "relay")

	origRegAdd := regAddFunc
	t.Cleanup(func() { regAddFunc = origRegAdd })

	var gotKey, gotManifest string
	regAddFunc = func(key, manifest string) error {
		gotKey = key
		gotManifest = manifest
		return nil
	}

	err := RegisterRelayNativeHostWithOptions([]string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, NativeHostOptions{
		Platform: "windows",
		Home:     home,
		Dir:      dir,
		Command:  []string{`C:\Users\100%real\oh-my-pig.exe`},
	})
	if err != nil {
		t.Fatalf("RegisterRelayNativeHostWithOptions: %v", err)
	}
	wantKey := `HKCU\Software\Google\Chrome\NativeMessagingHosts\` + NativeHostName
	if gotKey != wantKey {
		t.Fatalf("registry key = %q, want %q", gotKey, wantKey)
	}
	wantManifest := filepath.Join(dir, NativeHostName+".json")
	if gotManifest != wantManifest {
		t.Fatalf("registry manifest = %q, want %q", gotManifest, wantManifest)
	}
	// Verify % escaping in Windows .cmd launcher.
	cmdBytes, err := os.ReadFile(filepath.Join(dir, "native-host.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cmdBytes), `100%%real`) {
		t.Fatalf("expected %% to be escaped as %%%% in native-host.cmd, got: %q", string(cmdBytes))
	}
}

// TestNativeHostLauncherQuotesAndDiscardsChromeArgs ports upstream browser-relay-installs.test.ts:274-289.
// upstream: packages/coding-agent/test/tools/browser-relay-installs.test.ts:274-289
func TestNativeHostLauncherQuotesAndDiscardsChromeArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher test")
	}
	home := isolateRelayEnv(t)
	dir := filepath.Join(home, "relay")
	fakeBin := filepath.Join(home, "oh-my-pig's bin")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRelayNativeHost(nil, NativeHostOptions{
		Platform: "linux",
		Home:     home,
		Dir:      dir,
		Command:  []string{fakeBin, "flag's value"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(dir, "native-host"), "chrome-extension://unused/").CombinedOutput()
	if err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, string(out))
	}
	want := "flag's value\nbrowser-relay\nnative-host\n"
	if string(out) != want {
		t.Fatalf("launcher argv = %q, want %q", string(out), want)
	}
}

// TestRefreshRelayNativeHostDoesNotCreateMissingBrowserManifests verifies RefreshRelayNativeHost
// updates only existing browser manifests and leaves absent browsers untouched.
func TestRefreshRelayNativeHostDoesNotCreateMissingBrowserManifests(t *testing.T) {
	home := isolateRelayEnv(t)
	dir := filepath.Join(home, "relay")
	opts := NativeHostOptions{
		Platform: "linux",
		Home:     home,
		Dir:      dir,
		Command:  []string{"/bin/oh-my-pig"},
	}

	// Nothing exists yet: refresh is a no-op.
	if err := RefreshRelayNativeHost([]string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, opts); err != nil {
		t.Fatalf("RefreshRelayNativeHost with no launcher: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "native-host")); !os.IsNotExist(err) {
		t.Fatal("RefreshRelayNativeHost must not create launcher when absent")
	}

	// Create launcher + only Chrome's manifest; Chromium's does not exist.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeNativeHostLauncher(filepath.Join(dir, "native-host"), "linux", []string{"/old/bin"}); err != nil {
		t.Fatal(err)
	}
	chromeManifest := filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts", NativeHostName+".json")
	chromiumManifest := filepath.Join(home, ".config", "chromium", "NativeMessagingHosts", NativeHostName+".json")
	if err := writeFileAtomic(chromeManifest, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RefreshRelayNativeHost([]string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, opts); err != nil {
		t.Fatalf("RefreshRelayNativeHost: %v", err)
	}
	if _, err := os.Stat(chromiumManifest); !os.IsNotExist(err) {
		t.Fatal("RefreshRelayNativeHost created manifest for browser that had none")
	}
	data, err := os.ReadFile(chromeManifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/") {
		t.Fatalf("expected chrome manifest to be updated, got: %s", string(data))
	}
}

// TestFindUnpackedInstalls ports upstream browser-relay-chromium-profiles.test.ts.
// upstream: packages/coding-agent/test/tools/browser-relay-chromium-profiles.test.ts:1-85
func TestFindUnpackedInstalls(t *testing.T) {
	home := isolateRelayEnv(t)
	const id = "abcdefghijklmnopabcdefghijklmnop"

	writeJSON := func(rel string, v any) {
		t.Helper()
		full := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeJSON("Library/Application Support/Google/Chrome/Local State", map[string]any{
		"profile": map[string]any{
			"info_cache": map[string]any{
				"Default":   map[string]any{"name": "Work"},
				"Profile 1": map[string]any{"name": "Home"},
				"Profile 2": map[string]any{"name": "Spare"},
			},
		},
	})
	writeJSON("Library/Application Support/Google/Chrome/Default/Preferences", map[string]any{
		"extensions": map[string]any{"ui": map[string]any{"developer_mode": true}},
	})
	writeJSON("Library/Application Support/Google/Chrome/Default/Secure Preferences", map[string]any{
		"extensions": map[string]any{
			"settings": map[string]any{
				id: map[string]any{"location": 4, "disable_reasons": []any{}},
			},
		},
	})
	writeJSON("Library/Application Support/Google/Chrome/Profile 1/Preferences", map[string]any{
		"extensions": map[string]any{
			"settings": map[string]any{
				id: map[string]any{"location": 4, "disable_reasons": []any{1}},
			},
		},
	})
	// Web Store install (location 1) is not unpacked; malformed Preferences is ignored.
	writeJSON("Library/Application Support/Google/Chrome/Profile 2/Secure Preferences", map[string]any{
		"extensions": map[string]any{
			"settings": map[string]any{
				id: map[string]any{"location": 1},
			},
		},
	})
	badPrefs := filepath.Join(home, "Library/Application Support/Google/Chrome/Profile 2/Preferences")
	if err := os.WriteFile(badPrefs, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Browser without Local State is skipped.
	writeJSON("Library/Application Support/Chromium/Default/Secure Preferences", map[string]any{
		"extensions": map[string]any{
			"settings": map[string]any{
				id: map[string]any{"location": 4},
			},
		},
	})

	found := FindUnpackedInstalls(id, ChromiumLocations{
		Platform: "darwin",
		Home:     home,
	})
	want := []ChromiumProfileInstall{
		{Browser: "Google Chrome", Profile: "Work", Loaded: true, Enabled: true, DeveloperMode: true},
		{Browser: "Google Chrome", Profile: "Home", Loaded: true, Enabled: false, DeveloperMode: false},
		{Browser: "Google Chrome", Profile: "Spare", Loaded: false, Enabled: false, DeveloperMode: false},
	}
	if !slices.Equal(found, want) {
		t.Fatalf("FindUnpackedInstalls = %#v\nwant = %#v", found, want)
	}
}

// TestFindUnpackedInstallsSkipsUnreadableProfile verifies unreadable profile files
// (e.g., chmod 0000) are skipped without error and no files are modified.
func TestFindUnpackedInstallsSkipsUnreadableProfile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("POSIX non-root chmod test")
	}
	home := isolateRelayEnv(t)
	const id = "abcdefghijklmnopabcdefghijklmnop"

	base := filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	if err := os.MkdirAll(filepath.Join(base, "Default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "Profile 1"), 0o755); err != nil {
		t.Fatal(err)
	}
	localState := `{"profile":{"info_cache":{"Default":{"name":"Readable"},"Profile 1":{"name":"Locked"}}}}`
	if err := os.WriteFile(filepath.Join(base, "Local State"), []byte(localState), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultPrefs := `{"extensions":{"settings":{"` + id + `":{"location":4}}}}`
	if err := os.WriteFile(filepath.Join(base, "Default", "Preferences"), []byte(defaultPrefs), 0o644); err != nil {
		t.Fatal(err)
	}
	lockedPrefs := filepath.Join(base, "Profile 1", "Preferences")
	if err := os.WriteFile(lockedPrefs, []byte(defaultPrefs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockedPrefs, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockedPrefs, 0o644) })

	found := FindUnpackedInstalls(id, ChromiumLocations{Platform: "darwin", Home: home})
	want := []ChromiumProfileInstall{
		{Browser: "Google Chrome", Profile: "Readable", Loaded: true, Enabled: true, DeveloperMode: false},
		{Browser: "Google Chrome", Profile: "Locked", Loaded: false, Enabled: false, DeveloperMode: false},
	}
	if !slices.Equal(found, want) {
		t.Fatalf("FindUnpackedInstalls with unreadable profile = %#v\nwant = %#v", found, want)
	}
}
