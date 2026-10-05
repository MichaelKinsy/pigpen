package pi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/completions.test.ts: inline completions for a message being composed.

const completionsChat = "ahp-chat:/completions"

func TestMentionParsing(t *testing.T) {
	twin.Run(t, "completions", "finds a mention the cursor sits in", func(t *testing.T) {
		got, ok := pi.FindMention("look at @foo", 12)
		if !ok || got != (pi.Mention{Start: 8, End: 12, Query: "foo"}) {
			t.Fatalf("got %+v %v", got, ok)
		}
	})
	twin.Run(t, "completions", "finds a bare mention at the start of the text", func(t *testing.T) {
		if got, ok := pi.FindMention("@src", 4); !ok || got.Query != "src" {
			t.Fatalf("got %+v %v", got, ok)
		}
	})
	twin.Run(t, "completions", "ignores an `@` that is not preceded by whitespace", func(t *testing.T) {
		// Otherwise every email address and decorator opens the picker.
		if _, ok := pi.FindMention("mail me at a@b.com", 18); ok {
			t.Fatal("an email address opened the picker")
		}
	})
	twin.Run(t, "completions", "ignores a cursor that is past the end of the mention", func(t *testing.T) {
		if _, ok := pi.FindMention("@foo bar", 8); ok {
			t.Fatal("the cursor is past the mention")
		}
	})
	twin.Run(t, "completions", "returns the empty query for a lone trigger", func(t *testing.T) {
		if got, ok := pi.FindMention("@", 1); !ok || got.Query != "" {
			t.Fatalf("got %+v %v", got, ok)
		}
	})
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func complete(t *testing.T, s *pi.CompletionService, text string, offset int) ahptypes.CompletionsResult {
	t.Helper()
	r, err := s.Complete(context.Background(), ahptypes.CompletionsParams{Kind: ahptypes.CompletionItemKindUserMessage, Channel: completionsChat, Text: text, Offset: int64(offset)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func inserted(r ahptypes.CompletionsResult) []string {
	out := []string{}
	for _, i := range r.Items {
		out = append(out, i.InsertText)
	}
	return out
}

func TestCompletions(t *testing.T) {
	workspace := t.TempDir()
	for _, f := range []string{"readme.md", "report.txt", "other.js", ".hidden", "src/index.ts", "src/lib/deep.ts", "node_modules/reactive.js", "dist/generated.js", "build/artifact.js"} {
		touch(t, filepath.Join(workspace, f))
	}
	service := pi.NewCompletionService(pi.CompletionServiceOptions{WorkingDirectoryFor: func(string) string { return workspace }})
	at := func(text string) ahptypes.CompletionsResult { return complete(t, service, text, len([]rune(text))) }

	twin.Run(t, "completions", "offers files matching the typed prefix", func(t *testing.T) {
		got := inserted(at("look at @re"))
		sort.Strings(got)
		sameJSON(t, got, []string{"@readme.md", "@report.txt"}, "insert texts")
	})

	twin.Run(t, "completions", "attaches the resource each item refers to", func(t *testing.T) {
		r := at("@readme")
		if len(r.Items) == 0 {
			t.Fatal("no items")
		}
		att, ok := r.Items[0].Attachment.Value.(*ahptypes.MessageResourceAttachment)
		if !ok || att.Type != ahptypes.MessageAttachmentKindResource || att.Label != "readme.md" || !regexp.MustCompile(`readme\.md$`).MatchString(att.Uri) {
			t.Fatalf("attachment = %+v", r.Items[0].Attachment.Value)
		}
		testkit.AssertValid(t, "commands", "CompletionItem", r.Items[0])
	})

	twin.Run(t, "completions", "marks the range the client should replace", func(t *testing.T) {
		r := at("look at @re")
		// The span covers the `@` through the cursor, so accepting replaces the half-typed mention
		// rather than appending to it.
		if *r.Items[0].RangeStart != 8 || *r.Items[0].RangeEnd != 11 {
			t.Fatalf("range = %d..%d", *r.Items[0].RangeStart, *r.Items[0].RangeEnd)
		}
	})

	twin.Run(t, "completions", "returns nested files instead of directory navigation items", func(t *testing.T) {
		r := at("@sr")
		sameJSON(t, inserted(r), []string{"@src/index.ts", "@src/lib/deep.ts"}, "insert texts")
		for _, i := range r.Items {
			if k := i.Attachment.Value.(*ahptypes.MessageResourceAttachment).DisplayKind; k == nil || *k != "document" {
				t.Fatalf("displayKind = %v", k)
			}
		}
	})

	twin.Run(t, "completions", "matches a typed relative-path prefix", func(t *testing.T) {
		sameJSON(t, inserted(at("@src/in")), []string{"@src/index.ts"}, "insert texts")
	})

	twin.Run(t, "completions", "matches a nested file by basename", func(t *testing.T) {
		sameJSON(t, inserted(at("@deep")), []string{"@src/lib/deep.ts"}, "insert texts")
	})

	twin.Run(t, "completions", "keeps the temporary directory skip list narrow", func(t *testing.T) {
		got := inserted(at("@"))
		has := func(s string) bool {
			for _, g := range got {
				if g == s {
					return true
				}
			}
			return false
		}
		for _, g := range got {
			if regexp.MustCompile(`node_modules|\.hidden`).MatchString(g) {
				t.Fatalf("offered %s", g)
			}
		}
		if !has("@dist/generated.js") || !has("@build/artifact.js") {
			t.Fatalf("build output must be offered: %v", got)
		}
	})

	twin.Run(t, "completions", "does not interpret a query as path traversal", func(t *testing.T) {
		if got := at("@../../etc/pass").Items; len(got) != 0 {
			t.Fatalf("items = %v", got)
		}
	})

	twin.Run(t, "completions", "returns nothing for a completion kind it does not implement", func(t *testing.T) {
		r, err := service.Complete(context.Background(), ahptypes.CompletionsParams{Kind: "futureKind", Channel: completionsChat, Text: "@not", Offset: 4})
		if err != nil || len(r.Items) != 0 {
			t.Fatalf("items = %v (%v)", r.Items, err)
		}
	})

	twin.Run(t, "completions", "returns nothing when the cursor is not in a mention", func(t *testing.T) {
		if got := at("plain text").Items; len(got) != 0 {
			t.Fatalf("items = %v", got)
		}
	})

	twin.Run(t, "completions", "returns nothing for a chat it has no directory for", func(t *testing.T) {
		orphan := pi.NewCompletionService(pi.CompletionServiceOptions{WorkingDirectoryFor: func(string) string { return "" }})
		if got := complete(t, orphan, "@re", 3).Items; len(got) != 0 {
			t.Fatalf("items = %v", got)
		}
	})

	twin.Run(t, "completions", "caps how many items it returns", func(t *testing.T) {
		crowded := t.TempDir()
		for i := 0; i < 40; i++ {
			touch(t, filepath.Join(crowded, fmt.Sprintf("file-%d.txt", i)))
		}
		capped := pi.NewCompletionService(pi.CompletionServiceOptions{WorkingDirectoryFor: func(string) string { return crowded }, MaxItems: 5})
		if got := len(complete(t, capped, "@file", 5).Items); got != 5 {
			t.Fatalf("%d items", got)
		}
	})
}

func TestCompletionsOverTheWire(t *testing.T) {
	workspace := t.TempDir()
	touch(t, filepath.Join(workspace, "notes.md"))
	h := testkit.NewHost(host.Options{CompletionTriggerCharacters: []string{pi.MentionTrigger}})
	h.Serve(host.Capabilities{Completions: pi.NewCompletionService(pi.CompletionServiceOptions{
		WorkingDirectoryFor: func(channel string) string {
			if channel == completionsChat {
				return workspace
			}
			return ""
		},
	})})
	client := testkit.Connect(t, h)
	t.Cleanup(client.Close)
	initialization := client.Initialize("completions-client", nil)
	params := func(channel string) obj {
		p := obj{"kind": "userMessage", "text": "@not", "offset": 4}
		if channel != "" {
			p["channel"] = channel
		}
		return p
	}

	twin.Run(t, "completions", "advertises only the trigger it can answer", func(t *testing.T) {
		// "/" is deliberately absent: Pi's slash commands attach nothing, and every completion
		// item must carry an attachment.
		sameJSON(t, initialization.CompletionTriggerCharacters, []string{"@"}, "triggers")
		testkit.AssertValid(t, "commands", "InitializeResult", initialization)
	})

	twin.Run(t, "completions", "serves a schema-conforming result", func(t *testing.T) {
		p := params(completionsChat)
		p["text"], p["offset"] = "see @not", 8
		raw := client.Must("completions", p)
		var result struct{ Items []obj }
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 1 || result.Items[0]["insertText"] != "@notes.md" {
			t.Fatalf("result = %s", raw)
		}
		testkit.AssertValid(t, "commands", "CompletionsResult", testkit.Normalize(t, raw))
	})

	twin.Run(t, "completions", "rejects non-chat completion targets", func(t *testing.T) {
		for _, channel := range []string{"", "ahp-session:/completions"} {
			client.ExpectError("completions", params(channel), wire.CodeInvalidParams)
		}
	})

	twin.Run(t, "completions", "maps provider-alias completion targets onto the default chat", func(t *testing.T) {
		vscode := testkit.Connect(t, h)
		t.Cleanup(vscode.Close)
		vscode.Initialize("vscode-completions-client", obj{"clientInfo": obj{"name": "vscode-editor-window"}})
		for _, c := range []*testkit.Client{vscode, client} {
			var result struct{ Items []obj }
			c.Decode(c.Must("completions", params("pi:/completions")), &result)
			if len(result.Items) != 1 || result.Items[0]["insertText"] != "@notes.md" {
				t.Fatalf("result = %v", result.Items)
			}
		}
	})
}

func TestCompletionsWithoutAHandler(t *testing.T) {
	twin.Run(t, "completions", "answers with an empty list rather than MethodNotFound", func(t *testing.T) {
		// The client debounces keystrokes into this call; an error per keypress would be worse
		// than nothing to show.
		h := testkit.NewHost(host.Options{})
		client := testkit.Connect(t, h)
		t.Cleanup(client.Close)
		client.Initialize("bare-client", nil)
		var result struct{ Items []obj }
		client.Decode(client.Must("completions", obj{"kind": "userMessage", "channel": completionsChat, "text": "@x", "offset": 2}), &result)
		if len(result.Items) != 0 {
			t.Fatalf("items = %v", result.Items)
		}
	})
}
