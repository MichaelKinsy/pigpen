#!/usr/bin/env python3
"""Generates port/mutations.json: one deliberate defect per behavior of the shipped slice, and checks that every
`find` string is unique in its file (a mutation that does not apply must not pass silently)."""
import json, os, sys

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "tintinweb-subagents")
M = []


def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})


# agent_types.go: the registry and the spawn policy
m("isvalid-ignores-enabled", "agent_types.go", "return ok && r.m[k].Enabled", "return ok && (r.m[k].Enabled || true)")
m("fallback-hides-origin", "agent_types.go", 'return spawnResolution{OK: true, Type: "general-purpose", FellBackFrom: raw}', 'return spawnResolution{OK: true, Type: "general-purpose"}')
m("none-case-sensitive", "agent_types.go", "strings.ToLower(configured) == noFallback", "configured == noFallback")
m("unusable-fallback-accepted", "agent_types.go", "if !ok || !r.m[k].Enabled {", "if !ok {")
m("enabled-type-guesses-case", "agent_types.go", "if k, ok := r.unambiguous(raw); ok && r.m[k].Enabled {\n\t\treturn k", "if k, ok := r.resolveKey(raw); ok && r.m[k].Enabled {\n\t\treturn k")
m("defaults-disabled-ignored", "agent_types.go", "if !isDefaultsDisabled() {", "if true {")
m("no-general-purpose-fallback", "agent_types.go", 'if gp := r.m["general-purpose"]; gp != nil && gp.Enabled {', 'if gp := r.m["general-purpose"]; gp != nil && gp.Enabled && false {')
m("empty-tools-widened", "agent_types.go", "r.m[k].Enabled && r.m[k].BuiltinToolNames != nil {", "r.m[k].Enabled && len(r.m[k].BuiltinToolNames) > 0 {")
m("memory-tools-always-listed", "agent_types.go", "if !existing[n] {", "if true {")
m("user-names-inverted", "agent_types.go", "if r.m[k].IsDefault == defaults {", "if r.m[k].IsDefault != defaults {")
m("registration-keeps-old", "agent_types.go", "registry = r\n\ttypesMu.Unlock()", "if registry == nil {\n\t\tregistry = r\n\t}\n\ttypesMu.Unlock()") if False else None
m("config-view-display-name", "agent_types.go", 'if d == "" {\n\t\td = c.Name\n\t}', 'if d == "" {\n\t\td = ""\n\t}')

# agents.go and frontmatter.go: custom agent files
m("project-dir-source", "agents.go", '{filepath.Join(cwd, ".agents", "agents"), "project"},', '{filepath.Join(cwd, ".agents", "agents"), "global"},')
m("enabled-default-off", "agents.go", 'Enabled: fm["enabled"] != false,', 'Enabled: fm["enabled"] == true,')
m("negative-max-turns-kept", "agents.go", "if f, ok := v.(float64); ok && f >= 0 {", "if f, ok := v.(float64); ok {")
m("all-alias-case-sensitive", "agents.go", 'func isWildcard(e string) bool { return e == "*" || strings.ToLower(e) == "all" }', 'func isWildcard(e string) bool { return e == "*" || e == "all" }')
m("allowed-subagents-all-case", "agents.go", 'if i == "*" || strings.ToLower(i) == "all" {', 'if i == "*" || i == "all" {')
m("isolation-false-not-off", "agents.go", 'case "off", "none", "no", false:', 'case "off", "none", "no":')
m("memory-scope-any", "agents.go", '(s == "user" || s == "project" || s == "local")', 's != ""')
m("output-transcript-inverted", "agents.go", "b := v != false", "b := v == true")
m("declared-colon-name-loaded", "agents.go", 'if strings.Contains(declared, ":") {', "if false {")
m("description-defaults-to-file", "agents.go", "if !hasDesc {\n\t\t\tdesc = name\n\t\t}", "if !hasDesc {\n\t\t\tdesc = filenameType\n\t\t}")
m("warnings-repeat", "agents.go", "if !this[w] && !warnedLast[w] {", "if !this[w] {")
m("strict-ignored", "agents.go", "if strict {\n\t\t\t\treturn fmt.Errorf", "if false {\n\t\t\t\treturn fmt.Errorf")
m("disabled-fallback-claimed", "agents.go", "surviving.SourcePath != \"\" && surviving.Enabled {", 'surviving.SourcePath != "" {')
m("empty-frontmatter-block", "frontmatter.go", "if end+3 > 4 {", "if true {")
m("compact-mapping-allowed", "frontmatter.go", "c != '[' && strings.Contains(rest, \": \")", "c != '[' && false")
m("bom-kept", "frontmatter.go", 'content = strings.TrimPrefix(content, "\\ufeff")', "")

# manager.go: records, the limit and the queue
m("queue-lifo", "manager.go", "next = m.queue[0]\n\t\tm.queue = m.queue[1:]", "next = m.queue[len(m.queue)-1]\n\t\tm.queue = m.queue[:len(m.queue)-1]")
m("foreground-queued", "manager.go", "if background && m.running >= m.maxConcurrent {", "if m.running >= m.maxConcurrent {")
m("slot-not-released", "manager.go", "if r.Background {\n\t\tm.running--\n\t}", "if r.Background {\n\t}")
m("consume-running-agent", "manager.go", "if r == nil || r.Status == statusRunning || r.Status == statusQueued {", "if r == nil {")
m("stopped-reads-as-aborted", "manager.go", "case r.stopped:\n\t\tr.Status = statusStopped", "case r.stopped:\n\t\tr.Status = statusAborted")
m("steer-finished-agent", "manager.go", "if status != statusRunning || c == nil {", "if c == nil {")
m("handle-numbering-starts-late", "manager.go", "for n := 2; taken[candidate]; n++ {", "for n := 3; taken[candidate]; n++ {")
m("ambiguous-ref-resolved", "manager.go", "if found != nil {\n\t\t\t\treturn nil\n\t\t\t}", "if found != nil {\n\t\t\t}")
m("queued-abort-keeps-queue", "manager.go", "m.queue = append(m.queue[:i], m.queue[i+1:]...)\n\t\t\t\tbreak", "_ = i\n\t\t\t\tbreak")
m("handle-base-length", "manager.go", "len(r) > 32", "len(r) > 33")
m("handle-base-empty", "manager.go", 'if slug == "" {\n\t\treturn "agent"\n\t}', 'if slug == "" {\n\t\treturn ""\n\t}')

# extension.go: events and the notification
m("consumed-still-notified", "extension.go", "if consumed {\n\t\t\treturn\n\t\t}", "if consumed && false {\n\t\t\treturn\n\t\t}")
m("foreground-announced", "extension.go", "if !r.Background {\n\t\treturn\n\t}", "if false {\n\t\treturn\n\t}")
m("stopped-on-completed-channel", "extension.go", "return status == statusError || status == statusStopped || status == statusAborted", "return status == statusError || status == statusAborted")
m("notification-not-escaped", "extension.go", '"<result>"+escapeXML(preview)+"</result>"', '"<result>"+preview+"</result>"')
m("notification-not-truncated", "extension.go", "len(u) > resultMaxLen {", "len(u) > resultMaxLen+1000 {")
m("notification-steers", "extension.go", 'DeliverAs: "followUp"', 'DeliverAs: "steer"')
m("protocol-version", "extension.go", "protocolVersion = 2", "protocolVersion = 3")
m("wrapped-up-unmarked", "extension.go", 'return " (wrapped up at the turn limit — output may be partial)"', 'return ""')
m("error-status-text", "extension.go", 'return "Error: " + e', 'return "Error"')

# bus.go: the cross-extension protocol and spawn defaults
m("spawn-not-background", "bus.go", 'a.start(ctx, typ, prompt, description, "", model, "", maxTurns, true, false)', 'a.start(ctx, typ, prompt, description, "", model, "", maxTurns, false, false)')
m("stop-error-text", "bus.go", 'errors.New("Agent not found")', 'errors.New("Not found")')
m("explore-model-forced", "bus.go", 'spec.Model = "" // the original', 'spec.Model = cfg.Model // the original')
m("default-max-turns-ignored", "bus.go", "if a.defaultMaxTurns != nil {", "if a.defaultMaxTurns != nil && false {")
m("isolated-agent-file-ignored", "bus.go", "|| (cfg.Isolated != nil && *cfg.Isolated) || cfg.Extensions == false}", "}")
m("no-session-error", "bus.go", 'errors.New("No active session")', 'errors.New("no session")')

# runner.go: the child process
m("no-tools-flag", "runner.go", 'args = append(args, "--no-tools")', 'args = append(args, "--tools", "")')
m("append-flag", "runner.go", 'args = append(args, "--append-system-prompt", spec.SystemPrompt)', 'args = append(args, "--system-prompt", spec.SystemPrompt)')
m("turn-limit-off-by-one", "runner.go", "!c.wrapped && c.turns >= c.maxTurn", "!c.wrapped && c.turns > c.maxTurn")
# review (rev-pig-essentials): the grace-turn abort, the final-turn failure, the statuses and notes, the harness pig, the process group
m("grace-abort-off-by-one", "runner.go", "c.turns >= c.maxTurn+c.grace", "c.turns > c.maxTurn+c.grace")
m("grace-abort-missing", "runner.go", "c.aborted, stop = true, true", "c.aborted, stop = true, false")
m("grace-default", "runner.go", "const defaultGraceTurns = 5", "const defaultGraceTurns = 6")
m("grace-setting-ignored", "bus.go", "spec.GraceTurns = a.graceTurns", "spec.GraceTurns = 0")
m("turn-limit-steer-text", "runner.go", 'Wrap up immediately — provide your final answer now."', 'Wrap up now: give your final answer."')
m("final-turn-error-ignored", "runner.go", 'case c.lastStop == "error":', 'case c.lastStop == "error" && false:')
m("final-turn-error-default", "runner.go", 'res.Failure = "provider error with no output"', 'res.Failure = ""')
m("final-turn-length-ignored", "runner.go", 'case c.lastStop == "length" && strings.TrimSpace(c.lastText) == "":', 'case c.lastStop == "length" && false:')
m("aborted-reads-as-steered", "manager.go", "case res.Aborted:\n\t\tr.Status = statusAborted", "case res.Aborted:\n\t\tr.Status = statusSteered")
m("failure-reads-as-completed", "manager.go", "case res.Failure != \"\":", "case res.Failure != \"\" && false:")
m("steered-reads-as-completed", "manager.go", "case res.WrappedUp:\n\t\tr.Status = statusSteered", "case res.WrappedUp:\n\t\tr.Status = statusCompleted")
m("foreground-note-missing", "tools.go", "strings.Join(stats, \", \"), foregroundOutcomeNote(status), out)", "strings.Join(stats, \", \"), \"\", out)")
m("partial-output-dropped", "tools.go", '"%sAgent failed: %s%s", fallbackNote, errText, partialOutputSuffix(result)', '"%sAgent failed: %s%s", fallbackNote, errText, ""')
m("tool-use-id-missing", "extension.go", "if r.ToolCallID != \"\" {", "if r.ToolCallID != \"\" && false {")
m("stopped-note-mild", "extension.go", 'return " (STOPPED BY THE USER before completion — output is partial; the task was NOT finished)"', 'return " (stopped)"')
m("fallback-note-text", "tools.go", "`Note: Unknown agent type \"` + fellBack + `\" — using ` + resolved", "`Note: agent type \"` + fellBack + `\" is unknown; using ` + resolved")
m("harness-pig-ignored", "runner.go", 'if b := os.Getenv("PIG_HARNESS_BINARY"); b != "" {', 'if b := os.Getenv("PIG_HARNESS_BINARY"); b != "" && false {')
m("probe-keeps-ext-socket", "runner.go", 'return strings.HasPrefix(kv, "PIG_EXT_")', 'return strings.HasPrefix(kv, "PIG_EXT_NONE_")')
m("terminate-no-group-signal", "proc_unix.go", "_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)", "_ = cmd.Process.Signal(syscall.SIGTERM)")
m("stats-tool-calls-ignored", "runner.go", 'if f, ok := d["toolCalls"].(float64); ok {', 'if f, ok := d["toolCalls"].(float64); ok && false {')
m("tokens-dropped", "runner.go", "res.Tokens = int(f)", "res.Tokens = int(f) * 0")
m("child-may-nest", "runner.go", 'cmd.Env = append(os.Environ(), depthEnv+"=1")', "cmd.Env = os.Environ()")
m("isolation-flag-dropped", "runner.go", 'args = append(args, "--no-extensions")', "")
m("version-probe-any", "runner.go", "err == nil && version.Match(bytes.TrimSpace(out))", "err == nil && (version.Match(bytes.TrimSpace(out)) || true)")

# tools.go: the tools
m("tools-suffix-no-builtins", "tools.go", 'return "no built-ins, extension tools only"', 'return "none"')
m("model-label-date", "tools.go", "-\\d{8}$`", "-\\d{9}$`")
m("resume-prompt", "tools.go", "You are continuing earlier work. Your previous run ended with this answer:", "Earlier answer:")
m("inherit-context-allowed", "tools.go", 'if v, _ := p["inherit_context"].(bool); v {', "if false {")
m("wait-ignored", "tools.go", 'if wait, _ := p["wait"].(bool); wait {', "if false {")
m("foreground-failure-text", "tools.go", '"%sAgent failed: %s%s"', '"%sFailure: %s%s"')
m("duration-format", "tools.go", 'fmt.Sprintf("%.1fs", float64(ms)/1000)', 'fmt.Sprintf("%.2fs", float64(ms)/1000)')
m("tokens-format", "tools.go", 'fmt.Sprintf("%.1fk token", float64(n)/1000)', 'fmt.Sprintf("%.2fk token", float64(n)/1000)')
m("tokens-millions", "tools.go", "case n >= 1_000_000:", "case n >= 1_000_000_000:")
m("result-consumed-not-marked", "tools.go", "r.Consumed = true\n\t\ta.mgr.mu.Unlock()\n\t\ta.mu.Lock()", "a.mgr.mu.Unlock()\n\t\ta.mu.Lock()")
m("steer-event", "tools.go", '_ = ctx.Events().Emit("subagents:steered"', '_ = ctx.Events().Emit("subagents:steer"')
m("result-by-name-ignored", "tools.go", 'r := a.mgr.resolve(id)\n\tif r == nil {\n\t\treturn notFound(id), nil\n\t}\n\tif wait', 'r := a.mgr.get(id)\n\tif r == nil {\n\t\treturn notFound(id), nil\n\t}\n\tif wait')

# prompts.go and settings.go
m("prompt-header-spacing", "prompts.go", "autonomously.\\n\\n\" + envBlock", "autonomously.\\n\" + envBlock")
m("prompt-env-git", "prompts.go", 'repo = "Git repository: yes\\nBranch: " + env.Branch', 'repo = "Git repository: yes"')
m("prompt-append-base", "prompts.go", 'if identity == "" {\n\t\t\tidentity = genericBase\n\t\t}', 'if identity == "" {\n\t\t\tidentity = ""\n\t\t}')
m("env-platform-windows", "prompts.go", 'return "win32"', 'return "windows"')
m("git-branch-unknown", "prompts.go", 'env.Branch = "unknown"', 'env.Branch = ""')
m("settings-project-wins", "settings.go", "if p.MaxConcurrent != 0 {\n\t\ts.MaxConcurrent = p.MaxConcurrent\n\t}", "")
m("settings-ceiling", "settings.go", "n <= maxConcurrentCeiling {", "n <= maxConcurrentCeiling+1 {")
m("fallback-false-spelling", "settings.go", "if !v { // `false` is a spelling of \"none\"", "if v {")
m("fallback-trimmed", "settings.go", "if s := strings.TrimSpace(v); s != \"\" {", "if s := strings.TrimLeft(v, \" \"); s != \"\" {")

M = [x for x in M if x]
by_file = {}
for x in M:
    by_file.setdefault(x["file"], open(os.path.join(root, x["file"])).read())
bad = 0
for x in M:
    n = by_file[x["file"]].count(x["find"])
    if n != 1:
        print("find not unique/absent (%d): %s in %s" % (n, x["name"], x["file"]), file=sys.stderr)
        bad += 1
if bad:
    sys.exit(1)
with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "w") as f:
    json.dump(M, f, indent=1, ensure_ascii=False)
    f.write("\n")
print(len(M), "mutations")
