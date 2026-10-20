package pi_permission_system

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

func TestCommandRedactionTwins(t *testing.T) {
	const f = "logging/command-redaction"
	const M = RedactedPlaceholder
	warm := func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		bashparse.WarmBashParser()
		t.Cleanup(bashparse.ResetWarmBashParser)
	}
	red := func(t *testing.T, in, want string) {
		t.Helper()
		eq(t, RedactCommandSecrets(in), want, in)
	}
	tw(t, f, "masks a quoted env-prefix value", func(t *testing.T) { warm(t); red(t, `KEY="sk-abc123" curl https://x`, "KEY="+M+" curl https://x") })
	tw(t, f, "masks a bare value", func(t *testing.T) { warm(t); red(t, "KEY=sk-abc123", "KEY="+M) })
	tw(t, f, "masks a value under an export declaration", func(t *testing.T) {
		warm(t)
		red(t, `export OPENROUTER_KEY="sk-or-v1-xyz"`, "export OPENROUTER_KEY="+M)
	})
	tw(t, f, "masks a value containing spaces, which the parse keeps whole", func(t *testing.T) { warm(t); red(t, `TOKEN="a b c" deploy`, "TOKEN="+M+" deploy") })
	tw(t, f, "leaves an assignment whose name binds no credential", func(t *testing.T) { warm(t); red(t, `OUT="/tmp/x" deploy`, `OUT="/tmp/x" deploy`) })
	tw(t, f, "leaves an assignment with no value at all", func(t *testing.T) { warm(t); red(t, "KEY=", "KEY=") })
	tw(t, f, "masks the value after an env prefix", func(t *testing.T) { warm(t); red(t, "env MY_KEY=abc deploy", "env MY_KEY="+M+" deploy") })
	tw(t, f, "leaves a long-option argument, which binds no name", func(t *testing.T) { warm(t); red(t, "deploy --my-key=abc", "deploy --my-key=abc") })
	tw(t, f, "masks a double-quoted header argument", func(t *testing.T) {
		warm(t)
		red(t, `curl -sS -H "Authorization: Bearer sk-abc" https://x`, `curl -sS -H "Authorization:`+M+`" https://x`)
	})
	tw(t, f, "masks a single-quoted header argument", func(t *testing.T) {
		warm(t)
		red(t, `curl --header 'Authorization: Bearer sk-abc' https://x`, `curl --header 'Authorization:`+M+`' https://x`)
	})
	tw(t, f, "masks a header argument concatenated onto its flag", func(t *testing.T) {
		warm(t)
		red(t, `curl -H"Authorization: Bearer sk-abc" https://x`, `curl -H"Authorization:`+M+`" https://x`)
	})
	tw(t, f, "masks a header whose value is an expansion, keeping the quoting balanced", func(t *testing.T) {
		warm(t)
		red(t, `curl -H "Authorization: "$TOKEN https://x`, `curl -H "Authorization:`+M+`" https://x`)
	})
	tw(t, f, "masks an unquoted header argument", func(t *testing.T) {
		warm(t)
		red(t, "curl -H X-Api-Key:sk-abc https://x", "curl -H X-Api-Key:"+M+" https://x")
	})
	tw(t, f, "closes the quote the mask swallowed, not the one the argument opens with", func(t *testing.T) {
		warm(t)
		red(t, `curl -H Auth"orization: "$TOKEN https://x`, `curl -H Auth"orization:`+M+`" https://x`)
	})
	tw(t, f, "leaves a header field that binds no credential", func(t *testing.T) {
		warm(t)
		red(t, `curl -H "Content-Type: application/json" https://x`, `curl -H "Content-Type: application/json" https://x`)
	})
	tw(t, f, "leaves a camel-cased name, which is no HTTP field name", func(t *testing.T) {
		warm(t)
		red(t, `grep -n "legalDirectionalKeys: readonly" x.ts`, `grep -n "legalDirectionalKeys: readonly" x.ts`)
	})
	tw(t, f, "leaves a secret typed as a search pattern", func(t *testing.T) { warm(t); red(t, `grep -r "sk-ant-oat01-abc" .`, `grep -r "sk-ant-oat01-abc" .`) })
	tw(t, f, "leaves an assignment inside another language's source", func(t *testing.T) {
		warm(t)
		red(t, `python3 -c "print(sorted(d, key=lambda x: x[1]))"`, `python3 -c "print(sorted(d, key=lambda x: x[1]))"`)
	})
	tw(t, f, "leaves a flag-separated value", func(t *testing.T) { warm(t); red(t, "sort --key 2 f.txt", "sort --key 2 f.txt") })
	tw(t, f, "masks every one, left to right", func(t *testing.T) {
		warm(t)
		red(t, `KEY=sk-one curl -H "Authorization: Bearer sk-two" && TOKEN=sk-three deploy`, "KEY="+M+` curl -H "Authorization:`+M+`" && TOKEN=`+M+" deploy")
	})
	tw(t, f, "masks an enclosing span once rather than nesting a second mask inside it", func(t *testing.T) { warm(t); red(t, `KEY="Authorization: Bearer x" deploy`, "KEY="+M+" deploy") })

	t.Run("a secret inside an inline-shell payload", func(t *testing.T) {
		warm(t)
		for _, c := range [][3]string{
			{"a single-quoted bash -c payload", `bash -c 'TOKEN=sk-secret deploy'`, `bash -c 'TOKEN=` + M + ` deploy'`},
			{"a double-quoted sh -c payload", `sh -c "API_KEY=sk-x curl https://x"`, `sh -c "API_KEY=` + M + ` curl https://x"`},
			{"an eval payload, which takes no flag", `eval "TOKEN=sk-secret deploy"`, `eval "TOKEN=` + M + ` deploy"`},
			{"a payload whose value is an expansion", `bash -c "API_KEY=$SECRET deploy"`, `bash -c "API_KEY=` + M + ` deploy"`},
			{"a -c cluster carrying other short flags", `bash -ec 'export OPENROUTER_KEY="sk-y"'`, `bash -ec 'export OPENROUTER_KEY=` + M + `'`},
			{"a path-qualified shell", `/bin/bash -c 'MY_KEY=abc deploy'`, `/bin/bash -c 'MY_KEY=` + M + ` deploy'`},
			{"a payload behind a leading redirect", `>/dev/null bash -c 'TOKEN=sk-secret deploy'`, `>/dev/null bash -c 'TOKEN=` + M + ` deploy'`},
			{"a payload after a redirect between the shell and its flag", `bash 2>/dev/null -c 'TOKEN=sk-secret deploy'`, `bash 2>/dev/null -c 'TOKEN=` + M + ` deploy'`},
			{"a payload after a heredoc between the shell and its flag", "bash <<EOF -c 'TOKEN=sk-secret deploy'\nbody\nEOF", "bash <<EOF -c 'TOKEN=" + M + " deploy'\nbody\nEOF"},
			{"an unquoted payload, which the word rule already reached", `bash -c TOKEN=sk-x`, `bash -c TOKEN=` + M},
			{"a payload behind sudo", `sudo bash -c 'TOKEN=sk-secret deploy'`, `sudo bash -c 'TOKEN=` + M + ` deploy'`},
			{"a payload behind xargs", `xargs -I{} sh -c 'TOKEN=sk-secret deploy'`, `xargs -I{} sh -c 'TOKEN=` + M + ` deploy'`},
			{"a payload behind timeout, which takes a leading operand", `timeout 5 bash -c 'TOKEN=sk-secret deploy'`, `timeout 5 bash -c 'TOKEN=` + M + ` deploy'`},
			{"a payload behind two wrapper layers", `sudo timeout 5 bash -c 'TOKEN=sk-secret deploy'`, `sudo timeout 5 bash -c 'TOKEN=` + M + ` deploy'`},
		} {
			t.Run("masks "+c[0], func(t *testing.T) { red(t, c[1], c[2]) })
		}
		for _, c := range [][2]string{
			{"an interpreter payload, which is another language", `bash -c 'python3 -c "print(sorted(d, key=lambda x: x[1]))"'`},
			{"a long option inside a payload", `bash -c 'sort --key 2 f.txt'`},
			{"a search pattern inside a payload", `bash -c 'grep -r "sk-ant-oat01-abc" .'`},
			{"a header field that binds no credential", `bash -c 'curl -H "Content-Type: application/json" https://x'`},
			{"a shell that runs no inline program", `bash --help`},
			{"a vacant payload position", `bash -c`},
			{"a bare eval", `eval`},
		} {
			t.Run("leaves "+c[0], func(t *testing.T) { red(t, c[1], c[1]) })
		}
	})
	tw(t, f, "masks a payload nested inside another payload", func(t *testing.T) {
		warm(t)
		red(t, `bash -c 'bash -c "TOKEN=sk-inner x"'`, `bash -c 'bash -c "TOKEN=`+M+` x"'`)
	})
	tw(t, f, "masks the outer assignment and the payload's own, as two spans", func(t *testing.T) {
		warm(t)
		red(t, `TOKEN=sk-outer bash -c 'API_KEY=sk-inner x'`, "TOKEN="+M+` bash -c 'API_KEY=`+M+` x'`)
	})
	tw(t, f, "masks every payload of a chained command", func(t *testing.T) {
		warm(t)
		red(t, `bash -c 'echo hi' && bash -c 'TOKEN=sk2 x'`, `bash -c 'echo hi' && bash -c 'TOKEN=`+M+` x'`)
	})
	tw(t, f, "keeps a masked header inside a payload quote-balanced", func(t *testing.T) {
		warm(t)
		red(t, `bash -c 'curl -H "Authorization: Bearer sk-z" https://x'`, `bash -c 'curl -H "Authorization:`+M+`" https://x'`)
	})
	tw(t, f, "masks an ANSI-C quoted payload, whose slice skips the dollar too", func(t *testing.T) { warm(t); red(t, `bash -c $'TOKEN=sk-x deploy'`, `bash -c $'TOKEN=`+M+` deploy'`) })
	tw(t, f, "masks the whole argument when a segment boundary splits the value", func(t *testing.T) { warm(t); red(t, `bash -c 'TOKEN=sk-a'"bc"`, "bash -c "+M) })
	tw(t, f, "masks the whole argument when a variable is interpolated into it", func(t *testing.T) { warm(t); red(t, `bash -c 'TOKEN='"$SECRET"`, "bash -c "+M) })
	tw(t, f, "leaves a segmented payload that binds no credential", func(t *testing.T) { warm(t); red(t, `bash -c 'echo '"$GREETING"`, `bash -c 'echo '"$GREETING"`) })
	tw(t, f, "leaves a heredoc body, which is data rather than shell", func(t *testing.T) {
		warm(t)
		red(t, "cat > .env <<'EOF'\nAPI_KEY=sk-secret\nEOF", "cat > .env <<'EOF'\nAPI_KEY=sk-secret\nEOF")
	})
	tw(t, f, "returns an ordinary command unchanged", func(t *testing.T) { warm(t); red(t, "git status --short", "git status --short") })
	tw(t, f, "returns an empty command unchanged", func(t *testing.T) { warm(t); red(t, "", "") })
	tw(t, f, "masks what a recovering parse still resolved", func(t *testing.T) { warm(t); red(t, "KEY=sk-abc && echo )", "KEY="+M+" && echo )") })
	tw(t, f, "returns the command unchanged rather than raising", func(t *testing.T) {
		bashparse.ResetWarmBashParser()
		red(t, `KEY="sk-abc123" curl https://x`, `KEY="sk-abc123" curl https://x`)
	})

	mask := func(t *testing.T, in string) string {
		warm(t)
		return lgJSON(MaskCommandFields(jo(t, in)))
	}
	tw(t, f, "masks the command-bearing keys and leaves every other value alone", func(t *testing.T) {
		got := mask(t, `{"toolName":"bash","command":"KEY=\"sk-abc\" curl https://x","executedUnit":"TOKEN=sk-def deploy","matchedPattern":"curl *"}`)
		eq(t, got, `{"toolName":"bash","command":"KEY=`+M+` curl https://x","executedUnit":"TOKEN=`+M+` deploy","matchedPattern":"curl *"}`, "masked")
	})
	tw(t, f, "reaches a command nested inside another record", func(t *testing.T) {
		eq(t, mask(t, `{"forwarding":{"requests":[{"command":"KEY=sk-abc deploy"}]}}`), `{"forwarding":{"requests":[{"command":"KEY=`+M+` deploy"}]}}`, "nested")
	})
	tw(t, f, "leaves a non-string command value as it found it", func(t *testing.T) {
		eq(t, mask(t, `{"command":null}`), `{"command":null}`, "null")
	})
	t.Run("non-ASCII text before a secret keeps the splice on the right bytes", func(t *testing.T) {
		warm(t)
		red(t, "echo \u00e9\U0001F600 && KEY=sk-x deploy", "echo \u00e9\U0001F600 && KEY="+M+" deploy")
		red(t, "bash -c 'echo \u00e9 && TOKEN=sk-x d'", "bash -c 'echo \u00e9 && TOKEN="+M+" d'")
	})
}
