import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The page reads its id from the route params and, on a failed load, decides
// between redirecting to /login and rendering an explanation. Neither a
// router nor a real navigation belongs in a component test, so both are
// stubbed.
// vi.hoisted: vi.mock is lifted above the imports, so the state the factories
// close over has to be created up there with them.
const { pageState, redirectIfUnauthenticated } = vi.hoisted(() => ({
	pageState: { params: { id: 'req-1' } },
	redirectIfUnauthenticated: vi.fn()
}));

vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$lib/auth', async () => {
	const actual = await vi.importActual<typeof import('$lib/auth')>('$lib/auth');
	return { ...actual, redirectIfUnauthenticated };
});

import Page from './+page.svelte';

/** mockFetch stubs the global fetch with one enveloped response. */
function mockFetch(status: number, body: object = {}) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: body, error: status === 200 ? null : 'denied' }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** settle lets the load effect's promise chain run to completion. */
function settle(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}

beforeEach(() => {
	redirectIfUnauthenticated.mockReset();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Approval page', () => {
	describe('when the visitor is not signed in', () => {
		beforeEach(() => {
			mockFetch(401);
			// The real helper navigates; here it only reports that it would,
			// which is what the page branches on.
			redirectIfUnauthenticated.mockReturnValue(true);
		});

		// The bug this guards: a certificate request URL is how most people
		// first reach this app, and sending them to the identity provider
		// from here skipped /login — and with it the consent notice /login
		// is what shows.
		it('should send an unauthenticated visitor to the login screen', async () => {
			render(Page);
			await settle();
			expect(redirectIfUnauthenticated).toHaveBeenCalled();
		});

		it('should not render its own sign-in prompt', async () => {
			render(Page);
			await settle();
			expect(screen.queryByTestId('sign-in-button')).not.toBeInTheDocument();
		});

		it('should not render a load failure it is redirecting away from', async () => {
			render(Page);
			await settle();
			expect(screen.queryByText('Could not load this request')).not.toBeInTheDocument();
		});
	});

	describe('when the request belongs to someone else', () => {
		beforeEach(() => {
			mockFetch(403);
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		it('should explain that another account claimed it', async () => {
			render(Page);
			await settle();
			expect(await screen.findByTestId('load-failure-forbidden')).toBeInTheDocument();
		});

		it('should not offer to sign in as a way out', async () => {
			render(Page);
			await settle();
			expect(screen.queryByTestId('sign-in-button')).not.toBeInTheDocument();
		});
	});

	describe('when the request no longer exists', () => {
		beforeEach(() => {
			mockFetch(404);
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		it('should report it as a missing request', async () => {
			render(Page);
			await settle();
			expect(await screen.findByTestId('load-failure-not-found')).toBeInTheDocument();
		});
	});
});
