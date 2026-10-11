import { beforeEach, describe, expect, it, vi } from 'vitest';

import { detail, DETAIL_STORAGE_KEY } from './detail.svelte';

// Test methodology: drive the singleton directly with localStorage stubbed
// per case. What matters: advanced is the default, only the two real levels
// are taken from storage, a choice is remembered, and a browser refusing
// site data leaves a working page.

/** stubStorage installs a working localStorage seeded with `seed`. */
function stubStorage(seed: Record<string, string> = {}) {
	const store = new Map(Object.entries(seed));
	const api = {
		getItem: vi.fn((key: string) => store.get(key) ?? null),
		setItem: vi.fn((key: string, value: string) => void store.set(key, value))
	};
	vi.stubGlobal('localStorage', api);
	return api;
}

/** stubThrowingStorage refuses both operations, as blocked site data does. */
function stubThrowingStorage() {
	vi.stubGlobal('localStorage', {
		getItem: () => {
			throw new Error('site data blocked');
		},
		setItem: () => {
			throw new Error('site data blocked');
		}
	});
}

beforeEach(() => {
	vi.unstubAllGlobals();
	detail.level = 'advanced';
});

describe('detail level', () => {
	it('should start advanced when nothing is stored', () => {
		stubStorage();

		detail.start();

		expect(detail.level).toBe('advanced');
	});

	it('should start simplified when that is the stored preference', () => {
		stubStorage({ [DETAIL_STORAGE_KEY]: 'simplified' });

		detail.start();

		expect(detail.simplified).toBe(true);
	});

	it('should ignore a stored value that is not a level', () => {
		stubStorage({ [DETAIL_STORAGE_KEY]: 'compact' });

		detail.start();

		expect(detail.level).toBe('advanced');
	});

	it('should remember a chosen level', () => {
		const storage = stubStorage();

		detail.set('simplified');

		expect(storage.setItem).toHaveBeenCalledWith(DETAIL_STORAGE_KEY, 'simplified');
	});

	it('should keep the default when site data is blocked', () => {
		stubThrowingStorage();

		detail.start();

		expect(detail.level).toBe('advanced');
	});

	it('should still change the level for this page when site data is blocked', () => {
		stubThrowingStorage();

		detail.set('simplified');

		expect(detail.simplified).toBe(true);
	});
});
