// vitest config for record-menus.spec.ts: the original's imports of Pi's packages resolve to a Pi checkout
// (PI_SRC, default /opt/pp-shared-pig/pi-src), as the original runs under Pi.
const pi = process.env.PI_SRC || "/opt/pp-shared-pig/pi-src";
export default {
  resolve: { alias: {
    "@earendil-works/pi-tui": `${pi}/packages/tui/src/index.ts`,
    "@earendil-works/pi-coding-agent": `${pi}/packages/coding-agent/src/index.ts`,
    vitest: `${pi}/node_modules/vitest/dist/index.js`,
  } },
  test: { include: ["port/record-menus.spec.ts"], root: process.env.RECORD_ROOT },
};
