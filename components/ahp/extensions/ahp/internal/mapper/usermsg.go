package mapper

import (
	"strconv"
	"strings"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// NormalizeImageMimeType normalizes MIME values at the AHP/pi image boundary ("" when the value
// is not an image type).
func NormalizeImageMimeType(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	base = strings.ToLower(trimJS(base))
	if !strings.HasPrefix(base, "image/") {
		return ""
	}
	if base == "image/jpg" {
		return "image/jpeg"
	}
	return base
}

func blocksOf(content any) []map[string]any {
	arr, _ := content.([]any)
	var out []map[string]any
	for _, b := range arr {
		if m, ok := b.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// TextFromPiUserContent is the text of a pi user message (a string or content blocks).
func TextFromPiUserContent(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	var sb strings.Builder
	for _, b := range blocksOf(content) {
		if b["type"] == "text" {
			if s, ok := b["text"].(string); ok {
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

func imageAttachments(content any) []ahptypes.MessageAttachment {
	var out []ahptypes.MessageAttachment
	for _, b := range blocksOf(content) {
		data, okData := b["data"].(string)
		mime, okMime := b["mimeType"].(string)
		if b["type"] != "image" || !okData || !okMime {
			continue
		}
		contentType := NormalizeImageMimeType(mime)
		if contentType == "" {
			continue
		}
		kind := "image"
		out = append(out, ahptypes.MessageAttachment{Value: &ahptypes.MessageEmbeddedResourceAttachment{
			Type:        ahptypes.MessageAttachmentKindEmbeddedResource,
			Label:       "Image " + strconv.Itoa(len(out)+1),
			DisplayKind: &kind,
			Data:        data,
			ContentType: contentType,
		}})
	}
	return out
}

// UserMessageFromPiContent reconstructs the protocol-visible part of a user message stored by
// pi. pi persists image bytes but not client-side labels, so replay uses stable ordinal labels
// while preserving the actual image and MIME type.
func UserMessageFromPiContent(content any) ahptypes.Message {
	msg := ahptypes.Message{
		Text:   TextFromPiUserContent(content),
		Origin: ahptypes.MessageOrigin{Kind: ahptypes.MessageKindUser},
	}
	if atts := imageAttachments(content); len(atts) > 0 {
		msg.Attachments = atts
	}
	return msg
}
