// For release-package.yml: the pushed tag must be components/<name>/v<version> and the component's version must be that
// version: a Package's package.json, an Archon plugin's answered VERSION. Prints and (in Actions) outputs name, version and
// kind (package | archon-plugin), so the workflow runs each kind's own checks.
import { appendFileSync } from 'node:fs';
import { join } from 'node:path';
import { checkPluginTag, componentKind } from './archon-plugins.mjs';
import { checkPackageTag, parsePackageTag, root } from './packages.mjs';

const tag = process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : process.argv[2];
if (!tag) throw new Error('Not a tag: pass components/<name>/v<version>');
const parsed = parsePackageTag(tag);
if (!parsed) throw new Error(`${tag} is not a component tag (components/<name>/v<version>)`);
const kind = componentKind(join(root, 'components', parsed.name));
const { name, version } = kind === 'package' ? checkPackageTag(tag) : checkPluginTag(tag);
const lines = `name=${name}\nversion=${version}\nkind=${kind}\n`;
if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, lines);
else process.stdout.write(lines);
