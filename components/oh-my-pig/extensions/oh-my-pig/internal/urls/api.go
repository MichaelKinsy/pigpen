// Package urls is the internal URL router: one process-wide registry of scheme
// handlers (skill://, local://, agent://, proc://, ...). Tools ask the router
// what a URL is and how it may be used, and never branch on scheme names.
//
// upstream: packages/coding-agent/src/internal-urls/types.ts, router.ts
//
// This file is the contract between areas. An area that owns a scheme
// registers a Handler from its Register function. An area that consumes URLs
// (read, write, grep, glob, the system prompt) calls the functions in
// router.go. Exported names and signatures in this file are fixed; add, do not
// change.
package urls

import (
	"net/url"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Backing says how a scheme's resources exist. upstream: types.ts SchemeBacking.
type Backing string

const (
	// BackingFile: content is the bytes of the file Locate returns; read
	// routes it through its filesystem pipeline.
	BackingFile Backing = "file"
	// BackingVirtual: content is rendered by the handler.
	BackingVirtual Backing = "virtual"
	// BackingRemote: content lives on another host or service.
	BackingRemote Backing = "remote"
	// BackingDevice: a session-bound control surface (processes, replies).
	BackingDevice Backing = "device"
)

// Selectors is the read-selector grammar after the URL. upstream: SchemeSelectors.
type Selectors string

const (
	// SelectorsLines: a trailing `:<selector>` chain is a read selector.
	SelectorsLines Selectors = "lines"
	// SelectorsNone: the URL is passed through as written.
	SelectorsNone Selectors = "none"
	// SelectorsOpaque: server-defined URIs that may end in selector-shaped tails.
	SelectorsOpaque Selectors = "opaque"
)

// WritePolicy is the write policy of a writable scheme. upstream: SchemeWritePolicy.
type WritePolicy struct {
	// Via is "file" (tools write the file Locate returns with Create) or
	// "handler" (the handler's Write performs it).
	Via string
	// Payload is "text" (model-authored text) or "verbatim" (raw payload).
	Payload string
	// Scope is the mutation class. upstream: url-scheme-host.ts SchemeWriteScope.
	Scope string
	// MaxBytes is the largest payload accepted; 0 is unbounded.
	MaxBytes int
	// Cancels reports a write that only cancels background work
	// (proc://<id>/kill). Nil means never.
	Cancels func(u URL) bool
}

// Spec declares how tools may consume a scheme. upstream: SchemeSpec.
type Spec struct {
	Backing   Backing
	Selectors Selectors
	// PortAuthority: a trailing `:N` with no path after the authority is a port.
	PortAuthority bool
	// PathAuthority: the authority is the first path segment under one root.
	PathAuthority bool
	// Immutable is the default immutability of resolved resources.
	Immutable bool
	// UnspilledPages: located read pages get the full budget and never spill.
	UnspilledPages bool
	// ReadTier is the approval tier for reading; empty is "read".
	ReadTier string
	// Unbounded: read output bypasses result truncation limits.
	Unbounded bool
	// Linkable: renderers may call Locate to hyperlink these URLs.
	Linkable bool
	// CompactTranscript: read cards collapse like plain files.
	CompactTranscript bool
	// ImageQuestion: read peels a trailing `?q=<question>` as an image question.
	ImageQuestion bool
	// SingleSlashAlias: `scheme:/x` and `/cwd/scheme://x` are aliases.
	SingleSlashAlias bool
	// Write is nil on read-only schemes.
	Write *WritePolicy
}

// URL is a parsed internal URL. upstream: InternalUrl.
type URL struct {
	// Href is the normalized `scheme://...` form.
	Href string
	// RawHref is the exact input, before any normalization.
	RawHref string
	// Scheme is lowercased, without `://`.
	Scheme string
	// Host is the raw host segment, case preserved. upstream: rawHost.
	Host string
	// Path is the raw path after the host, traversal markers preserved.
	// upstream: rawPathname.
	Path string
	// Query is the parsed query.
	Query url.Values
	// RawQuery is the query as written, without `?`.
	RawQuery string
}

// Context is the caller of a resolve or a write. upstream: ResolveContext, WriteContext.
type Context struct {
	// SDK is the calling session's context: identity (session id and file),
	// cancellation, UI.
	SDK sdk.Context
	// Cwd is the working directory of the calling session.
	Cwd string
	// SkipDirectoryListing asks handlers to return the directory shape with
	// empty content where a listing is expensive.
	SkipDirectoryListing bool
	// ToolCallID is the tool call dispatching a write; empty otherwise.
	ToolCallID string
}

// Resource is what a handler resolves a URL to. upstream: InternalResource.
type Resource struct {
	// URL is the canonical URL that was resolved.
	URL string
	// Content is the resolved text. Bytes, when non-nil, is a binary resource
	// and Content is ignored.
	Content string
	Bytes   []byte
	// ContentType is the MIME type.
	ContentType string
	// SourcePath is the underlying filesystem path; never shown to the model.
	SourcePath string
	// SourceStartLine is the first source-file line of an excerpt.
	SourceStartLine int
	// Notes are additional notes about resolution.
	Notes []string
	// Details are structured snapshots for transcript rendering only.
	Details map[string]any
	// Value marks a discrete extracted value that read returns as-is: no line
	// selectors, no paging. upstream: shape "value".
	Value bool
	// Absent: the JSON path named no stored value.
	Absent bool
	// Immutable overrides Spec.Immutable for this resource when non-nil.
	Immutable *bool
	// Unbounded overrides Spec.Unbounded for this resource when non-nil.
	Unbounded *bool
	// IsDirectory: the resource is a directory listing, not file content.
	IsDirectory bool
}

// WriteResult replaces the write tool's default result. upstream: InternalWriteResult.
type WriteResult struct {
	// Text is the model-facing result.
	Text string
	// Details are transcript-only display details.
	Details map[string]any
	IsError bool
	// Useless marks a result compaction may drop once consumed.
	Useless bool
}

// LocateOptions are the options of Locator.Locate. upstream: LocateOptions.
type LocateOptions struct {
	// Directory locates the containing directory form where a scheme
	// distinguishes it.
	Directory bool
	// Create returns the path even when the entry does not exist yet.
	Create bool
	// LocalOnly reports a miss now instead of deferring it to a remote resolve.
	LocalOnly bool
}

// Leaf is one searchable document of a virtual container. upstream: enumerate.
type Leaf struct {
	URL     string
	Content string
}

// DirEntry is one immediate child of a directory. upstream: ShellFsDirEntry.
type DirEntry struct {
	Name  string
	IsDir bool
	Size  int64
}

// Completion is one autocomplete candidate. upstream: UrlCompletion.
type Completion struct {
	Value       string
	Label       string
	Description string
}

// SchemeHost are the session facts that decide which schemes the system
// prompt advertises. upstream: SchemeHost.
type SchemeHost struct {
	SkillURIAccess      bool
	RuleCount           int
	MemoryBackend       string
	SecurityEnabled     bool
	SettingsApproval    bool
	OutReplies          bool
	CrossInstanceAgents bool
	Subagent            bool
}

// Handler serves one scheme. upstream: ProtocolHandler.
type Handler interface {
	// Scheme is the scheme without `://`.
	Scheme() string
	// Spec is the declared consumption contract.
	Spec() Spec
	// Resolve returns the content of u. Errors carry a message the model reads.
	Resolve(c Context, u URL) (*Resource, error)
}

// Writer is implemented exactly by handlers whose Spec.Write.Via is "handler".
type Writer interface {
	// Write performs the write. A nil result keeps the write tool's default.
	Write(c Context, u URL, content string) (*WriteResult, error)
}

// Locator returns the local file or directory backing a URL without
// materializing content. ok is false when the URL has no local backing.
type Locator interface {
	Locate(c Context, u URL, opts LocateOptions) (path string, ok bool, err error)
}

// Enumerator expands a virtual container URL into its searchable leaves.
type Enumerator interface {
	Enumerate(c Context, u URL) ([]Leaf, error)
}

// DirectoryReader lists the immediate children of a remote or virtual directory.
type DirectoryReader interface {
	ReadDirectory(c Context, u URL) ([]DirEntry, error)
}

// Completer returns autocomplete candidates for the part after `scheme://`.
// It must be fast and local.
type Completer interface {
	Complete(c Context, query string) ([]Completion, error)
}

// PromptDocer supplies the scheme's one-line system-prompt entry; "" omits it.
type PromptDocer interface {
	PromptDoc(host SchemeHost) string
}

// PromptReferencer supplies the grammar the PromptDoc line leaves out, served
// at help://read/urls; "" omits it.
type PromptReferencer interface {
	PromptReference() string
}
