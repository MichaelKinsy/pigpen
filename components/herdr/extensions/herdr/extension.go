// Package herdr reports PiG's agent state to herdr, the terminal workspace manager.
package herdr

import (
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The attempt bounds of one request, in production 0.5 s then 1.5 s.
var (
	firstAttempt = 500 * time.Millisecond
	retryAttempt = 1500 * time.Millisecond
)

// Extension is the conventional Go factory. Red stub: it registers nothing.
func Extension() *sdk.Extension { return sdk.New("herdr") }

func resumeArgvFor(file, id *string) []string { return nil }

func isSafeResumeArg(value string) bool { return false }

func endpointFor(goos, socketPath string) string { return "" }
