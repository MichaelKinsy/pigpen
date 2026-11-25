package github

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// GitHub CLI host-trust guard (Changed on purpose vs omp v19.1.1):
//
// `gh help environment` documents that:
//   - `GH_ENTERPRISE_TOKEN` / `GITHUB_ENTERPRISE_TOKEN` are sent whenever a
//     command targets ANY GitHub Enterprise Server host (i.e. any host other
//     than github.com / *.ghe.com);
//   - `GH_TOKEN` / `GITHUB_TOKEN` are sent to `github.com` and any subdomain
//     of `ghe.com`.
//
// Therefore a model-chosen host in `pr://<host>/...`, `issue://<host>/...`, or
// `repo: "<host>/owner/repo"` could cause `gh` to transmit ambient enterprise
// tokens (or `GH_TOKEN` to an attacker-chosen `*.ghe.com` tenant) to an
// untrusted host.
//
// We restrict explicit non-default hosts to:
//   1. `github.com`
//   2. `GH_HOST` when set in the environment
//   3. Hosts present in `gh`'s `hosts.yml` (cached per session/config mtime)
//
// Any other host is refused before invoking `gh`.

type ghTrustedHostsCacheEntry struct {
	configPath string
	mtimeNs    int64
	hosts      map[string]bool
}

var (
	ghTrustedHostsMu sync.Mutex
	ghTrustedHosts   = make(map[string]ghTrustedHostsCacheEntry)
)

func clearGhTrustedHostsCache(sessionID string) {
	ghTrustedHostsMu.Lock()
	if sessionID == "" {
		clear(ghTrustedHosts)
	} else {
		delete(ghTrustedHosts, sessionID)
	}
	ghTrustedHostsMu.Unlock()
}

// parseGhHostsYML extracts top-level hostname keys from gh's hosts.yml
// (YAML map of `<hostname>:` -> nested auth settings) without external deps.
func parseGhHostsYML(content string) []string {
	var hosts []string
	for _, raw := range strings.Split(content, "\n") {
		if raw == "" || raw[0] == ' ' || raw[0] == '\t' || raw[0] == '#' {
			continue
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		key, _, hasColon := strings.Cut(trimmed, ":")
		if !hasColon {
			continue
		}
		key = strings.Trim(strings.TrimSpace(key), `"'`)
		if key != "" {
			hosts = append(hosts, strings.ToLower(key))
		}
	}
	return hosts
}

func authenticatedGhHosts(sessionID string) map[string]bool {
	cfgPath := filepath.Join(getGhConfigDir(), "hosts.yml")
	var mtimeNs int64 = -1
	if info, err := os.Stat(cfgPath); err == nil {
		mtimeNs = info.ModTime().UnixNano()
	}

	cacheKey := sessionID
	if cacheKey == "" {
		cacheKey = "_default"
	}

	ghTrustedHostsMu.Lock()
	if cached, ok := ghTrustedHosts[cacheKey]; ok && cached.configPath == cfgPath && cached.mtimeNs == mtimeNs {
		ghTrustedHostsMu.Unlock()
		return cached.hosts
	}
	ghTrustedHostsMu.Unlock()

	hosts := make(map[string]bool)
	if data, err := os.ReadFile(cfgPath); err == nil {
		for _, h := range parseGhHostsYML(string(data)) {
			hosts[h] = true
		}
	}

	ghTrustedHostsMu.Lock()
	ghTrustedHosts[cacheKey] = ghTrustedHostsCacheEntry{
		configPath: cfgPath,
		mtimeNs:    mtimeNs,
		hosts:      hosts,
	}
	ghTrustedHostsMu.Unlock()
	return hosts
}

// assertGhHostTrusted checks that host (when non-empty) is github.com, GH_HOST,
// or an authenticated host in gh's hosts.yml.
func assertGhHostTrusted(sessionID, host string) error {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == githubHost {
		return nil
	}
	if ghHost := strings.ToLower(strings.TrimSpace(os.Getenv("GH_HOST"))); ghHost != "" && h == ghHost {
		return nil
	}
	if authenticatedGhHosts(sessionID)[h] {
		return nil
	}
	return &untrustedGhHostError{host: h}
}

type untrustedGhHostError struct {
	host string
}

func (e *untrustedGhHostError) Error() string {
	return "Refusing to send GitHub CLI requests to untrusted host " + e.host +
		": it is not github.com, GH_HOST, or an authenticated host in gh's hosts.yml. " +
		"Run `gh auth login --hostname " + e.host + "` or set GH_HOST to trust it."
}

// validateRepoParam rejects a leading '-' on model-supplied repo arguments so
// positional `gh repo view <repo>` or `--repo` cannot be flag-injected, and
// verifies any explicit host prefix is trusted.
func validateRepoParam(sessionID, repo string) error {
	if repo == "" {
		return nil
	}
	if strings.HasPrefix(repo, "-") {
		return &invalidRepoError{repo: repo}
	}
	ref := ParseRepoRef(repo)
	if ref.Host != "" {
		return assertGhHostTrusted(sessionID, ref.Host)
	}
	return nil
}

type invalidRepoError struct {
	repo string
}

func (e *invalidRepoError) Error() string {
	return "invalid repo: " + e.repo + ". Expected [host/]owner/repo."
}

// extractGhTargetHost inspects gh argv for any explicit non-default host
// (`--hostname <h>`, `--repo <h>/o/r`, `repo view <h>/o/r`, or `https://<h>/...`).
func extractGhTargetHost(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--hostname" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--hostname="); ok {
			return v
		}
		if (a == "--repo" || a == "-R") && i+1 < len(args) {
			if ref := ParseRepoRef(args[i+1]); ref.Host != "" {
				return ref.Host
			}
		}
		if v, ok := strings.CutPrefix(a, "--repo="); ok {
			if ref := ParseRepoRef(v); ref.Host != "" {
				return ref.Host
			}
		}
		if strings.HasPrefix(a, "https://") || strings.HasPrefix(a, "http://") {
			if repo := repoFromURL(a); repo != "" {
				if ref := ParseRepoRef(repo); ref.Host != "" {
					return ref.Host
				}
			}
		}
	}
	if len(args) >= 3 && args[0] == "repo" && args[1] == "view" && !strings.HasPrefix(args[2], "-") {
		if ref := ParseRepoRef(args[2]); ref.Host != "" {
			return ref.Host
		}
	}
	return ""
}
