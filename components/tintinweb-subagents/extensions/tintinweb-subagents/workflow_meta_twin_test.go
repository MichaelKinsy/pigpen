package tintinweb_subagents

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Twins of test/workflow-meta.test.ts.

const wfMetaHeader = `export const meta = { name: 'wf', description: 'does a thing' }`

func wfMustMeta(t *testing.T, source string) (workflowMeta, string) {
	t.Helper()
	meta, body, err := extractMeta(source)
	if err != nil {
		t.Fatalf("extractMeta: %v", err)
	}
	return meta, body
}

func wfMetaErr(t *testing.T, source string, pattern string) {
	t.Helper()
	_, _, err := extractMeta(source)
	if err == nil {
		t.Fatalf("expected an error matching %q", pattern)
	}
	var me *workflowMetaError
	if !errors.As(err, &me) {
		t.Fatalf("error is not a workflowMetaError: %T", err)
	}
	if !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error %q does not match %q", err.Error(), pattern)
	}
}

func TestWorkflowMeta(t *testing.T) {
	const f = "workflow-meta"
	// extractMeta — acceptance
	tw(t, f, "accepts a pure literal and returns the parsed meta", func(t *testing.T) {
		meta, _ := wfMustMeta(t, wfMetaHeader+"\nreturn 1")
		eq(t, meta, workflowMeta{Name: "wf", Description: "does a thing"})
	})
	tw(t, f, "accepts optional whenToUse and phases", func(t *testing.T) {
		meta, _ := wfMustMeta(t, `export const meta = {
      name: 'review',
      description: 'review the diff',
      whenToUse: 'when a PR lands',
      phases: [{ title: 'Scan' }, { title: 'Verify', detail: 'adversarial', model: 'haiku' }],
    }
    return 1`)
		eq(t, meta.WhenToUse, sp("when a PR lands"))
		eq(t, meta.Phases, []workflowPhaseMeta{{Title: "Scan"}, {Title: "Verify", Detail: sp("adversarial"), Model: sp("haiku")}})
	})
	tw(t, f, "tolerates whitespace and newlines inside the declaration", func(t *testing.T) {
		meta, _ := wfMustMeta(t, "export   const\n  meta\n=\n{ name: 'a', description: 'b' }\nreturn 1")
		eq(t, meta.Name, "a")
	})
	tw(t, f, "allows a declaration that is not on the first line", func(t *testing.T) {
		meta, _ := wfMustMeta(t, "// a leading comment\n\n"+wfMetaHeader+"\nreturn 1")
		eq(t, meta.Name, "wf")
	})
	// extractMeta — body rewriting
	tw(t, f, "strips the export keyword so the body compiles outside a module", func(t *testing.T) {
		_, body := wfMustMeta(t, wfMetaHeader+"\nreturn 1")
		if regexp.MustCompile(`\bexport\b`).MatchString(body) {
			t.Fatalf("body still has export: %q", body)
		}
		if !strings.Contains(body, "const meta =") {
			t.Fatalf("body lost the declaration: %q", body)
		}
	})
	tw(t, f, "preserves byte offsets so reported line numbers stay accurate", func(t *testing.T) {
		source := wfMetaHeader + "\nreturn 1"
		_, body := wfMustMeta(t, source)
		eq(t, len(body), len(source))
		eq(t, len(strings.Split(body, "\n")), len(strings.Split(source, "\n")))
	})
	// extractMeta — brace scanning
	tw(t, f, "survives braces inside single- and double-quoted strings", func(t *testing.T) {
		meta, _ := wfMustMeta(t, `export const meta = { name: 'a}b', description: "c{d}e" }`+"\nreturn 1")
		eq(t, meta.Name, "a}b")
		eq(t, meta.Description, "c{d}e")
	})
	tw(t, f, "survives braces inside line and block comments", func(t *testing.T) {
		meta, _ := wfMustMeta(t, `export const meta = {
      // a stray } here
      name: 'a',
      /* and a { block } one */
      description: 'b',
    }
    return 1`)
		eq(t, meta.Name, "a")
	})
	tw(t, f, "survives braces inside template literals", func(t *testing.T) {
		meta, _ := wfMustMeta(t, "export const meta = { name: `a}b`, description: `plain` }\nreturn 1")
		eq(t, meta.Name, "a}b")
	})
	tw(t, f, "rejects a self-contained template substitution", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: `a${ {x:1}.x }b`, description: 'b' }\nreturn 1", `template interpolation`)
	})
	tw(t, f, "resumes template scanning after a substitution closes", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: `${ x }and}then`, description: 'b' }\nreturn 1", `template interpolation`)
	})
	tw(t, f, "leaves `${` inside an ordinary string alone", func(t *testing.T) {
		dollarBrace := "$" + "{"
		meta, _ := wfMustMeta(t, "export const meta = { name: 'a', description: 'costs "+dollarBrace+"5} per run' }\nreturn 1")
		eq(t, meta.Description, "costs "+dollarBrace+"5} per run")
	})
	tw(t, f, "does not stop at a brace inside an escaped quote", func(t *testing.T) {
		meta, _ := wfMustMeta(t, `export const meta = { name: 'it\'s }', description: 'b' }`+"\nreturn 1")
		eq(t, meta.Name, "it's }")
	})
	tw(t, f, "keeps scanning past nested objects", func(t *testing.T) {
		meta, _ := wfMustMeta(t, `export const meta = {
      name: 'a', description: 'b',
      phases: [{ title: 'x' }, { title: 'y' }],
    }
    return 1`)
		eq(t, len(meta.Phases), 2)
	})
	tw(t, f, "reports an unbalanced opening brace", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: 'a'\nreturn 1", `never closed`)
	})
	// extractMeta — rejection
	tw(t, f, "rejects a script with no meta at all", func(t *testing.T) {
		wfMetaErr(t, "return 1", `must begin with`)
	})
	tw(t, f, "rejects a computed name — the pure-literal rule", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: computeName(), description: 'b' }\nreturn 1", `PURE LITERAL`)
	})
	tw(t, f, "rejects a name built from a variable", func(t *testing.T) {
		wfMetaErr(t, "const base = 'x'\nexport const meta = { name: base, description: 'b' }\nreturn 1", `PURE LITERAL`)
	})
	tw(t, f, "rejects a spread", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { ...defaults, name: 'a', description: 'b' }\nreturn 1", `PURE LITERAL`)
	})
	tw(t, f, "rejects a missing name", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { description: 'b' }\nreturn 1", "`meta.name` is required")
	})
	tw(t, f, "rejects a missing description", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: 'a' }\nreturn 1", "`meta.description` is required")
	})
	tw(t, f, "rejects an empty name", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: '  ', description: 'b' }\nreturn 1", "`meta.name` is required")
	})
	tw(t, f, "rejects a non-object meta", func(t *testing.T) {
		wfMetaErr(t, "export const meta = 'nope'\nreturn 1", `must be assigned an object literal`)
	})
	tw(t, f, "rejects phases that are not an array", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: 'a', description: 'b', phases: 'Scan' }\nreturn 1", "`meta.phases` must be an array")
	})
	tw(t, f, "rejects a phase with no title", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: 'a', description: 'b', phases: [{ detail: 'x' }] }\nreturn 1", `phases\[0\].title`)
	})
	tw(t, f, "rejects a non-string whenToUse", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: 'a', description: 'b', whenToUse: 3 }\nreturn 1", "`meta.whenToUse` must be a string")
	})
	// extractMeta — isolation
	tw(t, f, "cannot reach host globals from the meta fragment", func(t *testing.T) {
		wfMetaErr(t, "export const meta = { name: process.platform, description: 'b' }\nreturn 1", `PURE LITERAL`)
	})
	tw(t, f, "bounds evaluation so a non-terminating literal cannot hang the host", func(t *testing.T) {
		started := time.Now()
		wfMetaErr(t, "export const meta = { name: (() => { while (true); })(), description: 'b' }\nreturn 1", `did not finish evaluating`)
		if time.Since(started) >= 2*time.Second {
			t.Fatalf("took %v", time.Since(started))
		}
	})
	tw(t, f, "does not execute the script body", func(t *testing.T) {
		meta, _ := wfMustMeta(t, wfMetaHeader+"\nthrow new Error('body ran')")
		eq(t, meta.Name, "wf")
	})
}
