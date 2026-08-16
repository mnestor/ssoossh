import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError, request } from './client';

/** stubFetch installs a fetch that answers every call with one response.
 * Typed as the real fetch so recorded calls carry its argument types. */
function stubFetch(response: Response | Error) {
	const fetchMock = vi.fn<typeof fetch>(() =>
		response instanceof Error ? Promise.reject(response) : Promise.resolve(response)
	);
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** firstCall returns the URL and init of the first recorded fetch call. */
function firstCall(fetchMock: ReturnType<typeof stubFetch>) {
	const [input, init] = fetchMock.mock.calls[0] ?? [];
	return { url: String(input), init: init ?? {} };
}

/** jsonResponse builds a Response carrying the API's {data, error} envelope. */
function jsonResponse(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('request', () => {
	it('should return the unwrapped data half of the envelope', async () => {
		stubFetch(jsonResponse({ data: { username: 'alice' }, error: null }));
		await expect(request('/users/me')).resolves.toEqual({ username: 'alice' });
	});

	it('should prefix the path with /api', async () => {
		const fetchMock = stubFetch(jsonResponse({ data: null }));
		await request('/users/me');
		expect(firstCall(fetchMock).url).toBe('/api/users/me');
	});

	it('should send the session cookie on same-origin calls', async () => {
		const fetchMock = stubFetch(jsonResponse({ data: null }));
		await request('/users/me');
		expect(firstCall(fetchMock).init.credentials).toBe('same-origin');
	});

	it('should serialize a body as JSON when one is given', async () => {
		const fetchMock = stubFetch(jsonResponse({ data: null }));
		await request('/certs/user', { method: 'POST', body: { hostname: 'db1' } });
		expect(firstCall(fetchMock).init.body).toBe('{"hostname":"db1"}');
	});

	it('should omit the content-type header when there is no body', async () => {
		const fetchMock = stubFetch(jsonResponse({ data: null }));
		await request('/users/me');
		expect(firstCall(fetchMock).init.headers).toBeUndefined();
	});

	it('should resolve to undefined for a 204 with no body', async () => {
		stubFetch(new Response(null, { status: 204 }));
		await expect(request('/certs/requests/x/approve', { method: 'POST' })).resolves.toBeUndefined();
	});

	it('should raise the status on an error response', async () => {
		stubFetch(jsonResponse({ data: null, error: 'forbidden' }, 403));
		await expect(request('/certs/requests/x')).rejects.toMatchObject({ status: 403 });
	});

	it('should raise the server message on an error response', async () => {
		stubFetch(jsonResponse({ data: null, error: 'forbidden' }, 403));
		await expect(request('/certs/requests/x')).rejects.toThrow('forbidden');
	});

	it('should fall back to the status when an error response carries no message', async () => {
		stubFetch(jsonResponse({ data: null }, 500));
		await expect(request('/certs')).rejects.toThrow('request failed (HTTP 500)');
	});

	// A 200 whose envelope carries an error is a server-side contradiction;
	// trusting data would render an empty page instead of saying so.
	it('should raise when a successful response carries an envelope error', async () => {
		stubFetch(jsonResponse({ data: { username: 'alice' }, error: 'inconsistent' }));
		await expect(request('/users/me')).rejects.toThrow('inconsistent');
	});

	// The SPA fallback serves index.html for unmatched paths, so a mistyped
	// route arrives as HTML with a 200.
	it('should report the status rather than a parse error for a non-JSON body', async () => {
		stubFetch(new Response('<!doctype html>', { status: 200 }));
		await expect(request('/typo')).rejects.toThrow('unexpected non-JSON response (HTTP 200)');
	});

	it('should report a network failure as status zero', async () => {
		stubFetch(new TypeError('Failed to fetch'));
		await expect(request('/users/me')).rejects.toMatchObject({ status: 0 });
	});
});

describe('ApiError', () => {
	it('should classify 401 as unauthenticated', () => {
		expect(new ApiError(401, 'nope').isUnauthenticated).toBe(true);
	});

	it('should classify 403 as forbidden', () => {
		expect(new ApiError(403, 'nope').isForbidden).toBe(true);
	});

	it('should classify 404 as not found', () => {
		expect(new ApiError(404, 'nope').isNotFound).toBe(true);
	});

	it('should not classify a 500 as any of the handled cases', () => {
		const error = new ApiError(500, 'boom');
		expect([error.isUnauthenticated, error.isForbidden, error.isNotFound]).toEqual([
			false,
			false,
			false
		]);
	});
});
