// jest-axe ships no type declarations of its own. This has to live in a file
// with no top-level import or export: inside a module file, `declare module`
// is read as an augmentation of an already-typed module rather than as an
// ambient declaration for an untyped one, and the error stays.
//
// The vitest matcher half is in testing.d.ts, which is a module file because
// augmenting vitest's Assertion requires one.
declare module 'jest-axe' {
	export function axe(html: Element | string, options?: unknown): Promise<unknown>;
	// Shaped to satisfy vitest's expect.extend(), which wants a record of
	// matcher functions returning a pass/message pair.
	export const toHaveNoViolations: {
		toHaveNoViolations(received: unknown): { pass: boolean; message: () => string };
	};
}
