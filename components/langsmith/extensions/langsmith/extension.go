// Package langsmith is a Go port of @langchain/langsmith-pi-extension 0.2.1.
package langsmith

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension returns the LangSmith tracing extension.
func Extension() *sdk.Extension {
	return sdk.New("langsmith")
}
