package bashanalysis

import "github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"

// ParseBashCommandsSync synchronously enumerates the command-pattern units of a bash command using
// the warmed parser (sync-commands.ts parseBashCommandsSync).
//
// It returns ok=false when the parser has not been warmed yet (the pre-warm window), so the caller
// can fall back to whole-string matching rather than block. Once warm it mirrors the enumeration
// the gate performs: chains split, nested substitutions/subshells descend, opaque wrappers flagged
// (#306). Only the command-pattern surface is produced. An unparseable command yields an empty
// slice (the caller's decompose path fails it closed, #452).
func ParseBashCommandsSync(command string) ([]BashCommand, bool) {
	parser := bashparse.GetWarmBashParser()
	if parser == nil {
		return nil, false
	}
	tree := parser.Parse(command)
	if tree == nil {
		return []BashCommand{}, true
	}
	return bashparse.WithSalvagedRoots(tree.Root, parser, func(salvaged []*bashparse.Node) []BashCommand {
		words := bashparse.NewWordReader(bashparse.ScanShellVariables(append([]*bashparse.Node{tree.Root}, salvaged...)))
		commands := CollectCommands(tree.Root, words)
		for _, root := range salvaged {
			commands = append(commands, CollectSalvagedCommands(root, words)...)
		}
		return commands
	}), true
}
