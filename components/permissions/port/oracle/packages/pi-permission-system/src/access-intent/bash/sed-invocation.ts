import type { ArgWord } from "./node-text";

// ── Public surface ─────────────────────────────────────────────────────────

/**
 * Whether a `sed` invocation's arguments withdraw its read claim.
 *
 * The claim stands only when the whole command line is **proven** read-only:
 * every option is one the allowlist knows, and every script parses under a
 * grammar that admits no command able to write a file or run one. Anything
 * the walk does not recognize withdraws the claim, so an unknown option or
 * command costs one ask rather than a missed write.
 *
 * A computed word withdraws the claim wherever it sits: GNU `sed` permutes
 * options, so even a trailing `"$f"` can arrive as `-i`.
 *
 * The walk never needs to know which `sed` is installed. Where GNU and BSD
 * would read the same argument list differently — an `-e` after the first
 * positional, which GNU takes as a script and BSD as a file — it withdraws.
 */
export function sedWithdrawsReadClaim(argWords: readonly ArgWord[]): boolean {
  if (argWords.some(({ computed }) => computed)) return true;
  const scripts = selectScripts(argWords.map(({ value }) => value));
  if (scripts === null) return true;
  return !scripts.every(provesReadOnlyScript);
}

// ── The option walk ────────────────────────────────────────────────────────

/** Short options that take no argument and cannot write: `-n -E -r -s -u -z`. */
const FLAG_LETTERS: ReadonlySet<string> = new Set([
  "n",
  "E",
  "r",
  "s",
  "u",
  "z",
]);

/**
 * Long options that take no argument and cannot write, matched exactly.
 *
 * Abbreviations are deliberately absent: GNU accepts `--qui` for `--quiet`
 * and `--in` for `--in-place` alike, and matching whole words only is what
 * lets every abbreviation withdraw without a prefix rule.
 */
const FLAG_WORDS: ReadonlySet<string> = new Set([
  "--quiet",
  "--silent",
  "--regexp-extended",
  "--separate",
  "--unbuffered",
  "--null-data",
  "--posix",
  "--debug",
  "--sandbox",
]);

const EXPRESSION_OPTION = "--expression";

/**
 * The scripts an argument list hands `sed`, or `null` when the list cannot be
 * proven to hand it only those.
 *
 * Every `-e` value is a script; with none, the first positional is. Options
 * are recognized wherever they sit until a `--`, because GNU permutes them.
 */
function selectScripts(values: readonly string[]): string[] | null {
  const scripts: string[] = [];
  let firstPositional: string | undefined;
  let optionsEnded = false;
  for (let i = 0; i < values.length; i++) {
    const value = values[i];
    if (optionsEnded || !isOptionWord(value)) {
      firstPositional ??= value;
      continue;
    }
    if (value === "--") {
      optionsEnded = true;
      continue;
    }
    if (FLAG_WORDS.has(value)) continue;
    const expression = expressionOf(value, values[i + 1]);
    if (expression === null) return null;
    // An -e after a positional is a script to GNU and a file to BSD.
    if (expression.script !== undefined && firstPositional !== undefined) {
      return null;
    }
    if (expression.script !== undefined) scripts.push(expression.script);
    if (expression.consumesNext) i++;
  }
  if (scripts.length > 0) return scripts;
  return firstPositional === undefined ? null : [firstPositional];
}

function isOptionWord(value: string): boolean {
  return value.startsWith("-") && value !== "-";
}

/** What one option word contributes: a script, and whether it took the next word. */
interface OptionReading {
  readonly script?: string;
  readonly consumesNext: boolean;
}

/**
 * Read one option word, or `null` when it is not one the allowlist proves.
 *
 * A short cluster may hold only flag letters until an `e`, which ends it: the
 * rest of the word is the script, or the next word is when nothing follows.
 */
function expressionOf(
  value: string,
  next: string | undefined,
): OptionReading | null {
  if (value.startsWith("--")) return longExpressionOf(value, next);
  for (let j = 1; j < value.length; j++) {
    const letter = value[j];
    if (letter === "e") {
      const attached = value.slice(j + 1);
      if (attached !== "") return { script: attached, consumesNext: false };
      return next === undefined ? null : { script: next, consumesNext: true };
    }
    if (!FLAG_LETTERS.has(letter)) return null;
  }
  return { consumesNext: false };
}

function longExpressionOf(
  value: string,
  next: string | undefined,
): OptionReading | null {
  if (value.startsWith(`${EXPRESSION_OPTION}=`)) {
    return {
      script: value.slice(EXPRESSION_OPTION.length + 1),
      consumesNext: false,
    };
  }
  if (value !== EXPRESSION_OPTION || next === undefined) return null;
  return { script: next, consumesNext: true };
}

// ── The script grammar ─────────────────────────────────────────────────────

/**
 * Commands that take no argument and neither write a file nor run a command.
 *
 * `l`, `q`, and `Q` accept an optional number, read by the scanner.
 */
const READ_ONLY_COMMANDS: ReadonlySet<string> = new Set([
  "p",
  "P",
  "d",
  "D",
  "n",
  "N",
  "g",
  "G",
  "h",
  "H",
  "x",
  "=",
  "z",
  "F",
  "l",
  "q",
  "Q",
]);

/** The commands among {@link READ_ONLY_COMMANDS} that take an optional number. */
const NUMBERED_COMMANDS: ReadonlySet<string> = new Set(["l", "q", "Q"]);

/**
 * Whether a script is made only of commands that cannot write a file or run
 * a command.
 *
 * An allowlist grammar: `w`, `W`, `r`, `R`, `e`, `v`, `a`, `i`, `c`, an `s`
 * flag outside `g p i I m M` and digits (so `s///w` and `s///e`), and any
 * character the scanner does not recognize end the proof.
 */
function provesReadOnlyScript(script: string): boolean {
  return new ScriptScanner(script).provesReadOnly();
}

/** Flags an `s` command may carry without writing a file or running one. */
const SUBSTITUTION_FLAGS = /[gpiImM0-9]/;

/** Flags a regex address may carry: GNU's case-insensitive and multiline. */
const ADDRESS_FLAGS: ReadonlySet<string> = new Set(["I", "M"]);

/** Commands whose argument is a label running to the end of the command. */
const LABEL_COMMANDS: ReadonlySet<string> = new Set(["b", "t", "T", ":"]);

/** How an address position read: nothing there, an address, or a malformed one. */
type AddressReading = "none" | "read" | "invalid";

/** A single left-to-right pass over one script. */
class ScriptScanner {
  private position = 0;
  private openBlocks = 0;

  constructor(private readonly source: string) {}

  provesReadOnly(): boolean {
    this.skipSeparators();
    while (!this.atEnd()) {
      if (!this.readCommand()) return false;
      this.skipSeparators();
    }
    return this.openBlocks === 0;
  }

  /** One `[address[,address]][!…]command`, and what may follow it. */
  private readCommand(): boolean {
    if (this.peek() === "#") return this.skipComment();
    if (!this.readAddresses()) return false;
    this.skipBlanks();
    while (this.peek() === "!") {
      this.position++;
      this.skipBlanks();
    }
    const command = this.peek();
    if (command === undefined) return false;
    this.position++;
    if (command === "{") {
      this.openBlocks++;
      return true;
    }
    if (!this.readCommandBody(command)) return false;
    this.skipBlanks();
    return this.atEnd() || this.atSeparator() || this.closesBlock();
  }

  /** What follows a command letter, up to where a separator must come. */
  private readCommandBody(command: string): boolean {
    if (command === "}") return this.closeBlock();
    if (command === "s") return this.readSubstitution();
    if (command === "y") return this.readTransliteration();
    if (LABEL_COMMANDS.has(command)) return this.readLabel(command);
    if (!READ_ONLY_COMMANDS.has(command)) return false;
    if (NUMBERED_COMMANDS.has(command)) {
      this.skipBlanks();
      this.skipDigits();
    }
    return true;
  }

  /** An optional address, then an optional `,` and a second one. */
  private readAddresses(): boolean {
    const first = this.readAddress();
    if (first === "invalid") return false;
    if (first === "none") return true;
    this.skipBlanks();
    if (this.peek() !== ",") return true;
    this.position++;
    this.skipBlanks();
    return this.readSecondAddress();
  }

  /** A line number, `first~step`, `$`, `/re/`, or `\cREc`. */
  private readAddress(): AddressReading {
    const next = this.peek();
    if (next === "$") {
      this.position++;
      return "read";
    }
    if (next === "/" || next === "\\") return this.readRegexAddress();
    if (!this.skipDigits()) return "none";
    if (this.peek() === "~") {
      this.position++;
      this.skipDigits();
    }
    return "read";
  }

  private readRegexAddress(): AddressReading {
    if (this.peek() === "\\") this.position++;
    const delimiter = this.takeDelimiter();
    if (delimiter === null || !this.readRegex(delimiter)) return "invalid";
    while (ADDRESS_FLAGS.has(this.peek() ?? "")) this.position++;
    return "read";
  }

  /** The end of a range: an address, or GNU's `+N` / `~N`. */
  private readSecondAddress(): boolean {
    const sign = this.peek();
    if (sign === "+" || sign === "~") {
      this.position++;
      return this.skipDigits();
    }
    return this.readAddress() === "read";
  }

  /** `s/regex/replacement/flags`, whose flags must not write or execute. */
  private readSubstitution(): boolean {
    const delimiter = this.takeDelimiter();
    if (delimiter === null) return false;
    if (!this.readRegex(delimiter)) return false;
    if (!this.readLiteral(delimiter)) return false;
    while (SUBSTITUTION_FLAGS.test(this.peek() ?? "")) this.position++;
    return true;
  }

  /** `y/source/target/`, whose operands are character lists, not regexes. */
  private readTransliteration(): boolean {
    const delimiter = this.takeDelimiter();
    if (delimiter === null) return false;
    return this.readLiteral(delimiter) && this.readLiteral(delimiter);
  }

  /**
   * A label, running to `;` or a newline.
   *
   * GNU ends a label there; BSD runs it to the newline, so it sees fewer
   * commands than this reading does, never more. A `}` stays in the label,
   * which leaves its block open and the proof unfinished — the reading on
   * which the two agree least.
   */
  private readLabel(command: string): boolean {
    this.skipBlanks();
    const start = this.position;
    while (!this.atEnd() && !this.atSeparator()) this.position++;
    return command !== ":" || this.position > start;
  }

  private closeBlock(): boolean {
    this.openBlocks--;
    return this.openBlocks >= 0;
  }

  /** A `}` directly after a command ends it, as a separator would. */
  private closesBlock(): boolean {
    return this.peek() === "}";
  }

  private skipComment(): boolean {
    while (!this.atEnd() && this.peek() !== "\n") this.position++;
    return true;
  }

  /**
   * The delimiter a regex or `s`/`y` command opens with, or `null` when it is
   * one this grammar does not accept.
   *
   * A letter, digit, backslash, blank, or newline delimiter is legal to GNU in
   * places and a trap everywhere: `\n` and `\d` stop meaning what they say.
   */
  private takeDelimiter(): string | null {
    const delimiter = this.peek();
    if (delimiter === undefined || !/[!-/:-@[-`{-~]/.test(delimiter)) {
      return null;
    }
    if (delimiter === "\\") return null;
    this.position++;
    return delimiter;
  }

  /**
   * A regex up to its closing delimiter, bracket expressions included.
   *
   * A delimiter inside a bracket expression ends the proof: BSD reads `[/]`
   * as a bracket and GNU ends the regex at its `/`, so the two disagree about
   * where every later section starts.
   */
  private readRegex(delimiter: string): boolean {
    while (!this.atEnd()) {
      const next = this.peek();
      if (next === "\n") return false;
      if (next === delimiter) {
        this.position++;
        return true;
      }
      if (next === "\\") {
        this.position += 2;
        continue;
      }
      if (next === "[") {
        if (!this.readBracket(delimiter)) return false;
        continue;
      }
      this.position++;
    }
    return false;
  }

  /** `[…]`, with `]` literal first and `[:class:]` / `[.x.]` / `[=x=]` inside. */
  private readBracket(delimiter: string): boolean {
    this.position++;
    if (this.peek() === "^") this.position++;
    if (this.peek() === "]") this.position++;
    while (!this.atEnd()) {
      const next = this.peek();
      if (next === "]") {
        this.position++;
        return true;
      }
      if (next === delimiter || next === "\n") return false;
      if (next === "[" && this.opensBracketClass()) {
        if (!this.skipBracketClass(delimiter)) return false;
        continue;
      }
      this.position++;
    }
    return false;
  }

  private opensBracketClass(): boolean {
    const marker = this.source[this.position + 1];
    return marker === ":" || marker === "." || marker === "=";
  }

  private skipBracketClass(delimiter: string): boolean {
    const marker = this.source[this.position + 1];
    const close = this.source.indexOf(`${marker}]`, this.position + 2);
    if (close === -1) return false;
    const body = this.source.slice(this.position + 2, close);
    if (body.includes(delimiter)) return false;
    this.position = close + 2;
    return true;
  }

  /**
   * A replacement or `y` operand up to its closing delimiter.
   *
   * Brackets mean nothing here, so `s/x/[]/` is the literal `[]`.
   */
  private readLiteral(delimiter: string): boolean {
    while (!this.atEnd()) {
      const next = this.peek();
      if (next === "\n") return false;
      if (next === delimiter) {
        this.position++;
        return true;
      }
      this.position += next === "\\" ? 2 : 1;
    }
    return false;
  }

  private skipSeparators(): void {
    while (this.atSeparator() || this.isBlank(this.peek())) this.position++;
  }

  private skipBlanks(): void {
    while (this.isBlank(this.peek())) this.position++;
  }

  /** Advance over digits; true when there was at least one. */
  private skipDigits(): boolean {
    const start = this.position;
    while (/[0-9]/.test(this.peek() ?? "")) this.position++;
    return this.position > start;
  }

  private atSeparator(): boolean {
    const next = this.peek();
    return next === ";" || next === "\n";
  }

  private isBlank(character: string | undefined): boolean {
    return character === " " || character === "\t";
  }

  private atEnd(): boolean {
    return this.position >= this.source.length;
  }

  private peek(): string | undefined {
    return this.source[this.position];
  }
}
