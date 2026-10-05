package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// Tests added by mutation checking: branches the upstream tests do not pin.

func TestFetchRemoteURLRedirectLimit(t *testing.T) {
	for _, limit := range []int{0, 2} {
		want := limit
		if want == 0 {
			want = 5 // the original allows five redirects (a literal on purpose)
		}
		fetches := 0
		loop := func(_ context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
			fetches++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://93.184.216.34/next" + strconv.Itoa(fetches)}}, Body: http.NoBody}, nil
		}
		_, err := FetchRemoteURL(bg(), "https://93.184.216.34/start", RequestInit{}, FetchRemoteOptions{Fetch: loop, MaxRedirects: limit})
		wantErr(t, err, `Too many redirects fetching https://93\.184\.216\.34/next`+strconv.Itoa(want))
		if fetches != want+1 {
			t.Fatalf("limit %d: %d fetches", limit, fetches)
		}
	}
}

func TestTavilyRetriesOnlyQuotaAndAuthStatuses(t *testing.T) {
	for status, retry := range map[int]bool{401: true, 402: true, 403: true, 429: true, 432: true, 400: false, 404: false, 500: false, 503: false} {
		isolate(t)
		t.Setenv("TAVILY_API_KEY_1", "first")
		t.Setenv("TAVILY_API_KEY_2", "second")
		var keys []string
		useNet(t, func(c netCall) netReply {
			keys = append(keys, strings.TrimPrefix(c.Header.Get("Authorization"), "Bearer "))
			return reply(status, "denied")
		})
		_, err := SearchWithTavily(bg(), "q", SearchOptions{})
		if err == nil {
			t.Fatalf("%d: expected an error", status)
		}
		if retry != (len(keys) == 2) {
			t.Errorf("status %d: retried=%v, keys %v", status, len(keys) == 2, keys)
		}
	}
}

func TestToolErrorTexts(t *testing.T) {
	r, _ := newRuntime(t, "")
	get := mustTool(t, r, "get_search_content")
	StoreResult("hardening1", &StoredSearchData{ID: "hardening1", Type: "search", Timestamp: nowMs(), Queries: []QueryResultData{{Query: "q", Answer: "a"}}})
	out := run(t, get, map[string]any{"responseId": "hardening1", "queryIndex": 0.0, "offset": -1.0})
	matchRE(t, `^Invalid offset: received -1 for .*; offset must be a non-negative integer\. Use 0 or a larger integer\.$`, out.Text())
}

func TestWebEnableUnavailableText(t *testing.T) {
	st := activationRun(t, `{}`, activationOpts{activate: true, unavailable: []string{"source_check", "fetch_content"}})
	if st.result.Text() != "Cannot enable unavailable tools: source_check, fetch_content." || !st.result.IsError {
		t.Fatalf("%+v", st.result)
	}
}
