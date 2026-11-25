package files

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

// Finding N8: End-to-end integration replay of all 20 edit calls (plus their reads)
// across all 4 edit oracle cases from /tmp/omp-proof2/reference/oracle/report.md
// and piglets/oh-my-pig/proof/cases/edit.json, going through the REAL `executeRead`
// -> `executeEdit` tool pipeline (not manually injecting seen lines into the store).
func TestOracleAll20EditCallsViaRealReadAndEdit(t *testing.T) {
	runRead := func(t *testing.T, store *hashline.Store, dir, path string) string {
		t.Helper()
		res, err := executeRead(store, dir, path)
		if err != nil {
			t.Fatalf("read(%q) unexpected error: %v", path, err)
		}
		return res.Content
	}
	runEdit := func(t *testing.T, store *hashline.Store, dir, input string) (string, bool) {
		t.Helper()
		res, err := executeEdit(sdk.Context{}, store, dir, input)
		if err != nil || res.IsError {
			if res.Content != "" {
				return res.Content, true
			}
			return err.Error(), true
		}
		return res.Content, false
	}

	t.Run("edit-hashline-ops (5 reads + 5 edits)", func(t *testing.T) {
		dir := t.TempDir()
		store := hashline.NewStore()
		if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("alpha\nbeta\ngamma\ndelta\nepsilon\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Call 0: read target.txt
		got0 := runRead(t, store, dir, "target.txt")
		want0 := "[target.txt#FEEF]\n1:alpha\n2:beta\n3:gamma\n4:delta\n5:epsilon"
		if got0 != want0 {
			t.Fatalf("call 0:\ngot:\n%s\nwant:\n%s", got0, want0)
		}

		// Call 1: PUT 2.=2: +BETA
		got1, isErr := runEdit(t, store, dir, "[target.txt#FEEF]\nPUT 2.=2:\n+BETA")
		want1 := "[target.txt#6E81]\n1:alpha\n2:BETA\n3:gamma\n4:delta"
		if isErr || got1 != want1 {
			t.Fatalf("call 1 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got1, want1)
		}

		// Call 2: PUT <1: +header
		got2, isErr := runEdit(t, store, dir, "[target.txt#6E81]\nPUT <1:\n+header")
		want2 := "[target.txt#77F5]\n1:header\n2:alpha\n3:BETA"
		if isErr || got2 != want2 {
			t.Fatalf("call 2 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got2, want2)
		}

		// Call 3: read target.txt (must record displayed lines 1..6 via real executeRead)
		got3 := runRead(t, store, dir, "target.txt")
		want3 := "[target.txt#77F5]\n1:header\n2:alpha\n3:BETA\n4:gamma\n5:delta\n6:epsilon"
		if got3 != want3 {
			t.Fatalf("call 3:\ngot:\n%s\nwant:\n%s", got3, want3)
		}

		// Call 4: PUT >6: +footer
		got4, isErr := runEdit(t, store, dir, "[target.txt#77F5]\nPUT >6:\n+footer")
		want4 := "[target.txt#1F76]\n5:delta\n6:epsilon\n7:footer"
		if isErr || got4 != want4 {
			t.Fatalf("call 4 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got4, want4)
		}

		// Call 5: read target.txt
		got5 := runRead(t, store, dir, "target.txt")
		want5 := "[target.txt#1F76]\n1:header\n2:alpha\n3:BETA\n4:gamma\n5:delta\n6:epsilon\n7:footer"
		if got5 != want5 {
			t.Fatalf("call 5:\ngot:\n%s\nwant:\n%s", got5, want5)
		}

		// Call 6: CUT 3.=4 @moved + PUT >7 @moved
		got6, isErr := runEdit(t, store, dir, "[target.txt#1F76]\nCUT 3.=4 @moved\nPUT >7 @moved")
		want6 := "[target.txt#1D7F]\n1:header\n2:alpha\n3:delta\n4:epsilon\n5:footer\n6:BETA\n7:gamma"
		if isErr || got6 != want6 {
			t.Fatalf("call 6 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got6, want6)
		}

		// Call 7: read target.txt
		got7 := runRead(t, store, dir, "target.txt")
		want7 := "[target.txt#1D7F]\n1:header\n2:alpha\n3:delta\n4:epsilon\n5:footer\n6:BETA\n7:gamma"
		if got7 != want7 {
			t.Fatalf("call 7:\ngot:\n%s\nwant:\n%s", got7, want7)
		}

		// Call 8: MV target-renamed.txt
		got8, isErr := runEdit(t, store, dir, "[target.txt#1D7F]\nMV target-renamed.txt")
		want8 := "[target-renamed.txt#1D7F]\nMoved to target-renamed.txt"
		if isErr || got8 != want8 {
			t.Fatalf("call 8 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got8, want8)
		}

		// Call 9: read target-renamed.txt
		got9 := runRead(t, store, dir, "target-renamed.txt")
		want9 := "[target-renamed.txt#1D7F]\n1:header\n2:alpha\n3:delta\n4:epsilon\n5:footer\n6:BETA\n7:gamma"
		if got9 != want9 {
			t.Fatalf("call 9:\ngot:\n%s\nwant:\n%s", got9, want9)
		}
	})

	t.Run("edit-block-and-rem (2 reads + 2 edits)", func(t *testing.T) {
		dir := t.TempDir()
		store := hashline.NewStore()
		if err := os.WriteFile(filepath.Join(dir, "mod.py"), []byte("def greet(name):\n    return name\n\ndef bye():\n    return \"bye\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("temp\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got0 := runRead(t, store, dir, "mod.py")
		want0 := "[mod.py#AE77]\n1:def greet(name):\n2:    return name\n3:\n4:def bye():\n5:    return \"bye\""
		if got0 != want0 {
			t.Fatalf("call 0:\ngot:\n%s\nwant:\n%s", got0, want0)
		}

		got1, isErr := runEdit(t, store, dir, "[mod.py#AE77]\nPUT 1*:\n+def greet(name):\n+    return f\"hello {name}\"")
		want1 := "[mod.py#CD28]\nPUT 1*: → resolved lines 1-2 (2 lines)\n1:def greet(name):\n2:    return f\"hello {name}\"\n3:\n4:def bye():\n5:    return \"bye\""
		if isErr || got1 != want1 {
			t.Fatalf("call 1 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got1, want1)
		}

		got2 := runRead(t, store, dir, "junk.txt")
		if got2 != "[junk.txt#B839]\n1:temp" {
			t.Fatalf("call 2: %q", got2)
		}

		got3, isErr := runEdit(t, store, dir, "[junk.txt#B839]\nREM")
		if isErr || got3 != "Deleted junk.txt" {
			t.Fatalf("call 3 (err=%v): %q", isErr, got3)
		}
	})

	t.Run("edit-errors-stale-tag-and-unseen (1 read + 2 edits)", func(t *testing.T) {
		dir := t.TempDir()
		store := hashline.NewStore()
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got0 := runRead(t, store, dir, "big.txt:1-2")
		want0 := "[big.txt#6E7D]\n1:l1\n2:l2\n3:l3\n4:l4\n5:l5\n\n[Showing lines 1-5 of 10. Use :6- to continue]"
		if got0 != want0 {
			t.Fatalf("call 0:\ngot:\n%s\nwant:\n%s", got0, want0)
		}

		got1, isErr := runEdit(t, store, dir, "[big.txt#0000]\nPUT 1.=1:\n+bad-tag")
		want1 := "Edit rejected for big.txt: hash #0000 is not from this session.\nThe current file hashes to #6E7D. If the rows below are the ones you mean, resend the edit under #6E7D; otherwise re-read the file with `read` and copy its [path#tag] header verbatim.\n\n*1:l1\n 2:l2\n 3:l3"
		if !isErr || got1 != want1 {
			t.Fatalf("call 1 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got1, want1)
		}

		got2, isErr := runEdit(t, store, dir, "[big.txt#6E7D]\nPUT 9.=9:\n+unseen-line")
		want2 := "This edit anchors to lines 9 of big.txt that [big.txt#6E7D] never displayed (it showed a partial range, a search hit, or a folded summary). Actual file content at those lines:\n  9:l9\nVerify the content matches what you intend to touch, then re-issue the edit with the same [path#tag] header — a straight retry now succeeds without a re-read. If the content does NOT match, fix your line numbers."
		if !isErr || got2 != want2 {
			t.Fatalf("call 2 (err=%v):\ngot:\n%s\nwant:\n%s", isErr, got2, want2)
		}
	})

	t.Run("edit-failures (2 reads + 11 edits)", func(t *testing.T) {
		dir := t.TempDir()
		store := hashline.NewStore()
		if err := os.WriteFile(filepath.Join(dir, "e.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if got := runRead(t, store, dir, "e.txt"); got != "[e.txt#2543]\n1:one\n2:two\n3:three" {
			t.Fatalf("call 0: %q", got)
		}
		if got := runRead(t, store, dir, "f.txt"); got != "[f.txt#CECD]\n1:keep" {
			t.Fatalf("call 1: %q", got)
		}

		steps := []struct {
			name    string
			input   string
			wantErr bool
			want    string
		}{
			{
				"call 2 missing header",
				"PUT 1.=1:\n+x",
				true,
				`input must begin with "[PATH#HASH]" on the first non-blank line for anchored edits; got: "PUT 1.=1:". Example: "[src/foo.ts#1A2B]" then edit ops.`,
			},
			{
				"call 3 missing file",
				"[nope.txt#0000]\nPUT 1.=1:\n+x",
				true,
				"File not found: nope.txt. Use the write tool to create new files.",
			},
			{
				"call 4 out-of-range unseen-lines",
				"[e.txt#2543]\nPUT 9.=9:\n+x",
				true,
				"This edit anchors to lines 9 of e.txt that [e.txt#2543] never displayed (it showed a partial range, a search hit, or a folded summary). Re-read them in full first with a ranged read like `e.txt:9` — it skips summarization and mints a fresh tag (a plain re-read just re-folds them) — then re-issue the edit.",
			},
			{
				"call 5 empty PUT body",
				"[e.txt#2543]\nPUT 1.=1:",
				false,
				"[e.txt#6DBE]\n1:two\n2:three\n\nWarnings:\nInterpreted an empty `PUT` body as deletion. Use `CUT N.=M` or `CUT N*` for bodyless deletes.",
			},
			{
				"call 6 unified diff -old",
				"[e.txt#6DBE]\nPUT 1.=1:\n-one\n+ONE",
				false,
				"[e.txt#2B61]\n1:ONE\n2:three\n\nWarnings:\nIgnored unified-diff `-old` row(s); the range already removes old content, so only `+new` rows were kept.",
			},
			{
				"call 7 unknown op",
				"[e.txt#2B61]\nFOO 1",
				true,
				`line 1: payload line has no preceding hunk header. Use ` + "`PUT N.=M:`" + `, ` + "`CUT N.=M`" + `, or ` + "`PUT <N:`/`PUT >N:`" + ` above the body. Got "FOO 1".`,
			},
			{
				"call 8 overlapping hunks",
				"[e.txt#2B61]\nPUT 1.=2:\n+a\nPUT 2.=3:\n+b",
				true,
				"line 3: anchor line 2 is already targeted by another hunk on line 1. Issue ONE hunk per range; payload is only the final desired content, never a before/after pair.",
			},
			{
				"call 9 empty named register paste",
				"[e.txt#2B61]\nPUT >1 @nope",
				false,
				"Edits to e.txt parsed and applied cleanly, but produced no change: your body row(s) are byte-identical to the file at the targeted lines. The bug is somewhere else — re-read the file before issuing another edit. Do NOT widen the payload or add lines; verify the anchor first.",
			},
			{
				"call 10 multi-file edit",
				"[e.txt#2B61]\nPUT >$:\n+tail\n[f.txt#CECD]\nPUT <1:\n+head",
				false,
				"[e.txt#7587]\n1:ONE\n2:three\n3:tail\n\n[f.txt#2218]\n1:head\n2:keep",
			},
			{
				"call 11 MV over existing",
				"[e.txt#7587]\nMV f.txt",
				false,
				"[f.txt#7587]\nMoved to f.txt",
			},
			{
				"call 12 empty input",
				"",
				true,
				`input must begin with "[PATH#HASH]" on the first non-blank line for anchored edits; got: "". Example: "[src/foo.ts#1A2B]" then edit ops.`,
			},
		}

		for _, step := range steps {
			got, isErr := runEdit(t, store, dir, step.input)
			if isErr != step.wantErr || got != step.want {
				t.Fatalf("%s (isErr=%v wantErr=%v):\ngot:\n%s\nwant:\n%s", step.name, isErr, step.wantErr, got, step.want)
			}
		}
	})
}
