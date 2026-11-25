package web

import (
	"reflect"
	"testing"
	"time"
)

func TestParseSearchQuery(t *testing.T) {
	// upstream: test/web/search/query.test.ts: "leaves plain queries untouched"
	t.Run("leaves plain queries untouched", func(t *testing.T) {
		q := ParseSearchQuery("rust async runtime comparison")
		if q.Text != "rust async runtime comparison" {
			t.Fatalf("Text = %q", q.Text)
		}
		if q.HasDirectives || q.HasConstraints {
			t.Fatalf("expected no directives/constraints: %+v", q)
		}
		got := termTexts(q.Terms)
		want := []string{"rust", "async", "runtime", "comparison"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("terms = %v, want %v", got, want)
		}
	})

	// upstream: test/web/search/query.test.ts: "keeps unknown colon tokens (URLs, paths, error codes) as text"
	t.Run("keeps unknown colon tokens as text", func(t *testing.T) {
		q := ParseSearchQuery(`error TS2345: https://example.com/a?b=c C:\Users\me`)
		if q.HasConstraints {
			t.Fatalf("expected HasConstraints=false")
		}
		want := []string{"error", "TS2345:", "https://example.com/a?b=c", `C:\Users\me`}
		if got := termTexts(q.Terms); !reflect.DeepEqual(got, want) {
			t.Fatalf("terms = %v, want %v", got, want)
		}
	})

	// upstream: test/web/search/query.test.ts: "parses site: aliases and exclusions with normalization"
	t.Run("parses site aliases and exclusions with normalization", func(t *testing.T) {
		q := ParseSearchQuery("kubernetes site:HTTPS://Docs.K8s.IO/ domain:cncf.io -site:*.reddit.com host:github.com/kubernetes")
		if !reflect.DeepEqual(q.Sites, []string{"docs.k8s.io", "cncf.io", "github.com/kubernetes"}) {
			t.Fatalf("Sites = %v", q.Sites)
		}
		if !reflect.DeepEqual(q.ExcludedSites, []string{"reddit.com"}) {
			t.Fatalf("ExcludedSites = %v", q.ExcludedSites)
		}
		if q.Text != "kubernetes" || !q.HasConstraints {
			t.Fatalf("unexpected text=%q hasConstraints=%v", q.Text, q.HasConstraints)
		}
	})

	// upstream: test/web/search/query.test.ts: "adopts the next token when a directive value is space-separated"
	t.Run("adopts next token when directive value is space-separated", func(t *testing.T) {
		q := ParseSearchQuery("site: arxiv.org transformer scaling")
		if !reflect.DeepEqual(q.Sites, []string{"arxiv.org"}) {
			t.Fatalf("Sites = %v", q.Sites)
		}
		if q.Text != "transformer scaling" {
			t.Fatalf("Text = %q", q.Text)
		}
	})

	// upstream: test/web/search/query.test.ts: "does not adopt operators or directives as space-separated values"
	t.Run("does not adopt operators or directives as space-separated values", func(t *testing.T) {
		q := ParseSearchQuery("site: OR site:example.com")
		if !reflect.DeepEqual(q.Sites, []string{"example.com"}) {
			t.Fatalf("Sites = %v", q.Sites)
		}
	})

	// upstream: test/web/search/query.test.ts: "parses inurl/intitle/intext variants including quoted values"
	t.Run("parses inurl/intitle/intext variants including quoted values", func(t *testing.T) {
		q := ParseSearchQuery(`inurl:docs intitle:"getting started" -intitle:deprecated inbody:websocket handshake`)
		if !reflect.DeepEqual(q.InURL, []string{"docs"}) ||
			!reflect.DeepEqual(q.InTitle, []string{"getting started"}) ||
			!reflect.DeepEqual(q.ExcludedInTitle, []string{"deprecated"}) ||
			!reflect.DeepEqual(q.InText, []string{"websocket"}) ||
			q.Text != "handshake" {
			t.Fatalf("unexpected parse: %+v", q)
		}
	})

	// upstream: test/web/search/query.test.ts: "routes every following term for allintitle:"
	t.Run("routes every following term for allintitle", func(t *testing.T) {
		q := ParseSearchQuery("allintitle: budget planning tips")
		if !reflect.DeepEqual(q.InTitle, []string{"budget", "planning", "tips"}) || q.Text != "" {
			t.Fatalf("unexpected parse: %+v", q)
		}
	})

	// upstream: test/web/search/query.test.ts: "parses filetype/ext with normalization"
	t.Run("parses filetype/ext with normalization", func(t *testing.T) {
		q := ParseSearchQuery("quarterly report filetype:PDF ext:.xlsx -filetype:doc")
		if !reflect.DeepEqual(q.Filetypes, []string{"pdf", "xlsx"}) || !reflect.DeepEqual(q.ExcludedFiletypes, []string{"doc"}) {
			t.Fatalf("unexpected filetypes: %+v", q)
		}
	})

	// upstream: test/web/search/query.test.ts: "parses date bounds in common forms"
	t.Run("parses date bounds in common forms", func(t *testing.T) {
		q := ParseSearchQuery("llm evals after:2024 before:2025-06-15")
		if q.After != "2024-01-01" || q.Before != "2025-06-15" || q.Text != "llm evals" {
			t.Fatalf("unexpected dates: %+v", q)
		}
		if got := ParseSearchQuery("x since:2023/07/01").After; got != "2023-07-01" {
			t.Fatalf("since = %q", got)
		}
		if got := ParseSearchQuery("x until:2024-02").Before; got != "2024-02-01" {
			t.Fatalf("until = %q", got)
		}
		if got := ParseSearchQuery("x after:6/15/2024").After; got != "2024-06-15" {
			t.Fatalf("mdy = %q", got)
		}
		if got := ParseSearchQuery("x after:15/6/2024").After; got != "2024-06-15" {
			t.Fatalf("dmy = %q", got)
		}
	})

	// upstream: test/web/search/query.test.ts: "degrades unparseable date values to plain terms"
	t.Run("degrades unparseable date values to plain terms", func(t *testing.T) {
		q := ParseSearchQuery("release before:soon")
		if q.Before != "" {
			t.Fatalf("expected empty Before, got %q", q.Before)
		}
		if got := termTexts(q.Terms); !reflect.DeepEqual(got, []string{"release", "before:soon"}) {
			t.Fatalf("terms = %v", got)
		}
	})

	// upstream: test/web/search/query.test.ts: "parses quoted phrases including smart quotes and negated phrases"
	t.Run("parses quoted phrases including smart quotes and negated phrases", func(t *testing.T) {
		q := ParseSearchQuery("\"exact phrase\" \u201csmart quoted\u201d -\"not this\"")
		want := []QueryTerm{
			{Text: "exact phrase", Phrase: true},
			{Text: "smart quoted", Phrase: true},
			{Text: "not this", Phrase: true, Negated: true},
		}
		if !reflect.DeepEqual(q.Terms, want) {
			t.Fatalf("Terms = %+v, want %+v", q.Terms, want)
		}
		if q.Text != `"exact phrase" "smart quoted" -"not this"` {
			t.Fatalf("Text = %q", q.Text)
		}
	})

	// upstream: test/web/search/query.test.ts: "groups OR alternatives and treats AND as default conjunction"
	t.Run("groups OR alternatives and treats AND as default conjunction", func(t *testing.T) {
		q := ParseSearchQuery("(react OR vue OR svelte) AND hooks")
		if !reflect.DeepEqual(termTexts(q.Terms), []string{"react", "vue", "svelte", "hooks"}) {
			t.Fatalf("terms = %v", termTexts(q.Terms))
		}
		if q.Terms[0].Group == 0 || q.Terms[0].Group != q.Terms[1].Group || q.Terms[1].Group != q.Terms[2].Group || q.Terms[3].Group != 0 {
			t.Fatalf("unexpected groups: %+v", q.Terms)
		}
		if q.Text != "(react OR vue OR svelte) hooks" {
			t.Fatalf("Text = %q", q.Text)
		}
	})

	// upstream: test/web/search/query.test.ts: "supports pipe as OR and NOT as negation"
	t.Run("supports pipe as OR and NOT as negation", func(t *testing.T) {
		q := ParseSearchQuery("deno | bun NOT node")
		if q.Terms[0].Group == 0 || q.Terms[0].Group != q.Terms[1].Group {
			t.Fatalf("expected grouped OR: %+v", q.Terms)
		}
		if q.Terms[2].Text != "node" || !q.Terms[2].Negated {
			t.Fatalf("expected negated node: %+v", q.Terms[2])
		}
	})

	// upstream: test/web/search/query.test.ts: "swallows OR between directives instead of grouping terms"
	t.Run("swallows OR between directives instead of grouping terms", func(t *testing.T) {
		q := ParseSearchQuery("caching site:redis.io OR site:memcached.org")
		if !reflect.DeepEqual(q.Sites, []string{"redis.io", "memcached.org"}) {
			t.Fatalf("Sites = %v", q.Sites)
		}
		if !reflect.DeepEqual(q.Terms, []QueryTerm{{Text: "caching"}}) {
			t.Fatalf("Terms = %+v", q.Terms)
		}
	})

	// upstream: test/web/search/query.test.ts: "keeps wikipedia-style parens inside directive values"
	t.Run("keeps wikipedia-style parens inside directive values", func(t *testing.T) {
		q := ParseSearchQuery("site:en.wikipedia.org/wiki/Rust_(programming_language) borrow checker")
		if !reflect.DeepEqual(q.Sites, []string{"en.wikipedia.org/wiki/rust_(programming_language)"}) || q.Text != "borrow checker" {
			t.Fatalf("unexpected parse: %+v", q)
		}
	})

	// upstream: test/web/search/query.test.ts: "treats legacy +term as an exact phrase"
	t.Run("treats legacy +term as an exact phrase", func(t *testing.T) {
		q := ParseSearchQuery("+immutable data")
		if q.Terms[0] != (QueryTerm{Text: "immutable", Phrase: true}) {
			t.Fatalf("Term[0] = %+v", q.Terms[0])
		}
	})

	// upstream: test/web/search/query.test.ts: "parses lang: into a language code"
	t.Run("parses lang: into a language code", func(t *testing.T) {
		q := ParseSearchQuery("documentation lang:EN-us")
		if q.Lang != "en-us" || q.HasConstraints {
			t.Fatalf("unexpected lang=%q hasConstraints=%v", q.Lang, q.HasConstraints)
		}
	})
}

func TestParseDateValue(t *testing.T) {
	// upstream: test/web/search/query.test.ts: "rejects invalid components"
	for _, bad := range []string{"2024-13-01", "2024-00-10", "notadate", "24-01-01"} {
		if got := ParseDateValue(bad); got != "" {
			t.Errorf("ParseDateValue(%q) = %q, want empty", bad, got)
		}
	}
}

func TestFormatQuery(t *testing.T) {
	// upstream: test/web/search/query.test.ts: "re-emits full Google syntax"
	t.Run("re-emits full Google syntax", func(t *testing.T) {
		q := ParseSearchQuery(`release notes site:github.com -site:gist.github.com filetype:md after:2024-05-01 intitle:"v2"`)
		got := FormatQuery(q, GoogleQuerySyntax)
		want := "release notes site:github.com -site:gist.github.com intitle:v2 filetype:md after:2024-05-01"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	// upstream: test/web/search/query.test.ts: "emits OR-grouped sites for multi-site queries"
	t.Run("emits OR-grouped sites for multi-site queries", func(t *testing.T) {
		q := ParseSearchQuery("cve site:nvd.nist.gov site:mitre.org")
		if got := FormatQuery(q, GoogleQuerySyntax); got != "cve (site:nvd.nist.gov OR site:mitre.org)" {
			t.Fatalf("got %q", got)
		}
	})

	// upstream: test/web/search/query.test.ts: "produces plain keywords when the engine supports no syntax"
	t.Run("produces plain keywords when engine supports no syntax", func(t *testing.T) {
		q := ParseSearchQuery(`(react OR vue) "state management" -redux site:dev.to filetype:pdf`)
		if got := FormatQuery(q, QuerySyntax{}); got != "react vue state management" {
			t.Fatalf("got %q", got)
		}
	})

	// upstream: test/web/search/query.test.ts: "falls back to constraint values for directive-only queries"
	t.Run("falls back to constraint values for directive-only queries", func(t *testing.T) {
		q := ParseSearchQuery("site:kubernetes.io filetype:yaml")
		if got := FormatQuery(q, QuerySyntax{}); got != "kubernetes.io yaml" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestFormatScraperQuery(t *testing.T) {
	// upstream: test/web/search/query.test.ts: "demotes path-carrying site: and inurl: to plain terms, keeping bare-domain site:"
	if got := FormatScraperQuery("site:github.com/can1357/oh-my-pi inurl:releases site:github.com 17.1.1 release", nil, GoogleQuerySyntax); got != "17.1.1 release github.com/can1357/oh-my-pi releases site:github.com" {
		t.Fatalf("got %q", got)
	}
	// upstream: test/web/search/query.test.ts: "demotes every site in an OR-groupable multi-site query when all carry paths"
	if got := FormatScraperQuery("cve site:nvd.nist.gov/vuln site:mitre.org/cgi-bin", nil, GoogleQuerySyntax); got != "cve nvd.nist.gov/vuln mitre.org/cgi-bin" {
		t.Fatalf("got %q", got)
	}
	// upstream: test/web/search/query.test.ts: "passes negated site:/inurl: through as operators"
	if got := FormatScraperQuery("foo -site:github.com/x -inurl:bar", nil, GoogleQuerySyntax); got != "foo -site:github.com/x -inurl:bar" {
		t.Fatalf("got %q", got)
	}
	// upstream: test/web/search/query.test.ts: "passes directive-free queries through byte-identical"
	if got := FormatScraperQuery("github.com/can1357/oh-my-pi 17.1.1 release", nil, GoogleQuerySyntax); got != "github.com/can1357/oh-my-pi 17.1.1 release" {
		t.Fatalf("got %q", got)
	}
	// upstream: test/web/search/query.test.ts: "respects a narrower engine syntax while still demoting hostile operators"
	if got := FormatScraperQuery("a site:x.com site:y.com/z inurl:w", nil, QuerySyntax{Phrases: true, Negation: true, Site: true}); got != "a y.com/z w site:x.com" {
		t.Fatalf("got %q", got)
	}
}

func TestMatchesSite(t *testing.T) {
	// upstream: test/web/search/query.test.ts: "matches exact hosts, subdomains, and path prefixes"
	cases := []struct {
		url  string
		site string
		want bool
	}{
		{"https://docs.k8s.io/setup", "k8s.io", true},
		{"https://k8s.io/", "k8s.io", true},
		{"https://notk8s.io/", "k8s.io", false},
		{"https://github.com/anthropics/sdk", "github.com/anthropics", true},
		{"https://github.com/other/sdk", "github.com/anthropics", false},
		{"not a url", "k8s.io", false},
	}
	for _, tc := range cases {
		if got := MatchesSite(tc.url, tc.site); got != tc.want {
			t.Errorf("MatchesSite(%q, %q) = %v, want %v", tc.url, tc.site, got, tc.want)
		}
	}
}

func TestApplyQueryConstraints(t *testing.T) {
	age := 3600
	sources := []SearchSource{
		{Title: "K8s docs — Install", URL: "https://kubernetes.io/docs/setup/install.pdf"},
		{Title: "Random blog", URL: "https://blog.example.com/k8s", PublishedDate: "2023-01-15"},
		{Title: "Reddit thread", URL: "https://www.reddit.com/r/kubernetes/post"},
		{Title: "K8s blog", URL: "https://kubernetes.io/blog/2024", AgeSeconds: &age},
	}

	// upstream: test/web/search/query.test.ts: "filters by included site"
	t.Run("filters by included site", func(t *testing.T) {
		q := ParseSearchQuery("install site:kubernetes.io")
		res := ApplyQueryConstraints(sources, q)
		if len(res.Sources) != 2 || res.Sources[0].URL != "https://kubernetes.io/docs/setup/install.pdf" || res.Sources[1].URL != "https://kubernetes.io/blog/2024" {
			t.Fatalf("unexpected sources: %+v", res.Sources)
		}
		if len(res.Dropped) != 0 {
			t.Fatalf("unexpected dropped: %v", res.Dropped)
		}
	})

	// upstream: test/web/search/query.test.ts: "relaxes a non-scope directive that would eliminate every result and reports it"
	t.Run("drops a constraint that would eliminate every result", func(t *testing.T) {
		q := ParseSearchQuery("install intitle:nonexistent")
		res := ApplyQueryConstraints(sources, q)
		if len(res.Sources) != len(sources) {
			t.Fatalf("expected all %d sources kept, got %d", len(sources), len(res.Sources))
		}
		if !reflect.DeepEqual(res.Dropped, []string{"intitle:nonexistent"}) {
			t.Fatalf("Dropped = %v", res.Dropped)
		}
	})

	// upstream: test/web/search/query.test.ts: "relaxes dimensions independently"
	t.Run("relaxes dimensions independently", func(t *testing.T) {
		q := ParseSearchQuery("install site:kubernetes.io filetype:docx")
		res := ApplyQueryConstraints(sources, q)
		if len(res.Sources) != 2 {
			t.Fatalf("expected 2 sources, got %d", len(res.Sources))
		}
		if !reflect.DeepEqual(res.Dropped, []string{"filetype:docx"}) {
			t.Fatalf("Dropped = %v", res.Dropped)
		}
	})

	t.Run("filters by date bounds keeping undated sources", func(t *testing.T) {
		origNow := nowFunc
		nowFunc = func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }
		defer func() { nowFunc = origNow }()

		q := ParseSearchQuery("k8s after:2024-01-01")
		res := ApplyQueryConstraints(sources, q)
		for _, s := range res.Sources {
			if s.URL == "https://blog.example.com/k8s" {
				t.Fatalf("2023 source should have been filtered out")
			}
		}
		if len(res.Sources) != 3 {
			t.Fatalf("expected 3 sources, got %d", len(res.Sources))
		}
	})
}

func termTexts(terms []QueryTerm) []string {
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = t.Text
	}
	return out
}
