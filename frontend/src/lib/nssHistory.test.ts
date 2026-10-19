import { describe, expect, it } from 'vitest';

import type { AuditEvent } from '#lib/api/types.js';

import { accountHistoryRows, groupHistoryRows, type HistoryRow } from './nssHistory';

// The line builders on their own, for the detail shapes the page tests do
// not reach: malformed lists, hidden actions, half-filled collisions and
// pending changes, and which uids an account's page matches on. Wire
// values are literals on purpose, to pin what the server sends.

/** ev is one audit event with the given detail. */
function ev(id: string, action: string, detail?: unknown): AuditEvent {
	return { id, action, created_at: '2026-10-18T09:00:00Z', system: true, detail } as AuditEvent;
}

describe('accepted id change rows', () => {
	it.each([
		{
			name: 'should name the admin when the accept event has an actor',
			event: {
				...ev('e1', 'nss.id_change_accepted', { old_uid: 9001, new_uid: 10001 }),
				system: false,
				actor: { username: 'mnestor' }
			} as AuditEvent,
			want: ['uid changed from 9001 to 10001, accepted by mnestor']
		},
		{
			name: 'should name the admin when no number moved',
			event: {
				...ev('e1', 'nss.id_change_accepted', { old_uid: 9001, new_uid: 9001 }),
				system: false,
				actor: { username: 'mnestor' }
			} as AuditEvent,
			want: ['Accepted an id change, accepted by mnestor']
		},
		{
			name: 'should leave the admin out when the actor has no username',
			event: {
				...ev('e1', 'nss.id_change_accepted', { old_uid: 9001, new_uid: 10001 }),
				system: false,
				actor: {}
			} as AuditEvent,
			want: ['uid changed from 9001 to 10001']
		}
	])('$name', ({ event, want }) => {
		expect(words(accountHistoryRows([event], 10001))).toEqual(want);
	});

	it('should set the admin name in mono when it names one', () => {
		const event = {
			...ev('e1', 'nss.id_change_accepted', { old_gid: 5000, new_gid: 5100 }),
			system: false,
			actor: { username: 'mnestor' }
		} as AuditEvent;
		expect(groupHistoryRows([event])[0].parts.at(-1)).toEqual({ text: 'mnestor', mono: true });
	});
});

/** words is each row's text, runs joined. */
function words(rows: HistoryRow[]): string[] {
	return rows.map((row) => row.parts.map((part) => part.text).join(''));
}

describe('groupHistoryRows', () => {
	it.each([
		{
			name: 'should skip list items that are not members when the lists are malformed',
			events: [
				ev('e1', 'nss.group_members_changed', {
					added: 'not a list',
					removed: [null, 'x', { name: 'nouid' }, { uid: 7, name: 'ok' }],
					removed_count: 4
				})
			],
			want: ['ok removed', 'and 3 more removed']
		},
		{
			name: 'should draw no line when the change only added members',
			events: [
				ev('e1', 'nss.group_members_changed', {
					added: [{ uid: 7, name: 'ok' }],
					removed: [],
					added_count: 1,
					removed_count: 0
				})
			],
			want: []
		},
		{
			name: 'should draw no line when the change only added past a capped list',
			events: [ev('e1', 'nss.group_members_changed', { added: [], removed: [], added_count: 300 })],
			want: []
		},
		{
			name: 'should keep only the removals when the change added and removed members',
			events: [
				ev('e1', 'nss.group_members_changed', {
					added: [{ uid: 7, name: 'ok' }],
					removed: [{ uid: 8, name: 'gone' }],
					added_count: 1,
					removed_count: 1
				})
			],
			want: ['gone removed']
		},
		{
			name: 'should say Members changed when the change lists nothing at all',
			events: [ev('e1', 'nss.group_members_changed', {})],
			want: ['Members changed']
		},
		{
			name: 'should drop the hidden audit actions when they are in the history',
			events: [ev('e1', 'admin.audit_viewed', {}), ev('e2', 'nss.group_missing', {})],
			want: ['Missing from the directory']
		},
		{
			name: 'should fall back to the DNs when a collision names no entries',
			events: [
				ev('e1', 'nss.gid_collision', {
					kind: 'gid',
					value: '5100',
					holder_dn: 'cn=ops,dc=x',
					other_dn: 'cn=ops2,dc=x'
				})
			],
			want: ['gid 5100 claimed by both cn=ops,dc=x and cn=ops2,dc=x']
		},
		{
			name: 'should say two entries when a collision has only one side',
			events: [ev('e1', 'nss.gid_collision', { kind: 'gid', value: '5100', holder_name: 'ops' })],
			want: ['gid 5100 claimed by two entries']
		},
		{
			name: 'should say a collision alone when it has no value',
			events: [ev('e1', 'nss.name_collision', { kind: 'group_name' })],
			want: ['Group name collision']
		},
		{
			name: 'should say an id change waits when its numbers are missing',
			events: [ev('e1', 'nss.id_change_pending', {})],
			want: ['Id change waiting to be accepted']
		},
		{
			name: 'should say an id change was accepted when no number moved',
			events: [ev('e1', 'nss.id_change_accepted', { old_gid: 5100, new_gid: 5100 })],
			want: ['Accepted an id change']
		},
		{
			name: 'should say Created when the member count is missing',
			events: [ev('e1', 'nss.group_created', undefined)],
			want: ['Created']
		},
		{
			name: 'should give a plain label when the event has no action',
			events: [ev('e1', '', {})],
			want: ['Recorded an event']
		}
	])('$name', ({ events, want }) => {
		expect(words(groupHistoryRows(events))).toEqual(want);
	});

	it('should key every line apart when one event draws several', () => {
		const rows = groupHistoryRows([
			ev('e1', 'nss.group_members_changed', {
				added: [],
				removed: [
					{ uid: 1, name: 'a' },
					{ uid: 2, name: 'b' }
				]
			})
		]);

		expect(rows.map((row) => row.key)).toEqual(['e1:0', 'e1:1']);
	});

	it('should set names in the mono when a line names a member', () => {
		const rows = groupHistoryRows([
			ev('e1', 'nss.group_members_changed', { added: [], removed: [{ uid: 1, name: 'a' }] })
		]);

		expect(rows[0].parts).toEqual([{ text: 'a', mono: true }, { text: ' removed' }]);
	});
});

describe('accountHistoryRows', () => {
	it.each([
		{
			name: 'should not match a uid a collision names when it is another account',
			events: [
				ev('e2', 'nss.uid_collision', { kind: 'uid', value: '9001', other_number: 9001 }),
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [{ uid: 9001, name: 'eve' }],
					removed: []
				})
			],
			want: ['uid 9001 claimed by two entries']
		},
		{
			name: 'should match the uid its own lifecycle event names when the stored uid moved since',
			events: [
				ev('e2', 'nss.account_created', { uid: 9001 }),
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [],
					removed: [{ uid: 9001, name: 'bob' }]
				})
			],
			want: ['Created', 'Removed from ops']
		},
		{
			name: 'should say only the removal when one change added and removed the account under different names',
			events: [
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [{ uid: 10001, name: 'bob' }],
					removed: [{ uid: 9001, name: 'robert' }]
				}),
				ev('e0', 'nss.id_change_accepted', { old_uid: 9001, new_uid: 10001 })
			],
			want: ['Removed from ops', 'uid changed from 9001 to 10001']
		},
		{
			name: 'should draw nothing when another account changed and no list was capped',
			events: [
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [{ uid: 10002, name: 'sonia' }],
					removed: [],
					added_count: 1,
					removed_count: 0
				})
			],
			want: []
		},
		{
			name: 'should name the group as a group when the detail has neither name nor gid',
			events: [ev('e1', 'nss.group_members_changed', { added: [], removed: [{ uid: 10001 }] })],
			want: ['Removed from a group']
		},
		{
			name: 'should draw nothing when the account was only added',
			events: [
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [{ uid: 10001, name: 'bob' }],
					removed: []
				})
			],
			want: []
		},
		{
			name: 'should keep the ambiguous line when both capped lists left the account out',
			events: [
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [],
					removed: [],
					added_count: 300,
					removed_count: 300
				})
			],
			want: ['Added to or removed from ops']
		},
		{
			name: 'should say Removed when only the capped removed list left the account out',
			events: [
				ev('e1', 'nss.group_members_changed', {
					name: 'ops',
					added: [],
					removed: [],
					added_count: 0,
					removed_count: 300
				})
			],
			want: ['Removed from ops']
		}
	])('$name', ({ events, want }) => {
		expect(words(accountHistoryRows(events, 10001))).toEqual(want);
	});

	it('should return no lines when the events are not a list', () => {
		expect(accountHistoryRows(null as unknown as AuditEvent[], 10001)).toEqual([]);
	});
});
