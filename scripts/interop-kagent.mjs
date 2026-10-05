// Builds a kagent-compatible A2A endpoint from a kagent checkout and runs the a2a interop tests against it.
//   KAGENT_GO_DIR=<kagent>/go PIG_BIN=pig node scripts/interop-kagent.mjs
// The endpoint is kagent's own adk/pkg/a2a/server package (copied, with only its readiness port changed)
// serving an echo executor: components/a2a/port/interop/kagent/main.go. Nothing is written into the checkout.
import { cpSync, existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { requirePig } from './pig-bin.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const scratch = [];
process.on('exit', () => { for (const dir of scratch) rmSync(dir, { recursive: true, force: true }); });
const tmp = (prefix) => { const dir = mkdtempSync(join(tmpdir(), prefix)); scratch.push(dir); return dir; };

// The SDK directory: PIG_SDK_DIR, else `$PIG_BIN reload --sdk-path` in a scratch PIG_HOME. The go.work maps it with
// `replace` (a module with a third-party dependency cannot resolve the SDK's v0.0.0 through `use` alone).
function sdkDir() {
  if (process.env.PIG_SDK_DIR) return resolve(process.env.PIG_SDK_DIR);
  const pig = requirePig(); // PIG_SDK_DIR is the other way to name the SDK
  const home = tmp('pigpen-sdk-');
  const out = execFileSync(pig, ['reload', '--sdk-path'], {
    encoding: 'utf8', env: { ...process.env, HOME: home, PIG_HOME: home, PIG_CODING_AGENT_DIR: join(home, 'agent') },
  });
  return out.trim().split('\n').at(-1);
}

const kagent = process.env.KAGENT_GO_DIR && resolve(process.env.KAGENT_GO_DIR);
if (!kagent || !existsSync(join(kagent, 'adk/pkg/a2a/server/server.go'))) {
  console.error('set KAGENT_GO_DIR to the go/ directory of a kagent checkout');
  process.exit(2);
}
const work = tmp('kagent-interop-');
const serverDir = join(work, 'server');
cpSync(join(kagent, 'adk/pkg/a2a/server'), serverDir, { recursive: true });
for (const f of readdirSync(serverDir)) if (f.endsWith('_test.go')) spawnSync('rm', [join(serverDir, f)]);
const serverGo = join(serverDir, 'server.go');
const source = readFileSync(serverGo, 'utf8');
if (!source.includes('Addr: ":8081"')) throw new Error('kagent changed its readiness address; update this script');
writeFileSync(serverGo, source.replace('Addr: ":8081"', 'Addr: "127.0.0.1:0"'));
cpSync(join(root, 'components/a2a/port/interop/kagent/main.go'), join(work, 'main.go'));
const goMod = readFileSync(join(kagent, 'go.mod'), 'utf8');
const goVersion = /^go (\S+)/m.exec(goMod)[1];
const substrate = /^replace .*substrate.*$/m.exec(goMod)?.[0] ?? '';
writeFileSync(join(work, 'go.mod'), `module kagentinterop\n\ngo ${goVersion}\n\nrequire github.com/kagent-dev/kagent/go v0.0.0\n\nreplace github.com/kagent-dev/kagent/go => ${kagent}\n${substrate}\n`);
cpSync(join(kagent, 'go.sum'), join(work, 'go.sum'));
const exe = join(work, 'kagent-a2a-echo');
const build = spawnSync('go', ['build', '-o', exe, '.'], { cwd: work, stdio: 'inherit', env: { ...process.env, GOFLAGS: '-mod=mod', GOWORK: 'off' } });
if (build.status !== 0) process.exit(build.status ?? 1);
const a2a = join(root, 'components/a2a/extensions/a2a');
const goWork = join(work, 'test.go.work');
writeFileSync(goWork, `go 1.26.0\n\nuse (\n\t${a2a}\n)\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ${sdkDir()}\n`);
const env = { ...process.env, GOWORK: goWork, GOFLAGS: '', KAGENT_A2A_ECHO: exe };
const test = spawnSync('go', ['test', '-count=1', '-race', '-v', '-run', 'Interop', './...', ...process.argv.slice(2)], { cwd: a2a, stdio: 'inherit', env });
process.exit(test.status ?? 1);
