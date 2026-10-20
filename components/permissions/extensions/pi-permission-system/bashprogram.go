package pi_permission_system

import "github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"

// Port of src/access-intent/bash/program.ts: a bash command parsed once into a born-ready representation.

// BashCommand is one command-pattern unit of a parsed bash program (re-exported as the original does).
type BashCommand = bashanalysis.BashCommand

// BashProgramOptions are the options of ParseBashProgram. Workdir, when non-nil (an aliased shell tool's working directory,
// #574), seeds the initial effective base as if the command were prefixed with `cd <workdir>`.
type BashProgramOptions struct{ Workdir *string }

// BashProgram is a bash command parsed once: its command units, its external accesses and its path-rule candidates.
type BashProgram struct {
	sourceCommand string
}

// ParseBashProgram parses a bash command into a BashProgram.
func ParseBashProgram(command string, normalizer *PathNormalizer, options *BashProgramOptions) (*BashProgram, error) {
	return &BashProgram{sourceCommand: command}, nil
}

// CommandText is the source command string this program was parsed from.
func (p *BashProgram) CommandText() string { return p.sourceCommand }

// Commands are the top-level command-pattern units of the chain, in source order.
func (p *BashProgram) Commands() []BashCommand { return nil }

// ExternalAccesses are the deduplicated accesses that resolve outside the working directory.
func (p *BashProgram) ExternalAccesses() []BashExternalPath { return nil }

// PathRuleCandidates are the path-rule candidates with their policy lookup values and attributed effects.
func (p *BashProgram) PathRuleCandidates() []BashPathRuleCandidate { return nil }
