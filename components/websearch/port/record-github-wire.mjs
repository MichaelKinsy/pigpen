// Records what the ORIGINAL GitHub extractors (github-extract.ts, github-api.ts, github-issue-pr.ts) do
// with fake `gh` and `git` programs: the commands they start (arguments and the environment they set),
// and the documents they return. The same fake programs (shell scripts, written below and stored in
// the fixture) are replayed against the Go port by extensions/websearch/github_wire_test.go, which
// compares command for command and document for document. api.github.com is a fixed endpoint, so
// the REST fallback is recorded with `globalThis.fetch` replaced by a recorder.
//
//   cd components/websearch/port && node record-github-wire.mjs          # rewrites extensions/websearch/testdata/github-wire.json
//   node record-github-wire.mjs --check                                   # fails when the recording differs
//
// Run it with the oracle's node_modules in place (port/oracle/node_modules) and Node 22.6+, on a
// host with /bin/sh, head, tr, yes, base64 and ln. The recording is deterministic apart from the
// random runtime directory names, which are normalized.
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const oracle = join(here, "oracle");
const outFile = join(here, "..", "extensions", "websearch", "testdata", "github-wire.json");

// ---- the fake programs ------------------------------------------------------------------------------

const sq = (s) => `'${String(s).replaceAll("'", `'\\''`)}'`;
// One printf per log line: concurrent programs append whole lines.
const logArgs = (name) => `line=${sq(name)}; for a in "$@"; do line="$line$(printf '\\t')$a"; done; printf '%s\\n' "$line" >> "$SHIM_LOG"`;
const logEnv = `printf 'env\\t%s\\t%s\\t%s\\n' "$GIT_TERMINAL_PROMPT" "$GCM_INTERACTIVE" "$GH_PROMPT_DISABLED" >> "$SHIM_LOG"`;
const out = (s, code = 0) => `printf '%s' ${sq(s)}\nexit ${code}`;
const outSh = (cmd, code = 0) => `${cmd}\nexit ${code}`;
const fail = (stderr = "", code = 1) => `printf '%s' ${sq(stderr)} >&2\nexit ${code}`;

// materialize writes a fake repository into "$dest". A value is text, {bytes: "\\001\\000"} (printf octal escapes),
// {gen: "sh producing the bytes on stdout"}, {link: target} or {dir: true}.
function materialize(tree) {
	const lines = [`mkdir -p "$dest"`];
	for (const [path, spec] of Object.entries(tree)) {
		const target = `"$dest/${path}"`;
		const parent = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
		if (parent) lines.push(`mkdir -p "$dest/${parent}"`);
		if (typeof spec === "string") lines.push(`printf '%s' ${sq(spec)} > ${target}`);
		else if (spec.dir) lines.push(`mkdir -p ${target}`);
		else if (spec.link) lines.push(`ln -s ${sq(spec.link)} ${target}`);
		else if (spec.bytes !== undefined) lines.push(`printf ${sq(spec.bytes)} > ${target}`);
		else if (spec.gen) lines.push(`(${spec.gen}) > ${target}`);
	}
	return lines.join("\n");
}

// program builds a fake executable: it logs its arguments, then answers by the first matching case label
// (a shell pattern over "$*"), else with the fallback.
function program(name, cases, fallback = "exit 1") {
	const body = cases.map(([label, sh]) => `${label})\n${sh}\n;;`).join("\n");
	return `${logArgs(name)}\ncase "$*" in\n${body}\nesac\n${fallback}`;
}

// gitClone is a fake git whose `clone` creates the repository.
const gitClone = (tree) => `${logArgs("git")}\n${logEnv}\nfor a; do dest="$a"; done\n${materialize(tree)}\nexit 0`;

const ghVersion = ["'--version'", out("gh version 2.0.0\n")];
// ghClone answers `gh repo clone o/r <dest> -- --depth 1 --single-branch [--branch ref]`.
const ghClone = (tree, ref) => [`'repo clone o/r '*' -- --depth 1 --single-branch${ref ? ` --branch ${ref}` : ""}'`, `${logEnv}\ndest="$4"\n${materialize(tree)}\nexit 0`];
const ghCloneFail = (ref) => [`'repo clone o/r '*' -- --depth 1 --single-branch${ref ? ` --branch ${ref}` : ""}'`, `${logEnv}\ndest="$4"\nmkdir -p "$dest"\nprintf partial > "$dest/partial"\nexit 1`];
const ghSize = (kb) => ["'api repos/o/r --jq .size'", out(`${kb}\n`)];
const ghBranch = (b) => ["'api repos/o/r --jq .default_branch'", out(`${b}\n`)];
const ghTree = (ref, paths) => [`'api repos/o/r/git/trees/${ref}?recursive=1 --jq .tree[].path'`, out(paths.join("\n") + "\n")];
const ghReadme = (ref, text) => [`'api repos/o/r/readme?ref=${ref} --jq .content'`, outSh(`printf '%s' ${sq(text)} | base64\n`)];
const ghFile = (ref, path, sh) => [`'api repos/o/r/contents/${path}?ref=${ref} --jq .content'`, outSh(sh)];

const longLines = (n, prefix = "Line") => Array.from({ length: n }, (_, i) => `${prefix} ${i + 1}`).join("\n");

// ---- the repositories ---------------------------------------------------------------------------------

const repoTree = {
	"README.md": longLines(1200) + "\n",
	"LICENSE": "MIT\n",
	"src/main.go": "package main\n\nfunc main() {}\n",
	"src/util/helper.go": "package util\n",
	"node_modules/dep/index.js": "module.exports = 1\n",
	"dist/out.js": "var a = 1\n",
	".git/config": "[core]\n",
	"assets/logo.png": { bytes: "\\211PNG\\r\\n\\032\\n\\000\\000" },
	"docs/guide.md": "# Guide\n",
	"\uff5c.txt": "wide bar\n",
	"\u{1F600}.txt": "smile\n",
	"\u00e9.txt": "acute\n",
	"Z.txt": "zed\n",
	"a.txt": "ay\n",
	"etclink": { link: "/etc" },
};

const blobTree = {
	"README.md": "# Hello\n",
	"src/main.go": "package main\n\nfunc main() {}\n",
	"assets/logo.png": { bytes: "\\211PNG\\r\\n\\032\\n\\000\\000" },
	"data.bin": { bytes: "a\\000b" },
	"big.txt": { gen: "yes '\u65e5' | head -n 110000 | tr -d '\\n'" },
	"bad.txt": { bytes: "ok\\377\\376bad\\303" },
	"bom.txt": { bytes: "\\357\\273\\277bom\\n" },
	"sizes/zero": { bytes: "" },
	"sizes/b512": { gen: "head -c 512 /dev/zero | tr '\\0' x" },
	"sizes/b1023": { gen: "head -c 1023 /dev/zero | tr '\\0' x" },
	"sizes/kb1": { gen: "head -c 1024 /dev/zero | tr '\\0' x" },
	"sizes/kb1_25": { gen: "head -c 1280 /dev/zero | tr '\\0' x" },
	"sizes/kb1_5": { gen: "head -c 1536 /dev/zero | tr '\\0' x" },
	"sizes/mb1": { gen: "head -c 1048576 /dev/zero | tr '\\0' x" },
	"sizes/mb1_25": { gen: "head -c 1310720 /dev/zero | tr '\\0' x" },
	"sizes/mb1_5": { gen: "head -c 1572864 /dev/zero | tr '\\0' x" },
	"escape": { link: "/etc" },
	"loop": { link: "." },
};

const manyFiles = Object.fromEntries(Array.from({ length: 260 }, (_, i) => [`f${i}.txt`, "x"]));

const sha40 = "0123456789abcdef0123456789abcdef01234567";
const treePaths = Array.from({ length: 250 }, (_, i) => `dir/file${i}.txt`);

// ---- issue and pull request fixtures -------------------------------------------------------------------

const user = (login) => ({ login });
const longBody = "paragraph\n".repeat(600);
const prView = {
	number: 7, title: "Improve fetch", state: "OPEN", isDraft: true, author: user("alice"), baseRefName: "main", headRefName: "feature",
	headRepositoryOwner: user("fork"), createdAt: "2026-01-01T00:00:00Z", mergedAt: "2026-01-03T00:00:00Z", closedAt: "2026-01-04T00:00:00Z",
	labels: [{ name: "bug" }, { name: "help wanted" }], milestone: { title: "v1" }, additions: 120, deletions: 30, changedFiles: 60,
	files: Array.from({ length: 60 }, (_, i) => ({ path: `src/f${i}.ts`, additions: i, deletions: 1 })),
	commits: Array.from({ length: 25 }, (_, i) => ({ oid: `abcdef0123456${i}`, messageHeadline: `commit ${i}` })),
	reviews: [
		{ author: user("rev1"), state: "COMMENTED", body: "first" }, { author: user("rev2"), state: "APPROVED", body: "x".repeat(400) },
		{ author: user("rev1"), state: "CHANGES_REQUESTED", body: "line1\nline2" }, { author: user("rev3"), state: "", body: "" },
	],
	statusCheckRollup: [{ name: "build", conclusion: "SUCCESS" }, { context: "ci/legacy", state: "FAILURE" }, { workflowName: "lint", status: "COMPLETED" }, {}],
	comments: Array.from({ length: 18 }, (_, i) => ({ id: i + 1, author: user(`user${i + 1}`), createdAt: "2026-01-02T00:00:00Z", body: i === 3 ? "y".repeat(900) : `comment ${i + 1}` })),
	body: longBody, closingIssuesReferences: [{ number: 5, title: "Bug five" }, { title: "no number" }], url: "https://github.com/o/r/pull/7",
};
const issueView = {
	number: 5, title: "Bug five", state: "CLOSED", stateReason: "COMPLETED", author: user("bob"), createdAt: "2026-01-01T00:00:00Z", closedAt: "2026-01-09T00:00:00Z",
	labels: [{ name: "bug" }], assignees: [{ login: "carol" }, { login: "dave" }], milestone: null,
	comments: Array.from({ length: 3 }, (_, i) => ({ id: 100 + i, author: user(`c${i}`), createdAt: "2026-01-02T00:00:00Z", body: `note ${i}` })), body: "",
	closedByPullRequestsReferences: [{ number: 7, title: "Improve fetch", url: "https://github.com/o/r/pull/7" }], url: "https://github.com/o/r/issues/5",
};
const json = (v) => JSON.stringify(v);
const reviewComment = (id, pr, extra = {}) => ({ id, pull_request_url: `https://api.github.com/repos/o/r/pulls/${pr}`, user: user("reviewer"), path: "src/a.ts", line: id, body: `thread ${id}`, ...extra });

const ghPr = (n, view, extra = []) => [
	ghVersion,
	[`'pr view ${n} --repo o/r --json '*`, out(json(view))],
	...extra,
];
const prPages = (n, pages) => pages.map((items, i) => [`'api repos/o/r/pulls/${n}/comments?per_page=100&page=${i + 1}'`, out(json(items))]);
const bigPage = Array.from({ length: 100 }, (_, i) => reviewComment(i + 1, 7));

const restPr = (changed, files) => ({ number: 7, title: "PR", state: "open", user: user("owner"), body: "body", draft: true, base: { ref: "main" }, head: { ref: "feature", repo: { owner: user("fork") } }, additions: 1, deletions: 0, changed_files: changed, review_comments: 12, comments: 75, merged_at: null, html_url: "https://github.com/o/r/pull/7", ...(files ?? {}) });

// ---- the cases ------------------------------------------------------------------------------------------------

const baseGh = [ghVersion, ghSize(1234)];
const cases = [
	{ name: "root clone through gh: tree, noise directories, links, README cut, UTF-16 name order", kind: "extract",
		gh: program("gh", [...baseGh, ghClone(repoTree)]), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "root clone through git when gh is missing, with .git suffix and trailing slash", kind: "extract",
		gh: "exit 1", git: gitClone({ "README.md": "# Hi\n", "src/a.txt": "a" }), steps: [{ url: "https://github.com/o/r.git/" }] },
	{ name: "tree and blob views of one cached clone", kind: "extract",
		gh: program("gh", [...baseGh, ghClone(blobTree, "main")]),
		steps: ["tree/main/sizes", "tree/main/docs/missing", "tree/main", "blob/main/src/main.go", "blob/main/assets/logo.png", "blob/main/data.bin", "blob/main/big.txt", "blob/main/bad.txt", "blob/main/bom.txt",
			"blob/main/src", "blob/main/nothing.txt", "blob/main", "blob/main/escape/hostname", "tree/main/loop"].map((p) => ({ url: `https://github.com/o/r/${p}` })) },
	{ name: "the clone is cached, concurrent callers share it and another ref clones again", kind: "extract",
		gh: program("gh", [...baseGh, ghClone({ "README.md": "# one\n" }), ghClone({ "README.md": "# two\n" }, "dev")]),
		steps: [{ concurrent: [{ url: "https://github.com/o/r" }, { url: "https://github.com/o/r" }, { url: "https://github.com/o/r/" }] }, { url: "https://github.com/o/r" }, { url: "https://github.com/o/r/tree/dev" }, { url: "https://github.com/o/r/tree/dev" }] },
	{ name: "a tree of 260 files is cut at 200 entries", kind: "extract",
		gh: program("gh", [...baseGh, ghClone(manyFiles)]), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "a repository over the size threshold shows the API view; forceClone clones it", kind: "extract",
		gh: program("gh", [ghVersion, ghSize(500000), ghBranch("main"), ghTree("main", treePaths), ghReadme("main", "R".repeat(9000)), ghClone({ "README.md": "# forced\n" })]),
		steps: [{ url: "https://github.com/o/r" }, { url: "https://github.com/o/r", forceClone: true }] },
	{ name: "a repository over a configured threshold", kind: "extract", config: { maxRepoSizeMB: 1 },
		gh: program("gh", [ghVersion, ghSize("2048 KB"), ghBranch("trunk"), ghTree("trunk", ["a", "b/c"]), ghReadme("trunk", "# small\n")]), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "a commit SHA URL uses the API: file, long file, tree and a failure", kind: "extract",
		gh: program("gh", [ghVersion, ghFile(sha40, "src/main.go", "printf 'package main\\n' | base64"), ghFile(sha40, "src/long.txt", "head -c 150000 /dev/zero | tr '\\0' a | base64"),
			ghTree(sha40, ["src", "src/main.go"]), ghReadme(sha40, "# at sha\n")]),
		steps: [`blob/${sha40}/src/main.go`, `blob/${sha40}/src/long.txt`, `tree/${sha40}/src`, `blob/${sha40}/src/gone.txt`, `blob/${sha40}`].map((p) => ({ url: `https://github.com/o/r/${p}` })) },
	{ name: "the API view needs a default branch, a tree or a README", kind: "extract",
		gh: program("gh", [ghVersion, ghSize(500000), ghBranch("main"), ghTree("main", [])]), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "no default branch: the API view is empty", kind: "extract",
		gh: program("gh", [ghVersion, ghSize(500000)]), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "a size that is not a number clones", kind: "extract",
		gh: program("gh", [ghVersion, ["'api repos/o/r --jq .size'", out("abc\n")], ghClone({ "README.md": "# nan\n" })]), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "a failed clone falls back to the API view and is not cached", kind: "extract",
		gh: program("gh", [ghVersion, ghSize(10), ghCloneFail(), ghBranch("main"), ghTree("main", ["a.txt"]), ghReadme("main", "# api\n")]), steps: [{ url: "https://github.com/o/r" }, { url: "https://github.com/o/r" }] },
	{ name: "a failed git clone without gh finds nothing", kind: "extract", gh: "exit 1", git: `${logArgs("git")}\nexit 128`, steps: [{ url: "https://github.com/o/r" }] },
	{ name: "a clone that exceeds the timeout is killed", kind: "extract", config: { cloneTimeoutSeconds: 1 }, gh: "exit 1",
		git: `${logArgs("git")}\n${logEnv}\nwhile :; do sleep 1; done`, steps: [{ url: "https://github.com/o/r" }] },
	{ name: "githubClone.enabled false leaves the URL alone", kind: "extract", config: { enabled: false }, gh: program("gh", baseGh), steps: [{ url: "https://github.com/o/r" }] },
	{ name: "URLs that are not repositories or are malformed", kind: "extract", gh: program("gh", baseGh),
		steps: ["https://github.com/o", "https://github.com/o/r/issues", "https://github.com/o/r/pull/3", "https://github.com/o/r/blob", "https://github.com/o/r/blob/main", "https://github.com/o/r/wiki/Home",
			"https://github.com/-bad/r", "https://github.com/o/r%2Fx", "https://example.com/o/r", "https://github.com/o/r/blob/%FF/x", "https://github.com/o/r/blob/a%00b/x"].map((url) => ({ url })) },
	{ name: "invalid configuration is an error", kind: "extract", rawConfig: "{ not json", gh: program("gh", baseGh), steps: [{ url: "https://github.com/o/r" }] },

	{ name: "pull request through gh: every section, caps, appendix and escalation commands", kind: "pr",
		gh: program("gh", ghPr(7, prView, prPages(7, [[reviewComment(1, 7, { body: "z".repeat(800) }), reviewComment(2, 7, { user: null, author: user("auth") })]]))), steps: [{ url: "https://github.com/o/r/pull/7/files" }] },
	{ name: "pull request: review threads bounded at three pages and an anchored thread beyond them", kind: "pr",
		gh: program("gh", ghPr(7, { number: 7, title: "Deep", state: "MERGED", author: user("a"), body: "b" }, [...prPages(7, [bigPage, bigPage, bigPage]), [`'api repos/o/r/pulls/comments/999'`, out(json(reviewComment(999, 7, { body: "deep" })))]])),
		steps: [{ url: "https://github.com/o/r/pull/7#discussion_r999" }] },
	{ name: "pull request: anchored thread from another pull request and a failing anchored request", kind: "pr",
		gh: program("gh", ghPr(7, { number: 7, title: "X", state: "OPEN", author: user("a"), body: "b" }, [...prPages(7, [[]]), [`'api repos/o/r/pulls/comments/999'`, out(json(reviewComment(999, 8)))]])),
		steps: [{ url: "https://github.com/o/r/pull/7#discussion_r999" }, { url: "https://github.com/o/r/pull/7#discussion_r1000" }] },
	{ name: "pull request: a failing first page is unavailable, a malformed page too", kind: "pr",
		gh: program("gh", ghPr(7, { number: 7, title: "X", state: "OPEN", author: user("a"), body: "b" }, [[`'api repos/o/r/pulls/7/comments?per_page=100&page=1'`, out("not json")]])), steps: [{ url: "https://github.com/o/r/pull/7" }] },
	{ name: "pull request: a page that fails after data is bounded", kind: "pr",
		gh: program("gh", ghPr(7, { number: 7, title: "X", state: "OPEN", author: user("a"), body: "b" }, [...prPages(7, [bigPage]), [`'api repos/o/r/pulls/7/comments?per_page=100&page=2'`, fail("boom")]])), steps: [{ url: "https://github.com/o/r/pull/7" }] },
	{ name: "pull request: unknown --json field retries with the core fields", kind: "pr",
		gh: program("gh", [ghVersion, [`'pr view 12 --repo o/r --json '*statusCheckRollup*`, fail("Unknown JSON field: statusCheckRollup\n")],
			[`'pr view 12 --repo o/r --json '*`, out(json({ number: 12, title: "Fallback fields", state: "OPEN", author: user("alice"), body: "body", comments: [] }))], [`'api '*`, out("[]")]]),
		steps: [{ url: "https://github.com/o/r/pull/12" }] },
	{ name: "pull request: gh view fails for another reason", kind: "pr",
		gh: program("gh", [ghVersion, [`'pr view 12 --repo o/r --json '*`, fail("HTTP 404\n")]]), steps: [{ url: "https://github.com/o/r/pull/12" }] },
	{ name: "issue through gh: assignees, closed-by pull requests and an anchored comment beyond the cap", kind: "pr",
		gh: program("gh", [ghVersion, [`'issue view 5 --repo o/r --json '*`, out(json({ ...issueView, comments: Array.from({ length: 20 }, (_, i) => ({ id: 100 + i, author: user(`c${i}`), createdAt: "2026-01-02T00:00:00Z", body: `note ${i}` })) }))]]),
		steps: [{ url: "https://github.com/o/r/issues/5#issuecomment-118" }, { url: "https://www.github.com/o/r/issues/5" }] },
	{ name: "issue: a minimal view", kind: "pr",
		gh: program("gh", [ghVersion, [`'issue view 5 --repo o/r --json '*`, out(json({ number: 5, title: "", state: "", author: null }))]]), steps: [{ url: "https://github.com/o/r/issues/5" }] },
	{ name: "githubPrIssue.enabled false leaves the URL alone", kind: "pr", prConfig: { enabled: false }, gh: program("gh", ghPr(7, prView)), steps: [{ url: "https://github.com/o/r/pull/7" }] },
	{ name: "githubPrIssue that is not an object is an error", kind: "pr", rawConfig: '{"githubPrIssue": 3}', gh: program("gh", ghPr(7, prView)), steps: [{ url: "https://github.com/o/r/pull/7" }] },
	{ name: "pull and issue URLs the extractor does not take", kind: "pr", gh: program("gh", ghPr(7, prView)),
		steps: ["https://github.com/o/r/pulls", "https://github.com/o/r/issues/new", "https://github.com/o/r/pull/7/unknown", "https://github.com/o/r/issues/5/edit", "https://github.com/o/r/pull/0", "https://github.com/o/r/pull/007x", "https://github.com/o/r/pull/9007199254740993"].map((url) => ({ url })) },

	{ name: "REST fallback for a pull request: files, review threads, anchored thread outside the first page", kind: "content", gh: "exit 1",
		responses: [
			["/pulls/7", 200, restPr(75)], ["/issues/7/comments?per_page=50", 200, [{ id: 1, user: user("first"), created_at: "2026-01-01T00:00:00Z", body: "first page" }]],
			["/pulls/7/files?per_page=50", 200, Array.from({ length: 50 }, (_, i) => ({ filename: `file${i}.ts`, additions: 1, deletions: 0 }))],
			["/pulls/7/comments?per_page=50", 200, [reviewComment(1, 7)]], ["/pulls/comments/42", 200, reviewComment(42, 7, { body: "outside review page" })]],
		urls: ["https://github.com/o/r/pull/7#discussion_r42"] },
	{ name: "REST fallback for an issue with an anchored comment, then one from another work item", kind: "content", gh: "exit 1",
		responses: [
			["/issues/12", 200, { number: 12, title: "Issue", state: "open", state_reason: "completed", user: user("owner"), body: "body", comments: 75, labels: [{ name: "bug" }], assignees: [user("x")], milestone: { title: "m" }, closed_at: "2026-02-01T00:00:00Z", html_url: "https://github.com/o/r/issues/12" }],
			["/issues/12/comments?per_page=50", 200, []], ["/issues/comments/99", 200, { id: 99, issue_url: "https://api.github.com/repos/o/r/issues/12", user: user("anchored"), created_at: "2026-01-02T00:00:00Z", body: "outside first page" }],
			["/issues/comments/98", 200, { id: 98, issue_url: "https://api.github.com/repos/other/r/issues/12", user: user("wrong"), body: "wrong issue" }]],
		urls: ["https://github.com/o/r/issues/12#issuecomment-99", "https://github.com/o/r/issues/12#issuecomment-98"] },
	{ name: "REST fallback: the rate limit stops the fetch, with and without a reset time", kind: "content", gh: "exit 1",
		responses: [["/issues/12", 403, "rate limited", { "x-ratelimit-remaining": "0", "x-ratelimit-reset": "1800000000" }], ["/issues/13", 403, "rate limited", { "x-ratelimit-remaining": "0" }], ["/issues/14", 403, "forbidden"],
			["/issues/15", 200, { number: 15, title: "ok", state: "open" }], ["/issues/15/comments?per_page=50", 429, "slow"]],
		urls: ["https://github.com/o/r/issues/12", "https://github.com/o/r/issues/13", "https://github.com/o/r/issues/14", "https://github.com/o/r/issues/15"] },
	{ name: "REST fallback: rate limit on a later request of a pull request", kind: "content", gh: "exit 1",
		responses: [["/pulls/7", 200, restPr(3)], ["/issues/7/comments?per_page=50", 200, []], ["/pulls/7/files?per_page=50", 403, "rate limited", { "x-ratelimit-remaining": "0" }]], urls: ["https://github.com/o/r/pull/7"] },
	{ name: "REST fallback: a missing pull request falls through to the page fetch", kind: "content", gh: "exit 1",
		responses: [["/pulls/7", 404, { message: "Not Found" }]], urls: ["https://github.com/o/r/pull/7"] },
	{ name: "REST fallback: a transport failure and an unreadable body", kind: "content", gh: "exit 1",
		responses: [["/issues/12", "throw", "fetch failed"], ["/issues/13", 200, "<html>not json</html>"]], urls: ["https://github.com/o/r/issues/12", "https://github.com/o/r/issues/13"] },
	{ name: "REST fallback: files and review threads that are not lists", kind: "content", gh: "exit 1",
		responses: [["/pulls/7", 200, restPr(3)], ["/issues/7/comments?per_page=50", 500, "oops"], ["/pulls/7/files?per_page=50", 200, { message: "x" }], ["/pulls/7/comments?per_page=50", 200, "[1, 2, {\"id\": 5}]"]], urls: ["https://github.com/o/r/pull/7"] },
	{ name: "a usable gh answers before the REST API is asked", kind: "content", gh: program("gh", ghPr(7, { number: 7, title: "Via gh", state: "OPEN", author: user("a"), body: "b" }, prPages(7, [[]]))), responses: [], urls: ["https://github.com/o/r/pull/7"] },
];

// render cases: renderGitHubPrIssue on constructed data
const renderCases = [
	{ name: "an issue with everything empty", data: { url: "u", owner: "o", repo: "r", kind: "issue", number: 5, view: {}, reviewThreads: [], fallbackNotes: [] } },
	{ name: "an anchored issue comment beyond the cap, truncated bodies in the appendix", data: { url: "u", owner: "o", repo: "r", kind: "issue", number: 5, anchor: "issuecomment-99", reviewThreads: [], fallbackNotes: ["note one"],
		view: { number: 5, title: "T", state: "open", comments: [...Array.from({ length: 20 }, (_, i) => ({ id: i + 1, user: user(`u${i}`), created_at: "d", body: "b".repeat(i === 1 ? 900 : 5) })), { id: 99, user: user("anchored"), created_at: "d", body: "found" }], commentsCount: 40, body: "x".repeat(5000) } } },
	{ name: "a pull request with a known comment total and review totals", data: { url: "u", owner: "o", repo: "r", kind: "pull", number: 7, anchor: "discussion_r3", reviewThreads: Array.from({ length: 40 }, (_, i) => ({ id: i + 1, user: user(`t${i}`), path: i % 2 ? "a.ts" : "", line: i % 3 ? i : undefined, original_line: 4, position: 5, body: i === 2 ? "w".repeat(800) : "ok" })),
		view: { number: 7, title: "P", state: "OPEN", author: user("a"), headRepositoryOwner: user("o"), headRefName: "f", baseRefName: "m", changedFiles: 80, files: [{ filename: "x", additions: 1 }], reviewCommentsCount: 99, comments: [{ databaseId: 3, author: user("z"), body: "c" }], commentsCount: 5, reviews: [], commits: [{ sha: "1234567890", commit: { message: "subject\nmore" } }, { oid: "ab" }, {}] } } },
	{ name: "bounded review threads, unavailable fields and fork head", data: { url: "u", owner: "o", repo: "r", kind: "pull", number: 7, reviewThreadsBounded: true, reviewThreads: [{ id: 1, user: user("a"), path: "p", line: 1, body: "b" }], fallbackNotes: ["n1", "n2"],
		view: { number: 7, title: "P", state: "OPEN", isDraft: true, author: user("a"), headRepositoryOwner: user("fork"), headRefName: "f", baseRefName: "m", reviewsUnavailable: true, commitsUnavailable: true, linkedReferencesUnavailable: true, anchorUnavailable: true } } },
	{ name: "unavailable review threads and an unavailable anchor", data: { url: "u", owner: "o", repo: "r", kind: "pull", number: 7, anchor: "discussion_r5", reviewThreadsUnavailable: true, reviewThreads: [], fallbackNotes: [],
		view: { number: 7, title: "P", state: "OPEN", author: user("a"), anchorUnavailable: true, comments: [{ id: 1 }] } } },
	{ name: "a document over the cap is cut", data: { url: "u", owner: "o", repo: "r", kind: "pull", number: 7, reviewThreads: Array.from({ length: 4 }, (_, i) => ({ id: i, user: user("a"), path: "p", line: 1, body: "q".repeat(699) })),
		view: { number: 7, title: "P", state: "OPEN", author: user("a"), body: "x".repeat(4001), reviews: Array.from({ length: 12 }, (_, i) => ({ author: user(`r${i}`), state: "COMMENTED", body: "w".repeat(301) })),
			comments: Array.from({ length: 300 }, (_, i) => ({ id: i, user: user("a"), body: "c".repeat(701) })) } } },
	{ name: "check rollups and linked references", data: { url: "u", owner: "o", repo: "r", kind: "pull", number: 7, reviewThreads: [], fallbackNotes: [],
		view: { number: 7, title: "P", state: "OPEN", author: user("a"), statusCheckRollup: Array.from({ length: 17 }, (_, i) => ({ name: `c${i}`, conclusion: i % 4 ? "SUCCESS" : "FAILURE" })), closingIssuesReferences: Array.from({ length: 22 }, (_, i) => ({ number: i, title: `t${i}` })),
			labels: ["x", { name: "n" }, { login: "l" }, { title: "t" }, {}], milestone: [], mergedAt: "m", closedAt: "c" } } },
];

// ---- running the original ---------------------------------------------------------------------------------------

function summarize(s) {
	if (typeof s !== "string" || s.length <= 20000) return s;
	return { len: s.length, sha256: createHash("sha256").update(s, "utf8").digest("hex"), head: s.slice(0, 200), tail: s.slice(-200) };
}

function nodeScript(c, dirs) {
	const imports = `const extract = await import(${JSON.stringify(join(oracle, "github-extract.ts"))}); const pr = await import(${JSON.stringify(join(oracle, "github-issue-pr.ts"))}); const content = await import(${JSON.stringify(join(oracle, "extract.ts"))});`;
	const lookup = `const lookup = async () => [{ address: "140.82.112.6", family: 4 }];`;
	const fetchStub = `
		const requests = [];
		const table = ${JSON.stringify(c.responses ?? [])};
		globalThis.fetch = async (input, init = {}) => {
			const url = String(input);
			const headers = Object.fromEntries(new Headers(init.headers || {}));
			requests.push(url.startsWith("https://api.github.com/") ? { url, method: init.method || "GET", accept: headers.accept, userAgent: headers["user-agent"] } : { url, method: init.method || "GET" });
			const hit = table.find(([suffix]) => url.endsWith("https://api.github.com/repos/o/r" + suffix) || url === "https://api.github.com/repos/o/r" + suffix);
			if (!hit) return new Response("not found", { status: 404, statusText: "Not Found" });
			const [, status, body, headersIn] = hit;
			if (status === "throw") throw new TypeError(body);
			return new Response(typeof body === "string" ? body : JSON.stringify(body), { status, statusText: ({ 200: "OK", 403: "Forbidden", 404: "Not Found", 429: "Too Many Requests", 500: "Internal Server Error" })[status] ?? "", headers: headersIn || {} });
		};`;
	const step = c.kind === "extract"
		? `const r = await extract.extractGitHub(s.url, undefined, s.forceClone); return r;`
		: c.kind === "pr"
			? `return await pr.extractGitHubIssuePr(s.url, undefined, { lookup });`
			: `return await content.extractContent(s.url, undefined, { lookup });`;
	const steps = c.kind === "content" ? c.urls.map((url) => ({ url })) : c.steps;
	return `${imports}${lookup}${fetchStub}
		const steps = ${JSON.stringify(steps)};
		const run = async (s) => { try { ${step} } catch (e) { return { thrown: e.message }; } };
		const results = [];
		for (const s of steps) {
			if (s.concurrent) results.push(await Promise.all(s.concurrent.map(run)));
			else results.push(await run(s));
		}
		extract.clearCloneCache();
		console.log("\\n@@RESULT@@" + JSON.stringify({ results, requests }));`;
}

function normalizer(dirs) {
	const roots = [dirs.clones, realpathSync(dirs.clones), dirs.agent];
	return (s) => {
		if (typeof s !== "string") return s;
		for (const r of roots) s = s.split(r).join(r === dirs.agent ? "<agent>" : "<clones>");
		s = s.replace(/runtime-[A-Za-z0-9]{6}/g, "runtime-<id>");
		s = s.replace(/^(Failed to parse \S+): .*$/s, "$1");
		s = s.replace(/^(GitHub REST fallback failed): .*$/s, "$1");
		return s;
	};
}

function project(r, norm) {
	if (r === null || r === undefined) return null;
	if (r.thrown !== undefined) return { thrown: norm(r.thrown) };
	const o = { url: r.url, title: norm(r.title), content: summarize(norm(r.content)), error: norm(r.error) };
	if (r.status !== undefined) o.status = r.status;
	if (typeof o.content === "string" && o.error && o.error.startsWith("GitHub REST fallback failed")) o.content = "<message>";
	return o;
}

function runCase(c) {
	const root = mkdtempSync(join(tmpdir(), "ghwire-"));
	const dirs = { root, bin: join(root, "bin"), agent: join(root, "agent"), clones: join(root, "clones"), home: join(root, "home") };
	for (const d of [dirs.bin, dirs.agent, dirs.clones, dirs.home]) mkdirSync(d, { recursive: true });
	const log = join(root, "shim.log");
	writeFileSync(log, "");
	const gh = c.gh ?? "exit 1";
	const git = c.git ?? "exit 1";
	for (const [name, body] of [["gh", gh], ["git", git]]) writeFileSync(join(dirs.bin, name), `#!/bin/sh\n${body}\n`, { mode: 0o755 });
	// "<clones>" in the stored configuration stands for the clone directory of the run.
	const configText = c.rawConfig ?? JSON.stringify({ githubClone: { clonePath: "<clones>", ...(c.config ?? {}) }, ...(c.prConfig ? { githubPrIssue: c.prConfig } : {}) });
	writeFileSync(join(dirs.agent, "web-search.json"), configText.split("<clones>").join(dirs.clones));
	const env = { PATH: `${dirs.bin}:/usr/local/bin:/usr/bin:/bin`, HOME: dirs.home, USERPROFILE: dirs.home, PI_CODING_AGENT_DIR: dirs.agent, SHIM_LOG: log };
	const r = spawnSync(process.execPath, ["--input-type=module"], { input: nodeScript(c, dirs), encoding: "utf8", env, maxBuffer: 64 << 20, timeout: 120000 });
	if (r.status !== 0) throw new Error(`${c.name}: ${r.stderr}`);
	const parsed = JSON.parse(r.stdout.split("@@RESULT@@").pop());
	const norm = normalizer(dirs);
	const calls = readFileSync(log, "utf8").split("\n").filter(Boolean).map(norm).sort();
	rmSync(root, { recursive: true, force: true });
	const results = parsed.results.map((x) => (Array.isArray(x) ? x.map((y) => project(y, norm)) : project(x, norm)));
	const recorded = { name: c.name, kind: c.kind, shims: { gh, git }, configText, steps: c.kind === "content" ? c.urls.map((url) => ({ url })) : c.steps, expect: { results, calls } };
	if (c.kind === "content") { recorded.responses = c.responses; recorded.expect.requests = parsed.requests; }
	return recorded;
}

function runRender(c) {
	c = { ...c, data: { reviewThreads: [], fallbackNotes: [], ...c.data } };
	const script = `const pr = await import(${JSON.stringify(join(oracle, "github-issue-pr.ts"))}); console.log("@@RESULT@@" + JSON.stringify(pr.renderGitHubPrIssue(${JSON.stringify(c.data)})));`;
	const r = spawnSync(process.execPath, ["--input-type=module"], { input: script, encoding: "utf8", env: { PATH: process.env.PATH, PI_CODING_AGENT_DIR: tmpdir() }, maxBuffer: 16 << 20 });
	if (r.status !== 0) throw new Error(`${c.name}: ${r.stderr}`);
	const o = JSON.parse(r.stdout.split("@@RESULT@@").pop());
	return { name: c.name, kind: "render", data: c.data, expect: { results: [{ url: o.url, title: o.title, content: summarize(o.content), error: o.error }] } };
}

// decode cases: Node's lenient decoders, which the API view and the file views depend on.
const utf8Vectors = ["", "616263", "c0 80", "c1 bf", "e0 80 80", "e0 9f bf", "ed a0 80", "ed 9f bf", "f0 80 80 80", "f0 8f bf bf", "f4 90 80 80", "f4 8f bf bf", "f5 80 80 80", "ff", "fe", "80", "bf", "c3", "c3 28", "e2 82", "e2 82 41", "e2 28 a1", "f0 9f 98", "f0 9f 98 41", "f0 9f 28 8c", "ef bb bf 61", "e6 97 a5 e6 9c ac", "61 c3 a9 ff 62", "df 7d", "f7 df 7d"];
const base64Vectors = ["", "QUJD", "QUJDRA==", "QUJDRA", "QUJDRA=", "QU JD\nRA==", "999999", "20000000UkVBRE1FIGhlbGxvCg==", "YWJj-_8", "YWJj+/8=", "QQ==QQ==", "Q", "QU", "QUJ", "!!QUJD", "QUJD\u00e9QUJD", "====", "UkVB\r\nRE1F\n"];
const intVectors = ["", " 12", "12abc", "+5", "-7", "0x10", "1e3", "abc", "  \n42\t", "99999999999999999999", "2048 KB", "٣", "1_000", "-0", "007"];
const decodeRecord = {
	name: "Node's base64 and UTF-8 decoders and parseInt", kind: "decode",
	utf8: utf8Vectors.map((hex) => ({ hex: hex.replaceAll(" ", ""), text: Buffer.from(hex.replaceAll(" ", ""), "hex").toString("utf-8") })),
	base64: base64Vectors.map((text) => ({ text, hex: Buffer.from(text, "base64").toString("hex") })),
	parseInt: intVectors.map((text) => ({ text, value: String(parseInt(text.trim(), 10)) })),
};
const recorded = [...cases.map(runCase), ...renderCases.map(runRender), decodeRecord];
const text = JSON.stringify(recorded, null, 1) + "\n";
if (process.argv.includes("--check")) {
	if (readFileSync(outFile, "utf8") !== text) { console.error("testdata/github-wire.json differs from a fresh recording"); process.exit(1); }
	console.log(`testdata/github-wire.json matches (${recorded.length} cases)`);
} else {
	writeFileSync(outFile, text);
	console.log(`wrote ${outFile} (${recorded.length} cases)`);
}
