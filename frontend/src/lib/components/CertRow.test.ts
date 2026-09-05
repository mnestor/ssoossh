import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

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
		...overrides
	};
}

describe('CertRow', () => {
	it('should name the row by the deciding account when one is recorded', () => {
		render(CertRow, { cert: cert(), now, onclick: vi.fn() });
		expect(screen.getByText('alice@example.com')).toBeInTheDocument();
	});

	it('should fall back to the key id when no account is recorded', () => {
		const record = cert({
			decided_by_email: undefined,
			decided_by_username: undefined
		});
		render(CertRow, { cert: record, now, onclick: vi.fn() });
		expect(screen.getByText('key-1')).toBeInTheDocument();
	});

	it('should report how long ago the certificate was requested', () => {
		render(CertRow, { cert: cert(), now, onclick: vi.fn() });
		expect(screen.getByText(/2h ago/)).toBeInTheDocument();
	});

	it('should report the granted lifetime rather than the time remaining', () => {
		render(CertRow, { cert: cert(), now, onclick: vi.fn() });
		expect(screen.getByText(/valid for 8h/)).toBeInTheDocument();
	});

	it('should use the event wording it is given', () => {
		render(CertRow, {
			cert: cert({ type: 'service' }),
			now,
			event: 'service enrollment requested',
			onclick: vi.fn()
		});
		expect(screen.getByText(/service enrollment requested/)).toBeInTheDocument();
	});

	it('should list the principals', () => {
		render(CertRow, { cert: cert({ principals: 'alice, alice-admin' }), now, onclick: vi.fn() });
		expect(screen.getByText('alice, alice-admin')).toBeInTheDocument();
	});

	it('should omit the principals line when there are none', () => {
		render(CertRow, { cert: cert({ principals: '' }), now, onclick: vi.fn() });
		expect(screen.queryByText(/principals:/)).not.toBeInTheDocument();
	});

	it('should read as approved when a certificate exists without a decision record', () => {
		const record = cert({ decided_by_outcome: undefined });
		render(CertRow, { cert: record, now, onclick: vi.fn() });
		expect(screen.getByText('approved')).toBeInTheDocument();
	});

	it('should read as denied when the decision record says so', () => {
		render(CertRow, { cert: cert({ decided_by_outcome: 'denied' }), now, onclick: vi.fn() });
		expect(screen.getByText('denied')).toBeInTheDocument();
	});

	it('should name the certificate type for assistive technology', () => {
		render(CertRow, { cert: cert({ type: 'pam' }), now, onclick: vi.fn() });
		expect(screen.getByLabelText('Certificate type: PAM')).toBeInTheDocument();
	});

	it('should name a console certificate for assistive technology', () => {
		render(CertRow, { cert: cert({ type: 'console' }), now, onclick: vi.fn() });
		expect(screen.getByLabelText('Certificate type: Console')).toBeInTheDocument();
	});

	it('should call onclick when the row is activated', async () => {
		const onclick = vi.fn();
		render(CertRow, { cert: cert(), now, onclick });
		await userEvent.click(screen.getByRole('button'));
		expect(onclick).toHaveBeenCalledOnce();
	});
});
