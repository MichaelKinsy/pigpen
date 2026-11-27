// SPDX-License-Identifier: MIT

package rex

import (
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// NewForTest builds the rex extension with injectable environment, PATH lookup,
// and whoami timeout for unit tests.
func NewForTest(getenv func(string) string, lookPath func(string) (string, error), timeout time.Duration) *sdk.Extension {
	return newExtension(options{
		getenv:   getenv,
		lookPath: lookPath,
		timeout:  timeout,
	})
}

// NewForTestWithWait builds the rex extension with an explicit before_agent_start
// wait cap for testing async probe completion across runs.
func NewForTestWithWait(getenv func(string) string, lookPath func(string) (string, error), timeout, beforeAgentWait time.Duration) *sdk.Extension {
	return newExtension(options{
		getenv:          getenv,
		lookPath:        lookPath,
		timeout:         timeout,
		beforeAgentWait: beforeAgentWait,
	})
}

// Fallback constants exposed for test assertions.
const (
	UnknownPaneFallbackLine = unknownPaneFallbackLine
	NoAnswerFallbackLine    = noAnswerFallbackLine
)

// AppendContextLineForTest exposes the before_agent_start prompt-append helper
// so unit tests can exercise GetSystemPrompt errors directly.
func AppendContextLineForTest(line string, getSystemPrompt func() (string, error)) (any, error) {
	return appendContextLine(line, getSystemPrompt)
}
