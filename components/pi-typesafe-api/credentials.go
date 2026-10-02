package pitypesafe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// KeySource says where the key in effect comes from.
type KeySource string

// The key sources.
const (
	SourceEnvironment KeySource = "environment"
	SourceStored      KeySource = "stored"
)

// KeyKind is the kind of a KeySituation.
type KeyKind string

// The key situations.
const (
	KeyEnvironment KeyKind = "environment"
	KeyStored      KeyKind = "stored"
	KeyMissing     KeyKind = "missing"
	KeyUnusable    KeyKind = "unusable"
	// KeyNotRequired is the own-model backend: it authenticates through PiG's model access, not a key of this package.
	KeyNotRequired KeyKind = "notrequired"
)

// KeySituation is the complete answer to "which key is in effect".
type KeySituation struct {
	Kind KeyKind
	// Key is set for environment and stored.
	Key string
	// KeyEnv names the variable that was read (environment only).
	KeyEnv string
	// Path is the store path (stored and unusable).
	Path string
	// Reason says why a stored key cannot be used (unusable only).
	Reason string
}

// AgentDir is PiG's writable agent directory: PIG_CODING_AGENT_DIR, else <PIG_HOME or ~/.pig>/agent. With
// PIG_USE_PI_DIRS=1 it is Pi's: PI_CODING_AGENT_DIR, else ~/.pi/agent. This is PiG's own rule (divergence D2),
// not the fixed Pi directory of the original.
func AgentDir() string {
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
			return expandTilde(v)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".pi", "agent")
	}
	if v := os.Getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return expandTilde(v)
	}
	if v := os.Getenv("PIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "agent")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "pig", "agent")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig", "agent")
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// TypeSafeDir is the directory every file of this package lives in.
func TypeSafeDir() string { return filepath.Join(AgentDir(), "pi-typesafe") }

// CredentialsPath is where the key lives, next to PiG's own auth.json.
func CredentialsPath() string { return filepath.Join(TypeSafeDir(), "auth.json") }

// NormalizeAPIKey accepts the key only when it is a plausible token; it never logs or echoes the value.
func NormalizeAPIKey(value any) (string, error) {
	key, _ := value.(string)
	key = strings.TrimSpace(key)
	ok := len(key) >= 16 && len(key) <= 512
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			ok = false
		}
	}
	if !ok {
		return "", newError(CodeValidation, "That does not look like a TypeSafe API key. Copy the complete key from console.typesafe.ai and try again; nothing was saved.")
	}
	return key, nil
}

// ReadStoredAPIKey returns the stored key, or "" when the file is missing, unreadable, or holds no usable
// value. It returns a configuration error when the file is readable by other users; KeySituationFor reports
// that case as unusable instead.
func ReadStoredAPIKey() (string, error) {
	path := CredentialsPath()
	info, err := os.Stat(path)
	if err != nil {
		return "", nil
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		// Refuse to use a key other local users can read; the user must fix permissions or log in again.
		return "", errorf(CodeConfiguration, "Refusing to read %s: it is readable by other users. Run chmod 600 on it, or run /typesafe logout and /typesafe login.", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) != nil {
		return "", nil
	}
	if key, ok := parsed["apiKey"].(string); ok {
		return strings.TrimSpace(key), nil
	}
	return "", nil
}

// KeySituationFor says what the environment, the login store, and file permissions add up to right now for
// one judgment backend (nil means the default). A valid backend never gets an error: the Unusable kind carries
// the user-facing reason. An invalid backend returns the same configuration error as ResolveBackend. The
// TypeSafe backend reads TYPESAFE_API_KEY, then the login store; every other backend reads only its own
// environment variable, because the store holds a TypeSafe key.
func KeySituationFor(backend any) (KeySituation, error) {
	config, err := ResolveBackend(backend)
	if err != nil {
		return KeySituation{}, err
	}
	if config.Local {
		return KeySituation{Kind: KeyNotRequired}, nil
	}
	if v := strings.TrimSpace(os.Getenv(config.KeyEnv)); v != "" {
		return KeySituation{Kind: KeyEnvironment, Key: v, KeyEnv: config.KeyEnv}, nil
	}
	if !UsesTypeSafeKey(config.BackendConfig) {
		return KeySituation{Kind: KeyMissing}, nil
	}
	path := CredentialsPath()
	key, err := ReadStoredAPIKey()
	if err != nil {
		if ie, ok := err.(*IntegrationError); ok && ie.Code == CodeConfiguration {
			return KeySituation{Kind: KeyUnusable, Path: path, Reason: ie.Message}, nil
		}
		return KeySituation{}, err
	}
	if key != "" {
		return KeySituation{Kind: KeyStored, Key: key, Path: path}, nil
	}
	return KeySituation{Kind: KeyMissing}, nil
}

// KeySourceLabel is a short phrase naming the source.
func KeySourceLabel(s KeySituation) string {
	switch s.Kind {
	case KeyEnvironment:
		if s.KeyEnv == "" {
			return typesafeKeyEnv
		}
		return s.KeyEnv
	case KeyStored:
		return "/typesafe login"
	case KeyMissing:
		return "no key"
	case KeyUnusable:
		return "unusable key"
	case KeyNotRequired:
		return "no key needed"
	}
	return ""
}

// ResolvedKey is a key and where it came from.
type ResolvedKey struct {
	Key    string
	Source KeySource
}

// ResolveAPIKey is the pre-0.4.0 key interface of the original: environment first so CI and scripts stay
// explicit, the stored key as the interactive default, nil when no key is configured, and a configuration
// error when a store must not be read. New code should call KeySituationFor.
func ResolveAPIKey(backend any) (*ResolvedKey, error) {
	s, err := KeySituationFor(backend)
	if err != nil {
		return nil, err
	}
	switch s.Kind {
	case KeyUnusable:
		return nil, newError(CodeConfiguration, s.Reason)
	case KeyEnvironment:
		return &ResolvedKey{Key: s.Key, Source: SourceEnvironment}, nil
	case KeyStored:
		return &ResolvedKey{Key: s.Key, Source: SourceStored}, nil
	}
	return nil, nil
}

// StoreAPIKey validates and saves a key with owner-only permissions and returns the path.
func StoreAPIKey(value any) (string, error) {
	key, err := NormalizeAPIKey(value)
	if err != nil {
		return "", err
	}
	path := CredentialsPath()
	body, _ := json.MarshalIndent(map[string]string{"apiKey": key}, "", "  ")
	if err := writeOwnerOnly(path, append(body, '\n')); err != nil {
		return "", errorf(CodeConfiguration, "Could not write %s. Check directory permissions, or set TYPESAFE_API_KEY in the environment instead.", path)
	}
	return path, nil
}

// writeOwnerOnly writes atomically with mode 0600 in a 0700 directory.
func writeOwnerOnly(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ClearStoredAPIKey removes the stored key and reports whether there was one.
func ClearStoredAPIKey() bool {
	path := CredentialsPath()
	if _, err := os.Stat(path); err != nil {
		return false
	}
	os.Remove(path)
	return true
}

// EnvKeySet reports whether TYPESAFE_API_KEY is set (non-blank) in the environment.
func EnvKeySet() bool { return strings.TrimSpace(os.Getenv(typesafeKeyEnv)) != "" }
