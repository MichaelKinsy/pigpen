package bashanalysis

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// representative is a typical agent command: a chain, a wrapper, a substitution, a redirect.
const representative = `cd /repo && sudo xargs grep -rn "TODO" src/ | sort -u > /tmp/todo.txt; echo "$(git rev-parse HEAD)"`

func BenchmarkParseBashCommandsSync(b *testing.B) {
	bashparse.WarmBashParser()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := ParseBashCommandsSync(representative); !ok {
			b.Fatal("cold")
		}
	}
}

func BenchmarkCollectPathCandidateTokens(b *testing.B) {
	bashparse.WarmBashParser()
	tree := bashparse.GetWarmBashParser().Parse(representative)
	reader := bashparse.NewWordReader(bashparse.ScanShellVariables([]*bashparse.Node{tree.Root}))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CollectPathCandidateTokens(tree.Root, reader)
	}
}

func BenchmarkCollectCommands(b *testing.B) {
	bashparse.WarmBashParser()
	tree := bashparse.GetWarmBashParser().Parse(representative)
	reader := bashparse.NewWordReader(bashparse.ScanShellVariables([]*bashparse.Node{tree.Root}))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CollectCommands(tree.Root, reader)
	}
}
