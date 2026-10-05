/**
 * The tool-surface region of a system prompt: which tools this session may
 * call, and the guidance those tools contribute.
 *
 * Pi renders that region itself, as its `<tools>` and `<rules>` sections, from
 * the active tool set this extension narrows — but only when it writes the
 * preamble. A subagent child's prompt is always a `customPrompt`, under which
 * Pi writes neither section, so a child states its own through
 * `systemPromptOptions.sections`, which Pi places after its `<cwd>` section.
 * This module renders those section contents.
 *
 * Rendering follows `buildSystemPrompt`'s own rules: a tool is listed only
 * when it has a snippet, and the guideline bullets are the allowed tools' own
 * `promptGuidelines`, then other extensions' rules, around Pi's built-in ones,
 * so the sections read as the ones Pi would have written for this session's
 * real surface.
 */

/** What a session's tool surface renders from. */
export interface ToolSurfaceInputs {
  /** Tools this session may call, in the order they should be listed. */
  readonly allowedTools: readonly string[];
  /** Pi's one-line tool descriptions, keyed by tool name. */
  readonly toolSnippets: Readonly<Record<string, string>>;
  /** Guideline bullets each tool contributes, keyed by tool name. */
  readonly guidelinesByTool: ReadonlyMap<string, readonly string[]>;
  /**
   * `systemPromptOptions.promptGuidelines`: the bullets other extensions add
   * to Pi's rules after the tools' own.
   *
   * Only a bullet no registered tool contributes is carried, so a denied
   * tool's bullet cannot return by this route.
   */
  readonly promptGuidelines: readonly string[];
}

/**
 * This session's tool surface as the contents of Pi's `tools` and `rules`
 * prompt sections, untagged: Pi wraps each `sections` entry in its own tags.
 */
export interface ToolSurfaceSections {
  /** `- name: snippet` lines; absent when no allowed tool has a snippet. */
  readonly tools?: string;
  /** `- rule` lines, in `buildSystemPrompt`'s order. */
  readonly rules: string;
}

/** Pi's two unconditional guideline bullets, in the order it writes them. */
const UNIVERSAL_GUIDELINES: readonly string[] = [
  "Be concise in your responses",
  "Show file paths clearly when working with files",
];

/**
 * Render this session's tool surface as section contents, for a node that
 * states it through `systemPromptOptions.sections`.
 *
 * The `tools` section is omitted when no allowed tool has a snippet, as Pi
 * lists a tool only when it has one.
 */
export function renderToolSurfaceSections(
  inputs: ToolSurfaceInputs,
): ToolSurfaceSections {
  const tools = toolBullets(inputs);
  const rules = ruleBullets(inputs).join("\n");
  return tools.length > 0 ? { tools: tools.join("\n"), rules } : { rules };
}

/**
 * One bullet per allowed tool that has a snippet.
 *
 * Pi lists a tool only when the caller supplied a one-line snippet for it, so
 * a tool without one is left unlisted here too rather than rendered bare.
 */
function toolBullets(inputs: ToolSurfaceInputs): string[] {
  return inputs.allowedTools
    .map((toolName) => ({ toolName, snippet: inputs.toolSnippets[toolName] }))
    .filter((tool) => Boolean(tool.snippet))
    .map((tool) => `- ${tool.toolName}: ${tool.snippet}`);
}

/**
 * The guideline bullets for the allowed set.
 *
 * Mirrors `buildSystemPrompt`'s assembly: its conditional file-exploration
 * bullet first, then each allowed tool's own contributions, then the bullets
 * other extensions added, then its two unconditional bullets — de-duplicated
 * in first-seen order, as Pi does.
 */
function ruleBullets(inputs: ToolSurfaceInputs): string[] {
  const bullets: string[] = [];
  const seen = new Set<string>();
  const addGuideline = (guideline: string): void => {
    const normalized = guideline.trim();
    if (normalized.length === 0 || seen.has(normalized)) {
      return;
    }
    seen.add(normalized);
    bullets.push(normalized);
  };

  const fileOperations = fileExplorationGuideline(new Set(inputs.allowedTools));
  if (fileOperations) {
    addGuideline(fileOperations);
  }

  for (const toolName of inputs.allowedTools) {
    for (const guideline of inputs.guidelinesByTool.get(toolName) ?? []) {
      addGuideline(guideline);
    }
  }

  for (const guideline of extensionGuidelines(inputs)) {
    addGuideline(guideline);
  }

  for (const guideline of UNIVERSAL_GUIDELINES) {
    addGuideline(guideline);
  }

  return bullets.map((bullet) => `- ${bullet}`);
}

/** The `promptGuidelines` bullets no registered tool contributes. */
function extensionGuidelines(inputs: ToolSurfaceInputs): string[] {
  const toolGuidelines = new Set(
    [...inputs.guidelinesByTool.values()]
      .flat()
      .map((guideline) => guideline.trim()),
  );
  return inputs.promptGuidelines.filter(
    (guideline) => !toolGuidelines.has(guideline.trim()),
  );
}

/**
 * Pi's shell-only file-exploration bullet, or `null` when it does not apply.
 *
 * Pi writes it only when a shell is available and none of the dedicated
 * exploration tools is, so a session holding `grep`/`find`/`ls` is not told to
 * reach for the shell instead.
 */
function fileExplorationGuideline(
  allowedTools: ReadonlySet<string>,
): string | null {
  const hasBash = allowedTools.has("bash");
  const hasPowerShell = allowedTools.has("powershell");
  const hasExplorationTool =
    allowedTools.has("grep") ||
    allowedTools.has("find") ||
    allowedTools.has("ls");

  if ((!hasBash && !hasPowerShell) || hasExplorationTool) {
    return null;
  }
  if (hasBash && hasPowerShell) {
    return "Use bash or PowerShell for file operations like listing, searching, and finding files";
  }
  if (hasPowerShell) {
    return "Use PowerShell for file operations like listing, searching, and finding files";
  }
  return "Use bash for file operations like ls, rg, find";
}
