// Times the original's conscience paths on the same inputs as extensions/warden/conscience_bench_test.go.
// Copy into port/oracle and run: node --import tsx --import ./tests/setup-env.ts ./_bench-conscience.ts
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { assess, buildBatchQuestions, eligibleCandidates, questionHash, sanitizeDescription } from "./src/conscience.ts";
import type { Judge } from "./src/conscience.ts";
import { loadSkillBody } from "./src/load.ts";
import { buildIndexPrompt, readIndex, validateIndex, writeIndex } from "./src/index-cmd.ts";
import type { IndexFile } from "./src/index-cmd.ts";

const time = async (name: string, n: number, f: (i: number) => unknown | Promise<unknown>) => {
  const runs: number[] = [];
  for (let r = 0; r < 11; r++) {
    const start = performance.now();
    for (let i = 0; i < n; i++) await f(i);
    runs.push((performance.now() - start) * 1000 / n);
  }
  runs.sort((a, b) => a - b);
  console.log(`${name}: median ${runs[5]!.toFixed(2)} us/op over 11 runs of ${n}`);
};

const dir = mkdtempSync(join(tmpdir(), "bench-"));
const mkSkills = (n: number, withFiles: boolean) => Array.from({ length: n }, (_, i) => {
  const filePath = join(dir, `skill-${i}.md`);
  if (withFiles) writeFileSync(filePath, `---\nname: skill-${i}\ndescription: Skill ${i} does a thing\n---\n\nBody of skill ${i}.\n`);
  return { name: `skill-${i}`, description: `Skill ${i} does a thing with files and tests`, filePath, baseDir: dir, sourceInfo: { path: filePath, source: "local", scope: "user" as const, origin: "top-level" as const }, disableModelInvocation: false };
});
const config = { enabled: true, skills: { mode: "recommend" as const, exclude: [] as string[] }, tools: { enabled: true, exclude: [] as string[] }, skipTools: [] as string[], timeoutMs: 3000, maxAssessments: 3, maxNudges: 2, maxSkillBytes: 32768, maxLoadedBytes: 65536, recommendThreshold: 0.5, advanceThreshold: 0.7 };
const judge: Judge = {
  evaluate: async ({ questions }) => {
    const answers: Record<string, unknown> = { conscience_disposition: { type: "choice", choice: "advance", confidence: 0.9, probabilities: { advance: 0.9, awaiting_user: 0, no_gap: 0, unclear: 0 } } };
    for (const id of Object.keys(questions)) if (id !== "conscience_disposition") answers[id] = { type: "score", score: 2, confidence: 0.8, legend: {}, probabilities: { "0": 0.1, "1": 0.2, "2": 0.5, "3": 0.2 } };
    return { answers };
  },
};
const tools = (n: number) => Array.from({ length: n }, (_, i) => ({ name: `tool_${i}`, description: `Tool ${i} lists things in the repository` }));
const deps = { judge, config, sharedTimeoutMs: 5000 };
const s40 = mkSkills(40, true);
await time("assess 40 skills", 300, () => assess("design a landing page for the repository", "", s40 as never, [], [], [], deps));
const s250 = mkSkills(250, true);
const t60 = tools(60);
await time("assess 250 skills + 60 tools", 100, () => assess("design a landing page for the repository", "", s250 as never, t60, [], [], deps));
const s100 = mkSkills(100, true);
const t100 = tools(100);
await time("eligibleCandidates 100 skill files + 100 tools", 100, () => eligibleCandidates(s100 as never, t100, config, [], []));
const batch = Array.from({ length: 31 }, (_, i) => ({ opaqueId: `c${i + 1}`, candidate: { kind: "skill" as const, id: `s${i}`, description: "d" } }));
await time("buildBatchQuestions + questionHash, 31 candidates", 2000, () => questionHash(buildBatchQuestions(batch, "task", "", [], []).questions));
const text = "Use this to read /Users/anon/project/file.ts and see https://docs.example.com/guide, config at ~/config.json; token: abcdefghijklmnop12345 for auth. ".repeat(12);
await time("sanitizeDescription 1.8 KB", 5000, () => sanitizeDescription(text));
const entries = Array.from({ length: 200 }, (_, i) => ({ kind: "skill", name: `skill-${i}`, scope: "global", sourceHash: "abc123def4567890", role: "evidence", lead: "Search the code knowledge graph", useWhen: ["When you need to find definitions", "When grep cannot find structural code"], notWhen: ["When a plain search works do that"], inputs: "A search query string", examples: ["find the config loader function", "search for all calls"], sourceQuality: "strong" }));
const raw = { formatVersion: 1, builtAt: "t", model: "m", entries };
await time("validateIndex 200 entries", 200, () => validateIndex(raw));
const file = validateIndex(raw).file as IndexFile;
const idxPath = join(dir, "idx", "global.json");
mkdirSync(join(dir, "idx"), { recursive: true });
await time("writeIndex + readIndex 200 entries", 200, () => { writeIndex(idxPath, file); return readIndex(idxPath); });
const sk100 = Array.from({ length: 100 }, (_, i) => ({ name: `skill-${i}`, description: "Skill does a thing with files and tests", filePath: `/home/u/.pi/agent/skills/skill-${i}/SKILL.md` }));
const tl60 = tools(60);
await time("buildIndexPrompt 100 skills + 60 tools", 5000, () => buildIndexPrompt("/p", sk100, tl60, { global: "/g.json", project: "/p.json" }));
const skillPath = join(dir, "SKILL.md");
writeFileSync(skillPath, "---\nname: bench\ndescription: Bench skill\n---\n\n" + "Polish the interface: align spacing, tighten type, check contrast.\n".repeat(120));
const loadSkill = { name: "bench", description: "Bench skill", filePath: skillPath, baseDir: dir, sourceInfo: { path: skillPath, source: "local", scope: "user" as const, origin: "top-level" as const }, disableModelInvocation: false };
const loadConfig = { ...config, skills: { mode: "load" as const, exclude: [] as string[] } };
await time("loadSkillBody 8 KB", 2000, () => loadSkillBody(loadSkill as never, loadConfig, { exemptRules: [], loadedBytes: 0, remainingMs: 5000, consentGiven: true, projectTrusted: true, catalogName: "bench", catalogDescription: "Bench skill", userInvoked: false, contextWindow: 200000, hasImages: false }));
rmSync(dir, { recursive: true, force: true });
