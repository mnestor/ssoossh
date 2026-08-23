import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
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

import type { CurrentUser } from '$lib/api/types';
import { session } from '$lib/session.svelte';

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

/** signedInAs builds a CurrentUser, defaulting everything the picker ignores. */
function signedInAs(overrides: Partial<CurrentUser> = {}): CurrentUser {
	return {
		subject: 'sub-alice',
		username: 'alice',
		email: 'alice@example.com',
		groups: [],
		other_accounts: [],
		service_accounts: [],
		is_auditor: false,
		...overrides
	};
}

/**
 * approveBody returns the decoded JSON body of the approve POST. The client
 * serializes with JSON.stringify, so the recorded body is a string — matching
 * it as an object silently never matches.
 */
function approveBody(): unknown {
	const calls = vi.mocked(globalThis.fetch).mock.calls;
	const approve = calls.find(([url]) => String(url).includes('/approve'));
	if (!approve) throw new Error('no approve request was sent');
	return JSON.parse(String((approve[1] as RequestInit).body));
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

	// session is a plain exported singleton with $state fields, so these set
	// it directly rather than mocking the module. principals: [] on the
	// detail keeps the read-only Principals row from duplicating the names
	// the picker renders.
	describe('principal selection for user certificates', () => {
		beforeEach(() => {
			mockFetch(200, {
				id: 'req-1',
				type: 'user',
				status: 'pending',
				source_ip: '198.51.100.7',
				public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
				principals: [],
				valid_seconds: 28800,
				requested: { extensions: [], no_touch_required: false },
				granted: { extensions: [], no_touch_required: false },
				created_at: '2026-08-14T09:00:00Z',
				approval_url: '/approve/req-1',
				is_owned_by_you: true,
				already_closed: false
			});
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		afterEach(() => {
			session.user = null;
		});

		/** principalBoxes returns the picker's checkbox values, in render order. */
		function principalBoxes(): string[] {
			return screen.getAllByRole('checkbox').map((box) => (box as HTMLInputElement).value);
		}

		it('should list the username before the approver other accounts', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt', 'alice-service'] });

			render(Page);
			await settle();

			expect(principalBoxes()).toEqual(['alice', 'alice-alt', 'alice-service']);
		});

		it('should drop a duplicate when an other account repeats the username', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt', 'alice'] });

			render(Page);
			await settle();

			expect(principalBoxes()).toEqual(['alice', 'alice-alt']);
		});

		it('should pre-check the username so the default matches the old behaviour', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt'] });

			render(Page);
			await settle();

			expect(screen.getByRole('checkbox', { name: 'alice' })).toBeChecked();
		});

		it('should send the selected principals under the snake_case wire name', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt'] });

			render(Page);
			await settle();

			await userEvent.click(screen.getByRole('checkbox', { name: 'alice-alt' }));
			await userEvent.click(screen.getByTestId('approve-button'));
			await settle();

			expect(approveBody()).toEqual({ principals: ['alice', 'alice-alt'] });
		});
	});
});
