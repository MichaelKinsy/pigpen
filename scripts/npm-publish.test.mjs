// The publish plan: Packages first, then Piglets, a version already on npm skipped, and nothing published when a Package
// fails (a Piglet's Packages must exist before the Piglet that names them). npm and the filesystem are injected.
import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { publishAll, publishPlan, trustedPublisherInstructions } from './npm-publish.mjs';

const plan = [
  { kind: 'package', name: '@pi-in-go/pigpen-a', version: '0.1.0', dir: '/t/packages/a' },
  { kind: 'package', name: '@pi-in-go/pigpen-b', version: '0.1.0', dir: '/t/packages/b' },
  { kind: 'piglet', name: '@pi-in-go/pigpen-piglet-x', version: '0.1.0', dir: '/t/piglets/x' },
];

describe('publishPlan', () => {
  it('lists every Package, then every Piglet, each by its scoped name and version', () => {
    const real = publishPlan('/nonexistent-build-dir', { skipBuild: true });
    assert.ok(real.length > 29);
    const firstPiglet = real.findIndex((e) => e.kind === 'piglet');
    assert.ok(firstPiglet > 0);
    assert.ok(real.slice(0, firstPiglet).every((e) => e.kind === 'package' && /^@pi-in-go\/pigpen-[a-z0-9-]+$/.test(e.name)));
    assert.ok(real.slice(firstPiglet).every((e) => e.kind === 'piglet' && e.name.startsWith('@pi-in-go/pigpen-piglet-')));
  });
});

describe('publishAll', () => {
  const harness = (existing = [], failing = []) => {
    const calls = [];
    return {
      calls,
      exists: (name, version) => existing.includes(`${name}@${version}`),
      publish: (entry, options) => { calls.push([entry.name, options]); if (failing.includes(entry.name)) throw new Error('E403 forbidden'); },
    };
  };
  it('publishes in order with public access', () => {
    const h = harness();
    const result = publishAll(plan, { ...h, provenance: false });
    assert.deepEqual(h.calls.map(([n]) => n), plan.map((e) => e.name));
    assert.deepEqual(result.published, plan.map((e) => `${e.name}@${e.version}`));
    assert.deepEqual(h.calls[0][1], { provenance: false, dryRun: false });
  });
  it('skips a version that is already on npm, and says so', () => {
    const h = harness(['@pi-in-go/pigpen-b@0.1.0']);
    const result = publishAll(plan, { ...h });
    assert.deepEqual(result.skipped, ['@pi-in-go/pigpen-b@0.1.0']);
    assert.deepEqual(h.calls.map(([n]) => n), ['@pi-in-go/pigpen-a', '@pi-in-go/pigpen-piglet-x']);
  });
  it('publishes nothing twice: a second run over a published set does nothing', () => {
    const everything = plan.map((e) => `${e.name}@${e.version}`);
    const h = harness(everything);
    const result = publishAll(plan, { ...h });
    assert.deepEqual(h.calls, []);
    assert.equal(result.published.length, 0);
  });
  it('stops at the first failure and never reaches the Piglets after a failed Package', () => {
    const h = harness([], ['@pi-in-go/pigpen-a']);
    assert.throws(() => publishAll(plan, { ...h }), /@pi-in-go\/pigpen-a.*E403/s);
    assert.deepEqual(h.calls.map(([n]) => n), ['@pi-in-go/pigpen-a']);
  });
  it('passes provenance and dry-run through', () => {
    const h = harness();
    publishAll(plan, { ...h, provenance: true, dryRun: true });
    assert.deepEqual(h.calls[0][1], { provenance: true, dryRun: true });
  });
  it('refuses to publish a Piglet while one of its Packages is neither on npm nor in this run', () => {
    const h = harness();
    const needing = [{ ...plan[2], needs: ['@pi-in-go/pigpen-missing@^0.1.0'] }];
    assert.throws(() => publishAll(needing, { ...h, satisfied: () => false }), /pigpen-missing/);
    assert.deepEqual(h.calls, []);
  });
});

describe('trustedPublisherInstructions', () => {
  const text = trustedPublisherInstructions(plan, { repository: 'MichaelKinsy/pigpen' });
  it('gives the exact npmjs.com settings, once, and the settings page of every package', () => {
    assert.match(text, /Organization or user: MichaelKinsy/);
    assert.match(text, /Repository: pigpen/);
    assert.match(text, /Workflow filename: npm-publish\.yml/);
    assert.match(text, /Environment name: \(leave empty\)/);
    for (const e of plan) assert.ok(text.includes(`https://www.npmjs.com/package/${e.name}/access`), e.name);
  });
  it('never mentions a token', () => {
    assert.doesNotMatch(text, /token/i);
  });
});
