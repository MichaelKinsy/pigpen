package pi_permission_system

import (
	"errors"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Cases beyond the original's tests: the Go API's own contracts (copies, options) and shapes the differential pins at the edges.

func TestBashProgramAccessors(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	program := bpParse(t, "cat /etc/hosts && ls ~/x", n)
	eqv(t, program.CommandText(), "cat /etc/hosts && ls ~/x")

	// The accessors hand out copies, as the original's spreads do.
	commands := program.Commands()
	commands[0] = BashCommand{Text: "tampered"}
	eqCommands(t, program, units("cat /etc/hosts", "ls ~/x")...)
	externals := program.ExternalAccesses()
	externals[0] = BashExternalPath{}
	if program.ExternalAccesses()[0].Path == nil {
		t.Error("ExternalAccesses shares its backing array")
	}
	candidates := program.PathRuleCandidates()
	candidates[0] = BashPathRuleCandidate{}
	if program.PathRuleCandidates()[0].Path == nil {
		t.Error("PathRuleCandidates shares its backing array")
	}
}

func TestBashProgramOptions(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	// No options, empty options and a nil workdir are the same program.
	plain := externalValues(bpParse(t, "cat ../secret.txt", n))
	for _, options := range []*BashProgramOptions{nil, {}, {Workdir: nil}} {
		program, err := ParseBashProgram("cat ../secret.txt", n, options)
		if err != nil {
			t.Fatal(err)
		}
		eqStrings(t, externalValues(program), plain...)
	}
	eqStrings(t, plain, "/projects/secret.txt")
	// An empty workdir is a defined one (`options?.workdir` is "", not undefined): it has no canonical form, so the resolver reads it
	// as outside the cwd and records it under its empty lexical value, as the original does (the differential's `emptywd`
	// configuration pins the same).
	eqStrings(t, externalValues(bpParse(t, "cat ../secret.txt", n, "")), "", "/projects/secret.txt")
	// A workdir under an unknown-base command still seeds only the start.
	eqStrings(t, externalValues(bpParse(t, "cat x", n, "/etc")), "/etc")
}

func TestBashProgramEmptyCommand(t *testing.T) {
	program := bpParse(t, "", bpNormalizer(t, bpCwd))
	eqv(t, len(program.Commands())+len(program.ExternalAccesses())+len(program.PathRuleCandidates()), 0)
	if program.Commands() == nil || program.ExternalAccesses() == nil || program.PathRuleCandidates() == nil {
		t.Error("an empty program holds nil slices")
	}
}

func TestBashProgramPipelineRedirectFollowsTheFold(t *testing.T) {
	// tree-sitter groups `cd /tmp/q && echo hi > ../out | cat` as `(cd /tmp/q && echo hi > ../out) | cat`: the redirect is part of
	// the first stage, whose leading `cd` folds, so its target resolves against /tmp/q.
	n := bpNormalizer(t, bpCwd)
	eqStrings(t, externalValues(bpParse(t, "cd /tmp/q && echo hi > ../out | cat", n)), "/tmp/q", "/tmp/out")
}

// A parser that cannot start is the caller's error (the original's parse() rejects); a parser that returns no tree gives the empty
// program, which keeps its command text.
func TestBashProgramParserFailures(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	old := bashparse.GetParser
	t.Cleanup(func() { bashparse.GetParser = old })

	bashparse.GetParser = func() (bashparse.Parser, error) { return nil, errors.New("no grammar") }
	if program, err := ParseBashProgram("cat /etc/hosts", n, nil); err == nil || program != nil {
		t.Errorf("got %v, %v; want the parser's error", program, err)
	}

	bashparse.GetParser = func() (bashparse.Parser, error) { return noTreeParser{}, nil }
	program, err := ParseBashProgram("cat /etc/hosts", n, &BashProgramOptions{Workdir: ptr("/etc")})
	if err != nil {
		t.Fatal(err)
	}
	eqv(t, program.CommandText(), "cat /etc/hosts")
	eqv(t, len(program.Commands())+len(program.ExternalAccesses())+len(program.PathRuleCandidates()), 0)
}

type noTreeParser struct{}

func (noTreeParser) Parse(string) *bashparse.Tree { return nil }

// The workdir's own entry is deduplicated against the command's tokens by the canonical form, as a token's own entries are.
func TestBashProgramWorkdirDedupUsesTheCanonicalForm(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	// /projects/my-app/wd is a symlink to /etc.
	mockRealpath(t, func(p string) (string, error) {
		if p == "/projects/my-app/wd" {
			return "/etc", nil
		}
		return p, nil
	})
	program := bpParse(t, "cat /projects/my-app/wd", n, "/projects/my-app/wd")
	got := program.ExternalAccesses()
	if len(got) != 1 {
		t.Fatalf("got %d entries: %q", len(got), externalValues(program))
	}
	eqv(t, got[0].Path.Value(), "/projects/my-app/wd")
	eqEffect(t, got[0].Effect, EffectRead, SourceCore) // the token's attribution stands over the implicit base
}

// A downstream pipeline stage runs in a subshell: its `cd` folds nothing.
func TestBashProgramDownstreamPipeStageDoesNotFold(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	eqStrings(t, externalValues(bpParse(t, "echo a | cd /tmp/q | cat ../x ; cat ../y", n)), "/tmp/q", "/projects/x", "/projects/y")
}

// After an unknown `cd`, a relative candidate is flagged against the cwd for display, unless the canonical path is a safe device,
// and it deduplicates by canonical path.
func TestBashProgramUnknownBaseProjection(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	eqStrings(t, externalValues(bpParse(t, `cd "$D" && cat ../../dev/null`, n)))
	mockRealpath(t, func(p string) (string, error) {
		if p == "/projects/a" || p == "/projects/b" {
			return "/vault", nil
		}
		return p, nil
	})
	eqStrings(t, externalValues(bpParse(t, `cd "$D" && cat ../a ../b`, n)), "/projects/a")
}

// Two spellings of one resolved path are one external access that keeps the first spelling; their proofs merge.
func TestBashProgramExternalDedupKeepsTheFirstSpelling(t *testing.T) {
	n := bpNormalizer(t, bpCwd)
	mockRealpath(t, func(p string) (string, error) {
		if p == "/projects/my-app/link/hosts" {
			return "/etc/hosts", nil
		}
		return p, nil
	})
	program := bpParse(t, "cat /projects/my-app/link/hosts /etc/hosts", n)
	eqStrings(t, externalValues(program), "/projects/my-app/link/hosts")
	eqStrings(t, externalBoundaries(program), "/etc/hosts")
}

// The existence probe asks the shape gate first: a flag, an assignment or a scope names no path even when an entry of that
// name exists; and it asks about the typed location, so a symlink counts by its own name.
func TestBashProgramProbeAsksShapeAndLocation(t *testing.T) {
	root := realTmp(t)
	n := NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), root)
	for _, name := range []string{"-r", "A=1", "@scope"} {
		writeFile(t, root, name, "x")
	}
	eqStrings(t, ruleTokens(bpParse(t, "ls -r A=1 @scope", n)))
	eqStrings(t, externalValues(bpParse(t, "ls -r A=1 @scope", n)))

	// Only the typed location exists; its canonical form does not.
	n = bpNormalizer(t, bpCwd)
	mockRealpath(t, func(p string) (string, error) {
		if p == "/projects/my-app/link" {
			return "/vault/gone", nil
		}
		return p, nil
	})
	old := lstatSync
	lstatSync = func(p string) bool { return p == "/projects/my-app/link" }
	t.Cleanup(func() { lstatSync = old })
	eqStrings(t, ruleTokens(bpParse(t, "cat link", n)), "link")
}
