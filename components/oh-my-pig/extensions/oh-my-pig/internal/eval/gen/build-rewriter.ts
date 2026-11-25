#!/usr/bin/env bun
// Generator for oh-my-pig's embedded omp JS cell rewriter asset.
//
// Bundles omp's unmodified JS cell pipeline from the pinned v19.1.1 export
// ($OMP_SRC) together with @babel/parser@7.29.9 (as pinned
// in omp's bun.lock) into a single CommonJS file loadable under both Bun and
// Node.js 22+, and writes a SHA-256 manifest next to it.
//
// Usage:
//   bun run internal/eval/gen/build-rewriter.ts [--omp-src <path>] [--babel-node-modules <path>] [--out-dir <path>]
//
// Environment overrides:
//   OMP_SRC             path to omp v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a) source (required unless --omp-src is passed)
//   BABEL_NODE_MODULES  node_modules dir containing @babel/parser@7.29.9

import * as crypto from "node:crypto";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

const PINNED_OMP_TAG = "v19.1.1";
const PINNED_OMP_COMMIT = "31ffee513f27a01bba7d4dfbe12bae239cdc646a";
const PINNED_BABEL_VERSION = "7.29.9";

const OMP_SHARED_REL = "packages/coding-agent/src/eval/js/shared";
const INPUT_REL_FILES = [
	`${OMP_SHARED_REL}/rewrite-imports.ts`,
	`${OMP_SHARED_REL}/indirect-eval.ts`,
	`${OMP_SHARED_REL}/local-module-loader.ts`,
] as const;
function sha256File(filePath: string): string {
	const data = fs.readFileSync(filePath);
	return crypto.createHash("sha256").update(data).digest("hex");
}

function parseArgs(argv: string[]) {
	let ompSrc = process.env.OMP_SRC || "";
	let babelNodeModules = process.env.BABEL_NODE_MODULES || "";
	let outDir = path.resolve(import.meta.dir, "..");
	for (let i = 0; i < argv.length; i++) {
		const arg = argv[i];
		if (arg === "--omp-src" && argv[i + 1]) {
			ompSrc = argv[++i];
		} else if (arg === "--babel-node-modules" && argv[i + 1]) {
			babelNodeModules = argv[++i];
		} else if (arg === "--out-dir" && argv[i + 1]) {
			outDir = path.resolve(argv[++i]);
		}
	}
	if (!ompSrc) {
		console.error(`OMP_SRC is not set; set OMP_SRC (or pass --omp-src) to omp tag ${PINNED_OMP_TAG} (commit ${PINNED_OMP_COMMIT})`);
		process.exit(1);
	}
	return { ompSrc: path.resolve(ompSrc), babelNodeModules, outDir };
}

function findBabelNodeModules(explicit: string, ompSrc: string): string {
	const candidates = [
		explicit,
		path.join(ompSrc, "node_modules"),
	].filter(Boolean);
	for (const dir of candidates) {
		const pkgPath = path.join(dir, "@babel", "parser", "package.json");
		if (!fs.existsSync(pkgPath)) continue;
		const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf8")) as { version?: string };
		if (pkg.version === PINNED_BABEL_VERSION) {
			return path.resolve(dir);
		}
	}
	throw new Error(
		`Could not locate @babel/parser@${PINNED_BABEL_VERSION} in any candidate node_modules (${candidates.join(", ") || "none"}). Pass --babel-node-modules or set BABEL_NODE_MODULES.`,
	);
}

async function main() {
	const { ompSrc, babelNodeModules: explicitBabel, outDir } = parseArgs(process.argv.slice(2));

	// Verify pinned omp source files exist.
	for (const rel of INPUT_REL_FILES) {
		const full = path.join(ompSrc, rel);
		if (!fs.existsSync(full)) {
			throw new Error(`Missing omp source file: ${full}`);
		}
	}

	// Verify omp bun.lock pins @babel/parser@7.29.9 when present.
	const lockPath = path.join(ompSrc, "bun.lock");
	if (fs.existsSync(lockPath)) {
		const lockText = fs.readFileSync(lockPath, "utf8");
		if (!lockText.includes(`"@babel/parser@${PINNED_BABEL_VERSION}"`)) {
			throw new Error(`Expected ${lockPath} to pin @babel/parser@${PINNED_BABEL_VERSION}`);
		}
	}

	const babelNodeModules = findBabelNodeModules(explicitBabel, ompSrc);
	const babelEntryPath = path.join(babelNodeModules, "@babel", "parser", "lib", "index.js");
	const babelPkgPath = path.join(babelNodeModules, "@babel", "parser", "package.json");
	if (!fs.existsSync(babelEntryPath)) {
		throw new Error(`Missing @babel/parser entry at ${babelEntryPath}`);
	}

	// Build deterministic entrypoint in a temporary directory.
	const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "omp-rewriter-gen-"));
	try {
		const rewriteImportsPath = path.join(ompSrc, `${OMP_SHARED_REL}/rewrite-imports.ts`);
		const indirectEvalPath = path.join(ompSrc, `${OMP_SHARED_REL}/indirect-eval.ts`);
		const localModuleLoaderPath = path.join(ompSrc, `${OMP_SHARED_REL}/local-module-loader.ts`);

		const entryPath = path.join(tmpDir, "entry.ts");
		const entrySource = [
			`export {`,
			`\twrapCode,`,
			`\tdiagnoseCellSyntaxError,`,
			`\trewriteImports,`,
			`\trewriteDynamicImports,`,
			`\tstripTypeScriptSyntax,`,
			`\tcollectModuleSourceSpecifiers,`,
			`\trewriteModuleSourceSpecifiers,`,
			`\tloadBabelParser,`,
			`} from ${JSON.stringify(rewriteImportsPath)};`,
			`export {`,
			`\tindirectEval,`,
			`\tawaitMaybePromise,`,
			`} from ${JSON.stringify(indirectEvalPath)};`,
			`export {`,
			`\tLocalModuleLoader,`,
			`} from ${JSON.stringify(localModuleLoaderPath)};`,
			``,
		].join("\n");
		fs.writeFileSync(entryPath, entrySource, "utf8");

		const outAssetPath = path.join(outDir, "js_rewriter.cjs");
		const outManifestPath = path.join(outDir, "js_rewriter.manifest.json");
		fs.mkdirSync(outDir, { recursive: true });

		const builtPath = path.join(tmpDir, "js_rewriter.cjs");
		const proc = Bun.spawnSync(
			[
				process.execPath,
				"build",
				entryPath,
				"--target=node",
				"--format=cjs",
				`--outfile=${builtPath}`,
			],
			{
				env: {
					...process.env,
					NODE_PATH: babelNodeModules,
				},
				stdout: "pipe",
				stderr: "pipe",
			},
		);
		if (proc.exitCode !== 0) {
			const stderr = new TextDecoder().decode(proc.stderr);
			const stdout = new TextDecoder().decode(proc.stdout);
			throw new Error(`bun build failed (exit ${proc.exitCode}):\n${stdout}\n${stderr}`);
		}

		// Normalize bundler module-header comments so regeneration produces
		// identical bytes regardless of tmpDir or workspace mount path.
		const rawBundle = fs
			.readFileSync(builtPath, "utf8")
			.replace(/^\/\/ [^\n]*\/@babel\/parser\/lib\/index\.js$/gm, `// @babel/parser@${PINNED_BABEL_VERSION}/lib/index.js`)
			.replace(/^\/\/ [^\n]*\/omp-rewriter-gen-[^/\n]+\/entry\.ts$/gm, "// internal/eval/gen/entry.ts")
			.replace(
				/^\/\/ [^\n]*\/(packages\/coding-agent\/src\/eval\/js\/shared\/[^\n]+)$/gm,
				`// omp@${PINNED_OMP_TAG}/$1`,
			);
		const header = [
			"// Code generated by internal/eval/gen/build-rewriter.ts; DO NOT EDIT.",
			`// Upstream: oh-my-pi ${PINNED_OMP_TAG} (${PINNED_OMP_COMMIT}) + @babel/parser@${PINNED_BABEL_VERSION}`,
			`// Bundler: bun ${Bun.version} (--target=node --format=cjs, unminified)`,
			"",
		].join("\n");
		const finalContent = header + rawBundle;
		fs.writeFileSync(outAssetPath, finalContent, "utf8");

		const inputs = [
			...INPUT_REL_FILES.map(rel => ({
				path: rel,
				sha256: sha256File(path.join(ompSrc, rel)),
			})),
			{
				path: `@babel/parser@${PINNED_BABEL_VERSION}/package.json`,
				sha256: sha256File(babelPkgPath),
			},
			{
				path: `@babel/parser@${PINNED_BABEL_VERSION}/lib/index.js`,
				sha256: sha256File(babelEntryPath),
			},
		];

		const outputBytes = Buffer.byteLength(finalContent, "utf8");
		const outputSha256 = crypto.createHash("sha256").update(finalContent).digest("hex");

		const manifest = {
			generator: "internal/eval/gen/build-rewriter.ts",
			ompTag: PINNED_OMP_TAG,
			ompCommit: PINNED_OMP_COMMIT,
			babelParserVersion: PINNED_BABEL_VERSION,
			bunVersion: Bun.version,
			output: {
				file: "js_rewriter.cjs",
				bytes: outputBytes,
				sha256: outputSha256,
			},
			inputs,
		};
		fs.writeFileSync(outManifestPath, JSON.stringify(manifest, null, 2) + "\n", "utf8");

		console.log(`Generated ${outAssetPath} (${outputBytes} bytes, sha256=${outputSha256})`);
		console.log(`Manifest  ${outManifestPath}`);
	} finally {
		fs.rmSync(tmpDir, { recursive: true, force: true });
	}
}

await main();
