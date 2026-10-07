import { getAgentDir } from "@earendil-works/pi-coding-agent";
import { join } from "node:path";

/**
 * Directory that holds pi's agent state (settings.json, sessions, ...).
 * Honors PI_CODING_AGENT_DIR and falls back to ~/.pi/agent.
 */
export { getAgentDir };

/** Absolute path to a file inside the pi agent directory. */
export function agentPath(...segments: string[]): string {
	return join(getAgentDir(), ...segments);
}
