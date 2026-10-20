package pi_goal_x

// The /goal-settings menu (goal-commands.ts handleSettingsMenu).

import (
	"regexp"
	"strconv"
	"strings"
)

// goalSettingRow is SettingRow; one declarative row table drives rendering and dispatch, so the displayed fields and the selectable
// fields can never drift apart.
type goalSettingRow struct {
	key, label, section, kind string
	// path is the settings path for the mutation (nil: [key]).
	path []string
}

var goalSettingRows = []goalSettingRow{
	{"autoSelectSingleGoal", "autoSelectSingleGoal", "Goal behavior", "boolean", nil},
	{"hideUnfocusedBanner", "hideUnfocusedBanner", "Goal behavior", "boolean", nil},
	{"hideUnfocusedPrompt", "hideUnfocusedPrompt", "Goal behavior", "boolean", nil},
	{"disableContracts", "disableContracts", "Goal behavior", "boolean", nil},
	{"strictExecutionContract", "explicit execution contracts (opt-in)", "Goal behavior", "boolean", nil},
	{"maxAutonomousRuns", "autonomous run allowance", "Goal behavior", "positiveInteger", nil},
	{"showAutonomousRuns", "show autonomous runs line", "Goal behavior", "boolean", nil},
	{"stallTimeoutMinutes", "stall timeout (minutes)", "Goal behavior", "positiveInteger", nil},
	{"objectiveMaxChars", "max objective length (0 = none)", "Goal behavior", "positiveInteger", nil},
	{"disableTasks", "disableTasks", "Task tracking", "boolean", nil},
	{"subtaskDepth", "subtaskDepth", "Task tracking", "positiveInteger", nil},
	{"disabled", "auditor disabled", "Completion auditor", "boolean", nil},
	{"provider", "provider", "Completion auditor", "modelSelector", nil},
	{"model", "model", "Completion auditor", "modelSelector", nil},
	{"thinkingLevel", "thinking_level", "Completion auditor", "thinking", nil},
	// Issue #26: opt-in blocker Oracle. Disabled by default; provider/model must BOTH be set explicitly.
	{"oracleEnabled", "oracle enabled", "Blocker Oracle", "boolean", []string{"oracle", "enabled"}},
	{"oracleProviderModel", "oracle provider/model", "Blocker Oracle", "modelSelector", []string{"oracle"}},
	{"oracleThinkingLevel", "oracle thinking_level", "Blocker Oracle", "thinking", []string{"oracle", "thinkingLevel"}},
	{"oracleProjectResources", "oracle project resources", "Blocker Oracle", "boolean", []string{"oracle", "projectResources"}},
	{"oracleMaxFailedAttemptsPerBlocker", "max failed attempts per blocker", "Blocker Oracle", "positiveInteger", []string{"oracle", "maxFailedAttemptsPerBlocker"}},
}

func (r goalSettingRow) rowPath() []string {
	if r.path != nil {
		return r.path
	}
	return []string{r.key}
}

// settingsValue is settingsValue(config, key).
func settingsValue(config goalSettings, key string) string {
	switch key {
	case "strictExecutionContract":
		return strconv.FormatBool(config.strictExecutionContract)
	case "disabled":
		return strconv.FormatBool(config.disabled)
	case "disableTasks":
		return strconv.FormatBool(config.disableTasks)
	case "disableContracts":
		return strconv.FormatBool(config.disableContracts)
	case "autoSelectSingleGoal":
		return strconv.FormatBool(config.autoSelectSingleGoal)
	case "auditorProjectResources":
		return strconv.FormatBool(config.auditorProjectResources)
	case "hideUnfocusedBanner":
		return strconv.FormatBool(config.hideUnfocusedBanner)
	case "hideUnfocusedPrompt":
		return strconv.FormatBool(config.hideUnfocusedPrompt)
	case "showAutonomousRuns":
		return strconv.FormatBool(config.showAutonomousRuns)
	case "subtaskDepth":
		return jsNumber(config.subtaskDepth)
	case "maxAutonomousRuns":
		if config.maxAutonomousRuns == nil {
			return "unlimited (default)"
		}
		if *config.maxAutonomousRuns == 0 {
			return "0 (disabled)"
		}
		return jsNumber(*config.maxAutonomousRuns)
	case "stallTimeoutMinutes":
		return jsNumber(config.stallTimeoutMinutes)
	case "objectiveMaxChars":
		return jsNumber(config.objectiveMaxChars)
	case "provider":
		return orDefaultText(config.provider)
	case "model":
		return orDefaultText(config.model)
	case "thinkingLevel":
		return orDefaultText(config.thinkingLevel)
	}
	return "(default)"
}

func orDefaultText(s string) string {
	if s == "" {
		return "(default)"
	}
	return s
}

// settingsPathDisplay is String(valueAtPath(snapshot.value, path) ?? "(default)") for the nested Blocker Oracle rows.
func settingsPathDisplay(v goalSettings, path []string) string {
	if len(path) == 0 || path[0] != "oracle" {
		return "(default)"
	}
	if len(path) == 1 {
		return "[object Object]"
	}
	switch path[1] {
	case "enabled":
		return strconv.FormatBool(v.oracle.enabled)
	case "projectResources":
		return strconv.FormatBool(v.oracle.projectResources)
	case "maxFailedAttemptsPerBlocker":
		return strconv.Itoa(v.oracle.maxFailedAttemptsPerBlocker)
	case "thinkingLevel":
		return orDefaultText(v.oracle.thinkingLevel)
	case "provider":
		return orDefaultText(v.oracle.provider)
	case "model":
		return orDefaultText(v.oracle.model)
	}
	return "(default)"
}

// layerValueAtPath is valueAtPath(layer, path): nil when any step is missing or not an object.
func layerValueAtPath(layer *jsObject, path []string) any {
	var cursor any = layer
	for _, seg := range path {
		o, ok := cursor.(*jsObject)
		if !ok || o == nil {
			return nil
		}
		v, has := o.get(seg)
		if !has {
			return nil
		}
		if _, isUndef := v.(undef); isUndef {
			return nil
		}
		cursor = v
	}
	if _, isUndef := cursor.(undef); isUndef {
		return nil
	}
	return cursor
}

func jsDisplay(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsNumber(t)
	case int:
		return strconv.Itoa(t)
	}
	return "[object Object]"
}

var settingsDigitsRe = regexp.MustCompile(`^[0-9]+$`)

func (c *core) handleSettingsMenu() {
	cwd := c.h.Cwd()
	if !c.h.HasUI() {
		// Headless: read-only report of both layer paths.
		c.h.Notify("Settings — project file: "+goalSettingsPath(cwd)+"\nGlobal file: "+globalSettingsPath(), "info")
		return
	}

	// After any mutation: reinstall the fixed tool profile when the effective disableTasks value changed since the last install, and
	// refresh the UI. This is the central settings side-effect hook — no ad hoc per-row refresh code.
	applyEffectiveSettings := func() {
		tasksEnabledNow := !loadGoalSettings(cwd).disableTasks
		if tasksEnabledNow != c.tasksEnabled {
			c.installGoalToolProfile(tasksEnabledNow)
		}
		// PR #29: settings changes must be visible immediately — the banner hide/restore happens through this coalesced UI refresh.
		c.updateUI()
	}
	applyMutation := func(scope settingsScope, m settingsMutation) bool {
		if _, err := mutateSettingsLayer(settingsMutateInput{scope: scope, cwd: cwd, mutation: m}); err != nil {
			c.h.Notify("Settings change failed: "+err.Error(), "warning")
			return false
		}
		applyEffectiveSettings()
		return true
	}
	scopePath := func(scope settingsScope) string {
		if scope == settingsScopeGlobal {
			return globalSettingsPath()
		}
		return goalSettingsPath(cwd)
	}
	selectOne := func(title string, options []string) (string, bool) {
		var v string
		var ok bool
		c.awaiting(func() { v, ok = c.h.Select(title, options) })
		return v, ok && v != ""
	}
	inputOne := func(title, placeholder string) (string, bool) {
		var v string
		var ok bool
		c.awaiting(func() { v, ok = c.h.Input(title, placeholder) })
		return v, ok
	}

	c.enterGoalModal()
	defer c.exitGoalModal()
	scope := settingsScopeProject
	for {
		snapshot := loadSettingsSnapshot(cwd)
		read := snapshot.project
		if scope == settingsScopeGlobal {
			read = snapshot.global
		}
		var localLayer *jsObject
		if read != nil {
			localLayer = read.layer
		}
		options := []string{"─── Editing: " + string(scope) + " (" + scopePath(scope) + ") ───"}
		lastSection := ""
		for _, row := range goalSettingRows {
			if row.section != lastSection {
				options = append(options, "─── "+row.section+" ───")
				lastSection = row.section
			}
			if envVar := envOverrideFor(row.key); envVar != "" {
				options = append(options, "  "+row.label+": "+settingsValue(snapshot.value, row.key)+" (environment; read-only)")
				continue
			}
			rowPath := row.rowPath()
			prov := snapshot.provenance[strings.Join(rowPath, ".")]
			var source string
			switch {
			case prov != nil && prov.source == "global":
				source = "(global override)"
				if scope == settingsScopeProject {
					source = "(inherited from global)"
				}
			case prov != nil && prov.source == "project":
				source = "(project; edit via project scope)"
				if scope == settingsScopeProject {
					source = "(project override)"
				}
			default:
				source = "(default)"
			}
			hasLocalOverride := layerValueAtPath(localLayer, rowPath) != nil
			if hasLocalOverride && prov != nil && prov.source == string(scope) {
				source = "(" + string(scope) + " override)"
			}
			// Nested rows render their leaf value directly.
			display := settingsValue(snapshot.value, row.key)
			if row.path != nil {
				display = settingsPathDisplay(snapshot.value, rowPath)
			}
			options = append(options, "  "+row.label+": "+display+" "+source)
		}
		options = append(options, "Done")
		selected, ok := selectOne("Goal settings", options)
		if !ok || selected == "Done" {
			break
		}
		if strings.HasPrefix(selected, "───") {
			continue // headers are not rows
		}
		trimmed := jsTrim(selected)
		if strings.HasPrefix(trimmed, "Scope:") {
			if scope == settingsScopeProject {
				scope = settingsScopeGlobal
			} else {
				scope = settingsScopeProject
			}
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon == -1 {
			continue
		}
		label := jsTrim(trimmed[:colon])
		var row *goalSettingRow
		for i := range goalSettingRows {
			if goalSettingRows[i].label == label {
				row = &goalSettingRows[i]
				break
			}
		}
		if row == nil {
			continue
		}
		if envVar := envOverrideFor(row.key); envVar != "" {
			c.h.Notify(row.label+" is read-only: overridden by the "+envVar+" env var.", "warning")
			continue
		}
		// Per-row settings path (flat keys default to [key]; nested Blocker Oracle rows carry explicit paths).
		rowPath := row.rowPath()
		pathKey := strings.Join(rowPath, ".")
		hasLocalOverride := layerValueAtPath(localLayer, rowPath) != nil
		inheritLabel := "Use default (delete global override)"
		if scope == settingsScopeProject {
			inheritLabel = "Use inherited value"
		}

		switch row.kind {
		case "boolean":
			effective := false
			if p := snapshot.provenance[pathKey]; p != nil {
				effective = p.value == true
			}
			actions := []string{"Set " + string(scope) + " override to true", "Set " + string(scope) + " override to false"}
			if hasLocalOverride {
				actions = append(actions, inheritLabel)
			}
			actions = append(actions, "Cancel")
			action, ok := selectOne(row.label+" ("+string(scope)+")", actions)
			if !ok || action == "Cancel" {
				continue
			}
			if action == inheritLabel {
				applyMutation(scope, settingsMutation{op: "unset", path: rowPath})
				continue
			}
			value := strings.HasSuffix(action, "true")
			if value != effective || !hasLocalOverride {
				applyMutation(scope, settingsMutation{op: "set", path: rowPath, value: value})
			}
		case "positiveInteger":
			min := 1
			if row.path == nil && (row.key == "stallTimeoutMinutes" || row.key == "objectiveMaxChars" || row.key == "maxAutonomousRuns") {
				min = 0
			}
			actions := []string{"Set " + string(scope) + " override..."}
			if hasLocalOverride {
				actions = append(actions, inheritLabel)
			}
			actions = append(actions, "Cancel")
			action, ok := selectOne(row.label+" ("+string(scope)+")", actions)
			if !ok || action == "Cancel" {
				continue
			}
			if action == inheritLabel {
				applyMutation(scope, settingsMutation{op: "unset", path: rowPath})
				continue
			}
			placeholder := strconv.Itoa(min)
			if p := snapshot.provenance[pathKey]; p != nil && p.value != nil {
				placeholder = jsDisplay(p.value)
			}
			input, ok := inputOne("Set "+row.label, placeholder)
			if !ok {
				continue
			}
			// Full-string decimal validation: rejects 1.5, 1x, negatives, infinity, and unsafe integers alike.
			value := jsTrim(input)
			num, _ := strconv.ParseFloat(value, 64)
			if !settingsDigitsRe.MatchString(value) || num > 9007199254740991 || num < float64(min) {
				c.h.Notify(row.label+" must be an integer >= "+strconv.Itoa(min)+" (e.g. "+strconv.Itoa(min)+", "+strconv.Itoa(min+1)+", "+strconv.Itoa(min+2)+")", "warning")
				continue
			}
			// Oracle attempt cap is bounded at 3 by the settings parser.
			if len(row.path) > 0 && row.path[0] == "oracle" && num > 3 {
				c.h.Notify(row.label+" must be an integer between 1 and 3", "warning")
				continue
			}
			applyMutation(scope, settingsMutation{op: "set", path: rowPath, value: num})
		case "thinking":
			currentValue := settingsValue(snapshot.value, row.key)
			current := currentValue
			if currentValue == "(default)" {
				current = ""
			}
			picked, ok := selectOne("Set "+row.label+" ("+string(scope)+")", thinkingLevelChoices(current))
			if !ok {
				continue
			}
			choice := strings.TrimLeftFunc(jsTrim(picked), isJSSpace)
			if rest, found := strings.CutPrefix(choice, "\u2713"); found {
				if trimmedRest := strings.TrimLeftFunc(rest, isJSSpace); len(trimmedRest) < len(rest) {
					choice = trimmedRest
				}
			}
			if choice == "(default)" {
				if hasLocalOverride {
					applyMutation(scope, settingsMutation{op: "unset", path: rowPath})
				}
				continue
			}
			known := false
			for _, l := range auditorThinkingLevels {
				known = known || l == choice
			}
			if !known {
				c.h.Notify("thinking_level must be one of: "+strings.Join(auditorThinkingLevels, ", ")+" (or \"(default)\")", "warning")
				continue
			}
			applyMutation(scope, settingsMutation{op: "set", path: rowPath, value: choice})
		default:
			c.settingsModelSelector(row, scope, snapshot, localLayer, applyMutation, selectOne, inputOne)
		}
	}
}

// settingsModelSelector handles the modelSelector rows (provider, model): a searchable model picker with explicit inherit/default
// handling. Auditor rows apply provider+model at the flat paths; the Oracle row writes oracle.provider/oracle.model.
func (c *core) settingsModelSelector(row *goalSettingRow, scope settingsScope, snapshot *settingsSnapshot, localLayer *jsObject,
	applyMutation func(settingsScope, settingsMutation) bool, selectOne func(string, []string) (string, bool), inputOne func(string, string) (string, bool)) {
	oraclePair := len(row.path) > 0 && row.path[0] == "oracle"
	var pairPrefix []string
	var configured string
	if oraclePair {
		pairPrefix = []string{"oracle"}
		var parts []string
		for _, p := range []string{snapshot.value.oracle.provider, snapshot.value.oracle.model} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		configured = strings.Join(parts, "/")
	} else {
		configured = configuredAuditorModelKey(snapshot.value.provider, snapshot.value.model)
	}
	session := ""
	if m := c.h.Model(); m != nil {
		session = m.provider + "/" + m.id
	}
	var available []auditorModelSummary
	if reg := c.h.ModelRegistry(); reg != nil {
		for _, m := range reg.getAvailable() {
			available = append(available, auditorModelSummary{provider: m.provider, id: m.id, name: m.name})
		}
	}
	choices := buildAuditorModelChoices(available, configured, session)
	filter, ok := inputOne("Filter auditor models (provider/id/name; blank = all)", "")
	if !ok {
		return
	}
	filtered := filterAuditorModelChoices(choices, filter)
	labels := make([]string, len(filtered))
	for i, ch := range filtered {
		labels[i] = ch.label
	}
	picked, ok := selectOne("Select auditor model", labels)
	if !ok {
		return
	}
	var choice *auditorChoice
	for i := range filtered {
		if filtered[i].label == picked {
			choice = &filtered[i]
			break
		}
	}
	if choice == nil {
		return
	}
	if choice.kind == "default" {
		for _, key := range []string{"provider", "model"} {
			if layerValueAtPath(localLayer, []string{key}) != nil {
				applyMutation(scope, settingsMutation{op: "unset", path: append(append([]string{}, pairPrefix...), key)})
			}
		}
		return
	}
	providerValue, modelValue := choice.provider, choice.model
	if choice.kind == "manual" {
		placeholder := configured
		if placeholder == "" {
			placeholder = "provider/model"
		}
		input, ok := inputOne("Set auditor provider/model", placeholder)
		if !ok {
			return
		}
		p, m, errMsg := parseManualAuditorModel(input)
		if errMsg != "" {
			c.h.Notify(errMsg, "warning")
			return
		}
		providerValue, modelValue = p, m
	}
	if applyMutation(scope, settingsMutation{op: "set", path: append(append([]string{}, pairPrefix...), "provider"), value: providerValue}) {
		applyMutation(scope, settingsMutation{op: "set", path: append(append([]string{}, pairPrefix...), "model"), value: modelValue})
	}
}
