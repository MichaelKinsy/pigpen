import { dirname } from "node:path";

import type { PathNormalizer } from "#src/path/path-normalizer";
import type { PermissionCheckResult, PermissionState } from "#src/types";

/**
 * Narrow interface for the raw (no-session-rules) permission checker used by
 * skill prompt resolution. `PermissionResolver` implements it (#478).
 */
export interface SkillPermissionChecker {
  checkPermission(
    surface: string,
    input: unknown,
    agentName?: string,
  ): PermissionCheckResult;
}

const AVAILABLE_SKILLS_OPEN_TAG = "<available_skills>";
const AVAILABLE_SKILLS_CLOSE_TAG = "</available_skills>";
const SKILL_BLOCK_PATTERN = "<skill>([\\s\\S]*?)<\\/skill>";
const SKILL_NAME_REGEX = /<name>([\s\S]*?)<\/name>/;
const SKILL_DESCRIPTION_REGEX = /<description>([\s\S]*?)<\/description>/;
const SKILL_LOCATION_REGEX = /<location>([\s\S]*?)<\/location>/;

type ParsedSkillPromptEntry = {
  name: string;
  description: string;
  location: string;
};

export type SkillPromptEntry = {
  name: string;
  description: string;
  location: string;
  state: PermissionState;
  normalizedLocation: string;
  normalizedBaseDir: string;
};

export type SkillPromptSection = {
  entries: ParsedSkillPromptEntry[];
};

function decodeXml(value: string): string {
  return value
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, "&");
}

function parseSkillEntries(sectionBody: string): ParsedSkillPromptEntry[] {
  const entries: ParsedSkillPromptEntry[] = [];
  const skillBlockRegex = new RegExp(SKILL_BLOCK_PATTERN, "g");

  for (const match of sectionBody.matchAll(skillBlockRegex)) {
    const block = match[1];
    const nameMatch = SKILL_NAME_REGEX.exec(block);
    const descriptionMatch = SKILL_DESCRIPTION_REGEX.exec(block);
    const locationMatch = SKILL_LOCATION_REGEX.exec(block);

    if (!nameMatch || !descriptionMatch || !locationMatch) {
      continue;
    }

    const name = decodeXml(nameMatch[1].trim());
    const description = decodeXml(descriptionMatch[1].trim());
    const location = decodeXml(locationMatch[1].trim());

    if (!name || !location) {
      continue;
    }

    entries.push({ name, description, location });
  }

  return entries;
}

export function parseAllSkillPromptSections(
  prompt: string,
): SkillPromptSection[] {
  const sections: SkillPromptSection[] = [];
  let searchStart = 0;

  while (searchStart < prompt.length) {
    const start = prompt.indexOf(AVAILABLE_SKILLS_OPEN_TAG, searchStart);
    if (start === -1) {
      break;
    }

    const closeStart = prompt.indexOf(
      AVAILABLE_SKILLS_CLOSE_TAG,
      start + AVAILABLE_SKILLS_OPEN_TAG.length,
    );
    if (closeStart === -1) {
      break;
    }

    const end = closeStart + AVAILABLE_SKILLS_CLOSE_TAG.length;
    const sectionBody = prompt.slice(
      start + AVAILABLE_SKILLS_OPEN_TAG.length,
      closeStart,
    );
    sections.push({
      entries: parseSkillEntries(sectionBody),
    });
    searchStart = end;
  }

  return sections;
}

function resolvePermissionState(
  skillName: string,
  permissionManager: SkillPermissionChecker,
  agentName: string | null,
  cache: Map<string, PermissionState>,
): PermissionState {
  const cachedState = cache.get(skillName);
  if (cachedState) {
    return cachedState;
  }

  const state = permissionManager.checkPermission(
    "skill",
    { name: skillName },
    agentName ?? undefined,
  ).state;
  cache.set(skillName, state);
  return state;
}

function createResolvedSkillEntry(
  entry: ParsedSkillPromptEntry,
  state: PermissionState,
  normalizer: PathNormalizer,
): SkillPromptEntry {
  return {
    name: entry.name,
    description: entry.description,
    location: entry.location,
    state,
    normalizedLocation: normalizer.comparableValue(entry.location),
    normalizedBaseDir: normalizer.comparableValue(dirname(entry.location)),
  };
}

/**
 * The skills listed in the prompt's `<available_skills>` catalogues that
 * policy does not deny, in catalogue order: what skill path matching may
 * treat as a skill's files. Edits no prompt text.
 */
export function visibleSkillPromptEntries(
  prompt: string,
  permissionManager: SkillPermissionChecker,
  agentName: string | null,
  normalizer: PathNormalizer,
): SkillPromptEntry[] {
  const permissionCache = new Map<string, PermissionState>();
  return parseAllSkillPromptSections(prompt)
    .flatMap((section) => section.entries)
    .map((entry) =>
      createResolvedSkillEntry(
        entry,
        resolvePermissionState(
          entry.name,
          permissionManager,
          agentName,
          permissionCache,
        ),
        normalizer,
      ),
    )
    .filter((entry) => entry.state !== "deny");
}

/**
 * The skills policy does not deny, judged by name, in their original order.
 *
 * Judging the skill list itself, rather than a catalogue rendered from it,
 * keeps the answer independent of whether that catalogue has been rendered yet.
 */
export function withoutDeniedSkills<T extends { readonly name: string }>(
  skills: readonly T[],
  permissionManager: SkillPermissionChecker,
  agentName: string | null,
): T[] {
  const permissionCache = new Map<string, PermissionState>();
  return skills.filter(
    (skill) =>
      resolvePermissionState(
        skill.name,
        permissionManager,
        agentName,
        permissionCache,
      ) !== "deny",
  );
}

export function findSkillPathMatch(
  normalizedPath: string,
  entries: readonly SkillPromptEntry[],
  normalizer: PathNormalizer,
): SkillPromptEntry | null {
  if (!normalizedPath || entries.length === 0) {
    return null;
  }

  for (const entry of entries) {
    if (
      entry.normalizedLocation &&
      normalizedPath === entry.normalizedLocation
    ) {
      return entry;
    }
  }

  let bestMatch: SkillPromptEntry | null = null;
  for (const entry of entries) {
    if (
      !entry.normalizedBaseDir ||
      !normalizer.isWithinDirectory(normalizedPath, entry.normalizedBaseDir)
    ) {
      continue;
    }

    if (
      !bestMatch ||
      entry.normalizedBaseDir.length > bestMatch.normalizedBaseDir.length
    ) {
      bestMatch = entry;
    }
  }

  return bestMatch;
}
