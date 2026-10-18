import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSGroupDetail, NSSGroupMembersResponse } from '#lib/api/types.js';
import { session } from '#lib/session.svelte.js';
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
// would own it as a private group.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

import { fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

const twoMembers: NSSGroupMembersResponse = {
	members: [{ uid: 10001, name: 'bob', state: 'active' }, { uid: 10077 }],
	total: 2
};

/** open renders the page for gid with the group and members reads routed. */
async function open(
	group: NSSGroupDetail | Response,
	members: Response | Route = envelope(twoMembers),
	gid = '5100'
) {
	resetFakePage(`http://localhost/admin/nss/groups/${gid}`);
	fakePage.params = { gid };
	const calls = routeFetch({
		[`GET /api/admin/nss/groups/${gid}`]: group instanceof Response ? group : envelope(group),
		[`GET /api/admin/nss/groups/${gid}/members`]: members
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
