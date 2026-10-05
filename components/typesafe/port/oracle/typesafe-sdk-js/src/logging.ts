import { TypeSafeError } from "./errors";
import type { Logger, LogLevel } from "./types";

/** Supported log levels, from most to least verbose. */
export const LOG_LEVELS: readonly LogLevel[] = ["debug", "info", "warn", "error", "off"];

export const DEFAULT_LOG_LEVEL: LogLevel = "warn";

const isLogLevel = (value: string): value is LogLevel =>
  (LOG_LEVELS as readonly string[]).includes(value);

/** Validate a configured log level, throwing `TypeSafeError` for unknown values. */
export const parseLogLevel = (value: string, source: string): LogLevel => {
  if (isLogLevel(value)) return value;
  throw new TypeSafeError(
    `Invalid log level "${value}" from ${source}. Expected one of: ${LOG_LEVELS.join(", ")}.`,
  );
};

// ---------------------------------------------------------------------------
// Loggers
// ---------------------------------------------------------------------------

const PREFIX = "[typesafe-sdk]";

/** Default console logger with the `[typesafe-sdk]` prefix. */
export const consoleLogger: Logger = {
  debug: (message, ...args) => console.debug(`${PREFIX} ${message}`, ...args),
  info: (message, ...args) => console.info(`${PREFIX} ${message}`, ...args),
  warn: (message, ...args) => console.warn(`${PREFIX} ${message}`, ...args),
  error: (message, ...args) => console.error(`${PREFIX} ${message}`, ...args),
};

const RANK: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3, off: 4 };

const drop = (): void => {};

/** Filter logger calls to the configured level and above. */
export const withLevel = (sink: Logger, level: LogLevel): Logger => {
  const enabled = (at: LogLevel): boolean => RANK[at] >= RANK[level];
  return {
    debug: enabled("debug") ? (message, ...args) => sink.debug(message, ...args) : drop,
    info: enabled("info") ? (message, ...args) => sink.info(message, ...args) : drop,
    warn: enabled("warn") ? (message, ...args) => sink.warn(message, ...args) : drop,
    error: enabled("error") ? (message, ...args) => sink.error(message, ...args) : drop,
  };
};

// ---------------------------------------------------------------------------
// Redaction
// ---------------------------------------------------------------------------

/** Credential headers that retain a key suffix for identification. */
const KEY_HEADERS: ReadonlySet<string> = new Set([
  "authorization",
  "proxy-authorization",
  "x-api-key",
]);

/** Headers whose values are redacted in full. */
const OPAQUE_HEADERS: ReadonlySet<string> = new Set(["cookie", "set-cookie"]);

/** Mask a key, preserving its scheme and the last four characters of secrets longer than eight. */
const redactKey = (value: string): string => {
  const [scheme, secret] = value.includes(" ") ? value.split(/\s+/, 2) : [undefined, value];
  const tail = secret && secret.length > 8 ? secret.slice(-4) : "";
  return `${scheme ? `${scheme} ` : ""}***${tail}`;
};

const redact = (name: string, value: string): string => {
  const lower = name.toLowerCase();
  if (KEY_HEADERS.has(lower)) return redactKey(value);
  if (OPAQUE_HEADERS.has(lower)) return "***";
  return value;
};

/** Copy headers with known credential values redacted. */
export const redactHeaders = (headers: Record<string, string>): Record<string, string> =>
  Object.fromEntries(Object.entries(headers).map(([name, value]) => [name, redact(name, value)]));
