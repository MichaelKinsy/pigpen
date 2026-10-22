// Writes cases.json: the inputs both the original (drive.mjs) and the Go port (diff_test.go) are run on.
// Deterministic: a fixed seed, so the file is reproducible and reviewable. Usage: node gen-cases.mjs > cases.json
let seed = 0x5eed1234;
const rand = () => {
	seed = (seed + 0x6d2b79f5) | 0;
	let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
	t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (list) => list[Math.floor(rand() * list.length)];
const int = (lo, hi) => lo + Math.floor(rand() * (hi - lo + 1));

const MARKS = ["{{", "}}", "|", "{{", "}}", "|"];
const CODE = ["`", "``", "```", "~~~", "\n```\n", "\n~~~\n", "`code`", "``a`b``", "```js\n{{x|y}}\n```", "  ```", "\t~~~~"];
const MATH = ["$", "$$", "\\(", "\\)", "\\[", "\\]", "$x+y$", "$$a b$$", "\\(a b\\)", "$ 5 and $6", "\\[ x \\]"];
const WORDS = ["The", "build", "fails", "on", "a", "stale", "cache.", "CI", "run,", "key", "package.json", "**bold**", "_it_", "x=1;", "(paren)", "“quote”", "don't", "Zażółć", "gęślą", "jaźń", "ΟΔΟΣ", "οδος", "İstanbul", "ß", "ǅ", "Ⅷ", "½", "x²"];
const SCRIPTS = ["中文", "，", "。", "（", "）", "漢字かな", "ひらがな", "カタカナ", "ー", "ゃ", "한국어", "😀", "👩‍💻", "é", "e\u0301", "\u200d", "\ufe0f", "\u00ad", "ﬁ", "،", "عربى"];
const SEGMENTED = ["ก", "ไทย", "ภาษาไทย", "ລາວ", "ខ្មែរ", "မြန်မာ"];
const SPACE = [" ", " ", " ", " ", "  ", "\n", "\t", "\u00a0", "\u3000", "\u2028", "\ufeff", "\u0085", "\r\n"];
const frags = () => [...WORDS, ...WORDS, ...MARKS, ...SPACE, ...SPACE, ...CODE, ...MATH, ...SCRIPTS, ...SEGMENTED];
const FRAGS = frags();
const text = (n) => Array.from({ length: n }, () => pick(FRAGS)).join("");

const PARSE_HAND = [
	"", "plain", "{{a|b}}", "{{a}}", "{{a|b", "{{a", "{{", "}}", "|", "{{a|{{b|c}}}}", "{{a|{{b|c}}d}}e", "{{x|1}}{{x|2}}{{x|3}}",
	"`{{a|b}}`", "``{{a|b}}``", "``` \n{{a|b}}\n```\n{{c|d}}", "~~~\n{{a|b}}\n~~~~~\n{{c|d}}", "```\nunclosed {{a|b}}", "{{a|b|c}}", "{{a\nb|long}}", "{{ a | b }}",
	"{{a|{{a|x}}{{a|y}}}}", "{{a|b}}}}", "{{{a|b}}}", "a }} b | c", "{{a|`}}`}}", "{{a|`x}}", "{{`a|b`|c}}", "x\n```\n{{a|b}}", "  ```\n{{a|b}}\n  ```", "{{a{{b|c}}|d}}",
	"{{中文|中文较长的版本}}", "{{a|b}} and {{c|{{d|e}}}} and {{a|f}}", "{{ \u00a0a\ufeff|b}}", "{{a|b}}\r\n```\r\n{{c|d}}\r\n```\r\n",
];
const parse = [...PARSE_HAND];
for (let i = 0; i < 3500; i++) parse.push(text(int(1, 30)));
// Mostly well-formed: every stretch closes, so nesting and keys get exercised.
const inner = (depth) => {
	const parts = [];
	for (let i = int(1, 6); i > 0; i--) {
		const r = rand();
		if (r < 0.35 && depth < 4) parts.push(`{{${text(int(1, 3))}|${inner(depth + 1)}}}`);
		else if (r < 0.5) parts.push(`{{${pick(WORDS)}}}`);
		else parts.push(pick([...WORDS, ...SPACE, ...SCRIPTS, ...CODE.slice(0, 3)]));
	}
	return parts.join(pick([" ", "", " "]));
};
for (let i = 0; i < 1500; i++) parse.push(inner(0));

const segmentedGrow = [];
const GROW_HAND = [
	["The build fails on a stale cache.", "The CI build fails, about one run in five, on a stale dependency cache keyed only by `package.json`."],
	["", "all new"], ["short", ""], ["a b", "x a y b z"], ["a b", "b a"], ["a b c", "a x b"], ["中文", "这是一个中文长句子，包含中文。"], ["Hi", "Hi, there."],
	["cache", "**cache**."], ["cache", "{{cache|nested cache}}"], ["see $x$", "we see $x + y$ here"], ["Σ", "ΟΔΟΣ ς σ"],
	["ไทย", "ภาษาไทยเขียนติดกัน ไทย"], ["x", "x"], ["a", "x a"], ["a", "a x"], ["a b", "a   b"], ["a b", " a x b "],
];
const grow = GROW_HAND.map(([short, long]) => ({ short, long }));
for (let i = 0; i < 2200; i++) {
	const words = Array.from({ length: int(1, 4) }, () => pick([...WORDS, ...SCRIPTS]));
	const short = words.join(rand() < 0.8 ? " " : "");
	let longParts = [];
	const around = () => Array.from({ length: int(0, 4) }, () => pick([...WORDS, ...SCRIPTS, ...SPACE.slice(0, 4)])).join(" ");
	if (rand() < 0.75) for (const w of words) longParts.push(around(), w);
	else longParts = Array.from({ length: int(1, 6) }, () => pick([...WORDS, ...SCRIPTS]));
	longParts.push(around());
	if (rand() < 0.2) longParts.splice(int(0, longParts.length), 0, `{{${pick(WORDS)}|${around()}}}`);
	if (rand() < 0.1) longParts.splice(int(0, longParts.length), 0, pick([...CODE.slice(0, 3), ...MATH]));
	grow.push({ short, long: longParts.join(rand() < 0.9 ? " " : "") });
}
for (let i = 0; i < 120; i++) {
	const word = pick(SEGMENTED);
	grow.push({ short: word, long: `${pick(WORDS)} ${word} ${pick(SEGMENTED)}${pick(SEGMENTED)} ${pick(WORDS)}` });
}
const PROGRESS = [0, 0.1, 0.2, 0.25, 0.4, 0.5, 0.6, 0.75, 0.9, 1];

const themes = {
	darkTrue: { name: "dark", appearance: "dark", mode: "truecolor", colors: { warning: { kind: "rgb", r: 255, g: 200, b: 0 }, accent: { kind: "rgb", r: 138, g: 190, b: 183 }, success: { kind: "rgb", r: 181, g: 189, b: 104 }, mdLink: { kind: "rgb", r: 129, g: 162, b: 190 } } },
	lightTrue: { name: "light", appearance: "light", mode: "truecolor", colors: { warning: { kind: "rgb", r: 154, g: 126, b: 10 }, accent: { kind: "rgb", r: 90, g: 128, b: 128 }, success: { kind: "rgb", r: 88, g: 110, b: 20 }, mdLink: { kind: "rgb", r: 84, g: 105, b: 140 } } },
	dark256: { name: "dark256", appearance: "dark", mode: "256color", colors: { warning: { kind: "rgb", r: 255, g: 200, b: 0 }, accent: { kind: "indexed", index: 109 }, success: { kind: "indexed", index: 3 }, mdLink: { kind: "oklch", l: 0.7, c: 0.1, h: 250 } } },
	oklch: { name: "oklch", appearance: "light", mode: "truecolor", colors: { warning: { kind: "oklch", l: 0.8, c: 0.3, h: 90 }, accent: { kind: "oklch", l: 0.5, c: 0.15, h: 300 }, success: { kind: "oklch", l: 1, c: 0.3, h: 150 }, mdLink: { kind: "indexed", index: 244 } } },
};

const R1 = "The build fails on a stale cache. {{Euler was a mathematician|Leonhard Euler was a {{Swiss|Swiss-born}} mathematician who worked in {{Basel|Basel and Berlin}} on {{graph theory|graph theory, the Basel problem}}}}. {{Short|Short one}} and {{Short|Short two}}.";
const R2 = "Plain answer with `{{code|span}}` and {{real|a real stretch}}.\n\n```\n{{fenced|block}}\n```\n";
const R3 = "{{a\nb|multi-line short form}} {{c|d}}";
const R4 = "No stretches here.";
const REPLIES = Array.from({ length: 5 }, (_, i) => `{{Reply ${i}|Reply number ${i}, in long form}} tail ${i}`);
const sessions = [
	{ name: "closed-render-no-theme", steps: [{ op: "transform", md: R1 }, { op: "transform", md: R2 }, { op: "transform", md: R3 }, { op: "transform", md: R4 }, { op: "transform", md: R1, type: "user" }, { op: "transform", md: R1, type: "assistant-thinking" }] },
	{ name: "click-open-animate-settle", theme: "darkTrue", steps: [{ op: "session_start" }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 0 }, { op: "transform", md: R1 }, { op: "tick", ms: 30 }, { op: "transform", md: R1 }, { op: "tick", ms: 200 }, { op: "transform", md: R1 }, { op: "tick", ms: 2000 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 1 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }] },
	{ name: "open-nested-hues-and-close", theme: "lightTrue", steps: [{ op: "session_start" }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 0 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 1 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 2 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 3 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 4 }, { op: "tick", ms: 100 }, { op: "transform", md: R1 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 3 }, { op: "tick", ms: 500 }, { op: "transform", md: R1 }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }] },
	{ name: "toggle-during-animation", theme: "dark256", steps: [{ op: "session_start" }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 0 }, { op: "tick", ms: 90 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 1 }, { op: "tick", ms: 60 }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 0 }, { op: "tick", ms: 90 }, { op: "transform", md: R1 }, { op: "tick", ms: 4000 }, { op: "transform", md: R1 }, { op: "shutdown" }, { op: "transform", md: R1 }] },
	{ name: "letters-open-and-close", theme: "oklch", steps: [{ op: "session_start" }, { op: "transform", md: R1 }, { op: "transform", md: R2 }, { op: "alt", key: "alt+c" }, { op: "alt", key: "alt+o" }, { op: "transform", md: R1 }, { op: "transform", md: R2 }, { op: "input", data: "x" }, { op: "transform", md: R1 }, { op: "alt", key: "alt+o" }, { op: "input", data: "s" }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "transform", md: R2 }, { op: "alt", key: "alt+c" }, { op: "transform", md: R1 }, { op: "transform", md: R2 }, { op: "input", data: "a" }, { op: "tick", ms: 3000 }, { op: "transform", md: R1 }, { op: "alt", key: "alt+o" }, { op: "input", data: "\u001b" }, { op: "transform", md: R1 }] },
	{ name: "multi-line-short-open-by-default", theme: "darkTrue", steps: [{ op: "session_start" }, { op: "transform", md: R3 }, { op: "alt", key: "alt+o" }, { op: "transform", md: R3 }, { op: "input", data: "a" }, { op: "tick", ms: 3000 }, { op: "transform", md: R3 }] },
	{ name: "lettering-window-of-three-replies", theme: "darkTrue", steps: [{ op: "session_start" }, ...REPLIES.map((md) => ({ op: "transform", md })), { op: "alt", key: "alt+o" }, ...REPLIES.map((md) => ({ op: "transform", md })), { op: "input", data: "a" }, { op: "tick", ms: 3000 }, ...REPLIES.map((md) => ({ op: "transform", md })), { op: "alt", key: "alt+c" }, ...REPLIES.map((md) => ({ op: "transform", md }))] },
	{ name: "read-notes", theme: "darkTrue", steps: [{ op: "session_start" }, { op: "before_agent_start" }, { op: "transform", md: R1 }, { op: "transform", md: R2 }, { op: "before_agent_start" }, { op: "before_agent_start" }, { op: "transform", md: R1 }, { op: "click", md: R1, link: 0 }, { op: "tick", ms: 3000 }, { op: "click", md: R1, link: 1 }, { op: "tick", ms: 3000 }, { op: "before_agent_start" }, { op: "before_agent_start" }, { op: "transform", md: R4 }, { op: "before_agent_start" }, { op: "transform", md: R3 }, { op: "before_agent_start" }] },
	{ name: "streaming-prefixes", theme: "darkTrue", steps: [{ op: "session_start" }, ...[...R2].map((_, i) => i).filter((i) => i % 7 === 0).map((i) => ({ op: "transform", md: R2.slice(0, i) })), { op: "transform", md: R2 }, { op: "alt", key: "alt+o" }, { op: "transform", md: R2 }, { op: "input", data: "a" }, { op: "before_agent_start" }, { op: "before_agent_start" }] },
	{ name: "non-link-clicks", theme: "darkTrue", steps: [{ op: "session_start" }, { op: "transform", md: R1 }, { op: "click", md: R1, link: -1 }, { op: "click", md: R1, link: 99 }, { op: "click", md: R1, link: 0, type: "press" }, { op: "tick", ms: 100 }, { op: "transform", md: R1 }] },
	{ name: "unclosed-stretches", theme: "darkTrue", steps: [{ op: "session_start" }, { op: "transform", md: "{{a|b" }, { op: "transform", md: "x {{a|b {{c|d}} " }, { op: "transform", md: "{{a" }, { op: "alt", key: "alt+o" }, { op: "alt", key: "alt+c" }] },
];
console.log(JSON.stringify({ parse, grow, progress: PROGRESS, themes, sessions }));
