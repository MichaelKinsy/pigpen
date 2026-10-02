/** Environment variable names for client configuration. Explicit options take precedence. */
export const ENV = {
  /** Required API key; used when `apiKey` is omitted. */
  apiKey: "TYPESAFE_API_KEY",
  /** API root; defaults to `https://api.typesafe.ai`. */
  baseURL: "TYPESAFE_BASE_URL",
  /** Default model name; defaults to `jev-latest`. */
  defaultModel: "TYPESAFE_DEFAULT_MODEL",
  /** Log level; defaults to `warn`. */
  logLevel: "TYPESAFE_LOG_LEVEL",
} as const;

export type EnvVar = (typeof ENV)[keyof typeof ENV];

/** Read a trimmed environment value, returning `undefined` for missing or blank values. */
export const readEnv = (name: EnvVar): string | undefined => {
  if (typeof process === "undefined" || !process.env) return undefined;
  return process.env[name]?.trim() || undefined;
};

/** Return the explicit value, falling back to the environment. */
export const fromCodeOrEnv = (fromCode: string | undefined, envVar: EnvVar): string | undefined =>
  fromCode ?? readEnv(envVar);
