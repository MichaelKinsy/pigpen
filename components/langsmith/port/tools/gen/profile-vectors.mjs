// Golden vectors for the profile lane primitives, produced by the real langsmith 0.10.5 / Node 24 code:
// JSON.parse + JSON.stringify(v, null, 2) round trips, `new URL(...)`, resolveTokenEndpoint's discovery requests and
// trust decisions, URLSearchParams encoding, hasValue and the trim of loadProfileClientConfig.
// Usage: node profile-vectors.mjs   (writes extensions/langsmith/testdata/profile_vectors.json)
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { load, writeVectors, xorshift32 } from "./wire-common.mjs";

const { hasValue, loadProfileClientConfig, resolveTokenEndpoint } = await load("utils/profiles.js");
const rng = xorshift32(20261106);
const pick = (a) => a[rng() % a.length];

// ---------------------------------------------------------------- JSON round trips
const jsonTexts = [
  "", " ", "{", "}", "[", "[1,]", '{"a":1,}', "01", "-", "-0", "1.", ".5", "1e", "1e+", "+1", "NaN", "Infinity", "'a'", '"\\x"', '"\t"', '"\n"', "[1] x", "nul", "true false",
  "\u00a01", "\ufeff{}", "{}\u2028", "\u2028{}", "{}\n", "\t\r\n {} \r\n", "{ }", "[ ]", '{"a" : 1 , "b" : [ ] }', '{"a":{"b":{"c":{}}}}', "[[],[[]],[{}]]",
  "1", "-1", "1.5", "1e21", "1e-7", "1E5", "123456789012345678901234567890", "0.1", "-0", "0e0", "5e-324", "1e400", "-1e400", "1e-400", "9007199254740993",
  "true", "false", "null", '"s"', '"\\u0041\\u00e9\\ud83d\\ude00"', '"\\ud800"', '"\\udc00"', '"\\ud800\\ud800"', '"\\udc00\\ud800"', '"\\ud83d\\u0041"', '"\\u"', '"\\u12"', '"\\uZZZZ"',
  '"\\/\\b\\f\\n\\r\\t\\"\\\\"', '"\u2028\u2029"', '"\u007f\u0080"', '"\\u0000"', '"\\u001f"', '"\u001f"',
  '{"b":1,"a":2}', '{"2":1,"1":2,"a":3,"01":4,"-1":5,"4294967295":6,"4294967294":7,"1.5":8}', '{"a":1,"a":2}', '{"a":1,"b":2,"a":3}', '{"__proto__":1,"x":2}', '{"__proto__":{"y":1}}',
  '{"constructor":1,"toString":2,"hasOwnProperty":3}', '{"":1}', '{"\\u0061":1,"a":2}', '{"é":1,"e":2,"😀":3,"\\ud800":4}',
  '[1,"a",null,true,{"x":[]}]', '{"a":[1,2,{"b":[3,[4,[5]]]}],"c":{}}', '{"k":"v"} ', '{"a":1}{', '{"a":1}}', '{"a"1}', '{"a":}', '{a:1}', '["a" "b"]', '[,1]', '{,}',
  "[" .repeat(50) + "]".repeat(50), '{"a":'.repeat(30) + "1" + "}".repeat(30),
];
function randJSON(depth) {
  const k = rng() % (depth > 3 ? 5 : 8);
  switch (k) {
    case 0: return null;
    case 1: return rng() % 2 === 0;
    case 2: return [0, 1, -1, 0.5, 1e21, 1e-7, 123456789, -2.5e-10, 1.7976931348623157e308, 4294967296][rng() % 10];
    case 3: case 4: return pick(["", "a", "é", "😀", "a\u2028b", "\ud800", "x\udc00y", "q\"uote", "back\\slash", "\u0001ctl", "tab\there", "/"]) + (rng() % 2 ? String(rng() % 1000) : "");
    case 5: case 6: { const n = rng() % 4; return Array.from({ length: n }, () => randJSON(depth + 1)); }
    default: {
      const n = rng() % 5;
      const o = {};
      for (let i = 0; i < n; i++) o[pick(["a", "b", "z", "0", "7", "10", "k1", "é", "😀", "", "constructor", "-1", "01"]) + (rng() % 3 === 0 ? String(rng() % 20) : "")] = randJSON(depth + 1);
      return o;
    }
  }
}
for (let i = 0; i < 200; i++) jsonTexts.push(JSON.stringify(randJSON(0), null, rng() % 3 === 0 ? 2 : undefined));
// Every prefix of a few documents: each truncation point is a distinct failure of JSON.parse.
for (const doc of ['{"a":[1,-2.5e3,"x\\u00e9\\n",true,false,null,{"b":{}}],"c":"d"}', '[{"k":"v"},[],"s\\ud83d\\ude00"]', ' {"a" : [ 1 , 2 ] } '])
  for (let n = 0; n < doc.length; n++) jsonTexts.push(doc.slice(0, n));
const json = jsonTexts.map((text) => {
  try {
    return { text, out: JSON.stringify(JSON.parse(text), null, 2) };
  } catch (e) {
    return { text, error: e.name };
  }
});

// ---------------------------------------------------------------- URLs
const urlInputs = [
  "https://api.smith.langchain.com", "https://api.smith.langchain.com/", "https://api.smith.langchain.com/api", "https://eu.api.smith.langchain.com/api/v1",
  "HTTP://EXAMPLE.COM:80/a/b/../c/./d?x=1&y=%20#frag", "http://example.com:8080", "https://example.com:443/x", "https://example.com:0443/x", "http://example.com:/p", "http://example.com:65535", "http://example.com:65536", "http://example.com:99999999999", "http://example.com:abc",
  "http://user:pass@example.com/", "http://user@example.com", "http://:pass@example.com", "http://@example.com", "http://us er@example.com", "http://u:p:q@example.com", "http://a@b@c.com/",
  "http:example.com", "http:/example.com", "http:\\\\example.com\\a\\b", "http:///example.com", "http://", "http:", "http://?x", "https:////example.com//a", "http://exa mple.com", "http://exa%20mple.com", "http://exa%41mple.com", "http://EXAMPLE.com", "http://ex_ample.com", "http://example.com.", "http://example..com", "http://-a.com", "http://a-.com",
  "http://127.0.0.1", "http://127.1", "http://0x7f.1", "http://0177.0.0.1", "http://2130706433", "http://256.0.0.1", "http://1.2.3.4.5", "http://1.2.3.", "http://1.2.3.4.", "http://0x", "http://09.1.1.1", "http://4294967295", "http://4294967296", "http://1.2.3.256", "http://a.1", "http://1.a", "http://0xg",
  "http://[::1]", "http://[::1]:8080/p", "http://[0:0:0:0:0:0:0:1]", "http://[2001:db8::1]", "http://[2001:0db8:0:0:1:0:0:1]", "http://[::ffff:1.2.3.4]", "http://[::ffff:7f00:1]", "http://[1::]", "http://[1:2:3:4:5:6:7:8]", "http://[::]", "http://[:::1]", "http://[1:2:3:4:5:6:7:8:9]", "http://[::1", "http://::1]", "http://[fe80::1%25eth0]", "http://[G::1]", "http://[]",
  "http://münchen.de/", "http://MÜNCHEN.DE/", "http://例え.jp", "http://example.com\u3002cn/", "http://ex\u00adample.com", "http://xn--mnchen-3ya.de", "http://%E4%BE%8B.jp", "http://a%2fb.com", "http://a%00b.com", "http://a%zzb.com",
  "http://example.com/a b", "http://example.com/a%20b", "http://example.com/a%zz", "http://example.com/é", "http://example.com/a?b c", "http://example.com/a?b'c", "https://example.com/a?b'c", "http://example.com/a#b c", "http://example.com/a#b`c", "http://example.com/{}", "http://example.com/^|`", "http://example.com/\"<>", "http://example.com/?\"<>#", "http://example.com/a\tb\nc", "  http://example.com/  ", "\u0001http://example.com/\u0002",
  "http://example.com/..", "http://example.com/a/..", "http://example.com/a/../..", "http://example.com/./", "http://example.com/a/./b/../c", "http://example.com/%2e%2E/x", "http://example.com/a/%2e", "http://example.com/a/.%2e/b", "http://example.com//", "http://example.com/a//b", "http://example.com\\a", "http://example.com/a\\b",
  "http://example.com?q", "http://example.com#h", "http://example.com/?", "http://example.com/#", "http://example.com/?#",
  "ftp://example.com:21/f", "ws://example.com:80/", "wss://example.com:443/", "file:///etc/passwd", "file://localhost/etc", "file://host/a", "file:/a/b", "file:a", "file://", "file:///C:/x", "file:///a/../b",
  "foo://example.com", "foo://example.com:99/p", "foo://Example.com/A/../b", "foo:bar", "foo:", "foo:/bar", "foo://", "foo://user@h/", "foo://h:x", "foo://[::1]/", "foo://a b/", "foo:/a/./b/..", "foo:a b?q c#f g", "mailto:a@b.c", "data:text/plain,hi", "javascript:alert(1)", "urn:isbn:1", "a+b-c.d:e", "1a://x", "://x", "x", "", "http", "/api", "//example.com", "?x", "#x",
  "http://example.com:80:80", "http://example.com:80:", "https://[::1]:443/", "https://[::1]:444/", "http://a.b.c.d.e.f", "http://localhost", "http://localhost:3000/api/v1",
  "https://host/api/v1/", "https://host/api//", "https://host///", "https://host/%7Efoo", "https://host/%7efoo", "https://host/a%2Fb",
];
const extraUrls = ["web+demo:/.//not-a-host/", "foo:/.//a", "foo:///", "foo:/./", "http://@", "file://@h/", "file://h:80/", "file://:80/", "foo://:80", "foo://u@:80/", "foo://u@/", "http://\u00ad/", "http://1..2", "http://99999999999999999999.1", "http://0x7f.0x0.0x0.0x1", "http://1.2.3.4.5.", "http://0.0.0.0x100", "http://0x100000000", "http://1.0x1000000", "http://.", "http://..", "http://a..", "http://%2e", "http://%2E%2E", "file://LOCALHOST/x", "file://localhost:", "file:///a/./b", "foo://h/%2e%2e/a"];
urlInputs.push(...extraUrls);
const urls = urlInputs.map((input) => {
  try {
    const u = new URL(input);
    return { input, href: u.href, origin: u.origin, protocol: u.protocol, host: u.host, pathname: u.pathname, username: u.username, password: u.password };
  } catch {
    return { input, error: true };
  }
});

// ---------------------------------------------------------------- discovery
// resolveTokenEndpoint with a fake fetch: records the URLs it is asked for (as `new Request(url)` serialises them;
// a URL fetch rejects is counted, not listed).
function fakeFetch(respond, log) {
  return async (url, init) => {
    let href;
    try {
      const u = new URL(url);
      new Request(url); // rejects credentials
      if (u.protocol !== "http:" && u.protocol !== "https:") throw new TypeError("scheme");
      href = new Request(url).url.replace(/#.*$/, "");
    } catch {
      log.rejected++;
      throw new TypeError("fetch failed");
    }
    log.requests.push({ method: init?.method, url: href, accept: init?.headers?.Accept ?? null });
    const r = respond(href, init);
    return new Response(r.body, { status: r.status });
  };
}
const discoveryApiUrls = [
  "https://api.smith.langchain.com", "https://api.smith.langchain.com/", "https://api.smith.langchain.com/api", "https://api.smith.langchain.com/api/", "https://api.smith.langchain.com/api/v1", "https://api.smith.langchain.com/api/v1/", "https://api.smith.langchain.com///",
  "https://host.example/langsmith", "https://host.example/langsmith/api", "https://host.example/langsmith/api/v1", "https://host.example/a/b/c/", "https://host.example/api/api", "https://host.example/api/api/v1",
  "http://localhost:1984", "http://localhost:1984/api/v1", "HTTP://LOCALHOST:80/Api", "https://host.example:443/api", "https://host.example?x=1", "https://host.example/api?x=1", "https://host.example/#frag", "https://host.example/api#frag",
  "https://user:pw@host.example/api", "ftp://host.example/api", "file:///tmp/x", "foo://bar/api", "foo:opaque", "mailto:a@b", "not a url", "host.example", "/api", "/", "", "api", "https://", "http://[::1]:8080/api", "https://münchen.example/api", "https://host.example/a b", "https://host.example/%7Etilde/api", "https://host.example/a/../b/api", "https://host.example\\api",
];
const discovery = [];
for (const apiUrl of discoveryApiUrls) {
  const log = { requests: [], rejected: 0 };
  const endpoint = await resolveTokenEndpoint(apiUrl, fakeFetch(() => ({ status: 404, body: "" }), log));
  discovery.push({ apiUrl, endpoint, ...log });
}

// Trust decisions: the fake server answers only the first metadata URL, with a document built from `doc`.
const trustCases = [];
const trustBases = ["https://host.example", "https://host.example/api", "https://host.example:8443/api", "http://localhost:1984"];
for (const apiUrl of trustBases) {
  const base = apiUrl;
  const origin = new URL(base).origin;
  const issuers = [base, `${base}/`, `${base}//`, base.toUpperCase(), base.replace("://", "://u@"), `${origin}`, `${origin}/api`, `${base}/x`, "", 5, null, `${base}?q`, new URL(base).protocol + "//" + new URL(base).hostname + "/" + new URL(base).pathname.slice(1)];
  const tokens = [`${origin}/oauth/token`, `${origin}:443/oauth/token`, `${origin.replace("https:", "http:")}/oauth/token`, `${origin}.evil.example/token`, "https://evil.example/token", "/relative", "", 5, null, undefined, `${origin}/t?x=1#y`, `${origin.toUpperCase()}/T`, `${origin}\\t`, `${origin.replace("://", "://u:p@")}/t`, "http://[::1]/t", `${origin}:8443/t`, "ftp://host.example/t", "https://host.example./t"];
  const devices = [`${origin}/oauth/device`, undefined, "", 7, "https://evil.example/d", `${origin}:99/d`];
  for (const issuer of issuers)
    for (const token of tokens) trustCases.push({ apiUrl, doc: { issuer, token_endpoint: token, device_authorization_endpoint: devices[0] } });
  for (const device of devices) for (const token of tokens.slice(0, 3)) trustCases.push({ apiUrl, doc: { issuer: base, token_endpoint: token, device_authorization_endpoint: device } });
}
const trust = [];
for (const { apiUrl, doc } of trustCases) {
  const log = { requests: [], rejected: 0 };
  let first = true;
  const endpoint = await resolveTokenEndpoint(apiUrl, fakeFetch(() => {
    if (first) {
      first = false;
      return { status: 200, body: JSON.stringify(doc) };
    }
    return { status: 404, body: "" };
  }, log));
  trust.push({ apiUrl, doc, endpoint, requests: log.requests.length });
}

// Non-JSON and odd bodies on the first metadata URL.
const bodies = ["", "null", "[]", "{}", "5", '"s"', "<html/>", "\ufeff{}", '{"issuer":"https://host.example"}'];
const oddBodies = [];
for (const body of bodies) {
  const log = { requests: [], rejected: 0 };
  let first = true;
  const endpoint = await resolveTokenEndpoint("https://host.example", fakeFetch(() => {
    if (first) {
      first = false;
      return { status: 200, body };
    }
    return { status: 404, body: "" };
  }, log));
  oddBodies.push({ body, endpoint, requests: log.requests.length });
}

// ---------------------------------------------------------------- form encoding
const formInputs = ["", "abc", "a b", "a+b", "a&b=c", "~", "*", "'()!", "%", "é", "😀", "\ud800", "x\udc00", "a\ud83d\ude00\ud83d", "\u0000\u001f\u007f", "\u2028", "-._", "\u00a0", "/?#[]@", "=", "a%20b", "日本語"];
for (let i = 0; i < 100; i++) {
  let s = "";
  for (let j = rng() % 12; j > 0; j--) s += String.fromCharCode(pick([rng() % 128, 0x80 + (rng() % 0x780), rng() % 0xffff, 0xd800 + (rng() % 0x800)]));
  formInputs.push(s);
}
const form = formInputs.map((s) => ({ input: s, out: new URLSearchParams({ grant_type: "refresh_token", client_id: "langsmith-cli", refresh_token: s }).toString() }));

// ---------------------------------------------------------------- trim / hasValue
const spaceUnits = [];
for (let c = 0; c < 0x100; c++) spaceUnits.push(c);
for (const c of [0x1680, 0x180e, 0x2000, 0x2001, 0x200a, 0x200b, 0x200c, 0x200d, 0x200e, 0x2028, 0x2029, 0x202f, 0x205f, 0x2060, 0x3000, 0xfeff, 0xfffe, 0xd800]) spaceUnits.push(c);
const hasValueCases = [];
for (const c of spaceUnits) {
  const s = String.fromCharCode(c);
  hasValueCases.push({ input: s, out: hasValue(s) });
  hasValueCases.push({ input: s + "x" + s, out: hasValue(s + "x" + s) });
}
for (const s of ["", " ", "x", " x ", "\t\n", "\u00a0\u3000"]) hasValueCases.push({ input: s, out: hasValue(s) });
const scratch = fs.mkdtempSync(path.join(process.env.LS_SCRATCH || os.tmpdir(), "profile-vec-"));
const cfgPath = path.join(scratch, "config.json");
process.env.LANGSMITH_CONFIG_FILE = cfgPath;
delete process.env.LANGSMITH_PROFILE;
const trimInputs = ['"', "'", '""', "''", '"a"', "'a'", "\"a'", "'a\"", '"a', 'a"', '  "a"  ', '"  a  "', '""a""', "'''", '"\u00a0a\u00a0"', "\u00a0'a'\u00a0", "a", "", " ", "\ufeff'a'\ufeff", '\u2028"a"\u2029', "\u180e a \u180e", '"\u0085a"'];
for (const c of spaceUnits) trimInputs.push(String.fromCharCode(c) + '"k' + String.fromCharCode(c) + "'" + String.fromCharCode(c));
const trim = trimInputs.map((input) => {
  fs.writeFileSync(cfgPath, JSON.stringify({ current_profile: "p", profiles: { p: { api_key: input, workspace_id: input, oauth: { access_token: input, refresh_token: input } } } }));
  const c = loadProfileClientConfig();
  return { input, apiKey: c.apiKey, access: c.oauthAccessToken, refresh: c.oauthRefreshToken, hasAuth: c.profileAuth !== undefined };
});
fs.rmSync(scratch, { recursive: true, force: true });

// WHATWG UTF-8 decode (Buffer.toString): every maximal invalid subpart becomes one U+FFFD.
const utf8Cases = [];
const seqs = ["", "41", "c3a9", "e282ac", "f09f9880", "80", "bf", "c0", "c1", "c080", "c1bf", "c2", "c2c2", "c2a9c2", "e0", "e080", "e09f80", "e0a080", "e0a0", "ed", "eda080", "edbfbf", "ed9fbf", "ee80", "e282", "e28228", "f0", "f08080", "f0908080", "f090", "f09080", "f09080c3", "f48f8080", "f4908080", "f4", "f480", "f5", "f5808080", "f8", "ff", "fe", "fffe", "41ff42", "e282ffc3a9", "f09f98", "f09f9841", "c3", "c328", "e2", "e2ac", "dfbf", "e0a080e0a080", "efbbbf41", "feff", "00", "c280", "f4808080", "f3bfbfbf"];
for (const h of seqs) utf8Cases.push(h);
for (let i = 0; i < 400; i++) {
  let h = "";
  for (let j = 1 + (rng() % 9); j > 0; j--) h += pick(["00", "41", "7f", "80", "8f", "90", "9f", "a0", "bf", "c2", "c3", "df", "e0", "e1", "ed", "ef", "f0", "f1", "f4", "f5", "ff", rng().toString(16).slice(-2).padStart(2, "0")]);
  utf8Cases.push(h);
}
const utf8 = utf8Cases.map((hex) => ({ hex, out: Buffer.from(hex, "hex").toString("utf8") }));
writeVectors("profile_vectors.json", { json, urls, discovery, trust, oddBodies, form, hasValue: hasValueCases, trim, utf8 });
