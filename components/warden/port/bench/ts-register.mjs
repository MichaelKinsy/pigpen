// Lets plain Node run the original's TypeScript: `./x.js` specifiers resolve to `./x.ts` (the original imports with .js).
import { register } from "node:module";
register("./ts-resolve.mjs", import.meta.url);
