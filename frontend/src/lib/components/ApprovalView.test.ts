import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { CertificateOptions, RequestDetail } from '$lib/api/types';
import ApprovalView from './ApprovalView.svelte';

/** options builds a CertificateOptions with everything empty by default. */
function options(overrides: Partial<CertificateOptions> = {}): CertificateOptions {
	return { extensions: [], no_touch_required: false, ...overrides };
}

/** detail builds a pending user request the caller owns. */
function detail(overrides: Partial<RequestDetail> = {}): RequestDetail {
	return {
		id: 'f0e1d2c3-0000-4000-8000-000000000000',
		type: 'user',
		status: 'pending',
		source_ip: '198.51.100.7',
		public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
		principals: ['alice'],
		valid_seconds: 28800,
		requested: options({ extensions: ['permit-pty'] }),
		granted: options({ extensions: ['permit-pty'] }),
		created_at: '2026-08-14T09:00:00Z',
		approval_url: '/approve/f0e1d2c3-0000-4000-8000-000000000000',
		is_owned_by_you: true,
		already_closed: false,
		...overrides
	};
}

/**
 * mount renders the view with no-op decision handlers unless overridden.
 *
 * selectedPrincipals defaults to ['alice'] because the approve route
 * pre-selects the approver's own username before rendering (see
 * routes/approve/[id]/+page.svelte), so a user-type request never reaches
 * this component with an empty selection. Mounting without one put the view
 * in a state the app does not produce, where approve is correctly disabled --
 * which is what the picker tests below assert deliberately.
 */
function mount(props: Partial<Parameters<typeof ApprovalView>[1]> = {}) {
	const onapprove = vi.fn();
	const ondeny = vi.fn();
	render(ApprovalView, {
		detail: detail(),
		selectedPrincipals: ['alice'],
		onapprove,
		ondeny,
		...props
	});
	return { onapprove, ondeny };
}

describe('ApprovalView', () => {
	it('should show the principals the certificate would carry', () => {
		mount();
		expect(screen.getByText('alice')).toBeInTheDocument();
	});

	it('should show the lifetime in readable units', () => {
		mount();
		expect(screen.getByText('8h')).toBeInTheDocument();
	});

	it('should show the source IP the request came from', () => {
		mount();
		expect(screen.getByText('198.51.100.7')).toBeInTheDocument();
	});

	// Regression: these lists used to be keyed by their own values, and a
	// keyed each block throws on a duplicate key rather than rendering it.
	// net.IP.String() drops an IPv6 zone, so one link-local address reported
	// by two interfaces arrives as the same string twice — which took the
	// whole approval page down mid-approval.
	it('should render a repeated registered IP rather than throwing', () => {
		const link = 'fe80::e8a7:34ff:fe9f:c7a9';
		mount({
			detail: detail({
				requested: options({ extensions: ['permit-pty'], source_addresses: [link, link] })
			})
		});
		expect(screen.getAllByText(link)).toHaveLength(2);
	});

	it('should render a repeated principal rather than throwing', () => {
		mount({ detail: detail({ principals: ['alice', 'alice'] }) });
		expect(screen.getAllByText('alice')).toHaveLength(2);
	});

	it('should render a repeated requested extension rather than throwing', () => {
		mount({
			detail: detail({
				requested: options({ extensions: ['permit-pty', 'permit-pty'] }),
				granted: options({ extensions: ['permit-pty'] })
			})
		});
		expect(screen.getAllByText('permit-pty')).toHaveLength(2);
	});

	it('should offer an approve action on a pending request the caller owns', () => {
		mount();
		expect(screen.getByRole('button', { name: 'Approve' })).toBeInTheDocument();
	});

	it('should report the approve action when the button is clicked', async () => {
		const { onapprove } = mount();
		await userEvent.click(screen.getByRole('button', { name: 'Approve' }));
		expect(onapprove).toHaveBeenCalledOnce();
	});

	it('should report the deny action when the button is clicked', async () => {
		const { ondeny } = mount();
		await userEvent.click(screen.getByRole('button', { name: 'Deny' }));
		expect(ondeny).toHaveBeenCalledOnce();
	});

	it('should disable the approve action while a decision is in flight', () => {
		mount({ busy: true });
		expect(screen.getByRole('button', { name: 'Working…' })).toBeDisabled();
	});

	it('should surface a failed decision without hiding the buttons', () => {
		mount({ actionError: 'request already resolved' });
		expect(screen.getByText('request already resolved')).toBeInTheDocument();
	});

	describe('when the request is a PAM certificate', () => {
		const pam = detail({ type: 'pam', principals: ['mnestor'] });

		it('should not describe it as an SSH certificate request', () => {
			mount({ detail: pam });
			expect(screen.queryByText('Approve a certificate request')).not.toBeInTheDocument();
		});

		it('should explain that this authorizes a local operation, not an SSH session', () => {
			mount({ detail: pam });
			expect(screen.getByText(/not an interactive SSH session/)).toBeInTheDocument();
		});
	});

	describe('when the granted options differ from the requested ones', () => {
		const narrowed = detail({
			requested: options({
				extensions: ['permit-pty', 'permit-port-forwarding'],
				force_command: '/bin/backup'
			}),
			granted: options({ extensions: ['permit-pty'] })
		});

		it('should still list the extension the server will grant', () => {
			mount({ detail: narrowed });
			expect(screen.getByText('permit-pty')).toBeInTheDocument();
		});

		// Trimmed options are shown, not hidden: the human is authorizing the
		// granted set and can only judge it against what was asked for.
		it('should still list the extension the server trimmed', () => {
			mount({ detail: narrowed });
			expect(screen.getByText('permit-port-forwarding')).toBeInTheDocument();
		});

		it('should label the trimmed extension as not permitted', () => {
			mount({ detail: narrowed });
			expect(screen.getAllByText('not permitted by this server').length).toBeGreaterThan(0);
		});

		it('should list a trimmed critical option with its value', () => {
			mount({ detail: narrowed });
			expect(screen.getByText(/force-command/)).toBeInTheDocument();
		});

		it('should warn that the certificate is less than was requested', () => {
			mount({ detail: narrowed });
			expect(screen.getByText('Less than was requested')).toBeInTheDocument();
		});
	});

	describe('when the request carries user-type client identity', () => {
		const withClient = detail({
			local_username: 'alice',
			local_hostname: 'alices-laptop'
		});

		it('should show the local user and hostname the client reported', () => {
			mount({ detail: withClient });
			expect(screen.getByText('alice@alices-laptop')).toBeInTheDocument();
		});
	});

	describe('when the request has no client identity', () => {
		it('should not show a client row', () => {
			mount();
			expect(screen.queryByText('Client')).not.toBeInTheDocument();
		});
	});

	describe('when the request reports registered IPs', () => {
		const withAddresses = detail({
			requested: options({
				extensions: ['permit-pty'],
				source_addresses: ['10.0.0.5', '203.0.113.9']
			})
		});

		it('should list the addresses the client registered', () => {
			mount({ detail: withAddresses });
			expect(screen.getByText('10.0.0.5')).toBeInTheDocument();
			expect(screen.getByText('203.0.113.9')).toBeInTheDocument();
		});
	});

	describe('when the request has no registered IPs', () => {
		it('should not show a registered IPs row', () => {
			mount();
			expect(screen.queryByText('Registered IPs')).not.toBeInTheDocument();
		});
	});

	describe('when the request belongs to another user', () => {
		const foreign = detail({ is_owned_by_you: false });

		it('should not offer an approve action', () => {
			mount({ detail: foreign });
			expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
		});

		it('should not offer a deny action', () => {
			mount({ detail: foreign });
			expect(screen.queryByRole('button', { name: 'Deny' })).not.toBeInTheDocument();
		});

		it('should explain that another account owns it', () => {
			mount({ detail: foreign });
			expect(screen.getByText(/belongs to another account/)).toBeInTheDocument();
		});
	});

	describe('when the request has already been resolved', () => {
		const resolved = detail({ status: 'denied', already_closed: true });

		it('should not offer an approve action', () => {
			mount({ detail: resolved });
			expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
		});

		it('should show the status it was resolved to', () => {
			mount({ detail: resolved });
			expect(screen.getByText('denied')).toBeInTheDocument();
		});

		it('should explain that the request is closed', () => {
			mount({ detail: resolved });
			expect(screen.getByText(/This request is closed/)).toBeInTheDocument();
		});
	});

	describe('when the request has expired', () => {
		const expired = detail({ status: 'expired', already_closed: true });

		it('should not offer an approve action', () => {
			mount({ detail: expired });
			expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
		});

		it('should show the expired status', () => {
			mount({ detail: expired });
			expect(screen.getByText('expired')).toBeInTheDocument();
		});
	});

	describe('when the request is already being signed', () => {
		const signing = detail({ status: 'signing' });

		it('should not offer a second approval', () => {
			mount({ detail: signing });
			expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
		});

		it('should explain that signing is already under way', () => {
			mount({ detail: signing });
			expect(screen.getByText(/certificate is being signed/)).toBeInTheDocument();
		});
	});

	describe('once a decision has been recorded', () => {
		it('should confirm an approval and point at the waiting client', () => {
			mount({ outcome: 'approved' });
			expect(screen.getByText('Approved')).toBeInTheDocument();
		});

		it('should replace the buttons after an approval', () => {
			mount({ outcome: 'approved' });
			expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
		});

		it('should confirm a denial', () => {
			mount({ outcome: 'denied' });
			expect(screen.getByText('Denied')).toBeInTheDocument();
		});
	});

	describe('service account picker', () => {
		const serviceDetail = detail({ type: 'service' });

		it('should not offer a picker for non-service requests', () => {
			mount({ serviceAccounts: ['svc-a'] });
			expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
		});

		it('should list the approver’s service accounts for a service request', () => {
			mount({ detail: serviceDetail, serviceAccounts: ['svc-a', 'svc-b'] });
			const select = screen.getByRole('combobox', { name: 'Service account to approve for' });
			expect(select).toBeInTheDocument();
			expect(screen.getByRole('option', { name: 'svc-b' })).toBeInTheDocument();
		});

		it('should keep Approve disabled until an account is chosen', async () => {
			const { onapprove } = mount({ detail: serviceDetail, serviceAccounts: ['svc-a'] });

			const approve = screen.getByRole('button', { name: /Approve/ });
			expect(approve).toBeDisabled();
			await userEvent.click(approve);
			expect(onapprove).not.toHaveBeenCalled();

			await userEvent.selectOptions(screen.getByRole('combobox'), 'svc-a');
			expect(approve).toBeEnabled();
			await userEvent.click(approve);
			expect(onapprove).toHaveBeenCalledOnce();
		});

		it('should block approval when the approver has no service accounts', () => {
			mount({ detail: serviceDetail, serviceAccounts: [] });
			expect(screen.getByTestId('blocked-no-service-accounts')).toBeInTheDocument();
			expect(screen.queryByRole('button', { name: /Approve/ })).not.toBeInTheDocument();
		});

		it('should not gate non-service approvals on an account', () => {
			mount({ serviceAccounts: [] });
			expect(screen.getByRole('button', { name: /Approve/ })).toBeEnabled();
		});
	});

	describe('decision record display', () => {
		it('should not show a decision record when decided_at is missing', () => {
			mount();
			expect(screen.queryByText('Decision record')).not.toBeInTheDocument();
		});

		it('should show a decision record when decided_at is present', () => {
			const withDecision = detail({
				decided_at: '2026-08-14T10:00:00Z',
				decided_by_outcome: 'approved',
				decided_by_username: 'alice'
			});
			mount({ detail: withDecision });
			expect(screen.getByText('Decision record')).toBeInTheDocument();
		});

		it('should show the decision outcome when available', () => {
			const withDecision = detail({
				decided_at: '2026-08-14T10:00:00Z',
				decided_by_outcome: 'approved'
			});
			mount({ detail: withDecision });
			expect(screen.getByText('approved')).toBeInTheDocument();
		});

		it('should show the decider username when available', () => {
			const withDecision = detail({
				decided_at: '2026-08-14T10:00:00Z',
				decided_by_username: 'bob'
			});
			mount({ detail: withDecision });
			expect(screen.getByText('bob')).toBeInTheDocument();
		});

		it('should show the decider email when available', () => {
			const withDecision = detail({
				decided_at: '2026-08-14T10:00:00Z',
				decided_by_email: 'alice@example.com'
			});
			mount({ detail: withDecision });
			expect(screen.getByText('alice@example.com')).toBeInTheDocument();
		});

		it('should show the decision source IP when available', () => {
			const withDecision = detail({
				decided_at: '2026-08-14T10:00:00Z',
				decided_source_ip: '203.0.113.42'
			});
			mount({ detail: withDecision });
			expect(screen.getByText('203.0.113.42')).toBeInTheDocument();
		});
	});

	describe('accessibility', () => {
		it('should have a live region for announcing action outcomes', () => {
			mount();
			const liveRegion = document.querySelector('[aria-live="polite"]');
			expect(liveRegion).toBeInTheDocument();
		});

		it('should announce approval to screen readers', () => {
			mount({ outcome: 'approved' });
			const liveRegion = document.querySelector('[aria-live="polite"]');
			expect(liveRegion?.textContent).toContain('approved');
		});

		it('should announce denial to screen readers', () => {
			mount({ outcome: 'denied' });
			const liveRegion = document.querySelector('[aria-live="polite"]');
			expect(liveRegion?.textContent).toContain('denied');
		});
	});

	describe('principal picker (user certificates)', () => {
		it('should not offer a picker for non-user requests', () => {
			mount({ detail: detail({ type: 'service' }), userPrincipals: ['alice'] });
			expect(screen.queryByText('Select principals')).not.toBeInTheDocument();
		});

		// principals: [] clears the read-only Principals row, which otherwise
		// renders 'alice' a second time and makes getByText ambiguous.
		it('should list the candidate principals when the request is a user request', () => {
			mount({
				detail: detail({ type: 'user', principals: [] }),
				userPrincipals: ['alice', 'alice-alt']
			});
			expect(screen.getByText('alice')).toBeInTheDocument();
			expect(screen.getByText('alice-alt')).toBeInTheDocument();
		});

		it('should pre-check the username principal', () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice', 'alice-alt'],
				selectedPrincipals: ['alice']
			});
			const checkboxes = screen.getAllByRole('checkbox');
			expect(checkboxes[0]).toBeChecked();
		});

		it('should keep approve disabled until at least one principal is selected', async () => {
			const { onapprove } = mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice'],
				selectedPrincipals: []
			});

			const approve = screen.getByRole('button', { name: /Approve/ });
			expect(approve).toBeDisabled();
			await userEvent.click(approve);
			expect(onapprove).not.toHaveBeenCalled();
		});

		it('should enable approve once at least one principal is selected', async () => {
			const { onapprove } = mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice'],
				selectedPrincipals: ['alice']
			});

			const approve = screen.getByRole('button', { name: /Approve/ });
			expect(approve).toBeEnabled();
			await userEvent.click(approve);
			expect(onapprove).toHaveBeenCalledOnce();
		});

		it('should still render service select for service-type requests', () => {
			mount({
				detail: detail({ type: 'service' }),
				serviceAccounts: ['svc-a'],
				userPrincipals: []
			});
			expect(screen.getByRole('combobox')).toBeInTheDocument();
			expect(screen.queryByText('Select principals')).not.toBeInTheDocument();
		});

		it('should render a single checkbox when the approver holds no other accounts', () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice'],
				selectedPrincipals: ['alice']
			});
			const checkboxes = screen.getAllByRole('checkbox');
			expect(checkboxes).toHaveLength(1);
		});
	});
});
