package herdr

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

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

// reporters maps each extension ExtensionFor built to its reporter.
var reporters sync.Map

// ExtensionFor is Extension over an environment lookup instead of the process environment. The extension it returns can
// be waited on with Quiesce and Taken.
func ExtensionFor(getenv func(string) string) *sdk.Extension {
	e, r := newExtension(getenv)
	if r != nil {
		reporters.Store(e, r)
	}
	return e
}

func reporterOf(e *sdk.Extension) *reporter {
	r, _ := reporters.Load(e)
	rep, _ := r.(*reporter)
	return rep
}

// Quiesce returns once the extension has no report queued or in flight: everything it will send, until the next event,
// has been sent. Call it after the last event returned. It takes as long as the herdr in use makes a request take, so a
// hung herdr costs the attempt bounds.
func Quiesce(e *sdk.Extension) {
	if r := reporterOf(e); r != nil {
		r.drains.Wait()
	}
}

// Taken is the number of requests the extension has taken to send so far. Each of them is on its way to herdr: a fake
// herdr that has seen that many distinct request ids has seen every request the extension has started.
func Taken(e *sdk.Extension) int {
	if r := reporterOf(e); r != nil {
		return int(r.taken.Load())
	}
	return 0
}
