/**
 * Shadows of node:net / node:http / node:https (B8 enforcement).
 * Any socket/request in a benchmark is a violation: benchmarks never touch
 * the network.
 */

import { createRequire } from "node:module";
import { recordViolation } from "./guard-state.mjs";

function makeShadow(builtin, forbiddenKeys) {
	const require = createRequire(import.meta.url);
	const real = require(builtin);
	const out = {};
	for (const key of Object.keys(real)) {
		if (forbiddenKeys.has(key)) {
			out[key] = function (..._args) {
				recordViolation("network", `${builtin}.${key} called in a benchmark`);
			};
		} else {
			out[key] = real[key];
		}
	}
	return out;
}

const NET_FORBIDDEN = new Set(["connect", "createConnection", "createServer", "Socket", "Server", "BlockList"]);
const HTTP_FORBIDDEN = new Set(["request", "get", "createServer", "Agent"]);
const HTTPS_FORBIDDEN = new Set(["request", "get", "createServer", "Agent"]);

export const net = makeShadow("net", NET_FORBIDDEN);
export const http = makeShadow("http", HTTP_FORBIDDEN);
export const https = makeShadow("https", HTTPS_FORBIDDEN);

export default net;
