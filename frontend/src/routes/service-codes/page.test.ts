import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { ServiceEnrollment } from '$lib/api/types';
import { resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// $app/state and $app/navigation are replaced with a reactive fake so the
// shallow-routing flow (click a row, panel opens) is exercised for real.
// See src/lib/testing/page.svelte.ts for why the fake refuses to update
// page.url.
vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakePushState } = await import('$lib/testing/page.svelte');
	return { pushState: fakePushState };
});

/** Timestamps relative to now, so the page's clock-relative copy is stable
 * without freezing a timer. */
const anHourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
const twoDaysAgo = new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString();
const inNinetyDays = new Date(Date.now() + 90 * 24 * 60 * 60 * 1000).toISOString();

/** deployCode is a fully populated live enrollment, overridable per test. */
function deployCode(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1',
		service_account: 'svc-deploy',
		approved_by_username: 'alice',
		certificate_request_id: 'req-1',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc123',
		options: {
			extensions: ['permit-pty'],
			force_command: '/usr/local/bin/deploy',
			source_addresses: ['198.51.100.0/24'],
			no_touch_required: false
		},
		certificate_valid_seconds: 3600,
		created_at: twoDaysAgo,
		expires_at: inNinetyDays,
		first_redeemed_at: anHourAgo,
		last_retrieved_at: anHourAgo,
		retrieval_count: 12,
		...overrides
	};
}

/** mockFetch answers the enrollment list with `enrollments`, /users/me with
 * the accounts the identity holds, and any other call — the panel's
 * retrieval log — with an empty log.
 *
 * heldAccounts defaults to the accounts on the codes, which is the ordinary
 * case; a test passes it explicitly to cover an account with no codes, or an
 * identity that has lost one it still has codes for. */
function mockFetch(enrollments: ServiceEnrollment[], heldAccounts?: string[]) {
	const service_accounts = heldAccounts ?? [
		...new Set(enrollments.map((e) => e.service_account).filter(Boolean))
	];

	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			let body: unknown = { enrollments };
			if (url.includes('/retrievals')) {
				body = { retrievals: [] };
			} else if (url.includes('/users/me')) {
				body = { service_accounts };
			}
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** openAccount drills from the account list into one account's codes. */
async function openAccount(name = 'svc-deploy') {
	await userEvent.click(await screen.findByText(name));
}

/** mockFetchError stubs the global fetch to reject. */
function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

beforeEach(() => {
	resetFakePage('http://localhost/service-codes');
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Service codes page', () => {
	// The page lists accounts first because an account is what owns a code:
	// everyone holding it holds every code approved for it.
	describe('the account list', () => {
		it('should list a row per service account rather than per code', async () => {
			mockFetch([
				deployCode(),
				deployCode({ id: 'enr-2' }),
				deployCode({ id: 'enr-3', service_account: 'svc-backup', principals: ['svc-backup'] })
			]);
			render(Page);
			await screen.findByText('svc-deploy');
			expect(screen.getByText('svc-backup')).toBeInTheDocument();
			expect(screen.getAllByRole('button')).toHaveLength(2);
		});

		it('should count the live codes behind an account', async () => {
			mockFetch([deployCode(), deployCode({ id: 'enr-2' })]);
			render(Page);
			expect(await screen.findByText(/2 live codes/)).toBeInTheDocument();
		});

		// The whole reason the account level exists: an account with nothing
		// redeemable is the unattended job about to stop working, and a list
		// built from codes alone would never mention it.
		it('should list an account the identity holds but has no codes for', async () => {
			mockFetch([], ['svc-idle']);
			render(Page);
			expect(await screen.findByText('svc-idle')).toBeInTheDocument();
			expect(screen.getByText(/no codes/)).toBeInTheDocument();
		});

		it('should flag an account whose codes have all expired', async () => {
			mockFetch([deployCode({ expires_at: anHourAgo })]);
			render(Page);
			expect(await screen.findByText('No live code')).toBeInTheDocument();
		});

		it('should still list an account the identity has lost but has codes for', async () => {
			mockFetch([deployCode()], []);
			render(Page);
			expect(await screen.findByText('svc-deploy')).toBeInTheDocument();
		});
	});

	describe('when an account is opened', () => {
		it('should list a row per code for that account', async () => {
			mockFetch([deployCode(), deployCode({ id: 'enr-2', key_id: 'svc-deploy/req-2' })]);
			render(Page);
			await openAccount();
			expect(await screen.findByText('svc-deploy/req-1')).toBeInTheDocument();
			expect(screen.getByText('svc-deploy/req-2')).toBeInTheDocument();
		});

		it('should name who approved each code', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await openAccount();
			expect(await screen.findByText(/by alice/)).toBeInTheDocument();
		});

		it('should mark a code that is still redeemable as active', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await openAccount();
			expect(await screen.findByText('Active')).toBeInTheDocument();
		});

		// A job that stopped working is explained by the code beneath it, so
		// an expired code moves down the page rather than off it.
		it('should keep an expired code under its own heading', async () => {
			mockFetch([deployCode({ expires_at: anHourAgo })]);
			render(Page);
			await openAccount();
			expect(await screen.findByText('Expired codes')).toBeInTheDocument();
			expect(screen.getByText('Expired')).toBeInTheDocument();
		});

		it('should say so when the account has no codes at all', async () => {
			mockFetch([], ['svc-idle']);
			render(Page);
			await openAccount('svc-idle');
			expect(await screen.findByTestId('account-empty')).toBeInTheDocument();
		});

		it('should offer a way back to the account list', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await openAccount();
			expect(await screen.findByTestId('service-codes-back')).toBeInTheDocument();
		});
	});

	describe('when a code is opened', () => {
		it('should show the details behind it', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await openAccount();
			await userEvent.click(await screen.findByText('svc-deploy/req-1'));
			expect(await screen.findByText('SHA256:abc123')).toBeInTheDocument();
		});

		it('should show the options fixed at approval', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await openAccount();
			await userEvent.click(await screen.findByText('svc-deploy/req-1'));
			expect(await screen.findByText('permit-pty')).toBeInTheDocument();
			expect(screen.getByText('/usr/local/bin/deploy')).toBeInTheDocument();
			expect(screen.getByText('198.51.100.0/24')).toBeInTheDocument();
		});

		it('should show the redemption log', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await openAccount();
			await userEvent.click(await screen.findByText('svc-deploy/req-1'));
			expect(await screen.findByText('Never retrieved.')).toBeInTheDocument();
		});
	});

	// The page exists because the code cannot be shown. A regression that put
	// one on the wire should fail here as loudly as it does server-side.
	it('should never render an enrollment code', async () => {
		mockFetch([{ ...deployCode(), ...({ code: 'super-secret-code' } as object) }]);
		render(Page);
		await openAccount();
		expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
	});

	describe('when the identity holds no service accounts', () => {
		it('should explain that codes are approved against an account', async () => {
			mockFetch([], []);
			render(Page);
			expect(
				await screen.findByText(/do not have access to any service accounts/)
			).toBeInTheDocument();
		});
	});

	describe('when the load fails', () => {
		it('should surface the error', async () => {
			mockFetchError('boom');
			render(Page);
			expect(await screen.findByText('Could not load your service codes')).toBeInTheDocument();
		});
	});
});
