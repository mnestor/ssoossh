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
