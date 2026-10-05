/**
 * Shadow of node:fs with sync-op counting + latency injection (B1/B2).
 *
 * Resolves via bench-adapter-hooks.mjs registerHooks. Uses createRequire to
 * reach the REAL fs module (avoiding infinite hook recursion), then re-exports
 * every member with the ten sync ops the extension uses patched to count and
 * (optionally) sleep under injected latency.
 */

import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const real = require("fs");

import { state, sleepMs } from "./guard-state.mjs";

function wrapSync(name) {
	const original = real[name];
	if (typeof original !== "function") return original;
	return function patched(...args) {
		state.fsOpCount++;
		if (state.trace) state.trace.push(name + ":" + String(args[0] ?? "").split("/").slice(-2).join("/"));
		if (state.latencyMs > 0) sleepMs(state.latencyMs);
		return original.apply(this, args);
	};
}

const SYNC_OPS = [
	"readFileSync",
	"writeFileSync",
	"appendFileSync",
	"readdirSync",
	"statSync",
	"lstatSync",
	"existsSync",
	"mkdirSync",
	"renameSync",
	"unlinkSync",
	"openSync", "closeSync", "readSync", "writeSync", "fsyncSync",
	// used by harness fixture setup only:
	"mkdtempSync",
	"rmSync",
];

export const readFileSync = wrapSync("readFileSync");
export const writeFileSync = wrapSync("writeFileSync");
export const appendFileSync = wrapSync("appendFileSync");
export const readdirSync = wrapSync("readdirSync");
export const statSync = wrapSync("statSync");
export const lstatSync = wrapSync("lstatSync");
export const existsSync = wrapSync("existsSync");
export const mkdirSync = wrapSync("mkdirSync");
export const renameSync = wrapSync("renameSync");
export const unlinkSync = wrapSync("unlinkSync");
export const mkdtempSync = wrapSync("mkdtempSync");
export const rmSync = wrapSync("rmSync");

// Async promises API: wrapped for counting + latency injection too (P1-7's
// parallel startup reader uses fs.promises; without wrapping, injected
// latency would silently bypass the measured path).
function wrapPromiseMethod(name, original) {
	return async function patchedAsync(...args) {
		state.fsOpCount++;
		if (state.latencyMs > 0) {
			await new Promise((resolve) => setTimeout(resolve, state.latencyMs));
		}
		return original.apply(this, args);
	};
}
const wrappedPromises = {};
for (const key of Object.keys(real.promises)) {
	const member = real.promises[key];
	wrappedPromises[key] = typeof member === "function" ? wrapPromiseMethod(key, member) : member;
}
export const promises = wrappedPromises;
export const constants = real.constants;
export const watch = real.watch;
export const watchFile = real.watchFile;
export const unwatchFile = real.unwatchFile;
export const openSync = wrapSync("openSync");
export const closeSync = wrapSync("closeSync");
export const readSync = wrapSync("readSync");
export const writeSync = wrapSync("writeSync");
export const fsyncSync = wrapSync("fsyncSync");
export const readFile = real.readFile;
export const writeFile = real.writeFile;
export const appendFile = real.appendFile;
export const accessSync = real.accessSync;
export const realpathSync = real.realpathSync;
export const readlinkSync = real.readlinkSync;
export const copyFileSync = real.copyFileSync;
export const truncateSync = real.truncateSync;
export const chmodSync = real.chmodSync;
export const chownSync = real.chownSync;
export const utimesSync = real.utimesSync;
export const readdir = real.readdir;
export const stat = real.stat;
export const lstat = real.lstat;
export const exists = real.exists;
export const mkdir = real.mkdir;
export const rm = real.rm;
export const createReadStream = real.createReadStream;
export const createWriteStream = real.createWriteStream;

export default real;
