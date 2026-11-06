package langsmith

// Port of utils/env.js (langsmith 0.10.5): getRuntimeEnvironment, getShas, getLangSmithEnvVarsMetadata,
// getLangSmithEnvironmentVariable(s), getEnvironmentVariable. OWNER: wire lane.

const (
	// sdkVersion is the langsmith JS SDK this port is wire-compatible with (index.js __version__).
	sdkVersion = "0.10.5"
	// portVersion is this Package's version; it identifies the sender on the wire (port/PORT.md, divergence D1).
	portVersion = "0.1.0"
	// integrationVersion is the INTEGRATION_VERSION of @langchain/langsmith-pi-extension 0.2.1 (dist/version.js).
	integrationVersion = "0.2.1"
)

// getEnvironmentVariable is process.env[name]: ok is false when the variable is unset.
func getEnvironmentVariable(name string) (string, bool) {
	panic("unported: runtime_env.go getEnvironmentVariable")
}

// getLangSmithEnvironmentVariable is `process.env["LANGSMITH_"+name] || process.env["LANGCHAIN_"+name]`:
// an empty LANGSMITH_ value falls through to LANGCHAIN_; ok is false when the result is undefined.
func getLangSmithEnvironmentVariable(name string) (string, bool) {
	panic("unported: runtime_env.go getLangSmithEnvironmentVariable")
}

// getRuntimeEnvironment is env.js getRuntimeEnvironment: {library, runtime, sdk, sdk_version, ...getShas()}.
// Values describe this process truthfully (runtime "go", sdk "pigpen-langsmith-go", sdk_version portVersion);
// the keys are the SDK's. Cached.
func getRuntimeEnvironment() *omap { panic("unported: runtime_env.go getRuntimeEnvironment") }

// getLangSmithEnvVarsMetadata is env.js getLangSmithEnvVarsMetadata (LANGCHAIN_/LANGSMITH_ variables that are
// not secret-bearing, LANGCHAIN_REVISION_ID as revision_id).
func getLangSmithEnvVarsMetadata() *omap {
	panic("unported: runtime_env.go getLangSmithEnvVarsMetadata")
}
