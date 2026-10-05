/**
 * Shadow of node:https (B8 enforcement). Any request/get/server/agent in a
 * benchmark throws; constants pass through.
 */

import { createRequire } from "node:module";
import { recordViolation } from "./guard-state.mjs";

const real = createRequire(import.meta.url)("https");

function forbid(name) {
	return function (..._args) {
		recordViolation("network", `node:https.${name} called in a benchmark`);
	};
}

export const request = forbid("request");
export const get = forbid("get");
export const createServer = forbid("createServer");
export const Agent = forbid("Agent");
export const globalAgent = real.globalAgent;
export const STATUS_CODES = real.STATUS_CODES;

export default real;
