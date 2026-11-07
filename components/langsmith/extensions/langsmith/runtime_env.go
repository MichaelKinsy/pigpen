package langsmith

// Port of utils/env.js (langsmith 0.10.5): getRuntimeEnvironment, getShas, getLangSmithEnvVarsMetadata,
// getLangSmithEnvironmentVariable(s), getEnvironmentVariable, getOtelEnabled, resolveTracingMode.

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// sdkVersion is the langsmith JS SDK this port is wire-compatible with (index.js __version__).
	sdkVersion = "0.10.5"
	// portVersion is this Package's version; it identifies the sender on the wire (port/PORT.md, divergence D1).
	portVersion = "0.1.0"
	// integrationVersion is the INTEGRATION_VERSION of @langchain/langsmith-pi-extension 0.2.1 (dist/version.js).
	integrationVersion = "0.2.1"
)

// getEnvironmentVariable is process.env[name]: ok is false when the variable is unset.
func getEnvironmentVariable(name string) (string, bool) { return os.LookupEnv(name) }

// getLangSmithEnvironmentVariable is `process.env["LANGSMITH_"+name] || process.env["LANGCHAIN_"+name]`:
// an empty LANGSMITH_ value falls through to LANGCHAIN_; ok is false when the result is undefined.
func getLangSmithEnvironmentVariable(name string) (string, bool) {
	if v, ok := os.LookupEnv("LANGSMITH_" + name); ok && v != "" {
		return v, true
	}
	return os.LookupEnv("LANGCHAIN_" + name)
}

// commonReleaseEnvs is the list in env.js getShas (env.js:196-216), in the original's order.
var commonReleaseEnvs = [...]string{
	"VERCEL_GIT_COMMIT_SHA",
	"NEXT_PUBLIC_VERCEL_GIT_COMMIT_SHA",
	"COMMIT_REF",
	"RENDER_GIT_COMMIT",
	"CI_COMMIT_SHA",
	"CIRCLE_SHA1",
	"CF_PAGES_COMMIT_SHA",
	"REACT_APP_GIT_SHA",
	"SOURCE_VERSION",
	"GITHUB_SHA",
	"TRAVIS_COMMIT",
	"GIT_COMMIT",
	"BUILD_VCS_NUMBER",
	"bamboo_planRepository_revision",
	"Build.SourceVersion",
	"BITBUCKET_COMMIT",
	"DRONE_COMMIT_SHA",
	"SEMAPHORE_GIT_SHA",
	"BUILDKITE_COMMIT",
}

// shasFrom is the body of getShas over an arbitrary lookup.
func shasFrom(lookup func(string) (string, bool)) *omap {
	shas := newOmap()
	for _, name := range commonReleaseEnvs {
		if v, ok := lookup(name); ok {
			shas.set(name, v)
		}
	}
	return shas
}

// runtimeEnvFrom is the body of getRuntimeEnvironment over an arbitrary lookup.
func runtimeEnvFrom(lookup func(string) (string, bool)) *omap {
	env := obj(
		"library", "langsmith",
		"runtime", "go",
		"sdk", "pigpen-langsmith-go",
		"sdk_version", portVersion,
	)
	return env.spread(shasFrom(lookup))
}

var (
	runtimeEnvOnce sync.Once
	runtimeEnv     *omap
)

// getRuntimeEnvironment is env.js getRuntimeEnvironment (env.js:50-62): {library, runtime, sdk, sdk_version, ...getShas()}.
// Values describe this process truthfully (runtime "go", sdk "pigpen-langsmith-go", sdk_version portVersion);
// the keys are the SDK's. Computed once per process (the original caches both it and getShas); callers must
// treat the result as read-only.
func getRuntimeEnvironment() *omap {
	runtimeEnvOnce.Do(func() { runtimeEnv = runtimeEnvFrom(os.LookupEnv) })
	return runtimeEnv
}

// excludedSubstrings marks a variable name as secret-bearing or personal (env.js:64-73).
var excludedSubstrings = [...]string{"key", "secret", "token", "password", "passwd", "pwd", "credential", "email"}

// isSensitiveEnvVarName is env.js:75-78 (case-insensitive substring match on the lower-cased name).
func isSensitiveEnvVarName(key string) bool {
	for _, sub := range excludedSubstrings {
		if containsFold(key, sub) {
			return true
		}
	}
	return false
}

// containsFold reports whether key.toLowerCase() contains the lower-case needle sub. ASCII names (every real
// variable) are scanned without allocating; others go through the JavaScript lower-casing (U+212A folds to `k`).
func containsFold(key, sub string) bool {
	ascii := true
	for i := 0; i < len(key); i++ {
		if key[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if !ascii {
		var buf [64]byte
		return strings.Contains(string(jsUTF8Lower(buf[:0], key)), sub)
	}
outer:
	for i := 0; i+len(sub) <= len(key); i++ {
		for j := 0; j < len(sub); j++ {
			c := key[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != sub[j] {
				continue outer
			}
		}
		return true
	}
	return false
}

// maskSecret is `value.slice(0, 2) + "*".repeat(value.length - 4) + value.slice(-2)` (env.js:107-109). A value
// shorter than four UTF-16 units makes `"*".repeat` throw a RangeError; ok is false then.
func maskSecret(value string) (string, bool) {
	u := utf16.Encode([]rune(value))
	if len(u) < 4 {
		return "", false
	}
	var b strings.Builder
	b.Grow(len(value))
	b.WriteString(string(utf16.Decode(u[:2])))
	b.WriteString(strings.Repeat("*", len(u)-4))
	b.WriteString(string(utf16.Decode(u[len(u)-2:])))
	return b.String(), true
}

// langSmithEnvFrom is env.js getLangSmithEnvironmentVariables (env.js:99-125) over an explicit environment block
// (os.Environ form, in enumeration order). The whole loop sits in a try/catch in the original: a sensitive value
// shorter than four UTF-16 units (an empty LANGSMITH_API_KEY, say) throws inside it and the variables collected
// so far are returned, the rest dropped (finding W3). With mask false sensitive entries are left out instead of
// being masked, which is all getLangSmithEnvVarsMetadata needs from them; the truncation still happens.
func langSmithEnvFrom(environ []string, mask bool) *omap {
	envVars := newOmap()
	for _, kv := range environ {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 { // Windows "=C:=..." entries have an empty name
			continue
		}
		key, value := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(key, "LANGCHAIN_") && !strings.HasPrefix(key, "LANGSMITH_") {
			continue
		}
		if isSensitiveEnvVarName(key) {
			if utf16Len(value) < 4 {
				return envVars // RangeError: Invalid count value
			}
			if mask {
				m, _ := maskSecret(value)
				envVars.set(key, m)
			}
			continue
		}
		envVars.set(key, value)
	}
	return envVars
}

// getLangSmithEnvironmentVariables is env.js getLangSmithEnvironmentVariables over the process environment.
func getLangSmithEnvironmentVariables() *omap { return langSmithEnvFrom(os.Environ(), true) }

// excludedMetadataVars is the `excluded` list of getLangSmithEnvVarsMetadata (env.js:136-148).
var excludedMetadataVars = [...]string{
	"LANGCHAIN_API_KEY",
	"LANGCHAIN_ENDPOINT",
	"LANGCHAIN_TRACING_V2",
	"LANGCHAIN_PROJECT",
	"LANGCHAIN_SESSION",
	"LANGSMITH_API_KEY",
	"LANGSMITH_ENDPOINT",
	"LANGSMITH_TRACING_V2",
	"LANGSMITH_CONFIG_FILE",
	"LANGSMITH_PROJECT",
	"LANGSMITH_SESSION",
}

// langSmithEnvVarsMetadataFrom is getLangSmithEnvVarsMetadata over an explicit environment block.
func langSmithEnvVarsMetadataFrom(environ []string) *omap {
	all := langSmithEnvFrom(environ, false)
	out := newOmap()
loop:
	for _, key := range all.keys {
		for _, ex := range excludedMetadataVars {
			if key == ex {
				continue loop
			}
		}
		// Sensitive names were left out by langSmithEnvFrom; the original's second isSensitiveEnvVarName test
		// (env.js:151) can therefore never reject anything here.
		value := all.vals[key]
		if key == "LANGCHAIN_REVISION_ID" {
			out.set("revision_id", value)
		} else {
			out.set(key, value)
		}
	}
	return out
}

// getLangSmithEnvVarsMetadata is env.js getLangSmithEnvVarsMetadata (LANGCHAIN_/LANGSMITH_ variables that are
// not secret-bearing, LANGCHAIN_REVISION_ID as revision_id).
func getLangSmithEnvVarsMetadata() *omap { return langSmithEnvVarsMetadataFrom(os.Environ()) }

// getOtelEnabled is env.js getOtelEnabled.
func getOtelEnabled() bool {
	if v, ok := getEnvironmentVariable("OTEL_ENABLED"); ok && v == "true" {
		return true
	}
	v, ok := getLangSmithEnvironmentVariable("OTEL_ENABLED")
	return ok && v == "true"
}

// resolveTracingMode is env.js resolveTracingMode (env.js:224-246): explicit config value, then
// LANGSMITH_TRACING_MODE / LANGCHAIN_TRACING_MODE, then the legacy OTEL_ENABLED variables, then "langsmith".
func resolveTracingMode(configValue *string) (string, error) {
	if configValue != nil {
		return *configValue, nil
	}
	if envMode, ok := getLangSmithEnvironmentVariable("TRACING_MODE"); ok && envMode != "" {
		lower := string(jsUTF8Lower(nil, envMode))
		if lower != "langsmith" && lower != "otel" {
			return "", fmt.Errorf("Invalid LANGSMITH_TRACING_MODE=%s. Must be one of: langsmith, otel", jsonQuote(envMode))
		}
		if getOtelEnabled() {
			fmt.Fprintln(wireWarnWriter, "Both LANGSMITH_TRACING_MODE and the legacy OTEL_ENABLED / "+
				"LANGSMITH_OTEL_ENABLED env vars are set. "+
				"LANGSMITH_TRACING_MODE takes precedence.")
		}
		return lower, nil
	}
	if getOtelEnabled() {
		return "otel", nil
	}
	return "langsmith", nil
}

// wireWarnWriter receives the console.warn text of the wire helpers (stderr; tests swap it).
var wireWarnWriter io.Writer = os.Stderr

// jsonQuote is JSON.stringify(s) for a string.
func jsonQuote(s string) string {
	b, ok, err := stringify(s)
	if err != nil || !ok {
		return strconv.Quote(s)
	}
	return string(b)
}
