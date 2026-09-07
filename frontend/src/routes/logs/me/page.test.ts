import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListResponse, DeniedRequest } from '$lib/api/types';
import Page from './+page.svelte';

/**
 * mockFetch stubs the global fetch, answering the certificate list with
 * `response` and the denial list with `denials`.
 *
 * The page reads two endpoints, because the server has two: a denial issues
 * no certificate, so it has no row in the table /api/certs reads. A stub
 * that answered both with the same body would let a test pass while the
 * page mixed them up.
 */
function mockFetch(response: object, status = 200, denials: object = { denials: [] }) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const body = String(input).includes('/decisions/denied') ? denials : response;
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** mockFetchError stubs the global fetch to reject with an error message. */
function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Certificate history page', () => {
	describe('when certificates load successfully with an empty list', () => {
		beforeEach(() => {
			const emptyResponse: CertificateListResponse = {
				certificates: []
			};
			mockFetch(emptyResponse);
		});

		it('should not show Loading… after the fetch resolves', async () => {
			render(Page);
			// Give the effect a chance to run and complete
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should show the empty-state message when no certificates exist', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(
				screen.getByText('You have not decided any certificate requests yet.')
			).toBeInTheDocument();
		});
	});

	describe('when the fetch fails', () => {
		beforeEach(() => {
			mockFetchError('server error');
		});

		it('should not show Loading… after the fetch rejects', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should show the error message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/Could not load your history/)).toBeInTheDocument();
		});
	});

	describe('when certificates load successfully with data', () => {
		beforeEach(() => {
			const populatedResponse: CertificateListResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '1',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: '2026-08-01T10:00:00Z',
						expires_at: new Date(Date.now() - 3600000).toISOString(),
						decided_at: '2026-08-01T10:00:00Z',
						decided_by_username: 'system',
						decided_by_subject: '',
						key_id: 'key-1'
					}
				]
			};
			mockFetch(populatedResponse);
		});

		it('should not show Loading… after the fetch resolves', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should display the certificate principal', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText('alice')).toBeInTheDocument();
		});
	});

	// The half of a decision history that /api/certs cannot answer. A denial
	// issues no certificate, so before this the page could only ever show
	// what somebody approved.
	describe('denied requests', () => {
		/** aDenial is one refusal as /api/decisions/denied returns it. */
		function aDenial(overrides: Partial<DeniedRequest> = {}): DeniedRequest {
			return {
				id: 'dec-1',
				certificate_request_id: 'req-1',
				type: 'pam',
				decided_at: '2026-08-02T10:00:00Z',
				reported_username: 'deploy',
				reported_hostname: 'rack07',
				...overrides
			};
		}

		it('should list a request the reader denied', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('denied-row')).toHaveTextContent('deploy@rack07');
		});

		it('should mark a denied row as denied', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('denied-row')).toHaveTextContent('denied');
		});

		// The point of interleaving rather than appending: a history read
		// top to bottom has to be in time order whichever kind each row is.
		it('should order a denial against the certificates by time', async () => {
			mockFetch(
				{
					certificates: [
						{
							id: 'cert-1',
							type: 'user',
							serial_number: '1',
							principals: 'alice',
							public_key_fingerprint: 'SHA256:abc123',
							issued_at: '2026-08-01T10:00:00Z',
							expires_at: '2026-08-01T18:00:00Z',
							key_id: 'key-1'
						}
					]
				},
				200,
				{ denials: [aDenial({ decided_at: '2026-08-03T10:00:00Z' })] }
			);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const denied = screen.getByTestId('denied-row');
			const cert = screen.getByTestId('cert-row');
			expect(denied.compareDocumentPosition(cert) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		});

		// A denial is not a certificate, so there is no page to open. A row
		// that looked clickable and went nowhere would be worse than one
		// that does not.
		it('should not link a denied row anywhere', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('denied-row').closest('a')).toBeNull();
		});

		it('should count a denial toward the type filter it was requested under', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial({ type: 'pam' })] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.click(screen.getByRole('button', { name: /PAM/ }));

			expect(screen.getByTestId('denied-row')).toBeInTheDocument();
		});

		it('should hide a denial under a type filter it does not match', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial({ type: 'pam' })] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.click(screen.getByRole('button', { name: /Console/ }));

			expect(screen.queryByTestId('denied-row')).not.toBeInTheDocument();
		});

		// The decisions table outlives certificate_requests by design, so a
		// denial whose request row is gone reports no type. It still has to
		// be listed: dropping it would quietly shorten the history.
		it('should still list a denial whose request type is unknown', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial({ type: undefined })] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('denied-row')).toBeInTheDocument();
		});

		// The reader has to be able to see it exists even though no filter
		// tab claims it.
		it('should fall back to the request id when nothing was reported', async () => {
			mockFetch({ certificates: [] }, 200, {
				denials: [aDenial({ reported_username: undefined, reported_hostname: undefined })]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('denied-row')).toHaveTextContent('req-1');
		});
	});

	it('should display Loading… initially', () => {
		mockFetch({ certificates: [] });
		render(Page);
		// Before the effect runs, should show loading
		expect(screen.getByText('Loading…')).toBeInTheDocument();
	});

	// A row is a link to the certificate's own page. It used to open a
	// dialog over the list, which could not be reloaded, linked to, or left
	// with the browser's own Back.
	describe('the rows', () => {
		beforeEach(() => {
			const response: CertificateListResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '1',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: '2026-08-01T10:00:00Z',
						expires_at: new Date(Date.now() + 86400000).toISOString(),
						key_id: 'key-1'
					}
				]
			};
			mockFetch(response);
		});

		it('should link a row to the certificate it is about', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByRole('link', { name: /key-1/ })).toHaveAttribute(
				'href',
				'/certs/cert-1?from=history'
			);
		});
	});
});
