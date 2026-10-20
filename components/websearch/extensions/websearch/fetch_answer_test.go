package websearch

import (
	"context"
	"strings"
	"testing"
)

// fetch_content mode "answer" (index.ts:2584-2646): every fetched page is replaced by the model's
// answer, the stored result keeps the fetched content, and a page that cannot be answered carries
// the reason.

const answerPageHTML = `<!doctype html><html><head><title>Answer Page</title></head><body><article><h1>Answer Page</h1><p>%s</p></article></body></html>`

func answerFixture(t *testing.T, config string) (*Runtime, *fakeSide, *fakeSideRegistry) {
	t.Helper()
	r, _ := newRuntime(t, config)
	model := scModel("test", "page-model")
	reg := &fakeSideRegistry{models: []map[string]any{model}}
	reg.completeFn = func(_ context.Context, _, request map[string]any, _ CompleteOptions) (map[string]any, error) {
		return stopText("The page says 42."), nil
	}
	return r, &fakeSide{model: model, reg: reg, session: "s-1"}, reg
}

func pageNet(t *testing.T, body string) *fakeNet {
	t.Helper()
	return useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: strings.Replace(answerPageHTML, "%s", body, 1)}
	})
}

func TestFetchContentAnswerMode(t *testing.T) {
	t.Run("one page is answered and the stored content stays the page", func(t *testing.T) {
		r, side, reg := answerFixture(t, "")
		page := strings.Repeat("The meaning of the page is forty-two. ", 20)
		pageNet(t, page)
		ctx := WithSideCallContext(bg(), side)
		out, err := mustTool(t, r, "fetch_content").Execute(ctx, map[string]any{"url": "https://93.184.216.34/page", "mode": "answer", "prompt": "What does it say?"}, nil)
		noErr(t, err)
		if out.IsError || len(out.Content) != 1 || out.Content[0].Text != "The page says 42." {
			t.Fatalf("%+v", out)
		}
		if dStr(out, "mode") != "answer" || dStr(out, "prompt") != "What does it say?" || dStr(out, "title") != "Answer Page" {
			t.Fatalf("%v", out.Details)
		}
		if total, _ := dNum(out, "totalChars"); total != float64(jsLen("The page says 42.")) {
			t.Fatalf("totalChars %v", total)
		}
		prompt := requestPrompt(reg.lastCall().request)
		if !strings.HasPrefix(prompt, "Question: What does it say?\nSource URL: https://93.184.216.34/page\n\n<untrusted_page_content>\n") || !strings.Contains(prompt, "forty-two") {
			t.Fatalf("%q", prompt)
		}
		stored := GetResult(dStr(out, "responseId"))
		if stored == nil || !strings.Contains(stored.URLs[0].Content, "forty-two") || strings.Contains(stored.URLs[0].Content, "The page says 42.") {
			t.Fatalf("%+v", stored)
		}
	})

	t.Run("several pages are answered and listed", func(t *testing.T) {
		r, side, reg := answerFixture(t, "")
		pageNet(t, strings.Repeat("Several pages share this body text. ", 20))
		ctx := WithSideCallContext(bg(), side)
		out, err := mustTool(t, r, "fetch_content").Execute(ctx, map[string]any{"urls": []any{"https://93.184.216.34/a", "https://93.184.216.35/b"}, "mode": "answer", "prompt": "Q?", "answerModel": "test/page-model"}, nil)
		noErr(t, err)
		text := out.Content[0].Text
		want := "- Answer Page (17 chars)\n- Answer Page (17 chars)\n"
		if !strings.Contains(text, "## Fetched URLs\n\n"+want) || reg.callCount() != 2 {
			t.Fatalf("%q %d", text, reg.callCount())
		}
		if n, _ := dNum(out, "successful"); n != 2 {
			t.Fatalf("%v", out.Details)
		}
	})

	t.Run("a page that cannot be answered carries the reason", func(t *testing.T) {
		r, side, reg := answerFixture(t, "")
		pageNet(t, strings.Repeat("Body text of a page that will not be answered. ", 20))
		reg.completeFn = func(context.Context, map[string]any, map[string]any, CompleteOptions) (map[string]any, error) {
			return map[string]any{"stopReason": "error", "errorMessage": "model exploded"}, nil
		}
		ctx := WithSideCallContext(bg(), side)
		out, err := mustTool(t, r, "fetch_content").Execute(ctx, map[string]any{"url": "https://93.184.216.34/page", "mode": "answer", "prompt": "Q?"}, nil)
		noErr(t, err)
		if out.Content[0].Text != "Error: Page answer failed: model exploded" || dStr(out, "error") != "Page answer failed: model exploded" {
			t.Fatalf("%+v", out)
		}
	})

	t.Run("the answer options are validated before anything is fetched", func(t *testing.T) {
		r, side, _ := answerFixture(t, "")
		n := useNet(t, func(netCall) netReply { return reply(200, "x") })
		ctx := WithSideCallContext(bg(), side)
		for _, c := range []struct {
			params map[string]any
			want   string
		}{
			{map[string]any{"url": "https://93.184.216.34/page", "mode": "answer"}, "Error: mode answer requires prompt."},
			{map[string]any{"url": "https://93.184.216.34/page", "mode": "answer", "prompt": "Q?", "model": "gemini"}, "Error: use answerModel, not model, with mode answer."},
			{map[string]any{"url": "https://93.184.216.34/page", "mode": "readable", "answerModel": "test/page-model"}, "Error: answerModel requires mode answer."},
		} {
			out, err := mustTool(t, r, "fetch_content").Execute(ctx, c.params, nil)
			noErr(t, err)
			if out.Content[0].Text != c.want {
				t.Fatalf("%q, want %q", out.Content[0].Text, c.want)
			}
		}
		wantCalls(t, n)
	})
}

func TestAnswerPagesLeavesUnanswerablePagesAlone(t *testing.T) {
	_, side, reg := answerFixture(t, "")
	fetched := []ExtractedContent{
		{URL: "https://a.test/err", Error: new("fetch failed")},
		{URL: "https://a.test/thumb", Content: "text", Thumbnail: &Image{Data: "x", MimeType: "image/png"}},
		{URL: "https://a.test/img", Content: "text", MimeType: "image/png"},
		{URL: "https://a.test/ok", Title: "T", Content: "text"},
	}
	presented := answerPages(WithSideCallContext(bg(), side), fetched, "Q?", "")
	if *presented[0].Error != "fetch failed" || presented[0].Content != "" {
		t.Fatalf("%+v", presented[0])
	}
	for _, i := range []int{1, 2} {
		if presented[i].Error == nil || *presented[i].Error != "Page answer requires textual fetched content" || presented[i].Content != "text" {
			t.Fatalf("%d: %+v", i, presented[i])
		}
	}
	if presented[3].Error != nil || presented[3].Content != "The page says 42." || presented[3].Title != "T" {
		t.Fatalf("%+v", presented[3])
	}
	if fetched[3].Content != "text" || reg.callCount() != 1 {
		t.Fatalf("the fetched slice was changed or %d pages were answered", reg.callCount())
	}

	// Without a session context there is no model to ask.
	none := answerPages(bg(), fetched[3:], "Q?", "")
	if none[0].Error == nil || !strings.HasPrefix(*none[0].Error, "Page answer failed: ") {
		t.Fatalf("%+v", none[0])
	}
}
