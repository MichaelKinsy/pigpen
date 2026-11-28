package urls

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Target is a routed read or search target. upstream: router.ts UrlTarget.
type Target struct {
	// Kind is "file" (a file-backed scheme whose URL located a local path:
	// read Path through the filesystem pipeline) or "resource" (resolve it).
	Kind string
	URL  URL
	Spec Spec
	// Path is set for Kind "file".
	Path string
	// Selector is the peeled read selector, without the leading colon.
	Selector string
}

// SyncLocator is an optional interface for linkable handlers that can locate a
// local path synchronously without I/O. upstream: types.ts ProtocolHandler.locateSync.
type SyncLocator interface {
	LocateSync(c Context, u URL) (string, bool)
}

// WriteTargetInfo is the parsed URL and scheme spec for a registered write target.
type WriteTargetInfo struct {
	URL  URL
	Spec Spec
}

// GlobInfo describes the segments of a registered URL and where globbing starts.
type GlobInfo struct {
	URL       string
	Scheme    string
	Segments  []string
	FirstGlob int
}

var (
	mu                     sync.RWMutex
	handlers               = map[string]Handler{}
	handlerOrder           []string
	builtinSchemes         = map[string]bool{}
	resourceFallbackScheme = "mcp"

	singleSlashAliasRE = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*):\/`)
	// Matches `/home/me/repo/local://x.md` or `C:\repo\local://x.md` (separator + scheme://).
	prefixedURLSegmentRE = regexp.MustCompile(`(?i)[\\/]([a-z][a-z0-9+.-]*):\/\/`)
	// Matches a leading scheme prefix (excluding Windows drive letters `C:\` or `C:/`).
	anySchemePrefixRE = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:`)
	winDrivePrefixRE  = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)
	globCharsRE       = regexp.MustCompile(`[*?\[{]`)
	queryStartRE      = regexp.MustCompile(`\?[\w.-]*=`)
)

// Register installs, or replaces, the handler of h.Scheme(). It fails when
// the spec's write policy and the Writer/Locator interfaces disagree.
// upstream: router.ts register.
func Register(h Handler) error {
	scheme := strings.ToLower(h.Scheme())
	spec := h.Spec()
	_, writes := h.(Writer)
	_, locates := h.(Locator)
	_, locatesSync := h.(SyncLocator)
	via := ""
	if spec.Write != nil {
		via = spec.Write.Via
	}
	switch {
	case via == "handler" && !writes:
		return fmt.Errorf(`%s:// spec.write.via is "handler" but the handler lacks write()`, scheme)
	case via != "handler" && writes:
		viaDesc := "absent"
		if spec.Write != nil {
			viaDesc = `"` + via + `"`
		}
		return fmt.Errorf("%s:// handler has write() but spec.write.via is %s", scheme, viaDesc)
	case via == "file":
		if spec.Backing != BackingFile {
			return fmt.Errorf(`%s:// spec.write.via "file" requires backing "file", not "%s"`, scheme, spec.Backing)
		}
		if !locates {
			return fmt.Errorf(`%s:// spec.write.via "file" requires a locate() hook`, scheme)
		}
		if spec.Immutable {
			return fmt.Errorf(`%s:// spec.write.via "file" contradicts spec.immutable`, scheme)
		}
	}
	if spec.Write != nil && spec.Write.Scope == "sandbox" && (!spec.Linkable || !locatesSync) {
		return fmt.Errorf(`%s:// spec.write.scope "sandbox" requires spec.linkable and a locateSync() hook`, scheme)
	}
	mu.Lock()
	if _, exists := handlers[scheme]; !exists {
		handlerOrder = append(handlerOrder, scheme)
	}
	handlers[scheme] = h
	mu.Unlock()
	return nil
}

// Unregister removes a scheme and reports whether it was registered.
func Unregister(scheme string) bool {
	mu.Lock()
	defer mu.Unlock()
	scheme = strings.ToLower(scheme)
	_, ok := handlers[scheme]
	if ok {
		delete(handlers, scheme)
		for i, s := range handlerOrder {
			if s == scheme {
				handlerOrder = append(handlerOrder[:i], handlerOrder[i+1:]...)
				break
			}
		}
	}
	return ok
}

// Lookup returns the handler of a scheme.
func Lookup(scheme string) (Handler, bool) {
	mu.RLock()
	defer mu.RUnlock()
	h, ok := handlers[strings.ToLower(scheme)]
	return h, ok
}

// LookupSpec returns the Spec of a registered scheme.
func LookupSpec(scheme string) (Spec, bool) {
	h, ok := Lookup(scheme)
	if !ok {
		return Spec{}, false
	}
	return h.Spec(), true
}

// IsBuiltin reports whether scheme was registered by RegisterExtension.
func IsBuiltin(scheme string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return builtinSchemes[strings.ToLower(scheme)]
}

// Schemes lists the registered schemes, sorted.
func Schemes() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(handlers))
	for scheme := range handlers {
		out = append(out, scheme)
	}
	sort.Strings(out)
	return out
}

// ResetForTests empties the registry.
func ResetForTests() {
	mu.Lock()
	handlers = map[string]Handler{}
	handlerOrder = nil
	builtinSchemes = map[string]bool{}
	mu.Unlock()
	sessionStateMu.Lock()
	sessionStates = map[string]*sessionState{}
	sessionStateMu.Unlock()
}

// SetResourceFallbackScheme configures the fallback scheme for unregistered custom URIs
// (default "mcp"). Pass "" to disable fallback.
func SetResourceFallbackScheme(scheme string) {
	mu.Lock()
	resourceFallbackScheme = strings.ToLower(scheme)
	mu.Unlock()
}

func isResourceFallbackScheme(scheme string) bool {
	switch scheme {
	case "file", "http", "https", "":
		return false
	}
	mu.RLock()
	fb := resourceFallbackScheme
	_, ok := handlers[fb]
	mu.RUnlock()
	return fb != "" && ok
}

func resourceFallbackHandler(scheme string) (Handler, bool) {
	if !isResourceFallbackScheme(scheme) {
		return nil, false
	}
	mu.RLock()
	h, ok := handlers[resourceFallbackScheme]
	mu.RUnlock()
	return h, ok
}

// Normalize rewrites mistyped spellings of a scheme that declares Spec.SingleSlashAlias
// to `scheme://x`: the single-slash alias (`local:/x`) and a filesystem path prefixed onto
// the URL (`/home/me/repo/local://x`). Other inputs pass through.
// upstream: router.ts normalize.
func Normalize(input string) string {
	hasURLPrefix := anySchemePrefixRE.MatchString(input) && !winDrivePrefixRE.MatchString(input)
	if !hasURLPrefix {
		// The first eligible scheme wins: later `local://` text belongs to the URL's own path.
		for _, idx := range prefixedURLSegmentRE.FindAllStringSubmatchIndex(input, -1) {
			// Must be followed by a non-slash or end of string (bare `local://`).
			after := idx[1]
			if after < len(input) && input[after] == '/' {
				continue
			}
			scheme := strings.ToLower(input[idx[2]:idx[3]])
			if spec, ok := LookupSpec(scheme); ok && spec.SingleSlashAlias {
				return input[idx[0]+1:]
			}
		}
	}
	if m := singleSlashAliasRE.FindStringSubmatchIndex(input); m != nil {
		// Reject `scheme://` (only single slash `scheme:/` not followed by `/`).
		if m[1] < len(input) && input[m[1]] == '/' {
			return input
		}
		scheme := strings.ToLower(input[m[2]:m[3]])
		if spec, ok := LookupSpec(scheme); ok && spec.SingleSlashAlias {
			return input[m[2]:m[3]] + "://" + input[m[1]:]
		}
	}
	return input
}

type registeredURL struct {
	url     string
	scheme  string
	handler Handler
}

func lookupRegistered(input string) (registeredURL, bool) {
	norm := Normalize(input)
	scheme, _, ok := SplitScheme(norm)
	if !ok {
		return registeredURL{}, false
	}
	h, ok := Lookup(scheme)
	if !ok {
		return registeredURL{}, false
	}
	return registeredURL{url: norm, scheme: scheme, handler: h}, true
}

// Parse parses an internal URL, normalizing single-slash aliases first.
// upstream: parse.ts parseInternalUrl.
func Parse(input string) (URL, error) {
	return parseInternal(Normalize(input))
}

// CanHandle reports whether input (after Normalize) is a hierarchical `scheme://`
// URL of a registered scheme.
// upstream: router.ts canHandle.
func CanHandle(input string) bool {
	_, ok := lookupRegistered(input)
	return ok
}

// CanResolve reports whether read can resolve input through either a native
// handler or the resource fallback scheme (including opaque URIs).
// upstream: router.ts canResolve.
func CanResolve(input string) bool {
	norm := Normalize(input)
	scheme := ExtractScheme(norm)
	if scheme == "" {
		return false
	}
	if _, ok := Lookup(scheme); ok {
		return CanHandle(norm)
	}
	return isResourceFallbackScheme(scheme)
}

// Split normalizes input and peels a trailing read selector per Spec.Selectors
// (+PortAuthority). Non-URLs and unknown schemes pass through unchanged.
// upstream: router.ts split.
func Split(input string) (pathOut, selector string) {
	return splitInternalURLSel(Normalize(input), LookupSpec)
}

// PeelWriteSelector normalizes a mutating tool's URL target and peels whole-file
// display selectors (`:raw`/`:conflicts`). Any other selector or any selector on a
// device-backed scheme returns an error. Non-URLs pass through.
// upstream: router.ts peelWriteSelector.
func PeelWriteSelector(input, tool string) (string, error) {
	p, sel := Split(input)
	if sel == "" {
		return p, nil
	}
	if reg, ok := lookupRegistered(p); ok && reg.handler.Spec().Backing == BackingDevice {
		return "", fmt.Errorf(`%s does not accept the trailing selector ":%s" on a device URL. Remove it.`, tool, sel)
	}
	if wholeFileSelectorRE.MatchString(sel) {
		return p, nil
	}
	return "", fmt.Errorf(
		`%s does not accept the trailing selector ":%s" — it addresses a whole file. Remove ":%s", or if the filename truly ends with it, percent-encode the ":" as %%3A.`,
		tool, sel, sel,
	)
}

func route(input string, allowFallback bool) (URL, Handler, error) {
	norm := Normalize(input)
	u, err := parseInternal(norm)
	if err != nil {
		return URL{}, nil, err
	}
	h, ok := Lookup(u.Scheme)
	if !ok && allowFallback {
		h, ok = resourceFallbackHandler(u.Scheme)
	}
	if !ok {
		mu.RLock()
		order := append([]string(nil), handlerOrder...)
		mu.RUnlock()
		avail := "none"
		if len(order) > 0 {
			parts := make([]string, len(order))
			for i, s := range order {
				parts[i] = s + "://"
			}
			avail = strings.Join(parts, ", ")
		}
		return URL{}, nil, fmt.Errorf("Unknown protocol: %s://\nSupported: %s", u.Scheme, avail)
	}
	return u, h, nil
}

// Resolve returns the resource input names, with Immutable defaulted from the
// scheme's spec. Supports the resource fallback scheme for unregistered custom URIs.
// upstream: router.ts resolve.
func Resolve(c Context, input string) (*Resource, error) {
	u, h, err := route(input, true)
	if err != nil {
		return nil, err
	}
	res, err := h.Resolve(c, u)
	if err != nil {
		return nil, err
	}
	if res != nil && res.Immutable == nil {
		immutable := h.Spec().Immutable
		res.Immutable = &immutable
	}
	return res, nil
}

// Write dispatches a handler-owned write. A nil result keeps the write
// tool's default result.
// upstream: router.ts write.
func Write(c Context, input, content string) (*WriteResult, error) {
	u, h, err := route(input, false)
	if err != nil {
		return nil, err
	}
	w, ok := h.(Writer)
	if !ok {
		return nil, fmt.Errorf("%s:// URLs are read-only for write; use the protocol-specific tool for mutations.", u.Scheme)
	}
	return w.Write(c, u, content)
}

// Locate returns the local path backing input (with read selector peeled); ok is
// false when the scheme has no Locator or the URL has no local backing.
// upstream: router.ts locate.
func Locate(c Context, input string, opts LocateOptions) (string, bool, error) {
	reg, ok := lookupRegistered(input)
	if !ok {
		return "", false, nil
	}
	l, ok := reg.handler.(Locator)
	if !ok {
		return "", false, nil
	}
	bare, _ := Split(reg.url)
	u, err := parseInternal(bare)
	if err != nil {
		return "", false, err
	}
	return l.Locate(c, u, opts)
}

// LocateSync returns the local path backing input synchronously for linkable schemes.
// upstream: router.ts locateSync.
func LocateSync(c Context, input string) (string, bool) {
	reg, ok := lookupRegistered(input)
	if !ok || !reg.handler.Spec().Linkable {
		return "", false
	}
	sl, ok := reg.handler.(SyncLocator)
	if !ok {
		return "", false
	}
	bare, _ := Split(reg.url)
	u, err := parseInternal(bare)
	if err != nil {
		return "", false
	}
	return sl.LocateSync(c, u)
}

// RequireLocal locates input locally or returns a diagnostic error.
// upstream: router.ts requireLocal.
func RequireLocal(c Context, input, action string, opts LocateOptions) (string, error) {
	opts.LocalOnly = true
	located, ok, err := Locate(c, input, opts)
	if err != nil {
		return "", err
	}
	if ok {
		return located, nil
	}
	reg, hasReg := lookupRegistered(input)
	if hasReg {
		spec := reg.handler.Spec()
		if _, hasLocate := reg.handler.(Locator); hasLocate && spec.Immutable && spec.Backing != BackingRemote {
			bare, _ := Split(reg.url)
			if u, parseErr := parseInternal(bare); parseErr == nil {
				if _, resErr := reg.handler.Resolve(c, u); resErr != nil {
					return "", fmt.Errorf("Cannot %s %s: %v", action, input, resErr)
				}
			}
		}
	}
	scheme := ExtractScheme(Normalize(input))
	if scheme == "" {
		scheme = input
	}
	msg := fmt.Sprintf("Cannot %s %s:// URL: no local file backs %s", action, scheme, input)
	if hasReg {
		backing := reg.handler.Spec().Backing
		if backing == BackingRemote || backing == BackingVirtual {
			return "", fmt.Errorf("%s. Use `read %s` to inspect it.", msg, input)
		}
	}
	return "", fmt.Errorf("%s", msg)
}

// ResolveTarget routes a read or search input (after selector peeling and resource
// fallback). Returns nil when input is not a router URL.
// upstream: router.ts target.
func ResolveTarget(c Context, input string) (*Target, error) {
	bare, sel := Split(input)
	scheme := ExtractScheme(bare)
	if scheme == "" {
		return nil, nil
	}
	var h Handler
	if reg, ok := Lookup(scheme); ok {
		if CanHandle(bare) {
			h = reg
		}
	} else if fb, ok := resourceFallbackHandler(scheme); ok {
		h = fb
	}
	if h == nil {
		return nil, nil
	}
	u, err := parseInternal(bare)
	if err != nil {
		return nil, err
	}
	spec := h.Spec()
	if spec.Backing == BackingFile {
		if l, ok := h.(Locator); ok {
			p, found, err := l.Locate(c, u, LocateOptions{})
			if err != nil {
				return nil, err
			}
			if found {
				return &Target{Kind: "file", URL: u, Spec: spec, Path: p, Selector: sel}, nil
			}
		}
	}
	return &Target{Kind: "resource", URL: u, Spec: spec, Selector: sel}, nil
}

// GlobSegments returns the authority + path segments of a registered URL (query
// and fragment cut) and the index of the first glob segment (-1 if none).
// upstream: router.ts #globSegments.
func GlobSegments(input string) (GlobInfo, bool) {
	reg, ok := lookupRegistered(input)
	if !ok {
		return GlobInfo{}, false
	}
	rest := reg.url[len(reg.scheme)+3:]
	if frag := strings.IndexByte(rest, '#'); frag != -1 {
		rest = rest[:frag]
	}
	if qLoc := queryStartRE.FindStringIndex(rest); qLoc != nil {
		rest = rest[:qLoc[0]]
	}
	segments := strings.Split(rest, "/")
	firstGlob := -1
	portAuth := reg.handler.Spec().PortAuthority
	for i, seg := range segments {
		if (i > 0 || !portAuth) && globCharsRE.MatchString(seg) {
			firstGlob = i
			break
		}
	}
	return GlobInfo{
		URL:       reg.url,
		Scheme:    reg.scheme,
		Segments:  segments,
		FirstGlob: firstGlob,
	}, true
}

// IsGlob reports whether input is a registered URL with a glob segment.
// upstream: router.ts isGlob.
func IsGlob(input string) bool {
	info, ok := GlobSegments(input)
	return ok && info.FirstGlob != -1
}

// FileWritable reports whether file-editing tools (edit, ast_edit) may write
// the file input locates: mutable and Spec.Write.Via == "file".
// upstream: router.ts fileWritable.
func FileWritable(input string) bool {
	reg, ok := lookupRegistered(input)
	if !ok {
		return false
	}
	spec := reg.handler.Spec()
	return spec.Write != nil && spec.Write.Via == "file" && !spec.Immutable
}

// WriteTarget returns the parsed URL and scheme spec of a registered input for
// the write tool's routing and gates; nil for non-URLs.
// upstream: router.ts writeTarget.
func WriteTarget(input string) *WriteTargetInfo {
	reg, ok := lookupRegistered(input)
	if !ok {
		return nil
	}
	u, err := parseInternal(reg.url)
	if err != nil {
		return nil
	}
	return &WriteTargetInfo{URL: u, Spec: reg.handler.Spec()}
}

// SandboxRoots returns the absolute roots of every `sandbox`-scope writable scheme.
// upstream: router.ts sandboxRoots.
func SandboxRoots(c Context) []string {
	mu.RLock()
	order := append([]string(nil), handlerOrder...)
	mu.RUnlock()
	var roots []string
	for _, scheme := range order {
		h, ok := Lookup(scheme)
		if !ok {
			continue
		}
		spec := h.Spec()
		if spec.Write == nil || spec.Write.Scope != "sandbox" {
			continue
		}
		if root, ok := LocateSync(c, scheme+"://"); ok {
			if abs, err := filepath.Abs(root); err == nil {
				roots = append(roots, abs)
			} else {
				roots = append(roots, root)
			}
		}
	}
	return roots
}

// Enumerate expands a virtual container URL into its searchable leaves.
// Returns (nil, false, nil) when the scheme does not implement Enumerator.
// upstream: router.ts enumerate.
func Enumerate(c Context, input string) ([]Leaf, bool, error) {
	reg, ok := lookupRegistered(input)
	if !ok {
		return nil, false, nil
	}
	en, ok := reg.handler.(Enumerator)
	if !ok {
		return nil, false, nil
	}
	u, err := parseInternal(reg.url)
	if err != nil {
		return nil, true, err
	}
	leaves, err := en.Enumerate(c, u)
	return leaves, true, err
}

// ReadDirectory lists immediate children of a remote or virtual directory URL.
// Returns (nil, false, nil) when the scheme does not implement DirectoryReader.
func ReadDirectory(c Context, input string) ([]DirEntry, bool, error) {
	reg, ok := lookupRegistered(input)
	if !ok {
		return nil, false, nil
	}
	dr, ok := reg.handler.(DirectoryReader)
	if !ok {
		return nil, false, nil
	}
	u, err := parseInternal(reg.url)
	if err != nil {
		return nil, true, err
	}
	entries, err := dr.ReadDirectory(c, u)
	return entries, true, err
}

// CompletionSchemes lists registered schemes whose handler implements Completer,
// in registration order.
// upstream: router.ts completionSchemes.
func CompletionSchemes() []string {
	mu.RLock()
	order := append([]string(nil), handlerOrder...)
	mu.RUnlock()
	var out []string
	for _, scheme := range order {
		if h, ok := Lookup(scheme); ok {
			if _, canComplete := h.(Completer); canComplete {
				out = append(out, scheme)
			}
		}
	}
	return out
}

// Complete returns autocomplete candidates for `scheme://<query>`.
// Returns (nil, false, nil) when the scheme is unknown or does not support completion.
// upstream: router.ts complete.
func Complete(c Context, scheme, query string) ([]Completion, bool, error) {
	h, ok := Lookup(scheme)
	if !ok {
		return nil, false, nil
	}
	comp, ok := h.(Completer)
	if !ok {
		return nil, false, nil
	}
	items, err := comp.Complete(c, query)
	return items, true, err
}

// ompSchemeOrder mirrors the constructor registration order of omp's InternalUrlRouter
// (upstream: internal-urls/router.ts:110-130) so Describe and Reference match omp's
// system-prompt order regardless of which Go package registers first.
var ompSchemeOrder = map[string]int{
	"skill":      1,
	"rule":       2,
	"memory":     3,
	"out":        4,
	"agent":      5,
	"history":    6,
	"local":      7,
	"proc":       8,
	"cfg":        9,
	"ssh":        10,
	"security":   11,
	"vault":      12,
	"issue":      13,
	"pr":         14,
	"mr":         15,
	"mcp":        16,
	"omp":        17,
	"help":       18,
	"attachment": 19,
	"conflict":   20,
}

func orderedPromptSchemes() []string {
	mu.RLock()
	order := append([]string(nil), handlerOrder...)
	mu.RUnlock()
	// Keep omp's built-in schemes in omp's exact order; custom/unknown schemes
	// follow in their registration order.
	sort.SliceStable(order, func(i, j int) bool {
		ri, oki := ompSchemeOrder[order[i]]
		rj, okj := ompSchemeOrder[order[j]]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			return oki
		}
		return false
	})
	return order
}

// Describe lists the system-prompt lines of the schemes usable in the session
// host describes, in omp's canonical scheme order.
// upstream: router.ts describe.
func Describe(host SchemeHost) []string {
	var out []string
	for _, scheme := range orderedPromptSchemes() {
		h, ok := Lookup(scheme)
		if !ok {
			continue
		}
		if d, ok := h.(PromptDocer); ok {
			if line := d.PromptDoc(host); line != "" {
				out = append(out, line)
			}
		}
	}
	return out
}

// Reference lists the per-scheme grammar served at help://read/urls, in omp's
// canonical scheme order.
// upstream: router.ts reference.
func Reference() []string {
	var out []string
	for _, scheme := range orderedPromptSchemes() {
		h, ok := Lookup(scheme)
		if !ok {
			continue
		}
		if r, ok := h.(PromptReferencer); ok {
			if text := r.PromptReference(); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}
