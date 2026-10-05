package websearch

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/content-find.test.mjs (upstream 9a734ed). String slicing in the expectations
// uses jsSlice: the original indexes UTF-16 code units.

func mustMatch(t *testing.T, re, s string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Fatalf("%q does not match %q", s, re)
	}
}

func queryCount(r FindResult, q string) int {
	for _, x := range r.QueryResults {
		if x.Query == q {
			return x.MatchCount
		}
	}
	return -1
}

func sections(text string) []string {
	var out []string
	re := regexp.MustCompile(`^\d+\. `)
	for _, s := range strings.Split(text, "\n\n") {
		if re.MatchString(s) {
			out = append(out, s)
		}
	}
	return out
}

func snippetsOf(secs []string) string {
	var b strings.Builder
	for _, s := range secs {
		lines := strings.Split(s, "\n")
		b.WriteString(strings.Join(lines[1:], "\n"))
	}
	return b.String()
}

func TestUpstream_content_find(t *testing.T) {
	const f = "content-find"

	tw(t, f, "findContent supports exact, case-insensitive, and fuzzy matches", func(t *testing.T) {
		text := "Alpha configuration guide.\n\nThe server configuraton value is 42."
		if findContent(text, []string{"configuration"}, FindExact).MatchCount != 1 ||
			findContent(text, []string{"ALPHA"}, FindCaseInsensitive).MatchCount != 1 ||
			findContent(text, []string{"configuration value"}, FindFuzzy).MatchCount != 1 {
			t.Fatal("match counts")
		}
	})

	tw(t, f, "findContent returns excerpts for densely overlapping ${mode} matches", func(t *testing.T) {
		for _, mode := range []FindMode{FindExact, FindCaseInsensitive, FindFuzzy} {
			mode := mode
			t.Run(string(mode), func(t *testing.T) {
				text := strings.Repeat("common context for this occurrence.\n\n", 4000)
				r := findContent(text, []string{"common"}, mode)
				if r.MatchCount != 4000 || r.ReturnedMatches <= 0 || r.ReturnedMatches >= r.MatchCount {
					t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
				}
				mustMatch(t, `common context`, r.Text)
				mustMatch(t, fmt.Sprintf(`Showing %d of 4000 matches\.`, r.ReturnedMatches), r.Text)
				if jsLen(r.Text) > 20000 {
					t.Fatal(jsLen(r.Text))
				}
			})
		}
	})

	tw(t, f, "findContent returns deterministic bounded dense and sparse results", func(t *testing.T) {
		for _, c := range []struct {
			text  string
			count int
		}{
			{strings.Repeat("common context.\n\n", 8192), 8192},
			{strings.Repeat("common"+strings.Repeat(" ", 900), 1024), 1024},
		} {
			r := findContent(c.text, []string{"common"}, FindExact)
			if r.MatchCount != c.count || r.ReturnedMatches <= 0 || r.ReturnedMatches >= c.count {
				t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
			}
			mustMatch(t, fmt.Sprintf(`Showing %d of %d matches\.`, r.ReturnedMatches, c.count), r.Text)
			if jsLen(r.Text) > 20000 || !reflect.DeepEqual(findContent(c.text, []string{"common"}, FindExact), r) {
				t.Fatal("bound or determinism")
			}
		}
	})

	tw(t, f, "findContent includes rare-query context with ${sparse ? \"sparse\" : \"dense\"} common matches", func(t *testing.T) {
		for _, c := range []struct {
			sparse  bool
			queries []string
		}{{false, []string{"common", "RareTarget"}}, {true, []string{"RareTarget", "common"}}} {
			c := c
			kind := "dense"
			if c.sparse {
				kind = "sparse"
			}
			t.Run(kind, func(t *testing.T) {
				n, pad, want := 4000, 20, 4001
				if c.sparse {
					n, pad, want = 80, 1000, 81
				}
				text := strings.Repeat("common "+strings.Repeat("x", pad)+"\n", n) + strings.Repeat("x", 2000) + "RareTarget has important context."
				r := findContent(text, c.queries, FindExact)
				if r.MatchCount != want || r.ReturnedMatches <= 1 || queryCount(r, "RareTarget") != 1 || jsLen(r.Text) > 20000 {
					t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
				}
				mustMatch(t, `common`, r.Text)
				mustMatch(t, `RareTarget has important context\.`, r.Text)
			})
		}
	})

	tw(t, f, "findContent preserves document order and formatting when all excerpts fit", func(t *testing.T) {
		text := "common first." + strings.Repeat("x", 1000) + "common second." + strings.Repeat("x", 1000) + "RareTarget last."
		r := findContent(text, []string{"common", "RareTarget"}, FindExact)
		want := strings.Join([]string{
			"Text matches (exact)",
			"1. \"common\" ×1\n" + jsSlice(text, 0, 406) + "…",
			"2. \"common\" ×1\n…" + jsSlice(text, 613, 1419) + "…",
			"3. \"RareTarget\" ×1\n…" + jsSlice(text, 1627, -1),
		}, "\n\n")
		if r.ReturnedMatches != 3 || r.Text != want {
			t.Fatalf("%d\n%s\n---\n%s", r.ReturnedMatches, r.Text, want)
		}
	})

	tw(t, f, "findContent measures formatted output rather than raw whitespace", func(t *testing.T) {
		text := strings.Repeat("common"+strings.Repeat(" ", 500), 99) + "common"
		r := findContent(text, []string{"common"}, FindExact)
		collapsed := strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(text, " "))
		if r.ReturnedMatches != 100 || r.Text != "Text matches (exact)\n\n1. \"common\" ×100\n"+collapsed {
			t.Fatalf("%d", r.ReturnedMatches)
		}
	})

	tw(t, f, "findContent preserves missing-query and truncation notices under overflow", func(t *testing.T) {
		text := strings.Repeat("common context.\n\n", 4000)
		missing := strings.Repeat("z", 500)
		r := findContent(text, []string{" common ", "common", missing}, FindExact)
		if len(r.QueryResults) != 2 || r.MatchCount != 4000 || r.ReturnedMatches <= 0 || r.ReturnedMatches > r.MatchCount {
			t.Fatalf("%+v", r.QueryResults)
		}
		if !strings.Contains(r.Text, `No matches: "`+missing+`"`) || jsLen(r.Text) > 20000 {
			t.Fatal("notices")
		}
		mustMatch(t, `Showing \d+ of 4000 matches\.`, r.Text)
	})

	tw(t, f, "findContent includes a near-limit missing-query notice", func(t *testing.T) {
		missing := strings.Repeat("z", 500)
		r := findContent(strings.Repeat("q"+strings.Repeat("x", 399), 49), []string{"q", missing}, FindExact)
		if r.MatchCount != 49 || r.ReturnedMatches != 49 || jsLen(r.Text) > 20000 {
			t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
		}
		mustMatch(t, `No matches: "`+missing+`"`, r.Text)
	})

	tw(t, f, "findContent reserves one witness per nested matching query", func(t *testing.T) {
		common := strings.Repeat("q", 499)
		queries := []string{common}
		for _, c := range "abcdefghi" {
			queries = append(queries, common+string(c))
		}
		queries[0] = common
		// upstream: [common, ..."abcdefghi"].map((v, i) => i ? common + v : v)
		var text strings.Builder
		for _, q := range queries[1:] {
			text.WriteString(q + strings.Repeat("x", 1000))
		}
		r := findContent(text.String(), queries, FindExact)
		snippets := snippetsOf(sections(r.Text))
		if r.MatchCount != 18 || r.ReturnedMatches != 18 || jsLen(r.Text) > 20000 {
			t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
		}
		for i, q := range queries {
			mustMatch(t, fmt.Sprintf(`Q%d = "%s"`, i+1, regexp.QuoteMeta(q)), r.Text)
			if !strings.Contains(snippets, q) {
				t.Fatalf("missing Q%d witness", i+1)
			}
		}
	})

	tw(t, f, "findContent reports an oversized fuzzy witness as omitted", func(t *testing.T) {
		r := findContent("a"+strings.Repeat("ʰ", 25000), []string{"a"}, FindFuzzy)
		if r.MatchCount != 1 || r.ReturnedMatches != 0 || jsLen(r.Text) > 20000 {
			t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
		}
		mustMatch(t, `No representative excerpt: Q1\.`, r.Text)
		mustMatch(t, `Showing 0 of 1 matches\.`, r.Text)
	})

	tw(t, f, "findContent chooses a fitting alternate fuzzy witness", func(t *testing.T) {
		short := "a" + strings.Repeat("ʰ", 100)
		overlapping := "a" + strings.Repeat("ʰ", 19825)
		r := findContent(short+"\n\n"+strings.Repeat("x", 1000)+"\n\n"+overlapping+" other", []string{"a other", "a"}, FindFuzzy)
		if r.MatchCount != 3 || r.ReturnedMatches != 2 || jsLen(r.Text) > 20000 || strings.Contains(r.Text, "No representative excerpt") {
			t.Fatalf("%d %d", r.MatchCount, r.ReturnedMatches)
		}
		mustMatch(t, `1\. Q1 ×1, Q2 ×1`, r.Text)
		mustMatch(t, `Showing 2 of 3 matches\.`, r.Text)
		if !strings.Contains(r.Text, overlapping) {
			t.Fatal("alternate witness missing")
		}
	})

	tw(t, f, "findContent unions overlapping witnesses and counts only contained matches", func(t *testing.T) {
		text := strings.Repeat("A"+strings.Repeat("x", 499)+"B", 100)
		queries := []string{jsSlice(text, 0, 500), jsSlice(text, 250, 750)}
		r := findContent(text, queries, FindExact)
		secs := sections(r.Text)
		if r.MatchCount != 199 || r.ReturnedMatches != 3 || len(secs) != 1 || jsLen(r.Text) > 20000 {
			t.Fatalf("%d %d %d", r.MatchCount, r.ReturnedMatches, len(secs))
		}
		mustMatch(t, `^1\. Q1 ×2, Q2 ×1\n`, secs[0])
		mustMatch(t, `Showing 3 of 199 matches\.`, r.Text)
	})

	tw(t, f, "findContent does not repeat context when bounded ranges split", func(t *testing.T) {
		query := strings.Repeat("q", 500)
		var markers []string
		var text strings.Builder
		for i := 0; i < 30; i++ {
			marker := fmt.Sprintf("overlap-marker-%02d", i)
			markers = append(markers, marker)
			text.WriteString(query + strings.Repeat("x", 50) + marker + strings.Repeat("x", 150-len(marker)))
		}
		r := findContent(text.String(), []string{query}, FindExact)
		secs := sections(r.Text)
		snippets := snippetsOf(secs)
		sectionMatches := 0
		for _, s := range secs {
			if m := regexp.MustCompile(`×(\d+)`).FindStringSubmatch(s); m != nil {
				var n int
				fmt.Sscan(m[1], &n)
				sectionMatches += n
			}
		}
		if r.MatchCount != 30 || sectionMatches != r.ReturnedMatches || strings.Count(snippets, query) != r.ReturnedMatches {
			t.Fatalf("%d %d %d", r.MatchCount, sectionMatches, r.ReturnedMatches)
		}
		any := false
		for _, m := range markers {
			if strings.Contains(snippets, m) {
				any = true
			}
			if strings.Count(snippets, m) > 1 {
				t.Fatalf("repeated source interval at %s", m)
			}
		}
		if !any {
			t.Fatal("no marker")
		}
		showing := regexp.MustCompile(`Showing \d+ of \d+ matches\.`).MatchString(r.Text)
		if r.ReturnedMatches < r.MatchCount {
			mustMatch(t, fmt.Sprintf(`Showing %d of 30 matches\.`, r.ReturnedMatches), r.Text)
		} else if showing {
			t.Fatal("must not report truncation")
		}
		if jsLen(r.Text) > 20000 {
			t.Fatal("bound")
		}
	})
}
