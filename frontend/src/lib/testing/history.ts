import { screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { axe, toHaveNoViolations } from 'jest-axe';
import { describe, expect, it } from 'vitest';

import type { AuditEvent, AuditEventsResponse } from '#lib/api/types.js';
import { envelope, type Route } from '#lib/testing/nss.js';

// The History section's cases, shared by the stored account's and the
// stored group's page tests so both pages are held to the same states:
// loading, empty, the lines, a failed read with Try again, and Load more
// with its focus and its failure. The section is also where its axe pass
// lives: it fetches on mount, so the component sweep (a11y.test.ts) leaves
// it to these. What each page's lines say is in that page's own test.

expect.extend(toHaveNoViolations);

/** historyEvent is one audit event as a history page carries it. */
export function historyEvent(id: string, action: string, createdAt: string): AuditEvent {
	return { id, action, created_at: createdAt, system: true, detail: { via: 'full_sync' } };
}

/**
 * nssEvent is one audit event on an NSS row's history with the given
 * detail, recorded by the server.
 */
export function nssEvent(
	id: string,
	action: string,
	detail: Record<string, unknown> = {},
	createdAt = '2026-10-18T09:00:00Z'
): AuditEvent {
	return { id, action, created_at: createdAt, system: true, detail };
}

/** historyLines is the words of each history line on screen, without the time. */
export function historyLines(testid: string): string[] {
	return screen
		.queryAllByTestId(`${testid}-row`)
		.map((row) => row.querySelector('p')?.textContent?.trim() ?? '');
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

// The newer event adds the account with uid 10001 to the group with gid
// 5100, the two rows the pages open, so it draws one line on either page.
const newerPage: AuditEventsResponse = {
	events: [
		nssEvent('ev-new', 'nss.group_members_changed', {
			name: 'ops',
			gid: 5100,
			added: [{ uid: 10001, name: 'bob' }],
			removed: [],
			added_count: 1,
			removed_count: 0
		})
	],
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

		it('should render a line with its time when there is an event', async () => {
			await open(envelope({ events: newerPage.events, total: 1 }));

			expect(screen.getByTestId(`${testid}-row`).querySelector('time')).toHaveAttribute(
				'datetime',
				'2026-10-18T09:00:00Z'
			);
		});

		it('should not render the generic audit timeline when there are events', async () => {
			await open(envelope({ events: newerPage.events, total: 1 }));

			expect(screen.queryByTestId('audit-timeline')).not.toBeInTheDocument();
		});

		it('should have no axe violations when events and Load more are shown', async () => {
			await open(pagedHistory());

			expect(await axe(screen.getByTestId(testid))).toHaveNoViolations();
		});

		it('should count the events shown when the last page is in', async () => {
			await open(envelope({ events: newerPage.events, total: 1 }));

			expect(screen.getByTestId(`${testid}-count`)).toHaveTextContent('1 event shown');
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

			await waitFor(() => expect(screen.getAllByTestId(`${testid}-row`)).toHaveLength(2));
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

			expect(screen.getAllByTestId(`${testid}-row`)).toHaveLength(1);
		});
	});
}
