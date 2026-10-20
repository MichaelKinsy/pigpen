//go:build unix

package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twins of the spawn-boundary cases that have a Go counterpart, through the fake host and a fake pig.

func TestAsyncSpawnTwins(t *testing.T) {
	const f = "async-spawn-package-dir"
	tskip(t, f, "detached spawn does not keep an inherited bundled-layout PI_PACKAGE_DIR", "Node and Pi loader internals: the original points a detached Node runner at Pi's package root through PI_PACKAGE_DIR; the Go port starts a pig binary and has no Pi package root to resolve")
	tskip(t, f, "detached launch honors the package-root environment override when host detection cannot identify the package", "Node and Pi loader internals: PI_SUBAGENTS_PI_CODING_AGENT_PACKAGE_ROOT overrides how the Node runner finds Pi's npm package; a pig child has no npm package")
	tskip(t, f, "detached launch ignores a whitespace-only package-root override", "Node and Pi loader internals: the whitespace-only override guard belongs to the Pi npm package root lookup, which the Go port does not have")
	tskip(t, f, "npm detached launch fails closed when the detected package root is absent", "Node and Pi loader internals: the npm-installed Pi package manifest check that makes the original fail closed does not exist for a pig child (its binary is resolved by PIG_SUBAGENT_PIG_BINARY, PIG_HARNESS_BINARY, a sibling or PATH, tested in run_safety_test.go)")
	tw(t, f, "detached spawn drops inherited Git repository routing variables and keeps other environment", func(t *testing.T) {
		e := newAsyncEnv(t)
		envFile := filepath.Join(tmp(t), "env.txt")
		wrapper := filepath.Join(tmp(t), "env-pig")
		fake := os.Getenv("PIG_SUBAGENT_PIG_BINARY")
		writeFile(t, wrapper, "#!/bin/sh\nenv > \""+envFile+"\"\nexec "+fake+" \"$@\"\n")
		if err := os.Chmod(wrapper, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIG_SUBAGENT_PIG_BINARY", wrapper)
		inherited := map[string]string{
			"GIT_DIR": "/outer/repo/.git", "GIT_WORK_TREE": "/outer/repo", "GIT_INDEX_FILE": "/outer/repo/.git/index", "GIT_COMMON_DIR": "/outer/repo/.git",
			"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.hooksPath", "GIT_CONFIG_VALUE_0": "/outer/hooks", "GIT_CONFIG_PARAMETERS": "'core.bare'='true'",
			"GIT_AUTHOR_NAME": "Kept Author", "GIT_ENV_SENTINEL_KEEP": "kept",
		}
		for k, v := range inherited {
			t.Setenv(k, v)
		}
		h := e.host(t)
		launch(t, h, map[string]any{})
		eventually(t, "the child's environment", func() bool { b, _ := os.ReadFile(envFile); return strings.Contains(string(b), "GIT_ENV_SENTINEL_KEEP") })
		b, _ := os.ReadFile(envFile)
		env := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				env[k] = v
			}
		}
		for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS"} {
			if _, ok := env[key]; ok {
				t.Errorf("%s reached the child", key)
			}
		}
		eq(t, env["GIT_AUTHOR_NAME"], "Kept Author", "GIT_AUTHOR_NAME")
		eq(t, env["GIT_ENV_SENTINEL_KEEP"], "kept", "GIT_ENV_SENTINEL_KEEP")
		eq(t, env["PIG_SUBAGENT"], "1", "launch-owned variables are still set")
	})
}

func TestAsyncSpawnPreloadAndBinaryTwins(t *testing.T) {
	tskip(t, "async-spawn-preload", "executeAsyncSingle preloads all peer aliases before the selected runner loader when aliases exist", "Node and TypeScript loader internals: the case checks the JITI_ALIAS peer-package map, --import preload and --experimental-strip-types arguments of the Node runner; a pig child loads nothing through Node")
	tskip(t, "binary-async-spawn", "compiled background launch ${entry} ${missingBootstrap ? \"rejects a missing bootstrap\" : \"uses Pi's loader without npm aliases\"}", "Bun compiled-binary internals: the case fakes a Bun host entry (/$bunfs, B:/~BUN) and the binary-bootstrap.ts extension a compiled Pi loads; PiG's pig child is started with its own binary and none of that exists")
}
