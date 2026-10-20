package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twin of test/brightdata-serp. The upstream activity-log assertions have no counterpart: this
// port keeps no activity log (the activity widget belongs to the curator slice).

const bdSerpBody = `{"organic":[` +
	`{"link":"https://github.com/nicobailon/pi-web-access","title":"Repo","description":"repo   description"},` +
	`{"link":"https://gist.github.com/nicobailon/abc","title":"Gist","description":"gist description"},` +
	`{"link":"https://example.com/nope","title":"Example","description":"example description"},` +
	`{"title":"Sponsored","description":"no link"}]}`

// bdSetup isolates the environment, writes the config (when not empty) and sets the variables.
func bdSetup(t *testing.T, cfg string, env map[string]string) string {
	t.Helper()
	_, dir := isolate(t)
	unsetenv(t, "BRIGHTDATA_SERP_ZONE", "BRIGHTDATA_UNLOCKER_ZONE")
	if cfg != "" {
		writeConfig(t, dir, cfg)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	return dir
}

var bdKeyZone = map[string]string{"BRIGHTDATA_API_KEY": "bd-test-key", "BRIGHTDATA_SERP_ZONE": "pi_serp"}

func bdRequestBody(t *testing.T, c netCall) (body map[string]any, serp *url.URL) {
	t.Helper()
	body = jsonBody(t, c)
	u, err := url.Parse(body["url"].(string))
	noErr(t, err)
	return body, u
}

func bdSearch(t *testing.T, body string, o SearchOptions) (*SearchResponse, *fakeNet, error) {
	t.Helper()
	fn := useNet(t, func(netCall) netReply { return reply(200, body) })
	r, err := SearchWithBrightData(bg(), "q", o)
	return r, fn, err
}

var bdStatusShapedRE = regexp.MustCompile(`(?i)\b(?:error|status|http)[\s:=-]{1,4}\d{3}\b`)

// bdRoutedError runs a routed search where Bright Data answers `serve` and Brave would answer.
func bdRoutedError(t *testing.T, cfg string, serve netReply) (error, []string) {
	t.Helper()
	bdSetup(t, cfg, map[string]string{"BRAVE_API_KEY": "brave-key"})
	fn := useNet(t, func(c netCall) netReply {
		if c.URL == "https://api.brightdata.com/request" {
			return serve
		}
		return jsonReply(map[string]any{"web": map[string]any{"results": []map[string]any{{"title": "brave", "url": "https://example.com/brave", "description": "ok"}}}})
	})
	_, err := Search(bg(), "forged", FullSearchOptions{Provider: Auto})
	return err, fn.urls()
}

const bdRouting = `"brightdataApiKey":"bd-test-key","brightdataSerpZone":"pi_serp","searchRouting":{"providers":["brightdata","brave"],"fallbackOn":["transient"]}`

func TestUpstream_brightdata_serp(t *testing.T) {
	const f = "brightdata-serp"

	tw(t, f, "the SERP request carries the configured zone, the parsed_light format, and brd_json=1", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		res, err := SearchWithBrightData(bg(), "vector databases", SearchOptions{NumResults: nf(3)})
		noErr(t, err)
		c := fn.calls[0]
		body, serp := bdRequestBody(t, c)
		if c.URL != "https://api.brightdata.com/request" || c.Header.Get("Authorization") != "Bearer bd-test-key" || body["zone"] != "pi_serp" || body["format"] != "raw" || body["data_format"] != "parsed_light" {
			t.Fatalf("%v", c)
		}
		q := serp.Query()
		if serp.Host != "www.google.com" || serp.Path != "/search" || q.Get("q") != "vector databases" || q.Get("brd_json") != "1" || q.Has("tbs") {
			t.Fatalf("%v", serp)
		}
		want := []SearchResult{
			{Title: "Repo", URL: "https://github.com/nicobailon/pi-web-access", Snippet: "repo description"},
			{Title: "Gist", URL: "https://gist.github.com/nicobailon/abc", Snippet: "gist description"},
			{Title: "Example", URL: "https://example.com/nope", Snippet: "example description"},
		}
		if !reflect.DeepEqual(res.Results, want) {
			t.Fatalf("%+v", res.Results)
		}
		matchRE(t, `repo description\nSource: Repo \(https://github\.com/nicobailon/pi-web-access\)`, res.Answer)
	})

	tw(t, f, "a SERP zone returns no page bodies, so includeContent never produces inline content", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		res, _, err := bdSearch(t, bdSerpBody, SearchOptions{IncludeContent: true})
		noErr(t, err)
		if res.InlineContent != nil {
			t.Fatal(res.InlineContent)
		}
		b, _ := json.Marshal(res)
		if string(b) != `{"answer":`+string(mustJSON(res.Answer))+`,"results":`+string(mustJSON(res.Results))+`}` {
			t.Fatal(string(b))
		}
	})

	tw(t, f, "numResults caps the returned results and the SERP page size", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		num := func() string {
			_, serp := bdRequestBody(t, fn.calls[len(fn.calls)-1])
			return serp.Query().Get("num")
		}
		few, _ := SearchWithBrightData(bg(), "q", SearchOptions{NumResults: nf(2)})
		fewNum := num()
		many, _ := SearchWithBrightData(bg(), "q", SearchOptions{NumResults: nf(50)})
		manyNum := num()
		none, _ := SearchWithBrightData(bg(), "q", SearchOptions{})
		noneNum := num()
		if len(few.Results) != 2 || fewNum != "7" || len(many.Results) != 3 || manyNum != "20" || len(none.Results) != 3 || noneNum != "10" {
			t.Fatalf("%d %s %d %s %d %s", len(few.Results), fewNum, len(many.Results), manyNum, len(none.Results), noneNum)
		}
	})

	tw(t, f, "recency is Google's own tbs filter, not a query hint and not a body parameter", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		for recency, tbs := range map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"} {
			_, err := SearchWithBrightData(bg(), "ai news", SearchOptions{RecencyFilter: recency})
			noErr(t, err)
			body, serp := bdRequestBody(t, fn.calls[len(fn.calls)-1])
			if serp.Query().Get("tbs") != tbs || serp.Query().Get("q") != "ai news" {
				t.Fatalf("%s: %v", recency, serp)
			}
			for _, p := range []string{"recency", "recency_minutes", "time_range", "start_date", "end_date", "days"} {
				if _, has := body[p]; has {
					t.Fatalf("%s is part of the request body", p)
				}
			}
		}
	})

	tw(t, f, "domain filters are sent to the engine as site: operators and re-applied locally", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		res, err := SearchWithBrightData(bg(), "sdk docs", SearchOptions{DomainFilter: []string{"github.com", "-gist.github.com"}, NumResults: nf(5)})
		noErr(t, err)
		multi, err := SearchWithBrightData(bg(), "sdk docs", SearchOptions{DomainFilter: []string{"github.com", "example.com"}})
		noErr(t, err)
		_, serp := bdRequestBody(t, fn.calls[len(fn.calls)-1])
		if serp.Query().Get("q") != "sdk docs site:github.com OR site:example.com" {
			t.Fatal(serp.Query().Get("q"))
		}
		if !reflect.DeepEqual(urlsOf(res.Results), []string{"https://github.com/nicobailon/pi-web-access"}) ||
			!reflect.DeepEqual(urlsOf(multi.Results), []string{"https://github.com/nicobailon/pi-web-access", "https://gist.github.com/nicobailon/abc", "https://example.com/nope"}) {
			t.Fatalf("%v %v", urlsOf(res.Results), urlsOf(multi.Results))
		}
	})

	tw(t, f, "availability requires both halves of the configuration and never spends to find out", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"pi_serp"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		if !IsBrightDataAvailable() || fn.count() != 0 {
			t.Fatal("a complete configuration must be available without a request")
		}
		for _, c := range []struct{ label, cfg, want string }{
			{"key only", `{"brightdataApiKey":"bd-test-key"}`, `Bright Data SERP zone is invalid or missing`},
			{"zone only", `{"brightdataSerpZone":"pi_serp"}`, `Bright Data API key not found`},
			{"neither", `{}`, `Bright Data SERP zone is invalid or missing`},
		} {
			bdSetup(t, c.cfg, nil)
			fn := useNet(t, func(netCall) netReply { return reply(200, "") })
			avail := IsBrightDataAvailable()
			_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
			if avail || fn.count() != 0 {
				t.Fatalf("%s: available=%v calls=%d", c.label, avail, fn.count())
			}
			wantErr(t, err, c.want)
		}
	})

	tw(t, f, "a malformed zone is rejected before the request instead of becoming an opaque 400", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"https://not-a-zone/"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, "") })
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		wantErr(t, err, `brightdataSerpZone in .*web-search\.json must be a zone name`)
		wantErr(t, err, `https://not-a-zone/`)
		wantErr(t, err, "must be of Bright Data type `serp`")
		if fn.count() != 0 {
			t.Fatal("a request was sent")
		}
	})

	tw(t, f, "an invalid zone makes Bright Data unavailable, never fatal for the other providers", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","searchRouting":{"providers":["brightdata","brave"],"fallbackOn":["transient"]}}`, map[string]string{"BRIGHTDATA_SERP_ZONE": "pi serp", "BRAVE_API_KEY": "brave-key"})
		fn := useNet(t, func(c netCall) netReply {
			if strings.HasPrefix(c.URL, "https://api.brightdata.com/") {
				t.Error("unreachable: zone was invalid")
			}
			return jsonReply(map[string]any{"web": map[string]any{"results": []map[string]any{{"title": "brave", "url": "https://example.com/brave", "description": "ok"}}}})
		})
		available := IsBrightDataAvailable()
		routed, err := Search(bg(), "q", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		_, reqErr := SearchWithBrightData(bg(), "q", SearchOptions{})
		for _, u := range fn.urls() {
			if strings.HasPrefix(u, "https://api.brightdata.com/") {
				t.Fatal("the invalid zone reached the paid endpoint")
			}
		}
		if available || routed.Provider != "brave" {
			t.Fatalf("%v %s", available, routed.Provider)
		}
		wantErr(t, reqErr, `Bright Data SERP zone is invalid: BRIGHTDATA_SERP_ZONE must be a zone name`)
		wantErr(t, reqErr, `pi serp`)
	})

	tw(t, f, "an Unlocker zone is never substituted for a missing SERP zone", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataUnlockerZone":"pi_unlocker"}`, map[string]string{"BRIGHTDATA_UNLOCKER_ZONE": "pi_unlocker_env"})
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		available := IsBrightDataAvailable()
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		if available || fn.count() != 0 {
			t.Fatalf("%v %d", available, fn.count())
		}
		wantErr(t, err, `Bright Data SERP zone is invalid or missing`)
		if strings.Contains(err.Error(), "pi_unlocker") {
			t.Fatal(err)
		}
	})

	tw(t, f, "an HTTP error surfaces the status and never leaks the key", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-secret"}`, map[string]string{"BRIGHTDATA_SERP_ZONE": "pi_serp"})
		useNet(t, func(netCall) netReply {
			return reply(401, `{"error":"unauthorized","message":"token bd-secret is not valid for this zone"}`)
		})
		res, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		if res != nil {
			t.Fatal("results returned")
		}
		wantErr(t, err, `Bright Data API error 401`)
		wantErr(t, err, `unauthorized`)
		wantErr(t, err, `\[redacted\]`)
		if strings.Contains(err.Error(), "bd-secret") {
			t.Fatal(err)
		}
	})

	tw(t, f, "a billed 200 that is not SERP JSON fails loudly and quotes what came back", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		bodies := []string{
			"<!DOCTYPE html><title>Our systems have detected unusual traffic</title>",
			"",
			`[{"link":"https://example.com/"}]`,
			`{"organic":"not-an-array"}`,
			`{"organic":["https://example.com/"]}`,
			`{"error":"zone not found","code":"zone_missing"}`,
			`{"errors":[{"message":"bad zone type"}]}`,
			`{"status":"ok","general":{"search_engine":"google"}}`,
		}
		i := 0
		useNet(t, func(netCall) netReply { i++; return reply(200, bodies[i-1]) })
		var errs []string
		for range bodies {
			r, err := SearchWithBrightData(bg(), "q", SearchOptions{})
			if err == nil {
				t.Fatalf("returned instead of throwing: %+v", r)
			}
			errs = append(errs, err.Error())
		}
		const inv = `Bright Data API returned invalid response for zone pi_serp: `
		for idx, re := range []string{
			`Bright Data API returned invalid JSON for zone pi_serp:.*unusual traffic`,
			`Bright Data API returned empty response for zone pi_serp`,
			inv + `expected an object envelope`,
			inv + `expected organic array`,
			inv + `expected organic\[0\] object`,
			inv + `Bright Data reported an error instead of a SERP.*zone not found.*code zone_missing`,
			inv + `Bright Data reported an error instead of a SERP.*bad zone type`,
			`expected an organic array and the envelope carried none.*` + "`unblocker`",
		} {
			matchRE(t, re, errs[idx])
		}
	})

	tw(t, f, "a quoted upstream body cannot forge our HTTP status for routing", func(t *testing.T) {
		for _, body := range []string{
			"<!DOCTYPE html><p>Error 503 from the proxied page</p>",
			"HTTP 503 from the proxied page",
			`{"error":"Error 503 from the proxied page","code":"upstream_failed"}`,
			`{"errors":"status: 503 from the proxied page"}`,
		} {
			err, calls := bdRoutedError(t, `{`+bdRouting+`}`, reply(200, body))
			if err == nil {
				t.Fatalf("%s: no error", body)
			}
			matchRE(t, `brightdata search failed \(invalid-response\)`, err.Error())
			matchRE(t, `upstream 503`, err.Error())
			noMatchRE(t, `(?i)(?:error|status|http)\s+503`, err.Error())
			if !eqStrings(calls, []string{"https://api.brightdata.com/request"}) {
				t.Fatalf("%s: %v", body, calls)
			}
		}
	})

	tw(t, f, "truncating a quoted body cannot re-create a status the sanitiser left alone", func(t *testing.T) {
		for label, body := range map[string]string{
			"non-JSON body padded to the 300-char cut":              strings.Repeat(".", 291) + "error 5031",
			"Bright Data error envelope padded to the 200-char cut": `{"error":"` + strings.Repeat(".", 191) + `error 5031"}`,
		} {
			err, calls := bdRoutedError(t, `{`+bdRouting+`}`, reply(200, body))
			if err == nil {
				t.Fatalf("%s: no error", label)
			}
			matchRE(t, `brightdata search failed \(invalid-response\)`, err.Error())
			noMatchRE(t, `(?i)\b(?:error|status|http)[\s:=-]{1,4}503\b`, err.Error())
			if !eqStrings(calls, []string{"https://api.brightdata.com/request"}) {
				t.Fatalf("%s: %v", label, calls)
			}
		}
	})

	tw(t, f, "truncation cannot forge a status in a non-2xx message or a rejected-zone echo", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		useNet(t, func(netCall) netReply { return reply(404, strings.Repeat(".", 291)+"error 5031") })
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		if got := bdStatusShapedRE.FindAllString(err.Error(), -1); !reflect.DeepEqual(got, []string{"error 404"}) {
			t.Fatalf("%v", got)
		}
		matchRE(t, `upstream 503`, err.Error())

		bdSetup(t, "", map[string]string{"BRIGHTDATA_API_KEY": "bd-test-key", "BRIGHTDATA_SERP_ZONE": strings.Repeat(".", 51) + "error 5031"})
		fn := useNet(t, func(netCall) netReply { return reply(200, "") })
		_, err = SearchWithBrightData(bg(), "q", SearchOptions{})
		if fn.count() != 0 || bdStatusShapedRE.FindAllString(err.Error(), -1) != nil {
			t.Fatalf("%d %v", fn.count(), err)
		}
		wantErr(t, err, `Bright Data SERP zone is invalid: BRIGHTDATA_SERP_ZONE must be a zone name`)
		wantErr(t, err, `upstream 503`)
	})

	tw(t, f, "an empty organic array is a legitimate empty result, not an error", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		res, _, err := bdSearch(t, `{"organic":[]}`, SearchOptions{})
		noErr(t, err)
		b, _ := json.Marshal(res)
		if string(b) != `{"answer":"","results":[]}` {
			t.Fatal(string(b))
		}
	})

	tw(t, f, "a malformed config root fails explicitly on the request path and only there", func(t *testing.T) {
		bdSetup(t, `[]`, map[string]string{"BRIGHTDATA_SERP_ZONE": "pi_serp"})
		available := IsBrightDataAvailable()
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		wantErr(t, err, `Invalid config in .*web-search\.json: expected a JSON object`)
		if available {
			t.Fatal("available")
		}
	})

	tw(t, f, "aborting propagates instead of returning an empty answer", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		useNet(t, func(netCall) netReply { return netReply{Err: errors.New("The operation was aborted")} })
		ctx, cancel := context.WithCancel(bg())
		cancel()
		_, err := SearchWithBrightData(ctx, "cancel", SearchOptions{})
		if err == nil || !isAbortError(err) {
			t.Fatalf("%v", err)
		}
	})

	tw(t, f, "the key uses the shared credential-source path, resolved at request time", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX shell credential command")
		}
		_, dir := isolate(t)
		marker := filepath.Join(dir, "brightdata-resolver-ran")
		writeConfig(t, dir, fmt.Sprintf(`{"brightdataApiKey":%q,"brightdataSerpZone":"pi_serp"}`, "!touch "+marker+" && printf bd-command-key"))
		availableBefore := IsBrightDataAvailable()
		_, statErr := os.Stat(marker)
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"organic":[]}`) })
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{NumResults: nf(1)})
		noErr(t, err)
		_, afterErr := os.Stat(marker)
		if !availableBefore || !os.IsNotExist(statErr) || afterErr != nil || fn.calls[0].Header.Get("Authorization") != "Bearer bd-command-key" {
			t.Fatalf("%v %v %v", availableBefore, statErr, afterErr)
		}
	})

	tw(t, f, "explicit brightdata routing reaches the provider and is attributed", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"pi_serp"}`, nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, bdSerpBody) })
		res, err := Search(bg(), "route", FullSearchOptions{Provider: ProviderSelection{Name: "brightdata"}})
		noErr(t, err)
		if res.Provider != "brightdata" || !eqStrings(fn.urls(), []string{"https://api.brightdata.com/request"}) || res.Results[0].URL != "https://github.com/nicobailon/pi-web-access" {
			t.Fatalf("%s %v", res.Provider, fn.urls())
		}
	})

	tw(t, f, "no error path prints the API token, at any site that quotes foreign text", func(t *testing.T) {
		const token = "bd-plant-1234"
		bdSetup(t, "", map[string]string{"BRIGHTDATA_API_KEY": token, "BRIGHTDATA_SERP_ZONE": "pi_serp"})
		bodies := []netReply{
			reply(401, `{"error":"unauthorized","message":"token `+token+` is not valid"}`),
			reply(200, token+" dead"),
			reply(200, `{"error":"token `+token+` is not valid for this zone","code":"auth_failed"}`),
		}
		i := 0
		useNet(t, func(netCall) netReply {
			if i < len(bodies) {
				i++
				return bodies[i-1]
			}
			return netReply{Err: errors.New("connect ECONNREFUSED while sending token " + token)}
		})
		var msgs []string
		for range 4 {
			_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
			if err == nil {
				t.Fatal("did not throw")
			}
			msgs = append(msgs, err.Error())
		}
		for idx, m := range msgs {
			if strings.Contains(m, token) || !strings.Contains(m, "[redacted]") {
				t.Fatalf("%d: %s", idx, m)
			}
		}
		if ok, _ := regexp.MatchString(`^Bright Data API returned invalid JSON for zone pi_serp: \[redacted\] dead$`, msgs[1]); !ok || strings.Contains(msgs[1], "Unexpected token") {
			t.Fatal(msgs[1])
		}
		for _, idx := range []int{0, 1, 2} {
			matchRE(t, `for zone pi_serp`, msgs[idx])
		}
		noMatchRE(t, `pi_serp`, msgs[3])
	})

	tw(t, f, "no prefix of the credential survives into a message, however the body is shaped", func(t *testing.T) {
		const token = "abcdef12-3456-7890-abcd-ef1234567890"
		bdSetup(t, "", map[string]string{"BRIGHTDATA_API_KEY": token, "BRIGHTDATA_SERP_ZONE": "pi_serp"})
		bodies := []string{token + " is expired, contact support", `{"error":"` + token + ` is not valid for this zone"}`, `{ "organic": [ ` + token, token}
		i := 0
		useNet(t, func(netCall) netReply { i++; return reply(200, bodies[i-1]) })
		for idx := range bodies {
			_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
			if err == nil {
				t.Fatalf("body %d did not throw", idx)
			}
			for n := 4; n <= len(token); n++ {
				if strings.Contains(err.Error(), token[:n]) {
					t.Fatalf("body %d leaked a %d-character prefix: %s", idx, n, err)
				}
			}
		}
	})

	tw(t, f, "a billed 200 quoting rate-limit text cannot classify as a silently-retryable failure", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		bodies := []string{
			"<html>You have exceeded your rate limit for this zone.</html>",
			`{"error":"too many requests for this zone"}`,
			`{"error":"quota exhausted"}`,
			"<html>Service Unavailable - server error</html>",
			"<html>socket timed out, network unreachable</html>",
		}
		i := 0
		useNet(t, func(netCall) netReply { i++; return reply(200, bodies[i-1]) })
		for idx := range bodies {
			_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
			if err == nil {
				t.Fatal("a billed 200 did not throw")
			}
			if kind := ClassifyProviderError("brightdata", err).Kind; kind != "invalid-response" {
				t.Fatalf("body %d classified as %s: %s", idx, kind, err)
			}
			if idx == 0 {
				matchRE(t, `upstream rate-limit notice`, err.Error())
			}
		}
	})

	tskip(t, f, "the activity entry is opened once and closed with the outcome that happened", "this port keeps no activity log (the activity widget and its entries belong to the curator slice), so there is no entry to open or close; the request, status and error behaviour it accompanies is pinned by the neighbouring cases")

	tw(t, f, "each request carries a 60-second timeout composed with the caller's signal", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"organic":[]}`) })
		_, err := SearchWithBrightData(bg(), "no caller signal", SearchOptions{})
		noErr(t, err)
		dl, ok := fn.calls[0].Ctx.Deadline()
		if left := time.Until(dl); !ok || left < 55*time.Second || left > 61*time.Second {
			t.Fatalf("deadline %v ok=%v", left, ok)
		}
		// The caller's cancellation reaches the request: a request that never answers ends when the
		// caller gives up, long before the 60 seconds.
		useNet(t, func(netCall) netReply { return netReply{Hang: true} })
		ctx, cancel := context.WithCancel(bg())
		time.AfterFunc(50*time.Millisecond, cancel)
		start := time.Now()
		_, err = SearchWithBrightData(ctx, "caller signal", SearchOptions{})
		if err == nil || !isAbortError(err) || time.Since(start) > 10*time.Second {
			t.Fatalf("%v after %v", err, time.Since(start))
		}
	})

	tw(t, f, "BRIGHTDATA_SERP_ZONE takes precedence over brightdataSerpZone, valid or not", func(t *testing.T) {
		cfg := `{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"config_zone"}`
		run := func(env map[string]string) (zone string, calls int, err error) {
			bdSetup(t, cfg, env)
			fn := useNet(t, func(netCall) netReply { return reply(200, `{"organic":[]}`) })
			_, err = SearchWithBrightData(bg(), "q", SearchOptions{})
			if len(fn.calls) > 0 {
				zone, _ = jsonBody(t, fn.calls[0])["zone"].(string)
			}
			return zone, fn.count(), err
		}
		if z, n, err := run(map[string]string{"BRIGHTDATA_SERP_ZONE": "env_zone"}); z != "env_zone" || n != 1 || err != nil {
			t.Fatalf("%s %d %v", z, n, err)
		}
		z, n, err := run(map[string]string{"BRIGHTDATA_SERP_ZONE": "not a zone"})
		if z != "" || n != 0 {
			t.Fatalf("a malformed env zone fell back to the config zone: %s %d", z, n)
		}
		wantErr(t, err, `Bright Data SERP zone is invalid: BRIGHTDATA_SERP_ZONE must be a zone name`)
		if strings.Contains(err.Error(), "config_zone") {
			t.Fatal(err)
		}
		if z, n, err := run(nil); z != "config_zone" || n != 1 || err != nil {
			t.Fatalf("%s %d %v", z, n, err)
		}
	})

	tw(t, f, "an unparseable config file throws without echoing its contents", func(t *testing.T) {
		bdSetup(t, "{\"k\": bd-planted-token-1234}\n", nil)
		fn := useNet(t, func(netCall) netReply { return reply(200, "") })
		available := IsBrightDataAvailable()
		_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		wantErr(t, err, `^Failed to parse .*web-search\.json: not valid JSON$`)
		if strings.Contains(err.Error(), "bd-planted-token-1234") || fn.count() != 0 || available {
			t.Fatalf("%v %d %v", err, fn.count(), available)
		}

		bdSetup(t, "{\"brightdataApiKey\": \"bd-test-key\",}\n", nil)
		_, err = SearchWithBrightData(bg(), "q", SearchOptions{})
		wantErr(t, err, `Failed to parse .*web-search\.json: not valid JSON, at position 35 \(line 1 column 36\)$`)
		if strings.Contains(err.Error(), "bd-test-key") {
			t.Fatal(err)
		}

		bdSetup(t, "{\"k\": error 503}\n", nil)
		_, err = SearchWithBrightData(bg(), "q", SearchOptions{})
		wantErr(t, err, `Failed to parse .*web-search\.json: not valid JSON`)
		if bdStatusShapedRE.FindAllString(err.Error(), -1) != nil {
			t.Fatal(err)
		}
	})

	tw(t, f, "Bright Data's 200 error envelope is reported whatever shape it arrives in", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		bodies := []string{`{"error":{"reason":"wrong zone type"},"error_code":42}`, `{"errors":"two things went wrong","code":7}`, `{"error":"` + strings.Repeat("B", 400) + `"}`}
		i := 0
		useNet(t, func(netCall) netReply { i++; return reply(200, bodies[i-1]) })
		var errs []string
		for range bodies {
			_, err := SearchWithBrightData(bg(), "q", SearchOptions{})
			if err == nil {
				t.Fatal("no error")
			}
			errs = append(errs, err.Error())
		}
		matchRE(t, `Bright Data reported an error instead of a SERP: \{"reason":"wrong zone type"\}`, errs[0])
		matchRE(t, `error_code 42`, errs[0])
		matchRE(t, `Bright Data reported an error instead of a SERP: two things went wrong`, errs[1])
		matchRE(t, `code 7`, errs[1])
		matchRE(t, `instead of a SERP: B{200}$`, errs[2])
	})

	tw(t, f, "site: operators, filter normalization and an unknown recency window", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		fn := useNet(t, func(netCall) netReply { return reply(200, `{"organic":[]}`) })
		for _, o := range []SearchOptions{
			{DomainFilter: []string{"github.com"}},
			{DomainFilter: []string{"-example.com"}},
			{DomainFilter: []string{"https://GitHub.com/some/path", "github.com", "not a domain", "-", "  ", "-Gist.GitHub.com"}},
			{RecencyFilter: "decade"},
			{NumResults: nf(nan())},
		} {
			_, err := SearchWithBrightData(bg(), "q", o)
			noErr(t, err)
		}
		var sent []*url.URL
		for _, c := range fn.calls {
			_, u := bdRequestBody(t, c)
			sent = append(sent, u)
		}
		if sent[0].Query().Get("q") != "q site:github.com" || sent[1].Query().Get("q") != "q -site:example.com" || sent[2].Query().Get("q") != "q site:github.com -site:gist.github.com" ||
			sent[3].Query().Has("tbs") || sent[3].Query().Get("q") != "q" || sent[4].Query().Get("num") != "10" {
			t.Fatalf("%v", sent)
		}
	})

	tw(t, f, "organic entries missing fields are mapped, and unparseable links are dropped", func(t *testing.T) {
		bdSetup(t, "", bdKeyZone)
		organic := `{"organic":[{"link":"https://a.example/1"},{"link":"not-a-url","title":"Bad link"},{"link":"https://b.example/2","title":"T2","description":"  s2   s2  "}]}`
		useNet(t, func(netCall) netReply { return reply(200, organic) })
		unfiltered, err := SearchWithBrightData(bg(), "q", SearchOptions{})
		noErr(t, err)
		filtered, err := SearchWithBrightData(bg(), "q", SearchOptions{DomainFilter: []string{"b.example"}})
		noErr(t, err)
		want := []SearchResult{{Title: "Source 1", URL: "https://a.example/1"}, {Title: "Bad link", URL: "not-a-url"}, {Title: "T2", URL: "https://b.example/2", Snippet: "s2 s2"}}
		if !reflect.DeepEqual(unfiltered.Results, want) ||
			unfiltered.Answer != "Source: Source 1 (https://a.example/1)\n\nSource: Bad link (not-a-url)\n\ns2 s2\nSource: T2 (https://b.example/2)" ||
			!reflect.DeepEqual(filtered.Results, want[2:]) {
			t.Fatalf("%+v %+v", unfiltered, filtered)
		}
	})

	tw(t, f, "Bright Data is never selected by auto without explicit routing", func(t *testing.T) {
		bdSetup(t, `{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"pi_serp"}`, nil)
		fn := useNet(t, func(netCall) netReply { return netReply{Err: errors.New("unexpected auto provider")} })
		_, err := Search(bg(), "auto", FullSearchOptions{Provider: Auto})
		for _, u := range fn.urls() {
			if strings.HasPrefix(u, "https://api.brightdata.com/") {
				t.Fatal(u)
			}
		}
		if err != nil && strings.Contains(err.Error(), "Bright Data") {
			t.Fatal(err)
		}
	})

	tw(t, f, "Bright Data HTTP ${status} is classified as ${kind}", func(t *testing.T) {
		for _, c := range []struct {
			status int
			kind   string
		}{{400, "invalid-request"}, {401, "auth"}, {403, "auth"}, {402, "quota"}, {429, "quota"}, {500, "transient"}, {503, "transient"}, {504, "transient"}} {
			fallbackOn := c.kind
			if c.kind == "auth" || c.kind == "invalid-request" {
				fallbackOn = "quota"
			}
			cfg := fmt.Sprintf(`{"brightdataApiKey":"bd-test-key","brightdataSerpZone":"pi_serp","searchRouting":{"providers":["brightdata","brave"],"fallbackOn":[%q]}}`, fallbackOn)
			bdSetup(t, cfg, map[string]string{"BRAVE_API_KEY": "brave-key"})
			fn := useNet(t, func(call netCall) netReply {
				if call.URL == "https://api.brightdata.com/request" {
					return reply(c.status, "provider failure")
				}
				return jsonReply(map[string]any{"web": map[string]any{"results": []map[string]any{{"title": "fallback", "url": "https://example.com/fallback", "description": "ok"}}}})
			})
			res, err := Search(bg(), "classified", FullSearchOptions{Provider: Auto})
			if c.kind == "auth" || c.kind == "invalid-request" {
				if err == nil {
					t.Fatalf("%d: fell through", c.status)
				}
				matchRE(t, fmt.Sprintf(`brightdata search failed \(%s\)`, c.kind), err.Error())
				if !eqStrings(fn.urls(), []string{"https://api.brightdata.com/request"}) {
					t.Fatalf("%d: %v", c.status, fn.urls())
				}
				continue
			}
			if err != nil || res.Provider != "brave" || fn.count() != 2 {
				t.Fatalf("%d: %v %v", c.status, err, fn.urls())
			}
		}
	})
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
