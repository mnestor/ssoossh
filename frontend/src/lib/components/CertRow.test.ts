import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import type { CertificateRecord } from '$lib/api/types';
import CertRow from './CertRow.svelte';

const now = new Date('2026-08-22T12:00:00Z');

/** cert builds a certificate record, overriding only what a case cares about. */
function cert(overrides: Partial<CertificateRecord> = {}): CertificateRecord {
	return {
		id: 'cert-1',
		type: 'user',
		serial_number: '1',
		key_id: 'key-1',
		principals: 'alice',
		public_key_fingerprint: 'SHA256:abc',
		issued_at: '2026-08-22T10:00:00Z',
		expires_at: '2026-08-22T18:00:00Z',
		decided_by_username: 'alice',
		decided_by_email: 'alice@example.com',
		reported_username: 'alice',
		reported_hostname: 'alice-laptop',
		...overrides
	};
}

describe('CertRow', () => {
	// The one indicator every row carries: the outcome mark beside it
	// cannot say whether a certificate still works, and that is what a
	// reader actually brings to a row.
	describe('the validity indicator', () => {
		it('should mark a certificate inside its window as still valid', () => {
			render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
			expect(screen.getByTestId('cert-validity')).toHaveAttribute('data-valid', 'true');
		});

		it('should mark a certificate past its expiry as expired', () => {
			render(CertRow, {
				cert: cert({ expires_at: '2026-08-22T11:00:00Z' }),
				now,
				href: '/certs/cert-1'
			});
			expect(screen.getByTestId('cert-validity')).toHaveAttribute('data-valid', 'false');
		});

		// The indicator names its own state in words, so a reader who cannot
		// see the glyph beside them still gets it. A pointer gets the title.
		it('should name the state it is reporting', () => {
			render(CertRow, {
				cert: cert({ expires_at: '2026-08-22T11:00:00Z' }),
				now,
				href: '/certs/cert-1'
			});
			expect(screen.getByTitle('Expired')).toBeInTheDocument();
		});

		// The mark sits beside the lifetime it qualifies, and once the window
		// has closed the lifetime is no longer what the reader wants from the
		// row: the state replaces it rather than sitting next to it.
		it('should say expired in place of the lifetime when the window has closed', () => {
			render(CertRow, {
				cert: cert({ expires_at: '2026-08-22T11:00:00Z' }),
				now,
				href: '/certs/cert-1'
			});
			expect(screen.getByTestId('cert-validity')).toHaveTextContent('expired');
		});

		it('should not recite the granted lifetime once the certificate has expired', () => {
			render(CertRow, {
				cert: cert({ expires_at: '2026-08-22T11:00:00Z' }),
				now,
				href: '/certs/cert-1'
			});
			expect(screen.queryByText(/valid for/)).not.toBeInTheDocument();
		});

		// It is a state rather than a record, so it moves with the clock the
		// list is rendered against.
		it('should follow the clock it is given', () => {
			const row = cert({ expires_at: '2026-08-22T13:00:00Z' });
			render(CertRow, { cert: row, now: new Date('2026-08-22T14:00:00Z'), href: '/certs/cert-1' });
			expect(screen.getByTestId('cert-validity')).toHaveAttribute('data-valid', 'false');
		});
	});

	it('should name the row by the client that asked for the certificate', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.getByText('alice@alice-laptop')).toBeInTheDocument();
	});

	it('should not name the row by the deciding account', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.queryByText('alice@example.com')).not.toBeInTheDocument();
	});

	it('should name a pam row by the account and machine that asked', () => {
		const record = cert({
			type: 'pam',
			reported_username: 'root',
			reported_hostname: 'web01'
		});
		render(CertRow, { cert: record, now, href: '/certs/cert-1' });
		expect(screen.getByText('root@web01')).toBeInTheDocument();
	});

	it('should name a service row by the address that fetched the certificate', () => {
		const record = cert({
			type: 'service',
			reported_username: undefined,
			reported_hostname: undefined,
			retrieved_source_ip: '198.51.100.44'
		});
		render(CertRow, { cert: record, now, href: '/certs/cert-1' });
		expect(screen.getByText('198.51.100.44')).toBeInTheDocument();
	});

	it('should prefer the retrieval address over the approver context on a service row', () => {
		const record = cert({
			type: 'service',
			reported_username: 'alice',
			reported_hostname: 'alice-laptop',
			retrieved_source_ip: '198.51.100.44'
		});
		render(CertRow, { cert: record, now, href: '/certs/cert-1' });
		expect(screen.getByText('198.51.100.44')).toBeInTheDocument();
	});

	it('should name the row by the hostname alone when no username was reported', () => {
		const record = cert({ reported_username: undefined });
		render(CertRow, { cert: record, now, href: '/certs/cert-1' });
		expect(screen.getByText('alice-laptop')).toBeInTheDocument();
	});

	it('should fall back to the key id when nothing reported where it came from', () => {
		const record = cert({
			reported_username: undefined,
			reported_hostname: undefined
		});
		render(CertRow, { cert: record, now, href: '/certs/cert-1' });
		expect(screen.getByText('key-1')).toBeInTheDocument();
	});

	it('should report how long ago the certificate was requested', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.getByText(/2h ago/)).toBeInTheDocument();
	});

	it('should report the granted lifetime rather than the time remaining', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.getByText(/valid for 8h/)).toBeInTheDocument();
	});

	// The lifetime and the mark that qualifies it are one indicator, so the
	// mark carries the words rather than sitting a column away from them.
	it('should carry the lifetime inside the validity indicator', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.getByTestId('cert-validity')).toHaveTextContent('valid for 8h');
	});

	it('should use the event wording it is given', () => {
		render(CertRow, {
			cert: cert({ type: 'service' }),
			now,
			event: 'service enrollment requested',
			href: '/certs/cert-1'
		});
		expect(screen.getByText(/service enrollment requested/)).toBeInTheDocument();
	});

	it('should list the principals', () => {
		render(CertRow, {
			cert: cert({ principals: 'alice, alice-admin' }),
			now,
			href: '/certs/cert-1'
		});
		expect(screen.getByText('alice, alice-admin')).toBeInTheDocument();
	});

	it('should omit the principals line when there are none', () => {
		render(CertRow, { cert: cert({ principals: '' }), now, href: '/certs/cert-1' });
		expect(screen.queryByText(/principals:/)).not.toBeInTheDocument();
	});

	// A list of certificates is approvals by definition, so the mark is
	// opt-in: it is only worth its width where refusals are interleaved.
	describe('the outcome mark', () => {
		it('should be absent unless the list asks for it', () => {
			render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
			expect(screen.queryByTestId('cert-outcome')).not.toBeInTheDocument();
		});

		it('should mark an approval when the list asks for it', () => {
			render(CertRow, { cert: cert(), now, showOutcome: true, href: '/certs/cert-1' });
			expect(screen.getByTestId('cert-outcome')).toHaveAttribute('data-outcome', 'approved');
		});

		// A certificate exists only because a request was approved, so an
		// absent decision record predates the audit trail rather than
		// meaning anything else.
		it('should read as approved when a certificate has no decision record', () => {
			const record = cert({ decided_by_outcome: undefined });
			render(CertRow, { cert: record, now, showOutcome: true, href: '/certs/cert-1' });
			expect(screen.getByTestId('cert-outcome')).toHaveAttribute('data-outcome', 'approved');
		});

		it('should read as denied when the decision record says so', () => {
			const record = cert({ decided_by_outcome: 'denied' });
			render(CertRow, { cert: record, now, showOutcome: true, href: '/certs/cert-1' });
			expect(screen.getByTestId('cert-outcome')).toHaveAttribute('data-outcome', 'denied');
		});

		it('should name the outcome for assistive technology', () => {
			render(CertRow, { cert: cert(), now, showOutcome: true, href: '/certs/cert-1' });
			expect(screen.getByLabelText('Approved')).toBeInTheDocument();
		});
	});

	it('should name the certificate type for assistive technology', () => {
		render(CertRow, { cert: cert({ type: 'pam' }), now, href: '/certs/cert-1' });
		expect(screen.getByLabelText('Certificate type: PAM')).toBeInTheDocument();
	});

	it('should name a console certificate for assistive technology', () => {
		render(CertRow, { cert: cert({ type: 'console' }), now, href: '/certs/cert-1' });
		expect(screen.getByLabelText('Certificate type: Console')).toBeInTheDocument();
	});

	it('should link to the certificate the row is about', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.getByRole('link')).toHaveAttribute('href', '/certs/cert-1');
	});
});
