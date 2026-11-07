package posthorse

// Named skips of the TS repository's own quality tooling tests (test/quality-*.test.ts and
// test/retained-qualification.test.ts). They exercise oxlint, oxfmt, tsc, the YAML/Renovate policy
// scripts and the release qualification of the TypeScript package: repository tooling that is not part
// of the ported code. Each case keeps its own reason, citing what the TS test checks.

import "testing"

func TestQualitySkips(t *testing.T) {
	// quality-config.test.ts: scripts/quality-policy.ts over the CI workflows and Renovate config.
	tskip(t, "quality-config", "configuration policy accepts a coherent future pin and support-floor update through its real CLI",
		"drives scripts/quality-policy.ts as a subprocess over a copied workflow tree to accept a Pi pin and support-floor bump; the Go port has no npm pin, workflow files or policy CLI")
	tskip(t, "quality-config", "configuration policy rejects each independently drifted workflow reference with its owner path",
		"mutates each GitHub workflow reference of the TS repo and expects the policy script to name the owning file and path; the Go port ships no workflows to drift")
	tskip(t, "quality-config", "configuration CLI rejects the historical stale support floor rather than a parse or process error",
		"checks the exit status and diagnostic of scripts/quality-policy.ts for the 0.9 era Pi support floor; that floor lives in the TS package.json and workflows, not in Go code")
	tskip(t, "quality-config", "the TUI peer cannot silently declare a different support floor",
		"asserts the @earendil-works/pi-tui peer range in the TS package.json equals the Pi floor; the Go port has no npm peer dependencies")
	tskip(t, "quality-config", "local Renovate rules cannot hide or override the single applicable Pi floor",
		"feeds renovate.json rule variants to the policy parser so that none can pin or ignore the Pi package; the Go port has no Renovate configuration")
	tskip(t, "quality-config", "unrelated literal-package and non-npm ranges retain independent owners",
		"checks that policy ownership of the Pi floor does not capture other literal packages or non-npm ranges in the TS repo's dependency files; no dependency manifests in the Go port")
	tskip(t, "quality-config", "configuration parsing rejects duplicate YAML authority keys and missing support policy",
		"exercises the repo's YAML parser on duplicate keys and a missing support-policy section of its workflow files; no YAML authority files exist in the Go port")

	// quality-integrity.test.ts: the TS repo's compiler and lint policy as data.
	tskip(t, "quality-integrity", "the real policy rejects configuration, composition and acceptance downgrades",
		"loads the real quality policy of the TS repo and expects each tsconfig, composition and acceptance weakening to be rejected; the Go gate is go vet and the test suite")
	tskip(t, "quality-integrity", "all effective compiler minimums remain enforced",
		"asserts each tsc strictness option (strict, noUncheckedIndexedAccess, ...) stays enabled in the effective tsconfig; Go has no equivalent option set")
	tskip(t, "quality-integrity", "omitted sources and unrun leaf projects cannot borrow a successful root compiler gate",
		"checks that tsc project references cannot skip a leaf tsconfig; Go packages are compiled by go build as a unit")
	tskip(t, "quality-integrity", "handwritten declarations cannot inherit the application's dependency skip",
		"asserts that .d.ts files do not inherit skipLibCheck from the application tsconfig; the Go port has no declaration files")
	tskip(t, "quality-integrity", "filesystem inventory and compiler-resolved imports preserve profile boundaries",
		"verifies the TS repo's source inventory against tsc-resolved imports per lint profile (src, test, scripts); Go package boundaries are enforced by the compiler")

	// quality-language.test.ts: oxlint type-aware Promise rules and tsc over JS and declarations.
	tskip(t, "quality-language", "all maintained JavaScript receives syntactic and semantic Promise checks",
		"runs oxlint's floating-promise and misused-promise rules over .js/.mjs files of the TS repo; the Go port has no JavaScript and no Promises")
	tskip(t, "quality-language", "semantic analysis covers TS, inherited checkJs and explicit ts-check",
		"probes oxlint type-aware analysis for @ts-check comments and inherited checkJs in fixture projects; TypeScript language service behaviour, not ported code")
	tskip(t, "quality-language", "canonical compiler errors remain a separate gate from semantic lint",
		"feeds literal tsc error programs to the verify pipeline and expects compiler diagnostics to stay distinct from lint findings; Go uses go vet and the compiler directly")
	tskip(t, "quality-language", "canonical compiler checks JavaScript alongside TypeScript",
		"asserts tsc reports errors in checked .js files next to .ts files; no JavaScript sources exist in the Go port")
	tskip(t, "quality-language", "handwritten declarations receive real compiler checking and reject unused expected errors",
		"runs tsc over hand-written .d.ts fixtures and requires unused @ts-expect-error to fail; TypeScript declaration semantics, absent from Go")

	// quality-policy.test.ts: suppression governance over TS/JSX sources.
	tskip(t, "quality-policy", "suppression policy reads actual comments, not strings, regexes or documentation",
		"tests the TS suppression-comment scanner (oxlint-disable, ts-ignore) against strings, regex literals and markdown; the Go port has no suppression directives to govern")
	tskip(t, "quality-policy", "suppression governance sees empty containers and punctuation gaps",
		"checks the scanner on directives inside empty containers and unusual punctuation of TS source; no directive syntax in Go code")
	tskip(t, "quality-policy", "JSX text is not a directive, but JSX expression comments are",
		"JSX parsing rule of the TS suppression scanner; the port has no JSX")
	tskip(t, "quality-policy", "used formatter suppressions cannot hide maintained source from acceptance",
		"asserts oxfmt-ignore regions cannot exclude maintained TS from the acceptance inventory; gofmt has no ignore directives")
	tskip(t, "quality-policy", "used next-line directives cannot borrow an old exception in any file",
		"checks that an oxlint-disable-next-line exception is bound to its file and line in the TS exception ledger; Go has no such ledger")
	tskip(t, "quality-policy", "only described dedicated negative type tests may use expect-error",
		"restricts @ts-expect-error to dedicated type-test files with descriptions; the Go port has no type-level tests")
	tskip(t, "quality-policy", "the real repository has complete source, project, profile and exception coverage",
		"walks the real TS repository tree and requires every source file to belong to a tsconfig project and lint profile; the Go module layout is validated by go vet and Pigpen's package checks")

	// quality-registrations.test.ts: the Promise-ownership rule for node:test registrations, and its fixture snippets.
	tskip(t, "quality-registrations", "Node registrations and test bodies require explicit Promise ownership",
		"runs the repo's lint rule over snippets of node:test registrations that must be awaited; Go tests have no Promise ownership concept")
	tskip(t, "quality-registrations", "top level",
		"a source-snippet name (`await test('top level', ...)`) inside the Promise-ownership probe's accepting fixture, scraped as a title; not a test case")
	tskip(t, "quality-registrations", "suite child",
		"a source-snippet name (`await test('suite child', ...)` inside a suite) of the probe's accepting fixture; not a test case")
	tskip(t, "quality-registrations", "owned work",
		"a source-snippet name of the accepting fixture that awaits a subtest and a Promise; not a test case")
	tskip(t, "quality-registrations", "parent",
		"a source-snippet name of the rejecting fixture whose context subtest lacks an await; not a test case")
	tskip(t, "quality-registrations", "body",
		"a source-snippet name of the rejecting fixture whose body leaves a Promise floating; not a test case")

	// quality-rules.test.ts: oxlint configuration probes (test/quality-rule-cases.ts, quality-rule-options.ts).
	tskip(t, "quality-rules", "configured rule boundary: ${example.name}",
		"loops over the oxlint rule-boundary table (quality-rule-cases.ts): each example passes at the limit and fails one past it under the repo's .oxlintrc; Go has no oxlint")
	tskip(t, "quality-rules", "configured structural boundary: ${example.name}",
		"loops over the structural-limit table (max-depth, max-params, complexity) at their exact thresholds under oxlint; no oxlint in Go")
	tskip(t, "quality-rules", "test role retains branching and parameter limits while exempting size",
		"checks the oxlint override for *.test.ts files (complexity 14/15, nesting 4/5, no max-lines); lint configuration of the TS repo")
	tskip(t, "quality-rules", "modified complexity counts one switch rather than every case",
		"oxlint modified-complexity semantics for a 20-case switch; TS lint rule, not ported behaviour")
	tskip(t, "quality-rules", "declaration files retain API safety without structural metrics",
		"oxlint profile for .d.ts files (max-params exempt, API-safety rules kept); the port has no declaration files")
	tskip(t, "quality-rules", "defined baseline choices retain accurate resource and diagnostic contracts",
		"fixture programs for the baseline oxlint choices (readonly parameters, void callbacks, console usage); TS lint policy")
	tskip(t, "quality-rules", "nullable object presence checks and genuine void shorthand remain permitted",
		"oxlint strict-boolean and void-expression rules allowing `if (input)` on nullable objects and `() => console.log()` for void; TS-specific lint exceptions")
	tskip(t, "quality-rules", "async interfaces may complete immediately and caught failures retain identity",
		"oxlint require-await and preserve-caught-error exemptions for async interface implementations; Promise lint rules")
	tskip(t, "quality-rules", "sequential asynchronous work retains native await ownership",
		"oxlint no-await-in-loop allowance for an ordered `for ... await` persist loop; a TS lint decision, Go loops need no await")
	tskip(t, "quality-rules", "terminal Promise callbacks may be void but interior callbacks must return",
		"oxlint promise/always-return behaviour on .then chains; Promise chains do not exist in the Go port")
	tskip(t, "quality-rules", "redundant catch rethrows remain detectable",
		"oxlint no-useless-catch finding on `catch (e) { throw e }`; TS try/catch lint")
	tskip(t, "quality-rules", "local sort and reverse stay allowed while void generic contracts stay real",
		"oxlint immutability and no-invalid-void-type decisions for [..].sort() and PromiseWithResolvers<void>; TS lint")
	tskip(t, "quality-rules", "dynamic execution constructs remain forbidden",
		"oxlint no-eval / no-new-func findings on eval and Function constructors; Go has no dynamic code evaluation")
	tskip(t, "quality-rules", "type imports and exports preserve runtime module contracts",
		"oxlint consistent-type-imports on `import type` versus value imports of node:fs; ES module typing rule")
	tskip(t, "quality-rules", "duplicate and self imports remain detectable",
		"oxlint import/no-duplicates and no-self-import findings on ES modules; Go's compiler rejects duplicate imports itself")
	tskip(t, "quality-rules", "native import analysis detects static dependency cycles",
		"the repo's import-cycle gate over two TS files importing each other; Go forbids import cycles at compile time")
	tskip(t, "quality-rules", "QX-011 records the published importer's external and awaited-dynamic cycle limits",
		"documents known blind spots (external package cycles, awaited dynamic import) of the published TS importer analysis; npm-specific")
	tskip(t, "quality-rules", "type-only cycles are not exempt from import boundaries",
		"asserts `import type` cycles still violate the TS repo's module boundaries; Go has no type-only imports")
	tskip(t, "quality-rules", "string timer callbacks are implied evaluation",
		"oxlint no-implied-eval on setTimeout('code') strings; no string-evaluated timers in Go")
	tskip(t, "quality-rules", "allowed scalar interpolation and contextual void contracts stay valid",
		"oxlint restrict-template-expressions allowing boolean/number interpolation and `this: void`; TS template literal lint")
	tskip(t, "quality-rules", "generic callback results retain their actual contract without artificial readonly wrappers",
		"oxlint prefer-readonly-parameter-types behaviour on generic callbacks returning T; TS type-aware lint")
	tskip(t, "quality-rules", "inferred callback inputs do not waive parameter-property mutation protection",
		"oxlint no-param-reassign (props) on inferred arrow parameters in .map callbacks; JS parameter mutation lint")
	tskip(t, "quality-rules", "native Promise executors deliberately discard timer handles using block bodies",
		"oxlint no-promise-executor-return rule for `new Promise(resolve => { setTimeout(resolve, 1) })`; Promise executors do not exist in Go")
	tskip(t, "quality-rules", "PromiseLike values have readonly permission but no floating-Promise exemption",
		"oxlint no-floating-promises on thenable values; Promise lint rule")
	tskip(t, "quality-rules", "normal lint fixes cannot manufacture unknown contracts from explicit any",
		"runs `oxlint --fix` on `export type Value = any` and asserts the autofix does not rewrite it to unknown; TS autofix policy")

	// quality-toolchain.test.ts: npm and SDK provenance of the verify script.
	tskip(t, "quality-toolchain", "the actual verify command rejects a different invoking npm before any child gate",
		"runs scripts/verify.ts under a different npm version and expects a refusal before child gates; the Go port has no npm toolchain")
	tskip(t, "quality-toolchain", "the loader accepts the installed graph and rejects a foreign SDK before execution",
		"checks the TS repo's pi-loader.ts accepting the installed @earendil-works SDK graph and refusing a foreign copy; Go resolves the PiG SDK through go.mod and the Pigpen requirement")
	tskip(t, "quality-toolchain", "verification cannot substitute a PATH guess when invoking npm provenance is missing",
		"asserts the verify script fails when npm_execpath is absent rather than guessing from PATH; npm-specific provenance")

	// retained-qualification.test.ts: release qualification of the TS package.
	tskip(t, "retained-qualification", "release qualification must cover the retained package and selected host",
		"checks the TS release qualification record names the packaged files and the Pi host version it ran against; Pigpen's equivalent evidence is port/PORT.md, the golden traces and the Package validation")
}
