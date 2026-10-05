package typesafe

// Environment variable names for client configuration. Explicit options take
// precedence over the environment, then the defaults. Empty or whitespace-only
// values are ignored.
const (
	EnvAPIKey       = "TYPESAFE_API_KEY"
	EnvBaseURL      = "TYPESAFE_BASE_URL"
	EnvDefaultModel = "TYPESAFE_DEFAULT_MODEL"
	EnvLogLevel     = "TYPESAFE_LOG_LEVEL"
)
