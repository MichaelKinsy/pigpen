#!/usr/bin/env node
// Writes extensions/tintinweb-subagents/workflow_tool_description.txt: the cooked text of the template literal
// `fullWorkflowToolDescription` in the original's src/workflow/tool-description.ts (the escapes and `\${` resolved as the
// JavaScript engine resolves them). {{typeList}} stays: the extension substitutes the live agent roster.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(join(here, "oracle", "src", "workflow", "tool-description.ts"), "utf8");
const open = "export const fullWorkflowToolDescription = `";
const start = source.indexOf(open) + open.length;
const end = source.lastIndexOf("`;");
const cooked = new Function(`return \`${source.slice(start, end)}\``)();
writeFileSync(join(here, "..", "extensions", "tintinweb-subagents", "workflow_tool_description.txt"), cooked);
