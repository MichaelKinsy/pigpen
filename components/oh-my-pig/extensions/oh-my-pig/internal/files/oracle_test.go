package files

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

const oracleSampleGo = `package sample

import (
	"fmt"
	"strings"
)

// Greeter says hello.
type Greeter struct {
	Name string
}

// Greet returns the greeting.
func (g Greeter) Greet(times int) string {
	var b strings.Builder
	for i := 0; i < times; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("hello " + g.Name)
	}
	return b.String()
}

func helper(x int) int {
	return x * 2
}

func main() {
	g := Greeter{Name: "pig"}
	fmt.Println(g.Greet(helper(1)))
}
`

// The expected text of every case is what omp 19.1.1 itself printed for the same file and the same call.
func TestOracleExactCases(t *testing.T) {
	dir := t.TempDir()
	samplePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(samplePath, []byte(oracleSampleGo), 0o644); err != nil {
		t.Fatal(err)
	}

	store := hashline.NewStore()
	// 0. whole-file read (establishes full-file seen-lines for #E2CA as in the oracle run)
	if _, err := executeRead(store, dir, "sample.go"); err != nil {
		t.Fatalf("read whole: %v", err)
	}

	// 1. read sample.go:16-19
	res1, err := executeRead(store, dir, "sample.go:16-19")
	if err != nil {
		t.Fatalf("read :16-19: %v", err)
	}
	want1 := strings.Join([]string{
		"[sample.go#E2CA]",
		"15:\tvar b strings.Builder",
		"16:\tfor i := 0; i < times; i++ {",
		"17:\t\tif i > 0 {",
		"18:\t\t\tb.WriteString(\", \")",
		"19:\t\t}",
		"20:\t\tb.WriteString(\"hello \" + g.Name)",
		"21:\t}",
		"22:\treturn b.String()",
		"",
		"[Showing lines 15-22 of 32. Use :23- to continue]",
	}, "\n")
	if res1.Content != want1 {
		t.Fatalf("read :16-19 mismatch:\ngot:\n%s\n\nwant:\n%s", res1.Content, want1)
	}

	// 2. read sample.go:3-4,26,30-31
	res2, err := executeRead(store, dir, "sample.go:3-4,26,30-31")
	if err != nil {
		t.Fatalf("read comma: %v", err)
	}
	want2 := strings.Join([]string{
		"[sample.go#E2CA]",
		"3:import (",
		"4:\t\"fmt\"",
		"…",
		"6:)",
		"…",
		"26:\treturn x * 2",
		"…",
		"30:\tg := Greeter{Name: \"pig\"}",
		"31:\tfmt.Println(g.Greet(helper(1)))",
	}, "\n")
	if res2.Content != want2 {
		t.Fatalf("read comma mismatch:\ngot:\n%s\n\nwant:\n%s", res2.Content, want2)
	}

	// 3. read directory (deep/ listed, NOT descended into)
	subDir := filepath.Join(dir, "sub")
	deepDir := filepath.Join(subDir, "deep")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "two.txt"), []byte("2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 15; i++ {
		p := filepath.Join(subDir, fmt.Sprintf("f%d.txt", i))
		if err := os.WriteFile(p, []byte("a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(subDir, "one.txt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pin mtimes so ordering is deterministic.
	fixed := time.Now().Add(-10 * time.Minute)
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chtimes(p, fixed, fixed)
		}
		return nil
	})
	dirRes, err := executeRead(store, dir, ".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if strings.Contains(dirRes.Content, "two.txt") {
		t.Fatalf("directory listing descended into deep/:\n%s", dirRes.Content)
	}
	if !strings.Contains(dirRes.Content, "    - deep/") || !strings.Contains(dirRes.Content, "    - … 5 more") || !strings.Contains(dirRes.Content, "    - one.txt") {
		t.Fatalf("directory listing missing expected entries:\n%s", dirRes.Content)
	}

	// 4. edit with three ops on original file (#E2CA -> #0AC9)
	edit1Input := strings.Join([]string{
		"[sample.go#E2CA]",
		"PUT 10.=10:",
		"+\tName  string",
		"+\tTitle string",
		"PUT >23:",
		"+",
		"+// Shout is loud.",
		"+func Shout(s string) string { return strings.ToUpper(s) }",
		"CUT 25.=27",
	}, "\n")
	edit1Res, err := executeEdit(sdk.Context{}, store, dir, edit1Input)
	if err != nil {
		t.Fatalf("edit1: %v", err)
	}
	wantEdit1 := strings.Join([]string{
		"[sample.go#0AC9]",
		"8:// Greeter says hello.",
		"9:type Greeter struct {",
		"10:\tName  string",
		"11:\tTitle string",
		"12:}",
		"13:",
		"…",
		"15:func (g Greeter) Greet(times int) string {",
		"…",
		"24:}",
		"25:",
		"26:// Shout is loud.",
		"27:func Shout(s string) string { return strings.ToUpper(s) }",
		"28:",
		"29:",
		"30:func main() {",
		"31:\tg := Greeter{Name: \"pig\"}",
		"…",
		"33:}",
	}, "\n")
	if edit1Res.Content != wantEdit1 {
		t.Fatalf("edit1 mismatch:\ngot:\n%s\n\nwant:\n%s", edit1Res.Content, wantEdit1)
	}

	// 5. edit naming old in-session tag #E2CA when file is at #0AC9 -> recovers and warns (#DA7A)
	edit2Input := "[sample.go#E2CA]\nPUT 1.=1:\n+package other\n"
	edit2Res, err := executeEdit(sdk.Context{}, store, dir, edit2Input)
	if err != nil {
		t.Fatalf("edit2 (recovery): %v", err)
	}
	wantEdit2 := strings.Join([]string{
		"[sample.go#DA7A]",
		"1:package other",
		"2:",
		"3:import (",
		"…",
		"6:)",
		"",
		"Warnings:",
		"Recovered from a stale file hash using an earlier in-session snapshot (a prior edit in this session advanced the hash).",
	}, "\n")
	if edit2Res.Content != wantEdit2 {
		t.Fatalf("edit2 mismatch:\ngot:\n%s\n\nwant:\n%s", edit2Res.Content, wantEdit2)
	}

	// 6. edit naming #0AC9 after external/unrecoverable change (simulate unrecoverable by modifying line 30 in a separate write or testing mismatch formatting directly)
	// First verify that an unrecoverable stale tag produces * on target line and space on context lines.
	if err := os.WriteFile(samplePath, []byte(strings.Replace(func() string {
		b, _ := os.ReadFile(samplePath)
		return string(b)
	}(), "func main() {", "func main() { // changed", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	res6, err6 := executeEdit(sdk.Context{}, store, dir, "[sample.go#0AC9]\nPUT 30*:\n+func main() {\n+\tfmt.Println(\"done\")\n+}\n")
	if err6 != nil {
		t.Fatalf("expected nil Go error with IsError:true, got %v", err6)
	}
	if !res6.IsError {
		t.Fatalf("expected stale tag rejection when target block changed")
	}
	if !strings.Contains(res6.Content, "Edit rejected for sample.go: file changed between read and edit.") ||
		!strings.Contains(res6.Content, "\n 28:\n 29:\n*30:func main() { // changed\n 31:") {
		t.Fatalf("unexpected stale tag rejection:\n%v", res6.Content)
	}
	// Restore #DA7A on disk for case 7.
	bCur, _ := os.ReadFile(samplePath)
	if err := os.WriteFile(samplePath, []byte(strings.Replace(string(bCur), "func main() { // changed", "func main() {", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	store.RecordSeenLines(samplePath, "DA7A", []int{28, 29, 30, 31, 32})

	// 7. block op with current tag #DA7A -> prints PUT 30*: → resolved lines 30-33 (4 lines)
	edit7Input := strings.Join([]string{
		"[sample.go#DA7A]",
		"PUT 30*:",
		"+func main() {",
		"+\tfmt.Println(\"done\")",
		"+}",
	}, "\n")
	edit7Res, err := executeEdit(sdk.Context{}, store, dir, edit7Input)
	if err != nil {
		t.Fatalf("edit7: %v", err)
	}
	wantEdit7 := strings.Join([]string{
		"[sample.go#4FA2]",
		"PUT 30*: → resolved lines 30-33 (4 lines)",
		"29:",
		"30:func main() {",
		"31:\tfmt.Println(\"done\")",
		"32:}",
	}, "\n")
	if edit7Res.Content != wantEdit7 {
		t.Fatalf("edit7 mismatch:\ngot:\n%s\n\nwant:\n%s", edit7Res.Content, wantEdit7)
	}
}
