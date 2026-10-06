// Package hardening is the shared library behind the opt-in enterprise profile of Pigpen Packages.
//
// With no profile file and no PIGPEN_<PACKAGE>_ENTERPRISE* environment variable the library does nothing: every
// flag is off, no file is written, no goroutine is started, no listener is opened and no connection is made. The
// packages are:
//
//	profile         loads <agent dir>/pigpen-enterprise/<package>.json; the typed errors
//	credfile        reads a credential from an auth.json-shaped file on every request
//	egress          an allow-listed, address-checked HTTP transport
//	audit           structured lifecycle events with a closed field set
//	headless        what a Package does instead of opening a dialog
//	policy          the Decider interface for tool-call decisions; FailClosed
//	policy/cedar    a Decider on Cedar policies (a nested module)
//	resourceserver  JWT access-token verification against a configured JWKS
//
// See README.md for the contract of each flag and docs/plan/enterprise-profile.md in the repository for the plan.
package hardening
