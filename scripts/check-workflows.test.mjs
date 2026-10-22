// The workflow hardening gate (scripts/check-workflows.mjs) on small workflows written here. It tests the gate, not
// Pigpen's own workflow: `npm run quality` runs the gate over .github/workflows, and a hosted run is the workflow's test.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';
import { hardeningProblems, pinProblems, targetProblems, releaseProblems, tagProblems, npmPublishProblems, cacheProblems, goToolchainProblems, vulnerabilityProblems } from './check-workflows.mjs';

const sha = 'a'.repeat(40);
const good = `
on: { pull_request: {} }
permissions:
  contents: read
jobs:
  build:
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@${sha} # v4
        with: { persist-credentials: false }
      - uses: ./.github/actions/local
      - run: echo ok
`;

describe('hardeningProblems', () => {
  it('accepts a pinned, read-only, secret-free workflow', () => {
    assert.deepEqual(hardeningProblems('ci.yml', good), []);
  });

  it('refuses an action pinned to a tag or branch', () => {
    const problems = hardeningProblems('ci.yml', good.replace(`@${sha}`, '@v4'));
    assert.match(problems.join('\n'), /ci\.yml: actions\/checkout@v4 is not pinned to a full commit SHA/);
  });

  it('refuses a reusable workflow that is not pinned', () => {
    const text = good.replace('  build:\n', '  reuse:\n    uses: org/repo/.github/workflows/x.yml@main\n  build:\n');
    assert.match(hardeningProblems('ci.yml', text).join('\n'), /org\/repo\/\.github\/workflows\/x\.yml@main is not pinned/);
  });

  it('refuses missing, write or widened permissions', () => {
    assert.match(hardeningProblems('ci.yml', good.replace('permissions:\n  contents: read\n', '')).join('\n'), /no top-level permissions/);
    assert.match(hardeningProblems('ci.yml', good.replace('contents: read', 'contents: write')).join('\n'), /contents: write/);
    assert.match(hardeningProblems('ci.yml', good.replace('permissions:\n  contents: read', 'permissions: write-all')).join('\n'), /write-all/);
    const job = good.replace('    runs-on: ubuntu-24.04\n', '    runs-on: ubuntu-24.04\n    permissions: { packages: write }\n');
    assert.match(hardeningProblems('ci.yml', job).join('\n'), /job build: packages: write/);
  });

  it('refuses secrets and pull_request_target', () => {
    assert.match(hardeningProblems('ci.yml', good.replace('echo ok', 'echo ${{ secrets.TOKEN }}')).join('\n'), /uses a secret/);
    assert.match(hardeningProblems('ci.yml', good.replace('pull_request: {}', 'pull_request_target: {}')).join('\n'), /pull_request_target/);
  });

  it('refuses a checkout that keeps its token, and a job without a timeout', () => {
    assert.match(hardeningProblems('ci.yml', good.replace('persist-credentials: false', 'fetch-depth: 0')).join('\n'), /persist-credentials: false/);
    assert.match(hardeningProblems('ci.yml', good.replace('    timeout-minutes: 10\n', '')).join('\n'), /job build has no timeout-minutes/);
  });

  it('accepts a matrix timeout only when every entry sets a positive number', () => {
    const matrix = (include) => good.replace('    timeout-minutes: 10\n', `    timeout-minutes: \${{ matrix.timeout }}\n    strategy:\n      matrix:\n        include: ${include}\n`);
    assert.deepEqual(hardeningProblems('ci.yml', matrix('[{ name: a, timeout: 5 }, { name: b, timeout: 20 }]')), []);
    assert.match(hardeningProblems('ci.yml', matrix('[{ name: a, timeout: 5 }, { name: b }]')).join('\n'), /job build has no timeout-minutes/);
    assert.match(hardeningProblems('ci.yml', matrix('[{ name: a, timeout: 0 }]')).join('\n'), /job build has no timeout-minutes/);
    assert.match(hardeningProblems('ci.yml', matrix('[{ name: a, timeout: "20" }]')).join('\n'), /job build has no timeout-minutes/);
  });
});

describe('pinProblems', () => {
  const requirement = { release: { repository: 'Owner/PiG', commit: 'c'.repeat(40) } };
  const workflow = (commit, ref = '${{ env.PIG_COMMIT }}') => ({
    env: { PIG_COMMIT: commit },
    jobs: { a: { steps: [{ uses: `actions/checkout@${sha}`, with: { repository: 'Owner/PiG', ref } }] } },
  });

  it('accepts one PiG pin, equal to the requirement and used by every PiG checkout', () => {
    assert.deepEqual(pinProblems(workflow('c'.repeat(40)), requirement), []);
  });

  it('refuses a PIG_COMMIT that differs from scripts/pig-requirement.json', () => {
    assert.match(pinProblems(workflow('d'.repeat(40)), requirement).join('\n'), /PIG_COMMIT .* is not the pinned release commit/);
  });

  it('refuses a PiG checkout at its own ref', () => {
    assert.match(pinProblems(workflow('c'.repeat(40), 'main'), requirement).join('\n'), /checks out Owner\/PiG at main/);
  });
});

describe('targetProblems', () => {
  const runners = { 'linux/amd64': 'ubuntu-24.04', 'windows/amd64': 'windows-2025' };
  const workflow = (include) => ({ jobs: { 'platform-matrix': { strategy: { matrix: { include } } } } });

  it('accepts a matrix with each declared target on its native runner', () => {
    const include = [{ target: 'linux/amd64', runner: 'ubuntu-24.04' }, { target: 'windows/amd64', runner: 'windows-2025' }];
    assert.deepEqual(targetProblems(workflow(include), ['linux/amd64', 'windows/amd64'], runners), []);
  });

  it('refuses a declared target the matrix does not build, and a target on the wrong runner', () => {
    const problems = targetProblems(workflow([{ target: 'linux/amd64', runner: 'windows-2025' }]), ['linux/amd64', 'windows/amd64'], runners).join('\n');
    assert.match(problems, /windows\/amd64 is a manifest target the platform matrix does not build/);
    assert.match(problems, /linux\/amd64 runs on windows-2025, not ubuntu-24\.04/);
  });
});

// The release workflow is the one place a secret exists: PIGLET_SIGNING_KEY, an environment secret, read only by a step
// that runs the pinned pig, never a repository script. These tests are the gate's rules on small workflows.
const release = `
on:
  push:
    tags: ['*/v*']
permissions:
  contents: read
jobs:
  plan:
    if: \${{ !startsWith(github.ref_name, 'npm/') }}
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@${sha}
        with: { persist-credentials: false }
      - run: npm ci --ignore-scripts && node scripts/build-matrix.mjs
  build:
    needs: plan
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    environment: release
    permissions: { contents: read, id-token: write, attestations: write }
    steps:
      - uses: actions/checkout@${sha}
        with: { persist-credentials: false }
      - run: npm ci --ignore-scripts && npm run stage
      - name: Build the signed Binary
        env:
          PIGLET_SIGNING_KEY: \${{ secrets.PIGLET_SIGNING_KEY }}
        run: |
          printf '%s\\n' "$PIGLET_SIGNING_KEY" > "$key"
          "$PIG_BIN" piglet build x --sign-key "$key"
  publish:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    environment: release
    permissions: { contents: write }
    steps:
      - uses: actions/checkout@${sha}
        with: { persist-credentials: false }
      - env:
          GH_TOKEN: \${{ github.token }}
          PIGLET_SIGNING_KEY: \${{ secrets.PIGLET_SIGNING_KEY }}
        run: '"$PIG_BIN" piglet publish x --sign-key "$key" --yes'
`;
const problemsOf = (text, file = 'release.yml') => [...hardeningProblems(file, text), ...releaseProblems(file, text)].join('\n');

describe('the release workflow', () => {
  it('accepts the signing key in the signing steps of the release environment, with the writes it needs', () => {
    assert.equal(problemsOf(release), '');
  });
  it('refuses the secret in any other workflow', () => {
    assert.match(problemsOf(release, 'ci.yml'), /uses a secret/);
  });
  it('refuses any other secret, and the signing key outside a step env', () => {
    assert.match(problemsOf(release.replace('secrets.PIGLET_SIGNING_KEY }}\n        run: |', 'secrets.OTHER }}\n        run: |')), /secrets\.OTHER/);
    assert.match(problemsOf(release.replace("      - run: npm ci --ignore-scripts && node scripts/build-matrix.mjs", "      - run: echo ${{ secrets.PIGLET_SIGNING_KEY }}")), /PIGLET_SIGNING_KEY.*only in the env of a step/s);
    assert.match(problemsOf(release.replace('    environment: release\n    permissions: { contents: read, id-token', '    env:\n      PIGLET_SIGNING_KEY: ${{ secrets.PIGLET_SIGNING_KEY }}\n    environment: release\n    permissions: { contents: read, id-token')), /only in the env of a step/);
  });
  it('refuses the key in a step that runs a repository script or npm', () => {
    assert.match(problemsOf(release.replace('"$PIG_BIN" piglet build x --sign-key "$key"', 'npm run build:piglet')), /runs a repository script/);
    assert.match(problemsOf(release.replace('"$PIG_BIN" piglet build x --sign-key "$key"', 'node scripts/x.mjs; "$PIG_BIN" piglet build')), /runs a repository script/);
    assert.match(problemsOf(release.replace('"$PIG_BIN" piglet build x --sign-key "$key"', './build.sh')), /does not run "\$PIG_BIN"/);
  });
  it('refuses the key in a job that is not in the release environment', () => {
    assert.match(problemsOf(release.replace('    environment: release\n    permissions: { contents: write }', '    permissions: { contents: write }')), /job publish.*environment: release/s);
    assert.match(problemsOf(release.replace(/environment: release/g, 'environment: other')), /environment: release/);
  });
  it('allows write permissions only where the release needs them', () => {
    assert.match(problemsOf(release.replace('permissions: { contents: write }', 'permissions: { contents: write, packages: write }')), /job publish: packages: write/);
    assert.match(problemsOf(release.replace('    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    steps:\n      - uses: actions/checkout@' + sha + '\n        with: { persist-credentials: false }\n      - run: npm ci --ignore-scripts && node', '    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n    permissions: { contents: write }\n    steps:\n      - uses: actions/checkout@' + sha + '\n        with: { persist-credentials: false }\n      - run: npm ci --ignore-scripts && node')), /job plan: contents: write/);
  });
  it('runs only on pushed Piglet tags', () => {
    assert.match(problemsOf(release.replace("push:\n    tags: ['*/v*']", "push:\n    tags: ['*/v*']\n  workflow_dispatch: {}")), /only on a pushed tag/);
    assert.match(problemsOf(release.replace("tags: ['*/v*']", "branches: [main]")), /only on a pushed tag/);
  });
});

const on = (...tags) => ({ on: { push: { tags } } });
describe('tagProblems', () => {
  it('accepts a Piglet filter that cannot match a Package tag, and the reverse', () => {
    assert.deepEqual(tagProblems({ 'release.yml': on('*/v*'), 'release-package.yml': on('components/*/v*') }), []);
  });
  it('refuses a Piglet workflow that a Package tag would start, and the reverse', () => {
    assert.match(tagProblems({ 'release.yml': on('**/v*') }).join('\n'), /release\.yml.*\*\*\/v\*.*components\/herdr\/v0\.1\.0/);
    assert.match(tagProblems({ 'release-package.yml': on('**') }).join('\n'), /release-package\.yml.*herdr\/v0\.1\.0/);
    assert.match(tagProblems({ 'release.yml': on('v*') }).join('\n'), /does not match the Piglet tag/);
  });
});

describe('the npm publish workflow', () => {
  const npm = `
on:
  push:
    tags: ['npm/v*']
  workflow_dispatch: {}
permissions:
  contents: read
jobs:
  publish:
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    permissions: { contents: read, id-token: write }
    steps:
      - uses: actions/checkout@${sha}
        with: { persist-credentials: false }
      - run: npm ci --ignore-scripts && node scripts/npm-publish.mjs --provenance
`;
  const problems = (text) => [...hardeningProblems('npm-publish.yml', text), ...npmPublishProblems('npm-publish.yml', text)].join('\n');
  it('accepts OIDC publishing: id-token in the publish job, no secret, tag or manual run', () => {
    assert.equal(problems(npm), '');
  });
  it('allows id-token: write only in the publish job, and no other write', () => {
    assert.match(problems(npm.replace('  publish:\n', '  other:\n    runs-on: ubuntu-24.04\n    timeout-minutes: 5\n    permissions: { id-token: write }\n    steps: []\n  publish:\n')), /job other: id-token: write/);
    assert.match(problems(npm.replace('{ contents: read, id-token: write }', '{ contents: write, id-token: write }')), /job publish: contents: write/);
  });
  it('refuses a token: no secret, no NODE_AUTH_TOKEN, no NPM_TOKEN', () => {
    assert.match(problems(npm.replace('      - run: npm ci', '      - env: { NODE_AUTH_TOKEN: x }\n        run: npm ci')), /token/);
    assert.match(problems(npm + '      - run: echo ${{ secrets.NPM_TOKEN }}\n'), /uses a secret/);
  });
  it('requires provenance and trusted publishing, and only tag or manual triggers', () => {
    assert.match(problems(npm.replace(' --provenance', '')), /--provenance/);
    assert.match(problems(npm.replace("push:\n    tags: ['npm/v*']", 'push:\n    branches: [main]')), /npm\/v\*/);
    assert.match(problems(npm.replace('workflow_dispatch: {}', 'pull_request: {}')), /only/);
  });
  it('is a tag no Piglet or Package workflow filter can be mistaken for, and release.yml skips it', () => {
    assert.deepEqual(tagProblems({ 'npm-publish.yml': on('npm/v*'), 'release-package.yml': on('components/*/v*') }), []);
    assert.match(tagProblems({ 'npm-publish.yml': on('**/v*') }).join('\n'), /npm-publish\.yml.*components\/herdr\/v0\.1\.0/);
    assert.equal(problemsOf(release), '');
    assert.match(problemsOf(release.replace("    if: ${{ !startsWith(github.ref_name, 'npm/') }}\n", '')), /npm\/ tag/);
  });
});

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

describe('the Go build cache', () => {
  const read = (name) => readFileSync(join(root, '.github/workflows', name), 'utf8');
  const workflows = () => ({ 'release.yml': read('release.yml'), 'gocache.yml': read('gocache.yml'), parsed: { 'gocache.yml': parse(read('gocache.yml')) } });
  it('is saved on main and only restored by the release', () => {
    assert.deepEqual(cacheProblems(workflows()), []);
  });
  it('refuses a release that saves the cache, which no later release could restore', () => {
    const w = workflows();
    w['release.yml'] = w['release.yml'].replace('actions/cache/restore@', 'actions/cache@');
    assert.match(cacheProblems(w).join('\n'), /saves the Go build cache/);
    w['release.yml'] = workflows()['release.yml'] + '\n      - uses: actions/cache/save@x\n';
    assert.match(cacheProblems(w).join('\n'), /saves the Go build cache/);
  });
  it('refuses a restore with no save on main, and a warm workflow that runs on a tag or a pull request', () => {
    const w = workflows();
    delete w['gocache.yml'];
    assert.match(cacheProblems(w).join('\n'), /no workflow on main saves/);
    const v = workflows();
    v.parsed['gocache.yml'] = { on: { push: { tags: ['*/v*'] } } };
    assert.match(cacheProblems(v).join('\n'), /only on a push to main/);
    v.parsed['gocache.yml'] = { on: { push: { branches: ['main'] }, pull_request: {}, workflow_dispatch: {} } };
    assert.match(cacheProblems(v).join('\n'), /only on a push to main/);
  });
  it('refuses keys that differ, so the restore would never match what main saved', () => {
    const w = workflows();
    w['gocache.yml'] = w['gocache.yml'].replace('hashFiles(\'.pig-src/go.sum\')', 'hashFiles(\'.pig-src/go.mod\')');
    assert.match(cacheProblems(w).join('\n'), /key differs/);
  });
});

describe('goToolchainProblems', () => {
  const workflow = (withs) => `
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/setup-go@${sha} # v6
        with: ${withs}
`;
  it("accepts a setup-go that reads PiG's .go-version", () => {
    assert.deepEqual(goToolchainProblems('ci.yml', workflow('{ go-version-file: .pig-src/.go-version }')), []);
  });
  it('refuses a go.mod floor, a literal go-version and no version input', () => {
    assert.match(goToolchainProblems('ci.yml', workflow('{ go-version-file: .pig-src/go.mod }')).join('\n'), /not \.pig-src\/\.go-version/);
    assert.match(goToolchainProblems('ci.yml', workflow("{ go-version: '1.27.2' }")).join('\n'), /literal go-version/);
    assert.match(goToolchainProblems('ci.yml', workflow('{ cache: false }')).join('\n'), /unset/);
  });
  it('ignores a workflow without setup-go', () => {
    assert.deepEqual(goToolchainProblems('npm-publish.yml', 'jobs:\n  a:\n    steps:\n      - run: echo\n'), []);
  });
});

describe('vulnerabilityProblems', () => {
  const workflow = () => parse(`
jobs:
  govulncheck:
    runs-on: ubuntu-24.04
    steps:
      - env: { PIG_SDK_DIR: x }
        run: node scripts/govulncheck.mjs
  ci-result:
    needs: [validate, govulncheck]
    steps:
      - env:
          GOVULNCHECK: \${{ needs.govulncheck.result }}
        run: '[ "$GOVULNCHECK" = success ]'
`);
  it('accepts one govulncheck job that ci-result needs and reads', () => {
    assert.deepEqual(vulnerabilityProblems(workflow()), []);
  });
  it('refuses a workflow that never runs the gate, or runs it twice', () => {
    const none = workflow();
    none.jobs.govulncheck.steps[0].run = 'echo skipped';
    assert.match(vulnerabilityProblems(none).join('\n'), /0 jobs run scripts\/govulncheck\.mjs/);
    const twice = workflow();
    twice.jobs.again = structuredClone(twice.jobs.govulncheck);
    assert.match(vulnerabilityProblems(twice).join('\n'), /2 jobs run scripts\/govulncheck\.mjs/);
  });
  it('refuses a gate that is conditional, or that the required check does not wait for and read', () => {
    const conditional = workflow();
    conditional.jobs.govulncheck.if = "github.event_name == 'push'";
    assert.match(vulnerabilityProblems(conditional).join('\n'), /has an if/);
    const unneeded = workflow();
    unneeded.jobs['ci-result'].needs = ['validate'];
    assert.match(vulnerabilityProblems(unneeded).join('\n'), /ci-result does not need job govulncheck/);
    const unread = workflow();
    unread.jobs['ci-result'].steps[0].env = {};
    assert.match(vulnerabilityProblems(unread).join('\n'), /does not read needs\.govulncheck\.result/);
  });
});
