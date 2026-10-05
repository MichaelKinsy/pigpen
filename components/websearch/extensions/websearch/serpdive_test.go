package websearch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Twins of serpdive-provider.test.mjs (the upstream titles are French).

const serpdiveOK = `{"query":"q","model":"krill","response_time_ms":1200,"results":[
 {"url":"https://github.com/nicobailon/pi-web-access","title":"Repo","content":"repo content"},
 {"url":"https://gist.github.com/nicobailon/abc","title":"Gist","content":"gist content"},
 {"url":"https://example.com/nope","title":"Example","content":"example content"}]}`

func serpdiveBody(t *testing.T, c netCall) map[string]any {
	t.Helper()
	var body map[string]any
	noErr(t, json.Unmarshal([]byte(c.Body), &body))
	return body
}

func TestUpstream_serpdive_provider(t *testing.T) {
	const F = "serpdive-provider"
	setup := func(t *testing.T, env map[string]string) *fakeNet {
		isolate(t)
		unsetenv(t, "SERPDIVE_API_KEY", "SERPDIVE_MODEL")
		for k, v := range env {
			t.Setenv(k, v)
		}
		return useNet(t, func(netCall) netReply { return reply(200, serpdiveOK) })
	}
	tw(t, F, "le modèle par défaut est krill, et aucune synthèse n'est demandée", func(t *testing.T) {
		fn := setup(t, map[string]string{"SERPDIVE_API_KEY": "serpdive-test-key"})
		res, err := SearchWithSerpdive(bg(), "vector databases", SearchOptions{NumResults: nf(3)})
		noErr(t, err)
		body := serpdiveBody(t, fn.calls[0])
		if fn.calls[0].URL != "https://api.serpdive.com/v1/search" || fn.calls[0].Header.Get("Authorization") != "Bearer serpdive-test-key" || body["model"] != "krill" {
			t.Fatalf("%+v %v", fn.calls[0], body)
		}
		if _, has := body["answer"]; has {
			t.Fatal("krill does not synthesize: answer must not be requested")
		}
		matchRE(t, `repo content`, res.Answer)
		matchRE(t, `Source: Repo`, res.Answer)
	})
	tw(t, F, "un modèle explicite demande la synthèse à l'API", func(t *testing.T) {
		fn := setup(t, map[string]string{"SERPDIVE_API_KEY": "k", "SERPDIVE_MODEL": "moby"})
		_, err := SearchWithSerpdive(bg(), "vector databases", SearchOptions{})
		noErr(t, err)
		body := serpdiveBody(t, fn.calls[0])
		if body["model"] != "moby" || body["answer"] != true {
			t.Fatal(body)
		}
	})
	tw(t, F, "un modèle inconnu retombe sur krill, jamais sur un modèle payant", func(t *testing.T) {
		fn := setup(t, map[string]string{"SERPDIVE_API_KEY": "k", "SERPDIVE_MODEL": "MAKO-turbo"})
		_, err := SearchWithSerpdive(bg(), "vector databases", SearchOptions{})
		noErr(t, err)
		if serpdiveBody(t, fn.calls[0])["model"] != "krill" {
			t.Fatal("unknown model must fall back to krill")
		}
	})
	tw(t, F, "max_results est plafonné à 10 — l'API n'accepte pas plus", func(t *testing.T) {
		fn := setup(t, map[string]string{"SERPDIVE_API_KEY": "k"})
		_, err := SearchWithSerpdive(bg(), "vector databases", SearchOptions{NumResults: nf(20)})
		noErr(t, err)
		if serpdiveBody(t, fn.calls[0])["max_results"] != 10.0 {
			t.Fatal(serpdiveBody(t, fn.calls[0]))
		}
	})
	tw(t, F, "la récence est un indice de requête, pas un paramètre temporel", func(t *testing.T) {
		fn := setup(t, map[string]string{"SERPDIVE_API_KEY": "k"})
		_, err := SearchWithSerpdive(bg(), "ai news", SearchOptions{RecencyFilter: "week"})
		noErr(t, err)
		body := serpdiveBody(t, fn.calls[0])
		if body["query"] != "ai news past week" {
			t.Fatal(body["query"])
		}
		for _, p := range []string{"time_range", "recency", "start_date", "end_date", "days"} {
			if _, has := body[p]; has {
				t.Fatalf("%s does not exist in the API", p)
			}
		}
	})
	tw(t, F, "le filtrage par domaine est appliqué côté client, sur ce qui revient", func(t *testing.T) {
		fn := setup(t, map[string]string{"SERPDIVE_API_KEY": "k"})
		res, err := SearchWithSerpdive(bg(), "sdk docs", SearchOptions{DomainFilter: []string{"github.com", "-gist.github.com"}, NumResults: nf(5), IncludeContent: true})
		noErr(t, err)
		body := serpdiveBody(t, fn.calls[0])
		for _, p := range []string{"include_domains", "exclude_domains", "domains"} {
			if _, has := body[p]; has {
				t.Fatalf("no domain parameter may be sent: %s", p)
			}
		}
		var urls, inline []string
		for _, r := range res.Results {
			urls = append(urls, r.URL)
		}
		for _, c := range res.InlineContent {
			inline = append(inline, c.URL)
		}
		want := []string{"https://github.com/nicobailon/pi-web-access"}
		if !eqStrings(urls, want) || !eqStrings(inline, want) {
			t.Fatalf("%v %v", urls, inline)
		}
	})
	tw(t, F, "une erreur HTTP remonte le statut sans jamais laisser fuir la clé", func(t *testing.T) {
		setup(t, map[string]string{"SERPDIVE_API_KEY": "serpdive-secret-key"})
		useNet(t, func(netCall) netReply {
			return reply(401, `{"error":"invalid_api_key","message":"This API key is invalid or was revoked. key=serpdive-secret-key"}`)
		})
		_, err := SearchWithSerpdive(bg(), "q", SearchOptions{})
		wantErr(t, err, `SERPdive API error 401`)
		wantErr(t, err, `invalid_api_key`)
		if strings.Contains(err.Error(), "serpdive-secret-key") {
			t.Fatal("key leaked: " + err.Error())
		}
	})
	tw(t, F, "la clé passe par le chemin de credentials partagé, résolue à la requête", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell credential command")
		}
		_, dir := isolate(t)
		unsetenv(t, "SERPDIVE_API_KEY", "SERPDIVE_MODEL")
		marker := filepath.Join(dir, "serpdive-resolver-ran")
		writeConfig(t, dir, fmt.Sprintf(`{"serpdiveApiKey":%q}`, "!touch "+marker+" && printf serpdive-command-key"))
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"results":[]}`) })
		availableBefore := IsSerpdiveAvailable()
		_, err := os.Stat(marker)
		ranBefore := err == nil
		_, err = SearchWithSerpdive(bg(), "q", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		_, err = os.Stat(marker)
		if !availableBefore || ranBefore || err != nil || fn.calls[0].Header.Get("Authorization") != "Bearer serpdive-command-key" {
			t.Fatalf("%v %v %v %v", availableBefore, ranBefore, err, fn.calls[0].Header)
		}
	})
}
