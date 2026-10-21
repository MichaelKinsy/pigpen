package pi_goal_x

import (
	"regexp"
	"strings"
)

// ported from goal-contract.ts. extractVerificationContract and sisyphusObjectiveSufficient live in format.go.

// untrustedTagRE is /<\/?untrusted_objective>/gi spelled out so RE2 does not fold U+017F / U+212A like (?i) would.
var untrustedTagRE = regexp.MustCompile(`</?[uU][nN][tT][rR][uU][sS][tT][eE][dD]_[oO][bB][jJ][eE][cC][tT][iI][vV][eE]>`)

// promptSafeObjective escapes untrusted-objective tags so injected objectives cannot break prompts.
func promptSafeObjective(objective string) string {
	return untrustedTagRE.ReplaceAllStringFunc(objective, func(tag string) string {
		return strings.ReplaceAll(strings.ReplaceAll(tag, "<", "&lt;"), ">", "&gt;")
	})
}
