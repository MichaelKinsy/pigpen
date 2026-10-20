package pi_subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config directory and settings files (src/shared/utils.ts, src/shared/settings.ts, src/agents/agents.ts).

const (
	defaultConfigDirName        = ".pi"
	piCodingAgentPackageName    = "@earendil-works/pi-coding-agent"
	piCodingAgentPackageRootEnv = "PI_SUBAGENTS_PI_CODING_AGENT_PACKAGE_ROOT"
)

// validConfigDirName is utils.ts:20 validConfigDirName.
func validConfigDirName(v any) string {
	if s, ok := v.(string); ok && jsTrim(s) != "" {
		return s
	}
	return ""
}

// readConfigDirNameFromPackageRoot is utils.ts:24.
func readConfigDirNameFromPackageRoot(root string) string {
	if root == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	v, err := parseJSON(b)
	o, _ := v.(*jsObject)
	if err != nil || o == nil {
		return ""
	}
	if n, _ := o.str("name"); n != piCodingAgentPackageName {
		return ""
	}
	if pc := o.obj("piConfig"); pc != nil {
		v, _ := pc.get("configDir")
		return validConfigDirName(v)
	}
	return ""
}

// resolveConfigDirName is utils.ts:resolveConfigDirName. module is the Pi module's CONFIG_DIR_NAME ("" for none),
// entryPoint the process entry file ("" for none: PiG has no JavaScript entry point), packageRoot the explicit Pi package root.
func resolveConfigDirName(module, entryPoint, packageRoot string) string {
	if v := validConfigDirName(module); v != "" {
		return v
	}
	if v := readConfigDirNameFromPackageRoot(packageRoot); v != "" {
		return v
	}
	if entryPoint != "" {
		if real, err := filepath.EvalSymlinks(entryPoint); err == nil {
			dir := filepath.Dir(real)
			for dir != filepath.Dir(dir) {
				if v := readConfigDirNameFromPackageRoot(dir); v != "" {
					return v
				}
				dir = filepath.Dir(dir)
			}
		}
	}
	return defaultConfigDirName
}

// getConfigDirName is utils.ts:getConfigDirName (no cache: the resolution is two small file reads and follows the environment).
func getConfigDirName() string {
	return resolveConfigDirName("", "", os.Getenv(piCodingAgentPackageRootEnv))
}

func getProjectConfigDir(projectRoot string) string {
	return filepath.Join(projectRoot, getConfigDirName())
}

// expandHomePath is settings.ts:231.
func expandHomePath(p string) string {
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

// readSettingsFileStrict is agents.ts:921: {} for a missing file, an error naming the file for anything unreadable.
func readSettingsFileStrict(filePath string) (*jsObject, error) {
	b, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return newObject(), nil
		}
		return nil, fmt.Errorf("Failed to read settings file '%s': %s", filePath, err.Error())
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse settings file '%s': %s", filePath, err.Error())
	}
	o, ok := v.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Settings file '%s' must contain a JSON object.", filePath)
	}
	return o, nil
}

// writeSettingsFile is agents.ts:944.
func writeSettingsFile(filePath string, settings *jsObject) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(marshalJSON(settings, "  ")+"\n"), 0o644)
}

// isPathWithin is agents.ts:2551.
func isPathWithin(root, candidate string) bool {
	r, _ := filepath.Abs(root)
	c, _ := filepath.Abs(candidate)
	rel, err := filepath.Rel(r, c)
	if err != nil {
		return false
	}
	return rel == "" || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// canonicalAgentPath is agents.ts:2406: resolves the existing ancestors of a path that may not exist.
func canonicalAgentPath(p string) string {
	resolved, _ := filepath.Abs(p)
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		return real
	}
	parent := filepath.Dir(resolved)
	if parent == resolved {
		return resolved
	}
	return filepath.Join(canonicalAgentPath(parent), filepath.Base(resolved))
}
