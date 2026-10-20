// Generated inputs of the bash analysis differential: wrappers around commands around redirects, plus
// handcrafted shapes (unicode, quoting, option spellings). Deterministic; no arguments.
//   node gen-extra.mjs <out.json>
import fs from "node:fs";

const wrappers = [
  "", "sudo ", "sudo -u root ", "env FOO=bar ", "env -u X ", "xargs ", "xargs -I{} ", "xargs -0 -n1 ", "timeout 5 ", "timeout -s KILL 10 ",
  "nice -n 5 ", "nohup ", "time ", "doas ", "setsid ", "stdbuf -o0 ", "watch -n 2 ", "flock /tmp/l ", "parallel ", "bash -c ", "sh -ec ",
  "eval ", "/bin/bash -c ", "sudo timeout 5 xargs ", "sudo bash -c ", "find . -exec ", "find . -name x -execdir ", "fd -x ", "fd --exec-batch ",
  "FOO=bar ", "HOME=/tmp/x ", "~/bin/", "$HOME/bin/", "xargs --unknown-opt ", "sudo -- ",
];
const inners = [
  "grep foo /etc/hosts", "grep -A 3 pat /etc/passwd", "sort -o /tmp/x in", "sort --out=/tmp/o in", "find . -delete", "find /etc -name '*.ts'",
  "fd -x rm", "sed -n 1,5p f.md", "sed -i s/a/b/ f", "sed -e 's/a/b/w out' f", "awk '{print $1}' data", "awk '{print > \"x\"}' data",
  "cat /etc/shadow", "ls -la", "echo hi", "rm -rf /tmp/x", "git status", "pnpm test", "cd /repo", "rg -g '!docs' pat /etc",
  "cat $(cat /etc/shadow)", "echo \"$(rm x)\"", "echo `id`", "cat \"$HOME/.ssh/id_rsa\"", "cat ~/x", "echo ~/x $HOME/y",
  "node -e 'x' /tmp/a", "python3 -c 'print(1)'", "grep --regexp=/etc/passwd f", "tar --directory=/etc -xf a.tar", "cat --file=/tmp/x",
  "echo 'héllo wörld' /tmp/ü", "echo 😀 /tmp/😀", "cat ünï/../x", "echo \"a  b\"  c", "cat a\\ b", "'sed' -n 1p f", "\"grep\" x f",
  "cat <<EOF\nhello $(rm x)\nEOF", "for f in /etc/shadow; do cat $f; done", "case $x in a) cat /etc/shadow;; esac",
  "if true; then rm x; fi", "(rm x)", "{ rm x; }", "f() { rm x; }", "x=$(rm y) z", "local x=$(rm y)", "[[ $(rm x) ]]", "! rm x",
  "cat a | tee b", "a && b || c", "a; b", "a &", "cat <<< $(rm x)", "echo hi # comment", "# only comment", "",
];
const suffixes = ["", " > /tmp/out", " >> /tmp/log", " < /etc/hosts", " 2>&1", " 2>/dev/null", " <> rw.txt", " > \"$OUT\"", " > $(rm x)", " | rm y"];

const out = [];
for (const w of wrappers) for (const i of inners) out.push(`${w}${i}`);
for (const i of inners) for (const s of suffixes) out.push(`${i}${s}`);
for (const w of ["sudo ", "xargs ", "env A=1 ", "timeout 5 "]) {
  for (const i of inners.slice(0, 25)) for (const s of suffixes.slice(1, 5)) out.push(`${w}${i}${s}`);
  for (const i of inners.slice(0, 25)) out.push(`>/tmp/o ${w}${i}`, `${w}2>/dev/null ${i}`);
}
// sed/awk/option shapes
for (const a of ["-n", "-i", "-i.bak", "-ne", "-nE", "--quiet", "--in-place", "-e", "-f", "--expression=1p", "--", "-s", "-z", "-l", "-E", "-r"]) {
  for (const sc of ["1p", "p", "s/a/b/", "s/a/b/w out", "$!N;P;D", "/x/,/y/p", "y/ab/cd/", "w out", "r x", "e id", "1,2", "}", "{p", "a\\\ntext", "b end;:end", "s/[/]/x/", "l 40", "/x/I p", "5,+3p", "0~4p", "\\|a|p", "s\u00e9a\u00e9b\u00e9", "s/😀/x/", "p # c"]) {
    out.push(`sed ${a} '${sc}' f`, `sed ${a} ${sc} f`, `sed ${a} -e '${sc}' f`);
  }
}
for (const a of ["-F:", "-F :", "-v n=3", "-vn=3", "-f", "-i", "-e", "--", "--source", "--file=p", "-W", "--version"]) {
  for (const p of ["{print}", "{print > \"o\"}", "NR>=3", "BEGIN{system(\"x\")}", "@include \"x\"", "{print | \"sh\"}", "x", "-"]) {
    out.push(`awk ${a} '${p}' f`, `awk '${p}' ${a} f`, `gawk ${a} '${p}' f`);
  }
}
for (const c of ["grep", "rg", "sd", "sed", "awk", "gawk", "nawk", "node", "bun", "python3", "perl", "ruby"]) {
  for (const f of ["-e", "-f", "-A", "-i", "-c", "-p", "-E", "--regexp", "--file", "--context", "--eval", "-F", "-v", "--source", "-g", "-tpy", "-e'x'", "-ie", "--opt=/tmp/a=b", "-o=/tmp/out", "--", "-"]) {
    for (const rest of ["pat /etc/passwd", "'pat' f", "$(cat /etc/shadow) f", "\"$(cat /etc/shadow)\" f", "3 /etc/passwd", "$N /etc/passwd", "'' pat f", "<<< x pat f", ""]) {
      out.push(`${c} ${f} ${rest}`.trimEnd());
    }
  }
}
// arity / comments
for (const c of ["git checkout main", "Git Checkout", "npm run dev", "docker compose up --build", "constructor x", "__proto__", "Constructor", "unknown-tool --flag", "aws s3 ls", "gh pr view", "bun x y", "İt x", "gİt x", "git", "NPM RUN dev"]) out.push(c);
for (const c of ["# c\nfind /home", "\u00a0# c\nls", "\t# c\n\nls\n", "  \n  ls  \n", "# a\n# b", "ls\u2028# x", "\r\n# x\r\nls", "\ufeff# x\nls"]) out.push(c);

// The argument lists of the Go port's own edge-case tests (bashanalysis/extras_test.go): each argument is
// single-quoted, so the stand-in words read it as one literal value, exactly the list the Go test passes.
const q = (cmd, args) => `${cmd} ${args.map((a) => `'${a}'`).join(" ")}`;
for (const f of ["-n", "-E", "-r", "-s", "-u", "-z", "-nEr", "--quiet", "--silent", "--regexp-extended", "--separate", "--unbuffered", "--null-data", "--posix", "--debug", "--sandbox", "-x"]) out.push(q("sed", [f, "p", "f"]));
for (const a of [["--", "-e", "p"], ["-n", "--", "p", "-i"], ["-ep", "f"], ["-nep", "f"], ["--expression"], ["--expression="], ["-n", "!"], ["-n", ""], ["  "]]) out.push(q("sed", a));
for (const c of ["w", "W", "r", "R", "e", "v", "a", "i", "c", "s", "y", "k", "l5", "q5", "Q5", "l 5", "q", "Q", "p5", "s/a/b/w", "s/a/b/e", "s/a/b/W", "s/a/b/x", "s/a/b/", "s/a/b/g", "s/a/b/pIiMm", "s/a/b/9", "s/a/b/0",
  "};{", "{p", "{p}", "{p};{p}", "{{p}}", "{p}p", "{p;}", "1!!p", "1 ! p", "!", "p\np", "p;;p", " \tp ", "pp", "",
  "$p", "1p", "12p", "1,3p", "1,$p", "1 , 3 p", "2~3p", "1~p", "\\,x,p", "/x/p", "/x/Ip", "/x/IMp", "/x/,/y/p", "1,+2p", "1,~2p", "0~2p", "1,p", "1,+p", "1,~p", "/x", "\\", "\\xp", "/x/Xp", "1,/y", "1~",
  "s/a\\/b/c/", "s/[^/]x/c/", "s/[]]/c/", "s/[^]]/c/", "s/[[:alpha:]/]/c/", "s/[[:alpha:]]/c/", "s/[[:alpha/c/", "s/[[.a.]]/c/", "s/[[=a=]]/c/", "s/[[x]]/c/", "s/[]/]/c/", "s/a/\\//", "s/a/[]/", "s/a/b\\n/", "s/a/b\n/", "s/a\nb/c/", "y/abc/\\n\\t\\\\/", "y/abc/\\n\\\\/", "y/a\n/b/", "y/a/b/ p", "y/a/b/ ;p", "s,a\\,b,c,", "s/a\\\nb/c/",
  ":a;ba", ":a\nta", "Ta", ":", "b", "{b}", ": a;p", "#c w x", "#c\nw x", "p #c", "s/😀/x/", "s😀a😀b😀", "s/\\😀/x/", "s/é/x/g", "   "]) out.push(q("sed", ["-n", c]), q("sed", [c]));
for (const d of ["!", ",", "|", "#", "%", ":", ";", "@", "[", "]", "^", "_", "`", "{", "}", "~", ".", "-", "a", "5", " ", "\\", "\t", "/"]) out.push(q("sed", [`s${d}a${d}b${d}`]));
for (const a of [["{print}", "-F:", "data"], ["{print}", "-v", "x=1"], ["--", "-x", "data"], ["-v", "n=3", "NR>=1", "d"], ["-F", "|", "{print $1}", "d"], ["{print}", "NR>=1"], ["NR>=1", "{print}"], ["-F"], ["-Fx", "p"], ["-vx", "p"]]) out.push(q("awk", a));
for (const u of ["find ';' -exec rm x", "find . -ok rm {} ;", "find . -okdir rm {} ;", "bash --norc script", "sudo sudo sudo bash -c x", "sudo sudo sudo sudo bash -c x", "find . -exec sh {} ; -c x", "grep -c x f", "bash -c -x", "bash -c '", "xargs -I {} rm {}", "find . -exec rm {} \\;", "env 1A=b ls", "env A1=b ls", "env _A=b ls", "env AB ls", "/usr/bin/sudo ls", "/usr/bin/ ls", "sudo 😀 ls", "env A=😀 B=é grep x", "flock /tmp/l -c ls", "timeout 5 ls", "nice -n 5 ls -l", "sudo -- -l", "sort --random-sort f", "sort o", "sort -", "sort --outputs", "g\u0130t x", "\u00a0# c\nls", "\u2005# c\nls"]) out.push(u);
fs.writeFileSync(process.argv[2], JSON.stringify([...new Set(out)]));
console.log(`extra=${new Set(out).size}`);
