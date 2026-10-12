import { render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet, flushSync, mount, unmount } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { rail } from '#lib/rail.svelte.js';
import { session } from '#lib/session.svelte.js';
import { fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import Layout from './+layout.svelte';
import ApprovalPage from './approve/[id]/+page.svelte';

// Test methodology: render the root layout the way SvelteKit does on a fresh
// load, with the page as its children, and hold /api/users/me open until the
// test lets it answer. That is the window issue #173 is about: the layout
// starts on the shell without the rail, the page mounts inside it and sends
// its own request, and then the session resolves and the rail appears. The
// page must survive that, rather than being torn down and built again.
//
// The page is mounted from inside a raw snippet's setup hook, so the snippet
// is the seam: its setup runs once per mount and its cleanup once per
// destroy, which is what these tests count.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

/** alice is a plain signed-in user, complete enough for the approval page's
 * principal picker. */
const alice = {
	subject: 'sub-alice',
	username: 'alice',
	name: 'Alice Ashworth',
	email: 'alice@example.com',
	groups: [],
	other_accounts: [],
	service_accounts: [],
	approvable_service_accounts: [],
	user_own_service_accounts: [],
	extra: {},
	is_admin: false,
	is_soc: false,
	is_auditor: false,
	detail_level: 'advanced'
};

/** pendingRequest is a user-certificate request awaiting a decision. */
const pendingRequest = {
	id: 'req-1',
	type: 'user',
	status: 'pending',
	source_ip: '198.51.100.7',
	public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
	principals: [],
	valid_seconds: 28800,
	requested: { extensions: [], no_touch_required: false },
	granted: { extensions: [], no_touch_required: false }
};

/** envelope wraps body in the API's response envelope. */
function envelope(status: number, body: object | null): Response {
	return new Response(JSON.stringify({ data: body, error: status === 200 ? null : 'denied' }), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

/**
 * stubFreshLoad answers the app's fetches with /users/me held open: nothing
 * about the session is known until the returned `resolveSession` is called
 * with the identity (null for a signed-out visitor). The request-detail GET
 * answers at once, signed in or not, as the server would for alice or with
 * a 401. Returns the fetch spy for counting.
 */
function stubFreshLoad() {
	let answerMe: (user: object | null) => void = () => {};
	const me = new Promise<object | null>((resolve) => {
		answerMe = resolve;
	});
	let signedIn = true;
	const spy = vi.fn((input: RequestInfo | URL) => {
		const url = String(input);
		if (url.includes('/users/me')) {
			return me.then((user) => envelope(user ? 200 : 401, user));
		}
		if (url.includes('/certs/requests/')) {
			return Promise.resolve(signedIn ? envelope(200, pendingRequest) : envelope(401, null));
		}
		// Branding and version: fail closed, the layout must not care.
		return Promise.resolve(new Response('not found', { status: 404 }));
	});
	vi.stubGlobal('fetch', spy);
	return {
		spy,
		resolveSession(user: object | null) {
			signedIn = user !== null;
			answerMe(user);
		}
	};
}

/** detailCalls returns the fetches made to the approval page's
 * request-detail endpoint, with the signal each one was sent with. */
function detailCalls(spy: ReturnType<typeof vi.fn>): { url: string; signal?: AbortSignal }[] {
	return spy.mock.calls
		.map(([input, init]) => ({
			url: String(input),
			signal: (init as RequestInit | undefined)?.signal ?? undefined
		}))
		.filter(({ url }) => url.includes('/certs/requests/req-1'));
}

/**
 * pageSnippet stands in for SvelteKit's page tree under the root layout. It
 * mounts `component` (or nothing) into its own element and counts how many
 * times the subtree was created and destroyed.
 */
function pageSnippet(component?: typeof ApprovalPage) {
	const counts = { mounts: 0, destroys: 0 };
	const children = createRawSnippet(() => ({
		render: () => '<div data-testid="page-host"></div>',
		setup: (node: Element) => {
			counts.mounts += 1;
			const instance = component ? mount(component, { target: node }) : null;
			return () => {
				counts.destroys += 1;
				if (instance) {
					unmount(instance);
				}
			};
		}
	}));
	return { children, counts };
}

/** settle lets pending promise chains and effects run to completion. */
async function settle(): Promise<void> {
	await new Promise((resolve) => setTimeout(resolve, 0));
	flushSync();
}

beforeEach(() => {
	vi.unstubAllGlobals();
	vi.stubGlobal('matchMedia', (query: string) => ({
		matches: false,
		media: query,
		addEventListener: () => {},
		removeEventListener: () => {}
	}));
	vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => {} });
	// The layout calls session.load() itself; every case starts unresolved,
	// as a fresh page load does.
	session.user = null;
	session.error = null;
	session.resolved = false;
	rail.collapsed = false;
	rail.drawerOpen = false;
});

describe('page mounting on a fresh load', () => {
	// Every shell the layout can settle on: the rail appearing (the case
	// that used to rebuild the page), and the ones where it never does.
	const cases = [
		{ name: 'a signed-in visitor on a rail page', href: '/dashboard', user: alice, rail: true },
		{ name: 'a signed-out visitor', href: '/dashboard', user: null, rail: false },
		{ name: 'a signed-in visitor on a focus page', href: '/c/ABCD-1234', user: alice, rail: false },
		{ name: 'a signed-in visitor on an approval', href: '/approve/req-1', user: alice, rail: true }
	];

	for (const tc of cases) {
		it(`should mount the page once when the session resolves for ${tc.name}`, async () => {
			const { resolveSession } = stubFreshLoad();
			resetFakePage(`http://localhost${tc.href}`);
			const { children, counts } = pageSnippet();

			render(Layout, { children });
			await settle();
			resolveSession(tc.user);
			await waitFor(() => expect(session.resolved).toBe(true));
			await settle();

			expect(counts).toEqual({ mounts: 1, destroys: 0 });
		});

		// Pins that each case really ended on the shell it names, so the
		// mount count above is measured across the switch it claims to be.
		it(`should settle on the ${tc.rail ? 'rail' : 'plain'} shell for ${tc.name}`, async () => {
			const { resolveSession } = stubFreshLoad();
			resetFakePage(`http://localhost${tc.href}`);
			const { children } = pageSnippet();

			render(Layout, { children });
			resolveSession(tc.user);
			await waitFor(() => expect(session.resolved).toBe(true));
			await settle();

			expect(screen.queryByTestId('app-rail') !== null).toBe(tc.rail);
		});
	}

	describe('on the approval page', () => {
		beforeEach(() => {
			resetFakePage('http://localhost/approve/req-1');
			fakePage.params = { id: 'req-1' };
		});

		// The request-detail GET is also what claims the request for the
		// caller on the server, so it is the one request on this page that
		// must not go out twice.
		it('should send the request-detail GET once when the session resolves after it', async () => {
			const { spy, resolveSession } = stubFreshLoad();
			const { children } = pageSnippet(ApprovalPage);

			render(Layout, { children });
			await settle();
			resolveSession(alice);
			await waitFor(() => expect(session.resolved).toBe(true));
			await screen.findByTestId('approval-view');
			await settle();

			expect(detailCalls(spy)).toHaveLength(1);
		});

		it('should not abort the request-detail GET when the session resolves', async () => {
			const { spy, resolveSession } = stubFreshLoad();
			const { children } = pageSnippet(ApprovalPage);

			render(Layout, { children });
			await settle();
			resolveSession(alice);
			await waitFor(() => expect(session.resolved).toBe(true));
			await screen.findByTestId('approval-view');
			await settle();

			expect(detailCalls(spy).some(({ signal }) => signal?.aborted)).toBe(false);
		});

		it('should show the approval view inside the rail shell once the session resolves', async () => {
			const { resolveSession } = stubFreshLoad();
			const { children } = pageSnippet(ApprovalPage);

			render(Layout, { children });
			await settle();
			resolveSession(alice);
			await waitFor(() => expect(session.resolved).toBe(true));
			await screen.findByTestId('approval-view');

			expect(screen.getByTestId('app-rail')).toBeInTheDocument();
		});
	});
});
