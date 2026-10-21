#!/usr/bin/env python3
"""Generates port/mutations-policy.json: one deliberate defect per behavior of the policy and session layers (synthesize.go,
permissionmanager.go, permissionresolver.go, permissiongate.go, session*.go and the restrictiveness fold of rule.go). Each `find` must
occur exactly once in its file (checked here). Run with
  pigeq mutate --unit-only --go extensions/pi-permission-system --mutations port/mutations-policy.json --sdk-dir $PIG_SDK_DIR --jobs 4
from components/permissions."""
import json, os

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "pi-permission-system")
M = []

def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})

SY = "synthesize.go"
m("syn-default-layer", SY, 'Layer: "default", Origin: origin', 'Layer: "builtin", Origin: origin')
m("syn-default-origin", SY, 'origin = "builtin"', 'origin = "global"')
m("syn-default-empty-origin", SY, 'if origin == "" {', 'if origin != "" {')
m("syn-baseline-surface", SY, 'r.Surface == "mcp" && r.Action == "allow"', 'r.Surface == "mcp" || r.Action == "allow"')
m("syn-baseline-action", SY, 'r.Surface == "mcp" && r.Action == "allow"', 'r.Surface == "mcp" && r.Action != "deny"')
m("syn-baseline-targets", SY, '"mcp_describe", "mcp_connect"}', '"mcp_describe"}')
m("syn-baseline-target-first", SY, '{"mcp_status", "mcp_list",', '{"mcp_list",')
m("syn-baseline-layer", SY, 'Action: "allow", Layer: "baseline", Origin: "baseline"', 'Action: "allow", Layer: "config", Origin: "baseline"')
m("syn-baseline-origin", SY, 'Layer: "baseline", Origin: "baseline"', 'Layer: "baseline", Origin: "builtin"')
m("syn-baseline-empty", SY, "if !hasAnyMcpAllow {\n\t\treturn Ruleset{}\n\t}", "if !hasAnyMcpAllow {\n\t\treturn Ruleset{{Surface: \"mcp\", Pattern: \"mcp_status\", Action: \"allow\", Layer: \"baseline\", Origin: \"baseline\"}}\n\t}")
m("syn-compose-order", SY, "out = append(out, defaults...)\n\tout = append(out, baseline...)", "out = append(out, baseline...)\n\tout = append(out, defaults...)")
m("syn-compose-config-first", SY, "return append(out, config...)", "return append(config, out...)")

PM = "permissionmanager.go"
m("pm-fallback-allow", PM, 'const defaultUniversalFallback = "ask"', 'const defaultUniversalFallback = "allow"')
m("pm-fallback-deny", PM, 'const defaultUniversalFallback = "ask"', 'const defaultUniversalFallback = "deny"')
m("pm-universal-ignored", PM, 'state, ok := isPermissionState(merged.Permission.vals["*"]); ok {', 'state, ok := isPermissionState(merged.Permission.vals["*"]); ok && false {')
m("pm-universal-origin", PM, 'universalFallbackOrigin := "builtin"', 'universalFallbackOrigin := "global"')
m("pm-universal-origin-lookup", PM, 'o, ok := merged.Origins["*"]["*"]; ok {', 'o, ok := merged.Origins["*"]["*"]; ok && false {')
m("pm-scope-project-dropped", PM, '\t\t{Name: "project", Permission: projectConfig.Permission},\n', '')
m("pm-scope-agent-dropped", PM, '\t\t{Name: "agent", Permission: agentConfig.Permission},\n', '')
m("pm-scope-project-agent-dropped", PM, '\t\t{Name: "project-agent", Permission: projectAgentConfig.Permission},\n', '')
m("pm-scope-global-dropped", PM, '{Name: "global", Permission: globalConfig.Permission}', '{Name: "global", Permission: func() *jsObject { _ = globalConfig; return nil }()}')
m("pm-scope-order", PM, '\t\t{Name: "project", Permission: projectConfig.Permission},\n\t\t{Name: "agent", Permission: agentConfig.Permission},\n',
  '\t\t{Name: "agent", Permission: agentConfig.Permission},\n\t\t{Name: "project", Permission: projectConfig.Permission},\n')
m("pm-scope-agent-order", PM, '\t\t{Name: "agent", Permission: agentConfig.Permission},\n\t\t{Name: "project-agent", Permission: projectAgentConfig.Permission},\n',
  '\t\t{Name: "project-agent", Permission: projectAgentConfig.Permission},\n\t\t{Name: "agent", Permission: agentConfig.Permission},\n')
m("pm-scope-name-project", PM, '{Name: "project", Permission: projectConfig.Permission}', '{Name: "global", Permission: projectConfig.Permission}')
m("pm-scope-name-agent", PM, '{Name: "agent", Permission: agentConfig.Permission}', '{Name: "project", Permission: agentConfig.Permission}')
m("pm-scope-name-project-agent", PM, '{Name: "project-agent", Permission: projectAgentConfig.Permission}', '{Name: "agent", Permission: projectAgentConfig.Permission}')
m("pm-baseline-from-relocated", PM, 'SynthesizeBaseline(configRules), relocation.Rules)', 'SynthesizeBaseline(relocation.Rules), relocation.Rules)')
m("pm-baseline-dropped", PM, 'SynthesizeBaseline(configRules), relocation.Rules)', 'Ruleset{}, relocation.Rules)')
m("pm-relocation-dropped", PM, 'SynthesizeBaseline(configRules), relocation.Rules)', 'SynthesizeBaseline(configRules), configRules)')
m("pm-compose-order", PM, 'ComposeRuleset(SynthesizeDefaults(universalFallback, universalFallbackOrigin), SynthesizeBaseline(configRules), relocation.Rules)',
  'ComposeRuleset(SynthesizeDefaults(universalFallback, universalFallbackOrigin), relocation.Rules, SynthesizeBaseline(configRules))')
m("pm-failclosed-project", PM, 'if projectConfig.Invalid {', 'if false {')
m("pm-failclosed-agent", PM, 'if agentConfig.Invalid {', 'if false {')
m("pm-failclosed-project-agent", PM, 'if projectAgentConfig.Invalid {', 'if false {')
m("pm-failclosed-global", PM, 'if projectConfig.Invalid {', 'if projectConfig.Invalid || globalConfig.Invalid {')
m("pm-failclosed-name-project", PM, 'failClosedScopes = append(failClosedScopes, "project")', 'failClosedScopes = append(failClosedScopes, "project-agent")')
m("pm-failclosed-name-agent", PM, 'failClosedScopes = append(failClosedScopes, "agent")', 'failClosedScopes = append(failClosedScopes, "project")')
m("pm-failclosed-name-project-agent", PM, 'failClosedScopes = append(failClosedScopes, "project-agent")', 'failClosedScopes = append(failClosedScopes, "agent")')
m("pm-failclosed-floor", PM, 'if len(failClosedScopes) > 0 {\n\t\tcomposed = FloorAllowsToAsk(composed)', 'if len(failClosedScopes) > 1 {\n\t\tcomposed = FloorAllowsToAsk(composed)')
m("pm-failclosed-floor-dropped", PM, 'composed = FloorAllowsToAsk(composed)', 'composed = composed')
m("pm-config-layer", PM, 'rules[i].Layer = "config"', 'rules[i].Layer = ""')
m("pm-config-universal-kept", PM, 'if k != "*" {', 'if true {')
m("pm-config-origin", PM, 'if o, ok := origins[rules[i].Surface][rules[i].Pattern]; ok {', 'if o, ok := origins[rules[i].Surface][rules[i].Pattern]; ok && false {')
m("pm-cache-clear", PM, '\tm.cache = map[string]resolvedCacheEntry{}\n}\n\nfunc agentNameOrEmpty', '\t_ = m.cache\n}\n\nfunc agentNameOrEmpty')
m("pm-cache-stamp", PM, 'ok && cached.stamp == stamp {', 'ok {')
m("pm-cache-key-agent", PM, 'cacheKey = *agentName', 'cacheKey = "__global__"')
m("pm-cwd-rebuild", PM, 'derivePolicyLoaderOptions(m.agentDir, cwd))', 'derivePolicyLoaderOptions(m.agentDir, ""))')
m("pm-cwd-agentdir-guard", PM, '\tif m.agentDir != "" {\n\t\tm.loader =', '\tif false {\n\t\tm.loader =')
m("pm-ctor-agentdir", PM, 'case o.AgentDir != "":', 'case false:')
m("pm-ctor-loader", PM, 'case o.PolicyLoader != nil:', 'case false:')
# pm-issues-copy (dropping the defensive copy of the loader's issue list) is equivalent: the appends never reach the loader's slice header.
m("pm-issues-failclosed", PM, 'if len(resolved.failClosedScopes) > 0 {\n\t\tissues', 'if len(resolved.failClosedScopes) > 1 {\n\t\tissues')
m("pm-issues-failclosed-join", PM, 'strings.Join(resolved.failClosedScopes, ", ")', 'strings.Join(resolved.failClosedScopes, ",")')
m("pm-issues-failclosed-text", PM, '"failing closed: \'allow\' rules are clamped to \'ask\' for this session "', '"failing closed: \'allow\' rules are clamped to \'deny\' for this session "')
m("pm-issues-mcp-notice", PM, 'if len(resolved.legacyMcpToolKeys) > 0 {', 'if false {')
m("pm-issues-resolve-first", PM, '\tresolved := m.resolvePermissions(agentName)\n\tissues := m.currentLoader().GetConfigIssues()', '\tissues := m.currentLoader().GetConfigIssues()\n\tresolved := m.resolvePermissions(agentName)')
m("pm-notice-quote", PM, 'named[i] = "\\"" + k + "\\""', 'named[i] = k')
m("pm-composed-config-only", PM, 'if r.Layer == "config" {\n\t\t\tout = append(out, r)', 'if r.Layer != "" {\n\t\t\tout = append(out, r)')
m("pm-toolperm-trim", PM, 'func (m *PermissionManager) GetToolPermission(toolName string, agentName *string) string {\n\tcomposed := m.resolvePermissions(agentName).composedRules\n\tname := jsTrim(toolName)',
  'func (m *PermissionManager) GetToolPermission(toolName string, agentName *string) string {\n\tcomposed := m.resolvePermissions(agentName).composedRules\n\tname := toolName')
m("pm-toolperm-mcp-tool", PM, 'if ClassifyToolKind(name) == ToolKindMcpTool {\n\t\treturn m.resolvePiMcpTool(name, composed)\n\t}\n', '')
m("pm-toolperm-catchall", PM, 'return Evaluate(name, "*", composed, m.flavor, "").Action', 'return Evaluate(name, "", composed, m.flavor, "").Action')
m("pm-denied-trim", PM, 'func (m *PermissionManager) IsToolFullyDenied(toolName string, agentName *string) bool {\n\tcomposed := m.resolvePermissions(agentName).composedRules\n\tname := jsTrim(toolName)',
  'func (m *PermissionManager) IsToolFullyDenied(toolName string, agentName *string) bool {\n\tcomposed := m.resolvePermissions(agentName).composedRules\n\tname := toolName')
m("pm-denied-mcp-tool", PM, 'return m.resolvePiMcpTool(name, composed) == "deny"', 'return m.resolvePiMcpTool(name, composed) != "allow"')
m("pm-denied-surface", PM, 'return IsSurfaceFullyDenied(name, composed, m.flavor)', 'return Evaluate(name, "*", composed, m.flavor, "").Action == "deny"')
m("pm-pi-mcp-values", PM, 'return EvaluateAnyValue(n.Surface, n.Values, composed, m.flavor).Rule.Action', 'return Evaluate(n.Surface, n.Values[0], composed, m.flavor, "").Action')
m("pm-check-agent", PM, 'm.resolvePermissions(intentAgentName(intent)).composedRules', 'm.resolvePermissions(nil).composedRules')
m("pm-check-session-appended", PM, 'if len(sessionRules) > 0 {', 'if false {')
m("pm-check-session-order", PM, 'fullRules = append(append(fullRules, composed...), sessionRules...)', 'fullRules = append(append(fullRules, sessionRules...), composed...)')
m("pm-check-session-threshold", PM, 'if len(sessionRules) > 0 {', 'if len(sessionRules) > 1 {')
m("pm-check-yolo", PM, '\tif m.yolo() {\n', '\tif false {\n')
m("pm-yolo-nil", PM, 'return m.isYoloEnabled != nil && m.isYoloEnabled()', 'return m.isYoloEnabled == nil || m.isYoloEnabled()')
m("pm-yolo-always", PM, 'return m.isYoloEnabled != nil && m.isYoloEnabled()', 'return true')
m("pm-check-tool-trim", PM, 'toolName := jsTrim(it.Surface)', 'toolName := it.Surface')
m("pm-check-tool-reported-name", PM, 'n.ResultExtras, toolName, it.Surface, fullRules, m.flavor)', 'n.ResultExtras, toolName, toolName, fullRules, m.flavor)')
m("pm-check-tool-mcp-servers", PM, 'n := NormalizeInput(toolName, it.Input, m.currentLoader().GetConfiguredMcpServerNames())', 'n := NormalizeInput(toolName, it.Input, nil)')
m("pm-check-bash-spellings", PM, 'n := NormalizeBashCommand(it.Command, it.Spellings)', 'n := NormalizeBashCommand(it.Command, nil)')
m("pm-result-reason", PM, '\t\tReason:   rule.Reason,\n', '')
m("pm-result-origin", PM, '\t\tOrigin:   rule.Origin,\n', '')
m("pm-result-pattern-config", PM, 'if rule.Layer == "config" || rule.Layer == "session" {', 'if rule.Layer == "session" {')
m("pm-result-pattern-session", PM, 'if rule.Layer == "config" || rule.Layer == "session" {', 'if rule.Layer == "config" {')
m("pm-result-pattern-always", PM, 'if rule.Layer == "config" || rule.Layer == "session" {', 'if true {')
m("pm-result-command", PM, 'if c, ok := resultExtras.vals["command"].(string); ok {', 'if c, ok := resultExtras.vals["command"].(string); ok && false {')
m("pm-result-mcp-target", PM, 'if ClassifyToolKind(surface) == ToolKindMcp {', 'if false {')
m("pm-result-mcp-target-all", PM, 'if ClassifyToolKind(surface) == ToolKindMcp {', 'if true {')
m("pm-source-session", PM, 'if rule.Layer == "session" {\n\t\treturn "session"', 'if false {\n\t\treturn "session"')
m("pm-source-special-ext", PM, 'family == "external_directory" || family == "path"', 'family == "external_directory"')
m("pm-source-special-path", PM, 'family == "external_directory" || family == "path"', 'family == "path"')
m("pm-source-mcp-default", PM, 'case ToolKindMcp, ToolKindMcpTool:\n\t\tif rule.Layer == "default" {', 'case ToolKindMcp, ToolKindMcpTool:\n\t\tif false {')
m("pm-source-mcp-tool-kind", PM, 'case ToolKindMcp, ToolKindMcpTool:', 'case ToolKindMcp:')
m("pm-source-skill", PM, 'case ToolKindSkill:\n\t\treturn "skill"', 'case ToolKindSkill:\n\t\treturn "tool"')
m("pm-source-bash", PM, 'case ToolKindBash:\n\t\treturn "bash"', 'case ToolKindBash:\n\t\treturn "tool"')
m("pm-source-path", PM, 'case ToolKindPath:\n\t\treturn "tool"', 'case ToolKindPath:\n\t\treturn "default"')
m("pm-source-extension-default", PM, '// Extension tools distinguish a synthesized-default match from a rule.\n\tif rule.Layer == "default" {', '// Extension tools distinguish a synthesized-default match from a rule.\n\tif false {')
m("pm-source-extension-tool", PM, '\t\treturn "default"\n\t}\n\treturn "tool"\n}', '\t\treturn "default"\n\t}\n\treturn "mcp"\n}')

RS = "permissionresolver.go"
m("rs-fold-skipped", RS, 'if !ok {\n\t\treturn r.permissionManager.Check(resolved, sessionRuleset)', 'if !ok || true {\n\t\treturn r.permissionManager.Check(resolved, sessionRuleset)')
m("rs-fold-first-only", RS, 'results := make([]PermissionCheckResult, len(members))\n\tfor i, surface := range members {', 'results := make([]PermissionCheckResult, len(members))\n\tfor i, surface := range members[:1] {')
m("rs-fold-surface", RS, 'results[i] = r.permissionManager.Check(intentWithSurface(resolved, surface), sessionRuleset)', 'results[i] = r.permissionManager.Check(intentWithSurface(resolved, intentSurface(resolved)+surface[:0]), sessionRuleset)')
m("rs-fold-restrictive-state", RS, 'func(c PermissionCheckResult) string { return c.State })', 'func(c PermissionCheckResult) string { return "ask" })')
m("rs-session-rules", RS, 'sessionRuleset := r.sessionRules.GetRuleset()', 'var sessionRuleset Ruleset')
m("rs-unwrap-values", RS, 'Values: it.Path.MatchValues()', 'Values: []string{it.Path.Value()}')
m("rs-unwrap-agent", RS, 'AgentName: it.AgentName}\n\tcase ToolAccessIntent:', 'AgentName: nil}\n\tcase ToolAccessIntent:')
m("rs-unwrap-surface", RS, 'PathValuesAccessIntent{Surface: it.Surface, Values: it.Path.MatchValues()', 'PathValuesAccessIntent{Surface: "path_read", Values: it.Path.MatchValues()')
m("rs-with-surface-tool", RS, 'case ToolAccessIntent:\n\t\tit.Surface = surface\n\t\treturn it', 'case ToolAccessIntent:\n\t\treturn it')
m("rs-with-surface-values", RS, 'case PathValuesAccessIntent:\n\t\tit.Surface = surface\n\t\treturn it', 'case PathValuesAccessIntent:\n\t\treturn it')
m("rs-with-surface-bash", RS, 'case BashCommandAccessIntent:\n\t\tit.Surface = surface\n\t\treturn it', 'case BashCommandAccessIntent:\n\t\treturn it')
m("rs-check-permission-agent", RS, 'Input: input, AgentName: agentName}, sessionRules)', 'Input: input}, sessionRules)')
m("rs-check-permission-session", RS, 'Input: input, AgentName: agentName}, sessionRules)', 'Input: input, AgentName: agentName}, nil)')
m("rs-tool-permission-agent", RS, 'return r.permissionManager.GetToolPermission(toolName, agentName)', 'return r.permissionManager.GetToolPermission(toolName, nil)')
m("rs-denied-agent", RS, 'return r.permissionManager.IsToolFullyDenied(toolName, agentName)', 'return r.permissionManager.IsToolFullyDenied(toolName, nil)')
m("rs-issues-agent", RS, 'return r.permissionManager.GetConfigIssues(agentName)', 'return r.permissionManager.GetConfigIssues(nil)')

RU = "rule.go"
m("rule-restrictive-tie", RU, 'if restrictiveness[state(r)] > restrictiveness[state(worst)] {', 'if restrictiveness[state(r)] >= restrictiveness[state(worst)] {')
m("rule-restrictive-order", RU, 'var restrictiveness = map[string]int{"allow": 0, "ask": 1, "deny": 2}', 'var restrictiveness = map[string]int{"allow": 0, "ask": 2, "deny": 1}')

GT = "permissiongate.go"
m("gate-deny", GT, 'if p.State == "deny" {', 'if p.State == "denied" {')
m("gate-deny-log-event", GT, 'p.WriteLog("permission_request.blocked", extra)', 'p.WriteLog("permission_request.denied", extra)')
m("gate-deny-log-resolution", GT, 'extra.set("resolution", "policy_denied")', 'extra.set("resolution", "policy_deny")')
m("gate-deny-log-decider", GT, 'extra.set("decidedBy", p.DecidedByRule.JS())', 'extra.set("decidedBy", "rule")')
m("gate-deny-log-context", GT, 'extra = p.LogContext.clone()', 'extra = newObject()')
m("gate-deny-reason", GT, 'return PermissionGateResult{Action: "block", DecidedBy: p.DecidedByRule, Reason: p.Messages.DenyReason}, nil\n\t}\n\n\tif p.State == "ask"', 'return PermissionGateResult{Action: "block", DecidedBy: p.DecidedByRule, Reason: ""}, nil\n\t}\n\n\tif p.State == "ask"')
m("gate-deny-decider", GT, 'return PermissionGateResult{Action: "block", DecidedBy: p.DecidedByRule, Reason: p.Messages.DenyReason}, nil\n\t}\n\n\tif p.State == "ask"', 'return PermissionGateResult{Action: "block", DecidedBy: DecisionSource{}, Reason: p.Messages.DenyReason}, nil\n\t}\n\n\tif p.State == "ask"')
m("gate-ask", GT, 'if p.State == "ask" {', 'if p.State == "asks" {')
m("gate-ask-error", GT, 'if err != nil {\n\t\t\treturn PermissionGateResult{}, err\n\t\t}', '_ = err')
m("gate-refused-decider", GT, 'return PermissionGateResult{Action: "block", DecidedBy: decidedBy, Reason: p.Messages.RefusedReason(decision)}, nil', 'return PermissionGateResult{Action: "block", DecidedBy: p.DecidedByRule, Reason: p.Messages.RefusedReason(decision)}, nil')
m("gate-refused-reason", GT, 'Reason: p.Messages.RefusedReason(decision)}, nil', 'Reason: p.Messages.DenyReason}, nil')
m("gate-approved-check", GT, 'if !decision.Approved {', 'if decision.Approved {')
m("gate-session-state", GT, 'decision.State == DecisionApprovedForSession && p.CanGrantForSession', 'decision.State == DecisionApprovedForSession || p.CanGrantForSession')
m("gate-session-needs-suggestion", GT, 'decision.State == DecisionApprovedForSession && p.CanGrantForSession', 'decision.State == DecisionApprovedForSession')
m("gate-session-state-only", GT, 'decision.State == DecisionApprovedForSession && p.CanGrantForSession', 'p.CanGrantForSession')
m("gate-session-width-default", GT, 'width = "proven"', 'width = "family"')
m("gate-session-width", GT, 'width := decision.SessionGrantWidth', 'width := SessionGrantWidth("")')
m("gate-ask-decider", GT, 'return PermissionGateResult{Action: "allow", DecidedBy: decidedBy}, nil\n\t}\n\n\tif p.State != "allow"', 'return PermissionGateResult{Action: "allow", DecidedBy: p.DecidedByRule}, nil\n\t}\n\n\tif p.State != "allow"')
m("gate-fail-closed", GT, 'if p.State != "allow" {', 'if false {')
m("gate-allow-decider", GT, 'return PermissionGateResult{Action: "allow", DecidedBy: p.DecidedByRule}, nil\n}', 'return PermissionGateResult{Action: "allow", DecidedBy: DecisionSource{}}, nil\n}')

PS = "pathsurfaces.go"
m("fam-members-order", PS, 'f + "_" + string(capabilityDirections[0]), f + "_" + string(capabilityDirections[1])', 'f + "_" + string(capabilityDirections[1]), f + "_" + string(capabilityDirections[0])')
m("fam-members-one", PS, 'm[f] = [2]string{', 'm[f+"x"] = [2]string{')

SR = "sessionrules.go"
m("sr-expand", SR, 'if members, ok := SurfaceFamilyMembers(surface); ok {', 'if members, ok := SurfaceFamilyMembers(surface); ok && false {')
m("sr-expand-one", SR, 'for _, target := range members {', 'for _, target := range members[:1] {')
m("sr-action", SR, 'Action: "allow", Layer: "session", Origin: "session"', 'Action: "ask", Layer: "session", Origin: "session"')
m("sr-layer", SR, 'Action: "allow", Layer: "session", Origin: "session"', 'Action: "allow", Layer: "config", Origin: "session"')
m("sr-origin", SR, 'Action: "allow", Layer: "session", Origin: "session"', 'Action: "allow", Layer: "session", Origin: "global"')
m("sr-pattern", SR, 'Surface: surface, Pattern: pattern,', 'Surface: surface, Pattern: surface,')
m("sr-ruleset-copy", SR, 'return append(Ruleset{}, s.rules...)', 'return s.rules')
m("sr-ruleset-nil", SR, 'return append(Ruleset{}, s.rules...)', 'return append(Ruleset(nil), s.rules...)')
m("sr-record", SR, 'for _, g := range approval.Grants {\n\t\ts.approveLocked(g.Surface, g.Pattern)', 'for _, g := range approval.Grants[:min(1, len(approval.Grants))] {\n\t\ts.approveLocked(g.Surface, g.Pattern)')
m("sr-record-surface", SR, 's.approveLocked(g.Surface, g.Pattern)', 's.approveLocked(SurfaceFamilyOf(g.Surface), g.Pattern)')
m("sr-clear", SR, 's.rules = nil\n\ts.mu.Unlock()', 's.mu.Unlock()')

AG = "sessionapprovalgrant.go"
m("ag-width-proven", AG, 'v == "proven" || v == "family"', 'v == "family"')
m("ag-width-family", AG, 'v == "proven" || v == "family"', 'v == "proven"')
m("ag-width-any", AG, 'v == "proven" || v == "family"', 'v == "proven" || v == "family" || v == "wide"')
m("ag-widen", AG, 'family := SurfaceFamilyOf(g.Surface); family != g.Surface {', 'family := SurfaceFamilyOf(g.Surface); family == g.Surface {')
m("ag-widen-pattern", AG, 'return ApprovalGrant{Surface: family, Pattern: g.Pattern}', 'return ApprovalGrant{Surface: family, Pattern: family}')
m("ag-direction-empty", AG, 'if len(grants) == 0 {\n\t\treturn "", false', 'if len(grants) < 0 {\n\t\treturn "", false')
m("ag-direction-agree", AG, 'if d, ok := CapabilityDirectionOf(g.Surface); !ok || d != direction {', 'if d, ok := CapabilityDirectionOf(g.Surface); !ok || d == "" {')
m("ag-direction-nondirectional", AG, 'if d, ok := CapabilityDirectionOf(g.Surface); !ok || d != direction {', 'if d, ok := CapabilityDirectionOf(g.Surface); ok && d != direction {')
m("ag-targets-dedupe", AG, 'if hasContentsSibling(g, grants) || seen[g.Pattern] {', 'if hasContentsSibling(g, grants) {')
m("ag-targets-sibling", AG, 'if hasContentsSibling(g, grants) || seen[g.Pattern] {', 'if seen[g.Pattern] {')
m("ag-targets-nonnil", AG, 'targets := []string{}', 'var targets []string')
m("ag-sibling-surface", AG, 'if other.Surface != g.Surface {\n\t\t\tcontinue', 'if false {\n\t\t\tcontinue')
m("ag-sibling-slash", AG, 'var contentsSuffixes = []string{"/*", "\\\\*"}', 'var contentsSuffixes = []string{"\\\\*"}')
m("ag-sibling-backslash", AG, 'var contentsSuffixes = []string{"/*", "\\\\*"}', 'var contentsSuffixes = []string{"/*"}')
m("ag-sibling-suffix", AG, 'other.Pattern == g.Pattern+suffix', 'other.Pattern == g.Pattern+suffix[:1]')

SA = "sessionapproval.go"
m("sa-single", SA, 'Grants: []ApprovalGrant{{Surface: surface, Pattern: pattern}}', 'Grants: []ApprovalGrant{{Surface: pattern, Pattern: surface}}')
m("sa-patterns", SA, 'grants[i] = ApprovalGrant{Surface: surface, Pattern: p}', 'grants[i] = ApprovalGrant{Surface: surface, Pattern: p + p}')
m("sa-copy", SA, 'return &SessionApproval{Grants: append([]ApprovalGrant{}, grants...)}', 'return &SessionApproval{Grants: grants}')
m("sa-recordable", SA, 'return len(a.Grants) > 0', 'return len(a.Grants) >= 0')
m("sa-width-proven", SA, 'if w == "proven" {\n\t\treturn a', 'if w != "proven" {\n\t\treturn a')
m("sa-width-widen", SA, 'out[i] = WidenGrant(g)', 'out[i] = g')
m("sa-forwarded-copy", SA, 'return ForwardedSessionApproval{Grants: append([]ApprovalGrant{}, a.Grants...)}', 'return ForwardedSessionApproval{Grants: a.Grants}')

AA = "sessionactiveagent.go"
m("aa-regex-fold", AA, '`(?i)<active_agent', '`<active_agent')
m("aa-regex-space", AA, '\\x{feff}]+name=', '\\x{feff}]*name=')
m("aa-regex-unicode-space", AA, '\\x{00a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}]+name=', ']+name=')
m("aa-regex-name", AA, '["\']([^"\']+)["\']', '["\']([^"\']*)["\']')
m("aa-regex-tail", AA, '[^>]*>`)', '[^>]*`)')
m("aa-normalize-trim", AA, 'trimmed := jsTrim(s)', 'trimmed := s')
m("aa-normalize-empty", AA, 'if trimmed == "" {', 'if false {')
m("aa-entry-type", AA, 'if t, _ := entry["type"].(string); t != "custom" {', 'if false {')
m("aa-entry-custom-type", AA, 'if ct, _ := entry["customType"].(string); ct != "active_agent" {', 'if false {')
m("aa-order", AA, 'for i := len(entries) - 1; i >= 0; i-- {\n\t\tentry := entries[i]', 'for i := 0; i < len(entries); i++ {\n\t\tentry := entries[i]')
m("aa-null-reset", AA, 'if present && name == nil {', 'if present && name == nil && false {')
m("aa-null-absent", AA, 'if present && name == nil {', 'if name == nil || present && false {')
m("aa-error", AA, 'if err != nil {\n\t\treturn nil, err\n\t}\n\tfor i := len(entries)', 'if err != nil {\n\t\treturn nil, nil\n\t}\n\tfor i := len(entries)')
m("aa-prompt-normalize", AA, 'if n, ok := NormalizeAgentName(m[1]); ok {', 'if n, ok := m[1], true; ok {')
m("aa-field-object", AA, 'case *jsObject:\n\t\tif o == nil {', 'case *jsObject:\n\t\tif true {')

SP = "sessionpermission.go"
m("sp-activate-normalizer", SP, 's.pathNormalizer = NewPathNormalizer(s.flavor, ctx.Cwd)', '')
m("sp-activate-context", SP, 's.context = &ctx', '')
m("sp-activate-forwarding", SP, 's.forwarding.Start(ctx.ForwarderContext)', '')
m("sp-activate-selection", SP, 's.authorizerSelection.Activate(ctx)', '')
m("sp-deactivate-context", SP, 's.context = nil\n\ts.mu.Unlock()\n\ts.forwarding.Stop()', 's.mu.Unlock()\n\ts.forwarding.Stop()')
m("sp-deactivate-forwarding", SP, 's.forwarding.Stop()', '')
m("sp-deactivate-selection", SP, 's.authorizerSelection.Deactivate()', '')
m("sp-notify-level", SP, 'ctx.Notifier.Notify(message, "warning")', 'ctx.Notifier.Notify(message, "error")')
m("sp-notify-context", SP, 'if ctx := s.GetRuntimeContext(); ctx != nil && ctx.Notifier != nil {', 'if ctx := s.GetRuntimeContext(); ctx != nil && ctx.Notifier != nil && false {')
m("sp-reset-trusted", SP, 'cwd := ""\n\tif projectTrusted {\n\t\tcwd = ctx.Cwd\n\t}\n\ts.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n\ts.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()',
  'cwd := ctx.Cwd\n\ts.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n\ts.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()')
m("sp-reset-untrusted-always", SP, 'cwd := ""\n\tif projectTrusted {\n\t\tcwd = ctx.Cwd\n\t}\n\ts.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n\ts.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()',
  'cwd := ""\n\ts.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n\ts.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()')
m("sp-reset-skills", SP, 'func (s *PermissionSession) ResetForNewSession(ctx SessionContext, projectTrusted bool) {\n\tcwd := ""\n\tif projectTrusted {\n\t\tcwd = ctx.Cwd\n\t}\n\ts.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n\ts.skillEntries = nil\n',
  'func (s *PermissionSession) ResetForNewSession(ctx SessionContext, projectTrusted bool) {\n\tcwd := ""\n\tif projectTrusted {\n\t\tcwd = ctx.Cwd\n\t}\n\ts.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n')
m("sp-reset-baseline", SP, 's.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\ts.Activate(ctx)', 's.skillEntries = nil\n\ts.mu.Unlock()\n\ts.Activate(ctx)')
m("sp-reset-activate", SP, 's.mu.Unlock()\n\ts.Activate(ctx)', 's.mu.Unlock()')
m("sp-shutdown-rules", SP, 's.sessionRules.Clear()', '')
m("sp-shutdown-skills", SP, 's.sessionRules.Clear()\n\ts.mu.Lock()\n\ts.skillEntries = nil', 's.sessionRules.Clear()\n\ts.mu.Lock()')
m("sp-shutdown-baseline", SP, 's.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\ts.Deactivate()', 's.skillEntries = nil\n\ts.mu.Unlock()\n\ts.Deactivate()')
m("sp-shutdown-deactivate", SP, 's.mu.Unlock()\n\ts.Deactivate()', 's.mu.Unlock()')
m("sp-reload-trusted", SP, 'cwd := ""\n\tif projectTrusted {\n\t\tif ctx := s.GetRuntimeContext(); ctx != nil {', 'cwd := ""\n\tif true {\n\t\tif ctx := s.GetRuntimeContext(); ctx != nil {')
m("sp-reload-skills", SP, 's.permissionManager.ConfigureForCwd(cwd)\n\ts.mu.Lock()\n\ts.skillEntries = nil\n\ts.mu.Unlock()\n}\n\n// ResolveExposedTools', 's.permissionManager.ConfigureForCwd(cwd)\n}\n\n// ResolveExposedTools')
m("sp-reload-baseline", SP, 's.skillEntries = nil\n\ts.mu.Unlock()\n}\n\n// ResolveExposedTools', 's.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n}\n\n// ResolveExposedTools')
m("sp-resolve-exposed", SP, 'return s.toolSurfaceBaseline.resolveExposed(observation.Active, observation.Registered, isExposed)', 'return surfaceResolution{exposed: observation.Active}')
m("sp-skills-set", SP, 's.skillEntries = entries\n\ts.mu.Unlock()', 's.mu.Unlock()')
m("sp-agent-order", SP, 'if fromSession != nil {\n\t\ts.knownAgentName = fromSession\n\t\treturn fromSession, nil\n\t}', 'if fromSession != nil && s.knownAgentName == nil {\n\t\ts.knownAgentName = fromSession\n\t\treturn fromSession, nil\n\t}')
m("sp-agent-error", SP, 'if err != nil {\n\t\treturn nil, err\n\t}\n\ts.mu.Lock()', 'if err != nil {\n\t\treturn nil, nil\n\t}\n\ts.mu.Lock()')
m("sp-agent-known-session", SP, 's.knownAgentName = fromSession\n', '')
m("sp-agent-known-prompt", SP, 's.knownAgentName = fromPrompt\n', '')
m("sp-agent-prompt", SP, 'if fromPrompt := s.promptAgentNameLookup(systemPrompt); fromPrompt != nil {', 'if fromPrompt := s.promptAgentNameLookup(systemPrompt); fromPrompt != nil && false {')
m("sp-agent-fallback", SP, 'return s.knownAgentName, nil\n}', 'return nil, nil\n}')
m("sp-refresh-cwd", SP, 'cwd = ctx.Cwd\n\t}\n\ts.configStore.Refresh(cwd, projectTrusted)', '}\n\ts.configStore.Refresh(cwd, projectTrusted)')
m("sp-refresh-trust", SP, 's.configStore.Refresh(cwd, projectTrusted)', 's.configStore.Refresh(cwd, true)')
m("sp-refresh-ui", SP, 'if ctx != nil && ctx.HasUI && ctx.Notifier != nil {', 'if ctx != nil && ctx.Notifier != nil {')
m("sp-refresh-sync", SP, 'syncPermissionSystemStatus(ctx.Notifier, s.configStore.Current())', 'syncPermissionSystemStatus(ctx.Notifier, defaultExtensionConfig())')
m("sp-log-cwd", SP, 'if ctx := s.GetRuntimeContext(); ctx != nil {\n\t\tcwd = ctx.Cwd\n\t}\n\ts.configStore.LogResolvedPaths(cwd)', 'if ctx := s.GetRuntimeContext(); ctx != nil {\n\t}\n\ts.configStore.LogResolvedPaths(cwd)')
m("sp-infra-config", SP, 'dirs = append(dirs, s.Config().PiInfrastructureReadPaths...)', '')
m("sp-infra-excluded", SP, 'ExcludedDirs: s.paths.PiInfrastructureExcludedDirs}', 'ExcludedDirs: nil}')
m("sp-preview-input", SP, 'ToolInputPreviewMaxLength: 200,', 'ToolInputPreviewMaxLength: 201,')
m("sp-preview-text", SP, 'ToolTextSummaryMaxLength: 80}', 'ToolTextSummaryMaxLength: 81}')
m("sp-shell-nil", SP, 'if raw == nil {\n\t\treturn nil\n\t}', 'if raw == nil {\n\t\treturn ShellToolsConfig{}\n\t}')
m("sp-shell-command", SP, 'cmd, ok := entry.str("commandArgument")', 'cmd, ok := entry.str("workdirArgument")')
m("sp-shell-workdir", SP, 'workdir, _ := entry.str("workdirArgument")', 'workdir := ""')
m("sp-shell-skip", SP, 'if !ok {\n\t\t\tcontinue\n\t\t}\n\t\tworkdir', 'if !ok && false {\n\t\t\tcontinue\n\t\t}\n\t\tworkdir')
m("sp-entries-no-manager", SP, 'if c.SessionManager == nil {\n\t\treturn nil, errors.New("the session context has no session manager")\n\t}', 'if c.SessionManager == nil {\n\t\t_ = errors.New("")\n\t\treturn nil, nil\n\t}')

# Equivalent mutants, left out (each was run and survived):
# - pm-cache-key-global: the global cache key's spelling is arbitrary as long as it is stable.
# - pm-check-path-values-empty: an empty value list and ["*"] both evaluate the "*" fallback (EvaluateAnyValue).
# - pm-result-target: the extras' target is only set for the mcp surface, where the matched candidate replaces it anyway.
# - aa-prompt-empty, aa-prompt-group: the regex finds nothing in "" and its capture is never empty.
# - sp-infra-copy: appending to a capacity-limited slice copies as well.
# - ag-direction-first: the loop's own check of the first grant makes the up-front one redundant.
# - sp-reload-configure: a no-op defer.
# - sr-lock-ruleset: the lock is observable only by the race detector (TestSessionRulesConcurrentUse runs under go test -race).
for mu in M:
    text = open(os.path.join(root, mu["file"])).read()
    n = text.count(mu["find"])
    assert n == 1, "%s: find occurs %d times in %s" % (mu["name"], n, mu["file"])
    assert mu["find"] != mu["replace"], mu["name"]
names = [mu["name"] for mu in M]
assert len(names) == len(set(names)), "duplicate names"
with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations-policy.json"), "w") as f:
    json.dump(M, f, indent=1)
    f.write("\n")
print(len(M), "mutations")
