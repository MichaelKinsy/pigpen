#!/usr/bin/env python3
"""Generates port/mutations-handlers.json: one deliberate defect per behavior of the handlers, the cross-extension service and the
composition root (lifecyclehandler.go, sessionturnprep.go, agentprep.go, servicelocator.go, servicelifecycle.go, permissionsservice.go,
bashadvisory.go, permissionevents.go, extension.go). Each `find` must occur exactly once in its file (checked here). Run with
  pigeq mutate --unit-only --go extensions/pi-permission-system --mutations port/mutations-handlers.json --sdk-dir $PIG_SDK_DIR --jobs 4
from components/permissions."""
import json, os

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "pi-permission-system")
M = []
# Pruned as equivalent: ap-skill-session-rules (a nil and an empty session ruleset evaluate identically) and cr-flavor-posix (the host flavor
# differs from "linux" only on windows, and the mutation run is on linux).

def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})

LC = "lifecyclehandler.go"
m("lc-untrusted-ignored", LC, "projectTrusted := ctx.ProjectTrusted()\n\t// Reset first", "projectTrusted := true\n\t// Reset first")
m("lc-refresh-before-reset", LC, "\tif err := h.session.ResetForNewSession(ctx, projectTrusted); err != nil {\n\t\treturn err\n\t}\n\th.session.RefreshConfig(&ctx, projectTrusted)",
  "\th.session.RefreshConfig(&ctx, projectTrusted)\n\tif err := h.session.ResetForNewSession(ctx, projectTrusted); err != nil {\n\t\treturn err\n\t}")
m("lc-no-paths-log", LC, "\th.session.LogResolvedConfigPaths()\n", "")
m("lc-report-before-refresh", LC, "\th.session.RefreshConfig(&ctx, projectTrusted)\n\th.session.LogResolvedConfigPaths()\n\t// The config was just re-read above, and the session is activated, so a warning has a UI to reach.\n\th.configIssues.Report()",
  "\th.configIssues.Report()\n\th.session.RefreshConfig(&ctx, projectTrusted)\n\th.session.LogResolvedConfigPaths()")
m("lc-trust-warn-always", LC, "\tif !projectTrusted {\n\t\th.warnProjectUntrusted(ctx, \"session_start\")", "\tif true {\n\t\th.warnProjectUntrusted(ctx, \"session_start\")")
m("lc-trust-warn-never", LC, "\tif !projectTrusted {\n\t\th.warnProjectUntrusted(ctx, \"session_start\")", "\tif false {\n\t\th.warnProjectUntrusted(ctx, \"session_start\")")
m("lc-agent-error-swallowed", LC, "\tagentName, err := h.session.ResolveAgentName(ctx, \"\")\n\tif err != nil {\n\t\treturn err\n\t}", "\tagentName, _ := h.session.ResolveAgentName(ctx, \"\")")
m("lc-no-issue-warn", LC, "\t\th.logger.Warn(issue)\n", "\t\t_ = issue\n")
m("lc-reload-debug-always", LC, 'if event.Reason == "reload" {', "if true {")
m("lc-reload-debug-never", LC, 'if event.Reason == "reload" {', "if false {")
m("lc-service-not-activated", LC, "\th.serviceLifecycle.Activate(ctx)\n\treturn nil", "\treturn nil")
m("lc-discover-any-reason", LC, 'if event.Reason != "reload" {\n\t\treturn\n\t}', "")
m("lc-discover-untrusted-ignored", LC, "projectTrusted := ctx.ProjectTrusted()\n\th.session.Reload(projectTrusted)", "projectTrusted := true\n\th.session.Reload(projectTrusted)")
m("lc-discover-no-warn", LC, '\t\th.warnProjectUntrusted(ctx, "resources_discover")\n', "")
m("lc-discover-cwd-null", LC, 'details.set("cwd", nil)', 'details.set("cwd", "")')
m("lc-shutdown-status-kept", LC, '\t\tctx.Notifier.SetStatus(permissionSystemStatusKey, "")\n', "")
m("lc-shutdown-no-audit", LC, "\th.audit.WriteSummary(h.logger)\n", "")
m("lc-shutdown-no-release", LC, "\th.dialogs.ReleaseAll(SessionEndedReason)\n", "")
m("lc-shutdown-release-after", LC, "\th.dialogs.ReleaseAll(SessionEndedReason)\n\th.session.Shutdown()", "\th.session.Shutdown()\n\th.dialogs.ReleaseAll(SessionEndedReason)")
m("lc-shutdown-no-session", LC, "\th.session.Shutdown()\n", "")
m("lc-shutdown-no-teardown", LC, "\th.serviceLifecycle.Teardown()\n}", "}")

TP = "sessionturnprep.go"
m("tp-no-warm", TP, "\tp.warmParser()\n", "")
m("tp-activate-error-swallowed", TP, "\tif err := p.session.Activate(ctx); err != nil {\n\t\treturn err\n\t}", "\t_ = p.session.Activate(ctx)")
m("tp-trust-ignored", TP, "p.session.RefreshConfig(&ctx, ctx.ProjectTrusted())", "p.session.RefreshConfig(&ctx, true)")
m("tp-no-report", TP, "\tp.configIssues.Report()\n", "")
m("tp-no-announce", TP, "\tp.readyAnnouncer.AnnounceReady(ctx)\n", "")
m("tp-announce-first", TP, "\tp.session.RefreshConfig(&ctx, ctx.ProjectTrusted())", "\tp.readyAnnouncer.AnnounceReady(ctx)\n\tp.session.RefreshConfig(&ctx, ctx.ProjectTrusted())")

AP = "agentprep.go"
m("ap-expose-inverted", AP, "return !isToolFullyDenied(toolName, agentName)", "return isToolFullyDenied(toolName, agentName)")
m("ap-agent-dropped", AP, "return !isToolFullyDenied(toolName, agentName)", "return !isToolFullyDenied(toolName, nil)")
m("ap-prep-error-swallowed", AP, "\tif err := h.turnPrep.Prepare(ctx); err != nil {\n\t\treturn err\n\t}", "\t_ = h.turnPrep.Prepare(ctx)")
m("ap-agent-error-swallowed", AP, "\tagentName, err := h.session.ResolveAgentName(ctx, systemPrompt)\n\tif err != nil {\n\t\treturn err\n\t}", "\tagentName, _ := h.session.ResolveAgentName(ctx, systemPrompt)")
m("ap-prompt-dropped", AP, "h.session.ResolveAgentName(ctx, systemPrompt)", 'h.session.ResolveAgentName(ctx, "")')
m("ap-registered-ignored", AP, "Registered: registeredSet}", "Registered: map[string]bool{}}")
m("ap-set-active-skipped", AP, "\th.toolRegistry.SetActive(allowedTools)\n", "")
m("ap-no-debug", AP, '\t\th.logger.Debug("tool_surface.changed", details)\n', "")
m("ap-debug-always", AP, "\tif surface.changed {", "\tif true {")
m("ap-subagent-not-checked", AP, "\treturn h.detector.IsSubagent(detectionContext(ctx.AuthorityContext))", "\treturn true, nil")
m("ap-custom-prompt-ignored", AP, 'if options.CustomPrompt == "" {', "if false {")
m("ap-tools-section-nil", AP, "\t\t\ttools := \"\"\n\t\t\tif sections.Tools != nil {\n\t\t\t\ttools = *sections.Tools\n\t\t\t}", '\t\t\ttools := ""')
m("ap-rules-section-dropped", AP, '\t\t\toptions.Sections.Set("rules", sections.Rules)\n', "")
m("ap-skills-not-narrowed", AP, "\toptions.Skills = WithoutDeniedSkills(", "\t_ = WithoutDeniedSkills(")
m("ap-skill-entries-dropped", AP, "\th.session.SetActiveSkillEntries(VisibleSkillPromptEntries(systemPrompt, rawSkillChecker{h.resolver}, agentName, h.session.GetPathNormalizer()))",
  "")
m("ap-fragments-joined", AP, 'strings.Join(fragments, "\\n")', 'strings.Join(fragments, "")')

SL = "servicelocator.go"
m("sl-unpublish-unconditional", SL, "if sessionServices[sessionID] == service {\n\t\tdelete(sessionServices, sessionID)\n\t}", "delete(sessionServices, sessionID)")
m("sl-publish-keeps-first", SL, "\tsessionServices[sessionID] = service\n\tsessionServicesMu.Unlock()", "\tif _, held := sessionServices[sessionID]; !held {\n\t\tsessionServices[sessionID] = service\n\t}\n\tsessionServicesMu.Unlock()")

SV = "servicelifecycle.go"
m("sv-no-rearm", SV, "\tl.announced = false\n\tl.mu.Unlock()\n\tif sessionID", "\tl.mu.Unlock()\n\tif sessionID")
m("sv-announce-always", SV, "\tif l.announced {\n\t\tl.mu.Unlock()\n\t\treturn\n\t}", "\tif false {\n\t\tl.mu.Unlock()\n\t\treturn\n\t}")
m("sv-announce-not-latched", SV, "\tl.announced = true\n\tl.mu.Unlock()\n\tl.emitReady(ctx)", "\tl.mu.Unlock()\n\tl.emitReady(ctx)")
m("sv-publish-after-ready", SV, "\t\tPublishPermissionsService(sessionID, l.service)\n\t\tl.mu.Lock()\n\t\tl.publishedSessionID = &sessionID\n\t\tl.mu.Unlock()\n\t}\n\tl.emitReady(ctx)",
  "\t\tl.mu.Lock()\n\t\tl.publishedSessionID = &sessionID\n\t\tl.mu.Unlock()\n\t}\n\tl.emitReady(ctx)\n\tif sessionID, ok := ReadSessionID(ctx.SessionManager); ok {\n\t\tPublishPermissionsService(sessionID, l.service)\n\t}")
m("sv-teardown-no-unsubscribe", SV, "\tfor _, unsubscribe := range l.subscriptions {\n\t\tunsubscribe()\n\t}\n", "")
m("sv-teardown-no-unpublish", SV, "\t\tUnpublishPermissionsService(*published, l.service)\n", "")
m("sv-teardown-keeps-id", SV, "\tpublished := l.publishedSessionID\n\tl.publishedSessionID = nil", "\tpublished := l.publishedSessionID")
m("sv-ready-role-inverted", SV, "AdjudicatesLocally: l.role.AdjudicatesLocally()}", "AdjudicatesLocally: !l.role.AdjudicatesLocally()}")
m("sv-ready-id-always", SV, "\tvar id *string\n\tif sessionID, ok := ReadSessionID(ctx.SessionManager); ok {\n\t\tid = &sessionID\n\t}", "\tid := new(string)")

PS = "permissionsservice.go"
m("ps-bash-not-advisory", PS, 'if surface == "bash" {', 'if surface == "bashx" {')
m("ps-bash-value-dropped", PS, "command = *value", 'command = ""')
m("ps-mcp-not-prefixed", PS, "return s.resolver.ResolvePrefixed(intent)", "return s.resolver.Resolve(ToolAccessIntent{Surface: surface, AgentName: agentName})")
m("ps-tool-permission-agent", PS, "return s.resolver.GetToolPermission(toolName, agentName)", "return s.resolver.GetToolPermission(toolName, nil)")
m("ps-fully-denied-agent", PS, "return s.resolver.IsToolFullyDenied(toolName, agentName)", "return s.resolver.IsToolFullyDenied(toolName, nil)")
m("ps-extractor-unwrapped", PS, "if resolved := s.accessExtractors.Resolve(toolName); resolved != nil {\n\t\treturn resolved.Extractor\n\t}", "")

BA = "bashadvisory.go"
m("ba-cold-agent-dropped", BA, "Input: o, AgentName: agentName})", "Input: o})")
m("ba-warm-ignored", BA, "if !warm {", "if !warm || true {")

EV = "permissionevents.go"
m("ev-ready-channel", EV, 'const PermissionsReadyChannel = "permissions:ready"', 'const PermissionsReadyChannel = "permissions:ready "')
m("ev-ready-unrecovered", EV, "func EmitReadyEvent(events PermissionEventBus, event PermissionsReadyEvent) {\n\tdefer func() { _ = recover() }()", "func EmitReadyEvent(events PermissionEventBus, event PermissionsReadyEvent) {")

CR = "extension.go"
m("cr-yolo-never", CR, "isYoloEnabled := func() bool { return isYoloModeEnabled(p.configStore.Current()) }", "isYoloEnabled := func() bool { return false }")
m("cr-trust-default", CR, "return err == nil && trusted\n\t\t},", "return err == nil || trusted\n\t\t},")
m("cr-input-error-allows", CR, '\t\t\tctx.Notify(UntrustedInputMessage(err), "warning")\n\t\t\treturn map[string]any{"action": "handled"}, nil', "\t\t\treturn nil, nil")
m("cr-input-handled-allows", CR, '\t\tif result.Action == "handled" {\n\t\t\treturn map[string]any{"action": "handled"}, nil\n\t\t}', "\t\t_ = result\n")
m("cr-lifecycle-bus-unsubscribed", CR, "p.serviceLifecycle = NewPermissionServiceLifecycle(service, p.selection, events, []func(){unsubSubagentLifecycle})", "p.serviceLifecycle = NewPermissionServiceLifecycle(service, p.selection, events, func() []func() { _ = unsubSubagentLifecycle; return nil }())")
m("cr-service-not-observed", CR, "NewObservedAuthorizerRegistrar(authorizerRegistry, p.selection, p.logger))", "authorizerRegistry)")
m("cr-skills-not-written-back", CR, "\t\twriteBackSkills(data, payload.Options)\n", "")
m("cr-timeout-default", CR, "return PermissionForwardingTimeoutMs\n\t\t\t},", "return 0\n\t\t\t},")
m("cr-warm-sync", CR, "func() { go bashparse.WarmBashParser() }", "func() { _ = bashparse.WarmBashParser }")

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations-handlers.json")
seen = set()
for x in M:
    assert x["name"] not in seen, x["name"]
    seen.add(x["name"])
    src = open(os.path.join(root, x["file"])).read()
    n = src.count(x["find"])
    assert n == 1, (x["name"], n)
json.dump(M, open(out, "w"), indent=1)
print(len(M), "mutations")
