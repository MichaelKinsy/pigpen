// Third-party listings: PiG Packages and Piglets that Pigpen does not own, listed in index.json from listings/<id>.json.
// Nothing here reads the network. A listing is a pin (a full commit of a public GitHub repository), the license and
// the maintainer the owner of that repository claims, and the PiG it was verified with. This module validates the
// files and turns each into an index entry with official: false and the real maintainer. What the pin actually
// contains is proved by `npm run verify:listings` (scripts/verify-listings.mjs), which CI runs on every run.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import Ajv from 'ajv';
import addFormats from 'ajv-formats';

export const root = fileURLToPath(new URL('../', import.meta.url));
export const LISTING_SCHEMA = JSON.parse(readFileSync(join(root, 'listing.schema.json'), 'utf8'));

const ajv = new Ajv({ allErrors: true, strictRequired: false });
addFormats(ajv);
const validate = ajv.compile(LISTING_SCHEMA);

const FILE = /^([a-z0-9][a-z0-9._-]*)\.json$/;
const NOT_A_LICENSE = new Set(['NONE', 'NOASSERTION', 'UNLICENSED', 'UNKNOWN']);

/**
 * Whether `text` is a well-formed SPDX license expression made of license ids ("MIT", "Apache-2.0 OR MIT",
 * "GPL-2.0-or-later WITH Classpath-exception-2.0", "(MIT AND BSD-3-Clause)"). It checks the grammar, not the SPDX list:
 * the list grows, and the pinned license file is what the reviewer and scripts/verify-listings.mjs read.
 * NONE, NOASSERTION, UNLICENSED and LicenseRef- are not licenses a listing can claim.
 */
export function validSpdx(text) {
  if (typeof text !== 'string' || text !== text.trim()) return false;
  const tokens = text.match(/\(|\)|[^\s()]+/g) ?? [];
  let at = 0;
  const id = (token) => /^[A-Za-z0-9][A-Za-z0-9.-]*\+?$/.test(token ?? '') && !NOT_A_LICENSE.has(token.replace(/\+$/, '')) && !/^(?:Document|License)Ref-/.test(token) && !['AND', 'OR', 'WITH'].includes(token);
  const term = () => {
    if (tokens[at] === '(') {
      at += 1;
      if (!expression() || tokens[at] !== ')') return false;
      at += 1;
      return true;
    }
    if (!id(tokens[at])) return false;
    at += 1;
    if (tokens[at] === 'WITH') {
      at += 1;
      if (!id(tokens[at]) || tokens[at].endsWith('+')) return false;
      at += 1;
    }
    return true;
  };
  const expression = () => {
    if (!term()) return false;
    while (tokens[at] === 'AND' || tokens[at] === 'OR') {
      at += 1;
      if (!term()) return false;
    }
    return true;
  };
  return expression() && at === tokens.length;
}

/** The ajv errors of one listing as readable lines; the common mistakes get a sentence that says what to do. */
function schemaProblems(errors) {
  return errors.map((error) => {
    const field = error.instancePath.replace(/^\//, '').replaceAll('/', '.');
    const missing = error.params?.missingProperty;
    if (error.keyword === 'required') {
      if (missing === 'commit') return 'commit is missing: pin the listing to a full 40-character commit hash (a tag or branch is not a pin)';
      if (missing === 'license') return 'license is missing: name the SPDX license the repository grants at the pinned commit';
      return `${missing} is missing`;
    }
    if (error.keyword === 'additionalProperties') {
      const extra = error.params.additionalProperty;
      if (['ref', 'branch', 'rev', 'revision', 'sha'].includes(extra)) return `${extra} is not a field: pin with "commit": "<full 40-character hash>"`;
      return `${field ? field + '.' : ''}${extra} is not a field of a listing`;
    }
    if (field === 'commit' && error.keyword === 'pattern') return 'commit must be a full 40-character lowercase commit hash: a branch, HEAD, a tag or an abbreviated hash is not a pin';
    if (field === 'repository' && error.keyword === 'pattern') return 'repository must be https://github.com/<owner>/<repo> (no .git, no trailing slash)';
    return `${field || 'listing'} ${error.message}`;
  });
}

/** The *.json files of a listings directory (default: this repository's): [{file, id, listing, problems}]; a file that is not valid JSON has problems and no listing. */
export function readListings(dir = join(root, 'listings')) {
  if (!existsSync(dir)) return [];
  return readdirSync(dir).sort().filter((file) => file !== 'README.md').map((file) => {
    const found = FILE.exec(file);
    if (!found) return { file, id: undefined, listing: undefined, problems: ['listings holds only <id>.json files (lowercase letters, digits, ., _ and -)'] };
    let listing;
    try {
      listing = JSON.parse(readFileSync(join(dir, file), 'utf8'));
    } catch (error) {
      return { file, id: found[1], listing: undefined, problems: [`not valid JSON: ${error.message}`] };
    }
    const problems = validate(listing) ? [] : schemaProblems(validate.errors);
    if (typeof listing.id === 'string' && listing.id !== found[1]) problems.push(`id ${listing.id} must equal the file name (${found[1]})`);
    if (typeof listing.license === 'string' && !validSpdx(listing.license)) problems.push(`license ${JSON.stringify(listing.license)} is not an SPDX license expression (for example MIT or Apache-2.0 OR MIT)`);
    return { file, id: found[1], listing, problems };
  });
}

/**
 * Every problem across `found` (readListings), prefixed with its file. `reserved` is the Set of ids and names Pigpen's own
 * Piglets and components hold (see reservedNames in generate-index.mjs); `repository` is Pigpen's own (owner/repo).
 */
export function listingProblems(found, { reserved, repository }) {
  const problems = [];
  const seen = new Map();
  for (const { file, listing, problems: own } of found) {
    for (const problem of own) problems.push(`listings/${file}: ${problem}`);
    if (!listing || own.length) continue;
    const ids = [listing.id, listing.name].filter((value) => typeof value === 'string');
    for (const id of ids) {
      if (reserved.has(id.toLowerCase())) problems.push(`listings/${file}: ${id} is the id or name of a Pigpen Piglet or component; choose another id (a listing never replaces one of Pigpen's own)`);
    }
    if (listing.repository.toLowerCase() === `https://github.com/${repository}`.toLowerCase()) problems.push(`listings/${file}: ${listing.repository} is Pigpen's own repository; its Piglets and components are listed from piglets/ and components/, not as a listing`);
    const other = seen.get(listing.id);
    if (other) problems.push(`listings/${file}: id ${listing.id} is already listed by listings/${other}`);
    seen.set(listing.id, file);
  }
  return problems;
}

/** `git:` source spec of the pin, the form `pig install` and `pig piglet add` document for a Git commit (and subdirectory). */
export function listingSpec(listing) {
  return `git:${listing.repository}.git@${listing.commit}${listing.subdirectory ? `#subdirectory=${encodeURIComponent(listing.subdirectory)}` : ''}`;
}

export const listingInstallCommand = (listing) => `${listing.kind === 'package' ? 'pig install' : 'pig piglet add'} '${listingSpec(listing)}'`;

/** The index entry of one valid listing. Never official, never featured, never signed by Pigpen. */
export function listingEntry(listing) {
  const { kind, verified } = listing;
  const what = kind === 'package' ? 'installed from the pinned commit, validated, built and loaded in a faux-model session' : 'added from the pinned commit, validated, built into a Binary and run through a faux-model turn';
  const notes = [
    `Third-party listing: ${listing.name} is maintained by ${listing.maintainer}, not by Pigpen. Pigpen pins commit ${listing.commit}${listing.tag ? ` (${listing.tag})` : ''} of ${listing.repository}; a later commit is not listed until the listing is updated. Pigpen did not review its code for security.`,
    `Verified with PiG ${verified.pigVersion} (${verified.pigCommit.slice(0, 12)}) on ${verified.date}: ${what}. That checks it loads, not what it does, and not Pi compatibility.`,
    ...(kind === 'piglet' ? ['Pigpen publishes no Binary for a listed Piglet: add the source with the command above and build it with pig piglet build.'] : []),
    ...(listing.notes ?? []),
  ];
  const common = {
    id: listing.id, kind, name: listing.name, description: listing.description, maintainer: listing.maintainer,
    official: false, featured: false, languages: listing.languages ?? [], repository: listing.repository,
    source: {
      type: 'git', spec: listingSpec(listing),
      url: `${listing.repository}/tree/${listing.commit}${listing.subdirectory ? '/' + listing.subdirectory : ''}`,
    },
    license: listing.license,
  };
  return kind === 'package'
    ? { ...common, status: 'available', version: listing.version, resources: listing.resources, installCommand: listingInstallCommand(listing), piCompatibility: 'not-tested', notes }
    : { ...common, status: 'planned', releases: [], addCommand: listingInstallCommand(listing), notes };
}
