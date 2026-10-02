// Build the pigeq command line (the equivalence harness) from a checkout:
//   PIG_BIN=/path/to/pig npm run build:pigeq [-- <output>]      (default dist/bin/pigeq)
// pigeq's module requires PiG's SDK at v0.0.0, which PiG substitutes at build time, so a plain
// `go build` cannot resolve it: build inside a temporary go.work like test:go-ports does.
import { spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { goModules, goWorkEnv, root } from './go-modules.mjs';

const output = resolve(process.argv[2] || join(root, 'dist/bin/pigeq'));
const modules = goModules().filter((dir) => dir.includes('extension-equivalence'));
const command = modules.find((dir) => dir.endsWith('cmd/pigeq'));
if (!command) throw new Error('cmd/pigeq module not found');
mkdirSync(dirname(output), { recursive: true });
const result = spawnSync('go', ['build', '-o', output, '.'], { cwd: command, env: goWorkEnv(modules), stdio: 'inherit' });
if (result.status === 0) console.log(`Built ${output}`);
process.exitCode = result.status ?? 1;
