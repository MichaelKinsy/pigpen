package pi_subagents

import (
	"errors"
	"testing"
)

// Twins of binary-pi-spawn, pi-spawn and pi-spawn-host-root. The original finds the Pi it spawns as a child (a compiled Bun
// image, the running Pi's package root, the Node interpreter plus Pi's cli.js); the port spawns `pig`, a Go binary, so the
// counterpart is which pig a child is: PIG_SUBAGENT_PIG_BINARY, the pig running this extension (PIG_HARNESS_BINARY), a pig
// next to this program, then PATH. The cases about Node, npm package roots and Bun virtual entries have no pig counterpart.

func spawnDeps(goos string, env map[string]string) pigSpawnDeps {
	return pigSpawnDeps{
		GOOS:       goos,
		Getenv:     func(k string) string { return env[k] },
		Executable: func() (string, error) { return "", errors.New("no executable") },
		Exists:     func(string) bool { return false },
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
	}
}

func TestBinaryPiSpawnTwins(t *testing.T) {
	const f = "binary-pi-spawn"
	hosts := []struct{ goos, image string }{
		{"linux", "/opt/standalone/pig-native"},
		{"windows", `C:\Program Files\pig\pig-native.exe`},
	}
	tw(t, f, "uses the actual image even when renamed", func(t *testing.T) {
		for _, h := range hosts {
			deps := spawnDeps(h.goos, map[string]string{"PIG_HARNESS_BINARY": h.image})
			eq(t, getPigSpawnCommand([]string{"-p"}, deps).Command, h.image, h.goos+": the renamed host image is the child")
		}
	})
	tw(t, f, "honors a nonempty override, otherwise retaining the actual image", func(t *testing.T) {
		for _, h := range hosts {
			with := map[string]string{"PIG_HARNESS_BINARY": h.image, "PIG_SUBAGENT_PIG_BINARY": " /custom/pig "}
			eq(t, getPigSpawnCommand(nil, spawnDeps(h.goos, with)).Command, "/custom/pig", h.goos+": override, trimmed")
			blank := map[string]string{"PIG_HARNESS_BINARY": h.image, "PIG_SUBAGENT_PIG_BINARY": " "}
			eq(t, getPigSpawnCommand(nil, spawnDeps(h.goos, blank)).Command, h.image, h.goos+": blank override keeps the image")
		}
	})
	tw(t, f, "does not treat an ordinary Bun script or Node as a compiled host", func(t *testing.T) {
		// Without the host's own image in the environment (an ordinary process, not a pig-hosted extension) nothing is
		// assumed to be the image: the sibling and PATH rules apply, and plain `pig` is the last answer.
		for _, h := range hosts {
			deps := spawnDeps(h.goos, map[string]string{"PIG_HARNESS_BINARY": "  "})
			deps.Executable = func() (string, error) { return "/work/script", nil }
			eq(t, getPigSpawnCommand(nil, deps).Command, "pig", h.goos+": no image, no sibling, no PATH entry")
		}
	})
}

func TestPiSpawnTwins(t *testing.T) {
	const f = "pi-spawn"
	args := []string{"--mode", "json", "Task: check output"}
	tw(t, f, "honors explicit PI_SUBAGENT_PI_BINARY override on any platform", func(t *testing.T) {
		deps := spawnDeps("windows", map[string]string{"PIG_SUBAGENT_PIG_BINARY": "/nix/store/pig-wrapper/bin/nhost-code-agent"})
		deps.Exists = func(string) bool { return true }
		eq(t, getPigSpawnCommand(args, deps), pigSpawnCommand{Command: "/nix/store/pig-wrapper/bin/nhost-code-agent", Args: args}, "command")
	})
	tskip(t, f, "runs a Windows ${extension} PI_SUBAGENT_PI_BINARY override through Node", "an override that names a .js/.mjs/.cjs Pi CLI script is run as `node <script>` on Windows because Node cannot exec a script there; pig is a native binary, so an override is always the program itself (the POSIX case below covers that it is never wrapped)")
	tw(t, f, "keeps a JavaScript PI_SUBAGENT_PI_BINARY override direct on POSIX", func(t *testing.T) {
		deps := spawnDeps("darwin", map[string]string{"PIG_SUBAGENT_PIG_BINARY": "/opt/pig/bin/pig-cli.mjs"})
		eq(t, getPigSpawnCommand(args, deps), pigSpawnCommand{Command: "/opt/pig/bin/pig-cli.mjs", Args: args}, "command")
		deps.GOOS = "windows"
		deps.Getenv = func(string) string { return `C:\Program Files\pig\bin\pig-cli.js` }
		eq(t, getPigSpawnCommand(args, deps), pigSpawnCommand{Command: `C:\Program Files\pig\bin\pig-cli.js`, Args: args}, "also on Windows: the override is the program, never `node <script>`")
	})
	tw(t, f, "ignores a blank PI_SUBAGENT_PI_BINARY override", func(t *testing.T) {
		deps := spawnDeps("darwin", map[string]string{"PIG_SUBAGENT_PIG_BINARY": "   "})
		eq(t, getPigSpawnCommand(args, deps), pigSpawnCommand{Command: "pig", Args: args}, "plain pig")
	})
	tw(t, f, "uses the standalone Pi executable directly on ${platform}", func(t *testing.T) {
		for _, c := range []struct{ goos, exe string }{{"darwin", "/opt/pig/pig"}, {"linux", "/opt/pig/pig"}, {"windows", `C:\Program Files\Pig\pig.exe`}} {
			deps := spawnDeps(c.goos, map[string]string{"PIG_HARNESS_BINARY": c.exe})
			deps.Exists = func(string) bool { return true }
			in := []string{"--mode", "json", "-p", "Task: review diff"}
			eq(t, getPigSpawnCommand(in, deps), pigSpawnCommand{Command: c.exe, Args: in}, c.goos)
		}
	})
	tskip(t, f, "uses node + argv1 on ${platform} when argv1 belongs to the Pi package", "spawns `node <process.argv[1]>` when the running script sits in the @earendil-works/pi-coding-agent package; a Go extension has no script entry and the pig binary needs no interpreter")
	tskip(t, f, "uses node + package bin on POSIX when argv1 is not a verified Pi entry", "resolves Pi's cli.js through the package.json `bin` of the npm package and runs it with Node; pig ships as one native executable, no package bin exists")
	tskip(t, f, "fails closed until the Windows CLI script becomes available", "Windows cannot exec a Pi npm shim, so the original throws until cli.js exists; exec.LookPath resolves pig.exe directly and the last answer is plain `pig`")
	tskip(t, f, "resolves the Windows CLI from the forwarded package-root env for wrapper hosts", "reads PI_SUBAGENTS_PI_CODING_AGENT_PACKAGE_ROOT (the Pi npm package root a wrapper host forwards) to find cli.js; the pig counterpart is the forwarded PIG_HARNESS_BINARY, covered by the standalone-executable case")
	tskip(t, f, "skips an unverified forwarded package-root env before resolving a verified Pi package", "verifies that a forwarded root's package.json is named @earendil-works/pi-coding-agent before trusting it; a pig path has no package manifest to verify")
	tw(t, f, "falls back to plain pi command on POSIX when CLI script cannot be resolved", func(t *testing.T) {
		deps := spawnDeps("darwin", map[string]string{})
		deps.Executable = func() (string, error) { return "/missing/host", nil }
		eq(t, getPigSpawnCommand(args, deps), pigSpawnCommand{Command: "pig", Args: args}, "plain pig")
	})
	tskip(t, f, "ignores embedded host entry points and resolves the Pi package bin on every platform", "an embedding host (pi-web) runs inside the same Node process, so argv1 is the host's script and the Pi package must be found beside it; the pig child never inherits a host entry, only PIG_HARNESS_BINARY")
	tskip(t, f, "validates argv1 ownership against its canonical target", "follows a symlinked argv1 into the Pi npm package to decide whether the script is Pi's own; the sibling-pig rule takes the path next to the real executable (os.Executable) and needs no ownership check of a script")
	tskip(t, f, "resolves CLI script from package bin when argv1 is not runnable JS", "Windows-style path.resolve of the package `bin` field next to package.json (the Pi npm layout)")
	tskip(t, f, "walks from package main entry to resolve package bin", "import.meta.resolve of the Pi package main entry, then a walk up to its package.json (Node module resolution)")
	tskip(t, f, "resolves CLI script via piPackageRoot when argv1 is not runnable", "the piPackageRoot dependency passes the Pi npm package root to the resolver; pig has no package root")
	tskip(t, f, "supports package bin as string", "the Pi package.json `bin` field given as a string instead of an object; npm metadata only")
	t.Run("a sibling pig next to this program is found before PATH, then PATH", func(t *testing.T) {
		deps := spawnDeps("linux", nil)
		deps.Executable = func() (string, error) { return "/opt/host/ext", nil }
		deps.Exists = func(p string) bool { return p == "/opt/host/pig" }
		deps.LookPath = func(string) (string, error) { return "/usr/bin/pig", nil }
		eq(t, getPigSpawnCommand(nil, deps).Command, "/opt/host/pig", "sibling")
		deps.Exists = func(string) bool { return false }
		eq(t, getPigSpawnCommand(nil, deps).Command, "/usr/bin/pig", "PATH")
		deps = spawnDeps("windows", nil)
		deps.Executable = func() (string, error) { return `C:\host\ext.exe`, nil }
		deps.Exists = func(p string) bool { return p == `C:\host\pig.exe` }
		eq(t, getPigSpawnCommand(nil, deps).Command, `C:\host\pig.exe`, "windows sibling has the .exe suffix")
	})
}

func TestPiSpawnHostRootTwins(t *testing.T) {
	const f = "pi-spawn-host-root"
	tskip(t, f, "returns the root of the package containing the entry", "findPiPackageRootFromEntry walks up from a Node entry file to the package.json named @earendil-works/pi-coding-agent; the pig child is identified by an executable path, not a package root")
	tskip(t, f, "keeps walking past packages with a different name", "the same package.json name walk, past a nested node_modules package of another name")
	tskip(t, f, "returns undefined for a missing entry", "the package.json walk of a nonexistent entry file; a missing pig path is tested by the sibling and PATH rules (plain pig is the last answer)")
	tskip(t, f, "finds the package through a bin script whose realpath lives inside it", "resolves process.argv[1] (a prefix/bin symlink into the Pi package) with realpath to find the package root; no argv script exists for the extension process")
	tskip(t, f, "ignores unrelated packages higher up the tree", "a package.json of another name above the entry is ignored by the Pi package-root walk")
	tskip(t, f, "keeps argv ownership ahead of both explicit roots", "precedence of process.argv[1] ownership over PI_PACKAGE_DIR and PI_SUBAGENTS_PI_CODING_AGENT_PACKAGE_ROOT; the pig precedence (override, then host image, then sibling, then PATH) is asserted by the pi-spawn twins")
	tskip(t, f, "reports an unreadable argv-owned manifest instead of selecting an override", "diagnoses an unreadable Pi package.json found through argv1; pig has no manifest")
	tskip(t, f, "prefers PI_PACKAGE_DIR, ignores blanks, and fails closed on explicit invalid roots", "validates PI_PACKAGE_DIR and the forwarded root env as Pi package roots; the pig analog (a blank PIG_SUBAGENT_PIG_BINARY is ignored) is in the pi-spawn twins")
	tskip(t, f, "recognizes POSIX share and Windows adjacent compiled Bun layouts", "locates the package.json that a compiled Bun Pi image ships beside it (share/pi-coding-agent or the image directory); pig is a Go image with no sidecar package")
	tskip(t, f, "does not infer image layouts for ordinary Node processes named pi", "refuses the compiled-Bun layout inference unless process.versions.bun is set; no Bun or Node runtime exists in this process")
}
