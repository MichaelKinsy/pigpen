import { writeFileSync } from "node:fs";
import { buildIndexPrompt } from "./src/index-cmd.ts";
const skills = [{ name: "tdd", description: "TDD", filePath: "/skills/tdd/SKILL.md" }, { name: "impeccable", description: "UI design", filePath: "/skills/impeccable/SKILL.md" }];
const tools = [{ name: "bash", description: "Run commands" }, { name: "search_graph", description: "Search the code graph" }];
writeFileSync(process.argv[2]!, buildIndexPrompt("/p", skills, tools, { global: "/g.json", project: "/p.json" }, { agentDir: "/home/u/.pi/agent" } as never));
writeFileSync(process.argv[3]!, buildIndexPrompt("/p", [], [], { global: "/g.json", project: "/p.json" }, { agentDir: "/a" } as never));
