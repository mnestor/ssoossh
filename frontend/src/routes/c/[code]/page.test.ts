import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

// The complete-verification-URL shortcut: /c/<code> resolves on load and
// redirects, or explains why it could not. A router and a real navigation
// belong in neither, so both are stubbed.
//
// vi.hoisted: vi.mock is lifted above the imports, so the state the
// factories close over has to be created up there with them.
const { pageState, goto, redirectIfUnauthenticated } = vi.hoisted(() => ({
	pageState: { params: { code: 'K7M4QP2X' } },
	goto: vi.fn(() => Promise.resolve()),
	redirectIfUnauthenticated: vi.fn(() => false)
}));

vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$app/navigation', () => ({ goto }));
vi.mock('$lib/auth', async () => {
	const actual = await vi.importActual<typeof import('$lib/auth')>('$lib/auth');
	return { ...actual, redirectIfUnauthenticated };
});

import Page from './+page.svelte';

/** mockFetch stubs the global fetch with one enveloped response. */
function mockFetch(status: number, body: object = {}) {
	const fetchMock = vi.fn();
	fetchMock.mockResolvedValue(
		new Response(JSON.stringify({ data: body, error: status === 200 ? null : 'nope' }), {
			status,
			headers: { 'Content-Type': 'application/json' }
		})
	);
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** settle lets the load effect's promise chain run to completion. */
function settle(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}

afterEach(() => {
	vi.unstubAllGlobals();
	goto.mockClear();
	redirectIfUnauthenticated.mockClear();
	pageState.params = { code: 'K7M4QP2X' };
});

describe('the console verification link', () => {
	it('should redirect to the approval page once the code resolves', async () => {
		mockFetch(200, { request_id: 'req-1', approval_url: '/approve/req-1' });

		render(Page);
		await settle();

		expect(goto).toHaveBeenCalledWith('/approve/req-1');
	});

	// The path segment is normalized before it is sent, so a link retyped in
	// lower case or with the display hyphen still works.
	it('should normalize the code out of the URL before submitting it', async () => {
		pageState.params = { code: 'k7m4-qp2x' };
		const fetchMock = mockFetch(200, { request_id: 'req-1', approval_url: '/approve/req-1' });

		render(Page);
		await settle();

		const [, init] = fetchMock.mock.calls[0];
		const body = JSON.parse(String(init.body));
		expect(body.code).toBe('K7M4QP2X');
	});

	const failures: Array<{ name: string; status: number; testid: string }> = [
		{ name: 'an unknown code', status: 404, testid: 'console-link-failure-not-found' },
		{ name: 'an expired login', status: 410, testid: 'console-link-failure-expired' },
		{ name: 'a login someone else claimed', status: 403, testid: 'console-link-failure-claimed' }
	];

	for (const { name, status, testid } of failures) {
		it(`should explain ${name} rather than redirecting`, async () => {
			mockFetch(status);

			render(Page);
			await settle();

			expect(screen.getByTestId(testid)).toBeInTheDocument();
			expect(goto).not.toHaveBeenCalled();
		});
	}

	it('should not render a failure when a signed-out visitor is redirected to login', async () => {
		redirectIfUnauthenticated.mockReturnValueOnce(true);
		mockFetch(401);

		render(Page);
		await settle();

		expect(screen.queryByTestId('console-link-failure-unknown')).not.toBeInTheDocument();
	});

	it('should explain a link that carries no usable code without calling the API', async () => {
		pageState.params = { code: '---' };
		const fetchMock = mockFetch(200);

		render(Page);
		await settle();

		expect(screen.getByTestId('console-link-failure-unknown')).toBeInTheDocument();
		expect(fetchMock).not.toHaveBeenCalled();
	});
});
