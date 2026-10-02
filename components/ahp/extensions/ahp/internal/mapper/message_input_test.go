package mapper_test

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

const onePixelJPEG = "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8QEBD/wAALCAABAAEBAREA/8QAFAABAAAAAAAAAAAAAAAAAAAACf/EABQQAQAAAAAAAAAAAAAAAAAAAAD/2gAIAQEAAD8AVN//2Q=="

func message(attachments []any, text ...string) obj {
	t := "question"
	if len(text) > 0 {
		t = text[0]
	}
	return obj{"text": t, "origin": obj{"kind": "user"}, "attachments": attachments}
}

func embedded(data any, extra obj) obj {
	a := obj{"type": "embeddedResource", "label": "notes.txt", "contentType": "text/plain", "data": data}
	for k, v := range extra {
		if v == nil {
			delete(a, k)
		} else {
			a[k] = v
		}
	}
	return a
}

func embeddedImage(data string, contentType string) obj {
	return embedded(data, obj{"label": "screenshot.png", "contentType": contentType, "displayKind": "image"})
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func rng(sl, sc, el, ec int) obj {
	return obj{"range": obj{"start": obj{"line": sl, "character": sc}, "end": obj{"line": el, "character": ec}}}
}

func mustText(t *testing.T, m obj) string {
	t.Helper()
	got, err := mapper.MessageTextForPi(m)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Twins of upstream test/message-input.test.ts.
func TestMessageInput(t *testing.T) {

	twin.Run(t, "message-input", "appends a file resource as a path with its selection", func(t *testing.T) {
		in := message([]any{obj{"type": "resource", "label": "outside.ts", "uri": "file:///outside.ts", "selection": rng(4, 2, 8, 0)}})
		if r := mapper.MessageRejectionReason(in); r != "" {
			t.Fatal(r)
		}
		path, _ := wire.FileURIToPath("file:///outside.ts")
		if got := mustText(t, in); got != "question\n\n"+path+":5:3-9:1" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "message-input", "appends attachment representations in order", func(t *testing.T) {
		in := message([]any{
			obj{"type": "simple", "label": "first", "modelRepresentation": "first context"},
			obj{"type": "resource", "label": "path", "uri": "file:///path.ts"},
			obj{"type": "simple", "label": "second", "modelRepresentation": "second context"},
		})
		path, _ := wire.FileURIToPath("file:///path.ts")
		if got := mustText(t, in); got != "question\n\nfirst context\n\n"+path+"\n\nsecond context" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "message-input", "preserves resource URIs whose schemes may carry their own routing", func(t *testing.T) {
		for _, uri := range []string{"https://example.com/context.txt", "virtual://client/context.txt?revision=1"} {
			in := message([]any{obj{"type": "resource", "label": "remote", "uri": uri}})
			if got := mustText(t, in); got != "question\n\n"+uri {
				t.Fatalf("%q", got)
			}
		}
	})

	twin.Run(t, "message-input", "preserves a VS Code-wrapped resource without guessing which host owns it", func(t *testing.T) {
		wrapped := "vscode-agent-host://another-host/Users/user/project/some%20file.ts?_ah%3DeyJzY2hlbWUiOiJmaWxlIn0"
		in := message([]any{obj{"type": "resource", "label": "some file.ts", "uri": wrapped}})
		if got := mustText(t, in); got != "question\n\n"+wrapped {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "message-input", "requires client-created simple attachments to carry a model representation", func(t *testing.T) {
		// JSON null and an absent key are both "not a string".
		for _, a := range []obj{
			{"type": "simple", "label": "missing", "modelRepresentation": nil},
			{"type": "simple", "label": "missing"},
		} {
			in := message([]any{a})
			if got := mapper.MessageRejectionReason(in); got != "A simple attachment requires modelRepresentation" {
				t.Fatalf("reason = %q", got)
			}
			if _, err := mapper.MessageTextForPi(in); err == nil || !strings.Contains(err.Error(), "requires modelRepresentation") {
				t.Fatalf("err = %v", err)
			}
		}
	})

	twin.Run(t, "message-input", "separates embedded images from the text prompt", func(t *testing.T) {
		in := message([]any{
			obj{"type": "simple", "label": "context", "modelRepresentation": "selected context"},
			embeddedImage(onePixelJPEG, "IMAGE/JPG; name=screenshot.jpg"),
		})
		got, err := mapper.MessageInputForPi(in)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, got, mapper.MessageInput{
			Text:   "question\n\nselected context",
			Images: []mapper.Image{{Type: "image", Data: onePixelJPEG, MimeType: "image/jpeg"}},
		}, "input")
	})

	twin.Run(t, "message-input", "rejects an empty embedded image", func(t *testing.T) {
		if got := mapper.MessageRejectionReason(message([]any{embeddedImage("", "image/png")})); got != "Embedded image screenshot.png is empty" {
			t.Fatal(got)
		}
	})

	twin.Run(t, "message-input", "appends UTF-8 embedded text without interpreting its MIME type", func(t *testing.T) {
		in := message([]any{embedded(b64("const answer = 42;"), obj{"contentType": "application/x-uncommon-text"})})
		if got := mustText(t, in); got != "question\n\nconst answer = 42;" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "message-input", "keeps embedded payload under its original 1-based selection marker", func(t *testing.T) {
		in := message([]any{embedded(b64("selected text"), obj{"selection": rng(20, 3, 20, 16)})})
		if got := mustText(t, in); got != "question\n\n[selection 21:4-21:17]\nselected text" {
			t.Fatalf("%q", got)
		}
	})

	twin.Run(t, "message-input", "rejects malformed or non-text embedded payloads", func(t *testing.T) {
		cases := []struct {
			a      obj
			reason string
		}{
			{embedded("%%%", nil), "valid base64"},
			{embedded(nil, nil), "valid base64"},
			{embedded(base64.StdEncoding.EncodeToString([]byte{0xc3, 0x28}), nil), "valid UTF-8"},
		}
		for _, c := range cases {
			if got := mapper.MessageRejectionReason(message([]any{c.a})); !strings.Contains(got, c.reason) {
				t.Fatalf("reason %q lacks %q", got, c.reason)
			}
		}
	})

	twin.Run(t, "message-input", "rejects malformed message and selection shapes without throwing", func(t *testing.T) {
		cases := []struct {
			in     any
			reason string
		}{
			{nil, `requires text and an origin`},
			{obj{"text": 42, "origin": obj{"kind": "user"}}, `requires text and an origin`},
			{obj{"text": "x", "origin": obj{"kind": "user"}, "attachments": obj{}}, `must be an array`},
			{message([]any{nil}), `requires a type and label`},
			{message([]any{obj{"type": "resource", "label": "bad range", "uri": "file:///bad",
				"selection": obj{"range": obj{"start": obj{"line": -1, "character": 0}, "end": obj{"line": 0, "character": 0}}}}}), `invalid text selection`},
			{message([]any{embedded("", obj{"contentType": nil})}), `requires a content type`},
		}
		for i, c := range cases {
			got := mapper.MessageRejectionReason(c.in)
			if !regexp.MustCompile(c.reason).MatchString(got) {
				t.Fatalf("case %d: reason %q does not match %q", i, got, c.reason)
			}
		}
	})

	twin.Run(t, "message-input", "rejects annotations and chat attachments", func(t *testing.T) {
		for _, a := range []obj{
			{"type": "annotations", "label": "diagnostics", "resource": "ahp-annotations:/fixture"},
			{"type": "chat", "label": "other chat", "resource": "ahp-chat:/fixture"},
		} {
			if got := mapper.MessageRejectionReason(message([]any{a})); !strings.Contains(got, "does not support") {
				t.Fatalf("reason = %q", got)
			}
		}
	})
}
