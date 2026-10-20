package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Twins of output-file.test.ts, output-file-path.test.ts, output-file-compaction-e2e.test.ts and
// output-transcript-wiring.test.ts. The original's fake session (a messages array and a subscription) becomes a
// fakeSession feeding the stream the events a child emits (obs_helpers_test.go).

func TestEncodeCwdTwins(t *testing.T) {
	cases := []struct{ title, in, want string }{
		{"encodes a POSIX absolute path by stripping the leading slash and replacing separators", "/home/user/project", "home-user-project"},
		{"handles a POSIX root path", "/", ""},
		{"encodes a Windows drive-letter path by stripping the drive prefix", `C:\Users\foo\project`, "Users-foo-project"},
		{"handles lowercase Windows drives", `c:\foo`, "foo"},
		{"handles a Windows path written with forward slashes", "C:/Users/foo/project", "Users-foo-project"},
		{"preserves server and share for UNC paths", `\\server\share\project`, "server-share-project"},
		{"handles mixed separators", `/home\user/project`, "home-user-project"},
		{"collapses runs of leading dashes after separator replacement", "///foo", "foo"},
		{"returns an empty string for an empty cwd", "", ""},
		{"leaves a relative-looking path with no leading separator alone", "foo/bar", "foo-bar"},
	}
	for _, c := range cases {
		tw(t, "output-file", c.title, func(t *testing.T) { eq(t, encodeCwd(c.in), c.want) })
	}
}

func assistant(text string) map[string]any {
	return obj{"role": "assistant", "content": []any{obj{"type": "text", "text": text}}}
}
func user(text string) map[string]any { return obj{"role": "user", "content": text} }

// streamFixture is the beforeEach of the original: an output file holding the initial entry.
func streamFixture(t *testing.T, startIndex int, initial ...map[string]any) (*fakeSession, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.output")
	writeInitialEntry(path, "agent-1", "do the thing", "/work")
	return newFakeSession(newTranscriptStream(path, "agent-1", "/work", startIndex), initial...), path
}

func TestStreamToOutputFileTwins(t *testing.T) {
	tw(t, "output-file", "writes nothing past the initial entry until turn_end fires", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("do the thing"))
		s.push(assistant("ok"))
		eq(t, len(readEntries(t, path)), 1) // only the initial entry
		s.fire(obj{"type": "turn_end"})
		eq(t, len(readEntries(t, path)), 2)
	})
	tw(t, "output-file", "tags assistant, user, and tool messages with the correct type field", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("thinking"), user("follow-up"), obj{"role": "tool", "content": []any{obj{"type": "tool_result", "content": "x"}}})
		s.fire(obj{"type": "turn_end"})
		var kinds []any
		for _, e := range readEntries(t, path) {
			kinds = append(kinds, e["type"])
			eq(t, e["agentId"], "agent-1")
			eq(t, e["isSidechain"], true)
			eq(t, e["cwd"], "/work")
		}
		eq(t, kinds, []any{"user", "assistant", "user", "toolResult"})
	})
	tw(t, "output-file", "never re-emits messages already flushed on a previous turn_end", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("one"))
		s.fire(obj{"type": "turn_end"})
		s.push(assistant("two"))
		s.fire(obj{"type": "turn_end"})
		s.fire(obj{"type": "turn_end"}) // a redundant turn_end with no new messages must not duplicate
		eq(t, len(readEntries(t, path)), 3)
	})
	tw(t, "output-file", "ignores session events other than turn_end", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("x"))
		s.fire(obj{"type": "message_start"})
		s.fire(obj{"type": "tool_call"})
		s.fire(obj{"type": "message_end"})
		eq(t, len(readEntries(t, path)), 1)
	})
	tw(t, "output-file", "resumes streaming after compaction shrinks the message array (#145)", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("one"), user("q2"), assistant("two"))
		s.fire(obj{"type": "turn_end"}) // 4 messages flushed
		s.fire(obj{"type": "compaction_start", "reason": "manual"})
		s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": false, "result": obj{"summary": "s"}})
		s.push(assistant("AFTER"))
		s.fire(obj{"type": "turn_end"})
		entries := readEntries(t, path)
		eq(t, len(entries), 5) // initial + one + q2 + two + AFTER: the kept tail is NOT re-written
		eq(t, strings.Contains(string(jsonLine(entries[len(entries)-1])), "AFTER"), true)
	})
	tw(t, "output-file", "flushes the not-yet-written tail before compaction discards it (#145)", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("tail-before-compact")) // a message lands with no turn_end yet (overflow mid-turn)
		eq(t, len(readEntries(t, path)), 1)
		s.fire(obj{"type": "compaction_start", "reason": "overflow"})
		entries := readEntries(t, path)
		eq(t, strings.Contains(string(jsonLine(entries[len(entries)-1])), "tail-before-compact"), true)
	})
	tw(t, "output-file", "re-anchors after the overflow-retry trim, not at compaction_end (#145)", func(t *testing.T) {
		// Pi emits compaction_end and trims the trailing error message afterwards; here the stream only sees what the
		// child's events carry, so the first message after compaction must still be written.
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("big"))
		s.fire(obj{"type": "turn_end"})
		s.fire(obj{"type": "compaction_start", "reason": "overflow"})
		s.fire(obj{"type": "compaction_end", "reason": "overflow", "aborted": false, "result": obj{"summary": "s"}, "willRetry": true})
		s.push(assistant("RETRY-ANSWER"))
		s.fire(obj{"type": "turn_end"})
		entries := readEntries(t, path)
		eq(t, strings.Contains(string(jsonLine(entries[len(entries)-1])), "RETRY-ANSWER"), true)
	})
	tw(t, "output-file", "does not re-anchor on aborted or failed compaction (#145)", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.fire(obj{"type": "compaction_start", "reason": "manual"})
		s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": true})
		s.fire(obj{"type": "compaction_end", "reason": "manual", "aborted": false, "errorMessage": "boom"})
		s.push(assistant("still-streaming"))
		s.fire(obj{"type": "turn_end"})
		entries := readEntries(t, path)
		eq(t, len(entries), 2) // nothing skipped, nothing duplicated
		eq(t, strings.Contains(string(jsonLine(entries[1])), "still-streaming"), true)
	})
	tw(t, "output-file", "cleanup() does a final flush and detaches the subscription", func(t *testing.T) {
		s, path := streamFixture(t, 1, user("go"))
		s.push(assistant("tail")) // a trailing message arrives with no turn_end before shutdown
		eq(t, len(readEntries(t, path)), 1)
		s.stream.close()
		eq(t, len(readEntries(t, path)), 2)
		s.push(assistant("ghost")) // post-cleanup messages must not be written
		s.fire(obj{"type": "turn_end"})
		eq(t, len(readEntries(t, path)), 2)
	})
	tw(t, "output-file", "starts at the given index so a resume appends only its own turns", func(t *testing.T) {
		prior := []map[string]any{user("do the thing"), assistant("first answer"), user("and again"), assistant("second answer")}
		s, path := streamFixture(t, len(prior), prior...)
		s.push(user("keep going"), assistant("third answer"))
		s.fire(obj{"type": "turn_end"})
		entries := readEntries(t, path)
		eq(t, len(entries), 3) // the initial entry + exactly the two new messages
		body := string(jsonLine(entries))
		eq(t, strings.Contains(body, "keep going") && strings.Contains(body, "third answer"), true)
		eq(t, strings.Contains(body, "first answer") || strings.Contains(body, "second answer"), false)
	})
}

func TestOutputFilePathTwins(t *testing.T) {
	uid := os.Getuid()
	if uid < 0 {
		uid = 0
	}
	fixture := func(t *testing.T) string { // a private tmpdir, as the original redirects tmpdir()
		t.Helper()
		dir := t.TempDir()
		t.Setenv("TMPDIR", dir)
		t.Setenv("TMP", dir)
		t.Setenv("TEMP", dir)
		return filepath.Join(os.TempDir(), "pi-subagents-"+strconv.Itoa(uid))
	}
	tw(t, "output-file-path", "builds the documented layout: <root>/<encoded-cwd>/<session>/tasks/<agent>.output", func(t *testing.T) {
		root := fixture(t)
		path, err := createOutputFilePath("/home/user/project", "agent-xyz", "session-123")
		eq(t, err, nil)
		eq(t, path, filepath.Join(root, "home-user-project", "session-123", "tasks", "agent-xyz.output"))
	})
	tw(t, "output-file-path", "creates the directory chain so the first write cannot fail", func(t *testing.T) {
		fixture(t)
		path, _ := createOutputFilePath("/home/user/project", "agent-xyz", "session-123")
		st, err := os.Stat(filepath.Dir(path))
		eq(t, err, nil)
		eq(t, st.IsDir(), true)
	})
	tw(t, "output-file-path", "keeps distinct cwds in separate subdirectories under the shared root", func(t *testing.T) {
		fixture(t)
		a, _ := createOutputFilePath("/home/user/project", "agent-xyz", "session-123")
		b, _ := createOutputFilePath("/home/user/other", "agent-2", "session-123")
		eq(t, strings.Contains(a, "home-user-project"), true)
		eq(t, strings.Contains(b, "home-user-other"), true)
		eq(t, a != b, true)
	})
	// Not in the ledger (skipIf win32 in the original): the root is owner-only, also when it pre-exists wide.
	if runtime.GOOS != "windows" {
		t.Run("creates the root owner-only", func(t *testing.T) {
			root := fixture(t)
			_, _ = createOutputFilePath("/home/user/project", "agent-xyz", "session-123")
			st, _ := os.Stat(root)
			eq(t, st.Mode().Perm(), os.FileMode(0o700))
		})
		t.Run("re-tightens a pre-existing world-readable root", func(t *testing.T) {
			root := fixture(t)
			must(t, os.MkdirAll(root, 0o755))
			must(t, os.Chmod(root, 0o755))
			_, _ = createOutputFilePath("/home/user/project", "agent-xyz", "session-123")
			st, _ := os.Stat(root)
			eq(t, st.Mode().Perm(), os.FileMode(0o700))
		})
	}
}

// The original drives a real pi session through a real compaction. Here a real rpcChild reads a scripted child's
// event stream through the stream (the fake pig's "compaction" mode: six exchanges, a compaction, a final one).
func TestOutputFileSurvivesCompactionTwin(t *testing.T) {
	tw(t, "output-file-compaction-e2e", "keeps writing post-compaction messages to the output file", func(t *testing.T) {
		useFakePig(t, "compaction")
		path := filepath.Join(t.TempDir(), "agent.output")
		writeInitialEntry(path, "agent-145", "repro", t.TempDir())
		stream := newTranscriptStream(path, "agent-145", "/work", 1)
		c, err := startRPCChild(t.Context(), childSpec{Prompt: "repro", Cwd: t.TempDir(), Hooks: &childHooks{Event: stream.onEvent}})
		eq(t, err, nil)
		_, err = c.wait()
		eq(t, err, nil)
		stream.close()
		entries := readEntries(t, path)
		all := string(jsonLine(entries))
		eq(t, strings.Contains(all, "final question"), true)
		eq(t, strings.Contains(all, "POST-COMPACTION-ANSWER"), true)
		// Re-anchoring did not double-write: every pre-compaction answer appears exactly once.
		for i := 0; i < 6; i++ {
			eq(t, strings.Count(all, "answer-"+strconv.Itoa(i)+" "), 1)
		}
		eq(t, len(entries), 1+11+2) // the initial entry, 5 first prompts + 6 answers, then the final question and its answer
	})
}

// output-transcript-wiring: which spawns write a transcript. The original asserts on mocked output-file functions;
// here the observable is record.OutputFile and the file.
func TestOutputTranscriptWiringTwins(t *testing.T) {
	run := func(t *testing.T, settingsJSON, frontmatter string, background bool) *agentRecord {
		t.Helper()
		r := startObsRig(t, obsWorkspace(t, settingsJSON, frontmatter))
		sub := "general-purpose"
		if frontmatter != "" {
			sub = "custom"
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.must("Agent", obj{"prompt": "process the data", "description": "d", "subagent_type": sub, "run_in_background": background})
		}()
		c := r.fleet.wait(t, 1)[0]
		c.finish("done")
		<-done
		rec := r.app.mgr.list()[0]
		<-rec.done
		return rec
	}
	tw(t, "output-transcript-wiring", "creates no transcript when a custom agent sets output_transcript false", func(t *testing.T) {
		rec := run(t, "", "output_transcript: false", false)
		eq(t, rec.OutputFile, "")
		eq(t, rec.transcript == nil, true)
	})
	tw(t, "output-transcript-wiring", "also suppresses the background transcript", func(t *testing.T) {
		rec := run(t, "", "output_transcript: false\nrun_in_background: true", true)
		eq(t, rec.OutputFile, "")
		eq(t, rec.transcript == nil, true)
	})
	tw(t, "output-transcript-wiring", "keeps transcript creation as the default", func(t *testing.T) {
		rec := run(t, "", "", false)
		eq(t, rec.OutputFile != "", true)
		eq(t, rec.transcript != nil, true)
		entries := readEntries(t, rec.OutputFile)
		eq(t, entries[0]["agentId"], rec.ID)
		eq(t, entries[0]["message"], obj{"role": "user", "content": "process the data"})
	})
	tw(t, "output-transcript-wiring", "suppresses the transcript project-wide when subagents.json sets outputTranscript false", func(t *testing.T) {
		rec := run(t, `{"outputTranscript": false}`, "", false)
		eq(t, rec.OutputFile, "")
		eq(t, rec.transcript == nil, true)
	})
	tw(t, "output-transcript-wiring", "lets agent frontmatter output_transcript true override a project outputTranscript false", func(t *testing.T) {
		rec := run(t, `{"outputTranscript": false}`, "output_transcript: true", false)
		eq(t, rec.OutputFile != "", true)
		eq(t, rec.transcript != nil, true)
	})
}

var _ = time.Second
