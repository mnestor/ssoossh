import {
	NSSCollisionKindGID,
	NSSCollisionKindGroupName,
	NSSCollisionKindUID,
	NSSCollisionKindUserName,
	NSSKeyStatusActive,
	NSSKeyStatusExpired,
	NSSKeyStatusRevoked,
	NSSRotationBucketGone,
	NSSRotationBucketNotSwitched,
	NSSRotationBucketSwitched,
	type NSSKeyPermission,
	type NSSKeyStatus,
	type NSSRotationBucket
} from '#lib/api/generated/enums.js';
import type { CurrentUser, NSSKey, NSSRotationReportRow } from '#lib/api/types.js';

// The NSS directory's vocabulary as the web UI reads it: what a caller may do
// with a fleet key, the state a key is shown in, and the kinds of collision
// and rotation bucket the server reports.
//
// The wire values themselves come from the generated constants
// (server/model/enums.go through tygo), so a value renamed on the server
// fails svelte-check here instead of silently matching nothing.

/**
 * KeyRotating is the status an active key is shown in while its previous
 * secret is still accepted. Not a server status: the browser derives it,
 * because it is the window an operator has to watch.
 */
export const KeyRotating = 'rotating';

/** KeyDisplayStatus is the status a key is shown in: the server's, or rotating. */
export type KeyDisplayStatus = NSSKeyStatus | typeof KeyRotating;

/** can reports whether the server lets the caller do `permission` to `key`. */
export function can(key: Pick<NSSKey, 'permissions'>, permission: NSSKeyPermission): boolean {
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
	if (key.status === NSSKeyStatusRevoked) return NSSKeyStatusRevoked;
	if (key.status === NSSKeyStatusExpired) return NSSKeyStatusExpired;
	return previousLive(key, now) ? KeyRotating : NSSKeyStatusActive;
}

/** ExpiryTone is how an expiry date is coloured: past, within 30 days, or neither. */
export type ExpiryTone = 'past' | 'soon' | 'normal';

/** SOON_DAYS is how close an expiry has to be before it is flagged. */
const SOON_DAYS = 30;

/**
 * expiryTone flags an expiry that has passed or falls within 30 days of
 * `now`. A key with no expiry never expires, so it is never flagged.
 */
export function expiryTone(expiresAt: string | undefined, now: Date): ExpiryTone {
	if (!expiresAt) return 'normal';
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
	return kind === NSSCollisionKindUID || kind === NSSCollisionKindUserName;
}

/** collisionNoun names the kind of value two entries contend for: "uid", "group name". */
export function collisionNoun(kind: string): string {
	switch (kind) {
		case NSSCollisionKindUID:
			return 'uid';
		case NSSCollisionKindGID:
			return 'gid';
		case NSSCollisionKindUserName:
			return 'account name';
		case NSSCollisionKindGroupName:
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
export type RotationGroups = Record<NSSRotationBucket, NSSRotationReportRow[]>;

/** groupRotationRows splits the rows by bucket, keeping their order; an unknown bucket is dropped. */
export function groupRotationRows(rows: NSSRotationReportRow[]): RotationGroups {
	const groups: RotationGroups = {
		[NSSRotationBucketNotSwitched]: [],
		[NSSRotationBucketSwitched]: [],
		[NSSRotationBucketGone]: []
	};
	for (const row of rows) {
		// The type says one of the three, but a newer server may send a
		// fourth; it is dropped rather than thrown on.
		const bucket: NSSRotationReportRow[] | undefined = groups[row.bucket];
		bucket?.push(row);
	}
	return groups;
}

/** localDay is a moment as a date field's YYYY-MM-DD, in the reader's zone. */
export function localDay(at: Date): string {
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}`;
}

/**
 * endOfDay is the last second of a date field's day in the reader's zone:
 * a key "expiring on the 3rd" should still work on the 3rd.
 */
export function endOfDay(day: string): Date {
	return new Date(`${day}T23:59:59`);
}

/**
 * latestExpiryDay is the last day the create form offers under the
 * server's nss.key_max_lifetime: the latest day whose end falls within
 * `now` plus the limit, so the server never has to trim a chosen day.
 * Null when the limit is not a positive number of seconds, since 0 sets
 * no limit. Under a day's limit it can be today, which the form does not
 * offer.
 */
export function latestExpiryDay(now: Date, lifetimeSeconds: number): string | null {
	if (!Number.isFinite(lifetimeSeconds) || lifetimeSeconds <= 0) return null;
	const ceiling = new Date(now.getTime() + lifetimeSeconds * 1000);
	const day = localDay(ceiling);
	if (endOfDay(day).getTime() <= ceiling.getTime()) return day;
	const before = new Date(ceiling);
	before.setDate(before.getDate() - 1);
	return localDay(before);
}

/**
 * renewedExpiry is the expiry a rotation at `now` gives the key, worked out
 * as the server does: the key's own lifetime again, measured from its last
 * rotation or its creation, and no longer than nss.key_max_lifetime
 * (`maxSeconds`, 0 for no limit). A key that never expired takes the limit.
 * Null when the key will still never expire.
 */
export function renewedExpiry(
	key: Pick<NSSKey, 'expires_at' | 'rotated_at' | 'created_at'>,
	now: Date,
	maxSeconds: number
): Date | null {
	const limit = Number.isFinite(maxSeconds) && maxSeconds > 0 ? maxSeconds * 1000 : 0;
	if (!key.expires_at) return limit > 0 ? new Date(now.getTime() + limit) : null;
	const expires = new Date(key.expires_at).getTime();
	let lifetime = expires - new Date(key.rotated_at ?? key.created_at).getTime();
	if (limit > 0 && (!(lifetime > 0) || lifetime > limit)) lifetime = limit;
	if (!(lifetime > 0)) return new Date(expires);
	return new Date(now.getTime() + lifetime);
}

/** graceEnd is when a secret rotated out at `now` stops being accepted. */
export function graceEnd(now: Date, graceSeconds: number): Date {
	return new Date(now.getTime() + Math.max(0, graceSeconds) * 1000);
}

/** KeyAlert is one usage alert the server reports for a key: which, and when it last fired. */
export interface KeyAlert {
	kind: 'misses' | 'sources';
	at: string;
}

/**
 * keyAlerts lists the usage alerts that fired for a key in the server's
 * lookback (the last 7 days), newest first. Empty when neither did.
 */
export function keyAlerts(
	key: Pick<NSSKey, 'miss_rate_alert_at' | 'key_source_alert_at'>
): KeyAlert[] {
	const alerts: KeyAlert[] = [];
	if (key.miss_rate_alert_at) alerts.push({ kind: 'misses', at: key.miss_rate_alert_at });
	if (key.key_source_alert_at) alerts.push({ kind: 'sources', at: key.key_source_alert_at });
	return alerts.sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime());
}
