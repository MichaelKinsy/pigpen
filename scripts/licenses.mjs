// SPDX licence handling for Pigpen: what a licence expression says, which licences may be fused into which, and the
// notices the catalogue and the README show for a licence that is not plain MIT. Pure functions over strings plus the
// two readers that find a component's and a Piglet's declared licence; nothing here touches the network.
//
// "Fuse" means: a Package's code is selected by a Piglet, so it ships inside that Piglet's Binary. The Binary is then one
// distributed work and has to be licensable as a whole. The table below answers, per licence, which outgoing licences a
// work containing it may carry. It is deliberately conservative: where an answer needs a lawyer (a licence exception, a
// licence the table does not know) the guard refuses and says so, and the owner extends this file after a review.
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const PERMISSIVE = ['MIT', 'MIT-0', 'Apache-2.0', 'BSD-2-Clause', 'BSD-3-Clause', 'ISC', '0BSD', 'Unlicense', 'CC0-1.0', 'Zlib', 'BSL-1.0', 'BlueOak-1.0.0'];
const GPL2 = ['GPL-2.0-only', 'GPL-2.0-or-later'];
const GPL3 = ['GPL-3.0-only', 'GPL-3.0-or-later'];
const AGPL3 = ['AGPL-3.0-only', 'AGPL-3.0-or-later'];
const LGPL21 = ['LGPL-2.1-only', 'LGPL-2.1-or-later'];
const LGPL3 = ['LGPL-3.0-only', 'LGPL-3.0-or-later'];

// Copyleft licence -> the outgoing licences a combined work containing it may have (itself always included). "-or-later"
// grants the later versions, so it may move to them; "-only" may not. The GPL-3.0 family may move to the AGPL-3.0 (GPLv3
// section 13), not the other way round. LGPL-2.1 may be converted to the GPL, version 2 or any later one (its section 3);
// LGPL-3.0 to GPL-3.0 (its section 2). The MPL-2.0 names the GPL and LGPL as secondary licences (section 1.12).
const COPYLEFT_INTO = {
  'GPL-2.0-only': ['GPL-2.0-only'],
  'GPL-2.0-or-later': [...GPL2, ...GPL3, ...AGPL3],
  'GPL-3.0-only': ['GPL-3.0-only', 'AGPL-3.0-only'],
  'GPL-3.0-or-later': [...GPL3, ...AGPL3],
  'AGPL-3.0-only': ['AGPL-3.0-only'],
  'AGPL-3.0-or-later': AGPL3,
  'LGPL-2.1-only': ['LGPL-2.1-only', ...GPL2, ...GPL3, ...AGPL3],
  'LGPL-2.1-or-later': [...LGPL21, ...LGPL3, ...GPL2, ...GPL3, ...AGPL3],
  'LGPL-3.0-only': ['LGPL-3.0-only', 'GPL-3.0-only', 'AGPL-3.0-only'],
  'LGPL-3.0-or-later': [...LGPL3, ...GPL3, ...AGPL3],
  'MPL-2.0': ['MPL-2.0', ...GPL2, ...GPL3, ...AGPL3, ...LGPL21, ...LGPL3],
};

const CANONICAL = new Map([...PERMISSIVE, ...Object.keys(COPYLEFT_INTO)].map((id) => [id.toLowerCase(), id]));
const NOT_A_LICENSE = new Set(['NONE', 'NOASSERTION', 'UNLICENSED', 'UNKNOWN']);

export const isCopyleft = (id) => Object.hasOwn(COPYLEFT_INTO, id);
// The GPL and AGPL families: using them relicenses the whole work. The LGPL and MPL are weaker (a library link, file-level
// copyleft); Pigpen still keeps them out of a Piglet under another licence, as its own policy.
const isStrongCopyleft = (id) => /^A?GPL-/.test(id);
// Licences whose terms stop at version 2, so a work that also holds Apache-2.0 code (GPLv3-only in effect) cannot carry them.
const STOPS_AT_VERSION_2 = new Set([...GPL2, ...LGPL21]);

/** An SPDX expression as a tree: {id, exception?} | {and: [...]} | {or: [...]}. Throws a short message when it is not one. */
export function parseSpdx(text) {
  if (typeof text !== 'string' || !text.trim()) throw new Error('no licence is declared');
  const tokens = text.match(/\(|\)|[^\s()]+/g);
  let at = 0;
  const bad = (why) => new Error(`${JSON.stringify(text)} is not an SPDX licence expression (${why})`);
  const atom = () => {
    const token = tokens[at++];
    if (token === '(') {
      const inner = orExpression();
      if (tokens[at++] !== ')') throw bad('unbalanced parenthesis');
      return inner;
    }
    if (token === undefined || token === ')' || ['AND', 'OR', 'WITH'].includes(token) || !/^[A-Za-z0-9][A-Za-z0-9.+-]*$/.test(token)) throw bad(`unexpected ${token === undefined ? 'end' : JSON.stringify(token)}`);
    if (NOT_A_LICENSE.has(token.toUpperCase()) || /^(?:Document|License)Ref-/i.test(token)) throw new Error(`${JSON.stringify(token)} is not a licence the guard can check`);
    const node = { id: token };
    if (tokens[at] === 'WITH') {
      at++;
      const exception = tokens[at++];
      if (!exception || !/^[A-Za-z0-9][A-Za-z0-9.-]*$/.test(exception)) throw bad('WITH needs an exception id');
      node.exception = exception;
    }
    return node;
  };
  const list = (join, next) => {
    const items = [next()];
    while (tokens[at] === join) { at++; items.push(next()); }
    return items.length === 1 ? items[0] : { [join.toLowerCase()]: items };
  };
  const andExpression = () => list('AND', atom);
  const orExpression = () => list('OR', andExpression);
  const tree = orExpression();
  if (at !== tokens.length) throw bad(`unexpected ${JSON.stringify(tokens[at])}`);
  return tree;
}

/** One licence id in its current SPDX form: the deprecated `GPL-3.0`, `GPL-3.0+` become GPL-3.0-only, GPL-3.0-or-later. */
export function canonicalId(id) {
  const m = /^((?:A|L)?GPL-\d\.\d)(-only|-or-later|\+)?$/i.exec(id);
  const text = m ? m[1].toUpperCase() + (m[2] === '+' || m[2]?.toLowerCase() === '-or-later' ? '-or-later' : '-only') : id;
  return CANONICAL.get(text.toLowerCase());
}

/** The licence ids an expression names, as written. */
export const spdxIds = (node) => (node.id ? [node.id] : (node.and ?? node.or).flatMap(spdxIds));

/** The canonical licence ids an expression names (unknown ids are dropped); empty for an unparseable expression. */
export function licenseIds(text) {
  try { return spdxIds(parseSpdx(text)).map(canonicalId).filter(Boolean); } catch { return []; }
}

const label = (id) => (isCopyleft(id) ? `${id} (copyleft)` : id);

function atomProblem(component, target) {
  if (component === target) return '';
  if (!isCopyleft(component)) {
    // The Binary carries the Piglet's licence. Apache-2.0 code makes the combination GPLv3-only in effect, which a licence that
    // stops at version 2 cannot promise (the GPLv2 section 6 "no further restrictions" term).
    if (component === 'Apache-2.0' && STOPS_AT_VERSION_2.has(target)) return `Apache-2.0 code is not compatible with ${target}: the combination would be GPLv3-only in effect, and ${target} promises version 2`;
    return '';
  }
  if (!isCopyleft(target)) {
    return isStrongCopyleft(component)
      ? `${component} (copyleft) would force the whole work, Piglet and Binary, to ${component}, not ${target}`
      : `Pigpen policy: ${component} (weak copyleft) is a standalone, opt-in Package and is not fused into a Piglet or Binary licensed ${target}`;
  }
  if (COPYLEFT_INTO[component].includes(target)) return '';
  return `${component} cannot be relicensed as ${target} (it may only go to: ${COPYLEFT_INTO[component].join(', ')})`;
}

/**
 * Whether a component under `component` (an SPDX expression) may be fused into a work whose own licence is `target`.
 * An OR in the component lets the distributor pick the alternative that works, an OR in the target lets the work be
 * offered under any one of its alternatives, and that one alternative has to accept the whole component; an AND on
 * either side has to hold for every part. To check several components together, join them with AND.
 * Returns {ok: true} or {ok: false, reason}. Missing, malformed, unknown and exception-bearing licences are refused.
 */
export function canFuse(component, target) {
  let c, t;
  try { c = parseSpdx(component); } catch (error) { return { ok: false, reason: `the component's licence: ${error.message}` }; }
  try { t = parseSpdx(target); } catch (error) { return { ok: false, reason: `the Piglet's licence: ${error.message}` }; }
  const problem = (cn, tn) => {
    // Order matters: pick the component's alternative, then ONE target alternative for the whole component, and only then
    // split an AND. Otherwise each part of `GPL-2.0-only AND GPL-3.0-only` could choose its own target alternative.
    if (cn.or) { const each = cn.or.map((part) => problem(part, tn)); return each.some((p) => !p) ? '' : each.join('; '); }
    if (tn.or) { const each = tn.or.map((part) => problem(cn, part)); return each.some((p) => !p) ? '' : each.join('; or '); }
    if (cn.and) return cn.and.map((part) => problem(part, tn)).find(Boolean) ?? '';
    if (tn.and) return tn.and.map((part) => problem(cn, part)).find(Boolean) ?? '';
    for (const [node, who] of [[cn, "the component's"], [tn, "the Piglet's"]]) {
      if (node.exception) return `${node.id} WITH ${node.exception}: ${who} licence carries an exception, which the guard does not evaluate; have the owner review it and extend scripts/licenses.mjs`;
    }
    const known = [canonicalId(cn.id), canonicalId(tn.id)];
    const unknown = [cn.id, tn.id].filter((_, i) => !known[i]);
    if (unknown.length) return `${unknown.join(', ')} is not in Pigpen's licence compatibility table (scripts/licenses.mjs); have the owner review it and add it`;
    return atomProblem(known[0], known[1]);
  };
  const reason = problem(c, t);
  return reason ? { ok: false, reason } : { ok: true };
}

/** The sentence the guard prints when a Package may not be fused into a Piglet; one place so tests and docs match. */
export function refusal({ kind = 'Package', component, componentLicense, piglet, pigletLicense, reason }) {
  return `refused: ${kind} "${component}" (${componentLicense ?? 'no licence declared'}) cannot be fused into the Piglet "${piglet}" (${pigletLicense ?? 'no licence declared'}): ${reason}. `
    + (kind === 'Package' ? 'Keep it a standalone, opt-in Package that users install on its own, or have the owner relicense the Piglet to a licence that is compatible.'
      : 'Extend a Piglet whose licence is compatible, or have the owner relicense the Piglet.');
}

/** A Package's declared licence: package.json `license`, else provenance.json (an Archon plugin has no package.json). */
export function componentLicense(directory) {
  for (const file of ['package.json', 'provenance.json']) {
    const path = join(directory, file);
    if (!existsSync(path)) continue;
    const license = JSON.parse(readFileSync(path, 'utf8')).license;
    if (typeof license === 'string' && license.trim()) return license.trim();
    if (file === 'package.json') return undefined;
  }
  return undefined;
}

/** A Piglet's declared licence: provenance.json `license` in its directory. */
export function pigletLicense(directory) {
  const path = join(directory, 'provenance.json');
  if (!existsSync(path)) return undefined;
  const license = JSON.parse(readFileSync(path, 'utf8')).license;
  return typeof license === 'string' && license.trim() ? license.trim() : undefined;
}

/** The catalogue note for a licence other than plain MIT; undefined for MIT. `kind` is 'package' or 'piglet'. */
export function licenseNote(license, kind = 'package') {
  if (license === undefined || license === 'MIT') return undefined;
  const copyleft = licenseIds(license).some(isCopyleft);
  if (!copyleft) return `Licence: ${license}.`;
  return `Licence: ${license} (copyleft, not MIT). ` + (kind === 'piglet'
    ? 'Every Package this Piglet selects has to be licence-compatible with it, and so does its Binary; Pigpen refuses a Package that is not.'
    : 'It is a standalone, opt-in Package: Pigpen never bundles it into a Piglet or Binary whose licence is not compatible, so install it on its own.');
}

export const LICENSES_BEGIN = '<!-- licenses:begin (generated by npm run generate; do not edit) -->';
export const LICENSES_END = '<!-- licenses:end -->';

/** Every component whose licence is not plain MIT, [{name, license}], sorted. Read from each component's own metadata. */
export function nonMitComponents(directory) {
  const base = join(directory, 'components');
  return readdirSync(base, { withFileTypes: true }).filter((entry) => entry.isDirectory()).map((entry) => ({ name: entry.name, license: componentLicense(join(base, entry.name)) }))
    .filter(({ license }) => license !== 'MIT').sort((a, b) => a.name.localeCompare(b.name, 'en'));
}

/** What the README block says about how a Package may be used, from its licence. */
function useOf(license) {
  if (license === undefined) return 'no licence declared; the guard refuses it';
  const ids = licenseIds(license);
  let known = true;
  try { known = spdxIds(parseSpdx(license)).every((id) => canonicalId(id)); } catch { known = false; }
  if (!known) return 'licence not in the compatibility table; the guard refuses it';
  return ids.some(isCopyleft) ? 'standalone, opt-in; never bundled into a Piglet or Binary under another licence' : 'permissive; may be bundled';
}

/** The generated README block listing the components that are not plain MIT. */
export function renderLicensesBlock(directory) {
  const rows = nonMitComponents(directory).map(({ name, license }) => `| [\`components/${name}\`](components/${name}) | ${license ?? 'none declared'} | ${useOf(license)} |`);
  return [LICENSES_BEGIN, '', 'Packages whose licence is not plain MIT (read from each Package\'s `package.json` `license`, else its `provenance.json`; every other component is MIT):', '',
    ...(rows.length ? ['| Package | Licence | Use |', '|---|---|---|', ...rows] : ['None.']), '', LICENSES_END].join('\n');
}

/** README text with the generated block replaced; throws if the markers are missing. */
export function withLicensesBlock(readme, directory) {
  const start = readme.indexOf(LICENSES_BEGIN);
  const end = readme.indexOf(LICENSES_END);
  if (start === -1 || end === -1 || end < start) throw new Error(`README.md needs the ${LICENSES_BEGIN} ... ${LICENSES_END} markers`);
  return readme.slice(0, start) + renderLicensesBlock(directory) + readme.slice(end + LICENSES_END.length);
}
