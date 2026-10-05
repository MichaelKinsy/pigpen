import { truncateToWidth as sdkTruncate, wrapTextWithAnsi as sdkWrap } from "@earendil-works/pi-tui";

// Cache pure SDK text transforms, keyed by complete ANSI content and layout arguments.
// Theme changes naturally miss. visibleWidth already has a cache in the supported SDK.
const cache = new Map<string, {value: string | string[]; chars: number}>();
let retainedChars = 0;
function memo<T extends string | string[]>(key: string, build: () => T): T {
	const cached = cache.get(key);
	if (cached) return cached.value as T;
	const value = build();
	const chars = key.length + (typeof value === "string" ? value.length : value.reduce((n, line) => n + line.length, 0));
	if (chars <= 500_000) {
		while (cache.size >= 1024 || retainedChars + chars > 500_000) {
			const oldest = cache.keys().next().value!;
			retainedChars -= cache.get(oldest)!.chars;
			cache.delete(oldest);
		}
		cache.set(key, {value, chars}); retainedChars += chars;
	}
	return value;
}

export function truncateToWidth(text: string, width: number, ellipsis = "...", pad = false): string {
	return memo(`t:${width}:${pad}:${ellipsis.length}:${ellipsis}${text}`, () => sdkTruncate(text, width, ellipsis, pad));
}

export function wrapTextWithAnsi(text: string, width: number): string[] {
	// Callers may edit the returned rows; cached rows stay privately owned.
	return memo(`w:${width}:${text}`, () => sdkWrap(text, width)).slice();
}
