import { execCommand } from "@getpaseo/plugin/server";
import { runAcpProvider } from "@getpaseo/plugin/server/acp";
import type { ProviderRegistration } from "@getpaseo/plugin/server/provider";
import { checkPigInstall } from "./install.js";
import { pigToolCalls } from "./tool-calls.js";

// The scaffold's tsconfig carries no Node types; the daemon runs this entry in Node.
declare const process: { env: Record<string, string | undefined> };

export const PIG_PROVIDER_ID = "pig";

/**
 * PiG as a Paseo provider: pig-acp speaks ACP to Paseo and starts one `pig --mode rpc` child per session.
 * A user points it elsewhere with agents.providers.pig.command, for example ["pig-acp", "--pig", "/opt/pig/pig"].
 */
export function createPigProvider(): ProviderRegistration {
  const acp = runAcpProvider({
    id: PIG_PROVIDER_ID,
    label: "PiG",
    description: "PiG, a faithful Go implementation of Pi, through pig-acp",
    icon: "icon.svg",
    command: ["pig-acp"],
    transformers: [pigToolCalls],
  });
  return {
    ...acp,
    async status(request) {
      const launch = request.launch ?? { command: "pig-acp", args: [], env: Object.fromEntries(Object.entries(process.env).filter((entry): entry is [string, string] => entry[1] !== undefined)) };
      return checkPigInstall((command, args, options) => execCommand(command, args, options), launch);
    },
  };
}
