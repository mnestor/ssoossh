import js from '@eslint/js';
import ts from 'typescript-eslint';
import svelte from 'eslint-plugin-svelte';
import prettier from 'eslint-config-prettier';
import globals from 'globals';

/** @type {import('eslint').Linter.FlatConfig[]} */
export default [
	js.configs.recommended,
	...ts.configs.recommended,
	...svelte.configs['flat/recommended'],
	...svelte.configs.prettier,
	prettier,
	...svelte.configs['flat/prettier'],
	{
		languageOptions: {
			globals: { ...globals.browser, ...globals.node }
		}
	},
	{
		// *.svelte.ts carries runes ($state, $derived) outside a component, so
		// it needs the Svelte parser too — the plain TS parser reads `$state<T>(…)`
		// as a syntax error.
		files: ['**/*.svelte', '**/*.svelte.ts'],
		languageOptions: { parserOptions: { parser: ts.parser } }
	},
	{
		// src/lib/api/generated is tygo output — see tygo.yaml.
		ignores: ['build/', '.svelte-kit/', 'dist/', 'src/lib/api/generated/']
	},
	{
		rules: { '@typescript-eslint/no-explicit-any': 'off' }
	}
];
