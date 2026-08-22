import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { CertificateRecord } from '$lib/api/types';
import CertDetailModal from './CertDetailModal.svelte';

/** cert builds a certificate record, overriding only what a case cares about. */
function cert(overrides: Partial<CertificateRecord> = {}): CertificateRecord {
	return {
		id: 'cert-1234-5678',
		type: 'user',
		serial_number: 42,
		key_id: 'key-1',
		principals: 'alice, alice-admin',
		public_key_fingerprint: 'SHA256:abc',
		issued_at: '2026-08-22T10:00:00Z',
		expires_at: '2026-08-22T18:00:00Z',
		...overrides
	};
}

describe('CertDetailModal', () => {
	it('should show the short form of the certificate id', () => {
		render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
		expect(screen.getByText('cert-')).toBeInTheDocument();
	});

	// A bare "#6eb31" reads as a hex colour. The label says what it is, and
	// the title carries the whole id for anyone matching it against a log.
	it('should label the id rather than prefixing it with a bare hash', () => {
		render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
		expect(screen.getByTitle('cert-1234-5678')).toHaveTextContent(/^ID/);
	});

	it('should list each principal separately', () => {
		render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
		expect(screen.getByText('alice-admin')).toBeInTheDocument();
	});

	it('should show the granted lifetime', () => {
		render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
		expect(screen.getByText('8h')).toBeInTheDocument();
	});

	it('should show who decided when a decision record exists', () => {
		const record = cert({
			decided_by_email: 'irene@example.com',
			decided_at: '2026-08-22T09:00:00Z'
		});
		render(CertDetailModal, { cert: record, onclosed: vi.fn() });
		expect(screen.getByText('irene@example.com')).toBeInTheDocument();
	});

	it('should show where the decision came from when recorded', () => {
		const record = cert({
			decided_by_email: 'irene@example.com',
			decided_source_ip: '203.0.113.42'
		});
		render(CertDetailModal, { cert: record, onclosed: vi.fn() });
		expect(screen.getByText('203.0.113.42')).toBeInTheDocument();
	});

	it('should omit the decision banner when nothing was recorded', () => {
		render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
		expect(screen.queryByText(/Approved by/)).not.toBeInTheDocument();
	});

	it('should call onclosed when the close button is used', async () => {
		const onclosed = vi.fn();
		render(CertDetailModal, { cert: cert(), onclosed });
		await userEvent.click(screen.getByRole('button', { name: 'Close' }));
		expect(onclosed).toHaveBeenCalledOnce();
	});

	// Regression: the dialog used to close on Escape without telling its
	// parent, which left the ?modal= parameter set. The row then refused to
	// reopen the certificate, because as far as the page was concerned it was
	// already open.
	it('should call onclosed when the dialog is dismissed with Escape', () => {
		const onclosed = vi.fn();
		render(CertDetailModal, { cert: cert(), onclosed });
		const dialog = document.querySelector('dialog');
		if (!dialog) {
			throw new Error('expected a <dialog> element to be rendered');
		}
		dialog.dispatchEvent(new Event('close'));
		expect(onclosed).toHaveBeenCalledOnce();
	});

	it('should offer a link to this specific certificate', () => {
		render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
		expect(
			screen.getByRole('button', { name: 'Copy link to this certificate' })
		).toBeInTheDocument();
	});
});
