/**
 * Shadow of node:http (B8 enforcement). Any request/get/server/agent in a
 * benchmark throws; constants and header helpers pass through.
 */

import { createRequire } from "node:module";
import { recordViolation } from "./guard-state.mjs";

const real = createRequire(import.meta.url)("http");

function forbid(name) {
	return function (..._args) {
		recordViolation("network", `node:http.${name} called in a benchmark`);
	};
}

export const request = forbid("request");
export const get = forbid("get");
export const createServer = forbid("createServer");
export const Agent = forbid("Agent");
export const METHODS = real.METHODS;
export const STATUS_CODES = real.STATUS_CODES;
export const maxHeaderSize = real.maxHeaderSize;
export const globalAgent = real.globalAgent;
export const validateHeaderName = real.validateHeaderName;
export const validateHeaderValue = real.validateHeaderValue;
export const setMaxIdleHTTPParsers = real.setMaxIdleHTTPParsers;

export default real;
