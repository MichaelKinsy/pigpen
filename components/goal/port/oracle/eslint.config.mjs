import js from "@eslint/js";
import tseslint from "typescript-eslint";
import globals from "globals";

/**
 * Small lint gate (intentionally quiet):
 * - JS best-practice rules via eslint:recommended.
 * - tsc owns undefined-name / unused-symbol reporting with the configured
 *   strictness; the core no-unused-vars rule also misfires on TS
 *   interface-conformance parameters and re-exported symbols.
 * - no-regex-spaces is off: several test regexes deliberately match multiple
 *   literal spaces in rendered output.
 * - no-explicit-any applies only to extensions/ (the shipped product); test
 *   scaffolding may use `as any` freely.
 */
export default tseslint.config(
	{ ignores: ["node_modules/**", "experiments/**", "specs/**", "docs/**", ".pi/**"] },
	js.configs.recommended,
	{
		files: ["**/*.ts"],
		plugins: { "@typescript-eslint": tseslint.plugin },
		languageOptions: {
			parser: tseslint.parser,
			ecmaVersion: 2022,
			sourceType: "module",
			globals: globals.node,
		},
		rules: {
			"no-undef": "off",
			"no-unused-vars": "off",
			"no-regex-spaces": "off",
			"no-empty": ["error", { allowEmptyCatch: true }],
		},
	},
	{
		files: ["extensions/**/*.ts"],
		rules: {
			"@typescript-eslint/no-explicit-any": "error",
		},
	},
	{
		files: ["**/*.mjs"],
		languageOptions: { ecmaVersion: 2022, sourceType: "module", globals: globals.node },
		rules: {
			"no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
		},
	},
);
