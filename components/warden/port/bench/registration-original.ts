// Times what the original does at load: the module import and the factory call with a stand-in `pi` that accepts every
// registration. Run from port/oracle after bundling (see port/bench/README in the PR body): the output is
// `import_ms factory_ms`, one line per process.
const started = performance.now();
const { default: factory } = await import("../oracle/src/extension.js");
const imported = performance.now();
const noop = () => undefined;
const pi = new Proxy({}, { get: (_t, prop) => (prop === "events" ? { on: noop, emit: noop } : noop) });
(factory as (pi: unknown) => void)(pi);
const done = performance.now();
console.log(`${(imported - started).toFixed(3)} ${(done - imported).toFixed(3)}`);
