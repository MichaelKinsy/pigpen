package a2aext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const (
	maxPromptBytes = 256 << 10
	// maxImages is how many images one task may carry.
	maxImages = 8
	// maxLabelName caps an attachment's file name where it is shown to the model.
	maxLabelName = 100
)

// Image is an image attached to a task, as the worker's RPC `prompt` carries it.
type Image struct {
	MediaType string
	Data      []byte
}

// inputModes are the media types a task may carry; the Agent Card advertises exactly these.
var inputModes = []string{"text/plain", "application/json", "image/png", "image/jpeg", "image/gif", "image/webp"}

var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// textApplicationTypes are the application/* types that are text, besides anything with a +json or +xml suffix.
var textApplicationTypes = map[string]bool{
	"application/json": true, "application/xml": true, "application/yaml": true, "application/x-yaml": true,
	"application/toml": true, "application/javascript": true, "application/x-sh": true, "application/sql": true,
}

// promptOf turns an A2A message into what the worker is given: one prompt and its images. What the worker can
// use is passed on (text, text-like files and data inline, images as images); what it cannot is refused with
// the reason, never dropped.
func promptOf(m *a2a.Message) (string, []Image, error) {
	if m == nil {
		return "", nil, fmt.Errorf("message is required: %w", a2a.ErrInvalidParams)
	}
	var (
		chunks []string
		images []Image
	)
	for i, p := range m.Parts {
		if p == nil {
			continue
		}
		switch c := p.Content.(type) {
		case a2a.Text:
			chunks = append(chunks, string(c))
		case a2a.URL:
			return "", nil, fmt.Errorf("part %d is a URL (%s); this listener does not fetch URLs, send the content as a raw or data part: %w",
				i+1, label(string(c)), a2a.ErrUnsupportedContentType)
		case a2a.Data:
			body, err := encodeJSON(c.Value)
			if err != nil {
				return "", nil, fmt.Errorf("part %d: the data cannot be encoded as JSON: %w", i+1, a2a.ErrInvalidParams)
			}
			chunks = append(chunks, attachment(p.Filename, "application/json", "", "json", body))
		case a2a.Raw:
			chunk, image, err := rawAttachment(i+1, p, c)
			if err != nil {
				return "", nil, err
			}
			if image != nil {
				images = append(images, *image)
				if len(images) > maxImages {
					return "", nil, fmt.Errorf("a task may carry at most %d images: %w", maxImages, a2a.ErrInvalidParams)
				}
			}
			chunks = append(chunks, chunk)
		default:
			return "", nil, fmt.Errorf("part %d has no content this listener understands: %w", i+1, a2a.ErrUnsupportedContentType)
		}
	}
	prompt := strings.Join(chunks, "\n")
	if strings.TrimSpace(prompt) == "" {
		return "", nil, fmt.Errorf("the message has no text: %w", a2a.ErrInvalidParams)
	}
	if len(prompt) > maxPromptBytes {
		return "", nil, fmt.Errorf("the message exceeds %d bytes: %w", maxPromptBytes, a2a.ErrInvalidParams)
	}
	return prompt, images, nil
}

// rawAttachment classifies one raw part: an image goes to the worker as an image, text is inlined.
func rawAttachment(n int, p *a2a.Part, raw a2a.Raw) (string, *Image, error) {
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("part %d is empty: %w", n, a2a.ErrInvalidParams)
	}
	if strings.TrimSpace(p.MediaType) == "" {
		return "", nil, fmt.Errorf("part %d has no media type; a raw part needs one (text/*, application/json or an image): %w", n, a2a.ErrUnsupportedContentType)
	}
	mt, params, err := mime.ParseMediaType(p.MediaType)
	if err != nil || !strings.Contains(mt, "/") || strings.HasSuffix(mt, "/") {
		return "", nil, fmt.Errorf("part %d has a malformed media type %s: %w", n, label(p.MediaType), a2a.ErrUnsupportedContentType)
	}
	switch {
	case imageTypes[mt]:
		return attachment(p.Filename, mt, ", attached as an image", "", nil), &Image{MediaType: mt, Data: bytes.Clone(raw)}, nil
	case isTextType(mt):
		if cs := strings.ToLower(params["charset"]); cs != "" && cs != "utf-8" && cs != "utf8" && cs != "us-ascii" {
			return "", nil, fmt.Errorf("part %d: the charset %s is not supported, text must be UTF-8: %w", n, label(cs), a2a.ErrUnsupportedContentType)
		}
		if !utf8.Valid(raw) {
			return "", nil, fmt.Errorf("part %d (%s) is not UTF-8 text: %w", n, mt, a2a.ErrUnsupportedContentType)
		}
		if bytes.IndexByte(raw, 0) >= 0 {
			return "", nil, fmt.Errorf("part %d (%s) holds NUL bytes, so it is binary, not text: %w", n, mt, a2a.ErrUnsupportedContentType)
		}
		return attachment(p.Filename, mt, "", "", raw), nil, nil
	}
	return "", nil, fmt.Errorf("part %d is %s, which the worker cannot use (it takes text, JSON and png, jpeg, gif or webp images): %w", n, mt, a2a.ErrUnsupportedContentType)
}

func isTextType(mt string) bool {
	return strings.HasPrefix(mt, "text/") || textApplicationTypes[mt] ||
		(strings.HasPrefix(mt, "application/") && (strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml")))
}

// attachment renders one attachment for the prompt: a one-line label, then the content in a fence longer than
// any run of backticks in it, so the content cannot close its own fence.
func attachment(filename, mediaType, note, lang string, content []byte) string {
	var b strings.Builder
	b.WriteString("[attachment ")
	if name := displayName(filename); name != "" {
		b.WriteString(strconv.Quote(name) + " ")
	}
	b.WriteString("(" + mediaType + ")" + note + "]")
	if content == nil {
		return b.String()
	}
	fence := strings.Repeat("`", max(3, longestRun(content, '`')+1))
	b.WriteString("\n" + fence + lang + "\n")
	b.Write(content)
	b.WriteString("\n" + fence)
	return b.String()
}

func longestRun(b []byte, c byte) int {
	best, run := 0, 0
	for _, x := range b {
		if x == c {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}

// displayName makes a peer-chosen file name safe to show on one line: no control characters, no backticks,
// at most maxLabelName bytes.
func displayName(name string) string { return oneLine(name, maxLabelName) }

// oneLine is s with control characters and backticks replaced, cut at max bytes on a character boundary.
func oneLine(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len()+utf8.RuneLen(r) > max {
			break
		}
		if unicode.IsControl(r) || r == '`' || r == utf8.RuneError {
			r = '_'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// label shows a peer-supplied string inside an error message on one line.
func label(s string) string { return strconv.Quote(displayName(s)) }

func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
