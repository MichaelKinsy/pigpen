#!/usr/bin/env bun
// Generator for oh-my-pig's embedded relay-only Chrome extension asset.
//
// Builds omp v19.1.1's `packages/browser-relay` extension from $OMP_SRC in a
// temporary copy after applying `internal/relay/gen/relay-only.patch` (which
// removes the side panel, Teach, schedules, GIF export and their permissions,
// and sets oh-my-pig's native host name, branding, and icon paths).
//
// oh-my-pig generates its own plain toolbar/extension icons (`icons/oh-my-pig-<size>.png`)
// deterministically in code (no external assets, no font dependencies, and omp's icons are
// not shipped).
//
// Because the side panel, Teach, and GIF export are removed, the relay-only
// bundle (`background.js` + `overlay.js`) has zero third-party npm runtime
// dependencies — only omp's own MIT-licensed TypeScript sources and LICENSE.
//
// Outputs (in --out-dir, default internal/relay):
//   extension/<files>                 unstamped extension files embedded via //go:embed
//   extension.manifest.json           generator, omp pin, patch hash, build id, input & output hashes
//
// Usage:
//   bun run internal/relay/gen/build-extension.ts [--omp-src <path>] [--out-dir <path>]
//
// Environment overrides:
//   OMP_SRC   path to omp v19.1.1 (commit 31ffee513f27a01bba7d4dfbe12bae239cdc646a) source

import * as crypto from "node:crypto";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as zlib from "node:zlib";

const PINNED_OMP_TAG = "v19.1.1";
const PINNED_OMP_COMMIT = "31ffee513f27a01bba7d4dfbe12bae239cdc646a";

const PATCH_PATH = path.resolve(import.meta.dir, "relay-only.patch");
const ICON_SIZES = [16, 32, 48, 128] as const;

// Exact upstream input files needed to build the relay-only extension (omp icons excluded).
const INPUT_REL_FILES = [
	"LICENSE",
	"packages/coding-agent/src/tools/browser/relay/protocol.ts",
	"packages/browser-relay/extension/background.ts",
	"packages/browser-relay/extension/debugger-ownership.ts",
	"packages/browser-relay/extension/download-pairing.ts",
	"packages/browser-relay/extension/foreign-frames.ts",
	"packages/browser-relay/extension/manifest.json",
	"packages/browser-relay/extension/messages.ts",
	"packages/browser-relay/extension/worker/calls.ts",
	"packages/browser-relay/extension/worker/groups.ts",
	"packages/browser-relay/extension/worker/identity.ts",
	"packages/browser-relay/extension/worker/overlay-host.ts",
	"packages/browser-relay/extension/worker/relay.ts",
	"packages/browser-relay/extension/worker/relay-events.ts",
	"packages/browser-relay/extension/worker/relay-rpc.ts",
	"packages/browser-relay/extension/worker/session-store.ts",
	"packages/browser-relay/extension/worker/tabs.ts",
	"packages/browser-relay/extension/overlay/chip.ts",
	"packages/browser-relay/extension/overlay/dom.ts",
	"packages/browser-relay/extension/overlay/favicon.ts",
	"packages/browser-relay/extension/overlay/main.ts",
	"packages/browser-relay/extension/overlay/motion.ts",
	"packages/browser-relay/extension/overlay/overlay.ts",
	"packages/browser-relay/extension/overlay/picker.ts",
	"packages/browser-relay/extension/overlay/pointer.ts",
	"packages/browser-relay/extension/overlay/presence.ts",
	"packages/browser-relay/extension/overlay/styles.ts",
] as const;

function sha256(data: Uint8Array | Buffer): string {
	return crypto.createHash("sha256").update(data).digest("hex");
}

// Deterministic CRC-32 table for PNG chunk encoding.
const CRC_TABLE = (() => {
	const table = new Uint32Array(256);
	for (let n = 0; n < 256; n++) {
		let c = n;
		for (let k = 0; k < 8; k++) {
			c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
		}
		table[n] = c >>> 0;
	}
	return table;
})();

function pngChunk(type: string, data: Uint8Array): Buffer {
	const typeBuf = Buffer.from(type, "ascii");
	const chunk = Buffer.allocUnsafe(12 + data.length);
	chunk.writeUInt32BE(data.length, 0);
	typeBuf.copy(chunk, 4);
	Buffer.from(data).copy(chunk, 8);
	let crc = 0xffffffff;
	for (let i = 4; i < 8 + data.length; i++) {
		crc = CRC_TABLE[(crc ^ chunk[i]!) & 0xff]! ^ (crc >>> 8);
	}
	chunk.writeUInt32BE((crc ^ 0xffffffff) >>> 0, 8 + data.length);
	return chunk;
}

/**
 * Render oh-my-pig's own plain icon as a deterministic RGBA PNG: a rounded-square rose badge
 * (#d95b8c with #b63f6f border) carrying a simple geometric snout oval (#fde4ee) and two
 * nostrils (#6c1d3d), visually distinct from omp's π mark. Zero external assets or fonts.
 */
function renderOhMyPigIconPNG(size: number): Buffer {
	const stride = 1 + size * 4;
	const raw = Buffer.alloc(stride * size);
	const half = size / 2;
	const cornerR = size * 0.22;
	const pad = size * 0.06;
	const innerHalf = half - pad - cornerR;

	// Rounded-rectangle signed distance in pixels (negative inside).
	const roundedRectSDF = (px: number, py: number): number => {
		const qx = Math.abs(px - half) - innerHalf;
		const qy = Math.abs(py - half) - innerHalf;
		return Math.hypot(Math.max(qx, 0), Math.max(qy, 0)) + Math.min(Math.max(qx, qy), 0) - cornerR;
	};

	const snoutRx = size * 0.30;
	const snoutRy = size * 0.22;
	const snoutCy = half + size * 0.04;
	const nostrilDx = size * 0.105;
	const nostrilRx = size * 0.052;
	const nostrilRy = size * 0.075;
	const borderWidth = Math.max(1, size * 0.06);

	for (let y = 0; y < size; y++) {
		const rowOff = y * stride;
		raw[rowOff] = 0; // PNG filter type: None
		const py = y + 0.5;
		for (let x = 0; x < size; x++) {
			const px = x + 0.5;
			const dist = roundedRectSDF(px, py);
			if (dist > 0.5) continue;

			// Anti-aliased outer edge alpha.
			const alpha = dist <= -0.5 ? 255 : Math.round((0.5 - dist) * 255);

			// Base rose badge with darker rim.
			let r = 0xd9;
			let g = 0x5b;
			let b = 0x8c;
			if (dist > -borderWidth) {
				r = 0xb6;
				g = 0x3f;
				b = 0x6f;
			}

			// Geometric snout oval in center.
			const snX = (px - half) / snoutRx;
			const snY = (py - snoutCy) / snoutRy;
			if (snX * snX + snY * snY <= 1.0) {
				r = 0xfd;
				g = 0xe4;
				b = 0xee;
				// Two oval nostrils.
				const nlX = (px - (half - nostrilDx)) / nostrilRx;
				const nrX = (px - (half + nostrilDx)) / nostrilRx;
				const nY = (py - snoutCy) / nostrilRy;
				if (nlX * nlX + nY * nY <= 1.0 || nrX * nrX + nY * nY <= 1.0) {
					r = 0x6c;
					g = 0x1d;
					b = 0x3d;
				}
			}

			const off = rowOff + 1 + x * 4;
			raw[off] = r;
			raw[off + 1] = g;
			raw[off + 2] = b;
			raw[off + 3] = alpha;
		}
	}

	const ihdr = Buffer.alloc(13);
	ihdr.writeUInt32BE(size, 0);
	ihdr.writeUInt32BE(size, 4);
	ihdr[8] = 8; // bit depth
	ihdr[9] = 6; // RGBA
	ihdr[10] = 0; // deflate compression
	ihdr[11] = 0; // adaptive filter
	ihdr[12] = 0; // no interlace

	const idat = zlib.deflateSync(raw, { level: 9 });
	const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
	return Buffer.concat([sig, pngChunk("IHDR", ihdr), pngChunk("IDAT", idat), pngChunk("IEND", Buffer.alloc(0))]);
}

function parseArgs(argv: string[]) {
	let ompSrc = process.env.OMP_SRC || "";
	let outDir = path.resolve(import.meta.dir, "..");
	for (let i = 0; i < argv.length; i++) {
		const arg = argv[i];
		if (arg === "--omp-src" && argv[i + 1]) {
			ompSrc = argv[++i];
		} else if (arg === "--out-dir" && argv[i + 1]) {
			outDir = path.resolve(argv[++i]);
		} else {
			throw new Error(`Unknown argument: ${arg}`);
		}
	}
	if (!ompSrc) {
		throw new Error(
			"OMP_SRC is required (pass --omp-src <path> or set OMP_SRC to omp v19.1.1 source directory)",
		);
	}
	return { ompSrc: path.resolve(ompSrc), outDir };
}

/** First 16 hex chars of SHA-256 over each unstamped file's name and bytes, in sorted name order.
 * Matches upstream packages/browser-relay/scripts/build-extension.ts:buildId.
 */
function computeBuildId(extDir: string, relNames: readonly string[]): string {
	const hasher = crypto.createHash("sha256");
	for (const name of relNames) {
		hasher.update(`${name}\0`);
		hasher.update(fs.readFileSync(path.join(extDir, name)));
		hasher.update("\0");
	}
	return hasher.digest("hex").slice(0, 16);
}

async function bundleEntry(sourceDir: string, outDir: string, entry: string, name: string): Promise<void> {
	const result = await Bun.build({
		entrypoints: [path.join(sourceDir, entry)],
		outdir: outDir,
		target: "browser",
		format: "iife",
		naming: { entry: `${name}.[ext]` },
		define: { "process.env.NODE_ENV": JSON.stringify("production") },
	});
	if (!result.success) {
		throw new AggregateError(result.logs, `Failed to bundle ${entry}`);
	}
	const outPath = path.join(outDir, `${name}.js`);
	if (!fs.existsSync(outPath)) {
		throw new Error(`Bundling ${entry} did not produce ${name}.js`);
	}
	// Normalize bundler module-header comments (relative or absolute temp-dir paths)
	// so regeneration is byte-for-byte deterministic regardless of cwd or tmpDir.
	const raw = fs.readFileSync(outPath, "utf8");
	const normalized = raw.replace(
		/^([ \t]*)\/\/ [^\n]*\/(packages\/(?:browser-relay|coding-agent)\/[^\n]+)$/gm,
		`$1// omp@${PINNED_OMP_TAG}/$2`,
	);
	fs.writeFileSync(outPath, normalized, "utf8");
}

async function main() {
	const { ompSrc, outDir } = parseArgs(process.argv.slice(2));

	for (const rel of INPUT_REL_FILES) {
		const full = path.join(ompSrc, rel);
		if (!fs.existsSync(full)) {
			throw new Error(`Missing omp source file: ${full}`);
		}
	}
	if (!fs.existsSync(PATCH_PATH)) {
		throw new Error(`Missing patch file: ${PATCH_PATH}`);
	}

	const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "omp-relay-ext-gen-"));
	try {
		// Copy only the needed files into tmpDir with writable permissions.
		for (const rel of INPUT_REL_FILES) {
			const src = path.join(ompSrc, rel);
			const dst = path.join(tmpDir, rel);
			fs.mkdirSync(path.dirname(dst), { recursive: true });
			fs.copyFileSync(src, dst);
			fs.chmodSync(dst, 0o644);
		}

		// Apply the reviewable unified diff patch.
		const patchProc = Bun.spawnSync(["patch", "-p1", "--forward", "--input", PATCH_PATH], {
			cwd: tmpDir,
			stdout: "pipe",
			stderr: "pipe",
		});
		if (patchProc.exitCode !== 0) {
			const stdout = new TextDecoder().decode(patchProc.stdout);
			const stderr = new TextDecoder().decode(patchProc.stderr);
			throw new Error(`patch failed (exit ${patchProc.exitCode}):\n${stdout}\n${stderr}`);
		}

		const extSrcDir = path.join(tmpDir, "packages/browser-relay/extension");
		const builtDir = path.join(tmpDir, "built");
		fs.mkdirSync(builtDir, { recursive: true });

		// Bundle the two relay-only entrypoints (background service worker + on-demand page overlay).
		await Promise.all([
			bundleEntry(extSrcDir, builtDir, "background.ts", "background"),
			bundleEntry(extSrcDir, builtDir, "overlay/main.ts", "overlay"),
		]);

		// Copy patched manifest.json and upstream MIT LICENSE, and generate oh-my-pig's own PNG icons.
		fs.copyFileSync(path.join(extSrcDir, "manifest.json"), path.join(builtDir, "manifest.json"));
		fs.copyFileSync(path.join(tmpDir, "LICENSE"), path.join(builtDir, "LICENSE"));
		const iconsDir = path.join(builtDir, "icons");
		fs.mkdirSync(iconsDir, { recursive: true });
		for (const size of ICON_SIZES) {
			fs.writeFileSync(path.join(iconsDir, `oh-my-pig-${size}.png`), renderOhMyPigIconPNG(size));
		}

		// Enumerate built files deterministically.
		const outputNames: string[] = [];
		const walk = (dir: string, prefix: string) => {
			for (const entry of fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
				const rel = prefix ? `${prefix}/${entry.name}` : entry.name;
				if (entry.isDirectory()) walk(path.join(dir, entry.name), rel);
				else outputNames.push(rel);
			}
		};
		walk(builtDir, "");
		outputNames.sort();

		// Sanity-check built manifest and bundle.
		const builtManifest = JSON.parse(fs.readFileSync(path.join(builtDir, "manifest.json"), "utf8"));
		if (builtManifest.side_panel || builtManifest.commands) {
			throw new Error("Built manifest still contains side_panel or commands");
		}
		const bgText = fs.readFileSync(path.join(builtDir, "background.js"), "utf8");
		if (bgText.includes("sh.omp.browser")) {
			throw new Error("Built background.js still references sh.omp.browser");
		}
		if (!bgText.includes("dev.pi_in_go.oh_my_pig.browser")) {
			throw new Error("Built background.js missing dev.pi_in_go.oh_my_pig.browser");
		}

		const build = computeBuildId(builtDir, outputNames);

		// Replace outDir/extension cleanly.
		const targetExtDir = path.join(outDir, "extension");
		fs.rmSync(targetExtDir, { recursive: true, force: true });
		fs.mkdirSync(targetExtDir, { recursive: true });
		const outputs = outputNames.map(rel => {
			const src = path.join(builtDir, rel);
			const dst = path.join(targetExtDir, rel);
			fs.mkdirSync(path.dirname(dst), { recursive: true });
			const bytes = fs.readFileSync(src);
			fs.writeFileSync(dst, bytes);
			return {
				file: rel,
				bytes: bytes.byteLength,
				sha256: sha256(bytes),
			};
		});

		const inputs = [
			{
				path: "internal/relay/gen/build-extension.ts",
				sha256: sha256(fs.readFileSync(path.resolve(import.meta.dir, "build-extension.ts"))),
			},
			{
				path: "internal/relay/gen/relay-only.patch",
				sha256: sha256(fs.readFileSync(PATCH_PATH)),
			},
			...INPUT_REL_FILES.map(rel => ({
				path: rel,
				sha256: sha256(fs.readFileSync(path.join(ompSrc, rel))),
			})),
		];

		const manifest = {
			generator: "internal/relay/gen/build-extension.ts",
			ompTag: PINNED_OMP_TAG,
			ompCommit: PINNED_OMP_COMMIT,
			bunVersion: Bun.version,
			build,
			totalBytes: outputs.reduce((sum, item) => sum + item.bytes, 0),
			outputs,
			inputs,
		};
		const manifestPath = path.join(outDir, "extension.manifest.json");
		fs.writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + "\n", "utf8");

		console.log(`Generated ${targetExtDir} (${outputs.length} files, ${manifest.totalBytes} bytes, build=${build})`);
		console.log(`Manifest  ${manifestPath}`);
	} finally {
		fs.rmSync(tmpDir, { recursive: true, force: true });
	}
}

await main();
