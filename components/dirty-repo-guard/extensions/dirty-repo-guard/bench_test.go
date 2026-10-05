package dirty_repo_guard

import (
	"strconv"
	"strings"
	"testing"
)

// The guard runs on a session switch or fork: one `git status --porcelain` (a process, the dominant cost) and
// then decide() over its output. A monorepo with thousands of changed files is the worst case for decide.
//
//	go test -run xxx -bench . -benchmem
func porcelain(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(" M src/module" + strconv.Itoa(i%50) + "/file" + strconv.Itoa(i) + ".go\n")
	}
	return b.String()
}

func BenchmarkDecideClean(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		decide(0, "", true)
	}
}

func BenchmarkDecide5000Changed(b *testing.B) {
	out := porcelain(5000)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		decide(0, out, true)
	}
}
