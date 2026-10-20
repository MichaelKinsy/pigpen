#!/usr/bin/env python3
"""Generates port/scenarios/*.json for the web tools: the request the model sees, every error that needs no
network, the /web-tools command, and the two self-hosted backends against fake servers (the hosted backends'
endpoints are fixed in the original, so their request shapes are checked by the layer-1 tests)."""
import json, os

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")
os.makedirs(out, exist_ok=True)


def call(name, **args):
    return {"name": name, "arguments": args}


def prompt(message="look it up", **kw):
    return {"name": kw.pop("name", "prompt"), "rpc": {"type": "prompt", "message": message}, **kw}


def scenario(name, description, turns, steps=None, **extra):
    # The original's github interceptor starts gh and git (it is opt-in, and this port does not implement it); a
    # scenario has to declare every command either side may start, so each scenario does, as absent from PATH.
    extra.setdefault("commands", {"gh": {"mode": "missing"}, "git": {"mode": "missing"}})
    llm = [{"toolCalls": t} for t in turns] + [{"text": "done"}]
    s = {"name": name, "description": description, "llm": llm, "steps": steps or [prompt()]}
    s.update(extra)
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(s, f, indent=2, ensure_ascii=False)
        f.write("\n")


def config_setup(cfg):
    """A setup step that writes the user's config file into the lane's hermetic HOME."""
    body = json.dumps(cfg).replace("'", "'\\''")
    return [["sh", "-c", "mkdir -p \"$HOME/.config/rpiv-web-tools\" && printf '%s' '" + body + "' > \"$HOME/.config/rpiv-web-tools/config.json\""]]


PROVIDERS = ["brave", "tavily", "serper", "exa", "youcom", "jina", "firecrawl", "perplexity"]

scenario("tool-definitions", "The request carries both tools with their schemas, descriptions and guidance.", [], steps=[prompt("hello")])

scenario("search-without-key", "web_search with no key: the default provider, every named provider, an unknown one, and clamped counts.",
         [[call("web_search", query="go news")]] + [[call("web_search", query="q", provider=p)] for p in PROVIDERS + ["searxng", "ollama"]])

scenario("search-config-key-errors", "A configured provider without its key, and a legacy empty key, still says the key is not set.",
         [[call("web_search", query="q")], [call("web_search", query="q", provider="serper")], [call("web_fetch", url="https://example.com/p")]],
         setup=config_setup({"provider": "tavily", "apiKey": "   "}))

scenario("fetch-url-guard", "web_fetch refuses malformed URLs, other protocols and private or loopback hosts, and a raw flag does not matter.",
         [[call("web_fetch", url="not a url")], [call("web_fetch", url="ftp://example.com/x")], [call("web_fetch", url="file:///etc/passwd")],
          [call("web_fetch", url="http://localhost:8080/")], [call("web_fetch", url="http://127.0.0.1/")], [call("web_fetch", url="http://10.0.0.5/")],
          [call("web_fetch", url="http://169.254.169.254/latest/meta-data")], [call("web_fetch", url="http://192.168.1.1/", raw=True)],
          [call("web_fetch", url="http://172.16.0.1/")], [call("web_fetch", url="http://[::1]/")], [call("web_fetch", url="http://foo.localhost/")]])

scenario("fetch-keyed-provider-without-key", "web_fetch with the Tavily provider selected by the environment but no key: the provider's own error.",
         [[call("web_fetch", url="https://example.com/page")]], env={"WEB_SEARCH_PROVIDER": "tavily"})

scenario("unknown-env-provider", "A bogus WEB_SEARCH_PROVIDER throws on search and on fetch, but a valid per-call provider still names its own error.",
         [[call("web_search", query="q")], [call("web_fetch", url="https://example.com/")], [call("web_search", query="q", provider="exa")]],
         env={"WEB_SEARCH_PROVIDER": "bogus"})

SEARX_BODY = json.dumps({"results": [{"title": "Go 1.27", "url": "https://go.dev/doc/go1.27", "content": "Release notes."},
                                    {"title": "Second", "url": "https://example.com/2", "content": "More."},
                                    {"title": "Third", "url": "https://example.com/3", "content": "Even more."}]})
scenario("searxng-search", "web_search through a SearXNG instance: the request it sends, the results, a trimmed count, a 403 hint and an empty answer.",
         [[call("web_search", query="go release", max_results=2)], [call("web_search", query="forbidden")], [call("web_search", query="nothing here")]],
         servers={"searx": {"routes": [
             {"method": "GET", "path": "/search", "query": {"q": "go release"}, "status": 200, "headers": {"Content-Type": "application/json"}, "body": SEARX_BODY},
             {"method": "GET", "path": "/search", "query": {"q": "forbidden"}, "status": 403, "body": "json disabled"},
             {"method": "GET", "path": "/search", "query": {"q": "nothing here"}, "status": 200, "headers": {"Content-Type": "application/json"}, "body": "{\"results\": []}"}]}},
         env={"SEARXNG_URL": "{{server:searx}}/", "WEB_SEARCH_PROVIDER": "searxng", "SEARXNG_API_KEY": "tok-123456"})

scenario("ollama-search", "web_search through a local Ollama instance: the experimental endpoint, then a 401 hint.",
         [[call("web_search", query="ollama news")], [call("web_search", query="signin")]],
         servers={"ollama": {"routes": [
             {"method": "POST", "path": "/api/experimental/web_search", "status": 200, "headers": {"Content-Type": "application/json"},
              "body": json.dumps({"results": [{"title": "Ollama", "url": "https://ollama.com", "content": "Run models."}]}), "times": 1},
             {"method": "POST", "path": "/api/experimental/web_search", "status": 401, "body": "unauthorized"}]}},
         env={"OLLAMA_HOST": "{{server:ollama}}", "WEB_SEARCH_PROVIDER": "ollama"})

OLLAMA_PATH = "/api/experimental/web_search"
OLLAMA_BODY = json.dumps({"results": [{"title": "Redirected", "url": "https://ollama.com/r", "content": "Arrived after twenty redirects."}]})
redirect = {"method": "POST", "path": OLLAMA_PATH, "status": 307, "headers": {"Location": OLLAMA_PATH}}
scenario("fetch-redirect-limit", "The original's fetch follows 20 redirects and fails on the 21st (a Go client stops at 10): an Ollama search answered after 20 redirects, then one that meets a 21st.",
         [[call("web_search", query="twenty")], [call("web_search", query="twenty-one")]],
         servers={"ollama": {"routes": [
             {**redirect, "times": 20},
             {"method": "POST", "path": OLLAMA_PATH, "status": 200, "headers": {"Content-Type": "application/json"}, "body": OLLAMA_BODY, "times": 1},
             {**redirect, "times": 21},
             {"method": "POST", "path": OLLAMA_PATH, "status": 200, "headers": {"Content-Type": "application/json"}, "body": OLLAMA_BODY}]}},
         env={"OLLAMA_HOST": "{{server:ollama}}", "WEB_SEARCH_PROVIDER": "ollama"})

scenario("command-show", "/web-tools --show with nothing configured.", [],
         steps=[prompt("/web-tools --show", name="show-empty")])

scenario("command-show-configured", "/web-tools --show with a config file, an environment key and a legacy key (every key masked).", [],
         steps=[prompt("/web-tools --show", name="show")],
         setup=config_setup({"provider": "exa", "apiKey": "legacy-brave-key-9999", "apiKeys": {"exa": "exa-secret-key-1234", "tavily": "tvly-cfg-key-5678"}, "baseUrls": {"ollama": "http://ollama.lan:11434"}}),
         env={"SERPER_API_KEY": "env-serper-key-4321", "SEARXNG_URL": "https://search.example.org"})

scenario("command-pick-key", "/web-tools: pick Tavily, type a key, then show: the key is saved masked and Tavily is active.", [],
         steps=[prompt("/web-tools", name="pick", ui=[{"value": "Tavily"}, {"value": "tvly-secret-1234"}]),
                prompt("/web-tools --show", name="show"),
                prompt("/web-tools", name="pick-again", ui=[{"value": "Tavily ✓ (configured)"}, {"value": ""}])])

scenario("command-cancelled", "/web-tools: cancelled at the provider picker, cancelled at the key prompt, and an empty key with none saved.", [],
         steps=[prompt("/web-tools", name="cancel-pick", ui=[{"cancelled": True}]),
                prompt("/web-tools", name="cancel-key", ui=[{"value": "Brave"}, {"cancelled": True}]),
                prompt("/web-tools", name="empty-key", ui=[{"value": "Brave"}, {"value": "   "}])])

scenario("command-searxng", "/web-tools: SearXNG asks for a URL and an optional key; the picker then marks it configured and active.", [],
         steps=[prompt("/web-tools", name="pick", ui=[{"value": "SearXNG"}, {"value": "http://search.internal:8080"}, {"value": ""}]),
                prompt("/web-tools --show", name="show")])

scenario("guidance-override", "A guidance override in the config replaces the tool snippet and guidelines in the request; an invalid one falls back.", [],
         steps=[prompt("hello")],
         setup=config_setup({"guidance": {"web_search": {"promptSnippet": "Custom search snippet", "promptGuidelines": ["Rule S1", "Rule S2"]},
                                           "web_fetch": {"promptSnippet": 123, "promptGuidelines": "not an array"}}}))


# The GitHub URL interceptor (opt-in). The original starts gh and git; both sides run them through the harness's
# command shims, so the command lines are compared too. The fetch needs a key first (instantiateProvider runs
# before the interceptor chain), hence BRAVE_SEARCH_API_KEY.
GH_ENV = {"BRAVE_SEARCH_API_KEY": "scenario-key"}


def github_config_setup(extra=None):
    """A setup step writing a config that enables the interceptor with its clone directory under the lane HOME."""
    return [["sh", "-c", "mkdir -p \"$HOME/.config/rpiv-web-tools\" && printf '{\"interceptors\":{\"github\":{\"clonePath\":\"%s/clones\"}}}' \"$HOME\" > \"$HOME/.config/rpiv-web-tools/config.json\""]]


# gh answers every command with the same text: "20000000" is the size in KB read by parseInt (over the 350 MB
# threshold), the rest is base64 for the contents calls, and the whole text is the default branch and the tree.
scenario("github-api-view", "With the interceptor on and gh answering a 20 GB size, a repository root, a blob and a commit URL get the API view.",
         [[call("web_fetch", url="https://github.com/o/r")],
          [call("web_fetch", url="https://github.com/o/r/blob/main/README.md")],
          [call("web_fetch", url="https://github.com/o/r/blob/0123456789abcdef0123456789abcdef01234567/src/a.go")]],
         setup=github_config_setup(), env=GH_ENV,
         commands={"gh": {"mode": "canned", "stdout": "20000000UkVBRE1FIGhlbGxvCg==", "exit": 0}, "git": {"mode": "missing"}})

# gh answers 100 (KB) to everything and its clone makes no directory: the clone views of an empty clone, with
# --branch for a ref and the cache for a repeat.
scenario("github-clone-gh", "With gh available and a small repository the clone command line is gh repo clone; an empty clone renders an empty tree, a ref adds --branch, and a repeat uses the cache.",
         [[call("web_fetch", url="https://github.com/o/r")],
          [call("web_fetch", url="https://github.com/o/r/tree/dev/src")],
          [call("web_fetch", url="https://github.com/o/r/blob/dev/src/main.go")],
          [call("web_fetch", url="https://github.com/o/r/tree/dev/src")],
          [call("web_fetch", url="https://github.com/o/r")]],
         setup=github_config_setup(), env=GH_ENV,
         commands={"gh": {"mode": "canned", "stdout": "100", "exit": 0}, "git": {"mode": "missing"}})

# git answers 0 and clones nothing. A real git with a local repository behind url.<base>.insteadOf is not possible
# here: the harness fixes GIT_CONFIG_GLOBAL, GIT_CONFIG_NOSYSTEM and GIT_CONFIG_COUNT, and git clone reads no
# repository-local config. The rendering of a populated clone is proven by the twins and the layer-1 tests instead.
scenario("github-clone-git", "Without gh, git clones a repository: the command line with and without a ref, the empty views of a clone that holds nothing, and the cache on a repeat.",
         [[call("web_fetch", url="https://github.com/o/r")],
          [call("web_fetch", url="https://github.com/o/r/tree/main/docs")],
          [call("web_fetch", url="https://github.com/o/r/blob/main/src/main.go")],
          [call("web_fetch", url="https://github.com/o/r/blob/main/data.bin")],
          [call("web_fetch", url="https://github.com/o/r/tree/feature%2Fx")],
          [call("web_fetch", url="https://github.com/o/r/tree/main/docs")]],
         setup=github_config_setup(), env=GH_ENV,
         commands={"gh": {"mode": "missing"}, "git": {"mode": "canned", "exit": 0}})

scenario("command-show-github", "/web-tools --show with the interceptor on: the token masked, the size limit and the clone path.", [],
         steps=[prompt("/web-tools --show", name="show")], setup=github_config_setup(), env={"GITHUB_TOKEN": "ghp_abcdefgh1234"})
