import { afterEach, describe, expect, it, vi } from 'vitest';

// Test methodology: the module holds one cached answer for the whole app,
// so each test imports a fresh copy via resetModules. The property that
// matters is failing closed: branding is cosmetic, and no failure of the
// unauthenticated /api/branding endpoint may break a page that asked.

/** freshBranding imports an uncached copy of the module. */
async function freshBranding() {
	vi.resetModules();
	return await import('./branding.svelte');
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('loadBranding', () => {
	it('should expose the configured branding after a successful load', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: { org_name: 'Example Corp' } }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		const { loadBranding, getBranding } = await freshBranding();
		await loadBranding();

		expect(getBranding().org_name).toBe('Example Corp');
	});

	it('should fail closed when the endpoint reports an error', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.resolve(new Response('not found', { status: 404 })))
		);

		const { loadBranding, getBranding } = await freshBranding();
		await loadBranding();

		expect(getBranding()).toEqual({});
	});

	it('should fail closed when the network is down', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('connection refused')))
		);

		const { loadBranding, getBranding } = await freshBranding();
		await loadBranding();

		expect(getBranding()).toEqual({});
	});

	it('should fail closed when the body has no data envelope', async () => {
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

		const { loadBranding, getBranding } = await freshBranding();
		await loadBranding();

		expect(getBranding()).toEqual({});
	});

	it('should fetch once and answer later loads from the cache', async () => {
		const fetchSpy = vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: { org_name: 'Example Corp' } }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		);
		vi.stubGlobal('fetch', fetchSpy);

		const { loadBranding } = await freshBranding();
		await loadBranding();
		await loadBranding();

		expect(fetchSpy).toHaveBeenCalledTimes(1);
	});
});

describe('getBranding', () => {
	it('should answer empty before any load finishes', async () => {
		const { getBranding } = await freshBranding();
		expect(getBranding()).toEqual({});
	});
});
