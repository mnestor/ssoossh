import { describe, expect, it } from 'vitest';

import { isInternalPath } from './paths';

describe('isInternalPath', () => {
	it('should accept a plain path', () => {
		expect(isInternalPath('/dashboard')).toBe(true);
	});

	it('should accept a path carrying a query string', () => {
		expect(isInternalPath('/approve/abc?from=cli')).toBe(true);
	});

	// Protocol-relative: the browser reads //evil.example as another origin,
	// which is the whole class of bug this guard exists for.
	it('should reject a protocol-relative URL', () => {
		expect(isInternalPath('//evil.example/steal')).toBe(false);
	});

	it('should reject an absolute URL', () => {
		expect(isInternalPath('https://evil.example/steal')).toBe(false);
	});

	it('should reject a path-relative target', () => {
		expect(isInternalPath('dashboard')).toBe(false);
	});

	it('should reject an empty string', () => {
		expect(isInternalPath('')).toBe(false);
	});

	it('should reject a missing target', () => {
		expect(isInternalPath(null)).toBe(false);
	});

	it('should reject an undefined target', () => {
		expect(isInternalPath(undefined)).toBe(false);
	});
});
