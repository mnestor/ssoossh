import { beforeEach, describe, expect, it, vi } from 'vitest';

import { preselect, rememberedPrincipals, rememberPrincipals, storageKey } from './principals.js';

// Test methodology: stub localStorage per case and drive the module's three
// functions. What matters: a selection survives per user and per request
// type, anything unreadable reads as nothing remembered, and the picker
// starts from the remembered principals the request offers or falls back.

/** stubStorage installs a working localStorage seeded with `seed`. */
function stubStorage(seed: Record<string, string> = {}) {
	const store = new Map(Object.entries(seed));
	const api = {
		getItem: vi.fn((key: string) => store.get(key) ?? null),
		setItem: vi.fn((key: string, value: string) => void store.set(key, value))
	};
	vi.stubGlobal('localStorage', api);
	return { api, store };
}

beforeEach(() => {
	vi.unstubAllGlobals();
});

describe('rememberedPrincipals', () => {
	it('should return nothing when nothing is stored', () => {
		stubStorage();
		expect(rememberedPrincipals('alice', 'user')).toEqual([]);
	});

	it('should return what was stored for the type', () => {
		stubStorage({ [storageKey('alice')]: JSON.stringify({ user: ['alice', 'alice-admin'] }) });
		expect(rememberedPrincipals('alice', 'user')).toEqual(['alice', 'alice-admin']);
	});

	it('should keep the types apart', () => {
		stubStorage({ [storageKey('alice')]: JSON.stringify({ user: ['alice-admin'] }) });
		expect(rememberedPrincipals('alice', 'pam')).toEqual([]);
	});

	it('should keep the users apart', () => {
		stubStorage({ [storageKey('alice')]: JSON.stringify({ user: ['alice'] }) });
		expect(rememberedPrincipals('bob', 'user')).toEqual([]);
	});

	const unreadable: [string, string][] = [
		['text that is not JSON', 'not json'],
		['a bare list', JSON.stringify(['alice'])],
		['a type holding something other than strings', JSON.stringify({ user: [1, 2] })],
		['null', 'null']
	];
	for (const [name, raw] of unreadable) {
		it(`should read ${name} as nothing remembered`, () => {
			stubStorage({ [storageKey('alice')]: raw });
			expect(rememberedPrincipals('alice', 'user')).toEqual([]);
		});
	}

	it('should read blocked site data as nothing remembered', () => {
		vi.stubGlobal('localStorage', {
			getItem: () => {
				throw new Error('site data blocked');
			}
		});
		expect(rememberedPrincipals('alice', 'user')).toEqual([]);
	});
});

describe('rememberPrincipals', () => {
	it('should store the selection for the type', () => {
		stubStorage();

		rememberPrincipals('alice', 'pam', ['root']);

		expect(rememberedPrincipals('alice', 'pam')).toEqual(['root']);
	});

	it('should leave the other types as they were', () => {
		stubStorage({ [storageKey('alice')]: JSON.stringify({ user: ['alice'] }) });

		rememberPrincipals('alice', 'pam', ['root']);

		expect(rememberedPrincipals('alice', 'user')).toEqual(['alice']);
	});

	it('should not throw when site data is blocked', () => {
		vi.stubGlobal('localStorage', {
			getItem: () => null,
			setItem: () => {
				throw new Error('site data blocked');
			}
		});
		expect(() => rememberPrincipals('alice', 'user', ['alice'])).not.toThrow();
	});
});

describe('preselect', () => {
	const cases: [string, string[], string[], string[], string[]][] = [
		[
			'remembered and offered',
			['alice-admin'],
			['alice', 'alice-admin'],
			['alice'],
			['alice-admin']
		],
		['in the order offered', ['b', 'a'], ['a', 'b', 'c'], ['a'], ['a', 'b']],
		['dropping what is no longer offered', ['gone', 'a'], ['a', 'b'], ['b'], ['a']],
		['nothing remembered', [], ['a', 'b'], ['a'], ['a']],
		['nothing remembered is offered', ['gone'], ['a', 'b'], ['a'], ['a']]
	];
	for (const [name, remembered, offered, fallback, want] of cases) {
		it(`should start from ${name}`, () => {
			expect(preselect(remembered, offered, fallback)).toEqual(want);
		});
	}
});
