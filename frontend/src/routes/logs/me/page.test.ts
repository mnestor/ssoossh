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
	requested.length = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			requested.push(url);
			const body = url.includes('/decisions/denied') ? denials : response;
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** requested records every URL the page fetched, newest last. */
const requested: string[] = [];

/** lastRequest is the most recent URL matching a path fragment. */
function lastRequest(fragment: string): string | undefined {
	return [...requested].reverse().find((url) => url.includes(fragment));
}

/**
 * stubViewport installs the matchMedia jsdom does not provide, reporting the
 * given width, and returns the switch that resizes it afterwards — which is
 * what a rotation or a dragged window looks like to the page.
 */
function stubViewport(narrow: boolean) {
	const listeners = new Set<(event: MediaQueryListEvent) => void>();
	const list = {
		matches: narrow,
		addEventListener: (_: string, fn: (event: MediaQueryListEvent) => void) => {
			listeners.add(fn);
		},
		removeEventListener: (_: string, fn: (event: MediaQueryListEvent) => void) => {
			listeners.delete(fn);
		}
	};
	vi.stubGlobal('matchMedia', () => list);
	return (next: boolean) => {
		list.matches = next;
		for (const fn of listeners) {
			fn({ matches: next } as MediaQueryListEvent);
		}
	};
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
				pam_service: 'sudo',
				tty: 'pts/3',
				remote_host: '10.1.2.9',
				...overrides
			};
		}

		/** aCertificate is one issued certificate, for the interleaving cases. */
		function aCertificate(issuedAt: string) {
			return {
				id: 'cert-1',
				type: 'user' as const,
				serial_number: '1',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abc123',
				issued_at: issuedAt,
				expires_at: '2026-08-01T18:00:00Z',
				key_id: 'key-1'
			};
		}

		/** show switches the outcome filter, which opens on "Approved". */
		async function show(outcome: 'Approved' | 'Denied' | 'Both') {
			await userEvent.click(screen.getByRole('button', { name: outcome }));
		}

		// The page has always been the list of certificates somebody holds,
		// and opening it on a mixture would change what an existing reader
		// gets without their asking.
		it('should open on approvals only', async () => {
			mockFetch({ certificates: [aCertificate('2026-08-01T10:00:00Z')] }, 200, {
				denials: [aDenial()]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('cert-row')).toBeInTheDocument();
			expect(screen.queryByTestId('denied-row')).not.toBeInTheDocument();
		});

		it('should show only denials when the outcome filter asks for them', async () => {
			mockFetch({ certificates: [aCertificate('2026-08-01T10:00:00Z')] }, 200, {
				denials: [aDenial()]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');

			expect(screen.getByTestId('denied-row')).toBeInTheDocument();
			expect(screen.queryByTestId('cert-row')).not.toBeInTheDocument();
		});

		it('should show both when the outcome filter asks for both', async () => {
			mockFetch({ certificates: [aCertificate('2026-08-01T10:00:00Z')] }, 200, {
				denials: [aDenial()]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Both');

			expect(screen.getByTestId('cert-row')).toBeInTheDocument();
			expect(screen.getByTestId('denied-row')).toBeInTheDocument();
		});

		it('should list a request the reader denied', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');

			expect(screen.getByTestId('denied-row')).toHaveTextContent('deploy@rack07');
		});

		it('should mark a denied row as denied', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');

			expect(screen.getByTestId('denied-row')).toHaveTextContent('denied');
		});

		// What makes a refusal recognisable a month later, in the column a
		// certificate row uses for the principals it granted.
		it('should say what the request claimed it was doing', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');

			expect(screen.getByTestId('denied-row')).toHaveTextContent('sudo · pts/3 · from 10.1.2.9');
		});

		// The point of interleaving rather than appending: a history read
		// top to bottom has to be in time order whichever kind each row is.
		it('should order a denial against the certificates by time', async () => {
			mockFetch({ certificates: [aCertificate('2026-08-01T10:00:00Z')] }, 200, {
				denials: [aDenial({ decided_at: '2026-08-03T10:00:00Z' })]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Both');

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
			await show('Denied');

			expect(screen.getByTestId('denied-row').closest('a')).toBeNull();
		});

		// Which denials match a type is the server's answer now -- see
		// ListDeniedForIdentity, which reads it off the joined request row.
		// What this page owes is asking the question.
		it('should send the type filter to the denial endpoint too', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial({ type: 'pam' })] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');
			await userEvent.click(screen.getByTestId('type-filter-console'));

			await vi.waitFor(() => expect(lastRequest('/decisions/denied')).toContain('type=console'));
		});

		// The decisions table outlives certificate_requests by design, so a
		// denial whose request row is gone reports no type. The row still
		// has to render: the server drops it from a type filter, but under
		// "All" it is a real refusal like any other.
		it('should still render a denial whose request type is unknown', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [aDenial({ type: undefined })] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');

			expect(screen.getByTestId('denied-row')).toBeInTheDocument();
		});

		it('should fall back to the request id when nothing was reported', async () => {
			mockFetch({ certificates: [] }, 200, {
				denials: [
					aDenial({
						reported_username: undefined,
						reported_hostname: undefined
					})
				]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await show('Denied');

			expect(screen.getByTestId('denied-row')).toHaveTextContent('req-1');
		});
	});

	// The same three groups and the same search box the admin certificate
	// list opens with, and like that list they ask the server. What is
	// pinned here is that the page sends what was asked for; what the
	// server does with it is pinned in server/service.
	describe('the filters', () => {
		/** aCert is one issued certificate, overridable per case. */
		function aCert(overrides: Record<string, unknown> = {}) {
			return {
				id: 'cert-1',
				type: 'user' as const,
				serial_number: '1',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abc123',
				key_id: 'workstation',
				issued_at: '2026-08-01T10:00:00Z',
				expires_at: '2099-01-01T00:00:00Z',
				reported_username: 'alice',
				reported_hostname: 'alice-laptop',
				...overrides
			};
		}

		// Ordered by how often a reader reaches for one: the type is what
		// most visits narrow by, and the outcome is the last thing they
		// would change.
		it('should put the outcome group last on the line', async () => {
			mockFetch({ certificates: [aCert()] });
			const { container } = render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const groups = [...container.querySelectorAll('[data-testid$="-filter"]')].map((group) =>
				group.getAttribute('data-testid')
			);
			expect(groups).toEqual(['type-filter', 'status-filter', 'outcome-filter']);
		});

		it('should name each filter group', async () => {
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('outcome-filter')).toBeInTheDocument();
			expect(screen.getByTestId('type-filter')).toBeInTheDocument();
			expect(screen.getByTestId('status-filter')).toBeInTheDocument();
		});

		// The page opens on approvals, so somebody whose only history is a
		// refusal would otherwise land on "nothing here" with no control on
		// screen to go and find it.
		it('should keep the filters on screen when nothing matches', async () => {
			mockFetch({ certificates: [] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('outcome-filter')).toBeInTheDocument();
			expect(
				screen.getByText('You have not decided any certificate requests yet.')
			).toBeInTheDocument();
		});

		it('should send the search term to the server', async () => {
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.type(screen.getByTestId('search-input'), 'buildbox');

			await vi.waitFor(() => expect(lastRequest('/certs')).toContain('q=buildbox'));
		});

		it('should send the type to the server', async () => {
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.click(screen.getByTestId('type-filter-pam'));

			await vi.waitFor(() => expect(lastRequest('/certs')).toContain('type=pam'));
		});

		it('should send the validity to the server', async () => {
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.click(screen.getByTestId('status-filter-expired'));

			await vi.waitFor(() => expect(lastRequest('/certs')).toContain('status=expired'));
		});

		it('should send no filter parameters when nothing is narrowed', async () => {
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const url = lastRequest('/certs') ?? '';
			expect(url).not.toContain('q=');
			expect(url).not.toContain('type=');
			expect(url).not.toContain('status=');
		});

		// The outcome does not sieve what arrived — it decides what is
		// fetched, so the endpoint that cannot contribute is never called.
		it('should not read the denial endpoint while showing approvals', async () => {
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(lastRequest('/decisions/denied')).toBeUndefined();
		});

		it('should not read the certificate endpoint while showing denials', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			requested.length = 0;

			await userEvent.click(screen.getByTestId('outcome-filter-denied'));

			await vi.waitFor(() => expect(lastRequest('/decisions/denied')).toBeDefined());
			expect(lastRequest('/certs')).toBeUndefined();
		});

		it('should read both endpoints when either outcome will do', async () => {
			mockFetch({ certificates: [] }, 200, { denials: [] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			requested.length = 0;

			await userEvent.click(screen.getByTestId('outcome-filter-any'));

			await vi.waitFor(() => expect(lastRequest('/decisions/denied')).toBeDefined());
			expect(lastRequest('/certs')).toBeDefined();
		});

		it('should say so when a narrowed history comes back empty', async () => {
			mockFetch({ certificates: [] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.click(screen.getByTestId('type-filter-pam'));

			await vi.waitFor(() =>
				expect(
					screen.getByText('Nothing in your history matches the selected filter.')
				).toBeInTheDocument()
			);
		});
	});

	// A request that never answers, so the wait itself is what is on screen.
	// With a resolving stub the response lands inside the same 300ms and the
	// gate is never the thing under test.
	it('should hold the rows back until the first response settles', async () => {
		vi.useFakeTimers();
		vi.stubGlobal(
			'fetch',
			vi.fn(() => new Promise(() => {}))
		);
		try {
			render(Page);
			expect(screen.queryByTestId('history-loading')).not.toBeInTheDocument();
			await vi.advanceTimersByTimeAsync(300);
			expect(screen.getByTestId('history-loading')).toBeInTheDocument();
		} finally {
			vi.useRealTimers();
		}
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

	// A phone-width row has space for the subject, the detail line and one
	// indicator. The outcome chip is what had to go, so the page has to
	// guarantee what the missing chip would have said.
	describe('on a phone-width viewport', () => {
		function aCert(): CertificateListResponse['certificates'][number] {
			return {
				id: 'cert-1',
				type: 'user',
				serial_number: '1',
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abc123',
				issued_at: '2026-08-01T10:00:00Z',
				expires_at: '2026-08-01T18:00:00Z',
				key_id: 'key-1'
			};
		}

		it('should not offer the outcome filter', async () => {
			stubViewport(true);
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByTestId('outcome-filter')).not.toBeInTheDocument();
		});

		it('should still offer the other filter groups', async () => {
			stubViewport(true);
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('type-filter')).toBeInTheDocument();
		});

		it('should not read the denial endpoint', async () => {
			stubViewport(true);
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(lastRequest('/decisions/denied')).toBeUndefined();
		});

		// The space the mark was taking is the whole point of pinning the
		// filter — every row here is an approval, so nothing is lost.
		it('should not mark the outcome on a row', async () => {
			stubViewport(true);
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByTestId('cert-outcome')).not.toBeInTheDocument();
		});

		// Wider than a phone, the list mixes approvals and refusals, so the
		// row has to say which it is.
		it('should mark the outcome on a row once the viewport widens', async () => {
			const resize = stubViewport(true);
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			resize(false);

			await vi.waitFor(() =>
				expect(screen.getByTestId('cert-outcome')).toHaveAttribute('data-outcome', 'approved')
			);
		});

		// Pinned, not merely hidden: a reader who asked for refusals on a
		// wide window must not be left looking at rows that no longer say
		// they were refused.
		it('should return a denied selection to approvals when the viewport narrows', async () => {
			const resize = stubViewport(false);
			mockFetch({ certificates: [aCert()] }, 200, {
				denials: [
					{
						id: 'denial-1',
						certificate_request_id: 'req-1',
						type: 'user',
						decided_at: '2026-08-01T09:00:00Z'
					} as DeniedRequest
				]
			});
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await userEvent.click(screen.getByTestId('outcome-filter-denied'));
			await vi.waitFor(() => expect(screen.getByTestId('denied-row')).toBeInTheDocument());

			resize(true);

			await vi.waitFor(() => expect(screen.getByTestId('cert-row')).toBeInTheDocument());
			expect(screen.queryByTestId('denied-row')).not.toBeInTheDocument();
		});
	});
});
