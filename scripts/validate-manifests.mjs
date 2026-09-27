// Real Piglet schema/resource validation belongs to the chosen PiG binary.
import { spawnSync } from 'node:child_process';
import { readManifests, generateIndex } from './generate-index.mjs';
export function run(args) {
  const result = spawnSync(process.env.PIG_BIN || 'pig', args, { stdio: 'inherit', shell: false });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error('PiG command failed: ' + args.slice(0, 2).join(' '));
}
generateIndex();
for (const { path } of readManifests()) run(['piglet', 'validate', path]);
