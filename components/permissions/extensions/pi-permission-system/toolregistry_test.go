package pi_permission_system

import "testing"

func TestToolRegistryTwins(t *testing.T) {
	const f = "exposure/tool-registry"
	S := func(s ...string) []string { return s }
	val := func(json string) any {
		v, err := parseJSON([]byte(json))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	list := func(json string) []any { return val(json).([]any) }
	name := func(v any) any {
		got, ok := GetToolNameFromValue(v)
		if !ok {
			return nil
		}
		return got
	}

	tw(t, f, "returns string value directly", func(t *testing.T) { eq(t, name("read"), "read", "name") })
	tw(t, f, "returns null for empty string", func(t *testing.T) { eq(t, name(""), nil, "name") })
	tw(t, f, "returns null for whitespace-only string", func(t *testing.T) { eq(t, name("   "), nil, "name") })
	tw(t, f, "returns null for null", func(t *testing.T) { eq(t, name(nil), nil, "name") })
	tw(t, f, "returns null for undefined", func(t *testing.T) { eq(t, name(undef{}), nil, "name") })
	tw(t, f, "extracts toolName from object", func(t *testing.T) { eq(t, name(val(`{"toolName":"write"}`)), "write", "name") })
	tw(t, f, "extracts name from object", func(t *testing.T) { eq(t, name(val(`{"name":"edit"}`)), "edit", "name") })
	tw(t, f, "extracts tool from object", func(t *testing.T) { eq(t, name(val(`{"tool":"bash"}`)), "bash", "name") })
	tw(t, f, "prefers toolName over name over tool", func(t *testing.T) {
		eq(t, name(val(`{"toolName":"first","name":"second","tool":"third"}`)), "first", "name")
	})
	tw(t, f, "falls back to name when toolName is empty", func(t *testing.T) { eq(t, name(val(`{"toolName":"","name":"edit"}`)), "edit", "name") })
	tw(t, f, "returns null for object with no recognised keys", func(t *testing.T) { eq(t, name(val(`{"unknown":"read"}`)), nil, "name") })
	tw(t, f, "returns null for number input", func(t *testing.T) { eq(t, name(42.0), nil, "name") })

	tw(t, f, "returns the tool's guideline bullets", func(t *testing.T) {
		got := GetToolPromptGuidelinesFromValue(val(`{"name":"read","promptGuidelines":["Use read to examine files instead of cat or sed.","Read whole files."]}`))
		eq(t, got, S("Use read to examine files instead of cat or sed.", "Read whole files."), "bullets")
	})
	tw(t, f, "returns an empty array when the tool declares none", func(t *testing.T) {
		eq(t, GetToolPromptGuidelinesFromValue(val(`{"name":"read"}`)), []string{}, "bullets")
	})
	tw(t, f, "drops blank and non-string entries", func(t *testing.T) {
		got := GetToolPromptGuidelinesFromValue(val(`{"name":"bash","promptGuidelines":["Use bash.","","   ",42,null,"Be careful."]}`))
		eq(t, got, S("Use bash.", "Be careful."), "bullets")
	})
	tw(t, f, "trims surrounding whitespace, mirroring Pi's own normalization", func(t *testing.T) {
		got := GetToolPromptGuidelinesFromValue(val(`{"name":"edit","promptGuidelines":["  Use edit for precise changes.  "]}`))
		eq(t, got, S("Use edit for precise changes."), "bullets")
	})
	tw(t, f, "returns an empty array when promptGuidelines is not an array", func(t *testing.T) {
		eq(t, GetToolPromptGuidelinesFromValue(val(`{"name":"read","promptGuidelines":"Use read."}`)), []string{}, "bullets")
	})
	tw(t, f, "returns an empty array for a non-object value", func(t *testing.T) {
		eq(t, GetToolPromptGuidelinesFromValue("read"), []string{}, "string")
		eq(t, GetToolPromptGuidelinesFromValue(nil), []string{}, "null")
		eq(t, GetToolPromptGuidelinesFromValue(undef{}), []string{}, "undefined")
	})

	tw(t, f, "reads names and guidelines in one pass", func(t *testing.T) {
		registered := ReadRegisteredTools(list(`[{"name":"read","promptGuidelines":["Use read."]},{"name":"bash","promptGuidelines":["Use bash.","Be careful."]}]`))
		eq(t, registered.Names, S("read", "bash"), "names")
		eq(t, registered.GuidelinesByTool["read"], S("Use read."), "read")
		eq(t, registered.GuidelinesByTool["bash"], S("Use bash.", "Be careful."), "bash")
	})
	tw(t, f, "records no guidelines entry for a tool that declares none", func(t *testing.T) {
		registered := ReadRegisteredTools(list(`[{"name":"read"}]`))
		eq(t, registered.Names, S("read"), "names")
		_, has := registered.GuidelinesByTool["read"]
		eq(t, has, false, "has")
	})
	tw(t, f, "reads bare tool-name strings, as getActive() returns", func(t *testing.T) {
		registered := ReadRegisteredTools(list(`["read","bash"]`))
		eq(t, registered.Names, S("read", "bash"), "names")
		eq(t, len(registered.GuidelinesByTool), 0, "size")
	})
	tw(t, f, "skips entries carrying no resolvable tool name", func(t *testing.T) {
		eq(t, ReadRegisteredTools(list(`[{"name":"read"},{"unknown":"x"},42,null]`)).Names, S("read"), "names")
	})

	check := func(requested string, tools string, aliases map[string]string) ToolRegistrationCheckResult {
		return CheckRequestedToolRegistration(&requested, list(tools), aliases)
	}
	tw(t, f, "returns missing-tool-name for null requested name", func(t *testing.T) {
		eq(t, CheckRequestedToolRegistration(nil, nil, nil).Status, "missing-tool-name", "status")
	})
	tw(t, f, "returns missing-tool-name for whitespace-only requested name", func(t *testing.T) {
		eq(t, check("   ", `[]`, nil).Status, "missing-tool-name", "status")
	})
	tw(t, f, "returns registered when tool name matches a string entry", func(t *testing.T) {
		result := check("read", `["read","write"]`, nil)
		eq(t, result.Status, "registered", "status")
		eq(t, result.RequestedToolName, "read", "requested")
		eq(t, result.NormalizedToolName, "read", "normalized")
	})
	tw(t, f, "returns registered when tool name matches an object entry by name", func(t *testing.T) {
		eq(t, check("edit", `[{"name":"edit"}]`, nil).Status, "registered", "status")
	})
	tw(t, f, "returns registered when tool name matches an object entry by toolName", func(t *testing.T) {
		eq(t, check("bash", `[{"toolName":"bash"}]`, nil).Status, "registered", "status")
	})
	tw(t, f, "returns unregistered when tool is not in the list", func(t *testing.T) {
		result := check("ghost", `["read","write"]`, nil)
		eq(t, result.Status, "unregistered", "status")
		eq(t, result.RequestedToolName, "ghost", "requested")
		eq(t, inStrings(result.AvailableToolNames, "read") && inStrings(result.AvailableToolNames, "write"), true, "available")
	})
	tw(t, f, "available tool names are sorted alphabetically", func(t *testing.T) {
		result := check("ghost", `["write","read","edit"]`, nil)
		eq(t, result.AvailableToolNames, S("edit", "read", "write"), "available")
	})
	tw(t, f, "resolves alias: requested alias maps to registered canonical name", func(t *testing.T) {
		result := check("Execute", `["bash"]`, map[string]string{"Execute": "bash"})
		eq(t, result.Status, "registered", "status")
		eq(t, result.NormalizedToolName, "bash", "normalized")
	})
	tw(t, f, "resolves alias: registered canonical is found via reverse alias lookup", func(t *testing.T) {
		// "bash" is registered; the alias maps "Execute" to "bash"; requesting "bash" directly should still resolve.
		eq(t, check("bash", `["bash"]`, map[string]string{"Execute": "bash"}).Status, "registered", "status")
	})
	tw(t, f, "returns unregistered with empty availableToolNames for empty tool list", func(t *testing.T) {
		result := check("read", `[]`, nil)
		eq(t, result.Status, "unregistered", "status")
		eq(t, result.AvailableToolNames, []string{}, "available")
	})
	tw(t, f, "skips tool list entries that yield no name", func(t *testing.T) {
		eq(t, check("read", `[null,{},{"unrelated":"x"},"read"]`, nil).Status, "registered", "status")
	})

	// Moved from permission-system.test.ts catch-all (#342)
	tw(t, f, "Tool registry resolves event tool names from string and object payloads", func(t *testing.T) {
		eq(t, name("  read  "), "read", "string")
		eq(t, name(val(`{"toolName":"write"}`)), "write", "toolName")
		eq(t, name(val(`{"name":"find"}`)), "find", "name")
		eq(t, name(val(`{"tool":"grep"}`)), "grep", "tool")
		eq(t, name(val(`{}`)), nil, "empty")
	})
	tw(t, f, "Tool registry blocks unregistered tools and handles aliases", func(t *testing.T) {
		const registered = `[{"toolName":"mcp"},{"toolName":"read"},{"toolName":"bash"}]`
		unknown := check("third_party_tool", registered, nil)
		eq(t, unknown.Status, "unregistered", "unknown")
		eq(t, unknown.AvailableToolNames, S("bash", "mcp", "read"), "available")
		eq(t, check("legacy_read", registered, map[string]string{"legacy_read": "read"}).Status, "registered", "alias")
		eq(t, check("   ", registered, nil).Status, "missing-tool-name", "missing")
	})
}
