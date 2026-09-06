import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import Page from './+page.svelte';

// Test methodology: render the audit feed page against a stubbed fetch.
// The behaviors that matter are the paging contract — "load more" grows
// one continuous list rather than replacing it — and the failure path,
// since an auditor staring at an empty feed must be able to tell "nothing
// happened" from "the feed did not load".

/** feedPage builds one AuditEventsResponse payload. */
function feedPage(events: object[], total: number, nextOffset = 0) {
	return { events, total, next_offset: nextOffset };
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

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('admin audit page', () => {
	it('should render the feed newest batch with its running count', async () => {
		stubFeed(feedPage([auditEvent('e1', 'user.disabled'), auditEvent('e2', 'auth.login')], 2));

		render(Page);

		expect(await screen.findByTestId('audit-timeline')).toBeInTheDocument();
		expect(screen.getAllByTestId('audit-event')).toHaveLength(2);
		expect(screen.getByText('Showing 2 of 2')).toBeInTheDocument();
	});

	it('should not offer to load more on the last page', async () => {
		stubFeed(feedPage([auditEvent('e1', 'auth.login')], 1, 0));

		render(Page);

		await screen.findByTestId('audit-timeline');
		expect(screen.queryByText('Load more')).not.toBeInTheDocument();
	});

	it('should grow one continuous list when loading more', async () => {
		const spy = stubFeed(
			feedPage([auditEvent('e1', 'user.disabled')], 2, 1),
			feedPage([auditEvent('e2', 'auth.login')], 2, 0)
		);

		render(Page);
		const user = userEvent.setup();

		await user.click(await screen.findByText('Load more'));

		await waitFor(() => expect(screen.getAllByTestId('audit-event')).toHaveLength(2));
		expect(screen.getByText('Showing 2 of 2')).toBeInTheDocument();
		expect(screen.queryByText('Load more')).not.toBeInTheDocument();

		const secondURL = String(spy.mock.calls[1][0]);
		expect(secondURL).toContain('offset=1');
	});

	// The reported bug: offset paging over a live table cannot promise the
	// window holds still, and the timeline keys its {#each} by id — so a row
	// arriving twice threw each_key_duplicate and took the page down rather
	// than merely rendering twice.
	it('should not repeat a row that arrives on both pages', async () => {
		stubFeed(
			feedPage([auditEvent('e1', 'user.disabled'), auditEvent('e2', 'auth.login')], 3, 2),
			feedPage([auditEvent('e2', 'auth.login'), auditEvent('e3', 'cert.approved')], 3, 0)
		);

		render(Page);
		const user = userEvent.setup();

		await user.click(await screen.findByText('Load more'));

		await waitFor(() => expect(screen.getAllByTestId('audit-event')).toHaveLength(3));
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
		expect(screen.getByText('Showing 1 of 2')).toBeInTheDocument();
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
});
