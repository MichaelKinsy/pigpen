package posthorse

import (
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Port of src/note-scopes.ts.
//
// The TypeBox schemas of the original (ownerSchema ... touchSchema, note-scopes.ts:9-62) are written as
// the check* validators below. `Check(schema, value)` of typebox 1.x validates JSON Schema semantics:
// an object schema accepts any non-array object, requires every declared key (a key whose value is
// null still counts as present, then fails its own type), ignores extra properties, `Type.Number()` is
// `Number.isFinite`, `Type.Literal` is deep equality, `Type.Union` is anyOf and `Type.Optional` only
// relaxes the key's presence.

const (
	// notesBindingType is NOTES_BINDING (note-scopes.ts:7).
	notesBindingType = "posthorse-notes-scope"
	// notesTouchType is NOTES_TOUCH (note-scopes.ts:8).
	notesTouchType = "posthorse-note-touch"
)

// noteError is an Error with a `cause` (new Error(message, { cause })).
type noteError struct {
	msg   string
	cause error
}

func (e *noteError) Error() string { return e.msg }
func (e *noteError) Unwrap() error { return e.cause }

func noteErrorf(cause error, msg string) error { return &noteError{msg: msg, cause: cause} }

// noteMissing is `error.code === "ENOENT" || error.code === "ENOTDIR"`.
func noteMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// noteOwner is NoteOwner (note-scopes.ts:9).
type noteOwner struct{ id, timestamp string }

// noteScope is NoteScope (note-scopes.ts:10). origin carries what the original's object spread
// `{ ...binding, path }` copies into an address when the scope object is a whole binding
// (addressOf, resolveReference, SessionNotes.read): version, kind, owner, ... end up in the
// serialized address. It is nil for a plain { root, scope }. A scope read back from a journal or a
// manifest keeps the leaked binding as its decoded map in leak instead and parses it on demand.
type noteScope struct {
	root, scope string
	origin      *noteBinding
	leak        map[string]any
}

// leaked is the binding the scope object spread, if any.
func (s noteScope) leaked() *noteBinding {
	if s.origin != nil {
		return s.origin
	}
	if s.leak != nil {
		if b, ok := checkBinding(s.leak); ok {
			return b
		}
	}
	return nil
}

// noteAddress is NoteAddress (note-scopes.ts:11).
type noteAddress struct {
	noteScope
	path string
}

// noteIdentity is NoteIdentity (note-scopes.ts:12-18).
type noteIdentity struct{ size, mtimeMs, ctimeMs, ino, dev float64 }

// noteSeed is NoteSeed (note-scopes.ts:19-26). The serialized key order is the one the original
// writes (note-snapshots.ts:127, `{ ...note, digest, identity }`).
type noteSeed struct {
	path     string
	source   noteAddress
	lineage  noteAddress
	digest   string
	identity noteIdentity
	anchors  []string
}

// noteBinding is NoteBinding (note-scopes.ts:27-42). version is always 1. kind is "project" or
// "fork"; source, seeds and absent only exist for a fork.
type noteBinding struct {
	kind        string
	owner       noteOwner
	root, scope string
	source      *noteScope
	seeds       []noteSeed
	absent      []string
}

// noteTouchFile is one element of NoteTouchRecord.files (note-scopes.ts:47-61).
type noteTouchFile struct {
	noteAddress
	seed *noteSeed
	kind string // "read" | "write" | "clear" | "delete" | "adopt"
}

// noteTouchRecord is NoteTouchRecord (note-scopes.ts:43-62).
type noteTouchRecord struct {
	owner          noteOwner
	scope          noteScope
	rootToolCallId string
	toolCallId     string
	files          []noteTouchFile
}

// scopeLike is anything the original passes as a NoteScope: a scope, an address or a binding.
type scopeLike interface{ scopeOf() noteScope }

func (s noteScope) scopeOf() noteScope { return s }
func (b *noteBinding) scopeOf() noteScope {
	return noteScope{root: b.root, scope: b.scope, origin: b}
}

// plain is the { root, scope } object the original builds explicitly.
func (s noteScope) plain() noteScope { return noteScope{root: s.root, scope: s.scope} }

// withPath is `{ ...scope, path }`.
func withPath[S scopeLike](scope S, path string) noteAddress {
	return noteAddress{noteScope: scope.scopeOf(), path: path}
}

// sameOwner: note-scopes.ts:77.
func sameOwner(left, right noteOwner) bool {
	return left.id == right.id && left.timestamp == right.timestamp
}

// nodeIsAbsolute is path.isAbsolute of the host platform.
func nodeIsAbsolute(p string) bool { return hostStyle.isAbsolute(p) }

// nodeRelative is path.relative of the host platform (note_nodepath.go).
func nodeRelative(from, to string) string { return jsRelative(from, to) }

// noteURIComponent is encodeURIComponent; a lone surrogate throws a URIError.
func noteURIComponent(s string) (string, error) {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '!', c == '~', c == '*', c == '\'', c == '(', c == ')':
			b.WriteByte(c)
		default:
			if c >= 0x80 && isWTF8Surrogate(s, i) {
				return "", errors.New("URI malformed")
			}
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String(), nil
}

// noteDateMs is Date.parse(text) finite: the ISO form Pi writes is decided without the host, any
// other text is answered by V8 through the host (sdk.ParseDate).
func noteDateMs(text string) (float64, bool) {
	ms, ok, err := sdk.ParseDate(text)
	if err != nil || !ok {
		return 0, false
	}
	return ms, true
}

// noteISOString is Date.prototype.toISOString for a finite time value.
func noteISOString(ms float64) string {
	days := int64(math.Floor(ms / 86400000))
	rest := int64(ms - float64(days)*86400000)
	// civil-from-days (proleptic Gregorian).
	z := days + 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	year := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day := doy - (153*mp+2)/5 + 1
	month := mp + 3
	if month > 12 {
		month -= 12
	}
	if month <= 2 {
		year++
	}
	pad := func(n int64, width int) string {
		s := strconv.FormatInt(n, 10)
		return strings.Repeat("0", max(0, width-len(s))) + s
	}
	var y string
	switch {
	case year >= 0 && year <= 9999:
		y = pad(year, 4)
	case year < 0:
		y = "-" + pad(-year, 6)
	default:
		y = "+" + pad(year, 6)
	}
	return y + "-" + pad(month, 2) + "-" + pad(day, 2) + "T" + pad(rest/3600000, 2) + ":" +
		pad(rest/60000%60, 2) + ":" + pad(rest/1000%60, 2) + "." + pad(rest%1000, 3) + "Z"
}

// forkScope: note-scopes.ts:80.
func forkScope(owner noteOwner) (string, error) {
	ms, ok := noteDateMs(owner.timestamp)
	if !ok {
		return "", errors.New("Invalid time value")
	}
	iso := strings.NewReplacer(":", "-", ".", "-").Replace(noteISOString(ms))
	id, err := noteURIComponent(owner.id)
	if err != nil {
		return "", err
	}
	return ".forks/" + iso + "_" + id + "/notes", nil
}

// scopeDirectory: note-scopes.ts:83.
func scopeDirectory[S scopeLike](scope S) string {
	s := scope.scopeOf()
	return filepath.Join(s.root, s.scope)
}

// sameScope: note-scopes.ts:86.
func sameScope[A, B scopeLike](left A, right B) bool {
	l, r := left.scopeOf(), right.scopeOf()
	return l.root == r.root && l.scope == r.scope
}

// inside: note-scopes.ts:89.
func inside(directory, path string) bool {
	part := nodeRelative(directory, path)
	return part == "" || (part != ".." && !strings.HasPrefix(part, ".."+string(hostStyle.sepByte())) && !nodeIsAbsolute(part))
}

// noteRelative: note-scopes.ts:93.
func noteRelative(path string) (string, error) {
	if !hostStyle.win32 {
		return noteRelativePosix(path)
	}
	return noteRelativeFor(win32Style, hostCwd(), path)
}

func noteRelativeErr(path string, managed bool) error {
	if managed {
		return errors.New("Invalid note path " + jsQuote(path) + ": must stay outside managed .forks paths and inside its scope.")
	}
	return errors.New("Invalid note path " + jsQuote(path) + ": use a relative path.")
}

// noteRelativePosix is noteRelativeFor(posixStyle, ...) without the allocation of resolve and relative:
// on posix `relative(resolve("/"), resolve("/", path))` is the lexically cleaned path.
func noteRelativePosix(path string) (string, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.IndexByte(path, 0) >= 0 {
		return "", noteRelativeErr(path, false)
	}
	normalized := strings.TrimPrefix(filepath.Clean("/"+path), "/")
	if normalized == "." {
		normalized = ""
	}
	// The `..` segment test is on the raw text with either separator (note-scopes.ts:99).
	dotdot := false
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			dotdot = true
			break
		}
	}
	first, _, _ := strings.Cut(normalized, "/")
	if dotdot || normalized == "" || first == ".forks" {
		return "", noteRelativeErr(path, true)
	}
	return normalized, nil
}

// noteRelativeFor is note-scopes.ts:93-107 over a path flavour: `relative(resolve("/"), resolve("/", path))`.
// A drive-relative name such as `C:x.md` resolves to `C:\x.md` on win32, so its relative path is `x.md`.
func noteRelativeFor(style nodeStyle, cwd, path string) (string, error) {
	if path == "" || style.isAbsolute(path) || strings.IndexByte(path, 0) >= 0 {
		return "", noteRelativeErr(path, false)
	}
	normalized := style.relative(cwd, style.resolve(cwd, "/"), style.resolve(cwd, "/", path))
	dotdot := false
	for _, seg := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			dotdot = true
			break
		}
	}
	first, _, _ := strings.Cut(normalized, string(style.sepByte()))
	if dotdot || normalized == "" || first == ".forks" {
		return "", noteRelativeErr(path, true)
	}
	return normalized, nil
}

// jsQuote is JSON.stringify(string).
func jsQuote(s string) string { return string(sdkjson.AppendStringifyString(nil, s)) }

// canonicalPath: note-scopes.ts:110. Existing ancestors resolve too, so aliases to files not created
// yet have one owner. realpathSync is the JS implementation (note_nodepath.go): `..` is removed
// lexically first and an error names the component Node stats.
func canonicalPath(path string) (string, error) {
	absolute := path
	if !nodeIsAbsolute(path) {
		absolute = hostCwd() + string(hostStyle.sepByte()) + path
	}
	real, err := nodeRealpath(absolute)
	if err == nil {
		return real, nil
	}
	if !noteMissing(err) {
		return "", err
	}
	parent := jsDirname(absolute)
	if parent == absolute {
		return absolute, nil
	}
	canon, err := canonicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(canon, nodeRelative(parent, absolute)), nil
}

// notePath: note-scopes.ts:124.
func notePath[S scopeLike](scope S, path string) (string, error) {
	s := scope.scopeOf()
	directory := scopeDirectory(s)
	rel, err := noteRelative(path)
	if err != nil {
		return "", err
	}
	target := filepath.Join(directory, rel)
	canonical, err := canonicalPath(target)
	if err != nil {
		return "", err
	}
	escapesPrivate := false
	if s.scope != "" {
		canonDir, err := canonicalPath(directory)
		if err != nil {
			return "", err
		}
		escapesPrivate = !inside(canonDir, canonical)
	}
	entersManaged := false
	if s.scope == "" {
		forks, err := canonicalPath(filepath.Join(s.root, ".forks"))
		if err != nil {
			return "", err
		}
		entersManaged = inside(forks, canonical)
	}
	if escapesPrivate || entersManaged {
		return "", errors.New("Note path leaves its scope through a filesystem alias: " + target)
	}
	return target, nil
}

// validScope: note-scopes.ts:136.
func validScope[S scopeLike](scope S) bool {
	s := scope.scopeOf()
	return nodeIsAbsolute(s.root) && (s.scope == "" || privateScope(s.scope))
}

// privateScope is /^\.forks\/[^/\\]+\/notes$/u.
func privateScope(s string) bool {
	const head, tail = ".forks/", "/notes"
	if len(s) < len(head)+1+len(tail) || !strings.HasPrefix(s, head) || !strings.HasSuffix(s, tail) {
		return false
	}
	return !strings.ContainsAny(s[len(head):len(s)-len(tail)], "/\\")
}

// validNoteName: note-scopes.ts:155.
func validNoteName(path string) bool {
	rel, err := noteRelative(path)
	return err == nil && rel == path
}

func validAddressPath(a noteAddress) bool { return validNoteName(a.path) }

// validSeed: note-scopes.ts:142.
func validSeed(seed noteSeed) bool {
	return validNoteName(seed.path) && validScope(seed.source) && validScope(seed.lineage) &&
		validNoteName(seed.source.path) && validNoteName(seed.lineage.path)
}

// validFork: note-scopes.ts:162.
func validFork(b *noteBinding) bool {
	want, err := forkScope(b.owner)
	if err != nil || b.scope != want {
		return false
	}
	if b.source != nil && !validScope(*b.source) {
		return false
	}
	for _, seed := range b.seeds {
		if !validSeed(seed) {
			return false
		}
	}
	for _, name := range b.absent {
		if !validNoteName(name) {
			return false
		}
	}
	return true
}

// ---- TypeBox Check of the schemas (note-scopes.ts:9-62) -------------------------------------

func jsObject(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func jsFinite(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case int32:
		f = float64(n)
	case uint64:
		f = float64(n)
	case uint32:
		f = float64(n)
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func keyString(m map[string]any, key string) (string, bool) {
	s, ok := m[key].(string)
	return s, ok
}

func checkOwner(v any) (noteOwner, bool) {
	m, ok := jsObject(v)
	if !ok {
		return noteOwner{}, false
	}
	id, ok1 := keyString(m, "id")
	ts, ok2 := keyString(m, "timestamp")
	return noteOwner{id, ts}, ok1 && ok2
}

// checkScope is Check(scopeSchema). A whole binding spread into the object is kept as the decoded map
// (noteScope.leak) and parsed only when the scope is written: TypeBox's Check visits the declared
// members only, so a fork of a fork must not re-validate the parent binding behind every seed.
func checkScope(v any) (noteScope, bool) {
	m, ok := jsObject(v)
	if !ok {
		return noteScope{}, false
	}
	root, ok1 := keyString(m, "root")
	scope, ok2 := keyString(m, "scope")
	if !ok1 || !ok2 {
		return noteScope{}, false
	}
	s := noteScope{root: root, scope: scope}
	if _, leaked := m["kind"]; leaked {
		s.leak = m
	}
	return s, true
}

// checkAddress is Check(addressSchema).
func checkAddress(v any) (noteAddress, bool) {
	s, ok := checkScope(v)
	if !ok {
		return noteAddress{}, false
	}
	path, ok := keyString(v.(map[string]any), "path")
	return noteAddress{noteScope: s, path: path}, ok
}

func checkIdentity(v any) (noteIdentity, bool) {
	m, ok := jsObject(v)
	if !ok {
		return noteIdentity{}, false
	}
	var out noteIdentity
	for _, f := range []struct {
		key string
		dst *float64
	}{{"size", &out.size}, {"mtimeMs", &out.mtimeMs}, {"ctimeMs", &out.ctimeMs}, {"ino", &out.ino}, {"dev", &out.dev}} {
		n, ok := jsFinite(m[f.key])
		if !ok {
			return noteIdentity{}, false
		}
		*f.dst = n
	}
	return out, true
}

func checkStrings(v any) ([]string, bool) {
	switch a := v.(type) {
	case []string:
		return a, true
	case []any:
		out := make([]string, len(a))
		for i, item := range a {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

func checkSeed(v any) (noteSeed, bool) {
	m, ok := jsObject(v)
	if !ok {
		return noteSeed{}, false
	}
	var s noteSeed
	var ok1, ok2, ok3, ok4, ok5, ok6 bool
	s.path, ok1 = keyString(m, "path")
	s.source, ok2 = checkAddress(m["source"])
	s.lineage, ok3 = checkAddress(m["lineage"])
	s.digest, ok4 = keyString(m, "digest")
	s.identity, ok5 = checkIdentity(m["identity"])
	s.anchors, ok6 = checkStrings(m["anchors"])
	return s, ok1 && ok2 && ok3 && ok4 && ok5 && ok6
}

func checkSeeds(v any) ([]noteSeed, bool) {
	a, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]noteSeed, len(a))
	for i, item := range a {
		s, ok := checkSeed(item)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// checkBinding is Check(bindingSchema): the union of the project and the fork object.
func checkBinding(v any) (*noteBinding, bool) {
	m, ok := jsObject(v)
	if !ok {
		return nil, false
	}
	if version, ok := jsFinite(m["version"]); !ok || version != 1 {
		return nil, false
	}
	owner, ok1 := checkOwner(m["owner"])
	root, ok2 := keyString(m, "root")
	kind, ok3 := keyString(m, "kind")
	scope, ok4 := keyString(m, "scope")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, false
	}
	b := &noteBinding{kind: kind, owner: owner, root: root, scope: scope}
	switch kind {
	case "project":
		return b, scope == ""
	case "fork":
		source, present := m["source"]
		if !present {
			return nil, false
		}
		if source != nil {
			s, ok := checkScope(source)
			if !ok {
				return nil, false
			}
			b.source = &s
		}
		var okSeeds, okAbsent bool
		b.seeds, okSeeds = checkSeeds(m["seeds"])
		b.absent, okAbsent = checkStrings(m["absent"])
		return b, okSeeds && okAbsent
	}
	return nil, false
}

// parseBinding: note-scopes.ts:170. nil is `undefined`.
func parseBinding(value any) *noteBinding {
	b, ok := checkBinding(value)
	if !ok || !validScope(b) || b.owner.id == "" {
		return nil
	}
	if _, ok := noteDateMs(b.owner.timestamp); !ok {
		return nil
	}
	if b.kind == "fork" && !validFork(b) {
		return nil
	}
	return b
}

// bindingsOf: note-scopes.ts:184.
func bindingsOf(entries []rec) []*noteBinding {
	var out []*noteBinding
	for _, entry := range entries {
		if t, _ := entry["type"].(string); t != "custom" {
			continue
		}
		if c, _ := entry["customType"].(string); c != notesBindingType {
			continue
		}
		if b := parseBinding(entry["data"]); b != nil {
			out = append(out, b)
		}
	}
	return out
}

var touchKinds = map[string]bool{"read": true, "write": true, "clear": true, "delete": true, "adopt": true}

func checkTouch(v any) (noteTouchRecord, bool) {
	m, ok := jsObject(v)
	if !ok {
		return noteTouchRecord{}, false
	}
	if version, ok := jsFinite(m["version"]); !ok || version != 1 {
		return noteTouchRecord{}, false
	}
	var t noteTouchRecord
	var ok1, ok2, ok3, ok4 bool
	t.owner, ok1 = checkOwner(m["owner"])
	t.scope, ok2 = checkScope(m["scope"])
	t.rootToolCallId, ok3 = keyString(m, "rootToolCallId")
	t.toolCallId, ok4 = keyString(m, "toolCallId")
	files, ok5 := m["files"].([]any)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return noteTouchRecord{}, false
	}
	t.files = make([]noteTouchFile, len(files))
	for i, item := range files {
		fm, ok := jsObject(item)
		if !ok {
			return noteTouchRecord{}, false
		}
		addr, ok := checkAddress(item)
		kind, okKind := keyString(fm, "kind")
		if !ok || !okKind || !touchKinds[kind] {
			return noteTouchRecord{}, false
		}
		f := noteTouchFile{noteAddress: addr, kind: kind}
		if raw, present := fm["seed"]; present {
			seed, ok := checkSeed(raw)
			if !ok {
				return noteTouchRecord{}, false
			}
			f.seed = &seed
		}
		t.files[i] = f
	}
	return t, true
}

// touchRecords: note-scopes.ts:193.
func touchRecords(entries []rec) []noteTouchRecord {
	var out []noteTouchRecord
	for _, entry := range entries {
		if t, _ := entry["type"].(string); t != "custom" {
			continue
		}
		if c, _ := entry["customType"].(string); c != notesTouchType {
			continue
		}
		record, ok := checkTouch(entry["data"])
		if !ok || !validScope(record.scope) {
			continue
		}
		valid := true
		for _, file := range record.files {
			if !(validScope(file) && validNoteName(file.path) && (file.seed == nil || validSeed(*file.seed))) {
				valid = false
				break
			}
		}
		if valid {
			out = append(out, record)
		}
	}
	return out
}

// seedsOf: note-scopes.ts:217. Map semantics: a later seed replaces an earlier one in place.
func seedsOf(binding *noteBinding, entries []rec) []noteSeed {
	var seeds []noteSeed
	index := map[string]int{}
	// The Map is keyed by the touch file's `path` for a touch (`seeds.set(file.path, file.seed)`,
	// note-scopes.ts:226) and by the seed's own path for the binding's seeds.
	set := func(key string, seed noteSeed) {
		if i, ok := index[key]; ok {
			seeds[i] = seed
			return
		}
		index[key] = len(seeds)
		seeds = append(seeds, seed)
	}
	if binding.kind == "fork" {
		for _, seed := range binding.seeds {
			set(seed.path, seed)
		}
	}
	for _, record := range touchRecords(entries) {
		if !sameOwner(record.owner, binding.owner) {
			continue
		}
		for _, file := range record.files {
			if file.seed != nil && sameScope(file, binding) {
				set(file.path, *file.seed)
			}
		}
	}
	return seeds
}

// ownedPaths: note-scopes.ts:233.
func ownedPaths(binding *noteBinding, entries []rec) map[string]struct{} {
	paths := map[string]struct{}{}
	if binding.kind == "fork" {
		for _, p := range binding.absent {
			paths[p] = struct{}{}
		}
		for _, seed := range binding.seeds {
			paths[seed.path] = struct{}{}
		}
	}
	for _, record := range touchRecords(entries) {
		if !sameOwner(record.owner, binding.owner) {
			continue
		}
		for _, file := range record.files {
			if file.kind != "read" && sameScope(binding, file) {
				paths[file.path] = struct{}{}
			}
		}
	}
	return paths
}

// sortNoteDirectories is the `directories.toSorted((a, b) => b.length - a.length)` of matchNoteDirectory
// (note-scopes.ts:257): a copy, longest first by UTF-16 length, ties in the given order. A scan that matches
// many paths against one directory set sorts once and calls matchSortedNoteDirectory.
func sortNoteDirectories(directories []string) []string {
	sorted := make([]string, len(directories))
	copy(sorted, directories)
	lengths := make(map[string]int, len(sorted))
	for _, d := range sorted {
		lengths[d] = u16len(d)
	}
	sort.SliceStable(sorted, func(i, j int) bool { return lengths[sorted[i]] > lengths[sorted[j]] })
	return sorted
}

// matchNoteDirectory: note-scopes.ts:252. Pure longest-prefix matching is also used for historical
// paths on unavailable filesystems.
func matchNoteDirectory(path string, directories []string) (directory, rel string, ok bool) {
	return matchSortedNoteDirectory(path, sortNoteDirectories(directories))
}

// matchSortedNoteDirectory is the loop of matchNoteDirectory over directories already sorted by
// sortNoteDirectories.
func matchSortedNoteDirectory(path string, sorted []string) (directory, rel string, ok bool) {
	slash := strings.ReplaceAll(path, "\\", "/")
	for _, d := range sorted {
		normalized := strings.TrimRight(strings.ReplaceAll(d, "\\", "/"), "/")
		part := ""
		if strings.HasPrefix(slash, normalized+"/") {
			part = slash[len(normalized)+1:]
		}
		if part != "" && part != ".forks" && !strings.HasPrefix(part, ".forks/") {
			return d, part, true
		}
	}
	return "", "", false
}

// scopeIndex resolves addresses against one scope list: the canonical directory of every scope is
// computed once (addressOf does it per call in the original; the files do not change inside one
// synchronous scan).
type scopeIndex struct {
	scopes      []noteScope
	directories []string
	sorted      []string
	ready       bool
	forks       map[string]string
}

func newScopeIndex(scopes []noteScope) *scopeIndex { return &scopeIndex{scopes: scopes} }

// resolve computes the canonical directories on first use, as addressOf does inside the call.
func (idx *scopeIndex) resolve() error {
	if idx.ready {
		return nil
	}
	dirs := make([]string, len(idx.scopes))
	for i, scope := range idx.scopes {
		dir, err := canonicalPath(scopeDirectory(scope))
		if err != nil {
			return err
		}
		dirs[i] = dir
	}
	idx.directories, idx.sorted, idx.ready = dirs, sortNoteDirectories(dirs), true
	return nil
}

func (idx *scopeIndex) canonicalForks(root string) (string, error) {
	if dir, ok := idx.forks[root]; ok {
		return dir, nil
	}
	dir, err := canonicalPath(filepath.Join(root, ".forks"))
	if err != nil {
		return "", err
	}
	if idx.forks == nil {
		idx.forks = map[string]string{}
	}
	idx.forks[root] = dir
	return dir, nil
}

// addressOf: note-scopes.ts:266.
func (idx *scopeIndex) addressOf(path string) (noteAddress, bool, error) {
	if err := idx.resolve(); err != nil {
		return noteAddress{}, false, err
	}
	canonical, err := canonicalPath(path)
	if err != nil {
		return noteAddress{}, false, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return noteAddress{}, false, err
	}
	for _, target := range [2]string{canonical, abs} {
		if dir, rel, ok := matchSortedNoteDirectory(target, idx.sorted); ok {
			for i, candidate := range idx.directories {
				if candidate == dir {
					return withPath(idx.scopes[i], rel), true, nil
				}
			}
		}
		for _, scope := range idx.scopes {
			if scope.scope != "" {
				continue
			}
			forks, err := idx.canonicalForks(scope.root)
			if err != nil {
				return noteAddress{}, false, err
			}
			if inside(forks, target) {
				return noteAddress{}, false, nil
			}
		}
	}
	return noteAddress{}, false, nil
}

// addressOf: note-scopes.ts:266.
func addressOf(path string, scopes []noteScope) (noteAddress, bool, error) {
	return newScopeIndex(scopes).addressOf(path)
}

// ---- serialization (JSON.stringify key order of the original) --------------------------------

func appendKey(dst []byte, key string, first bool) []byte {
	if !first {
		dst = append(dst, ',')
	}
	dst = sdkjson.AppendStringifyString(dst, key)
	return append(dst, ':')
}

func appendStr(dst []byte, s string) []byte { return sdkjson.AppendStringifyString(dst, s) }

func appendNum(dst []byte, f float64) []byte {
	out, err := sdkjson.AppendStringify(dst, f)
	if err != nil {
		return append(dst, "null"...)
	}
	return out
}

func appendStrings(dst []byte, items []string) []byte {
	dst = append(dst, '[')
	for i, s := range items {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendStr(dst, s)
	}
	return append(dst, ']')
}

func (o noteOwner) appendJSON(dst []byte) []byte {
	dst = append(dst, '{')
	dst = appendKey(dst, "id", true)
	dst = appendStr(dst, o.id)
	dst = appendKey(dst, "timestamp", false)
	dst = appendStr(dst, o.timestamp)
	return append(dst, '}')
}

func (i noteIdentity) appendJSON(dst []byte) []byte {
	dst = append(dst, '{')
	dst = appendKey(dst, "size", true)
	dst = appendNum(dst, i.size)
	dst = appendKey(dst, "mtimeMs", false)
	dst = appendNum(dst, i.mtimeMs)
	dst = appendKey(dst, "ctimeMs", false)
	dst = appendNum(dst, i.ctimeMs)
	dst = appendKey(dst, "ino", false)
	dst = appendNum(dst, i.ino)
	dst = appendKey(dst, "dev", false)
	dst = appendNum(dst, i.dev)
	return append(dst, '}')
}

// appendJSON writes `{ ...scope }`: the leaked binding when there is one, else { root, scope }.
func (s noteScope) appendJSON(dst []byte) []byte {
	if b := s.leaked(); b != nil {
		return b.appendJSON(dst)
	}
	dst = append(dst, '{')
	dst = appendKey(dst, "root", true)
	dst = appendStr(dst, s.root)
	dst = appendKey(dst, "scope", false)
	dst = appendStr(dst, s.scope)
	return append(dst, '}')
}

// appendJSON writes `{ ...scope, path }`.
func (a noteAddress) appendJSON(dst []byte) []byte {
	if b := a.leaked(); b != nil {
		dst = b.appendJSON(dst)
		dst = dst[:len(dst)-1]
		dst = appendKey(dst, "path", false)
		dst = appendStr(dst, a.path)
		return append(dst, '}')
	}
	dst = append(dst, '{')
	dst = appendKey(dst, "root", true)
	dst = appendStr(dst, a.root)
	dst = appendKey(dst, "scope", false)
	dst = appendStr(dst, a.scope)
	dst = appendKey(dst, "path", false)
	dst = appendStr(dst, a.path)
	return append(dst, '}')
}

func (s noteSeed) appendJSON(dst []byte) []byte {
	dst = append(dst, '{')
	dst = appendKey(dst, "path", true)
	dst = appendStr(dst, s.path)
	dst = appendKey(dst, "source", false)
	dst = s.source.appendJSON(dst)
	dst = appendKey(dst, "lineage", false)
	dst = s.lineage.appendJSON(dst)
	dst = appendKey(dst, "anchors", false)
	dst = appendStrings(dst, s.anchors)
	dst = appendKey(dst, "digest", false)
	dst = appendStr(dst, s.digest)
	dst = appendKey(dst, "identity", false)
	dst = s.identity.appendJSON(dst)
	return append(dst, '}')
}

// appendJSON writes the binding in the key order the original builds it in (note-provenance.ts:
// `{ version, kind, owner, root, scope, source, seeds, absent }`).
func (b *noteBinding) appendJSON(dst []byte) []byte { return b.appendJSONKind(dst, b.kind) }

// appendJSONKind is appendJSON with the `kind` member holding kind: `{ ...binding, kind }` overwrites the
// binding's own member in place.
func (b *noteBinding) appendJSONKind(dst []byte, kind string) []byte {
	dst = append(dst, `{"version":1`...)
	dst = appendKey(dst, "kind", false)
	dst = appendStr(dst, kind)
	dst = appendKey(dst, "owner", false)
	dst = b.owner.appendJSON(dst)
	dst = appendKey(dst, "root", false)
	dst = appendStr(dst, b.root)
	dst = appendKey(dst, "scope", false)
	dst = appendStr(dst, b.scope)
	if b.kind == "fork" {
		dst = appendKey(dst, "source", false)
		if b.source == nil {
			dst = append(dst, "null"...)
		} else {
			dst = b.source.appendJSON(dst)
		}
		dst = appendKey(dst, "seeds", false)
		dst = append(dst, '[')
		for i, seed := range b.seeds {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = seed.appendJSON(dst)
		}
		dst = append(dst, ']')
		dst = appendKey(dst, "absent", false)
		dst = appendStrings(dst, b.absent)
	}
	return append(dst, '}')
}

// appendJSON writes `{ ...address, kind, ...(seed) }` (note-store.ts:106 and the guards' recordedFiles): when the
// address spread a whole binding, that binding's own `kind` member takes the touch kind in its place.
func (f noteTouchFile) appendJSON(dst []byte) []byte {
	if b := f.leaked(); b != nil {
		dst = b.appendJSONKind(dst, f.kind)
		dst = dst[:len(dst)-1]
		dst = appendKey(dst, "path", false)
		dst = appendStr(dst, f.path)
	} else {
		dst = f.noteAddress.appendJSON(dst)
		dst = dst[:len(dst)-1]
		dst = appendKey(dst, "kind", false)
		dst = appendStr(dst, f.kind)
	}
	if f.seed != nil {
		dst = appendKey(dst, "seed", false)
		dst = f.seed.appendJSON(dst)
	}
	return append(dst, '}')
}

func (t noteTouchRecord) appendJSON(dst []byte) []byte {
	dst = append(dst, `{"version":1`...)
	dst = appendKey(dst, "owner", false)
	dst = t.owner.appendJSON(dst)
	dst = appendKey(dst, "scope", false)
	dst = t.scope.plain().appendJSON(dst)
	dst = appendKey(dst, "rootToolCallId", false)
	dst = appendStr(dst, t.rootToolCallId)
	dst = appendKey(dst, "toolCallId", false)
	dst = appendStr(dst, t.toolCallId)
	dst = appendKey(dst, "files", false)
	dst = append(dst, '[')
	for i, f := range t.files {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = f.appendJSON(dst)
	}
	dst = append(dst, ']')
	return append(dst, '}')
}

// The json.Marshaler / StringifyAppender faces: what pi.appendEntry and JSON.stringify serialize.
func (o noteOwner) MarshalJSON() ([]byte, error)       { return o.appendJSON(nil), nil }
func (s noteScope) MarshalJSON() ([]byte, error)       { return s.appendJSON(nil), nil }
func (a noteAddress) MarshalJSON() ([]byte, error)     { return a.appendJSON(nil), nil }
func (s noteSeed) MarshalJSON() ([]byte, error)        { return s.appendJSON(nil), nil }
func (b *noteBinding) MarshalJSON() ([]byte, error)    { return b.appendJSON(nil), nil }
func (f noteTouchFile) MarshalJSON() ([]byte, error)   { return f.appendJSON(nil), nil }
func (t noteTouchRecord) MarshalJSON() ([]byte, error) { return t.appendJSON(nil), nil }

func (o noteOwner) AppendStringify(dst []byte) ([]byte, error)       { return o.appendJSON(dst), nil }
func (s noteScope) AppendStringify(dst []byte) ([]byte, error)       { return s.appendJSON(dst), nil }
func (a noteAddress) AppendStringify(dst []byte) ([]byte, error)     { return a.appendJSON(dst), nil }
func (s noteSeed) AppendStringify(dst []byte) ([]byte, error)        { return s.appendJSON(dst), nil }
func (b *noteBinding) AppendStringify(dst []byte) ([]byte, error)    { return b.appendJSON(dst), nil }
func (f noteTouchFile) AppendStringify(dst []byte) ([]byte, error)   { return f.appendJSON(dst), nil }
func (t noteTouchRecord) AppendStringify(dst []byte) ([]byte, error) { return t.appendJSON(dst), nil }
