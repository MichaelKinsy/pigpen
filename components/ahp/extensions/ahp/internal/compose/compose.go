// Package compose is the composition root (port of src/host/pi-host.ts createPiHost): it builds
// a host and wires the independently tested services to it. Nothing here opens a socket; the
// caller decides whether and where to listen.
package compose

import (
	"context"
	"errors"
	"sync"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/svc"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// MentionTrigger is the character that starts a file mention in a message.
const MentionTrigger = "@"

// FilesystemOptions opts a client into browsing and editing files (resources and watches). The
// remote filesystem is never exposed unless this is given: Roots confine every path, and
// Unrestricted is the explicit way to drop the confinement.
type FilesystemOptions struct {
	Roots        []string
	Unrestricted bool
}

// TerminalOptions opts a client into shells on this machine.
type TerminalOptions struct {
	Shell string
	Spawn svc.PtySpawner // nil = the real PTY
}

// Options configure Build.
type Options struct {
	ServerInfo       *ahptypes.Implementation
	WorkingDirectory string
	// Models lists the models offered to clients; nil or empty means none.
	Models func() []pi.Model
	// SessionRoot is where Pi's session files live, for the catalogue and hydration.
	SessionRoot string
	// CreateSessionManager is the storage for sessions created through the host.
	CreateSessionManager pi.SessionManagerFactory
	CreateBackend        pi.BackendFactory
	DefaultSelection     func() *ahptypes.ModelSelection
	DeleteFile           func(path string) (pi.SessionFileDeletionResult, error)
	// WrapStore replaces the store of a session hydrated from disk (see pi.HydratorOptions).
	WrapStore func(sessionID string, opened pi.SessionStore) pi.SessionStore
	// AdoptForeignTurns mirrors prompts typed into the session locally into its chat.
	AdoptForeignTurns  bool
	AgentDir           string
	ProjectTrustPolicy pi.ProjectTrustPolicy
	Log                func(format string, args ...any)

	// Filesystem and Terminals are nil unless the user opted in.
	Filesystem *FilesystemOptions
	Terminals  *TerminalOptions

	// TuneChangesets adjusts the git changeset service (git backend, watcher, delays) before it is
	// built; nil in production, where the upstream defaults apply.
	TuneChangesets func(*changeset.Options)
}

// Built is the composed host and the services a caller may need to reach or shut down.
type Built struct {
	Host      *host.Host
	Sessions  *pi.Services
	Resources *svc.ResourceService // nil unless Filesystem was given
	Watches   *svc.WatchService    // nil unless Filesystem was given
	Terminals *svc.TerminalService // nil unless Terminals was given
	// Changesets publishes the git changesets of every session; always present.
	Changesets *changeset.Service

	closeOnce sync.Once
}

// Build composes the host.
func Build(opts Options) (*Built, error) {
	if opts.CreateBackend == nil {
		return nil, errors.New("compose: CreateBackend is required")
	}
	if opts.CreateSessionManager == nil {
		opts.CreateSessionManager = pi.InMemoryStorage
	}
	logf := func(format string, args ...any) {
		if opts.Log != nil {
			opts.Log(format, args...)
		}
	}
	h := host.New(host.Options{
		ServerInfo: opts.ServerInfo, CompletionTriggerCharacters: []string{MentionTrigger},
		Log: func(message string) { logf("%s", message) },
	})
	var models []pi.Model
	if opts.Models != nil {
		models = opts.Models()
	}
	h.InstallRootChannel([]ahptypes.AgentInfo{pi.BuildAgentInfo(models)})

	deleteFile := opts.DeleteFile
	if deleteFile == nil {
		deleteFile = pi.FileDeleter
	}
	sessions := pi.NewServices(pi.ServicesOptions{
		Host: h, SessionRoot: opts.SessionRoot, DefaultWorkingDirectory: opts.WorkingDirectory,
		CreateBackend: opts.CreateBackend, CreateSessionManager: opts.CreateSessionManager,
		DefaultSelection: opts.DefaultSelection, DeleteFile: deleteFile, WrapStore: opts.WrapStore, AdoptForeignTurns: opts.AdoptForeignTurns, Log: logf,
	})
	built := &Built{Host: h, Sessions: sessions}

	caps := sessions.Capabilities()
	caps.SessionConfig = pi.NewSessionConfigService(pi.SessionConfigOptions{
		DefaultWorkingDirectory: opts.WorkingDirectory, ProjectTrustPolicy: opts.ProjectTrustPolicy, AgentDir: opts.AgentDir,
	})
	caps.Completions = pi.NewCompletionService(pi.CompletionServiceOptions{WorkingDirectoryFor: func(chat string) string {
		if s, ok := sessions.Registry.GetByChat(chat); ok {
			return s.WorkingDirectory
		}
		return ""
	}})

	var policy *svc.PathPolicy
	if fs := opts.Filesystem; fs != nil {
		if len(fs.Roots) == 0 && !fs.Unrestricted {
			return nil, errors.New("compose: filesystem access needs roots, or Unrestricted set explicitly")
		}
		var roots []string
		if !fs.Unrestricted {
			roots = fs.Roots
		}
		var err error
		policy, err = svc.NewPathPolicy(roots...)
		if err != nil {
			return nil, err
		}
		built.Resources = svc.NewResourceService(svc.ResourceOptions{Paths: policy})
		built.Watches = svc.NewWatchService(h, svc.WatchOptions{Paths: policy, Log: func(m string) { logf("%s", m) }})
		caps.ResourceWatches = built.Watches
	}
	changesets := newChangesets(h, sessions, policy, opts, logf)
	built.Changesets = changesets
	var files host.ResourceHandler
	if built.Resources != nil {
		files = built.Resources
	}
	caps.Resources = &resources{files: files, changesets: changesets}
	caps.Sessions = &sessionLifecycle{registry: sessions.Registry, changesets: changesets}
	caps.Hydrator = hydrators{changesets, sessions.Hydrator}
	if t := opts.Terminals; t != nil {
		built.Terminals = svc.NewTerminalService(h, svc.TerminalOptions{
			DefaultWorkingDirectory: opts.WorkingDirectory, Shell: t.Shell, Spawn: t.Spawn, Log: func(m string) { logf("%s", m) },
		})
		caps.Terminals = built.Terminals
	}
	h.Serve(caps)
	return built, nil
}

// Close shuts the optional services down: terminals are killed, watches released.
func (b *Built) Close() {
	b.closeOnce.Do(func() {
		b.Changesets.Dispose()
		if b.Terminals != nil {
			b.Terminals.Shutdown()
		}
		if b.Watches != nil {
			<-b.Watches.Dispose()
		}
	})
}

func newChangesets(h *host.Host, sessions *pi.Services, policy *svc.PathPolicy, opts Options, logf func(string, ...any)) *changeset.Service {
	toSession := func(s *pi.LiveSession) changeset.Session {
		return changeset.Session{URI: s.URI, SessionID: s.SessionID, WorkingDirectory: s.WorkingDirectory}
	}
	byID := func(id string) (changeset.Session, bool) {
		s, ok := sessions.Registry.Get(wire.SessionURI(id))
		if !ok {
			return changeset.Session{}, false
		}
		return toSession(s), true
	}
	co := changeset.Options{
		GetSession: byID,
		GetSessionByChat: func(chat string) (changeset.Session, bool) {
			s, ok := sessions.Registry.GetByChat(chat)
			if !ok {
				return changeset.Session{}, false
			}
			return toSession(s), true
		},
		HydrateSession: func(ctx context.Context, id string) (changeset.Session, bool, error) {
			if _, err := sessions.Hydrator.Hydrate(ctx, wire.SessionURI(id)); err != nil {
				return changeset.Session{}, false, err
			}
			session, ok := byID(id)
			return session, ok, nil
		},
		OnSessionAvailable: func(listener func(changeset.Session)) func() {
			return sessions.Registry.OnSessionAvailable(func(s *pi.LiveSession) { listener(toSession(s)) })
		},
		OnSessionDeletionCommitted: func(listener func(string) error) func() {
			return sessions.Registry.OnSessionDeletionCommitted(listener)
		},
		Debounce:   changeset.DefaultDebounce,
		StateGrace: changeset.DefaultStateGrace,
		Log:        logf,
	}
	if policy != nil {
		co.AuthorizeResource = func(uri string) error { _, err := policy.PathFor(uri); return err }
	}
	if opts.TuneChangesets != nil {
		opts.TuneChangesets(&co)
	}
	return changeset.New(h, co)
}

// resources puts the git-blob: content of the changesets in front of the remote filesystem. The
// changesets are read-only views confined to a session's working directory, so their content is
// served whether or not the filesystem is opted in; everything else needs the filesystem.
type resources struct {
	files      host.ResourceHandler // nil unless the filesystem is opted in
	changesets *changeset.Service
}

func (r *resources) handler() (host.ResourceHandler, error) {
	if r.files == nil {
		return nil, wire.MethodNotFound("resource*")
	}
	return r.files, nil
}

func (r *resources) Read(ctx context.Context, p ahptypes.ResourceReadParams) (ahptypes.ResourceReadResult, error) {
	virtual, err := r.changesets.ReadResource(ctx, p)
	if err != nil {
		return ahptypes.ResourceReadResult{}, err
	}
	if virtual != nil {
		return *virtual, nil
	}
	files, err := r.handler()
	if err != nil {
		return ahptypes.ResourceReadResult{}, err
	}
	return files.Read(ctx, p)
}

func (r *resources) Write(ctx context.Context, p ahptypes.ResourceWriteParams) error {
	files, err := r.handler()
	if err != nil {
		return err
	}
	return files.Write(ctx, p)
}

func (r *resources) List(ctx context.Context, uri string) (ahptypes.ResourceListResult, error) {
	files, err := r.handler()
	if err != nil {
		return ahptypes.ResourceListResult{}, err
	}
	return files.List(ctx, uri)
}

func (r *resources) Resolve(ctx context.Context, p ahptypes.ResourceResolveParams) (ahptypes.ResourceResolveResult, error) {
	files, err := r.handler()
	if err != nil {
		return ahptypes.ResourceResolveResult{}, err
	}
	return files.Resolve(ctx, p)
}

func (r *resources) Mkdir(ctx context.Context, p ahptypes.ResourceMkdirParams) error {
	files, err := r.handler()
	if err != nil {
		return err
	}
	return files.Mkdir(ctx, p)
}

func (r *resources) Delete(ctx context.Context, p ahptypes.ResourceDeleteParams) error {
	files, err := r.handler()
	if err != nil {
		return err
	}
	return files.Delete(ctx, p)
}

func (r *resources) Move(ctx context.Context, p ahptypes.ResourceMoveParams) error {
	files, err := r.handler()
	if err != nil {
		return err
	}
	return files.Move(ctx, p)
}

func (r *resources) Copy(ctx context.Context, p ahptypes.ResourceCopyParams) error {
	files, err := r.handler()
	if err != nil {
		return err
	}
	return files.Copy(ctx, p)
}

// sessionLifecycle pauses a session's changeset work around a disposal and resumes it when the
// disposal fails (upstream's sessions.dispose in pi-host.ts).
type sessionLifecycle struct {
	registry   *pi.Registry
	changesets *changeset.Service
}

func (l *sessionLifecycle) Create(ctx context.Context, params ahptypes.CreateSessionParams) error {
	return l.registry.Create(ctx, params)
}

func (l *sessionLifecycle) Dispose(ctx context.Context, channel string) error {
	var sessionID string
	if s, ok := l.registry.Get(channel); ok {
		sessionID = s.SessionID
		l.changesets.SuspendSession(sessionID)
	}
	err := l.registry.Dispose(ctx, channel)
	if err != nil && sessionID != "" {
		l.changesets.ResumeSession(sessionID)
	}
	return err
}

// hydrators asks each in turn; the first to know the channel wins.
type hydrators []host.ChannelHydrator

func (h hydrators) Hydrate(ctx context.Context, channel string) (bool, error) {
	for _, one := range h {
		ok, err := one.Hydrate(ctx, channel)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
