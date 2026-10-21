package pi_permission_system

import (
	"fmt"
	"sync"
)

// Port of src/authority/authorizer-registry.ts: the registry for named live-authority chain links (ADR 0007 section 4). A link
// decides nothing until the operator names it in the authorizerChain config; this registry is storage only.

// AuthorizerLookup is the read-only lookup used by chain composition. A nil AuthorizeFunc is undefined.
type AuthorizerLookup interface {
	Get(name string) AuthorizeFunc
}

// AuthorizerRegistrar is the registration side of the registry.
type AuthorizerRegistrar interface {
	Register(name string, authorize AuthorizeFunc) (dispose func(), err error)
}

// AuthorizerRegistry is the persistent registry mapping link names to their authorize callbacks. It is safe for concurrent use.
type AuthorizerRegistry struct {
	mu    sync.Mutex
	links map[string]*registeredLink
}

// registeredLink boxes the callback so the disposer can compare identity, as the original compares function references.
type registeredLink struct{ authorize AuthorizeFunc }

// Register registers a link under name. It fails if a link is already registered for that name (resolution stays deterministic).
// The returned disposer removes the link; it is identity-guarded, so a stale call cannot evict a later registration.
func (r *AuthorizerRegistry) Register(name string, authorize AuthorizeFunc) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.links == nil {
		r.links = map[string]*registeredLink{}
	}
	if _, dup := r.links[name]; dup {
		return nil, fmt.Errorf("An authorizer is already registered for '%s'.", name)
	}
	entry := &registeredLink{authorize: authorize}
	r.links[name] = entry
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.links[name] == entry {
			delete(r.links, name)
		}
	}, nil
}

// Get answers the callback registered under name, or nil.
func (r *AuthorizerRegistry) Get(name string) AuthorizeFunc {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.links[name]; ok {
		return entry.authorize
	}
	return nil
}

// AdjudicationRole is the node's chain role as a fact a collaborator can read: does this node's authorizer chain run, or does it
// relay its asks to a serving node (ADR 0007 section 7)?
type AdjudicationRole interface {
	AdjudicatesLocally() bool
}

// ObservedAuthorizerRegistrar is the registrar a sibling extension reaches through registerAuthorizer: it accepts every
// registration, and records the ones this node will never consult (ADR 0012 decision 4, accept and observe). It decorates the
// registry: storage stays storage, and the chain's own lookup keeps reading the undecorated registry.
type ObservedAuthorizerRegistrar struct {
	registrar AuthorizerRegistrar
	role      AdjudicationRole
	logger    ReviewLogger
}

// NewObservedAuthorizerRegistrar is `new ObservedAuthorizerRegistrar(registrar, role, logger)`.
func NewObservedAuthorizerRegistrar(registrar AuthorizerRegistrar, role AdjudicationRole, logger ReviewLogger) *ObservedAuthorizerRegistrar {
	return &ObservedAuthorizerRegistrar{registrar: registrar, role: role, logger: logger}
}

// Register registers name into the underlying registry and returns its disposer. The role is read per registration, not captured at
// construction. A duplicate registration still fails, and records nothing: under the cross-node contract that is a genuine author
// bug, not a vacancy.
func (o *ObservedAuthorizerRegistrar) Register(name string, authorize AuthorizeFunc) (func(), error) {
	dispose, err := o.registrar.Register(name, authorize)
	if err != nil {
		return nil, err
	}
	if !o.role.AdjudicatesLocally() {
		entry := newObject()
		entry.set("name", name)
		o.logger.Review("authorizer_link_vacant", entry)
	}
	return dispose, nil
}
