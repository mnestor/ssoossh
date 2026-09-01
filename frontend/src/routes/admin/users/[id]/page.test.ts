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
// real enrollment count and not placeholder copy. Stubbing fetch rather than
// the endpoints module keeps that number flowing from a server response the
// way it does in production.
const ENROLLMENTS = 3;

/** mockDetail answers the detail and audit calls this page makes. */
function mockDetail(overrides: Record<string, unknown> = {}) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			// The audit timeline is a separate auditor-scoped read the page
			// makes on mount; answering it here keeps the stub matching the
			// calls production makes.
			const body = url.includes('/audit')
				? { events: [], total: 0 }
				: url.includes('/admin/config')
					? {
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

	it('should count the enrollments the disable leaves alone', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		// The real count from the server, not fixed copy: an admin deciding
		// whether to disable someone needs to know this is three services,
		// not "some". Scoped to the consequences paragraph because the page
		// behind the modal also shows an enrollment count.
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(`${ENROLLMENTS} live service enrollment`);
	});

	// The consequence that used to be here -- enrollments expiring after a
	// grace period -- is gone with group ownership. The dialog has to say the
	// opposite now, or an admin will hesitate to disable a leaver who
	// approved anything.
	it('should say the enrollments they approved keep working', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(/keep working/);
	});

	it('should say so when they approved no live enrollments', async () => {
		mockDetail({ service_enrollment_count: 0 });
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(/no live service enrollments/);
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

	// The server requires a non-empty reason, so the button is gated rather
	// than letting the confirm fail with a 400 the admin has to interpret.
	it('should not allow confirming a disable until a reason is given', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		await screen.findByText(/Disable User\?/);

		expect(screen.getByTestId('confirm-disable')).toBeDisabled();

		await userEvent.type(screen.getByTestId('disable-reason'), 'offboarded, SEC-1234');
		expect(screen.getByTestId('confirm-disable')).toBeEnabled();
	});

	it('should not treat a whitespace-only reason as a reason', async () => {
		mockDetail();
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		await screen.findByText(/Disable User\?/);
		await userEvent.type(screen.getByTestId('disable-reason'), '   ');

		expect(screen.getByTestId('confirm-disable')).toBeDisabled();
	});

	// The motivating case for the whole audit change: the person deciding
	// whether to re-enable has to be able to see why it was disabled.
	it('should show the recorded disable reason on a disabled account', async () => {
		mockDetail({
			disabled_at: '2026-08-20T09:00:00Z',
			disabled_by_username: 'root-admin',
			disabled_reason: 'offboarded, SEC-1234'
		});
		render(Page);
		expect(await screen.findByTestId('user-disabled-reason')).toHaveTextContent(
			'offboarded, SEC-1234'
		);
	});

	it('should require a reason before re-enabling', async () => {
		mockDetail({ disabled_at: '2026-08-20T09:00:00Z', disabled_by_username: 'root-admin' });
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByTestId('enable-user'));
		await screen.findByText(/Re-enable User\?/);

		expect(screen.getByTestId('confirm-enable')).toBeDisabled();

		await userEvent.type(screen.getByTestId('enable-reason'), 'cleared with security');
		expect(screen.getByTestId('confirm-enable')).toBeEnabled();
	});
});
