module github.com/MichaelKinsy/pigpen/pi-goal-x

go 1.26.0

// PiG resolves this requirement to the version-matched staged SDK at build time.
require github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1

// NFKC normalization for the blocker fingerprint (goal-oracle.ts normalizeBlockerReason).
require golang.org/x/text v0.42.0

require github.com/MichaelKinsy/pigpen/components/pitui v0.0.0
