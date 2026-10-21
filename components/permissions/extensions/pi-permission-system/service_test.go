package pi_permission_system

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Twins of test/service.test.ts, test/service/permissions-service.test.ts, test/service/service-lifecycle.test.ts,
// test/service/bash-advisory-check.test.ts and the permissions:ready half of test/service/permission-events.test.ts.

// svcFake is makeFakePermissionsService(): every getter answers nothing unless a test sets one. A pointer, so two fakes never compare
// equal.
type svcFake struct {
	PermissionsService
	id int
}

func makeFakeService() *svcFake { return &svcFake{id: 1} }

// recordingResolver is the FakeResolver of the permissions-service twins.
type recordingResolver struct {
	intents       []AccessIntent
	prefixed      []PathValuesAccessIntent
	result        PermissionCheckResult
	toolPerm      string
	toolPermArgs  [][2]any
	fullyDenied   bool
	fullyDenyArgs [][2]any
}

func newRecordingResolver() *recordingResolver {
	return &recordingResolver{result: PermissionCheckResult{State: "allow", ToolName: "read", Source: "tool", Origin: "builtin"}, toolPerm: "ask"}
}

func (r *recordingResolver) Resolve(intent AccessIntent) PermissionCheckResult {
	r.intents = append(r.intents, intent)
	return r.result
}
func (r *recordingResolver) ResolvePrefixed(intent PathValuesAccessIntent) PermissionCheckResult {
	r.prefixed = append(r.prefixed, intent)
	return r.result
}
func (r *recordingResolver) GetToolPermission(tool string, agent *string) string {
	r.toolPermArgs = append(r.toolPermArgs, [2]any{tool, agent})
	return r.toolPerm
}
func (r *recordingResolver) IsToolFullyDenied(tool string, agent *string) bool {
	r.fullyDenyArgs = append(r.fullyDenyArgs, [2]any{tool, agent})
	return r.fullyDenied
}

type normalizerSession struct{ n *PathNormalizer }

func (s normalizerSession) GetPathNormalizer() *PathNormalizer { return s.n }

type svcRig struct {
	service    *LocalPermissionsService
	resolver   *recordingResolver
	formatters *ToolInputFormatterRegistry
	extractors *ToolAccessExtractorRegistry
	authorizer *AuthorizerRegistry
	advisory   [][3]any
}

func makeLocalService() *svcRig {
	r := &svcRig{resolver: newRecordingResolver(), formatters: &ToolInputFormatterRegistry{}, extractors: &ToolAccessExtractorRegistry{}, authorizer: &AuthorizerRegistry{}}
	r.service = NewLocalPermissionsService(r.resolver, normalizerSession{NewPathNormalizer(PosixPathFlavor, "/test/project")}, r.formatters, r.extractors, r.authorizer)
	return r
}

func sp2(s string) *string { return &s }

const adapterSession = "adapter-session"

func unpublishAdapter() {
	if current := GetPermissionsService(adapterSession); current != nil {
		UnpublishPermissionsService(adapterSession, current)
	}
}

func TestServiceLocatorTwins(t *testing.T) {
	const f = "service"
	const parent, child = "parent-session", "child-session"
	cleanup := func() {
		for _, id := range []string{parent, child} {
			if c := GetPermissionsService(id); c != nil {
				UnpublishPermissionsService(id, c)
			}
		}
	}
	tw(t, f, "returns undefined for a session that published nothing", func(t *testing.T) {
		defer cleanup()
		eq(t, GetPermissionsService(parent) == nil, true, "nil")
	})
	tw(t, f, "returns the service published under that session id", func(t *testing.T) {
		defer cleanup()
		s := makeFakeService()
		PublishPermissionsService(parent, s)
		eq(t, GetPermissionsService(parent) == PermissionsService(s), true, "same")
	})
	tw(t, f, "keys each node's service separately", func(t *testing.T) {
		defer cleanup()
		p, c := makeFakeService(), makeFakeService()
		PublishPermissionsService(parent, p)
		PublishPermissionsService(child, c)
		eq(t, GetPermissionsService(parent) == PermissionsService(p), true, "parent")
		eq(t, GetPermissionsService(child) == PermissionsService(c), true, "child")
	})
	tw(t, f, "replaces the entry when a session republishes", func(t *testing.T) {
		defer cleanup()
		first, second := makeFakeService(), makeFakeService()
		PublishPermissionsService(parent, first)
		PublishPermissionsService(parent, second)
		eq(t, GetPermissionsService(parent) == PermissionsService(second), true, "second")
	})
	tw(t, f, "removes the entry when it still holds the given service", func(t *testing.T) {
		defer cleanup()
		s := makeFakeService()
		PublishPermissionsService(parent, s)
		UnpublishPermissionsService(parent, s)
		eq(t, GetPermissionsService(parent) == nil, true, "removed")
	})
	tw(t, f, "leaves a fresh publication alone when a superseded generation unpublishes", func(t *testing.T) {
		defer cleanup()
		old, fresh := makeFakeService(), makeFakeService()
		PublishPermissionsService(parent, old)
		PublishPermissionsService(parent, fresh)
		UnpublishPermissionsService(parent, old)
		eq(t, GetPermissionsService(parent) == PermissionsService(fresh), true, "fresh stays")
	})
	tw(t, f, "unpublish is safe to call for a session that published nothing", func(t *testing.T) {
		defer cleanup()
		UnpublishPermissionsService(parent, makeFakeService())
	})
	tw(t, f, "stays silent across a publish, resolve, and unpublish round trip", func(t *testing.T) {
		defer cleanup()
		s := makeFakeService()
		PublishPermissionsService(parent, s)
		_ = GetPermissionsService(parent)
		UnpublishPermissionsService(parent, s)
	})
	tw(t, f, "returns undefined rather than another node's service", func(t *testing.T) {
		defer cleanup()
		PublishPermissionsService(parent, makeFakeService())
		eq(t, GetPermissionsService(child) == nil, true, "nil")
	})
	tskip(t, f, "warns once, naming the keyed locator and the ready payload", "a Go string is always present: the original's no-argument call (process.emitWarning) cannot be made")
	tskip(t, f, "does not warn when a session id is passed", "a Go string is always present: there is no warning to withhold")

	round := func() (*recordingResolver, PermissionsService) {
		bashparse.ResetWarmBashParser()
		r := newRecordingResolver()
		r.result = PermissionCheckResult{ToolName: "bash", State: "allow", MatchedPattern: sp2("git *"), Source: "bash", Origin: "global"}
		PublishPermissionsService(adapterSession, NewLocalPermissionsService(r, normalizerSession{NewPathNormalizer(PosixPathFlavor, "/test/project")},
			&ToolInputFormatterRegistry{}, &ToolAccessExtractorRegistry{}, &AuthorizerRegistry{}))
		return r, GetPermissionsService(adapterSession)
	}
	tw(t, f, "resolves a non-path query via a tool intent", func(t *testing.T) {
		defer unpublishAdapter()
		r, s := round()
		got := s.CheckPermission("bash", sp2("git push"), sp2("Explore"))
		eq(t, got, r.result, "result")
		eq(t, len(r.intents), 1, "one resolve")
		want := ToolAccessIntent{Surface: "bash", AgentName: sp2("Explore")}
		it := r.intents[0].(ToolAccessIntent)
		eq(t, it.Surface, want.Surface, "surface")
		eq(t, *it.AgentName, "Explore", "agent")
		eq(t, marshalJSON(it.Input.(*jsObject), ""), `{"command":"git push"}`, "input")
	})
	tw(t, f, "resolves a path-surface query via an access-path intent", func(t *testing.T) {
		defer unpublishAdapter()
		r, s := round()
		s.CheckPermission("read", sp2("/test/project/.env"), nil)
		it, ok := r.intents[0].(AccessPathAccessIntent)
		eq(t, ok, true, "access-path")
		eq(t, it.Surface, "read", "surface")
	})
	tw(t, f, "delegates getToolPermission through the resolver", func(t *testing.T) {
		defer unpublishAdapter()
		r, s := round()
		r.toolPerm = "deny"
		eq(t, s.GetToolPermission("write", sp2("Explore")), "deny", "state")
		eq(t, r.toolPermArgs, [][2]any{{"write", sp2("Explore")}}, "args")
	})

	tw(t, f, "delegates to the registry and returns its disposer", func(t *testing.T) {
		defer unpublishAdapter()
		registry := &ToolInputFormatterRegistry{}
		formatter := NewToolInputFormatter(func(*jsObject) (string, bool) { return "preview", true })
		s := &svcRegistering{formatters: registry}
		PublishPermissionsService(adapterSession, s)
		dispose, err := GetPermissionsService(adapterSession).RegisterToolInputFormatter("my-tool", formatter)
		eq(t, err, nil, "err")
		eq(t, registry.Get("my-tool") == formatter, true, "registered")
		dispose()
		eq(t, registry.Get("my-tool") == nil, true, "disposed")
	})
	tw(t, f, "throws when a formatter is already registered for the tool name", func(t *testing.T) {
		defer unpublishAdapter()
		registry := &ToolInputFormatterRegistry{}
		_, _ = registry.Register("my-tool", NewToolInputFormatter(func(*jsObject) (string, bool) { return "", false }))
		PublishPermissionsService(adapterSession, &svcRegistering{formatters: registry})
		_, err := GetPermissionsService(adapterSession).RegisterToolInputFormatter("my-tool", NewToolInputFormatter(func(*jsObject) (string, bool) { return "", true }))
		eq(t, err != nil && strings.Contains(err.Error(), "my-tool"), true, "names the tool")
	})
	// the second "delegates to the registry and returns its disposer" is the extractor's twin
	tw(t, f, "delegates to the registry and returns its disposer", func(t *testing.T) {
		defer unpublishAdapter()
		registry := &ToolAccessExtractorRegistry{}
		extractor := NewToolAccessExtractor(func(*jsObject) (string, bool) { return "/etc/hosts", true })
		PublishPermissionsService(adapterSession, &svcRegistering{extractors: registry})
		dispose, err := GetPermissionsService(adapterSession).RegisterToolAccessExtractor("ffgrep", extractor)
		eq(t, err, nil, "err")
		eq(t, registry.Resolve("ffgrep").Extractor == extractor, true, "registered")
		dispose()
		eq(t, registry.Resolve("ffgrep") == nil, true, "disposed")
	})
	tw(t, f, "throws when an extractor is already registered for the tool name", func(t *testing.T) {
		defer unpublishAdapter()
		registry := &ToolAccessExtractorRegistry{}
		_, _ = registry.Register("ffgrep", NewToolAccessExtractor(func(*jsObject) (string, bool) { return "", false }))
		PublishPermissionsService(adapterSession, &svcRegistering{extractors: registry})
		_, err := GetPermissionsService(adapterSession).RegisterToolAccessExtractor("ffgrep", NewToolAccessExtractor(func(*jsObject) (string, bool) { return "", true }))
		eq(t, err != nil && strings.Contains(err.Error(), "ffgrep"), true, "names the tool")
	})
}

// svcRegistering is a fake service whose registration methods write to a real registry.
type svcRegistering struct {
	PermissionsService
	formatters *ToolInputFormatterRegistry
	extractors *ToolAccessExtractorRegistry
}

func (s *svcRegistering) RegisterToolInputFormatter(name string, f ToolInputFormatter) (func(), error) {
	return s.formatters.Register(name, f)
}
func (s *svcRegistering) RegisterToolAccessExtractor(name string, e ToolAccessExtractor) (func(), error) {
	return s.extractors.Register(name, e)
}

func TestPermissionsServiceTwins(t *testing.T) {
	const f = "service/permissions-service"
	tw(t, f, "resolves a non-path surface through a tool intent", func(t *testing.T) {
		r := makeLocalService()
		r.service.CheckPermission("skill", sp2("my-skill"), sp2("my-agent"))
		it := r.resolver.intents[0].(ToolAccessIntent)
		eq(t, it.Surface, "skill", "surface")
		eq(t, marshalJSON(it.Input.(*jsObject), ""), `{"name":"my-skill"}`, "input")
		eq(t, *it.AgentName, "my-agent", "agent")
	})
	tw(t, f, "routes a bash query through the advisory decomposition resolver", func(t *testing.T) {
		r := makeLocalService()
		expected := PermissionCheckResult{State: "deny", ToolName: "bash"}
		var gotCommand string
		var gotAgent *string
		var gotResolver ScopedPermissionResolver
		r.service.bashAdvisory = func(c string, a *string, res ScopedPermissionResolver) PermissionCheckResult {
			gotCommand, gotAgent, gotResolver = c, a, res
			return expected
		}
		result := r.service.CheckPermission("bash", sp2("cd /repo && npm install x"), sp2("my-agent"))
		eq(t, gotCommand, "cd /repo && npm install x", "command")
		eq(t, *gotAgent, "my-agent", "agent")
		eq(t, gotResolver == ScopedPermissionResolver(r.resolver), true, "resolver")
		eq(t, result, expected, "result")
	})
	tw(t, f, "passes an empty string to the advisory resolver for a value-less bash query", func(t *testing.T) {
		r := makeLocalService()
		var gotCommand string
		var gotAgent = sp2("unset")
		r.service.bashAdvisory = func(c string, a *string, _ ScopedPermissionResolver) PermissionCheckResult {
			gotCommand, gotAgent = c, a
			return PermissionCheckResult{}
		}
		r.service.CheckPermission("bash", nil, nil)
		eq(t, gotCommand, "", "command")
		eq(t, gotAgent == nil, true, "agent")
	})
	tw(t, f, "resolves an external_directory path query through an access-path intent matching the canonical alias", func(t *testing.T) {
		old := realpathSync
		defer func() { realpathSync = old }()
		realpathSync = func(p string) (string, error) {
			if p == "/test/project/link" {
				return "/test/project/real", nil
			}
			return p, nil
		}
		r := makeLocalService()
		r.service.CheckPermission("external_directory", sp2("link"), nil)
		eq(t, len(r.resolver.intents), 1, "one resolve")
		it, ok := r.resolver.intents[0].(AccessPathAccessIntent)
		eq(t, ok, true, "access-path")
		eq(t, it.Surface, "external_directory", "surface")
		eq(t, inStrings(it.Path.MatchValues(), "/test/project/real"), true, "canonical alias")
	})
	tw(t, f, "resolves a path-bearing tool query (read) through an access-path intent", func(t *testing.T) {
		r := makeLocalService()
		r.service.CheckPermission("read", sp2("/test/project/.env"), nil)
		it, ok := r.resolver.intents[0].(AccessPathAccessIntent)
		eq(t, ok, true, "access-path")
		eq(t, it.Surface, "read", "surface")
		eq(t, it.Path.Value(), "/test/project/.env", "value")
	})
	tw(t, f, "falls back to a tool intent for a value-less path query", func(t *testing.T) {
		r := makeLocalService()
		r.service.CheckPermission("path", nil, nil)
		_, ok := r.resolver.intents[0].(ToolAccessIntent)
		eq(t, ok, true, "tool intent")
	})
	tw(t, f, "returns the result from resolver.resolve", func(t *testing.T) {
		r := makeLocalService()
		r.resolver.result = PermissionCheckResult{State: "deny", ToolName: "skill"}
		eq(t, r.service.CheckPermission("skill", sp2("my-skill"), nil), r.resolver.result, "result")
	})
	tw(t, f, "delegates to resolver.getToolPermission", func(t *testing.T) {
		r := makeLocalService()
		r.resolver.toolPerm = "deny"
		eq(t, r.service.GetToolPermission("write", sp2("my-agent")), "deny", "state")
		eq(t, r.resolver.toolPermArgs, [][2]any{{"write", sp2("my-agent")}}, "args")
	})
	tw(t, f, "omits agentName when not provided", func(t *testing.T) {
		r := makeLocalService()
		r.service.GetToolPermission("read", nil)
		eq(t, r.resolver.toolPermArgs, [][2]any{{"read", (*string)(nil)}}, "args")
	})
	tw(t, f, "delegates to resolver.isToolFullyDenied", func(t *testing.T) {
		r := makeLocalService()
		r.resolver.fullyDenied = true
		eq(t, r.service.IsToolFullyDenied("write", sp2("my-agent")), true, "denied")
		eq(t, r.resolver.fullyDenyArgs, [][2]any{{"write", sp2("my-agent")}}, "args")
	})
	// same title as the getToolPermission twin above: both are twinned by the one subtest name per file
	tw(t, f, "omits agentName when not provided", func(t *testing.T) {
		r := makeLocalService()
		r.service.IsToolFullyDenied("read", nil)
		eq(t, r.resolver.fullyDenyArgs, [][2]any{{"read", (*string)(nil)}}, "args")
	})
	tw(t, f, "delegates to formatterRegistry.register and returns the unsubscribe function", func(t *testing.T) {
		r := makeLocalService()
		formatter := NewToolInputFormatter(func(*jsObject) (string, bool) { return "", false })
		dispose, err := r.service.RegisterToolInputFormatter("my-tool", formatter)
		eq(t, err, nil, "err")
		eq(t, r.formatters.Get("my-tool") == formatter, true, "registered")
		dispose()
		eq(t, r.formatters.Get("my-tool") == nil, true, "unsubscribed")
	})
	tw(t, f, "delegates to accessExtractorRegistry.register and returns the unsubscribe function", func(t *testing.T) {
		r := makeLocalService()
		extractor := NewToolAccessExtractor(func(*jsObject) (string, bool) { return "", false })
		dispose, err := r.service.RegisterToolAccessExtractor("ffgrep", extractor)
		eq(t, err, nil, "err")
		eq(t, r.extractors.Resolve("ffgrep").Extractor == extractor, true, "registered")
		dispose()
		eq(t, r.extractors.Resolve("ffgrep") == nil, true, "unsubscribed")
	})
	tw(t, f, "answers the extractor this node's registry holds", func(t *testing.T) {
		r := makeLocalService()
		extractor := NewToolAccessExtractor(func(*jsObject) (string, bool) { return "/etc/hosts", true })
		_, _ = r.service.RegisterToolAccessExtractor("ffgrep", extractor)
		eq(t, r.service.GetToolAccessExtractor("ffgrep") == extractor, true, "same")
	})
	tw(t, f, "answers the formatter this node's registry holds", func(t *testing.T) {
		r := makeLocalService()
		formatter := NewToolInputFormatter(func(*jsObject) (string, bool) { return "preview", true })
		_, _ = r.service.RegisterToolInputFormatter("my-tool", formatter)
		eq(t, r.service.GetToolInputFormatter("my-tool") == formatter, true, "same")
	})
	tw(t, f, "answers undefined for a tool with no registration", func(t *testing.T) {
		r := makeLocalService()
		eq(t, r.service.GetToolAccessExtractor("unregistered") == nil, true, "extractor")
		eq(t, r.service.GetToolInputFormatter("unregistered") == nil, true, "formatter")
	})
	tw(t, f, "stops answering once the registration is disposed", func(t *testing.T) {
		r := makeLocalService()
		dispose, _ := r.service.RegisterToolAccessExtractor("ffgrep", NewToolAccessExtractor(func(*jsObject) (string, bool) { return "/etc/hosts", true }))
		dispose()
		eq(t, r.service.GetToolAccessExtractor("ffgrep") == nil, true, "gone")
	})
	tw(t, f, "unwraps the resolution rather than exposing its origin", func(t *testing.T) {
		extractor := NewToolAccessExtractor(func(*jsObject) (string, bool) { return "", false })
		r := makeLocalService()
		r.service.accessExtractors = inheritedExtractors{extractor}
		// The cross-extension surface answers the capability, not which node supplied it.
		eq(t, r.service.GetToolAccessExtractor("ffgrep") == extractor, true, "same")
	})
	tw(t, f, "delegates to authorizerRegistry.register and returns the unsubscribe function", func(t *testing.T) {
		r := makeLocalService()
		authorize := func(*PromptPermissionDetails, PermissionQuery, AuthorizerLog) (AuthorizerVerdict, error) {
			return AuthorizerVerdict{Kind: "defer"}, nil
		}
		dispose, err := r.service.RegisterAuthorizer("model-judge", authorize)
		eq(t, err, nil, "err")
		eq(t, r.authorizer.Get("model-judge") != nil, true, "registered")
		dispose()
		eq(t, r.authorizer.Get("model-judge") == nil, true, "unsubscribed")
	})
}

// inheritedExtractors answers every tool with an extractor whose origin is inherited.
type inheritedExtractors struct{ e ToolAccessExtractor }

func (i inheritedExtractors) Register(string, ToolAccessExtractor) (func(), error) {
	return nil, errors.New("not used")
}
func (i inheritedExtractors) Resolve(string) *ResolvedToolAccessExtractor {
	return &ResolvedToolAccessExtractor{Extractor: i.e, Origin: RegistrationInherited}
}

// ---- service lifecycle ----

type readyBus struct {
	emitted         []PermissionsReadyEvent
	publishedAtEmit []bool
	probe           func() bool
	failNext        bool
}

func (b *readyBus) Emit(channel string, data any) error {
	if channel != PermissionsReadyChannel {
		return nil
	}
	b.emitted = append(b.emitted, data.(PermissionsReadyEvent))
	if b.probe != nil {
		b.publishedAtEmit = append(b.publishedAtEmit, b.probe())
	}
	return nil
}

type fakeRole struct{ local bool }

func (r *fakeRole) AdjudicatesLocally() bool { return r.local }

type idManager struct {
	id   string
	fail bool
}

func (m idManager) GetSessionID() (string, error) {
	if m.fail {
		return "", errors.New("session id unavailable")
	}
	return m.id, nil
}
func (idManager) GetSessionDir() (string, error)          { return "/sessions/test", nil }
func (idManager) GetEntries() ([]SessionEntryView, error) { return nil, nil }

func idCtx(id string, fail ...bool) SessionContext {
	ctx, _ := makeCtx(func(c *SessionContext) { c.SessionManager = idManager{id: id, fail: len(fail) > 0 && fail[0]} })
	return ctx
}

type lifecycleSvc struct {
	service *svcFake
	role    *fakeRole
	bus     *readyBus
	subs    []func()
	l       *PermissionServiceLifecycle
}

func makeServiceLifecycle(subs ...func()) *lifecycleSvc {
	s := &lifecycleSvc{service: makeFakeService(), role: &fakeRole{local: true}, bus: &readyBus{}, subs: subs}
	s.l = NewPermissionServiceLifecycle(s.service, s.role, s.bus, subs)
	return s
}

func TestServiceLifecycleTwins(t *testing.T) {
	const f = "service/service-lifecycle"
	clear := func(ids ...string) {
		for _, id := range ids {
			if c := GetPermissionsService(id); c != nil {
				UnpublishPermissionsService(id, c)
			}
		}
	}
	tw(t, f, "PermissionServiceLifecycle satisfies ServiceLifecycle", func(t *testing.T) {
		var _ ServiceLifecycle = makeServiceLifecycle().l
	})
	tw(t, f, "PermissionServiceLifecycle satisfies ReadyAnnouncer", func(t *testing.T) {
		var _ ReadyAnnouncer = makeServiceLifecycle().l
	})
	tw(t, f, "emits ready after publishing the service", func(t *testing.T) {
		defer clear("node-session")
		s := makeServiceLifecycle()
		s.bus.probe = func() bool { return GetPermissionsService("node-session") == PermissionsService(s.service) }
		s.l.Activate(idCtx("node-session"))
		eq(t, s.bus.publishedAtEmit, []bool{true}, "published when ready was emitted")
	})
	tw(t, f, "publishes the service under this node's own session id", func(t *testing.T) {
		defer clear("node-session")
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("node-session"))
		eq(t, GetPermissionsService("node-session") == PermissionsService(s.service), true, "published")
	})
	tw(t, f, "skips the keyed publication when the host exposes no session id", func(t *testing.T) {
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("", true))
		_, ok := s.l.CurrentSessionID()
		eq(t, ok, false, "nothing published")
	})
	tw(t, f, "emits the node's session id and chain role on ready", func(t *testing.T) {
		defer clear("node-session")
		s := makeServiceLifecycle()
		s.role.local = false
		s.l.Activate(idCtx("node-session"))
		eq(t, s.bus.emitted, []PermissionsReadyEvent{{SessionID: sp2("node-session"), AdjudicatesLocally: false}}, "ready")
	})
	tw(t, f, "emits a null session id when the host exposes none", func(t *testing.T) {
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("", true))
		eq(t, s.bus.emitted, []PermissionsReadyEvent{{SessionID: nil, AdjudicatesLocally: true}}, "ready")
	})
	tw(t, f, "emits the node's ready facts", func(t *testing.T) {
		s := makeServiceLifecycle()
		s.role.local = false
		s.l.AnnounceReady(idCtx("node-session"))
		eq(t, s.bus.emitted, []PermissionsReadyEvent{{SessionID: sp2("node-session"), AdjudicatesLocally: false}}, "ready")
	})
	tw(t, f, "announces only once per session", func(t *testing.T) {
		defer clear("test-session")
		s := makeServiceLifecycle()
		ctx, _ := makeCtx()
		s.l.Activate(ctx)
		s.bus.emitted = nil
		s.l.AnnounceReady(ctx)
		s.l.AnnounceReady(ctx)
		s.l.AnnounceReady(ctx)
		eq(t, len(s.bus.emitted), 1, "once")
	})
	tw(t, f, "announces again after a further activate re-arms the latch", func(t *testing.T) {
		defer clear("test-session")
		s := makeServiceLifecycle()
		ctx, _ := makeCtx()
		s.l.Activate(ctx)
		s.l.AnnounceReady(ctx)
		s.l.Activate(ctx)
		s.bus.emitted = nil
		s.l.AnnounceReady(ctx)
		eq(t, len(s.bus.emitted), 1, "once more")
	})
	tw(t, f, "announces even when no activate preceded it", func(t *testing.T) {
		s := makeServiceLifecycle()
		ctx, _ := makeCtx()
		s.l.AnnounceReady(ctx)
		eq(t, s.bus.emitted, []PermissionsReadyEvent{{SessionID: sp2("test-session"), AdjudicatesLocally: true}}, "ready")
	})
	tw(t, f, "recomputes the facts from the ctx it is handed", func(t *testing.T) {
		defer clear("activated-session")
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("activated-session"))
		s.bus.emitted = nil
		s.l.AnnounceReady(idCtx("latched-session"))
		eq(t, s.bus.emitted, []PermissionsReadyEvent{{SessionID: sp2("latched-session"), AdjudicatesLocally: true}}, "ready")
	})
	tw(t, f, "calls each subscription unsubscribe function", func(t *testing.T) {
		calls := 0
		s := makeServiceLifecycle(func() { calls++ }, func() { calls++ }, func() { calls++ })
		s.l.Teardown()
		eq(t, calls, 3, "unsubscribed")
	})
	tw(t, f, "unpublishes the service after running subscriptions", func(t *testing.T) {
		defer clear("node-session")
		var stillPublished bool
		var s *lifecycleSvc
		s = makeServiceLifecycle(func() { stillPublished = GetPermissionsService("node-session") != nil })
		s.l.Activate(idCtx("node-session"))
		s.l.Teardown()
		eq(t, stillPublished, true, "the subscription ran while the service was still published")
		eq(t, GetPermissionsService("node-session") == nil, true, "then unpublished")
	})
	tw(t, f, "removes the keyed entry it published", func(t *testing.T) {
		defer clear("node-session")
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("node-session"))
		s.l.Teardown()
		eq(t, GetPermissionsService("node-session") == nil, true, "removed")
	})
	tw(t, f, "removes the keyed entry of the session it last activated for", func(t *testing.T) {
		defer clear("first-session", "second-session")
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("first-session"))
		s.l.Activate(idCtx("second-session"))
		s.l.Teardown()
		eq(t, GetPermissionsService("second-session") == nil, true, "second removed")
		eq(t, GetPermissionsService("first-session") == PermissionsService(s.service), true, "first is not this teardown's")
	})
	tw(t, f, "removes nothing keyed when it never published", func(t *testing.T) {
		defer clear("bystander")
		other := makeFakeService()
		PublishPermissionsService("bystander", other)
		makeServiceLifecycle().l.Teardown()
		eq(t, GetPermissionsService("bystander") == PermissionsService(other), true, "untouched")
	})
	tw(t, f, "works with no subscriptions", func(t *testing.T) {
		defer clear("node-session")
		s := makeServiceLifecycle()
		s.l.Activate(idCtx("node-session"))
		s.l.Teardown()
		eq(t, GetPermissionsService("node-session") == nil, true, "removed")
	})
}

type failingBus struct{}

func (failingBus) Emit(string, any) error { panic("listener failed") }

func TestPermissionEventsReadyTwins(t *testing.T) {
	const f = "service/permission-events"
	tw(t, f, "channel names have the correct values", func(t *testing.T) {
		eq(t, PermissionsReadyChannel, "permissions:ready", "ready")
		eq(t, PermissionsUIPromptChannel, "permissions:ui_prompt", "ui_prompt")
		eq(t, PermissionsDecisionChannel, "permissions:decision", "decision")
	})
	tw(t, f, "emits the emitting node's facts on the permissions:ready channel", func(t *testing.T) {
		bus := &readyBus{}
		EmitReadyEvent(bus, PermissionsReadyEvent{SessionID: sp2("node-1"), AdjudicatesLocally: true})
		eq(t, bus.emitted, []PermissionsReadyEvent{{SessionID: sp2("node-1"), AdjudicatesLocally: true}}, "emitted")
	})
	tw(t, f, "emits a relaying node's facts unchanged", func(t *testing.T) {
		bus := &readyBus{}
		EmitReadyEvent(bus, PermissionsReadyEvent{SessionID: sp2("node-2"), AdjudicatesLocally: false})
		eq(t, bus.emitted, []PermissionsReadyEvent{{SessionID: sp2("node-2"), AdjudicatesLocally: false}}, "emitted")
	})
	tw(t, f, "carries no protocolVersion (the broadcast contract is types + semver)", func(t *testing.T) {
		eq(t, marshalJSON(ToRecord(map[string]any{"adjudicatesLocally": true, "sessionId": "n"}), ""), `{"adjudicatesLocally":true,"sessionId":"n"}`, "shape")
		b, _ := jsonMarshalForTest(PermissionsReadyEvent{SessionID: sp2("n"), AdjudicatesLocally: true})
		eq(t, string(b), `{"sessionId":"n","adjudicatesLocally":true}`, "wire shape: the two facts and nothing else")
	})
	tw(t, f, "swallows event bus errors because broadcasts are best-effort", func(t *testing.T) {
		EmitReadyEvent(failingBus{}, PermissionsReadyEvent{AdjudicatesLocally: true})
		EmitDecisionEvent(failingBus{}, PermissionDecisionEvent{})
	})
}

func jsonMarshalForTest(v any) ([]byte, error) { return json.Marshal(v) }
