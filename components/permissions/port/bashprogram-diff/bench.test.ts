// Latency of the original's BashProgram.parse on bench-inputs.json (median ns per parse over 15 runs of 100), printed as a BENCH
// line. Copied into an installed copy of the original by bench-rounds.sh for the run and removed after; the filesystem is a real
// temporary directory (the project cwd) so the existence probe does what it does in production.
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { BashProgram } from "#src/access-intent/bash/program";
import { warmBashParser } from "#src/access-intent/bash/parser";
import { posixPathFlavor } from "#src/path/path-flavor";
import { PathNormalizer } from "#src/path/path-normalizer";

it("times BashProgram.parse", { timeout: 0 }, async () => {
  const inputs: Record<string, string> = JSON.parse(readFileSync(process.env.BENCH_IN as string, "utf8"));
  const root = mkdtempSync(join(tmpdir(), "pbpb-"));
  mkdirSync(join(root, "src"));
  writeFileSync(join(root, "x.ts"), "x");
  const normalizer = new PathNormalizer(posixPathFlavor, root);
  const t0 = process.hrtime.bigint();
  await warmBashParser();
  const cold = Number(process.hrtime.bigint() - t0) / 1e6;
  const out: Record<string, number> = { cold_ms: cold };
  for (const [name, command] of Object.entries(inputs)) {
    for (let i = 0; i < 100; i++) await BashProgram.parse(command, normalizer);
    const runs: number[] = [];
    for (let r = 0; r < 15; r++) {
      const start = process.hrtime.bigint();
      const n = 100;
      for (let i = 0; i < n; i++) await BashProgram.parse(command, normalizer);
      runs.push(Number(process.hrtime.bigint() - start) / n);
    }
    runs.sort((a, b) => a - b);
    out[name] = runs[Math.floor(runs.length / 2)];
  }
  console.log(`BENCH ${JSON.stringify(out)}`);
});
