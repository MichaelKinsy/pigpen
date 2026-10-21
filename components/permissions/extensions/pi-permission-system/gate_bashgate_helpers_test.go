package pi_permission_system

import (
	"runtime"
	"testing"
)

// Shared helpers of the bash gate twins (test/handlers/gates/bash-*.test.ts): the parse-once derivation the handler performs, and the
// path-dispatch resolver of gate-fixtures.ts.

// bgNormalizer is `new PathNormalizer(pathFlavorForPlatform(platform), cwd)` over the real file system, as the originals build it.
func bgNormalizer(platform, cwd string) *PathNormalizer {
	return NewPathNormalizer(PathFlavorForPlatform(platform), cwd)
}

// bgProgram mirrors the handler's parse-once derivation: the command is parsed for the native bash tool when it is non-empty.
func bgProgram(t *testing.T, tcc ToolCallContext, normalizer *PathNormalizer) *BashProgram {
	t.Helper()
	input, _ := tcc.Input.(*jsObject)
	if tcc.ToolName != "bash" || input == nil {
		return nil
	}
	command, ok := input.str("command")
	if !ok || command == "" {
		return nil
	}
	program, err := ParseBashProgram(command, normalizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// describeBashPathOn is describeGateOnPlatform of bash-path.test.ts.
func describeBashPathOn(t *testing.T, platform string, tcc ToolCallContext, resolver ScopedPermissionResolver) GateResult {
	t.Helper()
	n := bgNormalizer(platform, tcc.Cwd)
	return DescribeBashPathGate(tcc, bgProgram(t, tcc, n), resolver, n)
}

// describeBashExtOn is describeGateOnPlatform of bash-external-directory.test.ts.
func describeBashExtOn(t *testing.T, platform string, tcc ToolCallContext, resolver ScopedPermissionResolver) GateResult {
	t.Helper()
	n := bgNormalizer(platform, tcc.Cwd)
	return DescribeBashExternalDirectoryGate(tcc, bgProgram(t, tcc, n), resolver, n)
}

// makePathDispatchResolver answers per path: a tool intent by its `path` input, an access-path intent by the first match value in the
// map, anything else by the default.
func makePathDispatchResolver(byPath map[string]PermissionCheckResult, def PermissionCheckResult) *gfResolver {
	r := makeResolver()
	r.fn = func(intent AccessIntent) PermissionCheckResult {
		switch it := intent.(type) {
		case ToolAccessIntent:
			if in, ok := it.Input.(*jsObject); ok {
				if p, ok := in.str("path"); ok {
					if c, ok := byPath[p]; ok {
						return c
					}
				}
			}
		case AccessPathAccessIntent:
			for _, v := range it.Path.MatchValues() {
				if c, ok := byPath[v]; ok {
					return c
				}
			}
		}
		return def
	}
	return r
}

// lastIntent is the last intent a resolver received.
func (r *gfResolver) lastIntent() AccessIntent { return r.calls[len(r.calls)-1] }

func isBypass(g GateResult) (*GateBypass, bool)         { b, ok := g.(*GateBypass); return b, ok }
func isDescriptor(g GateResult) (*GateDescriptor, bool) { d, ok := g.(*GateDescriptor); return d, ok }

// bgMockHome is `vi.mock("node:os")` with homedir() at home: the package hook and the environment the bash layer reads.
func bgMockHome(t *testing.T, home string) {
	t.Helper()
	mockHome(t, home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
}
