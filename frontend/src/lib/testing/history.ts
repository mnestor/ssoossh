import { screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { axe, toHaveNoViolations } from 'jest-axe';
import { describe, expect, it } from 'vitest';

import type { AuditEvent, AuditEventsResponse } from '#lib/api/types.js';
import { envelope, type Route } from '#lib/testing/nss.js';

// The History section's cases, shared by the stored account's and the
// stored group's page tests so both pages are held to the same states:
// loading, empty, the events, a failed read with Try again, and Load more
// with its focus and its failure. The section is also where its axe pass
// lives: it fetches on mount, so the component sweep (a11y.test.ts) leaves
// it to these.

expect.extend(toHaveNoViolations);

/** historyEvent is one audit event as a history page carries it. */
export function historyEvent(id: string, action: string, createdAt: string): AuditEvent {
	return { id, action, created_at: createdAt, system: true, detail: { via: 'full_sync' } };
}

/** emptyHistory is a history with no events. */
export const emptyHistory: AuditEventsResponse = { events: [], total: 0 };

/**
 * pagedHistory answers the first read with one event and a cursor, and the
 * read after that cursor with `second`: the older event, or a failure.
 */
export function pagedHistory(second: Response = envelope(olderPage)): Route {
	return (call) => (call.url.includes('after=ev-new') ? second.clone() : envelope(newerPage));
}

const newerPage: AuditEventsResponse = {
	events: [historyEvent('ev-new', 'nss.group_members_changed', '2026-10-18T09:00:00Z')],
	total: 2,
	next_cursor: 'ev-new'
};

const olderPage: AuditEventsResponse = {
	events: [historyEvent('ev-old', 'nss.account_created', '2026-10-01T09:00:00Z')],
	total: 2
};

/**
 * historySuite registers the History cases for one page. open renders the
 * page with its history read answered by `history`; testid is the
 * section's selector prefix and path the history endpoint.
 */
export function historySuite(options: {
	open: (history: Route | Response) => Promise<{ url: string }[]>;
	testid: string;
	path: string;
}) {
	const { open, testid, path } = options;

	describe('history', () => {
		it('should read the history from the row endpoint when the page opens', async () => {
			const calls = await open(envelope(emptyHistory));

			expect(calls.some((c) => c.url.startsWith(`${path}?`))).toBe(true);
		});

		it('should announce the loading history when the read is still out', async () => {
			await open(() => new Promise<Response>(() => {}));

			// The placeholder itself waits 300ms (LoadingBlock); the live
			// region is there at once.
			expect(screen.getByText('Loading the history…')).toBeInTheDocument();
		});

		it('should say no changes are recorded when the history is empty', async () => {
			await open(envelope(emptyHistory));

			expect(screen.getByTestId(`${testid}-empty`)).toHaveTextContent('No recorded changes.');
		});

		it('should render each event through the audit timeline when there are events', async () => {
			await open(envelope({ events: newerPage.events, total: 1 }));

			expect(screen.getByTestId('audit-event')).toHaveTextContent('nss.group_members_changed');
		});

		it('should have no axe violations when events and Load more are shown', async () => {
			await open(pagedHistory());

			expect(await axe(screen.getByTestId(testid))).toHaveNoViolations();
		});

		it('should count the events shown when the last page is in', async () => {
			await open(envelope({ events: newerPage.events, total: 1 }));

			expect(screen.getByTestId(`${testid}-count`)).toHaveTextContent('1 shown');
		});

		it('should say the history could not load when the read fails', async () => {
			await open(envelope(null, 500, 'database unavailable'));

			expect(screen.getByTestId(`${testid}-error`)).toHaveTextContent('database unavailable');
		});

		it('should read the history again when Try again is pressed', async () => {
			let reads = 0;
			await open(() => {
				reads += 1;
				return reads === 1 ? envelope(null, 500, 'boom') : envelope(emptyHistory);
			});
			await userEvent.click(screen.getByRole('button', { name: 'Try again' }));

			await waitFor(() => expect(screen.getByTestId(`${testid}-empty`)).toBeInTheDocument());
		});

		it('should append the older events when Load more is pressed', async () => {
			await open(pagedHistory());
			await userEvent.click(screen.getByTestId(`${testid}-load-more`));

			await waitFor(() => expect(screen.getAllByTestId('audit-event')).toHaveLength(2));
		});

		it('should move focus to the count line when Load more brings the last page in', async () => {
			await open(pagedHistory());
			await userEvent.click(screen.getByTestId(`${testid}-load-more`));

			await waitFor(() => expect(screen.getByTestId(`${testid}-count`)).toHaveFocus());
		});

		it('should keep the loaded events when Load more fails', async () => {
			await open(pagedHistory(envelope(null, 500, 'boom')));
			await userEvent.click(screen.getByTestId(`${testid}-load-more`));
			await screen.findByTestId(`${testid}-more-error`);

			expect(screen.getAllByTestId('audit-event')).toHaveLength(1);
		});
	});
}
