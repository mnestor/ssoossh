import { afterEach, describe, expect, it, vi } from 'vitest';

import { Session } from './session.svelte';

// Test methodology: a fresh Session per test against a stubbed fetch, since
// the class reaches the network only through getCurrentUser. The property
// that matters is the 401 rule: not being signed in is a normal state, and
// only other failures may surface as errors — otherwise an unreachable
// server reads as "logged out" and loops the user through login forever.

/** stubUserFetch answers /users/me with a user payload. */
function stubUserFetch(user: object) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: user, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** stubStatusFetch answers every request with an error envelope. */
function stubStatusFetch(status: number, message: string) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: null, error: message }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Session.load', () => {
	it('should record the identity when the user is signed in', async () => {
		stubUserFetch({ subject: 'sub-1', username: 'alice', email: 'a@example.com', groups: [] });

		const session = new Session();
		await session.load();

		expect(session.user?.username).toBe('alice');
		expect(session.signedIn).toBe(true);
		expect(session.error).toBeNull();
		expect(session.resolved).toBe(true);
	});

	it('should treat a 401 as signed out rather than an error', async () => {
		stubStatusFetch(401, 'not authenticated');

		const session = new Session();
		await session.load();

		expect(session.user).toBeNull();
		expect(session.signedIn).toBe(false);
		expect(session.error).toBeNull();
		expect(session.resolved).toBe(true);
	});

	it('should surface a server failure instead of pretending to be signed out', async () => {
		stubStatusFetch(500, 'database gone');

		const session = new Session();
		await session.load();

		expect(session.user).toBeNull();
		expect(session.error).toBe('database gone');
		expect(session.resolved).toBe(true);
	});

	it('should surface a network failure with its own message', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('connection refused')))
		);

		const session = new Session();
		await session.load();

		expect(session.user).toBeNull();
		expect(session.error).toBe('connection refused');
	});

	it('should clear a previous error when a retry succeeds', async () => {
		stubStatusFetch(500, 'database gone');
		const session = new Session();
		await session.load();
		expect(session.error).not.toBeNull();

		stubUserFetch({ subject: 'sub-1', username: 'alice', email: 'a@example.com', groups: [] });
		await session.load();

		expect(session.error).toBeNull();
		expect(session.signedIn).toBe(true);
	});
});

describe('Session.clear', () => {
	it('should drop the identity and stay resolved after a logout', async () => {
		stubUserFetch({ subject: 'sub-1', username: 'alice', email: 'a@example.com', groups: [] });
		const session = new Session();
		await session.load();

		session.clear();

		expect(session.user).toBeNull();
		expect(session.signedIn).toBe(false);
		expect(session.error).toBeNull();
		expect(session.resolved).toBe(true);
	});
});
