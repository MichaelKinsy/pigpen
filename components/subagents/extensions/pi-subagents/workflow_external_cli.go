package pi_subagents

import (
	"fmt"
	"strings"
)

// The part of src/runs/shared/external-cli-contract.ts that workflow receipts record and read back: the one-shot external CLI
// adapter's runner status and the bounded metadata a receipt keeps about it.

const (
	externalUnsupportedSteer             = "The one-shot stdin adapter closes input after launch and cannot accept live steer messages."
	externalUnsupportedResume            = "The one-shot stdin adapter has no durable external session identity."
	externalUnsupportedStructured        = "The generic external CLI adapter does not parse a trusted structured result."
	externalUnsupportedToolEvents        = "The generic external CLI adapter treats stdout as untrusted text, not native Pi tool events."
	externalUnsupportedSupervisor        = "The generic external CLI adapter has no trusted supervisor event transport."
	externalUnsupportedForkContext       = "Native Pi fork context is not available without an adapter-owned handoff artifact."
	externalUnsupportedExtensionBindings = "Native Pi extension bindings are never passed to external runners."
	externalPromptFileSteer              = "The one-shot prompt-file adapter closes input after launch and cannot accept live steer messages."
	externalPromptFileResume             = "The one-shot prompt-file adapter does not retain a durable external session identity."
)

// externalCapabilityKeys is the fixed capability set, in the original's order.
var externalCapabilityKeys = []string{"stop", "steer", "resume", "structuredOutput", "toolEvents", "supervisor", "forkContext", "extensionBindings"}

func externalCapabilities() *jsObject {
	return jo("stop", true, "steer", false, "resume", false, "structuredOutput", false, "toolEvents", false, "supervisor", "unsupported", "forkContext", false, "extensionBindings", false)
}

func externalUnsupportedReasons(promptFile bool) *jsObject {
	steer, resume := externalUnsupportedSteer, externalUnsupportedResume
	if promptFile {
		steer, resume = externalPromptFileSteer, externalPromptFileResume
	}
	return jo("steer", steer, "resume", resume, "structuredOutput", externalUnsupportedStructured, "toolEvents", externalUnsupportedToolEvents,
		"supervisor", externalUnsupportedSupervisor, "forkContext", externalUnsupportedForkContext, "extensionBindings", externalUnsupportedExtensionBindings)
}

// ResolveExternalCliRunnerStatus is resolveExternalCliRunnerStatus. adapter is "" for the generic adapter; machine may be nil.
func ResolveExternalCliRunnerStatus(adapter, command string, args []any, machine *jsObject) *jsObject {
	if args == nil {
		args = []any{}
	}
	cursor := adapter == "cursor-agent" || adapter == "cursor-agent-writer"
	id := adapter
	if id == "" {
		id = "external-cli"
	}
	mode, delivery := "one-shot-stdin", "stdin"
	if cursor {
		mode, delivery = "one-shot-prompt-file", "prompt-file"
	}
	out := jo("type", "external-cli", "command", command, "args", args, "promptDelivery", delivery, "adapter", jo("id", id, "version", float64(1), "executionMode", mode))
	switch adapter {
	case "codex-exec":
		out.set("safety", jo("sandbox", "read-only", "approvalPolicy", "never", "ephemeral", true))
	case "codex-exec-writer":
		out.set("safety", jo("access", "workspace-write", "sandbox", "workspace-write", "approvalPolicy", "never", "ephemeral", true))
	case "claude-code":
		out.set("safety", jo("access", "read-only", "authentication", "existing-cli-required", "permissionMode", "plan", "tools", "none", "mcp", "empty-strict", "settingSources", "user", "userSettingsTrust", "required", "sessionPersistence", false))
	case "claude-code-writer":
		out.set("safety", jo("access", "workspace-write", "authentication", "existing-cli-required", "permissionMode", "acceptEdits", "tools", "Read,Write,Edit,Glob,Grep", "mcp", "empty-strict", "settingSources", "user", "userSettingsTrust", "required", "sessionPersistence", false))
	case "cursor-agent":
		out.set("safety", jo("access", "read-only", "authentication", "cursor-api-key-or-existing-login", "mode", "ask", "sandbox", "enabled", "workspaceTrust", "existing-required", "sessionReuse", false))
	case "cursor-agent-writer":
		out.set("safety", jo("access", "workspace-write", "authentication", "cursor-api-key-or-existing-login", "mode", "print", "sandbox", "enabled", "workspaceTrust", "existing-required", "sessionReuse", false))
	}
	if machine != nil {
		out.set("machine", machine)
	}
	out.set("capabilities", externalCapabilities())
	reasons := externalUnsupportedReasons(cursor)
	out.set("unsupportedReasons", reasons)
	out.set("nonResumableReason", reasons.vals["resume"])
	return out
}

// ExternalCliReceiptMetadata is externalCliReceiptMetadata. externalProcess is the process status (stdoutPath, stderrPath,
// finalOutputPath, machine) or nil; outputReference may be "".
func ExternalCliReceiptMetadata(runner, externalProcess *jsObject, outputReference string) *jsObject {
	out := newObject()
	out.set("adapter", runner.obj("adapter").clone())
	out.set("capabilities", runner.obj("capabilities").clone())
	machine := runner.obj("machine")
	if externalProcess != nil && externalProcess.obj("machine") != nil {
		machine = externalProcess.obj("machine")
	}
	if machine != nil {
		m := machine.clone()
		if rg := machine.obj("remoteGit"); rg != nil {
			m.set("remoteGit", rg.clone())
		}
		out.set("machine", m)
	}
	if s := runner.obj("safety"); s != nil {
		out.set("safety", s.clone())
	}
	if externalProcess != nil {
		art := jo("stdoutPath", externalProcess.vals["stdoutPath"], "stderrPath", externalProcess.vals["stderrPath"])
		final, hasFinal := externalProcess.str("finalOutputPath")
		if outputReference != "" || (hasFinal && final != "") {
			if outputReference != "" {
				art.set("finalOutputPath", outputReference)
			} else {
				art.set("finalOutputPath", final)
			}
		}
		out.set("outputArtifacts", art)
	} else if outputReference != "" {
		out.set("outputArtifacts", jo("finalOutputPath", outputReference))
	}
	out.set("handoff", jo("mode", "fresh"))
	out.set("supervisor", jo("mode", "unsupported", "reason", runner.obj("unsupportedReasons").vals["supervisor"]))
	out.set("nonResumableReason", runner.vals["nonResumableReason"])
	return out
}

func recordOf(v any) (*jsObject, bool) { o, ok := v.(*jsObject); return o, ok }

func unknownFields(o *jsObject, allowed ...string) []string {
	var out []string
	for _, k := range o.order() {
		found := false
		for _, a := range allowed {
			if a == k {
				found = true
			}
		}
		if !found {
			out = append(out, k)
		}
	}
	return out
}

func nonBlankString(v any) bool { s, ok := v.(string); return ok && jsTrim(s) != "" }

func parseExternalCliMachine(value any, label string) error {
	m, ok := recordOf(value)
	if !ok {
		return fmt.Errorf("%s.machine must be an object.", label)
	}
	if u := unknownFields(m, "provider", "id", "label", "target", "session", "cwd", "remoteGit"); len(u) > 0 {
		return fmt.Errorf("%s.machine has unsupported fields: %s.", label, strings.Join(u, ", "))
	}
	if m.vals["provider"] != "herdr" || !nonBlankString(m.vals["id"]) || !nonBlankString(m.vals["target"]) || !nonBlankString(m.vals["cwd"]) {
		return fmt.Errorf("%s.machine is invalid.", label)
	}
	for _, f := range []string{"label", "session"} {
		if m.has(f) && !nonBlankString(m.vals[f]) {
			return fmt.Errorf("%s.machine.%s is invalid.", label, f)
		}
	}
	if !m.has("remoteGit") {
		return nil
	}
	rg, ok := recordOf(m.vals["remoteGit"])
	if !ok {
		return fmt.Errorf("%s.machine.remoteGit must be an object.", label)
	}
	bad := len(unknownFields(rg, "head", "branch", "dirty")) > 0
	for _, f := range []string{"head", "branch"} {
		if rg.has(f) && !nonBlankString(rg.vals[f]) {
			bad = true
		}
	}
	if _, isBool := rg.vals["dirty"].(bool); rg.has("dirty") && !isBool {
		bad = true
	}
	if bad {
		return fmt.Errorf("%s.machine.remoteGit is invalid.", label)
	}
	return nil
}

// safetyShape describes the allowed fields and exact values of one adapter's safety record.
type safetyShape struct {
	fields []string
	values [][2]any
}

func externalSafetyShape(id string, safety *jsObject) safetyShape {
	switch id {
	case "codex-exec":
		return safetyShape{[]string{"sandbox", "approvalPolicy", "ephemeral"}, [][2]any{{"sandbox", "read-only"}, {"approvalPolicy", "never"}, {"ephemeral", true}}}
	case "codex-exec-writer":
		return safetyShape{[]string{"access", "sandbox", "approvalPolicy", "ephemeral"}, [][2]any{{"access", "workspace-write"}, {"sandbox", "workspace-write"}, {"approvalPolicy", "never"}, {"ephemeral", true}}}
	case "claude-code":
		if _, has := safety.vals["authentication"]; !has || !safety.has("authentication") {
			return safetyShape{[]string{"permissionMode", "tools", "mcp", "settingSources", "sessionPersistence"}, [][2]any{{"permissionMode", "plan"}, {"tools", "none"}, {"mcp", "empty-strict"}, {"settingSources", "none"}, {"sessionPersistence", false}}}
		}
		return safetyShape{[]string{"access", "authentication", "permissionMode", "tools", "mcp", "settingSources", "userSettingsTrust", "sessionPersistence"},
			[][2]any{{"access", "read-only"}, {"authentication", "existing-cli-required"}, {"permissionMode", "plan"}, {"tools", "none"}, {"mcp", "empty-strict"}, {"settingSources", "user"}, {"userSettingsTrust", "required"}, {"sessionPersistence", false}}}
	case "claude-code-writer":
		return safetyShape{[]string{"access", "authentication", "permissionMode", "tools", "mcp", "settingSources", "userSettingsTrust", "sessionPersistence"},
			[][2]any{{"access", "workspace-write"}, {"authentication", "existing-cli-required"}, {"permissionMode", "acceptEdits"}, {"tools", "Read,Write,Edit,Glob,Grep"}, {"mcp", "empty-strict"}, {"settingSources", "user"}, {"userSettingsTrust", "required"}, {"sessionPersistence", false}}}
	case "cursor-agent", "cursor-agent-writer":
		writer := id == "cursor-agent-writer"
		access, mode := "read-only", "ask"
		if writer {
			access, mode = "workspace-write", "print"
		}
		return safetyShape{[]string{"access", "authentication", "mode", "sandbox", "workspaceTrust", "sessionReuse"},
			[][2]any{{"access", access}, {"authentication", "cursor-api-key-or-existing-login"}, {"mode", mode}, {"sandbox", "enabled"}, {"workspaceTrust", "existing-required"}, {"sessionReuse", false}}}
	case "grok-build":
		return safetyShape{[]string{"access", "authentication", "permissionMode", "tools", "deniedTools", "sandbox", "webSearch", "subagents", "config", "updates", "sessionPersistence"},
			[][2]any{{"access", "read-only"}, {"authentication", "xai-api-key-required"}, {"permissionMode", "plan"}, {"tools", "read_file,grep,list_dir"}, {"deniedTools", "run_terminal_cmd,search_replace,Agent,Bash,Edit,Write,MCPTool"}, {"sandbox", "read-only"}, {"webSearch", false}, {"subagents", false}, {"config", "temporary-home"}, {"updates", "disabled"}, {"sessionPersistence", false}}}
	}
	return safetyShape{}
}

// parseExternalCliReceiptMetadata validates the bounded adapter metadata of a receipt entry (workflow-receipt.ts:107); it
// rejects unknown fields so a receipt cannot smuggle raw output.
func parseExternalCliReceiptMetadata(value any, key, source string) error {
	if _, u := value.(undef); u {
		return nil
	}
	label := fmt.Sprintf("Invalid workflow receipt '%s': entry '%s' externalAdapter", source, key)
	md, ok := recordOf(value)
	if !ok {
		return fmt.Errorf("%s must be an object.", label)
	}
	if u := unknownFields(md, "adapter", "capabilities", "machine", "safety", "outputArtifacts", "handoff", "supervisor", "nonResumableReason"); len(u) > 0 {
		return fmt.Errorf("%s has unsupported fields: %s.", label, strings.Join(u, ", "))
	}
	adapter, ok := recordOf(md.vals["adapter"])
	if !ok {
		return fmt.Errorf("%s.adapter must be an object.", label)
	}
	if u := unknownFields(adapter, "id", "version", "executionMode"); len(u) > 0 {
		return fmt.Errorf("%s.adapter has unsupported fields: %s.", label, strings.Join(u, ", "))
	}
	id, _ := adapter.vals["id"].(string)
	switch id {
	case "external-cli", "codex-exec", "codex-exec-writer", "claude-code", "claude-code-writer", "cursor-agent", "cursor-agent-writer", "grok-build":
	default:
		return fmt.Errorf("%s.adapter is invalid.", label)
	}
	wantMode := "one-shot-stdin"
	if id == "cursor-agent" || id == "cursor-agent-writer" || id == "grok-build" {
		wantMode = "one-shot-prompt-file"
	}
	if v, _ := adapter.vals["version"].(float64); v != 1 || adapter.vals["executionMode"] != wantMode {
		return fmt.Errorf("%s.adapter is invalid.", label)
	}
	caps, ok := recordOf(md.vals["capabilities"])
	if !ok {
		return fmt.Errorf("%s.capabilities must be an object.", label)
	}
	var unknownCaps []string
	for _, k := range caps.order() {
		known := false
		for _, c := range externalCapabilityKeys {
			known = known || c == k
		}
		if !known {
			unknownCaps = append(unknownCaps, k)
		}
	}
	if len(unknownCaps) > 0 {
		return fmt.Errorf("%s.capabilities has unsupported fields: %s.", label, strings.Join(unknownCaps, ", "))
	}
	expected := externalCapabilities()
	for _, c := range externalCapabilityKeys {
		if caps.vals[c] != expected.vals[c] {
			return fmt.Errorf("%s.capabilities.%s is invalid.", label, c)
		}
	}
	if md.has("machine") {
		if err := parseExternalCliMachine(md.vals["machine"], label); err != nil {
			return err
		}
	}
	safetyV := md.vals["safety"]
	if id == "external-cli" {
		if md.has("safety") {
			return fmt.Errorf("%s.safety is invalid for the generic adapter.", label)
		}
	} else {
		safety, ok := recordOf(safetyV)
		if !ok {
			return fmt.Errorf("%s.safety is missing.", label)
		}
		shape := externalSafetyShape(id, safety)
		if u := unknownFields(safety, shape.fields...); len(u) > 0 {
			return fmt.Errorf("%s.safety has unsupported fields: %s.", label, strings.Join(u, ", "))
		}
		for _, kv := range shape.values {
			if safety.vals[kv[0].(string)] != kv[1] {
				return fmt.Errorf("%s.safety is invalid.", label)
			}
		}
	}
	handoff, ok := recordOf(md.vals["handoff"])
	if !ok {
		return fmt.Errorf("%s.handoff must be an object.", label)
	}
	if u := unknownFields(handoff, "mode"); len(u) > 0 {
		return fmt.Errorf("%s.handoff has unsupported fields: %s.", label, strings.Join(u, ", "))
	}
	if handoff.vals["mode"] != "fresh" {
		return fmt.Errorf("%s.handoff is invalid.", label)
	}
	sup, ok := recordOf(md.vals["supervisor"])
	if !ok {
		return fmt.Errorf("%s.supervisor must be an object.", label)
	}
	if u := unknownFields(sup, "mode", "reason"); len(u) > 0 {
		return fmt.Errorf("%s.supervisor has unsupported fields: %s.", label, strings.Join(u, ", "))
	}
	if sup.vals["mode"] != "unsupported" || !nonBlankString(sup.vals["reason"]) {
		return fmt.Errorf("%s.supervisor is invalid.", label)
	}
	if !nonBlankString(md.vals["nonResumableReason"]) {
		return fmt.Errorf("%s.nonResumableReason is missing.", label)
	}
	if md.has("outputArtifacts") {
		art, ok := recordOf(md.vals["outputArtifacts"])
		if !ok {
			return fmt.Errorf("%s.outputArtifacts must be an object.", label)
		}
		if u := unknownFields(art, "stdoutPath", "stderrPath", "finalOutputPath"); len(u) > 0 {
			return fmt.Errorf("%s.outputArtifacts has unsupported fields: %s.", label, strings.Join(u, ", "))
		}
		for _, f := range []string{"stdoutPath", "stderrPath", "finalOutputPath"} {
			if art.has(f) && !nonBlankString(art.vals[f]) {
				return fmt.Errorf("%s.outputArtifacts.%s must be a non-empty string.", label, f)
			}
		}
	}
	return nil
}
