import { render, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateResponse } from '$lib/api/types';
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
			expect(within(section).getAllByText('8h0m0s').length).toBe(2);
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
});
