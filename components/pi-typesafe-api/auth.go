package pitypesafe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const authVersion = 1

// AuthFailure is the last request that degraded TypeSafe, with no upstream body, header, key, or submitted state.
type AuthFailure struct {
	Code    ErrorCode `json:"code"`
	Status  int       `json:"status,omitempty"`
	Message string    `json:"message"`
	At      string    `json:"at"`
}

// AuthState is the whole answer to "is Jev actually available right now": which key is in effect, whether it
// has been accepted, and the last failure that degraded it. Consumers must consult it instead of treating their
// own consent flag as proof that judgments will happen.
type AuthState struct {
	// Backend is the value the caller passed (nil means the default).
	Backend any
	Kind    KeyKind
	Source  KeySource
	// Path is where the key would be read from.
	Path string
	// Reason says why a stored key cannot be used.
	Reason string
	// KeyName is a short label for the key source: TYPESAFE_API_KEY, /typesafe login, no key, unusable key.
	KeyName string
	// Verified is true when the key in effect was accepted by the backend.
	Verified   bool
	VerifiedAt string
	// LastFailure is cleared by the next successful request.
	LastFailure *AuthFailure
	// Usable means a key is present and the last authentication outcome was not a rejection. False means judgments are skipped.
	Usable bool
}

// AuthOptions selects the state file and backend.
type AuthOptions struct {
	Path    string
	Backend any
}

// AuthStatePath is the auth-state file: one small, owner-only record that outlives the process that wrote it.
func AuthStatePath() string { return filepath.Join(TypeSafeDir(), "auth-state.json") }

type storedAuth struct {
	Version     int          `json:"version"`
	VerifiedAt  string       `json:"verifiedAt,omitempty"`
	LastFailure *AuthFailure `json:"lastFailure,omitempty"`
}

var errorCodes = map[ErrorCode]bool{CodeConfiguration: true, CodeValidation: true, CodeBudget: true, CodeAborted: true, CodeTimeout: true, CodeHTTP: true, CodeConnection: true, CodeResponse: true}

func readAuthState(path string) storedAuth {
	data, err := os.ReadFile(path)
	if err != nil {
		return storedAuth{}
	}
	var raw struct {
		VerifiedAt  any `json:"verifiedAt"`
		LastFailure *struct {
			Code    any `json:"code"`
			Status  any `json:"status"`
			Message any `json:"message"`
			At      any `json:"at"`
		} `json:"lastFailure"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return storedAuth{}
	}
	var out storedAuth
	if v, ok := raw.VerifiedAt.(string); ok && utf16Len(v) <= 40 {
		out.VerifiedAt = v
	}
	if f := raw.LastFailure; f != nil {
		code, _ := f.Code.(string)
		message, _ := f.Message.(string)
		at, _ := f.At.(string)
		if errorCodes[ErrorCode(code)] && message != "" && at != "" && utf16Len(at) <= 40 {
			failure := &AuthFailure{Code: ErrorCode(code), Message: truncateUTF16(message, 300), At: at}
			if s, ok := f.Status.(float64); ok && s == float64(int(s)) {
				failure.Status = int(s)
			}
			out.LastFailure = failure
		}
	}
	return out
}

func truncateUTF16(s string, n int) string {
	if utf16Len(s) <= n {
		return s
	}
	count := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if count+w > n {
			return s[:i]
		}
		count += w
	}
	return s
}

// writeAuthState is owner-only, atomic, and best-effort: an unwritable auth record never changes how a request behaves.
func writeAuthState(path string, state storedAuth) {
	state.Version = authVersion
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = writeOwnerOnly(path, append(body, '\n'))
}

func rejected(f *AuthFailure) bool {
	return f != nil && f.Code == CodeHTTP && (f.Status == 401 || f.Status == 403)
}

// GetAuthState adds the key situation, the last outcome, and the clock up for a valid backend. An invalid
// backend returns the same configuration error as ResolveBackend, so validate a user-supplied endpoint with
// ResolveBackend first.
func GetAuthState(opts AuthOptions) (AuthState, error) {
	path := opts.Path
	if path == "" {
		path = AuthStatePath()
	}
	backend := opts.Backend
	if backend == nil {
		backend = DefaultBackend
	}
	situation, err := KeySituationFor(backend)
	if err != nil {
		return AuthState{}, err
	}
	stored := readAuthState(path)
	var source KeySource
	switch situation.Kind {
	case KeyEnvironment:
		source = SourceEnvironment
	case KeyStored:
		source = SourceStored
	}
	isRejected := rejected(stored.LastFailure)
	present := source != "" || situation.Kind == KeyNotRequired
	state := AuthState{
		Backend:     backend,
		Kind:        situation.Kind,
		Source:      source,
		Path:        CredentialsPath(),
		KeyName:     KeySourceLabel(situation),
		Verified:    stored.VerifiedAt != "" && !isRejected,
		VerifiedAt:  stored.VerifiedAt,
		LastFailure: stored.LastFailure,
		Usable:      present && !isRejected,
	}
	if situation.Kind == KeyUnusable {
		state.Path = situation.Path
		state.Reason = situation.Reason
	}
	return state, nil
}

// RecordAuthVerified records that the key was accepted: login verification, or any successful request. It clears the last failure.
func RecordAuthVerified(at time.Time) {
	writeAuthState(AuthStatePath(), storedAuth{VerifiedAt: isoTime(at)})
}

// RecordAuthFailure records the failure that degraded TypeSafe. The verification timestamp is kept so a recovered key stays known.
func RecordAuthFailure(err *IntegrationError, at time.Time) {
	current := readAuthState(AuthStatePath())
	writeAuthState(AuthStatePath(), storedAuth{
		VerifiedAt:  current.VerifiedAt,
		LastFailure: &AuthFailure{Code: err.Code, Status: err.Status, Message: err.Message, At: isoTime(at)},
	})
}

// ClearAuthState forgets verification and degradation: used when the key itself changes (login or logout).
func ClearAuthState() { _ = os.Remove(AuthStatePath()) }

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// AuthLevel is the severity of an AuthReport.
type AuthLevel string

// The levels: error when judgments are skipped or were rejected, warning when the key is unverified, else ok.
const (
	LevelOK      AuthLevel = "ok"
	LevelWarning AuthLevel = "warning"
	LevelError   AuthLevel = "error"
)

// AuthReport is one line plus a level, so a status command, a headless log, or a consumer's own status line
// can call out a degraded state instead of reporting "enabled".
type AuthReport struct {
	Level AuthLevel
	Text  string
}

// DescribeAuth turns a state into a level and one safe line.
func DescribeAuth(state AuthState) (AuthReport, error) {
	config, err := ResolveBackend(state.Backend)
	if err != nil {
		return AuthReport{}, err
	}
	label := config.Label + " key"
	since := ""
	if f := state.LastFailure; f != nil {
		since = " Last failure: " + f.Message
		if f.At != "" {
			since += " (" + f.At + ")"
		}
	}
	switch state.Kind {
	case KeyNotRequired:
		return AuthReport{LevelOK, config.Label + ": no key needed; judgments use the model PiG is configured with." + since}, nil
	case KeyMissing:
		how := config.KeyEnv + " is set in the environment"
		if UsesTypeSafeKey(config.BackendConfig) {
			how = "a key is configured (/typesafe login or " + typesafeKeyEnv + ")"
		}
		return AuthReport{LevelError, label + ": missing — every Jev judgment is skipped until " + how + "." + since}, nil
	case KeyUnusable:
		reason := state.Reason
		if reason == "" {
			reason = "unknown reason"
		}
		return AuthReport{LevelError, label + ": unusable (" + reason + ") — judgments are skipped until the key is fixed." + since}, nil
	}
	if rejected(state.LastFailure) {
		return AuthReport{LevelError, label + ": " + state.KeyName + " was rejected." + since}, nil
	}
	if !state.Verified {
		return AuthReport{LevelWarning, label + ": " + state.KeyName + " (not verified yet — the first request proves it)." + since}, nil
	}
	verified := "verified"
	if state.VerifiedAt != "" {
		verified += " " + state.VerifiedAt
	}
	return AuthReport{LevelOK, label + ": " + state.KeyName + " (" + verified + ")." + since}, nil
}
