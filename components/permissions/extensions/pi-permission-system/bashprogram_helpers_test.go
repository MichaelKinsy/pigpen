package pi_permission_system

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// The twins of test/access-intent/bash/program.test.ts and program-external-accesses.test.ts. The originals mock node:fs's
// realpathSync (default identity); the twins mock the package's realpathSync hook the same way, and read the real $HOME where the
// originals call homedir() (the filesystem-backed cases use a real temporary directory like the originals).

const (
	bpFile  = "access-intent/bash/program"
	bpxFile = "access-intent/bash/program-external-accesses"
)

// each runs one row of an it.each table: the ledger lists a parametrized title only through its rows, so the row is a plain
// subtest the twin checker does not count.
func each(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(name, fn)
}

// bpCwd is the working directory of the originals' fake project.
const bpCwd = "/projects/my-app"

// bpNormalizer is a PathNormalizer for the host platform's flavor over cwd, built with the identity realpath in force like the
// originals' describe-time normalizer (the cwd's canonical form is resolved once at construction).
func bpNormalizer(t *testing.T, cwd string) *PathNormalizer {
	t.Helper()
	mockRealpath(t, identityRealpath)
	return NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), cwd)
}

// bpParse is BashProgram.parse(command, normalizer, options).
func bpParse(t *testing.T, command string, normalizer *PathNormalizer, workdir ...string) *BashProgram {
	t.Helper()
	var options *BashProgramOptions
	if len(workdir) > 0 {
		options = &BashProgramOptions{Workdir: ptr(workdir[0])}
	}
	program, err := ParseBashProgram(command, normalizer, options)
	if err != nil {
		t.Fatalf("ParseBashProgram(%q): %v", command, err)
	}
	return program
}

// unit is a command unit holding only text, the original's `{ text }`.
func unit(text string) BashCommand { return BashCommand{Text: text} }

// ctxUnit is a nested command unit.
func ctxUnit(text string, context bashparse.BashCommandContext) BashCommand {
	return BashCommand{Text: text, Context: context}
}

// units is a list of text-only units.
func units(texts ...string) []BashCommand {
	out := make([]BashCommand, len(texts))
	for i, text := range texts {
		out[i] = unit(text)
	}
	return out
}

func eqCommands(t *testing.T, program *BashProgram, want ...BashCommand) {
	t.Helper()
	got := program.Commands()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n got %#v\nwant %#v", got, want)
	}
}

func hasCommand(commands []BashCommand, want BashCommand) bool {
	for _, c := range commands {
		if reflect.DeepEqual(c, want) {
			return true
		}
	}
	return false
}

func ruleTokens(program *BashProgram) []string {
	out := []string{}
	for _, c := range program.PathRuleCandidates() {
		out = append(out, c.Token)
	}
	return out
}

func externalValues(program *BashProgram) []string {
	out := []string{}
	for _, e := range program.ExternalAccesses() {
		out = append(out, e.Path.Value())
	}
	return out
}

func externalBoundaries(program *BashProgram) []string {
	out := []string{}
	for _, e := range program.ExternalAccesses() {
		out = append(out, e.Path.BoundaryValue())
	}
	return out
}

// ruleCandidate is the first rule candidate whose token is token.
func ruleCandidate(program *BashProgram, token string) (BashPathRuleCandidate, bool) {
	for _, c := range program.PathRuleCandidates() {
		if c.Token == token {
			return c, true
		}
	}
	return BashPathRuleCandidate{}, false
}

func mustRuleCandidate(t *testing.T, program *BashProgram, token string) BashPathRuleCandidate {
	t.Helper()
	c, ok := ruleCandidate(program, token)
	if !ok {
		t.Fatalf("no rule candidate for %q in %v", token, ruleTokens(program))
	}
	return c
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func eqStrings(t *testing.T, got []string, want ...string) {
	t.Helper()
	if got == nil {
		got = []string{}
	}
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func eqEffect(t *testing.T, got TokenEffect, effect AttributedEffect, source EffectSource) {
	t.Helper()
	if got != (TokenEffect{Effect: effect, Source: source}) {
		t.Errorf("effect: got %#v, want {%s %s}", got, effect, source)
	}
}

// join is node:path join for the host platform.
func join(parts ...string) string { return filepath.Join(parts...) }

// homePath is node:os homedir().
func homePath() string { return homeDir() }
