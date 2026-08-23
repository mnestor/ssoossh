// Registers jest-dom's matchers (toBeInTheDocument, toBeDisabled, …) on
// vitest's Assertion type.
//
// vitest-setup.ts is the runtime half — it installs the matchers before each
// test file. svelte-check does not read setup files, so without this the
// same matchers type-check as missing even though they work at runtime.
import '@testing-library/jest-dom/vitest';

// jest-axe's toHaveNoViolations augments jest's expect, not vitest's, so
// svelte-check reports the matcher as missing without this. The ambient
// declaration for the jest-axe module itself is in jest-axe.d.ts; see the
// comment there for why it cannot live in this file.
declare module 'vitest' {
	interface Assertion {
		toHaveNoViolations(): void;
	}
	interface AsymmetricMatchersContaining {
		toHaveNoViolations(): void;
	}
}
