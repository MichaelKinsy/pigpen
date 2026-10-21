#!/usr/bin/env python3
"""Retargets the mutation lists at the merged tree (perm-final-1): the lanes wrote their lists on separate branches, and later merges and perf
changes moved or removed the code some mutants name (pigeq refuses a mutation whose `find` does not occur exactly once). Run from
components/permissions; rewrites port/mutations-*.json in place and prints what it changed. `python3 port/checkfinds.py` is the static check.
RETARGET: name -> (file, find, replace) for code that still exists in another shape. DROP: name -> reason, for code that no longer exists
(the old root gate and its Policy, replaced by the composition root, whose mutants are in mutations-handlers.json; code moved into bashparse)."""
import glob, json, os, sys

RETARGET = {
 "rd-name-boundary-start": ("logredaction.go", "if i == 0 || isSepByte(s[i-1]) {", "if i > 0 && isSepByte(s[i-1]) {"),
 "rd-name-boundary-end": ("logredaction.go", "if j == len(s) || isSepByte(s[j]) || (s[j] == 's' && (j+1 == len(s) || isSepByte(s[j+1]))) {", "if j < len(s) && (isSepByte(s[j]) || (s[j] == 's' && j+1 < len(s) && isSepByte(s[j+1]))) {"),
 "rd-camel": ("logredaction.go", "|| matchesCamelKey(name)", "|| false"),
 "rd-lower": ("logredaction.go", "matchesSensitive(asciiLower(name))", "matchesSensitive(name)"),
 "js-seen-add": ("logjsonsafe.go", "\t\ts.small = append(s.small, id)\n", "\t\t_ = id\n"),
 "js-seen-check": ("logjsonsafe.go", "for _, k := range s.small {\n\t\tif k == id {", "for _, k := range s.small {\n\t\tif k != id {"),
 "js-array-key": ("logjsonsafe.go", "key = strconv.Itoa(i)", 'key = ""'),
 "js-object-order": ("logjsonsafe.go", "writeObjectWith(b, x.orderView(), x.vals, replacer)", "writeObjectWith(b, x.keys, x.vals, replacer)"),
 "ev-recover": ("permissionevents.go", "func EmitReadyEvent(events PermissionEventBus, event PermissionsReadyEvent) {\n\tdefer func() { _ = recover() }()", "func EmitReadyEvent(events PermissionEventBus, event PermissionsReadyEvent) {\n\tdefer func() {}()"),
 "pn-entry-lstat": ("canonicalize.go", "_, err := os.Lstat(p)", "_, err := os.Stat(p)"),
 "surf-members-order": ("pathsurfaces.go", 'm[f] = [2]string{f + "_" + string(capabilityDirections[0]), f + "_" + string(capabilityDirections[1])}', 'm[f] = [2]string{f + "_" + string(capabilityDirections[1]), f + "_" + string(capabilityDirections[0])}'),
 "mcp-pi-units": ("mcptargets.go", "units := utf16.Encode([]rune(name))\n\tfor _, u := range units {", "_ = utf16.Encode\n\tfor _, r := range []rune(name) {\n\t\tu := uint16(r)"),
 "sp-activate-selection": ("sessionpermission.go", "return s.authorizerSelection.Activate(ctx.AuthorityContext)", "return nil"),
 "sp-reset-skills": ("sessionpermission.go", "\ts.mu.Lock()\n\ts.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\treturn s.Activate(ctx)", "\ts.mu.Lock()\n\ts.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\treturn s.Activate(ctx)"),
 "sp-reset-baseline": ("sessionpermission.go", "\ts.skillEntries = nil\n\ts.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\treturn s.Activate(ctx)", "\ts.skillEntries = nil\n\ts.mu.Unlock()\n\treturn s.Activate(ctx)"),
 "sp-reset-activate": ("sessionpermission.go", "s.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\treturn s.Activate(ctx)", "s.toolSurfaceBaseline.reset()\n\ts.mu.Unlock()\n\treturn nil"),
 "sp-preview-input": ("toolinputpreview.go", "ToolInputPreviewMaxLength = 200", "ToolInputPreviewMaxLength = 201"),
 "sp-preview-text": ("toolinputpreview.go", "ToolTextSummaryMaxLength  = 80", "ToolTextSummaryMaxLength  = 81"),
 "ps-mcp-colon-star": ("patternsuggest.go", 'return trimmed[:i], ":*"', 'return trimmed[:i], ":"'),
 "ps-mcp-order": ("patternsuggest.go", 'if i := strings.Index(trimmed, ":"); i > 0 {\n\t\treturn trimmed[:i], ":*"\n\t}\n\tif i := strings.Index(trimmed, "_"); i > 0 {', 'if i := strings.Index(trimmed, "_"); i > 0 {\n\t\treturn trimmed[:i], "_*"\n\t}\n\tif i := strings.Index(trimmed, ":"); i > 0 {'),
 "ps-session-mcp": ("patternsuggest.go", "head, tail := mcpPatternParts(value)", 'head, tail := value, ""'),
 "gt-sibling": ("sessionapprovalgrant.go", "if hasContentsSibling(g, grants) || seen[g.Pattern] {", "if seen[g.Pattern] {"),
 "gt-seen": ("sessionapprovalgrant.go", "if hasContentsSibling(g, grants) || seen[g.Pattern] {", "if hasContentsSibling(g, grants) {"),
 "gt-surface": ("sessionapprovalgrant.go", "if other.Surface != g.Surface {\n\t\t\tcontinue\n\t\t}\n\t\tfor _, suffix := range contentsSuffixes {", "for _, suffix := range contentsSuffixes {"),
 "gt-suffix-win": ("sessionapprovalgrant.go", 'var contentsSuffixes = []string{"/*", "\\\\*"}', 'var contentsSuffixes = []string{"/*"}'),
 "gt-suffix-posix": ("sessionapprovalgrant.go", 'var contentsSuffixes = []string{"/*", "\\\\*"}', 'var contentsSuffixes = []string{"\\\\*"}'),
 "ip-collapse-run": ("toolinputpreview.go", "j := i + size\n\t\tfor j < len(s) {", "j := i + size\n\t\tfor j < len(s) && false {"),
 "ip-collapse-js": ("toolinputpreview.go", "if !isJSSpace(r) {\n\t\t\ti += size\n\t\t\tcontinue\n\t\t}", "if !(r == ' ' || r == '\\n' || r == '\\t' || r == '\\r') {\n\t\t\ti += size\n\t\t\tcontinue\n\t\t}"),
 "sv-deny-reason": ("fwd_server.go", "CreateDeniedPermissionDecision(check.Reason).Attribute(decidedBy)", "CreateDeniedPermissionDecision(nil).Attribute(decidedBy)"),
 "sv-deny-decider": ("fwd_server.go", "CreateDeniedPermissionDecision(check.Reason).Attribute(decidedBy)", "CreateDeniedPermissionDecision(check.Reason).Attribute(DecisionSource{})"),
}
GONE = "the code no longer exists in the merged tree"
DROP_FILES = {  # file of the mutant -> reason
 "extension.go": "extension.go is the composition root now (handlers lane); its mutants are in mutations-handlers.json",
 "config.go": "the old root gate's config.go (Policy) is deleted; the ported config, policy and manager layers have their own lists",
 "rule.go": "the old simple rule engine moved (the ported rule engine has its own list)",
}
DROP_NAMES = {
 "skill-deny-filter": "the old root gate's skill filter in skills.go was replaced by the composition root and the ported exposure code (mutations-handlers.json, mutations-gatecore.json)",
 "fv-label": "FlaggedElements' label comparison was rewritten away by the presentation lane (no `e.Label == label` remains)",
 "effect-unproven": "UnprovenEffect is now an alias of bashparse.UnprovenEffect; the bashparse lists cover the value",
}
ext = "extensions/pi-permission-system/"
def usable(m):
    p = ext + m["file"]
    return os.path.exists(p) and open(p).read().count(m["find"]) == 1
changed = dropped = 0
for f in sorted(glob.glob("port/mutations*.json")):
    ms = json.load(open(f)); out = []
    for m in ms:
        if usable(m): out.append(m); continue
        n = m["name"]
        if n in RETARGET and (f.endswith("handlers.json") is False):
            file, find, repl = RETARGET[n]
            m = dict(m, file=file, find=find, replace=repl)
            assert usable(m), f"{n}: retarget does not occur exactly once in {file}"
            out.append(m); changed += 1; print("retargeted", n); continue
        reason = DROP_NAMES.get(n) or DROP_FILES.get(m["file"]) or (GONE if not os.path.exists(ext + m["file"]) else None)
        if reason is None and os.path.basename(f) == "mutations.json":  # the first list: the refusal-era root gate, rewritten by the handlers lane
            reason = "the old root gate's code in " + m["file"] + " was rewritten by the composition root and the ported layers (their lists cover the behaviour)"
        if reason is None:
            sys.exit(f"unresolved: {f} {n} {m['file']}")
        dropped += 1; print("dropped", f, n, "-", reason)
    if len(out) != len(ms):
        json.dump(out, open(f, "w"), indent=1); open(f, "a").write("\n")
    elif out != ms:
        json.dump(out, open(f, "w"), indent=1); open(f, "a").write("\n")
print(changed, "retargeted,", dropped, "dropped")
