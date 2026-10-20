package pi_permission_system

import (
	"path/filepath"
	"strings"
)

// Port of src/config/config-loader.ts: reading, validating and merging the unified config files.

// UnifiedConfig is the on-disk config shape after validation (config-schema.ts UnifiedPermissionConfig). A nil pointer or slice is
// an absent field; an empty non-nil slice is a present empty list.
type UnifiedConfig struct {
	Schema                    *string
	DebugLog                  *bool
	PermissionReviewLog       *bool
	YoloMode                  *bool
	DoublePressToConfirm      *bool
	PermissionDialogKeys      DialogKeyOverrides
	PromptNotifications       []string
	ForwardingTimeoutMs       *float64
	PromptMaxRows             *float64
	PromptFieldMaxWidth       *float64
	ReviewLogFieldMaxWidth    *float64
	ToolInputPreviewMaxLength *float64
	ToolTextSummaryMaxLength  *float64
	PiInfrastructureReadPaths []string
	AuthorizerChain           []string
	Permission                *jsObject // the flat permission map, in the schema's output order
	ShellTools                *jsObject // tool name -> {commandArgument, workdirArgument?}
}

// UnifiedConfigLoadResult is a config and what was wrong with the file it came from.
type UnifiedConfigLoadResult struct {
	Config UnifiedConfig
	Issues []string
}

// normalizeFlatPermissionValue turns a raw `permission` value into a flat permission map: it keeps permission-state strings and
// deny-with-reason objects inside pattern maps and drops everything else. nil when nothing survives (config-loader.ts).
func normalizeFlatPermissionValue(value any) *jsObject {
	record, ok := value.(*jsObject)
	if !ok {
		return nil
	}
	normalized := newObject()
	hasAny := false
	for _, key := range record.order() {
		switch val := record.vals[key].(type) {
		case string:
			if _, ok := isPermissionState(val); ok {
				setOwn(normalized, key, val)
				hasAny = true
			}
		case *jsObject:
			m := newObject()
			mapHasAny := false
			for _, pattern := range val.order() {
				action := val.vals[pattern]
				if _, ok := denyWithReason(action); ok {
					setOwn(m, pattern, action)
					mapHasAny = true
				} else if _, ok := isPermissionState(action); ok {
					setOwn(m, pattern, action)
					mapHasAny = true
				}
			}
			if mapHasAny {
				setOwn(normalized, key, m)
				hasAny = true
			}
		}
	}
	if !hasAny {
		return nil
	}
	return normalized
}

func strPtrs(a any) []string {
	arr, _ := a.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, e.(string))
	}
	return out
}

// unifiedFromObject reads a parsed config the schema accepted.
func unifiedFromObject(root *jsObject) UnifiedConfig {
	var c UnifiedConfig
	if s, ok := root.str("$schema"); ok {
		c.Schema = &s
	}
	b := func(k string) *bool {
		if v, ok := root.flag(k); ok {
			return &v
		}
		return nil
	}
	n := func(k string) *float64 {
		if v, ok := root.num(k); ok {
			return &v
		}
		return nil
	}
	c.DebugLog, c.PermissionReviewLog, c.YoloMode, c.DoublePressToConfirm = b("debugLog"), b("permissionReviewLog"), b("yoloMode"), b("doublePressToConfirm")
	c.ForwardingTimeoutMs, c.PromptMaxRows, c.PromptFieldMaxWidth = n("forwardingTimeoutMs"), n("promptMaxRows"), n("promptFieldMaxWidth")
	c.ReviewLogFieldMaxWidth, c.ToolInputPreviewMaxLength, c.ToolTextSummaryMaxLength = n("reviewLogFieldMaxWidth"), n("toolInputPreviewMaxLength"), n("toolTextSummaryMaxLength")
	if keys := root.obj("permissionDialogKeys"); keys != nil {
		c.PermissionDialogKeys = DialogKeyOverrides{}
		for _, a := range actionOrder {
			if v, ok := keys.str(string(a)); ok {
				c.PermissionDialogKeys[a] = v
			}
		}
	}
	if v, ok := root.vals["promptNotifications"]; ok {
		c.PromptNotifications = strPtrs(v)
	}
	if v, ok := root.vals["piInfrastructureReadPaths"]; ok {
		c.PiInfrastructureReadPaths = strPtrs(v)
	}
	if v, ok := root.vals["authorizerChain"]; ok {
		c.AuthorizerChain = strPtrs(v)
	}
	if perm := root.obj("permission"); perm != nil {
		c.Permission = schemaPermission(perm)
	}
	if tools := root.obj("shellTools"); tools != nil {
		// zod's record output: "__proto__" assigns a prototype, it creates no key
		out := newObject()
		for _, k := range tools.order() {
			if k == "__proto__" {
				continue
			}
			alias := tools.obj(k)
			a := newObject()
			if s, ok := alias.str("commandArgument"); ok {
				a.set("commandArgument", s)
			}
			if s, ok := alias.str("workdirArgument"); ok {
				a.set("workdirArgument", s)
			}
			out.set(k, a)
		}
		c.ShellTools = out
	}
	return c
}

// toObject writes the config as the object the original holds (for saving): the schema's field order.
func (c UnifiedConfig) toObject() *jsObject {
	o := newObject()
	put := func(k string, v any, present bool) {
		if present {
			o.set(k, v)
		}
	}
	bl := func(k string, p *bool) {
		if p != nil {
			o.set(k, *p)
		}
	}
	nm := func(k string, p *float64) {
		if p != nil {
			o.set(k, *p)
		}
	}
	list := func(k string, a []string) {
		if a != nil {
			arr := make([]any, len(a))
			for i, s := range a {
				arr[i] = s
			}
			o.set(k, arr)
		}
	}
	if c.Schema != nil {
		o.set("$schema", *c.Schema)
	}
	bl("debugLog", c.DebugLog)
	bl("permissionReviewLog", c.PermissionReviewLog)
	bl("yoloMode", c.YoloMode)
	bl("doublePressToConfirm", c.DoublePressToConfirm)
	if c.PermissionDialogKeys != nil {
		keys := newObject()
		for _, a := range actionOrder {
			if v, ok := c.PermissionDialogKeys[a]; ok {
				keys.set(string(a), v)
			}
		}
		o.set("permissionDialogKeys", keys)
	}
	list("promptNotifications", c.PromptNotifications)
	nm("forwardingTimeoutMs", c.ForwardingTimeoutMs)
	nm("promptMaxRows", c.PromptMaxRows)
	nm("promptFieldMaxWidth", c.PromptFieldMaxWidth)
	nm("reviewLogFieldMaxWidth", c.ReviewLogFieldMaxWidth)
	nm("toolInputPreviewMaxLength", c.ToolInputPreviewMaxLength)
	nm("toolTextSummaryMaxLength", c.ToolTextSummaryMaxLength)
	list("piInfrastructureReadPaths", c.PiInfrastructureReadPaths)
	list("authorizerChain", c.AuthorizerChain)
	put("permission", c.Permission, c.Permission != nil)
	put("shellTools", c.ShellTools, c.ShellTools != nil)
	return o
}

// validateConfig validates raw parsed JSON against the config schema: the typed config on success; on failure the whole scope is
// rejected (an empty config contributes no rules, so missing surfaces fall through to the universal `ask` default) with one
// issue per violation.
func validateConfig(parsed any) UnifiedConfigLoadResult {
	if issues := validateUnifiedConfig(parsed); len(issues) > 0 {
		return UnifiedConfigLoadResult{Issues: issues}
	}
	return UnifiedConfigLoadResult{Config: unifiedFromObject(parsed.(*jsObject))}
}

// parseUnifiedText reads the text of the config file at path: comments stripped, JSON parsed with V8's error wording, then
// validated. Any failure is reported as "Failed to read config at '<path>': <message>".
func parseUnifiedText(text, path string) UnifiedConfigLoadResult {
	stripped := stripJSONComments(text)
	if msg := v8JSONError(stripped); msg != "" {
		return UnifiedConfigLoadResult{Issues: []string{"Failed to read config at '" + path + "': " + msg}}
	}
	v, err := parseJSON([]byte(stripped))
	if err != nil {
		return UnifiedConfigLoadResult{Issues: []string{"Failed to read config at '" + path + "': " + err.Error()}}
	}
	return validateConfig(v)
}

// loadUnifiedConfig loads a unified config file: empty with no issues when the file does not exist, empty with an issue when it
// cannot be read, parsed or validated.
func loadUnifiedConfig(path string) UnifiedConfigLoadResult {
	if !pathExists(path) {
		return UnifiedConfigLoadResult{}
	}
	text, err := readTextFile(path)
	if err != nil {
		return UnifiedConfigLoadResult{Issues: []string{"Failed to read config at '" + path + "': " + err.Error()}}
	}
	return parseUnifiedText(text, path)
}

func orBool(a, b *bool) *bool {
	if a != nil {
		return a
	}
	return b
}

func orNum(a, b *float64) *float64 {
	if a != nil {
		return a
	}
	return b
}

func orList(a, b []string) []string {
	if a != nil {
		return a
	}
	return b
}

// mergeUnifiedConfigs merges two unified configs: scalars and arrays are replaced when present in the override, permissionDialogKeys
// replaces the base map whole, shellTools merge by tool name, permission merges per surface. `$schema` is not carried.
func mergeUnifiedConfigs(base, override UnifiedConfig) UnifiedConfig {
	var m UnifiedConfig
	m.DebugLog, m.PermissionReviewLog = orBool(override.DebugLog, base.DebugLog), orBool(override.PermissionReviewLog, base.PermissionReviewLog)
	m.YoloMode, m.DoublePressToConfirm = orBool(override.YoloMode, base.YoloMode), orBool(override.DoublePressToConfirm, base.DoublePressToConfirm)
	m.ForwardingTimeoutMs = orNum(override.ForwardingTimeoutMs, base.ForwardingTimeoutMs)
	m.PromptMaxRows = orNum(override.PromptMaxRows, base.PromptMaxRows)
	m.PromptFieldMaxWidth = orNum(override.PromptFieldMaxWidth, base.PromptFieldMaxWidth)
	m.ReviewLogFieldMaxWidth = orNum(override.ReviewLogFieldMaxWidth, base.ReviewLogFieldMaxWidth)
	m.ToolInputPreviewMaxLength = orNum(override.ToolInputPreviewMaxLength, base.ToolInputPreviewMaxLength)
	m.ToolTextSummaryMaxLength = orNum(override.ToolTextSummaryMaxLength, base.ToolTextSummaryMaxLength)
	m.PiInfrastructureReadPaths = orList(override.PiInfrastructureReadPaths, base.PiInfrastructureReadPaths)
	m.AuthorizerChain = orList(override.AuthorizerChain, base.AuthorizerChain)
	m.PermissionDialogKeys = override.PermissionDialogKeys
	if m.PermissionDialogKeys == nil {
		m.PermissionDialogKeys = base.PermissionDialogKeys
	}
	m.PromptNotifications = orList(override.PromptNotifications, base.PromptNotifications)
	switch {
	case base.ShellTools != nil && override.ShellTools != nil:
		m.ShellTools = base.ShellTools.clone()
		for _, k := range override.ShellTools.order() {
			m.ShellTools.set(k, override.ShellTools.vals[k])
		}
	case base.ShellTools != nil:
		m.ShellTools = base.ShellTools
	case override.ShellTools != nil:
		m.ShellTools = override.ShellTools
	}
	switch {
	case base.Permission != nil && override.Permission != nil:
		m.Permission = MergeFlatPermissions(base.Permission, override.Permission)
	case base.Permission != nil:
		m.Permission = base.Permission
	case override.Permission != nil:
		m.Permission = override.Permission
	}
	return m
}

// MergedConfigResult is the global, project and merged configs and every issue found while loading them.
type MergedConfigResult struct {
	Global  UnifiedConfig
	Project UnifiedConfig
	Merged  UnifiedConfig
	Issues  []string
}

// MergeOptions are the options of loadAndMergeConfigs. IncludeProjectScope nil is true: the trusted, caller-agnostic path.
type MergeOptions struct{ IncludeProjectScope *bool }

// loadAndMergeConfigs loads the global and project configs from the new layout, detects legacy files, merges everything and
// collects issues. Merge order: legacy global policy, legacy extension runtime config (when its path differs from the new global
// one), new global, legacy project policy, new project config. Legacy files are noted, never translated. Without the project scope
// (an untrusted project, #644) the last two steps are skipped entirely.
func loadAndMergeConfigs(agentDir, cwd, extensionRoot string, opts MergeOptions) MergedConfigResult {
	includeProject := opts.IncludeProjectScope == nil || *opts.IncludeProjectScope
	allIssues := []string{}
	newGlobalPath := getGlobalConfigPath(agentDir)
	newProjectPath := getProjectConfigPath(cwd)
	legacyGlobalPolicyPath := getLegacyGlobalPolicyPath(agentDir)
	legacyProjectPolicyPath := getLegacyProjectPolicyPath(cwd)
	legacyExtConfigPath := getLegacyExtensionConfigPath(extensionRoot)

	var merged UnifiedConfig
	if pathExists(legacyGlobalPolicyPath) {
		legacy := loadUnifiedConfig(legacyGlobalPolicyPath)
		allIssues = append(allIssues, "Legacy global policy found at '"+legacyGlobalPolicyPath+"'. "+
			"Move it to '"+newGlobalPath+"':\n"+"  mv '"+legacyGlobalPolicyPath+"' '"+newGlobalPath+"'")
		// the move-it guidance is the actionable signal, so the strict-validation issues of a legacy file are suppressed
		merged = mergeUnifiedConfigs(merged, legacy.Config)
	}
	// extensionRoot "" is no root: the Go port has no install directory holding a legacy config.json to look for
	if extensionRoot != "" && filepath.Clean(legacyExtConfigPath) != filepath.Clean(newGlobalPath) && pathExists(legacyExtConfigPath) {
		legacy := loadUnifiedConfig(legacyExtConfigPath)
		allIssues = append(allIssues, "Legacy extension config found at '"+legacyExtConfigPath+"'. "+
			"Move runtime settings to '"+newGlobalPath+"':\n"+"  mv '"+legacyExtConfigPath+"' '"+newGlobalPath+"'")
		merged = mergeUnifiedConfigs(merged, legacy.Config)
	}
	globalResult := loadUnifiedConfig(newGlobalPath)
	allIssues = append(allIssues, globalResult.Issues...)
	merged = mergeUnifiedConfigs(merged, globalResult.Config)

	if includeProject && pathExists(legacyProjectPolicyPath) {
		legacy := loadUnifiedConfig(legacyProjectPolicyPath)
		allIssues = append(allIssues, "Legacy project policy found at '"+legacyProjectPolicyPath+"'. "+
			"Move it to '"+newProjectPath+"':\n"+"  mv '"+legacyProjectPolicyPath+"' '"+newProjectPath+"'")
		merged = mergeUnifiedConfigs(merged, legacy.Config)
	}
	var projectResult UnifiedConfigLoadResult
	if includeProject {
		projectResult = loadUnifiedConfig(newProjectPath)
	}
	allIssues = append(allIssues, projectResult.Issues...)
	merged = mergeUnifiedConfigs(merged, projectResult.Config)

	if issue := detectPermissiveBashFallback(merged.Permission); issue != "" {
		allIssues = append(allIssues, issue)
	}
	if issue := detectDeprecatedPreviewCaps(merged); issue != "" {
		allIssues = append(allIssues, issue)
	}
	if issue := detectUnusableDialogKeys(merged); issue != "" {
		allIssues = append(allIssues, issue)
	}
	return MergedConfigResult{Global: globalResult.Config, Project: projectResult.Config, Merged: merged, Issues: allIssues}
}

// detectDeprecatedPreviewCaps notices a config still setting one of the two superseded tool-preview caps; "" when neither is set.
func detectDeprecatedPreviewCaps(c UnifiedConfig) string {
	var set []string
	if c.ToolInputPreviewMaxLength != nil {
		set = append(set, "'toolInputPreviewMaxLength'")
	}
	if c.ToolTextSummaryMaxLength != nil {
		set = append(set, "'toolTextSummaryMaxLength'")
	}
	if len(set) == 0 {
		return ""
	}
	return "Permission config sets " + strings.Join(set, " and ") + ", which is deprecated and ignored. The prompt is bounded by " +
		"'promptMaxRows' and 'promptFieldMaxWidth' instead; remove the setting."
}

// detectUnusableDialogKeys names a permissionDialogKeys entry the dialog cannot honor, "" when every one is usable. A warning, not
// a fail-closed rejection: a mistyped hotkey is cosmetic.
func detectUnusableDialogKeys(c UnifiedConfig) string {
	issues := resolveDialogKeys(c.PermissionDialogKeys).Issues
	if len(issues) == 0 {
		return ""
	}
	return strings.Join(issues, " ")
}
