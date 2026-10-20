package pi_permission_system

import (
	"strings"
	"testing"
)

func TestDialogKeysTwins(t *testing.T) {
	const f = "config/dialog-keys"
	resolve := func(o DialogKeyOverrides) DialogKeyResolution { return resolveDialogKeys(o) }
	defaults := defaultDialogKeys()
	with := func(a PromptAction, v string) DialogKeyBindings {
		k := defaultDialogKeys()
		k[a] = v
		return k
	}
	tw(t, f, "admits every lowercase letter and digit", func(t *testing.T) {
		for _, c := range "abcdefghijklmnopqrstuvwxyz0123456789" {
			eq(t, strings.ContainsRune(bindableDialogKeyCharacters, c), true, string(c))
		}
	})
	tw(t, f, "admits exactly pi-tui's symbol keys, minus the one its matcher cannot see", func(t *testing.T) {
		r := loadConfigOracle(t)
		if len(r.PiTui.SymbolKeys) <= 20 {
			t.Fatal("the derivation is vacuous") // an empty list would make the claim vacuous
		}
		var admitted, want []string
		for _, s := range r.PiTui.SymbolKeys {
			if isBindableDialogKey(s) {
				admitted = append(admitted, s)
			}
			if s != "+" {
				want = append(want, s)
			}
		}
		eq(t, admitted, want, "symbols (pi-tui's Key, recorded from the dependency)")
	})
	tw(t, f, "excludes `+` because pi-tui reads it as a modifier separator", func(t *testing.T) {
		r := loadConfigOracle(t)
		eq(t, r.PiTui.Matches["+"], false, "matchesKey('+', '+') in pi-tui")
		eq(t, isBindableDialogKey("+"), false, "bindable")
	})
	tw(t, f, "holds only characters pi-tui's matcher accepts", func(t *testing.T) {
		r := loadConfigOracle(t)
		for _, c := range bindableDialogKeyCharacters {
			ok, recorded := r.PiTui.Matches[string(c)]
			eq(t, recorded, true, "recorded "+string(c))
			eq(t, ok, true, "matchesKey("+string(c)+")")
		}
	})
	// the upstream it.each cases (a %s title) run as subtests
	t.Run("isBindableDialogKey", func(t *testing.T) {
		for _, v := range []string{"1", "q", "/"} {
			eq(t, isBindableDialogKey(v), true, "accepts "+v)
		}
		for _, v := range []string{"", "yy", "A", "é", "+", "escape", "ctrl+g", " "} {
			eq(t, isBindableDialogKey(v), false, "rejects "+v)
		}
	})
	tw(t, f, "returns the shipped bindings with no issues", func(t *testing.T) {
		eq(t, resolve(nil), DialogKeyResolution{Keys: defaults, Issues: []string{}}, "resolution")
	})
	tw(t, f, "treats an empty map the same way", func(t *testing.T) {
		eq(t, resolve(DialogKeyOverrides{}), DialogKeyResolution{Keys: defaults, Issues: []string{}}, "resolution")
	})
	tw(t, f, "applies every configured decision", func(t *testing.T) {
		eq(t, resolve(DialogKeyOverrides{ActionApprove: "1", ActionApproveSession: "2", ActionApproveSessionBoth: "3", ActionDeny: "4", ActionDenyWithReason: "5"}),
			DialogKeyResolution{Keys: DialogKeyBindings{ActionApprove: "1", ActionApproveSession: "2", ActionApproveSessionBoth: "3", ActionDeny: "4", ActionDenyWithReason: "5"}, Issues: []string{}}, "resolution")
	})
	tw(t, f, "leaves an unnamed decision on its default", func(t *testing.T) {
		eq(t, resolve(DialogKeyOverrides{ActionApprove: "1"}), DialogKeyResolution{Keys: with(ActionApprove, "1"), Issues: []string{}}, "resolution")
	})
	tw(t, f, "accepts a symbol its matcher can see", func(t *testing.T) { eq(t, resolve(DialogKeyOverrides{ActionDeny: "/"}).Keys[ActionDeny], "/", "deny") })
	tw(t, f, "applies a swap, since neither decision keeps the other's key", func(t *testing.T) {
		k := defaultDialogKeys()
		k[ActionApprove], k[ActionDeny] = "n", "y"
		eq(t, resolve(DialogKeyOverrides{ActionApprove: "n", ActionDeny: "y"}), DialogKeyResolution{Keys: k, Issues: []string{}}, "resolution")
	})
	t.Run("with an unusable binding", func(t *testing.T) {
		for _, v := range []string{"", "yy", "A", "é", "+", "escape"} {
			r := resolve(DialogKeyOverrides{ActionApprove: v})
			eq(t, r.Keys, defaults, "keeps the default")
			eq(t, r.Issues, []string{`permissionDialogKeys.approve: "` + v + `" is not a bindable key. Use one lowercase letter, digit, or symbol; keeping the default "y".`}, "issues")
		}
	})
	t.Run("with a binding the dialog reserves", func(t *testing.T) {
		for _, v := range []string{"j", "k"} {
			r := resolve(DialogKeyOverrides{ActionDeny: v})
			eq(t, r.Keys, defaults, "keeps the default")
			eq(t, r.Issues, []string{`permissionDialogKeys.deny: "` + v + `" is reserved for moving the dialog's highlight; keeping the default "n".`}, "issues")
		}
	})
	tw(t, f, "drops both overrides when two decisions claim one character", func(t *testing.T) {
		r := resolve(DialogKeyOverrides{ActionApprove: "1", ActionDeny: "1"})
		eq(t, r.Keys, defaults, "keys")
		eq(t, r.Issues, []string{`permissionDialogKeys.approve: "1" is already bound to another decision; keeping the default "y".`,
			`permissionDialogKeys.deny: "1" is already bound to another decision; keeping the default "n".`}, "issues")
	})
	tw(t, f, "drops an override that claims another decision's default", func(t *testing.T) {
		r := resolve(DialogKeyOverrides{ActionApprove: "n"})
		eq(t, r.Keys, defaults, "keys")
		eq(t, r.Issues, []string{`permissionDialogKeys.approve: "n" is already bound to another decision; keeping the default "y".`}, "issues")
	})
	tw(t, f, "re-checks after a restored default collides in its turn", func(t *testing.T) {
		// `approve: "b"` collides with the untouched `approveSessionBoth` default; dropping it restores `approve: "y"`, which
		// then collides with the surviving `deny: "y"`. One pass would leave that duplicate bound.
		r := resolve(DialogKeyOverrides{ActionApprove: "b", ActionDeny: "y"})
		eq(t, r.Keys, defaults, "keys")
		eq(t, r.Issues, []string{`permissionDialogKeys.approve: "b" is already bound to another decision; keeping the default "y".`,
			`permissionDialogKeys.deny: "y" is already bound to another decision; keeping the default "n".`}, "issues")
	})
	tw(t, f, "keeps an override whose collision was resolved by a sibling move", func(t *testing.T) {
		// `approve` takes deny's letter, but `deny` moves off it in the same map
		eq(t, resolve(DialogKeyOverrides{ActionApprove: "n", ActionDeny: "4"}).Issues, []string{}, "issues")
	})
}

// valid reports whether the schema accepts the JSON text; issues are the schema's messages for it otherwise.
func valid(t *testing.T, text string) bool {
	t.Helper()
	v, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return len(validateUnifiedConfig(v)) == 0
}

func issuesOf(t *testing.T, text string) []string {
	t.Helper()
	v, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return validateUnifiedConfig(v)
}

func TestConfigSchemaTwins(t *testing.T) {
	const f = "config/config-schema"
	tw(t, f, "accepts a full config with runtime knobs and flat permission", func(t *testing.T) {
		eq(t, valid(t, `{"debugLog":true,"permissionReviewLog":false,"yoloMode":true,"toolInputPreviewMaxLength":1000,"toolTextSummaryMaxLength":120,`+
			`"piInfrastructureReadPaths":["/extra/path"],"permissionDialogKeys":{"approve":"1","deny":"4"},"permission":{"*":"ask","read":"allow",`+
			`"bash":{"*":"ask","git status":"allow","npm *":{"action":"deny","reason":"Use pnpm instead"}}}}`), true, "valid")
	})
	tw(t, f, "accepts an empty config", func(t *testing.T) { eq(t, valid(t, `{}`), true, "valid") })
	tw(t, f, "accepts a $schema field", func(t *testing.T) { eq(t, valid(t, `{"$schema":"https://example.com/s.json"}`), true, "valid") })
	tw(t, f, "rejects an unknown top-level key", func(t *testing.T) { eq(t, valid(t, `{"unknownField":"x"}`), false, "valid") })
	tw(t, f, "rejects a non-boolean debugLog", func(t *testing.T) {
		is := issuesOf(t, `{"debugLog":"yes"}`)
		eq(t, len(is) > 0, true, "rejected")
		eq(t, strings.HasPrefix(is[0], "Invalid config value at 'debugLog': "), true, "path")
	})
	tw(t, f, "rejects a non-integer toolInputPreviewMaxLength", func(t *testing.T) { eq(t, valid(t, `{"toolInputPreviewMaxLength":1.5}`), false, "valid") })
	tw(t, f, "rejects a zero toolInputPreviewMaxLength", func(t *testing.T) { eq(t, valid(t, `{"toolInputPreviewMaxLength":0}`), false, "valid") })
	tw(t, f, "rejects a non-string entry in piInfrastructureReadPaths", func(t *testing.T) { eq(t, valid(t, `{"piInfrastructureReadPaths":["a",1]}`), false, "valid") })
	tw(t, f, "rejects a string permission value", func(t *testing.T) { eq(t, valid(t, `{"permission":"allow"}`), false, "valid") })
	tw(t, f, "rejects an invalid PermissionState inside a permission map", func(t *testing.T) { eq(t, valid(t, `{"permission":{"write":"invalid"}}`), false, "valid") })
	tw(t, f, "rejects a deny-with-reason with a non-string reason", func(t *testing.T) {
		eq(t, valid(t, `{"permission":{"bash":{"npm *":{"action":"deny","reason":42}}}}`), false, "valid")
	})
	tw(t, f, "rejects an empty surface key", func(t *testing.T) { eq(t, valid(t, `{"permission":{"":"allow"}}`), false, "valid") })

	t.Run("directional surface keys", func(t *testing.T) {
		for _, s := range []string{"path_read", "path_write", "external_directory_read", "external_directory_write"} {
			eq(t, valid(t, `{"permission":{"`+s+`":{"~/dev/*":"allow"}}}`), true, "accepts "+s)
		}
		for _, s := range []string{"path_wrote", "path_reed", "path_delete", "external_directory_reed", "external_directory_"} {
			is := issuesOf(t, `{"permission":{"`+s+`":{"*":"deny"}}}`)
			eq(t, len(is) > 0 && strings.Contains(is[0], s), true, "rejects the misspelled directional key "+s)
		}
	})
	tw(t, f, "accepts exactly the directional surfaces the schema documents", func(t *testing.T) {
		props := buildPermissionsJSONSchema().obj("properties").obj("permission").obj("properties")
		var documented []string
		for _, k := range props.order() {
			if strings.HasPrefix(k, "path_") || strings.HasPrefix(k, "external_directory_") {
				documented = append(documented, k)
			}
		}
		eq(t, sortedCopy(documented), []string{"external_directory_read", "external_directory_write", "path_read", "path_write"}, "documented")
		for _, k := range documented {
			eq(t, valid(t, `{"permission":{"`+k+`":"allow"}}`), true, k)
		}
	})
	tw(t, f, "enumerates exactly those spellings when rejecting a misspelling", func(t *testing.T) {
		is := issuesOf(t, `{"permission":{"path_wrote":{"*":"deny"}}}`)
		eq(t, is, []string{`Invalid config value at 'permission.path_wrote': Unknown directional surface key "path_wrote". The legal spellings are path_read, path_write, external_directory_read, external_directory_write.`}, "issues")
	})
	tw(t, f, "still accepts an arbitrary tool-name surface, and keeps its rules", func(t *testing.T) {
		c := cfg(t, `{"permission":{"my_extension_tool":{"*":"ask"},"ffgrep":"allow"}}`)
		// asserting the parsed data, not just success: a schema without the catchall would strip an unmatched key
		eq(t, permJSON(c.Permission), `{"my_extension_tool":{"*":"ask"},"ffgrep":"allow"}`, "permission")
	})
	tw(t, f, "leaves a tool named like a family member of another family alone", func(t *testing.T) {
		eq(t, valid(t, `{"permission":{"my_tool_read":"allow"}}`), true, "valid")
	})
	t.Run("the fallback and bare-family surface keys", func(t *testing.T) {
		for _, s := range []string{"*", "path", "external_directory", "bash", "mcp", "skill"} {
			eq(t, valid(t, `{"permission":{"`+s+`":"allow"}}`), true, "accepts "+s+" as a string shorthand")
			eq(t, valid(t, `{"permission":{"`+s+`":{"*":"ask","~/dev/*":"allow"}}}`), true, "accepts "+s+" as a pattern map")
			eq(t, valid(t, `{"permission":{"`+s+`":"maybe"}}`), false, "rejects an invalid action on "+s)
		}
	})
	tw(t, f, "accepts a shellTools map with a full alias", func(t *testing.T) {
		eq(t, valid(t, `{"shellTools":{"exec_command":{"commandArgument":"cmd","workdirArgument":"workdir"}}}`), true, "valid")
	})
	tw(t, f, "accepts an alias with only commandArgument", func(t *testing.T) {
		eq(t, valid(t, `{"shellTools":{"exec_command":{"commandArgument":"cmd"}}}`), true, "valid")
	})
	tw(t, f, "rejects an alias missing commandArgument", func(t *testing.T) {
		eq(t, valid(t, `{"shellTools":{"exec_command":{"workdirArgument":"workdir"}}}`), false, "valid")
	})
	tw(t, f, "rejects an unknown field inside an alias", func(t *testing.T) {
		eq(t, valid(t, `{"shellTools":{"exec_command":{"commandArgument":"cmd","extra":"x"}}}`), false, "valid")
	})
	tw(t, f, "rejects a non-string commandArgument", func(t *testing.T) {
		eq(t, valid(t, `{"shellTools":{"exec_command":{"commandArgument":42}}}`), false, "valid")
	})
	tw(t, f, "rejects an empty-string commandArgument", func(t *testing.T) {
		eq(t, valid(t, `{"shellTools":{"exec_command":{"commandArgument":""}}}`), false, "valid")
	})
	tw(t, f, "accepts a full map of decisions", func(t *testing.T) {
		eq(t, valid(t, `{"permissionDialogKeys":{"approve":"1","approveSession":"2","approveSessionBoth":"3","deny":"4","denyWithReason":"5"}}`), true, "valid")
	})
	tw(t, f, "accepts a map naming only some decisions", func(t *testing.T) { eq(t, valid(t, `{"permissionDialogKeys":{"deny":"4"}}`), true, "valid") })
	tw(t, f, "rejects an unknown decision name", func(t *testing.T) { eq(t, valid(t, `{"permissionDialogKeys":{"approveAll":"1"}}`), false, "valid") })
	tw(t, f, "rejects a non-string binding", func(t *testing.T) { eq(t, valid(t, `{"permissionDialogKeys":{"approve":1}}`), false, "valid") })
	tw(t, f, "accepts a binding the resolver will refuse, leaving that to the resolver", func(t *testing.T) {
		eq(t, valid(t, `{"permissionDialogKeys":{"deny":"j"}}`), true, "valid")
	})
	tw(t, f, "accepts every channel", func(t *testing.T) { eq(t, valid(t, `{"promptNotifications":["bell","osc9","osc777"]}`), true, "valid") })
	tw(t, f, "accepts an empty list, which turns notifications off", func(t *testing.T) { eq(t, valid(t, `{"promptNotifications":[]}`), true, "valid") })
	tw(t, f, "rejects an unknown channel, naming the entry", func(t *testing.T) {
		is := issuesOf(t, `{"promptNotifications":["bell","beep"]}`)
		eq(t, len(is), 1, "one issue")
		eq(t, strings.HasPrefix(is[0], "Invalid config value at 'promptNotifications.1': "), true, "path")
	})
	tskip(t, f, "PermissionState is equivalent", "a compile-time expectTypeOf check that the PermissionState union inferred from the zod schema equals the hand-written type; the port has one Go definition of it, so there is nothing to compare")
	tskip(t, f, "DenyWithReason is equivalent", "a compile-time expectTypeOf check that the DenyWithReason object inferred from the zod schema equals the hand-written type; the port has one Go definition of it, so there is nothing to compare")
	tskip(t, f, "PatternValue is equivalent", "a compile-time expectTypeOf check that the PatternValue union of a state and a deny object inferred from the zod schema equals the hand-written type; the port has one Go definition of it, so there is nothing to compare")
	tskip(t, f, "FlatPermissionConfig is equivalent", "a compile-time expectTypeOf check that the FlatPermissionConfig record inferred from the zod schema equals the hand-written type; the port has one Go definition of it, so there is nothing to compare")

	schema := buildPermissionsJSONSchema()
	props := func() *jsObject { return schema.obj("properties") }
	perm := func() *jsObject { return props().obj("permission") }
	tw(t, f, "targets Draft 2020-12", func(t *testing.T) {
		eq(t, schema.vals["$schema"], "https://json-schema.org/draft/2020-12/schema", "$schema")
	})
	tw(t, f, "sets the root $id to the monorepo raw URL", func(t *testing.T) {
		eq(t, schema.vals["$id"], permissionsSchemaURL, "$id")
		eq(t, strings.Contains(permissionsSchemaURL, "gotgenes/pi-packages"), true, "repo")
	})
	tw(t, f, "forbids additional top-level properties", func(t *testing.T) { eq(t, schema.vals["additionalProperties"], false, "additionalProperties") })
	tw(t, f, "extracts the shared sub-schemas into $defs", func(t *testing.T) {
		eq(t, sortedCopy(schema.obj("$defs").order()), []string{"denyWithReason", "permissionMap", "permissionState"}, "$defs")
	})
	tw(t, f, "preserves markdownDescription for editor hovers", func(t *testing.T) {
		_, root := schema.vals["markdownDescription"].(string)
		_, yolo := props().obj("yoloMode").vals["markdownDescription"].(string)
		eq(t, root && yolo, true, "markdownDescription")
	})
	tw(t, f, "preserves the permission examples", func(t *testing.T) {
		_, ok := perm().vals["examples"].([]any)
		eq(t, ok, true, "examples")
	})
	tw(t, f, "names every well-known surface as a documented property", func(t *testing.T) {
		p := perm().obj("properties")
		eq(t, sortedCopy(p.order()), []string{"*", "bash", "external_directory", "external_directory_read", "external_directory_write", "mcp", "path", "path_read", "path_write", "skill"}, "properties")
		for _, k := range p.order() {
			_, d := p.obj(k).vals["description"].(string)
			_, m := p.obj(k).vals["markdownDescription"].(string)
			eq(t, d && m, true, "documented "+k)
		}
	})
	tw(t, f, "keeps each surface's documentation inside the per-property budget", func(t *testing.T) {
		p := perm().obj("properties")
		for _, k := range p.order() {
			if n := utf16Len(p.obj(k).vals["markdownDescription"].(string)); n > 800 {
				t.Errorf("%s: markdownDescription is %d long", k, n)
			}
		}
	})
	tw(t, f, "keeps the permission object's own description a summary", func(t *testing.T) {
		eq(t, utf16Len(perm().vals["markdownDescription"].(string)) < 1200, true, "length")
	})
	tw(t, f, "keeps arbitrary tool-name surfaces validating alongside them", func(t *testing.T) {
		eq(t, marshalJSON(perm().vals["additionalProperties"], ""), `{"anyOf":[{"$ref":"#/$defs/permissionState"},{"$ref":"#/$defs/permissionMap"}]}`, "additionalProperties")
	})
	tw(t, f, "validates against unifiedConfigSchema", func(t *testing.T) {
		b := readTestdataFile(t, "testdata/config.example.json")
		sameAsVendored(t, "testdata/config.example.json", "config/config.example.json")
		example, err := parseJSON(b)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, validateUnifiedConfig(example), []string(nil), "issues")
	})
	tw(t, f, "equals the generated schema (run `pnpm run gen:schema` if this fails)", func(t *testing.T) {
		// the committed file is embedded; it is what the original's generator writes (TestGeneratedSchemaMatchesTheOriginal)
		sameAsVendored(t, "schemas/permissions.schema.json", "schemas/permissions.schema.json")
		TestGeneratedSchemaMatchesTheOriginal(t)
	})
}
