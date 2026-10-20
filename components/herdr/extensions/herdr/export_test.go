package herdr

// The attempt timeouts of one herdr request: a test shortens them, and checks that production keeps the original's
// 0.5 s then 1.5 s (readEnv-independent, read before any test changes them).
var (
	FirstAttempt         = &firstAttempt
	RetryAttempt         = &retryAttempt
	OriginalFirstAttempt = firstAttempt
	OriginalRetryAttempt = retryAttempt
)

// ResumeArgvFor is resumeArgvFor over a session file and id (nil: the session manager had none).
func ResumeArgvFor(file, id *string) []string { return resumeArgvFor(file, id) }

// IsSafeResumeArg is isSafeResumeArg.
func IsSafeResumeArg(value string) bool { return isSafeResumeArg(value) }

// Endpoint is the socket endpoint for HERDR_SOCKET_PATH on goos.
func Endpoint(goos, socketPath string) string { return endpointFor(goos, socketPath) }
