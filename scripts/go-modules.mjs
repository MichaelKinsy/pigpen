// Shared helpers for scripts that build or test Pigpen's Go extension modules.
// PiG replaces an extension's SDK requirement with its staged SDK at build time, so the
// tests map that same SDK instead of the published module. These helpers write a temporary go.work that maps the SDK
// staged by the selected PiG binary, without touching the repository.
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { requirePig } from './pig-bin.mjs';

const scratchDirs = new Set();
process.on('exit', () => { for (const dir of scratchDirs) rmSync(dir, { recursive: true, force: true }); });

/** A private directory under TMPDIR that is removed when this process exits (a go.work, a staged SDK, a built tool). */
export function scratchDir(prefix) {
  const dir = mkdtempSync(join(tmpdir(), prefix));
  scratchDirs.add(dir);
  return dir;
}

export const root = fileURLToPath(new URL('../', import.meta.url));

/** Every Go module: a component's own root go.mod (a shared library Package), and each components/<name>/extensions/<dir> and piglets/<name>/extensions/<dir>. */
export function goModules(directory = root) {
  const found = [];
  for (const kind of ['components', 'piglets']) {
    const base = join(directory, kind);
    if (!existsSync(base)) continue;
    for (const owner of readdirSync(base, { withFileTypes: true })) {
      if (!owner.isDirectory()) continue;
      // A component may be a shared Go library module rooted at its Package directory (libraries/*, assets).
      if (existsSync(join(base, owner.name, 'go.mod'))) found.push(join(base, owner.name));
      const extensions = join(base, owner.name, 'extensions');
      if (!existsSync(extensions)) continue;
      for (const entry of readdirSync(extensions, { withFileTypes: true })) {
        const dir = join(extensions, entry.name);
        if (!entry.isDirectory() || !existsSync(join(dir, 'go.mod'))) continue;
        found.push(dir);
        // Commands beside a factory are modules of their own (PiG rejects a factory and a main package in one module).
        const commands = join(dir, 'cmd');
        if (existsSync(commands)) {
          for (const command of readdirSync(commands, { withFileTypes: true })) {
            if (command.isDirectory() && existsSync(join(commands, command.name, 'go.mod'))) found.push(join(commands, command.name));
          }
        }
      }
    }
  }
  return found.sort();
}

let caches;
/**
 * Go's build and module caches as this process sees them. A test that runs pig under an isolated HOME (in a tmux
 * pane, or a child build) must pass these on explicitly: Go's defaults live under HOME, and a tmux server that
 * is already running hands its panes its own environment, not the test's.
 */
export function goCaches() {
  if (!caches) {
    const [GOCACHE, GOMODCACHE] = execFileSync('go', ['env', 'GOCACHE', 'GOMODCACHE'], { encoding: 'utf8' }).trim().split('\n');
    caches = { GOCACHE, GOMODCACHE };
  }
  return caches;
}

/** The SDK directory: PIG_SDK_DIR, else `$PIG_BIN reload --sdk-path` in a scratch PIG_HOME. */
export function sdkDir(env = process.env) {
  if (env.PIG_SDK_DIR) return resolve(env.PIG_SDK_DIR);
  const pig = requirePig(env); // PIG_SDK_DIR is the other way to name the SDK
  const home = scratchDir('pigpen-sdk-');
  const out = execFileSync(pig, ['reload', '--sdk-path'], {
    encoding: 'utf8', env: { ...env, PIG_HOME: home, PIG_CODING_AGENT_DIR: join(home, 'agent') },
  });
  return out.trim().split('\n').at(-1);
}

/** The highest `go` line among the modules (at least 1.26): `go 1.26` sorts below a dependency's `go 1.26.0`. */
export function goDirective(dirs) {
  const parts = (v) => { const [a = 0, b = 0, c = -1] = v.split('.').map(Number); return [a, b, c]; };
  const less = (x, y) => { const px = parts(x), py = parts(y); for (let i = 0; i < 3; i++) if (px[i] !== py[i]) return px[i] < py[i]; return false; };
  let highest = '1.26';
  for (const dir of dirs) {
    const file = join(dir, 'go.mod');
    const m = existsSync(file) && /^go\s+([0-9][0-9.]*)\s*$/m.exec(readFileSync(file, 'utf8'));
    if (m && less(highest, m[1])) highest = m[1];
  }
  return highest;
}

const sdkModule = 'github.com/MichaelKinsy/PiG/extensions/sdk';

/**
 * The go.work text (mirrors eq.GoWorkFor): every module is used, the SDK is a replace (a module with a
 * third-party dependency cannot resolve sdk@v0.0.0 through `use`), and every used module also gets a versioned
 * replace to its own directory (with the SDK replaced, a `use`d library Package that another module requires at
 * v0.0.0 is otherwise not resolved: "unknown revision components/<lib>/v0.0.0").
 */
export function goWorkText(sdk, modules) {
  const path = (dir) => { const f = join(dir, 'go.mod'); return existsSync(f) ? /^module\s+(\S+)/m.exec(readFileSync(f, 'utf8'))?.[1] : undefined; };
  const replaces = modules.flatMap((dir) => (path(dir) ? [`replace ${path(dir)} v0.0.0 => ${dir}\n`] : [])).join('');
  return `go ${goDirective([sdk, ...modules])}\n\nuse (\n\t${modules.join('\n\t')}\n)\n\nreplace ${sdkModule} => ${sdk}\n${replaces}`;
}

/**
 * The Go build tags the selected SDK supports: `pigsdk_tool_renderer` when it has tool renderers (D89,
 * `Extension.ToolRenderer`, in the PiG 0.4.1 content and later, not in v0.4.0). A test that needs such an API
 * sits behind the tag, so it runs on an SDK that has it and is not compiled on one that does not.
 */
export function sdkBuildTags(sdk) {
  const tags = [];
  if (!existsSync(sdk)) return tags;
  const has = (pattern) => readdirSync(sdk).some((file) => file.endsWith('.go') && !file.endsWith('_test.go') && pattern.test(readFileSync(join(sdk, file), 'utf8')));
  if (has(/^func \(\w+ \*Extension\) ToolRenderer\(/m)) tags.push('pigsdk_tool_renderer');
  return tags;
}

/**
 * Whether a module's source registers a native Provider (`RegisterNativeProvider`). `pig install --validate-only` loads an
 * extension in a host with no native provider registry bound (PiG 0.4.0 and 0.4.1 fail it with "native provider registry
 * is not bound"), so such a module is load-checked in a real pig instead (scripts/validate-manifests.mjs).
 */
export function registersNativeProvider(dir) {
  if (!existsSync(dir)) return false;
  return readdirSync(dir).some((file) => file.endsWith('.go') && !file.endsWith('_test.go') && /\.RegisterNativeProvider\(/.test(readFileSync(join(dir, file), 'utf8')));
}

/** Write a go.work mapping the SDK and every module; returns the environment to use. */
export function goWorkEnv(modules = goModules(), env = process.env) {
  const dir = scratchDir('pigpen-gowork-');
  const work = join(dir, 'go.work');
  const sdk = sdkDir(env);
  writeFileSync(work, goWorkText(sdk, modules));
  const tags = sdkBuildTags(sdk);
  return { ...env, GOWORK: work, GOFLAGS: tags.length ? `-tags=${tags.join(',')}` : '' };
}
