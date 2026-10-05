#!/usr/bin/env python3
"""Generates port/mutations.json: one deliberate defect per behavior. Each `find` must occur exactly once in its file (checked
here), so a mutation never silently applies to the wrong place."""
import json, os, sys

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "pi-subagents")
M = []


def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})


# frontmatter.go
m("fm-fold-blank", "frontmatter.go", "n := blank\n\t\t\t\tif previousMore || currentMore {\n\t\t\t\t\tn++\n\t\t\t\t}", "n := blank")
m("fm-fold-more-indented", "frontmatter.go", "} else if previousMore || currentMore {\n\t\t\t\tfolded.WriteString(\"\\n\")", "} else if false {\n\t\t\t\tfolded.WriteString(\"\\n\")")
m("fm-fold-space", "frontmatter.go", 'folded.WriteString(" ")', 'folded.WriteString("\\n")')
m("fm-quoted", "frontmatter.go", "quoted := len(rawValue) >= 1 &&", "quoted := false &&")
m("fm-literal", "frontmatter.go", 'isLiteral := !quoted && (rawValue == "|" || rawValue == "|-")', 'isLiteral := false')
m("fm-block-blank-lines", "frontmatter.go", "((folded || literal) && trimmed == \"\")", "((folded || literal) && trimmed == \"\" && false)")
m("fm-end-marker", "frontmatter.go", 'strings.Index(normalized[3:], "\\n---")', 'strings.Index(normalized[3:], "\\n--")')
m("fm-crlf", "frontmatter.go", 'strings.ReplaceAll(content, "\\r\\n", "\\n")', "content")
m("fm-list-dash", "frontmatter.go", 'if strings.HasPrefix(value, "-") {', 'if false {')
m("fm-list-comma", "frontmatter.go", 'strings.Split(value, ",")', 'strings.Split(value, ";")')

# chain.go
m("chain-budget-soft", "chain.go", "soft.(float64) > hard.(float64)", "soft.(float64) >= hard.(float64)")
m("chain-budget-hard", "chain.go", "if !isInt(hard, 1) {", "if !isInt(hard, 0) {")
m("chain-inline-schema", "chain.go", 'strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")', 'strings.HasPrefix(raw, "{")')
m("chain-output-false", "chain.go", 'if raw == "false" {\n\t\t\t\tstep.Output', 'if raw == "none" {\n\t\t\t\tstep.Output')
m("chain-outputmode", "chain.go", 'raw == "inline" || raw == "file-only"', 'raw == "inline"')
m("chain-task-trim", "chain.go", 'task = jsTrim(strings.Join(lines[blank+1:], "\\n"))', 'task = strings.Join(lines[blank+1:], "\\n")')
m("chain-needs-name", "chain.go", 'fm.Get("name") == "" || fm.Get("description") == ""', 'fm.Get("name") == ""')
m("chain-extra-fields", "chain.go", 'if k == "name" || k == "package" || k == "description" {', 'if k == "name" || k == "description" {')
m("chain-serialize-package", "chain.go", 'if c.PackageName != "" {\n\t\tlines = append(lines, "package: "', 'if false {\n\t\tlines = append(lines, "package: "')
m("chain-serialize-reads", "chain.go", 'lines = append(lines, "reads: "+strings.Join(s.Reads.Items, ", "))', 'lines = append(lines, "reads: "+strings.Join(s.Reads.Items, ","))')
m("chain-heading-trailing", "chain.go", "return jsTrim(body[e:eol]), k, true\n\t\t}\n\t\tif k == eol", "return body[e:eol], k, true\n\t\t}\n\t\tif k == eol")

# identity.go
m("pkg-lower", "identity.go", "strings.ToLower(v)", "v")
m("pkg-edges", "identity.go", "return packageEdges.ReplaceAllString(s, \"\")", "return s")
m("pkg-pattern", "identity.go", "!identifierPattern.MatchString(name)", "false")
m("pkg-local-name", "identity.go", 'if pkg != "" && strings.HasPrefix(name, pkg+".") {', 'if false {')

# scope.go
m("scope-default", "scope.go", "return ScopeBoth\n}\n\n// MergeAgentsForScope", "return ScopeProject\n}\n\n// MergeAgentsForScope")
m("merge-user-before-project", "scope.go", "put(user)\n\t\tput(project)", "put(project)\n\t\tput(user)")
m("merge-package", "scope.go", "put(builtin)\n\tput(pkg)", "put(pkg)\n\tput(builtin)")
m("resolve-canonical", "scope.go", 'a.Name == raw }, "agent name")', 'false }, "agent name")')
m("resolve-rank", "scope.go", "sourceRank[m.Source] > sourceRank[best.Source]", "sourceRank[m.Source] < sourceRank[best.Source]")
m("resolve-local-guard", "scope.go", 'a.LocalName != "" && a.LocalName == raw', "a.LocalName == raw")

# discovery.go
m("disc-home-stop", "discovery.go", "if r := realDir(cur); r != \"\" && homes[r] {\n\t\t\treturn roots", "if r := realDir(cur); r != \"\" && !homes[r] {\n\t\t\treturn roots")
m("disc-legacy-skills", "discovery.go", "if isLegacySkillPath(dir, p) {", "if false {")
m("disc-chain-md", "discovery.go", '!strings.HasSuffix(n, ".chain.md") }', "true }")
m("disc-prune-git", "discovery.go", "if _, err := os.Stat(filepath.Join(dir, \".git\")); err == nil {\n\t\treturn true", "if false {\n\t\treturn true")
m("disc-preferred-last", "discovery.go", "[]string{legacy, preferred}", "[]string{preferred, legacy}")
m("disc-user-new", "discovery.go", "append(extraUserAgentDirs(), userOld, userNew)", "append(extraUserAgentDirs(), userOld)")
m("disc-git-root", "discovery.go", 'case "git-root":\n\t\t\tpolicyIdx = i', 'case "git-root":\n\t\t\tpolicyIdx = -(i + 1)')
m("disc-user-chains", "discovery.go", "chains, cd := loadChains(r.UserChainDir, SourceUser)", "chains, cd := loadChains(r.UserChainDir+\"x\", SourceUser)")

# agents.go
m("agents-needs-description", "agents.go", 'fm.Get("name") == "" || fm.Get("description") == ""', 'fm.Get("name") == ""')
m("agents-thinking-false", "agents.go", 'if fm.Get("thinking") == "false" {', 'if false {')
m("agents-delegate-append", "agents.go", 'if name == "delegate" {\n\t\treturn "append"', 'if name == "nobody" {\n\t\treturn "append"')
m("agents-alias-self", "agents.go", "seen[a] || a == agentName", "seen[a]")
m("agents-mcp-split", "agents.go", 'strings.HasPrefix(t, "mcp:")', 'strings.HasPrefix(t, "mcp-")')
m("agents-runner-type", "agents.go", "case \"external-cli\":\n\t\tif jsTrim", "case \"external-clx\":\n\t\tif jsTrim")
m("agents-async", "agents.go", 'case "true", "false":\n\t\t\tb := fm.Get("async") == "true"', 'case "true":\n\t\t\tb := fm.Get("async") == "true"')

# management.go
m("list-section-order", "management.go", "{SourceUser, \"User agents\"}, {SourceProject, \"Project agents\"}", "{SourceProject, \"Project agents\"}, {SourceUser, \"User agents\"}")
m("list-sort", "management.go", "sort.SliceStable(agents, func(i, j int) bool { return localeLess(agents[i].Name, agents[j].Name) })", "")
m("list-aliases", "management.go", 'parts = append(parts, "aliases: "+strings.Join(a.Aliases, ", "))', 'parts = append(parts, "alias: "+strings.Join(a.Aliases, ", "))')
m("list-diagnostics", "management.go", 'lines = append(lines, diagnosticLines(d.Diagnostics)...)', "")
m("get-blocking", "management.go", "if len(agents) == 0 || (match != nil && sourceRank[match.Source] > best) {", "if len(agents) == 0 {")
m("get-detail-prompt", "management.go", 'if jsTrim(a.SystemPrompt) != "" {', 'if false {')
m("get-scope-diagnostics", "management.go", "excluded := SourceProject\n\tif scope == ScopeProject {", "excluded := SourceUser\n\tif scope == ScopeProject {")

# run.go
m("run-model-inherit", "run.go", 'model == "" && a.Model != "" && a.Model != "inherit"', 'model == "" && a.Model != ""')
m("run-model-override", "run.go", 'model := modelOverride\n\tif model == "" &&', 'model := ""\n\tif model == "" &&')
m("run-replace-flag", "run.go", 'if a.SystemPromptMode == "append" {', 'if a.SystemPromptMode == "replace" {')
m("run-thinking-off", "run.go", 'args = append(args, "--thinking", "off")', 'args = append(args, "--thinking", "low")')
m("run-no-extensions", "run.go", 'args := []string{"--no-extensions", "--print"}', 'args := []string{"--print"}')
m("chain-default-first", "run.go", "if i == 0 {\n\t\t\tt = \"{task}\"", "if i == 1 {\n\t\t\tt = \"{task}\"")
m("chain-previous", "run.go", '"{task}", request, "{previous}", previous', '"{task}", request, "{previous}", request')
m("chain-resolve-first", "run.go", "resolved := make([]*AgentConfig, len(c.Steps))\n\tfor i, s := range c.Steps {", "resolved := make([]*AgentConfig, len(c.Steps))\n\tfor i, s := range c.Steps[:0] {")
m("chain-stop", "run.go", "\t\t\tvar ce *childExit\n", "\t\t\tcontinue\n\t\t\tvar ce *childExit\n")
m("run-exit", "run.go", "case res.Exit != 0:", "case res.Exit < 0:")
m("run-env-role", "run.go", '"PIG_AGENT_ROLE="+role)', '"PIG_AGENT_ROLE=")')

# run.go: the review's safety (rev-port-popular-5)
m("run-task-argv", "run.go", "Args: buildChildArgs(a, model), Stdin: childPrompt(task)", "Args: append(buildChildArgs(a, model), task), Stdin: childPrompt(task)")
m("run-prompt-prefix", "run.go", 'return "Task: " + task', "return task")
m("run-suffix-wins", "run.go", "if !hasThinkingSuffix(modelOverride) {", "if true {")
m("run-suffix-level", "run.go", "return i >= 0 && thinkingLevels[model[i+1:]]", "return i >= 0")
m("run-env-ext", "run.go", 'strings.HasPrefix(k, "PIG_EXT_"), ', "")
m("run-env-harness", "run.go", 'strings.HasPrefix(k, "PIG_HARNESS_"), ', "")
m("run-env-depth", "run.go", '"PIG_SUBAGENT_DEPTH="+strconv.Itoa(depth+1)', '"PIG_SUBAGENT_DEPTH="+strconv.Itoa(depth)')
m("run-env-exact", "run.go", "\tcmd.Env = req.Env\n", "\tcmd.Env = append(os.Environ(), req.Env...)\n")
m("run-dir", "run.go", "\tcmd.Dir = req.Cwd\n", "")
m("run-stdin", "run.go", "\tcmd.Stdin = strings.NewReader(req.Stdin)\n", "")
m("run-stderr-apart", "run.go", "cmd.Stdout, cmd.Stderr = stdout, stderr", "cmd.Stdout, cmd.Stderr = stdout, stdout")
m("run-exit-code", "run.go", "res.Exit = ee.ExitCode()", "res.Exit = 0")
m("run-cancel", "run.go", "\tcase <-done:\n\t\tcancelled = true", "\tcase <-make(chan struct{}):\n\t\tcancelled = true")
m("run-cancel-term", "run.go", "\t\tterminate(cmd.Process)\n", "")
m("run-cancel-kill", "run.go", "\t\t\t_ = cmd.Process.Kill()\n", "")
m("run-cancel-error", "run.go", "\tif cancelled {\n\t\treturn res, errCancelled", "\tif cancelled && false {\n\t\treturn res, errCancelled")
m("run-out-bytes", "run.go", "if w.total <= w.max && lines <= outputMaxLines {", "if lines <= outputMaxLines {")
m("run-out-lines", "run.go", "kept = strings.Join(parts[:outputMaxLines], \"\\n\")", "_ = parts")
m("run-out-rune", "run.go", "\t\tkept = kept[:len(kept)-1]\n", "\t\tbreak\n")
m("run-out-marker", "run.go", "strings.Count(kept, \"\\n\")+1, lines,", "strings.Count(kept, \"\\n\"), lines,")
m("run-stderr-tail", "run.go", "if len(w.buf) > w.max {", "if false {")
m("run-harness-bin", "run.go", '"PIG_SUBAGENT_PIG_BINARY", "PIG_HARNESS_BINARY"', '"PIG_SUBAGENT_PIG_BINARY"')
m("run-depth-env", "run.go", 'if n, ok := nonNegativeInt(getenv("PIG_SUBAGENT_DEPTH")); ok {', 'if n, ok := nonNegativeInt(""); ok {')
m("run-depth-flag", "run.go", "} else if getenv(\"PIG_SUBAGENT\") != \"\" {\n\t\tdepth = 1", "} else if false {\n\t\tdepth = 1")
m("run-depth-max", "run.go", 'if n, ok := nonNegativeInt(getenv("PI_SUBAGENT_MAX_DEPTH")); ok {', 'if n, ok := nonNegativeInt(""); ok {')
m("run-depth-negative", "run.go", "return n, err == nil && n >= 0", "return n, err == nil")
m("run-depth-compare", "run.go", "if depth < max {", "if depth <= max {")
m("run-cap", "run.go", "case childSlots <- struct{}{}:", "default:")
m("run-cap-cancel", "run.go", "\tcase <-done:\n\t\treturn \"\", fmt.Errorf(\"%s was cancelled\", a.Name)", "\tcase <-make(chan struct{}):\n\t\treturn \"\", fmt.Errorf(\"%s was cancelled\", a.Name)")
m("run-fail-stderr", "run.go", "\t\tdetail := res.Stderr\n", "\t\tdetail := \"\"\n")
m("run-cwd-relative", "run.go", "effective = filepath.Join(base, requested)", "effective = requested")
m("run-cwd-missing", "run.go", "case errors.Is(err, fs.ErrNotExist):", "case errors.Is(err, fs.ErrNotExist) && false:")
m("run-cwd-file", "run.go", "case !st.IsDir():", "case !st.IsDir() && false:")
m("run-cwd-resolution", "run.go", "if requested != effective {", "if false {")
m("run-either-b", "run.go", "\t\tcase <-b:\n", "")

# extension.go
m("ext-widget-clear", "extension.go", "_ = ctx.SetWidget(asyncWidget, nil)\n\t\treturn nil, nil\n\t})\n\te.OnEvent(sdk.EventBeforeAgentStart", "return nil, nil\n\t})\n\te.OnEvent(sdk.EventBeforeAgentStart")
m("ext-git-probe", "extension.go", '"rev-parse", "--show-toplevel"', '"rev-parse", "--git-dir"')
m("ext-deselect", "extension.go", "_ = setSelection(ctx, false)\n\t\t// The original asks git", "// The original asks git")
m("ext-enable-append", "extension.go", "if include {\n\t\tnext = append(next, subagentName)\n\t}", "")
m("ext-loader-active", "extension.go", "if !has {\n\t\tnext = append(next, loaderName)\n\t}", "")
m("ext-dedupe", "extension.go", "if !seen[n] {\n\t\t\tseen[n] = true\n\t\t\tuniq = append(uniq, n)\n\t\t}", "seen[n] = true\n\t\tuniq = append(uniq, n)")
m("ext-details", "extension.go", '"mode": "management", "results": []any{}', '"mode": "management"')
m("ext-refuse", "extension.go", "return nil, sdk.NewToolError(r.text)", "return textResult(r.text, true), nil")
m("ext-single-needs-task", "extension.go", 'return name, task, name != "" && task != ""', 'return name, task, name != ""')
# extension.go: the run entry points (review rev-port-popular-5)
m("ext-run-depth", "extension.go", "\tif msg := nestedBlocked(os.Getenv); msg != \"\" {\n\t\treturn nil, sdk.NewToolError(msg)", "\tif msg := nestedBlocked(os.Getenv); false {\n\t\treturn nil, sdk.NewToolError(msg)")
m("ext-chain-depth", "extension.go", "\tif msg := nestedBlocked(os.Getenv); msg != \"\" {\n\t\tctx.Notify(msg, \"error\")", "\tif msg := nestedBlocked(os.Getenv); false {\n\t\tctx.Notify(msg, \"error\")")
m("ext-run-cwd", "extension.go", "cwd, err := resolveRunCwd(ctx.Cwd(), requested)", "cwd, err := resolveRunCwd(ctx.Cwd(), requested[:0])")
m("ext-run-scope", "extension.go", 'scope := ResolveExecutionAgentScope(params["agentScope"])', "scope := ResolveExecutionAgentScope(nil)")
m("ext-run-model", "extension.go", "out, err := runAgent(done, cwd, a, task, model)", "out, err := runAgent(done, cwd, a, task, \"\")\n\t_ = model")
m("ext-chain-task", "extension.go", "agents, jsTrim(fields[1]), func", "agents, fields[1], func")
m("ext-chain-answer", "extension.go", "\tctx.Notify(out, \"info\")\n", "\tctx.Notify(out[:0], \"info\")\n")
m("ext-chain-last-wins", "extension.go", "\t\t\tfound = &chains[i]\n", "\t\t\tif found == nil {\n\t\t\t\tfound = &chains[i]\n\t\t\t}\n")

bad = False
for x in M:
    text = open(os.path.join(root, x["file"])).read()
    n = text.count(x["find"])
    if n != 1:
        print("find occurs", n, "times:", x["name"], file=sys.stderr)
        bad = True
if bad:
    sys.exit(1)
json.dump(M, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "w"), indent=1, ensure_ascii=False)
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "a").write("\n")
print(len(M), "mutations")
