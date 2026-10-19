import { describe, expect, it } from 'vitest';

import * as enums from '#lib/api/generated/enums.js';
import type { NSSRotationReportRow } from '#lib/api/types.js';
import {
	KeyRotating,
	can,
	collisionLabel,
	endOfDay,
	entryStateLabel,
	entryStateMeaning,
	entryStates,
	latestExpiryDay,
	renewedExpiry,
	expiryTone,
	formatPercent,
	graceEnd,
	groupRotationRows,
	isAccountCollision,
	keyAlerts,
	keyStatus,
	localDay,
	missRate,
	needsKeyCheck,
	notServedReason,
	nssKeyFile,
	parseEntryNumber,
	parseEntryState,
	showNSSKeys,
	splitConfigKeys
} from './nss';

const now = new Date('2026-10-18T12:00:00Z');

describe('wire values', () => {
	// Literal on purpose: these are the server's spellings, so a rename on
	// either side shows up here as well as in svelte-check.
	const cases: { name: string; got: string; want: string }[] = [
		{ name: 'key status active', got: enums.NSSKeyStatusActive, want: 'active' },
		{ name: 'key status expired', got: enums.NSSKeyStatusExpired, want: 'expired' },
		{ name: 'key status revoked', got: enums.NSSKeyStatusRevoked, want: 'revoked' },
		{ name: 'permission view', got: enums.NSSKeyPermissionView, want: 'view' },
		{ name: 'permission rotate', got: enums.NSSKeyPermissionRotate, want: 'rotate' },
		{ name: 'permission revoke', got: enums.NSSKeyPermissionRevoke, want: 'revoke' },
		{ name: 'permission edit', got: enums.NSSKeyPermissionEdit, want: 'edit' },
		{
			name: 'permission assign_group',
			got: enums.NSSKeyPermissionAssignGroup,
			want: 'assign_group'
		},
		{ name: 'bucket switched', got: enums.NSSRotationBucketSwitched, want: 'switched' },
		{ name: 'bucket not_switched', got: enums.NSSRotationBucketNotSwitched, want: 'not_switched' },
		{ name: 'bucket gone', got: enums.NSSRotationBucketGone, want: 'gone' },
		{ name: 'collision uid', got: enums.NSSCollisionKindUID, want: 'uid' },
		{ name: 'collision user_name', got: enums.NSSCollisionKindUserName, want: 'user_name' },
		{ name: 'collision gid', got: enums.NSSCollisionKindGID, want: 'gid' },
		{ name: 'collision group_name', got: enums.NSSCollisionKindGroupName, want: 'group_name' }
	];
	it.each(cases)('should spell the $name as the server does', ({ got, want }) => {
		expect(got).toBe(want);
	});

	it('should keep rotating apart from every server status', () => {
		expect(KeyRotating).toBe('rotating');
	});
});

describe('keyStatus', () => {
	const cases: {
		name: string;
		status: string;
		previous?: string;
		want: string;
	}[] = [
		{ name: 'an active key never rotated', status: 'active', want: 'active' },
		{
			name: 'an active key whose previous secret is live',
			status: 'active',
			previous: '2026-10-25T12:00:00Z',
			want: 'rotating'
		},
		{
			name: 'an active key whose grace has ended',
			status: 'active',
			previous: '2026-10-01T12:00:00Z',
			want: 'active'
		},
		{
			name: 'a revoked key with a live previous secret',
			status: 'revoked',
			previous: '2026-10-25T12:00:00Z',
			want: 'revoked'
		},
		{ name: 'an expired key', status: 'expired', want: 'expired' },
		{ name: 'an unknown status', status: 'paused', want: 'active' }
	];

	for (const { name, status, previous, want } of cases) {
		it(`should read ${want} when the key is ${name}`, () => {
			// Cast: the cases include a status no server sends today.
			expect(
				keyStatus({ status: status as enums.NSSKeyStatus, previous_expires_at: previous }, now)
			).toBe(want);
		});
	}
});

describe('can', () => {
	it('should allow what the permissions list names', () => {
		expect(can({ permissions: ['view', 'rotate'] }, enums.NSSKeyPermissionRotate)).toBe(true);
	});

	it('should refuse what the permissions list leaves out', () => {
		expect(can({ permissions: ['view'] }, enums.NSSKeyPermissionRevoke)).toBe(false);
	});

	it('should refuse everything when the list is empty', () => {
		expect(can({ permissions: [] }, enums.NSSKeyPermissionView)).toBe(false);
	});
});

describe('expiryTone', () => {
	const cases: { name: string; at: string; want: string }[] = [
		{ name: 'past', at: '2026-10-17T12:00:00Z', want: 'past' },
		{ name: 'exactly now', at: '2026-10-18T12:00:00Z', want: 'past' },
		{ name: 'in 30 days', at: '2026-11-17T12:00:00Z', want: 'soon' },
		{ name: 'in 31 days', at: '2026-11-18T12:00:00Z', want: 'normal' },
		{ name: 'unparseable', at: 'soon', want: 'normal' }
	];

	for (const { name, at, want } of cases) {
		it(`should read ${want} when the expiry is ${name}`, () => {
			expect(expiryTone(at, now)).toBe(want);
		});
	}

	it('should read normal when the key never expires', () => {
		expect(expiryTone(undefined, now)).toBe('normal');
	});
});

describe('endOfDay', () => {
	it("should be the day's last second in the reader's zone", () => {
		expect(endOfDay('2027-03-01').getTime()).toBe(new Date(2027, 2, 1, 23, 59, 59).getTime());
	});
});

describe('latestExpiryDay', () => {
	// Local times, so the cases read the same in every zone.
	const from = new Date(2026, 9, 18, 12, 0);
	const until = (end: Date) => (end.getTime() - from.getTime()) / 1000;
	const cases: { name: string; seconds: number; want: string | null }[] = [
		{ name: 'the limit is a year', seconds: 365 * 86400, want: '2027-10-17' },
		{
			name: 'the limit ends at the last second of a day',
			seconds: until(new Date(2026, 9, 20, 23, 59, 59)),
			want: '2026-10-20'
		},
		{
			name: 'the limit ends a second before that',
			seconds: until(new Date(2026, 9, 20, 23, 59, 58)),
			want: '2026-10-19'
		},
		{ name: 'the limit is an hour', seconds: 3600, want: '2026-10-17' },
		{ name: 'there is no limit', seconds: 0, want: null },
		{ name: 'the limit is negative', seconds: -60, want: null },
		{ name: 'the limit is not a number', seconds: Number.NaN, want: null }
	];
	for (const { name, seconds, want } of cases) {
		it(`should offer ${want ?? 'no bound'} when ${name}`, () => {
			expect(latestExpiryDay(from, seconds)).toBe(want);
		});
	}
});

describe('nssKeyFile', () => {
	it('should put the id on line 1 and the secret on line 2', () => {
		expect(nssKeyFile('nss_k7m4qp2x', 's3cret')).toBe('nss_k7m4qp2x\ns3cret\n');
	});
});

describe('collisions', () => {
	const cases: { kind: string; value: string; label: string; account: boolean }[] = [
		{ kind: 'uid', value: '10001', label: 'uid 10001', account: true },
		{ kind: 'user_name', value: 'alice', label: 'account name alice', account: true },
		{ kind: 'gid', value: '5000', label: 'gid 5000', account: false },
		{ kind: 'group_name', value: 'ops', label: 'group name ops', account: false },
		{ kind: 'shell', value: 'x', label: 'shell x', account: false }
	];

	for (const { kind, value, label, account } of cases) {
		it(`should name a ${kind} collision as "${label}"`, () => {
			expect(collisionLabel(kind, value)).toBe(label);
		});

		it(`should ${account ? '' : 'not '}treat a ${kind} collision as between accounts`, () => {
			expect(isAccountCollision(kind)).toBe(account);
		});
	}
});

describe('showNSSKeys', () => {
	const base = { nss_enabled: true, is_nss_key_admin: false, is_auditor: false };
	const cases: {
		name: string;
		user: typeof base | null;
		hasKeys: boolean;
		show: boolean;
		check: boolean;
	}[] = [
		{ name: 'signed out', user: null, hasKeys: false, show: false, check: false },
		{
			name: 'NSS is off, even for a key admin',
			user: { ...base, nss_enabled: false, is_nss_key_admin: true },
			hasKeys: true,
			show: false,
			check: false
		},
		{
			name: 'a fleet key admin whose groups manage no key',
			user: { ...base, is_nss_key_admin: true },
			hasKeys: false,
			show: false,
			check: true
		},
		{
			name: 'an auditor whose groups manage no key',
			user: { ...base, is_auditor: true },
			hasKeys: false,
			show: false,
			check: true
		},
		{
			name: 'an auditor whose groups manage a key',
			user: { ...base, is_auditor: true },
			hasKeys: true,
			show: true,
			check: true
		},
		{
			name: 'a key group member',
			user: base,
			hasKeys: true,
			show: true,
			check: true
		},
		{
			name: 'a user with no keys',
			user: base,
			hasKeys: false,
			show: false,
			check: true
		}
	];

	for (const { name, user, hasKeys, show, check } of cases) {
		it(`should ${show ? 'show' : 'hide'} the rail entry when the viewer is ${name}`, () => {
			expect(showNSSKeys(user, hasKeys)).toBe(show);
		});

		it(`should ${check ? '' : 'not '}ask for the key list when the viewer is ${name}`, () => {
			expect(needsKeyCheck(user)).toBe(check);
		});
	}
});

describe('missRate', () => {
	it('should be null when there were no lookups', () => {
		expect(missRate(0, 0)).toBeNull();
	});

	it('should divide misses by lookups', () => {
		expect(missRate(200, 50)).toBe(0.25);
	});
});

describe('formatPercent', () => {
	const cases: { share: number | null; want: string }[] = [
		{ share: null, want: '—' },
		{ share: 0, want: '0%' },
		{ share: 0.004, want: '<1%' },
		{ share: 0.256, want: '26%' },
		{ share: 1, want: '100%' }
	];

	for (const { share, want } of cases) {
		it(`should render ${share} as ${want}`, () => {
			expect(formatPercent(share)).toBe(want);
		});
	}
});

describe('groupRotationRows', () => {
	function row(source_ip: string, bucket: string): NSSRotationReportRow {
		return {
			source_ip,
			// Cast: one case sends a bucket no server sends today.
			bucket: bucket as enums.NSSRotationBucket,
			first_seen: '2026-10-01T00:00:00Z',
			last_seen: '2026-10-18T00:00:00Z',
			lookups: 1,
			last_generation: 1
		};
	}

	it('should split rows by bucket in their order when every bucket is present', () => {
		const groups = groupRotationRows([
			row('10.0.0.1', 'not_switched'),
			row('10.0.0.2', 'switched'),
			row('10.0.0.3', 'not_switched'),
			row('10.0.0.4', 'gone')
		]);
		expect({
			not_switched: groups.not_switched.map((r) => r.source_ip),
			switched: groups.switched.map((r) => r.source_ip),
			gone: groups.gone.map((r) => r.source_ip)
		}).toEqual({
			not_switched: ['10.0.0.1', '10.0.0.3'],
			switched: ['10.0.0.2'],
			gone: ['10.0.0.4']
		});
	});

	it('should drop a row whose bucket it does not know', () => {
		const groups = groupRotationRows([row('10.0.0.9', 'lost')]);
		expect(groups.not_switched.length + groups.switched.length + groups.gone.length).toBe(0);
	});
});

describe('localDay', () => {
	it('should pad the month and day when they are single digits', () => {
		expect(localDay(new Date(2027, 2, 5, 15, 0))).toBe('2027-03-05');
	});
});

describe('renewedExpiry', () => {
	const DAY = 86400;
	const at = (days: number) => new Date(now.getTime() + days * DAY * 1000).toISOString();
	const cases: {
		name: string;
		key: { expires_at?: string; rotated_at?: string; created_at: string };
		max: number;
		want: string | null;
	}[] = [
		{
			name: 'its own lifetime is inside the limit',
			key: { created_at: at(-10), expires_at: at(20) },
			max: 365 * DAY,
			want: at(30)
		},
		{
			name: 'it was rotated since it was made',
			key: { created_at: at(-400), rotated_at: at(-10), expires_at: at(20) },
			max: 365 * DAY,
			want: at(30)
		},
		{
			name: 'its own lifetime is past the limit',
			key: { created_at: at(-10), expires_at: at(355) },
			max: 30 * DAY,
			want: at(30)
		},
		{
			name: 'it never expired and there is a limit',
			key: { created_at: at(-10) },
			max: 365 * DAY,
			want: at(365)
		},
		{
			name: 'it never expires and there is no limit',
			key: { created_at: at(-10) },
			max: 0,
			want: null
		},
		{
			name: 'there is no limit',
			key: { created_at: at(-10), expires_at: at(20) },
			max: 0,
			want: at(30)
		},
		{
			name: 'its expiry precedes its start and there is no limit',
			key: { created_at: at(-1), expires_at: at(-2) },
			max: 0,
			want: at(-2)
		}
	];
	for (const { name, key, max, want } of cases) {
		it(`should renew to ${want ?? 'never'} when ${name}`, () => {
			expect(renewedExpiry(key, now, max)?.toISOString() ?? null).toBe(want);
		});
	}
});

describe('graceEnd', () => {
	const cases: { name: string; seconds: number; want: string }[] = [
		{ name: 'the grace is 30 days', seconds: 30 * 86400, want: '2026-11-17T12:00:00.000Z' },
		{ name: 'the grace is zero', seconds: 0, want: '2026-10-18T12:00:00.000Z' },
		{ name: 'the grace is negative', seconds: -60, want: '2026-10-18T12:00:00.000Z' }
	];
	for (const { name, seconds, want } of cases) {
		it(`should end at ${want} when ${name}`, () => {
			expect(graceEnd(now, seconds).toISOString()).toBe(want);
		});
	}
});

describe('keyAlerts', () => {
	const cases: {
		name: string;
		key: { miss_rate_alert_at?: string; key_source_alert_at?: string };
		want: string[];
	}[] = [
		{ name: 'neither alert fired', key: {}, want: [] },
		{
			name: 'only the miss rate alert fired',
			key: { miss_rate_alert_at: '2026-10-17T09:00:00Z' },
			want: ['misses']
		},
		{
			name: 'only the key source alert fired',
			key: { key_source_alert_at: '2026-10-17T09:00:00Z' },
			want: ['sources']
		},
		{
			name: 'both fired, the source alert later',
			key: {
				miss_rate_alert_at: '2026-10-15T09:00:00Z',
				key_source_alert_at: '2026-10-17T09:00:00Z'
			},
			want: ['sources', 'misses']
		}
	];
	for (const { name, key, want } of cases) {
		it(`should list ${want.join(' then ') || 'nothing'} when ${name}`, () => {
			expect(keyAlerts(key).map((alert) => alert.kind)).toEqual(want);
		});
	}
});

describe('entryStates', () => {
	it('should offer all six states for accounts', () => {
		expect(entryStates('account')).toHaveLength(6);
	});

	// The groups list answers 400 for state=disabled.
	it('should leave disabled out for groups', () => {
		expect(entryStates('group')).not.toContain(enums.NSSEntryStateDisabled);
	});
});

describe('parseEntryState', () => {
	const cases: { value: string | null; kind: 'account' | 'group'; want: string }[] = [
		{ value: 'withheld', kind: 'account', want: 'withheld' },
		{ value: 'disabled', kind: 'account', want: 'disabled' },
		{ value: 'disabled', kind: 'group', want: '' },
		{ value: 'Withheld', kind: 'account', want: '' },
		{ value: '', kind: 'group', want: '' },
		{ value: null, kind: 'group', want: '' }
	];

	for (const { value, kind, want } of cases) {
		it(`should read ${JSON.stringify(value)} as ${JSON.stringify(want)} when the list is the ${kind}s`, () => {
			expect(parseEntryState(value, kind)).toBe(want);
		});
	}
});

describe('entryStateLabel', () => {
	it('should capitalise a known state', () => {
		expect(entryStateLabel('tombstone')).toBe('Tombstone');
	});

	it('should pass an unknown state through as sent', () => {
		expect(entryStateLabel('quarantined')).toBe('quarantined');
	});
});

describe('entryStateMeaning', () => {
	it('should say a group tombstone is served with no members', () => {
		expect(entryStateMeaning('tombstone', 'group')).toContain('Served with no members');
	});

	it('should say an account tombstone is served as deleted', () => {
		expect(entryStateMeaning('tombstone', 'account')).toContain('Served as deleted');
	});

	it('should name only the gid for a pending group', () => {
		expect(entryStateMeaning('pending', 'group')).toContain('a different gid.');
	});

	it('should say nothing for the active state', () => {
		expect(entryStateMeaning('active', 'account')).toBe('');
	});
});

describe('notServedReason', () => {
	const cases: { reason: string; withheld?: string; kind: 'account' | 'group'; want: string }[] = [
		{
			reason: 'withheld',
			withheld: 'uid clash with bob2.',
			kind: 'account',
			want: 'Withheld by a collision: uid clash with bob2.'
		},
		{ reason: 'withheld', kind: 'group', want: 'Withheld by a collision over its gid or name.' },
		{
			reason: 'past_retention',
			kind: 'account',
			want: 'A tombstone older than nss.tombstone_retention, waiting for the purge to remove it.'
		},
		{
			reason: 'private_group_clash',
			kind: 'account',
			want: `nss.auto_private_groups is "true" and a directory group already holds this account's private group gid or name.`
		},
		{
			reason: 'no_gid',
			kind: 'account',
			want: `The entry has no gid attribute, which only nss.auto_private_groups "true" accepts; this mode has no primary gid to serve it with.`
		},
		{ reason: '', kind: 'group', want: 'The server gave no reason.' },
		{ reason: 'new_reason', kind: 'group', want: "The server's reason: new_reason." }
	];

	for (const { reason, withheld, kind, want } of cases) {
		it(`should say "${want}" when the reason is ${JSON.stringify(reason)}`, () => {
			expect(notServedReason(reason, kind, withheld)).toBe(want);
		});
	}
});

describe('splitConfigKeys', () => {
	it('should set each config key apart from the prose around it', () => {
		expect(splitConfigKeys('Served until nss.tombstone_retention ends.')).toEqual([
			{ text: 'Served until ', key: false },
			{ text: 'nss.tombstone_retention', key: true },
			{ text: ' ends.', key: false }
		]);
	});

	it('should leave prose with no key whole', () => {
		expect(splitConfigKeys('Nothing to see.')).toEqual([{ text: 'Nothing to see.', key: false }]);
	});

	it('should return nothing for empty prose', () => {
		expect(splitConfigKeys('')).toEqual([]);
	});
});

describe('parseEntryNumber', () => {
	const cases: { raw: string | undefined; want: number | null }[] = [
		{ raw: '10001', want: 10001 },
		{ raw: '0', want: 0 },
		{ raw: '4294967295', want: 4294967295 },
		{ raw: '4294967296', want: null },
		{ raw: '-1', want: null },
		{ raw: '1e3', want: null },
		{ raw: 'bob', want: null },
		{ raw: '', want: null },
		{ raw: undefined, want: null }
	];

	for (const { raw, want } of cases) {
		it(`should read ${JSON.stringify(raw)} as ${want} when it is a route segment`, () => {
			expect(parseEntryNumber(raw)).toBe(want);
		});
	}
});
