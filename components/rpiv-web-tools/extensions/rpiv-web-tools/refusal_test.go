package rpiv_web_tools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// wantRefusal is the exact message of an unported feature, written out in full (not built from
// the constants of refusal.go) so that a change of wording or of the install advice fails here.
func wantRefusal(feature string) string {
	return "@pi-in-go/pigpen-rpiv-web-tools (Go port) does not support " + feature + " yet. Install the original extension, npm:@juicesharp/rpiv-web-tools@2.12.0, which runs unchanged in PiG."
}

func readJSON(t *testing.T, rel string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The message names this Package and the original pinned in the vendored package.json.
func TestRefusalNamesPackageAndOriginal(t *testing.T) {
	pkg := readJSON(t, "../../package.json")
	orig := readJSON(t, "../../port/oracle/package.json")
	prov := readJSON(t, "../../provenance.json")
	if prov["upstreams"].([]any)[0].(map[string]any)["name"] != "rpiv-mono" {
		t.Fatal("provenance names another upstream")
	}
	want := pkg["name"].(string) + " (Go port) does not support x yet. Install the original extension, npm:" + orig["name"].(string) + "@" + orig["version"].(string) + ", which runs unchanged in PiG."
	if got := refusal("x"); got != want {
		t.Fatalf("refusal = %q, want %q", got, want)
	}
}

func TestRefusalGitHubInterceptor(t *testing.T) {
	fetch := func(t *testing.T, cfg any, url string) (string, error, *fakeNet) {
		t.Helper()
		clearEnv(t)
		if cfg != nil {
			writeConfigFile(t, cfg)
		}
		n := useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "text/plain"}, Body: "page"}
		})
		text, _, err := fetchURL(t, url, false)
		return text, err, n
	}
	on := obj{"interceptors": obj{"github": true}}
	for _, url := range []string{
		"https://github.com/octocat/Hello-World",
		"https://www.github.com/octocat/Hello-World.git",
		"https://github.com/octocat/Hello-World/tree/main/docs",
		"https://github.com/octocat/Hello-World/blob/main/README.md",
	} {
		t.Run("enabled refuses "+url, func(t *testing.T) {
			_, err, n := fetch(t, on, url)
			eq(t, errText(err), wantRefusal("the GitHub URL interceptor (interceptors.github)"))
			eq(t, len(n.Requests), 0)
		})
	}
	t.Run("an options object enables it", func(t *testing.T) {
		_, err, _ := fetch(t, obj{"interceptors": obj{"github": obj{"maxRepoSizeMB": 10.0}}}, "https://github.com/octocat/Hello-World")
		eq(t, errText(err), wantRefusal("the GitHub URL interceptor (interceptors.github)"))
	})
	t.Run("raw does not bypass it, as in the original", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, on)
		useNet(t, func(netReq) netResp {
			return netResp{Header: map[string]string{"Content-Type": "text/plain"}, Body: "page"}
		})
		_, _, err := fetchURL(t, "https://github.com/octocat/Hello-World", true)
		eq(t, errText(err), wantRefusal("the GitHub URL interceptor (interceptors.github)"))
	})
	for _, c := range []struct {
		name string
		cfg  any
		url  string
	}{
		{"off by default", nil, "https://github.com/octocat/Hello-World"},
		{"disabled", obj{"interceptors": obj{"github": false}}, "https://github.com/octocat/Hello-World"},
		{"an object with enabled false", obj{"interceptors": obj{"github": obj{"enabled": false}}}, "https://github.com/octocat/Hello-World"},
		{"an issue is not a code URL", on, "https://github.com/octocat/Hello-World/issues/1"},
		{"a user page is not a code URL", on, "https://github.com/octocat"},
		{"another host", on, "https://gitlab.com/octocat/Hello-World"},
	} {
		t.Run("fetched as a page: "+c.name, func(t *testing.T) {
			text, err, n := fetch(t, c.cfg, c.url)
			eq(t, err, nil)
			eq(t, strings.Contains(text, "page"), true)
			eq(t, len(n.Requests), 1)
		})
	}
}

func TestRefusalShowNamesTheInterceptor(t *testing.T) {
	clearEnv(t)
	got := strings.Join(githubInterceptorLines(webToolsConfig{"interceptors": obj{"github": true}}), "\n")
	eq(t, got, "  github: enabled in config. "+wantRefusal("the GitHub URL interceptor (interceptors.github)"))
}
