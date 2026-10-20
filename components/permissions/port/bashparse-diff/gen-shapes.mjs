// Shape corpus for the bash parse differential: every construct the walkers read, in combination,
// so the grammar's recovery and the two corrections meet inputs the original's tests never wrote.
// Output: JSON array of strings, deterministic.
//   node gen-shapes.mjs <out.json>
// shapes.txt: one input per line; a block between lines "@@" and "@@@" is one multi-line input.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = new Set();
const add = (s) => out.add(s);

// ---- standalone constructs
const lines = fs.readFileSync(path.join(here, "shapes.txt"), "utf8").split("\n");
const whole = [];
let block = null;
for (const l of lines) {
  if (l === "@@") block = [];
  else if (l === "@@@") {
    add(block.join("\n"));
    whole.push(block.join("\n"));
    block = null;
  } else if (block) block.push(l);
  else if (l.length) {
    add(l);
    whole.push(l);
  }
}
const program = whole.join("\n");
add(program);
add(program.replaceAll("\n", "\r\n"));
// Runs of neighbours as one program, so statements follow one another the way a script's do.
for (let i = 0; i + 5 <= whole.length; i += 3) add(whole.slice(i, i + 5).join("\n"));
for (let i = 0; i + 3 <= whole.length; i += 2) add(whole.slice(i, i + 3).join(" ; "));

// ---- combinatorial: heredoc operator x tail x body x host
const hosts = [
  (h) => h,
  (h) => `if true; then ${h}\nfi`,
  (h) => `(${h}\n)`,
  (h) => `{ ${h}\n}`,
  (h) => `echo $(${h}\n)`,
  (h) => `while true; do ${h}\ndone`,
  (h) => `f() { ${h}\n}`,
  (h) => `a && ${h}`,
  (h) => `a | ${h}`,
  (h) => `a ; ${h}`,
  (h) => `${h}\nls`,
];
const heads = ["cat", "git commit -F -", "xargs grep foo", "x=1 cat", "cat a", "cat 2>&1", "cat >p", "cat <q", "cat <<A", ""];
const ops = ["<<EOF", "<<-EOF", "<<'EOF'", '<<"EOF"', "<<\\EOF", "0<<EOF", "<< EOF", "<<EOF2", "<<E\\OF", "<<-'E F'"];
const tails = [
  "", " > o", " >> o", " 2>&1", " < i", " <<< x", " | b", " |& b", " && b", " || b", " ; b", " & ", " arg", " arg > o", " > o arg",
  " 2>/dev/null arg", " | a | b > o", " && a | b > o", " && a > o | b", " | a && b", " ) ", " $(x)", " push --force", " > o | b arg",
  " > o && b", " ;; b", " # c", " \\\n x",
];
const bodies = ["\nx\nEOF", "\n$(rm x)\nEOF", "\nEOF", "\n\nEOF\nnext", "\nx", "", "\nx\n  EOF", "\nx\n\tEOF\n", "\nx\nEOF2\nEOF"];
let n = 0;
for (const host of hosts)
  for (const head of heads)
    for (const op of ops)
      for (const tail of tails)
        for (const body of bodies) {
          n++;
          // thin the cross product deterministically: all of the plain-operator slice, one in seven of the rest
          if (n % 7 !== 0 && !(op === "<<EOF" && host === hosts[0])) continue;
          add(host(`${head ? head + " " : ""}${op}${tail}${body}`));
        }

// ---- combinatorial: redirects
const redirOps = [">", ">>", "<", "<<<", ">&", "<&", ">&-", "<&-", "&>", "&>>", ">|", "<>", "2>", "2>>", "2<", "2>&1", "2>&-", "10>", "{fd}>", "{fd}>&-", "1>&2", "0<", "3<>"];
const targets = ["o", "~/o", "$x", "$(x)", "<(x)", '"o p"', "'o'", "a\\ b", "o{1,2}", "*", "-", "2", "&1", ""];
const words = ["", " arg", " a b", " $(x)", " >p", " <q arg", " 2>&1", " | b", " && b", " ; b", " &"];
for (const cmd of ["cat", "git", "xargs rm", "a=1 cat", "cat a", "a | cat", "a && cat", "{ a; }", "(a)", "if a; then b; fi"])
  for (const op of redirOps)
    for (const t of targets)
      for (const w of words) {
        n++;
        if (n % 5 !== 0) continue;
        add(`${cmd} ${op}${t}${w}`);
        if (n % 25 === 0) add(`${cmd} ${op} ${t}${w}`);
        if (n % 35 === 0) add(`${op}${t} ${cmd}${w}`);
      }

// ---- unicode / odd characters (surrogates are lone halves a JS string can hold)
const odd = [
  "echo é", "echo 😀", "echo 😀 > 😀.txt", "cat <<É\nx\nÉ", "cat <<😀 | rm x\nx\n😀", "echo \u00a0x", "echo \u2028x", "echo \u0085x",
  "echo \ufeffx", "\ufeffls", "ls\u0000", "ls\u0000 -la", "echo \u0000", "echo \x01\x02", "echo \x7f", "echo \ud800", "echo \udc00x", "echo \ud83d",
  "echo \ud83dx", "cat <<EOF\n\ud800\nEOF", "echo 'é😀' \"é😀\" $(echo é😀)", "é=1", "x=é cmd", "echo $é", "echo ${é}", "é() { :; }",
  "echo 😀😀😀 > o\nls", "\t\tls\t-la\t", "ls\r\nrm x\r\n", "ls\rrm x", "ls\r", "\r\n", "\n", "\n\n\n", " ", "", "\t", "   ls   ",
  "ls\f-la", "ls\v-la", "ls\u200bx", "echo \u0301x", "echo e\u0301", "echo \u{1F468}\u200d\u{1F469}", "echo \u{10FFFF}", "echo \u{10000}",
  "cat <<EOF\r\nx\r\nEOF\r\n", "cat <<EOF\r\nx\r\nEOF", "cat <<EOF \r\nx\r\nEOF", "cat <<\"EOF\"\r\nx\r\nEOF",
  "a\\\r\nb", "echo a \\\r\n b", "echo 'a\r\nb'", "# c\r\nls", "ls # c\r\n",
  "echo é > o é", "cat <<EOF | rm é\né\nEOF", "echo \u{1F600}\ud800 > o", "ls \ud800\udc00",
];
for (const s of odd) add(s);

// ---- long inputs
add("echo " + "a ".repeat(5000));
add("echo " + "a".repeat(20000));
add(Array.from({ length: 800 }, (_, i) => `echo ${i}`).join("\n"));
add(Array.from({ length: 800 }, (_, i) => `echo ${i}`).join(" && "));
add(Array.from({ length: 300 }, (_, i) => `echo ${i}`).join(" | "));
add("(".repeat(200) + "ls" + ")".repeat(200));
add("$(".repeat(100) + "ls" + ")".repeat(100));
add("echo " + '"$('.repeat(40) + "ls" + ')"'.repeat(40));
add("{ ".repeat(100) + "ls" + "; }".repeat(100));
add("if a; then ".repeat(60) + "ls" + "; fi".repeat(60));
add("cat <<EOF\n" + "line\n".repeat(5000) + "EOF");
add("cat <<EOF | rm x\n" + "line $x $(ls)\n".repeat(2000) + "EOF\nls");
add("a >o ".repeat(500) + "b");
add("echo " + "'a b' ".repeat(3000));
add("echo " + "$x ".repeat(3000));
add("x=1 ".repeat(500) + "cmd");
add("a;".repeat(5000));
add("a &&".repeat(2000) + " b");
add("(((".repeat(300) + ")))".repeat(300));

fs.writeFileSync(process.argv[2], JSON.stringify([...out]));
console.log(`shapes=${out.size}`);
