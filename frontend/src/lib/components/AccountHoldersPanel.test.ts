import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { AccountHolder } from '$lib/api/types';
import AccountHoldersPanel from './AccountHoldersPanel.svelte';

// Test methodology: render the panel against a stubbed fetch. What matters
// is that the answer it exists to give is legible — who else can use this
// code — and that the two states a reader would misread if they were hidden
// (a disabled holder, nobody at all) are both said out loud.

/** holder builds one row, overriding only what a case cares about. */
function holder(overrides: Partial<AccountHolder> = {}): AccountHolder {
	return {
		user_id: 'u-alice',
		username: 'alice',
		name: 'Alice Ashworth',
		email: 'alice@example.com',
		disabled: false,
		own: false,
		...overrides
	};
}

/** stubHolders answers the holders fetch with the given rows. */
function stubHolders(holders: AccountHolder[], serviceAccount = 'svc-deploy') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: { service_account: serviceAccount, holders } }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** stubStatus answers the holders fetch with an error status. */
function stubStatus(status: number) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: null, error: 'nope' }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('AccountHoldersPanel', () => {
	it('should name each holder of the account', async () => {
		stubHolders([holder(), holder({ user_id: 'u-bob', username: 'bob', name: 'Bob Bell' })]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1', serviceAccount: 'svc-deploy' });

		expect(await screen.findByText('Alice Ashworth')).toBeInTheDocument();
		expect(screen.getByText('Bob Bell')).toBeInTheDocument();
	});

	it('should fall back to the username when no name was captured', async () => {
		stubHolders([holder({ name: '' })]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByText('alice')).toBeInTheDocument();
	});

	// Hiding a disabled holder would answer "who has access" with a set that
	// quietly grows again the moment the account is re-enabled.
	it('should list a disabled holder and mark them disabled', async () => {
		stubHolders([holder({ disabled: true })]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByText('Alice Ashworth')).toBeInTheDocument();
		expect(screen.getByText('Disabled')).toBeInTheDocument();
	});

	it('should say when a holder holds the account as their own', async () => {
		stubHolders([holder({ own: true })]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByText('their own account')).toBeInTheDocument();
	});

	// An account nobody known holds is the state that explains why nothing
	// arrives, so it has to be stated rather than left blank.
	it('should say so when nobody who has signed in holds the account', async () => {
		stubHolders([]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByTestId('account-holders-empty')).toBeInTheDocument();
	});

	it('should mark the viewer among the holders', async () => {
		stubHolders([holder()]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1', viewerUsername: 'alice' });

		expect(await screen.findByText('(you)')).toBeInTheDocument();
	});

	it('should not mark anyone as the viewer when the viewer is not a holder', async () => {
		stubHolders([holder()]);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1', viewerUsername: 'bob' });

		await screen.findByText('Alice Ashworth');
		expect(screen.queryByText('(you)')).not.toBeInTheDocument();
	});

	it('should explain a refusal rather than reading as an empty account', async () => {
		stubStatus(403);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByText(/do not have permission/)).toBeInTheDocument();
	});

	it('should report a failed load without claiming the account has no holders', async () => {
		stubStatus(500);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByTestId('account-holders-error')).toBeInTheDocument();
		expect(screen.queryByTestId('account-holders-empty')).not.toBeInTheDocument();
	});

	// The panel sits inside a dialog carrying everything else about the
	// code; an unexpected shape must not take that down with it.
	// The list is only as complete as the users table, and the panel has to
	// say so where it is read: as a footnote under the answer rather than a
	// preamble that reads as a reason to distrust the panel before there is
	// anything on it.
	it('should qualify the list with what it cannot know', async () => {
		stubHolders([holder()]);

		render(AccountHoldersPanel, { enrollmentId: 'enr-1', serviceAccount: 'svc-deploy' });

		expect(await screen.findByTestId('account-holders-caveat')).toBeInTheDocument();
	});

	// "Nobody holds this" is the answer most in need of the caveat: it is
	// the one a reader is likeliest to act on.
	it('should qualify an empty list too', async () => {
		stubHolders([]);

		render(AccountHoldersPanel, { enrollmentId: 'enr-1', serviceAccount: 'svc-deploy' });

		expect(await screen.findByTestId('account-holders-caveat')).toBeInTheDocument();
	});

	// A failed load has no list to qualify, and a caveat about completeness
	// beside an error would read as though something had been listed.
	it('should not qualify a list it failed to load', async () => {
		stubStatus(500);

		render(AccountHoldersPanel, { enrollmentId: 'enr-1', serviceAccount: 'svc-deploy' });

		await screen.findByTestId('account-holders-error');
		expect(screen.queryByTestId('account-holders-caveat')).not.toBeInTheDocument();
	});

	it('should render as empty when the response carries no holders array', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: { service_account: 'svc-deploy' } }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);
		render(AccountHoldersPanel, { enrollmentId: 'enr-1' });

		expect(await screen.findByTestId('account-holders-empty')).toBeInTheDocument();
	});
});
