// Golden vectors for profile.go / profile_lock.go: the real langsmith 0.10.5 ProfileAuth (utils/profiles.js,
// utils/profile-lock.js) is run against a loopback server on scenario configs, with Date.now() pinned, and what it
// sends and writes is recorded. profile_differential_test.go replays the same scenarios against the Go port.
// Usage: node profile-differential.mjs   (writes extensions/langsmith/testdata/profile_differential.json)
// Env: LS_SDK_DIST (default /home/kinsy/ls-work/oracle-run/node_modules/langsmith/dist).
import http from "node:http";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { load, writeVectors } from "./wire-common.mjs";

const { loadProfileClientConfig } = await load("utils/profiles.js");

const T0 = 1_800_000_000_000;
const iso = (ms) => new Date(ms).toISOString();

// ---------------------------------------------------------------- scenario helpers
const B = "{{BASE}}";
const t = (d) => `{{T0${d >= 0 ? "+" : ""}${d}}}`; // an ISO timestamp relative to the pinned clock
const cfgText = (obj, indent = 4) => JSON.stringify(obj, null, indent) + "\n";
const oauthCfg = (oauth, extra = {}, profileName = "default") =>
  cfgText({ current_profile: profileName, profiles: { [profileName]: { api_url: B, workspace_id: "ws-1", oauth, ...extra } } });
const route = (method, p, status, body, opts = {}) => ({
  method, path: p, status,
  body: typeof body === "string" || body === undefined ? (body ?? "") : JSON.stringify(body),
  ...opts,
});
const tokenPath = "/oauth/token";
const tokOK = (over = {}) => route("POST", tokenPath, 200, { access_token: "NEW_AT", refresh_token: "NEW_RT", expires_in: 3600, ...over });
const meta = (issuer, tokenEndpoint, over = {}) => ({
  issuer,
  device_authorization_endpoint: `${issuer}/oauth/device`,
  token_endpoint: tokenEndpoint,
  ...over,
});
const WK = "/.well-known/oauth-authorization-server";
const expiredOAuth = { access_token: "OLD_AT", refresh_token: "OLD_RT", expires_at: t(-1000) };
const AUTH = [{ op: "current" }, { op: "auth" }, { op: "current" }];

const scenarios = [];
const add = (name, s) => scenarios.push({ name, ...s });

// ---- no refresh
add("fresh-token-no-refresh", { config: oauthCfg({ access_token: "AT", refresh_token: "RT", expires_at: t(3_600_000) }), ops: [...AUTH, { op: "is", value: "Bearer AT" }, { op: "is", value: "Bearer X" }] });
add("leeway-59999-refreshes", { config: oauthCfg({ access_token: "AT", refresh_token: "RT", expires_at: t(59_999) }), routes: [tokOK()], ops: AUTH });
add("leeway-60000-refreshes", { config: oauthCfg({ access_token: "AT", refresh_token: "RT", expires_at: t(60_000) }), routes: [tokOK()], ops: AUTH });
add("leeway-60001-no-refresh", { config: oauthCfg({ access_token: "AT", refresh_token: "RT", expires_at: t(60_001) }), routes: [tokOK()], ops: AUTH });
add("no-expires-at-no-refresh", { config: oauthCfg({ access_token: "AT", refresh_token: "RT" }), routes: [tokOK()], ops: AUTH });
add("no-access-token-refreshes", { config: oauthCfg({ refresh_token: "RT" }), routes: [tokOK()], ops: AUTH });
add("blank-access-token-refreshes", { config: oauthCfg({ access_token: "   ", refresh_token: "RT", expires_at: t(3_600_000) }), routes: [tokOK()], ops: AUTH });
add("no-refresh-token-no-refresh", { config: oauthCfg({ access_token: "AT", expires_at: t(-100000) }), routes: [tokOK()], ops: AUTH });
add("expired-refresh-success", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [...AUTH, { op: "is", value: "Bearer NEW_AT" }, { op: "is", value: "Bearer OLD_AT" }] });

// ---- token response shapes
add("response-no-rotation", { config: oauthCfg(expiredOAuth), routes: [tokOK({ refresh_token: undefined, expires_in: undefined })], ops: AUTH });
add("response-expires-in-zero", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 0 })], ops: AUTH });
add("response-expires-in-negative", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: -5 })], ops: AUTH });
add("response-expires-in-string", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: "3600" })], ops: AUTH });
add("response-expires-in-fraction", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 1.5 })], ops: AUTH });
add("response-expires-in-tiny", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 0.0001 })], ops: AUTH });
add("response-expires-in-year-33658", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 1e12 })], ops: AUTH });
add("response-expires-in-out-of-range", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 1e13 })], ops: AUTH });
add("response-expires-in-boundary", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 8.64e12 - T0 / 1000 })], ops: AUTH });
add("response-access-token-number", { config: oauthCfg(expiredOAuth), routes: [tokOK({ access_token: 12345 })], ops: AUTH });
add("response-access-token-empty", { config: oauthCfg(expiredOAuth), routes: [tokOK({ access_token: "" })], ops: AUTH });
add("response-access-token-null", { config: oauthCfg(expiredOAuth), routes: [tokOK({ access_token: null })], ops: AUTH });
add("response-error-body", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 200, { error: "invalid_grant" })], ops: AUTH });
add("response-refresh-token-only", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 200, { refresh_token: "X" })], ops: AUTH });
add("response-refresh-token-number", { config: oauthCfg(expiredOAuth), routes: [tokOK({ refresh_token: 7 })], ops: AUTH });
for (const [n, body] of [["null", "null"], ["array", "[]"], ["string", '"abc"'], ["number", "7"], ["html", "<html></html>"], ["empty", ""], ["truncated", '{"access_token":"A'] ]) {
  add(`response-${n}`, { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 200, body)], ops: AUTH });
}
add("response-bom", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 200, "", { bodyBase64: Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), Buffer.from('{"access_token":"BOM_AT"}')]).toString("base64") })], ops: AUTH });
add("response-invalid-utf8", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 200, "", { bodyBase64: Buffer.concat([Buffer.from('{"access_token":"A'), Buffer.from([0xe2, 0x82]), Buffer.from('B'), Buffer.from([0xff, 0xc3]), Buffer.from('"}')]).toString("base64") })], ops: AUTH });
add("response-escapes", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 200, '{"access_token":"A\\u00e9\\ud83d\\ude00\\ud800Z\\n\\"q\\"","refresh_token":"R\\/S"}')], ops: AUTH });
for (const status of [400, 401, 403, 404, 500, 503]) {
  add(`refresh-failure-${status}`, { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, status, { error: "x" })], ops: AUTH });
}
add("refresh-no-server-route", { config: oauthCfg(expiredOAuth), routes: [], ops: AUTH });
add("refresh-redirect-307", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 307, "", { headers: { location: "/oauth/token2" } }), route("POST", "/oauth/token2", 200, { access_token: "REDIR_AT", expires_in: 60 })], ops: AUTH });
add("refresh-redirect-302-post", { config: oauthCfg(expiredOAuth), routes: [route("POST", tokenPath, 302, "", { headers: { location: "/oauth/token2" } }), route("GET", "/oauth/token2", 200, { access_token: "REDIR_AT" }), route("POST", "/oauth/token2", 200, { access_token: "POSTED_AT" })], ops: AUTH });

// ---- request encoding
for (const [n, rt] of [["spaces-and-symbols", "a b+c&d=e~*'()!"], ["unicode", "é😀\u2028"], ["lone-surrogate", "x\ud800y"], ["percent", "100%/+"], ["quoted", "\"quoted\""]]) {
  add(`request-refresh-token-${n}`, { config: oauthCfg({ access_token: "A", refresh_token: rt, expires_at: t(-1) }), routes: [tokOK()], ops: [{ op: "auth" }] });
}

// ---- discovery
add("discovery-root-metadata", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, `${B}/custom/token`)), route("POST", "/custom/token", 200, { access_token: "DISC_AT" })], ops: AUTH });
add("discovery-api-inserted-form", { config: oauthCfg(expiredOAuth).replaceAll(`"${B}"`, `"${B}/api"`), routes: [route("GET", `${WK}/api`, 200, meta(`${B}/api`, `${B}/api/oauth/token`)), route("POST", "/api/oauth/token", 200, { access_token: "API_AT" })], ops: AUTH });
add("discovery-api-appended-form", { config: oauthCfg(expiredOAuth).replaceAll(`"${B}"`, `"${B}/api"`), routes: [route("GET", `/api${WK}`, 200, meta(`${B}/api`, `${B}/api/oauth/token`)), route("POST", "/api/oauth/token", 200, { access_token: "API_AT" })], ops: AUTH });
add("discovery-api-origin-candidate", { config: oauthCfg(expiredOAuth).replaceAll(`"${B}"`, `"${B}/api"`), routes: [route("GET", WK, 200, meta(B, `${B}/root/token`)), route("POST", "/root/token", 200, { access_token: "ORIGIN_AT" })], ops: AUTH });
add("discovery-origin-gets-api-candidate", { config: oauthCfg(expiredOAuth), routes: [route("GET", `${WK}/api`, 200, meta(`${B}/api`, `${B}/api/tok`)), route("POST", "/api/tok", 200, { access_token: "API_AT" })], ops: AUTH });
add("discovery-api-v1-suffix", { config: oauthCfg(expiredOAuth).replaceAll(`"${B}"`, `"${B}/api/v1/"`), routes: [route("GET", WK, 200, meta(B, `${B}/t`)), route("POST", "/t", 200, { access_token: "V1_AT" })], ops: AUTH });
add("discovery-trailing-slashes", { config: oauthCfg(expiredOAuth).replaceAll(`"${B}"`, `"${B}///"`), routes: [route("GET", WK, 200, meta(`${B}/`, `${B}/t`)), route("POST", "/t", 200, { access_token: "SL_AT" })], ops: AUTH });
add("discovery-quoted-api-url", { config: oauthCfg(expiredOAuth).replaceAll(`"${B}"`, `"'${B}'"`), routes: [tokOK()], ops: AUTH });
add("discovery-issuer-mismatch", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta("https://other.example", "https://other.example/token")), tokOK()], ops: AUTH });
add("discovery-endpoint-other-host", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, "http://evil.example/token")), tokOK()], ops: AUTH });
add("discovery-endpoint-other-scheme", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, "{{BASES}}/token")), tokOK()], ops: AUTH });
add("discovery-missing-device-endpoint", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, `${B}/t`, { device_authorization_endpoint: undefined })), tokOK()], ops: AUTH });
add("discovery-empty-token-endpoint", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, "")), tokOK()], ops: AUTH });
add("discovery-token-endpoint-number", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, 5)), tokOK()], ops: AUTH });
add("discovery-endpoint-relative", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, "/token")), tokOK()], ops: AUTH });
add("discovery-issuer-trailing-slash-trusted", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(`${B}/`, `${B}/t`)), route("POST", "/t", 200, { access_token: "T_AT" })], ops: AUTH });
add("discovery-html-200", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, "<html>spa</html>", { ct: "text/html" }), tokOK()], ops: AUTH });
add("discovery-500", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 500, meta(B, `${B}/t`)), tokOK()], ops: AUTH });
add("discovery-array-doc", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, "[]"), tokOK()], ops: AUTH });
add("discovery-null-doc", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, "null"), tokOK()], ops: AUTH });
add("discovery-redirect", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 302, "", { headers: { location: "/meta2" } }), route("GET", "/meta2", 200, meta(B, `${B}/t2`)), route("POST", "/t2", 200, { access_token: "M2_AT" })], ops: AUTH });
add("discovery-token-endpoint-query", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, `${B}/t?x=1 2#frag`)), route("POST", "/t", 200, { access_token: "Q_AT" })], ops: AUTH });
add("discovery-endpoint-credentials", { config: oauthCfg(expiredOAuth), routes: [route("GET", WK, 200, meta(B, "{{BASEUSER}}/t")), tokOK()], ops: AUTH });

// ---- profile resolution and rewrite shape
add("env-profile-selects", { env: { LANGSMITH_PROFILE: "work" }, config: cfgText({ current_profile: "default", profiles: { default: { api_key: "k-default" }, work: { api_url: B, oauth: expiredOAuth } } }), routes: [tokOK()], ops: AUTH });
add("env-profile-missing", { env: { LANGSMITH_PROFILE: "nope" }, config: cfgText({ current_profile: "default", profiles: { default: { api_key: "k" } } }), ops: AUTH });
add("current-profile-selects", { config: cfgText({ current_profile: "work", profiles: { default: { api_key: "k-default" }, work: { api_key: "k-work" } } }), ops: AUTH });
add("default-without-current", { config: cfgText({ profiles: { default: { api_key: "k-default" } } }), ops: AUTH });
add("no-default-no-current", { config: cfgText({ profiles: { other: { api_key: "k" } } }), ops: AUTH });
add("rewrite-keeps-other-profiles-and-order", { config: cfgText({ version: 2, profiles: { zeta: { api_key: "z" }, default: { workspace_id: "w", api_url: B, oauth: { scope: ["a", "b"], refresh_token: "RT", note: null, access_token: "AT", expires_at: t(-5) }, extra: { n: 1.50, e: 1e21, neg0: -0, big: 12345678901234567890, small: 1e-7, empty: {}, arr: [], nested: [[], {}, [1, [2]]] } }, alpha: { api_key: "a" } }, current_profile: "default", tail: true }), routes: [tokOK()], ops: AUTH });
add("rewrite-integer-keys-reorder", { env: { LANGSMITH_PROFILE: "2" }, config: cfgText({ current_profile: "x", profiles: { b: { api_key: "b" }, 2: { api_url: B, oauth: expiredOAuth }, a: {}, 1: { api_key: "one" }, "-1": "neg" } }), routes: [tokOK()], ops: AUTH });
add("rewrite-tab-indented-source", { config: JSON.stringify({ current_profile: "default", profiles: { default: { api_url: B, oauth: expiredOAuth } } }, null, "\t"), routes: [tokOK()], ops: AUTH });
add("rewrite-minified-source", { config: JSON.stringify({ current_profile: "default", profiles: { default: { api_url: B, oauth: expiredOAuth } } }), routes: [tokOK()], ops: AUTH });
add("rewrite-duplicate-keys", { config: `{"profiles":{"default":{"api_url":"${B}","oauth":{"access_token":"A","refresh_token":"R","expires_at":"${t(-1)}","access_token":"A2"}}},"current_profile":"default","profiles2":1,"profiles":{"default":{"api_url":"${B}","oauth":{"refresh_token":"R2","zz":1,"refresh_token":"R3"}}}}`, routes: [tokOK()], ops: AUTH });
add("rewrite-escapes-roundtrip", { config: `{"current_profile":"default","note":"\\u00e9\\ud83d\\ude00\\ud800\\udc00\\ud800x\\u2028\\u007f\\/\\"","profiles":{"default":{"api_url":"${B}","oauth":{"refresh_token":"R","name":"caf\\u00E9"}}}}`, routes: [tokOK()], ops: AUTH });
add("rewrite-oauth-created", { config: cfgText({ current_profile: "default", profiles: { default: { api_url: B, api_key: "KEY" } } }), routes: [], ops: AUTH });
add("rewrite-no-oauth-object-refresh-null", { config: cfgText({ current_profile: "default", profiles: { default: { api_url: B, oauth: null, api_key: "K" } } }), ops: AUTH });

// ---- header shapes
add("api-key-only", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: "  key-1  " } } }), ops: [...AUTH, { op: "is", value: "Bearer key-1" }] });
add("api-key-quoted", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: " 'k\"' " } } }), ops: AUTH });
add("api-key-and-access-token", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: "K", oauth: { access_token: "AT" } } } }), ops: AUTH });
add("api-key-with-refresh-token", { config: cfgText({ current_profile: "default", profiles: { default: { api_url: B, api_key: "K", oauth: { refresh_token: "RT" } } } }), routes: [tokOK()], ops: AUTH });
add("api-key-blank", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: "   ", workspace_id: "w" } } }), ops: AUTH });
add("quote-stripping-cases", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: '""' } } }), ops: AUTH });
add("access-token-number-type-error", { config: cfgText({ current_profile: "default", profiles: { default: { oauth: { access_token: 5 } } } }), ops: AUTH });
add("api-key-number-type-error", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: 5 } } }), ops: AUTH });
add("api-key-object-type-error", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: {} } } }), ops: AUTH });
add("api-key-false-type-error", { config: cfgText({ current_profile: "default", profiles: { default: { api_key: false } } }), ops: AUTH });
add("refresh-token-array-type-error", { config: cfgText({ current_profile: "default", profiles: { default: { oauth: { refresh_token: ["x"] } } } }), ops: AUTH });
add("api-url-null", { config: cfgText({ current_profile: "default", profiles: { default: { api_url: null, workspace_id: null, api_key: "K" } } }), ops: AUTH });
add("api-url-empty", { config: cfgText({ current_profile: "default", profiles: { default: { api_url: "", workspace_id: "", api_key: "K" } } }), ops: AUTH });
add("profile-not-object", { config: cfgText({ current_profile: "default", profiles: { default: "just a string" } }), ops: AUTH });
add("profile-falsy", { config: cfgText({ current_profile: "default", profiles: { default: 0 } }), ops: AUTH });
add("profile-true", { config: cfgText({ current_profile: "default", profiles: { default: true } }), ops: AUTH });
add("profile-proto-name", { env: { LANGSMITH_PROFILE: "constructor" }, config: cfgText({ current_profile: "default", profiles: { default: { api_key: "K" } } }), ops: AUTH });
add("profile-name-number-current", { config: cfgText({ current_profile: 7, profiles: { 7: { api_key: "seven" } } }), ops: AUTH });
add("profiles-array", { env: { LANGSMITH_PROFILE: "0" }, config: cfgText({ profiles: [{ api_key: "arr" }] }), ops: AUTH });
add("profiles-array-huge-index", { env: { LANGSMITH_PROFILE: "4294967294" }, config: cfgText({ profiles: [{ api_key: "arr" }] }), ops: AUTH });
add("profiles-array-index-out-of-range", { env: { LANGSMITH_PROFILE: "3" }, config: cfgText({ profiles: [{ api_key: "arr" }] }), ops: AUTH });
add("profiles-array-refresh-rewrites-element", { env: { LANGSMITH_PROFILE: "1" }, config: cfgText({ profiles: [{ api_key: "zero" }, { api_url: B, oauth: expiredOAuth }] }), routes: [tokOK()], ops: AUTH });
add("profiles-string", { env: { LANGSMITH_PROFILE: "0" }, config: cfgText({ profiles: "abc" }), ops: AUTH });
add("current-profile-object", { config: cfgText({ current_profile: {}, profiles: { "[object Object]": { api_key: "obj" } } }), ops: AUTH });
add("current-profile-array", { config: cfgText({ current_profile: ["a", "b"], profiles: { "a,b": { api_key: "arr" } } }), ops: AUTH });
add("current-profile-true", { config: cfgText({ current_profile: true, profiles: { true: { api_key: "t" } } }), ops: AUTH });
for (const [n, text] of [["empty-file", ""], ["null", "null"], ["array", "[]"], ["string", '"s"'], ["number", "5"], ["invalid", "{"], ["bom", "\ufeff" + cfgText({ current_profile: "default", profiles: { default: { api_key: "K" } } })], ["trailing-comma", '{"profiles":{"default":{"api_key":"K"},},}'], ["comment", '{"profiles":{"default":{"api_key":"K"}}} // x']]) {
  add(`config-${n}`, { config: text, ops: AUTH });
}
add("config-missing", { config: null, ops: AUTH });
add("config-is-directory", { configIsDir: true, ops: AUTH });
add("config-via-home", { useHome: true, config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: AUTH });
add("config-invalid-utf8", { configBase64: Buffer.concat([Buffer.from('{"current_profile":"default","profiles":{"default":{"api_key":"K'), Buffer.from([0xff, 0xe2, 0x82]), Buffer.from('"}}}')]).toString("base64"), ops: AUTH });

// ---- reload and concurrency
add("reload-picks-up-fresh-file", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: oauthCfg({ access_token: "OTHER_AT", refresh_token: "OTHER_RT", expires_at: t(3_600_000) }) }, { op: "auth" }, { op: "current" }] });
add("reload-still-expired-refreshes-with-new-token", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: oauthCfg({ access_token: "O", refresh_token: "ROTATED_RT", expires_at: t(-5) }) }, { op: "auth" }] });
add("reload-profile-removed-keeps-state", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: cfgText({ current_profile: "default", profiles: { other: {} } }) }, { op: "auth" }] });
add("reload-invalid-json-keeps-state", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: "{not json" }, { op: "auth" }] });
add("reload-config-not-object-keeps-state", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: "[]" }, { op: "auth" }] });
add("reload-profile-not-truthy-keeps-state", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: cfgText({ current_profile: "default", profiles: { default: 0 } }) }, { op: "auth" }] });
add("reload-file-deleted-keeps-state", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "delete" }, { op: "auth" }] });
add("reload-no-refresh-token-skips", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "write", text: oauthCfg({ access_token: "ONLY", expires_at: t(-5) }) }, { op: "auth" }] });
add("concurrent-callers-share-one-refresh", { config: oauthCfg(expiredOAuth), routes: [tokOK()], ops: [{ op: "authAll", n: 5 }, { op: "auth" }] });
add("second-refresh-after-first-expired", { config: oauthCfg(expiredOAuth), routes: [tokOK({ expires_in: 1 })], ops: [{ op: "auth" }, { op: "auth" }] });
add("write-failure-keeps-token-in-memory", { config: oauthCfg(expiredOAuth), tmpIsDir: true, routes: [tokOK()], ops: AUTH });

// ---- expires_at forms
for (const [n, v] of [["date-only", "2020-01-01"], ["garbage", "not a date"], ["empty", ""], ["number-zero", 0], ["number", 1000], ["number-future", T0 + 3_600_000], ["array-iso", [t(-1)]], ["array-empty", []], ["true", true], ["object", {}], ["no-ms-z", "2020-01-02T03:04:05Z"], ["offset", "2020-01-02T03:04:05.000+01:00"], ["local", "2020-01-02T03:04:05"], ["rfc2822", "Thu, 01 Jan 2020 00:00:00 GMT"], ["lowercase", "2020-01-02t03:04:05.000z"], ["far-future", "9999-12-31T23:59:59.999Z"], ["padded", "  2020-01-02T03:04:05.000Z  "]]) {
  add(`expires-at-${n}`, { config: oauthCfg({ access_token: "AT", refresh_token: "RT", expires_at: v }), routes: [tokOK()], ops: [{ op: "auth" }] });
}

// ---- lock handling (clock pinned: a lock younger than 10 s would wait out a deadline that never comes)
const lockDir = "config.json.oauth.lock.lock";
add("stale-lock-broken", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/created_at`]: `${t(-20_000)}\nsomeone-else\n` }, routes: [tokOK()], ops: AUTH });
add("stale-lock-padded-created-at", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/created_at`]: `  ${t(-10_001)}  \nx\n` }, routes: [tokOK()], ops: AUTH });
add("stale-lock-rfc-date", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/created_at`]: "Thu, 01 Jan 1970 00:00:00 GMT\nx\n" }, routes: [tokOK()], ops: AUTH });
add("stale-lock-garbage-falls-back-to-mtime", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/created_at`]: "garbage\nx\n" }, routes: [tokOK()], ops: AUTH });
add("stale-lock-empty-metadata-uses-mtime", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/created_at`]: "" }, routes: [tokOK()], ops: AUTH });
add("stale-lock-no-metadata-uses-mtime", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/other`]: "x" }, routes: [tokOK()], ops: AUTH });
add("lock-dir-leftover-files-removed", { config: oauthCfg(expiredOAuth), preFiles: { [`${lockDir}/created_at`]: `${t(-20_000)}\nsomeone\n`, [`${lockDir}/sub/file`]: "x" }, routes: [tokOK()], ops: AUTH });

// ---------------------------------------------------------------- runner
const dateTable = {}; // every text Date.parse saw -> its value (null: NaN)
const realParse = Date.parse;
Date.parse = (text) => {
  const r = realParse(text);
  dateTable[String(text)] = Number.isNaN(r) ? null : r;
  return r;
};
Date.now = () => T0;

const fill = (s, ctx) =>
  s
    .replaceAll("{{BASE}}", ctx.base)
    .replaceAll("{{BASES}}", ctx.base.replace("http:", "https:"))
    .replaceAll("{{BASEUSER}}", ctx.base.replace("//", "//u:p@"))
    .replaceAll("{{DIR}}", ctx.dir)
    .replace(/\{\{T0([+-]\d+)?\}\}/g, (_, d) => iso(T0 + (d ? Number(d) : 0)));
const norm = (s, ctx) => (typeof s === "string" ? s.replaceAll(ctx.base, "{{BASE}}").replaceAll(ctx.dir, "{{DIR}}") : s);
const errStr = (e) => `${e?.name}: ${e?.message}`;
const fillValue = (v, ctx) => (typeof v === "string" ? fill(v, ctx) : Array.isArray(v) ? v.map((x) => fillValue(x, ctx)) : v && typeof v === "object" ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, fillValue(x, ctx)])) : v);

function listFiles(dir, rel = "") {
  const out = [];
  for (const e of fs.readdirSync(path.join(dir, rel), { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    const r = rel ? `${rel}/${e.name}` : e.name;
    out.push(e.isDirectory() ? `${r}/` : r);
    if (e.isDirectory()) out.push(...listFiles(dir, r));
  }
  return out;
}

async function runScenario(sc) {
  const dir = fs.mkdtempSync(path.join(process.env.LS_SCRATCH || os.tmpdir(), "profile-diff-"));
  const requests = [];
  const server = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", (c) => chunks.push(c));
    req.on("end", () => {
      const u = new URL(req.url, "http://x");
      requests.push({ method: req.method, path: req.url, accept: req.headers.accept ?? null, contentType: req.headers["content-type"] ?? null, body: Buffer.concat(chunks).toString("utf8") });
      const r = (sc.routes ?? []).find((x) => x.method === req.method && fill(x.path, ctx) === u.pathname);
      if (!r) {
        res.writeHead(404);
        return res.end();
      }
      const body = r.bodyBase64 ? Buffer.from(r.bodyBase64, "base64") : Buffer.from(fill(r.body, ctx));
      res.writeHead(r.status, { "content-type": r.ct ?? "application/json", "content-length": body.length, ...Object.fromEntries(Object.entries(r.headers ?? {}).map(([k, v]) => [k, fill(v, ctx)])) });
      res.end(body);
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const ctx = { base: `http://127.0.0.1:${server.address().port}`, dir };
  const configRel = sc.useHome ? ".langsmith/config.json" : "config.json";
  const configPath = path.join(dir, configRel);
  fs.mkdirSync(path.dirname(configPath), { recursive: true });
  if (sc.configIsDir) fs.mkdirSync(configPath);
  else if (sc.configBase64) fs.writeFileSync(configPath, Buffer.from(sc.configBase64, "base64"));
  else if (sc.config !== null && sc.config !== undefined) fs.writeFileSync(configPath, fill(sc.config, ctx));
  for (const [rel, text] of Object.entries(sc.preFiles ?? {})) {
    fs.mkdirSync(path.dirname(path.join(dir, rel)), { recursive: true });
    fs.writeFileSync(path.join(dir, rel), fill(text, ctx));
  }
  if (sc.tmpIsDir) fs.mkdirSync(configPath + ".tmp"); // writeFileAtomic cannot create its temp file
  for (const k of ["LANGSMITH_CONFIG_FILE", "LANGSMITH_PROFILE", "HOME", "USERPROFILE"]) delete process.env[k];
  if (sc.useHome) process.env.HOME = dir;
  else process.env.LANGSMITH_CONFIG_FILE = configPath;
  for (const [k, v] of Object.entries(sc.env ?? {})) process.env[k] = fill(v, ctx);

  const rec = { name: sc.name, load: null, ops: [], requests, config: null, files: [] };
  let auth;
  try {
    const cfg = loadProfileClientConfig();
    rec.load = {};
    for (const k of ["apiUrl", "apiKey", "workspaceId", "oauthAccessToken", "oauthRefreshToken"]) if (cfg[k] !== undefined) rec.load[k] = norm(cfg[k], ctx);
    rec.load.hasAuth = cfg.profileAuth !== undefined;
    auth = cfg.profileAuth;
  } catch (e) {
    rec.load = { error: errStr(e) };
  }
  const hdr = (h) => (h ? { name: h.name, value: norm(h.value, ctx) } : null);
  if (auth) {
    for (const op of sc.ops ?? []) {
      try {
        if (op.op === "current") rec.ops.push({ op: "current", result: hdr(auth.currentAuthHeader()) });
        else if (op.op === "auth") rec.ops.push({ op: "auth", result: hdr(await auth.getAuthHeader(fetch)) });
        else if (op.op === "authAll") rec.ops.push({ op: "authAll", result: (await Promise.all(Array.from({ length: op.n }, () => auth.getAuthHeader(fetch)))).map(hdr) });
        else if (op.op === "is") rec.ops.push({ op: "is", value: op.value, result: auth.isProfileAuthorizationHeader(op.value) });
        else if (op.op === "write") fs.writeFileSync(configPath, fill(op.text, ctx));
        else if (op.op === "delete") fs.rmSync(configPath);
      } catch (e) {
        rec.ops.push({ op: op.op, error: errStr(e) });
      }
    }
  }
  await new Promise((r) => setTimeout(r, 20));
  try {
    rec.config = norm(fs.readFileSync(configPath, "utf8"), ctx);
  } catch {
    rec.config = null;
  }
  rec.files = listFiles(dir);
  rec.requests = requests.map((q) => ({ ...q, body: norm(q.body, ctx) }));
  server.closeAllConnections();
  await new Promise((r) => server.close(r));
  fs.rmSync(dir, { recursive: true, force: true });
  return rec;
}

const results = [];
for (const sc of scenarios) results.push(await runScenario(sc));

writeVectors("profile_differential.json", { t0: T0, dateTable, scenarios, results });
