package pi

import (
	"context"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
)

// ServicesOptions wire the session services together.
type ServicesOptions struct {
	Host *host.Host
	// SessionRoot is where Pi's session files live (one sub-directory per working directory).
	SessionRoot             string
	DefaultWorkingDirectory string
	CreateBackend           BackendFactory
	CreateSessionManager    SessionManagerFactory
	DefaultSelection        func() *ahptypes.ModelSelection
	DeleteFile              func(path string) (SessionFileDeletionResult, error)
	WrapStore               func(sessionID string, opened SessionStore) SessionStore
	AdoptForeignTurns       bool
	Log                     func(format string, args ...any)
}

// Services is the registry, catalogue and hydrator wired to one host.
type Services struct {
	Registry  *Registry
	Catalogue *Catalogue
	Hydrator  *Hydrator
}

// NewServices builds the session services.
func NewServices(opts ServicesOptions) *Services {
	catalogue := NewCatalogue(opts.SessionRoot)
	registry := NewRegistry(RegistryOptions{
		Host: opts.Host, DefaultWorkingDirectory: opts.DefaultWorkingDirectory, CreateBackend: opts.CreateBackend,
		CreateSessionManager: opts.CreateSessionManager, DefaultSelection: opts.DefaultSelection, DeleteFile: opts.DeleteFile,
		FindSessionFile: catalogue.FindSessionFile, AdoptForeignTurns: opts.AdoptForeignTurns, Log: opts.Log,
	})
	hydrator := NewHydrator(HydratorOptions{
		Host: opts.Host, Catalogue: catalogue,
		IsLive:            registry.Has,
		IsDisposing:       registry.IsDisposing,
		FallbackSelection: opts.DefaultSelection,
		Adopt:             func(a AdoptedSession) { registry.Adopt(a) },
		WrapStore:         opts.WrapStore,
		Log:               opts.Log,
	})
	return &Services{Registry: registry, Catalogue: catalogue, Hydrator: hydrator}
}

type liveCatalogue struct{ s *Services }

func (c liveCatalogue) List(ctx context.Context, limit *int64, cursor *string) (ahptypes.ListSessionsResult, error) {
	return c.s.Catalogue.List(ctx, limit, cursor, c.s.Registry.CatalogueOverrides)
}

type turnPaging struct{ r *Registry }

func (p turnPaging) FetchTurns(ctx context.Context, params ahptypes.FetchTurnsParams) error {
	return p.r.FetchTurns(ctx, params)
}

// Capabilities are what the host serves through these services.
func (s *Services) Capabilities() host.Capabilities {
	return host.Capabilities{
		Catalogue: liveCatalogue{s}, Sessions: s.Registry, Hydrator: s.Hydrator, TurnPaging: turnPaging{s.Registry},
	}
}
