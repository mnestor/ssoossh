import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi, afterEach } from 'vitest';

import type { CurrentUser } from '$lib/api/types';
import Page from './+page.svelte';

/** aliceUser is a fully populated identity, overridable per test. */
function aliceUser(overrides: Partial<CurrentUser> = {}): CurrentUser {
	return {
		subject: 'sub-alice',
		username: 'alice',
		name: 'Alice Ashworth',
		email: 'alice@example.com',
		groups: ['ssh-users', 'ops'],
		other_accounts: ['alice.adm'],
		service_accounts: ['svc-backup', 'svc-deploy'],
		approvable_service_accounts: ['svc-backup', 'svc-deploy'],
		user_own_service_accounts: [],
		extra: {
			employee_id: 'E-40921',
			cost_center: 'CC-7781',
			teams: ['team-a', 'team-b']
		},
		is_admin: false,
		is_soc: false,
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

		it('should show the human-readable name', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('Alice Ashworth')).toBeInTheDocument();
		});

		// A name is optional in every direction: an unconfigured claim, an
		// absent one, and one of the wrong shape all store empty. The row is
		// dropped rather than showing a blank field.
		it('should omit the name row when nothing supplied one', async () => {
			mockFetch(aliceUser({ name: '' }));
			render(Page);
			await screen.findByText('sub-alice');
			expect(screen.queryByText('Name')).not.toBeInTheDocument();
		});

		it('should list every service account', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('svc-backup')).toBeInTheDocument();
			expect(screen.getByText('svc-deploy')).toBeInTheDocument();
		});

		it('should show the username as primary principal and other accounts together', async () => {
			mockFetch(aliceUser());
			render(Page);
			// Both should appear in the principals section
			expect(await screen.findByText('(primary)')).toBeInTheDocument();
			expect(screen.getByText('alice.adm')).toBeInTheDocument();
		});

		it('should list every group', async () => {
			mockFetch(aliceUser());
			render(Page);
			expect(await screen.findByText('ssh-users')).toBeInTheDocument();
			expect(screen.getByText('ops')).toBeInTheDocument();
		});

		it('should not show any access badge when the session holds no role', async () => {
			mockFetch(aliceUser());
			render(Page);
			await screen.findByText('sub-alice');
			expect(screen.queryByTestId('access-roles')).not.toBeInTheDocument();
		});

		it('should show the auditor badge for an auditor', async () => {
			mockFetch(aliceUser({ is_auditor: true }));
			render(Page);
			expect(await screen.findByText('Auditor')).toBeInTheDocument();
		});

		it('should show the SOC badge for a SOC member', async () => {
			mockFetch(aliceUser({ is_soc: true, is_auditor: true }));
			render(Page);
			expect(await screen.findByText('SOC')).toBeInTheDocument();
		});

		it('should show the admin badge for an admin', async () => {
			mockFetch(aliceUser({ is_admin: true, is_soc: true, is_auditor: true }));
			render(Page);
			expect(await screen.findByText('Admin')).toBeInTheDocument();
		});

		it('should show every held role for an admin, not just the narrowest', async () => {
			mockFetch(aliceUser({ is_admin: true, is_soc: true, is_auditor: true }));
			render(Page);
			const roles = await screen.findByTestId('access-roles');
			const labels = Array.from(roles.children).map((el) => el.textContent?.trim());
			expect(labels).toEqual(['Admin', 'SOC', 'Auditor']);
		});

		it('should show both roles when a SOC member is not an admin', async () => {
			mockFetch(aliceUser({ is_soc: true, is_auditor: true }));
			render(Page);
			const roles = await screen.findByTestId('access-roles');
			const labels = Array.from(roles.children).map((el) => el.textContent?.trim());
			expect(labels).toEqual(['SOC', 'Auditor']);
		});

		it('should show scalar extra fields', async () => {
			mockFetch(aliceUser({ extra: { employee_id: 'E-40921' } }));
			render(Page);
			expect(await screen.findByText('E-40921')).toBeInTheDocument();
		});

		it('should show list-valued extra fields as chips', async () => {
			mockFetch(aliceUser({ extra: { teams: ['team-a', 'team-b'] } }));
			render(Page);
			expect(await screen.findByText('team-a')).toBeInTheDocument();
			expect(screen.getByText('team-b')).toBeInTheDocument();
		});

		it('should show both scalar and list extra fields together', async () => {
			mockFetch(
				aliceUser({
					extra: {
						employee_id: 'E-40921',
						cost_center: 'CC-7781',
						teams: ['team-a', 'team-b']
					}
				})
			);
			render(Page);
			expect(await screen.findByText('E-40921')).toBeInTheDocument();
			expect(screen.getByText('CC-7781')).toBeInTheDocument();
			expect(screen.getByText('team-a')).toBeInTheDocument();
			expect(screen.getByText('team-b')).toBeInTheDocument();
		});
	});

	describe('when extra fields are empty or absent', () => {
		it('should handle empty extra object', async () => {
			mockFetch(aliceUser({ extra: {} }));
			render(Page);
			// Page should render without errors even with no extra fields.
			expect(await screen.findByText('sub-alice')).toBeInTheDocument();
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
			expect(await screen.findByText(/only your primary username/i)).toBeInTheDocument();
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
