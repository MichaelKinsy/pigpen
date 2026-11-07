// components/kagent/harness/pig-kagent.sh against a fake helm and a fake kubectl: the uninstall guard, the timeout parsing
// and the rule that a preflight read error stops the run instead of skipping a check. No cluster and no real helm are needed.
import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { after, describe, it } from 'node:test';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const script = join(root, 'components/kagent/harness/pig-kagent.sh');
const scratch = mkdtempSync(join(tmpdir(), 'pig-kagent-installer-test-'));
after(() => rmSync(scratch, { recursive: true, force: true }));

// What helm template prints for a release that creates a ModelConfig, a Harness, an AgentTemplate and an Agent (no EgressPolicy).
const render = `---
# Source: pig-kagent/templates/modelconfig.yaml
apiVersion: api.kagent.dev/v1alpha3
kind: ModelConfig
metadata:
  name: pig-model
  namespace: kagent
spec:
  apiKeySecret: pig-model-key
  apiKeySecretKey: api-key
  openAI:
    baseUrl: https://api.example.com/v1
---
# Source: pig-kagent/templates/harness.yaml
apiVersion: api.kagent.dev/v1alpha3
kind: Harness
metadata:
  name: pig
  namespace: kagent
spec:
  byo: {}
---
# Source: pig-kagent/templates/agenttemplate.yaml
apiVersion: api.kagent.dev/v1alpha3
kind: AgentTemplate
metadata:
  name: pig
  namespace: kagent
---
# Source: pig-kagent/templates/agent.yaml
apiVersion: api.kagent.dev/v1alpha3
kind: Agent
metadata:
  name: pig
  namespace: kagent
`;

const fakeHelm = `#!/bin/sh
echo "helm $*" >> "$FAKE_DIR/log"
[ "$1" != --kube-context ] || shift 2
case $1 in
  template)
    n=$(cat "$FAKE_DIR/templates" 2>/dev/null || echo 0)
    echo $((n + 1)) > "$FAKE_DIR/templates"
    if [ -n "\${FAKE_RENDER_OK:-}" ] && [ "$n" -ge "$FAKE_RENDER_OK" ]; then echo "Error: template boom" >&2; exit 1; fi
    cat "$FAKE_DIR/render";;
  show) echo 'pig.dev/tested-kagent: "v1.0.0-rc3"';;
  status)
    case \${FAKE_STATUS:-ok} in
      ok) echo "STATUS: deployed";;
      missing) echo "Error: release: not found" >&2; exit 1;;
      *) echo 'Error: Kubernetes cluster unreachable: connection refused' >&2; exit 1;;
    esac;;
  upgrade) echo "Release upgraded";;
  uninstall) echo "release uninstalled";;
esac
`;

const fakeKubectl = `#!/bin/sh
echo "kubectl $*" >> "$FAKE_DIR/log"
[ "$1" != --context ] || shift 2
all="$*"
case $1 in
  config) echo kind-fake;;
  version|explain) ;;
  create)
    if [ -n "\${FAKE_CREATE_FAIL:-}" ]; then echo "error: unable to recognize: no matches for kind" >&2; exit 1; fi
    doc=$(cat)
    case $all in
      *apiKeySecret*) echo "pig-model-key/api-key";;
      *baseUrl*) echo "https://api.example.com/v1";;
      *metadata.name*) printf '%s\\n' "$doc" | sed -n 's/^  name: //p' | sed -n 1p;;
    esac;;
  get)
    case $all in
      "get crd agents.kagent.dev"*) exit 1;;
      "get crd"*) echo "v1alpha3 v1 ";;
      "get deploy"*) echo "kagent/kagent-controller@v1.0.0-rc3";;
      "get ns"*) echo true;;
      "get pods"*"spec.containers"*) echo "--preview PerTemplateSnapshotLocation GoldenEgressPolicy";;
      "get pods"*) echo substrate;;
      "get workerpool"*) exit 0;;
      "get cm"*) ;;
      "get secret"*) if [ -n "\${FAKE_SECRET:-}" ]; then echo api-key; else echo 'Error from server (NotFound)' >&2; exit 1; fi;;
      "get harnesses.api.kagent.dev -n"*"range"*) [ -z "\${FAKE_LIST_FAIL:-}" ] || { echo "Error from server (Forbidden)" >&2; exit 1; }; printf '%s' "\${FAKE_HARNESSES:-}";;
      "get agents.api.kagent.dev -n"*"range"*) printf '%s' "\${FAKE_AGENTS:-}";;
      "get agents.api.kagent.dev"*) echo "True|1|1|";;
      *) exit 1;;
    esac;;
esac
`;

function setup(name, files = {}) {
  const dir = join(scratch, name);
  const bin = join(dir, 'bin');
  mkdirSync(bin, { recursive: true });
  writeFileSync(join(bin, 'helm'), fakeHelm, { mode: 0o755 });
  writeFileSync(join(bin, 'kubectl'), fakeKubectl, { mode: 0o755 });
  writeFileSync(join(dir, 'render'), render);
  writeFileSync(join(dir, 'log'), '');
  for (const [file, text] of Object.entries(files)) writeFileSync(join(dir, file), text);
  return dir;
}
const run = (dir, args, env = {}) => {
  const result = spawnSync('sh', [script, ...args], { encoding: 'utf8', env: { ...process.env, PATH: `${join(dir, 'bin')}:${process.env.PATH}`, FAKE_DIR: dir, ...env } });
  return { ...result, log: readFileSync(join(dir, 'log'), 'utf8'), out: `${result.stdout}${result.stderr}` };
};

describe('pig-kagent.sh uninstall', () => {
  // pig-harness (release pig-harness) owns Harness pig-harness; pig-review is another release whose Agent uses it.
  const harnesses = 'pig-harness|pig-harness\nother|someone-else\n';
  const agents = 'pig-harness|pig-harness|pig-harness\npig-review|pig-harness|pig-review\n';

  it('prints the context, namespace and release before it acts', () => {
    const dir = setup('ctx');
    const result = run(dir, ['uninstall', '--context', 'kind-prod', '-n', 'team-a', '-r', 'pig-x'], { FAKE_HARNESSES: '', FAKE_AGENTS: '' });
    assert.equal(result.status, 0, result.out);
    assert.match(result.stdout, /context kind-prod, namespace team-a, release pig-x/);
    assert.match(result.log, /helm --kube-context kind-prod uninstall pig-x -n team-a/);
  });

  it('names the current context when --context is not given', () => {
    const dir = setup('ctx-default');
    const result = run(dir, ['uninstall'], { FAKE_HARNESSES: '', FAKE_AGENTS: '' });
    assert.match(result.stdout, /context kind-fake, namespace kagent, release pig/);
  });

  it('refuses, and removes nothing, when another Agent uses the Harness this release owns', () => {
    const dir = setup('refuse');
    const result = run(dir, ['uninstall', '-r', 'pig-harness'], { FAKE_HARNESSES: harnesses, FAKE_AGENTS: agents });
    assert.equal(result.status, 2, result.out);
    assert.match(result.stderr, /owns Harness pig-harness, which other Agents in kagent use: pig-review\(Harness pig-harness\)/);
    assert.match(result.stderr, /--force/);
    assert.ok(!result.log.includes('helm uninstall'), result.log);
    assert.doesNotMatch(result.stderr, /pig-harness\(Harness pig-harness\)/, 'the release is not blocked by its own Agent');
  });

  it('--force removes it and warns about the Agents that lose their Harness', () => {
    const dir = setup('force');
    const result = run(dir, ['uninstall', '-r', 'pig-harness', '--force'], { FAKE_HARNESSES: harnesses, FAKE_AGENTS: agents });
    assert.equal(result.status, 0, result.out);
    assert.match(result.stdout, /WARN {2}removing Harness pig-harness of release pig-harness although other Agents use it: pig-review\(Harness pig-harness\)/);
    assert.ok(result.log.includes('helm uninstall pig-harness -n kagent'), result.log);
  });

  it('removes a release whose Harness only its own Agent uses', () => {
    const dir = setup('own');
    const result = run(dir, ['uninstall', '-r', 'pig-harness'], { FAKE_HARNESSES: harnesses, FAKE_AGENTS: 'pig-harness|pig-harness|pig-harness\n' });
    assert.equal(result.status, 0, result.out);
    assert.match(result.stdout, /owns Harness pig-harness; no other Agent in kagent references it/);
    assert.ok(result.log.includes('helm uninstall pig-harness'));
  });

  it('removes an Agent release that owns no Harness, whatever else uses the shared one', () => {
    const dir = setup('agent-only');
    const result = run(dir, ['uninstall', '-r', 'pig-review'], { FAKE_HARNESSES: harnesses, FAKE_AGENTS: agents });
    assert.equal(result.status, 0, result.out);
    assert.match(result.stdout, /release pig-review owns no Harness/);
    assert.ok(result.log.includes('helm uninstall pig-review'));
  });

  it('refuses when it cannot list the Harnesses, unless --force', () => {
    const dir = setup('listfail');
    const refused = run(dir, ['uninstall', '-r', 'pig-harness'], { FAKE_LIST_FAIL: '1' });
    assert.equal(refused.status, 2, refused.out);
    assert.match(refused.stderr, /cannot tell whether other Agents use a Harness of release pig-harness/);
    assert.ok(!refused.log.includes('helm uninstall'));
    const forced = run(dir, ['uninstall', '-r', 'pig-harness', '--force'], { FAKE_LIST_FAIL: '1' });
    assert.equal(forced.status, 0, forced.out);
    assert.match(forced.stdout, /WARN {2}cannot tell whether other Agents use a Harness/);
    assert.ok(forced.log.includes('helm uninstall pig-harness'));
  });

  it('does nothing, successfully, for a release that is not installed; a failing helm is an error, not "not installed"', () => {
    const dir = setup('missing');
    const missing = run(dir, ['uninstall'], { FAKE_STATUS: 'missing' });
    assert.equal(missing.status, 0, missing.out);
    assert.match(missing.stdout, /release pig is not installed in kagent; nothing to do/);
    assert.ok(!missing.log.includes('uninstall'), missing.log);
    const down = run(dir, ['uninstall'], { FAKE_STATUS: 'unreachable' });
    assert.equal(down.status, 2, down.out);
    assert.match(down.stderr, /cannot read release pig in kagent \(context kind-fake\): Error: Kubernetes cluster unreachable/);
    assert.ok(!down.log.includes('helm uninstall'));
  });

  it('--force is refused for the other subcommands', () => {
    const dir = setup('force-install');
    const result = run(dir, ['install', '--force']);
    assert.equal(result.status, 2, result.out);
    assert.match(result.stderr, /--force only applies to uninstall/);
  });
});

describe('pig-kagent.sh --timeout', () => {
  it('reads 08 and 010 as decimal seconds (no octal arithmetic)', () => {
    for (const value of ['08', '09', '010', '0']) {
      const dir = setup(`timeout-${value}`);
      const result = run(dir, ['install', '--skip-preflight', '--timeout', value]);
      assert.equal(result.status, 0, `--timeout ${value}: ${result.out}`);
      assert.match(result.stdout, /ok {4}Agent kagent\/pig is Ready/);
    }
  });
  it('still refuses anything that is not whole seconds', () => {
    const dir = setup('timeout-bad');
    for (const value of ['', '1.5', '-3', 'abc', '8s']) {
      const result = run(dir, ['install', '--skip-preflight', '--timeout', value]);
      assert.equal(result.status, 2, `--timeout '${value}'`);
      assert.match(result.stderr, /--timeout takes whole seconds/);
    }
  });
});

describe('pig-kagent.sh check reads the chart or stops', () => {
  it('runs the Secret check when everything can be read: a missing Secret is a FAIL, a present one is ok', () => {
    const missing = run(setup('secret-missing'), ['check']);
    assert.equal(missing.status, 1, missing.out);
    assert.match(missing.stdout, /FAIL {2}Secret kagent\/pig-model-key is missing or has no key api-key/);
    const present = run(setup('secret-present'), ['check'], { FAKE_SECRET: '1' });
    assert.equal(present.status, 0, present.out);
    assert.match(present.stdout, /ok {4}Secret kagent\/pig-model-key has key api-key/);
  });

  it('stops with a clear error when kubectl cannot read the rendered chart, instead of skipping the Secret checks', () => {
    const result = run(setup('create-fails'), ['check'], { FAKE_CREATE_FAIL: '1' });
    assert.equal(result.status, 2, result.out);
    assert.match(result.stderr, /pig-kagent\.sh: cannot read .* from the rendered .*yaml: error: unable to recognize/);
    assert.doesNotMatch(result.stdout, /Secret kagent\//, 'no Secret line, and no silent pass either');
    assert.doesNotMatch(result.stdout, /0 failed/);
  });

  it('stops with a clear error when the chart stops rendering after the first render', () => {
    const result = run(setup('render-fails'), ['check'], { FAKE_RENDER_OK: '1' });
    assert.equal(result.status, 2, result.out);
    assert.match(result.stderr, /pig-kagent\.sh: cannot render the chart to read .*yaml: Error: template boom/);
    assert.doesNotMatch(result.stdout, /0 failed/);
  });

  it('treats a template the chart does not render as empty, not as an error', () => {
    const result = run(setup('no-egress'), ['check'], { FAKE_SECRET: '1' });
    assert.equal(result.status, 0, result.out);
    assert.match(result.stdout, /this release adds 0 origin\(s\)/);
  });

  it('is not fooled by a Source path that only ends like another template', () => {
    const dir = setup('suffix', { render: render.replace('templates/modelconfig.yaml', 'templates/old-modelconfig.yaml') });
    const result = run(dir, ['check'], { FAKE_SECRET: '1' });
    assert.equal(result.status, 0, result.out);
    assert.doesNotMatch(result.stdout, /Secret kagent\/pig-model-key/, 'old-modelconfig.yaml is not modelconfig.yaml');
  });
});
