import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSAccountsResponse, NSSGroupsResponse } from '#lib/api/types.js';
import type { EntryKind } from '#lib/nss.js';
import { session } from '#lib/session.svelte.js';
import {
	accountSummary,
	envelope,
	groupSummary,
	routeFetch,
	settle,
	userFixture,
	type Call
} from '#lib/testing/nss.js';

// Test methodology: the stored entry list for both kinds, its reads routed
// to fixtures and the address driven by the fake page. What is pinned is
// what an admin mid-incident relies on: the search and state go to the
// server and into the address, every row opens its entry by number, each
// empty state says why and what next, NSS off is said rather than fetched,
// and a failed Load more keeps the rows already shown.

const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakeAfterNavigate } = await import('#lib/testing/page.svelte.js');
	return { goto, afterNavigate: fakeAfterNavigate };
});

import { fakeGoto, resetFakePage } from '#lib/testing/page.svelte.js';
import NSSEntryList from './NSSEntryList.svelte';

const shallowReplace = expect.objectContaining({ shallow: true, replace: true });

/** The per-kind facts every table-driven case below reads. */
const kinds: {
	kind: EntryKind;
	path: string;
	list: string;
	page: (names: string[], next?: string, total?: number, offset?: number) => Response;
	firstHref: string;
}[] = [
	{
		kind: 'account',
		path: '/api/admin/nss/users',
		list: '/admin/nss/accounts',
		page: (names, next, total, offset = 0) => {
			const body: NSSAccountsResponse = {
				accounts: names.map((name, index) => accountSummary(name, 10001 + offset + index)),
				total: total ?? names.length,
				next_cursor: next
			};
			return envelope(body);
		},
		firstHref: '/admin/nss/accounts/10001'
	},
	{
		kind: 'group',
		path: '/api/admin/nss/groups',
		list: '/admin/nss/groups',
		page: (names, next, total, offset = 0) => {
			const body: NSSGroupsResponse = {
				groups: names.map((name, index) => groupSummary(name, 5000 + offset + index)),
				total: total ?? names.length,
				next_cursor: next
			};
			return envelope(body);
		},
		firstHref: '/admin/nss/groups/5000'
	}
];

/** requested is the query of every list read the page made. */
function requested(calls: Call[], path: string): URLSearchParams[] {
	return calls
		.filter((call) => call.url.split('?')[0] === path)
		.map((call) => new URLSearchParams(call.url.split('?')[1] ?? ''));
}

beforeEach(() => {
	goto.mockReset();
	goto.mockImplementation(fakeGoto);
	session.user = userFixture({ is_auditor: true });
	session.resolved = true;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

for (const { kind, path, list, page, firstHref } of kinds) {
	describe(`stored ${kind} list`, () => {
		beforeEach(() => resetFakePage(`http://localhost${list}`));

		it(`should open each ${kind} by its number when a row is shown`, async () => {
			routeFetch({ [`GET ${path}`]: page(['alpha', 'beta']) });
			render(NSSEntryList, { kind });

			expect((await screen.findAllByTestId('nss-list-link'))[0]).toHaveAttribute('href', firstHref);
		});

		it('should show the state of each row as a badge when rows load', async () => {
			routeFetch({ [`GET ${path}`]: page(['alpha']) });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-row')).toHaveTextContent('Active');
		});

		it('should send the search the address names when the page opens', async () => {
			resetFakePage(`http://localhost${list}?q=bob`);
			const calls = routeFetch({ [`GET ${path}`]: page(['bob']) });
			render(NSSEntryList, { kind });
			await settle();

			expect(requested(calls, path)[0].get('q')).toBe('bob');
		});

		it('should send the state the address names when the page opens', async () => {
			resetFakePage(`http://localhost${list}?state=withheld`);
			const calls = routeFetch({ [`GET ${path}`]: page(['bob']) });
			render(NSSEntryList, { kind });
			await settle();

			expect(requested(calls, path)[0].get('state')).toBe('withheld');
		});

		it('should drop a state the address names that does not exist', async () => {
			resetFakePage(`http://localhost${list}?state=bogus`);
			const calls = routeFetch({ [`GET ${path}`]: page(['bob']) });
			render(NSSEntryList, { kind });
			await settle();

			expect(requested(calls, path)[0].has('state')).toBe(false);
		});

		it('should write a chosen state back to the address', async () => {
			routeFetch({ [`GET ${path}`]: page(['bob']) });
			render(NSSEntryList, { kind });
			await screen.findByTestId('nss-list-table');
			await userEvent.selectOptions(screen.getByTestId('nss-state-filter'), 'tombstone');

			await waitFor(() =>
				expect(goto).toHaveBeenCalledWith(`${list}?state=tombstone`, shallowReplace)
			);
		});

		it('should write a typed search back to the address', async () => {
			routeFetch({ [`GET ${path}`]: page(['bob']) });
			render(NSSEntryList, { kind });
			await screen.findByTestId('nss-list-table');
			await userEvent.type(screen.getByTestId('nss-search'), 'bob{Enter}');

			await waitFor(() => expect(goto).toHaveBeenCalledWith(`${list}?q=bob`, shallowReplace));
		});

		it('should carry the filters to the row for its back chip', async () => {
			resetFakePage(`http://localhost${list}?q=al`);
			routeFetch({ [`GET ${path}`]: page(['alpha']) });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-link')).toHaveAttribute(
				'href',
				`${firstHref}?back=${encodeURIComponent('?q=al')}`
			);
		});

		it('should say nothing is stored yet when the list is empty and unfiltered', async () => {
			routeFetch({ [`GET ${path}`]: page([]) });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-empty')).toHaveTextContent(
				`nss.${kind === 'account' ? 'user' : 'group'}_filter selects on its next pass`
			);
		});

		it('should offer Clear filters when the filters match nothing', async () => {
			resetFakePage(`http://localhost${list}?q=nobody`);
			routeFetch({ [`GET ${path}`]: page([]) });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-clear-filters')).toBeInTheDocument();
		});

		it('should say a host lookup still asks the directory when a search matches nothing', async () => {
			resetFakePage(`http://localhost${list}?q=nobody`);
			routeFetch({ [`GET ${path}`]: page([]) });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-empty-lookup')).toHaveTextContent(
				'still asks the directory'
			);
		});

		it('should read the unfiltered list again when Clear filters is pressed', async () => {
			resetFakePage(`http://localhost${list}?q=nobody&state=missing`);
			const calls = routeFetch({ [`GET ${path}`]: page([]) });
			render(NSSEntryList, { kind });
			await userEvent.click(await screen.findByTestId('nss-clear-filters'));
			await settle();

			expect(requested(calls, path).at(-1)?.toString()).toBe('limit=50');
		});

		it('should offer Try again when the list fails to load', async () => {
			routeFetch({ [`GET ${path}`]: envelope(null, 500, 'database unavailable') });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-error-retry')).toBeInTheDocument();
		});

		it('should load the list when Try again is pressed after a failure', async () => {
			let fail = true;
			routeFetch({
				[`GET ${path}`]: () => (fail ? envelope(null, 500, 'boom') : page(['alpha']))
			});
			render(NSSEntryList, { kind });
			const retry = await screen.findByTestId('nss-list-error-retry');
			fail = false;
			await userEvent.click(retry);

			expect(await screen.findByTestId('nss-list-table')).toBeInTheDocument();
		});

		it('should say NSS is off without asking the server when the session says so', async () => {
			session.user = userFixture({ is_auditor: true, nss_enabled: false });
			const calls = routeFetch({});
			render(NSSEntryList, { kind });
			await settle();

			expect(calls).toHaveLength(0);
		});

		it('should name nss.enabled when NSS is off', async () => {
			session.user = userFixture({ is_auditor: true, nss_enabled: false });
			render(NSSEntryList, { kind });

			expect(screen.getByTestId('nss-list-off')).toHaveTextContent('nss.enabled is false');
		});

		it('should say NSS is off when the server answers 404', async () => {
			routeFetch({ [`GET ${path}`]: envelope(null, 404, 'nss.enabled is false') });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-off')).toBeInTheDocument();
		});

		it('should append the next page when Load more is pressed', async () => {
			routeFetch({
				[`GET ${path}`]: (call) =>
					call.url.includes('after=c1')
						? page(['gamma'], undefined, 3, 2)
						: page(['alpha', 'beta'], 'c1', 3)
			});
			render(NSSEntryList, { kind });
			await userEvent.click(await screen.findByTestId('nss-list-load-more'));

			await waitFor(() => expect(screen.getAllByTestId('nss-list-row')).toHaveLength(3));
		});

		it('should keep the loaded rows when Load more fails', async () => {
			routeFetch({
				[`GET ${path}`]: (call) =>
					call.url.includes('after=c1')
						? envelope(null, 500, 'boom')
						: page(['alpha', 'beta'], 'c1', 3)
			});
			render(NSSEntryList, { kind });
			await userEvent.click(await screen.findByTestId('nss-list-load-more'));
			await screen.findByTestId('nss-list-more-error');

			expect(screen.getAllByTestId('nss-list-row')).toHaveLength(2);
		});

		it('should count what is shown and what is left when more remains', async () => {
			routeFetch({ [`GET ${path}`]: page(['alpha', 'beta'], 'c1', 5) });
			render(NSSEntryList, { kind });

			expect(await screen.findByTestId('nss-list-count')).toHaveTextContent(
				'2 shown, 3 more not loaded'
			);
		});
	});
}

describe('stored account list columns', () => {
	beforeEach(() => resetFakePage('http://localhost/admin/nss/accounts'));

	it('should show the gecos when the account has one', async () => {
		routeFetch({
			'GET /api/admin/nss/users': envelope({ accounts: [accountSummary('bob', 10001)], total: 1 })
		});
		render(NSSEntryList, { kind: 'account' });

		expect(await screen.findByTestId('nss-list-row')).toHaveTextContent('bob user');
	});

	// The groups list answers 400 for state=disabled: groups have no such state.
	it('should drop a disabled state from a groups address', async () => {
		resetFakePage('http://localhost/admin/nss/groups?state=disabled');
		const calls = routeFetch({ 'GET /api/admin/nss/groups': envelope({ groups: [], total: 0 }) });
		render(NSSEntryList, { kind: 'group' });
		await settle();

		expect(requested(calls, '/api/admin/nss/groups')[0].has('state')).toBe(false);
	});

	it('should not offer the disabled state for groups', async () => {
		resetFakePage('http://localhost/admin/nss/groups');
		routeFetch({ 'GET /api/admin/nss/groups': envelope({ groups: [], total: 0 }) });
		render(NSSEntryList, { kind: 'group' });

		expect(
			within(screen.getByTestId('nss-state-filter')).queryByRole('option', { name: 'Disabled' })
		).toBeNull();
	});

	it('should show a member count for each group', async () => {
		resetFakePage('http://localhost/admin/nss/groups');
		routeFetch({
			'GET /api/admin/nss/groups': envelope({
				groups: [groupSummary('ops', 5100, { member_count: 42 })],
				total: 1
			})
		});
		render(NSSEntryList, { kind: 'group' });

		expect(await screen.findByTestId('nss-list-row')).toHaveTextContent('42');
	});
});
