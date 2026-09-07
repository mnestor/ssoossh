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
	// The decision badge beside it cannot answer this: every row in a
	// certificate list was approved, and what a reader brings to one is
	// "can I still use this".
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

		// The icon is the whole indicator, so it has to carry a name: a
		// pointer gets the title, everything else gets the label.
		it('should name the state it is reporting', () => {
			render(CertRow, {
				cert: cert({ expires_at: '2026-08-22T11:00:00Z' }),
				now,
				href: '/certs/cert-1'
			});
			expect(screen.getByLabelText('Expired')).toBeInTheDocument();
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

	it('should read as approved when a certificate exists without a decision record', () => {
		const record = cert({ decided_by_outcome: undefined });
		render(CertRow, { cert: record, now, href: '/certs/cert-1' });
		expect(screen.getByText('approved')).toBeInTheDocument();
	});

	it('should read as denied when the decision record says so', () => {
		render(CertRow, { cert: cert({ decided_by_outcome: 'denied' }), now, href: '/certs/cert-1' });
		expect(screen.getByText('denied')).toBeInTheDocument();
	});

	// getByText, not getByLabelText: the badge names itself with an sr-only
	// string in the document rather than an aria-label on its span. ARIA
	// prohibits naming the `generic` role, so the label this used to assert
	// on was being dropped by every browser and the test passed against
	// markup no reader could hear. See TypeBadge.svelte.
	it('should name the certificate type for assistive technology', () => {
		render(CertRow, { cert: cert({ type: 'pam' }), now, href: '/certs/cert-1' });
		expect(screen.getByText('Certificate type: PAM')).toBeInTheDocument();
	});

	it('should name a console certificate for assistive technology', () => {
		render(CertRow, { cert: cert({ type: 'console' }), now, href: '/certs/cert-1' });
		expect(screen.getByText('Certificate type: Console')).toBeInTheDocument();
	});

	it('should link to the certificate the row is about', () => {
		render(CertRow, { cert: cert(), now, href: '/certs/cert-1' });
		expect(screen.getByRole('link')).toHaveAttribute('href', '/certs/cert-1');
	});
});
