#!/usr/bin/env python3
"""Generates port/mutations.json for the web tools and checks that every find string is unique in its file."""
import json, os, sys

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "rpiv-web-tools")
M = []


def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})


# providers.go: request shapes, response mapping, errors
m("brave-count-param", "providers.go", 'q.Set("count", fmt.Sprint(maxResults))', 'q.Set("limit", fmt.Sprint(maxResults))')
m("brave-token-header", "providers.go", '"X-Subscription-Token": p.apiKey', '"Authorization": "Bearer " + p.apiKey')
m("tavily-key-in-body", "providers.go", 'ordered{{"api_key", p.apiKey}, {"query", query}, {"max_results", maxResults}}', 'ordered{{"query", query}, {"max_results", maxResults}}')
m("tavily-extract-endpoint", "providers.go", '"https://api.tavily.com/extract"', '"https://api.tavily.com/search"')
m("tavily-failed-results-ignored", "providers.go", 'if failed := asList(data["failed_results"]); len(failed) > 0 {', 'if failed := asList(data["failed_results"]); len(failed) > 100 {')
m("serper-num-field", "providers.go", 'ordered{{"q", query}, {"num", maxResults}}', 'ordered{{"q", query}, {"limit", maxResults}}')
m("serper-link-field", "providers.go", 'key("title"), key("link"), key("snippet")', 'key("title"), key("url"), key("snippet")')
m("exa-snippet-limit", "providers.go", '{"maxCharacters", 300}', '{"maxCharacters", 3000}')
m("exa-fetch-limit", "providers.go", '{"maxCharacters", 1000000}', '{"maxCharacters", 100000}')
m("exa-fetch-title-dropped", "providers.go", 'Title: asStr(first["title"]), ContentType: "text/plain"}, nil\n}\n\n// ---- You.com', 'ContentType: "text/plain"}, nil\n}\n\n// ---- You.com')
m("youcom-snippet-order", "providers.go", 'if s := asList(m["snippets"]); len(s) > 0 && s[0] != nil {\n\t\t\treturn asStr(s[0])\n\t\t}\n\t\treturn asStr(m["description"])', 'if d := asStr(m["description"]); d != "" {\n\t\t\treturn d\n\t\t}\n\t\treturn asStr(asList(m["snippets"])[0])')
m("youcom-fetch-format", "providers.go", '{"formats", []string{"markdown"}}})\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\traw, err := decodeJSON(res)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\tvar item', '{"formats", []string{"html"}}})\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\traw, err := decodeJSON(res)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\tvar item')
m("jina-search-url-unencoded", "providers.go", 'u, _ := url.Parse("https://s.jina.ai/" + encodeURIComponent(query))', 'u, _ := url.Parse("https://s.jina.ai/" + query)')
m("jina-results-not-capped", "providers.go", "if len(rs) > maxResults {\n\t\trs = rs[:maxResults]\n\t}\n\treturn &searchResponse{query, rs}, nil", "return &searchResponse{query, rs}, nil")
m("jina-nested-results-lost", "providers.go", 'items = asList(asObj(data)["results"])', "items = nil")
m("jina-empty-body-accepted", "providers.go", 'if jsTrim(string(data)) == "" {', 'if len(data) == 0 {')
m("firecrawl-success-ignored", "providers.go", 'if ok, _ := data["success"].(bool); !ok {', 'if ok, _ := data["success"].(bool); !ok && false {')
m("firecrawl-error-text", "providers.go", 'msg = "scrape failed"', 'msg = "failed"')
m("perplexity-field", "providers.go", 'key("title"), key("url"), key("snippet"))}, nil\n}\n\n// ---- SearXNG', 'key("title"), key("url"), key("content"))}, nil\n}\n\n// ---- SearXNG')
m("api-error-format", "providers.go", 'return nil, fmt.Errorf("%s %s API error (%d): %s", b.lb, kind, res.StatusCode, text)', 'return nil, fmt.Errorf("%s %s API error: %d %s", b.lb, kind, res.StatusCode, text)')
m("missing-key-text", "providers.go", 'return fmt.Errorf("%s is not set. Run /web-tools to configure, or export the env var.", b.env)', 'return fmt.Errorf("%s is not set.", b.env)')
m("searxng-safesearch", "providers.go", 'q.Set("safesearch", "0")', 'q.Set("safesearch", "1")')
m("searxng-trailing-slash", "providers.go", "func stripTrailingSlashes(u string) string { return strings.TrimRight(u, \"/\") }", "func stripTrailingSlashes(u string) string { return u }")
m("searxng-403-hint", "providers.go", '"search.formats\' in its settings.yml)"', '"search.formats\' in settings.yml)"') if False else None
m("searxng-auth-header", "providers.go", 'headers["Authorization"] = "Bearer " + p.apiKey\n\t}\n\treq, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)', '_ = headers\n\t}\n\treq, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)')
m("searxng-results-not-capped", "providers.go", "items := asList(asObj(raw)[\"results\"])\n\tif len(items) > maxResults {\n\t\titems = items[:maxResults]\n\t}", "items := asList(asObj(raw)[\"results\"])")
m("searxng-scheme-check", "providers.go", 'if u.Scheme != "http" && u.Scheme != "https" {\n\t\treturn fmt.Errorf("%s must use', 'if false {\n\t\treturn fmt.Errorf("%s must use')
m("ollama-local-path", "providers.go", 'path := "/api/web_search"\n\tif p.local {\n\t\tpath = "/api/experimental/web_search"\n\t}', 'path := "/api/web_search"')
m("ollama-fetch-local-path", "providers.go", 'path := "/api/web_fetch"\n\tif p.local {\n\t\tpath = "/api/experimental/web_fetch"\n\t}', 'path := "/api/web_fetch"')
m("ollama-local-hosts", "providers.go", 'return h == "localhost" || h == "127.0.0.1" || h == "0.0.0.0" || h == "[::1]"', 'return h == "localhost" || h == "127.0.0.1"')
m("ollama-401-hint", "providers.go", '" (run `ollama signin` to authenticate)"', '" (sign in)"')
m("ollama-refused-text", "providers.go", "Make sure Ollama is running (ollama serve).", "Is Ollama running?")
m("ollama-bearer", "providers.go", 'h["Authorization"] = "Bearer " + p.apiKey', '_ = p')
m("fetch-failed-text", "providers.go", 'func (e *fetchFailed) Error() string { return "fetch failed" }', 'func (e *fetchFailed) Error() string { return e.cause.Error() }')
m("unknown-provider-text", "providers.go", 'return nil, fmt.Errorf("Unknown search provider: \\"%s\\"", name)', 'return nil, fmt.Errorf("Unknown provider: \\"%s\\"", name)')
m("provider-order", "providers.go", '{Name: "tavily", Label: "Tavily", EnvVar: tavilyKeyEnv, Roles: []string{"search", "fetch"}},\n\t{Name: "serper"', '{Name: "serper", Label: "Serper", EnvVar: serperKeyEnv, Roles: []string{"search"}},\n\t{Name: "tavily"') if False else None

# fetch.go and webtools.go
m("html-script-kept", "fetch.go", 'text := scriptBlockRe.ReplaceAllString(html, "")', "text := html")
m("html-entity-order", "fetch.go", 'text = strings.ReplaceAll(text, "&amp;", "&")\n\ttext = strings.ReplaceAll(text, "&lt;", "<")', 'text = strings.ReplaceAll(text, "&lt;", "<")\n\ttext = strings.ReplaceAll(text, "&amp;", "&")')
m("html-blank-lines", "fetch.go", 'blankRunRe      = regexp.MustCompile(`\\n{3,}`)', 'blankRunRe      = regexp.MustCompile(`\\n{4,}`)')
m("html-block-closers", "fetch.go", "</(p|div|h[1-6]|li|tr|br|blockquote|pre|section|article|header|footer|nav|details|summary)>", "</(p|div|h[1-6]|li|tr|br|blockquote|pre|section|article)>")
m("html-title-trim", "fetch.go", "return jsTrim(anyTagRe.ReplaceAllString(m[1], \"\"))", "return anyTagRe.ReplaceAllString(m[1], \"\")")
m("content-type-audio", "fetch.go", '[]string{"image/", "video/", "audio/"}', '[]string{"image/", "video/"}')
m("fetch-error-text", "fetch.go", 'return nil, fmt.Errorf("HTTP %d %s for %s", res.StatusCode, http.StatusText(res.StatusCode), rawURL)', 'return nil, fmt.Errorf("HTTP %d for %s", res.StatusCode, rawURL)')
m("fetch-raw-ignored", "fetch.go", "if !raw && isHTMLContentType(contentType) {", "if isHTMLContentType(contentType) {")
m("fetch-user-agent", "fetch.go", 'userAgent         = "Mozilla/5.0 (compatible; rpiv-pi/1.0)"', 'userAgent         = "rpiv-pi/1.0"')
m("fetch-content-length", "fetch.go", "if cl := res.Header.Get(\"Content-Length\"); cl != \"\" {", "if cl := res.Header.Get(\"Content-Length\"); cl == \"\" {")
m("key-env-over-config", "webtools.go", 'if k := envTrim(meta.EnvVar); k != "" {\n\t\t\treturn k, true\n\t\t}', 'if k := envTrim(meta.EnvVar); k != "" && false {\n\t\t\treturn k, true\n\t\t}')
m("key-legacy-any-provider", "webtools.go", "if name == legacyKeyProvider {\n\t\tif k, _ := cfg.str(\"apiKey\")", "if true {\n\t\tif k, _ := cfg.str(\"apiKey\")")
m("env-provider-not-validated", "webtools.go", 'if source == "env" {\n\t\t\tif err := assertKnownProvider(n); err != nil {', 'if source == "never" {\n\t\t\tif err := assertKnownProvider(n); err != nil {')
m("override-validated-against-env", "webtools.go", "if override != nil {\n\t\tif err := assertKnownProvider(*override); err != nil {", "if override != nil && envTrim(\"WEB_SEARCH_PROVIDER\") == \"\" {\n\t\tif err := assertKnownProvider(*override); err != nil {")
m("active-config-over-env", "webtools.go", 'if e := envTrim("WEB_SEARCH_PROVIDER"); e != "" {\n\t\treturn e, "env"\n\t}\n\tif p, _ := cfg.str("provider"); p != "" {\n\t\treturn p, "config"\n\t}', 'if p, _ := cfg.str("provider"); p != "" {\n\t\treturn p, "config"\n\t}\n\tif e := envTrim("WEB_SEARCH_PROVIDER"); e != "" {\n\t\treturn e, "env"\n\t}')
m("unknown-provider-list", "webtools.go", '"Unknown web_search provider: \\"%s\\". Valid providers: %s."', '"Unknown web_search provider: \\"%s\\". Valid: %s."')
m("base-url-config-over-env", "webtools.go", 'if u := envTrim(meta.BaseURLEnvVar); u != "" {\n\t\treturn u\n\t}\n\tif u, _ := cfg.baseURL(meta.Name); jsTrim(u) != "" {\n\t\treturn jsTrim(u)\n\t}', 'if u, _ := cfg.baseURL(meta.Name); jsTrim(u) != "" {\n\t\treturn jsTrim(u)\n\t}\n\tif u := envTrim(meta.BaseURLEnvVar); u != "" {\n\t\treturn u\n\t}')
m("mask-width", "webtools.go", "head, tail := string(u[:min(4, len(u))]), string(u[max(0, len(u)-4):])", "head, tail := string(u[:min(3, len(u))]), string(u[max(0, len(u)-3):])")
m("clamp-max", "webtools.go", "maxSearchResults     = 10", "maxSearchResults     = 20")
m("clamp-default", "webtools.go", "defaultSearchResults = 5", "defaultSearchResults = 10")
m("guard-172", "webtools.go", "case a == 172 && b >= 16 && b <= 31:", "case a == 172 && b >= 16 && b <= 30:")
m("guard-link-local", "webtools.go", "case a == 169 && b == 254:", "case a == 169 && b == 253:")
m("guard-localhost-subdomain", "webtools.go", 'if h == "localhost" || strings.HasSuffix(h, ".localhost") {', 'if h == "localhost" {')
m("guard-ipv6-unique-local", "webtools.go", 'strings.HasPrefix(h, "fe80:") || strings.HasPrefix(h, "fc") || strings.HasPrefix(h, "fd")', 'strings.HasPrefix(h, "fe80:") || strings.HasPrefix(h, "fc")')
m("guard-protocol-text", "webtools.go", '"Unsupported URL protocol: %s:. Only http and https are supported."', '"Unsupported URL protocol: %s. Only http and https are supported."')
m("truncate-byte-newline", "webtools.go", "if i > 0 {\n\t\t\tlineBytes++\n\t\t}", "")
m("truncate-first-line", "webtools.go", "if len(lines[0]) > maxBytes {", "if len(lines[0]) > maxBytes*2 {")
m("truncate-line-limit", "webtools.go", "defaultMaxLines      = 2000", "defaultMaxLines      = 2001")
m("size-kb-decimals", "webtools.go", 'return fmt.Sprintf("%.1fKB", float64(b)/1024)', 'return fmt.Sprintf("%.0fKB", float64(b)/1024)')
m("footer-text", "webtools.go", "omitted. Full content saved to: %s]", "omitted. Saved to: %s]")
m("header-title", "webtools.go", 'lines = append(lines, "**Title:** "+title)', 'lines = append(lines, "**Title**: "+title)')
m("results-body-format", "webtools.go", 'text += fmt.Sprintf("%d. **%s**\\n   %s\\n   %s\\n\\n", i+1, r.Title, r.URL, r.Snippet)', 'text += fmt.Sprintf("%d. **%s**\\n  %s\\n  %s\\n\\n", i+1, r.Title, r.URL, r.Snippet)')
m("no-results-text", "webtools.go", 'return fmt.Sprintf("No results found for \\"%s\\".", query)', 'return fmt.Sprintf("No results for \\"%s\\".", query)')
m("fetch-ignores-provider-fetch", "webtools.go", "if fp, ok := p.(fetchProvider); ok {", "if fp, ok := p.(fetchProvider); ok && false {")
m("fetch-details-contentlength", "webtools.go", 'details = append(details, kv{"contentLength", *resp.ContentLength})', "_ = resp.ContentLength")
m("spill-prefix", "webtools.go", 'os.MkdirTemp("", "rpiv-fetch-")', 'os.MkdirTemp("", "fetch-")')
m("search-partial-update", "webtools.go", "if onUpdate != nil {\n\t\tonUpdate(fmt.Sprintf(\"Searching %s for", "if false {\n\t\tonUpdate(fmt.Sprintf(\"Searching %s for")

# config.go
m("config-xdg-relative-accepted", "config.go", "if filepath.IsAbs(expanded) {\n\t\treturn expanded\n\t}", "return expanded")
m("config-legacy-fallback", "config.go", "raw = loadJSONConfig(filepath.Join(defaultConfigDir(), configName, \"config.json\"))\n\t}", "raw = map[string]any{}\n\t}")
m("config-salvage-widen-array", "config.go", "case []any:\n\t\t\tdelete(parent, segs[i])\n\t\t\treturn true", "case []any:\n\t\t\treturn false")
m("config-no-salvage", "config.go", "if s := salvageConfig(raw); s != nil {\n\t\treturn s\n\t}", "")
# config-mode (WriteFile 0644 then the chmod to 0600) is an equivalent mutant: the chmod that follows gives the same mode, so it is not listed
m("config-chmod", "config.go", "_ = os.Chmod(path, 0o600)", "_ = path")
m("config-union-nested-drop", "config.go", 'bad("interceptors", "github") // a union reports the field itself', 'bad("interceptors", "github", "enabled")')
m("guidance-empty-guideline", "config.go", "if !ok || s == \"\" {\n\t\t\t\tvalid = false", "if !ok {\n\t\t\t\tvalid = false")
m("guidance-empty-snippet", "config.go", 'if s, ok := g["promptSnippet"].(string); ok && s != "" {', 'if s, ok := g["promptSnippet"].(string); ok {')

# command.go and extension.go
m("command-show-flag", "command.go", 'if strings.Contains(args, "--show") {', 'if args == "--show" {')
m("command-no-ui-text", "command.go", 'ctx.Notify("/web-tools requires interactive mode", "error")', 'ctx.Notify("/web-tools needs a UI", "error")')
m("command-active-first", "command.go", "for _, p := range providers {\n\t\tif p.Name == active {\n\t\t\tordered = append(ordered, p)\n\t\t}\n\t}", "")
m("command-label-marker", "command.go", 'markers = append(markers, "✓")', 'markers = append(markers, "*")')
m("command-configured-marker", "command.go", 'markers = append(markers, "(configured)")', 'markers = append(markers, "(set)")')
m("command-legacy-key-kept", "command.go", 'delete(toSave, "apiKey")\n\t\tif !writeConfig(toSave) {\n\t\t\tctx.Notify(fmt.Sprintf("Failed to save %s config', 'if !writeConfig(toSave) {\n\t\t\tctx.Notify(fmt.Sprintf("Failed to save %s config')
m("command-legacy-key-kept-plain", "command.go", 'delete(toSave, "apiKey")\n\tif !writeConfig(toSave) {\n\t\tctx.Notify(fmt.Sprintf("Failed to save %s API key', 'if !writeConfig(toSave) {\n\t\tctx.Notify(fmt.Sprintf("Failed to save %s API key')
m("command-empty-key-saved", "command.go", 'if keyToWrite == "" {\n\t\treturn unchanged()\n\t}', "")
m("command-existing-key-ignored", "command.go", 'if keyToWrite == "" && hasExisting {\n\t\tkeyToWrite = existing\n\t}', "")
m("command-write-failure-silent", "command.go", 'ctx.Notify(fmt.Sprintf("Failed to save %s API key to %s — disk write failed", meta.Label, configPath()), "error")', 'ctx.Notify(fmt.Sprintf("Saved %s API key to %s", meta.Label, configPath()), "info")')
m("command-searxng-default-url", "command.go", 'return def, true, nil\n}', 'return "", true, nil\n}')
m("command-key-masked-in-prompt", "command.go", 'placeholder = fmt.Sprintf("Press Enter to keep current (%s), or type new key", maskKey(existing))', 'placeholder = fmt.Sprintf("Press Enter to keep current (%s), or type new key", existing)')
m("show-legacy-key", "command.go", 'case meta.Name == legacyKeyProvider && hasString(cfg, "apiKey"):', 'case false:')
m("show-url-source", "command.go", 'resolved, src = meta.DefaultBaseURL, "default"', 'resolved, src = meta.DefaultBaseURL, "config"')
m("show-interceptor-hint", "command.go", '`  ↳ enable:  add  "interceptors": { "github": true }   to config.json`', '`  enable it in config.json`')
m("tool-guidance-fallback", "extension.go", "snippet, guidelines := defSnippet, defGuidelines\n\tif g.HasSnippet {\n\t\tsnippet = g.PromptSnippet\n\t}", "snippet, guidelines := defSnippet, defGuidelines")
m("tool-description", "extension.go", "Search the web for information. Returns a list of results", "Search the web. Returns a list of results")
m("schema-provider-description", "extension.go", "there is no silent fallback.", "there is a silent fallback.")

by_file = {}
# review (rev-pig-essentials): the WHATWG host parser behind the URL guard, and the request going to the parsed URL
m("host-ipv4-forms-unparsed", "util.go", "if !endsInANumber(ascii) {", "if true {")
m("host-octal-as-decimal", "util.go", "s, radix = s[1:], 8", "s, radix = s[1:], 10")
m("host-hex-ignored", "util.go", 's[:2] == "0x" || s[:2] == "0X"', 's[:2] == "0X"')
m("host-short-form-shift", "util.go", "v += n << (8 * (3 - i))", "v += n << (8 * (2 - i))")
m("host-last-part-range", "util.go", "if last >= uint64(1)<<(8*(5-len(nums))) {", "if last >= uint64(1)<<32 {")
m("host-ipv6-uncompressed", "util.go", "if compress == i {", "if compress == i && false {")
m("host-ipv6-zone-accepted", "util.go", "|| a.Zone() != \"\" {", "{")
m("host-idna-skipped", "util.go", "if ascii, err = idnaProfile.ToASCII(h); err != nil {", "if false {")
m("host-port-dropped", "util.go", 'u.Host = host + ":" + port', "u.Host = host")
m("fetch-raw-url-requested", "webtools.go", "fetchViaGenericHTML(ctx, target.Href, rawURL, raw)", "fetchViaGenericHTML(ctx, target.Href[:0]+rawURL, rawURL, raw)")

M = [x for x in M if x]
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
