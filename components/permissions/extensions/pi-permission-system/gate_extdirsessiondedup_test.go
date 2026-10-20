package pi_permission_system

import (
	"os"
	"path/filepath"
	"testing"
)

// Twins of test/handlers/external-directory-session-dedup.test.ts: sequential calls to the same external path prompt once, through a
// real PermissionSession, PermissionResolver and SessionRules.

func makeDedupWiring(t *testing.T, prompter ...*gfEscalator) *gfHandlerRig {
	t.Helper()
	p := gfDecision(true, DecisionApprovedForSession, gfHuman)
	if len(prompter) > 0 {
		p = prompter[0]
	}
	registry := []any{}
	for _, n := range []string{"read", "write", "edit", "ls", "bash", "add_directory"} { // add_directory: an extension tool whose path argument names a directory
		registry = append(registry, obj("name", n))
	}
	return makeHandler(t, gfHandlerOpts{managerCheck: makeExtDirDedupCheck(), prompter: p, toolRegistryAll: registry, sharedRecorder: true})
}

func makeWideSessionApprovingPrompter() *gfEscalator {
	return gfDecision(true, DecisionApprovedForSession, gfHuman, func(p *PermissionPromptDecision) { p.SessionGrantWidth = "family" })
}

func TestExternalDirectorySessionDedupTwins(t *testing.T) {
	const f = "handlers/external-directory-session-dedup"
	call := func(t *testing.T, rig *gfHandlerRig, ctx SessionContext, event any) GateOutcome {
		t.Helper()
		return rig.toolCall(t, event, ctx)
	}
	escalations := func(rig *gfHandlerRig) int { return len(rig.prompter.calls) }
	toolEvent := func(tool, path, id string) *jsObject {
		e := extDirToolEvent(tool, path)
		e.set("toolCallId", id)
		return e
	}
	bashEvent := func(cmd, id string) *jsObject {
		e := extDirBashEvent(cmd)
		e.set("toolCallId", id)
		return e
	}

	t.Run("path-bearing tools (read, write, edit)", func(t *testing.T) {
		tw(t, f, "does not re-prompt for the same external path after session approval", func(t *testing.T) {
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			const p = "/outside/project/data.txt"
			// First call: should prompt
			eq(t, call(t, rig, ctx, toolEvent("read", p, "tc-1")), gfAllow, "first")
			eq(t, escalations(rig), 1, "escalations")
			// Second call: same path, should hit the session rule, no prompt
			eq(t, call(t, rig, ctx, toolEvent("read", p, "tc-2")), gfAllow, "second")
			eq(t, escalations(rig), 1, "escalations")
		})
		tw(t, f, "does not re-prompt for a different file in the same external directory", func(t *testing.T) {
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, toolEvent("read", "/outside/project/a.txt", "tc-1"))
			eq(t, escalations(rig), 1, "escalations")
			call(t, rig, ctx, toolEvent("read", "/outside/project/b.txt", "tc-2"))
			eq(t, escalations(rig), 1, "escalations")
		})
		tw(t, f, "does prompt for a file in a different external directory", func(t *testing.T) {
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, toolEvent("read", "/outside/alpha/file.txt", "tc-1"))
			eq(t, escalations(rig), 1, "escalations")
			call(t, rig, ctx, toolEvent("read", "/outside/beta/file.txt", "tc-2"))
			eq(t, escalations(rig), 2, "escalations")
		})
		tw(t, f, "re-prompts when user approved once (not for session)", func(t *testing.T) {
			rig := makeDedupWiring(t, makeApprovingPrompter())
			ctx, _ := makeCtx()
			const p = "/outside/project/data.txt"
			call(t, rig, ctx, toolEvent("read", p, "tc-1"))
			eq(t, escalations(rig), 1, "escalations")
			// no session rule recorded, should prompt again
			call(t, rig, ctx, toolEvent("read", p, "tc-2"))
			eq(t, escalations(rig), 2, "escalations")
		})
	})

	t.Run("bash commands with external paths", func(t *testing.T) {
		tw(t, f, "does not re-prompt for a bash command referencing the same external path after session approval", func(t *testing.T) {
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			eq(t, call(t, rig, ctx, bashEvent("cat /tmp/out.txt", "tc-1")), gfAllow, "first")
			eq(t, escalations(rig), 1, "escalations")
			// different bash command, same external path, same direction
			eq(t, call(t, rig, ctx, bashEvent("head -n 5 /tmp/out.txt", "tc-2")), gfAllow, "second")
			eq(t, escalations(rig), 1, "escalations")
		})
		tw(t, f, "does not re-prompt for read after bash already read the same directory", func(t *testing.T) {
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, bashEvent("cat /tmp/out.txt", "tc-1"))
			eq(t, escalations(rig), 1, "escalations")
			// same directory, different tool, same direction: `read`'s identity proves the same read the core word did
			call(t, rig, ctx, toolEvent("read", "/tmp/out.txt", "tc-2"))
			eq(t, escalations(rig), 1, "escalations")
		})

		t.Run("the read/write axis narrows a session grant (#807)", func(t *testing.T) {
			tw(t, f, "re-prompts for a read after only a write was approved", func(t *testing.T) {
				rig := makeDedupWiring(t)
				ctx, _ := makeCtx()
				// The redirect proves a write, so the grant is recorded on `external_directory_write`.
				call(t, rig, ctx, bashEvent("echo hello > /tmp/out.txt", "tc-1"))
				eq(t, escalations(rig), 1, "escalations")
				// A read is the other direction, and the two are independent bits rather than tiers (ADR 0013 sections 3-4).
				call(t, rig, ctx, bashEvent("cat /tmp/out.txt", "tc-2"))
				eq(t, escalations(rig), 2, "escalations")
			})
			tw(t, f, "re-prompts for the read tool after only a bash write was approved", func(t *testing.T) {
				rig := makeDedupWiring(t)
				ctx, _ := makeCtx()
				call(t, rig, ctx, bashEvent("echo hello > /tmp/out.txt", "tc-1"))
				eq(t, escalations(rig), 1, "escalations")
				call(t, rig, ctx, toolEvent("read", "/tmp/out.txt", "tc-2"))
				eq(t, escalations(rig), 2, "escalations")
			})
			tw(t, f, "covers a later write with an approved write", func(t *testing.T) {
				rig := makeDedupWiring(t)
				ctx, _ := makeCtx()
				call(t, rig, ctx, bashEvent("echo hello > /tmp/out.txt", "tc-1"))
				eq(t, escalations(rig), 1, "escalations")
				call(t, rig, ctx, bashEvent("echo bye >> /tmp/out.txt", "tc-2"))
				eq(t, escalations(rig), 1, "escalations")
			})
		})

		t.Run("a both-directions session grant covers the other bit (#813)", func(t *testing.T) {
			tw(t, f, "covers a later read with a write approved at the family width", func(t *testing.T) {
				rig := makeDedupWiring(t, makeWideSessionApprovingPrompter())
				ctx, _ := makeCtx()
				// The redirect still proves only a write, but the human widened the grant to the bare family.
				call(t, rig, ctx, bashEvent("echo hello > /tmp/out.txt", "tc-1"))
				eq(t, escalations(rig), 1, "escalations")
				call(t, rig, ctx, bashEvent("cat /tmp/out.txt", "tc-2"))
				eq(t, escalations(rig), 1, "escalations")
			})
			tw(t, f, "covers the read tool with a bash write approved at the family width", func(t *testing.T) {
				rig := makeDedupWiring(t, makeWideSessionApprovingPrompter())
				ctx, _ := makeCtx()
				call(t, rig, ctx, bashEvent("echo hello > /tmp/out.txt", "tc-1"))
				eq(t, escalations(rig), 1, "escalations")
				call(t, rig, ctx, toolEvent("read", "/tmp/out.txt", "tc-2"))
				eq(t, escalations(rig), 1, "escalations")
			})
			tw(t, f, "covers a later write with a read approved at the family width", func(t *testing.T) {
				rig := makeDedupWiring(t, makeWideSessionApprovingPrompter())
				ctx, _ := makeCtx()
				call(t, rig, ctx, bashEvent("cat /tmp/out.txt", "tc-1"))
				eq(t, escalations(rig), 1, "escalations")
				call(t, rig, ctx, bashEvent("echo bye >> /tmp/out.txt", "tc-2"))
				eq(t, escalations(rig), 1, "escalations")
			})
		})
	})

	tw(t, f, "re-prompts for the same path after session shutdown", func(t *testing.T) {
		rig := makeDedupWiring(t)
		ctx, _ := makeCtx()
		const p = "/tmp/sibling/foo.ts"
		// First access: prompt fires and records session approval.
		call(t, rig, ctx, toolEvent("read", p, "tc-1"))
		eq(t, escalations(rig), 1, "escalations")
		// Second access: covered by session approval, no re-prompt.
		call(t, rig, ctx, toolEvent("read", p, "tc-2"))
		eq(t, escalations(rig), 1, "escalations")
		// Shutdown clears session approvals.
		rig.session.Shutdown()
		// Third access: session rules cleared, must re-prompt.
		call(t, rig, ctx, toolEvent("read", p, "tc-3"))
		eq(t, escalations(rig), 2, "escalations")
	})

	t.Run("session approval of an external directory", func(t *testing.T) {
		// Real filesystem: the grant's scope depends on whether the path names a directory, which is fs state.
		fixture := func(t *testing.T) (projectA, projectB string) {
			t.Helper()
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			projectA, projectB = filepath.Join(root, "projects", "project-a"), filepath.Join(root, "projects", "project-b")
			for p, file := range map[string]string{projectA: "x.txt", projectB: "sample.txt"} {
				if err := os.MkdirAll(p, 0o777); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p, file), []byte("x"), 0o666); err != nil {
					t.Fatal(err)
				}
			}
			mockRealpath(t, func(p string) (string, error) { return filepath.EvalSymlinks(p) })
			return
		}
		tw(t, f, "does not cover a sibling directory after approving ls of one", func(t *testing.T) {
			a, b := fixture(t)
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, toolEvent("ls", a, "tc-1"))
			call(t, rig, ctx, toolEvent("read", b+"/sample.txt", "tc-2"))
			eq(t, escalations(rig), 2, "escalations")
		})
		tw(t, f, "does not cover a sibling directory after approving an extension tool on one", func(t *testing.T) {
			a, b := fixture(t)
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, toolEvent("add_directory", a, "tc-1"))
			call(t, rig, ctx, toolEvent("read", b+"/sample.txt", "tc-2"))
			eq(t, escalations(rig), 2, "escalations")
		})
		tw(t, f, "covers the approved directory itself and its contents", func(t *testing.T) {
			a, _ := fixture(t)
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, toolEvent("ls", a, "tc-1"))
			call(t, rig, ctx, toolEvent("read", a+"/x.txt", "tc-2"))
			call(t, rig, ctx, toolEvent("ls", a, "tc-3"))
			eq(t, escalations(rig), 1, "escalations")
		})
		tw(t, f, "offers the directory approval as its contents glob", func(t *testing.T) {
			a, _ := fixture(t)
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, toolEvent("ls", a, "tc-1"))
			var grants []ApprovalGrant
			if s := rig.prompter.calls[0].SessionApproval; s != nil {
				grants = s.Grants
			}
			eq(t, DescribeGrantTarget(grants), `"`+a+`/*"`, "grant target")
		})
		tw(t, f, "does not cover a sibling directory after approving a bash ls of one", func(t *testing.T) {
			a, b := fixture(t)
			rig := makeDedupWiring(t)
			ctx, _ := makeCtx()
			call(t, rig, ctx, bashEvent("ls "+a, "tc-1"))
			call(t, rig, ctx, bashEvent("cat "+b+"/sample.txt", "tc-2"))
			eq(t, escalations(rig), 2, "escalations")
		})
	})
}
