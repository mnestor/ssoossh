// Registers jest-dom's matchers (toBeInTheDocument, toBeDisabled, …) on
// vitest's Assertion type.
//
// vitest-setup.ts is the runtime half — it installs the matchers before each
// test file. svelte-check does not read setup files, so without this the
// same matchers type-check as missing even though they work at runtime.
import '@testing-library/jest-dom/vitest';
