// Generated inputs of the BashProgram differential: path-bearing commands (redirects, cd chains, wrappers, heredocs, env
// assignments, globs, symlinks, HOME/PWD spellings). Deterministic; no arguments.
//   node gen-paths.mjs <out.json>
// `@R@` stands for the fixture root (the project cwd of the "root" scenarios) and `@O@` for the fixture's outside directory;
// the probe and the Go test substitute their own temporary directories.
import fs from "node:fs";

const tokens = [
  // absolute, external, device, root, glob
  "/etc/passwd", "/etc/hosts", "/etc/shadow", "/etc", "/tmp/x", "/tmp/tmp.*", "/", "//", "///", "/dev/null", "/dev/stdin", "/dev/stdout",
  "/dev/null/sub", "/etc/[p]asswd", "/et[c]/pa*", "/usr/local/bin/", "/projects/my-app/src/x.ts", "/projects/my-app", "/projects/secret",
  "/home/x", "/c/Other/x", "/c/projects/app/inside.txt", "/d/secrets/pw", "C:/Windows/win.ini", "C:/Projects/App/inside", "C:\\x", "dir\\file", "a\\b",
  // home and variables
  "~", "~/x", "~/.ssh/id_rsa", "~/.ssh/[i]d", "~root/x", "$HOME", "$HOME/x", '"$HOME/x"', '"${HOME}/x"', "${HOME}", "${HOME:-/tmp}/x", "$PWD", '"$PWD/x"',
  "${PWD}/y", "$OTHER/x", "'~/x'", '"~/x"', "$(pwd)/x", "`pwd`/x",
  // relative
  "src/foo.ts", "./a", "../x", "../../x", "a/../../b", "a/../b", ".env", ".", "..", "id_rsa", "outside-link", "vault", "vault/secret", "link/secret",
  "v1..v2", "HEAD..origin/main", "a_sym", "dangling", "nested/inner.txt", "inner.txt", "src/[s]ecret.env", "*.ts", "src/*", "nested", "out.txt", "inside.txt",
  "foo", "-x", "--file=/tmp/x", "--file=src/foo.ts", "--directory=/etc", "@scope/pkg", "https://a/b", "a=b", "/x:y", "~/x:/y",
  // fixture-rooted
  "@R@/src/foo.ts", "@R@/id_rsa", "@R@/vault/secret", "@R@/link/secret", "@R@/../x", "@O@/secret", "@O@", "@R@", "@R@/nested", "@R@/gone", "@R@/.env",
];

const singles = [
  "cat T", "ls T", "ls -la T", "rm -rf T", "touch T", "mkdir -p T", "cp T /tmp/dest", "cp src/a T", "mv T U", "echo T", "echo hi > T", "echo hi >> T",
  "cat < T", "cat 2> T", "cat &> T", "cat >| T", "cat >&- T", "cat 2>&1 T", "cat >&2 T", "echo hi > T 2>&1", "cat <> T", "echo hi 1>T", "sort < T > out.txt",
  "cat T > T", "cat T >> /tmp/o", "cat T && head T", "cat T | tee T", "cat T; echo //", "grep pat T", "grep -A 3 pat T", "grep -r -e pat T", "grep --regexp=T f",
  "grep -f T x", "grep -v \"//.*x\" T", "rg -e pat T", "rg -g '*.ts' pat T", "sed -n p T", "sed -i s/a/b/ T", "sed -i '' s/a/b/ T", "sed 's/a/b/' T > out", "sed -e T f",
  "awk '{print}' T", "awk -F: '{print $1}' T", "awk -f T x", "sd a b T", "find T", "find T -delete", "find T -name '*.ts'", "find . -path T", "fd pat T", "fd -x rm T",
  "xargs cat T", "xargs grep foo T", "xargs sort -o T", "sudo cat T", "sudo -u root rm T", "env FOO=1 cat T", "timeout 5 cat T", "nice -n 5 cat T", "nohup cat T",
  "find . -exec cat T {} \\;", "bash -c 'cat T'", "sh -c \"cat T\"", "eval \"cat T\"", "node -e 'x' T", "python3 -c 'print(1)' T", "node T", "bash T", "source T", ". T",
  "tar --directory=T -xf a.tar", "tar -C T -xf a", "curl -o T http://x", "git -C T status", "git diff T", "git log T", "git log HEAD..T", "ln -s T U", "chmod +x T",
  "for f in T; do cat $f; done", "for f in T U; do echo; done", "for f in a b; do cat T; done", "while true; do cat T; done", "if true; then cat T; fi",
  "if cat T; then echo; fi", "case T in a) echo b;; esac", "case $x in T) echo b;; esac", "case $x in a) cat T;; esac", "select f in T; do echo; done",
  "echo $(cat T)", "echo \"$(cat T)\"", "echo `cat T`", "cat <(cat T)", "cat > >(tee T)", "echo hi > $(cat T)", "echo hi > ${D}/$(cat T)", "FOO=$(cat T) echo hi",
  "FOO=T echo hi", "FOO=T cat U", "A=1 B=2 cat T", "local x=$(cat T)", "export X=T", "declare x=$(cat T)", "[[ -f T ]]", "[ -f T ]", "test -f T", "[[ $(cat T) ]]", "unset $(cat T)",
  "$(cat T)", "`cat T`", "( cat T )", "{ cat T; }", "! cat T", "f() { cat T; }", "cat T &", "cat T & cat U",
  "cat <<EOF\nT\nEOF", "cat <<'EOF'\nT\nEOF", "cat <<-EOF\n\tT\nEOF", "cat <<EOF T\nb\nEOF", "cat T <<EOF\nb\nEOF", "cat <<EOF > T\nb\nEOF", "cat <<EOF ~/x/in\nb\nEOF",
  "cat <<EOF | cat T\nb\nEOF", "cat <<EOF && cd T && cat U\nb\nEOF", "cat <<'MSG' 2>&1 | cat T\nm\nMSG", "cat <<EOF ; cat T\nb\nEOF", "cat <<EOF & cat T\nb\nEOF",
  "cat <<EOF arg > T\nb\nEOF", "xargs grep foo <<EOF > T\nb\nEOF", "cat <<< T", "cat <<< $(cat T)", "cat < <(cat T)",
  "HOME=/etc; cat T", "export HOME=/etc; cat T", "unset HOME; cat T", "cat T; HOME=/etc", "PWD=/; cat T", "read HOME; cat T", "source f; cat T", "for HOME in /etc; do cat T; done",
  "HOME=-delete; find /etc T", "HOME=-delete; find T", "find /etc T", "cat <<EOF ; HOME=/etc\nx\nEOF\ncat T", "env -i HOME=\"$HOME\" cat T",
  "cat T # c", "# T", "cat 'T'", "cat \"T\"", "cat \"x T\"", "cat T\"x\"", "cat \\T", "cat $x T", "cat T$x", "cat T*", "cat {T,U}", "cat T U", "cat -- T", "cat -n T",
  "2>/dev/null cat T", "cat 2>/dev/null T", "cat T 2>/dev/null", ">/tmp/o cat T", "git push 2>&1 T", "pnpm x 2>&1 | tail ; cat T",
  "node -e \"// T\"", "node -e \"$(cat T)\"", "node build.js T", "python3 -c \"open('T')\"",
];

const cdTargets = ["a", "a/b", "..", "../x", "/etc", "@R@/nested", "@R@", "@O@", "nested/sub", "/projects/my-app/src", "/projects/my-app/packages/sub", "~", "-", '"$D"', "$(echo a)",
  "/c/Other", "/tmp", "C:/x", "vault", "link", "src", "$HOME/x", "~/x", "", "--", "'a b'", "\"a\"/b", "a$x", "nested/.."];
const cdOperands = ["../x", "../../x", "../../../x", "x", "src/foo.ts", "inner.txt", "id_rsa", "/etc/passwd", "~/x", "$HOME/x", '"$PWD/x"', "$PWD", "./y", ".pi/../../../.pi/s/SKILL.md",
  "@R@/id_rsa", "vault/secret", "link/secret", "C:/Projects/App/inside.txt", "/c/projects/app/inside.txt", "/tmp/a", "HEAD..origin/main", "a/../../b", "out.txt", "secret"];
const chains = [
  "cd T && cat U", "cd T; cat U", "cd T || cat U", "cd T | cat U", "cd T & cat U", "( cd T && cat U )", "( cd T ) && cat U", "{ cd T; cat U; }", "{ cd T; } && cat U",
  "cd T && cd .. && cat U", "cd T && cd ../.. && cat U", "echo $(cd T && cat U)", "cd T && pnpm x 2>&1 | cat U", "cd T && pnpm x 2>&1 | tail ; cat U", "cd a && cd T 2>&1 | tail ; cat U",
  "cd T && grep pat U > ../o", "cd -- T && cat U", "cd 2>/dev/null T && cat U", "2>/dev/null cd T && cat U", "cd \"T\" && cat U", "cd 'T' && cat U", "pushd T && cat U",
  "cd T x && cat U", "cd T && echo hi > U", "cd T && cat < U", "cd T && for f in U; do cat $f; done", "cd T && sed -n p U", "cd T && find U", "cd T && xargs cat U", "cd T\ncat U",
  "cat U && cd T && cat U", "if true; then cd T; cat U; fi", "cd T && ( cat U )", "cd T && cat <<EOF > U\nb\nEOF", "cd T && cat <<'M' 2>&1 | cat U\nm\nM", "cd /etc && cd T && cat U",
];

const out = [];
for (const t of tokens) for (const s of singles) out.push(s.replaceAll("T", t).replaceAll("U", "src/foo.ts"));
// a second operand per template for the shapes that read two paths
for (const t of tokens.filter((_, i) => i % 3 === 0)) for (const s of singles.filter((x) => x.includes("U"))) out.push(s.replaceAll("T", "/etc/hosts").replaceAll("U", t));
for (const c of cdTargets) for (const o of cdOperands) for (const ch of chains) out.push(ch.replaceAll("T", c).replaceAll("U", o));
for (const t of ["~/x", "/etc", "a", "src"]) for (const ch of ["cd T && cat ../b", "cd T"]) for (const w of ["", "sudo ", "xargs ", "env A=1 "]) out.push(`${w}${ch.replaceAll("T", t)}`);
// jsonl-safe, deterministic, unique
const seen = new Set();
const unique = out.filter((s) => (seen.has(s) ? false : (seen.add(s), true)));
fs.writeFileSync(process.argv[2], JSON.stringify(unique));
console.log(`generated=${unique.length}`);
