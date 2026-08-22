// Stryker configuration for mutation testing
// Scoped to test files with the strongest assertions

export default {
	mutate: [
		// Frontend source files under test (TypeScript only due to Svelte 5.x compatibility)
		'src/lib/**/*.ts',
		// Exclude test files themselves
		'!**/*.test.ts',
	],
	testRunner: 'vitest',
	reporters: ['html', 'clear-text'],
	// Do not gate on mutation score initially - just establish baseline
	timeoutMs: 5000,
	timeoutFactor: 1.25,
	// Exclude node_modules and build outputs
	ignorePatterns: [
		'node_modules',
		'dist',
		'.svelte-kit'
	],
}
