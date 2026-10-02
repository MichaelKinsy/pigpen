package mapper

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Image is a pi image-content block.
type Image struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// MessageInput is pi's prompt shape: text plus optional images.
type MessageInput struct {
	Text   string  `json:"text"`
	Images []Image `json:"images,omitempty"`
}

// The port of src/pi/message-input.ts. A message arrives as decoded JSON straight from a client,
// so every shape is checked: a malformed value is rejected with a reason, never a panic. Resource
// references become paths or URIs, simple attachments contribute their model representation,
// embedded text is decoded as UTF-8, embedded images become pi image blocks; other attachment
// kinds are unsupported.

func isRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func isNonNegativeInteger(v any) bool {
	f, ok := num(v)
	return ok && f >= 0 && f == math.Trunc(f) && f <= 1<<53-1
}

func isTextRange(v any) bool {
	rng, ok := isRecord(v)
	if !ok {
		return false
	}
	start, ok1 := isRecord(rng["start"])
	end, ok2 := isRecord(rng["end"])
	if !ok1 || !ok2 {
		return false
	}
	return isNonNegativeInteger(start["line"]) && isNonNegativeInteger(start["character"]) &&
		isNonNegativeInteger(end["line"]) && isNonNegativeInteger(end["character"])
}

var base64Shape = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

func decodeBase64(v any) ([]byte, bool) {
	data, ok := v.(string)
	if !ok || !base64Shape.MatchString(data) || len(data)%4 == 1 {
		return nil, false
	}
	if padding := strings.IndexByte(data, '='); padding != -1 && (padding < len(data)-2 || len(data)%4 != 0) {
		return nil, false
	}
	trimmed := strings.TrimRight(data, "=")
	decoded, err := base64.RawStdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, false
	}
	// Canonical form only: the bytes must re-encode to the same text.
	if base64.RawStdEncoding.EncodeToString(decoded) != trimmed {
		return nil, false
	}
	return decoded, true
}

func rangeText(rng map[string]any) string {
	start, _ := isRecord(rng["start"])
	end, _ := isRecord(rng["end"])
	return fmt.Sprintf("%d:%d-%d:%d", intOr(start["line"], 0)+1, intOr(start["character"], 0)+1, intOr(end["line"], 0)+1, intOr(end["character"], 0)+1)
}

func selectionRange(attachment map[string]any) (map[string]any, bool) {
	sel, ok := isRecord(attachment["selection"])
	if !ok {
		return nil, false
	}
	r, ok := isRecord(sel["range"])
	return r, ok
}

func baseContentType(contentType string) string {
	base, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(trimJS(base))
}

type rejection string

// embeddedContent returns text or an image, or a rejection reason.
func embeddedContent(att map[string]any) (text string, image *Image, reject rejection) {
	label, _ := att["label"].(string)
	ct, ok := att["contentType"].(string)
	if !ok {
		return "", nil, rejection(fmt.Sprintf("Embedded resource %s requires a content type", label))
	}
	contentType := baseContentType(ct)
	if contentType == "" {
		return "", nil, rejection(fmt.Sprintf("Embedded resource %s requires a content type", label))
	}
	bytesData, ok := decodeBase64(att["data"])
	if !ok {
		return "", nil, rejection(fmt.Sprintf("Embedded resource %s is not valid base64", label))
	}
	if mime := NormalizeImageMimeType(contentType); mime != "" {
		if len(bytesData) == 0 {
			return "", nil, rejection(fmt.Sprintf("Embedded image %s is empty", label))
		}
		return "", &Image{Type: "image", Data: base64.StdEncoding.EncodeToString(bytesData), MimeType: mime}, ""
	}
	// MIME is advisory for non-images: successful UTF-8 decoding decides whether an embedded
	// resource can safely join pi's text prompt.
	if !utf8.Valid(bytesData) {
		return "", nil, rejection(fmt.Sprintf("Embedded resource %s is not valid UTF-8", label))
	}
	decoded := string(bytesData)
	if r, ok := selectionRange(att); ok {
		return fmt.Sprintf("[selection %s]\n%s", rangeText(r), decoded), nil, ""
	}
	return decoded, nil, ""
}

func resourceText(att map[string]any) (string, rejection) {
	uri, ok := att["uri"].(string)
	if !ok {
		return "", "A resource attachment requires a URI"
	}
	reference := uri
	if len(reference) >= 5 && strings.EqualFold(reference[:5], "file:") {
		// A remote or malformed file URI stays as it was rather than being guessed into a path.
		if p, err := wire.FileURIToPath(reference); err == nil {
			reference = p
		}
	}
	if r, ok := selectionRange(att); ok {
		reference += ":" + rangeText(r)
	}
	return reference, ""
}

type prepared struct {
	input  MessageInput
	reject rejection
}

func prepareMessage(value any) prepared {
	rej := func(s string) prepared { return prepared{reject: rejection(s)} }
	m, ok := isRecord(value)
	if !ok {
		return rej("A message requires text and an origin")
	}
	text, okText := m["text"].(string)
	origin, okOrigin := isRecord(m["origin"])
	if !okText || !okOrigin {
		return rej("A message requires text and an origin")
	}
	if _, ok := origin["kind"].(string); !ok {
		return rej("A message requires text and an origin")
	}
	var attachments []any
	if raw, present := m["attachments"]; present && raw != nil {
		arr, ok := raw.([]any)
		if !ok {
			return rej("Message attachments must be an array")
		}
		attachments = arr
	}
	if model, present := m["model"]; present && model != nil {
		mm, ok := isRecord(model)
		if !ok {
			return rej("A message model requires an id")
		}
		if _, ok := mm["id"].(string); !ok {
			return rej("A message model requires an id")
		}
	}
	if agent, present := m["agent"]; present && agent != nil {
		return rej("This host does not support custom agents")
	}

	representations := []string{}
	var images []Image
	for _, raw := range attachments {
		att, ok := isRecord(raw)
		if !ok {
			return rej("Every message attachment requires a type and label")
		}
		typ, okType := att["type"].(string)
		label, okLabel := att["label"].(string)
		if !okType || !okLabel {
			return rej("Every message attachment requires a type and label")
		}
		if r, present := att["range"]; present && r != nil && !isTextRange(r) {
			return rej(fmt.Sprintf("Attachment %s has an invalid text range", label))
		}
		if sel, present := att["selection"]; present && sel != nil {
			sm, ok := isRecord(sel)
			if !ok || !isTextRange(sm["range"]) {
				return rej(fmt.Sprintf("Attachment %s has an invalid text selection", label))
			}
		}
		switch typ {
		case "resource":
			s, r := resourceText(att)
			if r != "" {
				return prepared{reject: r}
			}
			representations = append(representations, s)
		case "simple":
			rep, ok := att["modelRepresentation"].(string)
			if !ok {
				return rej("A simple attachment requires modelRepresentation")
			}
			if rep != "" {
				representations = append(representations, rep)
			}
		case "embeddedResource":
			s, img, r := embeddedContent(att)
			if r != "" {
				return prepared{reject: r}
			}
			if img != nil {
				images = append(images, *img)
			} else {
				representations = append(representations, s)
			}
		default:
			return rej(fmt.Sprintf("This host does not support %s attachments", typ))
		}
	}

	parts := []string{}
	for _, p := range append([]string{text}, representations...) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return prepared{input: MessageInput{Text: strings.Join(parts, "\n\n"), Images: images}}
}

// MessageRejectionReason says why a client message cannot become a pi prompt ("" when it can).
func MessageRejectionReason(message any) string {
	return string(prepareMessage(message).reject)
}

// MessageInputForPi converts an AHP message to pi's prompt shape; the error carries the
// rejection reason.
func MessageInputForPi(message any) (MessageInput, error) {
	p := prepareMessage(message)
	if p.reject != "" {
		return MessageInput{}, errors.New(string(p.reject))
	}
	return p.input, nil
}

// MessageTextForPi is the text of MessageInputForPi.
func MessageTextForPi(message any) (string, error) {
	in, err := MessageInputForPi(message)
	return in.Text, err
}
