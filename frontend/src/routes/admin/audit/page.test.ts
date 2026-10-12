import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The page reads its filters from the address and writes them back, so the
// router is stubbed: a fake page for the address, and a replaceState that
// records nothing. The filter behaviour itself is in page.filters.test.ts.
vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ replaceState: vi.fn(), goto: vi.fn() }));

import { resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

// Test methodology: render the audit feed page against a stubbed fetch.
// The behaviors that matter are the paging contract — "load more" grows
// one continuous list rather than replacing it — and the failure path,
// since an auditor staring at an empty feed must be able to tell "nothing
// happened" from "the feed did not load".

/**
 * feedPage builds one AuditEventsResponse payload; nextCursor is the
 * after for the following page, omitted on the last.
 */
function feedPage(events: object[], total: number, nextCursor?: string) {
	return nextCursor === undefined ? { events, total } : { events, total, next_cursor: nextCursor };
}

/** auditEvent builds one feed row. */
function auditEvent(id: string, action: string) {
	return {
		id,
		created_at: '2026-08-29T12:00:00Z',
		action,
		actor: { username: 'alice' }
	};
}

/** stubFeed answers successive fetches with successive payloads. */
function stubFeed(...pages: object[]) {
	const spy = vi.fn();
	for (const page of pages) {
		spy.mockResolvedValueOnce(
			new Response(JSON.stringify({ data: page, error: null }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		);
	}
	vi.stubGlobal('fetch', spy);
	return spy;
}

beforeEach(() => {
	resetFakePage('http://localhost/admin/audit');
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('admin audit page', () => {
	it('should render the feed newest batch with its running count', async () => {
		stubFeed(feedPage([auditEvent('e1', 'user.disabled'), auditEvent('e2', 'auth.login')], 2));

		render(Page);

		expect(await screen.findByTestId('audit-timeline')).toBeInTheDocument();
		expect(screen.getAllByTestId('audit-event')).toHaveLength(2);
		expect(screen.getByTestId('audit-count')).toHaveTextContent('2 shown');
	});

	it('should render a refused self-disable with its label', async () => {
		stubFeed(feedPage([auditEvent('e1', 'user.disable_refused')], 1));

		render(Page);

		expect(await screen.findByTestId('audit-event')).toHaveTextContent(
			'alice was refused a self-disable'
		);
	});

	it('should not offer to load more on the last page', async () => {
		stubFeed(feedPage([auditEvent('e1', 'auth.login')], 1));

		render(Page);

		await screen.findByTestId('audit-timeline');
		expect(screen.queryByText('Load more')).not.toBeInTheDocument();
	});

	it('should grow one continuous list when loading more', async () => {
		const spy = stubFeed(
			feedPage([auditEvent('e1', 'user.disabled')], 2, 'e1'),
			feedPage([auditEvent('e2', 'auth.login')], 2)
		);

		render(Page);
		const user = userEvent.setup();

		await user.click(await screen.findByText('Load more'));

		await waitFor(() => expect(screen.getAllByTestId('audit-event')).toHaveLength(2));
		expect(screen.getByTestId('audit-count')).toHaveTextContent('2 shown');
		expect(screen.queryByText('Load more')).not.toBeInTheDocument();

		const secondURL = String(spy.mock.calls[1][0]);
		expect(secondURL).toContain('after=e1');
	});

	it('should never send an offset', async () => {
		const spy = stubFeed(
			feedPage([auditEvent('e1', 'user.disabled')], 2, 'e1'),
			feedPage([auditEvent('e2', 'auth.login')], 2)
		);

		render(Page);
		const user = userEvent.setup();
		await user.click(await screen.findByText('Load more'));
		await waitFor(() => expect(spy).toHaveBeenCalledTimes(2));

		expect(
			spy.mock.calls.map((call) => String(call[0])).some((url) => url.includes('offset'))
		).toBe(false);
	});

	it('should count only the rows it actually rendered', async () => {
		stubFeed(
			feedPage(
				[
					auditEvent('e1', 'user.disabled'),
					// Dropped from the display, so counting it would leave
					// "Showing 2" above a single row.
					auditEvent('e2', 'admin.user_viewed')
				],
				2
			)
		);

		render(Page);

		await screen.findByTestId('audit-timeline');
		expect(screen.getAllByTestId('audit-event')).toHaveLength(1);
		expect(screen.getByTestId('audit-count')).toHaveTextContent('1 shown');
		// Everything is loaded; the hidden row is not "missing".
		expect(screen.getByTestId('audit-count')).not.toHaveTextContent('not loaded');
	});

	it('should surface a load failure instead of an empty feed', async () => {
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

		expect(await screen.findByTestId('audit-error')).toHaveTextContent('not authorized as auditor');
	});

	// "No audit events recorded" under a failed load read as "nothing
	// happened" during exactly the kind of incident the page is for.
	it('should not say there are no events when the first load failed', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('offline')))
		);
		render(Page);
		await screen.findByTestId('audit-error');
		expect(screen.queryByText('No audit events recorded.')).not.toBeInTheDocument();
	});

	it('should keep the loaded events when an older page fails', async () => {
		let calls = 0;
		const first = feedPage([auditEvent('e1', 'user.disabled')], 2, 'e1');
		vi.stubGlobal(
			'fetch',
			vi.fn(() => {
				calls += 1;
				if (calls > 1) return Promise.reject(new Error('offline'));
				return Promise.resolve(
					new Response(JSON.stringify({ data: first, error: null }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			})
		);
		render(Page);
		const user = userEvent.setup();
		await user.click(await screen.findByTestId('audit-load-more'));
		await screen.findByTestId('audit-more-error');
		expect(screen.getAllByTestId('audit-event')).toHaveLength(1);
	});

	// Where keyboard focus lands once the pressed control is gone: the count
	// line after the last page, Try again after a failure. See
	// LoadMoreFooter, which owns the rule for every Load more list.
	describe('keyboard focus after Load more', () => {
		const first = feedPage([auditEvent('e1', 'user.disabled')], 2, 'e1');
		const last = feedPage([auditEvent('e2', 'user.enabled')], 2);

		/** stubPages answers each fetch in turn; 'offline' is a failed request. */
		function stubPages(...pages: (object | 'offline')[]) {
			let call = 0;
			vi.stubGlobal(
				'fetch',
				vi.fn(() => {
					const page = pages[Math.min(call++, pages.length - 1)];
					if (page === 'offline') return Promise.reject(new Error('offline'));
					return Promise.resolve(
						new Response(JSON.stringify({ data: page, error: null }), {
							status: 200,
							headers: { 'Content-Type': 'application/json' }
						})
					);
				})
			);
		}

		const cases: { name: string; pages: (object | 'offline')[]; retry: boolean; lands: string }[] =
			[
				{
					name: 'should move focus to the count line when the last page loads',
					pages: [first, last],
					retry: false,
					lands: 'audit-count'
				},
				{
					name: 'should move focus to Try again when Load more fails',
					pages: [first, 'offline'],
					retry: false,
					lands: 'audit-more-error-retry'
				},
				{
					name: 'should move focus to the count line when a retry loads the last page',
					pages: [first, 'offline', last],
					retry: true,
					lands: 'audit-count'
				}
			];

		for (const { name, pages, retry, lands } of cases) {
			it(name, async () => {
				stubPages(...pages);
				render(Page);
				const user = userEvent.setup();
				await user.click(await screen.findByTestId('audit-load-more'));
				if (retry) {
					await user.click(await screen.findByTestId('audit-more-error-retry'));
				}
				await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId(lands)));
			});
		}
	});
});
