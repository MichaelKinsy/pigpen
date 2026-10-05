// Package compose is the composition root (port of src/host/pi-host.ts createPiHost): it builds
// a host and wires the independently tested services to it. Nothing here opens a socket; the
// caller decides whether and where to listen.
package compose

import (
	"errors"
	"sync"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
	"github.com/MichaelKinsy/pigpen/ahp/internal/svc"
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
}

// Built is the composed host and the services a caller may need to reach or shut down.
type Built struct {
	Host      *host.Host
	Sessions  *pi.Services
	Resources *svc.ResourceService // nil unless Filesystem was given
	Watches   *svc.WatchService    // nil unless Filesystem was given
	Terminals *svc.TerminalService // nil unless Terminals was given

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

	if fs := opts.Filesystem; fs != nil {
		if len(fs.Roots) == 0 && !fs.Unrestricted {
			return nil, errors.New("compose: filesystem access needs roots, or Unrestricted set explicitly")
		}
		var roots []string
		if !fs.Unrestricted {
			roots = fs.Roots
		}
		policy, err := svc.NewPathPolicy(roots...)
		if err != nil {
			return nil, err
		}
		built.Resources = svc.NewResourceService(svc.ResourceOptions{Paths: policy})
		built.Watches = svc.NewWatchService(h, svc.WatchOptions{Paths: policy, Log: func(m string) { logf("%s", m) }})
		caps.Resources = built.Resources
		caps.ResourceWatches = built.Watches
	}
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
		if b.Terminals != nil {
			b.Terminals.Shutdown()
		}
		if b.Watches != nil {
			<-b.Watches.Dispose()
		}
	})
}
