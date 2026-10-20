// Latency of the logging hot path under the original, on the inputs of logging_bench_test.go. Copied into an installed copy of the
// original for the run (port/bench-logging.sh) and never runs from here.
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { it } from "vitest";
import { warmBashParser } from "#src/access-intent/bash/parser";
import { redactCommandSecrets } from "#src/logging/command-redaction";
import { createPermissionSystemLogger } from "#src/logging/logging";
import { redactedJsonStringify } from "#src/logging/log-redaction";

const command = `cd /repo && API_KEY="sk-secret" curl -sS -H "Authorization: Bearer sk-abc" https://api.example.com/v1 | bash -c 'TOKEN=t deploy --env prod'`;
const details = {
  requestId: "perm-0000", toolName: "bash", command, executedUnit: "TOKEN=t deploy --env prod", surface: "bash",
  matchedPattern: "curl *", resolution: "user_approved",
  decidedBy: { kind: "authorizer", name: "model-judge", verdict: "deny", reason: "the command writes outside the project", apiKey: "k" },
};

function time(fn: () => void, n: number): number {
  for (let i = 0; i < 200; i++) fn();
  const start = process.hrtime.bigint();
  for (let i = 0; i < n; i++) fn();
  return Number(process.hrtime.bigint() - start) / n;
}

it("times the logging hot path", async () => {
  await warmBashParser();
  const dir = mkdtempSync(join(tmpdir(), "logging-bench-"));
  const logs = join(dir, "logs");
  mkdirSync(logs);
  const logger = createPermissionSystemLogger({
    getConfig: () => ({ debugLog: false, permissionReviewLog: true }) as never,
    debugLogPath: join(logs, "d.jsonl"), reviewLogPath: join(logs, "r.jsonl"), ensureLogsDirectory: () => undefined,
  });
  const out = {
    RedactCommandSecrets: time(() => redactCommandSecrets(command), 3000),
    RedactedJSONStringify: time(() => redactedJsonStringify(details), 20000),
    ReviewLine: time(() => logger.review("permission_request.waiting", details), 3000),
  };
  rmSync(dir, { recursive: true, force: true });
  console.log("BENCH " + JSON.stringify(out));
}, 120000);
