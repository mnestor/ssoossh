import { afterEach, describe, expect, it, vi } from 'vitest';

import { approveRequest, denyRequest, getRequestDetail, loginURL, logout } from './endpoints';

/** okFetch installs a fetch that answers everything with an empty envelope.
 * Typed as the real fetch so recorded calls carry its argument types. */
function okFetch() {
	const fetchMock = vi.fn<typeof fetch>(() =>
		Promise.resolve(
			new Response(JSON.stringify({ data: null, error: null }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		)
	);
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** firstCall returns the URL and init of the first recorded fetch call. */
function firstCall(fetchMock: ReturnType<typeof okFetch>) {
	const [input, init] = fetchMock.mock.calls[0] ?? [];
	return { url: String(input), init: init ?? {} };
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('loginURL', () => {
	it('should carry a relative path as return_to', () => {
		expect(loginURL('/approve/abc')).toBe('/auth/login?return_to=%2Fapprove%2Fabc');
	});

	it('should omit return_to when none is given', () => {
		expect(loginURL()).toBe('/auth/login');
	});

	// The server re-validates this, but sending something it will refuse
	// would silently drop the user on / after login instead.
	it('should refuse an absolute URL as return_to', () => {
		expect(loginURL('https://evil.example/steal')).toBe('/auth/login');
	});

	it('should refuse a protocol-relative URL as return_to', () => {
		expect(loginURL('//evil.example/steal')).toBe('/auth/login');
	});

	it('should refuse an empty return_to', () => {
		expect(loginURL('')).toBe('/auth/login');
	});
});

describe('request-id encoding', () => {
	it('should percent-encode the request id in the detail path', async () => {
		const fetchMock = okFetch();
		await getRequestDetail('a/../b');
		expect(firstCall(fetchMock).url).toBe('/api/certs/requests/a%2F..%2Fb');
	});

	it('should percent-encode the request id in the approve path', async () => {
		const fetchMock = okFetch();
		await approveRequest('a b');
		expect(firstCall(fetchMock).url).toBe('/api/certs/requests/a%20b/approve');
	});

	it('should post rather than get when denying', async () => {
		const fetchMock = okFetch();
		await denyRequest('abc');
		expect(firstCall(fetchMock).init.method).toBe('POST');
	});
});

describe('logout', () => {
	// Outside /api because the auth routes are browser redirects rather than
	// JSON calls — the /api prefix would 404.
	it('should post to the auth route rather than the api prefix', async () => {
		const fetchMock = okFetch();
		await logout();
		expect(firstCall(fetchMock).url).toBe('/auth/logout');
	});

	it('should raise the status when logout fails', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.resolve(new Response(null, { status: 500 })))
		);
		await expect(logout()).rejects.toMatchObject({ status: 500 });
	});

	it('should report a network failure as status zero', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new TypeError('Failed to fetch')))
		);
		await expect(logout()).rejects.toMatchObject({ status: 0 });
	});
});
