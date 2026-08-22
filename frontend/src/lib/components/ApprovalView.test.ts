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

/** mount renders the view with no-op decision handlers unless overridden. */
function mount(props: Partial<Parameters<typeof ApprovalView>[1]> = {}) {
	const onapprove = vi.fn();
	const ondeny = vi.fn();
	render(ApprovalView, { detail: detail(), onapprove, ondeny, ...props });
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
			requested: options({ extensions: ['permit-pty'], source_addresses: ['10.0.0.5', '203.0.113.9'] })
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
});
