// Managed extension install registry, installation, and synchronization across registered directories.
//
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts (omp v19.1.1)
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// relayRecheckPath is the HTTP POST path on a running relay asking connected extensions to reload.
// Wire protocol path intentionally matches omp's RELAY_RECHECK_PATH.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:212
const relayRecheckPath = "/omp/extensions/recheck"

// InstallRecord is one managed extension directory and the last asset version written there.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:33-37
type InstallRecord struct {
	Dir        string  `json:"dir"`
	OmpVersion *string `json:"ompVersion"`
	WrittenAt  int64   `json:"writtenAt"`
}

// InstallRegistry holds every managed install folder and the stamp of the last complete sync.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:40-44
type InstallRegistry struct {
	Installs   []InstallRecord `json:"installs"`
	LastSynced string          `json:"lastSynced,omitempty"`
}

// InstallSyncStatus describes the outcome for one directory during SyncRelayInstalls.
type InstallSyncStatus string

const (
	SyncStatusWritten  InstallSyncStatus = "written"
	SyncStatusUpToDate InstallSyncStatus = "up to date"
	SyncStatusMissing  InstallSyncStatus = "missing"
	SyncStatusFailed   InstallSyncStatus = "failed"
)

// InstallSyncResult is the outcome for one registered directory during a sync.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:47-51
type InstallSyncResult struct {
	Dir        string
	Status     InstallSyncStatus
	OmpVersion string
	Error      string
}

// InstallSummary reports a known install folder and whether its manifest is still on disk.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:86-92 (PanelInstall)
type InstallSummary struct {
	Dir     string
	Version *string
	Missing bool
}

// InstallLocations overrides paths and the running relay URL for an isolated install registry.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:54-62
type InstallLocations struct {
	RegistryFile string
	DefaultDir   string
	// RelayURL overrides which relay receives POST /omp/extensions/recheck.
	// If RelayURLSet is true and RelayURL is "", no relay is contacted.
	// If RelayURLSet is false, the default discovered relay is used unless RegistryFile is overridden.
	RelayURL    string
	RelayURLSet bool
	// Version overrides surface.Version for tests.
	Version string
	// NativeHost overrides native host refresh options (used by tests to stay inside a temp HOME).
	NativeHost *NativeHostOptions
}

// InstallSyncMode controls SyncRelayInstalls behavior.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:226-228
type InstallSyncMode string

const (
	SyncModeExplicit InstallSyncMode = "explicit"
	SyncModeStartup  InstallSyncMode = "startup"
	SyncModeRelay    InstallSyncMode = "relay"
)

// InstallSyncOptions configures SyncRelayInstalls.
type InstallSyncOptions struct {
	InstallLocations
	Mode InstallSyncMode
}

type resolvedInstallLocations struct {
	registryFile string
	defaultDir   string
	relayURL     string
	version      string
	nativeHost   *NativeHostOptions
}

func resolveInstallLocations(opts InstallLocations) resolvedInstallLocations {
	registryFile := opts.RegistryFile
	if registryFile == "" {
		registryFile = InstallsFile()
	}
	defaultDir := opts.DefaultDir
	if defaultDir == "" {
		if opts.RegistryFile != "" {
			defaultDir = filepath.Join(filepath.Dir(opts.RegistryFile), "extension")
		} else {
			defaultDir = DefaultExtensionDir()
		}
	}
	relayURL := ""
	if opts.RelayURLSet {
		relayURL = opts.RelayURL
	} else if opts.RegistryFile == "" {
		if eps := DiscoverRelayEndpoints(); len(eps) > 0 {
			relayURL = eps[0]
		}
	}
	version := opts.Version
	if version == "" {
		version = surface.Version
	}
	return resolvedInstallLocations{
		registryFile: registryFile,
		defaultDir:   defaultDir,
		relayURL:     relayURL,
		version:      version,
		nativeHost:   opts.NativeHost,
	}
}

func installKey(dir string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(dir)
	}
	return dir
}

// discoverEndpointsForRecheck reads <pid>-<port> endpoint files in EndpointsDir, newest first.
// Kept private to avoid colliding with discovery.go's DiscoverRelayEndpoints on merge.
// upstream: packages/coding-agent/src/tools/browser/relay/discovery.ts:18-42
func discoverEndpointsForRecheck(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type ep struct {
		port    int
		modTime time.Time
	}
	var found []ep
	for _, entry := range entries {
		name := entry.Name()
		idx := strings.IndexByte(name, '-')
		if idx <= 0 || idx == len(name)-1 || strings.IndexByte(name[idx+1:], '-') >= 0 {
			continue
		}
		pid, err1 := strconv.Atoi(name[:idx])
		port, err2 := strconv.Atoi(name[idx+1:])
		if err1 != nil || err2 != nil || pid < 1 || port < 1 || port > 65535 {
			continue
		}
		if !pidAlive(pid) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		found = append(found, ep{port: port, modTime: info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].modTime.After(found[j].modTime)
	})
	urls := make([]string, len(found))
	for i, e := range found {
		urls[i] = fmt.Sprintf("http://127.0.0.1:%d", e.port)
	}
	return urls
}

// recheckRelay notifies a running relay that install folders changed so connected extensions
// reload once idle. Tolerates no relay running or any network/status error.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:90-96
func recheckRelay(relayURL string) {
	if relayURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	target := strings.TrimRight(relayURL, "/") + RelayRecheckPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return
	}
	if tok, err := RelayTokenForURL(relayURL); err == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

var registryMu sync.Mutex

// withRegistryLock serializes access to registryFile in-process and across processes via an
// adjacent .lock file (with stale-lock recovery).
func withRegistryLock(registryFile string, fn func() error) error {
	registryMu.Lock()
	defer registryMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(registryFile), 0o755); err != nil {
		return err
	}
	lockPath := registryFile + ".lock"
	deadline := time.Now().Add(5 * time.Second)
	var lockFile *os.File
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			lockFile = f
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		// Break a stale lock older than 30s.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for lock on %s", registryFile)
		}
		time.Sleep(15 * time.Millisecond)
	}
	if lockFile != nil {
		defer func() { _ = os.Remove(lockPath) }()
	}
	return fn()
}

// ReadRelayInstallRegistry reads the shared registry without creating it, consolidating duplicate
// canonical paths by keeping the newest writtenAt entry.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:99-137
func ReadRelayInstallRegistry(file string) (InstallRegistry, error) {
	if file == "" {
		file = InstallsFile()
	}
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return InstallRegistry{Installs: []InstallRecord{}}, nil
		}
		return InstallRegistry{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return InstallRegistry{}, err
	}
	rowsRaw, ok := raw["installs"].([]any)
	if !ok {
		return InstallRegistry{}, fmt.Errorf("Invalid browser relay install registry: %s", file)
	}
	byKey := make(map[string]InstallRecord)
	order := make([]string, 0, len(rowsRaw))
	for _, item := range rowsRaw {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		dir, ok := row["dir"].(string)
		if !ok || !filepath.IsAbs(dir) {
			continue
		}
		var verPtr *string
		if rawVer, exists := row["ompVersion"]; !exists {
			continue
		} else if rawVer != nil {
			s, ok := rawVer.(string)
			if !ok {
				continue
			}
			verPtr = &s
		}
		writtenAtFloat, ok := row["writtenAt"].(float64)
		if !ok {
			continue
		}
		normDir := filepath.Clean(dir)
		key := installKey(normDir)
		rec := InstallRecord{
			Dir:        normDir,
			OmpVersion: verPtr,
			WrittenAt:  int64(writtenAtFloat),
		}
		existing, exists := byKey[key]
		if !exists {
			order = append(order, key)
			byKey[key] = rec
		} else if rec.WrittenAt >= existing.WrittenAt {
			byKey[key] = rec
		}
	}
	installs := make([]InstallRecord, 0, len(order))
	for _, key := range order {
		installs = append(installs, byKey[key])
	}
	lastSynced, _ := raw["lastSynced"].(string)
	return InstallRegistry{
		Installs:   installs,
		LastSynced: lastSynced,
	}, nil
}

// readManifestVersion returns the version_name stamped in manifest.json:
// found=false when the file is absent; "" when unstamped or invalid JSON.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:140-149
func readManifestVersion(file string) (version string, found bool, err error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", true, nil
	}
	if v, ok := parsed["version_name"].(string); ok {
		return v, true, nil
	}
	return "", true, nil
}

func canonicalDir(dir string) (string, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func adoptDefaultInstall(registry *InstallRegistry, defaultDir string) (bool, error) {
	dir, err := canonicalDir(defaultDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	targetKey := installKey(dir)
	for _, row := range registry.Installs {
		if installKey(row.Dir) == targetKey {
			return false, nil
		}
	}
	info, err := os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	registry.Installs = append(registry.Installs, InstallRecord{
		Dir:        dir,
		OmpVersion: nil,
		WrittenAt:  info.ModTime().UnixMilli(),
	})
	return true, nil
}

func writeInstallRegistry(file string, registry InstallRegistry) error {
	if registry.Installs == nil {
		registry.Installs = []InstallRecord{}
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(file, append(data, '\n'), 0o644)
}

// ReadRelayInstalls reports known install folders, their recorded versions, and whether they are missing.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:170-181
func ReadRelayInstalls(opts InstallLocations) ([]InstallSummary, error) {
	loc := resolveInstallLocations(opts)
	registry, err := ReadRelayInstallRegistry(loc.registryFile)
	if err != nil {
		return nil, err
	}
	if _, err := adoptDefaultInstall(&registry, loc.defaultDir); err != nil {
		return nil, err
	}
	out := make([]InstallSummary, 0, len(registry.Installs))
	for _, row := range registry.Installs {
		_, statErr := os.Stat(filepath.Join(row.Dir, "manifest.json"))
		out = append(out, InstallSummary{
			Dir:     row.Dir,
			Version: row.OmpVersion,
			Missing: statErr != nil,
		})
	}
	return out, nil
}

// RegisteredRelayExtensionIDs returns the Chrome extension ID of every registered install folder
// (including the default folder once it exists).
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:188-193
func RegisteredRelayExtensionIDs(opts InstallLocations) ([]string, error) {
	loc := resolveInstallLocations(opts)
	registry, err := ReadRelayInstallRegistry(loc.registryFile)
	if err != nil {
		return nil, err
	}
	if _, err := adoptDefaultInstall(&registry, loc.defaultDir); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(registry.Installs))
	for _, row := range registry.Installs {
		ids = append(ids, ExtensionIDForPath(row.Dir, runtime.GOOS))
	}
	return ids, nil
}

// InstallRelayExtension writes the extension to dir, records its canonical path in installs.json,
// preserves any existing default install, and notifies a running relay if files changed.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:196-223
func InstallRelayExtension(dir string, opts InstallLocations) (string, error) {
	loc := resolveInstallLocations(opts)
	if dir == "" {
		dir = loc.defaultDir
	}
	var (
		canonical string
		written   bool
	)
	err := withRegistryLock(loc.registryFile, func() error {
		registry, err := ReadRelayInstallRegistry(loc.registryFile)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		canonical, err = canonicalDir(dir)
		if err != nil {
			return err
		}
		written, err = WriteRelayExtension(canonical, loc.version, nil)
		if err != nil {
			return err
		}
		key := installKey(canonical)
		foundIdx := -1
		for i, row := range registry.Installs {
			if installKey(row.Dir) == key {
				foundIdx = i
				break
			}
		}
		ver := loc.version
		updated := foundIdx < 0 || written || registry.Installs[foundIdx].OmpVersion == nil || *registry.Installs[foundIdx].OmpVersion != ver
		if foundIdx < 0 {
			registry.Installs = append(registry.Installs, InstallRecord{
				Dir:        canonical,
				OmpVersion: &ver,
				WrittenAt:  time.Now().UnixMilli(),
			})
		} else if updated {
			registry.Installs[foundIdx].OmpVersion = &ver
			registry.Installs[foundIdx].WrittenAt = time.Now().UnixMilli()
		}
		adopted, err := adoptDefaultInstall(&registry, loc.defaultDir)
		if err != nil {
			return err
		}
		if updated || adopted {
			if err := writeInstallRegistry(loc.registryFile, registry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if written {
		recheckRelay(loc.relayURL)
	}
	return canonical, nil
}

// SyncRelayInstalls reconciles every registered install directory with the embedded extension
// without recreating missing directories, refreshes existing native host manifests, and asks
// a running relay to recheck when any files changed.
// upstream: packages/coding-agent/src/tools/browser/relay/installs.ts:231-304
func SyncRelayInstalls(opts InstallSyncOptions) ([]InstallSyncResult, error) {
	loc := resolveInstallLocations(opts.InstallLocations)
	initial, err := ReadRelayInstallRegistry(loc.registryFile)
	if err != nil {
		return nil, err
	}
	if len(initial.Installs) == 0 {
		if _, err := os.Stat(filepath.Join(loc.defaultDir, "manifest.json")); err != nil {
			return nil, nil
		}
	}
	bundle, err := LoadExtensionBundle()
	if err != nil {
		return nil, err
	}
	stamp := loc.version + "+" + bundle.Build
	if opts.Mode == SyncModeStartup && initial.LastSynced == stamp {
		return nil, nil
	}

	var results []InstallSyncResult
	err = withRegistryLock(loc.registryFile, func() error {
		registry, err := ReadRelayInstallRegistry(loc.registryFile)
		if err != nil {
			return err
		}
		if opts.Mode == SyncModeStartup && registry.LastSynced == stamp {
			return nil
		}
		changed, err := adoptDefaultInstall(&registry, loc.defaultDir)
		if err != nil {
			return err
		}
		results = make([]InstallSyncResult, 0, len(registry.Installs))
		for i := range registry.Installs {
			row := &registry.Installs[i]
			dir := row.Dir
			info, statErr := os.Lstat(dir)
			if statErr != nil {
				if errors.Is(statErr, fs.ErrNotExist) {
					results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusMissing})
				} else {
					results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusFailed, Error: statErr.Error()})
				}
				continue
			}
			if !info.IsDir() {
				results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusMissing})
				continue
			}
			installed, hasManifest, readErr := readManifestVersion(filepath.Join(dir, "manifest.json"))
			if readErr != nil {
				results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusFailed, Error: readErr.Error()})
				continue
			}
			if opts.Mode == SyncModeRelay && hasManifest && isOlderSemver(loc.version, installed) {
				results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusUpToDate, OmpVersion: installed})
				continue
			}
			written, writeErr := WriteRelayExtension(dir, loc.version, bundle)
			if writeErr != nil {
				if errors.Is(writeErr, fs.ErrNotExist) {
					results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusMissing})
				} else {
					results = append(results, InstallSyncResult{Dir: dir, Status: SyncStatusFailed, Error: writeErr.Error()})
				}
				continue
			}
			ver := loc.version
			if written || row.OmpVersion == nil || *row.OmpVersion != ver {
				row.OmpVersion = &ver
				row.WrittenAt = time.Now().UnixMilli()
				changed = true
			}
			status := SyncStatusUpToDate
			if written {
				status = SyncStatusWritten
			}
			results = append(results, InstallSyncResult{
				Dir:        dir,
				Status:     status,
				OmpVersion: ver,
			})
		}

		if opts.Mode != SyncModeRelay {
			ids := make([]string, len(registry.Installs))
			for i, row := range registry.Installs {
				ids[i] = ExtensionIDForPath(row.Dir, runtime.GOOS)
			}
			nhOpts := NativeHostOptions{Dir: filepath.Dir(loc.registryFile)}
			if loc.nativeHost != nil {
				nhOpts = *loc.nativeHost
				if nhOpts.Dir == "" {
					nhOpts.Dir = filepath.Dir(loc.registryFile)
				}
			}
			if refreshErr := RefreshRelayNativeHost(ids, nhOpts); refreshErr == nil {
				anyFailed := false
				for _, r := range results {
					if r.Status == SyncStatusFailed {
						anyFailed = true
						break
					}
				}
				if registry.LastSynced != stamp && !anyFailed {
					registry.LastSynced = stamp
					changed = true
				}
			}
		}

		if changed {
			if err := writeInstallRegistry(loc.registryFile, registry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if opts.Mode != SyncModeRelay {
		for _, r := range results {
			if r.Status == SyncStatusWritten {
				recheckRelay(loc.relayURL)
				break
			}
		}
	}
	return results, nil
}

var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)

// isOlderSemver reports whether version is provably older than than (matching upstream isOlderOmpVersion).
// Kept private so it does not collide with protocol.go's IsOlderOmpVersion.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:194-197
func isOlderSemver(version, than string) bool {
	if version == "" {
		return than != ""
	}
	if len(version) < 5 || len(version) > 64 || len(than) < 5 || len(than) > 64 {
		return false
	}
	a := semverRe.FindStringSubmatch(version)
	b := semverRe.FindStringSubmatch(than)
	if a == nil || b == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		ai, _ := strconv.Atoi(a[i])
		bi, _ := strconv.Atoi(b[i])
		if ai != bi {
			return ai < bi
		}
	}
	if a[4] == b[4] {
		return false
	}
	if a[4] == "" {
		return false
	}
	if b[4] == "" {
		return true
	}
	ap := strings.Split(a[4], ".")
	bp := strings.Split(b[4], ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		if ap[i] == bp[i] {
			continue
		}
		an, errA := strconv.Atoi(ap[i])
		bn, errB := strconv.Atoi(bp[i])
		switch {
		case errA == nil && errB == nil:
			return an < bn
		case errA == nil:
			return true
		case errB == nil:
			return false
		default:
			return ap[i] < bp[i]
		}
	}
	return len(ap) < len(bp)
}
