import type { PluginServerContext } from "@getpaseo/plugin/server";
import { createPigProvider } from "./server/provider.js";

export default function contribute(server: Pick<PluginServerContext, "registerProvider">) {
  server.registerProvider(createPigProvider());
  return () => {};
}
