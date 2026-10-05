// The build plan, as GitHub Actions outputs. On a release tag `<name>/v<release.version>` it selects exactly that Piglet
// (`name`) and its build.targets, each with its native runner (`matrix`); anything else about the tag is refused here,
// before a runner is started. Without a tag it plans every Piglet, which the validation workflow uses to check the
// target metadata. Runs with no secrets: the release workflow's `plan` job and `ci.yml` both call it.
import { appendFileSync } from 'node:fs';
import { readManifests } from './generate-index.mjs';
import { runners } from './runners.mjs';
const releaseTag = process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : '';
const manifests = readManifests();
const selected = releaseTag ? manifests.filter(({ manifest }) => releaseTag === `${manifest.name}/v${manifest.release.version}`) : manifests;
if (releaseTag && selected.length !== 1) throw new Error(`Tag ${releaseTag} matches ${selected.length} Piglets (need exactly one <name>/v<release.version>)`);
if (!selected.length) throw new Error('No Piglet manifests');
const include = selected.flatMap(({ manifest }) => (manifest.build?.targets || []).map(target => {
  if (!runners[target]) throw new Error('Add a native runner for target ' + target);
  const [goos, goarch] = target.split('/');
  return { name: manifest.name, target, goos, goarch, runner: runners[target] };
}));
if (!include.length) throw new Error('Every Piglet needs explicit build.targets');
const lines = [...(releaseTag ? [`name=${selected[0].manifest.name}`] : []), `matrix=${JSON.stringify({ include })}`];
if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, lines.join('\n') + '\n');
else console.log(lines.join('\n'));
