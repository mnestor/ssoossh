import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createRawSnippet } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { rail } from '$lib/rail.svelte';
import { session } from '$lib/session.svelte';
import { resetFakePage } from '$lib/testing/page.svelte';
import Layout from './+layout.svelte';

// Test methodology: render the root layout around a marker child with fetch
// stubbed per URL, since the layout is what kicks off the app-wide session,
// branding, and version loads.
//
// The layout's job is now one decision: does this screen get the rail. It
// does when there is an identity to navigate as and the screen is not a
// single-task one, and the behaviours pinned are the consequences of that
// decision going either way — plus sign-out, which still belongs here
// because the layout owns the call and the rail only raises the request.

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

/** stubStorage installs the localStorage the theme and rail preferences
 * read at startup. */
function stubStorage() {
	vi.stubGlobal('localStorage', {
		getItem: () => null,
		setItem: () => {}
	});
}

beforeEach(() => {
	vi.unstubAllGlobals();
	stubMatchMedia();
	stubStorage();
	resetFakePage('http://localhost/dashboard');
	// The layout triggers session.load() itself; start each case unresolved.
	session.user = null;
	session.error = null;
	session.resolved = false;
	rail.collapsed = false;
	rail.drawerOpen = false;
});

describe('root layout', () => {
	it('should give a signed-in identity the rail', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		expect(await screen.findByTestId('app-rail')).toBeInTheDocument();
		expect(screen.getByTestId('page-child')).toBeInTheDocument();
		expect(screen.queryByText('Sign in')).not.toBeInTheDocument();
	});

	it('should offer sign-in to a signed-out visitor instead of the rail', async () => {
		stubAppFetch(null);

		render(Layout, { children });

		expect(await screen.findByText('Sign in')).toBeInTheDocument();
		expect(screen.queryByTestId('app-rail')).not.toBeInTheDocument();
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

	// A single-task screen gets no navigation column even for someone who
	// could use one: an approval is a decision raised by a session
	// elsewhere, and a rail beside it invites wandering off mid-decision.
	it('should withhold the rail on a single-task screen', async () => {
		stubAppFetch(alice);
		resetFakePage('http://localhost/approve/req-123');

		render(Layout, { children });

		await waitFor(() => expect(session.resolved).toBe(true));
		expect(screen.queryByTestId('app-rail')).not.toBeInTheDocument();
		expect(screen.getByTestId('page-child')).toBeInTheDocument();
	});

	// Below `lg` the rail is off-canvas, and this button is the only way
	// back to it, so what it will do has to be legible from its name.
	it('should say whether the menu button opens or closes the drawer', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		const trigger = await screen.findByRole('button', { name: 'Open navigation menu' });
		expect(trigger).toHaveAttribute('aria-controls', 'app-rail-drawer');

		await userEvent.click(trigger);

		expect(screen.getByRole('button', { name: 'Close navigation menu' })).toBeInTheDocument();
	});

	it('should show the rail a second time as a drawer once it is opened', async () => {
		stubAppFetch(alice);

		render(Layout, { children });
		await userEvent.click(await screen.findByRole('button', { name: 'Open navigation menu' }));

		// One for the desktop column, one for the drawer. Both are the same
		// component, which is what keeps them from disagreeing.
		expect(screen.getAllByTestId('app-rail')).toHaveLength(2);
	});

	// A drawer that only closes by re-pressing a trigger it now covers is a
	// trap for anyone who opened it by accident.
	it('should close the drawer on Escape', async () => {
		stubAppFetch(alice);

		render(Layout, { children });
		await userEvent.click(await screen.findByRole('button', { name: 'Open navigation menu' }));
		await userEvent.keyboard('{Escape}');

		await waitFor(() => expect(rail.drawerOpen).toBe(false));
	});

	it('should end the server session and drop the identity on sign out', async () => {
		const spy = stubAppFetch(alice);

		render(Layout, { children });
		const user = userEvent.setup();

		// The desktop rail and the drawer would both offer one; only the
		// desktop rail is rendered until the drawer is opened. Sign out sits
		// behind the identity row's drop-up.
		await user.click(await screen.findByTestId('rail-user-trigger'));
		await user.click(screen.getByTestId('rail-sign-out'));

		await waitFor(() => expect(session.user).toBeNull());
		const logoutCall = spy.mock.calls.find(([input]) => String(input).includes('/auth/logout'));
		expect(logoutCall).toBeDefined();
	});
});
