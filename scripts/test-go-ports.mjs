// go vet and go test for every Go extension module: `npm run test:go-ports -- -race -count=3`.
// Needs PIG_SDK_DIR or PIG_BIN (see go-modules.mjs) and a Go toolchain.
import { spawnSync } from 'node:child_process';
import { relative } from 'node:path';
import { goModules, goWorkEnv, root } from './go-modules.mjs';
import { requirePig } from './pig-bin.mjs';

const modules = goModules();
const env = goWorkEnv(modules);
// The ACP companion's end-to-end scenarios run a real pig; they skip without one.
// A PIG_BIN that is not the pig Pigpen targets would fail those scenarios for reasons that are not Pigpen's.
if (process.env.PIG_BIN && !env.PIG_ACP_E2E_PIG) env.PIG_ACP_E2E_PIG = requirePig();
const extra = process.argv.slice(2);
let failed = false;
for (const module of modules) {
  for (const args of [['vet', './...'], ['test', ...extra, './...']]) {
    console.log(`\n# ${relative(root, module)}: go ${args.join(' ')}`);
    const result = spawnSync('go', args, { cwd: module, env, stdio: 'inherit' });
    if (result.status !== 0) failed = true;
  }
}
process.exitCode = failed ? 1 : 0;
