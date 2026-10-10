// A real terminal for the Piglet Binary end-to-end runs: one tmux session per Binary, driven with the keys a user types
// and read back from the screen. Everything is state-based (wait for the text a step produces), never a fixed sleep.
// Cleanup kills only the session this object created, by its exact name.
import { spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

let counter = 0;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// A tmux server of its own (-L), so a run never touches the caller's sessions; it exits with its last session.
const socket = `pigpen-e2e-${process.pid}`;
const configFile = join(tmpdir(), `${socket}.conf`);
writeFileSync(configFile, 'set -g extended-keys on\nset -g escape-time 0\nset -g extended-keys-format csi-u\n');
process.on('exit', () => rmSync(configFile, { force: true }));

const tmux = (...args) => {
  const result = spawnSync('tmux', ['-L', socket, '-f', configFile, ...args], { encoding: 'utf8' });
  if (result.error) throw result.error;
  return result;
};

/** POSIX shell quoting for one word. */
export const shellQuote = (value) => `'${String(value).replaceAll("'", `'\\''`)}'`;

/**
 * The PATH a Binary runs with: one directory under `home` holding links to a few system tools and nothing else. No Node,
 * no Go, no package manager: a Piglet Binary of Go extensions has to run with nothing else installed, which is the claim
 * these runs check. (`/usr/bin` alone is not enough: a machine may keep Go or Node there.)
 */
export const isolatedPath = (home) => join(home, 'sysbin');

/** The system tools a Binary may call by name: a shell, git, and the coreutils a `bash` tool or a hook would use. */
export const SYSTEM_TOOLS = ['sh', 'bash', 'env', 'git', 'ls', 'cat', 'echo', 'sleep', 'true', 'false', 'uname', 'mkdir', 'rm', 'cp', 'mv', 'head', 'tail', 'tr', 'sed', 'grep', 'sort', 'wc', 'dirname', 'basename', 'pwd', 'date', 'printf'];

/** Create `isolatedPath(home)` with a link to each tool in SYSTEM_TOOLS that exists in /usr/bin or /bin. */
export function makeSystemBin(home, tools = SYSTEM_TOOLS) {
  const dir = isolatedPath(home);
  mkdirSync(dir, { recursive: true });
  for (const tool of tools) {
    for (const from of ['/usr/bin', '/bin']) {
      if (existsSync(join(from, tool))) { symlinkSync(join(from, tool), join(dir, tool)); break; }
    }
  }
  return dir;
}

/** The clean environment of one run: nothing of the caller's PIG_*, PI_* or credentials, all state under `home`. */
export function e2eEnv(home, extra = {}) {
  return {
    PATH: isolatedPath(home), LANG: 'C.UTF-8', TERM: 'xterm-256color', HOME: home,
    PIG_HOME: join(home, 'pig'), PIG_CODING_AGENT_DIR: join(home, 'agent'), PI_CODING_AGENT_DIR: join(home, 'pi'),
    XDG_RUNTIME_DIR: join(home, 'run'), XDG_CACHE_HOME: join(home, 'cache'), XDG_CONFIG_HOME: join(home, 'config'), XDG_DATA_HOME: join(home, 'data'), XDG_STATE_HOME: join(home, 'state'),
    TMPDIR: join(home, 'tmp'), PIG_TEST_FAUX: '1', PIG_OFFLINE: '1', PI_SKIP_VERSION_CHECK: '1', GIT_TERMINAL_PROMPT: '0',
    ...extra,
  };
}

export class Terminal {
  /**
   * @param {string} bin       the Piglet Binary (or any pig)
   * @param {{args?: string[], env?: Record<string,string>, cols?: number, rows?: number, files?: Record<string,string>, prepare?: (home: string) => Record<string,string>}} options
   */
  constructor(bin, { args = ['--model', 'test-faux/faux-1', '--no-session'], env = {}, cols = 120, rows = 40, files = {}, prepare } = {}) {
    this.name = `pigpen-e2e-${process.pid}-${++counter}`;
    this.home = mkdtempSync(join(tmpdir(), 'pigpen-e2e-'));
    this.work = join(this.home, 'work');
    for (const dir of ['tmp', 'work', 'agent', 'pig', 'run', 'cache', 'config', 'data', 'state']) mkdirSync(join(this.home, dir), { recursive: true, mode: 0o700 });
    for (const [file, text] of Object.entries(files)) {
      mkdirSync(join(this.work, file, '..'), { recursive: true });
      writeFileSync(join(this.work, file), text);
    }
    makeSystemBin(this.home);
    const prepared = prepare ? prepare(this.home) : {};
    const exports = Object.entries(e2eEnv(this.home, { ...prepared, ...env })).map(([k, v]) => `export ${k}=${shellQuote(v)}`);
    const script = join(this.home, 'run.sh');
    // The exit status is written next to the session so a run can tell a clean /quit from a crash after the pane is gone.
    writeFileSync(script, `#!/bin/sh\n${exports.join('\n')}\ncd ${shellQuote(this.work)}\n${shellQuote(bin)} ${args.map(shellQuote).join(' ')}\nprintf '%s' "$?" > ${shellQuote(join(this.home, 'exit'))}\n`);
    chmodSync(script, 0o755);
    const started = tmux('new-session', '-d', '-s', this.name, '-x', String(cols), '-y', String(rows), script);
    if (started.status !== 0) throw new Error(`tmux new-session failed: ${started.stderr}`);
  }

  /** The visible screen. */
  screen() {
    const result = tmux('capture-pane', '-p', '-t', this.name);
    return result.status === 0 ? result.stdout : '';
  }

  /** True while the session still exists (the Binary has not exited). */
  alive() {
    return tmux('has-session', '-t', this.name).status === 0;
  }

  /** The Binary's exit status once it has exited, else undefined. */
  exitStatus() {
    const result = spawnSync('cat', [join(this.home, 'exit')], { encoding: 'utf8' });
    return result.status === 0 && result.stdout !== '' ? Number(result.stdout) : undefined;
  }

  /** Type literal text without pressing Enter. */
  type(text) {
    tmux('send-keys', '-t', this.name, '-l', text);
  }

  /** Press named keys (tmux names: Enter, Escape, C-c, Down, ...). */
  key(...names) {
    tmux('send-keys', '-t', this.name, ...names);
  }

  /** Type a line and press Enter. */
  async say(text) {
    this.type(text);
    await sleep(60); // let the editor take the burst before Enter; the editor handles the keys in order, this only avoids a stale popup race
    this.key('Enter');
  }

  /** Wait until `predicate(screenText)` is true; throws with the screen on timeout. */
  async until(what, predicate, timeoutMs = 30_000) {
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      const text = this.screen();
      if (predicate(text)) return text;
      if (Date.now() > deadline) throw new Error(`timed out after ${timeoutMs} ms waiting for ${what}\n--- screen ---\n${text}\n--- end ---`);
      await sleep(50);
    }
  }

  /** Wait for text (string or RegExp) to appear on the screen. */
  waitFor(needle, timeoutMs) {
    const test = needle instanceof RegExp ? (text) => needle.test(text) : (text) => text.includes(needle);
    return this.until(String(needle), test, timeoutMs);
  }

  /** Wait for text to leave the screen. */
  waitGone(needle, timeoutMs) {
    const test = needle instanceof RegExp ? (text) => !needle.test(text) : (text) => !text.includes(needle);
    return this.until(`${needle} to disappear`, test, timeoutMs);
  }

  /** Wait for the Binary to exit, returning its status. */
  async waitExit(timeoutMs = 20_000) {
    const deadline = Date.now() + timeoutMs;
    while (this.alive()) {
      if (Date.now() > deadline) throw new Error(`the Binary did not exit within ${timeoutMs} ms\n--- screen ---\n${this.screen()}`);
      await sleep(50);
    }
    return this.exitStatus();
  }

  /** Kill this session (never another) and remove its home. */
  close() {
    if (this.alive()) tmux('kill-session', '-t', this.name);
    rmSync(this.home, { recursive: true, force: true });
  }
}

/** The editor is ready when PiG's input box and footer are drawn. */
export const promptReady = (text) => /─{20,}[\s\S]*─{20,}[\s\S]*faux-1/.test(text);
