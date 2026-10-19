import {
	AuditNSSAccountCreated,
	AuditNSSAccountMissing,
	AuditNSSAccountRenamed,
	AuditNSSAccountRevived,
	AuditNSSAccountTombstoned,
	AuditNSSGIDCollision,
	AuditNSSGroupCreated,
	AuditNSSGroupMembersChanged,
	AuditNSSGroupMissing,
	AuditNSSGroupRenamed,
	AuditNSSGroupRevived,
	AuditNSSGroupTombstoned,
	AuditNSSIDChangeAccepted,
	AuditNSSIDChangePending,
	AuditNSSNameCollision,
	AuditNSSNameMismatch,
	AuditNSSTombstonePurged,
	AuditNSSUIDCollision
} from '#lib/api/generated/auditactions.js';
import { NSSCollisionKindGroupName, NSSCollisionKindUserName } from '#lib/api/generated/enums.js';
import type { AuditEvent } from '#lib/api/types.js';
import { describeAuditAction, visibleAuditEvents } from '#lib/audit.js';
import { collisionNoun } from '#lib/nss.js';

// The History section of a stored NSS account's or group's page, as one
// short line per thing that happened: "Removed from wheel", "sonia
// removed", "Renamed from a to b". Built from each audit event's detail,
// which is what was true when it was recorded, never from a fresh lookup.
// Additions draw no line (owner's call, 2026-10-18): the memberships and
// members tables date each current membership in their Added column. One
// nss.group_members_changed event can become many lines on a group's page
// (one per member removed or renumbered) and one or none on an account's
// page (only what happened to that account), and an event that only added
// draws none.

/** One run of a line's text; mono marks a name or number read character by character. */
export interface HistoryPart {
	text: string;
	mono?: boolean;
}

/** One line of the history: its words and when the event behind it was recorded. */
export interface HistoryRow {
	/** Unique within the list: the event id and the line's place in it. */
	key: string;
	parts: HistoryPart[];
	/** The event's created_at, for formatDateTime. */
	at: string;
}

/** One member as nss.group_members_changed lists it. */
interface Member {
	uid: number;
	name: string;
}

/** text is a plain run of a line. */
function text(value: string): HistoryPart {
	return { text: value };
}

/** mono is a run set in the mono: a name, a number, a DN. */
function mono(value: string): HistoryPart {
	return { text: value, mono: true };
}

/** detailOf is the event's detail, or an empty one when it is missing or not an object. */
function detailOf(event: AuditEvent): Record<string, unknown> {
	const detail: unknown = event.detail;
	return detail !== null && typeof detail === 'object' && !Array.isArray(detail)
		? (detail as Record<string, unknown>)
		: {};
}

/** str is a detail value as text: a string as is, a number in decimal, anything else "". */
function str(value: unknown): string {
	if (typeof value === 'string') return value;
	if (typeof value === 'number' && Number.isFinite(value)) return String(value);
	return '';
}

/** num is a detail value as a number, or undefined when it is not one. */
function num(value: unknown): number | undefined {
	return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

/** members reads an added or removed list, skipping anything that is not a {uid, name}. */
function members(value: unknown): Member[] {
	if (!Array.isArray(value)) return [];
	const out: Member[] = [];
	for (const item of value) {
		if (item === null || typeof item !== 'object') continue;
		const uid = num((item as Record<string, unknown>).uid);
		if (uid === undefined) continue;
		out.push({ uid, name: str((item as Record<string, unknown>).name) });
	}
	return out;
}

/** memberLabel names a member: its account name, or its uid when it had no stored account. */
function memberLabel(member: Member): string {
	return member.name || `uid ${member.uid}`;
}

/** groupLabel names the group of a member change: its name then, else its gid. */
function groupLabel(detail: Record<string, unknown>): string {
	const name = str(detail.name);
	if (name) return name;
	const gid = str(detail.gid);
	return gid ? `gid ${gid}` : 'a group';
}

/** notListed is how many of a list's total the event's capped list left out. */
function notListed(count: unknown, listed: Member[]): number {
	const total = num(count);
	return total !== undefined && total > listed.length ? total - listed.length : 0;
}

/**
 * renumbered pairs the members an event both removed and added under the
 * same account name: one account whose uid was re-keyed by an accepted
 * uid change, not two membership changes. Names are unique among stored
 * accounts, so the same name on both sides of one write is the same row.
 */
function renumbered(added: Member[], removed: Member[]): [Member, Member][] {
	const pairs: [Member, Member][] = [];
	const used = new Set<Member>();
	for (const a of added) {
		if (!a.name) continue;
		const r = removed.find((m) => !used.has(m) && m.name === a.name && m.uid !== a.uid);
		if (r) {
			used.add(r);
			pairs.push([r, a]);
		}
	}
	return pairs;
}

/** capitalized is the phrase with its first letter upper-cased. */
function capitalized(phrase: string): string {
	return phrase.charAt(0).toUpperCase() + phrase.slice(1);
}

/**
 * fallback is the one line for an event this view has no wording for: the
 * audit log's own phrase for the action, or the raw action name for one
 * this build does not know. Never the detail, never "[object Object]".
 */
function fallback(event: AuditEvent): HistoryPart[] {
	const action = typeof event.action === 'string' ? event.action : '';
	if (!action) return [text('Recorded an event')];
	const phrase = describeAuditAction(action);
	return phrase === action ? [mono(action)] : [text(capitalized(phrase))];
}

/** renamedParts is "Renamed from a to b", or "Renamed" when the names are not both there. */
function renamedParts(oldName: string, newName: string): HistoryPart[] {
	if (!oldName || !newName) return [text('Renamed')];
	return [text('Renamed from '), mono(oldName), text(' to '), mono(newName)];
}

/** changeParts is "uid changed from 1 to 2" for each number that moved, joined with "and". */
function changeParts(pairs: [string, unknown, unknown][], verb: string): HistoryPart[] {
	const parts: HistoryPart[] = [];
	for (const [noun, from, to] of pairs) {
		const a = str(from);
		const b = str(to);
		if (!a || !b || a === b) continue;
		parts.push(text(parts.length === 0 ? `${noun} ${verb} from ` : ` and ${noun} from `));
		parts.push(mono(a), text(' to '), mono(b));
	}
	return parts;
}

/** collisionParts is "uid 1000 claimed by both bob and alice", or as much of it as the detail has. */
function collisionParts(detail: Record<string, unknown>): HistoryPart[] {
	const kind = str(detail.kind);
	const value = str(detail.value);
	// A name collision is told apart by the two numbers; a number
	// collision by the two names.
	const byName = kind === NSSCollisionKindUserName || kind === NSSCollisionKindGroupName;
	const noun = kind ? collisionNoun(kind) : 'value';
	const side = (name: unknown, number: unknown, dn: unknown): string => {
		if (byName) {
			const n = str(number);
			return n ? `${kind === NSSCollisionKindGroupName ? 'gid' : 'uid'} ${n}` : str(dn);
		}
		return str(name) || str(dn);
	};
	const holder = side(detail.holder_name, detail.holder_number, detail.holder_dn);
	const other = side(detail.other_name, detail.other_number, detail.other_dn);
	// uid and gid stay lower case, as everywhere else they are written.
	const lead = noun === 'uid' || noun === 'gid' ? noun : capitalized(noun);
	const head: HistoryPart[] = value ? [text(`${lead} `), mono(value)] : [text(`${lead} collision`)];
	if (!holder || !other) return value ? [...head, text(' claimed by two entries')] : head;
	return [...head, text(' claimed by both '), mono(holder), text(' and '), mono(other)];
}

/**
 * lifecycleParts is the one line of an event that is not a member change,
 * on either page. kind decides which number a pending change names.
 */
function lifecycleParts(event: AuditEvent, kind: 'account' | 'group'): HistoryPart[] {
	const detail = detailOf(event);
	switch (event.action) {
		case AuditNSSAccountCreated:
			return [text('Created')];
		case AuditNSSGroupCreated: {
			const count = num(detail.member_count);
			if (count === undefined) return [text('Created')];
			return [text(`Created with ${count} ${count === 1 ? 'member' : 'members'}`)];
		}
		case AuditNSSAccountRenamed:
			return renamedParts(str(detail.old_name), str(detail.new_name));
		case AuditNSSGroupRenamed: {
			const oldName = str(detail.old_name);
			const newName = str(detail.new_name);
			if (detail.name_blocked === true && oldName && newName) {
				return [
					text('Renamed to '),
					mono(newName),
					text(' in the directory, but kept '),
					mono(oldName),
					text(': another group holds that name')
				];
			}
			if (oldName && newName && oldName === newName) {
				const dn = str(detail.new_dn);
				return dn
					? [text('Moved in the directory to '), mono(dn)]
					: [text('Moved in the directory')];
			}
			return renamedParts(oldName, newName);
		}
		case AuditNSSAccountMissing:
		case AuditNSSGroupMissing:
			return [text('Missing from the directory')];
		case AuditNSSAccountTombstoned:
		case AuditNSSGroupTombstoned:
			return [text('Tombstoned')];
		case AuditNSSAccountRevived:
		case AuditNSSGroupRevived:
			return [text(detail.was_tombstoned === true ? 'Revived from a tombstone' : 'Revived')];
		case AuditNSSTombstonePurged:
			return [text('Purged')];
		case AuditNSSIDChangeAccepted: {
			const parts = changeParts(
				[
					['uid', detail.old_uid, detail.new_uid],
					['gid', detail.old_gid, detail.new_gid]
				],
				'changed'
			);
			// The one line here a person caused: an admin accepted it, so
			// the line names them. Every other line is the sync's doing.
			const accepted = parts.length > 0 ? parts : [text('Accepted an id change')];
			const by = event.actor?.username;
			return by ? [...accepted, text(', accepted by '), mono(by)] : accepted;
		}
		case AuditNSSIDChangePending: {
			const parts = changeParts(
				[
					[kind === 'account' ? 'uid' : 'gid', detail.stored, detail.pending],
					['gid', detail.stored_gid, detail.pending_gid]
				],
				'change'
			);
			return parts.length > 0
				? [...parts, text(' waiting to be accepted')]
				: [text('Id change waiting to be accepted')];
		}
		case AuditNSSUIDCollision:
		case AuditNSSGIDCollision:
		case AuditNSSNameCollision:
			return collisionParts(detail);
		case AuditNSSNameMismatch: {
			const posix = str(detail.posix_name);
			const username = str(detail.username);
			if (!posix || !username) return [text('POSIX name differs from the username')];
			return [
				text('POSIX name '),
				mono(posix),
				text(' differs from the username '),
				mono(username)
			];
		}
		default:
			return fallback(event);
	}
}

/** row builds one line of event. */
function row(event: AuditEvent, index: number, parts: HistoryPart[]): HistoryRow {
	return { key: `${event.id}:${index}`, parts, at: event.created_at };
}

/** ownUIDActions are the events about the account itself, whose detail names its uid then. */
const ownUIDActions: string[] = [
	AuditNSSAccountCreated,
	AuditNSSAccountRenamed,
	AuditNSSAccountMissing,
	AuditNSSAccountTombstoned,
	AuditNSSAccountRevived,
	AuditNSSTombstonePurged,
	AuditNSSIDChangeAccepted
];

/**
 * accountUIDs is every uid the account has carried as far as the loaded
 * history tells: its stored uid and each uid its own events name, so a
 * member change from before an accepted uid change still matches.
 */
function accountUIDs(events: AuditEvent[], uid: number): Set<number> {
	const uids = new Set<number>([uid]);
	for (const event of events) {
		if (!ownUIDActions.includes(event.action)) continue;
		const detail = detailOf(event);
		for (const value of [detail.uid, detail.old_uid, detail.new_uid]) {
			const n = num(value);
			if (n !== undefined) uids.add(n);
		}
	}
	return uids;
}

/**
 * accountMemberParts is what one member change did to the account that
 * the history lists: removed from the group, or nothing (an addition,
 * which the Added column dates instead; another account's change; or a
 * re-key by an accepted uid change, which its own event already says).
 * When the account is past the capped lists, which list it was cut from
 * decides the line: cut from the removed list is a removal, from the
 * added list nothing, and both cut is the one ambiguous line, kept
 * because it may be a removal.
 */
function accountMemberParts(detail: Record<string, unknown>, uids: Set<number>): HistoryPart[][] {
	const added = members(detail.added);
	const removed = members(detail.removed);
	const group = mono(groupLabel(detail));
	const mineAdded = added.filter((m) => uids.has(m.uid));
	const mineRemoved = removed.filter((m) => uids.has(m.uid));
	const rekeyed = renumbered(added, removed).some(([r, a]) => uids.has(r.uid) || uids.has(a.uid));
	if (rekeyed) return [];

	if (mineRemoved.length > 0) return [[text('Removed from '), group]];
	if (mineAdded.length > 0) return [];

	const addedCut = notListed(detail.added_count, added) > 0;
	const removedCut = notListed(detail.removed_count, removed) > 0;
	if (addedCut && removedCut) return [[text('Added to or removed from '), group]];
	if (removedCut) return [[text('Removed from '), group]];
	return [];
}

/**
 * groupMemberParts is one line per member a change removed, a re-keyed
 * member as one renumbered line, and "and N more removed" when the event
 * capped its removed list, so no removal is silently dropped. Additions
 * draw no line, so a change that only added draws none; "Members changed"
 * is left for an event whose detail lists nothing at all.
 */
function groupMemberParts(detail: Record<string, unknown>): HistoryPart[][] {
	const added = members(detail.added);
	const removed = members(detail.removed);
	const pairs = renumbered(added, removed);
	const paired = new Set(pairs.flat());

	const lines: HistoryPart[][] = pairs.map(([r, a]) => [
		mono(a.name),
		text(' renumbered from uid '),
		mono(String(r.uid)),
		text(' to '),
		mono(String(a.uid))
	]);
	for (const m of removed) if (!paired.has(m)) lines.push([mono(memberLabel(m)), text(' removed')]);
	const moreRemoved = notListed(detail.removed_count, removed);
	if (moreRemoved > 0) lines.push([text(`and ${moreRemoved} more removed`)]);
	if (lines.length > 0) return lines;
	const addedAny = added.length > 0 || (num(detail.added_count) ?? 0) > 0;
	return addedAny ? [] : [[text('Members changed')]];
}

/**
 * accountHistoryRows is the history of the stored account with uid, newest
 * first as the events came: one line per lifecycle event, and per member
 * change only what happened to this account.
 */
export function accountHistoryRows(events: AuditEvent[], uid: number): HistoryRow[] {
	const visible = visibleAuditEvents(events);
	const uids = accountUIDs(visible, uid);
	return visible.flatMap((event) => {
		const lines =
			event.action === AuditNSSGroupMembersChanged
				? accountMemberParts(detailOf(event), uids)
				: [lifecycleParts(event, 'account')];
		return lines.map((parts, i) => row(event, i, parts));
	});
}

/**
 * groupHistoryRows is the history of a stored group, newest first as the
 * events came: one line per member removed or renumbered, none per member
 * added, and one per lifecycle event.
 */
export function groupHistoryRows(events: AuditEvent[]): HistoryRow[] {
	return visibleAuditEvents(events).flatMap((event) => {
		const lines =
			event.action === AuditNSSGroupMembersChanged
				? groupMemberParts(detailOf(event))
				: [lifecycleParts(event, 'group')];
		return lines.map((parts, i) => row(event, i, parts));
	});
}
