import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListResponse, DeniedRequest } from '#lib/api/types.js';
import Page from './+page.svelte';

// The page writes its filters back to the address bar with a shallow goto.
// Unmocked, SvelteKit's real client throws on every call, since no app is
// running under jsdom. page.url.test.ts covers what gets written.
vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

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
			expect(screen.getByTestId('history-empty')).toHaveTextContent('No certificates yet');
		});

		// The page opens on approvals; "no decisions" was false for someone
		// whose only decision was a refusal.
		it('should point at refused requests rather than say there are no decisions', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByTestId('history-empty')).toHaveTextContent(
				'Refused requests are under Outcome: Denied'
			);
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

		// A failed read is not "you have decided nothing".
		it('should not show the empty-history message under the error', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(
				screen.queryByText('You have not decided any certificate requests yet.')
			).not.toBeInTheDocument();
		});

		it('should read the history again when Try again is pressed', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const calls = vi.mocked(fetch).mock.calls.length;

			await userEvent.click(screen.getByRole('button', { name: 'Try again' }));

			expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(calls);
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
			expect(screen.getByTestId('history-empty')).toHaveTextContent('No certificates yet');
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
					screen.getByText('Nothing in your history matches this search or these filters.')
				).toBeInTheDocument()
			);
		});

		// The filtered empty state offers its own way back, not just the
		// filter chips above it.
		it('should drop the narrowing when Clear filters is pressed', async () => {
			mockFetch({ certificates: [] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await userEvent.click(screen.getByTestId('type-filter-pam'));

			await userEvent.click(await screen.findByTestId('history-clear-filters'));

			expect(screen.getByTestId('type-filter-pam')).toHaveAttribute('aria-pressed', 'false');
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

		// The type badge is a mark, not a name: the row says the type in
		// words, so nobody has to hover the glyph to tell user from PAM.
		it('should name a user certificate in words on its detail line', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByTestId('cert-row')).toHaveTextContent(/user certificate issued/);
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

		// A refusal carries its own mark (DeniedRow), so the outcome filter
		// stays on offer on a phone; pinning it to approvals left a refusal
		// unreachable there.
		it('should still offer the outcome filter', async () => {
			stubViewport(true);
			mockFetch({ certificates: [aCert()] });
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('outcome-filter')).toBeInTheDocument();
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
		it('should keep a denied selection when the viewport narrows', async () => {
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
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.getByTestId('denied-row')).toBeInTheDocument();
		});
	});
});

describe('merging certificates and refusals', () => {
	function cert(id: string, issuedAt: string) {
		return {
			id,
			type: 'user',
			serial_number: id,
			principals: 'alice',
			public_key_fingerprint: 'SHA256:abc',
			issued_at: issuedAt,
			expires_at: new Date(Date.now() + 86400000).toISOString(),
			key_id: id
		};
	}

	function denial(id: string, decidedAt: string): DeniedRequest {
		return {
			id,
			certificate_request_id: `req-${id}`,
			type: 'user',
			decided_at: decidedAt
		} as DeniedRequest;
	}

	// The certificates have more to fetch, so anything older than the oldest
	// loaded one may still have certificates to come. Showing the old refusal
	// there made a timeline with a hole in it.
	it('should hold back a refusal older than the certificates loaded so far', async () => {
		mockFetch(
			{
				certificates: [cert('c1', '2026-10-11T10:00:00Z'), cert('c2', '2026-10-10T10:00:00Z')],
				next_cursor: 'more'
			},
			200,
			{ denials: [denial('d1', '2026-10-10T12:00:00Z'), denial('d2', '2026-04-01T00:00:00Z')] }
		);
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId('outcome-filter-any'));
		await vi.waitFor(() => expect(screen.getAllByTestId('denied-row')).toHaveLength(1));
	});

	it('should show every refusal once neither source has more', async () => {
		mockFetch({ certificates: [cert('c1', '2026-10-11T10:00:00Z')], next_cursor: null }, 200, {
			denials: [denial('d1', '2026-10-10T12:00:00Z'), denial('d2', '2026-04-01T00:00:00Z')]
		});
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId('outcome-filter-any'));
		await vi.waitFor(() => expect(screen.getAllByTestId('denied-row')).toHaveLength(2));
	});
});

describe('paging', () => {
	// One pager: a page-of-ten pager used to sit over Load more and read
	// "Page 3 of 3" while more existed.
	it('should offer no Previous or Next', async () => {
		mockFetch({ certificates: [], next_cursor: 'more' });
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.queryByRole('button', { name: 'Previous' })).not.toBeInTheDocument();
	});

	it('should show Load more while there is more', async () => {
		mockFetch({
			certificates: [
				{
					id: 'c1',
					type: 'user',
					serial_number: '1',
					principals: 'alice',
					public_key_fingerprint: 'SHA256:abc',
					issued_at: '2026-10-11T10:00:00Z',
					expires_at: new Date(Date.now() + 86400000).toISOString(),
					key_id: 'k1'
				}
			],
			next_cursor: 'more'
		});
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.getByTestId('history-load-more')).toBeInTheDocument();
	});
});

describe('the announced count', () => {
	it('should count refusals as denied requests', async () => {
		mockFetch({ certificates: [] }, 200, {
			denials: [
				{
					id: 'd1',
					certificate_request_id: 'req-1',
					type: 'user',
					decided_at: '2026-10-10T12:00:00Z'
				}
			]
		});
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId('outcome-filter-denied'));
		await vi.waitFor(() =>
			expect(screen.getByTestId('list-status')).toHaveTextContent('1 denied request found')
		);
	});
});

describe('when Load more fails', () => {
	it('should show the failure with its retry instead of a second Load more', async () => {
		let certReads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn((input: RequestInfo | URL) => {
				const url = String(input);
				if (url.includes('/decisions/denied')) {
					return Promise.resolve(
						new Response(JSON.stringify({ data: { denials: [] }, error: null }), {
							status: 200,
							headers: { 'Content-Type': 'application/json' }
						})
					);
				}
				certReads += 1;
				if (certReads > 1) return Promise.reject(new Error('offline'));
				return Promise.resolve(
					new Response(
						JSON.stringify({
							data: {
								certificates: [
									{
										id: 'c1',
										type: 'user',
										serial_number: '1',
										principals: 'alice',
										public_key_fingerprint: 'SHA256:abc',
										issued_at: '2026-10-11T10:00:00Z',
										expires_at: new Date(Date.now() + 86400000).toISOString(),
										key_id: 'k1'
									}
								],
								next_cursor: 'more'
							},
							error: null
						}),
						{ status: 200, headers: { 'Content-Type': 'application/json' } }
					)
				);
			})
		);
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId('history-load-more'));
		await screen.findByTestId('history-more-error');
		expect(screen.queryByTestId('history-load-more')).not.toBeInTheDocument();
	});
});

// Where keyboard focus lands once the pressed control is gone: the count
// line after the last page, Try again after a failure. See LoadMoreFooter,
// which owns the rule for every Load more list.
describe('keyboard focus after Load more', () => {
	/** historyCert builds one certificate row issued at the given time. */
	function historyCert(id: string, issuedAt: string) {
		return {
			id,
			type: 'user',
			serial_number: id,
			principals: 'alice',
			public_key_fingerprint: 'SHA256:abc',
			issued_at: issuedAt,
			expires_at: new Date(Date.now() + 86400000).toISOString(),
			key_id: id
		};
	}

	const first = { certificates: [historyCert('c1', '2026-10-11T10:00:00Z')], next_cursor: 'c1' };
	const last = { certificates: [historyCert('c2', '2026-10-10T10:00:00Z')] };

	/**
	 * stubPages answers the nth certificate read with the nth page
	 * ('offline' is a failed read) and every denial read with none.
	 */
	function stubPages(...pages: (object | 'offline')[]) {
		let call = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn((input: RequestInfo | URL) => {
				const ok = (data: object) =>
					Promise.resolve(
						new Response(JSON.stringify({ data, error: null }), {
							status: 200,
							headers: { 'Content-Type': 'application/json' }
						})
					);
				if (String(input).includes('/decisions/denied')) return ok({ denials: [] });
				const page = pages[Math.min(call++, pages.length - 1)];
				if (page === 'offline') return Promise.reject(new Error('offline'));
				return ok(page);
			})
		);
	}

	const cases: { name: string; pages: (object | 'offline')[]; retry: boolean; lands: string }[] = [
		{
			name: 'should move focus to the count line when the last page loads',
			pages: [first, last],
			retry: false,
			lands: 'history-count'
		},
		{
			name: 'should move focus to Try again when Load more fails',
			pages: [first, 'offline'],
			retry: false,
			lands: 'history-more-error-retry'
		},
		{
			name: 'should move focus to the count line when a retry loads the last page',
			pages: [first, 'offline', last],
			retry: true,
			lands: 'history-count'
		}
	];

	for (const { name, pages, retry, lands } of cases) {
		it(name, async () => {
			stubPages(...pages);
			render(Page);
			await userEvent.click(await screen.findByTestId('history-load-more'));
			if (retry) {
				await userEvent.click(await screen.findByTestId('history-more-error-retry'));
			}
			await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByTestId(lands)));
		});
	}

	it('should keep the count line once the last page is in', async () => {
		stubPages(first, last);
		render(Page);
		await userEvent.click(await screen.findByTestId('history-load-more'));
		await screen.findByText(/c2/);
		expect(screen.getByTestId('history-count')).toHaveTextContent(/^2 shown$/);
	});
});
