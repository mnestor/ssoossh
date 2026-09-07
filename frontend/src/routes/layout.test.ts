import { render, screen, waitFor, within } from '@testing-library/svelte';
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

	// A screen reached without an identity gets no navigation column: the
	// destinations on it are all behind a 401.
	it('should withhold the rail on a screen reached without an identity', async () => {
		stubAppFetch(alice);
		resetFakePage('http://localhost/c/ABCD-1234');

		render(Layout, { children });

		await waitFor(() => expect(session.resolved).toBe(true));
		expect(screen.queryByTestId('app-rail')).not.toBeInTheDocument();
		expect(screen.getByTestId('page-child')).toBeInTheDocument();
	});

	// An approval is reached by signing in, so its reader is inside the app
	// and keeps the app's chrome. It used to be treated as a kiosk screen,
	// which left a decision floating in an otherwise empty window.
	it('should keep the rail on an approval', async () => {
		stubAppFetch(alice);
		resetFakePage('http://localhost/approve/req-123');

		render(Layout, { children });

		await waitFor(() => expect(session.resolved).toBe(true));
		expect(screen.getByTestId('app-rail')).toBeInTheDocument();
	});

	// Below `lg` the rail is off-canvas and this button is what opens it, so
	// its name has to say so. The name no longer flips to "Close navigation
	// menu" while the drawer is up: the scrim covers this button and the
	// drawer's Tab cycle holds the caret, so it cannot be reached while open,
	// and the drawer has its own close button with that name. Two controls
	// answering to one name is what the flip produced.
	it('should name the menu button for what pressing it does', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		const trigger = await screen.findByRole('button', { name: 'Open navigation menu' });
		expect(trigger).toHaveAttribute('aria-controls', 'app-rail-drawer');
	});

	it('should report the drawer state on the menu button', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		const trigger = await screen.findByRole('button', { name: 'Open navigation menu' });
		expect(trigger).toHaveAttribute('aria-expanded', 'false');

		await userEvent.click(trigger);

		expect(trigger).toHaveAttribute('aria-expanded', 'true');
	});

	// The drawer covers the control that opened it, and the scrim behind it
	// is decorative — `aria-hidden` and out of the tab order — so without a
	// close button of its own the only way out from a keyboard was Escape.
	it('should give the drawer its own close control', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		await userEvent.click(await screen.findByRole('button', { name: 'Open navigation menu' }));

		const close = screen.getByRole('button', { name: 'Close navigation menu' });
		await userEvent.click(close);

		expect(screen.queryByRole('dialog', { name: 'Navigation menu' })).not.toBeInTheDocument();
	});

	// The drawer renders earlier in the document than the header that opens
	// it, so leaving focus on the trigger sent the next Tab past the menu and
	// into the page behind it.
	it('should move focus into the drawer when it opens', async () => {
		stubAppFetch(alice);

		render(Layout, { children });

		await userEvent.click(await screen.findByRole('button', { name: 'Open navigation menu' }));

		const drawer = screen.getByRole('dialog', { name: 'Navigation menu' });
		expect(drawer.contains(document.activeElement)).toBe(true);
	});

	it('should show the rail a second time as a drawer once it is opened', async () => {
		stubAppFetch(alice);

		render(Layout, { children });
		await userEvent.click(await screen.findByRole('button', { name: 'Open navigation menu' }));

		// One for the desktop column, one for the drawer. Both are the same
		// component, which is what keeps them from disagreeing.
		expect(screen.getAllByTestId('app-rail')).toHaveLength(2);
	});

	// The collapsed width is a desktop preference kept in one persisted
	// flag, and the control that clears it is hidden below `lg`. Before the
	// drawer was forced expanded, collapsing on a laptop and later opening
	// the app on a phone gave a strip of unlabelled icons with no way back.
	it('should open the drawer expanded even when the rail is collapsed', async () => {
		stubAppFetch(alice);
		rail.collapsed = true;

		render(Layout, { children });
		await userEvent.click(await screen.findByRole('button', { name: 'Open navigation menu' }));

		const drawer = document.getElementById('app-rail-drawer');
		expect(drawer).not.toBeNull();
		// The desktop copy stays collapsed; only the drawer is overridden.
		expect(within(drawer as HTMLElement).getByText('Dashboard')).toBeVisible();
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
