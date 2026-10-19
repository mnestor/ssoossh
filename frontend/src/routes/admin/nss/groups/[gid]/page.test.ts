import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSGroupDetail, NSSGroupMembersResponse } from '#lib/api/types.js';
import { formatDateTime } from '#lib/format.js';
import { session } from '#lib/session.svelte.js';
import { emptyHistory, historyLines, historySuite, nssEvent } from '#lib/testing/history.js';
import {
	envelope,
	groupFixture,
	routeFetch,
	settle,
	userFixture,
	type Route
} from '#lib/testing/nss.js';

// Test methodology: one stored group and its members routed to fixtures,
// the gid taken from the fake page's params. Pinned: the verdict is the
// server's, a truncated member list names nss.max_group_members, members
// page with Load more and link to their accounts, a member with no stored
// account says it is never served, a members failure leaves the group
// standing, and a gid with no stored group points at the account that
// would own it as a private group. The History section's states are the
// shared historySuite's.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

import { fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

const twoMembers: NSSGroupMembersResponse = {
	members: [
		{ uid: 10001, name: 'bob', state: 'active', added_at: '2026-10-17T12:00:00Z' },
		{ uid: 10077 }
	],
	total: 2
};

/** open renders the page for gid with the group, members and history reads routed. */
async function open(
	group: NSSGroupDetail | Response,
	members: Response | Route = envelope(twoMembers),
	gid = '5100',
	history: Response | Route = envelope(emptyHistory)
) {
	resetFakePage(`http://localhost/admin/nss/groups/${gid}`);
	fakePage.params = { gid };
	const calls = routeFetch({
		[`GET /api/admin/nss/groups/${gid}`]: group instanceof Response ? group : envelope(group),
		[`GET /api/admin/nss/groups/${gid}/members`]: members,
		[`GET /api/admin/nss/groups/${gid}/audit`]: history
	});
	render(Page);
	await settle();
	await settle();
	return calls;
}

beforeEach(() => {
	session.user = userFixture({ is_auditor: true, is_soc: true });
	session.resolved = true;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

describe('stored group verdict', () => {
	it('should say the group is served when the server sent a served record', async () => {
		await open(groupFixture());

		expect(screen.getByTestId('nss-group-verdict-word')).toHaveTextContent('Served');
	});

	it('should say how many member names a host receives', async () => {
		await open(groupFixture());

		expect(screen.getByTestId('nss-group-served-count')).toHaveTextContent('2');
	});

	it('should name nss.max_group_members when the member list was cut', async () => {
		await open(
			groupFixture({
				member_count: 3000,
				served: { name: 'ops', gid: 5100, member_count: 2000, members_truncated: true }
			})
		);

		expect(screen.getByTestId('nss-group-truncated')).toHaveTextContent('nss.max_group_members');
	});

	it('should not mention the cap when the member list is whole', async () => {
		await open(groupFixture());

		expect(screen.queryByTestId('nss-group-truncated')).toBeNull();
	});

	const reasons: { reason: string; withheld?: string; says: string }[] = [
		{
			reason: 'withheld',
			withheld: 'gid 5100 is also carried by cn=ops2',
			says: 'gid 5100 is also carried by cn=ops2'
		},
		{ reason: 'withheld', says: 'over its gid or name' },
		{ reason: 'past_retention', says: 'nss.tombstone_retention' }
	];

	for (const { reason, withheld, says } of reasons) {
		it(`should say "${says}" when the group's not_served is ${reason}`, async () => {
			await open(
				groupFixture({ served: undefined, not_served: reason, withheld_reason: withheld })
			);

			expect(screen.getByTestId('nss-group-verdict-reason')).toHaveTextContent(says);
		});
	}

	it('should replace what hosts get with nothing when the group is not served', async () => {
		await open(groupFixture({ served: undefined, not_served: 'withheld' }));

		expect(screen.getByTestId('nss-group-not-served')).toHaveTextContent('finds no entry');
	});
});

describe('stored group members', () => {
	it('should link a stored member to its account', async () => {
		await open(groupFixture());

		expect(screen.getByTestId('nss-group-member-link')).toHaveAttribute(
			'href',
			'/admin/nss/accounts/10001'
		);
	});

	it.each([
		{
			name: 'should show the added time when the member was stored after the first import',
			index: 0,
			want: formatDateTime('2026-10-17T12:00:00Z')
		},
		{
			name: 'should say Before first import when the member has no added time',
			index: 1,
			want: 'Before first import'
		}
	])('$name', async ({ index, want }) => {
		await open(groupFixture());

		expect(screen.getAllByTestId('nss-group-member-added')[index]).toHaveTextContent(want);
	});

	it('should say a member with no stored account is never served', async () => {
		await open(groupFixture());

		expect(screen.getByTestId('nss-group-member-unstored')).toHaveTextContent('never served');
	});

	it('should show the uid of a member with no stored account', async () => {
		await open(groupFixture());

		expect(screen.getAllByTestId('nss-group-member')[1]).toHaveTextContent('10077');
	});

	it('should say no rows are stored when the group has no members', async () => {
		await open(groupFixture({ member_count: 0 }), envelope({ members: [], total: 0 }));

		expect(screen.getByTestId('nss-group-members-empty')).toBeInTheDocument();
	});

	it('should append the next page of members when Load more is pressed', async () => {
		await open(groupFixture(), (call) =>
			call.url.includes('after=10001')
				? envelope({ members: [{ uid: 10077 }], total: 2 })
				: envelope({
						members: [{ uid: 10001, name: 'bob', state: 'active' }],
						total: 2,
						next_cursor: '10001'
					})
		);
		await userEvent.click(screen.getByTestId('nss-group-members-load-more'));

		await waitFor(() => expect(screen.getAllByTestId('nss-group-member')).toHaveLength(2));
	});

	it('should keep the loaded members when Load more fails', async () => {
		await open(groupFixture(), (call) =>
			call.url.includes('after=10001')
				? envelope(null, 500, 'boom')
				: envelope({
						members: [{ uid: 10001, name: 'bob', state: 'active' }],
						total: 2,
						next_cursor: '10001'
					})
		);
		await userEvent.click(screen.getByTestId('nss-group-members-load-more'));
		await screen.findByTestId('nss-group-members-more-error');

		expect(screen.getAllByTestId('nss-group-member')).toHaveLength(1);
	});

	it('should keep the group facts when the members fail to load', async () => {
		await open(groupFixture(), envelope(null, 500, 'boom'));

		expect(screen.getByTestId('nss-group-identity')).toBeInTheDocument();
	});

	it('should offer Try again when the members fail to load', async () => {
		await open(groupFixture(), envelope(null, 500, 'boom'));

		expect(screen.getByTestId('nss-group-members-error-retry')).toBeInTheDocument();
	});
});

describe('stored group not found', () => {
	it('should say no stored group has the gid when the server answers 404', async () => {
		await open(envelope(null, 404, 'no stored group'), undefined, '10001');

		expect(screen.getByTestId('nss-group-missing')).toHaveTextContent(
			'No stored group has gid 10001'
		);
	});

	it('should link to the account whose private group the gid may be', async () => {
		await open(envelope(null, 404, 'no stored group'), undefined, '10001');

		expect(screen.getByTestId('nss-group-missing-account')).toHaveAttribute(
			'href',
			'/admin/nss/accounts/10001'
		);
	});

	it('should say a host lookup would still ask the directory when none is stored', async () => {
		await open(envelope(null, 404, 'no stored group'), undefined, '10001');

		expect(screen.getByTestId('nss-group-missing')).toHaveTextContent('still asks the directory');
	});

	it('should not link SOC to the admin-only Probe when none is stored', async () => {
		await open(envelope(null, 404, 'no stored group'), undefined, '10001');

		expect(screen.queryByTestId('nss-group-missing-probe-link')).toBeNull();
	});
});

describe('stored group state section', () => {
	it('should show the pending gid change when one is pending', async () => {
		await open(groupFixture({ state: 'pending', pending_gid: 5200 }));

		expect(screen.getByTestId('nss-marks-pending')).toHaveTextContent('gid 5100 to 5200');
	});

	it('should say a tombstoned group is served with no members', async () => {
		await open(groupFixture({ state: 'tombstone', deleted_at: '2026-10-01T00:00:00Z' }));

		expect(screen.getByTestId('nss-marks-meaning')).toHaveTextContent('Served with no members');
	});
});

historySuite({
	open: (history) => open(groupFixture(), envelope(twoMembers), '5100', history),
	testid: 'nss-group-history',
	path: '/api/admin/nss/groups/5100/audit'
});

// The group's history lines. The page is gid 5100 (ops); each case is the
// events the history read answers with and the lines it should draw,
// newest first: one line per member removed or renumbered, "and N more
// removed" for a capped removed list, no line for an addition, one line
// per lifecycle event.
describe('group history lines', () => {
	/** change is an nss.group_members_changed event for this group. */
	function change(
		id: string,
		added: { uid: number; name?: string }[],
		removed: { uid: number; name?: string }[],
		counts: { added?: number; removed?: number } = {}
	) {
		return nssEvent(id, 'nss.group_members_changed', {
			name: 'ops',
			gid: 5100,
			added,
			removed,
			added_count: counts.added ?? added.length,
			removed_count: counts.removed ?? removed.length
		});
	}

	it.each([
		{
			name: 'should draw no line when the change only added a member',
			events: [change('e1', [{ uid: 10002, name: 'sonia' }], [])],
			want: []
		},
		{
			name: 'should say the member was removed when one was removed',
			events: [change('e1', [], [{ uid: 10003, name: 'mnestor' }])],
			want: ['mnestor removed']
		},
		{
			name: 'should draw only the removal when one change added three and removed one',
			events: [
				change(
					'e1',
					[
						{ uid: 10002, name: 'sonia' },
						{ uid: 10004, name: 'ann' },
						{ uid: 10005, name: 'raj' }
					],
					[{ uid: 10003, name: 'mnestor' }]
				)
			],
			want: ['mnestor removed']
		},
		{
			name: 'should draw no line when the change only added past a capped list',
			events: [
				change(
					'e1',
					[
						{ uid: 10002, name: 'sonia' },
						{ uid: 10004, name: 'ann' }
					],
					[],
					{ added: 414 }
				)
			],
			want: []
		},
		{
			name: 'should add an and N more line when the removed list was capped',
			events: [change('e1', [], [{ uid: 10003, name: 'mnestor' }], { removed: 4 })],
			want: ['mnestor removed', 'and 3 more removed']
		},
		{
			name: 'should name a member by its uid when it had no stored account',
			events: [change('e1', [], [{ uid: 10077 }])],
			want: ['uid 10077 removed']
		},
		{
			name: 'should say a member was renumbered when an accepted uid change re-keyed it',
			events: [change('e1', [{ uid: 10001, name: 'bob' }], [{ uid: 9001, name: 'bob' }])],
			want: ['bob renumbered from uid 9001 to 10001']
		},
		{
			name: 'should say Members changed when the change lists nobody',
			events: [nssEvent('e1', 'nss.group_members_changed', { name: 'ops' })],
			want: ['Members changed']
		},
		{
			name: 'should count the members when the group was stored',
			events: [nssEvent('e1', 'nss.group_created', { gid: 5100, member_count: 3 })],
			want: ['Created with 3 members']
		},
		{
			name: 'should say one member when the group was stored with one',
			events: [nssEvent('e1', 'nss.group_created', { gid: 5100, member_count: 1 })],
			want: ['Created with 1 member']
		},
		{
			name: 'should name both names when the group was renamed',
			events: [nssEvent('e1', 'nss.group_renamed', { old_name: 'operators', new_name: 'ops' })],
			want: ['Renamed from operators to ops']
		},
		{
			name: 'should say the old name was kept when another group holds the new one',
			events: [
				nssEvent('e1', 'nss.group_renamed', {
					old_name: 'ops',
					new_name: 'staff',
					name_blocked: true
				})
			],
			want: ['Renamed to staff in the directory, but kept ops: another group holds that name']
		},
		{
			name: 'should name the new DN when the group moved without a rename',
			events: [
				nssEvent('e1', 'nss.group_renamed', {
					old_name: 'ops',
					new_name: 'ops',
					new_dn: 'cn=ops,ou=Teams,dc=corp,dc=example'
				})
			],
			want: ['Moved in the directory to cn=ops,ou=Teams,dc=corp,dc=example']
		},
		{
			name: 'should say it went missing when a full sync did not see it',
			events: [nssEvent('e1', 'nss.group_missing', { gid: 5100 })],
			want: ['Missing from the directory']
		},
		{
			name: 'should say Tombstoned when the missing group was tombstoned',
			events: [nssEvent('e1', 'nss.group_tombstoned', { gid: 5100 })],
			want: ['Tombstoned']
		},
		{
			name: 'should say Revived when a missing group came back',
			events: [nssEvent('e1', 'nss.group_revived', { was_tombstoned: false })],
			want: ['Revived']
		},
		{
			name: 'should name both gids when the gid change was accepted',
			events: [nssEvent('e1', 'nss.id_change_accepted', { old_gid: 5000, new_gid: 5100 })],
			want: ['gid changed from 5000 to 5100']
		},
		{
			name: 'should say the change waits when a gid change is pending',
			events: [nssEvent('e1', 'nss.id_change_pending', { stored: 5100, pending: 5200 })],
			want: ['gid change from 5100 to 5200 waiting to be accepted']
		},
		{
			name: 'should name both groups when a gid collision was raised',
			events: [
				nssEvent('e1', 'nss.gid_collision', {
					kind: 'gid',
					value: '5100',
					holder_name: 'ops',
					other_name: 'operations'
				})
			],
			want: ['gid 5100 claimed by both ops and operations']
		},
		{
			name: 'should fall back to the raw action when the action is unknown',
			events: [nssEvent('e1', 'nss.something_new', { nested: { a: 1 } })],
			want: ['nss.something_new']
		},
		{
			name: 'should fall back to the audit phrase when the action has no line of its own',
			events: [nssEvent('e1', 'nss.initial_import', { created: 12 })],
			want: ['Imported the directory for the first time']
		},
		{
			name: 'should keep the newest first when a change follows the creation',
			events: [
				change('e2', [], [{ uid: 10002, name: 'sonia' }]),
				nssEvent('e1', 'nss.group_created', { member_count: 2 }, '2026-10-01T09:00:00Z')
			],
			want: ['sonia removed', 'Created with 2 members']
		}
	])('$name', async ({ events, want }) => {
		await open(groupFixture(), envelope(twoMembers), '5100', envelope({ events, total: 1 }));

		expect(historyLines('nss-group-history')).toEqual(want);
	});

	it('should give every line of one change the same time when it is expanded', async () => {
		const events = [
			change(
				'e1',
				[],
				[
					{ uid: 10002, name: 'sonia' },
					{ uid: 10004, name: 'ann' }
				]
			)
		];
		await open(groupFixture(), envelope(twoMembers), '5100', envelope({ events, total: 1 }));

		const times = screen
			.getAllByTestId('nss-group-history-row')
			.map((row) => row.querySelector('time')?.getAttribute('datetime'));
		expect(new Set(times)).toEqual(new Set(['2026-10-18T09:00:00Z']));
	});

	it('should say none is listed when the loaded events only added members', async () => {
		const events = [change('e1', [{ uid: 10002, name: 'sonia' }], [])];
		await open(groupFixture(), envelope(twoMembers), '5100', envelope({ events, total: 1 }));

		expect(screen.getByTestId('nss-group-history-none-shown')).toHaveTextContent(
			'None of the events loaded so far is listed here. Additions show in the Added column of Members above.'
		);
	});

	it('should never render an object as text when the detail is not an object', async () => {
		const events = [{ ...nssEvent('e1', 'nss.group_members_changed'), detail: 'x' as never }];
		await open(groupFixture(), envelope(twoMembers), '5100', envelope({ events, total: 1 }));

		expect(screen.getByTestId('nss-group-history')).not.toHaveTextContent('[object Object]');
	});
});
