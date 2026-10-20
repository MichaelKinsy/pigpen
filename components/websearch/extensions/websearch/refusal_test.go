package websearch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refusal is the exact message of an unported feature, written out in full (not built from the
// constants of refusal.go) so that a change of wording or of the install advice fails here.
func refusal(feature string) string {
	return "@pi-in-go/pigpen-websearch (Go port) does not support " + feature + " yet. Install the original extension, npm:pi-web-access@0.33.0, which runs unchanged in PiG."
}

func readJSONFile(t *testing.T, rel string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("the Package files are not beside this module (a mutation run copies the module alone); the repository run checks them")
		}
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The message names this Package and the original pinned in provenance.json; both must follow
// package.json and the vendored original.
func TestRefusalNamesPackageAndOriginal(t *testing.T) {
	pkg := readJSONFile(t, "../../package.json")
	orig := readJSONFile(t, "../../port/oracle/package.json")
	prov := readJSONFile(t, "../../provenance.json")
	upstream := prov["upstreams"].([]any)[0].(map[string]any)
	if upstream["name"] != orig["name"] {
		t.Fatalf("provenance names %v, the vendored original is %v", upstream["name"], orig["name"])
	}
	want := pkg["name"].(string) + " (Go port) does not support x yet. Install the original extension, npm:" + orig["name"].(string) + "@" + orig["version"].(string) + ", which runs unchanged in PiG."
	if got := unsupported("x").Error(); got != want {
		t.Fatalf("refusal = %q, want %q", got, want)
	}
	if got := refusal("x"); got != want {
		t.Fatalf("test helper = %q, want %q", got, want)
	}
}

func execTool(t *testing.T, r *Runtime, name string, params map[string]any) (ToolOutput, error) {
	t.Helper()
	return mustTool(t, r, name).Execute(bg(), params, nil)
}

func wantRefusal(t *testing.T, err error, feature string) {
	t.Helper()
	if want := refusal(feature); err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// providerRuntime isolates the configuration (which clears the provider variables), writes the
// config, then sets the environment of the case.
func providerRuntime(t *testing.T, cfg string, env map[string]string) *Runtime {
	t.Helper()
	r, _ := newRuntime(t, cfg)
	for k, v := range env {
		t.Setenv(k, v)
	}
	return r
}

func TestRefusalSearchWorkflows(t *testing.T) {
	for _, workflow := range []string{"summary-review", "auto-summary"} {
		workflow := workflow
		t.Run("workflow parameter "+workflow, func(t *testing.T) {
			r := providerRuntime(t, `{}`, map[string]string{"TAVILY_API_KEY": "k"})
			n := searchProviderNet(t)
			_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "tavily", "workflow": workflow})
			wantRefusal(t, err, `web_search workflow "`+workflow+`" (the search curator and generated summaries)`)
			wantCalls(t, n)
		})
	}
	t.Run("configured workflow auto-summary", func(t *testing.T) {
		cfg := `{"workflow":"auto-summary"}`
		r := providerRuntime(t, cfg, map[string]string{"TAVILY_API_KEY": "k"})
		n := searchProviderNet(t)
		_, err := execTool(t, r, "web_search", map[string]any{"query": "x", "provider": "tavily"})
		wantRefusal(t, err, `web_search workflow "auto-summary" (the search curator and generated summaries)`)
		wantCalls(t, n)
	})
}

func TestRefusalFetchHostedProviders(t *testing.T) {
	t.Run("a provider that is not configured is skipped as in the original", func(t *testing.T) {
		r, _ := newRuntime(t, "")
		useNet(t, func(netCall) netReply { return reply(500, "boom") })
		out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/page"})
		if err != nil {
			t.Fatal(err)
		}
		if out.IsError || strings.Contains(out.Text(), "(Go port)") {
			t.Fatal(out.Text())
		}
	})
}
