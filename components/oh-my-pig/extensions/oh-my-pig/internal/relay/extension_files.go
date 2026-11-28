// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts (omp v19.1.1)
// upstream: packages/coding-agent/src/tools/browser/relay/embedded-extension.ts (omp v19.1.1)
package relay

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
)

//go:embed extension extension/icons
var embeddedExtensionFS embed.FS

// ExtensionBundle holds the unstamped embedded extension files and their content digest.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:18-22
type ExtensionBundle struct {
	// Build is the first 16 hex chars of SHA-256 over each unstamped file's name and bytes
	// in sorted relative path order (upstream: packages/browser-relay/scripts/build-extension.ts:105-113).
	Build string
	// Files maps slash-separated relative paths inside the extension directory to raw bytes.
	Files map[string][]byte
}

// ExtensionStamp is the version and build stamp declared at the top of an installed background.js.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:218-223
type ExtensionStamp struct {
	Version  string
	Build    string
	HasBuild bool
}

var (
	embeddedBundleOnce sync.Once
	embeddedBundleVal  *ExtensionBundle
	embeddedBundleErr  error
)

// LoadExtensionBundle loads the unstamped relay-only extension embedded in the binary.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:37-58
func LoadExtensionBundle() (*ExtensionBundle, error) {
	embeddedBundleOnce.Do(func() {
		files := make(map[string][]byte)
		err := fs.WalkDir(embeddedExtensionFS, "extension", func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			rel := strings.TrimPrefix(p, "extension/")
			data, err := embeddedExtensionFS.ReadFile(p)
			if err != nil {
				return err
			}
			files[rel] = data
			return nil
		})
		if err != nil {
			embeddedBundleErr = fmt.Errorf("load embedded extension: %w", err)
			return
		}
		if _, ok := files["manifest.json"]; !ok {
			embeddedBundleErr = errors.New("embedded oh-my-pig extension lacks manifest.json")
			return
		}
		if _, ok := files["background.js"]; !ok {
			embeddedBundleErr = errors.New("embedded oh-my-pig extension lacks background.js")
			return
		}
		embeddedBundleVal = &ExtensionBundle{
			Build: computeExtensionBuildID(files),
			Files: files,
		}
	})
	if embeddedBundleErr != nil {
		return nil, embeddedBundleErr
	}
	// Return a shallow copy of the map so callers cannot mutate the cached bundle.
	out := make(map[string][]byte, len(embeddedBundleVal.Files))
	for k, v := range embeddedBundleVal.Files {
		out[k] = v
	}
	return &ExtensionBundle{Build: embeddedBundleVal.Build, Files: out}, nil
}

// EmbeddedExtensionBuild returns the build digest of the embedded unstamped extension.
// upstream: packages/coding-agent/src/tools/browser/relay/embedded-extension.ts
func EmbeddedExtensionBuild() (string, error) {
	bundle, err := LoadExtensionBundle()
	if err != nil {
		return "", err
	}
	return bundle.Build, nil
}

// computeExtensionBuildID matches packages/browser-relay/scripts/build-extension.ts:105-113.
func computeExtensionBuildID(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(files[name])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// stampExtensionManifest sets version_name in manifest.json.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:238-240
func stampExtensionManifest(manifestJSON []byte, version string) ([]byte, error) {
	var parsed map[string]any
	if err := json.Unmarshal(manifestJSON, &parsed); err != nil {
		return nil, fmt.Errorf("parse extension manifest.json: %w", err)
	}
	parsed["version_name"] = version
	out, err := json.MarshalIndent(parsed, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("marshal stamped manifest.json: %w", err)
	}
	return append(out, '\n'), nil
}

// stampExtensionScript prepends OMP_INSTALL_VERSION and OMP_INSTALL_BUILD to background.js.
// Wire constant names are intentionally kept as OMP_INSTALL_VERSION / OMP_INSTALL_BUILD so
// the upstream extension worker reads them unmodified.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:207-209
func stampExtensionScript(script []byte, version, build string) ([]byte, error) {
	vJSON, err := json.Marshal(version)
	if err != nil {
		return nil, err
	}
	bJSON, err := json.Marshal(build)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Grow(len(script) + len(vJSON) + len(bJSON) + 64)
	buf.WriteString("const OMP_INSTALL_VERSION = ")
	buf.Write(vJSON)
	buf.WriteString(";\nconst OMP_INSTALL_BUILD = ")
	buf.Write(bJSON)
	buf.WriteString(";\n")
	buf.Write(script)
	return buf.Bytes(), nil
}

var extensionStampRe = regexp.MustCompile(`^const OMP_INSTALL_VERSION = ("(?:[^"\\]|\\.)*");\n(?:const OMP_INSTALL_BUILD = ("(?:[^"\\]|\\.)*");\n)?`)

// readExtensionScriptStamp parses the stamp at the top of an installed background.js.
// upstream: packages/coding-agent/src/tools/browser/relay/protocol.ts:226-235
func readExtensionScriptStamp(script string) *ExtensionStamp {
	match := extensionStampRe.FindStringSubmatch(script)
	if len(match) < 2 || match[1] == "" {
		return nil
	}
	var version string
	if err := json.Unmarshal([]byte(match[1]), &version); err != nil {
		return nil
	}
	stamp := &ExtensionStamp{Version: version}
	if len(match) >= 3 && match[2] != "" {
		var build string
		if err := json.Unmarshal([]byte(match[2]), &build); err != nil {
			return nil
		}
		stamp.Build = build
		stamp.HasBuild = true
	}
	return stamp
}

// RelayExtensionFiles returns the extension files stamped with version.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:70-89
func RelayExtensionFiles(version string, bundle *ExtensionBundle) (map[string][]byte, error) {
	if bundle == nil {
		var err error
		bundle, err = LoadExtensionBundle()
		if err != nil {
			return nil, err
		}
	}
	rawManifest, okM := bundle.Files["manifest.json"]
	rawScript, okS := bundle.Files["background.js"]
	if !okM || !okS {
		return nil, errors.New("the oh-my-pig extension lacks manifest.json or background.js")
	}
	stampedManifest, err := stampExtensionManifest(rawManifest, version)
	if err != nil {
		return nil, err
	}
	stampedScript, err := stampExtensionScript(rawScript, version, bundle.Build)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(bundle.Files))
	for name, content := range bundle.Files {
		if name == "manifest.json" || name == "background.js" {
			continue
		}
		files[name] = content
	}
	files["manifest.json"] = stampedManifest
	files["background.js"] = stampedScript
	return files, nil
}

// WriteRelayExtension writes the stamped extension files of version into dir atomically.
// Assets are published first, manifest.json after assets, and background.js last, so an
// interrupted write leaves the worker reporting the previous version and Chrome never reads
// a partial file. Returns true if any file changed on disk.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:105-118
func WriteRelayExtension(dir, version string, bundle *ExtensionBundle) (bool, error) {
	files, err := RelayExtensionFiles(version, bundle)
	if err != nil {
		return false, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if name == "manifest.json" || name == "background.js" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	changed := false
	for _, name := range names {
		target := filepath.Join(dir, filepath.FromSlash(name))
		wrote, err := publishExtensionFile(target, files[name], 0o644)
		if err != nil {
			return changed, err
		}
		if wrote {
			changed = true
		}
	}
	wroteManifest, err := publishExtensionFile(filepath.Join(dir, "manifest.json"), files["manifest.json"], 0o644)
	if err != nil {
		return changed, err
	}
	if wroteManifest {
		changed = true
	}
	wroteScript, err := publishExtensionFile(filepath.Join(dir, "background.js"), files["background.js"], 0o644)
	if err != nil {
		return changed, err
	}
	return wroteScript || changed, nil
}

// publishExtensionFile writes content to target via a sibling temp file and atomic rename,
// skipping the write when target already has identical bytes.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:120-139
func publishExtensionFile(target string, content []byte, mode fs.FileMode) (bool, error) {
	if current, err := os.ReadFile(target); err == nil {
		if bytes.Equal(current, content) {
			return false, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := writeFileAtomic(target, content, mode); err != nil {
		return false, err
	}
	return true, nil
}

// writeFileAtomic stages data to a sibling temp file and renames it into place.
// upstream: packages/coding-agent/src/utils/atomic-file.ts
func writeFileAtomic(target string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	var suffix [8]byte
	_, _ = rand.Read(suffix[:])
	staged := fmt.Sprintf("%s.%s.tmp", target, hex.EncodeToString(suffix[:]))
	if err := os.WriteFile(staged, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(staged, mode); err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := renameAtomic(staged, target); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

// UnpackedExtensionID returns the Chrome extension ID for an unpacked install at dir,
// or "" if dir does not exist. On Unix Chrome canonicalizes via realpath; on Windows
// it uses _wfullpath (filepath.Abs) without resolving symlinks/junctions.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:150-157
func UnpackedExtensionID(dir string) string {
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	var abs string
	var err error
	if runtime.GOOS == "windows" {
		abs, err = filepath.Abs(dir)
	} else {
		abs, err = filepath.EvalSymlinks(dir)
		if err == nil {
			abs, err = filepath.Abs(abs)
		}
	}
	if err != nil {
		return ""
	}
	return ExtensionIDForPath(abs, runtime.GOOS)
}

// ExtensionIDForPath computes Chromium's `crx_file::id_util::GenerateIdForPath`:
// the first 128 bits of SHA-256 over the path's native bytes (UTF-8 on Unix,
// UTF-16LE with uppercased ASCII drive letter on Windows), hex nibbles mapped to a-p.
// Accepts Go GOOS values ("darwin", "linux", "windows") as well as Node's "win32".
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:163-173
func ExtensionIDForPath(absolutePath, goos string) string {
	if goos == "" {
		goos = runtime.GOOS
	}
	var raw []byte
	if goos == "windows" || goos == "win32" {
		if len(absolutePath) >= 2 && absolutePath[1] == ':' && absolutePath[0] >= 'a' && absolutePath[0] <= 'z' {
			absolutePath = strings.ToUpper(absolutePath[:1]) + absolutePath[1:]
		}
		encoded := utf16.Encode([]rune(absolutePath))
		raw = make([]byte, len(encoded)*2)
		for i, u := range encoded {
			raw[2*i] = byte(u)
			raw[2*i+1] = byte(u >> 8)
		}
	} else {
		raw = []byte(absolutePath)
	}
	sum := sha256.Sum256(raw)
	var id [32]byte
	for i := range 16 {
		id[2*i] = 'a' + (sum[i] >> 4)
		id[2*i+1] = 'a' + (sum[i] & 0x0f)
	}
	return string(id[:])
}

// InstalledExtensionStamp returns the stamp declared at the top of dir/background.js, or nil if absent/unstamped.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:179-186
func InstalledExtensionStamp(dir string) *ExtensionStamp {
	f, err := os.Open(filepath.Join(dir, "background.js"))
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	if n == 0 {
		return nil
	}
	return readExtensionScriptStamp(string(buf[:n]))
}

// InstalledExtensionVersion returns the version_name stamped in dir/manifest.json, or "" if absent or unreadable.
// upstream: packages/coding-agent/src/tools/browser/relay/extension-files.ts:189-199
func InstalledExtensionVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	v, _ := parsed["version_name"].(string)
	return v
}
