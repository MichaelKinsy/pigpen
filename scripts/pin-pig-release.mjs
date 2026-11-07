#!/usr/bin/env node
// Pins Pigpen to a published PiG release: scripts/pig-requirement.json (`pig`, and `release` with the tag, its source
// commit and every asset's SHA-256 from the release's SHA256SUMS) and the PIG_COMMIT of each workflow that builds with it.
//
// usage: node scripts/pin-pig-release.mjs <version>     e.g. node scripts/pin-pig-release.mjs 0.5.0
import { execFileSync } from 'node:child_process';
import { readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { runners } from './runners.mjs';

const version = process.argv[2]?.replace(/^v/, '');
if (!version || !/^\d+\.\d+\.\d+$/.test(version)) {
  console.error('usage: node scripts/pin-pig-release.mjs <version>');
  process.exit(2);
}
const root = fileURLToPath(new URL('..', import.meta.url));
const requirementFile = `${root}scripts/pig-requirement.json`;
const requirement = JSON.parse(readFileSync(requirementFile, 'utf8'));
const repository = requirement.release.repository;
const tag = `v${version}`;

// The commit the tag names: the peeled entry of an annotated tag, else the tag itself.
const refs = execFileSync('git', ['ls-remote', `https://github.com/${repository}.git`, `refs/tags/${tag}`, `refs/tags/${tag}^{}`], { encoding: 'utf8' });
const lines = refs.trim().split('\n').filter(Boolean).map((line) => line.split('\t'));
const peeled = lines.find(([, ref]) => ref.endsWith('^{}')) ?? lines[0];
if (!peeled) throw new Error(`${repository} has no tag ${tag}`);
const commit = peeled[0];

const sumsURL = `https://github.com/${repository}/releases/download/${tag}/SHA256SUMS`;
const response = await fetch(sumsURL);
if (!response.ok) throw new Error(`${sumsURL}: HTTP ${response.status}`);
const sums = new Map((await response.text()).trim().split('\n').map((line) => {
  const [sha256, file] = line.trim().split(/\s+\*?/);
  return [file, sha256];
}));

const assets = {};
// Every target a Piglet can build for, not the targets already pinned: the pin must be fillable from an empty `assets`.
for (const target of Object.keys(runners)) {
  const [os, arch] = target.split('/');
  const file = `pig-${version}-${os}-${arch}.${os === 'windows' ? 'zip' : 'tar.gz'}`;
  const sha256 = sums.get(file);
  if (!sha256) throw new Error(`${sumsURL} lists no ${file}`);
  assets[target] = { file, sha256 };
}
requirement.pig = version;
// Pinning needs the release's SHA256SUMS, so a pinned release is a published one: no `published` flag survives.
const { published: _published, ...pinned } = requirement.release;
requirement.release = { ...pinned, tag, commit, assets };

// Keep the file's layout: one asset per line.
const body = JSON.stringify(requirement, null, 2).replace(/\{\n\s+"file": ("[^"]+"),\n\s+"sha256": ("[^"]+")\n\s+\}/g, '{ "file": $1, "sha256": $2 }');
writeFileSync(requirementFile, `${body}\n`);

// Every workflow that carries a PIG_COMMIT pin, found rather than listed: a new one (release-pig-acp.yml was added after the list)
// is re-pinned too, and scripts/check-workflows.mjs refuses any pin that differs from scripts/pig-requirement.json.
const pinLine = /PIG_COMMIT: '[0-9a-f]{40}' # PiG .*$/m;
const pinned_ = [];
for (const workflow of readdirSync(`${root}.github/workflows`).filter((file) => /\.ya?ml$/.test(file)).sort()) {
  const path = `${root}.github/workflows/${workflow}`;
  const text = readFileSync(path, 'utf8');
  if (!pinLine.test(text)) continue;
  writeFileSync(path, text.replace(pinLine, `PIG_COMMIT: '${commit}' # PiG ${tag}`));
  pinned_.push(workflow);
}
if (!pinned_.length) throw new Error('no workflow has a PIG_COMMIT line to update');
console.log(`pinned PiG ${tag} (${commit}) in scripts/pig-requirement.json and ${pinned_.length} workflows: ${pinned_.join(', ')}`);
