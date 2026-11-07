import { mkdirSync, mkdtempSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const { resolveFileTarget } = await import("./src/files.ts");
const base = realpathSync(mkdtempSync(join(tmpdir(), "rvres-")));
mkdirSync(join(base, "d")); writeFileSync(join(base, "d", "f.md"), "x"); writeFileSync(join(base, "file"), "x");
mkdirSync(join(base, "sub"));
symlinkSync("d", join(base, "link-d")); symlinkSync("d/f.md", join(base, "link-f")); symlinkSync("missing/new.md", join(base, "dangling"));
symlinkSync("cyc2", join(base, "cyc1")); symlinkSync("cyc1", join(base, "cyc2")); symlinkSync("link-d/../d/f.md", join(base, "up"));
symlinkSync("../link-d/f.md", join(base, "sub", "back")); symlinkSync(join(base, "d") + "/", join(base, "abs-d-slash"));
symlinkSync("loopdir", join(base, "loopdir"));
const inputs = ["d/f.md", "link-f", "link-d/f.md", "link-d/new.md", "dangling", "cyc1", "file/x.md", "file/../d/f.md", "d/", "link-d/", "missingdir/x.md", "sub/back", "d/f.md/", "up", "abs-d-slash", "loopdir/x.md", "d/./f.md", "d//f.md", "link-d/../file"];
const out: string[][] = [];
for (const rel of inputs) {
	try { out.push([rel, (await resolveFileTarget(join(base, rel).replace(/\/$/, "") + (rel.endsWith("/") ? "/" : ""))).replace(base, "<base>")]); }
	catch (e: any) { out.push([rel, "ERR:" + String(e.message).replaceAll(base, "<base>") + "|" + e.code]); }
}
console.log(JSON.stringify(out));
