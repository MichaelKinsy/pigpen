import { readFileSync } from "node:fs";
import { parseEvaluationRequest } from "./src/schema.ts";
const cases = JSON.parse(readFileSync("battery.json","utf8"));
const out = cases.map((c: unknown) => { try { parseEvaluationRequest(c); return "OK"; } catch (e) { return (e as Error).message.replace(/ Expected \{.*$/, ""); } });
console.log(JSON.stringify(out, null, 1));
