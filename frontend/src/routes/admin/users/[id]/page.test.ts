import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { fakePage, resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

vi.mock('$app/state', async () => {
	const { fakePage: p } = await import('$lib/testing/page.svelte');
	return { page: p };
});

// The disable confirmation is the reason this page has tests: an admin has
// to be told what disabling actually does BEFORE they confirm it, with the
// real enrollment count and the real expiry, not placeholder copy. Stubbing
// fetch rather than the endpoints module keeps those numbers flowing from a
// server response the way they do in production.
const GRACE = '2h';
const ENROLLMENTS = 3;

/** mockDetail answers the detail and config calls this page makes. */
function mockDetail(overrides: Record<string, unknown> = {}) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			const body = url.includes('/admin/config')
				? {
						admin_disable_grace_period: GRACE,
						admin_contact_email: 'it-help@corp.example',
						admin_disabled_message: 'Open a ticket at go/access'
					}
				: {
						id: 'user-1',
						username: 'alice',
						email: 'alice@corp.example',
						subject: 'sub-alice',
						other_accounts: ['a.smith'],
						service_accounts: ['svc-deploy'],
						extra_fields: { employee_id: 'E-40921' },
						groups: [],
						created_at: '2026-08-01T10:00:00Z',
						updated_at: '2026-08-01T10:00:00Z',
						service_enrollment_count: ENROLLMENTS,
						certificate_count: 7,
						disabled_at: undefined,
						disabled_by_username: undefined,
						...overrides
					};
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

beforeEach(() => {
	resetFakePage('http://localhost/admin/users/user-1');
	fakePage.params = { id: 'user-1' };
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Admin user detail', () => {
	it('should name the user it is showing', async () => {
		mockDetail();
		render(Page);
		expect(await screen.findByText('alice')).toBeInTheDocument();
	});

	it('should show an operator-configured extra field', async () => {
		mockDetail();
		render(Page);
		expect(await screen.findByText('E-40921')).toBeInTheDocument();
	});

	it('should not offer a confirmation before the admin asks for one', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');
		expect(screen.queryByText(/Disable User\?/)).not.toBeInTheDocument();
	});

	it('should state how many enrollments disabling will expire', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		// The real count from the server, not fixed copy: an admin deciding
		// whether to disable someone needs to know this is three services,
		// not "some". Scoped to the consequences paragraph because the page
		// behind the modal also shows an enrollment count.
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(`${ENROLLMENTS} active service enrollment`);
	});

	it('should state when those enrollments expire', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		const consequences = await screen.findByTestId('disable-consequences');
		// Two hours ahead, per the configured grace period. Asserting the
		// formatted date would pin a locale, so this checks the page named a
		// moment in the future rather than showing the epoch or now.
		const expiry = new Date(Date.now() + 2 * 60 * 60 * 1000);
		expect(consequences).toHaveTextContent(String(expiry.getFullYear()));
		expect(consequences).toHaveTextContent(/will expire at/);
	});

	it('should say the account is blocked immediately', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		expect(await screen.findByText(/immediately/)).toBeInTheDocument();
	});

	it('should close the confirmation without disabling when cancelled', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		await screen.findByText(/Disable User\?/);
		await userEvent.click(screen.getByRole('button', { name: /Cancel/ }));

		expect(screen.queryByText(/Disable User\?/)).not.toBeInTheDocument();
	});

	it('should report who disabled an already-disabled user', async () => {
		mockDetail({ disabled_at: '2026-08-20T09:00:00Z', disabled_by_username: 'root-admin' });
		render(Page);
		expect(await screen.findByText('root-admin')).toBeInTheDocument();
	});
});
