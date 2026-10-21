#!/usr/bin/env python3
"""Generates port/mutations-bashprogram.json: one deliberate defect per behavior of BashProgram and BashPathResolver. Same format and
check as gen-mutations.py (each `find` must occur exactly once in its file); kept apart so the lanes' generators do not collide.
Run `pigeq mutate --unit-only --go extensions/pi-permission-system --mutations port/mutations-bashprogram.json --sdk-dir <PiG sdk>`."""
import json, os

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "pi-permission-system")
M = []


def m(name, file, find, replace):
    M.append({"name": "bashprogram-" + name, "file": file, "find": find, "replace": replace})


R = "bashpathresolver.go"
P = "bashprogram.go"

# program.go
m("program-no-salvage-roots", P, "roots := append([]*bashparse.Node{tree.Root}, salvaged...)", "roots := []*bashparse.Node{tree.Root}")
m("program-no-salvaged-commands", P, "units = append(units, bashanalysis.CollectSalvagedCommands(root, words)...)", "_ = root")
m("program-salvaged-not-walked", P, "Resolve(tree.Root, salvaged)", "Resolve(tree.Root, nil)")
m("program-workdir-dropped", P, "workdir = options.Workdir", "workdir = nil")
m("program-commands-shared", P, "return append([]BashCommand{}, p.commandUnits...)", "return p.commandUnits")
m("program-externals-shared", P, "return append([]BashExternalPath{}, p.externals...)", "return p.externals")
m("program-candidates-shared", P, "return append([]BashPathRuleCandidate{}, p.candidates...)", "return p.candidates")
m("program-unparsed-keeps-text", P, "return &BashProgram{sourceCommand: command}, nil\n\t}\n\tvar workdir", "return &BashProgram{}, nil\n\t}\n\tvar workdir")
m("program-parser-error", P, "if err != nil {\n\t\treturn nil, err\n\t}\n\ttree", "if err != nil {\n\t\treturn &BashProgram{}, nil\n\t}\n\ttree")

# resolver: seed, workdir
m("resolve-no-seed", R, "initialBase = r.deriveBaseFromCdTarget(cwdBase, *r.workdir)", "initialBase = cwdBase")
m("resolve-salvage-known-base", R, "r.walkForCandidates(salvaged, unknownBase, &collected)", "r.walkForCandidates(salvaged, initialBase, &collected)")
m("resolve-rebound-home-kept", R, "if !r.words.SpellsReboundHome(c.token) {", "if true {")
m("resolve-externals-skip-workdir", R, "ExternalAccesses: r.withWorkdirExternal(r.projectExternalPaths(candidates)),", "ExternalAccesses: r.projectExternalPaths(candidates),")
m("workdir-always-external", R, "if !isExternal {\n\t\treturn append([]BashExternalPath{}, tokenExternals...)", "if !isExternal && false {\n\t\treturn append([]BashExternalPath{}, tokenExternals...)")
m("workdir-no-canonical-gate", R, "canonical := wdPath.BoundaryValue()\n\tisExternal := true\n\tif canonical != \"\" {\n\t\tisExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(canonical)\n\t}\n", "canonical := wdPath.BoundaryValue()\n\tisExternal := true\n")
m("workdir-literal-only-internal", R, "canonical := wdPath.BoundaryValue()\n\tisExternal := true", "canonical := wdPath.BoundaryValue()\n\tisExternal := false")
m("workdir-key-lexical", R, "key := canonical\n\tif key == \"\" {\n\t\tkey = wdPath.Value()\n\t}\n\tfor", "key := wdPath.Value()\n\tfor")
m("workdir-dedup-lexical", R, "identity := e.Path.BoundaryValue()\n\t\tif identity == \"\" {\n\t\t\tidentity = e.Path.Value()\n\t\t}\n\t\tif identity == key {", "if e.Path.Value() == key {")
m("workdir-no-dedup", R, "if identity == key {\n\t\t\treturn", "if false {\n\t\t\treturn")
m("workdir-effect-read", R, "{Path: wdPath, Effect: UnprovenEffect}}", "{Path: wdPath, Effect: TokenEffect{Effect: EffectRead, Source: SourceCore}}}")
m("workdir-appended", R, "return append([]BashExternalPath{{Path: wdPath, Effect: UnprovenEffect}}, tokenExternals...)", "return append(append([]BashExternalPath{}, tokenExternals...), BashExternalPath{Path: wdPath, Effect: UnprovenEffect})")
m("workdir-token-resolved", R, "wdPath := r.normalizer.ForBashToken(*r.workdir, nil)", "wdPath := r.normalizer.ForPath(*r.workdir, nil)")

# resolver: walk
m("walk-program-not-sequence", R, 'case "program", "list", "redirected_statement":', 'case "program", "list":')
m("walk-list-not-sequence", R, 'case "program", "list", "redirected_statement":', 'case "program", "redirected_statement":')
m("walk-command-no-fold", R, "return r.foldCd(node, base)\n\tcase \"pipeline\":", "return base\n\tcase \"pipeline\":")
m("walk-pipeline-default", R, "return r.walkPipeline(node, base, out)\n\tcase \"subshell\":", "tagTokens(bashanalysis.CollectPathCandidateTokens(node, r.words), base, out)\n\t\treturn base\n\tcase \"subshell\":")
m("walk-subshell-leaks", R, "r.walkCurrentShellSequence(node, base, out)\n\t\treturn base\n\tcase \"compound_statement\":", "return r.walkCurrentShellSequence(node, base, out)\n\tcase \"compound_statement\":")
m("walk-brace-group-isolated", R, "// folded base.\n\t\treturn r.walkCurrentShellSequence(node, base, out)", "// folded base.\n\t\tr.walkCurrentShellSequence(node, base, out)\n\t\treturn base")
m("seq-background-folds", R, "if !isBackgrounded(seqNode, i) {\n\t\t\tcurrent = after\n\t\t}", "current = after")
m("seq-never-folds", R, "if !isBackgrounded(seqNode, i) {\n\t\t\tcurrent = after\n\t\t}", "_ = after")
m("background-operator", R, 'return next.Type() == "&"', 'return next.Type() == "&&"')
m("pipeline-first-subshell", R, "current = r.foldPipelineFirstStage(child, current, out)\n\t\t\tfirst = false", "tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)\n\t\t\tfirst = false")
m("pipeline-downstream-folds", R, "// Downstream stage (after a `|`): subshell. Collect against the folded base, do not fold.\n\t\ttagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)", "current = r.walkForCandidates(child, current, out)")
m("pipeline-downstream-base", R, "tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)\n\t}\n\treturn current", "tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), base, out)\n\t}\n\treturn current")
m("pipeline-returns-input", R, "tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)\n\t}\n\treturn current", "tagTokens(bashanalysis.CollectPathCandidateTokens(child, r.words), current, out)\n\t}\n\treturn base")
m("first-stage-list", R, 'if node.Type() == "list" {\n\t\treturn r.foldListExceptTerminal', 'if node.Type() == "listx" {\n\t\treturn r.foldListExceptTerminal')
m("first-stage-redirected", R, 'if node.Type() == "redirected_statement" {\n\t\tcurrent := base', 'if node.Type() == "redirected_statementx" {\n\t\tcurrent := base')
m("first-stage-bare-folds", R, "// Bare `command` or any other shape: a true subshell first stage.\n\ttagTokens(bashanalysis.CollectPathCandidateTokens(node, r.words), base, out)\n\treturn base", "// Bare `command` or any other shape: a true subshell first stage.\n\ttagTokens(bashanalysis.CollectPathCandidateTokens(node, r.words), base, out)\n\treturn r.foldCd(node, base)")
m("list-terminal-folds", R, "if i < len(namedChildren)-1 {", "if i < len(namedChildren) {")
m("list-terminal-skipped", R, "if i < len(namedChildren)-1 {", "if i < len(namedChildren)-2 {")

# cd folding
m("cd-name", R, 'name != "cd" {', 'name != "cdx" {')
m("cd-nonliteral-keeps-base", R, "if !ok {\n\t\treturn unknownBase\n\t}\n\treturn r.deriveBaseFromCdTarget(base, target)", "if !ok {\n\t\treturn base\n\t}\n\treturn r.deriveBaseFromCdTarget(base, target)")
m("cd-absolute-unknown", R, 'case "absolute":\n\t\treturn effectiveBase{known: true, offset: interpreted.Value}', 'case "absolute":\n\t\treturn unknownBase')
m("cd-absolute-joins", R, 'case "absolute":\n\t\treturn effectiveBase{known: true, offset: interpreted.Value}', 'case "absolute":\n\t\treturn effectiveBase{known: true, offset: r.normalizer.JoinBase(base.offset, interpreted.Value)}')
m("cd-relative-recovers", R, 'case "relative":\n\t\tif !base.known {\n\t\t\treturn unknownBase\n\t\t}', 'case "relative":\n\t\tif !base.known {\n\t\t\treturn effectiveBase{known: true, offset: target}\n\t\t}')
m("cd-relative-target", R, "offset: r.normalizer.JoinBase(base.offset, target)}", "offset: r.normalizer.JoinBase(base.offset, interpreted.Value)}")
m("cd-relative-replaces", R, "offset: r.normalizer.JoinBase(base.offset, target)}", "offset: target}")
m("cd-unknown-kind-known", R, "\treturn unknownBase\n}\n\n// ── Projection", "\treturn base\n}\n\n// ── Projection")
m("cd-prefix-types", R, "if bashanalysis.CommandPrefixTypes[child.Type()] {\n\t\t\tcontinue\n\t\t}\n\t\t// A redirect", "// A redirect")
m("cd-redirect-types", R, "if bashparse.RedirectNodeTypes[child.Type()] {\n\t\t\tcontinue\n\t\t}\n\t\tif !child.IsNamed()", "if !child.IsNamed()")
m("cd-dashdash", R, 'if child.Type() == "word" && child.Text() == "--" {', 'if child.Type() == "word" && child.Text() == "---" {')
m("cd-no-target", R, "\t\treturn literalTextOf(child)\n\t}\n\treturn \"\", false", "\t\treturn literalTextOf(child)\n\t}\n\treturn \".\", true")
m("literal-dash", R, 'if text == "-" || strings.HasPrefix(text, "~") {', 'if strings.HasPrefix(text, "~") {')
m("literal-tilde", R, 'if text == "-" || strings.HasPrefix(text, "~") {', 'if text == "-" {')
m("literal-raw-strip", R, "return text[1 : len(text)-1], true", "return text, true")
m("literal-concat-null", R, "part, ok := literalTextOf(child)\n\t\t\tif !ok {\n\t\t\t\treturn \"\", false\n\t\t\t}", "part, _ := literalTextOf(child)")
m("literal-string-quote", R, "if child.Type() == `\"` {\n\t\t\t\tcontinue\n\t\t\t}", "")
m("literal-string-content", R, 'if child.Type() != "string_content" {\n\t\t\t\treturn "", false\n\t\t\t}', "")
m("literal-default", R, "result.WriteString(child.Text())\n\t\t}\n\t\treturn result.String(), true\n\t}\n\treturn \"\", false\n}", "result.WriteString(child.Text())\n\t\t}\n\t\treturn result.String(), true\n\t}\n\treturn node.Text(), true\n}")

# projection: external
m("ext-role-admits", R, "func admittedByRole(role bashanalysis.TokenRole) bool {\n\treturn role == bashanalysis.RoleRedirectDestination", "func admittedByRole(role bashanalysis.TokenRole) bool {\n\treturn false")
m("ext-role-all", R, "return role == bashanalysis.RoleRedirectDestination", "return role != bashanalysis.RoleOperand || true")
m("ext-strict-classifier", R, "candidate, _ = bashanalysis.ClassifyTokenAsPathCandidate(c.token)", "candidate, _ = bashanalysis.ClassifyTokenAsRuleCandidate(c.token, r.normalizer.Flavor)")
m("ext-empty-candidate", R, "// JavaScript's `!candidate`: null and the empty string alike.\n\t\tif candidate == \"\" {", "// JavaScript's `!candidate`: null and the empty string alike.\n\t\tif false {")
m("ext-no-probe", R, "if probed := r.probeBareToken(c.token, c.base); probed != nil {\n\t\t\t\tr.collectIfExternal", "if probed := r.probeBareToken(c.token, c.base); probed != nil && false {\n\t\t\t\tr.collectIfExternal")
m("ext-probe-effect", R, "r.collectIfExternal(probed.path, c.effect, seen, &externalPaths)", "r.collectIfExternal(probed.path, UnprovenEffect, seen, &externalPaths)")
m("ext-unknown-relative", R, "if !c.base.known && r.isRelativeCandidate(candidate) {", "if false {")
m("ext-unknown-all", R, "if !c.base.known && r.isRelativeCandidate(candidate) {", "if !c.base.known {")
m("ext-unknown-safe", R, "if canonical != \"\" && !IsSafeSystemPath(canonical) {", "if canonical != \"\" {")
m("ext-unknown-forliteral", R, "accessPath := r.normalizer.ForPath(candidate, nil)\n\t\t\tcanonical", "accessPath := r.normalizer.ForLiteral(candidate)\n\t\t\tcanonical")
m("ext-unknown-key", R, "recordExternal(accessPath, c.effect, canonical, seen, &externalPaths)", "recordExternal(accessPath, c.effect, accessPath.Value(), seen, &externalPaths)")
m("ext-base-ignored", R, "if c.base.known {\n\t\t\trb := r.normalizer.ResolveBase(c.base.offset)\n\t\t\tresolveBase = &rb\n\t\t}\n\t\tr.collectIfExternal", "r.collectIfExternal")
m("ext-base-bash-token", R, "r.collectIfExternal(r.normalizer.ForBashToken(candidate, resolveBase), c.effect, seen, &externalPaths)", "r.collectIfExternal(r.normalizer.ForPath(candidate, resolveBase), c.effect, seen, &externalPaths)")
m("ext-literal-internal", R, "isExternal := true\n\tif canonical != \"\" {\n\t\tisExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(canonical)\n\t}\n\tif !isExternal {\n\t\treturn\n\t}\n\tkey", "isExternal := false\n\tif canonical != \"\" {\n\t\tisExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(canonical)\n\t}\n\tif !isExternal {\n\t\treturn\n\t}\n\tkey")
m("ext-boundary-lexical", R, "isExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(canonical)\n\t}\n\tif !isExternal {\n\t\treturn\n\t}\n\tkey", "isExternal = r.normalizer.IsBoundaryOutsideWorkingDirectory(lexical)\n\t}\n\tif !isExternal {\n\t\treturn\n\t}\n\tkey")
m("ext-key-lexical", R, "key := canonical\n\tif key == \"\" {\n\t\tkey = lexical\n\t}\n\trecordExternal", "key := lexical\n\trecordExternal")
m("ext-key-canonical-only", R, "key := canonical\n\tif key == \"\" {\n\t\tkey = lexical\n\t}\n\trecordExternal", "key := canonical\n\trecordExternal")
m("ext-record-no-merge", R, "(*out)[index] = BashExternalPath{Path: existing.Path, Effect: MergeTokenEffects(existing.Effect, effect)}", "(*out)[index] = BashExternalPath{Path: existing.Path, Effect: effect}")
m("ext-record-keeps-new-path", R, "(*out)[index] = BashExternalPath{Path: existing.Path, Effect: MergeTokenEffects(existing.Effect, effect)}", "(*out)[index] = BashExternalPath{Path: accessPath, Effect: MergeTokenEffects(existing.Effect, effect)}")
m("ext-record-duplicates", R, "index, found := seen[dedupKey]\n\tif !found {", "index, found := seen[dedupKey]\n\tif !found || true {")

# projection: rule candidates
m("rule-role-ignored", R, "shaped, ok := c.token, true\n\t\tif !admittedByRole(c.role) {", "shaped, ok := c.token, true\n\t\tif true {")
m("rule-strict-classifier", R, "shaped, ok = bashanalysis.ClassifyTokenAsRuleCandidate(c.token, r.normalizer.Flavor)", "shaped, ok = bashanalysis.ClassifyTokenAsPathCandidate(c.token)")
m("rule-posix-flavor", R, "ClassifyTokenAsRuleCandidate(c.token, r.normalizer.Flavor)", "ClassifyTokenAsRuleCandidate(c.token, PosixPathFlavor)")
m("rule-no-probe", R, "if !ok {\n\t\t\tprobed = r.probeBareToken(c.token, c.base)", "if !ok {\n\t\t\tprobed = nil")
m("rule-key-first", R, 'key := strings.Join(matchValues, "\\x00")', "key := matchValues[0]")
m("rule-no-merge", R, "result[index].Effect = MergeTokenEffects(result[index].Effect, c.effect)", "result[index].Effect = c.effect")
m("rule-merge-keeps-first", R, "result[index].Effect = MergeTokenEffects(result[index].Effect, c.effect)", "_ = index")
m("rule-no-dedup", R, "if index, found := seen[key]; found {", "if index, found := seen[key]; found && false {")
m("rule-effect-dropped", R, "result = append(result, BashPathRuleCandidate{Token: probed.token, Path: probed.path, Effect: c.effect})", "result = append(result, BashPathRuleCandidate{Token: probed.token, Path: probed.path, Effect: UnprovenEffect})")

# probe
m("probe-shape", R, "bare, ok := bashanalysis.ClassifyBareTokenCandidate(token)\n\tif !ok || !base.known {", "bare, ok := token, true\n\tif !ok || !base.known {")
m("probe-unknown-base", R, "if !ok || !base.known {\n\t\treturn nil\n\t}\n\trb", "if !ok {\n\t\treturn nil\n\t}\n\trb")
m("probe-base-ignored", R, "rb := r.normalizer.ResolveBase(base.offset)\n\tpath := r.normalizer.ForBashToken(bare, &rb)", "path := r.normalizer.ForBashToken(bare, nil)")
m("probe-exists", R, "if lexical == \"\" || !r.normalizer.EntryExists(lexical) {", "if lexical == \"\" {")
m("probe-lexical", R, "if lexical == \"\" || !r.normalizer.EntryExists(lexical) {", "if lexical == \"\" || !r.normalizer.EntryExists(path.BoundaryValue()) {")
m("probe-returns-token", R, "return &probedToken{token: bare, path: path}", "return &probedToken{token: token + \"x\", path: path}")
m("rulepath-unknown-literal", R, "if !base.known && r.isRelativeCandidate(candidate) {\n\t\treturn r.normalizer.ForLiteral(NormalizePathPolicyLiteral(candidate))", "if !base.known && r.isRelativeCandidate(candidate) {\n\t\treturn r.normalizer.ForPath(candidate, nil)")
m("rulepath-unknown-unnormalized", R, "return r.normalizer.ForLiteral(NormalizePathPolicyLiteral(candidate))", "return r.normalizer.ForLiteral(candidate)")
m("rulepath-unknown-all", R, "if !base.known && r.isRelativeCandidate(candidate) {\n\t\treturn r.normalizer.ForLiteral", "if !base.known {\n\t\treturn r.normalizer.ForLiteral")
m("rulepath-base", R, "var resolveBase *string\n\tif base.known {\n\t\trb := r.normalizer.ResolveBase(base.offset)\n\t\tresolveBase = &rb\n\t}\n\treturn r.normalizer.ForBashToken(candidate, resolveBase)", "return r.normalizer.ForBashToken(candidate, nil)")
m("rulepath-bash-token", R, "return r.normalizer.ForBashToken(candidate, resolveBase)\n}", "return r.normalizer.ForPath(candidate, resolveBase)\n}")
m("relative-absolute", R, "return !r.normalizer.IsAbsolute(candidate) && !strings.HasPrefix(candidate, \"~\")", "return !strings.HasPrefix(candidate, \"/\") && !strings.HasPrefix(candidate, \"~\")")
m("relative-home", R, "return !r.normalizer.IsAbsolute(candidate) && !strings.HasPrefix(candidate, \"~\")", "return !r.normalizer.IsAbsolute(candidate)")
m("tag-effect", R, "pathCandidate{token: t.Token, base: base, effect: t.Effect, role: t.Role}", "pathCandidate{token: t.Token, base: base, effect: UnprovenEffect, role: t.Role}")
m("tag-role", R, "pathCandidate{token: t.Token, base: base, effect: t.Effect, role: t.Role}", "pathCandidate{token: t.Token, base: base, effect: t.Effect, role: bashanalysis.RoleOperand}")
m("tag-base", R, "pathCandidate{token: t.Token, base: base, effect: t.Effect, role: t.Role}", "pathCandidate{token: t.Token, base: cwdBase, effect: t.Effect, role: t.Role}")
m("pipe-redirect-base", R, "tagTokens(bashanalysis.CollectRedirectTokens(child, r.words), current, out)", "tagTokens(bashanalysis.CollectRedirectTokens(child, r.words), base, out)")

# Mutants that survive the whole suite (twins, extras and the complete differential) and are equivalent, with the reason. They are not
# in mutations-bashprogram.json. The two skip-type and fold arguments rest on the grammar's tree shapes (checked on 83,300 inputs),
# `rule-key-separator` is argued and not proven.
EQUIVALENT = {'walk-default-folds': 'foldCd acts only on a `command` node (it reads its command_name child); the default branch never receives one', 'seq-unnamed-children': 'an unnamed child (`&&`, `;`, `&`) yields no tokens through CollectPathCandidateTokens and folds nothing', 'seq-skip-types': 'comment, heredoc_body and heredoc_end are not children of a sequence in any tree of the corpus (the grammar and HoistHeredocTails put them elsewhere); a comment there would collect no tokens anyway', 'background-named': '`&` is never a named node, so the extra check cannot change the answer', 'pipeline-skip-types': 'a comment child of a pipeline collects no tokens and folds nothing, as the first stage or a downstream one', 'first-stage-redirect-fold': 'the redirect falls to the default branch, which collects it against the same base the fold would have used', 'first-stage-redirect-unnamed': 'an unnamed child of a redirected_statement yields no tokens', 'list-skip-types': 'a comment inside a first-stage list is not produced by the grammar (the comment ends the line, so the pipeline cannot continue); not observed in the corpus', 'cd-name-missing-folds': 'an undeterminable name reads as the empty string, which is not `cd`', 'cd-unnamed-children': 'a `command` node has no unnamed children that reach the operand scan', 'cd-arg-types': 'literalTextOf answers null for every type outside the argument types, so the guard before it is redundant', 'literal-raw-length': 'a raw_string is never shorter than its two quotes: an unterminated one parses as an ERROR node', 'literal-raw-suffix': 'a raw_string always ends with its quote: an unterminated one parses as an ERROR node', 'ext-unknown-no-canonical': 'ForPath of a non-empty relative candidate always has a canonical form, so the empty-canonical guard is unreachable', 'ext-lexical-empty': 'every token that reaches collectIfExternal is non-empty, so its lexical form is non-empty (the probe path guards empty itself)', 'rule-probe-token': 'ClassifyTokenAsRuleCandidate returns its input, so shaped equals the token', 'rule-empty-values': "every access path of a non-empty token has at least one match value; the guard is the original's defensive check", 'rule-key-separator': 'a collision needs two match-value lists whose concatenations agree; the forms the path layer produces (absolute, relative, raw, canonical) do not construct one (argued, not proven)'}

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations-bashprogram.json")
bad = 0
for mu in M:
    src = open(os.path.join(root, mu["file"])).read()
    n = src.count(mu["find"])
    if n != 1:
        print("find occurs %d times: %s" % (n, mu["name"]))
        bad += 1
if bad:
    raise SystemExit(1)
json.dump(M, open(out, "w"), indent=1)
print("%d mutations written to %s" % (len(M), out))
