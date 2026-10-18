import type { CurrentUser, NSSKey, NSSRotationReportRow } from '#lib/api/types.js';

// The NSS directory's vocabulary as the web UI reads it: what a caller may do
// with a fleet key, the state a key is shown in, and the kinds of collision
// and rotation bucket the server reports.
//
// The wire values below are spelled out because tygo generates no constants
// for them: they are Go string constants in server/service (nss_keys.go) and
// model.NSSCollisionKind, neither of which tygo.yaml exports. One place, so a
// renamed value is fixed once; the tests pin each against the server's
// spelling.

/** What a caller may do with one key (service.NSSKeyPermission*). */
export const NSSKeyPermission = {
	View: 'view',
	Rotate: 'rotate',
	Revoke: 'revoke',
	Edit: 'edit',
	AssignGroup: 'assign_group'
} as const;

export type NSSKeyPermissionName = (typeof NSSKeyPermission)[keyof typeof NSSKeyPermission];

/** The status the server derives for a key (service.NSSKeyStatus*). */
export const NSSKeyWireStatus = {
	Active: 'active',
	Expired: 'expired',
	Revoked: 'revoked'
} as const;

/**
 * KeyDisplayStatus is the status a key is shown in. `rotating` is not a
 * server status: it is an active key whose previous secret is still
 * accepted, which is the window an operator has to watch.
 */
export type KeyDisplayStatus = 'active' | 'rotating' | 'expired' | 'revoked';

/** The rotation report's buckets (NSSRotationReportRowResponse.Bucket). */
export const RotationBucket = {
	Switched: 'switched',
	NotSwitched: 'not_switched',
	Gone: 'gone'
} as const;

export type RotationBucketName = (typeof RotationBucket)[keyof typeof RotationBucket];

/** The contested value of a collision (model.NSSCollisionKind). */
export const NSSCollisionKind = {
	UID: 'uid',
	UserName: 'user_name',
	GID: 'gid',
	GroupName: 'group_name'
} as const;

/** can reports whether the server lets the caller do `permission` to `key`. */
export function can(key: Pick<NSSKey, 'permissions'>, permission: NSSKeyPermissionName): boolean {
	return key.permissions.includes(permission);
}

/** previousLive reports whether the key's previous secret is still accepted at `now`. */
export function previousLive(key: Pick<NSSKey, 'previous_expires_at'>, now: Date): boolean {
	if (!key.previous_expires_at) {
		return false;
	}
	const until = new Date(key.previous_expires_at);
	return !Number.isNaN(until.getTime()) && until.getTime() > now.getTime();
}

/**
 * keyStatus is the status a key is shown in: the server's, with `rotating`
 * for an active key whose previous secret is still live. An unknown server
 * status reads as active rather than vanishing; the badge still names it.
 */
export function keyStatus(
	key: Pick<NSSKey, 'status' | 'previous_expires_at'>,
	now: Date
): KeyDisplayStatus {
	if (key.status === NSSKeyWireStatus.Revoked) return 'revoked';
	if (key.status === NSSKeyWireStatus.Expired) return 'expired';
	return previousLive(key, now) ? 'rotating' : 'active';
}

/** ExpiryTone is how an expiry date is coloured: past, within 30 days, or neither. */
export type ExpiryTone = 'past' | 'soon' | 'normal';

/** SOON_DAYS is how close an expiry has to be before it is flagged. */
const SOON_DAYS = 30;

/** expiryTone flags an expiry that has passed or falls within 30 days of `now`. */
export function expiryTone(expiresAt: string, now: Date): ExpiryTone {
	const at = new Date(expiresAt);
	if (Number.isNaN(at.getTime())) {
		return 'normal';
	}
	const left = at.getTime() - now.getTime();
	if (left <= 0) return 'past';
	return left <= SOON_DAYS * 24 * 60 * 60 * 1000 ? 'soon' : 'normal';
}

/**
 * nssKeyFile is the content of a host's /etc/ssoossh/nss.key: line 1 the
 * key id, line 2 the secret (nssd/config ReadKeyFile).
 */
export function nssKeyFile(id: string, secret: string): string {
	return `${id}\n${secret}\n`;
}

/** isAccountCollision reports whether a collision is between accounts rather than groups. */
export function isAccountCollision(kind: string): boolean {
	return kind === NSSCollisionKind.UID || kind === NSSCollisionKind.UserName;
}

/** collisionNoun names the kind of value two entries contend for: "uid", "group name". */
export function collisionNoun(kind: string): string {
	switch (kind) {
		case NSSCollisionKind.UID:
			return 'uid';
		case NSSCollisionKind.GID:
			return 'gid';
		case NSSCollisionKind.UserName:
			return 'account name';
		case NSSCollisionKind.GroupName:
			return 'group name';
		default:
			return kind;
	}
}

/** collisionLabel names what two entries contend for: "uid 10001", "group name ops". */
export function collisionLabel(kind: string, value: string): string {
	return `${collisionNoun(kind)} ${value}`;
}

/**
 * showNSSKeys decides whether the rail offers NSS keys: only where the
 * server serves them, and then to the fleet key admin role and the admin
 * area's roles (who see every key, auditors read-only), or to anyone whose
 * key list is not empty because a key's group names them.
 */
export function showNSSKeys(
	user: Pick<CurrentUser, 'nss_enabled' | 'is_nss_key_admin' | 'is_auditor'> | null,
	hasKeys: boolean
): boolean {
	if (!user?.nss_enabled) {
		return false;
	}
	return user.is_nss_key_admin || user.is_auditor || hasKeys;
}

/** needsKeyCheck reports whether the rail has to ask the server for keys to decide. */
export function needsKeyCheck(
	user: Pick<CurrentUser, 'nss_enabled' | 'is_nss_key_admin' | 'is_auditor'> | null
): boolean {
	return !!user?.nss_enabled && !user.is_nss_key_admin && !user.is_auditor;
}

/** missRate is misses as a share of lookups, or null with no lookups. */
export function missRate(lookups: number, misses: number): number | null {
	return lookups > 0 ? misses / lookups : null;
}

/** formatPercent renders a share as a whole percent, "<1%" for a small non-zero share. */
export function formatPercent(share: number | null): string {
	if (share === null) return '—';
	if (share > 0 && share < 0.01) return '<1%';
	return `${Math.round(share * 100)}%`;
}

/** RotationGroups is the report's rows split by bucket. */
export interface RotationGroups {
	not_switched: NSSRotationReportRow[];
	switched: NSSRotationReportRow[];
	gone: NSSRotationReportRow[];
}

/** groupRotationRows splits the rows by bucket, keeping their order; an unknown bucket is dropped. */
export function groupRotationRows(rows: NSSRotationReportRow[]): RotationGroups {
	const groups: RotationGroups = { not_switched: [], switched: [], gone: [] };
	for (const row of rows) {
		const bucket = groups[row.bucket as RotationBucketName];
		bucket?.push(row);
	}
	return groups;
}
