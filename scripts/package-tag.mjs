// For release-package.yml: the pushed tag must be components/<name>/v<version> and the component's manifest must have that
// version. Prints and (in Actions) outputs name and version.
import { appendFileSync } from 'node:fs';
import { checkPackageTag } from './packages.mjs';

const tag = process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : process.argv[2];
if (!tag) throw new Error('Not a tag: pass components/<name>/v<version>');
const { name, version } = checkPackageTag(tag);
const lines = `name=${name}\nversion=${version}\n`;
if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, lines);
else process.stdout.write(lines);
