package websearch

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Twin of test/searchinfinity.

func TestUpstream_searchinfinity(t *testing.T) {
	const f = "searchinfinity"

	tw(t, f, "Searchinfinity availability reads environment and config credentials", func(t *testing.T) {
		_, dir := isolate(t)
		if IsSearchinfinityAvailable() {
			t.Fatal("available without a credential")
		}
		t.Setenv("SEARCHINFINITY_API_KEY", "synthetic-searchinfinity-env-key")
		if !IsSearchinfinityAvailable() {
			t.Fatal("env key ignored")
		}
		unsetenv(t, "SEARCHINFINITY_API_KEY")
		writeConfig(t, dir, `{"searchinfinityApiKey":"synthetic-searchinfinity-config-key"}`)
		if !IsSearchinfinityAvailable() {
			t.Fatal("config key ignored")
		}
	})

	tw(t, f, "explicit Searchinfinity search maps filters and prefers model summaries", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"provider":"searchinfinity"}`)
		t.Setenv("SEARCHINFINITY_API_KEY", "synthetic-searchinfinity-test-key")
		fn := useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"ResponseMetadata": map[string]any{"RequestId": "req-1"}, "Result": map[string]any{"ResultCount": 1,
				"WebResults": []map[string]any{{"Id": "abc", "SortId": 1, "Title": "Searchinfinity Docs", "Url": "https://www.byteplus.com/docs", "Snippet": "  short   snippet ",
					"Summary": "  Model   generated summary. ", "PublishTime": "2026-07-30T00:00:00+08:00"}},
				"SearchContext": map[string]any{"OriginQuery": "searchinfinity docs", "SearchType": "web"}, "TimeCost": 100, "LogId": "req-1"}})
		})
		res, err := Search(bg(), "searchinfinity docs", FullSearchOptions{Provider: ProviderSelection{Name: "searchinfinity"}, SearchOptions: SearchOptions{NumResults: nf(1), RecencyFilter: "week",
			DomainFilter: []string{"https://www.byteplus.com/docs", "-example.com"}}})
		noErr(t, err)
		c := fn.calls[0]
		want := map[string]any{"Query": "searchinfinity docs", "Count": 1.0, "Filter": map[string]any{"Sites": "www.byteplus.com", "BlockHosts": "example.com"}, "TimeRange": "OneWeek"}
		if len(fn.calls) != 1 || c.URL != "https://torchlight.byteintlapi.com/search_api/web_search" || c.Header.Get("Authorization") != "Bearer synthetic-searchinfinity-test-key" ||
			c.Header.Get("Content-Type") != "application/json" || !reflect.DeepEqual(jsonBody(t, c), want) {
			t.Fatalf("%v", c)
		}
		if res.Provider != "searchinfinity" || !reflect.DeepEqual(res.Results, []SearchResult{{Title: "Searchinfinity Docs", URL: "https://www.byteplus.com/docs", Snippet: "Model generated summary."}}) {
			t.Fatalf("%+v", res)
		}
	})

	tw(t, f, "Searchinfinity maps business error codes to HTTP semantics", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCHINFINITY_API_KEY", "synthetic-searchinfinity-test-key")
		useNet(t, func(c netCall) netReply {
			var body map[string]any
			_ = json.Unmarshal([]byte(c.Body), &body)
			if body["Query"] == "bad key" {
				return jsonReply(map[string]any{"ResponseMetadata": map[string]any{"RequestId": "req-2a", "Error": map[string]any{"CodeN": 700901, "Code": "invalid_api_key", "Message": "invalid api key"}}, "Result": nil})
			}
			return jsonReply(map[string]any{"ResponseMetadata": map[string]any{"RequestId": "req-2b", "Error": map[string]any{"CodeN": 700429, "Code": "700429", "Message": "QPS Exceeded"}}, "Result": nil})
		})
		var errs []string
		for _, q := range []string{"bad key", "too fast"} {
			_, err := SearchWithSearchinfinity(bg(), q, SearchOptions{})
			if err == nil {
				t.Fatal("no error")
			}
			errs = append(errs, err.Error())
		}
		matchRE(t, `Searchinfinity Search API error 401`, errs[0])
		matchRE(t, `invalid api key`, errs[0])
		matchRE(t, `code 700901 invalid_api_key`, errs[0])
		matchRE(t, `Searchinfinity Search API error 429`, errs[1])
		matchRE(t, `QPS Exceeded`, errs[1])
		matchRE(t, `code 700429`, errs[1])
	})

	tw(t, f, "Searchinfinity errors redact credentials", func(t *testing.T) {
		isolate(t)
		t.Setenv("SEARCHINFINITY_API_KEY", "synthetic-searchinfinity-secret")
		useNet(t, func(netCall) netReply { return reply(401, "rejected synthetic-searchinfinity-secret") })
		_, err := SearchWithSearchinfinity(bg(), "redact me", SearchOptions{})
		wantErr(t, err, `Searchinfinity Search API error 401`)
		if strings.Contains(err.Error(), "synthetic-searchinfinity-secret") || !strings.Contains(strings.ToLower(err.Error()), "[redacted]") {
			t.Fatal(err)
		}
	})

	tw(t, f, "configured searchRouting can select Searchinfinity", func(t *testing.T) {
		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["searchinfinity"],"fallbackOn":["network"]}}`)
		t.Setenv("SEARCHINFINITY_API_KEY", "synthetic-searchinfinity-test-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"ResponseMetadata": map[string]any{"RequestId": "req-3"}, "Result": map[string]any{"ResultCount": 1,
				"WebResults":    []map[string]any{{"Id": "x", "SortId": 1, "Title": "Routed", "Snippet": "Searchinfinity route", "Url": "https://example.com/routed"}},
				"SearchContext": map[string]any{"OriginQuery": "route", "SearchType": "web"}, "TimeCost": 10, "LogId": "req-3"}})
		})
		res, err := Search(bg(), "route", FullSearchOptions{Provider: Auto})
		noErr(t, err)
		if res.Provider != "searchinfinity" || res.Results[0].Title != "Routed" {
			t.Fatalf("%+v", res)
		}
	})
}
