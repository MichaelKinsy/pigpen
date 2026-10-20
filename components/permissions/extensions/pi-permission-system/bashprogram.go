package pi_permission_system

import (
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Port of src/access-intent/bash/program.ts: a bash command parsed once into a born-ready representation.
//
// Parsing is the expensive step; ParseBashProgram performs it a single time and eagerly resolves all three typed slices so the
// bash permission gates do not each re-parse or re-walk the command, and so the slices are guaranteed to agree.

// BashCommand is one command-pattern unit of a parsed bash program (re-exported as the original does).
type BashCommand = bashanalysis.BashCommand

// BashProgramOptions are the options of ParseBashProgram. Workdir, when non-nil (an aliased shell tool's working directory,
// #574), seeds the initial effective base as if the command were prefixed with `cd <workdir>`, so relative tokens resolve against
// it, and the workdir itself is flagged as external when it resolves outside the cwd.
type BashProgramOptions struct{ Workdir *string }

// BashProgram is a bash command parsed once: its command units, its external accesses and its path-rule candidates.
type BashProgram struct {
	sourceCommand string
	commandUnits  []BashCommand
	externals     []BashExternalPath
	candidates    []BashPathRuleCandidate
}

// ParseBashProgram parses a bash command into a born-ready BashProgram.
//
// It builds the full AST with the bash grammar, enumerates command units and walks path-candidate tokens once, then eagerly
// resolves all three slices through the injected PathNormalizer (platform and cwd baked in). Heredoc bodies, comments and other
// non-argument content are skipped. An unparseable command yields an empty program; the error is the parser failing to
// initialize.
//
// A bare token (`id_rsa`, `outside-link`) enters both slices when it names an existing filesystem entry: the existence probe the
// resolver owns (ADR 0009, #645). No policy is consulted, so every caller gets identical slices for a given command and working
// directory.
func ParseBashProgram(command string, normalizer *PathNormalizer, options *BashProgramOptions) (*BashProgram, error) {
	parser, err := bashparse.GetParser()
	if err != nil {
		return nil, err
	}
	tree := parser.Parse(command)
	if tree == nil {
		return &BashProgram{sourceCommand: command}, nil
	}
	var workdir *string
	if options != nil {
		workdir = options.Workdir
	}
	return bashparse.WithSalvagedRoots(tree.Root, parser, func(salvaged []*bashparse.Node) *BashProgram {
		roots := append([]*bashparse.Node{tree.Root}, salvaged...)
		words := bashparse.NewWordReader(bashparse.ScanShellVariables(roots))
		resolved := NewBashPathResolver(normalizer, words, workdir).Resolve(tree.Root, salvaged)
		units := bashanalysis.CollectCommands(tree.Root, words)
		for _, root := range salvaged {
			units = append(units, bashanalysis.CollectSalvagedCommands(root, words)...)
		}
		return &BashProgram{
			sourceCommand: command,
			commandUnits:  units,
			externals:     resolved.ExternalAccesses,
			candidates:    resolved.RuleCandidates,
		}
	}), nil
}

// CommandText is the source command string this program was parsed from.
func (p *BashProgram) CommandText() string { return p.sourceCommand }

// Commands are the top-level command-pattern units of the chain, in source order, with the units of nested executions
// (substitutions, subshells) and of salvaged regions appended as the enumeration produced them. A copy: callers cannot alter the
// program.
func (p *BashProgram) Commands() []BashCommand { return append([]BashCommand{}, p.commandUnits...) }

// ExternalAccesses are the deduplicated accesses that resolve outside the cwd: each an AccessPath (lexical form as typed,
// canonical form for the boundary decision) with its attributed effect.
func (p *BashProgram) ExternalAccesses() []BashExternalPath {
	return append([]BashExternalPath{}, p.externals...)
}

// PathRuleCandidates are the path-rule candidates paired with their policy lookup values and the attributed effect that routes
// each to a directional surface.
func (p *BashProgram) PathRuleCandidates() []BashPathRuleCandidate {
	return append([]BashPathRuleCandidate{}, p.candidates...)
}
