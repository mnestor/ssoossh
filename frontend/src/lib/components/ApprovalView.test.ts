import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { CertificateOptions, RequestDetail } from '#lib/api/types.js';
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
		expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
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
		expect(screen.getAllByText('198.51.100.7').length).toBeGreaterThan(0);
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
		// The principals name the approver; target_account names the local
		// account the sudo is being attempted as. They are deliberately
		// different values here so a test cannot pass by coincidence.
		const pam = detail({
			type: 'pam',
			principals: ['mike.nestor', 'mnestor'],
			target_account: 'root'
		});

		it('should not describe it as an SSH certificate request', () => {
			mount({ detail: pam });
			expect(screen.queryByText('Approve a certificate request')).not.toBeInTheDocument();
		});

		it('should explain that this authorizes a local operation, not an SSH session', () => {
			mount({ detail: pam });
			expect(screen.getByText(/not an interactive SSH session/)).toBeInTheDocument();
		});

		it('should show the local account the request is attempting to act as', () => {
			mount({ detail: pam });
			expect(screen.getByText('root')).toBeInTheDocument();
		});

		it('should mark the local account as reported by the client, not proven', () => {
			mount({ detail: pam });
			expect(screen.getByText('reported by the client')).toBeInTheDocument();
		});

		it('should still show the approver accounts as the principals', () => {
			mount({ detail: pam });
			expect(screen.getByText('mike.nestor')).toBeInTheDocument();
		});

		it('should omit the local account row when the request carries none', () => {
			mount({ detail: detail({ type: 'pam', principals: ['alice'] }) });
			expect(screen.queryByText('Account')).not.toBeInTheDocument();
		});
	});

	// A PAM or console certificate is shaped by server config, not by a
	// person: the requesting machine minted a throwaway key and asked for
	// nothing. The key and option sections would only be noise between the
	// approver and the rows that matter.
	describe('when the request authorizes a local operation', () => {
		const narrowedOptions = {
			requested: options({
				extensions: ['permit-pty', 'permit-port-forwarding'],
				force_command: '/bin/backup'
			}),
			granted: options({ extensions: ['permit-pty'] })
		};

		for (const type of ['pam', 'console'] as const) {
			const local = detail({ type, target_account: 'root', ...narrowedOptions });

			it(`should not show the public key for a ${type} request`, () => {
				mount({ detail: local });
				expect(screen.queryByText('Public key')).not.toBeInTheDocument();
			});

			it(`should not show the extensions for a ${type} request`, () => {
				mount({ detail: local });
				expect(
					screen.queryByText('Extensions this certificate will carry')
				).not.toBeInTheDocument();
			});

			it(`should not show the critical options for a ${type} request`, () => {
				mount({ detail: local });
				expect(screen.queryByText('Critical options')).not.toBeInTheDocument();
			});

			it(`should not warn that less was granted for a ${type} request`, () => {
				mount({ detail: local });
				expect(screen.queryByTestId('narrowed-warning')).not.toBeInTheDocument();
			});
		}

		it('should still show the public key for a user request', () => {
			mount({ detail: detail(narrowedOptions) });
			expect(screen.getByText('Public key')).toBeInTheDocument();
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
		// A local account deliberately unlike the principals the default
		// fixture carries, so an assertion on it matches the claimed-context
		// row rather than a principal chip.
		const withClient = detail({
			local_username: 'alice.local',
			local_hostname: 'alices-laptop'
		});

		// The account and the machine are rows of the claimed-context block
		// now that a user request reports the rest of the context too, and
		// the combined "alice@alices-laptop" row is suppressed rather than
		// repeating both halves a second time.
		it('should show the local user as the account', () => {
			mount({ detail: withClient });
			expect(screen.getByText('alice.local')).toBeInTheDocument();
		});

		it('should show the local hostname as the host', () => {
			mount({ detail: withClient });
			expect(screen.getByText('alices-laptop')).toBeInTheDocument();
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

		it('should say a denied request issued no certificate', () => {
			mount({ detail: resolved });
			expect(screen.getByTestId('blocked-already-resolved')).toHaveTextContent(
				'denied and no certificate was issued'
			);
		});

		// One sentence for every closed status used to tell the approver of
		// an approved request to run the client again, which started a
		// second one.
		it.each([
			{ status: 'approved' as const, text: 'nothing left to do' },
			{ status: 'enrolled' as const, text: 'nothing left to do' },
			{ status: 'denied' as const, text: 'Run the client again if that was a mistake' },
			{ status: 'failed' as const, text: 'Signing this certificate failed' }
		])('should word a $status request for what happened to it', ({ status, text }) => {
			mount({ detail: detail({ status, already_closed: true }) });
			expect(screen.getByTestId('blocked-already-resolved')).toHaveTextContent(text);
		});

		it('should give a request that expired on the server the expired notice', () => {
			mount({ detail: detail({ status: 'expired', already_closed: true }) });
			expect(screen.getByTestId('expired-notice')).toBeInTheDocument();
		});

		it('should stop asking for a decision in the heading', () => {
			mount({ detail: resolved });
			expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Certificate request');
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

		// The page does not read the request again after a decision, so the
		// badge has to follow the outcome or it keeps saying pending (#191).
		// The badge prints the lowercase status; the outcome alert's title is
		// capitalised, so exact text tells the two apart.
		it.each([
			{ type: 'user' as const, outcome: 'approved' as const, badge: 'approved' },
			{ type: 'pam' as const, outcome: 'approved' as const, badge: 'approved' },
			{ type: 'console' as const, outcome: 'approved' as const, badge: 'approved' },
			{ type: 'service' as const, outcome: 'approved' as const, badge: 'enrolled' },
			{ type: 'user' as const, outcome: 'denied' as const, badge: 'denied' },
			{ type: 'service' as const, outcome: 'denied' as const, badge: 'denied' }
		])(
			'should show $badge in the status badge when a $type request is $outcome',
			({ type, outcome, badge }) => {
				mount({
					detail: detail({ type }),
					outcome,
					serviceAccounts: ['svc-deploy'],
					selectedServiceAccount: 'svc-deploy'
				});
				expect(screen.getByText(badge)).toBeInTheDocument();
			}
		);

		it('should stop showing pending once a decision is recorded', () => {
			mount({ outcome: 'approved' });
			expect(screen.queryByText('pending')).not.toBeInTheDocument();
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

		// cert_options.service.allow_user_accounts lets someone put an
		// unattended job on a reusable code under an account that is already
		// theirs. The certificate is still a service certificate, and "a
		// certificate for my own account" is the exact phrase that reads as
		// "a certificate I can log in with" — so the page has to say
		// otherwise before the choice is made.
		it('should keep the picker ungrouped when no own accounts are offered', () => {
			mount({ detail: serviceDetail, serviceAccounts: ['svc-a'], userOwnServiceAccounts: [] });
			expect(screen.queryByRole('group', { name: /Your own accounts/ })).not.toBeInTheDocument();
		});

		it('should separate the approver’s own accounts from the claimed ones', () => {
			mount({
				detail: serviceDetail,
				serviceAccounts: ['svc-a', 'alice'],
				userOwnServiceAccounts: ['alice']
			});
			expect(screen.getByRole('group', { name: 'Service accounts' })).toBeInTheDocument();
			expect(
				screen.getByRole('group', { name: 'Your own accounts (non-interactive)' })
			).toBeInTheDocument();
		});

		it('should hint at what an own account produces before one is chosen', () => {
			mount({
				detail: serviceDetail,
				serviceAccounts: ['svc-a', 'alice'],
				userOwnServiceAccounts: ['alice']
			});
			expect(screen.getByTestId('own-account-hint')).toBeInTheDocument();
			expect(screen.queryByTestId('own-account-notice')).not.toBeInTheDocument();
		});

		it('should say the certificate is not a login once an own account is chosen', async () => {
			mount({
				detail: serviceDetail,
				serviceAccounts: ['svc-a', 'alice'],
				userOwnServiceAccounts: ['alice']
			});

			await userEvent.selectOptions(screen.getByRole('combobox'), 'alice');

			const notice = await screen.findByTestId('own-account-notice');
			expect(notice).toHaveTextContent(/not a way to sign in as yourself/i);
			expect(notice).toHaveTextContent(/service certificate/i);
		});

		it('should not warn when a claimed service account is chosen', async () => {
			mount({
				detail: serviceDetail,
				serviceAccounts: ['svc-a', 'alice'],
				userOwnServiceAccounts: ['alice']
			});

			await userEvent.selectOptions(screen.getByRole('combobox'), 'svc-a');

			expect(screen.queryByTestId('own-account-notice')).not.toBeInTheDocument();
		});

		it('should still allow approving an own account', async () => {
			const { onapprove } = mount({
				detail: serviceDetail,
				serviceAccounts: ['alice'],
				userOwnServiceAccounts: ['alice']
			});

			await userEvent.selectOptions(screen.getByRole('combobox'), 'alice');
			await userEvent.click(screen.getByRole('button', { name: /Approve/ }));

			expect(onapprove).toHaveBeenCalledOnce();
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

	describe('principal picker', () => {
		/** picker returns the chip group, or null when the row is read-only. */
		function picker() {
			return screen.queryByRole('group', { name: 'Principals to include' });
		}

		it('should not offer a picker for service requests', () => {
			mount({ detail: detail({ type: 'service' }), userPrincipals: ['alice'] });
			expect(picker()).not.toBeInTheDocument();
		});

		// PAM and console approvers pick too: the host matches the
		// certificate's principals against the local account, so which of
		// the approver's accounts go in is their decision.
		for (const type of ['pam', 'console'] as const) {
			it(`should offer a picker for a ${type} request`, () => {
				mount({
					detail: detail({ type, principals: [], target_account: 'root' }),
					userPrincipals: ['alice', 'alice-alt']
				});
				expect(picker()).toBeInTheDocument();
			});

			it(`should keep approve disabled for a ${type} request with nothing selected`, () => {
				mount({
					detail: detail({ type, target_account: 'root' }),
					userPrincipals: ['alice'],
					selectedPrincipals: []
				});
				expect(screen.getByRole('button', { name: /Approve/ })).toBeDisabled();
			});
		}

		it('should put the picker in the principals row while deciding', () => {
			mount({ detail: detail({ type: 'user', principals: ['alice'] }), userPrincipals: ['alice'] });
			expect(picker()).toBeInTheDocument();
		});

		it('should show the recorded principals once a decision exists', () => {
			mount({
				detail: detail({ type: 'user', principals: ['alice'], decided_at: '2026-08-14T10:00:00Z' }),
				userPrincipals: ['alice']
			});
			expect(picker()).not.toBeInTheDocument();
		});

		it("should show the server principals when the request is not the viewer's to decide", () => {
			mount({
				detail: detail({ type: 'user', principals: ['bob'], is_owned_by_you: false }),
				userPrincipals: ['alice']
			});
			expect(picker()).not.toBeInTheDocument();
			expect(screen.getByText('bob')).toBeInTheDocument();
		});

		it('should list the candidate principals as chips', () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice', 'alice-alt']
			});
			expect(screen.getByRole('button', { name: 'alice' })).toBeInTheDocument();
			expect(screen.getByRole('button', { name: 'alice-alt' })).toBeInTheDocument();
		});

		it('should press the chips that are selected', () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice', 'alice-alt'],
				selectedPrincipals: ['alice']
			});
			expect(screen.getByRole('button', { name: 'alice', pressed: true })).toBeInTheDocument();
			expect(screen.getByRole('button', { name: 'alice-alt', pressed: false })).toBeInTheDocument();
		});

		it('should add a principal when its chip is pressed', async () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice', 'alice-alt'],
				selectedPrincipals: ['alice']
			});
			await userEvent.click(screen.getByRole('button', { name: 'alice-alt' }));
			expect(screen.getByRole('button', { name: 'alice-alt', pressed: true })).toBeInTheDocument();
		});

		it('should remove a principal when its chip is pressed again', async () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice'],
				selectedPrincipals: ['alice']
			});
			await userEvent.click(screen.getByRole('button', { name: 'alice' }));
			expect(screen.getByRole('button', { name: 'alice', pressed: false })).toBeInTheDocument();
		});

		it('should freeze the chips once a decision has been sent', () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice'],
				selectedPrincipals: ['alice'],
				outcome: 'approved'
			});
			expect(screen.getByRole('button', { name: 'alice' })).toBeDisabled();
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
			expect(picker()).not.toBeInTheDocument();
		});

		it('should render a single chip when the approver holds no other accounts', () => {
			mount({
				detail: detail({ type: 'user' }),
				userPrincipals: ['alice'],
				selectedPrincipals: ['alice']
			});
			expect(within(picker()!).getAllByRole('button')).toHaveLength(1);
		});
	});
	// The address is offered at approval because that is the moment the
	// approver is already deciding what the enrollment is for. It is optional,
	// and only ever a service-type question: no other type produces an
	// enrollment for it to address.
	describe('the notification address', () => {
		it('should offer an optional notification address on a service request', () => {
			mount({
				detail: detail({ type: 'service' }),
				serviceAccounts: ['svc-a'],
				userPrincipals: []
			});
			expect(screen.getByTestId('notification-email-input')).toBeInTheDocument();
		});

		it('should not offer one on a user request', () => {
			mount({ detail: detail({ type: 'user' }), userPrincipals: ['alice'] });
			expect(screen.queryByTestId('notification-email-input')).not.toBeInTheDocument();
		});

		// Optional means optional: an empty address must not stand between the
		// approver and the button.
		it('should not block approval when the address is left empty', async () => {
			const { onapprove } = mount({
				detail: detail({ type: 'service' }),
				serviceAccounts: ['svc-a'],
				selectedServiceAccount: 'svc-a',
				userPrincipals: []
			});
			await userEvent.click(screen.getByTestId('approve-button'));
			expect(onapprove).toHaveBeenCalledOnce();
		});
	});
	// A console login is the largest of the four grants: it authorizes a
	// whole interactive session on a machine the approver cannot see. The
	// context rows are what let them tell a login they caused from one an
	// attacker started, and every one of them is self-reported.
	describe('a console login', () => {
		/** consoleDetail is a pending console request with full context. */
		function consoleDetail(overrides: Partial<RequestDetail> = {}): RequestDetail {
			return detail({
				type: 'console',
				target_account: 'root',
				hostname: 'web01',
				pam_service: 'login',
				tty: 'tty1',
				...overrides
			});
		}

		it('should say it is a console login rather than a certificate request', () => {
			mount({ detail: consoleDetail() });
			expect(screen.getByText('Approve a console login')).toBeInTheDocument();
		});

		const contextRows: Array<{ name: string; label: string; value: string }> = [
			{ name: 'the machine', label: 'Host', value: 'web01' },
			{ name: 'the PAM service', label: 'Service', value: 'login' },
			{ name: 'the terminal', label: 'Terminal', value: 'tty1' }
		];

		for (const { name, label, value } of contextRows) {
			it(`should show ${name}`, () => {
				mount({ detail: consoleDetail() });
				expect(screen.getByText(label)).toBeInTheDocument();
				expect(screen.getByText(value)).toBeInTheDocument();
			});
		}

		it('should mark the context as reported by the client rather than proven', () => {
			mount({ detail: consoleDetail() });
			expect(screen.getAllByText('reported by the client').length).toBeGreaterThan(1);
		});

		it('should omit a context row the client did not send', () => {
			mount({ detail: consoleDetail({ tty: undefined }) });
			expect(screen.queryByText('Terminal')).not.toBeInTheDocument();
		});

		it('should label the account row the same as a PAM request, not "logging in as"', () => {
			mount({ detail: consoleDetail() });
			expect(screen.getByText('Account')).toBeInTheDocument();
			expect(screen.getByText('root')).toBeInTheDocument();
		});

		// A console has nobody connecting to it over the network, so a
		// remote host on a request claiming to be one is worth saying
		// outright rather than rendering as one more grey row.
		it('should warn when a console request reports a remote host', () => {
			mount({ detail: consoleDetail({ remote_host: '198.51.100.7' }) });
			expect(screen.getByTestId('console-remote-host-warning')).toBeInTheDocument();
		});

		it('should not warn when a console request reports no remote host', () => {
			mount({ detail: consoleDetail() });
			expect(screen.queryByTestId('console-remote-host-warning')).not.toBeInTheDocument();
		});

		it('should not warn about a remote host on a PAM request, where one is ordinary', () => {
			mount({ detail: detail({ type: 'pam', remote_host: '198.51.100.7' }) });
			expect(screen.queryByTestId('console-remote-host-warning')).not.toBeInTheDocument();
		});

		// The approval window is the shortest of the four, so an approver
		// who cannot see the clock cannot tell a slow sign-in from a
		// request that has already died.
		it('should show how long is left to approve', () => {
			mount({ detail: consoleDetail() });
			expect(screen.getByText('Approvable until')).toBeInTheDocument();
		});

		it('should not show a deadline for a request that is already closed', () => {
			mount({ detail: consoleDetail({ already_closed: true, status: 'approved' }) });
			expect(screen.queryByText('Approvable until')).not.toBeInTheDocument();
		});
	});

	// PAM and console requests now share exactly one claimed-context block:
	// every row is the requester's own, unauthenticated claim about who is
	// doing this and from where (see ApprovalView.svelte's claimedContext).
	describe('the claimed context block', () => {
		/** localAuthDetail is a pending PAM request carrying the full set of
		 * self-reported host context fields. */
		function localAuthDetail(overrides: Partial<RequestDetail> = {}): RequestDetail {
			return detail({
				type: 'pam',
				target_account: 'root',
				requesting_user: 'alice',
				process: 'sudo -i',
				hostname: 'web01',
				machine_id: '3f2c1e0d9b8a7f6e5d4c3b2a19080706',
				pam_service: 'sudo',
				tty: 'pts/3',
				os: 'Debian GNU/Linux 13 (trixie)',
				client: 'pam_ssoossh-c/0.3.0',
				client_mode: 'auto',
				caller_uid: 1000,
				caller_pid: 4242,
				caller_ppid: 4200,
				client_time: '2026-08-14T09:00:00Z',
				trusted_ca_fingerprints: ['SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM'],
				...overrides
			});
		}

		it('should render the block for a PAM request', () => {
			mount({ detail: localAuthDetail() });
			expect(screen.getByText('Invoked by')).toBeInTheDocument();
			expect(screen.getByText('sudo -i')).toBeInTheDocument();
		});

		it('should still render the block for a console request', () => {
			mount({ detail: localAuthDetail({ type: 'console' }) });
			expect(screen.getByText('Invoked by')).toBeInTheDocument();
			expect(screen.getByText('sudo -i')).toBeInTheDocument();
		});

		// A user request reports the same context now that the client
		// collects it (internal/hostinfo), so the block renders for one
		// too -- sourcing the account and host from local_username and
		// local_hostname, which is the pair ReportedIdentity returns for
		// this type.
		it('should render the block for a user request', () => {
			mount({
				detail: detail({
					type: 'user',
					local_username: 'alice.local',
					local_hostname: 'alices-laptop',
					requesting_user: 'bob',
					process: 'ssoossh ssh login',
					tty: '/dev/pts/3',
					os: 'macOS 26.5.2 Darwin 25.5.0',
					client: 'ssoossh/1.2.3'
				})
			});
			expect(screen.getByText('Account')).toBeInTheDocument();
			expect(screen.getByText('alice.local')).toBeInTheDocument();
			expect(screen.getByText('Invoked by')).toBeInTheDocument();
			expect(screen.getByText('ssoossh ssh login')).toBeInTheDocument();
			expect(screen.getByText('macOS 26.5.2 Darwin 25.5.0')).toBeInTheDocument();
		});

		// PAM-only fields stay out of a user request's block: the server
		// never fills them for this type, so there is nothing to show.
		it('should omit the PAM-only rows for a user request', () => {
			mount({
				detail: detail({
					type: 'user',
					local_username: 'alice.local',
					local_hostname: 'alices-laptop'
				})
			});
			expect(screen.queryByText('Service')).not.toBeInTheDocument();
		});

		it('should show the group id alongside the other process ids', () => {
			mount({ detail: localAuthDetail({ caller_uid: 1000, caller_gid: 20, caller_pid: 4242 }) });
			expect(screen.getByText(/gid 20/)).toBeInTheDocument();
		});

		it('should omit "Invoked by" when the reported user is the same as the account', () => {
			mount({ detail: localAuthDetail({ requesting_user: 'root' }) });
			expect(screen.queryByText('Invoked by')).not.toBeInTheDocument();
		});

		it('should show the machine id beneath the host', () => {
			mount({ detail: localAuthDetail() });
			expect(screen.getByText('3f2c1e0d9b8a7f6e5d4c3b2a19080706')).toBeInTheDocument();
		});

		it('should append the configured mode to the reported client', () => {
			mount({ detail: localAuthDetail() });
			expect(screen.getByText('pam_ssoossh-c/0.3.0 (mode=auto)')).toBeInTheDocument();
		});

		it('should show the process ids together', () => {
			mount({ detail: localAuthDetail() });
			expect(screen.getByText('uid 1000 · pid 4242 · ppid 4200')).toBeInTheDocument();
		});

		it('should list the trusted CA fingerprints under "Host trusts"', () => {
			mount({ detail: localAuthDetail() });
			expect(screen.getByText('Host trusts')).toBeInTheDocument();
			expect(
				screen.getByText('SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM')
			).toBeInTheDocument();
		});

		it('should not show a "Host trusts" row when no fingerprints were reported', () => {
			mount({ detail: localAuthDetail({ trusted_ca_fingerprints: undefined }) });
			expect(screen.queryByText('Host trusts')).not.toBeInTheDocument();
		});

		describe('the host clock', () => {
			it('should show no skew note at 29 seconds of drift', () => {
				mount({
					detail: localAuthDetail({
						created_at: '2026-08-14T09:00:00Z',
						client_time: '2026-08-14T08:59:31Z'
					})
				});
				expect(screen.getByText('Host clock')).toBeInTheDocument();
				expect(screen.queryByText(/behind server|ahead of server/)).not.toBeInTheDocument();
			});

			it('should show a skew note once drift exceeds 30 seconds, at 31 seconds', () => {
				mount({
					detail: localAuthDetail({
						created_at: '2026-08-14T09:00:00Z',
						client_time: '2026-08-14T08:59:29Z'
					})
				});
				expect(screen.getByText('31s behind server')).toBeInTheDocument();
			});
		});

		describe('the script-not-person note', () => {
			it('should appear on a PAM request with a service but no terminal or remote host', () => {
				mount({
					detail: detail({
						type: 'pam',
						target_account: 'svc-backup',
						pam_service: 'cron'
					})
				});
				expect(screen.getByTestId('pam-headless-note')).toBeInTheDocument();
			});

			it('should not appear when the PAM request reports a terminal', () => {
				mount({
					detail: detail({
						type: 'pam',
						target_account: 'root',
						pam_service: 'sudo',
						tty: 'pts/3'
					})
				});
				expect(screen.queryByTestId('pam-headless-note')).not.toBeInTheDocument();
			});

			it('should not appear when the PAM request reports a remote host', () => {
				mount({
					detail: detail({
						type: 'pam',
						target_account: 'root',
						pam_service: 'sshd',
						remote_host: '198.51.100.7'
					})
				});
				expect(screen.queryByTestId('pam-headless-note')).not.toBeInTheDocument();
			});

			it('should not appear when the request carries no PAM service at all', () => {
				mount({ detail: detail({ type: 'pam', target_account: 'root' }) });
				expect(screen.queryByTestId('pam-headless-note')).not.toBeInTheDocument();
			});

			it('should not appear on a console request, where a service alone is ordinary', () => {
				mount({
					detail: detail({ type: 'console', target_account: 'root', pam_service: 'login' })
				});
				expect(screen.queryByTestId('pam-headless-note')).not.toBeInTheDocument();
			});
		});
	});
});

describe('the check beside the buttons', () => {
	// The one defence against approving someone else's request is the
	// approver's own memory of starting it. It used to be said only at the
	// simplified level, and in muted text away from the buttons.
	it.each([
		{ type: 'user' as const, text: 'Approve only if you started this yourself, just now' },
		{
			type: 'pam' as const,
			text: 'Approve only if you just ran sudo or a similar command yourself'
		},
		{ type: 'console' as const, text: 'Approve only if you are at the console' },
		{
			type: 'service' as const,
			text: 'Approve only if you just ran ssoossh service enroll yourself'
		}
	])('should ask the approver of a $type request whether they started it', ({ type, text }) => {
		mount({ detail: detail({ type }), serviceAccounts: ['svc-deploy'], userPrincipals: ['alice'] });
		expect(screen.getByTestId('approve-only-if')).toHaveTextContent(text);
	});

	it('should name the machine the request says it came from', () => {
		mount({ detail: detail({ local_hostname: 'alice-laptop' }) });
		expect(screen.getByTestId('approve-only-if')).toHaveTextContent('on alice-laptop');
	});

	it('should say when the approver and the request share an address', () => {
		mount({ detail: detail({ source_ip: '198.51.100.7', viewer_ip: '198.51.100.7' }) });
		expect(screen.getByTestId('approve-addresses')).toHaveTextContent('They match.');
	});

	// A difference is information, not a failure: plenty of people approve
	// from one machine a request their terminal made on another.
	it('should say when the addresses differ without refusing', () => {
		mount({ detail: detail({ source_ip: '198.51.100.7', viewer_ip: '203.0.113.20' }) });
		expect(screen.getByTestId('approve-addresses')).toHaveTextContent('They differ');
	});

	it('should still offer Approve when the addresses differ', () => {
		mount({ detail: detail({ source_ip: '198.51.100.7', viewer_ip: '203.0.113.20' }) });
		expect(screen.getByRole('button', { name: 'Approve' })).toBeEnabled();
	});
});

describe('when the deadline passes with the page open', () => {
	it('should replace the buttons with the expired notice at the advanced level', () => {
		mount({ detail: detail({ expires_at: new Date(Date.now() - 1000).toISOString() }) });
		expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument();
	});

	it('should say the request expired', () => {
		mount({ detail: detail({ expires_at: new Date(Date.now() - 1000).toISOString() }) });
		expect(screen.getByTestId('expired-notice')).toBeInTheDocument();
	});
});

describe('when every principal is unticked', () => {
	it('should say why Approve is disabled, beside it', () => {
		mount({ selectedPrincipals: [], userPrincipals: ['alice'] });
		expect(screen.getByTestId('approve-needs-principal')).toHaveTextContent(
			'Select at least one account to approve.'
		);
	});
});

describe('after a decision', () => {
	it('should move focus to the outcome', () => {
		mount({ outcome: 'approved' });
		expect(screen.getByTestId('outcome')).toHaveFocus();
	});
});

it('should title a failed decision for what failed', () => {
	mount({ actionError: 'request expired' });
	expect(screen.getByTestId('action-error')).toHaveTextContent('Could not record your decision');
});

it('should point to the reader’s own history for the log of decisions', () => {
	mount();
	expect(screen.getByRole('link', { name: 'your history' })).toHaveAttribute('href', '/logs/me');
});
