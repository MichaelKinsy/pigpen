import type { ArgWord } from "./node-text";

// ── Public surface ─────────────────────────────────────────────────────────

/**
 * Whether an `awk` invocation's arguments withdraw its read claim.
 *
 * The claim stands only when the command line is **proven** read-only: the
 * only options are `-F` and `-v`, the program arrives as literal text, and
 * that text holds none of the constructs through which a POSIX or GNU awk
 * program writes a file or runs a command.
 *
 * The program check is a character scan, not a parse, so it over-retracts:
 * `NR>=100` withdraws the claim because `>` is also `print >`'s redirect.
 * That costs relief and never safety — a withdrawn claim consults both
 * surfaces, exactly as `awk` did before it joined the core.
 *
 * A computed word withdraws the claim wherever it sits, since it could spell
 * `-f` or the program itself.
 */
export function awkWithdrawsReadClaim(argWords: readonly ArgWord[]): boolean {
  if (argWords.some(({ computed }) => computed)) return true;
  const program = selectProgram(argWords.map(({ value }) => value));
  return program === null || !provesReadOnlyProgram(program);
}

// ── The option walk ────────────────────────────────────────────────────────

/** `-F sep` and `-v name=value`: the options every awk shares that cannot write. */
const VALUE_OPTIONS: ReadonlySet<string> = new Set(["-F", "-v"]);

/**
 * The program text an argument list hands `awk`, or `null` when the list
 * cannot be proven to hand it only that.
 *
 * The program is the first positional. An option-looking word anywhere else
 * withdraws the claim: whether an awk stops reading options at the program
 * varies by implementation, so a trailing `-f` is not assumed to be a file.
 */
function selectProgram(values: readonly string[]): string | null {
  let program: string | null = null;
  let optionsEnded = false;
  for (let i = 0; i < values.length; i++) {
    const value = values[i];
    if (optionsEnded || !isOptionWord(value)) {
      program ??= value;
      continue;
    }
    if (program !== null) return null;
    if (value === "--") {
      optionsEnded = true;
      continue;
    }
    if (VALUE_OPTIONS.has(value)) {
      if (i + 1 >= values.length) return null;
      i++;
      continue;
    }
    if (!isAttachedValueOption(value)) return null;
  }
  return program;
}

function isOptionWord(value: string): boolean {
  return value.startsWith("-") && value !== "-";
}

/** `-F:` or `-vn=3`: an allowed option carrying its value in the same word. */
function isAttachedValueOption(value: string): boolean {
  return VALUE_OPTIONS.has(value.slice(0, 2)) && value.length > 2;
}

// ── The program scan ───────────────────────────────────────────────────────

/**
 * Text whose presence means the program may write a file or run a command.
 *
 * `>` covers `print >` and `>>`; `|` covers a pipe to or from a command and
 * gawk's `|&` coprocess; `system` covers `system()`; `@` covers gawk's
 * `@include`, `@load`, and indirect function calls.
 */
const WRITE_CAPABLE_TEXT: readonly string[] = [">", "|", "system", "@"];

function provesReadOnlyProgram(program: string): boolean {
  return !WRITE_CAPABLE_TEXT.some((text) => program.includes(text));
}
