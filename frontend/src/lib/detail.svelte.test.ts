import { beforeEach, describe, expect, it, vi } from 'vitest';

import { detail, DETAIL_STORAGE_KEY } from './detail.svelte';

// Test methodology: drive the singleton directly with localStorage and
// fetch stubbed per case. What matters: simplified is the default until
// something says otherwise, the browser cache paints first, the server's
// level wins and is cached, a choice is saved to the account, a failed save
// puts the old level back and says so, and a browser refusing site data
// leaves a working page.

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

/** stubSave answers PUT /api/users/me/display with `level` as stored. */
function stubSave(level: string) {
	const fetchMock = vi.fn(() =>
		Promise.resolve(
			new Response(JSON.stringify({ data: { detail_level: level }, error: null }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		)
	);
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** stubSaveFailure answers the save with a server error. */
function stubSaveFailure() {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: null, error: 'database is down' }), {
					status: 500,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

beforeEach(() => {
	vi.unstubAllGlobals();
	detail.level = 'simplified';
	detail.saveError = null;
});

describe('detail level', () => {
	it('should start simplified when nothing is cached', () => {
		stubStorage();

		detail.start();

		expect(detail.simplified).toBe(true);
	});

	it('should paint the cached level until the session arrives', () => {
		stubStorage({ [DETAIL_STORAGE_KEY]: 'advanced' });

		detail.start();

		expect(detail.level).toBe('advanced');
	});

	it('should ignore a cached value that is not a level', () => {
		stubStorage({ [DETAIL_STORAGE_KEY]: 'compact' });

		detail.start();

		expect(detail.level).toBe('simplified');
	});

	it("should take the account's level over the cache", () => {
		stubStorage({ [DETAIL_STORAGE_KEY]: 'simplified' });
		detail.start();

		detail.adopt('advanced');

		expect(detail.level).toBe('advanced');
	});

	it("should cache the account's level for the next load", () => {
		const storage = stubStorage();

		detail.adopt('advanced');

		expect(storage.setItem).toHaveBeenCalledWith(DETAIL_STORAGE_KEY, 'advanced');
	});

	it('should save a chosen level to the account', async () => {
		stubStorage();
		const fetchMock = stubSave('advanced');

		await detail.set('advanced');

		expect(fetchMock).toHaveBeenCalledWith(
			'/api/users/me/display',
			expect.objectContaining({ method: 'PUT', body: JSON.stringify({ detail_level: 'advanced' }) })
		);
	});

	it('should show the chosen level before the save answers', () => {
		stubStorage();
		stubSave('advanced');

		void detail.set('advanced');

		expect(detail.level).toBe('advanced');
	});

	it('should put the previous level back when the save fails', async () => {
		stubStorage();
		stubSaveFailure();

		await detail.set('advanced');

		expect(detail.level).toBe('simplified');
	});

	it('should say the save failed', async () => {
		stubStorage();
		stubSaveFailure();

		await detail.set('advanced');

		expect(detail.saveError).not.toBeNull();
	});

	it('should keep the default when site data is blocked', () => {
		stubThrowingStorage();

		detail.start();

		expect(detail.level).toBe('simplified');
	});

	it('should still save a chosen level when site data is blocked', async () => {
		stubThrowingStorage();
		stubSave('advanced');

		await detail.set('advanced');

		expect(detail.level).toBe('advanced');
	});
});
