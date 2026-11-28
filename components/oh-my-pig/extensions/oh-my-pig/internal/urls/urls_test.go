package urls

import (
	"errors"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- Fixture handlers for router contract tests ---

type fixtureHandler struct {
	scheme string
	spec   Spec
	res    func(c Context, u URL) (*Resource, error)
}

func (f fixtureHandler) Scheme() string { return f.scheme }
func (f fixtureHandler) Spec() Spec     { return f.spec }
func (f fixtureHandler) Resolve(c Context, u URL) (*Resource, error) {
	if f.res != nil {
		return f.res(c, u)
	}
	return nil, errors.New("fixture:// never resolves")
}

type fixtureWriter struct {
	fixtureHandler
}

func (fixtureWriter) Write(_ Context, _ URL, _ string) (*WriteResult, error) {
	return nil, nil
}

type fixtureLocator struct {
	fixtureHandler
	loc func(c Context, u URL, opts LocateOptions) (string, bool, error)
}

func (f fixtureLocator) Locate(c Context, u URL, opts LocateOptions) (string, bool, error) {
	if f.loc != nil {
		return f.loc(c, u, opts)
	}
	return "/tmp/fixture", true, nil
}

type fixtureLocatorWriter struct {
	fixtureLocator
}

func (fixtureLocatorWriter) Write(_ Context, _ URL, _ string) (*WriteResult, error) {
	return nil, nil
}

type fixtureEnumerator struct {
	fixtureHandler
	leaves []Leaf
}

func (f fixtureEnumerator) Enumerate(_ Context, _ URL) ([]Leaf, error) {
	return f.leaves, nil
}

type fixturePromptDoc struct {
	fixtureHandler
	doc string
	ref string
}

func (f fixturePromptDoc) PromptDoc(_ SchemeHost) string { return f.doc }
func (f fixturePromptDoc) PromptReference() string       { return f.ref }

func resetAll(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ResetForTests()
		SetLocalRootOverrideForTests(nil)
		SetLocalRootProvider(nil)
		ResetSkillsForTests()
		ResetRulesForTests()
		ResetOutPolicyForTests()
		ResetHelpTopicsForTests()
		ResetAttachmentsForTests()
		ResetConflictsForTests()
		SetResourceFallbackScheme("mcp")
	})
	ResetForTests()
	SetLocalRootOverrideForTests(nil)
	SetLocalRootProvider(nil)
	ResetSkillsForTests()
	ResetRulesForTests()
	ResetOutPolicyForTests()
	ResetHelpTopicsForTests()
	ResetAttachmentsForTests()
	ResetConflictsForTests()
	SetResourceFallbackScheme("mcp")
}

// =============================================================================
// parseInternalUrl tests (test/marketplace/parse-internal-url.test.ts)
// =============================================================================

// upstream: test/marketplace/parse-internal-url.test.ts: "parses a simple skill:// URL"
func TestParseInternalURL_SimpleSkill(t *testing.T) {
	u, err := Parse("skill://brainstorming")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "brainstorming" || u.Scheme != "skill" {
		t.Fatalf("got %+v", u)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "parses skill:// with path"
func TestParseInternalURL_SkillWithPath(t *testing.T) {
	u, err := Parse("skill://my-skill/subdir/file.md")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "my-skill" || u.Path != "/subdir/file.md" {
		t.Fatalf("got %+v", u)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "preserves query parameters when URL parses normally"
func TestParseInternalURL_QueryParameters(t *testing.T) {
	u, err := Parse("agent://output_id?q=foo.bar")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "output_id" || u.Query.Get("q") != "foo.bar" {
		t.Fatalf("got %+v", u)
	}
	// Semicolon inside query value must not drop the parameter (URLSearchParams parity).
	u2, err := Parse("attachment://1?q=a;b&k=v")
	if err != nil {
		t.Fatalf("Parse semicolon query failed: %v", err)
	}
	if u2.Query.Get("q") != "a;b" || u2.Query.Get("k") != "v" {
		t.Fatalf("semicolon query lost params: %+v", u2.Query)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "parses skill://plugin:name (colon as namespace separator)"
func TestParseInternalURL_ColonInHost(t *testing.T) {
	u, err := Parse("skill://superpowers:brainstorming")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "superpowers:brainstorming" || u.Scheme != "skill" || u.Path != "" {
		t.Fatalf("got %+v", u)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "parses skill://plugin:name/path"
func TestParseInternalURL_ColonInHostWithPath(t *testing.T) {
	u, err := Parse("skill://superpowers:brainstorming/subdir/file.md")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "superpowers:brainstorming" || u.Path != "/subdir/file.md" {
		t.Fatalf("got %+v", u)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "parses namespaced URL with path after multiple colons"
func TestParseInternalURL_MultipleColons(t *testing.T) {
	u, err := Parse("skill://superpowers:brainstorming:1-5/extra")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "superpowers:brainstorming:1-5" || u.Path != "/extra" {
		t.Fatalf("got %+v", u)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "provides empty searchParams for fallback-parsed URLs"
func TestParseInternalURL_EmptyQueryForFallback(t *testing.T) {
	u, err := Parse("skill://superpowers:brainstorming")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Query == nil || u.Query.Get("q") != "" {
		t.Fatalf("expected non-nil empty Query, got %+v", u.Query)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "decodes %3A with path"
func TestParseInternalURL_PercentEncodedHost(t *testing.T) {
	u, err := Parse("skill://superpowers%3Abrainstorming/file.md")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u.Host != "superpowers:brainstorming" || u.Path != "/file.md" {
		t.Fatalf("got %+v", u)
	}
	// upstream: test/marketplace/parse-internal-url.test.ts: "decodes multiple %3A segments"
	u2, err := Parse("skill://a%3Ab%3Ac")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if u2.Host != "a:b:c" {
		t.Fatalf("got host=%q", u2.Host)
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "throws on completely invalid input", "throws on empty string"
func TestParseInternalURL_InvalidAndEmpty(t *testing.T) {
	for _, bad := range []string{"not-a-url", ""} {
		if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "Invalid URL") {
			t.Errorf("Parse(%q) = %v, want Invalid URL error", bad, err)
		}
	}
}

// upstream: test/marketplace/parse-internal-url.test.ts: "parses empty host", "handles host with only valid port (new URL succeeds)", "handles host with hyphens and dots", "handles uppercase scheme", "rawHost does not include fragment"
func TestParseInternalURL_EdgeCases(t *testing.T) {
	u, err := Parse("skill:///path/to/file")
	if err != nil || u.Host != "" || u.Path != "/path/to/file" {
		t.Fatalf("empty host: %+v %v", u, err)
	}
	u, err = Parse("skill://host:8080")
	if err != nil || u.Host != "host:8080" {
		t.Fatalf("host:8080: %+v %v", u, err)
	}
	u, err = Parse("skill://my-plugin.v2")
	if err != nil || u.Host != "my-plugin.v2" {
		t.Fatalf("hyphens/dots: %+v %v", u, err)
	}
	u, err = Parse("SKILL://my-skill")
	if err != nil || u.Host != "my-skill" || u.Scheme != "skill" {
		t.Fatalf("uppercase scheme: %+v %v", u, err)
	}
	u, err = Parse("skill://name#frag")
	if err != nil || u.Host != "name" {
		t.Fatalf("fragment: %+v %v", u, err)
	}
	// Preserves rawHost case and traversal in rawPathname.
	u, err = Parse("local://FooBar/../secret.txt")
	if err != nil || u.Host != "FooBar" || u.Path != "/../secret.txt" {
		t.Fatalf("raw preservation: %+v %v", u, err)
	}
}

// upstream: src/internal-urls/parse.ts extractUriScheme guards
func TestExtractScheme(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"local://notes.md", "local"},
		{"SKILL://foo", "skill"},
		{"urn:example:document", "urn"},
		{"custom:item", "custom"},
		{"C:\\Users\\me\\file.txt", ""},
		{"C:/Users/me/file.txt", ""},
		{"C:foo", ""},
		{"foo.ts:50", ""},
		{"Makefile:12", ""},
		{"README:raw:1-20", ""},
		{"Dockerfile:198-220;a.yml:1-60", ""},
		{"plain/path.go", ""},
	}
	for _, tc := range cases {
		if got := ExtractScheme(tc.in); got != tc.want {
			t.Errorf("ExtractScheme(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// =============================================================================
// Router spec contract tests (test/internal-urls/router-spec-contracts.test.ts)
// =============================================================================

// upstream: test/internal-urls/router-spec-contracts.test.ts: "InternalUrlRouter.register invariants"
func TestRouterRegisterInvariants(t *testing.T) {
	resetAll(t)

	fileWrite := &WritePolicy{Via: "file", Payload: "text", Scope: "workspace"}

	if err := Register(fixtureWriter{fixtureHandler{
		scheme: "fixture",
		spec:   Spec{Backing: BackingVirtual, Selectors: SelectorsLines},
	}}); err == nil || !strings.Contains(err.Error(), "fixture:// handler has write() but spec.write.via is absent") {
		t.Fatalf("expected error for write() without spec.write, got %v", err)
	}

	if err := Register(fixtureLocatorWriter{fixtureLocator{
		fixtureHandler: fixtureHandler{
			scheme: "fixture",
			spec:   Spec{Backing: BackingFile, Selectors: SelectorsLines, Write: fileWrite},
		},
	}}); err == nil || !strings.Contains(err.Error(), `fixture:// handler has write() but spec.write.via is "file"`) {
		t.Fatalf("expected error for write() with via=file, got %v", err)
	}

	if err := Register(fixtureHandler{
		scheme: "fixture",
		spec:   Spec{Backing: BackingVirtual, Selectors: SelectorsLines, Write: &WritePolicy{Via: "handler", Payload: "text", Scope: "workspace"}},
	}); err == nil || !strings.Contains(err.Error(), `fixture:// spec.write.via is "handler" but the handler lacks write()`) {
		t.Fatalf("expected error for via=handler without write(), got %v", err)
	}

	if err := Register(fixtureLocator{
		fixtureHandler: fixtureHandler{scheme: "fixture", spec: Spec{Backing: BackingVirtual, Selectors: SelectorsLines, Write: fileWrite}},
	}); err == nil || !strings.Contains(err.Error(), `requires backing "file", not "virtual"`) {
		t.Fatalf("expected virtual backing error, got %v", err)
	}
	if err := Register(fixtureHandler{
		scheme: "fixture",
		spec:   Spec{Backing: BackingFile, Selectors: SelectorsLines, Write: fileWrite},
	}); err == nil || !strings.Contains(err.Error(), "requires a locate() hook") {
		t.Fatalf("expected locate hook error, got %v", err)
	}
	if err := Register(fixtureLocator{
		fixtureHandler: fixtureHandler{scheme: "fixture", spec: Spec{Backing: BackingFile, Selectors: SelectorsLines, Immutable: true, Write: fileWrite}},
	}); err == nil || !strings.Contains(err.Error(), "contradicts spec.immutable") {
		t.Fatalf("expected immutable contradiction error, got %v", err)
	}
	if CanHandle("fixture://x") {
		t.Fatalf("rejected handler should not be registered")
	}

	sandboxWrite := &WritePolicy{Via: "file", Payload: "text", Scope: "sandbox"}
	if err := Register(fixtureLocator{
		fixtureHandler: fixtureHandler{scheme: "fixture", spec: Spec{Backing: BackingFile, Selectors: SelectorsLines, Write: sandboxWrite}},
	}); err == nil || !strings.Contains(err.Error(), "requires spec.linkable and a locateSync()") {
		t.Fatalf("expected sandbox linkable error, got %v", err)
	}

	RegisterExtension(nil)
	if err := Register(fixtureLocator{
		fixtureHandler: fixtureHandler{scheme: "fixture", spec: Spec{Backing: BackingFile, Selectors: SelectorsLines, Write: fileWrite}},
	}); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if !CanHandle("fixture://x") || !FileWritable("fixture://x") {
		t.Fatalf("expected fixture://x to be handled and fileWritable")
	}
	if IsBuiltin("fixture") || !IsBuiltin("LOCAL") {
		t.Fatalf("unexpected IsBuiltin results: fixture=%v LOCAL=%v", IsBuiltin("fixture"), IsBuiltin("LOCAL"))
	}
}

// upstream: test/internal-urls/router-spec-contracts.test.ts: "InternalUrlRouter URL shape"
func TestRouterNormalizeAndGlob(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)
	_ = Register(fixtureHandler{scheme: "vault", spec: Spec{Backing: BackingFile, Selectors: SelectorsLines}})
	_ = Register(fixtureHandler{scheme: "issue", spec: Spec{Backing: BackingRemote, Selectors: SelectorsLines}})
	_ = Register(fixtureHandler{scheme: "memory", spec: Spec{Backing: BackingFile, Selectors: SelectorsLines}})
	_ = Register(fixtureHandler{scheme: "ssh", spec: Spec{Backing: BackingRemote, Selectors: SelectorsLines, PortAuthority: true}})

	if got := Normalize("local:/notes.md"); got != "local://notes.md" {
		t.Errorf("Normalize(local:/notes.md) = %q", got)
	}
	if got := Normalize("vault:/Work/a.md"); got != "vault:/Work/a.md" {
		t.Errorf("Normalize(vault:/Work/a.md) = %q", got)
	}

	normCases := []struct{ in, want string }{
		{"/home/me/repo/local://notes.md", "local://notes.md"},
		{"notes/local://a/b.md:10-20", "local://a/b.md:10-20"},
		{"/repo/local://a/b/local://notes.md", "local://a/b/local://notes.md"},
		{`C:\repo\local://rules.md`, "local://rules.md"},
		{"C:/repo/local://rules.md", "local://rules.md"},
		{"/repo/local://", "local://"},
		{"/home/me/repo/local:/notes.md", "/home/me/repo/local:/notes.md"},
		{"https://host/local://x", "https://host/local://x"},
		{"/repo/skill://x", "/repo/skill://x"},
	}
	for _, tc := range normCases {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if !CanHandle("/repo/local://") {
		t.Errorf("expected CanHandle(/repo/local://) = true")
	}

	nonGlobs := []string{
		"vault://W?op=search&q=[x]",
		"vault://W?op=search&q=a/*.md",
		"issue://?search=fix*",
		"issue://owner/repo?state=closed",
		"vault://Work/a.md?op=backlinks",
		"ssh://host*/etc/hosts",
	}
	for _, u := range nonGlobs {
		if IsGlob(u) {
			t.Errorf("IsGlob(%q) = true, want false", u)
		}
	}
	globs := []string{
		"local://*.md",
		"local:/drafts/*.md",
		"memory://root/skills/?.md",
		"vault://Work/*.md?op=x",
		"ssh://host/etc/*.conf",
	}
	for _, u := range globs {
		if !IsGlob(u) {
			t.Errorf("IsGlob(%q) = false, want true", u)
		}
	}
}

func TestRouterSelectorsAndPeelWrite(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)
	_ = Register(fixtureHandler{scheme: "ssh", spec: Spec{Backing: BackingRemote, Selectors: SelectorsLines, PortAuthority: true}})

	splitCases := []struct {
		in       string
		wantPath string
		wantSel  string
	}{
		{"local://3.log:raw:1-50", "local://3.log", "raw:1-50"},
		{"local:/3.log:1-30,:-30", "local://3.log", "1-30,-30"},
		{"local://3.log:1-3:raw,9:raw", "local://3.log", "1-3,9:raw"},
		{"local://3.log:raw:-100-5", "local://3.log", "raw:-100-5"},
		{"ssh://host:2222", "ssh://host:2222", ""},
		{"ssh://host:2222/etc/hosts:1-10", "ssh://host:2222/etc/hosts", "1-10"},
		{"mcp://server:1234/item:1-5", "mcp://server:1234/item:1-5", ""},
		{"out://reply:1-5", "out://reply:1-5", ""},
		{"plain.txt:1-5", "plain.txt:1-5", ""},
	}
	for _, tc := range splitCases {
		p, sel := Split(tc.in)
		if p != tc.wantPath || sel != tc.wantSel {
			t.Errorf("Split(%q) = (%q, %q), want (%q, %q)", tc.in, p, sel, tc.wantPath, tc.wantSel)
		}
	}

	if p, err := PeelWriteSelector("local:/notes.md:raw", "write"); err != nil || p != "local://notes.md" {
		t.Errorf("PeelWriteSelector(:raw) = (%q, %v)", p, err)
	}
	if _, err := PeelWriteSelector("local://notes.md:1-10", "edit"); err == nil || !strings.Contains(err.Error(), "addresses a whole file") {
		t.Errorf("expected line range error in PeelWriteSelector, got %v", err)
	}
	_ = Register(fixtureHandler{scheme: "dev", spec: Spec{Backing: BackingDevice, Selectors: SelectorsLines}})
	if _, err := PeelWriteSelector("dev://1:raw", "write"); err == nil || !strings.Contains(err.Error(), "device URL") {
		t.Errorf("expected device URL error in PeelWriteSelector, got %v", err)
	}
}

// upstream: src/internal-urls/router.ts:110-130 (Describe and Reference follow omp's canonical scheme order)
func TestRouterDescribeAndReferenceSchemeOrder(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)
	// Register other lanes' schemes AFTER builtins — Describe/Reference must still
	// order agent/history between out and local, and proc/issue after local.
	_ = Register(fixturePromptDoc{fixtureHandler: fixtureHandler{scheme: "agent", spec: Spec{Backing: BackingFile, Selectors: SelectorsLines}}, doc: "agent://doc"})
	_ = Register(fixturePromptDoc{fixtureHandler: fixtureHandler{scheme: "history", spec: Spec{Backing: BackingVirtual, Selectors: SelectorsLines}}, doc: "history://doc"})
	_ = Register(fixturePromptDoc{fixtureHandler: fixtureHandler{scheme: "proc", spec: Spec{Backing: BackingDevice, Selectors: SelectorsNone}}, doc: "proc://doc", ref: "proc://ref"})
	_ = Register(fixturePromptDoc{fixtureHandler: fixtureHandler{scheme: "issue", spec: Spec{Backing: BackingRemote, Selectors: SelectorsLines}}, doc: "issue://doc", ref: "issue://ref"})

	lines := Describe(SchemeHost{SkillURIAccess: true, RuleCount: 1, OutReplies: true})
	var prefixes []string
	for _, l := range lines {
		scheme, _, _ := strings.Cut(l, "://")
		prefixes = append(prefixes, scheme)
	}
	want := []string{"skill", "rule", "out", "agent", "history", "local", "proc", "issue", "omp"}
	if strings.Join(prefixes, ",") != strings.Join(want, ",") {
		t.Fatalf("Describe order = %v, want %v", prefixes, want)
	}

	refs := Reference()
	var refSchemes []string
	for _, r := range refs {
		scheme, _, _ := strings.Cut(r, "://")
		refSchemes = append(refSchemes, scheme)
	}
	wantRefs := []string{"proc", "issue", "help"}
	if strings.Join(refSchemes, ",") != strings.Join(wantRefs, ",") {
		t.Fatalf("Reference order = %v, want %v", refSchemes, wantRefs)
	}
}

// upstream: test/internal-urls/router-spec-contracts.test.ts: "surfaces the handler's not-found detail for an unknown agent output id"
func TestRouterRequireLocalAndFallbackAndEnumerate(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	_ = Register(fixtureLocator{
		fixtureHandler: fixtureHandler{
			scheme: "agent",
			spec:   Spec{Backing: BackingFile, Selectors: SelectorsLines, Immutable: true},
			res: func(_ Context, u URL) (*Resource, error) {
				return nil, errors.New("Not found: " + u.Host)
			},
		},
		loc: func(_ Context, _ URL, _ LocateOptions) (string, bool, error) {
			return "", false, nil
		},
	})
	if _, err := RequireLocal(Context{}, "agent://reviewer_9", "find", LocateOptions{}); err == nil ||
		!strings.Contains(err.Error(), "Cannot find agent://reviewer_9: Not found: reviewer_9") {
		t.Fatalf("unexpected RequireLocal error: %v", err)
	}

	if !CanResolve("urn:example:document") || !CanResolve("custom://item") {
		t.Errorf("expected CanResolve true for custom/opaque schemes via mcp fallback")
	}
	if CanResolve("https://example.com") || CanResolve("file:///tmp/x") {
		t.Errorf("expected CanResolve false for https/file")
	}

	_ = Register(fixtureEnumerator{
		fixtureHandler: fixtureHandler{scheme: "docs", spec: Spec{Backing: BackingVirtual, Selectors: SelectorsLines, Immutable: true}},
		leaves:         []Leaf{{URL: "docs://a.md", Content: "alpha"}},
	})
	leaves, ok, err := Enumerate(Context{}, "docs://")
	if err != nil || !ok || len(leaves) != 1 || leaves[0].URL != "docs://a.md" {
		t.Fatalf("unexpected Enumerate result: %+v ok=%v err=%v", leaves, ok, err)
	}
}

// =============================================================================
// Local handler tests (test/internal-urls/local-protocol.test.ts)
// =============================================================================

// upstream: test/internal-urls/local-protocol.test.ts: "lists files at local://", "reads a local file from session local root", "locates files and the root directory, and nothing for missing entries"
func TestLocalHandler_ListReadLocate(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	tmp := t.TempDir()
	localDir := filepath.Join(tmp, "local")
	SetLocalRootOverrideForTests(func(_ sdk.Context) (string, error) {
		return localDir, nil
	})

	if err := os.MkdirAll(filepath.Join(localDir, "subtasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "handoff.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "subtasks", "trace.txt"), []byte("trace"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Resolve(Context{}, "local://")
	if err != nil || res.ContentType != "text/markdown" || !strings.Contains(res.Content, "handoff.json") {
		t.Fatalf("Resolve(local://) = (%+v, %v)", res, err)
	}
	res, err = Resolve(Context{}, "local://subtasks/trace.txt")
	if err != nil || res.Content != "trace" || res.ContentType != "text/plain" {
		t.Fatalf("Resolve(trace.txt) = (%+v, %v)", res, err)
	}

	realLocal, _ := filepath.EvalSymlinks(localDir)
	p, ok, err := Locate(Context{}, "local://handoff.json", LocateOptions{})
	if err != nil || !ok || p != filepath.Join(realLocal, "handoff.json") {
		t.Fatalf("Locate(handoff.json) = (%q, %v, %v)", p, ok, err)
	}
	p, ok, err = Locate(Context{}, "local://", LocateOptions{})
	if err != nil || !ok || p != realLocal {
		t.Fatalf("Locate(local://) = (%q, %v, %v)", p, ok, err)
	}
	if _, ok, err := Locate(Context{}, "local://missing.json", LocateOptions{}); err != nil || ok {
		t.Fatalf("expected missing entry not found, got ok=%v err=%v", ok, err)
	}

	// Binary guard and CopyLocalArtifacts
	if err := os.WriteFile(filepath.Join(localDir, "img.png"), []byte{0x89, 'P', 'N', 'G'}, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = Resolve(Context{}, "local://img.png")
	if err != nil || !strings.Contains(res.Content, "Cannot read binary local:// file") {
		t.Fatalf("expected binary notice, got (%+v, %v)", res, err)
	}
	dst := filepath.Join(tmp, "copied")
	if err := CopyLocalArtifacts(localDir, dst); err != nil {
		t.Fatalf("CopyLocalArtifacts failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "subtasks", "trace.txt")); err != nil {
		t.Fatalf("expected copied file: %v", err)
	}
	roots := SandboxRoots(Context{})
	if len(roots) != 1 || roots[0] != localDir {
		t.Fatalf("SandboxRoots = %v, want [%s]", roots, localDir)
	}
}

// upstream: test/internal-urls/local-protocol.test.ts: "blocks path traversal attempts", "uses session id fallback root when artifacts dir is unavailable", "keeps the fallback root inside its own session dir under omp-local", "uses a stable short temp root for long Windows artifact paths"
func TestLocalHandler_TraversalAndRootDerivation(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	tmp := t.TempDir()
	SetLocalRootOverrideForTests(func(_ sdk.Context) (string, error) {
		return filepath.Join(tmp, "local"), nil
	})

	for _, bad := range []string{"local://../secret.txt", "local://%2E%2E/secret.txt", "local://..hidden", "local://d/..x"} {
		if _, err := Resolve(Context{}, bad); err == nil || !strings.Contains(err.Error(), "Path traversal (..) is not allowed in local:// URLs") {
			t.Errorf("Resolve(%q) = %v, expected traversal error", bad, err)
		}
	}

	for _, sid := range []string{"..", "."} {
		got := resolveLocalRootPath("", sid, "linux")
		want := filepath.Join(os.TempDir(), "omp-local", "session")
		if got != want {
			t.Errorf("resolveLocalRootPath('', %q) = %q, want %q", sid, got, want)
		}
	}

	longDir := filepath.Join(os.TempDir(), strings.Repeat("a", 220), "artifacts")
	expectedShort := filepath.Join(os.TempDir(), "omp-local", "session_long")
	if got := resolveLocalRootPath(longDir, "session:long", "windows"); got != expectedShort {
		t.Errorf("windows short root = %q, want %q", got, expectedShort)
	}

	// Real SDK context root derivation (.jsonl vs non-.jsonl session file).
	SetLocalRootOverrideForTests(nil)
	sessionFile := filepath.Join(tmp, "sess-123.jsonl")
	ext := sdk.New("urls-local-root-test")
	var gotRoot string
	var gotErr error
	ext.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		gotRoot, gotErr = LocalRoot(ctx)
		return nil, nil
	})
	host := startFakeHost(t, ext, fakeHostOpts{SessionID: "sess-123", SessionFile: sessionFile})
	host.fireEvent(sdk.EventAgentStart, map[string]any{})
	wantRoot := filepath.Join(tmp, "sess-123", "local")
	if gotErr != nil || gotRoot != wantRoot {
		t.Fatalf("LocalRoot(.jsonl) = (%q, %v), want %q", gotRoot, gotErr, wantRoot)
	}

	// Non-.jsonl session file falls back to temp root.
	ext2 := sdk.New("urls-local-nonjsonl-test")
	var gotRoot2 string
	ext2.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		gotRoot2, _ = LocalRoot(ctx)
		return nil, nil
	})
	host2 := startFakeHost(t, ext2, fakeHostOpts{SessionID: "sess-nonjsonl", SessionFile: filepath.Join(tmp, "sess.txt")})
	host2.fireEvent(sdk.EventAgentStart, map[string]any{})
	wantTemp := filepath.Join(os.TempDir(), "omp-local", "sess-nonjsonl")
	if gotRoot2 != wantTemp {
		t.Fatalf("LocalRoot(non-.jsonl) = %q, want %q", gotRoot2, wantTemp)
	}
}

// upstream: test/internal-urls/local-protocol.test.ts: "blocks symlink escapes outside local root", "refuses write targets reaching outside the local root through missing dirs or dangling symlinks", "names the URL, never the host path, when a write target cannot be created", "refuses write targets under a local root that is a dangling symlink"
func TestLocalHandler_SymlinkContainmentAndErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	resetAll(t)
	RegisterExtension(nil)

	tmp := t.TempDir()
	localRoot := filepath.Join(tmp, "local")
	outside := filepath.Join(tmp, "outside")
	_ = os.MkdirAll(localRoot, 0o755)
	_ = os.MkdirAll(outside, 0o755)
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644)
	_ = os.Symlink(outside, filepath.Join(localRoot, "linked"))
	_ = os.Symlink(filepath.Join(outside, "victim.txt"), filepath.Join(localRoot, "dangling"))
	_ = os.Symlink(filepath.Join(localRoot, "loopB"), filepath.Join(localRoot, "loopA"))
	_ = os.Symlink(filepath.Join(localRoot, "loopA"), filepath.Join(localRoot, "loopB"))
	_ = os.WriteFile(filepath.Join(localRoot, "file.txt"), []byte("x"), 0o644)

	SetLocalRootOverrideForTests(func(_ sdk.Context) (string, error) {
		return localRoot, nil
	})

	if _, err := Resolve(Context{}, "local://linked/secret.txt"); err == nil || !strings.Contains(err.Error(), "local:// URL escapes local root") {
		t.Errorf("expected symlink escape error, got %v", err)
	}
	if _, _, err := Locate(Context{}, "local://linked/newdir/f", LocateOptions{Create: true}); err == nil || !strings.Contains(err.Error(), "local:// URL escapes local root") {
		t.Errorf("expected create escape error, got %v", err)
	}
	if _, _, err := Locate(Context{}, "local://dangling", LocateOptions{Create: true}); err == nil || !strings.Contains(err.Error(), "dangling symlink") {
		t.Errorf("expected dangling symlink error, got %v", err)
	}
	if p, ok, err := Locate(Context{}, "local://fresh/dir/f", LocateOptions{Create: true}); err != nil || !ok || p != filepath.Join(localRoot, "fresh", "dir", "f") {
		t.Errorf("expected creatable fresh path, got (%q, %v, %v)", p, ok, err)
	}

	// Exact omp error wording: names URL, never host path.
	if _, _, err := Locate(Context{}, "local://loopA/x.md", LocateOptions{Create: true}); err == nil || err.Error() != "local:// URL goes through a symlink loop: local://loopA/x.md" {
		t.Errorf("loop error = %v, want exact omp message", err)
	}
	if _, _, err := Locate(Context{}, "local://file.txt/x.md", LocateOptions{Create: true}); err == nil || err.Error() != "local:// URL goes through a file, not a directory: local://file.txt/x.md" {
		t.Errorf("not-dir error = %v, want exact omp message", err)
	}

	// Dangling symlink as the local root itself.
	danglingRoot := filepath.Join(tmp, "dangling-local")
	_ = os.Symlink(filepath.Join(tmp, "gone"), danglingRoot)
	SetLocalRootOverrideForTests(func(_ sdk.Context) (string, error) {
		return danglingRoot, nil
	})
	if _, _, err := Locate(Context{}, "local://x.md", LocateOptions{Create: true}); err == nil || err.Error() != "local:// URL goes through a dangling symlink: local://x.md" {
		t.Errorf("dangling root create error = %v", err)
	}
	if p, ok, err := Locate(Context{}, "local://x.md", LocateOptions{}); err != nil || ok || p != "" {
		t.Errorf("dangling root locate = (%q, %v, %v), want miss", p, ok, err)
	}

	// Merely missing root is allowed on create without creating it.
	freshRoot := filepath.Join(tmp, "fresh", "local")
	SetLocalRootOverrideForTests(func(_ sdk.Context) (string, error) {
		return freshRoot, nil
	})
	if p, ok, err := Locate(Context{}, "local://x.md", LocateOptions{Create: true}); err != nil || !ok || p != filepath.Join(freshRoot, "x.md") {
		t.Errorf("missing root create = (%q, %v, %v)", p, ok, err)
	}
	if _, err := os.Stat(freshRoot); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Locate(Create) must not create directory on disk")
	}
}

// upstream: test/internal-urls/local-protocol.test.ts: "prefers caller-supplied context.localProtocolOptions over the installed override", "surfaces ENOENT against the caller's local root when the file is missing in that session", "listLocalFiles returns nested files newest first and [] for a missing root"
func TestLocalHandler_ProviderPrecedenceAndListOrder(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	tmp := t.TempDir()
	overrideRoot := filepath.Join(tmp, "override", "local")
	callerRoot := filepath.Join(tmp, "caller", "local")
	_ = os.MkdirAll(overrideRoot, 0o755)
	_ = os.MkdirAll(callerRoot, 0o755)
	_ = os.WriteFile(filepath.Join(overrideRoot, "PLAN.md"), []byte("# wrong session"), 0o644)
	_ = os.WriteFile(filepath.Join(callerRoot, "PLAN.md"), []byte("# caller session"), 0o644)

	SetLocalRootOverrideForTests(func(_ sdk.Context) (string, error) {
		return overrideRoot, nil
	})
	// Production SetLocalRootProvider (e.g. mapping subagent to parent root) wins over override.
	SetLocalRootProvider(func(_ sdk.Context) (string, bool) {
		return callerRoot, true
	})

	res, err := Resolve(Context{}, "local://PLAN.md")
	if err != nil || res.Content != "# caller session" {
		t.Fatalf("Resolve(PLAN.md) = (%+v, %v), want caller session", res, err)
	}

	// Missing in caller root surfaces ENOENT even though override has it.
	_ = os.WriteFile(filepath.Join(overrideRoot, "ONLY_OVERRIDE.md"), []byte("# wrong"), 0o644)
	if _, err := Resolve(Context{}, "local://ONLY_OVERRIDE.md"); err == nil || !strings.Contains(err.Error(), "Local file not found: local://ONLY_OVERRIDE.md") {
		t.Fatalf("expected Local file not found against caller root, got %v", err)
	}

	// listLocalFiles: missing root -> empty; nested files newest first.
	missingRoot := filepath.Join(tmp, "absent", "local")
	SetLocalRootProvider(func(_ sdk.Context) (string, bool) { return missingRoot, true })
	empty, err := ListLocalFiles(sdk.Context{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("ListLocalFiles(missing) = (%v, %v), want empty", empty, err)
	}

	orderRoot := filepath.Join(tmp, "ordered", "local")
	_ = os.MkdirAll(filepath.Join(orderRoot, "sub"), 0o755)
	base := time.Now().Add(-time.Minute)
	for _, item := range []struct {
		name string
		off  time.Duration
	}{
		{"old.md", 0},
		{"sub/new.md", 2 * time.Second},
		{"mid.md", 1 * time.Second},
	} {
		fp := filepath.Join(orderRoot, filepath.FromSlash(item.name))
		_ = os.WriteFile(fp, []byte(item.name), 0o644)
		mt := base.Add(item.off)
		_ = os.Chtimes(fp, mt, mt)
	}
	SetLocalRootProvider(func(_ sdk.Context) (string, bool) { return orderRoot, true })
	files, err := ListLocalFiles(sdk.Context{})
	if err != nil || len(files) != 3 {
		t.Fatalf("ListLocalFiles = (%+v, %v)", files, err)
	}
	gotURLs := []string{files[0].URL, files[1].URL, files[2].URL}
	wantURLs := []string{"local://sub/new.md", "local://mid.md", "local://old.md"}
	if strings.Join(gotURLs, ",") != strings.Join(wantURLs, ",") {
		t.Fatalf("ListLocalFiles order = %v, want %v", gotURLs, wantURLs)
	}
}

// =============================================================================
// Skill handler tests (test/skill-url-containment.test.ts + SDK host loading)
// =============================================================================

// upstream: src/internal-urls/skill-protocol.ts:243 (reads loaded skills from host SDK GetSystemPromptOptions)
func TestSkillHandler_HostLoadedSkillsViaSDK(t *testing.T) {
	resetAll(t)

	tmp := t.TempDir()
	pkgSkillDir := filepath.Join(tmp, "pkg", "skills", "pkg-skill")
	_ = os.MkdirAll(filepath.Join(pkgSkillDir, "refs"), 0o755)
	_ = os.WriteFile(filepath.Join(pkgSkillDir, "SKILL.md"), []byte("Package skill body"), 0o644)
	_ = os.WriteFile(filepath.Join(pkgSkillDir, "refs", "doc.md"), []byte("Ref doc"), 0o644)

	ext := sdk.New("urls-skill-sdk-test")
	RegisterExtension(ext)

	var resMain, resRef, resNorm *Resource
	var errMain, errRef, errNorm, errMissing error
	ext.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		c := Context{SDK: ctx, Cwd: tmp}
		resMain, errMain = Resolve(c, "skill://pkg-skill")
		resRef, errRef = Resolve(c, "skill://pkg-skill/refs/doc.md")
		// skill://sk/a/../refs/doc.md normalizes dot segments like WHATWG URL.pathname
		resNorm, errNorm = Resolve(c, "skill://pkg-skill/a/../refs/doc.md")
		_, errMissing = Resolve(c, "skill://not-loaded")
		return nil, nil
	})

	// Skill lives outside cwd/.pig/skills — only host GetSystemPromptOptions knows it.
	host := startFakeHost(t, ext, fakeHostOpts{
		SessionID: "sess-skill",
		Cwd:       tmp,
		Skills: []sdk.SystemPromptSkill{{
			Name:        "pkg-skill",
			Description: "From a package",
			FilePath:    filepath.Join(pkgSkillDir, "SKILL.md"),
			BaseDir:     pkgSkillDir,
		}},
	})
	host.fireEvent(sdk.EventAgentStart, map[string]any{})

	if errMain != nil || resMain == nil || resMain.Content != "Package skill body" {
		t.Fatalf("Resolve(skill://pkg-skill) = (%+v, %v)", resMain, errMain)
	}
	if errRef != nil || resRef == nil || resRef.Content != "Ref doc" {
		t.Fatalf("Resolve(refs/doc.md) = (%+v, %v)", resRef, errRef)
	}
	if errNorm != nil || resNorm == nil || resNorm.Content != "Ref doc" {
		t.Fatalf("Resolve(a/../refs/doc.md) = (%+v, %v)", resNorm, errNorm)
	}
	if errMissing == nil || !strings.Contains(errMissing.Error(), "Unknown skill: not-loaded") {
		t.Fatalf("expected Unknown skill, got %v", errMissing)
	}
}

// upstream: test/skill-url-containment.test.ts: "serves a bare URI as the skill directory, never the instruction file", "refuses a bare URI whose base directory escapes the plugin root", "opens in-root symlinks at their canonical target, read-only", "refuses symlinks escaping the plugin root", "reports dangling symlinks as missing", "leaves uncontained (non-plugin) skills unrestricted"
func TestSkillHandler_Containment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	resetAll(t)
	RegisterExtension(nil)

	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pluginRoot := filepath.Join(tmp, "plugin")
	skillDir := filepath.Join(pluginRoot, "skills", "docs")
	_ = os.MkdirAll(filepath.Join(skillDir, "references"), 0o755)
	_ = os.MkdirAll(filepath.Join(pluginRoot, "shared"), 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("Body\n"), 0o644)
	_ = os.WriteFile(filepath.Join(skillDir, "synthesized.md"), []byte("Synthesized\n"), 0o644)
	_ = os.WriteFile(filepath.Join(pluginRoot, "shared", "inside.md"), []byte("inside contents\n"), 0o644)
	outside := filepath.Join(tmp, "secret.md")
	_ = os.WriteFile(outside, []byte("outside contents\n"), 0o644)
	_ = os.Symlink(filepath.Join(pluginRoot, "shared", "inside.md"), filepath.Join(skillDir, "references", "ok.md"))
	_ = os.Symlink(outside, filepath.Join(skillDir, "references", "leak.md"))
	_ = os.Symlink(filepath.Join(tmp, "not-created.md"), filepath.Join(skillDir, "references", "dangle.md"))

	pluginSkill := SkillEntry{
		Name:        "docs",
		Description: "Test skill",
		FilePath:    filepath.Join(skillDir, "synthesized.md"),
		BaseDir:     skillDir,
		ContainRoot: pluginRoot,
	}
	SetActiveSkills([]SkillEntry{pluginSkill})

	// Bare URI with Directory: true locates skillDir, not synthesized.md.
	if p, ok, err := Locate(Context{}, "skill://docs", LocateOptions{Directory: true}); err != nil || !ok || p != skillDir {
		t.Fatalf("Locate(directory) = (%q, %v, %v), want %q", p, ok, err, skillDir)
	}
	// In-root symlink resolves to its canonical target inside pluginRoot.
	if p, ok, err := Locate(Context{}, "skill://docs/references/ok.md", LocateOptions{}); err != nil || !ok || p != filepath.Join(pluginRoot, "shared", "inside.md") {
		t.Fatalf("in-root symlink Locate = (%q, %v, %v)", p, ok, err)
	}
	// Escaping symlink is refused.
	if _, err := Resolve(Context{}, "skill://docs/references/leak.md"); err == nil || !strings.Contains(err.Error(), "resolves outside the plugin root") {
		t.Fatalf("escaping symlink error = %v", err)
	}
	// Dangling symlink is reported as missing (Locate returns false, Resolve errors).
	if _, ok, err := Locate(Context{}, "skill://docs/references/dangle.md", LocateOptions{}); err != nil || ok {
		t.Fatalf("dangling symlink Locate = (ok=%v, err=%v), want missing", ok, err)
	}
	// Base directory escaping plugin root is refused.
	escapingBase := pluginSkill
	escapingBase.BaseDir = tmp
	SetActiveSkills([]SkillEntry{escapingBase})
	if _, _, err := Locate(Context{}, "skill://docs", LocateOptions{Directory: true}); err == nil || !strings.Contains(err.Error(), "resolves outside the plugin root") {
		t.Fatalf("escaping baseDir error = %v", err)
	}
	// Uncontained (non-plugin) skills remain unrestricted.
	localSkill := pluginSkill
	localSkill.ContainRoot = ""
	SetActiveSkills([]SkillEntry{localSkill})
	if res, err := Resolve(Context{}, "skill://docs/references/leak.md"); err != nil || res.Content != "outside contents\n" {
		t.Fatalf("uncontained skill Resolve = (%+v, %v)", res, err)
	}
}

// =============================================================================
// Rule handler tests (test/internal-urls/rule-protocol.test.ts)
// =============================================================================

// upstream: test/internal-urls/rule-protocol.test.ts: "resolves a rule scoped only to a subagent...", "does not leak a subagent's scoped rule...", "falls back to the process-global snapshot...", "completes against the caller's scoped rule set..."
func TestRuleHandler(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	tmp := t.TempDir()
	ruleFile := filepath.Join(tmp, "main-only.md")
	_ = os.WriteFile(ruleFile, []byte("main body"), 0o644)

	SetActiveRules([]RuleEntry{{Name: "main-only", Path: ruleFile, Content: "main body", Description: "main desc"}})
	res, err := Resolve(Context{}, "rule://main-only")
	if err != nil || res.Content != "main body" || res.ContentType != "text/markdown" {
		t.Fatalf("Resolve(rule://main-only) = (%+v, %v)", res, err)
	}
	if p, ok, err := Locate(Context{}, "rule://main-only", LocateOptions{}); err != nil || !ok || p != ruleFile {
		t.Fatalf("Locate(rule://main-only) = (%q, %v, %v)", p, ok, err)
	}

	SetRulesProvider(func(c Context) []RuleEntry {
		if c.Cwd == "/scout" {
			return []RuleEntry{{Name: "scout-only", Content: "scout body"}}
		}
		return nil
	})
	res, err = Resolve(Context{Cwd: "/scout"}, "rule://scout-only")
	if err != nil || res.Content != "scout body" {
		t.Fatalf("scoped Resolve failed: %+v %v", res, err)
	}
	if _, err := Resolve(Context{}, "rule://scout-only"); err == nil || !strings.Contains(err.Error(), "Unknown rule") {
		t.Fatalf("expected Unknown rule for unscoped context, got %v", err)
	}
	comps, ok, err := Complete(Context{Cwd: "/scout"}, "rule", "")
	if err != nil || !ok || len(comps) != 1 || comps[0].Value != "scout-only" {
		t.Fatalf("scoped Complete failed: %+v %v %v", comps, ok, err)
	}
}

// =============================================================================
// Out handler tests (test/internal-urls/out-protocol.test.ts)
// =============================================================================

// upstream: test/internal-urls/out-protocol.test.ts: "is advertised only where replies reach the user", "accepts Markdown as an elidable result and rejects disabled, blank, and oversize replies"
func TestOutHandler(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	hasOut := func(outReplies bool) bool {
		for _, line := range Describe(SchemeHost{OutReplies: outReplies}) {
			if strings.HasPrefix(line, "out://") {
				return true
			}
		}
		return false
	}
	if !hasOut(true) || hasOut(false) {
		t.Fatalf("unexpected out:// Describe gating")
	}

	// Closed by default when no policy provider is installed.
	if _, err := Write(Context{}, "out://", "## Status"); err == nil || !strings.Contains(err.Error(), "out.enabled") {
		t.Fatalf("expected out:// closed by default, got %v", err)
	}

	if _, err := Resolve(Context{}, "out://"); err == nil || !strings.Contains(err.Error(), "write-only") {
		t.Fatalf("expected write-only error, got %v", err)
	}

	SetOutPolicyProvider(func(c Context) OutPolicy {
		switch c.Cwd {
		case "/sub":
			return OutPolicy{Enabled: true, Subagent: true}
		case "/off":
			return OutPolicy{Enabled: false}
		default:
			return OutPolicy{Enabled: true}
		}
	})

	res, err := Write(Context{}, "out://", "## Status\n\nhalfway")
	if err != nil || res == nil || res.Text != "Shown to the user." || !res.Useless {
		t.Fatalf("unexpected out:// write result: %+v err=%v", res, err)
	}

	if _, err := Write(Context{}, "out://", " \n "); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("expected non-empty error, got %v", err)
	}
	if _, err := Write(Context{}, "out://", strings.Repeat("x", MaxOutReplyBytes+1)); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("expected 64 KiB error, got %v", err)
	}
	if _, err := Write(Context{Cwd: "/sub"}, "out://", "hi"); err == nil || !strings.Contains(err.Error(), "Subagents") {
		t.Fatalf("expected subagent refusal, got %v", err)
	}
	if _, err := Write(Context{Cwd: "/off"}, "out://", "hi"); err == nil || !strings.Contains(err.Error(), "out.enabled") {
		t.Fatalf("expected disabled refusal, got %v", err)
	}

	// Verify out:// does NOT call SendMessage on the host (no conversation re-injection).
	ext := sdk.New("urls-out-nosend-test")
	var writeErr error
	ext.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		_, writeErr = Write(Context{SDK: ctx}, "out://", "progress update")
		return nil, nil
	})
	host := startFakeHost(t, ext, fakeHostOpts{SessionID: "sess-out"})
	host.fireEvent(sdk.EventAgentStart, map[string]any{})
	if writeErr != nil {
		t.Fatalf("out:// Write failed: %v", writeErr)
	}
	for _, call := range host.recordedCalls() {
		if call.Method == "sendMessage" {
			t.Fatalf("out:// must not call sendMessage; got %+v", call)
		}
	}
}

// =============================================================================
// Help handler tests (test/tools/help-selectors.test.ts)
// =============================================================================

// upstream: test/tools/help-selectors.test.ts: "lists a tool's doc topics", "fails an unknown eval doc topic with the available topic names", "reads an eval doc topic in a session without the eval tool"
func TestHelpHandler(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	if tool, topic, ok := ParseHelpURL("help://read/urls"); !ok || tool != "read" || topic != "urls" {
		t.Fatalf("ParseHelpURL(help://read/urls) = (%q, %q, %v)", tool, topic, ok)
	}
	if tool, topic, ok := ParseHelpURL("help://read"); !ok || tool != "read" || topic != "" {
		t.Fatalf("ParseHelpURL(help://read) = (%q, %q, %v)", tool, topic, ok)
	}

	res, err := Resolve(Context{}, "help://read")
	if err != nil || res.Content != "help://read/sources\nhelp://read/urls" {
		t.Fatalf("help://read = (%q, %v)", res.Content, err)
	}
	res, err = Resolve(Context{}, "help://read/urls")
	if err != nil || !strings.Contains(res.Content, "help://<tool>/<topic>") {
		t.Fatalf("help://read/urls = (%+v, %v)", res, err)
	}
	res, err = Resolve(Context{}, "help://read/sources")
	if err != nil || !strings.Contains(res.Content, "SQLite") {
		t.Fatalf("help://read/sources = (%+v, %v)", res, err)
	}

	// Eval topic order matches omp: judge, helpers, agents, then preludes.
	SetEvalPreludeDocsProvider(func(_ sdk.Context) map[string]string {
		return map[string]string{"browser": "# Browser Prelude\nDrive tabs."}
	})
	res, err = Resolve(Context{}, "help://eval")
	if err != nil || res.Content != "help://eval/judge\nhelp://eval/helpers\nhelp://eval/agents\nhelp://eval/browser" {
		t.Fatalf("help://eval order = %q, err=%v", res.Content, err)
	}
	res, err = Resolve(Context{}, "help://eval/browser")
	if err != nil || !strings.Contains(res.Content, "# Browser Prelude") {
		t.Fatalf("help://eval/browser = (%+v, %v)", res, err)
	}
	if _, err := Resolve(Context{}, "help://eval/nope"); err == nil ||
		!strings.Contains(err.Error(), "Unknown topic 'nope' for eval. Available: judge, helpers, agents, browser.") {
		t.Fatalf("unexpected unknown topic error: %v", err)
	}
	if _, err := Resolve(Context{}, "help://unknown"); err == nil || !strings.Contains(err.Error(), "No tool 'unknown'") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}

	// Active tool with no doc topics returns "Tool 'bash' has no doc topics."
	ext := sdk.New("urls-help-active-test")
	var bashErr error
	ext.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		_, bashErr = Resolve(Context{SDK: ctx}, "help://bash")
		return nil, nil
	})
	host := startFakeHost(t, ext, fakeHostOpts{SessionID: "sess-help", ActiveTools: []string{"bash", "read"}})
	host.fireEvent(sdk.EventAgentStart, map[string]any{})
	if bashErr == nil || bashErr.Error() != "Tool 'bash' has no doc topics." {
		t.Fatalf("help://bash error = %v, want Tool 'bash' has no doc topics.", bashErr)
	}
}

// =============================================================================
// Attachment & MCP handler tests
// =============================================================================

func TestAttachmentHandler(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	RegisterAttachment(Context{}, Attachment{
		URI:        "attachment://1",
		Label:      "screenshot.png",
		MIMEType:   "image/png",
		SourcePath: "/tmp/screenshot.png",
	})
	res, err := Resolve(Context{}, "attachment://1")
	if err != nil || !strings.Contains(res.Content, "screenshot.png: image/png") || res.SourcePath != "/tmp/screenshot.png" {
		t.Fatalf("Resolve(attachment://1) = (%+v, %v)", res, err)
	}
	p, ok, err := Locate(Context{}, "attachment://1", LocateOptions{})
	if err != nil || !ok || p != "/tmp/screenshot.png" {
		t.Fatalf("Locate(attachment://1) = (%q, %v, %v)", p, ok, err)
	}
	if _, err := Resolve(Context{}, "attachment://9"); err == nil || !strings.Contains(err.Error(), "Available attachment URIs: attachment://1") {
		t.Fatalf("expected available attachment error, got %v", err)
	}
}

// upstream: test/internal-urls/mcp-protocol.test.ts: "requires resource URI in mcp URL", "preserves a literal semicolon in an exact MCP resource URI"
func TestMCPHandler(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	// Not advertised in PromptDoc while SDK cannot read resources.
	for _, line := range Describe(SchemeHost{SkillURIAccess: true, RuleCount: 1, OutReplies: true}) {
		if strings.HasPrefix(line, "mcp://") {
			t.Fatalf("mcp:// must not be advertised while SDK lacks resource reads")
		}
	}

	// URI extraction preserves query, trailing slash on native URI, and literal semicolon.
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"mcp://test://notes?a=1&b=2", "test://notes?a=1&b=2"},
		{"catalog://root/", "catalog://root/"},
		{"catalog://items;active", "catalog://items;active"},
	} {
		u, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		got, err := extractMCPResourceURI(u)
		if err != nil || got != tc.want {
			t.Errorf("extractMCPResourceURI(%q) = (%q, %v), want %q", tc.in, got, err, tc.want)
		}
	}

	if _, err := Resolve(Context{}, "mcp://"); err == nil || !strings.Contains(err.Error(), "requires a resource URI") {
		t.Fatalf("expected empty mcp:// error, got %v", err)
	}
	if _, err := Resolve(Context{}, "mcp://test://notes"); err == nil || !strings.Contains(err.Error(), "does not expose MCP") {
		t.Fatalf("expected SDK gap error, got %v", err)
	}
	if _, err := Resolve(Context{}, "urn:example:document"); err == nil || !strings.Contains(err.Error(), "urn:example:document") {
		t.Fatalf("expected fallback MCP error naming urn, got %v", err)
	}
}

// =============================================================================
// Multi-session isolation, lifecycle.End, and RegisterExtension idempotency
// =============================================================================

func TestMultiSessionIsolationAndLifecycle(t *testing.T) {
	resetAll(t)
	lifecycle.ResetForTests()
	t.Cleanup(lifecycle.ResetForTests)

	tmp := t.TempDir()
	fileA := filepath.Join(tmp, "a.go")
	fileB := filepath.Join(tmp, "b.go")
	conflictText := "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> feat\n"
	_ = os.WriteFile(fileA, []byte(conflictText), 0o644)
	_ = os.WriteFile(fileB, []byte(conflictText), 0o644)

	skillDirA := filepath.Join(tmp, "skillA")
	skillDirB := filepath.Join(tmp, "skillB")
	_ = os.MkdirAll(skillDirA, 0o755)
	_ = os.MkdirAll(skillDirB, 0o755)
	_ = os.WriteFile(filepath.Join(skillDirA, "SKILL.md"), []byte("Skill A body"), 0o644)
	_ = os.WriteFile(filepath.Join(skillDirB, "SKILL.md"), []byte("Skill B body"), 0o644)

	// Build two fresh extension instances (as PiG does for parent + child or two sessions),
	// plus verify RegisterExtension registers all 8 builtins with zero diagnostics and no panic.
	ext1 := sdk.New("urls-sess-1")
	RegisterExtension(ext1)
	if diags := RegistrationDiagnostics(); len(diags) != 0 {
		t.Fatalf("expected 0 registration diagnostics, got %v", diags)
	}
	for _, want := range []string{"skill", "rule", "out", "local", "mcp", "help", "attachment", "conflict"} {
		if _, ok := Lookup(want); !ok {
			t.Fatalf("built-in scheme %q not registered", want)
		}
	}

	ext2 := sdk.New("urls-sess-2")
	RegisterExtension(ext2)

	var ctx1, ctx2 sdk.Context
	ext1.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx1 = ctx
		c := Context{SDK: ctx, Cwd: tmp}
		RegisterAttachment(c, Attachment{URI: "attachment://1", Label: "a.png", MIMEType: "image/png", SourcePath: "/tmp/a.png"})
		h := SessionConflictHistory(c)
		for _, b := range ScanConflictLines(strings.Split(conflictText, "\n"), 1) {
			h.Register(fileA, "a.go", b)
		}
		return nil, nil
	})
	ext2.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx2 = ctx
		c := Context{SDK: ctx, Cwd: tmp}
		RegisterAttachment(c, Attachment{URI: "attachment://1", Label: "b.png", MIMEType: "image/png", SourcePath: "/tmp/b.png"})
		h := SessionConflictHistory(c)
		for _, b := range ScanConflictLines(strings.Split(conflictText, "\n"), 1) {
			h.Register(fileB, "b.go", b)
		}
		return nil, nil
	})

	// Force GetSystemPromptOptions to fail so skill resolution relies on per-session
	// before_agent_start cache, exercising sessionStates isolation and cleanup.
	failPromptOpts := func(method string, _ map[string]any) (any, string) {
		if method == "getSystemPromptOptions" {
			return nil, "unavailable"
		}
		return nil, ""
	}
	host1 := startFakeHost(t, ext1, fakeHostOpts{SessionID: "sess-1", Cwd: tmp, OnCall: failPromptOpts})
	host2 := startFakeHost(t, ext2, fakeHostOpts{SessionID: "sess-2", Cwd: tmp, OnCall: failPromptOpts})
	host1.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"systemPromptOptions": map[string]any{
			"skills": []any{map[string]any{"name": "skill-a", "filePath": filepath.Join(skillDirA, "SKILL.md"), "baseDir": skillDirA}},
		},
	})
	host2.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"systemPromptOptions": map[string]any{
			"skills": []any{map[string]any{"name": "skill-b", "filePath": filepath.Join(skillDirB, "SKILL.md"), "baseDir": skillDirB}},
		},
	})
	host1.fireEvent(sdk.EventAgentStart, map[string]any{})
	host2.fireEvent(sdk.EventAgentStart, map[string]any{})

	// Both sessions see only their own attachment, conflict, and skill state.
	if res, err := Resolve(Context{SDK: ctx1, Cwd: tmp}, "attachment://1"); err != nil || res.SourcePath != "/tmp/a.png" {
		t.Fatalf("sess-1 attachment = (%+v, %v)", res, err)
	}
	if res, err := Resolve(Context{SDK: ctx2, Cwd: tmp}, "attachment://1"); err != nil || res.SourcePath != "/tmp/b.png" {
		t.Fatalf("sess-2 attachment = (%+v, %v)", res, err)
	}
	if res, err := Resolve(Context{SDK: ctx1, Cwd: tmp}, "skill://skill-a"); err != nil || res.Content != "Skill A body" {
		t.Fatalf("sess-1 skill = (%+v, %v)", res, err)
	}
	if _, err := Resolve(Context{SDK: ctx2, Cwd: tmp}, "skill://skill-a"); err == nil {
		t.Fatalf("sess-2 must not see sess-1 skill")
	}

	// Shut down session 1 (via session_shutdown -> lifecycle.End("sess-1")) and call End twice + unknown ID.
	host1.fireEvent(sdk.EventSessionShutdown, map[string]any{})
	lifecycle.End("sess-1")
	lifecycle.End("unknown-session")

	// Session 1's state is released from the unified sessionStates table; session 2 stays intact.
	sessionStateMu.RLock()
	_, has1 := sessionStates["sess-1"]
	st2, has2 := sessionStates["sess-2"]
	hasSkills2 := has2 && st2.hasSkills
	hasAtt2 := has2 && len(st2.attachments) > 0
	hasConf2 := has2 && st2.conflicts != nil
	sessionStateMu.RUnlock()
	if has1 || !has2 || !hasSkills2 || !hasAtt2 || !hasConf2 {
		t.Fatalf("sessionStates after sess-1 shutdown: has1=%v has2=%v (skills=%v att=%v conf=%v)", has1, has2, hasSkills2, hasAtt2, hasConf2)
	}

	// Session 2 still works completely intact.
	if res, err := Resolve(Context{SDK: ctx2, Cwd: tmp}, "attachment://1"); err != nil || res.SourcePath != "/tmp/b.png" {
		t.Fatalf("sess-2 attachment after sess-1 shutdown = (%+v, %v)", res, err)
	}
	if res, err := Resolve(Context{SDK: ctx2, Cwd: tmp}, "skill://skill-b"); err != nil || res.Content != "Skill B body" {
		t.Fatalf("sess-2 skill after sess-1 shutdown = (%+v, %v)", res, err)
	}
	if res, err := Resolve(Context{SDK: ctx2, Cwd: tmp}, "conflict://1/ours"); err != nil || res.Content != "ours" {
		t.Fatalf("sess-2 conflict after sess-1 shutdown = (%+v, %v)", res, err)
	}

	// Child-session cleanup via lifecycle.End("sess-2") directly (no session_shutdown event).
	lifecycle.End("sess-2")
	sessionStateMu.RLock()
	_, has2After := sessionStates["sess-2"]
	sessionStateMu.RUnlock()
	if has2After {
		t.Fatalf("expected sess-2 state released by lifecycle.End")
	}
}

// Verifies N4: SkillHandler.Complete reads the per-session cache first without a host call per keystroke.
func TestSkillHandler_CompleteUsesCacheWithoutHostCall(t *testing.T) {
	resetAll(t)
	tmp := t.TempDir()
	ext := sdk.New("urls-skill-complete-cache")
	RegisterExtension(ext)

	var ctx sdk.Context
	ext.OnEvent(sdk.EventAgentStart, func(c sdk.Context, _ map[string]any) (any, error) {
		ctx = c
		return nil, nil
	})
	promptOptsCalls := 0
	host := startFakeHost(t, ext, fakeHostOpts{
		SessionID: "sess-cache",
		Cwd:       tmp,
		OnCall: func(method string, _ map[string]any) (any, string) {
			if method == "getSystemPromptOptions" {
				promptOptsCalls++
				return sdk.SystemPromptOptions{
					Cwd: tmp,
					Skills: []sdk.SystemPromptSkill{{
						Name:        "cached-skill",
						Description: "from host",
						FilePath:    filepath.Join(tmp, "SKILL.md"),
						BaseDir:     tmp,
					}},
				}, ""
			}
			return nil, ""
		},
	})
	host.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"systemPromptOptions": map[string]any{
			"skills": []any{map[string]any{"name": "cached-skill", "description": "from cache", "filePath": filepath.Join(tmp, "SKILL.md"), "baseDir": tmp}},
		},
	})
	host.fireEvent(sdk.EventAgentStart, map[string]any{})

	comps, ok, err := Complete(Context{SDK: ctx, Cwd: tmp}, "skill", "")
	if err != nil || !ok || len(comps) != 1 || comps[0].Value != "cached-skill" {
		t.Fatalf("Complete(skill) = (%+v, %v, %v)", comps, ok, err)
	}
	if promptOptsCalls != 0 {
		t.Fatalf("expected 0 getSystemPromptOptions host calls when cache is populated, got %d", promptOptsCalls)
	}
}

// Verifies SetLocalRootProvider rejects relative roots rather than resolving them against process cwd.
func TestLocalHandler_RejectsRelativeProviderRoot(t *testing.T) {
	resetAll(t)
	RegisterExtension(nil)

	SetLocalRootProvider(func(_ sdk.Context) (string, bool) {
		return "relative/local/dir", true
	})
	if _, err := LocalRoot(sdk.Context{}); err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("expected relative path error from LocalRoot, got %v", err)
	}
	if _, err := Resolve(Context{}, "local://notes.md"); err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("expected relative path error from Resolve, got %v", err)
	}
}
