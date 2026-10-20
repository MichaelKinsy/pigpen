// The same cases as extensions/warden/security_bench_test.go, against the original. Run with:
//   port/bench/run.sh (node --experimental-transform-types with port/bench/ts-register.mjs)
import { findSecrets, maskSecrets } from "../oracle/src/redact.js";
import { RunawayMonitor } from "../oracle/src/runaway.js";
import { buildProseRequest, restatedShare } from "../oracle/src/prose.js";

function benchOutput(): string {
  const parts: string[] = [];
  let size = 0;
  for (let i = 0; size < 64 * 1024; i++) {
    if (i === 600) { const k = "export API_KEY=k7Rm2Qx9Lp4Ns8Vt1Wd6" + "Yc3Zb5Hf7Jg0Pk2Mn5Qz\n"; parts.push(k); size += k.length; }
    const line = `build step ${i}: compiled module_${i % 97}.o in ${(i * 37) % 1000} ms, token count ${i * 13}\n`;
    parts.push(line); size += line.length;
  }
  return parts.join("");
}
const paragraphs = Array.from({ length: 40 }, (_, i) => `Paragraph ${i} explains one distinct part of the merge process in its own words.`).join("\n\n");
const done = "CON-375 done: draft PR 2688 is pushed with code, tests and screenshots, and Linear is In Review. Worktree millia-con375 awaits review.";
const fresh = "The security notice named variable names only: no values were echoed and nothing was committed. pr-evidence holds PNG files.";
const reply = "CON-375 is complete: the draft pull request 2688 is pushed together with code, tests and screenshots, and Linear sits In Review. The worktree millia-con375 now awaits review. " + "Every check passed and nothing else changed in the worktree today. ".repeat(3);
const output = benchOutput();
const config = { enabled: true, repeats: 4, thinkingRepeats: 10, minChars: 400, recover: true };
let sink: unknown;

const cases: Record<string, () => void> = {
  MaskSecrets64KB: () => { sink = maskSecrets(output); },
  FindSecrets64KB: () => { sink = findSecrets(output); },
  RunawayStream: () => {
    const m = new RunawayMonitor();
    m.feed({ type: "text_start" });
    for (let i = 0; i < paragraphs.length; i += 7) {
      const kind = m.feed({ type: "text_delta", delta: paragraphs.slice(i, i + 7) });
      if (kind) sink = m.check(kind, config);
    }
  },
  RestatedShare: () => { sink = restatedShare(reply, [done, fresh, done, fresh]); },
  BuildProseRequest: () => { sink = buildProseRequest("explain the bug", "Great question! Let me walk you through it. ".repeat(40), "technical"); },
};

const only = process.argv[2];
for (const [name, run] of Object.entries(cases)) {
  if (only && only !== name) continue;
  for (let i = 0; i < 20; i++) run();
  const started = process.hrtime.bigint();
  let n = 0;
  while (process.hrtime.bigint() - started < 300_000_000n) { run(); n++; }
  const ns = Number(process.hrtime.bigint() - started) / n;
  console.log(`${name} ${ns.toFixed(0)}`);
}
void sink;
