package pi_permission_system

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Twins of test/handlers/native-tool-target-acceptance.test.ts: a path rule applies to the file a built-in Pi tool opens, end to end
// with real files and the real PermissionManager and PermissionResolver.

func gfFileURL(p string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(p)}).String()
}

func TestNativeToolTargetAcceptanceTwins(t *testing.T) {
	const f = "handlers/native-tool-target-acceptance"
	realTemp := func(t *testing.T, prefix string) string {
		t.Helper()
		d, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	setup := func(t *testing.T) (cwd string, normalizer *PathNormalizer) {
		t.Helper()
		mockRealpath(t, func(p string) (string, error) { return filepath.EvalSymlinks(p) })
		cwd = realTemp(t, "native-target-acc-")
		return cwd, NewPathNormalizer(PathFlavorForPlatform(runtime.GOOS), cwd)
	}
	onDisk := func(t *testing.T, cwd, name string) string {
		t.Helper()
		file := filepath.Join(cwd, name)
		if err := os.WriteFile(file, []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
		return file
	}
	resolverFor := func(t *testing.T, cwd, permissionJSON string) *PermissionResolver {
		t.Helper()
		m := managerWithConfig(t, permissionJSON)
		m.ConfigureForCwd(cwd)
		return NewPermissionResolver(m, &SessionRules{})
	}
	jstr := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	readCall := func(cwd, path string) ToolCallContext {
		return ToolCallContext{ToolName: "read", Input: obj("path", path), ToolCallID: "tc-1", Cwd: cwd}
	}
	asDescriptor := func(t *testing.T, g GateResult) *GateDescriptor {
		t.Helper()
		d, ok := g.(*GateDescriptor)
		if !ok {
			t.Fatalf("got %T, want a descriptor", g)
		}
		return d
	}

	// Each spelling a built-in `read` resolves to a different file on disk than it names.
	type spelling struct {
		label, name string
		spell       func(file string) string
	}
	spellings := []spelling{
		{"a file URL", "secret.txt", gfFileURL},
		{"a percent-encoded file URL", "secret.txt", func(file string) string { return strings.Replace(gfFileURL(file), "secret", "s%65cret", 1) }},
		{"a no-break space for a space", "my file.txt", func(file string) string { return strings.Replace(file, " ", "\u00A0", 1) }},
		{"a straight quote for a curly one", "d\u2019x.txt", func(file string) string { return strings.Replace(file, "\u2019", "'", 1) }},
		{"a space for the AM/PM narrow no-break space", "Shot 1.02.03\u202FPM.png", func(file string) string { return strings.Replace(file, "\u202F", " ", 1) }},
	}

	t.Run("the path gate", func(t *testing.T) {
		for _, s := range spellings {
			each(t, "denies "+s.label+" of a denied file", func(t *testing.T) {
				cwd, n := setup(t)
				file := onDisk(t, cwd, s.name)
				r := resolverFor(t, cwd, `{"*":"allow","path":{"*":"allow",`+jstr(file)+`:"deny"}}`)
				d := asDescriptor(t, DescribePathGate(readCall(cwd, s.spell(file)), r, n, nil))
				eq(t, d.PreCheck.State, "deny", "state")
			})
			each(t, "lets "+s.label+" through when the allow names the file it opens", func(t *testing.T) {
				cwd, n := setup(t)
				file := onDisk(t, cwd, s.name)
				r := resolverFor(t, cwd, `{"*":"allow","path":{"*":"deny",`+jstr(file)+`:"allow"}}`)
				eq(t, DescribePathGate(readCall(cwd, s.spell(file)), r, n, nil) == nil, true, "null")
			})
		}
		tw(t, f, "judges a `..` escape as the file it opens, not its as-typed parent", func(t *testing.T) {
			cwd, n := setup(t)
			file := onDisk(t, cwd, "secret.txt")
			r := resolverFor(t, cwd, `{"*":"allow","path":{"*":"allow",`+jstr(file)+`:"deny",`+jstr(cwd+"/public/*")+`:"allow"}}`)
			dotDot := cwd + "/public/../secret.txt"
			d := asDescriptor(t, DescribePathGate(readCall(cwd, dotDot), r, n, nil))
			eq(t, d.PreCheck.State, "deny", "state")
			for _, v := range n.ForToolPath("read", dotDot).MatchValues() {
				if v == dotDot {
					t.Errorf("matchValues contain the as-typed spelling %q", dotDot)
				}
			}
		})
		tw(t, f, "discloses the file a fallback opens in the ask", func(t *testing.T) {
			cwd, n := setup(t)
			file := onDisk(t, cwd, "d\u2019x.txt")
			r := resolverFor(t, cwd, `{"*":"allow","path":{"*":"ask"}}`)
			d := asDescriptor(t, DescribePathGate(readCall(cwd, strings.Replace(file, "\u2019", "'", 1)), r, n, nil))
			eq(t, d.Payload.Evidence, []PromptEvidence{{Label: "resolves to", Text: file}}, "evidence")
		})
		tw(t, f, "approves the directory of the file a fallback opens for the session", func(t *testing.T) {
			cwd, n := setup(t)
			curlyDir := filepath.Join(cwd, "q\u2019s")
			if err := os.Mkdir(curlyDir, 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(curlyDir, "a.txt"), []byte("x"), 0o666); err != nil {
				t.Fatal(err)
			}
			r := resolverFor(t, cwd, `{"*":"allow","path":{"*":"ask"}}`)
			d := asDescriptor(t, DescribePathGate(readCall(cwd, filepath.Join(cwd, "q's", "a.txt")), r, n, nil))
			eq(t, d.SessionApproval.Grants, []ApprovalGrant{{Surface: "path_read", Pattern: curlyDir + "/*"}}, "grants")
		})
	})

	t.Run("the external_directory gate", func(t *testing.T) {
		outsideFile := func(t *testing.T, name string) string {
			t.Helper()
			dir := realTemp(t, "native-outside-")
			file := filepath.Join(dir, name)
			if err := os.WriteFile(file, []byte("x"), 0o666); err != nil {
				t.Fatal(err)
			}
			return file
		}
		scope := InfrastructureReadScope{Dirs: []string{}, ExcludedDirs: []string{}}
		tw(t, f, "asks for an outside file spelled as a file URL", func(t *testing.T) {
			cwd, n := setup(t)
			file := outsideFile(t, "secret.txt")
			r := resolverFor(t, cwd, `{"*":"allow","external_directory":{"*":"ask"}}`)
			d := asDescriptor(t, DescribeExternalDirectoryGate(readCall(cwd, gfFileURL(file)), scope, r, n, nil))
			eq(t, d.PreCheck.State, "ask", "state")
		})
		tw(t, f, "discloses the outside file a file URL names in the ask", func(t *testing.T) {
			cwd, n := setup(t)
			file := outsideFile(t, "secret.txt")
			r := resolverFor(t, cwd, `{"*":"allow","external_directory":{"*":"ask"}}`)
			d := asDescriptor(t, DescribeExternalDirectoryGate(readCall(cwd, gfFileURL(file)), scope, r, n, nil))
			eq(t, d.Payload.Evidence, []PromptEvidence{{Label: "resolves to", Text: file}, {Label: "working directory", Text: cwd}}, "evidence")
		})
		tw(t, f, "allows a rewritten spelling of an outside file the allow names", func(t *testing.T) {
			cwd, n := setup(t)
			file := outsideFile(t, "my file.txt")
			r := resolverFor(t, cwd, `{"*":"allow","external_directory":{"*":"ask",`+jstr(file)+`:"allow"}}`)
			d := asDescriptor(t, DescribeExternalDirectoryGate(readCall(cwd, strings.Replace(file, " ", "\u00A0", 1)), scope, r, n, nil))
			eq(t, d.PreCheck.State, "allow", "state")
		})
	})

	t.Run("the per-tool gate", func(t *testing.T) {
		for _, s := range spellings {
			each(t, "blocks "+s.label+" of a file the read rule denies", func(t *testing.T) {
				cwd, n := setup(t)
				file := onDisk(t, cwd, s.name)
				r := resolverFor(t, cwd, `{"*":"allow","read":{"*":"allow",`+jstr(file)+`:"deny"}}`)
				in := makeGateInputs(t)
				in.normalizer = func() *PathNormalizer { return n }
				mockRealpath(t, func(p string) (string, error) { return filepath.EvalSymlinks(p) })
				runner, _ := makeGateRunner()
				out, err := NewToolCallGatePipeline(r, in, nil, nil).Evaluate(readCall(cwd, s.spell(file)), runner)
				if err != nil {
					t.Fatal(err)
				}
				eq(t, out.Action, "block", "action")
			})
		}
	})
}
