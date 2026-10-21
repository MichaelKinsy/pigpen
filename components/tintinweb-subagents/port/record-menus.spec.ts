// Records the original's drawing of the menus on fixed inputs (testdata/menus-fixtures.json) into
// testdata/menus-golden.json, which menus_golden_test.go compares line by line. Run by vitest from a Pi checkout (Pi's
// pi-tui resolves through the alias in port/record-menus.config.mjs); Node never runs at the port's runtime.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { SettingsList } from "@earendil-works/pi-tui";
import { it } from "vitest";
import { layoutWorkflowCard, styleWorkflowCardLines } from "./oracle/src/ui/workflow-card.js";
import { initialWorkflowDialogState, layoutWorkflowDialog } from "./oracle/src/ui/workflow-dialog.js";

const here = join(__dirname, "..", "extensions", "tintinweb-subagents", "testdata");
const theme = { fg: (c: string, t: string) => `<${c}>${t}</${c}>`, bold: (t: string) => `*${t}*` };

it("records the menus", () => {
  const fixtures = JSON.parse(readFileSync(join(here, "menus-fixtures.json"), "utf8"));
  const golden: Record<string, Record<string, string[]>> = { cards: {}, dialogs: {}, settingsLists: {} };
  for (const c of fixtures.cards) golden.cards[c.name] = styleWorkflowCardLines(layoutWorkflowCard(c.input), theme);
  for (const d of fixtures.dialogs) {
    const state = { ...initialWorkflowDialogState(), ...d.input.state };
    golden.dialogs[d.name] = styleWorkflowCardLines(layoutWorkflowDialog({ ...d.input, state }), theme);
  }
  const slTheme = {
    label: (t: string, s: boolean) => (s ? theme.fg("accent", t) : t),
    value: (t: string, s: boolean) => (s ? theme.fg("accent", t) : theme.fg("muted", t)),
    description: (t: string) => theme.fg("dim", t),
    cursor: theme.fg("accent", "→ "),
    hint: (t: string) => theme.fg("dim", t),
  };
  for (const s of fixtures.settingsLists) {
    const list = new SettingsList(structuredClone(s.items), s.maxVisible, slTheme, () => {}, () => {});
    for (let i = 0; i < (s.downs ?? 0); i++) list.handleInput("\x1b[B");
    for (let i = 0; i < (s.ups ?? 0); i++) list.handleInput("\x1b[A");
    golden.settingsLists[s.name] = list.render(s.width);
  }
  writeFileSync(join(here, "menus-golden.json"), `${JSON.stringify(golden, null, 1)}\n`);
});
