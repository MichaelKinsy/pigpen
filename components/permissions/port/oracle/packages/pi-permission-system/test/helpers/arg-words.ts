import type { ArgWord } from "#src/access-intent/bash/node-text";

/**
 * Argument words the source spells exactly, as `WordReader.argWord` would read them
 * from literal source text.
 */
export function literalArgWords(...values: string[]): ArgWord[] {
  return values.map((value) => ({
    value,
    computed: false,
    mayLeadWithDash: value.startsWith("-"),
  }));
}

/**
 * An argument word whose value only the shell decides, with the leading-dash
 * fact stated rather than derived — `value` is the unresolved source spelling.
 */
export function computedArgWord(
  value: string,
  mayLeadWithDash: boolean,
): ArgWord {
  return { value, computed: true, mayLeadWithDash };
}
