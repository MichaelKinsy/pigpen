package pitypesafe

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Registry names of the judgment backends.
const (
	BackendTypeSafe    = "typesafe"
	BackendOpenRouter  = "openrouter"
	BackendCommandCode = "commandcode"
	// BackendOwnModel answers with the model PiG is configured with instead of sending the
	// content to a TypeSafe host: the second backend of the shared client (ownmodel).
	BackendOwnModel = "ownmodel"
)

// DefaultBackend is the backend every key and auth function assumes when none is named.
const DefaultBackend = BackendTypeSafe

const typesafeKeyEnv = "TYPESAFE_API_KEY"

// BackendConfig describes one judgment backend.
type BackendConfig struct {
	// Label is the human name for status lines: "TypeSafe", "OpenRouter", "Command Code".
	Label string
	Host  string
	// KeyEnv is the environment variable that carries this backend's key. Empty means the TypeSafe key resolution applies.
	KeyEnv string
	// Path is the request path, when the backend does not serve the SDK's own /v1/systemone.
	Path string
	// ModelsPath is the model list path, when the backend does not serve the SDK's own /v1/models.
	ModelsPath string
	// ModelsField is the field the model list arrives in, when it is not the SDK's own "models".
	ModelsField string
	// ModelsIDField is the entry field carrying the id callers pass as model, when the SDK's own "name" is only a label.
	ModelsIDField string
	// ModelsVerifyKey says whether the model list checks the key. A public list accepts any key, so it proves nothing. Nil means it does.
	ModelsVerifyKey *bool
	// Local marks a backend that sends nothing to a TypeSafe host (the own-model backend).
	Local bool
}

// BackendEndpoint is a caller-supplied endpoint that serves the Jev decisions protocol. It is passed per
// call and never added to the registry.
type BackendEndpoint struct {
	BackendConfig
	// DefaultModel is sent when the caller names no model. Without it, a model must be passed to New.
	DefaultModel string
}

// ResolvedBackend is a backend resolved and validated: what the client will actually use.
type ResolvedBackend struct {
	BackendConfig
	// Name is the registry name; empty for a caller-supplied endpoint.
	Name string
	// DefaultModel is the model id sent when the caller names none, already in the backend's form; empty when the endpoint names none.
	DefaultModel string
	// ModelsVerifyKey is always explicit after resolution.
	ModelsVerifyKey bool
}

func boolPtr(b bool) *bool { return &b }

// DecisionsBackends is the registry of known judgment backends.
var DecisionsBackends = map[string]BackendConfig{
	BackendTypeSafe: {Label: "TypeSafe", Host: "https://api.typesafe.ai", KeyEnv: typesafeKeyEnv},
	BackendOpenRouter: {
		Label: "OpenRouter", Host: "https://openrouter.ai", KeyEnv: "OPENROUTER_API_KEY",
		Path: "/api/alpha/decisions", ModelsPath: "/api/v1/models", ModelsField: "data", ModelsIDField: "id",
		ModelsVerifyKey: boolPtr(false),
	},
	BackendCommandCode: {
		Label: "Command Code", Host: "https://api.commandcode.ai", KeyEnv: "COMMANDCODE_API_KEY",
		Path: "/provider/v1/systemone", ModelsPath: "/provider/v1/models", ModelsField: "data", ModelsIDField: "id",
		ModelsVerifyKey: boolPtr(false),
	},
}

// ownModelConfig is the own-model backend: no host, no key, nothing leaves for a TypeSafe service.
var ownModelConfig = BackendConfig{Label: "Own model", Local: true}

func registryNames() string {
	names := []string{BackendTypeSafe, BackendOpenRouter, BackendCommandCode, BackendOwnModel}
	return strings.Join(names, ", ")
}

// defaultModels are each backend's default model id as the caller writes it, before mapping.
var defaultModels = map[string]string{
	BackendTypeSafe:    "jev-latest",
	BackendOpenRouter:  "typesafe/jev-1.13",
	BackendCommandCode: "typesafe/jev",
}

var jevVersion = regexp.MustCompile(`^jev-(\d+)\.(\d+)(?:\.\d+)?$`)

// BackendModelID is the model id to send for a caller's model on this registry backend. OpenRouter routes a
// bare Jev id under the typesafe author: jev-latest becomes ~typesafe/jev-latest, and a bare
// jev-<major>.<minor> (with or without an optional .<patch>) becomes typesafe/jev-<major>.<minor>. An id that
// already carries an author, a bare id this rule does not know, and every model on a backend without a mapping
// pass through unchanged. Mapping an already mapped id changes nothing.
func BackendModelID(backend, model string) string {
	if strings.Contains(model, "/") || backend != BackendOpenRouter {
		return model
	}
	if model == "jev-latest" {
		return "~typesafe/jev-latest"
	}
	if m := jevVersion.FindStringSubmatch(model); m != nil {
		return "typesafe/jev-" + m[1] + "." + m[2]
	}
	return model
}

// DefaultModelID is the model a client sends when the caller names none, in the form that backend accepts.
func DefaultModelID(backend string) string {
	return BackendModelID(backend, defaultModels[backend])
}

// UsesTypeSafeKey says whether a backend's key comes from the TypeSafe resolution (TYPESAFE_API_KEY, then
// the login store) or only from its own environment variable.
func UsesTypeSafeKey(b BackendConfig) bool {
	return !b.Local && (b.KeyEnv == "" || b.KeyEnv == typesafeKeyEnv)
}

const keyEnvMessage = "Backend keyEnv must name an environment variable: letters, digits, and underscores, not starting with a digit."
const hostMessage = "Backend host must be an absolute https: URL with no user info, path, query, or fragment (http: is allowed only for localhost, 127.0.0.0/8, and [::1])."

var (
	keyEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	loopbackV4    = regexp.MustCompile(`^127(?:\.\d{1,3}){3}$`)
)

func refuse(message string) error { return newError(CodeConfiguration, message) }

func hasRawQueryOrFragment(v string) bool { return strings.ContainsAny(v, "?#") }

// utf16Len is the JavaScript string length of s.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// endpointFields converts a caller-supplied endpoint to the field map the validation reads, so a struct and a
// map (from a settings file, say) meet the same rules, including the type rules a struct cannot break.
func endpointFields(spec any) (map[string]any, bool) {
	switch t := spec.(type) {
	case map[string]any:
		return t, true
	case BackendEndpoint:
		return endpointToMap(t), true
	case *BackendEndpoint:
		if t == nil {
			return nil, false
		}
		return endpointToMap(*t), true
	}
	return nil, false
}

func endpointToMap(e BackendEndpoint) map[string]any {
	m := map[string]any{"label": e.Label, "host": e.Host, "keyEnv": e.KeyEnv}
	for k, v := range map[string]string{"path": e.Path, "modelsPath": e.ModelsPath, "modelsField": e.ModelsField, "modelsIdField": e.ModelsIDField, "defaultModel": e.DefaultModel} {
		if v != "" {
			m[k] = v
		}
	}
	if e.ModelsVerifyKey != nil {
		m["modelsVerifyKey"] = *e.ModelsVerifyKey
	}
	return m
}

// ResolveBackend resolves a registry name or a caller-supplied endpoint (a BackendEndpoint, a pointer to
// one, or a map with the same fields) into the validated form the client uses. A name resolves to its
// registry entry unchanged; an endpoint is validated field by field and returned as a fresh value whose Host
// is origin only. Messages never quote a caller value (a host can carry credentials in its user info) except
// the label, and only after it is validated. Validation runs on every call; nothing is cached. nil resolves
// to DefaultBackend.
func ResolveBackend(backend any) (ResolvedBackend, error) {
	if backend == nil {
		backend = DefaultBackend
	}
	if name, ok := backend.(string); ok {
		if name == BackendOwnModel {
			return ResolvedBackend{BackendConfig: ownModelConfig, Name: name, ModelsVerifyKey: false}, nil
		}
		entry, ok := DecisionsBackends[name]
		if !ok {
			return ResolvedBackend{}, refuse(fmt.Sprintf("Unknown judgment backend %q. Valid backends: %s.", name, registryNames()))
		}
		if entry.KeyEnv == "" {
			entry.KeyEnv = typesafeKeyEnv
		}
		return ResolvedBackend{BackendConfig: entry, Name: name, DefaultModel: DefaultModelID(name), ModelsVerifyKey: entry.ModelsVerifyKey == nil || *entry.ModelsVerifyKey}, nil
	}
	spec, ok := endpointFields(backend)
	if !ok {
		return ResolvedBackend{}, refuse("backend must be a registry name or a backend object.")
	}
	str := func(field string) (string, bool, bool) { // value, present, isString
		v, present := spec[field]
		if !present || v == nil {
			return "", false, true
		}
		s, isStr := v.(string)
		return s, true, isStr
	}
	label, _, isStr := str("label")
	label = strings.TrimSpace(label)
	if !isStr || label == "" || utf16Len(label) > 60 {
		return ResolvedBackend{}, refuse("Backend label must be a nonempty string of at most 60 characters.")
	}
	hostText, _, isStr := str("host")
	if !isStr || hasRawQueryOrFragment(hostText) {
		return ResolvedBackend{}, refuse(hostMessage)
	}
	origin, err := validateHost(hostText)
	if err != nil {
		return ResolvedBackend{}, err
	}
	for _, f := range []struct{ field, message string }{
		{"path", `Backend path must be a string that starts with "/".`},
		{"modelsPath", `Backend modelsPath must be a string that starts with "/".`},
	} {
		v, present, isStr := str(f.field)
		if present && (!isStr || !strings.HasPrefix(v, "/") || hasRawQueryOrFragment(v)) {
			return ResolvedBackend{}, refuse(f.message)
		}
	}
	for _, f := range []struct{ field, message string }{
		{"modelsField", "Backend modelsField must be a nonempty string."},
		{"modelsIdField", "Backend modelsIdField must be a nonempty string."},
	} {
		v, present, isStr := str(f.field)
		if present && (!isStr || v == "") {
			return ResolvedBackend{}, refuse(f.message)
		}
	}
	verify := false
	if v, present := spec["modelsVerifyKey"]; present && v != nil {
		b, isBool := v.(bool)
		if !isBool {
			return ResolvedBackend{}, refuse("Backend modelsVerifyKey must be a boolean.")
		}
		verify = b
	}
	keyEnv, _, isStr := str("keyEnv")
	if !isStr || !keyEnvPattern.MatchString(keyEnv) {
		return ResolvedBackend{}, refuse(keyEnvMessage)
	}
	if strings.EqualFold(keyEnv, typesafeKeyEnv) {
		return ResolvedBackend{}, refuse("Backend keyEnv must not be TYPESAFE_API_KEY: the TypeSafe key is only sent to the typesafe backend. Give this endpoint its own variable.")
	}
	defaultModel, present, isStr := str("defaultModel")
	if present && (!isStr || strings.TrimSpace(defaultModel) == "" || utf16Len(defaultModel) > 100) {
		return ResolvedBackend{}, refuse("Backend defaultModel must be a nonempty string of at most 100 characters.")
	}
	out := ResolvedBackend{Name: "", DefaultModel: defaultModel, ModelsVerifyKey: verify}
	out.Label, out.Host, out.KeyEnv = label, origin, keyEnv
	out.Path, _, _ = str("path")
	out.ModelsPath, _, _ = str("modelsPath")
	out.ModelsField, _, _ = str("modelsField")
	out.ModelsIDField, _, _ = str("modelsIdField")
	out.ModelsVerifyKey = verify
	return out, nil
}

// validateHost applies the host rule and returns the origin (scheme, host and port).
func validateHost(text string) (string, error) {
	u, err := url.Parse(text)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Opaque != "" {
		return "", refuse(hostMessage)
	}
	scheme := strings.ToLower(u.Scheme)
	hostname := strings.ToLower(u.Hostname())
	// A dotted form with an octet above 255 is not an address (the original's URL parser rejects it); Go would
	// resolve it as a name, so the 127.0.0.0/8 rule also needs it to parse as an IP.
	loopback := hostname == "localhost" || hostname == "::1" || (loopbackV4.MatchString(hostname) && net.ParseIP(hostname) != nil)
	if scheme != "https" && !(scheme == "http" && loopback) {
		return "", refuse(hostMessage)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", refuse(hostMessage)
	}
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, nil
}

// BackendHost is the destination host only (host and port, no scheme), for example api.commandcode.ai, for
// consent text. The own-model backend has none and returns "".
func BackendHost(backend any) (string, error) {
	resolved, err := ResolveBackend(backend)
	if err != nil {
		return "", err
	}
	if resolved.Host == "" {
		return "", nil
	}
	u, err := url.Parse(resolved.Host)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

// BackendNames lists the registry names, sorted, for help text.
func BackendNames() []string {
	names := []string{BackendOwnModel}
	for n := range DecisionsBackends {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
