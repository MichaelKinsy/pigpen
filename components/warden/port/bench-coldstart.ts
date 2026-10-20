// Process start to registration of the ORIGINAL: loads the extension's source with Pi's jiti and calls its factory
// against a pi object that accepts every registration. Compare with the Go probe (a main that builds warden.Extension()).
//   node --import ./tests/setup-env.ts <pi>/node_modules/jiti/lib/jiti-cli.mjs ../bench-coldstart.ts   (from port/oracle)
import extension from "./oracle/src/extension.ts";

const pi = new Proxy({}, { get: () => () => undefined });
(extension as (pi: unknown) => void)(pi);
