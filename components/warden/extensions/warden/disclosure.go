package warden

import (
	"strings"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// The data-flow disclosure: what warden reads on your machine, what it sends and where, and what it never
// sends. It is shown before anything leaves the machine, and again by /warden status.

// OfflineNote is what the offline checks do; they send nothing anywhere.
const OfflineNote = "Offline checks run on this machine and send nothing: they read each tool call for force-pushes, `rm -rf` of paths outside the project, DROP/TRUNCATE, secrets files, and a few other irreversible commands, and watch the run for repeated identical calls."

// Sent lists what a judged check sends, with its bounds (from the constants that enforce them).
var Sent = []string{
	"the tool name and the command or path of the call (credentials redacted, at most 2,000 characters)",
	"for a write or edit, an excerpt of the content (redacted, at most 1,500 characters)",
	"your latest request and the goal of the thread (redacted, at most 1,500 characters each) and up to 8 earlier messages (750 characters each)",
	"the agent's own words before the call (redacted, at most 500 characters)",
	"for the stuck check, the last few tool calls and the tail of their output (400 characters each)",
	"for the done check, the agent's final message (redacted, at most 2,000 characters) and how many files changed and which checks ran",
}

// NeverSent lists what is never included.
var NeverSent = "files the call does not name, environment variables, your PiG auth and settings files, and API keys (the TypeSafe key is read from the environment only to sign the request)"

// TypeSafeTarget names where the TypeSafe backend sends.
func TypeSafeTarget(getenv func(string) string) string {
	base := getenv(typesafe.EnvBaseURL)
	if base == "" {
		base = typesafe.DefaultBaseURL
	}
	return base
}

// Disclosure is the consent text for one backend. target is the base URL (TypeSafe) or "provider/model".
func Disclosure(backend, target string) string {
	var b strings.Builder
	b.WriteString("Warden makes two kinds of checks.\n\n")
	b.WriteString(OfflineNote + "\n\n")
	if backend == BackendNone {
		b.WriteString("You chose offline only: nothing leaves this machine.")
		return b.String()
	}
	b.WriteString("Judged checks (irreversible, off-task, stuck, done-claims) ask a model. Each request sends:\n")
	for _, s := range Sent {
		b.WriteString("  • " + s + "\n")
	}
	b.WriteString("\nNever sent: " + NeverSent + ".\n\n")
	switch backend {
	case BackendTypeSafe:
		b.WriteString("Destination: TypeSafe (Jev) at " + target + ", with the key in TYPESAFE_API_KEY. TypeSafe's terms and billing apply to those requests.")
	case BackendOwnModel:
		b.WriteString("Destination: your session model (" + target + "), through PiG's model registry: the same provider, account and terms as this chat. Nothing goes to TypeSafe.")
	}
	b.WriteString("\n\nYou can turn this off at any time with /warden disable.")
	return b.String()
}
