// Compile every Go extension module for platforms CI has no runner for: `go build` and `go vet` (which also compiles the
// tests) with CGO_ENABLED=0, so a dependency that needs cgo or a file that is not portable fails here.
//
//   node scripts/cross-check.mjs [os/arch ...]      (default: android/arm64, Termux)
//
// This is a compile check, not a Piglet Binary: PiG's native builder builds only the machine it runs on, so a fused
// android Binary cannot be built from a Linux runner. Needs PIG_SDK_DIR or PIG_BIN (see go-modules.mjs) and Go.
import { spawnSync } from 'node:child_process';
import { devNull } from 'node:os';
import { relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { goModules, goWorkEnv, root } from './go-modules.mjs';

/**
 * The go commands run in each module. The build discards its output: with `./...` matching a single `main` package,
 * `go build` writes the executable into the module directory, which left a compiled binary in the source tree.
 */
export function goCommands() {
  return [['build', '-o', devNull, './...'], ['vet', './...']];
}

function main(targets) {
  if (!targets.length) targets.push('android/arm64');
  const bad = targets.filter((t) => !/^[a-z0-9]+\/[a-z0-9]+$/.test(t));
  if (bad.length) {
    console.error(`cross-check: not an os/arch target: ${bad.join(', ')}`);
    return 2;
  }
  const modules = goModules();
  const base = goWorkEnv(modules);
  let failed = false;
  for (const target of targets) {
    let targetFailed = false;
    const [GOOS, GOARCH] = target.split('/');
    const env = { ...base, GOOS, GOARCH, CGO_ENABLED: '0' };
    for (const module of modules) {
      for (const args of goCommands()) {
        const result = spawnSync('go', args, { cwd: module, env, encoding: 'utf8' });
        if (result.status !== 0) {
          failed = targetFailed = true;
          console.log(`FAIL ${target} ${relative(root, module)}: go ${args.join(' ')}\n${(result.stderr || result.stdout).trim()}`);
        }
      }
    }
    console.log(`${targetFailed ? 'FAIL' : 'ok  '} ${target}: go build and go vet over ${modules.length} modules, CGO_ENABLED=0`);
  }
  return failed ? 1 : 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) process.exitCode = main(process.argv.slice(2));
