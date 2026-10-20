package pi_permission_system

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// Port of src/path/node-modules-discovery.ts.

// spawnNpmRootGlobal runs `npm root -g` (5 s timeout, stdin and stderr ignored) and returns its exit status and stdout; ok false
// when it could not run at all. Replaceable in tests (the original mocks spawnSync).
var spawnNpmRootGlobal = func() (status int, stdout string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "npm", "root", "-g")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if ee, isExit := err.(*exec.ExitError); isExit {
			return ee.ExitCode(), out.String(), true
		}
		return 0, "", false
	}
	return 0, out.String(), true
}

// walkUpToNodeModules walks up from the file URL until a directory literally named node_modules is found.
func walkUpToNodeModules(fromURL string) (string, bool) {
	file, err := fileURLToPath(fromURL, HostPath == Win32Path)
	if err != nil {
		return "", false
	}
	dir := HostPath.Dirname(file)
	for dir != HostPath.Dirname(dir) {
		if HostPath.Basename(dir) == "node_modules" {
			return dir, true
		}
		dir = HostPath.Dirname(dir)
	}
	return "", false
}

// DiscoverGlobalNodeModulesRoot discovers the global node_modules root: first by walking up from fromURL (a file URL; the
// extension's own module in the original) to a node_modules directory, else by `npm root -g`. ok false is the original's null.
func DiscoverGlobalNodeModulesRoot(fromURL string) (string, bool) {
	if root, ok := walkUpToNodeModules(fromURL); ok {
		return root, true
	}
	status, stdout, ok := spawnNpmRootGlobal()
	if !ok {
		return "", false
	}
	root := jsTrim(stdout)
	if status == 0 && root != "" && existsSync(root) {
		return root, true
	}
	return "", false
}
