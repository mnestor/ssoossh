import { afterEach, describe, expect, it, vi } from 'vitest';

// Test methodology: like branding, the module caches one answer app-wide,
// so each test imports a fresh copy. The difference worth pinning is the
// failure shape: version stays unknown (null) rather than caching an empty
// answer, so the footer renders nothing and a later load may try again.

/** freshVersion imports an uncached copy of the module. */
async function freshVersion() {
	vi.resetModules();
	return await import('./version.svelte');
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('loadVersion', () => {
	it('should expose the build identity after a successful load', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: { version: 'v1.2.3', commit: 'abc1234' } }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		const { loadVersion, getVersion } = await freshVersion();
		await loadVersion();

		expect(getVersion()?.version).toBe('v1.2.3');
	});

	it('should leave the version unknown when the endpoint reports an error', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.resolve(new Response('not found', { status: 404 })))
		);

		const { loadVersion, getVersion } = await freshVersion();
		await loadVersion();

		expect(getVersion()).toBeNull();
	});

	it('should leave the version unknown when the network is down', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('connection refused')))
		);

		const { loadVersion, getVersion } = await freshVersion();
		await loadVersion();

		expect(getVersion()).toBeNull();
	});

	it('should leave the version unknown when the body has no data envelope', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({}), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		const { loadVersion, getVersion } = await freshVersion();
		await loadVersion();

		expect(getVersion()).toBeNull();
	});

	it('should fetch once and answer later loads from the cache', async () => {
		const fetchSpy = vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: { version: 'v1.2.3' } }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		);
		vi.stubGlobal('fetch', fetchSpy);

		const { loadVersion } = await freshVersion();
		await loadVersion();
		await loadVersion();

		expect(fetchSpy).toHaveBeenCalledTimes(1);
	});

	// Unlike branding, a failure is not cached as an answer: the footer
	// stays empty now, and a later load is allowed to try again.
	it('should try again after a failed load', async () => {
		const fetchSpy = vi
			.fn()
			.mockResolvedValueOnce(new Response('boom', { status: 500 }))
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ data: { version: 'v1.2.3' } }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		vi.stubGlobal('fetch', fetchSpy);

		const { loadVersion, getVersion } = await freshVersion();
		await loadVersion();
		expect(getVersion()).toBeNull();

		await loadVersion();
		expect(getVersion()?.version).toBe('v1.2.3');
		expect(fetchSpy).toHaveBeenCalledTimes(2);
	});
});
