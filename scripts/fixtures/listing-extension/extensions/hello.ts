// SPDX-License-Identifier: MIT
// Fixture for scripts/verify-listings.mjs: a tool and a command, no imports, so any Pi-compatible host loads it.
export default function (pi: any) {
  pi.registerTool({
    name: "listing_fixture_hello",
    label: "Listing fixture hello",
    description: "Return a fixed string. Used by the listing verification test only.",
    parameters: { type: "object", properties: {}, additionalProperties: false },
    async execute() {
      return { content: [{ type: "text", text: "hello from the listing fixture" }], details: {} };
    },
  });
  pi.registerCommand("listing-fixture-hello", { description: "Listing fixture command", handler: async () => {} });
}
