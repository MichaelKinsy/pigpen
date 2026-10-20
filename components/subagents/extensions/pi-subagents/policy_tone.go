package pi_subagents

import (
	"regexp"
)

// Port of the thinking level reader of src/tui/running-tone.ts and the two error tests of src/shared/extension-context.ts it uses.

var (
	staleExtensionContext = regexp.MustCompile(`(?i)extension ctx is stale|extension context no longer active|stale after session replacement or reload`)
	unboundExtensionState = regexp.MustCompile(`(?i)extension runtime not initialized`)
)

// isStaleExtensionContextError reports an error Pi raises for an extension context that a session replacement or reload retired.
func isStaleExtensionContextError(err error) bool {
	return err != nil && staleExtensionContext.MatchString(err.Error())
}

// isUnboundExtensionRuntimeError reports the error Pi raises from an action method while extensions are still loading.
func isUnboundExtensionRuntimeError(err error) bool {
	return err != nil && unboundExtensionState.MatchString(err.Error())
}

// readMainThinkingLevel reads the main session's level through read; a stale or not-yet-bound runtime has no level to show ("").
// Any other error is returned.
func readMainThinkingLevel(read func() (string, error)) (string, error) {
	level, err := read()
	if err != nil {
		if isStaleExtensionContextError(err) || isUnboundExtensionRuntimeError(err) {
			return "", nil
		}
		return "", err
	}
	return level, nil
}
