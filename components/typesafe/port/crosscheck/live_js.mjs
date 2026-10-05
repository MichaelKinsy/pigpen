// Live companion of libraries/typesafe/live_test.go: runs the same calls through the official SDK.
// Needs TYPESAFE_API_KEY (the owner supplies it). Usage: node live_js.mjs <dist/index.mjs>
import { pathToFileURL } from "node:url";
const sdk = await import(pathToFileURL(process.argv[2]).href);
const client = new sdk.TypeSafeClient({ logLevel: "off" });
const models = await client.models.list();
const res = await client.systemOne({
  state: "I was charged twice for my subscription. Please refund one payment.",
  questions: {
    billing: sdk.noul("Is this about billing?"),
    kind: sdk.choice("What does the customer want?", { refund: "a refund", cancel: "to cancel", other: null }),
    urgency: sdk.score("How urgent is it?", ["not urgent", "somewhat urgent", "urgent"]),
  },
});
process.stdout.write(JSON.stringify({ models, result: res }) + "\n");
