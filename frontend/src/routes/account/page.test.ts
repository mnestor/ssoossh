import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi, afterEach } from 'vitest';

import type { CurrentUser } from '$lib/api/types';
import Page from './+page.svelte';

/** aliceUser is a fully populated identity, overridable per test. */
function aliceUser(overrides: Partial<CurrentUser> = {}): CurrentUser {
	return {
		subject: 'sub-alice',
		username: 'alice',
		email: 'alice@example.com',
		groups: ['ssh-users', 'ops'],
		other_accounts: ['alice.adm'],
		service_accounts: ['svc-backup', 'svc-deploy'],
		is_auditor: false,
		...overrides
	};
}

/** mockFetch stubs the global fetch with a response body and status. */
function mockFetch(response: object, status = 200) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: response, error: null }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** mockFetchError stubs the global fetch to reject with an error message. */
function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Account page', () => {
	describe('when the identity loads successfully', () => {
		it('should show the username', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findAllByText('alice')).not.toHaveLength(0);
		});

		it('should show the email', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
		});

		it('should show the subject', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('sub-alice')).toBeInTheDocument();
		});

		it('should list every service account', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('svc-backup')).toBeInTheDocument();
			expect(screen.getByText('svc-deploy')).toBeInTheDocument();
		});

		it('should list every other account', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('alice.adm')).toBeInTheDocument();
		});

		it('should list every group', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('ssh-users')).toBeInTheDocument();
			expect(screen.getByText('ops')).toBeInTheDocument();
		});

		it('should not show the auditor badge for a non-auditor', async () => {
			mockFetch(aliceUser());
			render(Page);
			await screen.findByText('sub-alice');
			expect(screen.queryByText('Auditor')).not.toBeInTheDocument();
		});

		it('should show the auditor badge for an auditor', async () => {
			mockFetch(aliceUser({ is_auditor: true }));
			render(Page);
			expect(await screen.findByText('Auditor')).toBeInTheDocument();
		});
	});

	describe('when the identity has no linked accounts', () => {
		it('should explain that no service accounts are linked', async () => {
			mockFetch(aliceUser({ service_accounts: [] }));
			render(Page);
			expect(await screen.findByText(/No service accounts are linked/)).toBeInTheDocument();
		});

		it('should explain that no alternate accounts are linked', async () => {
			mockFetch(aliceUser({ other_accounts: [] }));
			render(Page);
			expect(await screen.findByText(/No alternate accounts are linked/)).toBeInTheDocument();
		});

		it('should explain that the identity carries no groups', async () => {
			mockFetch(aliceUser({ groups: [] }));
			render(Page);
			expect(await screen.findByText(/carries no groups/)).toBeInTheDocument();
		});
	});

	describe('when the load fails', () => {
		it('should surface the error', async () => {
			mockFetchError('boom');
			render(Page);
			expect(await screen.findByText('Could not load your account')).toBeInTheDocument();
		});
	});
});
