/**
 * Shadow of node:net (B8 enforcement). Any socket/server construction or
 * connection attempt in a benchmark throws; everything else passes through.
 */

import { createRequire } from "node:module";
import { recordViolation } from "./guard-state.mjs";

const real = createRequire(import.meta.url)("net");

function forbid(name) {
	return function (..._args) {
		recordViolation("network", `node:net.${name} called in a benchmark`);
	};
}

export const connect = forbid("connect");
export const createConnection = forbid("createConnection");
export const createServer = forbid("createServer");
export const Socket = forbid("Socket");
export const Server = forbid("Server");
export const BlockList = real.BlockList;
export const isIP = real.isIP;
export const isIPv4 = real.isIPv4;
export const isIPv6 = real.isIPv6;
export const getDefaultAutoSelectFamily = real.getDefaultAutoSelectFamily;
export const setDefaultAutoSelectFamily = real.setDefaultAutoSelectFamily;
export const setDefaultAutoSelectFamilyAttemptTimeout = real.setDefaultAutoSelectFamilyAttemptTimeout;

export default real;
