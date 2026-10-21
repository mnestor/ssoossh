import {
	NSSCollisionKindGID,
	NSSCollisionKindGroupName,
	NSSCollisionKindUID,
	NSSCollisionKindUserName,
	NSSEntryStateActive,
	NSSEntryStateDisabled,
	NSSEntryStateMissing,
	NSSEntryStatePending,
	NSSEntryStateTombstone,
	NSSEntryStateWithheld,
	NSSKeyStatusActive,
	NSSKeyStatusExpired,
	NSSKeyStatusRevoked,
	NSSNotServedNoGID,
	NSSNotServedPastRetention,
	NSSNotServedPrivateGroupClash,
	NSSNotServedWithheld,
	NSSRotationBucketGone,
	NSSRotationBucketNotSwitched,
	NSSRotationBucketSwitched,
	type NSSEntryState,
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

/** KeyListBack is the list a key's pages were opened from, and its filters. */
export interface KeyListBack {
	/** From All NSS keys (`from=admin`) rather than the keys a reader manages. */
	fromAdmin: boolean;
	/** The list's own query string (`back`), or '' for none or a malformed one. */
	search: string;
}

/**
 * keyListBack reads the way back a key's pages carry: `from` is only
 * compared, and `back` is kept only as a query string ("?..." with no "#"),
 * so neither can steer a back chip off its fixed route.
 */
export function keyListBack(params: Pick<URLSearchParams, 'get'>): KeyListBack {
	const carried = params.get('back') ?? '';
	return {
		fromAdmin: params.get('from') === 'admin',
		search: carried.startsWith('?') && !carried.includes('#') ? carried : ''
	};
}

/**
 * keyListContext is that way back as the query string a key's pages pass
 * between them ("?from=admin&back=..."), '' when there is none. The key page
 * hands it to its rotation report and the report hands it back, so the
 * chain of back chips ends on the list it began at.
 */
export function keyListContext(params: Pick<URLSearchParams, 'get'>): string {
	const { fromAdmin, search } = keyListBack(params);
	const out = new URLSearchParams();
	if (fromAdmin) out.set('from', 'admin');
	if (search) out.set('back', search);
	const query = out.toString();
	return query ? `?${query}` : '';
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
 * server serves them, and then to a viewer who may create keys (the fleet
 * key admin role, which every admin holds) or whose key list is not empty,
 * because the list is the keys their groups manage. A key admin with no key
 * in their groups lands on the page's empty state, which offers New key and
 * links All NSS keys. Auditors and SOC without that role reach every key
 * from the admin group's own entry (/admin/nss/keys), so for them only the
 * list decides.
 */
export function showNSSKeys(
	user: Pick<CurrentUser, 'nss_enabled' | 'is_nss_key_admin'> | null,
	hasKeys: boolean
): boolean {
	return !!user?.nss_enabled && (user.is_nss_key_admin || hasKeys);
}

/**
 * needsKeyCheck reports whether the rail has to ask the server for keys to
 * decide: wherever NSS is on, except for a fleet key admin, whom the role
 * alone already shows the entry to.
 */
export function needsKeyCheck(
	user: Pick<CurrentUser, 'nss_enabled' | 'is_nss_key_admin'> | null
): boolean {
	return !!user?.nss_enabled && !user.is_nss_key_admin;
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

// ---- The stored entry viewer ---------------------------------------------
//
// The admin directory viewer (/admin/nss) shows each stored account and group
// with a state and, beside it, what a host's lookup is served now. The served
// verdict is never worked out here: it is the server's `served` and
// `not_served`, built by the lookup code itself. What lives here is only how
// the server's words are said.

/** EntryKind is which of the two stored lists an entry belongs to. */
export type EntryKind = 'account' | 'group';

/** entryStateLabels names each state in the words its badge and filter use. */
const entryStateLabels: Record<NSSEntryState, string> = {
	[NSSEntryStateActive]: 'Active',
	[NSSEntryStateDisabled]: 'Disabled',
	[NSSEntryStateMissing]: 'Missing',
	[NSSEntryStatePending]: 'Pending',
	[NSSEntryStateWithheld]: 'Withheld',
	[NSSEntryStateTombstone]: 'Tombstone'
};

/**
 * entryStates lists the states one kind of entry can be in, in the order the
 * filter offers them: the served states, then the ones that need a look.
 * Disabled is never set on a group.
 */
export function entryStates(kind: EntryKind): NSSEntryState[] {
	const all: NSSEntryState[] = [
		NSSEntryStateActive,
		NSSEntryStateDisabled,
		NSSEntryStateMissing,
		NSSEntryStatePending,
		NSSEntryStateWithheld,
		NSSEntryStateTombstone
	];
	return kind === 'group' ? all.filter((state) => state !== NSSEntryStateDisabled) : all;
}

/** entryStateLabel is a state's word, or the raw value for one this build does not know. */
export function entryStateLabel(state: string): string {
	return entryStateLabels[state as NSSEntryState] ?? state;
}

/**
 * parseEntryState reads a state filter from the address. A value the kind
 * cannot be in is dropped, not refused, as every list does.
 */
export function parseEntryState(value: string | null, kind: EntryKind): NSSEntryState | '' {
	return entryStates(kind).find((state) => state === value) ?? '';
}

/**
 * entryStateMeaning is what a state means for what hosts get, said once
 * for the state section of a detail page. Config keys are left as text;
 * ConfigProse sets them in mono.
 */
export function entryStateMeaning(state: string, kind: EntryKind): string {
	const number = kind === 'account' ? 'uid or gid' : 'gid';
	switch (state) {
		case NSSEntryStateWithheld:
			return 'Part of a collision over a number or a name. Lookups treat it as not found until one of the entries changes in the directory.';
		case NSSEntryStateTombstone:
			return kind === 'account'
				? 'Missing from the directory for longer than nss.tombstone_after. Served as deleted until nss.tombstone_retention ends, then not at all.'
				: 'Missing from the directory for longer than nss.tombstone_after. Served with no members until nss.tombstone_retention ends, then not at all.';
		case NSSEntryStateDisabled:
			return "Locked by the directory's disabled flag or by the linked person's disable. Still served, with nss.disabled_shell as its shell.";
		case NSSEntryStateMissing:
			return 'The last full sync did not see it in the directory. Served as before until it becomes a tombstone.';
		case NSSEntryStatePending:
			return `The directory now gives a different ${number}. Hosts keep the stored number until an admin accepts the change or the directory reverts.`;
		default:
			return '';
	}
}

/**
 * notServedReason says why a host's lookup finds nothing, from the server's
 * not_served and, for a withheld row, its withheld_reason. A reason this
 * build does not know is shown as sent rather than guessed at.
 */
export function notServedReason(reason: string, kind: EntryKind, withheldReason = ''): string {
	switch (reason) {
		case NSSNotServedWithheld:
			return withheldReason
				? `Withheld by a collision: ${withheldReason.trim().replace(/\.$/, '')}.`
				: `Withheld by a collision over its ${kind === 'account' ? 'uid' : 'gid'} or name.`;
		case NSSNotServedPastRetention:
			return 'A tombstone older than nss.tombstone_retention, waiting for the purge to remove it.';
		case NSSNotServedPrivateGroupClash:
			return `nss.auto_private_groups is "true" and a directory group already holds this account's private group gid or name.`;
		case NSSNotServedNoGID:
			return `The entry has no gid attribute, which only nss.auto_private_groups "true" accepts; this mode has no primary gid to serve it with.`;
		case '':
			return 'The server gave no reason.';
		default:
			return `The server's reason: ${reason}.`;
	}
}

/** A run of prose, set in mono when it is a config key. */
export interface ProseSegment {
	text: string;
	key: boolean;
}

/**
 * splitConfigKeys cuts prose into plain text and the config keys inside it
 * (`nss.tombstone_retention`, `ldap.id_attribute`), so a page can set the
 * keys in mono (DESIGN.md, "Where the mono goes") from one sentence.
 */
export function splitConfigKeys(text: string): ProseSegment[] {
	return text
		.split(/((?:nss|ldap)\.[a-z_]+(?:\.[a-z_]+)*)/)
		.filter((part) => part !== '')
		.map((part) => ({ text: part, key: /^(?:nss|ldap)\./.test(part) }));
}

/**
 * parseEntryNumber reads a uid or gid from a route segment: a plain decimal
 * that fits in 32 bits, or null for anything else, which no stored entry
 * can be addressed by.
 */
export function parseEntryNumber(raw: string | undefined): number | null {
	if (!raw || !/^\d{1,10}$/.test(raw)) {
		return null;
	}
	const value = Number(raw);
	return value <= 0xffffffff ? value : null;
}
