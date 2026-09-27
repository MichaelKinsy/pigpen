import { appendFileSync } from 'node:fs';
import { readManifests } from './generate-index.mjs';
const runners = { 'linux/amd64': 'ubuntu-24.04', 'linux/arm64': 'ubuntu-24.04-arm', 'darwin/arm64': 'macos-15', 'windows/amd64': 'windows-2025' };
const releaseTag = process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : '';
const manifests = readManifests();
const selected = releaseTag ? manifests.filter(({ manifest }) => releaseTag === `${manifest.name}/v${manifest.release.version}`) : manifests;
if (!selected.length) throw new Error('Tag must match a manifest name and release.version');
if (releaseTag) throw new Error('Monorepo publication blocked: PiG lacks tag-prefix support. See RELEASE-BLOCKERS.md. No signing secrets or write token have been used.');
const include = selected.flatMap(({ manifest }) => (manifest.build?.targets || []).map(target => {
  if (!runners[target]) throw new Error('Add a native runner for target ' + target);
  const [goos, goarch] = target.split('/');
  return { name: manifest.name, target, goos, goarch, runner: runners[target] };
}));
if (!include.length) throw new Error('Every Piglet needs explicit build.targets');
const matrix = JSON.stringify({ include });
if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `matrix=${matrix}\n`);
else console.log(matrix);
