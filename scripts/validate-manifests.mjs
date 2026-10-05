// Real Piglet schema/resource validation belongs to the chosen PiG binary.
import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { readManifests, generateIndex, root } from './generate-index.mjs';
import { goModules, registersNativeProvider } from './go-modules.mjs';
import { requirePig } from './pig-bin.mjs';
import { loadsInPig } from './platform-smoke.mjs';
export function run(args) {
  const result = spawnSync(requirePig(), args, { stdio: 'inherit', shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error('PiG command failed: ' + args.slice(0, 2).join(' '));
}
// An extension that registers a native Provider cannot go through `install --validate-only` (that host binds no native
// provider registry). Load it in a real pig instead, with the test model.
function loadInPig(module) {
  const result = loadsInPig(requirePig(), module);
  if (!result.ok) throw new Error(`PiG could not load extension ${module}: ${result.detail}`);
  console.log('loaded extension: ' + module);
}
generateIndex();
for (const component of readdirSync(join(root, 'components'), { withFileTypes: true })) {
  if (component.isDirectory()) run(['package', 'validate', join(root, 'components', component.name)]);
}
for (const { path } of readManifests(join(root, 'dist/staged'))) run(['piglet', 'validate', path]);
// A Go extension that passes the workspace tests can still fail under pig (a stale go.mod, a missing require:
// pigpen-a2a). Load each one the way pig does: the extension directory, not the Package root.
for (const module of goModules()) {
  if (module.includes('/cmd/') || !module.includes('/extensions/')) continue;
  if (registersNativeProvider(module)) {
    loadInPig(module);
    continue;
  }
  run(['install', module, '--validate-only']);
}
