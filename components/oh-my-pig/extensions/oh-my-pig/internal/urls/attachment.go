package urls

import (
	"fmt"
	"strings"
	"sync"
)

// Attachment describes an image attachment registered for the calling session.
type Attachment struct {
	// URI is the canonical `attachment://<N>` identifier.
	URI        string
	Label      string
	MIMEType   string
	SourcePath string
}

var (
	attachMu       sync.RWMutex
	attachProvider func(c Context) []Attachment
)

// RegisterAttachment records an image attachment for the session in c.
func RegisterAttachment(c Context, a Attachment) {
	key, ok := resolveSessionKey(c)
	if !ok {
		return
	}
	sessionStateMu.Lock()
	defer sessionStateMu.Unlock()
	st := getOrCreateSessionStateLocked(key)
	for i := range st.attachments {
		if st.attachments[i].URI == a.URI {
			st.attachments[i] = a
			return
		}
	}
	st.attachments = append(st.attachments, a)
}

// SetAttachmentsProvider installs a callback returning the active session's attachments.
func SetAttachmentsProvider(fn func(c Context) []Attachment) {
	attachMu.Lock()
	attachProvider = fn
	attachMu.Unlock()
}

// ResetAttachmentsForTests clears all registered attachments and providers.
func ResetAttachmentsForTests() {
	attachMu.Lock()
	attachProvider = nil
	attachMu.Unlock()
	sessionStateMu.Lock()
	for _, st := range sessionStates {
		st.attachments = nil
	}
	sessionStateMu.Unlock()
}

func resolveAttachments(c Context) []Attachment {
	attachMu.RLock()
	prov := attachProvider
	attachMu.RUnlock()
	if prov != nil {
		return prov(c)
	}
	key, ok := resolveSessionKey(c)
	if !ok {
		return nil
	}
	sessionStateMu.RLock()
	st, exists := sessionStates[key]
	var list []Attachment
	if exists && len(st.attachments) > 0 {
		list = append([]Attachment(nil), st.attachments...)
	}
	sessionStateMu.RUnlock()
	return list
}
func attachmentURI(u URL) string {
	return "attachment://" + u.Host + u.Path
}

// AttachmentHandler serves `attachment://<N>` URLs for session image attachments.
// upstream: attachment-protocol.ts AttachmentProtocolHandler.
type AttachmentHandler struct{}

func (AttachmentHandler) Scheme() string { return "attachment" }

func (AttachmentHandler) Spec() Spec {
	return Spec{
		Backing:       BackingFile,
		Selectors:     SelectorsNone,
		Immutable:     true,
		ImageQuestion: true,
	}
}

func (AttachmentHandler) Resolve(c Context, u URL) (*Resource, error) {
	uri := attachmentURI(u)
	attachments := resolveAttachments(c)
	for _, a := range attachments {
		if a.URI == uri {
			content := fmt.Sprintf("%s: %s image attachment stored at %s", a.Label, a.MIMEType, a.SourcePath)
			return &Resource{
				URL:         uri,
				Content:     content,
				ContentType: "text/plain",
				SourcePath:  a.SourcePath,
			}, nil
		}
	}
	avail := "none"
	if len(attachments) > 0 {
		uris := make([]string, len(attachments))
		for i, a := range attachments {
			uris[i] = a.URI
		}
		avail = strings.Join(uris, ", ")
	}
	return nil, fmt.Errorf(
		"Could not resolve image attachment '%s'. Available attachment URIs: %s. Use one of the listed attachment URIs, or attach an image first when none are available.",
		uri, avail,
	)
}

func (AttachmentHandler) Locate(c Context, u URL, _ LocateOptions) (string, bool, error) {
	uri := attachmentURI(u)
	for _, a := range resolveAttachments(c) {
		if a.URI == uri && a.SourcePath != "" {
			return a.SourcePath, true, nil
		}
	}
	return "", false, nil
}
