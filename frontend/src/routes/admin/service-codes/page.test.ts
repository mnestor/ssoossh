import userEvent from '@testing-library/user-event';
import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import Page from './+page.svelte';

// Test methodology: render the admin service-codes list against a stubbed
// fetch. What matters: the list renders what the API returns, an empty
// answer and a failure read differently, and a row is a link to the code's
// own page — the detail itself is /admin/service-codes/[id]'s to test.

/** adminEnrollment builds one row of the admin list. */
function adminEnrollment(id: string, account: string) {
	return {
		id,
		service_account: account,
		approved_by_username: 'alice',
		approved_by_email: 'alice@example.com',
		certificate_request_id: 'req-' + id,
		principals: [account],
		key_id: account + '/req-' + id,
		public_key_fingerprint: 'SHA256:abc',
		options: { extensions: [], no_touch_required: false },
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: '2026-11-20T12:00:00Z',
		retrieval_count: 3
	};
}

/** json wraps a body in the envelope every endpoint answers with. */
function json(data: unknown): Response {
	return new Response(JSON.stringify({ data, error: null }), {
		status: 200,
		headers: { 'Content-Type': 'application/json' }
	});
}

/** stubList answers the list fetch with one page and no next cursor. */
function stubList(enrollments: object[]) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.resolve(json({ enrollments, total: enrollments.length })))
	);
}

/**
 * stubPages answers the nth request with the nth body, repeating the last;
 * a string body is a failed request. It returns the URLs requested.
 */
function stubPages(...bodies: (object | string)[]) {
	const urls: string[] = [];
	let call = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			urls.push(String(input));
			const body = bodies[Math.min(call++, bodies.length - 1)];
			if (typeof body === 'string') {
				return Promise.resolve(
					new Response(JSON.stringify({ data: null, error: body }), {
						status: 500,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			return Promise.resolve(json(body));
		})
	);
	return urls;
}

/** firstPage and lastPage are a two-code list, one code per page. */
const firstPage = {
	enrollments: [adminEnrollment('enr-1', 'svc-deploy')],
	total: 2,
	next_cursor: 'enr-1'
};
const lastPage = { enrollments: [adminEnrollment('enr-2', 'svc-backup')], total: 2 };

beforeEach(() => {
	vi.unstubAllGlobals();
});

describe('admin service codes page', () => {
	it('should list every enrollment the API returns', async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy'), adminEnrollment('enr-2', 'svc-backup')]);

		render(Page);

		expect(await screen.findAllByTestId('enrollment-row')).toHaveLength(2);
		expect(screen.getByText('svc-deploy')).toBeInTheDocument();
		expect(screen.getByText('svc-backup')).toBeInTheDocument();
	});

	// A reader who cannot see the rows change is told what the list became
	// (WCAG 4.1.3), as the other admin lists already do.
	it('should announce how many codes were found', async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy'), adminEnrollment('enr-2', 'svc-backup')]);

		render(Page);

		await screen.findAllByTestId('enrollment-row');
		expect(screen.getByTestId('list-status')).toHaveTextContent('2 service codes found.');
	});

	it('should say so when no codes exist', async () => {
		stubList([]);

		render(Page);

		expect(await screen.findByTestId('enrollments-empty')).toBeInTheDocument();
	});

	it('should surface a load failure instead of an empty list', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: null, error: 'not authorized as auditor' }), {
						status: 403,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		render(Page);

		expect(await screen.findByText('Could not load service codes')).toBeInTheDocument();
		expect(screen.queryByTestId('enrollments-empty')).not.toBeInTheDocument();
	});

	// A code is its own page now. The list's job is to address it: the
	// detail, and everything a reader can do to a code from it, belongs to
	// /admin/service-codes/[id].
	it("should link each row to the code's own page", async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy')]);

		render(Page);

		expect((await screen.findAllByTestId('enrollment-row'))[0]).toHaveAttribute(
			'href',
			'/admin/service-codes/enr-1'
		);
	});

	it('should not offer Load more when one page holds every code', async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy')]);
		render(Page);
		await screen.findAllByTestId('enrollment-row');
		expect(screen.queryByTestId('enrollments-load-more')).not.toBeInTheDocument();
	});
});

describe('loading more codes', () => {
	it('should count the codes shown and the older still to load', async () => {
		stubPages(firstPage);
		render(Page);
		expect(await screen.findByTestId('enrollments-count')).toHaveTextContent(
			'1 shown, 1 older not loaded'
		);
	});

	it('should count only what is shown once the last page is in', async () => {
		stubPages(firstPage, lastPage);
		render(Page);
		await userEvent.click(await screen.findByTestId('enrollments-load-more'));
		await screen.findByText('svc-backup');
		expect(screen.getByTestId('enrollments-count')).toHaveTextContent(/^2 shown$/);
	});

	it('should offer Load more when more codes exist', async () => {
		stubPages(firstPage);
		render(Page);
		expect(await screen.findByTestId('enrollments-load-more')).toBeInTheDocument();
	});

	it('should ask for the page after the cursor when Load more is pressed', async () => {
		const urls = stubPages(firstPage, lastPage);
		render(Page);
		await userEvent.click(await screen.findByTestId('enrollments-load-more'));
		await screen.findByText('svc-backup');
		expect(urls[1]).toContain('after=enr-1');
	});

	it('should append the next page below the codes already shown', async () => {
		stubPages(firstPage, lastPage);
		render(Page);
		await userEvent.click(await screen.findByTestId('enrollments-load-more'));
		await screen.findByText('svc-backup');
		expect(screen.getAllByTestId('enrollment-row')).toHaveLength(2);
	});

	it('should remove Load more once the last page is in', async () => {
		stubPages(firstPage, lastPage);
		render(Page);
		await userEvent.click(await screen.findByTestId('enrollments-load-more'));
		await screen.findByText('svc-backup');
		expect(screen.queryByTestId('enrollments-load-more')).not.toBeInTheDocument();
	});

	it('should keep the codes shown and offer a retry when Load more fails', async () => {
		stubPages(firstPage, 'boom', lastPage);
		render(Page);
		await userEvent.click(await screen.findByTestId('enrollments-load-more'));
		await screen.findByTestId('enrollments-more-error');
		expect(screen.getByText('svc-deploy')).toBeInTheDocument();
		await userEvent.click(screen.getByRole('button', { name: /try again/i }));
		expect(await screen.findByText('svc-backup')).toBeInTheDocument();
	});

	// Where keyboard focus lands once the pressed control is gone: the count
	// line after the last page, Try again after a failure. See
	// LoadMoreFooter, which owns the rule for every Load more list.
	const focusCases: { name: string; bodies: (object | string)[]; retry: boolean; lands: string }[] =
		[
			{
				name: 'should move focus to the count line when the last page loads',
				bodies: [firstPage, lastPage],
				retry: false,
				lands: 'enrollments-count'
			},
			{
				name: 'should move focus to Try again when Load more fails',
				bodies: [firstPage, 'boom'],
				retry: false,
				lands: 'enrollments-more-error-retry'
			},
			{
				name: 'should move focus to the count line when a retry loads the last page',
				bodies: [firstPage, 'boom', lastPage],
				retry: true,
				lands: 'enrollments-count'
			}
		];

	for (const { name, bodies, retry, lands } of focusCases) {
		it(name, async () => {
			stubPages(...bodies);
			render(Page);
			await userEvent.click(await screen.findByTestId('enrollments-load-more'));
			if (retry) {
				await userEvent.click(await screen.findByTestId('enrollments-more-error-retry'));
			}
			await vi.waitFor(() => expect(document.activeElement).toBe(screen.getByTestId(lands)));
		});
	}

	it('should start a new search from its first page', async () => {
		const urls = stubPages(firstPage, lastPage);
		render(Page);
		await userEvent.click(await screen.findByTestId('enrollments-load-more'));
		await screen.findByText('svc-backup');
		await userEvent.type(screen.getByTestId('search-enrollments'), 'deploy{Enter}');
		await vi.waitFor(() => expect(urls.some((url) => url.includes('q=deploy'))).toBe(true));
		expect(
			urls.filter((url) => url.includes('q=deploy')).every((url) => !url.includes('after='))
		).toBe(true);
	});
});

describe('a search that matches nothing', () => {
	it('should offer a way back to the full list', async () => {
		stubList([]);
		render(Page);
		await screen.findByTestId('enrollments-empty');
		await userEvent.type(screen.getByTestId('search-enrollments'), 'nothing-like-this{Enter}');
		expect(await screen.findByTestId('enrollments-clear-search')).toBeInTheDocument();
	});

	it('should promise only what the detail page can do', () => {
		stubList([]);
		render(Page);
		expect(screen.queryByText(/reassign/)).not.toBeInTheDocument();
	});
});
