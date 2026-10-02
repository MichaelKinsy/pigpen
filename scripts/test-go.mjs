// Runs `go test` for every Go extension under components/*/extensions/*.
// The extensions require the PiG SDK at v0.0.0, which `pig` resolves at build time.
// Tests need the same SDK, so a temporary go.work maps it. Select the SDK with, in order:
//   PIG_SDK_DIR        a PiG checkout's extensions/sdk directory
//   PIG_SOURCE_ROOT    a PiG checkout; uses <root>/extensions/sdk
//   PIG_BIN            a pig binary; uses the SDK its `extension init` stages (isolated PIG_HOME)
// Extra arguments go to `go test`, for example `-race -count=10`.
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { root } from './generate-index.mjs';
import { requirePig } from './pig-bin.mjs';

function sdkDirectory(scratch) {
  if (process.env.PIG_SDK_DIR) return process.env.PIG_SDK_DIR;
  if (process.env.PIG_SOURCE_ROOT) return join(process.env.PIG_SOURCE_ROOT, 'extensions/sdk');
  const pig = requirePig(); // PIG_SDK_DIR and PIG_SOURCE_ROOT are the other ways to locate the SDK
  const home = join(scratch, 'pig-home');
  const result = spawnSync(pig, ['extension', 'init', join(scratch, 'probe'), '--json'], {
    encoding: 'utf8',
    env: { ...process.env, PIG_HOME: home, PIG_CODING_AGENT_DIR: join(scratch, 'agent'), GIT_TERMINAL_PROMPT: '0' },
  });
  if (result.status !== 0) throw new Error(`pig extension init failed: ${result.stderr}`);
  return JSON.parse(result.stdout).sdkPath;
}

function goExtensions() {
  const found = [];
  const components = join(root, 'components');
  for (const component of readdirSync(components, { withFileTypes: true })) {
    const extensions = join(components, component.name, 'extensions');
    if (!component.isDirectory() || !existsSync(extensions)) continue;
    for (const entry of readdirSync(extensions, { withFileTypes: true })) {
      const dir = join(extensions, entry.name);
      if (entry.isDirectory() && existsSync(join(dir, 'go.mod'))) found.push(dir);
    }
  }
  return found;
}

const scratch = mkdtempSync(join(tmpdir(), 'pigpen-go-'));
let status = 0;
try {
  const sdk = sdkDirectory(scratch);
  if (!existsSync(join(sdk, 'go.mod'))) throw new Error(`${sdk}: not a PiG Go SDK directory`);
  const extensions = goExtensions();
  if (!extensions.length) throw new Error('No Go extensions found under components/*/extensions/');
  const work = join(scratch, 'go.work');
  writeFileSync(work, `go 1.26\n\nuse (\n${[sdk, ...extensions].map(dir => `\t${JSON.stringify(dir)}\n`).join('')})\n`);
  for (const dir of extensions) {
    const result = spawnSync('go', ['test', '-count=1', ...process.argv.slice(2), './...'], {
      cwd: dir,
      stdio: 'inherit',
      env: { ...process.env, GOWORK: work, GOFLAGS: '-mod=readonly' },
    });
    if (result.error) throw result.error;
    if (result.status !== 0) status = result.status || 1;
  }
} catch (error) {
  console.error(error.message);
  status = 1;
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
process.exitCode = status;
