import { render, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateResponse } from '$lib/api/types';
import { formatDateTime, formatDateTimeRange } from '$lib/format';
import Page from './+page.svelte';

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

function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

beforeEach(() => {
	vi.clearAllMocks();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Certificate detail page', () => {
	describe('when certificate loads successfully', () => {
		beforeEach(() => {
			const cert: CertificateResponse = {
				id: 'cert-123',
				type: 'user',
				serial_number: '42',
				key_id: 'my-key',
				principals: 'alice,alice@example.com',
				public_key_fingerprint: 'SHA256:abcd1234',
				issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
				expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
			};
			mockFetch(cert);
		});

		it('should render the certificate details', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/cert-123/)).toBeInTheDocument();
		});

		it('should display the certificate type as a chip', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const certDetails = screen.getByTestId('cert-details');
			// The type is named once, by the chip in the identity strip — the
			// field list does not repeat it.
			expect(within(certDetails).getByText('User')).toBeInTheDocument();
		});

		it('should display the key ID', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/my-key/)).toBeInTheDocument();
		});

		it('should display the serial number', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/42/)).toBeInTheDocument();
		});
	});

	// A serial is 63 bits of randomness, so nearly every real one is past
	// Number.MAX_SAFE_INTEGER. Parsed as a JSON number it rounds silently and
	// the page shows a serial that matches no certificate -- which is why the
	// wire carries it as a string.
	describe('when the serial is larger than a JS number holds exactly', () => {
		it('should render every digit of the serial', async () => {
			mockFetch({
				id: 'cert-123',
				type: 'user',
				serial_number: '3260700569889958163',
				key_id: 'my-key',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abcd1234',
				issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
				expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByTestId('cert-serial-number')).toHaveTextContent('3260700569889958163');
		});
	});

	describe('when the certificate carries issued options', () => {
		const base: CertificateResponse = {
			id: 'cert-123',
			type: 'user',
			serial_number: '42',
			key_id: 'my-key',
			principals: 'alice',
			public_key_fingerprint: 'SHA256:abcd1234',
			issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
			expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
		};

		it('should list the extensions the certificate was signed with', async () => {
			mockFetch({ ...base, extensions: ['permit-pty', 'permit-agent-forwarding'] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const grants = screen.getByTestId('cert-grants');
			expect(within(grants).getByText('permit-agent-forwarding')).toBeInTheDocument();
		});

		it('should show each critical option with its value', async () => {
			mockFetch({ ...base, critical_options: { 'force-command': '/usr/bin/backup' } });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const grants = screen.getByTestId('cert-grants');
			expect(within(grants).getByText(/force-command.*\/usr\/bin\/backup/)).toBeInTheDocument();
		});

		it('should say none when the certificate carries no extensions', async () => {
			mockFetch({ ...base, critical_options: { 'force-command': '/usr/bin/backup' } });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const grants = screen.getByTestId('cert-grants');
			expect(within(grants).getByText('None')).toBeInTheDocument();
		});

		it('should say none for both when the certificate carries neither', async () => {
			mockFetch(base);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const grants = screen.getByTestId('cert-grants');
			expect(within(grants).getAllByText('None')).toHaveLength(2);
		});
	});

	describe('when the certificate authenticates a local operation', () => {
		// A pam or console certificate is verified by pam_ssoossh and thrown
		// away; it never reaches an sshd that would act on an extension or a
		// critical option, so the grants card would read "None / None"
		// forever. See config.CertOptionsPAM.Extensions.
		const local: CertificateResponse = {
			id: 'cert-pam',
			type: 'pam',
			serial_number: '42',
			key_id: 'root@web01',
			principals: 'mnestor',
			public_key_fingerprint: 'SHA256:abcd1234',
			issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
			expires_at: new Date('2024-08-24T10:00:30Z').toISOString()
		};

		it('should hide the grants card when the certificate is a PAM one', async () => {
			mockFetch(local);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByTestId('cert-grants')).not.toBeInTheDocument();
		});

		it('should hide the grants card when the certificate is a console one', async () => {
			mockFetch({ ...local, type: 'console' });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByTestId('cert-grants')).not.toBeInTheDocument();
		});

		// PAM extensions default to empty but are configurable, and an audit
		// page must never hide something that really was signed in.
		it('should still show the grants card when a PAM certificate carries an extension', async () => {
			mockFetch({ ...local, extensions: ['permit-pty'] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(within(screen.getByTestId('cert-grants')).getByText('permit-pty')).toBeInTheDocument();
		});

		it('should still show the grants card when a PAM certificate carries a critical option', async () => {
			mockFetch({ ...local, critical_options: { 'force-command': '/usr/bin/id' } });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByTestId('cert-grants')).toBeInTheDocument();
		});

		it('should keep the grants card on a user certificate that grants nothing', async () => {
			mockFetch({ ...local, type: 'user' });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByTestId('cert-grants')).toBeInTheDocument();
		});
	});

	describe('when the decision snapshotted what asked for the certificate', () => {
		const asked: CertificateResponse = {
			id: 'cert-pam',
			type: 'pam',
			serial_number: '42',
			key_id: 'root@web01',
			principals: 'mnestor',
			public_key_fingerprint: 'SHA256:abcd1234',
			issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
			expires_at: new Date('2024-08-24T10:00:30Z').toISOString(),
			reported_username: 'root',
			reported_hostname: 'web01',
			reported_pam_service: 'sudo',
			reported_tty: 'pts/3',
			reported_requesting_user: 'alice',
			reported_process: 'sudo systemctl restart nginx',
			reported_machine_id: '3f2c1e0d9b8a7f6e',
			reported_client: 'pam_ssoossh-c/0.3.0'
		};

		it('should join the reported account and host into user@host', async () => {
			mockFetch(asked);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const context = screen.getByTestId('cert-reported-context');
			expect(within(context).getByText('root@web01')).toBeInTheDocument();
		});

		it('should show the command that asked', async () => {
			mockFetch(asked);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const context = screen.getByTestId('cert-reported-context');
			expect(within(context).getByText('sudo systemctl restart nginx')).toBeInTheDocument();
		});

		// The user-certificate case: local_username/local_hostname on the
		// request, which the server resolves into the same reported pair.
		it('should show the local client of a user certificate', async () => {
			mockFetch({
				id: 'cert-user',
				type: 'user',
				serial_number: '42',
				key_id: 'alice',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abcd1234',
				issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
				expires_at: new Date('2024-08-24T18:00:00Z').toISOString(),
				reported_username: 'alice',
				reported_hostname: 'alice-laptop'
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const context = screen.getByTestId('cert-reported-context');
			expect(within(context).getByText('alice@alice-laptop')).toBeInTheDocument();
		});

		it('should omit the section when nothing was reported', async () => {
			mockFetch({
				id: 'cert-bare',
				type: 'user',
				serial_number: '42',
				key_id: 'alice',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abcd1234',
				issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
				expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByTestId('cert-reported-context')).not.toBeInTheDocument();
		});
	});

	describe('when the certificate carries a decision record', () => {
		const decided: CertificateResponse = {
			id: 'cert-123',
			type: 'user',
			serial_number: '42',
			key_id: 'my-key',
			principals: 'alice',
			public_key_fingerprint: 'SHA256:abcd1234',
			issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
			expires_at: new Date('2024-08-24T18:00:00Z').toISOString(),
			decided_by_email: 'approver@example.com',
			decided_by_groups: ['sre', 'oncall'],
			decided_source_ip: '203.0.113.7',
			decided_at: new Date('2024-08-24T09:59:00Z').toISOString()
		};

		// Who and when are one event, so they are one row: a row apiece made
		// the reader carry a name down the list to the timestamp it belongs
		// to.
		it('should say when the decision was made beside who made it', async () => {
			mockFetch(decided);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			// The junction, not just the timestamp: Svelte trims markup
			// whitespace at an element's edge, so the space between the two
			// has to be an expression to survive.
			expect(screen.getByTestId('cert-decided-by')).toHaveTextContent(
				`approver@example.com at ${formatDateTime(decided.decided_at ?? '')}`
			);
		});

		it('should not carry a separate decided-at row', async () => {
			mockFetch(decided);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByText('Decided at')).not.toBeInTheDocument();
		});

		it('should name the approver alone when no decision time was recorded', async () => {
			mockFetch({ ...decided, decided_at: undefined });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-decided-by')).toHaveTextContent('approver@example.com');
		});

		it('should name the approver when the request was approved', async () => {
			mockFetch(decided);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Approved by')).toBeInTheDocument();
		});

		it('should list the approver groups when the record carries them', async () => {
			mockFetch(decided);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('oncall')).toBeInTheDocument();
		});

		it('should label the decision as denied when the outcome was denied', async () => {
			mockFetch({ ...decided, decided_by_outcome: 'denied' });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Denied by')).toBeInTheDocument();
		});

		it('should omit the decision section when no decider is recorded', async () => {
			mockFetch({ ...decided, decided_by_email: undefined });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByTestId('cert-decision')).not.toBeInTheDocument();
		});

		it('should not show principals granted, options granted or a lifetime policy when none are recorded', async () => {
			mockFetch(decided);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).queryByText('Principals granted')).not.toBeInTheDocument();
			expect(within(section).queryByText('Options granted')).not.toBeInTheDocument();
			expect(within(section).queryByText('Lifetime policy')).not.toBeInTheDocument();
		});
	});

	describe('when the decision recorded what it granted', () => {
		const decidedWithGrant: CertificateResponse = {
			id: 'cert-123',
			type: 'user',
			serial_number: '42',
			key_id: 'my-key',
			principals: 'alice',
			public_key_fingerprint: 'SHA256:abcd1234',
			issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
			expires_at: new Date('2024-08-24T18:00:00Z').toISOString(),
			decided_by_email: 'approver@example.com',
			decided_at: new Date('2024-08-24T09:59:00Z').toISOString(),
			decided_principals: ['alice', 'alice.other']
		};

		it('should list the principals the decision granted', async () => {
			mockFetch(decidedWithGrant);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Principals granted')).toBeInTheDocument();
			expect(within(section).getByText('alice.other')).toBeInTheDocument();
		});

		it('should list the extensions the decision granted', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_granted_options: {
					extensions: ['permit-pty'],
					no_touch_required: false
				}
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Options granted')).toBeInTheDocument();
			expect(within(section).getByText('permit-pty')).toBeInTheDocument();
		});

		it('should show the force-command the decision granted', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_granted_options: {
					extensions: [],
					force_command: '/usr/bin/backup',
					no_touch_required: false
				}
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText(/force-command.*\/usr\/bin\/backup/)).toBeInTheDocument();
		});

		// Same reason the grants card is hidden for these types: an empty
		// options row is a row that teaches a reader to skip the section.
		it('should hide the empty options row when the certificate is a PAM one', async () => {
			mockFetch({
				...decidedWithGrant,
				type: 'pam',
				decided_granted_options: { extensions: [], no_touch_required: false }
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).queryByText('Options granted')).not.toBeInTheDocument();
		});

		it('should still show the options row when a PAM decision granted something', async () => {
			mockFetch({
				...decidedWithGrant,
				type: 'pam',
				decided_granted_options: { extensions: ['permit-pty'], no_touch_required: false }
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Options granted')).toBeInTheDocument();
		});

		it('should say none when the decision granted no options at all', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_granted_options: { extensions: [], no_touch_required: false }
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			const optionsRow = within(section).getByText('Options granted').closest('div');
			expect(optionsRow).not.toBeNull();
			expect(within(optionsRow as HTMLElement).getByText('None')).toBeInTheDocument();
		});

		it('should not show an options-granted row when the decision recorded none', async () => {
			mockFetch(decidedWithGrant);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).queryByText('Options granted')).not.toBeInTheDocument();
		});

		it('should show the tier name and condition from the lifetime policy explanation', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: JSON.stringify({
					v: 1,
					cert_type: 'user',
					policy_configured: true,
					tier: { name: 'on-call', condition: 'member of oncall', max_duration: '4h0m0s' },
					ceiling: '24h0m0s',
					effective_duration: '4h0m0s'
				})
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Tier')).toBeInTheDocument();
			expect(within(section).getByText('on-call')).toBeInTheDocument();
			expect(within(section).getByText(/member of oncall/)).toBeInTheDocument();
		});

		it('should show the ceiling and effective duration from the lifetime policy explanation', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: JSON.stringify({
					v: 1,
					cert_type: 'user',
					policy_configured: false,
					ceiling: '8h0m0s',
					effective_duration: '8h0m0s'
				})
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Ceiling')).toBeInTheDocument();
			expect(within(section).getAllByText('8h').length).toBe(2);
		});

		// The engine records these by calling String() on a Go Duration, so
		// they arrive as "8h0m0s" and have to be read as a length of time.
		it('should render the ceiling the way every other lifetime reads', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: JSON.stringify({
					v: 1,
					cert_type: 'user',
					policy_configured: true,
					ceiling: '24h0m0s',
					effective_duration: '1h30m0s'
				})
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('policy-ceiling')).toHaveTextContent('1d');
		});

		it('should render a part-hour effective duration in hours and minutes', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: JSON.stringify({
					v: 1,
					cert_type: 'user',
					policy_configured: true,
					ceiling: '24h0m0s',
					effective_duration: '1h30m0s'
				})
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('policy-effective-duration')).toHaveTextContent('1h 30m');
		});

		// The field is opaque on the wire, so a value the UI cannot read is
		// shown as it arrived rather than swallowed.
		it('should show a ceiling it cannot parse as it arrived', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: JSON.stringify({
					v: 1,
					cert_type: 'user',
					policy_configured: true,
					ceiling: 'unbounded',
					effective_duration: '8h0m0s'
				})
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('policy-ceiling')).toHaveTextContent('unbounded');
		});

		it('should show the source rule cidr when the lifetime policy explanation carries one', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: JSON.stringify({
					v: 1,
					cert_type: 'user',
					policy_configured: false,
					ceiling: '8h0m0s',
					effective_duration: '1h0m0s',
					source_rule: { cidr: '10.0.0.0/8', max_duration: '1h0m0s' }
				})
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).getByText('Source rule')).toBeInTheDocument();
			expect(within(section).getByText('10.0.0.0/8')).toBeInTheDocument();
		});

		it('should render no lifetime policy section when the explanation fails to parse', async () => {
			mockFetch({
				...decidedWithGrant,
				decided_policy_explanation: 'not valid json'
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const section = screen.getByTestId('cert-decision');
			expect(within(section).queryByText('Lifetime policy')).not.toBeInTheDocument();
		});
	});

	describe('when certificate fetch returns 404', () => {
		beforeEach(() => {
			mockFetch({ error: 'not found' }, 404);
		});

		it('should render access denied message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/not found|not authorized|access denied/i)).toBeInTheDocument();
		});
	});

	describe('when certificate fetch fails with network error', () => {
		beforeEach(() => {
			mockFetchError('network timeout');
		});

		it('should render error message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/could not load|error/i)).toBeInTheDocument();
		});
	});

	describe('loading state', () => {
		beforeEach(() => {
			mockFetch({ id: 'cert-123', type: 'user' }, 200);
		});

		it('should show loading state initially', () => {
			render(Page);
			expect(screen.getByText(/loading|loading\.\.\./i)).toBeInTheDocument();
		});
	});

	// Issued, expires and the lifetime between them used to be three rows,
	// which made a reader subtract one date from another to answer "does this
	// still work?".
	describe('the validity window', () => {
		function aCert(expiresAt: string): CertificateResponse {
			return {
				id: 'cert-123',
				type: 'user',
				serial_number: '42',
				key_id: 'my-key',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abcd1234',
				issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
				expires_at: expiresAt
			};
		}

		/** live is a certificate whose window is still open, whenever the
		 *  suite runs. */
		function live(): CertificateResponse {
			return aCert(new Date(Date.now() + 8 * 3600 * 1000).toISOString());
		}

		/** dead is a certificate whose window closed in 2024. */
		function dead(): CertificateResponse {
			return aCert(new Date('2024-08-24T18:00:00Z').toISOString());
		}

		it('should state both ends of the window in one row', async () => {
			const cert = live();
			mockFetch(cert);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-valid-period')).toHaveTextContent(
				formatDateTimeRange(cert.issued_at, cert.expires_at)
			);
		});

		it('should not carry a separate issued row', async () => {
			mockFetch(live());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByText('Issued at')).not.toBeInTheDocument();
		});

		it('should not carry a separate expiry row', async () => {
			mockFetch(live());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByText('Expires at')).not.toBeInTheDocument();
		});

		it('should say how much of the window is left', async () => {
			mockFetch(live());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-valid-period')).toHaveTextContent('left');
		});

		// Only what is left: the granted lifetime is the distance between two
		// dates the row already prints.
		it('should not restate the granted lifetime beside it', async () => {
			mockFetch(live());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-valid-period')).not.toHaveTextContent('·');
		});

		it('should mark a certificate inside its window as still valid', async () => {
			mockFetch(live());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-validity')).toHaveAttribute('data-valid', 'true');
		});

		it('should mark a certificate past its expiry as expired', async () => {
			mockFetch(dead());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-validity')).toHaveAttribute('data-valid', 'false');
		});

		it('should say the window has closed rather than how long is left', async () => {
			mockFetch(dead());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-valid-period')).toHaveTextContent('(expired)');
		});

		// A certificate page exists because a request was approved, so a pill
		// saying so answered a question nobody arrived with.
		it('should not label the outcome in the identity strip', async () => {
			mockFetch(live());
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByText('approved')).not.toBeInTheDocument();
		});
	});
});
