package rpiv_web_tools

import (
	"net/url"
	"strings"
)

// The port refuses what it does not implement with one message that names the working
// alternative: the original extension, which runs unchanged in PiG. The package name is this
// Package's package.json name and the spec is the original pinned in provenance.json (the layout
// test refusal_test.go keeps both in step). A refusal is a tool error, never a fallback.
const (
	portPackage  = "@pi-in-go/pigpen-rpiv-web-tools"
	originalSpec = "npm:@juicesharp/rpiv-web-tools@2.12.0"
)

// refusal is the message for an unported feature.
func refusal(feature string) string {
	return portPackage + " (Go port) does not support " + feature + " yet. Install the original extension, " + originalSpec + ", which runs unchanged in PiG."
}

const githubInterceptorFeature = "the GitHub URL interceptor (interceptors.github)"

var githubNonCodeSegments = []string{"issues", "pull", "pulls", "discussions", "releases", "wiki", "actions", "settings", "security", "projects", "graphs", "compare", "commits", "tags", "branches", "stargazers", "watchers", "network", "forks", "milestone", "labels", "packages", "codespaces", "contribute", "community", "sponsors", "invitations", "notifications", "insights"}

// githubCodeURL is parseGitHubUrl of interceptors/github.ts: whether the interceptor would take
// this URL (a repository root, or a blob or tree URL of github.com or www.github.com).
func githubCodeURL(href string) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return false
	}
	var segments []string
	for _, s := range strings.Split(u.EscapedPath(), "/") {
		if s == "" {
			continue
		}
		if d, err := url.PathUnescape(s); err == nil {
			s = d
		}
		segments = append(segments, s)
	}
	if len(segments) < 2 {
		return false
	}
	if len(segments) > 2 {
		for _, n := range githubNonCodeSegments {
			if strings.ToLower(segments[2]) == n {
				return false
			}
		}
	}
	if len(segments) == 2 {
		return true
	}
	return (segments[2] == "blob" || segments[2] == "tree") && len(segments) >= 4
}
