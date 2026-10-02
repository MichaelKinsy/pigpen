package websearch

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Twins of test/source-check.test.mjs (pure functions) and test/fetch-params.test.mjs.
// The source_check tool cases live in tools_test.go.

func rr(url, snippet string, rank int) RankedSearchResult {
	return RankedSearchResult{SearchResult: SearchResult{Title: "Example", URL: url, Snippet: snippet}, Rank: rank}
}

func TestUpstream_source_check(t *testing.T) {
	const f = "source-check"

	tw(t, f, "source-check creates a real SHA-256 hash and exact whitespace offsets", func(t *testing.T) {
		content := "Intro.\n\nThe API\t supports streaming responses.\nTail."
		passages := BuildPassages(
			[]ResearchSource{{Rank: 1, URL: "https://docs.example.com/api", Title: "API", Snippet: ptr("API supports streaming responses."), Quality: "official_docs"}},
			[]ExtractedContent{{URL: "https://docs.example.com/api", Title: "API", Content: content}}, "")
		var page *ResearchPassage
		for i := range passages {
			if passages[i].ExtractionSpan != nil {
				page = &passages[i]
			}
		}
		if page == nil || page.Text != "The API\t supports streaming responses." {
			t.Fatalf("%+v", passages)
		}
		if jsSlice(content, page.ExtractionSpan.Start, page.ExtractionSpan.End) != page.Text {
			t.Fatal("span must slice the original content")
		}
		if HashContent("abc") != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
			t.Fatal(HashContent("abc"))
		}
	})

	tw(t, f, "fetched content supplies exact passages when the provider snippet is empty", func(t *testing.T) {
		content := "The API supports streaming responses. Other details follow."
		a := BuildResearchArtifact(BuildArtifactInput{
			Query:   "API supports streaming responses",
			Results: []RankedSearchResult{rr("https://docs.example.com/api", "", 1)},
			Fetched: []ExtractedContent{{URL: "https://docs.example.com/api", Title: "API", Content: content}},
		})
		if len(a.Passages) != 1 || a.Passages[0].Text != "The API supports streaming responses." ||
			a.Passages[0].ExtractionSpan == nil || *a.Passages[0].ExtractionSpan != (Span{0, 37}) {
			t.Fatalf("%+v", a.Passages)
		}
	})

	tw(t, f, "artifact assembly handles omitted domain filters and failed fetches", func(t *testing.T) {
		a := BuildResearchArtifact(BuildArtifactInput{
			Query:   "API claim",
			Results: []RankedSearchResult{rr("https://example.com/a", "The API is confirmed.", 1)},
			Fetched: []ExtractedContent{{URL: "https://example.com/a", Title: "Example", Content: "", Error: ptr("blocked")}},
		})
		s := a.Sources[0]
		if len(a.Filters.DomainInclude) != 0 || s.Fetched == nil || *s.Fetched || s.FetchError != "blocked" || s.ContentHash != "" || s.FetchTimestamp == nil {
			t.Fatalf("%+v %+v", a.Filters, s)
		}
	})

	tw(t, f, "claim assessment preserves passages and storage without semantic passage labels", func(t *testing.T) {
		storageEnv(t)
		a := BuildResearchArtifact(BuildArtifactInput{
			Query:   "API claim",
			Results: []RankedSearchResult{rr("https://example.com/a", "According to the API documentation, the API is confirmed.", 1)},
		})
		assessed := a
		assessed.Claims = []ClaimAssessment{AssessClaim("API documentation is confirmed", a.Passages)}
		noErr(t, StoreResearchArtifact(&assessed))
		got := GetResearchArtifact(assessed.ID)
		if assessed.ID == "" || got == nil || !reflect.DeepEqual(*got, assessed) {
			t.Fatalf("%+v", got)
		}
		c := assessed.Claims[0]
		if c.Status != "unclear" || len(c.SupportingPassages) != 0 || len(c.ContradictingPassages) != 0 {
			t.Fatalf("%+v", c)
		}
		if len(assessed.Passages) != 1 || assessed.Passages[0].PassageID != "p-1-0" {
			t.Fatalf("%+v", assessed.Passages)
		}
	})

	marker := "The extension is designed around one rule: the user owns intent; the agent executes only after the goal is explicit and confirmed."
	claim := "pi-goal-x is developed by Anthropic officially and only supports Claude models"

	tw(t, f, "claim assessment does not support the exact issue claim from an unrelated confirmed marker", func(t *testing.T) {
		a := AssessClaim(claim, []ResearchPassage{{PassageID: "p-5-1", SourceURL: "fixture://fork", SourceRank: 1, Text: marker}})
		if a.Status != "unclear" || a.Confidence != 0.3 || len(a.SupportingPassages) != 0 || len(a.ContradictingPassages) != 0 ||
			!strings.Contains(strings.ToLower(a.Rationale), "review the cited passages manually") {
			t.Fatalf("%+v", a)
		}
	})

	tw(t, f, "duplicate marker passages cannot amplify a semantic verdict", func(t *testing.T) {
		var passages []ResearchPassage
		for i := 0; i < 6; i++ {
			passages = append(passages, ResearchPassage{PassageID: "p-" + fmt.Sprint(i+1) + "-1", SourceURL: "fixture://fork-" + fmt.Sprint(i+1), SourceRank: i + 1, Text: marker})
		}
		one := AssessClaim(claim, passages[:1])
		dup := AssessClaim(claim, passages)
		if dup.Status != "unclear" || dup.Confidence != one.Confidence || len(dup.SupportingPassages) != 0 || len(dup.ContradictingPassages) != 0 {
			t.Fatalf("%+v", dup)
		}
	})

	tw(t, f, "contradiction markers do not produce semantic passage labels", func(t *testing.T) {
		a := AssessClaim("The API supports streaming responses", []ResearchPassage{{PassageID: "p-1-1", SourceURL: "fixture://denial", SourceRank: 1,
			Text: "The API supports streaming responses claim is false and incorrect."}})
		if a.Status != "unclear" || len(a.SupportingPassages) != 0 || len(a.ContradictingPassages) != 0 {
			t.Fatalf("%+v", a)
		}
	})

	tw(t, f, "claim assessment only uses missing-evidence when no passages were retrieved", func(t *testing.T) {
		m := AssessClaim("API supports streaming responses", nil)
		if m.Status != "missing-evidence" || m.Confidence != 0.2 {
			t.Fatalf("%+v", m)
		}
	})
}

func TestUpstream_fetch_params(t *testing.T) {
	const f = "fetch-params"
	norm := func(t *testing.T, params map[string]any) NormalizedFetchContentParams {
		t.Helper()
		n, err := NormalizeFetchContentParams(params)
		noErr(t, err)
		return n
	}

	tw(t, f, "fetch_content params fall back to url when urls is an empty array", func(t *testing.T) {
		n := norm(t, map[string]any{"url": "https://example.com/docs", "urls": []any{}})
		if !reflect.DeepEqual(n.URLList, []string{"https://example.com/docs"}) {
			t.Fatalf("%v", n.URLList)
		}
	})

	tw(t, f, "fetch_content params keep non-empty urls precedence over url", func(t *testing.T) {
		n := norm(t, map[string]any{"url": "https://example.com/fallback", "urls": []any{"https://example.com/primary"}})
		if !reflect.DeepEqual(n.URLList, []string{"https://example.com/primary"}) {
			t.Fatalf("%v", n.URLList)
		}
	})

	tw(t, f, "fetch_content params ignore blank optional strings and blank urls", func(t *testing.T) {
		n := norm(t, map[string]any{"url": "  https://example.com/one  ", "urls": []any{"", " https://example.com/two ", "https://example.com/one"},
			"prompt": "", "timestamp": "   ", "model": " gemini-3.6-flash "})
		if !reflect.DeepEqual(n.URLList, []string{"https://example.com/two", "https://example.com/one"}) ||
			n.Options.Prompt != "" || n.Options.Timestamp != "" || n.Options.Model != "gemini-3.6-flash" {
			t.Fatalf("%+v", n)
		}
		if norm(t, map[string]any{"model": ""}).Options.Model != "" {
			t.Fatal("blank model")
		}
	})

	tw(t, f, "fetch_content params preserve forceClone only for boolean values", func(t *testing.T) {
		a, b, c := norm(t, map[string]any{"forceClone": true}), norm(t, map[string]any{"forceClone": false}), norm(t, map[string]any{"forceClone": "true"})
		if a.Options.ForceClone == nil || !*a.Options.ForceClone || b.Options.ForceClone == nil || *b.Options.ForceClone || c.Options.ForceClone != nil {
			t.Fatal("forceClone")
		}
	})

	tw(t, f, "fetch_content params drop out-of-range, non-positive, and non-integer frames", func(t *testing.T) {
		for _, frames := range []any{13.0, 0.0, -1.0, 1.5, "1"} {
			if n := norm(t, map[string]any{"frames": frames, "timestamp": "1:23"}); n.Options.Frames != 0 {
				t.Fatalf("frames %v kept as %d", frames, n.Options.Frames)
			}
		}
	})

	tw(t, f, "fetch_content params ignore bridge-filled default frames for ordinary page fetches", func(t *testing.T) {
		n := norm(t, map[string]any{"url": "https://example.com/docs", "urls": []any{}, "frames": 1.0, "prompt": "Summarize this page", "timestamp": ""})
		if n.Options.Frames != 0 {
			t.Fatal("frames must be dropped")
		}
	})

	tw(t, f, "fetch_content params preserve explicit video frame options", func(t *testing.T) {
		if norm(t, map[string]any{"url": "https://youtu.be/demo", "frames": 1.0, "timestamp": "1:23"}).Options.Frames != 1 ||
			norm(t, map[string]any{"url": "https://youtu.be/demo", "frames": 2.0}).Options.Frames != 2 {
			t.Fatal("frames")
		}
	})

	tw(t, f, "fetch_content params validate fetch and answer modes", func(t *testing.T) {
		n := norm(t, map[string]any{"mode": "answer", "answerModel": " test/page-model "})
		want := FetchOptions{Mode: "answer", AnswerModel: "test/page-model"}
		if !reflect.DeepEqual(n.Options, want) {
			t.Fatalf("%+v", n.Options)
		}
		_, err := NormalizeFetchContentParams(map[string]any{"mode": "invalid"})
		wantErr(t, err, `mode must be`)
	})

	tw(t, f, "fetch_content params validate auth profile input", func(t *testing.T) {
		if a := norm(t, map[string]any{"auth": true}).Options.Auth; a == nil || !a.True {
			t.Fatal("auth true")
		}
		if a := norm(t, map[string]any{"auth": " work "}).Options.Auth; a == nil || a.Name != "work" {
			t.Fatal("auth name")
		}
		if norm(t, map[string]any{"auth": false}).Options.Auth != nil {
			t.Fatal("auth false")
		}
		for _, bad := range []any{" ", 1.0} {
			_, err := NormalizeFetchContentParams(map[string]any{"auth": bad})
			wantErr(t, err, `auth must be`)
		}
	})
}
