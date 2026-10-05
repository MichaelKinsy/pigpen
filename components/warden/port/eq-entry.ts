// Entry for the equivalence harness: loads the vendored, unmodified original from source (Pi compiles TypeScript
// itself), so no build step is needed. The oracle directory is not changed.
export { default } from "./oracle/src/extension.ts";
