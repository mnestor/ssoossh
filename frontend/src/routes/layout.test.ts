import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createRawSnippet } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '$lib/session.svelte';
import { resetFakePage } from '$lib/testing/page.svelte';
import Layout from './+layout.svelte';

// Test methodology: render the root layout around a marker child with fetch
// stubbed per URL, since the layout is what kicks off the app-wide session,
// branding, and version loads. The behaviors pinned: the header offers
// exactly one of the identity affordances (the user menu when signed in, a
// sign-in button when not, nothing on /login), and signing out both ends
// the server session and drops the cached identity.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

const children = createRawSnippet(() => ({
	render: () => '<p data-testid="page-child">page content</p>'
}));

/**
 * stubAppFetch answers the layout's startup calls: /users/me according to
 * user (null means signed out), branding and version empty, and
 * /auth/logout with success. Returns the spy for call assertions.
 */
function stubAppFetch(user: object | null) {
	const spy = vi.fn((input: RequestInfo | URL) => {
		const url = String(input);
		if (url.includes('/users/me')) {
			const status = user ? 200 : 401;
			const body = user ? { data: user, error: null } : { data: null, error: 'not authenticated' };
			return Promise.resolve(
				new Response(JSON.stringify(body), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		}
		if (url.includes('/auth/logout')) {
			return Promise.resolve(new Response(null, { status: 204 }));
		}
		// Branding and version: fail closed, the layout must not care.
		return Promise.resolve(new Response('not found', { status: 404 }));
	});
	vi.stubGlobal('fetch', spy);
	return spy;
}

/** alice is a plain signed-in user. */
const alice = {
	subject: 'sub-1',
	username: 'alice',
	email: 'alice@example.com',
	groups: [],
	other_accounts: [],
	service_accounts: []
};

/** stubMatchMedia installs the matchMedia jsdom does not provide, which
 * the layout's theme effects need. */
function stubMatchMedia() {
	vi.stubGlobal('matchMedia', (query: string) => ({
		matches: false,
		media: query,
		addEventListener: () => {},
		removeEventListener: () => {}
	}));
}

beforeEach(() => {
	vi.unstubAllGlobals();
	stubMatchMedia();
	resetFakePage('http://localhost/dashboard');
	// The layout triggers session.load() itself; start each case unresolved.
	session.user = null;
	session.error = null;
	session.resolved = false;
});

describe('root layout', () => {
	it('should show the navigation and user menu once signed in', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
		expect(screen.getByText('Dashboard')).toBeInTheDocument();
		expect(screen.getByText('History')).toBeInTheDocument();
		expect(screen.getByTestId('page-child')).toBeInTheDocument();
		expect(screen.queryByText('Sign in')).not.toBeInTheDocument();
	});

	it('should offer sign-in to a signed-out visitor', async () => {
		stubAppFetch(null);

		render(Layout, { children });

		expect(await screen.findByText('Sign in')).toBeInTheDocument();
		expect(screen.queryByText('Dashboard')).not.toBeInTheDocument();
		expect(screen.getByTestId('page-child')).toBeInTheDocument();
	});

	// /login already carries the sign-in action; a second button in the
	// header would point at the screen on show.
	it('should not duplicate the sign-in button on the login page', async () => {
		stubAppFetch(null);
		resetFakePage('http://localhost/login');

		render(Layout, { children });

		await waitFor(() => expect(session.resolved).toBe(true));
		expect(screen.queryByText('Sign in')).not.toBeInTheDocument();
	});

	it('should end the server session and drop the identity on sign out', async () => {
		const spy = stubAppFetch(alice);

		render(Layout, { children });
		const user = userEvent.setup();

		await user.click(await screen.findByText('alice@example.com'));
		await user.click(await screen.findByText('Sign out'));

		await waitFor(() => expect(session.user).toBeNull());
		const logoutCall = spy.mock.calls.find(([input]) => String(input).includes('/auth/logout'));
		expect(logoutCall).toBeDefined();
	});
});
