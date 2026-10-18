import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSAccountDetail } from '#lib/api/types.js';
import { session } from '#lib/session.svelte.js';
import { formatDateTime } from '#lib/format.js';
import { emptyHistory, historyLines, historySuite, nssEvent } from '#lib/testing/history.js';
import {
	accountFixture,
	envelope,
	routeFetch,
	settle,
	userFixture,
	type Route
} from '#lib/testing/nss.js';

// Test methodology: one stored account routed to a fixture, the uid taken
// from the fake page's params. The pinned behaviours are the ones an admin
// acts on: the served verdict is the server's (served, or each not_served
// reason in words), what hosts get is hidden when nothing is served, the
// state section appears only off active, every related entry is a link, and
// a uid with no stored account says a host would still ask the directory.
// The History section's states are the shared historySuite's.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

import { fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

/**
 * open renders the page for uid with the account read answered by
 * `account` and its history by `history` (none recorded by default).
 */
async function open(
	account: NSSAccountDetail | Response,
	uid = '10001',
	history: Route | Response = envelope(emptyHistory)
) {
	resetFakePage(`http://localhost/admin/nss/accounts/${uid}`);
	fakePage.params = { uid };
	const calls = routeFetch({
		[`GET /api/admin/nss/users/${uid}`]: account instanceof Response ? account : envelope(account),
		[`GET /api/admin/nss/users/${uid}/audit`]: history
	});
	render(Page);
	await settle();
	await settle();
	return calls;
}

/** notServed is the account as a host lookup misses it for `reason`. */
function notServed(reason: string, overrides: Partial<NSSAccountDetail> = {}): NSSAccountDetail {
	return accountFixture({ served: undefined, not_served: reason, ...overrides });
}

beforeEach(() => {
	session.user = userFixture({ is_auditor: true });
	session.resolved = true;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

describe('stored account verdict', () => {
	it('should say the account is served when the server sent a served record', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-verdict-word')).toHaveTextContent('Served');
	});

	it('should say a served account is served disabled when its status is disabled', async () => {
		const account = accountFixture();
		await open({ ...account, served: { ...account.served!, status: 'disabled' } });

		expect(screen.getByTestId('nss-account-verdict')).toHaveTextContent(
			'served disabled, with nss.disabled_shell'
		);
	});

	const reasons: { reason: string; account?: Partial<NSSAccountDetail>; says: string }[] = [
		{
			reason: 'withheld',
			account: { withheld_reason: 'uid 10001 is also carried by uid=bob2,ou=People' },
			says: 'Withheld by a collision: uid 10001 is also carried by uid=bob2,ou=People.'
		},
		{ reason: 'withheld', says: 'Withheld by a collision over its uid or name.' },
		{ reason: 'past_retention', says: 'older than nss.tombstone_retention' },
		{
			reason: 'private_group_clash',
			says: `nss.auto_private_groups is "true" and a directory group already holds this account's private group`
		},
		{
			reason: 'no_gid',
			account: { gid: undefined },
			says: 'The entry has no gid attribute, which only nss.auto_private_groups "true" accepts'
		},
		{ reason: 'something_new', says: "The server's reason: something_new." }
	];

	for (const { reason, account, says } of reasons) {
		it(`should say "${says}" when the server's not_served is ${reason}`, async () => {
			await open(notServed(reason, account));

			expect(screen.getByTestId('nss-account-verdict-reason')).toHaveTextContent(says);
		});
	}

	it('should say the account has no gid when the server sent none', async () => {
		await open(accountFixture({ gid: undefined }));

		expect(screen.getByTestId('nss-account-numbers')).toHaveTextContent('uid 10001, no gid');
	});

	it('should explain a stored account without a gid when the server sent none', async () => {
		await open(accountFixture({ gid: undefined }));

		expect(screen.getByTestId('nss-account-no-gid')).toHaveTextContent(
			'None: the entry has no gid attribute, which only nss.auto_private_groups true accepts'
		);
	});

	it('should show the stored gid when the server sent one', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-numbers')).toHaveTextContent(/uid 10001, gid \d+/);
	});

	it('should say the account is not served when the server sent no served record', async () => {
		await open(notServed('withheld'));

		expect(screen.getByTestId('nss-account-verdict-word')).toHaveTextContent('Not served');
	});

	// The verdict is never re-derived: an active state with no served record
	// is still not served.
	it('should follow the server rather than the state when they seem to disagree', async () => {
		await open(notServed('private_group_clash', { state: 'active' }));

		expect(screen.getByTestId('nss-account-verdict-word')).toHaveTextContent('Not served');
	});

	it('should replace what hosts get with nothing when the account is not served', async () => {
		await open(notServed('withheld'));

		expect(screen.getByTestId('nss-account-not-served')).toHaveTextContent('finds no entry');
	});
});

describe('stored account sections', () => {
	it('should list the served groups primary first', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-served-groups')).toHaveTextContent(
			/staff \(5000, primary\)\s*ops \(5100\)/
		);
	});

	it('should link each served group to its page', async () => {
		await open(accountFixture());

		expect(screen.getAllByTestId('nss-account-served-group')[1]).toHaveAttribute(
			'href',
			'/admin/nss/groups/5100'
		);
	});

	it('should link a stored membership to its group', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-membership-link')).toHaveAttribute(
			'href',
			'/admin/nss/groups/5100'
		);
	});

	it.each([
		{
			name: 'should show the added time when the membership was stored after the first import',
			addedAt: '2026-10-17T12:00:00Z',
			want: formatDateTime('2026-10-17T12:00:00Z')
		},
		{
			name: 'should say Before first import when the membership has no added time',
			addedAt: undefined,
			want: 'Before first import'
		}
	])('$name', async ({ addedAt, want }) => {
		await open(
			accountFixture({ groups: [{ gid: 5100, name: 'ops', state: 'active', added_at: addedAt }] })
		);

		expect(screen.getByTestId('nss-account-membership-added')).toHaveTextContent(want);
	});

	it('should head the added column Added when memberships are listed', async () => {
		await open(accountFixture());

		expect(
			within(screen.getByTestId('nss-account-memberships-table')).getByRole('columnheader', {
				name: 'Added'
			})
		).toBeInTheDocument();
	});

	it('should say no stored group has the gid when a membership names none', async () => {
		await open(accountFixture({ groups: [{ gid: 7777 }] }));

		expect(screen.getByTestId('nss-account-membership-unstored')).toBeInTheDocument();
	});

	it('should note that the primary group is listed only when a row names it', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-memberships')).toHaveTextContent(
			'The primary group is listed only when a membership row names it'
		);
	});

	it('should link the linked person to their user page', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-person-link')).toHaveAttribute(
			'href',
			'/admin/users/user-bob'
		);
	});

	it('should mark the linked person disabled when they are', async () => {
		const account = accountFixture();
		await open({
			...account,
			person: { ...account.person!, disabled_at: '2026-10-10T00:00:00Z' }
		});

		expect(screen.getByTestId('nss-account-person-disabled')).toBeInTheDocument();
	});

	it('should say there is no linked person when none is linked', async () => {
		await open(accountFixture({ person: undefined }));

		expect(screen.getByTestId('nss-account-no-person')).toBeInTheDocument();
	});

	it('should explain an empty DN when another entry took it', async () => {
		await open(accountFixture({ dn: '' }));

		expect(screen.getByTestId('nss-account-no-dn')).toHaveTextContent('directory id alone');
	});

	it('should show no state section when the account is active', async () => {
		await open(accountFixture());

		expect(screen.queryByTestId('nss-marks')).toBeNull();
	});
});

describe('stored account state section', () => {
	const marks: { name: string; account: Partial<NSSAccountDetail>; testid: string }[] = [
		{
			name: 'missing',
			account: { state: 'missing', missing_since: '2026-10-16T00:00:00Z' },
			testid: 'nss-marks-missing'
		},
		{
			name: 'a tombstone',
			account: { state: 'tombstone', deleted_at: '2026-10-01T00:00:00Z' },
			testid: 'nss-marks-tombstone'
		},
		{
			name: 'withheld',
			account: {
				state: 'withheld',
				withheld_at: '2026-10-12T00:00:00Z',
				withheld_reason: 'uid clash'
			},
			testid: 'nss-marks-withheld'
		},
		{
			name: 'pending',
			account: { state: 'pending', pending_uid: 20001, pending_since: '2026-10-15T00:00:00Z' },
			testid: 'nss-marks-pending'
		},
		{
			name: 'disabled in the directory',
			account: { state: 'disabled', dir_disabled: true },
			testid: 'nss-marks-dir-disabled'
		}
	];

	for (const { name, account, testid } of marks) {
		it(`should show the mark when the account is ${name}`, async () => {
			await open(accountFixture(account));

			expect(screen.getByTestId(testid)).toBeInTheDocument();
		});
	}

	it('should say the uid moves from the stored to the pending value', async () => {
		await open(accountFixture({ state: 'pending', pending_uid: 20001 }));

		expect(screen.getByTestId('nss-marks-pending')).toHaveTextContent('uid 10001 to 20001');
	});

	it('should link an admin to the Collisions panel when the account is withheld', async () => {
		session.user = userFixture({ is_admin: true, is_soc: true, is_auditor: true });
		await open(accountFixture({ state: 'withheld', withheld_reason: 'uid clash' }));

		expect(screen.getByTestId('nss-marks-collisions-link')).toHaveAttribute(
			'href',
			'/admin/directory#collisions'
		);
	});

	it('should link an admin to the Pending id changes panel when a change is pending', async () => {
		session.user = userFixture({ is_admin: true, is_soc: true, is_auditor: true });
		await open(accountFixture({ state: 'pending', pending_gid: 6000 }));

		expect(screen.getByTestId('nss-marks-pending-ref-link')).toHaveAttribute(
			'href',
			'/admin/directory#pending-id-changes'
		);
	});

	it('should not link an auditor to the admin-only Directory page', async () => {
		await open(accountFixture({ state: 'withheld', withheld_reason: 'uid clash' }));

		expect(within(screen.getByTestId('nss-marks')).queryByRole('link')).toBeNull();
	});
});

describe('stored account not found', () => {
	it('should say no stored account has the uid when the server answers 404', async () => {
		await open(envelope(null, 404, 'no stored account has uid 4242'), '4242');

		expect(screen.getByTestId('nss-account-missing')).toHaveTextContent(
			'No stored account has uid 4242'
		);
	});

	it('should say a host lookup would still ask the directory when none is stored', async () => {
		await open(envelope(null, 404, 'no stored account'), '4242');

		expect(screen.getByTestId('nss-account-missing')).toHaveTextContent(
			'still asks the directory on demand'
		);
	});

	it('should link an admin to the Probe when none is stored', async () => {
		session.user = userFixture({ is_admin: true, is_soc: true, is_auditor: true });
		await open(envelope(null, 404, 'no stored account'), '4242');

		expect(screen.getByTestId('nss-account-missing-probe-link')).toHaveAttribute(
			'href',
			'/admin/directory#probe'
		);
	});

	it('should tell an auditor an admin can run the Probe when none is stored', async () => {
		await open(envelope(null, 404, 'no stored account'), '4242');

		expect(screen.getByTestId('nss-account-missing-probe')).toHaveTextContent(
			'An admin can check what the directory itself says with the Probe'
		);
	});

	it('should not ask the server when the uid is not a number', async () => {
		const calls = await open(accountFixture(), 'bob');

		expect(calls).toHaveLength(0);
	});

	it('should keep the back chip when the account is not found', async () => {
		await open(envelope(null, 404, 'no stored account'), '4242');

		expect(screen.getByTestId('nss-account-back')).toHaveAttribute('href', '/admin/nss/accounts');
	});

	it('should say NSS is off when the 404 names nss.enabled', async () => {
		await open(envelope(null, 404, 'the NSS directory is off (nss.enabled is false)'));

		expect(screen.getByTestId('nss-account-off')).toBeInTheDocument();
	});
});

describe('stored account load', () => {
	it('should offer Try again when the account fails to load', async () => {
		await open(envelope(null, 500, 'database unavailable'));

		expect(screen.getByTestId('nss-account-error-retry')).toBeInTheDocument();
	});

	it('should load the account when Try again is pressed', async () => {
		let fail = true;
		resetFakePage('http://localhost/admin/nss/accounts/10001');
		fakePage.params = { uid: '10001' };
		routeFetch({
			'GET /api/admin/nss/users/10001': () =>
				fail ? envelope(null, 500, 'boom') : envelope(accountFixture())
		});
		render(Page);
		const retry = await screen.findByTestId('nss-account-error-retry');
		fail = false;
		await userEvent.click(retry);

		expect(await screen.findByTestId('nss-account-identity')).toBeInTheDocument();
	});

	it('should return the back chip to the list filters it was opened from', async () => {
		resetFakePage(
			`http://localhost/admin/nss/accounts/10001?back=${encodeURIComponent('?q=bo&state=missing')}`
		);
		fakePage.params = { uid: '10001' };
		routeFetch({ 'GET /api/admin/nss/users/10001': envelope(accountFixture()) });
		render(Page);

		expect(screen.getByTestId('nss-account-back')).toHaveAttribute(
			'href',
			'/admin/nss/accounts?q=bo&state=missing'
		);
	});

	it('should ignore a back value that is not a query string', async () => {
		resetFakePage(
			`http://localhost/admin/nss/accounts/10001?back=${encodeURIComponent('//evil.example')}`
		);
		fakePage.params = { uid: '10001' };
		routeFetch({ 'GET /api/admin/nss/users/10001': envelope(accountFixture()) });
		render(Page);

		expect(screen.getByTestId('nss-account-back')).toHaveAttribute('href', '/admin/nss/accounts');
	});
});

historySuite({
	open: (history) => open(accountFixture(), '10001', history),
	testid: 'nss-account-history',
	path: '/api/admin/nss/users/10001/audit'
});

// The account's history lines. The page is uid 10001 (bob); each case is
// the events the history read answers with and the lines it should draw,
// newest first. A member change lists many members, and only what
// happened to this account becomes a line.
describe('account history lines', () => {
	/** change is an nss.group_members_changed event for the group name. */
	function change(
		id: string,
		name: string,
		added: { uid: number; name?: string }[],
		removed: { uid: number; name?: string }[],
		counts: { added?: number; removed?: number } = {}
	) {
		return nssEvent(id, 'nss.group_members_changed', {
			name,
			gid: 5100,
			added,
			removed,
			added_count: counts.added ?? added.length,
			removed_count: counts.removed ?? removed.length
		});
	}

	/** others is n listed members that are not this account. */
	function others(n: number) {
		return Array.from({ length: n }, (_, i) => ({ uid: 20000 + i, name: `u${i}` }));
	}

	it.each([
		{
			name: 'should draw no line when the account was added',
			events: [change('e1', 'all-users', [{ uid: 10001, name: 'bob' }], [])],
			want: []
		},
		{
			name: 'should draw no line when the account was added and another member removed',
			events: [change('e1', 'ops', [{ uid: 10001, name: 'bob' }], [{ uid: 10003, name: 'mike' }])],
			want: []
		},
		{
			name: 'should say Removed from the group when the account was removed',
			events: [change('e1', 'wheel', [], [{ uid: 10001, name: 'bob' }])],
			want: ['Removed from wheel']
		},
		{
			name: 'should draw only this account when a change lists other members too',
			events: [
				change(
					'e1',
					'ops',
					[{ uid: 10002, name: 'sonia' }],
					[
						{ uid: 10003, name: 'mike' },
						{ uid: 10001, name: 'bob' }
					]
				)
			],
			want: ['Removed from ops']
		},
		{
			name: 'should name the group by its gid when the event has no group name',
			events: [change('e1', '', [], [{ uid: 10001 }])],
			want: ['Removed from gid 5100']
		},
		{
			name: 'should draw no line when the account is past only the capped added list',
			events: [change('e1', 'all-users', others(100), [], { added: 500 })],
			want: []
		},
		{
			name: 'should say Removed when the account is past the capped removed list',
			events: [change('e1', 'all-users', [], others(100), { removed: 500 })],
			want: ['Removed from all-users']
		},
		{
			name: 'should say added or removed when both lists were capped without the account',
			events: [change('e1', 'all-users', others(100), others(100), { added: 300, removed: 300 })],
			want: ['Added to or removed from all-users']
		},
		{
			name: 'should match an earlier uid when an accepted uid change names it',
			events: [
				nssEvent('e2', 'nss.id_change_accepted', {
					old_uid: 9001,
					new_uid: 10001,
					old_gid: 5000,
					new_gid: 5000
				}),
				change('e1', 'wheel', [], [{ uid: 9001, name: 'bob' }])
			],
			want: ['uid changed from 9001 to 10001', 'Removed from wheel']
		},
		{
			name: 'should draw no line for the re-key when an accepted uid change moved the memberships',
			events: [change('e1', 'wheel', [{ uid: 10001, name: 'bob' }], [{ uid: 9001, name: 'bob' }])],
			want: []
		},
		{
			name: 'should say Created when the account was stored',
			events: [nssEvent('e1', 'nss.account_created', { uid: 10001, gid: 5000 })],
			want: ['Created']
		},
		{
			name: 'should name both names when the account was renamed',
			events: [nssEvent('e1', 'nss.account_renamed', { old_name: 'bobby', new_name: 'bob' })],
			want: ['Renamed from bobby to bob']
		},
		{
			name: 'should say Renamed alone when the rename names are missing',
			events: [nssEvent('e1', 'nss.account_renamed', {})],
			want: ['Renamed']
		},
		{
			name: 'should say it went missing when a full sync did not see it',
			events: [nssEvent('e1', 'nss.account_missing', { uid: 10001 })],
			want: ['Missing from the directory']
		},
		{
			name: 'should say Tombstoned when the missing account was tombstoned',
			events: [nssEvent('e1', 'nss.account_tombstoned', { uid: 10001 })],
			want: ['Tombstoned']
		},
		{
			name: 'should say Revived from a tombstone when a tombstone came back',
			events: [nssEvent('e1', 'nss.account_revived', { was_tombstoned: true })],
			want: ['Revived from a tombstone']
		},
		{
			name: 'should say Revived when a missing account came back',
			events: [nssEvent('e1', 'nss.account_revived', { was_tombstoned: false })],
			want: ['Revived']
		},
		{
			name: 'should say Purged when the tombstone was purged',
			events: [nssEvent('e1', 'nss.tombstone_purged', { uid: 10001 })],
			want: ['Purged']
		},
		{
			name: 'should name both numbers when the uid and gid change was accepted',
			events: [
				nssEvent('e1', 'nss.id_change_accepted', {
					old_uid: 9001,
					new_uid: 10001,
					old_gid: 4000,
					new_gid: 5000
				})
			],
			want: ['uid changed from 9001 to 10001 and gid from 4000 to 5000']
		},
		{
			name: 'should say the change waits when a uid change is pending',
			events: [nssEvent('e1', 'nss.id_change_pending', { stored: 10001, pending: 20001 })],
			want: ['uid change from 10001 to 20001 waiting to be accepted']
		},
		{
			name: 'should name both accounts when a uid collision was raised',
			events: [
				nssEvent('e1', 'nss.uid_collision', {
					kind: 'uid',
					value: '10001',
					holder_name: 'bob',
					other_name: 'robert'
				})
			],
			want: ['uid 10001 claimed by both bob and robert']
		},
		{
			name: 'should name both uids when a name collision was raised',
			events: [
				nssEvent('e1', 'nss.name_collision', {
					kind: 'user_name',
					value: 'bob',
					holder_number: 10001,
					other_number: 10009
				})
			],
			want: ['Account name bob claimed by both uid 10001 and uid 10009']
		},
		{
			name: 'should name both names when the POSIX name differs from the username',
			events: [nssEvent('e1', 'nss.name_mismatch', { username: 'bbaker', posix_name: 'bob' })],
			want: ['POSIX name bob differs from the username bbaker']
		},
		{
			name: 'should fall back to the raw action when the action is unknown',
			events: [nssEvent('e1', 'nss.something_new', { nested: { a: 1 } })],
			want: ['nss.something_new']
		},
		{
			name: 'should keep the newest first when several events came',
			events: [
				nssEvent('e3', 'nss.account_revived', {}, '2026-10-18T09:00:00Z'),
				nssEvent('e2', 'nss.account_missing', {}, '2026-10-10T09:00:00Z'),
				nssEvent('e1', 'nss.account_created', {}, '2026-10-01T09:00:00Z')
			],
			want: ['Revived', 'Missing from the directory', 'Created']
		}
	])('$name', async ({ events, want }) => {
		await open(accountFixture(), '10001', envelope({ events, total: events.length }));

		expect(historyLines('nss-account-history')).toEqual(want);
	});

	it('should say none is listed when no loaded event drew a line', async () => {
		const events = [change('e1', 'ops', [{ uid: 10001, name: 'bob' }], [])];
		await open(accountFixture(), '10001', envelope({ events, total: 1 }));

		expect(screen.getByTestId('nss-account-history-none-shown')).toHaveTextContent(
			'None of the events loaded so far is listed here. Additions show in the Added column of Stored memberships above.'
		);
	});

	it('should say additions are not listed when the history describes itself', async () => {
		await open(accountFixture());

		expect(screen.getByTestId('nss-account-history')).toHaveTextContent(
			'Additions are not listed; the Added column of Stored memberships dates each current one.'
		);
	});

	it('should show the time through formatDateTime when a line is drawn', async () => {
		const events = [nssEvent('e1', 'nss.account_created', {}, '2026-10-01T09:00:00Z')];
		await open(accountFixture(), '10001', envelope({ events, total: 1 }));

		expect(screen.getByTestId('nss-account-history-row').querySelector('time')).toHaveTextContent(
			formatDateTime('2026-10-01T09:00:00Z')
		);
	});
});
