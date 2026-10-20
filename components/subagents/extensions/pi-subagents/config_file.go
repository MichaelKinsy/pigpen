package pi_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Port of the file half of src/extension/config.ts (getConfigPath, loadConfig, saveConfig, updateConfig, validateConfig) for the
// keys whose validators are ported: scheduledRuns (resolveScheduledStoreRoot), missions, authorityPolicy, permissions and the
// scalar keys. The validators of fleetKeybindings, artifactConfig, capacity, modelResponseAliases, mainWindowRenderer and
// orcaProgressTabs belong to the slices that own those keys and are not run here; the other slices read their own keys raw.

// resolveScheduledStoreRoot is resolveScheduledStoreRoot: an absolute path or "~/..." (a project-relative one would make the
// store depend on the working directory).
func resolveScheduledStoreRoot(value string) (string, error) {
	expanded := value
	if strings.HasPrefix(value, "~/") {
		expanded = filepath.Join(homeDir(), value[2:])
	}
	if !filepath.IsAbs(expanded) && !strings.HasPrefix(expanded, "/") {
		return "", fmt.Errorf("config.scheduledRuns.storeRoot must be an absolute path or \"~/...\", got %s", marshalJSON(value, ""))
	}
	return filepath.Clean(expanded), nil
}

func validateScheduledRunsConfig(v any) error {
	if isUndefined(v) {
		return nil
	}
	o, ok := asObject(v)
	if !ok {
		return errors.New("config.scheduledRuns must be a JSON object")
	}
	root, has := o.vals["storeRoot"]
	if !has || isUndefined(root) {
		return nil
	}
	s, isStr := root.(string)
	if !isStr || jsTrim(s) == "" {
		return errors.New("config.scheduledRuns.storeRoot must be a non-empty string")
	}
	_, err := resolveScheduledStoreRoot(s)
	return err
}

var artifactDirPreferences = map[string]bool{"project": true, "session": true, "temp": true}

func validateForkContextConfig(v any) error {
	if isUndefined(v) {
		return nil
	}
	o, ok := asObject(v)
	if !ok {
		return errors.New("config.forkContext must be a JSON object")
	}
	mode, hasMode := o.vals["mode"]
	if hasMode && !isUndefined(mode) && mode != "full" && mode != "pruned" {
		return errors.New(`config.forkContext.mode must be "full" or "pruned"`)
	}
	model, hasModel := o.vals["model"]
	if hasModel && !isUndefined(model) {
		if s, ok := model.(string); !ok || jsTrim(s) == "" {
			return errors.New("config.forkContext.model must be a non-empty string")
		}
	}
	if mode == "pruned" && (!hasModel || isUndefined(model)) {
		return errors.New(`config.forkContext.model is required when config.forkContext.mode is "pruned"`)
	}
	return nil
}

// validateExtensionConfig is validateConfig for the keys listed above.
func validateExtensionConfig(c *jsObject) error {
	get := func(k string) (any, bool) {
		v, ok := c.vals[k]
		return v, ok && !isUndefined(v)
	}
	if v, ok := get("worktree"); ok {
		if _, isBool := v.(bool); !isBool {
			return errors.New("config.worktree must be a boolean")
		}
	}
	if v, ok := get("worktreeProvider"); ok && v != "auto" && v != "native" && v != "worktrunk" {
		return errors.New(`config.worktreeProvider must be "auto", "native", or "worktrunk"`)
	}
	if v, ok := get("defaultSubagentContext"); ok && v != "fresh" && v != "fork" {
		return errors.New(`config.defaultSubagentContext must be "fresh" or "fork"`)
	}
	if err := validateForkContextConfig(c.vals["forkContext"]); err != nil {
		return err
	}
	if v, ok := get("checkpointBeforeDeadlineMs"); ok {
		f, isNum := v.(float64)
		if !isNum || !isIntegerNum(f) || f <= 0 || f > 2147483647 {
			return errors.New("config.checkpointBeforeDeadlineMs must be a positive integer no larger than 2147483647")
		}
	}
	if v, ok := get("artifactDir"); ok {
		if s, isStr := v.(string); !isStr || !artifactDirPreferences[s] {
			return errors.New(`config.artifactDir must be "project", "session", or "temp"`)
		}
	}
	if v, ok := get("maxActiveAsyncRunsPerSession"); ok {
		f, isNum := v.(float64)
		if !isNum || !isIntegerNum(f) || f < 0 {
			return errors.New("config.maxActiveAsyncRunsPerSession must be a non-negative integer")
		}
	}
	if v, ok := get("resultScanLogging"); ok && v != "all" && v != "activity" && v != "off" {
		return errors.New(`config.resultScanLogging must be "all", "activity", or "off"`)
	}
	mv, hasMissions := get("missions")
	if _, err := validateMissionStoreConfig(mv, hasMissions, "config.missions"); err != nil {
		return err
	}
	if _, err := validateAuthorityPolicy(c.vals["authorityPolicy"], "config.authorityPolicy"); err != nil {
		return err
	}
	if _, err := validatePermissionConfig(c.vals["permissions"], "config.permissions"); err != nil {
		return err
	}
	if err := validateScheduledRunsConfig(c.vals["scheduledRuns"]); err != nil {
		return err
	}
	if _, ok := get("modelExclusions"); ok {
		return errors.New("config.modelExclusions was removed; model failures are no longer persisted or used for automatic switching")
	}
	return nil
}

// extensionConfigPath is getConfigPath: <agent dir>/extensions/subagent/config.json.
func extensionConfigPath() string { return asyncConfigPath() }

func readConfigForUpdate(path string) (*jsObject, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newObject(), nil
		}
		return nil, err
	}
	v, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*jsObject)
	if !ok {
		return nil, fmt.Errorf("Subagent config at '%s' must be a JSON object", path)
	}
	if err := validateExtensionConfig(o); err != nil {
		return nil, err
	}
	return o, nil
}

// saveExtensionConfig is saveConfig: tab-indented JSON.
func saveExtensionConfig(c *jsObject) error {
	path := extensionConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(marshalJSON(c, "\t")+"\n"), 0o644)
}

// updateExtensionConfig is updateConfig: the updater's result is validated before anything is written.
func updateExtensionConfig(update func(*jsObject) *jsObject) (*jsObject, error) {
	cur, err := readConfigForUpdate(extensionConfigPath())
	if err != nil {
		return nil, err
	}
	next := update(cur)
	if err := validateExtensionConfig(next); err != nil {
		return nil, err
	}
	return next, saveExtensionConfig(next)
}

// loadStrictExtensionConfig is loadConfig: a config that fails validation is reported and replaced by an empty one.
func loadStrictExtensionConfig() *jsObject {
	cfg, err := readConfigForUpdate(extensionConfigPath())
	if err != nil {
		scheduleLogError(fmt.Sprintf("Failed to load subagent config from '%s':", extensionConfigPath()), err)
		return newObject()
	}
	return cfg
}
