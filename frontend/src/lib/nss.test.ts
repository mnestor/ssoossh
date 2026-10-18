import { describe, expect, it } from 'vitest';

import * as enums from '#lib/api/generated/enums.js';
import type { NSSRotationReportRow } from '#lib/api/types.js';
import {
	KeyRotating,
	can,
	collisionLabel,
	expiryTone,
	formatPercent,
	groupRotationRows,
	isAccountCollision,
	keyStatus,
	missRate,
	needsKeyCheck,
	nssKeyFile,
	showNSSKeys
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
			name: 'a fleet key admin',
			user: { ...base, is_nss_key_admin: true },
			hasKeys: false,
			show: true,
			check: false
		},
		{
			name: 'an auditor',
			user: { ...base, is_auditor: true },
			hasKeys: false,
			show: true,
			check: false
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
