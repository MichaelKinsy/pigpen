// A stand-in for mpv in the end-to-end runs: `--version` prints a version, and started with `--input-ipc-server=<path>` it
// listens there and answers every JSON IPC request with success, so pig-music can attach and show its player. It plays
// nothing. Run by a shebang line that names the absolute Node of the test (the Binary under test needs no Node; this
// script is test scaffolding only).
import { createServer } from 'node:net';
import { rmSync } from 'node:fs';

const args = process.argv.slice(2);
if (args.includes('--version')) {
  console.log('mpv 0.38.0 Copyright (c) 2000-2024 mpv/MPlayer/mplayer2 projects');
  process.exit(0);
}
const socket = args.map((arg) => /^--input-ipc-server=(.+)$/.exec(arg)?.[1]).find(Boolean);
if (!socket) process.exit(0);
rmSync(socket, { force: true });
const server = createServer((connection) => {
  let pending = '';
  connection.setEncoding('utf8');
  connection.on('data', (chunk) => {
    pending += chunk;
    for (let index = pending.indexOf('\n'); index >= 0; index = pending.indexOf('\n')) {
      const line = pending.slice(0, index);
      pending = pending.slice(index + 1);
      if (!line.trim()) continue;
      let request;
      try { request = JSON.parse(line); } catch { continue; }
      const [name, property] = request.command ?? [];
      const data = name === 'get_property' && property === 'playlist-pos' ? -1 : null;
      connection.write(`${JSON.stringify({ request_id: request.request_id, error: 'success', data })}\n`);
    }
  });
  connection.on('error', () => {});
});
server.listen(socket);
process.on('SIGTERM', () => { server.close(); rmSync(socket, { force: true }); process.exit(0); });
