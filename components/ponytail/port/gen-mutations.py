#!/usr/bin/env python3
"""Generates port/mutations.json: one deliberate defect per behavior, checking every `find` string is unique."""
import json, os, sys
root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "ponytail")
M = []
def m(name, file, find, replace): M.append({"name": name, "file": file, "find": find, "replace": replace})

# config.go
m("normalize-keeps-case", "config.go", "func normalizeMode(mode string) string {\n\tn := strings.ToLower(jsTrim(mode))", "func normalizeMode(mode string) string {\n\tn := jsTrim(mode)")
m("normalize-config-no-review", "config.go", 'validModes   = []string{"off", "lite", "full", "ultra", "review"}', 'validModes   = []string{"off", "lite", "full", "ultra"}')
m("review-is-runtime", "config.go", 'runtimeModes = []string{"off", "lite", "full", "ultra"}', 'runtimeModes = []string{"off", "lite", "full", "ultra", "review"}')
m("persisted-ignores-config-modes", "config.go", "return normalizeConfigMode(mode)\n}", 'return ""\n}')
m("js-space-bom", "config.go", "return unicode.IsSpace(r) || r == '\\ufeff'", "return unicode.IsSpace(r)")
m("deactivation-substring", "config.go", 'return t == "stop ponytail" || t == "normal mode"', 'return strings.Contains(t, "stop ponytail") || strings.Contains(t, "normal mode")')
m("deactivation-punctuation", "config.go", "return r == '.' || r == '!' || r == '?' || isJSSpace(r) })", "return r == '.' || isJSSpace(r) })")
m("deactivation-case", "config.go", "t := strings.ToLower(jsTrim(text))\n\tt = strings.TrimRightFunc", "t := jsTrim(text)\n\tt = strings.TrimRightFunc")
m("config-dir-xdg-ignored", "config.go", 'if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {', 'if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && false {')
m("config-dir-fallback", "config.go", 'return filepath.Join(home, ".config", "ponytail")', 'return filepath.Join(home, "ponytail")')
m("config-bom-kept", "config.go", 'if json.Unmarshal(bytes.TrimPrefix(data, []byte("\\xef\\xbb\\xbf")), &cfg) != nil {', "if json.Unmarshal(data, &cfg) != nil {")
m("default-env-first", "config.go", 'if env := os.Getenv("PONYTAIL_DEFAULT_MODE"); env != "" && slices.Contains', 'if env := os.Getenv("PONYTAIL_DEFAULT_MODE"); env != "" && false && slices.Contains')
m("default-env-case", "config.go", "return strings.ToLower(env)\n\t}", "return env\n\t}")
m("default-config-case", "config.go", "return strings.ToLower(s)\n\t\t}", "return s\n\t\t}")
m("default-config-review", "config.go", 'isStr && slices.Contains(runtimeModes, strings.ToLower(s))', 'isStr && slices.Contains(validModes, strings.ToLower(s))')
m("flag-env-ignored", "config.go", "if v, set := os.LookupEnv(env); set {", "if v, set := os.LookupEnv(env); set && false {")
m("flag-no-word", "config.go", 'case "", "0", "false", "no":', 'case "", "0", "false":')
m("flag-config-truthy", "config.go", "return cfg[field] == true", "return cfg[field] != nil && cfg[field] != false")
m("flag-env-untrimmed", "config.go", "switch strings.ToLower(jsTrim(v)) {", "switch strings.ToLower(v) {")
m("write-review-allowed", "config.go", "n := normalizeMode(mode)\n\tif n == \"\" {\n\t\treturn \"\", nil\n\t}", "n := normalizePersistedMode(mode)\n\tif n == \"\" {\n\t\treturn \"\", nil\n\t}")
m("write-no-mkdir", "config.go", "if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {", "if err := error(nil); err != nil {")
m("write-sorts-existing-key-last", "config.go", 'if _, has := vals["defaultMode"]; !has {\n\t\tkeys = append(keys, "defaultMode")\n\t}', 'keys = append(slices.DeleteFunc(keys, func(k string) bool { return k == "defaultMode" }), "defaultMode")')
m("write-duplicate-first-wins", "config.go", "vals[k] = raw // a duplicate", "if _, dup := vals[k]; !dup {\n\t\t\tvals[k] = raw\n\t\t} // a duplicate")
m("write-indent", "config.go", 'json.Indent(&out, b.Bytes(), "", "  ")', 'json.Indent(&out, b.Bytes(), "", "    ")')
m("write-bom-kept", "config.go", 'keys, vals = orderedObject(bytes.TrimPrefix(data, []byte("\\xef\\xbb\\xbf")))', "keys, vals = orderedObject(data)")
m("write-not-object-kept", "config.go", "if t, err := dec.Token(); err != nil || t != json.Delim('{') {", "if t, err := dec.Token(); err != nil || (t != json.Delim('{') && false) {")

# instructions.go
m("frontmatter-kept", "instructions.go", 'body = frontmatterRe.ReplaceAllString(body, "")', "")
m("frontmatter-greedy", "instructions.go", "`(?s)^---.*?---`", "`(?s)^---.*---`")
m("table-rows-all-kept", "instructions.go", "if lm := normalizeMode(jsTrim(m[1])); lm != \"\" && lm != effective {\n\t\t\t\tcontinue\n\t\t\t} else if", "if lm := normalizeMode(jsTrim(m[1])); lm != \"\" && lm != effective && false {\n\t\t\t\tcontinue\n\t\t\t} else if")
m("examples-all-kept", "instructions.go", "if lm := normalizeMode(jsTrim(m[1])); lm != \"\" && lm != effective {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t}\n\t\tkept", "if lm := normalizeMode(jsTrim(m[1])); lm != \"\" && lm != effective && false {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t}\n\t\tkept")
m("unquoted-example-dropped", "instructions.go", '`^-` + jsSpace + `*([^:]+):` + jsSpace + `*"`', '`^-` + jsSpace + `*([^:]+):`')
# label-case (normalizeMode(m[1]) without jsTrim) is an equivalent mutant: normalizeMode trims itself, so it is not listed
m("crlf-not-split", "instructions.go", "`\\r?\\n`", "`\\n`")
m("filter-default-mode", "instructions.go", "if effective == \"\" {\n\t\teffective = defaultMode\n\t}\n\tbody =", "if effective == \"\" {\n\t\teffective = \"lite\"\n\t}\n\tbody =")
m("review-pointer", "instructions.go", 'Behavior defined by /ponytail-review skill."', 'Behavior defined by /ponytail-review."')
m("instructions-default-mode", "instructions.go", "if configured == \"\" {\n\t\tconfigured = defaultMode\n\t}", "if configured == \"\" {\n\t\tconfigured = \"ultra\"\n\t}")
m("instructions-header", "instructions.go", '"PONYTAIL MODE ACTIVE — level: " + effective + "\\n\\n"', '"PONYTAIL MODE ACTIVE — level: " + effective + "\\n"')

# extension.go
m("parse-bare-off", "extension.go", 'if fallback == "off" {\n\t\t\tfallback = "full"\n\t\t}', "")
m("parse-fallback-default", "extension.go", "if fallback == \"\" {\n\t\tfallback = defaultMode\n\t}\n\twords", "if fallback == \"\" {\n\t\tfallback = \"lite\"\n\t}\n\twords")
m("parse-default-review", "extension.go", "if m := normalizeMode(second); m != \"\" {", "if m := normalizePersistedMode(second); m != \"\" {")
m("parse-status-prefix", "extension.go", 'case "status":\n\t\treturn command{Type: "status"}', 'case "status":\n\t\treturn command{Type: "invalid"}')
m("parse-invalid-mode-name", "extension.go", 'return command{Type: "invalid", Reason: "invalid-mode", Mode: words[0]}', 'return command{Type: "invalid", Reason: "invalid-mode"}')
m("session-first-entry-wins", "extension.go", "for i := len(list) - 1; i >= 0; i-- {", "for i := 0; i < len(list); i++ {")
m("session-any-custom-type", "extension.go", 'e["type"] != "custom" || e["customType"] != "ponytail-mode"', 'e["customType"] != "ponytail-mode"')
m("session-invalid-entry-stops", "extension.go", "if m := normalizePersistedMode(mode); m != \"\" {\n\t\t\treturn m\n\t\t}\n\t}\n\treturn fallback", "return normalizePersistedMode(mode)\n\t}\n\treturn fallback")
m("session-fallback-default", "extension.go", "if fallback == \"\" {\n\t\tfallback = defaultMode\n\t}\n\tvar list", "if fallback == \"\" {\n\t\tfallback = \"lite\"\n\t}\n\tvar list")
m("status-dot-inverted", "extension.go", 'dot := "○"\n\tif active {\n\t\tdot = "●"\n\t}', 'dot := "●"\n\tif active {\n\t\tdot = "○"\n\t}')
m("status-off-not-cleared", "extension.go", 'if mode == "off" {\n\t\tctx.SetStatus("ponytail", "")\n\t\treturn\n\t}', 'if mode == "off" {\n\t\treturn\n\t}')
m("status-hide-ignored", "extension.go", "if hide || ctx == nil {", "if (hide && false) || ctx == nil {")
m("status-icon", "extension.go", '"full": "⚡"', '"full": "🔥"')
m("setmode-no-entry", "extension.go", 'if ctx != nil {\n\t\t_ = ctx.AppendEntry("ponytail-mode", map[string]any{"mode": n})\n\t}', "")
m("setmode-notice-text", "extension.go", '"Ponytail mode set to %s."', '"Ponytail mode is %s."')
m("deactivation-notifies", "extension.go", 'a.setMode("off", &ctx, false)', 'a.setMode("off", &ctx, true)')
m("alias-never-queues", "extension.go", "if idle, err := ctx.IsIdle(); err == nil && !idle {", "if idle, err := ctx.IsIdle(); err == nil && !idle && false {")
m("alias-skill-name", "extension.go", 'e.Command("ponytail-"+n, "Run /skill:pigpen-ponytail-"+n, a.alias("pigpen-ponytail-"+n))', 'e.Command("ponytail-"+n, "Run /skill:pigpen-ponytail-"+n, a.alias("ponytail-"+n))')
m("input-extension-source", "extension.go", 'if data["source"] == "extension" {', 'if data["source"] == "never" {')
m("input-when-off", "extension.go", "if on && isDeactivationCommand(text) {", "if (on || true) && isDeactivationCommand(text) {")
m("start-default-stale", "extension.go", "a.dflt = getDefaultMode()\n\t\ta.hideStatus", "a.hideStatus")
m("start-quiet-ignored", "extension.go", "if !getQuietStartup() {", "if true {")
m("start-notice-text", "extension.go", '"Ponytail loaded: "+mode', '"Ponytail: "+mode')
m("agent-active-inverted", "extension.go", "for event, active := range map[string]bool{sdk.EventAgentStart: true, sdk.EventAgentEnd: false} {", "for event, active := range map[string]bool{sdk.EventAgentStart: false, sdk.EventAgentEnd: true} {")
m("prompt-off-injected", "extension.go", 'if mode == "" || mode == "off" {', 'if mode == "" {')
m("prompt-base-dropped", "extension.go", "base = s + \"\\n\\n\"", "base = \"\"")
m("prompt-base-separator", "extension.go", "base = s + \"\\n\\n\"", "base = s + \"\\n\"")
m("status-text", "extension.go", 'ctx.Notify(fmt.Sprintf("Ponytail: current %s • default %s", mode, dflt), "info")', 'ctx.Notify(fmt.Sprintf("Ponytail: %s • default %s", mode, dflt), "info")')
m("save-failure-silent", "extension.go", 'ctx.Notify("Failed to save default mode: "+err.Error(), "error")', "")
m("default-override-notice", "extension.go", "if now == written {", "if true {")
m("invalid-notice-level", "extension.go", '"Unknown or unsupported /ponytail mode.", "warning"', '"Unknown or unsupported /ponytail mode.", "info"')
m("default-not-refreshed", "extension.go", "now := getDefaultMode()\n\t\t\t\ta.mu.Lock()\n\t\t\t\ta.dflt = now\n\t\t\t\ta.mu.Unlock()", "now := getDefaultMode()")
m("description-modes", "extension.go", 'return "Set mode: " + strings.Join(runtimeModes, "|")', 'return "Set mode: " + strings.Join(validModes, "|")')

by = {}
for x in M:
    by.setdefault(x["file"], open(os.path.join(root, x["file"])).read())
bad = 0
for x in M:
    n = by[x["file"]].count(x["find"])
    if n != 1:
        print("find not unique/absent (%d): %s in %s" % (n, x["name"], x["file"]), file=sys.stderr); bad += 1
if bad: sys.exit(1)
json.dump(M, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "w"), indent=1, ensure_ascii=False)
print(len(M), "mutations")
