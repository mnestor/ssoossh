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

	// The origin of a service certificate replaces the retrieval log this
	// panel used to try to fetch. That fetch was keyed on the certificate's
	// own id against an endpoint that resolves request ids, so it never
	// matched and the section never rendered; the source IP now arrives on
	// the certificate itself.
	describe('where a service certificate was fetched', () => {
		/** serviceCert is a service certificate carrying its retrieval origin. */
		function serviceCert(overrides: Partial<CertificateRecord> = {}): CertificateRecord {
			return cert({
				type: 'service',
				retrieved_source_ip: '198.51.100.44',
				retrieved_at: '2026-08-22T09:59:00Z',
				enrollment_id: 'enr-1',
				...overrides
			});
		}

		it('should show the address the certificate was retrieved from', () => {
			render(CertDetailModal, { cert: serviceCert(), onclosed: vi.fn() });
			expect(screen.getByText('198.51.100.44')).toBeInTheDocument();
		});

		it('should link to the service code it was redeemed from', () => {
			render(CertDetailModal, { cert: serviceCert(), onclosed: vi.fn() });
			expect(screen.getByRole('link', { name: /View the code this came from/ })).toHaveAttribute(
				'href',
				'/service-codes?modal=enr-1'
			);
		});

		// An older certificate, or one whose retrieval row has gone, carries
		// no origin — and a section headed "where it was fetched" with nothing
		// under it is worse than no section.
		it('should omit the section when no origin was reported', () => {
			render(CertDetailModal, { cert: cert({ type: 'service' }), onclosed: vi.fn() });
			expect(screen.queryByText('Where it was fetched')).not.toBeInTheDocument();
		});

		it('should omit the section for a non-service certificate', () => {
			render(CertDetailModal, { cert: cert(), onclosed: vi.fn() });
			expect(screen.queryByText('Where it was fetched')).not.toBeInTheDocument();
		});

		// The panel is rendered from the certificate it was handed. Reaching
		// for the network here is what produced the dead code this replaced.
		it('should not fetch anything to render a service certificate', () => {
			const fetchMock = vi.fn();
			vi.stubGlobal('fetch', fetchMock);
			render(CertDetailModal, { cert: serviceCert(), onclosed: vi.fn() });
			expect(fetchMock).not.toHaveBeenCalled();
			vi.unstubAllGlobals();
		});
	});
});
