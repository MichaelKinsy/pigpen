// Package changeset is the git changeset service of pi-ahp (src/pi/changeset-uri.ts,
// git-changes.ts, changeset-service.ts). This file is the signature stub of the red commit: every
// function compiles and does nothing, so the twins fail on behaviour.
package changeset

import (
	"context"
	"time"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
)

// Kind says which static changeset a channel is.
type Kind string

// The two changesets this host advertises.
const (
	LatestCommit Kind = "latest-commit"
	Uncommitted  Kind = "uncommitted"
)

// Kinds lists every changeset kind in catalogue order.
var Kinds = []Kind{LatestCommit, Uncommitted}

// ParsedURI is a recognised changeset channel.
type ParsedURI struct {
	SessionID string
	Kind      Kind
}

func URI(sessionID string, kind Kind) string                          { return "" }
func ParseURI(uri string) (ParsedURI, bool)                           { return ParsedURI{}, false }
func Catalogue(sessionID string, hasCommit bool) []ahptypes.Changeset { return nil }

type Workspace struct {
	Cwd, RepositoryRoot, GitDirectory, CommonGitDirectory, Pathspec, Head, Parent string
}

type BlobRef struct{ SessionID, OID, Path string }

type Git interface {
	Inspect(ctx context.Context, cwd string) (*Workspace, error)
	Compute(ctx context.Context, workspace Workspace, sessionID string, kind Kind) ([]ahptypes.ChangesetFile, error)
	IgnoredDirectories(ctx context.Context, workspace Workspace) ([]string, error)
	PathForBlob(workspace Workspace, blob BlobRef) (string, bool)
	ReadBlob(ctx context.Context, workspace Workspace, blob BlobRef) ([]byte, error)
}

func ParseBlobURI(uri string) (BlobRef, bool) { return BlobRef{}, false }

type GitChanges struct{}

func (GitChanges) Inspect(context.Context, string) (*Workspace, error) { return nil, nil }
func (GitChanges) Compute(context.Context, Workspace, string, Kind) ([]ahptypes.ChangesetFile, error) {
	return nil, nil
}
func (GitChanges) IgnoredDirectories(context.Context, Workspace) ([]string, error) { return nil, nil }
func (GitChanges) PathForBlob(Workspace, BlobRef) (string, bool)                   { return "", false }
func (GitChanges) ReadBlob(context.Context, Workspace, BlobRef) ([]byte, error)    { return nil, nil }

type Watcher interface{ Close() error }

type WatchFunc func(ctx context.Context, paths []string, ignored func(path string) bool, changed func(path string), onError func(error)) (Watcher, error)

const DefaultPollInterval = time.Second

func PollWatch(interval time.Duration) WatchFunc { return nil }

const (
	DefaultDebounce   = 250 * time.Millisecond
	DefaultStateGrace = 30 * time.Second
)

type Session struct{ URI, SessionID, WorkingDirectory string }

type Options struct {
	GetSession                 func(sessionID string) (Session, bool)
	GetSessionByChat           func(chat string) (Session, bool)
	HydrateSession             func(ctx context.Context, sessionID string) (Session, bool, error)
	OnSessionAvailable         func(listener func(Session)) (remove func())
	OnSessionDeletionCommitted func(listener func(sessionID string) error) (remove func())
	AuthorizeResource          func(uri string) error
	Git                        Git
	Watch                      WatchFunc
	PollInterval               time.Duration
	Debounce                   time.Duration
	StateGrace                 time.Duration
	Log                        func(format string, args ...any)
}

type Service struct{}

func New(h *host.Host, opts Options) *Service { return &Service{} }

func (*Service) ActiveWatcherCount() int                       { return 0 }
func (*Service) Attach(context.Context, Session) error         { return nil }
func (*Service) Hydrate(context.Context, string) (bool, error) { return false, nil }
func (*Service) SuspendSession(string)                         {}
func (*Service) ResumeSession(string)                          {}
func (*Service) RemoveSession(string)                          {}
func (*Service) Dispose()                                      {}
func (*Service) ReadResource(context.Context, ahptypes.ResourceReadParams) (*ahptypes.ResourceReadResult, error) {
	return nil, nil
}
