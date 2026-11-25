#!/usr/bin/env bun
/**
 * Generates schema.json (embedded into internal/settings) from the pinned omp
 * source tree at tag v19.1.1 (commit 31ffee513f).
 *
 * Usage:
 *   OMP_SRC=/path/to/omp-v19.1.1 bun internal/settings/gen/extract.ts [omp-root] [out.json]
 *
 * Environment / defaults:
 *   OMP_SRC:  path to omp v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a) source (required unless passed as arg 1)
 *   out path: internal/settings/schema.json
 */
import { plugin } from "bun";
import * as fs from "node:fs";
import * as path from "node:path";

const ompRoot = process.argv[2] ?? process.env.OMP_SRC ?? "";
if (!ompRoot) {
	console.error("OMP_SRC is not set; set OMP_SRC (or pass arg 1) to omp tag v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a)");
	process.exit(1);
}
const outPath = process.argv[3] ?? path.resolve(import.meta.dir, "..", "schema.json");

interface PkgInfo {
	dir: string;
	pkg: { name: string; exports?: Record<string, unknown> };
}

const pkgMap: Record<string, PkgInfo> = {};
for (const dir of fs.readdirSync(path.join(ompRoot, "packages"))) {
	const p = path.join(ompRoot, "packages", dir, "package.json");
	if (fs.existsSync(p)) {
		const pkg = JSON.parse(fs.readFileSync(p, "utf8")) as PkgInfo["pkg"];
		pkgMap[pkg.name] = { dir: path.join(ompRoot, "packages", dir), pkg };
	}
}

function resolvePkgExport(info: PkgInfo, subpath: string): string | null {
	const exp = info.pkg.exports;
	if (exp) {
		const key = subpath ? `./${subpath}` : ".";
		const target = exp[key];
		if (!target && subpath) {
			for (const [k, v] of Object.entries(exp)) {
				if (k.endsWith("/*")) {
					const prefix = k.slice(2, -1);
					if (subpath.startsWith(prefix)) {
						const rest = subpath.slice(prefix.length);
						const t =
							typeof v === "string"
								? v
								: ((v as Record<string, unknown>).import ??
									(v as Record<string, unknown>).default);
						if (typeof t === "string") {
							const cand = path.join(info.dir, t.replace("*", rest));
							if (fs.existsSync(cand)) return cand;
							if (fs.existsSync(cand + ".ts")) return cand + ".ts";
							if (fs.existsSync(path.join(cand, "index.ts"))) return path.join(cand, "index.ts");
						}
					}
				}
			}
		}
		if (target) {
			const t =
				typeof target === "string"
					? target
					: ((target as Record<string, unknown>).import ??
						(target as Record<string, unknown>).default);
			if (typeof t === "string") {
				const cand = path.join(info.dir, t);
				if (fs.existsSync(cand)) return cand;
			}
		}
	}
	const base = subpath ? path.join(info.dir, "src", subpath) : path.join(info.dir, "src", "index.ts");
	for (const c of [base, base + ".ts", path.join(base, "index.ts")]) {
		if (fs.existsSync(c) && fs.statSync(c).isFile()) return c;
	}
	return null;
}

plugin({
	name: "omp-workspace",
	setup(build) {
		build.onResolve({ filter: /^@oh-my-pi\// }, args => {
			const parts = args.path.split("/");
			const pkgName = parts.slice(0, 2).join("/");
			const subpath = parts.slice(2).join("/");
			const info = pkgMap[pkgName];
			if (!info) return;
			const resolved = resolvePkgExport(info, subpath);
			if (resolved) return { path: resolved };
		});
	},
});

// Keys currently honoured by oh-my-pig (updated as areas wire settings in).
// See internal/settings/WIRING.md for the per-area wiring list.
const HONOURED_KEYS: Record<string, true> = {
	"ask.timeout": true, // cfg:// approval timeout (and ask tool once wired)
};

interface OmpSettingLike {
	id: string;
	type: string;
	default: unknown;
	enumValues?: readonly string[];
	ui?: { description?: string };
	envName?: string;
	envFallback?: boolean | "blank";
	isCredential?: boolean;
	definition: {
		type: string;
		default: unknown;
		merge?: string;
		items?: { label: string; values: readonly string[] };
		pathScoped?: { valuesKey: string; itemType?: string };
	};
}

// Runtime-selected ompRoot path; plugin() above must register before loading omp modules.
await import(path.join(ompRoot, "packages/coding-agent/src/config/all-settings.ts"));
const reg = (await import(path.join(ompRoot, "packages/coding-agent/src/config/registry.ts"))) as {
	all: () => readonly OmpSettingLike[];
};
const all = reg.all();

interface SchemaEntry {
	id: string;
	type: string;
	default?: unknown;
	hasDefault: boolean;
	enum?: string[];
	description?: string;
	envName?: string;
	envFallback?: string;
	credential?: boolean;
	arrayMerge?: string;
	arrayItems?: { label: string; values: string[] };
	pathScoped?: { valuesKey: string; itemType?: string };
	honoured?: boolean;
}

const entries: SchemaEntry[] = all.map(s => {
	const def = s.definition;
	const entry: SchemaEntry = {
		id: s.id,
		type: s.type,
		hasDefault: def.default !== undefined,
	};
	if (def.default !== undefined) {
		entry.default = s.default;
	}
	if (s.enumValues) {
		entry.enum = [...s.enumValues];
	}
	if (s.ui?.description) {
		entry.description = s.ui.description;
	}
	if (s.envName) {
		entry.envName = s.envName;
	}
	if (s.envFallback === true) {
		entry.envFallback = "true";
	} else if (s.envFallback === "blank") {
		entry.envFallback = "blank";
	}
	if (s.isCredential) {
		entry.credential = true;
	}
	if (def.type === "array" && def.merge) {
		entry.arrayMerge = def.merge;
	}
	if (def.type === "array" && def.items) {
		entry.arrayItems = {
			label: def.items.label,
			values: [...def.items.values],
		};
	}
	if (def.pathScoped) {
		entry.pathScoped = {
			valuesKey: def.pathScoped.valuesKey,
			...(def.pathScoped.itemType ? { itemType: def.pathScoped.itemType } : {}),
		};
	}
	if (HONOURED_KEYS[s.id]) {
		entry.honoured = true;
	}
	return entry;
});

fs.writeFileSync(outPath, JSON.stringify(entries, null, 2) + "\n", "utf8");
console.log(`Wrote ${entries.length} settings to ${outPath}`);
