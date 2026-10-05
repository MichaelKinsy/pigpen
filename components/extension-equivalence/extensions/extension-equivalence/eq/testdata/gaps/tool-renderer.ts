// Gap-check fixture: an extension that draws calls to MCP tools before their server connected, with
// pi.registerToolRenderer (Pi 1.0.1, types.d.ts:499-504 and 1219; docs/extensions.md:190). The Go SDK
// implements it as Extension.ToolRenderer (PiG 0.4.1, D89), so `pigeq gaps` must not report it.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Text } from "@earendil-works/pi-tui";

export default function (pi: ExtensionAPI) {
	pi.registerToolRenderer((toolName, next) => {
		if (!toolName.startsWith("mcp_")) return next();
		return (
			next() ?? {
				renderShell: "self",
				renderCall: (args, theme) => new Text(theme.bold(toolName) + " " + JSON.stringify(args), 0, 0),
			}
		);
	});
}
