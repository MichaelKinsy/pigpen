package pi_permission_system

import "testing"

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
